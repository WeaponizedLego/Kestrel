package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/WeaponizedLego/kestrel/internal/fileops"
	"github.com/WeaponizedLego/kestrel/internal/library/cluster"
	"github.com/WeaponizedLego/kestrel/internal/scanner"
)

// FileOpsHandler serves /api/files/* — the destructive endpoints.
// It's thin on purpose: the Manager already owns journaling,
// validation, and batch semantics. The handler decodes, dispatches,
// and encodes. It also owns the post-mutation side effects that sit
// outside the Manager's contract: invalidating the cluster cache and
// broadcasting "library:updated" so the UI refetches derived views
// (duplicates, tagging queue).
type FileOpsHandler struct {
	mgr       *fileops.Manager
	clusters  *cluster.Manager
	publisher scanner.Publisher
}

// NewFileOpsHandler wires the handler to a Manager, the cluster cache
// (invalidated after a successful delete so duplicate/auto-tag views
// stop showing removed photos), and an optional event publisher used
// to broadcast "library:updated" after a mutation. Passing nil for
// clusters or publisher disables the corresponding side effect but
// leaves the mutation itself intact.
func NewFileOpsHandler(mgr *fileops.Manager, clusters *cluster.Manager, publisher scanner.Publisher) *FileOpsHandler {
	return &FileOpsHandler{mgr: mgr, clusters: clusters, publisher: publisher}
}

// Register attaches routes. /api prefix is stripped by the server.
func (h *FileOpsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/files/move", h.move)
	mux.HandleFunc("/files/delete", h.delete)
	mux.HandleFunc("/files/undo", h.undo)
	mux.HandleFunc("/files/undo/depth", h.undoDepth)
}

// moveRequest is the JSON body for POST /api/files/move.
type moveRequest struct {
	Paths  []string `json:"paths"`
	Dest   string   `json:"dest"`
	Verify bool     `json:"verify"`
}

func (h *FileOpsHandler) move(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	results, err := h.mgr.Move(req.Paths, fileops.MoveOptions{
		Dest:   req.Dest,
		Verify: req.Verify,
	})
	if errors.Is(err, fileops.ErrScanInProgress) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summariseResults(results, "moved"))
}

// deleteRequest is the JSON body for POST /api/files/delete.
type deleteRequest struct {
	Paths     []string `json:"paths"`
	Permanent bool     `json:"permanent"`
}

func (h *FileOpsHandler) delete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req deleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	results, err := h.mgr.Delete(req.Paths, fileops.DeleteOptions{
		Permanent: req.Permanent,
	})
	if errors.Is(err, fileops.ErrScanInProgress) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	successes := 0
	for _, r := range results {
		if r.Success {
			successes++
		}
	}
	if successes > 0 {
		if h.clusters != nil {
			h.clusters.Invalidate()
		}
		if h.publisher != nil {
			h.publisher.Publish("library:updated", map[string]any{
				"deleted":   successes,
				"permanent": req.Permanent,
			})
		}
	}
	writeJSON(w, http.StatusOK, summariseResults(results, "deleted"))
}

func (h *FileOpsHandler) undo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	summary, err := h.mgr.Undo()
	if errors.Is(err, fileops.ErrNothingToUndo) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, fileops.ErrScanInProgress) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *FileOpsHandler) undoDepth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"depth": h.mgr.UndoDepth()})
}

// summariseResults turns a per-file slice into a response shape the
// frontend can render uniformly: the count of successes under the
// verb-keyed field, the count of failures under "failed", and the
// full per-file results for error display.
func summariseResults(results []fileops.Result, verb string) map[string]any {
	success, failure := 0, 0
	for _, r := range results {
		if r.Success {
			success++
		} else {
			failure++
		}
	}
	return map[string]any{
		verb:      success,
		"failed":  failure,
		"results": results,
	}
}
