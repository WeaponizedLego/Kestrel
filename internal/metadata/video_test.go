package metadata

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExtract_VideoWithFFprobe_Smoke(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH")
	}
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command("ffmpeg",
		"-y",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=2:size=640x360:rate=10",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		clip,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("synth video failed: %v", err)
	}

	meta, err := Extract(clip)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Width != 640 || meta.Height != 360 {
		t.Errorf("dims %dx%d, want 640x360", meta.Width, meta.Height)
	}
}

func TestExtract_VideoWithoutFFprobe(t *testing.T) {
	// Even on a host without ffprobe (or for an unreadable file), a
	// video must yield zero Metadata, no error — the scanner relies on
	// this to keep the photo in the library regardless of host setup.
	meta, err := Extract("/nonexistent/path/clip.mp4")
	if err != nil {
		t.Fatalf("Extract returned error for missing video: %v", err)
	}
	if meta.Width != 0 || meta.Height != 0 || !meta.TakenAt.IsZero() {
		t.Errorf("expected zero Metadata, got %+v", meta)
	}
}

func TestParseCreationTime(t *testing.T) {
	tests := []struct {
		raw    string
		wantOK bool
	}{
		{"2024-06-15T12:34:56.000000Z", true},
		{"2024-06-15T12:34:56Z", true},
		{"2024-06-15 12:34:56", true},
		{"", false},
		{"not a timestamp", false},
	}
	for _, tt := range tests {
		got := parseCreationTime(tt.raw)
		if tt.wantOK && got.IsZero() {
			t.Errorf("parseCreationTime(%q) returned zero time", tt.raw)
		}
		if !tt.wantOK && !got.IsZero() {
			t.Errorf("parseCreationTime(%q) = %v, want zero", tt.raw, got)
		}
	}
}
