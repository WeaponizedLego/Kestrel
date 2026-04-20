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
//
// Kept for callers that only need the bytes; GenerateWithHash is the
// richer entry point used by the scanner to also capture a perceptual
// hash off the same decoded image.
func Generate(path string) ([]byte, error) {
	data, _, err := GenerateWithHash(path)
	return data, err
}

// GenerateWithHash does the same work as Generate and, in the same
// pass over the decoded image, computes a 64-bit difference-hash
// (dHash) suitable for near-duplicate / visually-similar clustering
// (see internal/library/cluster). Returning both values from one call
// avoids decoding the image twice during a scan.
//
// A zero hash means "not computed" (e.g. extremely small input); the
// caller treats it as absent, the same as a freshly-loaded v1 Photo.
func GenerateWithHash(path string) ([]byte, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return nil, 0, fmt.Errorf("decoding %s: %w", path, err)
	}

	dst := scaleToFit(src, ThumbSize)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, 0, fmt.Errorf("encoding thumbnail for %s: %w", path, err)
	}

	hash := perceptualHash(src)
	return buf.Bytes(), hash, nil
}

// dHashWidth and dHashHeight are the resample dimensions for the
// difference-hash. 9×8 gives 8 horizontal differences per row × 8
// rows = 64 bits. Well-understood in the image-hashing literature —
// see Neal Krawetz, "Kind of Like That", 2013.
const (
	dHashWidth  = 9
	dHashHeight = 8
)

// perceptualHash computes a 64-bit dHash of src. The algorithm:
//
//  1. Downscale to dHashWidth × dHashHeight with bilinear filtering.
//  2. Convert to grayscale luminance.
//  3. For each of 8 rows, compare adjacent horizontal pixels: bit=1
//     if left < right, else 0. That yields 8×8 = 64 bits of
//     "brightness is increasing here" signal.
//
// dHash (vs. aHash or pHash-via-DCT) is used because it's robust to
// global brightness shifts, survives JPEG re-encoding cleanly, and
// needs no FFT/DCT machinery — a useful property for a pure-Go,
// CGO-free build.
func perceptualHash(src image.Image) uint64 {
	small := image.NewRGBA(image.Rect(0, 0, dHashWidth, dHashHeight))
	draw.BiLinear.Scale(small, small.Bounds(), src, src.Bounds(), draw.Over, nil)

	var lumen [dHashHeight][dHashWidth]uint32
	for y := 0; y < dHashHeight; y++ {
		for x := 0; x < dHashWidth; x++ {
			r, g, b, _ := small.At(x, y).RGBA()
			// Rec. 601 luma weights; the 0..0xFFFF → 0..255 shift keeps
			// the comparison in a tight range without allocating floats.
			lumen[y][x] = (299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000
		}
	}

	var hash uint64
	var bit uint = 63
	for y := 0; y < dHashHeight; y++ {
		for x := 0; x < dHashWidth-1; x++ {
			if lumen[y][x] < lumen[y][x+1] {
				hash |= 1 << bit
			}
			bit--
		}
	}
	return hash
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
