package api

import (
	"encoding/json"
	"net/http"

	"github.com/WeaponizedLego/kestrel/internal/thumbnail"
)

// ThumbsHandler serves the thumbnail endpoints: synchronous byte
// fetches for the viewer, and prefetch hints from the scroll layer.
type ThumbsHandler struct {
	provider thumbnail.Provider
}

// NewThumbsHandler wires the handler to the shared provider.
func NewThumbsHandler(provider thumbnail.Provider) *ThumbsHandler {
	return &ThumbsHandler{provider: provider}
}

// Register attaches every thumb route to mux. The server strips "/api"
// before dispatch so paths here are registered without it.
func (h *ThumbsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/thumb", h.serveThumb)
	mux.HandleFunc("/viewport", h.viewport)
	mux.HandleFunc("/navigate", h.navigate)
}

// serveThumb handles GET /api/thumb?path=/abs/path/to/photo.jpg — the
// byte fetch for a single thumbnail. Returns 404 when the thumbnail
// hasn't been generated yet so the frontend can show a placeholder.
func (h *ThumbsHandler) serveThumb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	data, err := h.provider.GetOrLoad(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if data == nil {
		writeError(w, http.StatusNotFound, "thumbnail not available")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

// viewportRequest is the JSON body of POST /api/viewport. Paths is
// the full visible list; Lookahead is the server-side guess at the
// user's scroll direction (next pages).
type viewportRequest struct {
	Paths     []string `json:"paths"`
	Lookahead []string `json:"lookahead,omitempty"`
}

// viewport handles POST /api/viewport. The visible paths are pinned
// at TierViewport; the lookahead paths are pre-fetched at
// TierLookahead so scrolling stays hit-heavy.
func (h *ThumbsHandler) viewport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req viewportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	h.provider.Prefetch(req.Paths, thumbnail.TierViewport)
	h.provider.Prefetch(req.Lookahead, thumbnail.TierLookahead)
	writeJSON(w, http.StatusOK, map[string]int{
		"queued": len(req.Paths) + len(req.Lookahead),
	})
}

// navigateRequest is the JSON body of POST /api/navigate. Current is
// the active folder's thumbnails; Children are the next drill-down
// candidates (one level deep).
type navigateRequest struct {
	Current  []string `json:"current"`
	Children []string `json:"children,omitempty"`
}

// navigate handles POST /api/navigate. Warms the folder and (one
// level of) child folder thumbnails at their respective tiers.
func (h *ThumbsHandler) navigate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is allowed")
		return
	}
	var req navigateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	h.provider.Prefetch(req.Current, thumbnail.TierFolder)
	h.provider.Prefetch(req.Children, thumbnail.TierChildFolder)
	writeJSON(w, http.StatusOK, map[string]int{
		"queued": len(req.Current) + len(req.Children),
	})
}
