// Package assets wraps the embedded frontend build so that the server
// can serve it through http.FS. The Vite build writes its output into
// internal/assets/dist/ (configured in frontend/vite.config.ts) so the
// //go:embed directive below picks it up automatically.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the embedded dist/ directory rooted at its top-level, so
// callers can serve "index.html" directly without knowing the wrapper
// folder name.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, fmt.Errorf("rooting embedded assets at dist: %w", err)
	}
	return sub, nil
}
