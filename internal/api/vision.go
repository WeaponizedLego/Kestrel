package api

import (
	"context"
	"net/http"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/vision"
)

// VisionHandler exposes the sidecar status to the frontend so the
// StatusBar badge and the TaggingQueue faces tab can render
// reachability state. It is purely a read-through of vision.Client's
// cached snapshot — no detection work happens here.
type VisionHandler struct {
	client *vision.Client
}

// NewVisionHandler returns a handler wired to client. client may be
// nil: in that case the /status endpoint reports "off" permanently,
// which matches the user-facing contract ("feature disabled") without
// requiring conditional registration upstream.
func NewVisionHandler(client *vision.Client) *VisionHandler {
	return &VisionHandler{client: client}
}

// Register attaches the vision routes to mux. Registered under the
// same /api strip as the other handlers.
func (h *VisionHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/vision/status", h.status)
}

// status responds to GET /api/vision/status with the last probe
// result. When the query carries ?refresh=1, run a fresh probe
// inline so a user clicking the badge sees reality immediately
// instead of waiting for the next background tick.
func (h *VisionHandler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	if h.client == nil {
		writeJSON(w, http.StatusOK, vision.Status{
			State:     "off",
			CheckedAt: time.Now().Unix(),
		})
		return
	}
	if r.URL.Query().Get("refresh") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		h.client.ProbeOnce(ctx)
	}
	writeJSON(w, http.StatusOK, h.client.Snapshot())
}
