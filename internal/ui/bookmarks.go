package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// MaxBookmarks caps the per-context bookmark list. Compare view assumes at
// most 4 columns fit on a typical 100-col terminal.
const MaxBookmarks = 4

// WorkerBookmark identifies a bookmarked worker. K8sContext disambiguates
// across clusters when the operator switches context between sessions.
type WorkerBookmark struct {
	Endpoint   string `json:"endpoint"`
	Label      string `json:"label"`
	K8sContext string `json:"k8s_context"`
}

// BookmarkStore manages the in-memory bookmark list and persists to JSON.
// Not thread-safe — Bubbletea's update loop is single-goroutine and is the
// only caller in production.
type BookmarkStore struct {
	path      string
	bookmarks []WorkerBookmark
}

// NewBookmarkStore creates a store backed by the given JSON path. A
// missing or malformed file is tolerated: the resulting store starts
// empty and the file is created on first Save().
func NewBookmarkStore(path string) *BookmarkStore {
	s := &BookmarkStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var entries []WorkerBookmark
	if err := json.Unmarshal(data, &entries); err != nil {
		return s
	}
	s.bookmarks = entries
	return s
}

// Toggle adds or removes the (k8sCtx, endpoint) pair. Returns added=true
// when the entry was added, added=false when it was removed. Returns an
// error only when adding would exceed MaxBookmarks; an existing entry can
// always be removed without error.
func (s *BookmarkStore) Toggle(k8sCtx, endpoint, label string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("nil bookmark store")
	}
	for i, b := range s.bookmarks {
		if b.K8sContext == k8sCtx && b.Endpoint == endpoint {
			s.bookmarks = append(s.bookmarks[:i], s.bookmarks[i+1:]...)
			return false, nil
		}
	}
	if s.countForContext(k8sCtx) >= MaxBookmarks {
		return false, fmt.Errorf("max %d bookmarks", MaxBookmarks)
	}
	s.bookmarks = append(s.bookmarks, WorkerBookmark{
		Endpoint:   endpoint,
		Label:      label,
		K8sContext: k8sCtx,
	})
	return true, nil
}

// countForContext returns the bookmark count for one context.
func (s *BookmarkStore) countForContext(k8sCtx string) int {
	n := 0
	for _, b := range s.bookmarks {
		if b.K8sContext == k8sCtx {
			n++
		}
	}
	return n
}

// IsBookmarked reports whether the (k8sCtx, endpoint) pair is bookmarked.
func (s *BookmarkStore) IsBookmarked(k8sCtx, endpoint string) bool {
	if s == nil {
		return false
	}
	for _, b := range s.bookmarks {
		if b.K8sContext == k8sCtx && b.Endpoint == endpoint {
			return true
		}
	}
	return false
}

// List returns all bookmarks for the given context, in insertion order.
func (s *BookmarkStore) List(k8sCtx string) []WorkerBookmark {
	if s == nil {
		return nil
	}
	var out []WorkerBookmark
	for _, b := range s.bookmarks {
		if b.K8sContext == k8sCtx {
			out = append(out, b)
		}
	}
	return out
}

// Reconcile drops bookmarks for the given context whose endpoint is not
// present in live. Bookmarks for other contexts are preserved.
func (s *BookmarkStore) Reconcile(k8sCtx string, live []*metrics.WorkerMetrics) {
	if s == nil {
		return
	}
	liveEPs := make(map[string]bool, len(live))
	for _, w := range live {
		if w == nil {
			continue
		}
		liveEPs[w.Endpoint] = true
	}
	kept := s.bookmarks[:0]
	for _, b := range s.bookmarks {
		if b.K8sContext == k8sCtx && !liveEPs[b.Endpoint] {
			continue
		}
		kept = append(kept, b)
	}
	s.bookmarks = kept
}

// Save writes the bookmark list to s.path, creating parent directories as
// needed. Returns an error when the path is empty or the write fails.
func (s *BookmarkStore) Save() error {
	if s == nil {
		return fmt.Errorf("nil bookmark store")
	}
	if s.path == "" {
		return fmt.Errorf("empty bookmark path")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir bookmarks dir: %w", err)
	}
	data, err := json.MarshalIndent(s.bookmarks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bookmarks: %w", err)
	}
	return os.WriteFile(s.path, data, 0o644)
}

// DefaultBookmarksPath returns the platform-conventional config path:
// $XDG_CONFIG_HOME/llmtop/bookmarks.json, falling back to
// $HOME/.config/llmtop/bookmarks.json.
func DefaultBookmarksPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "llmtop", "bookmarks.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "llmtop", "bookmarks.json")
}
