package scanner

import (
	"path/filepath"
	"strings"
)

// skipDirNames is the set of directory basenames that are never worth
// scanning or watching. Build artefacts, package caches, and VCS
// internals — none of which contain photos a user would expect to see
// in Kestrel, all of which can balloon to tens of thousands of
// subdirectories and ruin both walk time and the OS file-descriptor
// budget the watcher draws from.
//
// Entries here must be names users are extremely unlikely to use for a
// real photo folder. When in doubt, leave it out — the fix is one
// missed photo, not tens of thousands of phantom watches.
var skipDirNames = map[string]struct{}{
	"node_modules": {},
	"__pycache__":  {},
	"cdk.out":      {},
	"vendor":       {},
}

// skipDirSuffixes are macOS-style "package" directory suffixes — names
// the OS treats as opaque files even though they're directories on
// disk. Application bundles (.app, .bundle, .framework, .kext, .xpc),
// Apple media libraries (.photoslibrary, .musiclibrary, .tvlibrary,
// .aplibrary), and the like never contain user-visible photo files
// the user would expect Kestrel to manage. Skipping them prevents
// scenarios like "I deleted a sound file from inside an .app bundle
// and broke the application." Suffix match is case-insensitive
// because macOS filesystems usually are.
var skipDirSuffixes = []string{
	".app",
	".bundle",
	".framework",
	".kext",
	".xpc",
	".plugin",
	".photoslibrary",
	".musiclibrary",
	".tvlibrary",
	".aplibrary",
	".photolibrary",
}

// ShouldSkipDir reports whether a directory name should be excluded
// from scans, decomposition, and watching. Hidden directories (names
// starting with ".") are always skipped — they are conventionally
// non-user-browseable and dominate the .git/.cache/.venv noise that
// blows up the watcher.
func ShouldSkipDir(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true
	}
	if _, skip := skipDirNames[name]; skip {
		return true
	}
	lower := strings.ToLower(name)
	for _, suffix := range skipDirSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// PathHasSkippedComponent reports whether any segment of path matches
// the skip rule. Used at boundaries (watcher bootstrap, migration) to
// reject paths that the walker would have skipped at discovery time
// — necessary because watchroots.json may already contain skipped
// paths from before this filter was added.
func PathHasSkippedComponent(path string) bool {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	for _, p := range parts {
		if ShouldSkipDir(p) {
			return true
		}
	}
	return false
}
