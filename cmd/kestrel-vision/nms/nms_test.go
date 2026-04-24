package nms

import "testing"

// Two strongly-overlapping boxes should collapse to the higher-score
// one. This is the core NMS property.
func TestApply_SuppressesDuplicates(t *testing.T) {
	boxes := []Box{
		{X: 0, Y: 0, W: 10, H: 10, Score: 0.9, Index: 0},
		{X: 1, Y: 1, W: 10, H: 10, Score: 0.8, Index: 1}, // ~IoU 0.68 with #0
	}
	kept := Apply(boxes, 0.5)
	if len(kept) != 1 {
		t.Fatalf("want 1 box, got %d", len(kept))
	}
	if kept[0].Index != 0 {
		t.Errorf("kept box Index = %d, want 0 (higher score)", kept[0].Index)
	}
}

// Disjoint boxes must all survive — NMS must not suppress when IoU
// is below the threshold.
func TestApply_KeepsDisjoint(t *testing.T) {
	boxes := []Box{
		{X: 0, Y: 0, W: 10, H: 10, Score: 0.9, Index: 0},
		{X: 100, Y: 100, W: 10, H: 10, Score: 0.8, Index: 1},
	}
	kept := Apply(boxes, 0.5)
	if len(kept) != 2 {
		t.Fatalf("want 2 boxes, got %d", len(kept))
	}
}

// Output must be sorted by score descending so downstream "take top
// K" logic is a straight prefix.
func TestApply_SortsByScore(t *testing.T) {
	boxes := []Box{
		{X: 0, Y: 0, W: 10, H: 10, Score: 0.5, Index: 0},
		{X: 20, Y: 0, W: 10, H: 10, Score: 0.9, Index: 1},
		{X: 40, Y: 0, W: 10, H: 10, Score: 0.7, Index: 2},
	}
	kept := Apply(boxes, 0.5)
	if len(kept) != 3 {
		t.Fatalf("want 3 boxes, got %d", len(kept))
	}
	for i := 1; i < len(kept); i++ {
		if kept[i-1].Score < kept[i].Score {
			t.Errorf("output not sorted: kept[%d].Score=%f < kept[%d].Score=%f",
				i-1, kept[i-1].Score, i, kept[i].Score)
		}
	}
}

// An empty input must return an empty (or nil) output without
// panicking or allocating.
func TestApply_Empty(t *testing.T) {
	if out := Apply(nil, 0.5); len(out) != 0 {
		t.Errorf("empty input produced %d boxes", len(out))
	}
}
