package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// videoExts mirrors the scanner's video extension list. Kept as a
// local copy so this package does not import scanner. The two lists
// agree by convention; the scanner is the single gate that decides
// whether a file reaches Extract at all.
var videoExts = map[string]struct{}{
	".mp4":  {},
	".mov":  {},
	".m4v":  {},
	".avi":  {},
	".mkv":  {},
	".webm": {},
}

// isVideoPath reports whether path has a recognised video extension.
func isVideoPath(path string) bool {
	_, ok := videoExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// ffprobeTimeout caps how long a single ffprobe call may run. Probing
// is normally instant; the cap is a guard against pathological files.
const ffprobeTimeout = 15 * time.Second

// ffprobeOutput is the subset of ffprobe's JSON we read. Only the
// first video stream is requested, so streams[] always has at most one
// element.
type ffprobeOutput struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
	Format struct {
		Tags map[string]string `json:"tags"`
	} `json:"format"`
}

// extractVideoMetadata runs ffprobe against path and returns the
// pixel dimensions plus a best-effort capture time pulled from the
// container's "creation_time" tag (when present). Missing ffprobe or
// unreadable files yield a zero Metadata — the caller treats that as
// "video, but we know nothing else", which keeps the photo in the
// library without a hard error.
func extractVideoMetadata(path string) Metadata {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return Metadata{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffprobeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:format_tags=creation_time",
		"-of", "json",
		path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Metadata{}
	}

	var out ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Metadata{}
	}

	var meta Metadata
	if len(out.Streams) > 0 {
		meta.Width = out.Streams[0].Width
		meta.Height = out.Streams[0].Height
	}
	if t, ok := out.Format.Tags["creation_time"]; ok {
		meta.TakenAt = parseCreationTime(t)
	}
	return meta
}

// parseCreationTime accepts the handful of timestamp formats ffprobe
// emits in container "creation_time" tags. Unparseable input yields a
// zero time so the caller drops the year/month auto-tags cleanly.
func parseCreationTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

