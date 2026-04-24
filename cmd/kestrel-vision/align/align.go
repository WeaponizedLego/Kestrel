// Package align warps a detected face into ArcFace's canonical
// 112×112 layout so the embedder sees eyes, nose, and mouth in the
// same positions every time. Without alignment, ArcFace's same-
// person cosine similarity drops by ~5% — small enough to miss in
// casual testing, large enough to hurt clustering accuracy.
//
// The algorithm fits a 2D similarity transform (translation +
// rotation + uniform scale) that maps the five detected landmarks
// to the five canonical ArcFace reference landmarks in a least-
// squares sense, then resamples the source image under that
// transform. Pure Go, no CGO, no external matrix library — five
// points is small enough to solve by hand.
package align

import (
	"errors"
	"image"
	"image/color"
)

// ArcFaceSize is the side length of the aligned crop.
const ArcFaceSize = 112

// Landmarks is the 5-point face landmark layout SCRFD emits, in the
// order ArcFace expects: left eye, right eye, nose, left mouth,
// right mouth. Coordinates are in pixels of the original image.
type Landmarks [5]Point

// Point is a 2D coordinate in pixels.
type Point struct{ X, Y float64 }

// arcFaceTemplate is the canonical destination layout ArcFace
// reference preprocessing uses. Values from InsightFace's
// deepinsight/insightface arcface_torch repo, scaled to 112×112.
//
//	left eye:    (38.2946, 51.6963)
//	right eye:   (73.5318, 51.5014)
//	nose:        (56.0252, 71.7366)
//	mouth left:  (41.5493, 92.3655)
//	mouth right: (70.7299, 92.2041)
var arcFaceTemplate = Landmarks{
	{X: 38.2946, Y: 51.6963},
	{X: 73.5318, Y: 51.5014},
	{X: 56.0252, Y: 71.7366},
	{X: 41.5493, Y: 92.3655},
	{X: 70.7299, Y: 92.2041},
}

// ErrDegenerate is returned when the detected landmarks collapse to
// a point (all five near-identical) and no meaningful transform can
// be fit. Caller skips the face.
var ErrDegenerate = errors.New("degenerate landmarks: cannot fit alignment")

// FaceCrop returns a size×size RGBA image containing the face
// aligned to the ArcFace canonical template. Uses bilinear
// resampling under a 2D similarity transform fit by least squares
// over the five landmark correspondences.
func FaceCrop(src image.Image, lm Landmarks, size int) (*image.RGBA, error) {
	// Fit a similarity transform:  [x']   [a -b] [x] + [tx]
	//                              [y'] = [b  a] [y] + [ty]
	// from src landmarks (lm) to destination template (scaled).
	// Solve by centring both sets and computing the closed-form
	// similarity (Umeyama, 1991).
	scale := float64(size) / ArcFaceSize
	dst := scaledTemplate(arcFaceTemplate, scale)

	a, b, tx, ty, err := fitSimilarity(lm, dst)
	if err != nil {
		return nil, err
	}

	// Inverse map destination pixel (u,v) back to source pixel (x,y):
	//   inv = 1/(a² + b²) · [ a  b ]
	//                       [-b  a ]
	// with the negated translation applied in destination space.
	det := a*a + b*b
	if det == 0 {
		return nil, ErrDegenerate
	}
	inv := 1.0 / det
	ia, ib := a*inv, b*inv

	out := image.NewRGBA(image.Rect(0, 0, size, size))
	srcBounds := src.Bounds()
	for v := 0; v < size; v++ {
		for u := 0; u < size; u++ {
			fu := float64(u) - tx
			fv := float64(v) - ty
			x := ia*fu + ib*fv
			y := -ib*fu + ia*fv
			out.Set(u, v, bilinearSample(src, srcBounds, x, y))
		}
	}
	return out, nil
}

// fitSimilarity returns (a, b, tx, ty) such that applying
//
//	x' = a*x - b*y + tx
//	y' = b*x + a*y + ty
//
// to src minimises the squared distance to dst. Closed-form five-
// point least squares — no matrix library needed.
func fitSimilarity(src, dst Landmarks) (a, b, tx, ty float64, err error) {
	const n = 5
	var sxm, sym, dxm, dym float64
	for i := 0; i < n; i++ {
		sxm += src[i].X
		sym += src[i].Y
		dxm += dst[i].X
		dym += dst[i].Y
	}
	sxm /= n
	sym /= n
	dxm /= n
	dym /= n

	var sxx, sxy, syx, syy, srcVar float64
	for i := 0; i < n; i++ {
		sx := src[i].X - sxm
		sy := src[i].Y - sym
		dx := dst[i].X - dxm
		dy := dst[i].Y - dym
		sxx += sx * dx
		syy += sy * dy
		sxy += sx * dy
		syx += sy * dx
		srcVar += sx*sx + sy*sy
	}
	if srcVar == 0 {
		return 0, 0, 0, 0, ErrDegenerate
	}
	a = (sxx + syy) / srcVar
	b = (sxy - syx) / srcVar
	tx = dxm - (a*sxm - b*sym)
	ty = dym - (b*sxm + a*sym)
	return a, b, tx, ty, nil
}

// scaledTemplate returns arcFaceTemplate with every coordinate
// multiplied by scale (for non-112 target sizes).
func scaledTemplate(t Landmarks, scale float64) Landmarks {
	var out Landmarks
	for i, p := range t {
		out[i] = Point{X: p.X * scale, Y: p.Y * scale}
	}
	return out
}

// bilinearSample returns the bilinearly-interpolated pixel at
// (fx, fy) in src. Out-of-bounds samples are clamped to the edge
// so the warped crop doesn't show hard black when alignment pushes
// past the image boundary.
func bilinearSample(src image.Image, b image.Rectangle, fx, fy float64) color.Color {
	x0 := int(fx)
	y0 := int(fy)
	x1 := x0 + 1
	y1 := y0 + 1
	dx := fx - float64(x0)
	dy := fy - float64(y0)

	x0c := clamp(x0, b.Min.X, b.Max.X-1)
	x1c := clamp(x1, b.Min.X, b.Max.X-1)
	y0c := clamp(y0, b.Min.Y, b.Max.Y-1)
	y1c := clamp(y1, b.Min.Y, b.Max.Y-1)

	r00, g00, b00, a00 := src.At(x0c, y0c).RGBA()
	r10, g10, b10, a10 := src.At(x1c, y0c).RGBA()
	r01, g01, b01, a01 := src.At(x0c, y1c).RGBA()
	r11, g11, b11, a11 := src.At(x1c, y1c).RGBA()

	lerp := func(a, b, c, d uint32) uint8 {
		af := float64(a >> 8)
		bf := float64(b >> 8)
		cf := float64(c >> 8)
		df := float64(d >> 8)
		top := af + (bf-af)*dx
		bot := cf + (df-cf)*dx
		return uint8(top + (bot-top)*dy)
	}
	return color.RGBA{
		R: lerp(r00, r10, r01, r11),
		G: lerp(g00, g10, g01, g11),
		B: lerp(b00, b10, b01, b11),
		A: lerp(a00, a10, a01, a11),
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
