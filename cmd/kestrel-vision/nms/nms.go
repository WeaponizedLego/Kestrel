// Package nms implements non-maximum suppression over detection
// boxes. Both SCRFD and YOLOv8 produce many overlapping candidates
// for the same real-world object; NMS keeps the highest-confidence
// one and suppresses its neighbours.
//
// Pure-Go, no CGO, testable without any model weights.
package nms

import "sort"

// Box is the input/output shape. X,Y,W,H are in whatever coordinate
// system the caller cares about (letterboxed, original, …) — NMS
// only looks at overlaps. Index carries caller context (which face
// landmarks to keep, which YOLO class the box belongs to, …) so the
// caller can re-associate suppressed-or-kept results with their
// source rows in the model output tensor.
type Box struct {
	X, Y, W, H float64
	Score      float32
	Index      int
}

// Apply returns the subset of boxes that survive NMS. Algorithm:
//
//  1. Sort boxes by Score descending.
//  2. Walk top-to-bottom. Keep a box if no already-kept box overlaps
//     it by more than iouThreshold.
//
// Complexity is O(N²) in the worst case; for a single image's
// candidate list (typically a few hundred) this is negligible.
func Apply(boxes []Box, iouThreshold float64) []Box {
	if len(boxes) == 0 {
		return nil
	}
	work := make([]Box, len(boxes))
	copy(work, boxes)
	sort.Slice(work, func(i, j int) bool { return work[i].Score > work[j].Score })

	kept := make([]Box, 0, len(work))
	for _, candidate := range work {
		suppressed := false
		for _, chosen := range kept {
			if iou(candidate, chosen) > iouThreshold {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// iou is intersection-over-union between two axis-aligned boxes.
// Returns 0 for non-overlapping or degenerate (zero-area) boxes so
// the caller doesn't have to special-case them.
func iou(a, b Box) float64 {
	ax2, ay2 := a.X+a.W, a.Y+a.H
	bx2, by2 := b.X+b.W, b.Y+b.H

	ix1 := maxF(a.X, b.X)
	iy1 := maxF(a.Y, b.Y)
	ix2 := minF(ax2, bx2)
	iy2 := minF(ay2, by2)

	iw := ix2 - ix1
	ih := iy2 - iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	union := a.W*a.H + b.W*b.H - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
