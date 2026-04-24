package preprocess

import (
	"image"
	"image/color"
	"testing"
)

// Letterbox of a non-square image should preserve aspect, pad the
// short axis with grey 114, and report offsets the UnLetterbox step
// can use to round-trip a bbox back to original coordinates.
func TestLetterbox_PreservesAspectAndReportsPadding(t *testing.T) {
	src := solidImage(200, 100, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	dst, lb := Letterbox(src, 640)

	if dst.Bounds().Dx() != 640 || dst.Bounds().Dy() != 640 {
		t.Fatalf("dest size = %dx%d, want 640x640", dst.Bounds().Dx(), dst.Bounds().Dy())
	}
	if lb.Scale != 3.2 {
		t.Errorf("scale = %v, want 3.2", lb.Scale)
	}
	// Padding lives on the top/bottom because the longer axis is width.
	if lb.PadX != 0 {
		t.Errorf("padX = %d, want 0", lb.PadX)
	}
	if lb.PadY == 0 {
		t.Error("expected vertical padding on landscape input, got 0")
	}

	// A corner pixel is in the padded region; it must be grey 114.
	c := dst.At(0, 0)
	r, g, b, _ := c.RGBA()
	if r>>8 != 114 || g>>8 != 114 || b>>8 != 114 {
		t.Errorf("pad pixel = %d,%d,%d, want 114,114,114", r>>8, g>>8, b>>8)
	}
}

// A bbox predicted in letterboxed space must round-trip back to the
// original image's coordinate system. This guards against off-by-pad
// regressions that are easy to introduce.
func TestUnLetterbox_RoundTrip(t *testing.T) {
	src := solidImage(200, 100, color.White)
	_, lb := Letterbox(src, 640)

	// A bbox covering pixels (10,10)-(90,60) on the ORIGINAL image
	// maps forward to (10*3.2, 10*3.2+padY)..(90*3.2, 60*3.2+padY) on
	// the letterboxed image. Round-tripping through UnLetterbox must
	// return approximately the original coords.
	origX, origY, origW, origH := 10.0, 10.0, 80.0, 50.0
	lbX := origX * lb.Scale
	lbY := origY*lb.Scale + float64(lb.PadY)
	lbW := origW * lb.Scale
	lbH := origH * lb.Scale

	ix, iy, iw, ih := UnLetterbox(lbX, lbY, lbW, lbH, lb)
	if abs(ix-int(origX)) > 1 || abs(iy-int(origY)) > 1 {
		t.Errorf("topleft round-trip: got (%d,%d), want ~(%d,%d)", ix, iy, int(origX), int(origY))
	}
	if abs(iw-int(origW)) > 1 || abs(ih-int(origH)) > 1 {
		t.Errorf("size round-trip: got (%dx%d), want ~(%dx%d)", iw, ih, int(origW), int(origH))
	}
}

// ArcFaceInput must produce a 3*112*112 float32 tensor with values
// centred around zero — the normalisation is (x-127.5)/127.5, so
// midrange grey (128) lands near 0 and pure black lands near -1.
func TestArcFaceInput_TensorShapeAndNormalisation(t *testing.T) {
	grey := solidImage(ArcFaceSize, ArcFaceSize, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	out := ArcFaceInput(grey)
	want := 3 * ArcFaceSize * ArcFaceSize
	if len(out) != want {
		t.Fatalf("len = %d, want %d", len(out), want)
	}
	// (128 - 127.5) / 127.5 ≈ 0.00392
	for i, v := range out {
		if v < -0.01 || v > 0.01 {
			t.Fatalf("element %d = %f, expected near zero for grey input", i, v)
			break
		}
	}
}

// SCRFD input swaps channels to BGR order. A red-only image should
// therefore have its RED-value content land in the LAST channel of
// the CHW tensor (index 2 in BGR).
func TestSCRFDInput_BGRChannelOrder(t *testing.T) {
	red := solidImage(SCRFDSize, SCRFDSize, color.RGBA{R: 200, G: 0, B: 0, A: 255})
	out, _ := SCRFDInput(red)
	plane := SCRFDSize * SCRFDSize
	// SCRFD normalises as (x-127.5)/128 → red 200 → (200-127.5)/128 ≈ 0.566
	// Channel 2 is R (in BGR order). Channels 0 and 1 (B and G) should
	// be near zero or negative (128 midpoint - 127.5).
	sampleIdx := plane / 2 // middle pixel, probably not in pad region at 640x640 red image
	if out[2*plane+sampleIdx] < 0.4 {
		t.Errorf("channel 2 (R in BGR) sample = %f, expected ~0.56 for red input", out[2*plane+sampleIdx])
	}
	if out[sampleIdx] > 0.1 {
		t.Errorf("channel 0 (B in BGR) sample = %f, expected near zero for red input", out[sampleIdx])
	}
}

// Helpers.

func solidImage(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
