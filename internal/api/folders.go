package api

import (
	"net/http"
	"path/filepath"
	"sort"
)

// FolderNode is one row in the /api/folders response. The tree is
// returned as a flat list; the frontend reconstructs hierarchy using
// Parent. A node whose Parent is "" is a root (no other folder node
// contains it).
//
// Count is photos *directly* in this folder. Total includes this
// folder plus all descendants — the metric a sidebar usually wants
// to show next to a folder name.
type FolderNode struct {
	Path   string `json:"path"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Total  int    `json:"total"`
}

// folders handles GET /api/folders. It derives the folder tree from
// the live photo set: every directory that contains at least one
// photo (directly or transitively) becomes a node. The response is
// a single flat array sorted by path so hierarchy is visible to
// humans reading the JSON and stable across reloads.
//
// Computed on demand from lib.AllPhotos() — one pass, O(N × depth).
// Can be cached at the library level later if it shows up hot.
func (h *LibraryHandler) folders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}

	photos := h.lib.AllPhotos()
	direct := make(map[string]int, len(photos)/4)
	total := make(map[string]int, len(photos)/4)

	for _, p := range photos {
		parent := filepath.Dir(p.Path)
		direct[parent]++
		// Walk up the ancestor chain, counting each as containing
		// this photo. Stop when Dir returns itself (filesystem root).
		for dir := parent; ; {
			total[dir]++
			up := filepath.Dir(dir)
			if up == dir {
				break
			}
			dir = up
		}
	}

	nodes := make([]FolderNode, 0, len(total))
	for path, n := range total {
		nodes = append(nodes, FolderNode{
			Path:   path,
			Parent: parentOrEmpty(path),
			Name:   baseName(path),
			Count:  direct[path],
			Total:  n,
		})
	}

	// Roots are nodes whose Parent isn't itself a node in the tree.
	// Blanking Parent for those lets the frontend identify roots
	// without re-scanning the set.
	known := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		known[n.Path] = struct{}{}
	}
	for i := range nodes {
		if _, ok := known[nodes[i].Parent]; !ok {
			nodes[i].Parent = ""
		}
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	writeJSON(w, http.StatusOK, nodes)
}

// parentOrEmpty returns p's parent directory, or "" when p is already
// the filesystem root (Dir returns itself for "/" and "C:\").
func parentOrEmpty(p string) string {
	d := filepath.Dir(p)
	if d == p {
		return ""
	}
	return d
}

// baseName returns the last path element. Falls back to the path
// itself when filepath.Base would collapse to "/" or "." — gives the
// sidebar something readable for a root like "/".
func baseName(p string) string {
	b := filepath.Base(p)
	if b == "/" || b == "." || b == string(filepath.Separator) {
		return p
	}
	return b
}
