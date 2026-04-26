// Package autotag derives a set of normalized auto-tags for a photo
// from its EXIF metadata, filesystem context, and an optional offline
// geocoder. Tags are lowercase, deduplicated, and stable across scans
// so re-running a scan on the same photo produces the same output.
//
// Design rules:
//
//   - Pure function: Derive has no side effects and no I/O beyond what
//     its inputs already carry. That keeps it trivially testable and
//     safe to call from every scanner worker in parallel.
//   - Best-effort: missing EXIF fields produce fewer tags, never an
//     error. The scanner is allowed to tag a photo with "kind:photo"
//     and nothing else.
//   - No user intent: nothing here writes to the user-facing Tags
//     field. The scanner attaches the result to Photo.AutoTags, which
//     the UI renders distinctly so the user can tell inferred from
//     confirmed.
package autotag

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/metadata"
)

// Options toggles optional derivation sources. Flags default to the
// safe/quiet choice: per-folder tags are noisy for users who already
// have deep hierarchies, so they are off unless opted in.
type Options struct {
	// FolderTags attaches a "folder:<name>" tag for the photo's
	// immediate parent directory. Off by default — folder structure is
	// sometimes meaningful, sometimes noise.
	FolderTags bool

	// Geo, when non-nil, is consulted for GPS-tagged photos to produce
	// "place:<city>" and "country:<iso>" tags. Can be stubbed in tests
	// or left nil when no offline dataset is available.
	Geo GeoLookup
}

// GeoLookup maps decimal-degree coordinates to a human-friendly place
// name plus ISO country code. Implementations load their dataset at
// construction time; Lookup must be safe to call concurrently.
// Return ok=false when the coordinates fall outside the dataset.
type GeoLookup interface {
	Lookup(lat, lon float64) (place string, countryISO string, ok bool)
}

// Derive returns the auto-tag set for a photo. Inputs must already be
// extracted (Meta) or known to the caller (path, opts) — the function
// does no filesystem or parse work of its own.
//
// Output is normalized: lowercase, trimmed, deduplicated, and sorted
// alphabetically. Sorted output means tests don't need order-agnostic
// assertions and two scans over the same file produce byte-identical
// tag slices.
func Derive(path string, meta metadata.Metadata, opts Options) []string {
	out := newTagSet()

	out.add(kindTag(path))

	out.addAll(dateTags(meta.TakenAt))
	out.addAll(cameraTags(meta))
	out.addAll(exposureTags(meta))
	out.add(orientationTag(meta))

	if opts.FolderTags {
		out.add(folderTag(path))
	}

	if meta.GPSValid && opts.Geo != nil {
		if place, country, ok := opts.Geo.Lookup(meta.GPSLat, meta.GPSLon); ok {
			out.add(formatTag("place", place))
			out.add(formatTag("country", country))
		}
	}

	return out.sorted()
}

// videoExts is the set of extensions Kestrel treats as video. The
// scanner doesn't accept them today, but keeping the kind tag here
// lets a future video pipeline drop in without touching consumers of
// the tag stream.
var videoExts = map[string]struct{}{
	".mp4": {}, ".mov": {}, ".m4v": {}, ".avi": {}, ".mkv": {}, ".webm": {},
}

// audioExts mirrors the scanner's audio extension list. Kept here so
// kindTag can classify audio without importing scanner; the two lists
// agree by convention.
var audioExts = map[string]struct{}{
	".mp3": {}, ".m4a": {}, ".aac": {}, ".flac": {}, ".wav": {}, ".ogg": {}, ".opus": {},
}

// kindTag classifies the file as photo, video, or audio by extension.
// Unknown extensions default to photo — the scanner only accepts
// known formats today, so an unknown here means a future caller is
// passing something we haven't classified yet.
func kindTag(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := videoExts[ext]; ok {
		return "kind:video"
	}
	if _, ok := audioExts[ext]; ok {
		return "kind:audio"
	}
	return "kind:photo"
}

// dateTags produces year and month tags from a capture time. Zero
// times are skipped so photos without EXIF don't get "year:0001".
func dateTags(t time.Time) []string {
	if t.IsZero() {
		return nil
	}
	year := fmt.Sprintf("year:%04d", t.Year())
	month := fmt.Sprintf("month:%04d-%02d", t.Year(), int(t.Month()))
	return []string{year, month}
}

// cameraTags emits camera-make, camera-model, and lens tags. Models
// often embed the make ("Canon EOS R5" on a body whose make is
// "Canon"), so we emit both: `camera:canon`, `camera:canon-eos-r5`.
// Duplicates are collapsed by the surrounding tag set.
func cameraTags(m metadata.Metadata) []string {
	var out []string
	if m.CameraMake != "" {
		out = append(out, formatTag("camera", m.CameraMake))
	}
	if m.CameraModel != "" {
		// Prefix the make so "EOS R5" on a Canon body is searchable as
		// canon-eos-r5 rather than just eos-r5. Skipped when the model
		// already starts with the make to avoid canon-canon-eos-r5.
		combined := m.CameraModel
		if m.CameraMake != "" && !strings.HasPrefix(strings.ToLower(combined), strings.ToLower(m.CameraMake)) {
			combined = m.CameraMake + " " + combined
		}
		out = append(out, formatTag("camera", combined))
	}
	if m.LensModel != "" {
		out = append(out, formatTag("lens", m.LensModel))
	}
	return out
}

// exposureTags emits ISO bucket and flash tags. ISO is bucketed (low /
// mid / high) because raw ISO numbers are too granular to be useful
// chips; users filter by vibe ("high-iso night shots"), not exact 1600.
func exposureTags(m metadata.Metadata) []string {
	var out []string
	switch {
	case m.ISO <= 0:
		// Unknown → no tag.
	case m.ISO <= 200:
		out = append(out, "iso:low")
	case m.ISO <= 1600:
		out = append(out, "iso:mid")
	default:
		out = append(out, "iso:high")
	}
	if m.FlashFired {
		out = append(out, "flash:on")
	}
	return out
}

// orientationTag turns the EXIF orientation value (1–8) into a
// human-friendly portrait/landscape tag. Values 5–8 carry a 90° twist,
// which swaps the on-disk aspect ratio; 1–4 don't. Unknown orientation
// (0) falls back to comparing pixel dimensions so photos missing EXIF
// still get a tag. Returns "" only when neither signal is available.
func orientationTag(m metadata.Metadata) string {
	switch m.Orientation {
	case 5, 6, 7, 8:
		return "orientation:portrait"
	case 1, 2, 3, 4:
		return "orientation:landscape"
	}
	if m.Width > 0 && m.Height > 0 {
		if m.Height > m.Width {
			return "orientation:portrait"
		}
		return "orientation:landscape"
	}
	return ""
}

// folderTag derives a single "folder:<name>" tag from the photo's
// immediate parent directory. Opt-in via Options.FolderTags because
// deep hierarchies can spam the tag list with irrelevant chips.
func folderTag(path string) string {
	dir := filepath.Dir(path)
	name := filepath.Base(dir)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	return formatTag("folder", name)
}

// formatTag is the single normalization chokepoint for tag emission.
// It lowercases, trims whitespace, and replaces internal runs of
// whitespace/underscores/slashes with a single dash so multi-word
// values ("Canon EOS R5", "Rome / Lazio") become predictable slugs.
func formatTag(prefix, value string) string {
	slug := strings.ToLower(strings.TrimSpace(value))
	if slug == "" {
		return ""
	}
	// Collapse runs of separators to a single "-" so "canon  eos r5"
	// and "canon eos_r5" both become canon-eos-r5.
	var b strings.Builder
	b.Grow(len(prefix) + 1 + len(slug))
	b.WriteString(prefix)
	b.WriteByte(':')
	dash := false
	for _, r := range slug {
		if r == ' ' || r == '\t' || r == '_' || r == '/' || r == '\\' {
			if !dash {
				b.WriteByte('-')
				dash = true
			}
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	out := b.String()
	// Trim a trailing dash left by collapsing separators at the end.
	return strings.TrimRight(out, "-")
}

// tagSet collects and deduplicates tags while preserving the
// lightweight contract Derive offers (sorted output, no empties).
type tagSet struct {
	m map[string]struct{}
}

func newTagSet() *tagSet { return &tagSet{m: make(map[string]struct{}, 16)} }

func (s *tagSet) add(tag string) {
	if tag == "" {
		return
	}
	s.m[tag] = struct{}{}
}

func (s *tagSet) addAll(tags []string) {
	for _, t := range tags {
		s.add(t)
	}
}

func (s *tagSet) sorted() []string {
	if len(s.m) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.m))
	for t := range s.m {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
