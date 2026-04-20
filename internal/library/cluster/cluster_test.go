package cluster

import (
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

type stubLib struct{ photos []*library.Photo }

func (s stubLib) AllPhotos() []*library.Photo { return s.photos }

func TestClusters_GroupsNearDuplicates(t *testing.T) {
	// Three nearly-identical hashes (differ by 2 bits) + one far.
	// Duplicate threshold of 5 should cluster the first three; the
	// outlier stays unclustered.
	base := uint64(0xFFFF_FFFF_FFFF_FFFF)
	m := NewManager(stubLib{photos: []*library.Photo{
		{Path: "/a.jpg", PHash: base},
		{Path: "/b.jpg", PHash: base ^ 0b11},
		{Path: "/c.jpg", PHash: base ^ 0b110},
		{Path: "/far.jpg", PHash: 0x0},
	}})

	dup := m.Clusters(Duplicate)
	if len(dup) != 1 {
		t.Fatalf("expected 1 duplicate cluster, got %d: %+v", len(dup), dup)
	}
	if dup[0].Size != 3 {
		t.Fatalf("expected cluster of 3, got %d", dup[0].Size)
	}
	wantMembers := []string{"/a.jpg", "/b.jpg", "/c.jpg"}
	for i, m := range dup[0].Members {
		if m != wantMembers[i] {
			t.Fatalf("member[%d] = %s; want %s", i, m, wantMembers[i])
		}
	}
}

func TestClusters_SkipsZeroHash(t *testing.T) {
	m := NewManager(stubLib{photos: []*library.Photo{
		{Path: "/a.jpg"}, // PHash zero → skipped
		{Path: "/b.jpg"}, // PHash zero → skipped
	}})
	if got := m.Clusters(Duplicate); len(got) != 0 {
		t.Fatalf("expected no clusters, got %+v", got)
	}
}

func TestClusters_SingletonsSuppressed(t *testing.T) {
	// One pair + one lonely hash → one cluster of 2, the singleton
	// must not appear.
	m := NewManager(stubLib{photos: []*library.Photo{
		{Path: "/a.jpg", PHash: 0xFFFF_FFFF_FFFF_FFFF},
		{Path: "/b.jpg", PHash: 0xFFFF_FFFF_FFFF_FFF0},
		{Path: "/solo.jpg", PHash: 0x1},
	}})
	got := m.Clusters(Duplicate)
	if len(got) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(got))
	}
	if got[0].Size != 2 {
		t.Fatalf("expected cluster of 2, got %d", got[0].Size)
	}
}

func TestProgress_CountsTaggedAndUntagged(t *testing.T) {
	m := NewManager(stubLib{photos: []*library.Photo{
		{Path: "/a.jpg", Tags: []string{"sunset"}},
		{Path: "/b.jpg"},
		{Path: "/c.jpg"},
	}})
	p := m.Progress()
	if p.Total != 3 || p.Tagged != 1 || p.Untagged != 2 {
		t.Fatalf("progress: %+v", p)
	}
}
