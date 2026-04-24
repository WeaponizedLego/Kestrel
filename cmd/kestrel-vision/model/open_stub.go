//go:build !cgo

package model

import "errors"

// ErrNoRuntime is returned by every OpenXxx in CGO-less builds. The
// sidecar's pipeline_stub.go short-circuits before these are called,
// but keeping real implementations here (instead of a panic) means
// any direct call still fails cleanly.
var ErrNoRuntime = errors.New("ONNX Runtime not available in this build (built without CGO)")

func OpenFaceDetector(modelsDir string) (FaceDetector, error) {
	return nil, ErrNoRuntime
}
func OpenFaceEmbedder(modelsDir string) (FaceEmbedder, error) {
	return nil, ErrNoRuntime
}
func OpenObjectDetector(modelsDir string) (ObjectDetector, error) {
	return nil, ErrNoRuntime
}
