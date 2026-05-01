package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/library/cluster"
	"github.com/WeaponizedLego/kestrel/internal/platform"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
	"github.com/WeaponizedLego/kestrel/internal/watchroots"
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
	roots     *watchroots.Store
}

// NewLibraryHandler wires the handler to the shared library, scan
// runner, cluster manager, and optional event publisher. The cluster
// manager is invalidated by mutation endpoints that drop photos from
// the library so the next Tagging Queue query doesn't surface stale
// members. The publisher is used to broadcast "library:updated" when
// a mutation endpoint (e.g. bulk tagging) changes what the UI sees;
// passing nil disables that broadcast but leaves the mutation itself
// intact.
func NewLibraryHandler(lib *library.Library, runner *scanner.Runner, clusters *cluster.Manager, publisher scanner.Publisher, roots *watchroots.Store) *LibraryHandler {
	return &LibraryHandler{lib: lib, runner: runner, clusters: clusters, publisher: publisher, roots: roots}
}

// Register attaches every library route to mux. The server strips the
// "/api" prefix before calling in, so routes here are registered
// without it.
func (h *LibraryHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/photos", h.listPhotos)
	mux.HandleFunc("/photo", h.servePhoto)
	mux.HandleFunc("/photo/meta", h.photoMeta)
	mux.HandleFunc("/scan", h.scan)
	mux.HandleFunc("/scan/cancel", h.cancelScan)
	mux.HandleFunc("/scan/status", h.scanStatus)
	mux.HandleFunc("/watched-roots", h.watchedRoots)
	mux.HandleFunc("/browse", h.browse)
	mux.HandleFunc("/folders", h.folders)
	mux.HandleFunc("/reveal", h.reveal)
	mux.HandleFunc("/clipboard/copy", h.clipboardCopy)
	mux.HandleFunc("/tags", h.setTags)
	mux.HandleFunc("/folder-tags", h.addFolderTags)
	mux.HandleFunc("/folder/remove", h.removeFolder)
	mux.HandleFunc("/folder/create", h.createFolder)
	mux.HandleFunc("/tags/bulk", h.addBulkTags)
	mux.HandleFunc("/tags/list", h.listTags)
	mux.HandleFunc("/tags/rename", h.renameTag)
	mux.HandleFunc("/tags/merge", h.mergeTags)
	mux.HandleFunc("/tags/delete", h.deleteTag)
	mux.HandleFunc("/tags/hidden", h.setTagHidden)
	mux.HandleFunc("/resync", h.resync)
	mux.HandleFunc("/rescan", h.rescan)
}

// listTags responds to GET /api/tags/list with the user-tag vocabulary
// (name + per-tag photo count + kind + hidden flag), sorted by name.
//
// Query params:
//
//	include_hidden=1 — also include user tags marked hidden.
//	include_auto=1   — also include derived auto-tags (read-only in
//	                   the UI: they regenerate on every scan).
//
// Both default off so the unopinionated call still returns the classic
// "editable user vocabulary" list.
func (h *LibraryHandler) listTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	includeHidden := boolQuery(r.URL.Query().Get("include_hidden"))
	includeAuto := boolQuery(r.URL.Query().Get("include_auto"))
	writeJSON(w, http.StatusOK, h.lib.AllTagsFiltered(includeHidden, includeAuto))
}

// boolQuery accepts the common truthy spellings for a flag-style query
// param. Anything else (including empty) reads as false.
func boolQuery(raw string) bool {
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// setTagHiddenRequest is the JSON body for POST /api/tags/hidden.
type setTagHiddenRequest struct {
	Name   string `json:"name"`
	Hidden bool   `json:"hidden"`
}

// setTagHidden responds to POST /api/tags/hidden by marking a user tag
// hidden (or un-hiding it). Hidden is a Tag-Manager-visibility flag
// only — photos still match searches on a hidden tag exactly as
// before.
//
// Rejects two cases at the boundary:
//   - the literal library.HiddenTag ("hidden"), which already has a
//     fixed meaning elsewhere (photo suppression) and shouldn't be
//     shadowed by a manager-visibility toggle;
//   - auto-only tags (present as an AutoTag but not on any Photo.Tags),
//     which regenerate every scan and aren't meaningful to persist.
func (h *LibraryHandler) setTagHidden(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req setTagHiddenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	norm := strings.ToLower(strings.TrimSpace(req.Name))
	if norm == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if norm == library.HiddenTag {
		writeError(w, http.StatusBadRequest,
			`"hidden" is reserved for photo suppression and cannot be toggled here`)
		return
	}
	if req.Hidden && !tagExistsOnUserTags(h.lib, norm) {
		writeError(w, http.StatusBadRequest, "only user tags can be hidden")
		return
	}
	if err := h.lib.SetTagHidden(norm, req.Hidden); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.publisher != nil {
		h.publisher.Publish("library:updated", map[string]any{
			"hidden-tag": norm,
			"hidden":     req.Hidden,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   norm,
		"hidden": req.Hidden,
	})
}

// tagExistsOnUserTags reports whether any photo carries name as a
// user tag. Used to reject "hide this auto-only tag" requests — the
// user can still hide an auto-tag name by first adding it to at least
// one photo as a real user tag, which is the intended escape hatch.
func tagExistsOnUserTags(lib *library.Library, name string) bool {
	for _, stat := range lib.AllTagsFiltered(true, false) {
		if stat.Kind == library.TagKindUser && stat.Name == name {
			return true
		}
	}
	return false
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
	if p, err := h.lib.GetPhoto(req.Path); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tags": p.Tags})
		return
	}
	if a, err := h.lib.GetAudio(req.Path); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tags": a.Tags})
		return
	}
	writeError(w, http.StatusInternalServerError, "tags persisted but path vanished")
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
	if !h.pathInLibrary(req.Path) {
		writeError(w, http.StatusNotFound, "photo not in library")
		return
	}
	if err := platform.RevealInFileManager(req.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revealed": true})
}

// clipboardCopy responds to POST /api/clipboard/copy by placing the
// raw file bytes of the given photo onto the system clipboard with
// the correct image MIME type. Unlike a browser canvas copy this
// preserves animated GIF/WebP frames because the file is never
// re-encoded. Path is gated through the library so callers can't push
// arbitrary files onto the clipboard.
func (h *LibraryHandler) clipboardCopy(w http.ResponseWriter, r *http.Request) {
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
	if err := platform.CopyImageToClipboard(req.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"copied": true})
}

// browseEntry is one row in a browse listing. Files are omitted;
// Kestrel only scans folders, so the picker UI never needs them.
type browseEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	HasChildren bool   `json:"has_children"`
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
		child := filepath.Join(path, e.Name())
		out = append(out, browseEntry{
			Name:        e.Name(),
			Path:        child,
			HasChildren: dirHasSubdir(child),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// dirHasSubdir reports whether path contains at least one
// non-dot sub-directory. Permission errors and unreadable mounts
// produce false — the picker UI shows "no chevron", which is the
// honest signal: we couldn't tell.
func dirHasSubdir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
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

// photoMeta responds to GET /api/photo/meta?path=… with the JSON
// representation of a single Photo. Used by surfaces that have a path
// but need the full struct (e.g. the lightbox preview launched from
// the duplicate-cluster list, which can't reuse the surrounding
// PhotoGrid's selection state).
func (h *LibraryHandler) photoMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if p, err := h.lib.GetPhoto(path); err == nil {
		writeJSON(w, http.StatusOK, p)
		return
	}
	if a, err := h.lib.GetAudio(path); err == nil {
		projected := library.AudioAsPhoto(a)
		writeJSON(w, http.StatusOK, &projected)
		return
	}
	writeError(w, http.StatusNotFound, "photo not in library")
}

// servePhoto streams the original image bytes for /api/photo?path=…
// The photo must already be in the library — otherwise we'd happily
// serve any file the binary can read, which is a traversal foot-gun
// even behind the loopback token gate.
//
// http.ServeFile already handles HTTP Range requests, which the
// browser <video> element relies on to seek — no extra plumbing
// needed beyond hinting Content-Type so the browser picks the right
// decoder before bytes start streaming.
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
	if !h.pathInLibrary(path) {
		writeError(w, http.StatusNotFound, "photo not in library")
		return
	}
	if ct := mediaContentType(path); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

// pathInLibrary reports whether path is registered as either a photo
// or an audio entry. Used by every "stream a file by path" or "act on
// the file at path" endpoint as the traversal gate, since by the time
// audio entered the wire shape every path-keyed handler must accept
// both kinds.
func (h *LibraryHandler) pathInLibrary(path string) bool {
	if _, err := h.lib.GetPhoto(path); err == nil {
		return true
	}
	if _, err := h.lib.GetAudio(path); err == nil {
		return true
	}
	return false
}

// mediaContentType returns the MIME type Kestrel wants attached to a
// served file, or "" to defer to net/http's own sniffing. Video and
// audio types are pinned explicitly because http.ServeFile's sniffer
// can produce "application/octet-stream" for less common containers
// (e.g. .mkv, .flac, .opus), which the browser then refuses to play.
func mediaContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/ogg; codecs=opus"
	}
	return ""
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
	photos := mergeSortedMedia(h.lib.Sorted(key, desc), h.lib.SortedAudio(key, desc), key, desc)
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
	// "untagged" is a virtual token: peel it off and apply it as a hard
	// filter (zero user Tags) before the regular token match runs.
	// Why: it has to gate even under match=any, since "untagged OR foo"
	// has no useful reading — the user means "untagged, narrowed by foo".
	tokens, untaggedOnly := peelToken(tokens, library.UntaggedTag)
	if untaggedOnly {
		photos = filterUntagged(photos)
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

// filterUntagged returns the subset of photos with zero user Tags.
// AutoTags are ignored — they're auto-derived and almost every photo
// has at least one, so including them would make the filter useless.
// Matches Library.UntaggedByFolder and cluster.Progress definitions.
func filterUntagged(photos []*library.Photo) []*library.Photo {
	out := make([]*library.Photo, 0, len(photos))
	for _, p := range photos {
		if len(p.Tags) == 0 {
			out = append(out, p)
		}
	}
	return out
}

// peelToken removes every occurrence of needle from tokens and reports
// whether at least one was removed. Used to lift virtual tokens out of
// the regular token-match pipeline.
func peelToken(tokens []string, needle string) ([]string, bool) {
	found := false
	out := tokens[:0:0]
	for _, t := range tokens {
		if t == needle {
			found = true
			continue
		}
		out = append(out, t)
	}
	return out, found
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
// the token in either Tags or AutoTags, or (b) the token is a
// case-insensitive substring of the photo's Name. AutoTags are
// included so users can search the inferred attributes (kind:video,
// camera:canon, year:2024, …) the same way they search user tags.
// matchAny=true means any one token is enough; false means every
// token must match.
func filterByTokens(photos []*library.Photo, tokens []string, matchAny bool) []*library.Photo {
	if len(tokens) == 0 {
		return photos
	}
	out := make([]*library.Photo, 0, len(photos))
	for _, p := range photos {
		name := strings.ToLower(p.Name)
		tags := tagSetFor(p)
		if matchesTokens(tokens, name, tags, matchAny) {
			out = append(out, p)
		}
	}
	return out
}

// tagSetFor returns a lookup set combining the photo's user Tags and
// AutoTags. Both slices are stored normalized by their producers, so
// no extra work is needed here.
func tagSetFor(p *library.Photo) map[string]struct{} {
	if len(p.Tags) == 0 && len(p.AutoTags) == 0 {
		return nil
	}
	s := make(map[string]struct{}, len(p.Tags)+len(p.AutoTags))
	for _, t := range p.Tags {
		s[t] = struct{}{}
	}
	for _, t := range p.AutoTags {
		s[t] = struct{}{}
	}
	return s
}

// mergeSortedMedia returns the order-preserving merge of two
// pre-sorted slices: photos straight from Library.Sorted and audios
// projected into Photo shape. Both inputs share the same SortKey and
// direction so the merge step is O(N+M) — no extra sort needed.
//
// Audio entries land on the wire as Photo-shaped JSON (zero
// Width/Height/EXIF, AutoTags carrying kind:audio) so the frontend's
// existing PhotoGrid renders them without a parallel type. The kind
// is recoverable by inspecting AutoTags client-side; see
// frontend/src/util/media.ts.
func mergeSortedMedia(photos []*library.Photo, audios []*library.Audio, key library.SortKey, desc bool) []*library.Photo {
	if len(audios) == 0 {
		return photos
	}
	projected := make([]*library.Photo, len(audios))
	for i, a := range audios {
		p := library.AudioAsPhoto(a)
		projected[i] = &p
	}
	if len(photos) == 0 {
		return projected
	}
	out := make([]*library.Photo, 0, len(photos)+len(projected))
	i, j := 0, 0
	for i < len(photos) && j < len(projected) {
		if mediaLess(photos[i], projected[j], key, desc) {
			out = append(out, photos[i])
			i++
		} else {
			out = append(out, projected[j])
			j++
		}
	}
	out = append(out, photos[i:]...)
	out = append(out, projected[j:]...)
	return out
}

// mediaLess reports whether a should sort before b under key/desc.
// Mirrors the comparators in Library.rebuildIndicesLocked so the
// merge order matches the per-slice sort order exactly.
func mediaLess(a, b *library.Photo, key library.SortKey, desc bool) bool {
	less := false
	switch key {
	case library.SortDate:
		az, bz := a.TakenAt.IsZero(), b.TakenAt.IsZero()
		switch {
		case az != bz:
			less = !az
		case !a.TakenAt.Equal(b.TakenAt):
			less = a.TakenAt.Before(b.TakenAt)
		default:
			less = a.Path < b.Path
		}
	case library.SortSize:
		switch {
		case a.SizeBytes != b.SizeBytes:
			less = a.SizeBytes < b.SizeBytes
		default:
			less = a.Path < b.Path
		}
	default: // SortName
		switch {
		case a.Name != b.Name:
			less = a.Name < b.Name
		default:
			less = a.Path < b.Path
		}
	}
	if desc {
		return !less
	}
	return less
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
	// Record the folder as a watched root immediately so the user's
	// add is durable even if the scan never progresses. The scanner's
	// OnDirsFound callback will decompose every descendant directory
	// into its own sub-root as the walker discovers it; the eager
	// Upsert here just makes the top-level visible right away.
	// Failure to write must not block the scan itself.
	if h.roots != nil {
		if err := h.roots.UpsertTree(req.Folder, []string{req.Folder}); err != nil {
			writeJSON(w, http.StatusAccepted, scanResponse{ID: id, Root: req.Folder})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, scanResponse{ID: id, Root: req.Folder})
}

// isCoveredByExistingRoot reports whether folder is equal to, or a
// descendant of, any currently-watched root. Used by the scan handler
// to avoid registering sub-paths (e.g. /Photos/2025 when /Photos is
// already watched) as separate roots.
func (h *LibraryHandler) isCoveredByExistingRoot(folder string) bool {
	if h.roots == nil || folder == "" {
		return false
	}
	sep := string(filepath.Separator)
	clean := filepath.Clean(folder)
	target := strings.TrimRight(clean, sep) + sep
	for _, r := range h.roots.List() {
		root := strings.TrimRight(filepath.Clean(r.Path), sep) + sep
		if strings.HasPrefix(target, root) {
			return true
		}
	}
	return false
}

// rescanRequest is the JSON body accepted by POST /api/rescan. An
// empty Folder means "sweep every watched root"; a non-empty Folder
// scopes the rescan to that subtree.
type rescanRequest struct {
	Folder string `json:"folder"`
}

// rescanResponse is returned synchronously from POST /api/rescan. The
// sweep itself runs asynchronously; clients observe progress via the
// existing scan:* events and a trailing rescan:done when every root
// in the plan has finished.
type rescanResponse struct {
	Roots []string `json:"roots"`
}

// rescan responds to POST /api/rescan. Unlike /api/scan, which walks
// one folder and registers it as a watch root, /api/rescan is the
// "keep me in sync with disk" button: walk the selected folder (or
// every watched root when none is selected) at normal intensity,
// then prune entries under that subtree whose files are gone.
//
// A normal-intensity scan already preempts any low-intensity
// background rescan, so this is the "high priority" variant of the
// scheduler's own loop.
func (h *LibraryHandler) rescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req rescanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	// Refuse when a normal (user-triggered) scan is already running.
	// A low-intensity background run would just get preempted by the
	// first Start below, so we let that through.
	if activeID, activeRoot, intensity := h.runner.ActiveDetail(); activeID != "" && intensity == scanner.IntensityNormal {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":       scanner.ErrScanInProgress.Error(),
			"active_id":   activeID,
			"active_root": activeRoot,
		})
		return
	}

	roots := h.rescanPlan(req.Folder)
	if len(roots) == 0 {
		writeJSON(w, http.StatusOK, rescanResponse{Roots: []string{}})
		return
	}

	go h.runRescan(roots)
	writeJSON(w, http.StatusAccepted, rescanResponse{Roots: roots})
}

// rescanPlan resolves the request's Folder into the ordered list of
// roots to scan. An explicit folder is scanned alone; an empty folder
// expands to every currently-watched root.
func (h *LibraryHandler) rescanPlan(folder string) []string {
	if folder != "" {
		return []string{folder}
	}
	if h.roots == nil {
		return nil
	}
	list := h.roots.List()
	out := make([]string, 0, len(list))
	for _, r := range list {
		out = append(out, r.Path)
	}
	return out
}

// runRescan is the goroutine body for POST /api/rescan. It walks each
// root sequentially via runner.Start (waiting for each to finish
// before starting the next) and runs a scoped PruneMissingUnder after
// the walk so deleted files disappear from the library. Totals are
// summarised in a trailing rescan:done event.
func (h *LibraryHandler) runRescan(roots []string) {
	slog.Info("rescan started", "roots", len(roots))
	var pruned int
	completed := 0
	total := len(roots)
	defer func() {
		// Always emit rescan:done, even on a panic in PruneMissingUnder
		// or a runner.Start error mid-sweep. The frontend gates its
		// "Re-scanning…" UI on this event; missing it is what causes
		// the status bar to get stuck.
		if r := recover(); r != nil {
			slog.Error("rescan panic", "error", r, "completed", completed, "total", total)
		}
		if h.publisher != nil {
			h.publisher.Publish("rescan:done", map[string]any{
				"roots":     completed,
				"pruned":    pruned,
				"requested": len(roots),
			})
		}
		slog.Info("rescan finished",
			"requested", len(roots),
			"completed", completed,
			"pruned", pruned,
		)
	}()
	for i, root := range roots {
		h.publishRescanProgress(i+1, total, root, "scan")
		if _, err := h.runner.Start(root); err != nil {
			// Another user scan raced in between the initial check
			// and here: stop the sweep. The scan:started/done
			// stream from the other run keeps the UI honest; we
			// just don't add to it.
			slog.Warn("rescan aborted: runner.Start failed", "root", root, "error", err)
			break
		}
		h.runner.WaitForActive()

		h.publishRescanProgress(i+1, total, root, "prune")
		missing := h.lib.PruneMissingUnder(root, fileExists)
		if len(missing) > 0 {
			pruned += len(missing)
			if h.publisher != nil {
				h.publisher.Publish("library:updated", map[string]any{
					"pruned": len(missing),
					"root":   root,
				})
			}
		}
		completed++
	}
}

// publishRescanProgress annotates the running rescan with its current
// root, so the UI can show "folder i of N — <phase>" alongside the
// underlying scan:progress stream. phase is "scan" before runner.Start
// for a root and "prune" just before PruneMissingUnder.
func (h *LibraryHandler) publishRescanProgress(index, total int, root, phase string) {
	if h.publisher == nil {
		return
	}
	h.publisher.Publish("rescan:progress", map[string]any{
		"root_index":   index,
		"root_total":   total,
		"current_root": root,
		"phase":        phase,
	})
}

// watchedRoots handles GET (list) and DELETE (remove) of watched
// roots. The list shapes the background rescanner's workload; the
// delete is the "stop syncing this folder" affordance, and it
// intentionally does not touch the library — removing photos is a
// separate user choice (POST /api/folder/remove).
func (h *LibraryHandler) watchedRoots(w http.ResponseWriter, r *http.Request) {
	if h.roots == nil {
		writeError(w, http.StatusServiceUnavailable, "watched roots not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.roots.List())
	case http.MethodDelete:
		path := r.URL.Query().Get("path")
		if path == "" {
			writeError(w, http.StatusBadRequest, "path is required")
			return
		}
		if err := h.roots.Remove(path); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "only GET and DELETE are allowed")
	}
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
	id, root, intensity := h.runner.ActiveDetail()
	writeJSON(w, http.StatusOK, map[string]any{
		"running":   id != "",
		"id":        id,
		"root":      root,
		"intensity": intensity,
	})
}
