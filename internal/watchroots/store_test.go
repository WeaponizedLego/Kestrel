package watchroots

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "watchroots.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func TestUpsertTreeAddsAllAndAssignsOrigin(t *testing.T) {
	s := newTestStore(t)
	origin := "/data/photos"
	paths := []string{"/data/photos", "/data/photos/sub", "/data/photos/sub/deep"}
	if err := s.UpsertTree(origin, paths); err != nil {
		t.Fatalf("UpsertTree: %v", err)
	}
	got := s.List()
	if len(got) != 3 {
		t.Fatalf("List: want 3 roots, got %d", len(got))
	}
	for _, r := range got {
		if r.Origin != origin {
			t.Errorf("origin: want %q, got %q", origin, r.Origin)
		}
	}
}

func TestUpsertTreeIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	paths := []string{"/a", "/a/b"}
	if err := s.UpsertTree("/a", paths); err != nil {
		t.Fatalf("first UpsertTree: %v", err)
	}
	if err := s.UpsertTree("/a", paths); err != nil {
		t.Fatalf("second UpsertTree: %v", err)
	}
	if got := len(s.List()); got != 2 {
		t.Errorf("List len: want 2, got %d", got)
	}
}

func TestRemoveTombstonesAndBlocksReadd(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertTree("/a", []string{"/a", "/a/b"}); err != nil {
		t.Fatalf("UpsertTree: %v", err)
	}
	if err := s.Remove("/a/b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !s.IsIgnored("/a/b") {
		t.Fatal("expected /a/b to be ignored after Remove")
	}
	// Re-adding the parent must not pull /a/b back in.
	if err := s.UpsertTree("/a", []string{"/a", "/a/b"}); err != nil {
		t.Fatalf("re-UpsertTree: %v", err)
	}
	for _, r := range s.List() {
		if r.Path == "/a/b" {
			t.Fatal("/a/b reappeared after tombstone")
		}
	}
}

func TestParentIgnoreCoversDescendants(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertTree("/a", []string{"/a", "/a/b", "/a/b/c"}); err != nil {
		t.Fatalf("UpsertTree: %v", err)
	}
	if err := s.RemoveByOrigin("/a"); err != nil {
		t.Fatalf("RemoveByOrigin: %v", err)
	}
	if got := len(s.List()); got != 0 {
		t.Errorf("List after origin removal: want 0, got %d", got)
	}
	if !s.IsIgnored("/a/b/c") {
		t.Fatal("expected /a/b/c covered by parent ignore")
	}
	// New subdirs under the tombstoned parent must also be skipped.
	if err := s.UpsertTree("/a", []string{"/a/brand-new"}); err != nil {
		t.Fatalf("UpsertTree blocked-by-ignore: %v", err)
	}
	if got := len(s.List()); got != 0 {
		t.Errorf("List after blocked re-add: want 0, got %d", got)
	}
}

func TestSubscribeFiresOnChange(t *testing.T) {
	s := newTestStore(t)
	var mu sync.Mutex
	var addedCalls, removedCalls int
	unsub := s.Subscribe(func(added, removed []Root) {
		mu.Lock()
		addedCalls += len(added)
		removedCalls += len(removed)
		mu.Unlock()
	})
	defer unsub()

	if err := s.UpsertTree("/a", []string{"/a", "/a/b"}); err != nil {
		t.Fatalf("UpsertTree: %v", err)
	}
	if err := s.Remove("/a/b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if addedCalls != 2 {
		t.Errorf("added calls: want 2, got %d", addedCalls)
	}
	if removedCalls != 1 {
		t.Errorf("removed calls: want 1, got %d", removedCalls)
	}
}

func TestSubscribeNotFiredByMarkScanned(t *testing.T) {
	s := newTestStore(t)
	if err := s.Upsert("/a"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	called := false
	unsub := s.Subscribe(func(added, removed []Root) { called = true })
	defer unsub()
	if err := s.MarkScanned("/a", time.Now()); err != nil {
		t.Fatalf("MarkScanned: %v", err)
	}
	if called {
		t.Error("MarkScanned must not fire subscribers")
	}
}

func TestLegacyArrayFormatLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watchroots.json")
	legacy := []byte(`[{"path":"/a","added_at":"2024-01-02T03:04:05Z"}]`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy: %v", err)
	}
	got := s.List()
	if len(got) != 1 {
		t.Fatalf("len roots: want 1, got %d", len(got))
	}
	if got[0].Origin != "/a" {
		t.Errorf("legacy Origin should default to Path, got %q", got[0].Origin)
	}
}

func TestFlushUsesObjectFormat(t *testing.T) {
	s := newTestStore(t)
	if err := s.Upsert("/a"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("expected object-format JSON, got error: %v", err)
	}
	if len(f.Roots) != 1 {
		t.Errorf("Roots in flushed file: want 1, got %d", len(f.Roots))
	}
}

