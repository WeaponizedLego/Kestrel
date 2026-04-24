package cluster

import (
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

// buildFaceClusters groups faces whose embeddings sit within the
// cosine-distance threshold. This test fixes three "alice"-like
// embeddings and one clearly different "bob" embedding, then asserts
// the clusterer finds exactly one cluster of three paths.
func TestBuildFaceClusters_GroupsNearEmbeddings(t *testing.T) {
	alice := normalized([]float32{1, 0.1, 0, 0})
	aliceNoisy1 := normalized([]float32{0.95, 0.15, 0.02, 0.01})
	aliceNoisy2 := normalized([]float32{0.9, 0.2, 0.05, 0})
	bob := normalized([]float32{0, 0, 1, 0.1})

	photos := []*library.Photo{
		{Path: "/a.jpg", Faces: []library.FaceDetection{{Embedding: alice}}},
		{Path: "/b.jpg", Faces: []library.FaceDetection{{Embedding: aliceNoisy1}}},
		{Path: "/c.jpg", Faces: []library.FaceDetection{{Embedding: aliceNoisy2}}},
		{Path: "/d.jpg", Faces: []library.FaceDetection{{Embedding: bob}}},
	}

	got := buildFaceClusters(photos, FaceCosineThreshold)
	if len(got) != 1 {
		t.Fatalf("want 1 cluster, got %d: %+v", len(got), got)
	}
	if got[0].Size != 3 {
		t.Errorf("want cluster size 3, got %d (members=%v)", got[0].Size, got[0].Members)
	}
}

// A single face (no matching neighbour) must not produce a cluster
// of size one — clusters below size 2 are suppressed everywhere in
// the package so the "burst of one" case never reaches the UI.
func TestBuildFaceClusters_SingletonsSuppressed(t *testing.T) {
	photos := []*library.Photo{
		{Path: "/a.jpg", Faces: []library.FaceDetection{{Embedding: normalized([]float32{1, 0, 0, 0})}}},
	}
	got := buildFaceClusters(photos, FaceCosineThreshold)
	if len(got) != 0 {
		t.Fatalf("singleton must not cluster; got %+v", got)
	}
}

// Naming one face in a cluster (PersonTag set) flips Untagged to 0
// on the cluster-wide level — the UI uses this to know which face
// groups still need a name.
func TestBuildFaceClusters_NamedClusterIsTagged(t *testing.T) {
	emb := normalized([]float32{1, 0, 0, 0})
	photos := []*library.Photo{
		{Path: "/a.jpg", Faces: []library.FaceDetection{{Embedding: emb, PersonTag: "alice"}}},
		{Path: "/b.jpg", Faces: []library.FaceDetection{{Embedding: emb}}},
	}
	got := buildFaceClusters(photos, FaceCosineThreshold)
	if len(got) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(got))
	}
	if got[0].Untagged != 0 {
		t.Errorf("named cluster must have Untagged=0, got %d", got[0].Untagged)
	}
}

// normalized scales v to unit length so synthetic test embeddings
// behave like the sidecar's L2-normalised output.
func normalized(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1 / (sqrt(sum)))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

func sqrt(x float64) float64 {
	// Avoid math import to keep this file tiny; good enough for a
	// test fixture normalising hand-picked vectors.
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}
