package api

import (
	"encoding/json"
	"net/http"

	"github.com/WeaponizedLego/kestrel/internal/settings"
)

// SettingsHandler serves /api/settings: the small UI preferences
// (theme, sort key/order, grid cell size) that survive across launches
// even though the loopback port — and therefore the browser's
// localStorage origin — changes every time.
type SettingsHandler struct {
	store *settings.Store
}

// NewSettingsHandler wires the handler to a Settings store.
func NewSettingsHandler(store *settings.Store) *SettingsHandler {
	return &SettingsHandler{store: store}
}

// Register attaches the /settings route. The server strips the "/api"
// prefix before calling in.
func (h *SettingsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/settings", h.handle)
}

// handle dispatches by method: GET returns the current settings, PUT
// merges a sparse patch and returns the resulting full settings.
func (h *SettingsHandler) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.store.Get())
	case http.MethodPut:
		var patch settings.Settings
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		merged, err := h.store.Update(patch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, merged)
	default:
		writeError(w, http.StatusMethodNotAllowed, "only GET and PUT are allowed")
	}
}
