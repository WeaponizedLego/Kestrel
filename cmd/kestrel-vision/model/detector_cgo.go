//go:build cgo

package model

import (
	"fmt"
	"image"
	"sync"

	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/align"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/nms"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/preprocess"
	ort "github.com/yalue/onnxruntime_go"
)

// SCRFD-2.5G outputs 9 tensors — {score, bbox, kps} × 3 feature-map
// strides — each with 2 anchors per cell. Shapes at 640×640 input:
//
//   stride 8:  (1, 12800, 1) score  (1, 12800, 4) bbox  (1, 12800, 10) kps
//   stride 16: (1,  3200, 1)        (1,  3200, 4)        (1,  3200, 10)
//   stride 32: (1,   800, 1)        (1,   800, 4)        (1,   800, 10)
//
// `numAnchorsPerCell = 2` is what produces the ×2 in the element
// counts (H*W*anchors). The anchor decoding below follows the
// official SCRFD reference (InsightFace arcface_torch repo).
const (
	scrfdConfThreshold = 0.5
	scrfdIoUThreshold  = 0.4
	scrfdMaxFaces      = 50
	scrfdAnchorsPerCell = 2
)

// scrfdStride describes one output head. The three stride configs
// are hard-coded because they line up with the model export that
// kestrel-vision ships — swapping to a different SCRFD variant
// (smaller/larger grid) means changing this table.
type scrfdStride struct {
	stride int
	cells  int // grid side (H == W for square input)
}

// scrfdStrides are ordered to match the model's output list:
// the output tensors appear stride-8 first, then 16, then 32.
var scrfdStrides = []scrfdStride{
	{stride: 8, cells: preprocess.SCRFDSize / 8},   // 80×80
	{stride: 16, cells: preprocess.SCRFDSize / 16}, // 40×40
	{stride: 32, cells: preprocess.SCRFDSize / 32}, // 20×20
}

// scrfdDetector owns the ONNX session plus its 1 input and 9 output
// tensors. Pre-allocated so Detect doesn't allocate session-tensors
// per call.
type scrfdDetector struct {
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	outputs []*ort.Tensor[float32] // length 9, grouped {score,bbox,kps} per stride
	mu      sync.Mutex
}

// OpenFaceDetector loads SCRFD-2.5G. Model bytes come from modelsDir
// when set; otherwise from the embedded copy.
func OpenFaceDetector(modelsDir string) (FaceDetector, error) {
	data, source, err := resolveModelBytes(modelsDir, "scrfd_2.5g.onnx")
	if err != nil {
		return nil, err
	}
	if err := initEnvironment(); err != nil {
		return nil, err
	}

	inputShape := ort.NewShape(1, 3, preprocess.SCRFDSize, preprocess.SCRFDSize)
	input, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		releaseEnvironment()
		return nil, fmt.Errorf("allocating scrfd input tensor: %w", err)
	}

	// Allocate the 9 output tensors in the order the SCRFD ONNX
	// export lists them (score_8, bbox_8, kps_8, score_16, ...).
	outputs := make([]*ort.Tensor[float32], 0, 9)
	outputNames := make([]string, 0, 9)
	outputValues := make([]ort.Value, 0, 9)
	for _, s := range scrfdStrides {
		anchors := s.cells * s.cells * scrfdAnchorsPerCell
		score, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(anchors), 1))
		if err != nil {
			cleanupOutputs(outputs, input)
			releaseEnvironment()
			return nil, fmt.Errorf("allocating scrfd score_%d tensor: %w", s.stride, err)
		}
		bbox, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(anchors), 4))
		if err != nil {
			score.Destroy()
			cleanupOutputs(outputs, input)
			releaseEnvironment()
			return nil, fmt.Errorf("allocating scrfd bbox_%d tensor: %w", s.stride, err)
		}
		kps, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(anchors), 10))
		if err != nil {
			score.Destroy()
			bbox.Destroy()
			cleanupOutputs(outputs, input)
			releaseEnvironment()
			return nil, fmt.Errorf("allocating scrfd kps_%d tensor: %w", s.stride, err)
		}
		outputs = append(outputs, score, bbox, kps)
		outputNames = append(outputNames,
			fmt.Sprintf("score_%d", s.stride),
			fmt.Sprintf("bbox_%d", s.stride),
			fmt.Sprintf("kps_%d", s.stride),
		)
		outputValues = append(outputValues, score, bbox, kps)
	}

	session, err := ort.NewAdvancedSessionWithONNXData(
		data,
		[]string{"input.1"},
		outputNames,
		[]ort.Value{input},
		outputValues,
		nil,
	)
	if err != nil {
		cleanupOutputs(outputs, input)
		releaseEnvironment()
		return nil, fmt.Errorf("creating scrfd session from %s: %w", source, err)
	}
	return &scrfdDetector{
		session: session,
		input:   input,
		outputs: outputs,
	}, nil
}

// Detect runs SCRFD over img and returns face boxes with bounding
// boxes + 5 landmarks in original-image pixel coordinates.
func (d *scrfdDetector) Detect(img image.Image) ([]FaceBox, error) {
	tensor, geom := preprocess.SCRFDInput(img)

	d.mu.Lock()
	defer d.mu.Unlock()

	copy(d.input.GetData(), tensor)
	if err := d.session.Run(); err != nil {
		return nil, fmt.Errorf("running scrfd session: %w", err)
	}

	// Decode candidates across all three strides in letterboxed
	// pixel space. NMS afterwards, then un-letterbox.
	type candidate struct {
		box nms.Box
		lm  align.Landmarks
	}
	var candidates []candidate

	for si, s := range scrfdStrides {
		scoreT := d.outputs[si*3+0].GetData()
		bboxT := d.outputs[si*3+1].GetData()
		kpsT := d.outputs[si*3+2].GetData()

		anchors := s.cells * s.cells * scrfdAnchorsPerCell
		if len(scoreT) != anchors || len(bboxT) != anchors*4 || len(kpsT) != anchors*10 {
			return nil, fmt.Errorf("scrfd stride %d output shape mismatch: got score=%d bbox=%d kps=%d",
				s.stride, len(scoreT), len(bboxT), len(kpsT))
		}

		// Iterate grid cells × anchors-per-cell. The cell index
		// order is (row-major, anchor-major): for y in rows, for x
		// in cols, for a in 0..anchorsPerCell.
		i := 0
		for y := 0; y < s.cells; y++ {
			for x := 0; x < s.cells; x++ {
				for a := 0; a < scrfdAnchorsPerCell; a++ {
					score := scoreT[i]
					if score >= scrfdConfThreshold {
						cx := float64(x * s.stride)
						cy := float64(y * s.stride)
						sf := float64(s.stride)

						// Bbox deltas are distances from anchor
						// center to each edge, measured in strides.
						l := float64(bboxT[i*4+0]) * sf
						t := float64(bboxT[i*4+1]) * sf
						r := float64(bboxT[i*4+2]) * sf
						b := float64(bboxT[i*4+3]) * sf

						x1 := cx - l
						y1 := cy - t
						x2 := cx + r
						y2 := cy + b

						// Landmark deltas are also in strides from
						// anchor center — 5 points × 2 coords.
						var lm align.Landmarks
						for k := 0; k < 5; k++ {
							lm[k] = align.Point{
								X: cx + float64(kpsT[i*10+k*2])*sf,
								Y: cy + float64(kpsT[i*10+k*2+1])*sf,
							}
						}

						candidates = append(candidates, candidate{
							box: nms.Box{
								X: x1, Y: y1, W: x2 - x1, H: y2 - y1,
								Score: score, Index: len(candidates),
							},
							lm: lm,
						})
					}
					i++
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// NMS over the combined candidate set. The index on each Box
	// carries the position in `candidates` so we can recover the
	// landmarks after suppression.
	boxes := make([]nms.Box, len(candidates))
	for i, c := range candidates {
		boxes[i] = c.box
	}
	kept := nms.Apply(boxes, scrfdIoUThreshold)
	if len(kept) > scrfdMaxFaces {
		kept = kept[:scrfdMaxFaces]
	}

	faces := make([]FaceBox, 0, len(kept))
	for _, b := range kept {
		orig := candidates[b.Index]
		ix, iy, iw, ih := preprocess.UnLetterbox(b.X, b.Y, b.W, b.H, geom)
		// Un-letterbox landmarks the same way: subtract pad, divide
		// by scale, clamp to image bounds.
		var origLM align.Landmarks
		for k, pt := range orig.lm {
			px, py, _, _ := preprocess.UnLetterbox(pt.X, pt.Y, 1, 1, geom)
			origLM[k] = align.Point{X: float64(px), Y: float64(py)}
		}
		faces = append(faces, FaceBox{
			BBox:      PixelBox{X: ix, Y: iy, W: iw, H: ih},
			Landmarks: origLM,
			Score:     b.Score,
		})
	}
	return faces, nil
}

func (d *scrfdDetector) Name() string { return "scrfd-2.5g" }

func (d *scrfdDetector) Close() error {
	if d.session == nil {
		return nil
	}
	_ = d.session.Destroy()
	d.session = nil
	cleanupOutputs(d.outputs, d.input)
	d.outputs = nil
	d.input = nil
	releaseEnvironment()
	return nil
}

// cleanupOutputs destroys every tensor in outs plus the single input
// tensor. Used by error paths during Open* so half-constructed
// sessions don't leak.
func cleanupOutputs(outs []*ort.Tensor[float32], input *ort.Tensor[float32]) {
	for _, o := range outs {
		if o != nil {
			o.Destroy()
		}
	}
	if input != nil {
		input.Destroy()
	}
}
