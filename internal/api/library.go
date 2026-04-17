package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
)

// LibraryHandler serves the library endpoints: list/filter photos,
// trigger/cancel scans, serve full-res files, browse the filesystem,
// and return the folder tree. Scan lifecycle is delegated to a
// scanner.Runner so a long scan doesn't hold an HTTP request hostage.
type LibraryHandler struct {
	lib    *library.Library
	runner *scanner.Runner
}

// NewLibraryHandler wires the handler to the shared library and scan
// runner. The runner owns the Publisher / ThumbStore / Thumbnailer —
// the handler just brokers HTTP calls to it.
func NewLibraryHandler(lib *library.Library, runner *scanner.Runner) *LibraryHandler {
	return &LibraryHandler{lib: lib, runner: runner}
}

// Register attaches every library route to mux. The server strips the
// "/api" prefix before calling in, so routes here are registered
// without it.
func (h *LibraryHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/photos", h.listPhotos)
	mux.HandleFunc("/photo", h.servePhoto)
	mux.HandleFunc("/scan", h.scan)
	mux.HandleFunc("/scan/cancel", h.cancelScan)
	mux.HandleFunc("/scan/status", h.scanStatus)
	mux.HandleFunc("/browse", h.browse)
	mux.HandleFunc("/folders", h.folders)
}

// browseEntry is one row in a browse listing. Files are omitted;
// Kestrel only scans folders, so the picker UI never needs them.
type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// browseResponse describes one directory's contents. Parent is empty
// at the filesystem root so the frontend can hide the "up" control.
type browseResponse struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent"`
	Entries []browseEntry `json:"entries"`
}

// browse handles GET /api/browse?path=/abs/dir and returns the
// sub-directories of path. An empty path defaults to the user's home
// directory. Non-directories and hidden dot-folders are filtered out.
func (h *LibraryHandler) browse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	raw := r.URL.Query().Get("path")
	path, err := resolveBrowsePath(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := readSubdirs(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, browseResponse{
		Path:    path,
		Parent:  parentPath(path),
		Entries: entries,
	})
}

// resolveBrowsePath turns a raw query value into a cleaned absolute
// path. Empty → user home. The result must be absolute so the
// frontend never has to resolve paths relative to the server CWD.
func resolveBrowsePath(raw string) (string, error) {
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locating home directory: %w", err)
		}
		return home, nil
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("path must be absolute, got %q", raw)
	}
	return filepath.Clean(raw), nil
}

// readSubdirs lists the directory entries at path, keeping only
// sub-directories and dropping dot-folders. Sorting keeps the UI
// stable across refreshes.
func readSubdirs(path string) ([]browseEntry, error) {
	dir, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	out := make([]browseEntry, 0, len(dir))
	for _, e := range dir {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, browseEntry{
			Name: e.Name(),
			Path: filepath.Join(path, e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// parentPath returns the parent directory, or "" when path is already
// the filesystem root. filepath.Dir returns the same path when it's
// a root, which we turn into "" so the UI can hide the up-button.
func parentPath(path string) string {
	parent := filepath.Dir(path)
	if parent == path {
		return ""
	}
	return parent
}

// servePhoto streams the original image bytes for /api/photo?path=…
// The photo must already be in the library — otherwise we'd happily
// serve any file the binary can read, which is a traversal foot-gun
// even behind the loopback token gate.
func (h *LibraryHandler) servePhoto(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if _, err := h.lib.GetPhoto(path); err != nil {
		writeError(w, http.StatusNotFound, "photo not in library")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

// listPhotos responds to GET /api/photos with a JSON array of every
// photo in the library, served from a pre-built sort index.
//
// Query params:
//
//	sort  — "name" (default), "date", or "size"
//	order — "asc" (default) or "desc"
//
// The server always returns pre-sorted data; per the project rules
// (see CLAUDE.md) the frontend must never re-sort on the client.
func (h *LibraryHandler) listPhotos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	key, err := parseSortKey(r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	desc, err := parseOrder(r.URL.Query().Get("order"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	photos := h.lib.Sorted(key, desc)
	if folder := r.URL.Query().Get("folder"); folder != "" {
		photos = filterByFolder(photos, folder)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		photos = filterByName(photos, q)
	}
	writeJSON(w, http.StatusOK, photos)
}

// filterByName returns the subset of photos whose Name contains the
// query (case-insensitive substring match). Kept in the handler layer
// because it's trivial matching, not a pre-built index the library
// owns. Order is preserved.
func filterByName(photos []*library.Photo, q string) []*library.Photo {
	needle := strings.ToLower(q)
	out := make([]*library.Photo, 0, len(photos))
	for _, p := range photos {
		if strings.Contains(strings.ToLower(p.Name), needle) {
			out = append(out, p)
		}
	}
	return out
}

// filterByFolder returns the subset of photos whose path lives under
// folder (transitively — sub-folders are included). The trailing
// separator avoids "/foo" matching "/foobar". Photos directly in
// folder are included; the folder itself (if by some accident a
// photo had that exact path) is not, since we require a separator.
func filterByFolder(photos []*library.Photo, folder string) []*library.Photo {
	sep := string(filepath.Separator)
	prefix := strings.TrimRight(folder, sep) + sep
	out := make([]*library.Photo, 0, len(photos))
	for _, p := range photos {
		if strings.HasPrefix(p.Path, prefix) {
			out = append(out, p)
		}
	}
	return out
}

// parseSortKey validates the ?sort= query value. An empty string
// falls back to SortName so the endpoint is still useful for
// unopinionated clients.
func parseSortKey(raw string) (library.SortKey, error) {
	switch raw {
	case "", "name":
		return library.SortName, nil
	case "date":
		return library.SortDate, nil
	case "size":
		return library.SortSize, nil
	default:
		return 0, fmt.Errorf("sort must be one of name|date|size, got %q", raw)
	}
}

// parseOrder validates the ?order= query value. Returns true for
// descending order; empty or "asc" yields ascending.
func parseOrder(raw string) (bool, error) {
	switch raw {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	default:
		return false, fmt.Errorf("order must be asc or desc, got %q", raw)
	}
}

// scanRequest is the JSON body accepted by POST /api/scan.
type scanRequest struct {
	Folder string `json:"folder"`
}

// scanResponse is returned synchronously from POST /api/scan. The
// scan itself runs asynchronously; clients observe progress and
// completion via WebSocket events (scan:started / scan:progress /
// scan:done).
type scanResponse struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

// scan responds to POST /api/scan. Kicks off a background scan via
// the runner and returns 202 immediately with the scan's ID. A 409
// reply means another scan is already running — the response body
// carries the active scan's ID/root so the UI can offer to cancel.
func (h *LibraryHandler) scan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req scanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Folder == "" {
		writeError(w, http.StatusBadRequest, "folder is required")
		return
	}

	id, err := h.runner.Start(req.Folder)
	if errors.Is(err, scanner.ErrScanInProgress) {
		activeID, activeRoot := h.runner.Active()
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":       err.Error(),
			"active_id":   activeID,
			"active_root": activeRoot,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, scanResponse{ID: id, Root: req.Folder})
}

// cancelScan responds to POST /api/scan/cancel. Flips the running
// scan's context so workers drain at their next file boundary.
// Returns 200 with `cancelled` true when there was a scan to stop,
// false when the runner was idle.
func (h *LibraryHandler) cancelScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"cancelled": h.runner.Cancel(),
	})
}

// scanStatus responds to GET /api/scan/status so a freshly-loaded
// page can discover a scan that was already running when the user
// hit refresh. Idle runs return {"running": false}.
func (h *LibraryHandler) scanStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	id, root := h.runner.Active()
	writeJSON(w, http.StatusOK, map[string]any{
		"running": id != "",
		"id":      id,
		"root":    root,
	})
}
