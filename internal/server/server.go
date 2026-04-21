// Package server owns the HTTP transport layer: router, middleware,
// listener lifecycle, and (in later phases) the WebSocket hub. Domain
// packages register handlers against the router it exposes; they must
// not import server in the reverse direction.
package server

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"

	"github.com/WeaponizedLego/kestrel/internal/api"
)

// Config bundles the collaborators Start needs to build a server.
// Handlers are wired here so main.go can stay small.
type Config struct {
	// Bind is the address to listen on. Use "127.0.0.1:0" to let the OS
	// pick a free port; the chosen port is returned in the URL.
	Bind string

	// Assets is the embedded frontend build (see internal/assets). In
	// DevMode it may be nil: the frontend is served by Vite instead.
	Assets fs.FS

	// Token is the per-run session token required on every /api/* call.
	Token string

	// DevMode disables asset serving at "/" so the user can point a
	// browser at Vite's dev server directly.
	DevMode bool

	// LibraryHandler handles /api/library and /api/scan endpoints.
	LibraryHandler *api.LibraryHandler

	// ThumbsHandler handles /api/thumb and prefetch hint endpoints.
	ThumbsHandler *api.ThumbsHandler

	// TaggingHandler handles /api/clusters and /api/tagging/* endpoints.
	// Optional: passing nil disables assisted tagging without breaking
	// the rest of the API surface.
	TaggingHandler *api.TaggingHandler

	// FileOpsHandler handles /api/files/{move,delete,undo} — the
	// destructive endpoints. Optional: nil disables file operations.
	FileOpsHandler *api.FileOpsHandler

	// Hub fans events out to /ws subscribers. Required.
	Hub *Hub
}

// Start binds a listener, wires the router, and starts serving in a
// goroutine. It returns the running http.Server, the base URL clients
// should hit, and any startup error. Call srv.Shutdown(ctx) to stop.
func Start(cfg Config) (*http.Server, string, error) {
	listener, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		return nil, "", fmt.Errorf("listening on %s: %w", cfg.Bind, err)
	}

	mux := http.NewServeMux()
	registerAPI(mux, cfg)
	if cfg.Hub != nil {
		mux.Handle("/ws", wsHandler(cfg.Hub, cfg.Token, listener.Addr().String(), cfg.DevMode))
	}
	if !cfg.DevMode && cfg.Assets != nil {
		mux.Handle("/", assetsHandler(cfg.Assets, cfg.Token))
	}

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("http server stopped unexpectedly", "err", err)
		}
	}()

	url := fmt.Sprintf("http://%s", listener.Addr().String())
	return srv, url, nil
}

// registerAPI wires every /api/* route through the token middleware.
func registerAPI(mux *http.ServeMux, cfg Config) {
	apiMux := http.NewServeMux()
	cfg.LibraryHandler.Register(apiMux)
	if cfg.ThumbsHandler != nil {
		cfg.ThumbsHandler.Register(apiMux)
	}
	if cfg.TaggingHandler != nil {
		cfg.TaggingHandler.Register(apiMux)
	}
	if cfg.FileOpsHandler != nil {
		cfg.FileOpsHandler.Register(apiMux)
	}
	mux.Handle("/api/", tokenMiddleware(cfg.Token, http.StripPrefix("/api", apiMux)))
}

// Shutdown is a thin wrapper so callers don't import net/http just to
// stop the server cleanly.
func Shutdown(ctx context.Context, srv *http.Server) error {
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down http server: %w", err)
	}
	return nil
}
