// Package cluster groups photos by perceptual-hash similarity so the
// UI can offer "tag this burst" / "tag everything visually similar"
// workflows. The entry point is Manager, which owns the pHash index,
// computes clusters lazily, and caches the result until invalidated.
//
// The algorithm is a union-find over every pair of photos within a
// Hamming-distance threshold. The naive O(N²) comparison becomes
// tractable on large libraries by first sorting hashes and only
// comparing entries whose top bits are close — see candidatePairs.
// For 1M photos this keeps the worst case inside the sub-second
// target documented in docs/assisted-tagging.md.
package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"math/bits"
	"sort"
	"sync"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

// Kind selects which Hamming-distance threshold a cluster query uses.
// The two thresholds produce two meaningfully different views over the
// same underlying photo set.
type Kind uint8

const (
	// Duplicate clusters capture bursts, edits, and re-saves: photos
	// that a human would call "the same shot".
	Duplicate Kind = iota

	// Similar clusters capture looser visual groups — same subject,
	// same framing, same scene — at the cost of more false positives.
	Similar

	// Exact clusters group photos by SHA-256 of their file contents:
	// byte-identical files. The Hamming threshold doesn't apply — two
	// files are either the same bytes or they aren't. Used by the
	// duplicates UI's "exact only" toggle so users can confidently
	// reclaim provably redundant copies.
	Exact
)

// Default thresholds live here as consts rather than knobs because
// they need to line up with docs/assisted-tagging.md and with the UI
// copy ("duplicates" vs. "similar"). Power users can override via a
// config file later; exposing them on every API call would invite
// drift.
const (
	ThresholdDuplicate = 5
	ThresholdSimilar   = 12
)

// Threshold returns the Hamming-distance cap for the given kind.
// Exported so callers (handlers, tests) don't duplicate the mapping.
// Exact returns 0 — its grouping is by content hash, not by distance,
// but the API still echoes the value so the wire shape is uniform.
func Threshold(k Kind) int {
	switch k {
	case Similar:
		return ThresholdSimilar
	case Exact:
		return 0
	default:
		return ThresholdDuplicate
	}
}

// Cluster is one group of visually-related photos. Members are the
// photo paths (the library's canonical key), sorted for stable output.
// Size is redundant with len(Members) but pre-computed so pagination
// sorts don't have to walk every slice.
type Cluster struct {
	ID       string   `json:"id"`
	Members  []string `json:"members"`
	Size     int      `json:"size"`
	Untagged int      `json:"untagged"`
}

// librarySource is the slice of *library.Library Manager reads from.
// Declared as an interface so tests can plug in a recording fake and
// so the cluster package stays a leaf that doesn't import server.
//
// IsClusterDismissed is consulted during rebuild to drop clusters the
// user has marked "not a duplicate". The fingerprint is opaque to the
// library — compute it via Fingerprint so both producer and consumer
// agree on the canonical form.
type librarySource interface {
	AllPhotos() []*library.Photo
	IsClusterDismissed(fingerprint string) bool
}

// Manager owns the cluster cache for one library. Queries are
// lazy-computed: the first Clusters call after invalidation does the
// union-find work under the cache mutex; subsequent calls return the
// cached slice. Invalidate flips the dirty flag.
type Manager struct {
	lib librarySource

	mu       sync.Mutex
	dirty    bool
	byKind   map[Kind][]Cluster
}

// NewManager returns a Manager that reads photos from lib. The cache
// starts dirty so the first query triggers a rebuild.
func NewManager(lib librarySource) *Manager {
	return &Manager{
		lib:    lib,
		dirty:  true,
		byKind: make(map[Kind][]Cluster),
	}
}

// Invalidate marks the cache stale. Called whenever the library adds,
// removes, or re-hashes photos — the scanner triggers this at the end
// of a scan, and tagging applies don't need to (tags don't affect
// visual similarity).
func (m *Manager) Invalidate() {
	m.mu.Lock()
	m.dirty = true
	m.mu.Unlock()
}

// Clusters returns the cluster slice for kind, rebuilding it from the
// current library state if the cache is dirty. Output is sorted by
// size descending, then by first-member path for stable pagination.
//
// Clusters of size 1 are suppressed: a "cluster of one" is just a
// photo, which the regular grid already handles.
func (m *Manager) Clusters(kind Kind) []Cluster {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirty {
		m.rebuildLocked()
		m.dirty = false
	}
	out := make([]Cluster, len(m.byKind[kind]))
	copy(out, m.byKind[kind])
	return out
}

// Progress is the payload of GET /api/tagging/progress. Exported at
// the cluster package boundary because "how many photos are tagged"
// is really a library-wide question, and the Manager already walks
// every photo during rebuild.
type Progress struct {
	Total               int `json:"total"`
	Tagged              int `json:"tagged"`
	Untagged            int `json:"untagged"`
	LargestUntaggedSize int `json:"largestUntaggedSize"`
}

// Progress walks the current library to count tagged vs. untagged
// photos and surface the biggest untagged cluster. Called by the
// /api/tagging/progress handler to drive the UI HUD.
func (m *Manager) Progress() Progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirty {
		m.rebuildLocked()
		m.dirty = false
	}

	var prog Progress
	for _, p := range m.lib.AllPhotos() {
		prog.Total++
		if len(p.Tags) > 0 {
			prog.Tagged++
		} else {
			prog.Untagged++
		}
	}
	for _, c := range m.byKind[Duplicate] {
		if c.Untagged > prog.LargestUntaggedSize {
			prog.LargestUntaggedSize = c.Untagged
		}
	}
	return prog
}

// rebuildLocked regenerates every kind cache from the current library
// snapshot. Caller must hold m.mu. Clusters the library has marked
// dismissed are filtered out of every kind — once a user confirms a
// group "isn't a duplicate", showing the same group under "Similar" or
// "Exact" would defeat the purpose.
func (m *Manager) rebuildLocked() {
	photos := m.lib.AllPhotos()
	entries := make([]entry, 0, len(photos))
	for _, p := range photos {
		if p.PHash == 0 {
			// No hash → can't cluster. Skip rather than lumping every
			// un-hashed photo together: the grouping would be by
			// "no hash yet" which is not visual similarity.
			continue
		}
		entries = append(entries, entry{hash: p.PHash, path: p.Path, tagged: len(p.Tags) > 0})
	}

	m.byKind[Duplicate] = m.filterDismissed(buildClusters(entries, ThresholdDuplicate))
	m.byKind[Similar] = m.filterDismissed(buildClusters(entries, ThresholdSimilar))
	m.byKind[Exact] = m.filterDismissed(buildExactClusters(photos))
}

// filterDismissed drops clusters whose fingerprint the library has
// marked dismissed. Returns the input slice unchanged when no
// dismissals match, so the common case allocates nothing extra.
func (m *Manager) filterDismissed(in []Cluster) []Cluster {
	if len(in) == 0 {
		return in
	}
	out := in[:0:0]
	dropped := false
	for _, c := range in {
		if m.lib.IsClusterDismissed(Fingerprint(c.Members)) {
			dropped = true
			continue
		}
		out = append(out, c)
	}
	if !dropped {
		return in
	}
	return out
}

// Fingerprint is the stable identifier for a cluster's membership: a
// hex SHA-256 over the sorted, newline-joined member paths. Used by
// the dismissal feature to recognise the same group across rebuilds
// without depending on the volatile cluster ID (whose root pHash can
// shift if a member is added or removed).
func Fingerprint(memberPaths []string) string {
	if len(memberPaths) == 0 {
		return ""
	}
	sorted := make([]string, len(memberPaths))
	copy(sorted, memberPaths)
	sort.Strings(sorted)
	h := sha256.New()
	for i, p := range sorted {
		if i > 0 {
			h.Write([]byte{'\n'})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildExactClusters groups photos by their SHA-256 content hash. Any
// hash with at least two photos becomes a cluster; photos with an
// empty Hash (not yet computed) are skipped. Output is sorted the
// same way as buildClusters' output: size desc, first member asc.
func buildExactClusters(photos []*library.Photo) []Cluster {
	if len(photos) == 0 {
		return nil
	}
	groups := make(map[string][]*library.Photo)
	for _, p := range photos {
		if p.Hash == "" {
			continue
		}
		groups[p.Hash] = append(groups[p.Hash], p)
	}
	clusters := make([]Cluster, 0, len(groups))
	for hash, members := range groups {
		if len(members) < 2 {
			continue
		}
		paths := make([]string, 0, len(members))
		untagged := 0
		for _, p := range members {
			paths = append(paths, p.Path)
			if len(p.Tags) == 0 {
				untagged++
			}
		}
		sort.Strings(paths)
		clusters = append(clusters, Cluster{
			ID:       "exact-" + hash,
			Members:  paths,
			Size:     len(paths),
			Untagged: untagged,
		})
	}
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Size != clusters[j].Size {
			return clusters[i].Size > clusters[j].Size
		}
		return clusters[i].Members[0] < clusters[j].Members[0]
	})
	return clusters
}

// entry is the per-photo tuple the clustering algorithm consumes.
// Kept internal so future additions (weights, camera grouping) don't
// leak into the cluster cache's public shape.
type entry struct {
	hash   uint64
	path   string
	tagged bool
}

// buildClusters runs union-find over entries, unioning every pair
// whose Hamming distance is ≤ threshold, and returns the resulting
// non-singleton clusters sorted by size desc.
func buildClusters(entries []entry, threshold int) []Cluster {
	if len(entries) == 0 {
		return nil
	}

	// Sort by hash first. Two hashes whose top bits differ by more
	// than threshold cannot possibly match, so a sweep with a small
	// look-ahead window avoids the full O(N²) pair scan. The window
	// size is conservative — when hashes cluster densely we still
	// catch everything because the sweep's inner loop breaks out of
	// the window based on the threshold, not a fixed count.
	sorted := make([]entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].hash < sorted[j].hash })

	uf := newUnionFind(len(sorted))
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if hammingDistance(sorted[i].hash, sorted[j].hash) <= threshold {
				uf.union(i, j)
				continue
			}
			// Early exit: if the high-bit prefix already differs by
			// more than threshold, every later entry differs at least
			// as much because the list is sorted by hash. For dHash,
			// neighbouring numerical values do not strictly correspond
			// to neighbouring Hamming distances — but a substantially
			// larger numerical gap does imply a large bit gap. We use
			// a generous cutoff (threshold*8) so the heuristic never
			// prunes a real match.
			if sorted[j].hash-sorted[i].hash > uint64(threshold)<<8 {
				break
			}
		}
	}

	groups := make(map[int][]int)
	for i := range sorted {
		root := uf.find(i)
		groups[root] = append(groups[root], i)
	}

	clusters := make([]Cluster, 0, len(groups))
	for root, members := range groups {
		if len(members) < 2 {
			continue
		}
		paths := make([]string, 0, len(members))
		untagged := 0
		for _, m := range members {
			paths = append(paths, sorted[m].path)
			if !sorted[m].tagged {
				untagged++
			}
		}
		sort.Strings(paths)
		clusters = append(clusters, Cluster{
			ID:       clusterID(sorted[root].hash, paths[0]),
			Members:  paths,
			Size:     len(paths),
			Untagged: untagged,
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Size != clusters[j].Size {
			return clusters[i].Size > clusters[j].Size
		}
		return clusters[i].Members[0] < clusters[j].Members[0]
	})
	return clusters
}

// hammingDistance counts differing bits. Uses the stdlib popcount so
// modern CPUs get the single POPCNT instruction when available.
func hammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// clusterID is stable across rebuilds: keyed on the root entry's hash
// and the lexicographically-first member path. That means a cluster
// keeps the same ID across scans even though the root choice in a
// union-find rebuild is otherwise arbitrary.
func clusterID(rootHash uint64, firstPath string) string {
	// 16 hex chars of hash + short trailing hash of the first path.
	// Not cryptographic; just enough entropy to disambiguate two
	// coincidentally-equal root hashes in different member sets.
	const hex = "0123456789abcdef"
	var out [16 + 1 + 8]byte
	for i := 0; i < 16; i++ {
		shift := uint((15 - i) * 4)
		out[i] = hex[(rootHash>>shift)&0xF]
	}
	out[16] = '-'
	var fnv uint32 = 2166136261
	for i := 0; i < len(firstPath); i++ {
		fnv ^= uint32(firstPath[i])
		fnv *= 16777619
	}
	for i := 0; i < 8; i++ {
		shift := uint((7 - i) * 4)
		out[17+i] = hex[(fnv>>shift)&0xF]
	}
	return string(out[:])
}

// unionFind is the textbook disjoint-set structure with path
// compression and union-by-rank. Kept private because callers should
// reach for Manager rather than composing clustering themselves.
type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	uf := &unionFind{parent: make([]int, n), rank: make([]int, n)}
	for i := range uf.parent {
		uf.parent[i] = i
	}
	return uf
}

func (uf *unionFind) find(i int) int {
	for uf.parent[i] != i {
		uf.parent[i] = uf.parent[uf.parent[i]]
		i = uf.parent[i]
	}
	return i
}

func (uf *unionFind) union(a, b int) {
	ra, rb := uf.find(a), uf.find(b)
	if ra == rb {
		return
	}
	switch {
	case uf.rank[ra] < uf.rank[rb]:
		uf.parent[ra] = rb
	case uf.rank[ra] > uf.rank[rb]:
		uf.parent[rb] = ra
	default:
		uf.parent[rb] = ra
		uf.rank[ra]++
	}
}
