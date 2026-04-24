package main

import (
	"context"
	"image"

	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// Pipeline is the seam between the HTTP layer and whichever ML
// backend is compiled in. Two implementations live in this package,
// selected by build tag:
//
//   - pipeline_cgo.go  (//go:build cgo)  — real ONNX Runtime pipeline
//   - pipeline_stub.go (//go:build !cgo) — empty-result fallback
//
// Keeping the interface here (untagged) means the HTTP handlers in
// server.go never care which backend is active; a build tag flip is
// the only mechanical change between the two modes.
type Pipeline interface {
	// Detect runs face + object detection over img and returns the
	// combined result. Implementations must be safe for concurrent
	// use: the server dispatches one Detect per in-flight request.
	Detect(ctx context.Context, img image.Image) (protocol.DetectResponse, error)

	// LoadedModels reports the identifiers of the loaded models for
	// /healthz. Stable strings ("scrfd-2.5g", "arcface-r100",
	// "yolov8n") so the core status badge can render them verbatim.
	LoadedModels() []string

	// Mode is a short label for logs ("onnx", "stub") so a user
	// scanning their terminal can tell at a glance which backend
	// is compiled in.
	Mode() string

	// Close releases any ONNX sessions and runtime handles. Safe to
	// call more than once.
	Close() error
}
