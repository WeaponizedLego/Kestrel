package main

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// maxDetectBody is the byte cap on a single /detect upload. 64 MiB
// covers the largest practical RAW-dev export; any bigger is almost
// certainly a misuse and we'd rather 413 than OOM.
const maxDetectBody = 64 << 20

// newServer wires the HTTP routes for the sidecar. Kept separate from
// main.go so tests can build a server against a stub Pipeline without
// starting a listener or touching the handshake file.
func newServer(token string, p Pipeline) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(protocol.PathHealthz, requireBearer(token, healthzHandler(p)))
	mux.Handle(protocol.PathDetect, requireBearer(token, detectHandler(p)))
	return &http.Server{Handler: mux}
}

// requireBearer wraps h with a static bearer-token check. Any
// mismatch returns 401 without a body so a probe can't learn
// anything from error shapes.
func requireBearer(token string, h http.Handler) http.Handler {
	want := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// healthzHandler answers GET /healthz with the sidecar version and
// the loaded-model identifiers. Core's probe uses this both as an
// aliveness check and to surface model identity in the UI badge
// tooltip.
func healthzHandler(p Pipeline) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "only GET is allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, protocol.Health{
			Version: version,
			Models:  p.LoadedModels(),
		})
	})
}

// detectHandler consumes POST /detect and returns the faces and
// objects found in the uploaded image. The pipeline is responsible
// for the ML work; this handler only does HTTP framing and decode.
func detectHandler(p Pipeline) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxDetectBody)
		defer r.Body.Close()

		img, _, err := image.Decode(r.Body)
		if err != nil {
			// Drain in case the decoder bailed on partial bytes so the
			// keep-alive connection stays usable for the next image.
			_, _ = io.Copy(io.Discard, r.Body)
			http.Error(w, fmt.Sprintf("decoding image: %v", err), http.StatusBadRequest)
			return
		}

		resp, err := p.Detect(r.Context(), img)
		if err != nil {
			http.Error(w, fmt.Sprintf("detection failed: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

// writeJSON is a tiny local helper; the sidecar intentionally does
// not import internal/api to keep the dependency direction clean.
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
