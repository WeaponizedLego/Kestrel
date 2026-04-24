//go:build cgo

package main

import (
	"context"
	"fmt"
	"image"

	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/align"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/model"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/preprocess"
	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// onnxPipeline is the CGO-enabled implementation. It holds one ONNX
// session per model and runs them serially per request. Concurrency
// is handled by the HTTP server — ONNX Runtime is internally
// thread-safe for Run calls, so we do not serialise across requests.
//
// Construction is split so tests can plug in fakes: newPipeline picks
// real model loaders, but any of the three can be replaced by a stub
// in pipelineFromModels.
type onnxPipeline struct {
	faceDet  model.FaceDetector
	faceEmb  model.FaceEmbedder
	objDet   model.ObjectDetector
	modelIDs []string
}

// newPipeline loads all three models from modelsDir (or from the
// embedded defaults if modelsDir is empty) and returns a ready
// pipeline. Fails loud if any model is missing — partial ML is worse
// than no ML because the user can't tell what they're actually getting.
func newPipeline(modelsDir string) (Pipeline, error) {
	faceDet, err := model.OpenFaceDetector(modelsDir)
	if err != nil {
		return nil, fmt.Errorf("loading face detector: %w", err)
	}
	faceEmb, err := model.OpenFaceEmbedder(modelsDir)
	if err != nil {
		faceDet.Close()
		return nil, fmt.Errorf("loading face embedder: %w", err)
	}
	objDet, err := model.OpenObjectDetector(modelsDir)
	if err != nil {
		faceDet.Close()
		faceEmb.Close()
		return nil, fmt.Errorf("loading object detector: %w", err)
	}
	return &onnxPipeline{
		faceDet: faceDet,
		faceEmb: faceEmb,
		objDet:  objDet,
		modelIDs: []string{
			faceDet.Name(),
			faceEmb.Name(),
			objDet.Name(),
		},
	}, nil
}

// Detect runs the full pipeline over img:
//
//  1. SCRFD — bounding boxes + 5-point landmarks per face.
//  2. Align + crop each face to ArcFace's canonical 112×112 layout.
//  3. ArcFace — L2-normalised 512-d embedding per face.
//  4. YOLOv8n — object class hits over the same source image.
//  5. Marshal into the protocol response.
func (p *onnxPipeline) Detect(ctx context.Context, img image.Image) (protocol.DetectResponse, error) {
	if err := ctx.Err(); err != nil {
		return protocol.DetectResponse{}, err
	}

	faces, err := p.faceDet.Detect(img)
	if err != nil {
		return protocol.DetectResponse{}, fmt.Errorf("face detection: %w", err)
	}

	outFaces := make([]protocol.Face, 0, len(faces))
	for _, f := range faces {
		crop, err := align.FaceCrop(img, f.Landmarks, align.ArcFaceSize)
		if err != nil {
			// An alignment failure on one face shouldn't kill the
			// whole response — skip it, continue with the rest.
			continue
		}
		tensor := preprocess.ArcFaceInput(crop)
		emb, err := p.faceEmb.Embed(tensor)
		if err != nil {
			return protocol.DetectResponse{}, fmt.Errorf("face embedding: %w", err)
		}
		outFaces = append(outFaces, protocol.Face{
			BBox: protocol.BBox{
				X: f.BBox.X, Y: f.BBox.Y, W: f.BBox.W, H: f.BBox.H,
			},
			Confidence: f.Score,
			Embedding:  emb,
		})
	}

	hits, err := p.objDet.Detect(img)
	if err != nil {
		return protocol.DetectResponse{}, fmt.Errorf("object detection: %w", err)
	}
	outObjects := make([]protocol.Object, 0, len(hits))
	for _, h := range hits {
		outObjects = append(outObjects, protocol.Object{
			Label:      h.Label,
			Confidence: h.Score,
			BBox: protocol.BBox{
				X: h.BBox.X, Y: h.BBox.Y, W: h.BBox.W, H: h.BBox.H,
			},
		})
	}

	return protocol.DetectResponse{
		Faces:   outFaces,
		Objects: outObjects,
	}, nil
}

func (p *onnxPipeline) LoadedModels() []string { return p.modelIDs }
func (p *onnxPipeline) Mode() string           { return "onnx" }

func (p *onnxPipeline) Close() error {
	var firstErr error
	if err := p.faceDet.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := p.faceEmb.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := p.objDet.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
