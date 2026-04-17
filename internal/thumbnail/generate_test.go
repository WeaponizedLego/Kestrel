package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate_PNGDownscales(t *testing.T) {
	path := writePNG(t, 800, 400)

	data, err := Generate(path)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Decode the result and confirm dimensions fit the thumb box.
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if cfg.Width > ThumbSize || cfg.Height > ThumbSize {
		t.Fatalf("thumbnail %dx%d exceeds ThumbSize=%d", cfg.Width, cfg.Height, ThumbSize)
	}
	// 800x400 → 256x128 after fit-within downscale.
	if cfg.Width != 256 || cfg.Height != 128 {
		t.Fatalf("expected 256x128, got %dx%d", cfg.Width, cfg.Height)
	}
}

func TestGenerate_SmallImageNotUpscaled(t *testing.T) {
	path := writePNG(t, 100, 60)

	data, err := Generate(path)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if cfg.Width != 100 || cfg.Height != 60 {
		t.Fatalf("small image should pass through, got %dx%d", cfg.Width, cfg.Height)
	}
}

func TestGenerate_NonImageReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.jpg")
	if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(path); err == nil {
		t.Fatal("expected an error for non-image bytes")
	}
}

// writePNG encodes a solid-colour image of the given size as a PNG
// file under t.TempDir and returns its absolute path.
func writePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 64, B: 200, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "img.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	return path
}
