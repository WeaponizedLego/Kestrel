package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
)

// writeTimeout bounds how long a single Event write can block the per-
// connection writer goroutine. A stuck socket gets kicked rather than
// backing up the Hub.
const writeTimeout = 5 * time.Second

// wsHandler returns the HTTP handler mounted at /ws. The connection is
// authenticated via a ?token= query parameter (WebSocket handshakes
// can't carry arbitrary headers in the browser) and the Origin header
// is checked against the bound URL so a rogue page can't upgrade.
//
// devMode relaxes the Origin check: in dev the browser sits behind
// Vite on a different port, so the Origin header points at Vite's
// URL rather than the Go listener's host. The token still gates
// access.
//
// The protocol is one-way: server pushes Events, the client only reads.
// Any payload the client sends is ignored — but the read loop still
// runs so that close frames and ping/pong are handled promptly.
func wsHandler(hub *Hub, token, originHost string, devMode bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.Error(w, "missing or invalid session token", http.StatusUnauthorized)
			return
		}
		if !devMode && !originMatches(r.Header.Get("Origin"), originHost) {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// We've already checked Origin above; tell the library to
			// skip its own check so it doesn't reject our loopback URL
			// in browsers that send an Origin the library can't parse.
			InsecureSkipVerify: true,
		})
		if err != nil {
			// Accept already wrote a response on failure.
			return
		}
		defer conn.Close(websocket.StatusInternalError, "closing")

		serveClient(r.Context(), conn, hub)
	})
}

// serveClient subscribes to the hub and pumps Events to the socket
// until the client disconnects or ctx is done. The read goroutine
// exists only to observe close frames; it discards any payload.
func serveClient(ctx context.Context, conn *websocket.Conn, hub *Hub) {
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Reader: detects client-initiated close. Payloads are ignored.
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			if err := writeEvent(ctx, conn, e); err != nil {
				return
			}
		}
	}
}

// writeEvent marshals e as JSON and writes it as a single text frame
// within writeTimeout. A failure here closes the connection; the outer
// loop will exit and the hub unsubscribe will fire.
func writeEvent(ctx context.Context, conn *websocket.Conn, e Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding event %q: %w", e.Kind, err)
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, body)
}

// originMatches returns true when the Origin header (if present) is a
// URL whose host matches the server's bound host. An absent Origin is
// accepted — non-browser clients (tests, curl) don't send one and the
// token check already gates access.
func originMatches(origin, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == host
}
