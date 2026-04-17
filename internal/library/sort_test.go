package library

import (
	"testing"
	"time"
)

func TestSorted_ByName(t *testing.T) {
	lib := New()
	lib.ReplaceAll([]*Photo{
		{Path: "/p/c.jpg", Name: "c.jpg"},
		{Path: "/p/a.jpg", Name: "a.jpg"},
		{Path: "/p/b.jpg", Name: "b.jpg"},
	})

	got := names(lib.Sorted(SortName, false))
	want := []string{"a.jpg", "b.jpg", "c.jpg"}
	if !equalStrings(got, want) {
		t.Fatalf("Sorted(SortName) = %v, want %v", got, want)
	}

	desc := names(lib.Sorted(SortName, true))
	wantDesc := []string{"c.jpg", "b.jpg", "a.jpg"}
	if !equalStrings(desc, wantDesc) {
		t.Fatalf("Sorted(SortName desc) = %v, want %v", desc, wantDesc)
	}
}

func TestSorted_BySize(t *testing.T) {
	lib := New()
	lib.ReplaceAll([]*Photo{
		{Path: "/p/big.jpg", Name: "big.jpg", SizeBytes: 1000},
		{Path: "/p/small.jpg", Name: "small.jpg", SizeBytes: 10},
		{Path: "/p/mid.jpg", Name: "mid.jpg", SizeBytes: 100},
	})

	got := names(lib.Sorted(SortSize, false))
	want := []string{"small.jpg", "mid.jpg", "big.jpg"}
	if !equalStrings(got, want) {
		t.Fatalf("Sorted(SortSize) = %v, want %v", got, want)
	}
}

func TestSorted_ByDate_ZerosSinkToEnd(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	lib := New()
	lib.ReplaceAll([]*Photo{
		{Path: "/p/nodate.jpg", Name: "nodate.jpg"},
		{Path: "/p/newer.jpg", Name: "newer.jpg", TakenAt: base.Add(time.Hour)},
		{Path: "/p/older.jpg", Name: "older.jpg", TakenAt: base},
	})

	got := names(lib.Sorted(SortDate, false))
	want := []string{"older.jpg", "newer.jpg", "nodate.jpg"}
	if !equalStrings(got, want) {
		t.Fatalf("Sorted(SortDate) = %v, want %v", got, want)
	}
}

func TestSorted_ReturnsCopy(t *testing.T) {
	lib := New()
	lib.ReplaceAll([]*Photo{
		{Path: "/p/a.jpg", Name: "a.jpg"},
		{Path: "/p/b.jpg", Name: "b.jpg"},
	})

	snap := lib.Sorted(SortName, false)
	snap[0] = nil // mutating caller's slice must not affect future calls

	again := lib.Sorted(SortName, false)
	if again[0] == nil {
		t.Fatal("Sorted returned a shared slice; mutating the caller's copy reached the index")
	}
}

func TestAddPhoto_RebuildsIndices(t *testing.T) {
	lib := New()
	lib.ReplaceAll([]*Photo{
		{Path: "/p/a.jpg", Name: "a.jpg"},
		{Path: "/p/c.jpg", Name: "c.jpg"},
	})
	lib.AddPhoto(&Photo{Path: "/p/b.jpg", Name: "b.jpg"})

	got := names(lib.Sorted(SortName, false))
	want := []string{"a.jpg", "b.jpg", "c.jpg"}
	if !equalStrings(got, want) {
		t.Fatalf("after AddPhoto, Sorted = %v, want %v", got, want)
	}
}

func names(photos []*Photo) []string {
	out := make([]string, len(photos))
	for i, p := range photos {
		out[i] = p.Name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
