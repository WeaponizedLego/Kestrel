package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/scanner"
	"github.com/WeaponizedLego/kestrel/internal/watchroots"
)

type fakeRunner struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeRunner) StartLowIntensity(root string, _ scanner.LowOptions) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, root)
	f.mu.Unlock()
	return "scan-id", nil
}

func (f *fakeRunner) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func newWatcher(t *testing.T) (*Watcher, *fakeRunner, *watchroots.Store) {
	t.Helper()
	store, err := watchroots.Open(filepath.Join(t.TempDir(), "watchroots.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runner := &fakeRunner{}
	w, err := New(Config{
		Store:    store,
		Runner:   runner,
		Debounce: 80 * time.Millisecond,
		Retry:    50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.fsw.Close() })
	return w, runner, store
}

func waitForCalls(t *testing.T, runner *fakeRunner, want int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got := runner.snapshot()
		if len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return runner.snapshot()
}

func TestWatcher_FileCreateTriggersDebouncedRescan(t *testing.T) {
	w, runner, store := newWatcher(t)
	root := t.TempDir()
	if err := store.UpsertTree(root, []string{root}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	// Let the bootstrap watch register.
	time.Sleep(30 * time.Millisecond)

	// Burst of writes — should coalesce into one rescan.
	for i := 0; i < 5; i++ {
		path := filepath.Join(root, "f.jpg")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	calls := waitForCalls(t, runner, 1, 1*time.Second)
	if len(calls) != 1 {
		t.Fatalf("expected exactly one rescan, got %d (%v)", len(calls), calls)
	}
	if calls[0] != root {
		t.Errorf("rescan path: want %q, got %q", root, calls[0])
	}
}

func TestWatcher_NewSubdirRegistersAsSubRoot(t *testing.T) {
	w, runner, store := newWatcher(t)
	root := t.TempDir()
	if err := store.UpsertTree(root, []string{root}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	newDir := filepath.Join(root, "brand-new")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Give the watcher a beat to register, then drop a file in the new dir.
	time.Sleep(120 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(newDir, "f.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// We expect rescans for both the parent (Create event) and the child
	// (file write inside the new sub-root). Wait long enough that both
	// debounces fire.
	_ = waitForCalls(t, runner, 2, 2*time.Second)

	rootEntries := store.List()
	foundChild := false
	for _, r := range rootEntries {
		if r.Path == newDir {
			foundChild = true
			if r.Origin != root {
				t.Errorf("origin: want %q, got %q", root, r.Origin)
			}
		}
	}
	if !foundChild {
		t.Fatalf("expected new subdir registered as sub-root; entries=%v", rootEntries)
	}
}

func TestWatcher_RemoveDropsWatchAndStoreEntry(t *testing.T) {
	w, runner, store := newWatcher(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTree(root, []string{root, sub}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(30 * time.Millisecond)

	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to react.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		stillThere := false
		for _, r := range store.List() {
			if r.Path == sub {
				stillThere = true
				break
			}
		}
		if !stillThere {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for _, r := range store.List() {
		if r.Path == sub {
			t.Fatal("sub-root entry still present after directory removal")
		}
	}
	// The parent's debounce timer should fire shortly; wait for it.
	if calls := waitForCalls(t, runner, 1, 1*time.Second); len(calls) == 0 {
		t.Error("expected at least one rescan after subdir removal")
	}
}
