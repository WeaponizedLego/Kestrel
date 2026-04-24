package platform

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

// fileURI builds a `file://` URI for an absolute local path, with
// correct percent-encoding for special characters (tildes, spaces,
// non-ASCII) so the receiving app parses the path verbatim.
func fileURI(path string) string {
	u := &url.URL{Scheme: "file", Path: path}
	return u.String()
}

// CopyImageToClipboard places a reference to the file at path onto
// the system clipboard, matching what a file manager produces when
// you hit Ctrl+C on a file. Receiving apps (Discord, Element,
// browsers, other file managers, most image viewers) read the file
// themselves, so animation, color profiles, and metadata are all
// preserved — nothing is re-encoded.
//
// Per-OS backend (see clipboard_<goos>.go):
//   - Linux: wl-copy (Wayland) or xclip (X11) with text/uri-list.
//   - macOS: osascript writing a POSIX file reference, same format
//     Finder's Copy produces.
//   - Windows: PowerShell Set-Clipboard -Path, same CF_HDROP format
//     Explorer's Copy produces.
func CopyImageToClipboard(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("copying %s to clipboard: path must be absolute", path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("copying %s to clipboard: %w", path, err)
	}
	return copyFileToClipboard(path)
}

// copyFileToClipboardUnsupported is the default implementation used
// on any GOOS without a dedicated clipboard_<goos>.go.
func copyFileToClipboardUnsupported(path string) error {
	return fmt.Errorf("copying %s to clipboard: unsupported platform %q", path, runtime.GOOS)
}
