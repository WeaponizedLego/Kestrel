//go:build !cgo

package main

import (
	"context"
	"image"
	"log/slog"
	"sync"

	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// stubPipeline is the CGO-less fallback. It exists so the sidecar
// still builds and responds to /detect in build environments without
// ONNX Runtime. The first Detect logs a one-shot warning so a
// developer running the wrong binary sees why nothing is happening.
type stubPipeline struct {
	once sync.Once
}

func newPipeline(_ string) (Pipeline, error) {
	return &stubPipeline{}, nil
}

func (s *stubPipeline) Detect(_ context.Context, _ image.Image) (protocol.DetectResponse, error) {
	s.once.Do(func() {
		slog.Warn("kestrel-vision built without CGO — /detect returns empty results; rebuild with CGO_ENABLED=1 and ONNX Runtime installed to enable inference")
	})
	return protocol.DetectResponse{}, nil
}

func (s *stubPipeline) LoadedModels() []string { return []string{"stub"} }
func (s *stubPipeline) Mode() string           { return "stub" }
func (s *stubPipeline) Close() error           { return nil }
