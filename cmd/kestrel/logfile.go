package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// logRotateSize is the byte threshold at which the active log file is
// renamed to <path>.1 (overwriting any previous backup) and a fresh
// file is opened. 10 MiB keeps a useful diagnostic window without
// risking the user's disk on a runaway loop.
const logRotateSize int64 = 10 << 20

// rotatingFile is a tiny size-triggered log rotator. It implements
// io.Writer so slog can target it directly, and io.Closer so the
// shutdown path can flush and release the OS handle.
//
// Two files are kept on disk at most: the active path and one rolled
// backup at path+".1". Older history is dropped on rotation — this is
// a local diagnostic log, not an audit trail.
type rotatingFile struct {
	path    string
	maxSize int64

	mu   sync.Mutex
	file *os.File
	size int64
}

// openRotatingFile opens path for appended writes, creating the parent
// directory and the file if needed, and seeds the in-memory size from
// Stat so subsequent writes can decide on rotation without re-stating.
func openRotatingFile(path string, maxSize int64) (*rotatingFile, error) {
	f, size, err := openLogFile(path)
	if err != nil {
		return nil, err
	}
	return &rotatingFile{
		path:    path,
		maxSize: maxSize,
		file:    f,
		size:    size,
	}, nil
}

// Write appends p to the active log file, rotating first if the new
// total would cross maxSize. Rotation failures fall back to writing
// into the existing file rather than dropping the line — losing log
// output is worse than a slightly-oversized file.
func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size+int64(len(p)) > r.maxSize {
		if err := r.rotateLocked(); err != nil {
			// Best-effort: report to stderr (slog itself may be the
			// caller, so we can't recurse into it) and keep going.
			fmt.Fprintf(os.Stderr, "kestrel: log rotation failed: %v\n", err)
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// Close releases the underlying file handle. Safe to call on a nil
// receiver so callers can `defer h.Close()` without nil-checking.
func (r *rotatingFile) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// rotateLocked closes the active file, renames it to <path>.1
// (overwriting any existing backup), and reopens path fresh. Caller
// must hold r.mu.
func (r *rotatingFile) rotateLocked() error {
	if err := r.file.Close(); err != nil {
		return fmt.Errorf("closing log for rotation: %w", err)
	}
	r.file = nil

	backup := r.path + ".1"
	// Best-effort remove of the previous backup. os.Rename overwrites
	// on POSIX but errors on Windows when the destination exists, so
	// the explicit Remove keeps the rotator portable.
	_ = os.Remove(backup)
	if err := os.Rename(r.path, backup); err != nil && !os.IsNotExist(err) {
		// Couldn't rotate; reopen the original and keep writing into
		// it so we don't lose subsequent log output.
		f, size, openErr := openLogFile(r.path)
		if openErr != nil {
			return fmt.Errorf("renaming %s to %s: %w", r.path, backup, err)
		}
		r.file = f
		r.size = size
		return fmt.Errorf("renaming %s to %s: %w", r.path, backup, err)
	}

	f, _, err := openLogFile(r.path)
	if err != nil {
		return err
	}
	r.file = f
	r.size = 0
	return nil
}

// openLogFile creates the parent dir and opens path for appended
// writes, returning the current file size from Stat so the rotator
// can avoid a Stat-per-write.
func openLogFile(path string) (*os.File, int64, error) {
	if err := os.MkdirAll(parentDir(path), 0o755); err != nil {
		return nil, 0, fmt.Errorf("creating log dir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, 0, fmt.Errorf("opening log file %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("stat log file %s: %w", path, err)
	}
	return f, info.Size(), nil
}

// parentDir is filepath.Dir without pulling the import into this file
// just for one call site. Kept as its own helper so the inline use in
// openLogFile reads cleanly.
func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

// multiHandler fans a single slog record out to several backing
// handlers. Used to send the same log line to both stderr and the
// rotating file. Errors from any handler are joined so a broken sink
// doesn't silently swallow output from the others.
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return joinErrs(errs)
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: next}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: next}
}

func joinErrs(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	// errors.Join exists since Go 1.20 — but importing "errors" here
	// just for one call clutters the file; build the chain manually.
	out := errs[0]
	for _, e := range errs[1:] {
		out = fmt.Errorf("%w; %v", out, e)
	}
	return out
}

// ensure rotatingFile satisfies io.WriteCloser at compile time so
// tests and main wiring catch interface drift early.
var _ io.WriteCloser = (*rotatingFile)(nil)
