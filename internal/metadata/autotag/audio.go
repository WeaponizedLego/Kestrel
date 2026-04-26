package autotag

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/metadata"
)

// DeriveAudio returns the auto-tag set for an audio file. Inputs are
// the file path (used for codec fallback when ffprobe is missing),
// the file's mtime (audio has no EXIF capture time, so mtime is the
// best year:YYYY signal we have), and the AudioMeta extracted by
// metadata.ExtractAudio.
//
// Output follows the same normalization contract as Derive:
// lowercase, trimmed, deduplicated, sorted alphabetically. Missing
// inputs produce fewer tags, never an error.
func DeriveAudio(path string, mtime time.Time, am metadata.AudioMeta) []string {
	out := newTagSet()

	out.add("kind:audio")
	out.add(audioCodecTag(path, am.Codec))
	out.add(audioDurationBucket(am.DurationSec))
	out.add(audioBitrateBucket(am.BitrateKbps))
	out.add(audioChannelsTag(am.Channels))
	out.add(audioYearTag(mtime))

	return out.sorted()
}

// audioCodecTag emits codec:<name>. ffprobe is the source of truth
// when it ran; otherwise the file extension is used as a fallback so
// machines without ffprobe still tag the codec.
func audioCodecTag(path, ffprobeCodec string) string {
	codec := strings.ToLower(strings.TrimSpace(ffprobeCodec))
	if codec == "" {
		ext := strings.ToLower(filepath.Ext(path))
		codec = strings.TrimPrefix(ext, ".")
	}
	if codec == "" {
		return ""
	}
	return formatTag("codec", codec)
}

// audioDurationBucket emits duration:short|medium|long. Boundaries:
//   - short  : < 5 minutes
//   - medium : 5–30 minutes
//   - long   : > 30 minutes
//
// A zero duration means "unknown" and yields no tag.
func audioDurationBucket(sec float64) string {
	if sec <= 0 {
		return ""
	}
	switch {
	case sec < 5*60:
		return "duration:short"
	case sec <= 30*60:
		return "duration:medium"
	default:
		return "duration:long"
	}
}

// audioBitrateBucket emits bitrate:low|mid|high. Boundaries:
//   - low  : < 128 kbps
//   - mid  : 128–256 kbps
//   - high : > 256 kbps
//
// A zero bitrate means "unknown" and yields no tag.
func audioBitrateBucket(kbps int) string {
	if kbps <= 0 {
		return ""
	}
	switch {
	case kbps < 128:
		return "bitrate:low"
	case kbps <= 256:
		return "bitrate:mid"
	default:
		return "bitrate:high"
	}
}

// audioChannelsTag emits channels:mono|stereo|surround. Zero or
// negative channel counts yield no tag.
func audioChannelsTag(ch int) string {
	switch {
	case ch <= 0:
		return ""
	case ch == 1:
		return "channels:mono"
	case ch == 2:
		return "channels:stereo"
	default:
		return "channels:surround"
	}
}

// audioYearTag derives a year:YYYY tag from the file's mtime. Zero
// times yield no tag (defensive — os.Stat normally returns non-zero).
func audioYearTag(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("year:%04d", t.Year())
}
