package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func tempBookmarkPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "bookmarks.json")
}

func TestBookmarkStore_ToggleAddRemove(t *testing.T) {
	s := NewBookmarkStore(tempBookmarkPath(t))
	added, err := s.Toggle("ctx", "http://w1", "w1")
	if err != nil {
		t.Fatalf("first toggle returned error: %v", err)
	}
	if !added {
		t.Errorf("expected added=true on first toggle, got false")
	}
	if got := len(s.List("ctx")); got != 1 {
		t.Errorf("expected 1 bookmark after add, got %d", got)
	}
	added, err = s.Toggle("ctx", "http://w1", "w1")
	if err != nil {
		t.Fatalf("second toggle returned error: %v", err)
	}
	if added {
		t.Errorf("expected added=false on second toggle (removal), got true")
	}
	if got := len(s.List("ctx")); got != 0 {
		t.Errorf("expected 0 bookmarks after remove, got %d", got)
	}
}

func TestBookmarkStore_MaxEnforced(t *testing.T) {
	s := NewBookmarkStore(tempBookmarkPath(t))
	for i := 0; i < MaxBookmarks; i++ {
		ep := "http://w" + string(rune('0'+i))
		if _, err := s.Toggle("ctx", ep, "w"); err != nil {
			t.Fatalf("toggle %d returned error: %v", i, err)
		}
	}
	added, err := s.Toggle("ctx", "http://overflow", "ov")
	if err == nil {
		t.Errorf("expected error when exceeding MaxBookmarks, got nil")
	}
	if added {
		t.Errorf("expected added=false on overflow, got true")
	}
}

func TestBookmarkStore_ContextIsolation(t *testing.T) {
	s := NewBookmarkStore(tempBookmarkPath(t))
	_, _ = s.Toggle("ctx-a", "http://w1", "w1")
	_, _ = s.Toggle("ctx-b", "http://w1", "w1")
	if got := len(s.List("ctx-a")); got != 1 {
		t.Errorf("ctx-a expected 1, got %d", got)
	}
	if got := len(s.List("ctx-b")); got != 1 {
		t.Errorf("ctx-b expected 1, got %d", got)
	}
}

func TestBookmarkStore_RoundTripPersistence(t *testing.T) {
	path := tempBookmarkPath(t)
	s1 := NewBookmarkStore(path)
	_, _ = s1.Toggle("ctx", "http://w1", "w1")
	if err := s1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s2 := NewBookmarkStore(path)
	if got := len(s2.List("ctx")); got != 1 {
		t.Errorf("expected round-trip to preserve 1 bookmark, got %d", got)
	}
}

func TestBookmarkStore_ReconcileDropsMissing(t *testing.T) {
	s := NewBookmarkStore(tempBookmarkPath(t))
	_, _ = s.Toggle("ctx", "http://w1", "w1")
	_, _ = s.Toggle("ctx", "http://w2", "w2")
	live := []*metrics.WorkerMetrics{
		{Endpoint: "http://w1"},
	}
	s.Reconcile("ctx", live)
	if got := len(s.List("ctx")); got != 1 {
		t.Errorf("expected 1 after reconcile, got %d", got)
	}
}

func TestBookmarkStore_NewMissingFileNoError(t *testing.T) {
	path := tempBookmarkPath(t)
	// Don't create the file.
	s := NewBookmarkStore(path)
	if got := len(s.List("ctx")); got != 0 {
		t.Errorf("expected empty store on missing path, got %d", got)
	}
}

func TestBookmarkStore_IsBookmarked(t *testing.T) {
	s := NewBookmarkStore(tempBookmarkPath(t))
	if s.IsBookmarked("ctx", "http://w1") {
		t.Errorf("expected IsBookmarked=false before toggle")
	}
	_, _ = s.Toggle("ctx", "http://w1", "w1")
	if !s.IsBookmarked("ctx", "http://w1") {
		t.Errorf("expected IsBookmarked=true after toggle")
	}
}

func TestBookmarkStore_NilSafe(t *testing.T) {
	var s *BookmarkStore
	if s.IsBookmarked("ctx", "ep") {
		t.Errorf("nil store should not report bookmarked")
	}
	if got := s.List("ctx"); got != nil {
		t.Errorf("nil store List should return nil, got %v", got)
	}
	s.Reconcile("ctx", nil) // must not panic
	if err := s.Save(); err == nil {
		t.Errorf("nil store Save should return error")
	}
}

func TestBookmarkStore_SaveCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "tree", "bookmarks.json")
	s := NewBookmarkStore(nested)
	_, _ = s.Toggle("ctx", "http://w1", "w1")
	if err := s.Save(); err != nil {
		t.Fatalf("Save with deep path: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("expected file at %s, got err %v", nested, err)
	}
}
