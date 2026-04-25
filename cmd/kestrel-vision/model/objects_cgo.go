//go:build cgo

package model

import (
	"fmt"
	"image"
	"sync"

	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/nms"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/preprocess"
	ort "github.com/yalue/onnxruntime_go"
)

// yolov8 output layout: [1, 84, 8400].
//
//	84 = 4 bbox coords (cx, cy, w, h in letterboxed pixels) + 80
//	     COCO class probabilities (already sigmoid'd by the exported
//	     model).
//	8400 = 80*80 + 40*40 + 20*20 anchor-free grid cells across the
//	       three feature-map strides (8, 16, 32).
//
// The decode is per-cell: find the argmax class, emit the bbox if
// the score is ≥ yolov8ConfThreshold, then NMS.
const (
	yolov8Channels       = 84
	yolov8NumAnchors     = 8400
	yolov8NumClasses     = 80
	yolov8ConfThreshold  = 0.25
	yolov8IoUThreshold   = 0.45
	yolov8MaxDetections  = 100 // hard cap on returned hits
)

// yolov8Detector owns the ONNX session and the pre-allocated tensors
// it runs inference through.
type yolov8Detector struct {
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	mu      sync.Mutex
}

// OpenObjectDetector loads YOLOv8n. Model bytes come from modelsDir
// when set; otherwise from the embedded copy.
func OpenObjectDetector(modelsDir string) (ObjectDetector, error) {
	data, source, err := resolveModelBytes(modelsDir, "yolov8n.onnx")
	if err != nil {
		return nil, err
	}
	if err := initEnvironment(); err != nil {
		return nil, err
	}

	inputShape := ort.NewShape(1, 3, preprocess.YOLOv8Size, preprocess.YOLOv8Size)
	input, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		releaseEnvironment()
		return nil, fmt.Errorf("allocating yolov8 input tensor: %w", err)
	}
	outputShape := ort.NewShape(1, yolov8Channels, yolov8NumAnchors)
	output, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		input.Destroy()
		releaseEnvironment()
		return nil, fmt.Errorf("allocating yolov8 output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSessionWithONNXData(
		data,
		[]string{"images"},
		[]string{"output0"},
		[]ort.Value{input},
		[]ort.Value{output},
		nil,
	)
	if err != nil {
		input.Destroy()
		output.Destroy()
		releaseEnvironment()
		return nil, fmt.Errorf("creating yolov8 session from %s: %w", source, err)
	}
	return &yolov8Detector{
		session: session,
		input:   input,
		output:  output,
	}, nil
}

// Detect runs YOLOv8n over img and returns the surviving hits after
// threshold + NMS, un-letterboxed to original image coordinates and
// capped at yolov8MaxDetections.
func (d *yolov8Detector) Detect(img image.Image) ([]ObjectHit, error) {
	tensor, geom := preprocess.YOLOv8Input(img)

	d.mu.Lock()
	defer d.mu.Unlock()

	copy(d.input.GetData(), tensor)
	if err := d.session.Run(); err != nil {
		return nil, fmt.Errorf("running yolov8 session: %w", err)
	}

	out := d.output.GetData()
	want := yolov8Channels * yolov8NumAnchors
	if len(out) != want {
		return nil, fmt.Errorf("yolov8 output length = %d, want %d", len(out), want)
	}

	// Decode each anchor cell. Output is channel-major: the 4 bbox
	// coords and 80 class probs for cell `a` live at out[c*N + a]
	// where N = yolov8NumAnchors.
	candidates := make([]cellHit, 0, 256)
	for a := 0; a < yolov8NumAnchors; a++ {
		// Find best class for this anchor.
		bestCls := -1
		bestScore := float32(yolov8ConfThreshold)
		for c := 0; c < yolov8NumClasses; c++ {
			s := out[(4+c)*yolov8NumAnchors+a]
			if s > bestScore {
				bestScore = s
				bestCls = c
			}
		}
		if bestCls < 0 {
			continue
		}
		cx := float64(out[0*yolov8NumAnchors+a])
		cy := float64(out[1*yolov8NumAnchors+a])
		w := float64(out[2*yolov8NumAnchors+a])
		h := float64(out[3*yolov8NumAnchors+a])
		candidates = append(candidates, cellHit{
			bbox:  nms.Box{X: cx - w/2, Y: cy - h/2, W: w, H: h, Score: bestScore, Index: a},
			class: bestCls,
		})
	}

	// NMS independently per class — an NMS that ignores class can
	// suppress a dog bbox sitting on top of a person bbox, which is
	// almost never what we want for tagging.
	byClass := make(map[int][]nms.Box)
	for _, ch := range candidates {
		byClass[ch.class] = append(byClass[ch.class], ch.bbox)
	}
	hits := make([]ObjectHit, 0, len(candidates))
	for cls, boxes := range byClass {
		kept := nms.Apply(boxes, yolov8IoUThreshold)
		for _, b := range kept {
			ix, iy, iw, ih := preprocess.UnLetterbox(b.X, b.Y, b.W, b.H, geom)
			hits = append(hits, ObjectHit{
				Label: cocoLabel(cls),
				Score: b.Score,
				BBox:  PixelBox{X: ix, Y: iy, W: iw, H: ih},
			})
		}
	}

	// Top-K by score so callers never see a flood of low-confidence
	// hits when a busy image produces hundreds of classes.
	if len(hits) > yolov8MaxDetections {
		sortHitsByScoreDesc(hits)
		hits = hits[:yolov8MaxDetections]
	}
	return hits, nil
}

func (d *yolov8Detector) Name() string { return "yolov8n" }

func (d *yolov8Detector) Close() error {
	if d.session == nil {
		return nil
	}
	_ = d.session.Destroy()
	d.session = nil
	if d.input != nil {
		d.input.Destroy()
		d.input = nil
	}
	if d.output != nil {
		d.output.Destroy()
		d.output = nil
	}
	releaseEnvironment()
	return nil
}

// cellHit is a transient per-anchor candidate carrying its class so
// we can do per-class NMS after decoding.
type cellHit struct {
	bbox  nms.Box
	class int
}

// sortHitsByScoreDesc sorts hits in-place by confidence descending.
// Tiny insertion sort is fine — hits is capped at a few hundred
// before the Top-K trim.
func sortHitsByScoreDesc(hits []ObjectHit) {
	for i := 1; i < len(hits); i++ {
		cur := hits[i]
		j := i - 1
		for j >= 0 && hits[j].Score < cur.Score {
			hits[j+1] = hits[j]
			j--
		}
		hits[j+1] = cur
	}
}

// cocoLabels is the index→name map YOLOv8n was trained on. Order
// matches the class index in the output tensor.
var cocoLabels = [80]string{
	"person", "bicycle", "car", "motorcycle", "airplane", "bus", "train",
	"truck", "boat", "traffic light", "fire hydrant", "stop sign",
	"parking meter", "bench", "bird", "cat", "dog", "horse", "sheep",
	"cow", "elephant", "bear", "zebra", "giraffe", "backpack", "umbrella",
	"handbag", "tie", "suitcase", "frisbee", "skis", "snowboard",
	"sports ball", "kite", "baseball bat", "baseball glove", "skateboard",
	"surfboard", "tennis racket", "bottle", "wine glass", "cup", "fork",
	"knife", "spoon", "bowl", "banana", "apple", "sandwich", "orange",
	"broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair",
	"couch", "potted plant", "bed", "dining table", "toilet", "tv",
	"laptop", "mouse", "remote", "keyboard", "cell phone", "microwave",
	"oven", "toaster", "sink", "refrigerator", "book", "clock", "vase",
	"scissors", "teddy bear", "hair drier", "toothbrush",
}

func cocoLabel(i int) string {
	if i < 0 || i >= len(cocoLabels) {
		return "unknown"
	}
	return cocoLabels[i]
}
