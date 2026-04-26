package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// audioExts mirrors the scanner's audio extension list. Kept as a
// local copy so this package does not import scanner. The two lists
// agree by convention; the scanner is the single gate that decides
// whether a file reaches ExtractAudio at all.
var audioExts = map[string]struct{}{
	".mp3":  {},
	".m4a":  {},
	".aac":  {},
	".flac": {},
	".wav":  {},
	".ogg":  {},
	".opus": {},
}

// IsAudioPath reports whether path has a recognised audio extension.
func IsAudioPath(path string) bool {
	_, ok := audioExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// AudioMeta is the audio analogue of Metadata's image fields. Zero
// values mean "unknown" — a missing ffprobe yields a zero AudioMeta
// rather than an error so the scanner still indexes the file.
type AudioMeta struct {
	Codec       string  // ffprobe codec_name, e.g. "mp3", "flac", "aac"
	DurationSec float64 // 0 = unknown
	BitrateKbps int     // 0 = unknown
	Channels    int     // 0 = unknown
}

// audioFFProbeOutput is the subset of ffprobe's JSON we read for
// audio. Only the first audio stream is requested.
type audioFFProbeOutput struct {
	Streams []struct {
		CodecName  string `json:"codec_name"`
		Channels   int    `json:"channels"`
		Duration   string `json:"duration"`    // seconds, decimal string
		BitRate    string `json:"bit_rate"`    // bps, decimal string
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

// ExtractAudio runs ffprobe against path and returns codec, duration,
// bitrate, and channel count. Missing ffprobe or unreadable files
// yield a zero AudioMeta so the scanner still indexes the file with
// extension-derived autotags only.
func ExtractAudio(path string) AudioMeta {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return AudioMeta{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffprobeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name,channels,duration,bit_rate:format=duration,bit_rate",
		"-of", "json",
		path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return AudioMeta{}
	}

	var out audioFFProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return AudioMeta{}
	}

	var meta AudioMeta
	if len(out.Streams) > 0 {
		s := out.Streams[0]
		meta.Codec = strings.ToLower(s.CodecName)
		meta.Channels = s.Channels
		if d, err := strconv.ParseFloat(s.Duration, 64); err == nil {
			meta.DurationSec = d
		}
		if b, err := strconv.Atoi(s.BitRate); err == nil {
			meta.BitrateKbps = b / 1000
		}
	}
	// Container-level values fill in when the stream-level fields are
	// absent (common for VBR MP3 / Ogg).
	if meta.DurationSec == 0 {
		if d, err := strconv.ParseFloat(out.Format.Duration, 64); err == nil {
			meta.DurationSec = d
		}
	}
	if meta.BitrateKbps == 0 {
		if b, err := strconv.Atoi(out.Format.BitRate); err == nil {
			meta.BitrateKbps = b / 1000
		}
	}
	return meta
}
