package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// dirsFoundFlushEvery and dirsFoundFlushInterval bound how aggressively
// the BFS walker fans discovered directories out to OnDirsFound. The
// flusher fires whichever boundary it hits first so a slow walk still
// publishes regularly and a fast walk doesn't drown the hub.
const (
	dirsFoundFlushEvery    = 100
	dirsFoundFlushInterval = 50 * time.Millisecond
)

// dirQueueSize bounds how many directories the BFS pool may queue
// before walkers block. Big enough to keep workers fed on deep trees,
// small enough that cancellation drains promptly.
const dirQueueSize = 1024

// walkOptions parameterises the BFS walker. Workers <= 0 means
// runtime.NumCPU(). OnDirsFound is invoked with batches of newly
// discovered absolute directory paths, including root itself, so a
// caller decomposing into sub-roots sees every directory exactly once.
type walkOptions struct {
	Workers      int
	OnDirsFound  func(dirs []string)
	SingleThread bool
}

// shouldSingleThreadWalk reports whether the caller forced single-
// threaded mode or the environment escape hatch is set. Spinning disks
// are the canonical reason to opt out — concurrent seeks ruin
// throughput on rotational media.
func shouldSingleThreadWalk(opts walkOptions) bool {
	if opts.SingleThread {
		return true
	}
	return os.Getenv("KESTREL_SINGLE_THREAD_WALK") == "1"
}

// walkPathsBFS is a parallel breadth-first directory walker. It writes
// every supported-media file path into paths and, if OnDirsFound is
// non-nil, batches discovered directory paths through the callback.
// Returns the number of files queued.
//
// The pool tracks outstanding directories with an atomic counter; the
// walk is complete the moment the counter hits zero, at which point
// the directory queue is closed and workers drain. The caller closes
// paths after walkPathsBFS returns.
func walkPathsBFS(ctx context.Context, root string, paths chan<- string, opts walkOptions) (int, error) {
	if shouldSingleThreadWalk(opts) {
		return walkPathsSingle(ctx, root, paths, opts)
	}

	slog.Info("scan walking root (parallel)", "root", root, "workers", walkWorkerCount(opts))

	// Verify the root itself is readable up front so the user sees a
	// real error rather than a silent zero-count walk.
	if _, err := os.Stat(root); err != nil {
		return 0, fmt.Errorf("walking %s: %w", root, err)
	}

	dirs := make(chan string, dirQueueSize)
	var outstanding atomic.Int64
	var fileCount atomic.Int64

	flusher := newDirsFoundFlusher(opts.OnDirsFound)
	defer flusher.close()

	// Seed with root itself.
	flusher.add(root)
	outstanding.Add(1)
	dirs <- root

	var firstErr error
	var errMu sync.Mutex
	recordErr := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	var wg sync.WaitGroup
	workers := walkWorkerCount(opts)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case dir, ok := <-dirs:
					if !ok {
						return
					}
					if err := walkOneDir(ctx, dir, dirs, paths, &outstanding, &fileCount, flusher); err != nil {
						if !errors.Is(err, context.Canceled) {
							recordErr(err)
						}
					}
					// Decrement and, if we just drained the last
					// outstanding directory, close dirs to release
					// every worker.
					if outstanding.Add(-1) == 0 {
						close(dirs)
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		slog.Info("scan walk cancelled", "root", root, "queued", fileCount.Load())
		return int(fileCount.Load()), err
	}
	if firstErr != nil {
		slog.Info("scan walk ended with error", "root", root, "queued", fileCount.Load(), "err", firstErr)
		return int(fileCount.Load()), firstErr
	}
	slog.Info("scan walk complete", "root", root, "queued", fileCount.Load())
	return int(fileCount.Load()), nil
}

// walkOneDir lists dir's children, classifies each, and either pushes
// subdirectories back onto dirs or sends file paths to paths. It does
// not adjust outstanding for `dir` itself — the caller owns that
// decrement after the function returns. Each child directory pushed
// adds exactly 1 to outstanding before being enqueued.
func walkOneDir(
	ctx context.Context,
	dir string,
	dirs chan<- string,
	paths chan<- string,
	outstanding *atomic.Int64,
	fileCount *atomic.Int64,
	flusher *dirsFoundFlusher,
) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A single unreadable directory must not abort the entire
		// walk — log and skip, matching the scanner's "skip unreadable
		// files" stance.
		slog.Warn("walk: reading dir failed", "dir", dir, "err", err)
		return nil
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		full := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if ShouldSkipDir(entry.Name()) {
				continue
			}
			flusher.add(full)
			outstanding.Add(1)
			select {
			case dirs <- full:
			case <-ctx.Done():
				// Roll back the optimistic increment so the close
				// condition is still reachable when the in-flight
				// workers drain.
				outstanding.Add(-1)
				return ctx.Err()
			}
			continue
		}
		if !isSupportedMedia(full) {
			continue
		}
		select {
		case paths <- full:
			fileCount.Add(1)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// walkWorkerCount resolves opts.Workers for the walker, with the same
// 1..NumCPU clamping the file-processor pool uses.
func walkWorkerCount(opts walkOptions) int {
	max := runtime.NumCPU()
	if opts.Workers <= 0 {
		return max
	}
	if opts.Workers > max {
		return max
	}
	return opts.Workers
}

// walkPathsSingle is the single-threaded fallback retained behind
// KESTREL_SINGLE_THREAD_WALK=1 for spinning disks where concurrent
// seeks would tank throughput. Behaviour matches the historic
// walkPaths implementation exactly so a parity test can compare both.
func walkPathsSingle(ctx context.Context, root string, paths chan<- string, opts walkOptions) (int, error) {
	slog.Info("scan walking root (single-thread)", "root", root)
	flusher := newDirsFoundFlusher(opts.OnDirsFound)
	defer flusher.close()

	var count int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			if path != root && ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			flusher.add(path)
			return nil
		}
		if !isSupportedMedia(path) {
			return nil
		}
		select {
		case paths <- path:
			count++
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		slog.Info("scan walk ended", "root", root, "queued", count, "err", err)
	} else {
		slog.Info("scan walk complete", "root", root, "queued", count)
	}
	return count, err
}

// dirsFoundFlusher batches discovered directory paths and hands them
// off to the user's OnDirsFound callback in chunks. Flushes happen on
// either a path-count boundary or a periodic timer, whichever fires
// first, keeping WS chatter bounded regardless of walk speed.
//
// The flusher is goroutine-safe; the BFS walkers all write to the same
// instance.
type dirsFoundFlusher struct {
	cb     func([]string)
	mu     sync.Mutex
	buf    []string
	stop   chan struct{}
	closed bool
}

func newDirsFoundFlusher(cb func([]string)) *dirsFoundFlusher {
	f := &dirsFoundFlusher{cb: cb, stop: make(chan struct{})}
	if cb == nil {
		return f
	}
	go f.tickLoop()
	return f
}

func (f *dirsFoundFlusher) tickLoop() {
	ticker := time.NewTicker(dirsFoundFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-ticker.C:
			f.flush()
		}
	}
}

func (f *dirsFoundFlusher) add(path string) {
	if f.cb == nil {
		return
	}
	f.mu.Lock()
	f.buf = append(f.buf, path)
	full := len(f.buf) >= dirsFoundFlushEvery
	var batch []string
	if full {
		batch = f.buf
		f.buf = nil
	}
	f.mu.Unlock()
	if batch != nil {
		f.cb(batch)
	}
}

func (f *dirsFoundFlusher) flush() {
	f.mu.Lock()
	if len(f.buf) == 0 {
		f.mu.Unlock()
		return
	}
	batch := f.buf
	f.buf = nil
	f.mu.Unlock()
	f.cb(batch)
}

func (f *dirsFoundFlusher) close() {
	if f.cb == nil {
		return
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	f.mu.Unlock()
	close(f.stop)
	f.flush()
}
