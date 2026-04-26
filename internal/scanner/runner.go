package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/metadata/autotag"
)

// IntensityNormal is the default full-speed scan a user triggers via
// POST /api/scan. IntensityLow is the background rescanner's mode:
// fewer workers, a small per-file sleep, and preemptible when a user
// scan arrives.
const (
	IntensityNormal = "normal"
	IntensityLow    = "low"
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
	ID        string
	Root      string
	Intensity string
	cancel    context.CancelFunc
	done      chan struct{} // closed when the run goroutine exits
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

	// Autotag is passed through to each Scan as Options.Autotag.
	// A zero value is safe: scanner-level autotag.Derive then emits
	// the cheap subset (kind, year, camera, …).
	Autotag autotag.Options

	// OnFinish runs after every scan, successful or cancelled. Used
	// by main.go to flush library_meta.gob — so a cancelled scan's
	// work survives a crash even before the auto-save ticker fires —
	// and to invalidate the cluster cache so the next Tagging Queue
	// query reflects freshly-hashed photos.
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
			Autotag:     cfg.Autotag,
		},
		onFinish: cfg.OnFinish,
	}
}

// Start schedules a full-speed scan of root. If the runner is busy
// with a low-intensity background rescan, that one is cancelled and
// drained first so the user's action wins — low-intensity is exactly
// the kind of work we want to yield to user intent. A normal-priority
// scan in progress still returns ErrScanInProgress unchanged.
func (r *Runner) Start(root string) (string, error) {
	return r.start(root, IntensityNormal, r.opts, true)
}

// LowOptions tunes one background-rescan run. Zero values fall back
// to the runner's defaults, so a scheduler that just wants "something
// quieter than full speed" can pass LowOptions{} without picking
// magic numbers itself.
type LowOptions struct {
	Workers       int
	ThrottleSleep time.Duration
}

// StartLowIntensity schedules a background rescan of root with the
// given throttling. Fails fast with ErrScanInProgress if any scan is
// already running — the scheduler just skips this cycle and tries
// again on the next tick rather than preempting a peer or a user
// scan it hasn't yet learned about.
func (r *Runner) StartLowIntensity(root string, low LowOptions) (string, error) {
	opts := r.opts
	opts.Workers = low.Workers
	opts.ThrottleSleep = low.ThrottleSleep
	return r.start(root, IntensityLow, opts, false)
}

// start is the shared body of Start and StartLowIntensity. preempt
// controls whether a running low-intensity scan is cancelled to make
// room; user-triggered scans set it true, the scheduler sets it
// false.
func (r *Runner) start(root, intensity string, opts Options, preempt bool) (string, error) {
	if preempt {
		if err := r.PreemptLowIntensity(); err != nil {
			return "", err
		}
	}

	r.mu.Lock()
	if r.active != nil {
		r.mu.Unlock()
		return "", ErrScanInProgress
	}
	id := newScanID()
	ctx, cancel := context.WithCancel(context.Background())
	r.active = &scanHandle{
		ID:        id,
		Root:      root,
		Intensity: intensity,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	handle := r.active
	r.mu.Unlock()

	r.publish("scan:started", map[string]any{
		"id":        id,
		"root":      root,
		"intensity": intensity,
	})
	slog.Info("scan started", "id", id, "root", root, "intensity", intensity)
	go r.run(ctx, id, root, intensity, opts, handle)
	return id, nil
}

// PreemptLowIntensity cancels an active low-intensity scan and waits
// for its goroutine to drain. A normal scan is left alone so two
// concurrent user scans still collide cleanly via ErrScanInProgress.
// Returns nil when there's nothing to preempt. Exported so callers
// that want to claim exclusive access to the library (file ops) can
// kick the background scanner aside first.
func (r *Runner) PreemptLowIntensity() error {
	r.mu.Lock()
	if r.active == nil || r.active.Intensity != IntensityLow {
		r.mu.Unlock()
		return nil
	}
	handle := r.active
	handle.cancel()
	r.mu.Unlock()

	// Wait outside the lock so the goroutine can grab it to clear
	// active and publish terminal events. run() closes handle.done
	// after it finishes those, so we're safe to proceed right after.
	<-handle.done
	return nil
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

// Active reports the running scan's ID, root, and intensity, or
// ("", "", "") when idle. The ID stays non-empty until the goroutine
// fully drains after a cancel, so a "is cancelling" UI can display
// this value.
func (r *Runner) Active() (id, root string) {
	id, root, _ = r.ActiveDetail()
	return id, root
}

// ActiveDetail is Active with the scan's intensity tag. Exposed so
// /api/scan/status can tell the UI whether the current run is a
// user-triggered foreground scan or a quiet background rescan.
func (r *Runner) ActiveDetail() (id, root, intensity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return "", "", ""
	}
	return r.active.ID, r.active.Root, r.active.Intensity
}

// WaitForActive blocks until the currently-active scan (if any)
// finishes. Returns immediately when idle. Used by the background
// scheduler to sequence one root after another.
func (r *Runner) WaitForActive() {
	r.mu.Lock()
	if r.active == nil {
		r.mu.Unlock()
		return
	}
	done := r.active.done
	r.mu.Unlock()
	<-done
}

// Shutdown cancels any active scan and waits for it to drain. Safe
// to call when idle. Wired into main.go's shutdown path so the
// process doesn't exit with a scan goroutine still writing to disk.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	if r.active == nil {
		r.mu.Unlock()
		return
	}
	handle := r.active
	handle.cancel()
	r.mu.Unlock()
	<-handle.done
}

// run is the body of the background goroutine: invoke Scan, publish
// the terminal events, clear active, run OnFinish.
func (r *Runner) run(ctx context.Context, id, root, intensity string, opts Options, handle *scanHandle) {
	start := time.Now()
	added, err := Scan(ctx, root, r.lib, opts)
	cancelled := ctx.Err() != nil
	duration := time.Since(start)

	r.mu.Lock()
	r.active = nil
	r.mu.Unlock()

	payload := map[string]any{
		"id":        id,
		"root":      root,
		"added":     added,
		"cancelled": cancelled,
		"intensity": intensity,
	}
	if err != nil && !cancelled {
		payload["error"] = err.Error()
		slog.Error("scan failed", "root", root, "err", err)
	}
	r.publish("scan:done", payload)
	slog.Info("scan finished",
		"id", id,
		"root", root,
		"intensity", intensity,
		"added", added,
		"cancelled", cancelled,
		"duration", duration,
	)
	// library:updated is the "refresh your view" event — published
	// separately so future non-scan mutations can reuse it.
	r.publish("library:updated", map[string]int{
		"count": r.lib.Len(),
		"added": added,
	})

	if r.onFinish != nil {
		r.onFinish(added, cancelled)
	}
	close(handle.done)
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
