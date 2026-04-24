package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// VideoProvider extracts a single still frame from a video file as a
// decoded image, suitable for handing to the existing thumbnail
// scaling + perceptual-hash pipeline. Implementations must be safe to
// call concurrently — the scanner runs one per worker.
type VideoProvider interface {
	// ExtractFrame returns the frame at atSeconds into path. Decoders
	// are allowed to round to the nearest keyframe.
	ExtractFrame(path string, atSeconds float64) (image.Image, error)

	// Available reports whether the underlying decoder is usable on
	// this host. A false return means ExtractFrame will always fail;
	// callers should substitute a placeholder image instead.
	Available() bool
}

// videoExts mirrors autotag.videoExts. Kept here as a separate copy so
// thumbnail does not depend on metadata/autotag — the two packages
// agree by convention, and the scanner's supportedExts is the single
// gate that decides whether either is invoked.
var videoExts = map[string]struct{}{
	".mp4":  {},
	".mov":  {},
	".m4v":  {},
	".avi":  {},
	".mkv":  {},
	".webm": {},
}

// IsVideoPath reports whether path points to a file extension thumbnail
// treats as video. Comparison is case-insensitive.
func IsVideoPath(path string) bool {
	_, ok := videoExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// ffmpegFrameTimeout caps how long a single frame extraction is
// allowed to run. Most clips finish in well under a second; the
// timeout is here to keep a malformed file from stalling a worker.
const ffmpegFrameTimeout = 30 * time.Second

// FFmpegVideo extracts frames by invoking the system ffmpeg binary.
// It is CGO-free (no library bindings) and therefore stays inside the
// project's pure-Go cross-compile contract.
type FFmpegVideo struct {
	binary string
}

// NewFFmpegVideo returns a VideoProvider backed by an ffmpeg binary
// found in PATH. When ffmpeg is missing the returned provider's
// Available reports false and ExtractFrame returns an error — callers
// fall back to a placeholder.
func NewFFmpegVideo() *FFmpegVideo {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return &FFmpegVideo{}
	}
	return &FFmpegVideo{binary: bin}
}

// Available reports whether ffmpeg was found in PATH.
func (f *FFmpegVideo) Available() bool { return f.binary != "" }

// ExtractFrame seeks to atSeconds and decodes a single frame, returned
// as a Go image.Image. The seek is placed before -i so ffmpeg uses the
// fast keyframe-aware path — exact frame accuracy is not required for
// a thumbnail and the speed difference is large on long files.
func (f *FFmpegVideo) ExtractFrame(path string, atSeconds float64) (image.Image, error) {
	if f.binary == "" {
		return nil, fmt.Errorf("ffmpeg not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegFrameTimeout)
	defer cancel()

	args := []string{
		"-loglevel", "error",
		"-ss", strconv.FormatFloat(atSeconds, 'f', 3, 64),
		"-i", path,
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	}
	cmd := exec.CommandContext(ctx, f.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running ffmpeg for %s: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced no output for %s", path)
	}
	img, err := jpeg.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("decoding ffmpeg frame for %s: %w", path, err)
	}
	return img, nil
}

// noVideo is the default zero-value provider used when no video
// support has been wired in. It is always unavailable and every
// extraction call fails — callers fall back to the placeholder.
type noVideo struct{}

func (noVideo) Available() bool                                  { return false }
func (noVideo) ExtractFrame(string, float64) (image.Image, error) { return nil, fmt.Errorf("no video provider configured") }

// videoPlaceholder builds a small, deterministic stand-in image used
// when frame extraction is impossible (no ffmpeg, malformed file, …).
// Dark slate background with a centered play triangle so the user can
// tell at a glance that a cell holds a video that did not generate a
// real thumbnail.
func videoPlaceholder() image.Image {
	const size = ThumbSize
	bg := color.RGBA{0x1f, 0x24, 0x2c, 0xff}
	fg := color.RGBA{0xc0, 0xc6, 0xd0, 0xff}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, bg)
		}
	}

	// Centered isoceles triangle pointing right. Width tw, height th
	// chosen to occupy roughly the middle third of the cell.
	tw, th := size/3, size/3
	cx, cy := size/2, size/2
	left := cx - tw/2
	top := cy - th/2
	for dy := 0; dy < th; dy++ {
		// Triangle taper: at the vertical centre the row is widest;
		// at the top and bottom the row shrinks to a single pixel.
		half := dy
		if dy > th/2 {
			half = th - dy
		}
		row := top + dy
		for dx := 0; dx < half; dx++ {
			img.Set(left+dx, row, fg)
		}
	}
	return img
}
