package server

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// tokenMetaPlaceholder is the literal string the shell renderer writes
// into index.html. The server rewrites it to a real <meta> tag carrying
// the per-run token on every request for "/" or "/index.html".
const tokenMetaPlaceholder = "<!--KESTREL_TOKEN_META-->"

// assetsHandler serves the embedded frontend build. The only special
// case is index.html, where it injects the session token so the
// frontend's transport layer can read it from the DOM.
func assetsHandler(assets fs.FS, token string) http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			if err := serveIndex(w, assets, token); err != nil {
				http.Error(w, fmt.Sprintf("serving index: %v", err), http.StatusInternalServerError)
			}
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex reads index.html from the embedded FS, replaces the token
// placeholder with a real meta tag, and writes the result.
func serveIndex(w http.ResponseWriter, assets fs.FS, token string) error {
	f, err := assets.Open("index.html")
	if err != nil {
		return fmt.Errorf("opening index.html: %w", err)
	}
	defer f.Close()

	body, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("reading index.html: %w", err)
	}

	meta := fmt.Sprintf(`<meta name="kestrel-token" content="%s">`, token)
	body = bytes.Replace(body, []byte(tokenMetaPlaceholder), []byte(meta), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(body)
	return err
}
