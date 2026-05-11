// Black-box contract tests for V8 (Bookmark & Compare).
// Spec: docs/feature-spec-v2.md §3 V8, §7 glossary.
//
// Pinned contracts:
//   - NewBookmarkStore on missing path returns empty store, no error.
//   - Toggle adds when absent, removes when present.
//   - Toggle past 4 returns (false, err) — cap-at-4 invariant.
//   - List is keyed by k8sContext.
//   - Reconcile drops bookmarks for endpoints not in live worker set.
//   - Save() round-trip via NewBookmarkStore restores the same list.
//   - JSON shape on disk matches WorkerBookmark struct tags.
//   - RenderBookmarkCompare with 0 bookmarks contains "No bookmarks".
//   - RenderBookmarkCompare with ring=nil does not panic.

package ui_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/internal/ui"
)

// ─── BookmarkStore ───────────────────────────────────────────────────────────

func TestNewBookmarkStore_MissingPath_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist", "bookmarks.json")
	store := ui.NewBookmarkStore(path)
	if store == nil {
		t.Fatalf("NewBookmarkStore returned nil for missing path")
	}
	if got := store.List("ctx"); len(got) != 0 {
		t.Errorf("empty store should have List len 0, got %d", len(got))
	}
}

func TestBookmarkStore_Toggle_AddsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	added, err := store.Toggle("ctx-1", "http://w0:8000", "worker-0")
	if err != nil {
		t.Fatalf("Toggle returned err: %v", err)
	}
	if !added {
		t.Errorf("Toggle on absent bookmark should return added=true")
	}
	if got := store.List("ctx-1"); len(got) != 1 {
		t.Errorf("List should have 1 entry after toggle-add, got %d", len(got))
	}
}

func TestBookmarkStore_Toggle_RemovesWhenPresent(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	_, _ = store.Toggle("ctx-1", "http://w0:8000", "worker-0")
	added, err := store.Toggle("ctx-1", "http://w0:8000", "worker-0")
	if err != nil {
		t.Fatalf("second Toggle returned err: %v", err)
	}
	if added {
		t.Errorf("Toggle on present bookmark should return added=false")
	}
	if got := store.List("ctx-1"); len(got) != 0 {
		t.Errorf("List should be empty after toggle-remove, got %d", len(got))
	}
}

func TestBookmarkStore_Toggle_CapAt4_Returns_Error(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	for i := range 4 {
		ep := "http://w" + string(rune('0'+i)) + ":8000"
		lbl := "worker-" + string(rune('0'+i))
		if _, err := store.Toggle("ctx-1", ep, lbl); err != nil {
			t.Fatalf("Toggle %d: unexpected err %v", i, err)
		}
	}
	// 5th add must return (false, err).
	added, err := store.Toggle("ctx-1", "http://w4:8000", "worker-4")
	if err == nil {
		t.Errorf("5th Toggle should return non-nil error (cap at 4)")
	}
	if added {
		t.Errorf("5th Toggle should return added=false")
	}
}

// At the cap of 4, toggling off an existing entry must always succeed (no
// error) even though the count is at the cap. Round-1 flagged this as
// untested; the impl handles it because the remove branch runs before the
// cap check, but a regression that reorders those branches would silently
// break the V8 contract.
func TestBookmarkStore_Toggle_RemoveAtCap_AlwaysSucceeds(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	endpoints := []string{
		"http://w0:8000",
		"http://w1:8000",
		"http://w2:8000",
		"http://w3:8000",
	}
	for i, ep := range endpoints {
		if _, err := store.Toggle("ctx-1", ep, "worker-"+string(rune('0'+i))); err != nil {
			t.Fatalf("Toggle %d: unexpected err %v", i, err)
		}
	}
	if got := len(store.List("ctx-1")); got != 4 {
		t.Fatalf("setup: expected 4 bookmarks at cap, got %d", got)
	}

	// Remove the 2nd one (not the last) — exercises the loop's interior
	// path, not just the tail.
	added, err := store.Toggle("ctx-1", endpoints[1], "worker-1")
	if err != nil {
		t.Fatalf("Toggle-remove at cap returned err=%v, must always succeed", err)
	}
	if added {
		t.Errorf("Toggle-remove should return added=false, got true")
	}
	if got := len(store.List("ctx-1")); got != 3 {
		t.Errorf("after Toggle-remove at cap, expected 3 bookmarks, got %d", got)
	}

	// And a follow-up add must now succeed because we're back under cap.
	added, err = store.Toggle("ctx-1", "http://w-new:8000", "worker-new")
	if err != nil {
		t.Errorf("post-remove add should succeed (count=3 < cap=4), got err=%v", err)
	}
	if !added {
		t.Errorf("post-remove add should return added=true")
	}
}

// Removing every entry one-by-one from a full store must drain to empty
// without error.
func TestBookmarkStore_Toggle_DrainFromCap_ToEmpty(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	endpoints := []string{
		"http://w0:8000",
		"http://w1:8000",
		"http://w2:8000",
		"http://w3:8000",
	}
	for i, ep := range endpoints {
		if _, err := store.Toggle("ctx-1", ep, "worker-"+string(rune('0'+i))); err != nil {
			t.Fatalf("Toggle %d: unexpected err %v", i, err)
		}
	}
	for _, ep := range endpoints {
		added, err := store.Toggle("ctx-1", ep, "ignored")
		if err != nil {
			t.Fatalf("drain Toggle(%q) returned err=%v", ep, err)
		}
		if added {
			t.Errorf("drain Toggle(%q) should return added=false", ep)
		}
	}
	if got := len(store.List("ctx-1")); got != 0 {
		t.Errorf("after draining all bookmarks, List should be empty, got %d", got)
	}
}

func TestBookmarkStore_List_KeyedByK8sContext(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	_, _ = store.Toggle("ctx-A", "http://w0:8000", "w0-A")
	_, _ = store.Toggle("ctx-B", "http://w0:8000", "w0-B")

	a := store.List("ctx-A")
	b := store.List("ctx-B")
	if len(a) != 1 || a[0].Label != "w0-A" {
		t.Errorf("List(ctx-A) should contain w0-A only, got %v", a)
	}
	if len(b) != 1 || b[0].Label != "w0-B" {
		t.Errorf("List(ctx-B) should contain w0-B only, got %v", b)
	}
}

func TestBookmarkStore_Reconcile_RemovesDeadEndpoints(t *testing.T) {
	dir := t.TempDir()
	store := ui.NewBookmarkStore(filepath.Join(dir, "bookmarks.json"))
	_, _ = store.Toggle("ctx-1", "http://w0:8000", "w0")
	_, _ = store.Toggle("ctx-1", "http://w1:8000", "w1")

	// w1 is no longer in the live set.
	live := []*metrics.WorkerMetrics{
		{Endpoint: "http://w0:8000", Label: "w0"},
	}
	store.Reconcile("ctx-1", live)

	got := store.List("ctx-1")
	if len(got) != 1 {
		t.Fatalf("expected 1 bookmark after Reconcile (w1 dead), got %d", len(got))
	}
	if got[0].Endpoint != "http://w0:8000" {
		t.Errorf("survivor should be w0, got %v", got[0])
	}
}

func TestBookmarkStore_Save_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.json")

	s1 := ui.NewBookmarkStore(path)
	if _, err := s1.Toggle("ctx-1", "http://w0:8000", "w0"); err != nil {
		t.Fatalf("Toggle err: %v", err)
	}
	if _, err := s1.Toggle("ctx-1", "http://w1:8000", "w1"); err != nil {
		t.Fatalf("Toggle err: %v", err)
	}
	if err := s1.Save(); err != nil {
		t.Fatalf("Save err: %v", err)
	}

	s2 := ui.NewBookmarkStore(path)
	got := s2.List("ctx-1")
	if len(got) != 2 {
		t.Errorf("after round-trip List(ctx-1) should have 2 entries, got %d", len(got))
	}
}

func TestBookmarkStore_Save_FileFormat_IsJSONWithExportedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bookmarks.json")
	store := ui.NewBookmarkStore(path)
	if _, err := store.Toggle("ctx-X", "http://w0:8000", "w0"); err != nil {
		t.Fatalf("Toggle err: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save err: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !json.Valid(raw) {
		t.Errorf("bookmarks.json must be valid JSON, got: %s", raw)
	}
	// Spec lists JSON tags: endpoint, label, k8s_context.
	for _, key := range []string{"endpoint", "label", "k8s_context"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("bookmarks.json should contain key %q, got: %s", key, raw)
		}
	}
}

// ─── RenderBookmarkCompare ───────────────────────────────────────────────────

func TestRenderBookmarkCompare_NoBookmarks_ContainsPlaceholder(t *testing.T) {
	out := ui.RenderBookmarkCompare(nil, nil, nil, 120, 30)
	if !strings.Contains(out, "No bookmarks") {
		t.Errorf("RenderBookmarkCompare with 0 bookmarks should contain \"No bookmarks\", got %q", out)
	}
}

func TestRenderBookmarkCompare_NilRing_DoesNotPanic(t *testing.T) {
	bookmarks := []ui.WorkerBookmark{
		{Endpoint: "http://w0:8000", Label: "w0", K8sContext: "ctx"},
		{Endpoint: "http://w1:8000", Label: "w1", K8sContext: "ctx"},
	}
	live := []*metrics.WorkerMetrics{
		{Endpoint: "http://w0:8000", Label: "w0"},
		{Endpoint: "http://w1:8000", Label: "w1"},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderBookmarkCompare panicked with ring=nil: %v", r)
		}
	}()
	out := ui.RenderBookmarkCompare(live, nil, bookmarks, 120, 30)
	if out == "" {
		t.Errorf("RenderBookmarkCompare with bookmarks should not be empty even when ring is nil")
	}
}

func TestRenderBookmarkCompare_LabelsAppearInOutput(t *testing.T) {
	bookmarks := []ui.WorkerBookmark{
		{Endpoint: "http://w0:8000", Label: "alpha", K8sContext: "ctx"},
		{Endpoint: "http://w1:8000", Label: "beta", K8sContext: "ctx"},
	}
	live := []*metrics.WorkerMetrics{
		{Endpoint: "http://w0:8000", Label: "alpha"},
		{Endpoint: "http://w1:8000", Label: "beta"},
	}
	out := ui.RenderBookmarkCompare(live, nil, bookmarks, 120, 30)
	for _, lbl := range []string{"alpha", "beta"} {
		if !strings.Contains(out, lbl) {
			t.Errorf("RenderBookmarkCompare should include worker label %q, got %q", lbl, out)
		}
	}
}
