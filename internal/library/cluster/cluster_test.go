package cluster

import (
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

type stubLib struct {
	photos    []*library.Photo
	dismissed map[string]struct{}
}

func (s stubLib) AllPhotos() []*library.Photo { return s.photos }

func (s stubLib) IsClusterDismissed(fp string) bool {
	_, ok := s.dismissed[fp]
	return ok
}

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

func TestClusters_ExactGroupsByContentHash(t *testing.T) {
	// Two photos share the same SHA-256 (true duplicates), one has a
	// distinct hash, one has no hash yet. PHash differences are
	// irrelevant for the exact view — only Hash matters.
	m := NewManager(stubLib{photos: []*library.Photo{
		{Path: "/a.jpg", Hash: "aa", PHash: 1},
		{Path: "/b.jpg", Hash: "aa", PHash: 99},
		{Path: "/c.jpg", Hash: "bb", PHash: 1},
		{Path: "/d.jpg", Hash: "", PHash: 1},
	}})
	got := m.Clusters(Exact)
	if len(got) != 1 {
		t.Fatalf("expected 1 exact cluster, got %d: %+v", len(got), got)
	}
	if got[0].Size != 2 {
		t.Fatalf("expected cluster of 2, got %d", got[0].Size)
	}
	if got[0].Members[0] != "/a.jpg" || got[0].Members[1] != "/b.jpg" {
		t.Fatalf("members = %v, want [/a.jpg /b.jpg]", got[0].Members)
	}
	if got[0].ID != "exact-aa" {
		t.Fatalf("id = %q, want exact-aa", got[0].ID)
	}
}

func TestClusters_DismissedClustersFilteredOut(t *testing.T) {
	// A near-duplicate cluster of 2 that the user has dismissed must
	// not appear under Duplicate, Similar, or Exact.
	photos := []*library.Photo{
		{Path: "/a.jpg", Hash: "h", PHash: 0xFFFF_FFFF_FFFF_FFFF},
		{Path: "/b.jpg", Hash: "h", PHash: 0xFFFF_FFFF_FFFF_FFFE},
	}
	fp := Fingerprint([]string{"/a.jpg", "/b.jpg"})
	m := NewManager(stubLib{
		photos:    photos,
		dismissed: map[string]struct{}{fp: {}},
	})
	if got := m.Clusters(Duplicate); len(got) != 0 {
		t.Fatalf("Duplicate: expected 0, got %+v", got)
	}
	if got := m.Clusters(Similar); len(got) != 0 {
		t.Fatalf("Similar: expected 0, got %+v", got)
	}
	if got := m.Clusters(Exact); len(got) != 0 {
		t.Fatalf("Exact: expected 0, got %+v", got)
	}
}

func TestFingerprint_StableAcrossOrder(t *testing.T) {
	a := Fingerprint([]string{"/x", "/a", "/m"})
	b := Fingerprint([]string{"/m", "/x", "/a"})
	if a != b {
		t.Fatalf("fingerprint should be order-independent: %s vs %s", a, b)
	}
	if a == "" {
		t.Fatalf("fingerprint must not be empty for non-empty input")
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
