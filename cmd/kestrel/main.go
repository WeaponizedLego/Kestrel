// Command kestrel starts the loopback HTTP server, opens the user's
// default browser, and blocks until a termination signal arrives. The
// responsibilities kept here are narrow on purpose: flag parsing, wiring
// of collaborators, lifecycle signals. Everything else lives in the
// internal/* packages.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/api"
	"github.com/WeaponizedLego/kestrel/internal/assets"
	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/persistence"
	"github.com/WeaponizedLego/kestrel/internal/platform"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
	"github.com/WeaponizedLego/kestrel/internal/server"
	"github.com/WeaponizedLego/kestrel/internal/thumbnail"
)

// autoSaveInterval is how often the background persistence goroutine
// flushes the library to library_meta.gob. Five minutes keeps crash
// data loss bounded without grinding the disk on idle sessions.
const autoSaveInterval = 5 * time.Minute

// avgThumbnailBytes is the per-thumb estimate used to decide between
// Eager and Tiered mode at startup. 20 KB mirrors the assumption in
// docs/system-design.md → "Adaptive Mode Selection".
const avgThumbnailBytes int64 = 20 * 1024

// shutdownGrace is the max time Shutdown waits for in-flight HTTP
// requests to drain before forcing the server closed.
const shutdownGrace = 5 * time.Second

// browserIdleGrace is how long the server waits with zero WebSocket
// subscribers before treating "the user closed their browser" as a
// shutdown request. A short network blip or a tab refresh disconnects
// the socket; the frontend reconnects within ~1s (see events.ts
// backoff), so 10s is comfortable for real closure versus noise.
const browserIdleGrace = 10 * time.Second

// browserIdlePoll is the watcher's tick rate. Cheap — it only reads
// an atomic-ish counter on the Hub — so sub-second polling wouldn't
// hurt, but 1s is plenty responsive for a process-exit condition.
const browserIdlePoll = 1 * time.Second

// devToken is the fixed session token used in --dev mode. It matches
// the constant in frontend/vite.config.ts so `npm run dev` and
// `go run ./cmd/kestrel --dev` agree on auth without either side
// having to read the other's output. Not a secret: dev mode is
// loopback-only and the real token is regenerated in production.
const devToken = "dev-kestrel-token"

func main() {
	dev := flag.Bool("dev", false, "development mode: skip browser launch and asset embedding")
	addr := flag.String("addr", "", "address to bind; empty = 127.0.0.1:0 in prod, 127.0.0.1:5174 in --dev (matches vite proxy)")
	debug := flag.Bool("debug", false, "enable debug-level logging")
	flag.Parse()

	initLogging(*debug)

	bind := *addr
	if bind == "" {
		if *dev {
			bind = "127.0.0.1:5174"
		} else {
			bind = "127.0.0.1:0"
		}
	}

	if err := run(*dev, bind); err != nil {
		slog.Error("kestrel exiting", "err", err)
		os.Exit(1)
	}
}

// initLogging installs a text-format slog handler on the default
// logger. Text beats JSON for local developer output; switch to JSON
// later if we ship a log-collector.
func initLogging(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// run wires the server and blocks until a shutdown signal arrives.
func run(devMode bool, bind string) error {
	lib := library.New()

	metaPath, err := platform.LibraryMetaPath()
	if err != nil {
		return fmt.Errorf("resolving library metadata path: %w", err)
	}
	if err := loadLibrary(lib, metaPath); err != nil {
		// Persistence failure must not block startup — a fresh library
		// is always a valid state. Log and continue.
		slog.Warn("loading library failed; starting fresh", "path", metaPath, "err", err)
	}

	hub := server.NewHub()

	thumbsPath, err := platform.ThumbsPackPath()
	if err != nil {
		return fmt.Errorf("resolving thumbs pack path: %w", err)
	}
	pack, err := openThumbsPack(thumbsPath)
	if err != nil {
		return fmt.Errorf("opening thumbs pack: %w", err)
	}
	defer func() {
		if err := pack.Close(); err != nil {
			slog.Error("closing thumbs pack", "path", thumbsPath, "err", err)
		}
	}()

	provider := buildProvider(lib, pack, hub)
	defer provider.Close()

	runner := scanner.NewRunner(scanner.RunnerConfig{
		Library:     lib,
		Publisher:   hub,
		ThumbStore:  pack,
		Thumbnailer: thumbnail.Generate,
		// Flush metadata right after every scan — a cancelled scan
		// loses nothing more than whatever failed to save before the
		// next tick, instead of up to autoSaveInterval.
		OnFinish: func(added int, cancelled bool) {
			if err := persistence.Save(metaPath, lib.AllPhotos()); err != nil {
				slog.Error("post-scan save failed", "path", metaPath, "err", err)
			}
		},
	})

	libraryHandler := api.NewLibraryHandler(lib, runner, hub)
	thumbsHandler := api.NewThumbsHandler(provider)

	var token string
	if devMode {
		token = devToken
	} else {
		token, err = server.NewSessionToken()
		if err != nil {
			return fmt.Errorf("generating session token: %w", err)
		}
	}

	var assetFS fs.FS
	if !devMode {
		assetFS, err = assets.FS()
		if err != nil {
			return fmt.Errorf("loading embedded assets: %w", err)
		}
	}

	srv, url, err := server.Start(server.Config{
		Bind:           bind,
		Assets:         assetFS,
		Token:          token,
		DevMode:        devMode,
		LibraryHandler: libraryHandler,
		ThumbsHandler:  thumbsHandler,
		Hub:            hub,
	})
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}

	releaseLock, err := acquireSingleInstance(url)
	if err != nil {
		// Shut the half-started server down before returning.
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = server.Shutdown(ctx, srv)
		return err
	}
	defer releaseLock()

	slog.Info("kestrel listening", "url", url)

	autoSaveCtx, stopAutoSave := context.WithCancel(context.Background())
	defer stopAutoSave()
	go autoSaveLoop(autoSaveCtx, lib, metaPath, autoSaveInterval)

	shutdownCtx, triggerShutdown := context.WithCancel(context.Background())
	defer triggerShutdown()

	// Watch for the browser tab closing. We arm the watcher only after
	// a client has connected at least once, so a slow browser launch
	// (or a failed browser launch) doesn't instantly shut us down.
	// Disabled under --dev because hot reloads and dev-server restarts
	// would otherwise kill the backend repeatedly.
	if !devMode {
		go watchBrowserLifecycle(shutdownCtx, hub, browserIdleGrace, browserIdlePoll, func() {
			slog.Info("no browser tabs connected; shutting down")
			triggerShutdown()
		})
	}

	if !devMode {
		if err := platform.OpenBrowser(url); err != nil {
			slog.Warn("could not launch browser", "url", url, "err", err)
		}
	}

	waitForShutdown(shutdownCtx)
	slog.Info("shutdown requested")

	stopAutoSave()
	if err := persistence.Save(metaPath, lib.AllPhotos()); err != nil {
		slog.Error("final save failed", "path", metaPath, "err", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(ctx, srv); err != nil {
		return err
	}
	slog.Info("kestrel stopped cleanly")
	return nil
}

// acquireSingleInstance claims the single-instance lock for this run.
// If another live instance already holds it, opens the browser there
// and returns an error that shortcircuits startup. The returned
// release function clears the lock on shutdown.
func acquireSingleInstance(url string) (func(), error) {
	lockPath, err := platform.LockPath()
	if err != nil {
		return nil, fmt.Errorf("resolving lock path: %w", err)
	}
	ok, existing, err := platform.AcquireLock(lockPath, platform.LockInfo{
		PID: os.Getpid(),
		URL: url,
	})
	if err != nil {
		return nil, fmt.Errorf("acquiring single-instance lock: %w", err)
	}
	if !ok {
		slog.Info("another kestrel is already running; handing off", "url", existing.URL)
		if err := platform.OpenBrowser(existing.URL); err != nil {
			slog.Warn("could not re-open browser at running instance", "url", existing.URL, "err", err)
		}
		return nil, fmt.Errorf("kestrel is already running at %s", existing.URL)
	}
	release := func() {
		if err := platform.ReleaseLock(lockPath); err != nil {
			slog.Error("releasing single-instance lock", "path", lockPath, "err", err)
		}
	}
	return release, nil
}

// openThumbsPack creates the cache directory if missing and opens
// (or initialises) the pack file.
func openThumbsPack(path string) (*thumbnail.Pack, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating cache dir for %s: %w", path, err)
	}
	return thumbnail.Open(path)
}

// buildProvider picks Eager vs Tiered based on the estimated thumb
// memory footprint versus the detected budget, then wires the
// PathHasher closure that translates photo paths into pack hashes.
func buildProvider(lib *library.Library, pack *thumbnail.Pack, hub *server.Hub) *thumbnail.TieredProvider {
	budget := platform.ThumbnailBudget()
	photoCount := int64(lib.Len())
	estimated := photoCount * avgThumbnailBytes

	mode := thumbnail.ModeTiered
	if estimated > 0 && estimated < budget/2 {
		mode = thumbnail.ModeEager
	}

	slog.Info(
		"thumbnail cache configured",
		"mode", mode,
		"budget", platform.FormatBytes(budget),
		"photos", photoCount,
		"estimated", platform.FormatBytes(estimated),
	)

	provider := thumbnail.NewProvider(thumbnail.Config{
		Pack:        pack,
		Hasher:      hasherFor(lib),
		Publisher:   hub,
		Mode:        mode,
		BudgetBytes: budget,
	})

	if mode == thumbnail.ModeEager {
		paths := make([]string, 0, photoCount)
		for _, p := range lib.AllPhotos() {
			paths = append(paths, p.Path)
		}
		if err := provider.WarmEager(paths); err != nil {
			slog.Warn("warming eager cache", "err", err)
		}
	}
	return provider
}

// hasherFor returns a PathHasher backed by lib. Unknown paths and
// malformed hex digests are reported as not-found rather than errors
// so the provider treats them as "no thumbnail yet".
func hasherFor(lib *library.Library) thumbnail.PathHasher {
	return func(path string) ([32]byte, bool) {
		var zero [32]byte
		photo, err := lib.GetPhoto(path)
		if err != nil {
			return zero, false
		}
		return thumbnail.HashFromHex(photo.Hash)
	}
}

// loadLibrary reads library_meta.gob (if it exists) and populates lib.
// A missing file is intentionally not an error: first-run binaries
// start with an empty library and build one via the scan API.
func loadLibrary(lib *library.Library, path string) error {
	photos, err := persistence.Load(path)
	if err != nil {
		return err
	}
	if len(photos) == 0 {
		return nil
	}
	lib.ReplaceAll(photos)
	slog.Info("library loaded", "count", len(photos), "path", path)
	return nil
}

// autoSaveLoop flushes the library on a ticker until ctx is cancelled.
// Save errors are logged but don't stop the loop — the next tick will
// try again, and the shutdown save is the real safety net.
func autoSaveLoop(ctx context.Context, lib *library.Library, path string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := persistence.Save(path, lib.AllPhotos()); err != nil {
				slog.Error("auto-save failed", "path", path, "err", err)
			}
		}
	}
}

// waitForShutdown blocks until the process receives SIGINT/SIGTERM or
// the supplied context is cancelled (the browser-lifecycle watcher
// triggers this when the user has closed every tab).
func waitForShutdown(ctx context.Context) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-stop:
	case <-ctx.Done():
	}
}

// watchBrowserLifecycle calls onIdle when the hub has been empty for
// grace after having been non-empty at least once. Returns when ctx
// is cancelled. Designed for the "close the browser tab → kill the
// server" UX: without this, the Go binary outlives its only client
// and leaks into the user's process list.
//
// The "has been non-empty at least once" guard is important: the
// first browser connection arrives a few hundred ms after startup,
// and we don't want to shut down before the user's tab has ever
// reached us.
func watchBrowserLifecycle(
	ctx context.Context,
	hub *server.Hub,
	grace time.Duration,
	poll time.Duration,
	onIdle func(),
) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	armed := false
	var idleSince time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if hub.SubscriberCount() > 0 {
				armed = true
				idleSince = time.Time{}
				continue
			}
			if !armed {
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
				continue
			}
			if time.Since(idleSince) >= grace {
				onIdle()
				return
			}
		}
	}
}
