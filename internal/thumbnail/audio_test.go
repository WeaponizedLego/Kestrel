package thumbnail

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestGenerateAudioThumbnail_Shape(t *testing.T) {
	data, err := GenerateAudioThumbnail("/somewhere/my-track-2026.mp3")
	if err != nil {
		t.Fatalf("GenerateAudioThumbnail: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JPEG bytes")
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != ThumbSize || b.Dy() != ThumbSize {
		t.Fatalf("bounds %dx%d, want %dx%d", b.Dx(), b.Dy(), ThumbSize, ThumbSize)
	}
}

func TestGenerateAudioThumbnail_LongFilename(t *testing.T) {
	// A pathological filename: long, no breaks, no extension. The
	// renderer must not panic and must still produce a 256×256 JPEG.
	data, err := GenerateAudioThumbnail("/" + repeat('a', 200) + ".flac")
	if err != nil {
		t.Fatalf("GenerateAudioThumbnail: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestIsAudioPath(t *testing.T) {
	if !IsAudioPath("/x/y.MP3") {
		t.Fatal("expected MP3 to be audio")
	}
	if IsAudioPath("/x/y.jpg") {
		t.Fatal("jpg should not be audio")
	}
}

func repeat(b byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return string(out)
}
