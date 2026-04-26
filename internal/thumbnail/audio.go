package thumbnail

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// audioExts is the local copy of the scanner's audio extension list.
// Keeping it here means thumbnail does not import scanner.
var audioExts = map[string]struct{}{
	".mp3": {}, ".m4a": {}, ".aac": {}, ".flac": {}, ".wav": {}, ".ogg": {}, ".opus": {},
}

// IsAudioPath reports whether path has a recognised audio extension.
func IsAudioPath(path string) bool {
	_, ok := audioExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// audioCardBackground is a dark, slightly-blue tone that complements
// the daisyUI dark theme palette. Hard-coded as RGBA so the renderer
// has no theme dependency — the goal is one consistent look the
// frontend can rely on regardless of which built-in theme the user
// has selected.
var audioCardBackground = color.RGBA{R: 0x1f, G: 0x24, B: 0x32, A: 0xff}

// audioCardForeground is the filename text colour. Light enough to
// read at thumbnail size against audioCardBackground.
var audioCardForeground = color.RGBA{R: 0xe5, G: 0xe7, B: 0xeb, A: 0xff}

// audioCardBadgeFill is the small rounded badge fill for the format
// chip in the corner. A subtle accent against the background.
var audioCardBadgeFill = color.RGBA{R: 0x37, G: 0x41, B: 0x57, A: 0xff}

// GenerateAudioThumbnail renders a deterministic 256×256 JPEG card
// for an audio file: the filename (without extension) wrapped to the
// available width, plus a small format badge in the top-right with
// the uppercased extension. No audio decoding is required — this
// runs whether or not ffmpeg or fpcalc are installed.
func GenerateAudioThumbnail(path string) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, ThumbSize, ThumbSize))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: audioCardBackground}, image.Point{}, draw.Src)

	base := filepath.Base(path)
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(base), "."))
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	drawFilename(img, stem)
	drawFormatBadge(img, ext)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encoding audio thumbnail for %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// drawFilename wraps stem to fit within the card and centres it
// vertically. basicfont.Face7x13 is a pure-Go bitmap face — adequate
// for a 256×256 card and CGO-free, which keeps cross-compilation
// clean.
func drawFilename(img *image.RGBA, stem string) {
	face := basicfont.Face7x13
	const (
		marginX     = 16
		maxLineW    = ThumbSize - 2*marginX
		maxLines    = 4
		lineSpacing = 4
	)
	lines := wrapText(stem, face, maxLineW, maxLines)
	if len(lines) == 0 {
		return
	}
	advance := face.Metrics().Height.Round() + lineSpacing
	totalH := advance*len(lines) - lineSpacing
	startY := (ThumbSize-totalH)/2 + face.Metrics().Ascent.Round()

	d := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{C: audioCardForeground},
		Face: face,
	}
	for i, line := range lines {
		w := font.MeasureString(face, line).Round()
		x := (ThumbSize - w) / 2
		if x < marginX {
			x = marginX
		}
		d.Dot = fixed.P(x, startY+i*advance)
		d.DrawString(line)
	}
}

// drawFormatBadge paints a small filled rectangle in the top-right
// with the uppercased extension. Empty extensions render nothing —
// the file still has a filename card, just without the badge.
func drawFormatBadge(img *image.RGBA, ext string) {
	if ext == "" {
		return
	}
	face := basicfont.Face7x13
	label := "[" + ext + "]"
	textW := font.MeasureString(face, label).Round()
	textH := face.Metrics().Height.Round()

	const (
		padX   = 6
		padY   = 4
		margin = 10
	)
	w := textW + 2*padX
	h := textH + 2*padY
	x1 := ThumbSize - margin
	y0 := margin
	x0 := x1 - w
	y1 := y0 + h

	rect := image.Rect(x0, y0, x1, y1)
	draw.Draw(img, rect, &image.Uniform{C: audioCardBadgeFill}, image.Point{}, draw.Src)

	d := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{C: audioCardForeground},
		Face: face,
	}
	d.Dot = fixed.P(x0+padX, y0+padY+face.Metrics().Ascent.Round())
	d.DrawString(label)
}

// wrapText breaks s into at most maxLines whose rendered width fits
// within maxWidth pixels for face. Long stems are split on word
// boundaries first, then on character boundaries when a single word
// is wider than maxWidth. Overflow past maxLines is replaced with an
// ellipsis on the final line.
func wrapText(s string, face font.Face, maxWidth, maxLines int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	words := splitWords(s)
	if len(words) == 0 {
		return nil
	}

	var lines []string
	current := ""
	flush := func() {
		if current != "" {
			lines = append(lines, current)
			current = ""
		}
	}
	fits := func(candidate string) bool {
		return font.MeasureString(face, candidate).Round() <= maxWidth
	}

	for _, word := range words {
		// Word longer than the line: split it into character chunks.
		if !fits(word) {
			flush()
			for _, chunk := range splitTooLongWord(word, face, maxWidth) {
				lines = append(lines, chunk)
				if len(lines) >= maxLines {
					return ellipsisize(lines, face, maxWidth, maxLines)
				}
			}
			continue
		}
		candidate := current
		if candidate != "" {
			candidate += " "
		}
		candidate += word
		if fits(candidate) {
			current = candidate
			continue
		}
		flush()
		current = word
		if len(lines) >= maxLines {
			return ellipsisize(lines, face, maxWidth, maxLines)
		}
	}
	flush()
	if len(lines) > maxLines {
		return ellipsisize(lines, face, maxWidth, maxLines)
	}
	return lines
}

// splitWords mirrors strings.Fields but treats "_" and "-" as
// soft-breakable so audio filenames like "track_01_intro" wrap on
// the underscore instead of running off the card.
func splitWords(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '_' || r == '-' {
			if i > start {
				out = append(out, s[start:i+1])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// splitTooLongWord chops word into pieces that each fit within
// maxWidth. Used for words like very long hashes or run-on filenames
// that have no natural break.
func splitTooLongWord(word string, face font.Face, maxWidth int) []string {
	var out []string
	current := ""
	for _, r := range word {
		candidate := current + string(r)
		if font.MeasureString(face, candidate).Round() > maxWidth && current != "" {
			out = append(out, current)
			current = string(r)
			continue
		}
		current = candidate
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

// ellipsisize trims lines to maxLines and replaces the trailing
// characters of the last visible line with an ellipsis if there's
// overflow.
func ellipsisize(lines []string, face font.Face, maxWidth, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	out := lines[:maxLines]
	last := out[maxLines-1]
	const ell = "…"
	for {
		if font.MeasureString(face, last+ell).Round() <= maxWidth {
			out[maxLines-1] = last + ell
			return out
		}
		if last == "" {
			out[maxLines-1] = ell
			return out
		}
		last = last[:len(last)-1]
	}
}
