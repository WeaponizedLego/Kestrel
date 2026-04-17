package scanner

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

// recordingPublisher captures Publish calls for assertions. Protected
// by a mutex because the runner publishes from its own goroutine.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}
type recordedEvent struct {
	Kind    string
	Payload any
}

func (r *recordingPublisher) Publish(kind string, payload any) {
	r.mu.Lock()
	r.events = append(r.events, recordedEvent{kind, payload})
	r.mu.Unlock()
}
func (r *recordingPublisher) byKind(kind string) []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedEvent
	for _, e := range r.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestRunner_StartEmitsStartedAndDone(t *testing.T) {
	root := t.TempDir()
	writeImageTree(t, root, []string{"a.jpg", "b.jpg"})

	lib := library.New()
	pub := &recordingPublisher{}
	var finishAdded int
	var finishCancelled bool
	finished := make(chan struct{})

	r := NewRunner(RunnerConfig{
		Library:   lib,
		Publisher: pub,
		OnFinish: func(added int, cancelled bool) {
			finishAdded = added
			finishCancelled = cancelled
			close(finished)
		},
	})

	id, err := r.Start(root)
	if err != nil || id == "" {
		t.Fatalf("Start: id=%q err=%v", id, err)
	}

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not finish in time")
	}

	if finishAdded != 2 || finishCancelled {
		t.Fatalf("OnFinish: added=%d cancelled=%v, want 2/false", finishAdded, finishCancelled)
	}

	if got := pub.byKind("scan:started"); len(got) != 1 {
		t.Errorf("scan:started count = %d, want 1", len(got))
	}
	if got := pub.byKind("scan:done"); len(got) != 1 {
		t.Errorf("scan:done count = %d, want 1", len(got))
	}
}

func TestRunner_SecondStartIsRefused(t *testing.T) {
	root := t.TempDir()
	// Enough files that the first scan is still running when we try
	// a second Start. 50 tiny files is plenty on any reasonable host
	// because hashing + png decode dominates.
	names := make([]string, 50)
	for i := range names {
		names[i] = fmt.Sprintf("img%d.jpg", i)
	}
	writeImageTree(t, root, names)

	r := NewRunner(RunnerConfig{Library: library.New()})
	if _, err := r.Start(root); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := r.Start(root)
	if !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("second Start err = %v, want ErrScanInProgress", err)
	}
	// Let the first scan drain so the test doesn't leave a goroutine.
	r.Cancel()
	waitIdle(t, r)
}

func TestRunner_CancelKeepsPartialWork(t *testing.T) {
	// A cancelled scan must leave behind whatever was added before
	// the cancel took effect. Because AddPhoto is atomic per file,
	// the exact count is non-deterministic, but it must be ≥ 0 and
	// ≤ total, and the runner must emit scan:done with cancelled=true.
	root := t.TempDir()
	names := make([]string, 100)
	for i := range names {
		names[i] = fmt.Sprintf("img%d.jpg", i)
	}
	writeImageTree(t, root, names)

	lib := library.New()
	pub := &recordingPublisher{}
	done := make(chan bool, 1)

	r := NewRunner(RunnerConfig{
		Library:   lib,
		Publisher: pub,
		OnFinish:  func(_ int, c bool) { done <- c },
	})
	if _, err := r.Start(root); err != nil {
		t.Fatal(err)
	}
	// Cancel almost immediately — workers may have committed a few
	// files by this point, which is what we want to assert.
	r.Cancel()

	var cancelled bool
	select {
	case cancelled = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled scan never finished")
	}
	if !cancelled {
		t.Fatal("OnFinish reported cancelled=false after Cancel()")
	}
	if lib.Len() > len(names) {
		t.Fatalf("lib.Len = %d exceeds total %d", lib.Len(), len(names))
	}
}

func TestRunner_CancelIdleIsNoOp(t *testing.T) {
	r := NewRunner(RunnerConfig{Library: library.New()})
	if r.Cancel() {
		t.Fatal("Cancel returned true on an idle runner")
	}
}

// waitIdle polls Active until the runner reports no scan. Prevents
// test goroutines leaking and makes ordering explicit.
func waitIdle(t *testing.T, r *Runner) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if id, _ := r.Active(); id == "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("runner never became idle")
}
