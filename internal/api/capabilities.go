package api

import (
	"net/http"
	"os/exec"
)

// CapabilitiesHandler reports which optional host tools Kestrel can
// invoke. Today the only capabilities are ffmpeg (for video frame
// extraction) and ffprobe (for video metadata). The frontend reads
// this once on load to decide whether to show the "install ffmpeg"
// banner in the photo viewer.
type CapabilitiesHandler struct{}

// NewCapabilitiesHandler returns a zero-config handler. Detection is
// done per request via exec.LookPath so the user can install ffmpeg
// without restarting Kestrel.
func NewCapabilitiesHandler() *CapabilitiesHandler { return &CapabilitiesHandler{} }

// Register attaches the capabilities route to mux. The /api prefix is
// stripped by the server before this is called.
func (h *CapabilitiesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/capabilities", h.serve)
}

// capabilitiesResponse is the JSON shape returned by GET /api/capabilities.
type capabilitiesResponse struct {
	FFmpeg  bool `json:"ffmpeg"`
	FFprobe bool `json:"ffprobe"`
}

func (h *CapabilitiesHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		FFmpeg:  hasBinary("ffmpeg"),
		FFprobe: hasBinary("ffprobe"),
	})
}

// hasBinary reports whether name resolves on the current PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
