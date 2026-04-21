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
	"github.com/WeaponizedLego/kestrel/internal/library/cluster"
	"github.com/WeaponizedLego/kestrel/internal/platform"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
)

// LibraryHandler serves the library endpoints: list/filter photos,
// trigger/cancel scans, serve full-res files, browse the filesystem,
// and return the folder tree. Scan lifecycle is delegated to a
// scanner.Runner so a long scan doesn't hold an HTTP request hostage.
type LibraryHandler struct {
	lib       *library.Library
	runner    *scanner.Runner
	clusters  *cluster.Manager
	publisher scanner.Publisher
}

// NewLibraryHandler wires the handler to the shared library, scan
// runner, cluster manager, and optional event publisher. The cluster
// manager is invalidated by mutation endpoints that drop photos from
// the library so the next Tagging Queue query doesn't surface stale
// members. The publisher is used to broadcast "library:updated" when
// a mutation endpoint (e.g. bulk tagging) changes what the UI sees;
// passing nil disables that broadcast but leaves the mutation itself
// intact.
func NewLibraryHandler(lib *library.Library, runner *scanner.Runner, clusters *cluster.Manager, publisher scanner.Publisher) *LibraryHandler {
	return &LibraryHandler{lib: lib, runner: runner, clusters: clusters, publisher: publisher}
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
	mux.HandleFunc("/reveal", h.reveal)
	mux.HandleFunc("/tags", h.setTags)
	mux.HandleFunc("/folder-tags", h.addFolderTags)
	mux.HandleFunc("/folder/remove", h.removeFolder)
	mux.HandleFunc("/tags/bulk", h.addBulkTags)
	mux.HandleFunc("/tags/list", h.listTags)
	mux.HandleFunc("/tags/rename", h.renameTag)
	mux.HandleFunc("/tags/merge", h.mergeTags)
	mux.HandleFunc("/tags/delete", h.deleteTag)
	mux.HandleFunc("/resync", h.resync)
}

// listTags responds to GET /api/tags/list with the full user-tag
// vocabulary (name + per-tag photo count), sorted by name. AutoTags
// are excluded on purpose — see Library.AllTags.
func (h *LibraryHandler) listTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	writeJSON(w, http.StatusOK, h.lib.AllTags())
}

// renameTagRequest is the JSON body for POST /api/tags/rename. Both
// fields are required and must not normalize to the same value.
type renameTagRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// renameTag responds to POST /api/tags/rename. Rewrites every photo
// that carries `from` to carry `to` instead, de-duplicating when the
// photo already has `to`. Returns the split counts so the UI can
// report "renamed on N, absorbed into M existing".
func (h *LibraryHandler) renameTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req renameTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	renamed, absorbed, err := h.lib.RenameTag(req.From, req.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if (renamed > 0 || absorbed > 0) && h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"renamed-tag": req.From,
			"to":          req.To,
			"renamed":     renamed,
			"absorbed":    absorbed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"renamed":  renamed,
		"absorbed": absorbed,
	})
}

// mergeTagsRequest is the JSON body for POST /api/tags/merge.
type mergeTagsRequest struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// mergeTags responds to POST /api/tags/merge. Same mechanics as
// renameTag — kept as a distinct endpoint so the intent ("I know both
// tags exist") stays legible in the API surface.
func (h *LibraryHandler) mergeTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req mergeTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	renamed, absorbed, err := h.lib.MergeTags(req.Source, req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if (renamed > 0 || absorbed > 0) && h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"merged-tag": req.Source,
			"into":       req.Target,
			"renamed":    renamed,
			"absorbed":   absorbed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"renamed":  renamed,
		"absorbed": absorbed,
	})
}

// deleteTagRequest is the JSON body for POST /api/tags/delete.
type deleteTagRequest struct {
	Name string `json:"name"`
}

// deleteTag responds to POST /api/tags/delete by stripping the named
// tag from every photo that carries it. Returns the number of photos
// actually modified.
func (h *LibraryHandler) deleteTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req deleteTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	affected := h.lib.DeleteTag(req.Name)
	if affected > 0 && h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"deleted-tag": req.Name,
			"affected":    affected,
		})
	}
	writeJSON(w, http.StatusOK, map[string]int{"affected": affected})
}

// bulkTagsRequest is the JSON body accepted by POST /api/tags/bulk.
// Tags are merged into every photo in Paths — additive semantics, the
// same as folder-tags, because replacing tag sets across a
// multi-selection is nearly always destructive.
type bulkTagsRequest struct {
	Paths []string `json:"paths"`
	Tags  []string `json:"tags"`
}

// addBulkTags responds to POST /api/tags/bulk by merging the given
// tag set into every photo whose path is listed. Unknown paths are
// silently skipped (a file may have been pruned between the client
// selecting it and this call arriving). Publishes library:updated
// when anything actually changed.
func (h *LibraryHandler) addBulkTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req bulkTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "paths is required")
		return
	}
	if len(req.Tags) == 0 {
		writeError(w, http.StatusBadRequest, "tags is required")
		return
	}

	updated := h.lib.AddTagsToPaths(req.Paths, req.Tags)
	if updated > 0 && h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"bulk-tagged": updated,
		})
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// resync responds to POST /api/resync by dropping photos whose files
// no longer exist on disk. Stat calls happen outside the library lock
// (see Library.PruneMissing), so a sweep over 1M photos on a slow
// drive is background-safe. Publishes library:updated when anything
// actually changed so open grids refresh.
//
// Named "resync" in the API (vs. "prune") to match the user-facing
// mental model: the backing store moved on, re-read it.
func (h *LibraryHandler) resync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	removed := h.lib.PruneMissing(fileExists)
	if len(removed) > 0 && h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"pruned": len(removed),
		})
	}
	writeJSON(w, http.StatusOK, map[string]int{"removed": len(removed)})
}

// fileExists reports whether path points to something the filesystem
// can stat. "Exists" here means "not a definite ENOENT" — transient
// permission errors leave the entry in place so a flaky mount doesn't
// nuke the library. The contract that any non-ENOENT stat means
// "keep" matches users' mental model of prune as "delete only files
// I know are gone".
func fileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}

// folderTagsRequest is the JSON body accepted by POST /api/folder-tags.
// Tags are merged into every photo under Folder (transitively).
type folderTagsRequest struct {
	Folder string   `json:"folder"`
	Tags   []string `json:"tags"`
}

// addFolderTags responds to POST /api/folder-tags by merging the
// given tag set into every photo under folder (prefix match, same
// semantics as filterByFolder). Publishes "library:updated" on success
// so open photo grids refresh automatically.
func (h *LibraryHandler) addFolderTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req folderTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Folder == "" {
		writeError(w, http.StatusBadRequest, "folder is required")
		return
	}
	if !filepath.IsAbs(req.Folder) {
		writeError(w, http.StatusBadRequest, "folder must be absolute")
		return
	}
	if len(req.Tags) == 0 {
		writeError(w, http.StatusBadRequest, "tags is required")
		return
	}

	updated := h.lib.AddTagsToFolder(req.Folder, req.Tags)
	if updated > 0 && h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"folder":  req.Folder,
			"updated": updated,
		})
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// removeFolderRequest is the JSON body accepted by POST /api/folder/remove.
type removeFolderRequest struct {
	Folder string `json:"folder"`
}

// removeFolder responds to POST /api/folder/remove by deleting every
// photo under folder (prefix match, same semantics as filterByFolder)
// from the in-memory library. Files on disk are untouched — re-scanning
// the same folder re-adds them. Invalidates the cluster cache and
// publishes "library:updated" when anything was actually removed so
// open grids and the Tagging Queue refresh.
func (h *LibraryHandler) removeFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req removeFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Folder == "" {
		writeError(w, http.StatusBadRequest, "folder is required")
		return
	}
	if !filepath.IsAbs(req.Folder) {
		writeError(w, http.StatusBadRequest, "folder must be absolute")
		return
	}

	removed := h.lib.RemovePhotosInFolder(req.Folder)
	if len(removed) > 0 {
		if h.clusters != nil {
			h.clusters.Invalidate()
		}
		if h.publisher != nil {
			h.publisher.Publish("library:updated", map[string]any{
				"folder":  req.Folder,
				"removed": len(removed),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"removed": len(removed)})
}

// tagsRequest is the JSON body accepted by POST /api/tags. Tags is the
// full replacement set for the photo — the server normalizes it.
type tagsRequest struct {
	Path string   `json:"path"`
	Tags []string `json:"tags"`
}

// setTags responds to POST /api/tags. Replaces the tag set on the
// referenced photo; input is normalized by the library. 404 when the
// path isn't in the library (same gate as /api/photo).
func (h *LibraryHandler) setTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req tagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := h.lib.SetTags(req.Path, req.Tags); err != nil {
		if errors.Is(err, library.ErrPhotoNotFound) {
			writeError(w, http.StatusNotFound, "photo not in library")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Return the canonical tag set so the frontend can sync to the
	// normalized form (lowercase, deduped) without a second fetch.
	p, err := h.lib.GetPhoto(req.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": p.Tags})
}

// revealRequest is the JSON body accepted by POST /api/reveal.
type revealRequest struct {
	Path string `json:"path"`
}

// reveal responds to POST /api/reveal by opening the OS file manager at
// the given photo's location. The path must already be in the library
// — same gate as /api/photo — so a caller can't ask the backend to
// reveal an arbitrary file.
func (h *LibraryHandler) reveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req revealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if _, err := h.lib.GetPhoto(req.Path); err != nil {
		writeError(w, http.StatusNotFound, "photo not in library")
		return
	}
	if err := platform.RevealInFileManager(req.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revealed": true})
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
	tokens := tokenizeQuery(r.URL.Query().Get("q"))
	// Hidden photos are suppressed from every listing unless the query
	// opts in by mentioning the "hidden" tag. This is independent of
	// the AND/ANY match mode so a user can always reach them with a
	// single-token search for "hidden".
	if !containsToken(tokens, library.HiddenTag) {
		photos = excludeHidden(photos)
	}
	if len(tokens) > 0 {
		matchAny := r.URL.Query().Get("match") == "any"
		photos = filterByTokens(photos, tokens, matchAny)
	}
	writeJSON(w, http.StatusOK, photos)
}

// excludeHidden returns a copy of photos with entries tagged "hidden"
// removed. Cheap for the common case — most photos won't carry the
// tag — and the allocation is amortised against the full list we'd
// otherwise return.
func excludeHidden(photos []*library.Photo) []*library.Photo {
	out := make([]*library.Photo, 0, len(photos))
	for _, p := range photos {
		if photoHasTag(p, library.HiddenTag) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// containsToken reports whether tokens includes needle (case-sensitive
// match against the already-lowercased tokens).
func containsToken(tokens []string, needle string) bool {
	for _, t := range tokens {
		if t == needle {
			return true
		}
	}
	return false
}

// photoHasTag reports whether p carries tag. Photo.Tags is stored
// normalized, so a direct equality check is enough.
func photoHasTag(p *library.Photo, tag string) bool {
	for _, t := range p.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// filterByTokens whitelists photos whose tokens match. Each token
// matches a photo when either (a) the photo has an exact tag equal to
// the token, or (b) the token is a case-insensitive substring of the
// photo's Name. matchAny=true means any one token is enough; false
// means every token must match.
func filterByTokens(photos []*library.Photo, tokens []string, matchAny bool) []*library.Photo {
	if len(tokens) == 0 {
		return photos
	}
	out := make([]*library.Photo, 0, len(photos))
	for _, p := range photos {
		name := strings.ToLower(p.Name)
		tags := tagSet(p.Tags)
		if matchesTokens(tokens, name, tags, matchAny) {
			out = append(out, p)
		}
	}
	return out
}

// tokenizeQuery splits q on whitespace and lowercases each token,
// dropping empty fragments. Mirrors the normalization SetTags applies
// so typed input lines up against stored tags.
func tokenizeQuery(q string) []string {
	fields := strings.Fields(strings.ToLower(q))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// tagSet returns a lookup set of the photo's tags. Photo.Tags is
// already normalized (lowercase, deduped) by the library, so no
// further work is needed here.
func tagSet(tags []string) map[string]struct{} {
	if len(tags) == 0 {
		return nil
	}
	s := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		s[t] = struct{}{}
	}
	return s
}

// matchesTokens reports whether tokens match against name + tags under
// the selected mode. A token matches when the tag set contains it
// exactly, or when it appears as a substring in the lowercased name.
func matchesTokens(tokens []string, name string, tags map[string]struct{}, matchAny bool) bool {
	for _, tok := range tokens {
		_, isTag := tags[tok]
		hit := isTag || strings.Contains(name, tok)
		if matchAny && hit {
			return true
		}
		if !matchAny && !hit {
			return false
		}
	}
	// Loop ended without an early return: under "all" every token
	// matched, under "any" none did.
	return !matchAny
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
