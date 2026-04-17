// Package thumbnail turns source images into fixed-size JPEG
// thumbnails and persists them in a packed binary file (thumbs.pack).
// The package is CGO-free: decoding goes through the stdlib plus
// golang.org/x/image for WebP, and resampling uses image/draw.
//
// Generate is a pure function — given a path it returns JPEG bytes —
// so the scanner can call it without any shared state. The on-disk
// format lives in pack.go.
package thumbnail

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// ThumbSize is the longest-edge pixel dimension Generate scales a
// source image down to. 256 lines up with the grid cell size in
// docs/ui-design.md; pick the visual size there and this will follow.
const ThumbSize = 256

// JPEGQuality is the libjpeg quality passed to image/jpeg.Encode. 82
// is a reasonable "looks fine to a human, compresses well" default —
// the grid never renders thumbs at 1:1 so artefacts are invisible.
const JPEGQuality = 82

// Generate reads the image at path, resamples it to fit within
// ThumbSize×ThumbSize while preserving aspect ratio, and returns the
// encoded JPEG bytes. Returns an error for unreadable files or image
// formats the decoders don't recognise — callers treat those as
// best-effort skips (see scanner).
func Generate(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}

	dst := scaleToFit(src, ThumbSize)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encoding thumbnail for %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// scaleToFit returns src resampled so neither dimension exceeds
// maxDim, preserving aspect ratio. Images already at or below the
// target are returned untouched — no point re-encoding a thumbnail
// that's already thumb-sized. CatmullRom gives crisper results than
// bilinear at downscales typical for photo thumbnails.
func scaleToFit(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return src
	}

	var nw, nh int
	if w >= h {
		nw = maxDim
		nh = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		nh = maxDim
		nw = int(float64(w) * float64(maxDim) / float64(h))
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
