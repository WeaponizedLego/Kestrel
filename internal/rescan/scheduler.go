// Package rescan drives the background sync loop that keeps the
// library in step with disk. Every watched sub-root is periodically
// re-walked in a low-intensity mode — fewer workers, a small per-file
// sleep, preemptible by user-triggered scans — so a user can leave
// Kestrel open in the background (gaming, watching video) without
// feeling it grind.
//
// The design is deliberately small: one goroutine, one ticker, no
// queues. The fast path for newly-added files is the fsnotify-driven
// internal/watcher package; this scheduler is the backstop that
// catches missed events, prunes deletions, and copes with network
// mounts that don't deliver kernel notifications.
package rescan

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
	"github.com/WeaponizedLego/kestrel/internal/watchroots"
)

// Default cadence. Conservative on purpose — the feature exists so
// the library stays roughly accurate, not so it reflects filesystem
// edits within seconds.
const (
	DefaultInterval      = 30 * time.Minute
	DefaultIdleThreshold = 2 * time.Minute
	DefaultPerRootGap    = 5 * time.Second
	DefaultThrottleSleep = 10 * time.Millisecond
)

// Activity is the subset of server.Activity the scheduler reads.
// Declared here (rather than imported from server) so the dependency
// direction stays rescan → nothing-in-server.
type Activity interface {
	LastActive() time.Time
}

// Publisher is the hub sliver used to broadcast library:updated
// after a prune. Matching scanner.Publisher's shape so main.go can
// pass the same Hub.
type Publisher interface {
	Publish(kind string, payload any)
}

// Config bundles the scheduler's collaborators and tunables. All
// Duration fields fall back to sensible defaults when zero.
type Config struct {
	Roots     *watchroots.Store
	Runner    *scanner.Runner
	Library   *library.Library
	Activity  Activity
	Publisher Publisher

	Interval      time.Duration
	IdleThreshold time.Duration
	PerRootGap    time.Duration

	// Workers and ThrottleSleep control the low-intensity posture.
	// Zero Workers → runtime.NumCPU()/4 (at least 1). Zero
	// ThrottleSleep → DefaultThrottleSleep.
	Workers       int
	ThrottleSleep time.Duration
}

// Scheduler is the background rescan loop. Create with New, then
// call Run in a goroutine tied to the process's shutdown context.
type Scheduler struct {
	cfg Config
}

// New returns a Scheduler with the given configuration. Zero-valued
// Duration fields are replaced with package defaults so callers can
// pass the minimum dependency set and get reasonable behaviour.
func New(cfg Config) *Scheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.IdleThreshold <= 0 {
		cfg.IdleThreshold = DefaultIdleThreshold
	}
	if cfg.PerRootGap < 0 {
		cfg.PerRootGap = DefaultPerRootGap
	}
	if cfg.ThrottleSleep <= 0 {
		cfg.ThrottleSleep = DefaultThrottleSleep
	}
	if cfg.Workers <= 0 {
		w := runtime.NumCPU() / 4
		if w < 1 {
			w = 1
		}
		cfg.Workers = w
	}
	return &Scheduler{cfg: cfg}
}

// Run blocks until ctx is cancelled, executing one rescan cycle per
// tick. The very first tick fires after Interval, not immediately,
// so a freshly-started process doesn't compete with the user's
// startup interactions.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cycle(ctx)
		}
	}
}

// cycle runs one pass over every watched root, subject to idle and
// busy gates. Any error in one root is logged and skipped — the next
// cycle gets another chance.
func (s *Scheduler) cycle(ctx context.Context) {
	if s.cfg.Roots == nil || s.cfg.Runner == nil || s.cfg.Library == nil {
		return
	}
	if s.cfg.Activity != nil {
		since := time.Since(s.cfg.Activity.LastActive())
		if since < s.cfg.IdleThreshold {
			slog.Info("rescan: user active recently, skipping cycle", "since", since)
			return
		}
	}
	if id, _ := s.cfg.Runner.Active(); id != "" {
		slog.Info("rescan: scan already running, skipping cycle", "id", id)
		return
	}

	roots := s.cfg.Roots.List()
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].LastScannedAt.Before(roots[j].LastScannedAt)
	})
	slog.Info("rescan cycle starting", "roots", len(roots))

	for _, root := range roots {
		if ctx.Err() != nil {
			return
		}
		s.rescanOne(ctx, root.Path)

		if s.cfg.PerRootGap > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.cfg.PerRootGap):
			}
		}
	}
}

// rescanOne kicks a low-intensity scan for root, waits for it to
// finish, then prunes photos under root that no longer exist on
// disk. A ErrScanInProgress from the runner means the user started
// something while we were deciding to go — bail without pretending
// we scanned.
func (s *Scheduler) rescanOne(ctx context.Context, root string) {
	_, err := s.cfg.Runner.StartLowIntensity(root, scanner.LowOptions{
		Workers:       s.cfg.Workers,
		ThrottleSleep: s.cfg.ThrottleSleep,
	})
	if errors.Is(err, scanner.ErrScanInProgress) {
		return
	}
	if err != nil {
		slog.Warn("rescan: starting low-intensity scan failed", "root", root, "err", err)
		return
	}

	s.cfg.Runner.WaitForActive()
	if ctx.Err() != nil {
		return
	}

	// Prune photos that vanished under this root since the last
	// cycle. Scoping by root means a transient error inside one
	// watched folder can't drop entries from unrelated folders.
	removed := s.cfg.Library.PruneMissingUnder(root, fileExists)
	if len(removed) > 0 && s.cfg.Publisher != nil {
		s.cfg.Publisher.Publish("library:updated", map[string]any{
			"pruned": len(removed),
			"root":   root,
		})
	}

	if err := s.cfg.Roots.MarkScanned(root, time.Now()); err != nil {
		slog.Warn("rescan: marking root scanned failed", "root", root, "err", err)
	}
	slog.Info("rescan root finished", "root", root, "pruned", len(removed))
}

// fileExists mirrors the semantics used by /api/resync: any non-
// ENOENT stat (including permission errors) is treated as "keep the
// entry". A flaky network mount must never drop photos from the
// library.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}
