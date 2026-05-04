package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// systemPrefixes is the GOOS-specific list of absolute path prefixes
// that Kestrel must never scan, watch, or auto-register as a sub-root.
// These are OS-managed areas: TCC-protected directories on macOS,
// AppData/Windows on Windows, /proc and friends on Linux. None of them
// hold user photos, all of them blow up walk and watcher budgets.
//
// Computed once at init from environment variables ($HOME on Unix,
// %WINDIR% etc. on Windows) so a missing env var degrades to the
// fixed-prefix subset rather than panicking.
var systemPrefixes = computeSystemPrefixes()

func computeSystemPrefixes() []string {
	switch runtime.GOOS {
	case "darwin":
		return darwinSystemPrefixes()
	case "windows":
		return windowsSystemPrefixes()
	case "linux":
		return linuxSystemPrefixes()
	default:
		return nil
	}
}

func darwinSystemPrefixes() []string {
	prefixes := []string{
		"/System",
		"/private/var",
		"/Library",
		"/cores",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		prefixes = append(prefixes,
			filepath.Join(home, "Library"),
			filepath.Join(home, ".Trash"),
		)
	}
	return prefixes
}

func windowsSystemPrefixes() []string {
	var prefixes []string
	for _, env := range []string{
		"WINDIR",
		"PROGRAMFILES",
		"PROGRAMFILES(X86)",
		"PROGRAMDATA",
		"APPDATA",
		"LOCALAPPDATA",
	} {
		if v := os.Getenv(env); v != "" {
			prefixes = append(prefixes, v)
		}
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		prefixes = append(prefixes, filepath.Join(home, "AppData"))
	}
	return prefixes
}

func linuxSystemPrefixes() []string {
	return []string{
		"/proc",
		"/sys",
		"/dev",
		"/run",
		"/var",
		"/boot",
	}
}

// IsSystemPath reports whether path lies inside an OS-managed area
// that Kestrel should never scan, watch, or register as a sub-root.
// Used at every boundary that can introduce a path into the store
// (POST /api/scan, walker decomposition, watcher subtree register,
// store Open cleanup).
//
// Comparison is exact-equal-or-descendant against a fixed prefix list.
// On Windows the comparison is case-insensitive segment-by-segment;
// elsewhere the filesystem is case-sensitive and we compare bytewise.
func IsSystemPath(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	for _, prefix := range systemPrefixes {
		if hasPathPrefix(clean, filepath.Clean(prefix)) {
			return true
		}
	}
	return false
}

// hasPathPrefix reports whether path equals prefix or descends from
// it. On Windows the test is case-insensitive on the prefix segment,
// matching how the OS treats paths.
func hasPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	sep := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		if strings.EqualFold(path, prefix) {
			return true
		}
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix)+sep)
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+sep)
}
