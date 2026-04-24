package thumbnail

import (
	"bytes"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsVideoPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/x/clip.mp4", true},
		{"/x/clip.MOV", true},
		{"/x/clip.mkv", true},
		{"/x/clip.webm", true},
		{"/x/photo.jpg", false},
		{"/x/photo.PNG", false},
		{"/x/no-ext", false},
	}
	for _, tt := range tests {
		if got := IsVideoPath(tt.path); got != tt.want {
			t.Errorf("IsVideoPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestNewMediaThumbnailer_VideoFallsBackToPlaceholder(t *testing.T) {
	// nil provider → noVideo, which is always unavailable. The
	// thumbnailer must yield placeholder bytes (zero hash) without
	// returning an error so the scanner still indexes the file.
	thumb := NewMediaThumbnailer(nil)

	data, hash, err := thumb(filepath.Join(t.TempDir(), "missing.mp4"))
	if err != nil {
		t.Fatalf("thumbnailer returned error: %v", err)
	}
	if hash != 0 {
		t.Errorf("placeholder hash = %d, want 0", hash)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("placeholder is not valid JPEG: %v", err)
	}
	if cfg.Width != ThumbSize || cfg.Height != ThumbSize {
		t.Errorf("placeholder dims %dx%d, want %dx%d", cfg.Width, cfg.Height, ThumbSize, ThumbSize)
	}
}

func TestNewMediaThumbnailer_ImagePathUnchanged(t *testing.T) {
	// An image extension must go through the existing decode path —
	// the video provider should not be touched.
	path := writePNG(t, 100, 100)
	thumb := NewMediaThumbnailer(nil)

	data, _, err := thumb(path)
	if err != nil {
		t.Fatalf("image thumbnail: %v", err)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("not valid JPEG: %v", err)
	}
}

func TestFFmpegVideo_ExtractFrame_Smoke(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	clip := writeTestClip(t)
	v := NewFFmpegVideo()
	if !v.Available() {
		t.Fatal("Available() = false despite ffmpeg on PATH")
	}
	img, err := v.ExtractFrame(clip, 0.5)
	if err != nil {
		t.Fatalf("ExtractFrame: %v", err)
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatalf("frame bounds %v have zero dimension", b)
	}
}

func TestNewMediaThumbnailer_VideoWithFFmpeg_Smoke(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	clip := writeTestClip(t)
	thumb := NewMediaThumbnailer(NewFFmpegVideo())

	data, hash, err := thumb(clip)
	if err != nil {
		t.Fatalf("thumbnailer: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not valid JPEG: %v", err)
	}
	if cfg.Width > ThumbSize || cfg.Height > ThumbSize {
		t.Errorf("thumb %dx%d exceeds ThumbSize=%d", cfg.Width, cfg.Height, ThumbSize)
	}
	if hash == 0 {
		t.Error("hash is zero — looks like the placeholder path was taken instead of a real frame")
	}
}

// writeTestClip generates a tiny 2-second 320x240 H.264 clip via the
// host ffmpeg's lavfi testsrc. Returns the file path. The caller is
// expected to have already gated on ffmpeg availability.
func writeTestClip(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command("ffmpeg",
		"-y",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=2:size=320x240:rate=10",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		out,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("synth video failed: %v", err)
	}
	return out
}

func TestFFmpegVideo_AvailableMatchesLookup(t *testing.T) {
	// The constructor must not panic when ffmpeg is missing; it just
	// reports unavailable. We don't assert which one is true on the
	// host, only that the contract holds.
	v := NewFFmpegVideo()
	if v.binary == "" && v.Available() {
		t.Fatal("Available() returned true with empty binary")
	}
	if v.binary != "" && !v.Available() {
		t.Fatal("Available() returned false despite binary path being set")
	}
}
