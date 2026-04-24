// Command kestrel-vision is the optional ML sidecar for Kestrel.
// It binds a loopback HTTP server, writes a small handshake file
// that the core binary reads to locate it, and serves detection
// requests for face embeddings and object classes.
//
// Two build modes coexist here:
//
//   - With CGO enabled (//go:build cgo), the sidecar links ONNX
//     Runtime through github.com/yalue/onnxruntime_go and runs
//     SCRFD (face detection) + ArcFace (face embedding) + YOLOv8n
//     (object detection) over every posted image.
//
//   - Without CGO, the sidecar still builds and still serves valid
//     responses — they just come back empty. This keeps the existing
//     pure-Go build matrix green while the ONNX integration lands,
//     and gives a working shape for the badge / handshake tests.
//
// See TASKS.md at the repo root for the phased plan.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/platform"
	"github.com/WeaponizedLego/kestrel/internal/vision"
)

// version is stamped into /healthz so core can reject a skewed
// sidecar. Keep in lockstep with cmd/kestrel for releases.
const version = "v0.1.0"

// shutdownGrace caps how long the sidecar waits for in-flight
// /detect calls to drain before forcing the HTTP server closed.
const shutdownGrace = 5 * time.Second

func main() {
	bind := flag.String("addr", "127.0.0.1:0", "address to bind (loopback only by default)")
	debug := flag.Bool("debug", false, "enable debug-level logging")
	modelsDir := flag.String("models", "", "directory containing ONNX model files (overrides embedded models)")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if err := run(*bind, *modelsDir); err != nil {
		slog.Error("kestrel-vision exiting", "err", err)
		os.Exit(1)
	}
}

// run wires the server and blocks until SIGINT/SIGTERM.
func run(bind, modelsDir string) error {
	if !strings.HasPrefix(bind, "127.0.0.1:") && !strings.HasPrefix(bind, "localhost:") {
		return fmt.Errorf("refusing to bind non-loopback address %q", bind)
	}

	token, err := newToken()
	if err != nil {
		return fmt.Errorf("generating auth token: %w", err)
	}

	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", bind, err)
	}

	pipeline, err := newPipeline(modelsDir)
	if err != nil {
		return fmt.Errorf("initialising pipeline: %w", err)
	}
	defer pipeline.Close()

	srv := newServer(token, pipeline)

	url := "http://" + listener.Addr().String()
	endpointPath, err := platform.VisionEndpointPath()
	if err != nil {
		return fmt.Errorf("resolving endpoint path: %w", err)
	}
	if err := os.MkdirAll(parentDir(endpointPath), 0o755); err != nil {
		return fmt.Errorf("creating endpoint dir: %w", err)
	}
	if err := vision.WriteEndpoint(endpointPath, url, token); err != nil {
		return fmt.Errorf("writing handshake: %w", err)
	}
	defer func() {
		if err := vision.RemoveEndpoint(endpointPath); err != nil {
			slog.Warn("removing handshake file", "err", err)
		}
	}()

	slog.Info("kestrel-vision listening",
		"url", url,
		"models", pipeline.LoadedModels(),
		"mode", pipeline.Mode(),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		slog.Info("shutdown requested", "signal", sig.String())
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down http server: %w", err)
	}
	slog.Info("kestrel-vision stopped cleanly")
	return nil
}

// newToken returns a short random bearer token. 16 bytes = 128 bits
// of entropy; loopback-only means we only need enough to defeat a
// cross-process guess, not a motivated attacker.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// parentDir returns the directory containing path. A tiny local
// helper so main.go doesn't pull filepath just for one call.
func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == os.PathSeparator || path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

// Context is passed through run so shutdown can carry a timeout.
// Kept as a var (not a build-time import of "context") because the
// surrounding file needs it exactly once — we import context at the
// top for the net/http.Server.Shutdown call.
var _ = context.Canceled
