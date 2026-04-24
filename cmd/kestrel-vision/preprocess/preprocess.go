// Package preprocess turns a decoded image.Image into the float32
// tensors each ONNX model expects. Every model has its own contract
// (size, normalisation, channel order); one function per model keeps
// the contracts explicit and testable without needing ONNX Runtime.
//
// All outputs are CHW float32 slices in row-major order — that's
// what ONNX Runtime wants for the standard "N×C×H×W" input layout
// with N=1. The caller adds the batch dimension when allocating the
// session input tensor.
package preprocess

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
)

// SCRFDSize is the input side length SCRFD-2.5G expects (640×640).
// Image is letterboxed — resized to fit preserving aspect, padded
// with grey — so bboxes map back cleanly.
const SCRFDSize = 640

// YOLOv8Size is YOLOv8n's input side length.
const YOLOv8Size = 640

// ArcFaceSize is the crop size ArcFace expects. Aligned face crops
// come out of align.FaceCrop at this resolution.
const ArcFaceSize = 112

// LetterboxResult records the geometry used by a letterbox so the
// caller can map model-output bboxes back to original-image pixels.
type LetterboxResult struct {
	Scale     float64 // source→dest scale factor
	PadX, PadY int    // padding applied on top-left in dest pixels
	OrigW     int
	OrigH     int
	DestSize  int
}

// Letterbox resizes src so the longer side is destSize while
// preserving aspect, then pads with grey (114) to a destSize×destSize
// square. The 114 constant matches YOLO reference preprocessing —
// same value works for SCRFD.
//
// Returns the padded image and a LetterboxResult describing the
// transform, so bbox output can be un-letterboxed after inference.
func Letterbox(src image.Image, destSize int) (*image.RGBA, LetterboxResult) {
	b := src.Bounds()
	origW, origH := b.Dx(), b.Dy()

	scale := float64(destSize) / float64(maxInt(origW, origH))
	newW := int(float64(origW) * scale)
	newH := int(float64(origH) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, destSize, destSize))
	// Fill with grey 114 so padding pixels don't bias the model.
	grey := color.RGBA{R: 114, G: 114, B: 114, A: 255}
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: grey}, image.Point{}, draw.Src)

	padX := (destSize - newW) / 2
	padY := (destSize - newH) / 2
	target := image.Rect(padX, padY, padX+newW, padY+newH)
	draw.CatmullRom.Scale(dst, target, src, b, draw.Over, nil)

	return dst, LetterboxResult{
		Scale:    scale,
		PadX:     padX,
		PadY:     padY,
		OrigW:    origW,
		OrigH:    origH,
		DestSize: destSize,
	}
}

// UnLetterbox maps a bbox from model-output letterboxed coordinates
// back to original-image pixel coordinates. Outputs are clipped to
// the original image bounds so a slightly-off model prediction can't
// produce negative dimensions downstream.
func UnLetterbox(x, y, w, h float64, lb LetterboxResult) (ix, iy, iw, ih int) {
	fx := (x - float64(lb.PadX)) / lb.Scale
	fy := (y - float64(lb.PadY)) / lb.Scale
	fw := w / lb.Scale
	fh := h / lb.Scale

	ix = clamp(int(fx), 0, lb.OrigW-1)
	iy = clamp(int(fy), 0, lb.OrigH-1)
	iw = clamp(int(fw), 1, lb.OrigW-ix)
	ih = clamp(int(fh), 1, lb.OrigH-iy)
	return
}

// SCRFDInput produces the tensor SCRFD expects: BGR, normalised as
// (x-127.5)/128, CHW float32, shape [3, 640, 640]. Returns the
// geometry alongside the tensor so the caller can un-letterbox
// detection bboxes back to original image coordinates.
func SCRFDInput(src image.Image) ([]float32, LetterboxResult) {
	img, geom := Letterbox(src, SCRFDSize)
	return imageToCHW(img, orderBGR, -127.5, 1.0/128.0), geom
}

// YOLOv8Input produces the tensor YOLOv8 expects: RGB, normalised as
// x/255, CHW float32, shape [3, 640, 640]. Returns the geometry so
// detections can be un-letterboxed to original coordinates.
func YOLOv8Input(src image.Image) ([]float32, LetterboxResult) {
	img, lb := Letterbox(src, YOLOv8Size)
	return imageToCHW(img, orderRGB, 0, 1.0/255.0), lb
}

// ArcFaceInput produces the tensor ArcFace expects: already-aligned
// 112×112 RGB face, normalised as (x-127.5)/127.5, CHW float32.
// The caller must supply an aligned crop — this function does NOT
// do alignment; use align.FaceCrop first.
func ArcFaceInput(alignedFace image.Image) []float32 {
	return imageToCHW(alignedFace, orderRGB, -127.5, 1.0/127.5)
}

// channelOrder selects which byte layout imageToCHW writes.
type channelOrder uint8

const (
	orderRGB channelOrder = iota
	orderBGR
)

// imageToCHW walks img in scan order, applies the per-channel
// normalisation (pixel+offset)*scale, and writes CHW float32 output.
// Keeps the hot loop tight: one At call per pixel, no per-channel
// conditional inside the inner loop.
func imageToCHW(src image.Image, order channelOrder, offset, scale float32) []float32 {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]float32, 3*w*h)
	// CHW means channel 0 occupies [0, w*h), channel 1 [w*h, 2*w*h), etc.
	plane := w * h
	var c0Off, c1Off, c2Off int
	switch order {
	case orderRGB:
		c0Off, c1Off, c2Off = 0, plane, 2*plane
	case orderBGR:
		c0Off, c1Off, c2Off = 2*plane, plane, 0
	}

	idx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// RGBA() returns 16-bit; shift to 0..255.
			rf := float32(r>>8)
			gf := float32(g >> 8)
			bf := float32(bl >> 8)
			out[c0Off+idx] = (rf + offset) * scale
			out[c1Off+idx] = (gf + offset) * scale
			out[c2Off+idx] = (bf + offset) * scale
			idx++
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
