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

// themeMetaPlaceholder is the sibling placeholder for the persisted
// daisyUI theme name. Injected at request time so the theme attribute
// is correct before the Vue islands hydrate — without this the user
// sees a flash of the default theme before the frontend can react.
const themeMetaPlaceholder = "<!--KESTREL_THEME_META-->"

// assetsHandler serves the embedded frontend build. The only special
// case is index.html, where it injects the session token and the
// persisted theme so the frontend's transport layer can read both
// from the DOM. theme may be nil if no settings store is wired —
// callers fall through to the frontend's compiled-in default.
func assetsHandler(assets fs.FS, token string, theme func() string) http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			if err := serveIndex(w, assets, token, theme); err != nil {
				http.Error(w, fmt.Sprintf("serving index: %v", err), http.StatusInternalServerError)
			}
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex reads index.html from the embedded FS, replaces the
// token and theme placeholders with real meta tags, and writes the
// result.
func serveIndex(w http.ResponseWriter, assets fs.FS, token string, theme func() string) error {
	f, err := assets.Open("index.html")
	if err != nil {
		return fmt.Errorf("opening index.html: %w", err)
	}
	defer f.Close()

	body, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("reading index.html: %w", err)
	}

	tokenMeta := fmt.Sprintf(`<meta name="kestrel-token" content="%s">`, token)
	body = bytes.Replace(body, []byte(tokenMetaPlaceholder), []byte(tokenMeta), 1)

	themeName := ""
	if theme != nil {
		themeName = theme()
	}
	themeMeta := fmt.Sprintf(`<meta name="kestrel-theme" content="%s">`, themeName)
	body = bytes.Replace(body, []byte(themeMetaPlaceholder), []byte(themeMeta), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(body)
	return err
}
