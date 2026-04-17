package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

func TestScan_FindsSupportedImages(t *testing.T) {
	root := t.TempDir()
	writeImageTree(t, root, []string{
		"a.jpg",
		"b.JPEG",
		"sub/c.png",
		"sub/deep/d.png",
	})
	writeRaw(t, filepath.Join(root, "ignored.txt"), "x")
	writeRaw(t, filepath.Join(root, "no-ext"), "x")

	lib := library.New()
	added, err := Scan(context.Background(), root, lib, Options{})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if added != 4 {
		t.Fatalf("Scan added %d photos, want 4", added)
	}
	photos := lib.AllPhotos()
	if got, want := len(photos), 4; got != want {
		t.Fatalf("library has %d photos, want %d", got, want)
	}
	for _, p := range photos {
		if !isSupportedImage(p.Path) {
			t.Errorf("Scan returned unsupported file: %s", p.Path)
		}
		if p.Name == "" {
			t.Errorf("photo missing Name: %+v", p)
		}
		if p.Hash == "" || len(p.Hash) != 64 {
			t.Errorf("photo missing or malformed Hash: %q", p.Hash)
		}
		if p.Width <= 0 || p.Height <= 0 {
			t.Errorf("photo missing dimensions: %dx%d", p.Width, p.Height)
		}
	}
}

func TestScan_LargeTreeYieldsAllPhotos(t *testing.T) {
	root := t.TempDir()

	const want = 50 // smaller than Phase 1 because each file now hashes + decodes
	names := make([]string, want)
	for i := 0; i < want; i++ {
		names[i] = fmt.Sprintf("dir%d/img%d.jpg", i%8, i)
	}
	writeImageTree(t, root, names)

	lib := library.New()
	added, err := Scan(context.Background(), root, lib, Options{})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if added != want {
		t.Fatalf("Scan added %d photos, want %d", added, want)
	}
	if got := lib.Len(); got != want {
		t.Fatalf("library has %d photos, want %d", got, want)
	}
}

func TestScan_IsAdditiveAcrossRoots(t *testing.T) {
	// Scanning two roots in sequence must accumulate, not replace —
	// this is the multi-root guarantee the docs describe. Rescanning
	// an existing root overwrites by path (map key) without touching
	// unrelated photos.
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeImageTree(t, rootA, []string{"a1.jpg", "a2.jpg"})
	writeImageTree(t, rootB, []string{"b1.jpg", "b2.jpg", "b3.jpg"})

	lib := library.New()

	if _, err := Scan(context.Background(), rootA, lib, Options{}); err != nil {
		t.Fatalf("Scan(rootA): %v", err)
	}
	if got := lib.Len(); got != 2 {
		t.Fatalf("after rootA, lib.Len = %d, want 2", got)
	}

	if _, err := Scan(context.Background(), rootB, lib, Options{}); err != nil {
		t.Fatalf("Scan(rootB): %v", err)
	}
	if got := lib.Len(); got != 5 {
		t.Fatalf("after rootB, lib.Len = %d, want 5 (both roots)", got)
	}

	// Rescanning rootA must not duplicate or wipe rootB's entries.
	added, err := Scan(context.Background(), rootA, lib, Options{})
	if err != nil {
		t.Fatalf("Scan(rootA, rescan): %v", err)
	}
	if added != 2 {
		t.Fatalf("rescan added %d, want 2 (overwrites by path)", added)
	}
	if got := lib.Len(); got != 5 {
		t.Fatalf("after rescan, lib.Len = %d, want 5", got)
	}
}

func TestScan_MissingRoot(t *testing.T) {
	_, err := Scan(context.Background(), filepath.Join(t.TempDir(), "nope"), library.New(), Options{})
	if err == nil {
		t.Fatalf("expected error for missing root")
	}
}

func TestScan_RespectsCancellation(t *testing.T) {
	root := t.TempDir()

	names := make([]string, 100)
	for i := range names {
		names[i] = fmt.Sprintf("img%d.jpg", i)
	}
	writeImageTree(t, root, names)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before starting so the walk should bail out fast

	_, err := Scan(ctx, root, library.New(), Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestIsSupportedImage(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"lowercase jpg", "/x/a.jpg", true},
		{"uppercase JPEG", "/x/a.JPEG", true},
		{"png", "/x/a.png", true},
		{"webp", "/x/a.webp", true},
		{"unsupported", "/x/a.txt", false},
		{"no extension", "/x/a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSupportedImage(tt.path); got != tt.want {
				t.Errorf("isSupportedImage(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// writeImageTree materialises a tiny but real image at every relative
// path under root, choosing the encoder based on the extension.
func writeImageTree(t *testing.T, root string, relPaths []string) {
	t.Helper()
	for _, rel := range relPaths {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, encodeTinyImage(t, rel), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// encodeTinyImage returns a 2×2 PNG or JPEG payload depending on the
// extension on path. Tests that need scan output but not specific pixel
// content can call this for a minimal valid image.
func encodeTinyImage(t *testing.T, path string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	var buf bytes.Buffer
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("encode png: %v", err)
		}
	default:
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatalf("encode jpeg: %v", err)
		}
	}
	return buf.Bytes()
}

// writeRaw drops arbitrary bytes at path so we can confirm the scanner
// ignores files with unsupported extensions.
func writeRaw(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
