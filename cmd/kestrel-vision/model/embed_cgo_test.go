//go:build cgo

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveModelBytes prefers the filesystem when modelsDir is set, so
// a dev can point --models at an override without rebuilding. This
// verifies that precedence: a filesystem file shadows whatever the
// embed FS may or may not contain.
func TestResolveModelBytes_FilesystemWinsOverEmbedded(t *testing.T) {
	dir := t.TempDir()
	want := []byte("filesystem-override")
	if err := os.WriteFile(filepath.Join(dir, "arcface_r100.onnx"), want, 0o600); err != nil {
		t.Fatalf("writing override: %v", err)
	}

	got, source, err := resolveModelBytes(dir, "arcface_r100.onnx")
	if err != nil {
		t.Fatalf("resolveModelBytes: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("bytes = %q, want %q", got, want)
	}
	if !strings.HasPrefix(source, "filesystem:") {
		t.Errorf("source = %q, want filesystem:* prefix", source)
	}
}

// A blank modelsDir and no embedded copy must fail with a message
// that points the user at the fetch script — this is the default
// first-run condition before scripts/fetch-vision-models.sh runs,
// and the error is the only signal they'll have.
func TestResolveModelBytes_MissingEverywhere(t *testing.T) {
	_, _, err := resolveModelBytes("", "arcface_r100.onnx")
	if err == nil {
		t.Fatal("expected error when no models available")
	}
	if !strings.Contains(err.Error(), "fetch-vision-models.sh") {
		t.Errorf("error = %q, want hint about fetch script", err.Error())
	}
}

// verifySHA treats an unpopulated expected hash as "skip check":
// Phase 2 ships with placeholder empty strings for the three
// models so early builds don't reject every load.
func TestVerifySHA_EmptyExpectedSkips(t *testing.T) {
	modelSHA["arcface_r100.onnx"] = ""
	if err := verifySHA("arcface_r100.onnx", []byte("anything"), "test"); err != nil {
		t.Errorf("expected no error with blank expected sha, got %v", err)
	}
}

// A mismatch between actual and expected must fail — this is the
// whole reason the map exists, to catch corruption.
func TestVerifySHA_MismatchFails(t *testing.T) {
	data := []byte("hello")
	sum := sha256.Sum256(data)
	// Stash and restore the map so the test doesn't bleed into other
	// tests that rely on the default empty entry.
	prev := modelSHA["arcface_r100.onnx"]
	defer func() { modelSHA["arcface_r100.onnx"] = prev }()
	modelSHA["arcface_r100.onnx"] = hex.EncodeToString(sum[:])

	if err := verifySHA("arcface_r100.onnx", []byte("different"), "test"); err == nil {
		t.Error("expected sha mismatch error, got nil")
	}
}

// An unknown model name (not in modelSHA) skips verification —
// callers that add a new model are expected to add it to the map
// too, but a missing entry shouldn't crash inference.
func TestVerifySHA_UnknownModelSkips(t *testing.T) {
	if err := verifySHA("brand-new-model.onnx", []byte("x"), "test"); err != nil {
		t.Errorf("unknown model should skip, got %v", err)
	}
}
