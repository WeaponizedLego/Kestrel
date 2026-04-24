//go:build cgo

package model

import (
	"fmt"
	"math"
	"sync"

	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/preprocess"
	ort "github.com/yalue/onnxruntime_go"
)

// arcFaceEmbeddingDim is the output vector length. ArcFace r100
// produces 512-d embeddings across every buffalo_l checkpoint; we
// hard-code it so a malformed model surfaces loudly instead of
// silently returning a wrong-size slice.
const arcFaceEmbeddingDim = 512

// arcfaceEmbedder owns the ONNX session for ArcFace. The session is
// created with a fixed [1, 3, 112, 112] input shape so we can reuse
// the backing tensors across calls — no per-Detect allocation.
type arcfaceEmbedder struct {
	session  *ort.AdvancedSession
	input    *ort.Tensor[float32]
	output   *ort.Tensor[float32]
	mu       sync.Mutex // ort sessions are thread-safe, but we reuse
	                    // the backing tensors, so Embed must serialize.
}

// OpenFaceEmbedder loads ArcFace r100 (buffalo_l) from modelsDir.
func OpenFaceEmbedder(modelsDir string) (FaceEmbedder, error) {
	path, err := resolveModelPath(modelsDir, "arcface_r100.onnx")
	if err != nil {
		return nil, err
	}
	if err := initEnvironment(); err != nil {
		return nil, err
	}

	inputShape := ort.NewShape(1, 3, preprocess.ArcFaceSize, preprocess.ArcFaceSize)
	input, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		releaseEnvironment()
		return nil, fmt.Errorf("allocating arcface input tensor: %w", err)
	}
	outputShape := ort.NewShape(1, arcFaceEmbeddingDim)
	output, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		input.Destroy()
		releaseEnvironment()
		return nil, fmt.Errorf("allocating arcface output tensor: %w", err)
	}

	// Input/output names for ArcFace ONNX exports from InsightFace
	// are conventionally "input.1" / "683", but renames happen. If
	// this load errors, the user's first clue is a meaningful
	// "input not found" message from the runtime — we wrap it so
	// they can see which model failed.
	session, err := ort.NewAdvancedSession(
		path,
		[]string{"input.1"},
		[]string{"683"},
		[]ort.Value{input},
		[]ort.Value{output},
		nil,
	)
	if err != nil {
		input.Destroy()
		output.Destroy()
		releaseEnvironment()
		return nil, fmt.Errorf("creating arcface session from %s: %w", path, err)
	}
	return &arcfaceEmbedder{
		session: session,
		input:   input,
		output:  output,
	}, nil
}

// Embed runs ArcFace over a preprocessed tensor (CHW float32 with
// ArcFace normalisation already applied) and returns an L2-normalised
// 512-d identity vector ready for cosine-similarity clustering.
func (e *arcfaceEmbedder) Embed(chwTensor []float32) ([]float32, error) {
	want := 3 * preprocess.ArcFaceSize * preprocess.ArcFaceSize
	if len(chwTensor) != want {
		return nil, fmt.Errorf("arcface input length = %d, want %d", len(chwTensor), want)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	copy(e.input.GetData(), chwTensor)
	if err := e.session.Run(); err != nil {
		return nil, fmt.Errorf("running arcface session: %w", err)
	}

	raw := e.output.GetData()
	if len(raw) != arcFaceEmbeddingDim {
		return nil, fmt.Errorf("arcface output length = %d, want %d", len(raw), arcFaceEmbeddingDim)
	}

	// L2-normalise so the cluster code can treat dot product as
	// cosine similarity. Copy first — raw aliases the tensor's
	// backing storage which the next Run will overwrite.
	out := make([]float32, arcFaceEmbeddingDim)
	copy(out, raw)
	l2Normalize(out)
	return out, nil
}

func (e *arcfaceEmbedder) Name() string { return "arcface-r100" }

// Close releases the session and backing tensors, then drops the
// environment reference. Safe to call more than once.
func (e *arcfaceEmbedder) Close() error {
	if e.session == nil {
		return nil
	}
	_ = e.session.Destroy()
	e.session = nil
	if e.input != nil {
		e.input.Destroy()
		e.input = nil
	}
	if e.output != nil {
		e.output.Destroy()
		e.output = nil
	}
	releaseEnvironment()
	return nil
}

// l2Normalize rescales v in place so its L2 norm is 1. A zero-norm
// vector (degenerate input) is left untouched — cosine similarity
// against any non-zero vector is then zero, which is the least-bad
// behaviour under a broken embedding.
func l2Normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}
