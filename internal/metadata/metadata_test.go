package metadata

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

func TestExtract_PNGDimensions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "img.png")
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	meta, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Width != 4 || meta.Height != 3 {
		t.Errorf("dims = %dx%d, want 4x3", meta.Width, meta.Height)
	}
	if !meta.TakenAt.IsZero() {
		t.Errorf("expected zero TakenAt for EXIF-less PNG, got %v", meta.TakenAt)
	}
	if meta.CameraMake != "" {
		t.Errorf("expected empty CameraMake for EXIF-less PNG, got %q", meta.CameraMake)
	}
}

func TestExtract_JPEGWithoutExifDoesNotFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.jpg")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	meta, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract should tolerate missing EXIF, got %v", err)
	}
	if meta.Width != 2 || meta.Height != 2 {
		t.Errorf("dims = %dx%d, want 2x2", meta.Width, meta.Height)
	}
}

func TestExtract_NotAnImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.jpg")
	if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Extract(path); err == nil {
		t.Fatalf("expected error for non-image bytes")
	}
}

func TestExtract_MissingFile(t *testing.T) {
	if _, err := Extract(filepath.Join(t.TempDir(), "nope.jpg")); err == nil {
		t.Fatalf("expected error for missing file")
	}
}
