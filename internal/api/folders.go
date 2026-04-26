package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// createFolderRequest is the JSON body accepted by POST /api/folder/create.
// Parent must be an absolute path inside an existing watched root; Name
// is a single path segment (no separators, no traversal).
type createFolderRequest struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

// createFolder responds to POST /api/folder/create by making a new
// directory on disk under Parent. It is gated on the watched-root set
// so callers can't materialise directories anywhere on the filesystem.
//
// The library is unchanged — empty directories carry no photos and so
// don't appear in /api/folders until something is moved into them.
// That's an intentional consequence of the in-memory truth model, not
// a bug we paper over with a synthetic node.
func (h *LibraryHandler) createFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req createFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	parent := strings.TrimSpace(req.Parent)
	if parent == "" {
		writeError(w, http.StatusBadRequest, "parent is required")
		return
	}
	if !filepath.IsAbs(parent) {
		writeError(w, http.StatusBadRequest, "parent must be absolute")
		return
	}
	parent = filepath.Clean(parent)

	if err := validateFolderName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)

	if !h.folderKnownToLibrary(parent) {
		writeError(w, http.StatusForbidden, "parent is not a known library folder")
		return
	}
	target := filepath.Join(parent, name)
	// Defence-in-depth against a normalised name escaping the parent
	// (e.g. embedded NUL or platform-specific quirks) — the joined
	// target must still sit under the same accepted parent.
	if !strings.HasPrefix(
		strings.TrimRight(target, string(filepath.Separator))+string(filepath.Separator),
		strings.TrimRight(parent, string(filepath.Separator))+string(filepath.Separator),
	) {
		writeError(w, http.StatusBadRequest, "target escapes parent")
		return
	}

	if err := os.Mkdir(target, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":  "folder already exists",
				"path":   target,
				"exists": true,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Errorf("creating subfolder %s: %w", target, err).Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    target,
		"created": true,
	})
}

// folderKnownToLibrary reports whether path is a folder Kestrel
// already knows about: either it sits under a watched root, or at
// least one photo currently in the library lives under it. The latter
// covers folders that show up in the sidebar tree because they were
// scanned ad-hoc, even after their watched-root entry was removed.
func (h *LibraryHandler) folderKnownToLibrary(path string) bool {
	if h.isCoveredByExistingRoot(path) {
		return true
	}
	if h.lib == nil {
		return false
	}
	prefix := strings.TrimRight(path, string(filepath.Separator)) + string(filepath.Separator)
	for _, p := range h.lib.AllPhotos() {
		if strings.HasPrefix(p.Path, prefix) {
			return true
		}
	}
	return false
}

// validateFolderName rejects single-segment names that would walk out
// of the parent or collide with the dot-folder filter applied by the
// browse endpoint. Returns nil for a usable name.
func validateFolderName(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" {
		return errors.New("name is required")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name %q is not allowed", name)
	}
	if strings.HasPrefix(name, ".") {
		return errors.New("name must not start with a dot")
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return errors.New("name must not contain path separators")
	}
	return nil
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
