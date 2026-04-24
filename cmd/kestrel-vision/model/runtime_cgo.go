//go:build cgo

package model

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// ONNX Runtime has one global environment per process. Every
// NewAdvancedSession call assumes it's been initialized. We reference-
// count so independent Open*() calls and Close()s balance without the
// sidecar having to orchestrate a shared lifecycle.

var (
	envMu      sync.Mutex
	envRefs    int
	envInitErr error
)

// initEnvironment brings up ONNX Runtime if no session is currently
// holding it alive. Locates the shared library via the standard
// platform-specific fallbacks below — users who install the runtime
// in a non-standard place can override with ORT_SHARED_LIBRARY.
func initEnvironment() error {
	envMu.Lock()
	defer envMu.Unlock()

	if envRefs > 0 {
		envRefs++
		return envInitErr
	}

	// SetSharedLibraryPath must happen before InitializeEnvironment.
	// The runtime picks up the chosen path the first time it inits;
	// overriding later is a no-op on some platforms.
	if path := os.Getenv("ORT_SHARED_LIBRARY"); path != "" {
		ort.SetSharedLibraryPath(path)
	} else if path := defaultSharedLibraryPath(); path != "" {
		ort.SetSharedLibraryPath(path)
	}

	envInitErr = ort.InitializeEnvironment()
	if envInitErr != nil {
		envInitErr = fmt.Errorf("initializing ONNX runtime (set ORT_SHARED_LIBRARY to the libonnxruntime path if needed): %w", envInitErr)
		return envInitErr
	}
	envRefs = 1
	return nil
}

// releaseEnvironment drops one reference and tears down the runtime
// when the last session closes. Safe to call redundantly — extra
// releases below zero are ignored.
func releaseEnvironment() {
	envMu.Lock()
	defer envMu.Unlock()
	if envRefs <= 0 {
		return
	}
	envRefs--
	if envRefs == 0 {
		_ = ort.DestroyEnvironment()
	}
}

// defaultSharedLibraryPath returns a best-guess location for the
// ONNX Runtime shared library next to the sidecar binary. This
// matches how we ship it in Phase 3: drop the .so/.dylib/.dll next
// to kestrel-vision and things Just Work.
func defaultSharedLibraryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)

	var name string
	switch runtime.GOOS {
	case "windows":
		name = "onnxruntime.dll"
	case "darwin":
		name = "libonnxruntime.dylib"
	default:
		name = "libonnxruntime.so"
	}
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	// Fall back to letting the OS linker find it via the normal
	// search paths (LD_LIBRARY_PATH, DYLD_LIBRARY_PATH, PATH).
	return ""
}

// resolveModelPath returns modelsDir/name if modelsDir is set and the
// file exists. Returns an error otherwise — Phase 2 will replace this
// with //go:embed, at which point a missing file becomes impossible
// for a released binary.
func resolveModelPath(modelsDir, name string) (string, error) {
	if modelsDir == "" {
		return "", fmt.Errorf("--models dir not set; cannot locate %s (Phase 2 will embed)", name)
	}
	p := filepath.Join(modelsDir, name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("locating %s in %s: %w", name, modelsDir, err)
	}
	return p, nil
}
