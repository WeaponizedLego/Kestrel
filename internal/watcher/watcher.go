// Package watcher reacts to filesystem events on every watched
// sub-root and triggers a low-intensity rescan of just that directory
// within seconds of a change. It is the fast path for "I just dropped
// a screenshot, where is it?" workflows; the periodic scheduler in
// internal/rescan remains the backstop for missed events.
//
// The watcher subscribes to internal/watchroots.Store so adding a
// sub-root immediately registers a watch and removing one drops it.
// On a Create event for a new directory the watcher walks the new
// subtree, calls Store.UpsertTree, and the subscription fans out to
// register watches for every newly-registered descendant.
//
// Linux note: each fsnotify Add consumes one inotify watch slot. A
// huge tree can exhaust fs.inotify.max_user_watches (default 8192).
// We log a clear warning on ENOSPC and continue — that sub-root falls
// back to the periodic scheduler instead of the fast path.
package watcher

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/WeaponizedLego/kestrel/internal/scanner"
	"github.com/WeaponizedLego/kestrel/internal/watchroots"
)

// Defaults tuned for a desktop UX: events arrive in bursts (e.g. a
// file copy fires Create + several Writes), so a 3 s debounce after
// the last event coalesces a burst into one rescan.
const (
	DefaultDebounce = 3 * time.Second
	DefaultRetry    = 30 * time.Second
)

// Runner is the subset of *scanner.Runner the watcher needs.
type Runner interface {
	StartLowIntensity(root string, low scanner.LowOptions) (string, error)
}

// Store is the subset of *watchroots.Store the watcher needs.
type Store interface {
	List() []watchroots.Root
	Subscribe(fn watchroots.Subscriber) func()
	UpsertTree(origin string, paths []string) error
	Remove(path string) error
	IsIgnored(path string) bool
}

// Config bundles the watcher's collaborators and tunables.
type Config struct {
	Store    Store
	Runner   Runner
	LowOpts  scanner.LowOptions
	Debounce time.Duration
	Retry    time.Duration
}

// Watcher is the runtime owner of an fsnotify watch set keyed by
// sub-root path. Construct with New, then call Run on a goroutine
// tied to the process shutdown context.
type Watcher struct {
	cfg Config

	fsw *fsnotify.Watcher

	mu       sync.Mutex
	timers   map[string]*time.Timer
	watching map[string]string // path → origin

	// exhausted is set the first time fsnotify.Add fails with ENOSPC
	// or EMFILE. Once set, subsequent addWatch calls are no-ops — we
	// stop trying so a runaway tree can't drown the log or starve the
	// rest of the process for file descriptors. The periodic
	// scheduler keeps covering the affected sub-roots regardless.
	exhausted atomic.Bool
}

// New returns a Watcher with the given config. Caller must Run before
// any events flow.
func New(cfg Config) (*Watcher, error) {
	if cfg.Store == nil {
		return nil, errors.New("watcher: Store is required")
	}
	if cfg.Runner == nil {
		return nil, errors.New("watcher: Runner is required")
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = DefaultDebounce
	}
	if cfg.Retry <= 0 {
		cfg.Retry = DefaultRetry
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		cfg:      cfg,
		fsw:      fsw,
		timers:   map[string]*time.Timer{},
		watching: map[string]string{},
	}, nil
}

// Run blocks until ctx is cancelled, dispatching fsnotify events and
// reacting to watchroots subscription updates.
func (w *Watcher) Run(ctx context.Context) {
	defer func() { _ = w.fsw.Close() }()

	// Bootstrap: register every existing sub-root, skipping anything
	// that matches the scanner's skip rule (build artefacts, hidden
	// dirs, etc.). watchroots.json may already contain skipped paths
	// from before this filter existed; honouring it here prevents the
	// next launch from repeating an FD-exhaustion meltdown.
	for _, r := range w.cfg.Store.List() {
		if scanner.PathHasSkippedComponent(r.Path) || scanner.IsSystemPath(r.Path) {
			continue
		}
		w.addWatch(r.Path, r.Origin)
		if w.exhausted.Load() {
			break
		}
	}

	// Subscribe to set changes; the callback dispatches synchronously
	// from the store's mutator so we keep its work tiny — just queue
	// path updates onto a channel processed by Run.
	updates := make(chan storeUpdate, 64)
	unsub := w.cfg.Store.Subscribe(func(added, removed []watchroots.Root) {
		select {
		case updates <- storeUpdate{added: added, removed: removed}:
		default:
			// Channel full — fall back to a fresh List() reconciliation
			// next time we get a chance. The periodic scheduler will
			// catch any sub-root we miss until then.
			slog.Warn("watcher: store update channel full; relying on periodic scheduler")
		}
	})
	defer unsub()

	for {
		select {
		case <-ctx.Done():
			return

		case up := <-updates:
			for _, r := range up.added {
				w.addWatch(r.Path, r.Origin)
			}
			for _, r := range up.removed {
				w.dropWatch(r.Path)
			}

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ctx, ev)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Warn("watcher: fsnotify error", "err", err)
		}
	}
}

type storeUpdate struct {
	added   []watchroots.Root
	removed []watchroots.Root
}

// addWatch registers path with fsnotify and records the origin so
// dynamic-subdir creates can reuse it. ENOSPC on Linux means we hit
// the inotify limit — log loudly and continue without the watch; the
// scheduler will keep covering that path on its 30-min backstop
// cycle.
func (w *Watcher) addWatch(path, origin string) {
	if w.exhausted.Load() {
		return
	}
	if scanner.PathHasSkippedComponent(path) || scanner.IsSystemPath(path) {
		return
	}
	w.mu.Lock()
	if _, ok := w.watching[path]; ok {
		w.mu.Unlock()
		return
	}
	w.watching[path] = origin
	w.mu.Unlock()

	if err := w.fsw.Add(path); err != nil {
		// ENOSPC (Linux inotify limit) and EMFILE (per-process file
		// descriptor cap, hits first on macOS where each kqueue watch
		// burns one FD) both mean "we cannot watch any more paths."
		// Trip the circuit breaker once and stop trying — the
		// periodic scheduler keeps covering this path on its 30-min
		// cycle.
		if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) {
			if w.exhausted.CompareAndSwap(false, true) {
				slog.Warn(
					"watcher: hit OS watch/file-descriptor limit; disabling watcher and falling back to periodic scan. "+
						"On Linux raise fs.inotify.max_user_watches; on macOS raise the per-process FD limit (ulimit -n).",
					"path", path,
					"err", err,
				)
			}
			return
		}
		slog.Warn("watcher: add failed", "path", path, "err", err)
	}
}

// dropWatch unregisters path from fsnotify and forgets its timer.
func (w *Watcher) dropWatch(path string) {
	w.mu.Lock()
	delete(w.watching, path)
	if t, ok := w.timers[path]; ok {
		t.Stop()
		delete(w.timers, path)
	}
	w.mu.Unlock()
	if err := w.fsw.Remove(path); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
		// Path may already be gone (the directory was deleted on disk
		// before we got here); a "no such watch" is not interesting.
		slog.Debug("watcher: remove returned error", "path", path, "err", err)
	}
}

// handleEvent classifies an fsnotify event and either schedules a
// debounced rescan, registers a newly-created subdirectory, or drops
// a watch for a deleted directory.
func (w *Watcher) handleEvent(ctx context.Context, ev fsnotify.Event) {
	parent := filepath.Dir(ev.Name)
	w.mu.Lock()
	origin, ok := w.watching[parent]
	w.mu.Unlock()
	// We only respond to events under directories we explicitly watch.
	if !ok {
		return
	}

	switch {
	case ev.Op&fsnotify.Create != 0:
		// If the new path is a directory, walk it and register every
		// directory under it as a sub-root. Files inside fall out via
		// the rescan triggered below.
		if isDir(ev.Name) {
			w.registerNewSubtree(ev.Name, origin)
		}
		w.scheduleRescan(ctx, parent)

	case ev.Op&(fsnotify.Write|fsnotify.Rename|fsnotify.Chmod) != 0:
		w.scheduleRescan(ctx, parent)

	case ev.Op&fsnotify.Remove != 0:
		// A removed directory might be a sub-root itself; if so, drop
		// the watch and forget the entry. The store gets updated by
		// the resulting subscription event.
		w.mu.Lock()
		_, isSubRoot := w.watching[ev.Name]
		w.mu.Unlock()
		if isSubRoot {
			if err := w.cfg.Store.Remove(ev.Name); err != nil {
				slog.Warn("watcher: store remove failed", "path", ev.Name, "err", err)
			}
		}
		w.scheduleRescan(ctx, parent)
	}
}

// registerNewSubtree walks the freshly-created directory and adds
// every directory to the store under origin. The store fires
// subscribers, which calls back into addWatch for each new sub-root.
// Ignored paths are skipped by UpsertTree itself.
func (w *Watcher) registerNewSubtree(root, origin string) {
	if w.cfg.Store.IsIgnored(root) {
		return
	}
	if scanner.IsSystemPath(root) {
		return
	}
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && scanner.ShouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if scanner.IsSystemPath(path) {
			return filepath.SkipDir
		}
		if w.cfg.Store.IsIgnored(path) {
			return filepath.SkipDir
		}
		dirs = append(dirs, path)
		return nil
	})
	if err != nil {
		slog.Warn("watcher: walking new subtree failed", "path", root, "err", err)
	}
	if len(dirs) == 0 {
		return
	}
	if err := w.cfg.Store.UpsertTree(origin, dirs); err != nil {
		slog.Warn("watcher: upserting new subtree failed", "path", root, "err", err)
	}
}

// scheduleRescan starts (or resets) the per-path debounce timer. When
// it fires we trigger a low-intensity scan of just that sub-root. If
// the runner is busy we re-arm a longer retry timer rather than drop
// the event.
func (w *Watcher) scheduleRescan(ctx context.Context, path string) {
	w.mu.Lock()
	if t, ok := w.timers[path]; ok {
		t.Stop()
	}
	w.timers[path] = time.AfterFunc(w.cfg.Debounce, func() {
		w.fireRescan(ctx, path)
	})
	w.mu.Unlock()
}

// fireRescan kicks off the low-intensity scan; on a busy runner it
// reschedules itself after Retry. Run from a timer goroutine.
func (w *Watcher) fireRescan(ctx context.Context, path string) {
	if ctx.Err() != nil {
		return
	}
	_, err := w.cfg.Runner.StartLowIntensity(path, w.cfg.LowOpts)
	if err == nil {
		slog.Info("watcher: rescan triggered", "path", path)
		return
	}
	if errors.Is(err, scanner.ErrScanInProgress) {
		slog.Debug("watcher: runner busy, retrying", "path", path, "retry", w.cfg.Retry)
		w.mu.Lock()
		w.timers[path] = time.AfterFunc(w.cfg.Retry, func() {
			w.fireRescan(ctx, path)
		})
		w.mu.Unlock()
		return
	}
	slog.Warn("watcher: rescan failed to start", "path", path, "err", err)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
