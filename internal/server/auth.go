package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
)

const tokenHeader = "X-Kestrel-Token"

// NewSessionToken returns a random 32-byte token encoded as hex.
// A fresh token is generated on every server start so that previous
// runs' tokens can't be replayed.
func NewSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// tokenMiddleware rejects requests that don't carry the expected
// session token. The X-Kestrel-Token header is preferred; a ?token=
// query param is accepted as a fallback so <img src> and <a href>
// can reach /api/photo and /api/thumb directly without a fetch+blob
// round-trip.
func tokenMiddleware(expected string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(tokenHeader)
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if got != expected {
			http.Error(w, "missing or invalid session token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
