package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/library/cluster"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
)

// TaggingHandler owns the assisted-tagging endpoints: cluster listing,
// cluster-wide tag application, and the progress HUD. Everything here
// reads from or writes to the in-memory library via cluster.Manager;
// nothing touches disk directly.
type TaggingHandler struct {
	lib       *library.Library
	clusters  *cluster.Manager
	publisher scanner.Publisher
}

// NewTaggingHandler wires the handler. publisher can be nil (events
// are optional), but the library and cluster manager are required —
// there's no graceful mode without either.
func NewTaggingHandler(lib *library.Library, clusters *cluster.Manager, publisher scanner.Publisher) *TaggingHandler {
	return &TaggingHandler{lib: lib, clusters: clusters, publisher: publisher}
}

// Register attaches the tagging routes to mux. Registered under the
// same /api prefix strip as LibraryHandler, so paths here do not
// include "/api".
func (h *TaggingHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/clusters", h.listClusters)
	mux.HandleFunc("/clusters/dismiss", h.dismissCluster)
	mux.HandleFunc("/clusters/undismiss", h.undismissCluster)
	mux.HandleFunc("/tagging/apply", h.apply)
	mux.HandleFunc("/tagging/progress", h.progress)
	mux.HandleFunc("/untagged", h.untaggedByFolder)
}

// untaggedPhotoDTO is the per-photo wire shape for GET /api/untagged.
// Hash drives the thumbnail URL on the frontend; callers that don't
// need dimensions (e.g. a count-only UI) can ignore them.
type untaggedPhotoDTO struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"sizeBytes"`
}

// untaggedFolderDTO is a folder bucket for GET /api/untagged.
type untaggedFolderDTO struct {
	Folder string             `json:"folder"`
	Count  int                `json:"count"`
	Photos []untaggedPhotoDTO `json:"photos"`
}

// untaggedByFolder responds to GET /api/untagged with photos that have
// no user tags, grouped by parent directory. Powers the repurposed
// Tagging Queue island, which browses a fresh library folder-by-folder
// rather than by pHash similarity.
func (h *TaggingHandler) untaggedByFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	buckets := h.lib.UntaggedByFolder()
	folders := make([]untaggedFolderDTO, 0, len(buckets))
	total := 0
	for _, b := range buckets {
		photos := make([]untaggedPhotoDTO, 0, len(b.Photos))
		for _, p := range b.Photos {
			photos = append(photos, untaggedPhotoDTO{
				Path:      p.Path,
				Name:      p.Name,
				Width:     p.Width,
				Height:    p.Height,
				Hash:      p.Hash,
				SizeBytes: p.SizeBytes,
			})
		}
		total += len(photos)
		folders = append(folders, untaggedFolderDTO{
			Folder: b.Folder,
			Count:  len(photos),
			Photos: photos,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"folders": folders,
		"total":   total,
	})
}

// listClusters responds to GET /api/clusters?kind=duplicate|similar.
// Returns the cached cluster list; if the cache is dirty the Manager
// rebuilds it synchronously under its own lock. The synchronous path
// is fine for MVP — a 100K-photo rebuild is ~tens of ms — and keeps
// the API free of the 202/polling dance documented as a future option.
func (h *TaggingHandler) listClusters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	kind, err := parseClusterKind(r.URL.Query().Get("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	clusters := h.clusters.Clusters(kind)
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":      clusterKindString(kind),
		"threshold": cluster.Threshold(kind),
		"clusters":  clusters,
	})
}

// parseClusterKind maps the ?kind= query value onto cluster.Kind.
// Empty or "duplicate" → Duplicate; "similar" → Similar; "exact" →
// Exact (byte-identical SHA-256 grouping); anything else is a bad
// request so callers get a clear 4xx rather than a silently-default
// result.
func parseClusterKind(raw string) (cluster.Kind, error) {
	switch raw {
	case "", "duplicate":
		return cluster.Duplicate, nil
	case "similar":
		return cluster.Similar, nil
	case "exact":
		return cluster.Exact, nil
	default:
		return 0, fmt.Errorf("kind must be duplicate, similar, or exact, got %q", raw)
	}
}

// clusterKindString is the inverse of parseClusterKind, used only to
// echo back which kind the response pertains to. Keeping it alongside
// the parser documents the wire vocabulary as a single table.
func clusterKindString(k cluster.Kind) string {
	switch k {
	case cluster.Similar:
		return "similar"
	case cluster.Exact:
		return "exact"
	default:
		return "duplicate"
	}
}

// dismissRequest is the JSON body for POST /api/clusters/dismiss and
// /api/clusters/undismiss. Members is the canonical list of photo
// paths in the cluster; the server computes the fingerprint server-side
// so client and server can't drift on the canonicalisation rules.
type dismissRequest struct {
	Members []string `json:"members"`
}

// dismissCluster responds to POST /api/clusters/dismiss. Marks the
// referenced group as "not a duplicate" so it won't reappear in any
// of the duplicate/similar/exact views. Invalidates the cluster cache
// and broadcasts clusters:ready so any open review modal refreshes.
func (h *TaggingHandler) dismissCluster(w http.ResponseWriter, r *http.Request) {
	h.toggleDismiss(w, r, true)
}

// undismissCluster responds to POST /api/clusters/undismiss. Reverses
// a previous dismissal so the cluster can resurface on the next
// rebuild. Used by the undo toast.
func (h *TaggingHandler) undismissCluster(w http.ResponseWriter, r *http.Request) {
	h.toggleDismiss(w, r, false)
}

// toggleDismiss is the shared body of dismissCluster and
// undismissCluster — the only difference is which library method gets
// called. Kept private so the dismiss/undismiss endpoints stay one
// HandleFunc each at the registration site.
func (h *TaggingHandler) toggleDismiss(w http.ResponseWriter, r *http.Request, dismiss bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req dismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Members) < 2 {
		writeError(w, http.StatusBadRequest, "members must contain at least two paths")
		return
	}
	fp := cluster.Fingerprint(req.Members)
	var err error
	if dismiss {
		err = h.lib.DismissCluster(fp)
	} else {
		err = h.lib.UndismissCluster(fp)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.clusters.Invalidate()
	if h.publisher != nil {
		h.publisher.Publish("clusters:ready", map[string]any{
			"reason":      "cluster-dismissed",
			"dismissed":   dismiss,
			"fingerprint": fp,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fingerprint": fp,
		"dismissed":   dismiss,
	})
}

// applyRequest is the JSON body accepted by POST /api/tagging/apply.
// ClusterID is informational (it lets the client confirm it tagged
// the expected group); server-side the Members list is what drives
// the merge, so a cluster changing mid-session is not fatal.
type applyRequest struct {
	ClusterID string   `json:"clusterId"`
	Members   []string `json:"members"`
	Tags      []string `json:"tags"`
}

// apply responds to POST /api/tagging/apply by merging tags into
// every path in Members. Uses the existing additive semantics from
// Library.AddTagsToPaths so a user toggling between cluster and
// similar scopes doesn't blow away earlier manual work. Publishes
// library:updated and invalidates the cluster cache on success.
func (h *TaggingHandler) apply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req applyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Members) == 0 {
		writeError(w, http.StatusBadRequest, "members is required")
		return
	}
	if len(req.Tags) == 0 {
		writeError(w, http.StatusBadRequest, "tags is required")
		return
	}

	updated := h.lib.AddTagsToPaths(req.Members, req.Tags)
	if updated > 0 {
		// The cluster membership doesn't change when we tag, but the
		// "untagged count per cluster" does — invalidate so the next
		// progress / clusters query reflects the new state.
		h.clusters.Invalidate()
		if h.publisher != nil {
			h.publisher.Publish("library:updated", map[string]any{
				"tagging-applied": updated,
				"clusterId":       req.ClusterID,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// progress responds to GET /api/tagging/progress. The cluster manager
// walks the library once to count tagged vs. untagged and surface the
// biggest still-untagged group; the UI uses that to render the HUD at
// the top of the Tagging Queue island.
func (h *TaggingHandler) progress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	writeJSON(w, http.StatusOK, h.clusters.Progress())
}
