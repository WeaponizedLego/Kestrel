package align

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// The fit should produce an identity-like transform (a≈1, b≈0,
// tx≈0, ty≈0) when the src landmarks are already exactly the
// canonical template. Regression test against a sign-flip in the
// similarity fit.
func TestFitSimilarity_IdentityOnTemplate(t *testing.T) {
	a, b, tx, ty, err := fitSimilarity(arcFaceTemplate, arcFaceTemplate)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if math.Abs(a-1) > 1e-6 {
		t.Errorf("a = %f, want ~1", a)
	}
	if math.Abs(b) > 1e-6 {
		t.Errorf("b = %f, want ~0", b)
	}
	if math.Abs(tx) > 1e-6 || math.Abs(ty) > 1e-6 {
		t.Errorf("translation = (%f,%f), want ~(0,0)", tx, ty)
	}
}

// Scaling the src landmarks by k must produce a = 1/k (the inverse
// — the fit maps src→dst, so a 2× src needs a 0.5× scale to hit dst).
func TestFitSimilarity_RecoversScale(t *testing.T) {
	var scaled Landmarks
	const k = 2.0
	for i, p := range arcFaceTemplate {
		scaled[i] = Point{X: p.X * k, Y: p.Y * k}
	}
	a, b, _, _, err := fitSimilarity(scaled, arcFaceTemplate)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	// Expected: similarity scale = 1/k = 0.5, rotation ≈ 0.
	if math.Abs(a-1.0/k) > 1e-6 {
		t.Errorf("a = %f, want ~%f", a, 1.0/k)
	}
	if math.Abs(b) > 1e-6 {
		t.Errorf("b = %f, want ~0 (no rotation)", b)
	}
}

// When all five landmarks collapse to one point the fit has no
// meaningful solution — we return ErrDegenerate rather than NaN-ing
// downstream.
func TestFitSimilarity_DegenerateInput(t *testing.T) {
	var same Landmarks
	for i := range same {
		same[i] = Point{X: 100, Y: 100}
	}
	_, _, _, _, err := fitSimilarity(same, arcFaceTemplate)
	if err == nil {
		t.Fatal("expected error for degenerate landmarks, got nil")
	}
}

// FaceCrop happy path: give it a synthetic image with coloured
// reference pixels at the canonical ArcFace landmark positions on a
// 224×224 image, and assert the aligned crop has those pixel colours
// near the canonical 112×112 positions. This catches bugs in the
// inverse mapping (off-by-one, wrong sign).
func TestFaceCrop_AlignedColorsLandOnTemplate(t *testing.T) {
	const srcSize = 224
	// Reference landmarks at 2× ArcFace template so we'd expect an
	// identity-ish rescale-by-half transform.
	var srcLM Landmarks
	for i, p := range arcFaceTemplate {
		srcLM[i] = Point{X: p.X * 2, Y: p.Y * 2}
	}

	src := image.NewRGBA(image.Rect(0, 0, srcSize, srcSize))
	// Fill with grey so un-touched pixels are easy to identify.
	grey := color.RGBA{R: 128, G: 128, B: 128, A: 255}
	for y := 0; y < srcSize; y++ {
		for x := 0; x < srcSize; x++ {
			src.Set(x, y, grey)
		}
	}
	// Paint a distinctive red dot at each source landmark position.
	for _, p := range srcLM {
		for dy := -2; dy <= 2; dy++ {
			for dx := -2; dx <= 2; dx++ {
				src.Set(int(p.X)+dx, int(p.Y)+dy, color.RGBA{R: 255, A: 255})
			}
		}
	}

	out, err := FaceCrop(src, srcLM, ArcFaceSize)
	if err != nil {
		t.Fatalf("FaceCrop: %v", err)
	}
	if out.Bounds().Dx() != ArcFaceSize || out.Bounds().Dy() != ArcFaceSize {
		t.Fatalf("crop size = %v, want %dx%d", out.Bounds(), ArcFaceSize, ArcFaceSize)
	}
	// Each canonical landmark pixel should be dominated by red.
	for i, p := range arcFaceTemplate {
		c := out.At(int(p.X), int(p.Y))
		r, g, b, _ := c.RGBA()
		if r>>8 < 180 {
			t.Errorf("landmark %d at (%d,%d): R=%d (expected red dominant, G=%d, B=%d)",
				i, int(p.X), int(p.Y), r>>8, g>>8, b>>8)
		}
	}
}
