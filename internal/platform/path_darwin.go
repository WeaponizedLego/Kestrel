//go:build darwin

package platform

import (
	"os"
	"strings"
)

// EnsureToolPath augments PATH with the canonical macOS locations where
// CLI tools (Homebrew, MacPorts) install binaries. .app bundles launched
// from Finder inherit launchd's minimal PATH and would otherwise miss
// user-installed tools like ffmpeg even when they are correctly installed.
func EnsureToolPath() {
	extra := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/opt/local/bin",
		"/opt/local/sbin",
	}
	current := os.Getenv("PATH")
	parts := strings.Split(current, string(os.PathListSeparator))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		seen[p] = struct{}{}
	}
	added := make([]string, 0, len(extra))
	for _, dir := range extra {
		if _, ok := seen[dir]; ok {
			continue
		}
		added = append(added, dir)
	}
	if len(added) == 0 {
		return
	}
	combined := append(added, parts...)
	_ = os.Setenv("PATH", strings.Join(combined, string(os.PathListSeparator)))
}
