// Package api holds the HTTP handlers. Handlers stay thin: decode the
// request, call into a domain package, encode the response. Business
// logic lives in internal/library, internal/scanner, etc.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON encodes v as JSON and writes it with the given status.
// Encoding errors are logged via http.Error after the status is set,
// so the client at least sees a non-2xx response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Can't change the status now, but we can at least surface it.
		slog.Error("encoding json response", "err", err)
	}
}

// writeError writes a JSON error envelope with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
