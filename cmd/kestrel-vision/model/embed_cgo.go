//go:build cgo

package model

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// embeddedModels carries the .onnx files scripts/fetch-vision-models.sh
// drops into cmd/kestrel-vision/models/. The embed pattern includes
// README / .gitkeep so the build never fails on an empty directory —
// those are filtered out when we look up a specific model by name.
//
//go:embed models
var embeddedModels embed.FS

// modelSHA pins expected checksums so a corrupted embedded file or
// a wrong-file at the --models path can't silently poison inference.
// Empty string means "don't check" — acceptable during Phase 2
// bootstrapping while we nail down which exact model revisions to
// ship. Fill these in (matching scripts/fetch-vision-models.sh)
// once the upstream URLs are final.
var modelSHA = map[string]string{
	"scrfd_2.5g.onnx":   "",
	"arcface_r100.onnx": "",
	"yolov8n.onnx":      "",
}

// resolveModelBytes returns the raw bytes of a model file, plus a
// short source label for logging. Priority:
//
//  1. modelsDir (flag) — dev override, always wins when set.
//  2. embedded copy — what a released binary uses.
//  3. error — binary built without models and no override given.
//
// Enforces modelSHA[name] when non-empty so build corruption or a
// swapped override file surface loudly.
func resolveModelBytes(modelsDir, name string) ([]byte, string, error) {
	if modelsDir != "" {
		path := filepath.Join(modelsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s from --models dir: %w", name, err)
		}
		if err := verifySHA(name, data, path); err != nil {
			return nil, "", err
		}
		return data, "filesystem:" + path, nil
	}

	data, err := embeddedModels.ReadFile("models/" + name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", fmt.Errorf(
				"no %s available: run scripts/fetch-vision-models.sh before building, "+
					"or pass --models to an already-populated directory", name)
		}
		return nil, "", fmt.Errorf("reading embedded %s: %w", name, err)
	}
	if err := verifySHA(name, data, "embedded"); err != nil {
		return nil, "", err
	}
	return data, "embedded", nil
}

// verifySHA checks data against modelSHA[name]. A blank expected
// hash skips verification and logs the computed one so a dev can
// paste it back into the map once they're happy with a given
// build.
func verifySHA(name string, data []byte, source string) error {
	want, ok := modelSHA[name]
	if !ok {
		return nil
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if want == "" {
		slog.Debug("model sha256 (no expected value set)", "model", name, "source", source, "sha256", got)
		return nil
	}
	if got != want {
		return fmt.Errorf("sha256 mismatch for %s (%s): got %s, want %s",
			name, source, got, want)
	}
	return nil
}
