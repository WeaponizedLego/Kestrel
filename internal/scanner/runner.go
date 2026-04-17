package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
)

// ErrScanInProgress is returned by Runner.Start when another scan is
// already active. Only one scan runs at a time — Kestrel is a
// single-user desktop app and concurrent scans would confuse the
// progress UI and multiply disk pressure.
var ErrScanInProgress = errors.New("a scan is already in progress")

// Runner owns the lifecycle of background scans: start, cancel,
// observe. It wraps Scan with a context the caller can flip from a
// different goroutine (e.g. a second HTTP request) so long scans
// don't hold the client.
//
// Additive writes in Scan mean a cancelled scan keeps whatever got
// committed before the cancel — the user doesn't lose hours of
// hashing work by clicking Stop on a multi-TB run.
type Runner struct {
	lib      Library
	opts     Options
	onFinish func(added int, cancelled bool)

	mu     sync.Mutex
	active *scanHandle
}

type scanHandle struct {
	ID     string
	Root   string
	cancel context.CancelFunc
}

// RunnerConfig bundles the dependencies used by every scan launched
// through this runner. Publisher/ThumbStore/Thumbnailer are the same
// fields scanner.Options has; collapsing them into RunnerConfig means
// main.go configures them once at startup instead of per-request.
type RunnerConfig struct {
	Library     Library
	Publisher   Publisher
	ThumbStore  ThumbStore
	Thumbnailer Thumbnailer
	// OnFinish runs after every scan, successful or cancelled. Used
	// by main.go to flush library_meta.gob — so a cancelled scan's
	// work survives a crash even before the auto-save ticker fires.
	OnFinish func(added int, cancelled bool)
}

// NewRunner returns a Runner configured with the given deps. The
// returned value is safe for concurrent Start/Cancel/Active calls.
func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{
		lib: cfg.Library,
		opts: Options{
			Publisher:   cfg.Publisher,
			ThumbStore:  cfg.ThumbStore,
			Thumbnailer: cfg.Thumbnailer,
		},
		onFinish: cfg.OnFinish,
	}
}

// Start schedules a scan of root in a background goroutine and
// returns immediately with the scan ID. Fails with ErrScanInProgress
// if another scan is active. Observers drive the UI via the
// scan:started / scan:progress / scan:done events.
func (r *Runner) Start(root string) (string, error) {
	r.mu.Lock()
	if r.active != nil {
		r.mu.Unlock()
		return "", ErrScanInProgress
	}
	id := newScanID()
	ctx, cancel := context.WithCancel(context.Background())
	r.active = &scanHandle{ID: id, Root: root, cancel: cancel}
	r.mu.Unlock()

	r.publish("scan:started", map[string]string{"id": id, "root": root})
	go r.run(ctx, id, root)
	return id, nil
}

// Cancel stops the running scan. Returns true if there was one to
// cancel. The scan goroutine still runs through its cleanup —
// scan:done, OnFinish — so callers observe completion through those.
func (r *Runner) Cancel() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return false
	}
	r.active.cancel()
	return true
}

// Active reports the running scan's ID and root, or ("", "") when
// idle. The ID stays non-empty until the goroutine fully drains
// after a cancel, so a "is cancelling" UI can display this value.
func (r *Runner) Active() (id, root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return "", ""
	}
	return r.active.ID, r.active.Root
}

// run is the body of the background goroutine: invoke Scan, publish
// the terminal events, clear active, run OnFinish.
func (r *Runner) run(ctx context.Context, id, root string) {
	added, err := Scan(ctx, root, r.lib, r.opts)
	cancelled := ctx.Err() != nil

	r.mu.Lock()
	r.active = nil
	r.mu.Unlock()

	payload := map[string]any{
		"id":        id,
		"root":      root,
		"added":     added,
		"cancelled": cancelled,
	}
	if err != nil && !cancelled {
		payload["error"] = err.Error()
		slog.Error("scan failed", "root", root, "err", err)
	}
	r.publish("scan:done", payload)
	// library:updated is the "refresh your view" event — published
	// separately so future non-scan mutations can reuse it.
	r.publish("library:updated", map[string]int{
		"count": r.lib.Len(),
		"added": added,
	})

	if r.onFinish != nil {
		r.onFinish(added, cancelled)
	}
}

func (r *Runner) publish(kind string, payload any) {
	if r.opts.Publisher != nil {
		r.opts.Publisher.Publish(kind, payload)
	}
}

// newScanID returns a random hex string. Short (16 hex chars) — IDs
// only need to disambiguate concurrent scans within one session, not
// be globally unique.
func newScanID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
