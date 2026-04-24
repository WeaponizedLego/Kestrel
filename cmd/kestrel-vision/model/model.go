// Package model is the wrapper layer around the three ONNX sessions
// kestrel-vision runs: SCRFD (face detection), ArcFace (face
// embedding), YOLOv8n (object detection).
//
// The exported surface (interfaces + result structs) is untagged so
// the rest of the sidecar can compile against it without CGO. The
// ONNX-linked implementations live in files tagged //go:build cgo;
// the default-build fallbacks return a clear error so a CGO-less
// binary that somehow reaches newPipeline fails loudly rather than
// silently.
package model

import (
	"image"

	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/align"
)

// FaceBox is one face detection from SCRFD. BBox is in pixel
// coordinates of the original image (un-letterboxed). Landmarks are
// the 5 points (left eye, right eye, nose, left mouth, right mouth)
// that feed align.FaceCrop.
type FaceBox struct {
	BBox      PixelBox
	Landmarks align.Landmarks
	Score     float32
}

// ObjectHit is one object detection from YOLOv8n.
type ObjectHit struct {
	Label string
	Score float32
	BBox  PixelBox
}

// PixelBox is an axis-aligned bounding box in original-image
// pixels. X,Y are the top-left corner; W,H are positive extents.
type PixelBox struct{ X, Y, W, H int }

// FaceDetector runs SCRFD over a decoded image.
type FaceDetector interface {
	Detect(img image.Image) ([]FaceBox, error)
	Name() string
	Close() error
}

// FaceEmbedder runs ArcFace over an already-preprocessed tensor
// (the caller is responsible for alignment + preprocessing).
type FaceEmbedder interface {
	Embed(chwTensor []float32) ([]float32, error)
	Name() string
	Close() error
}

// ObjectDetector runs YOLOv8n over a decoded image.
type ObjectDetector interface {
	Detect(img image.Image) ([]ObjectHit, error)
	Name() string
	Close() error
}
