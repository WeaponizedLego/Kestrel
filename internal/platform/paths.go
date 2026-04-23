package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// appDirName is the per-user directory Kestrel owns under the OS
// config root. Kept as a single constant so tests and future helpers
// (lock file, thumbs.pack) all agree on the location.
const appDirName = "kestrel"

// LibraryMetaPath returns the absolute path of library_meta.gob under
// the user's config directory (e.g. ~/.config/kestrel on Linux). The
// directory is not created here — Save handles that lazily so a
// read-only Load doesn't have a side effect.
func LibraryMetaPath() (string, error) {
	return appDataPath("library_meta.gob")
}

// ThumbsPackPath returns the absolute path of thumbs.pack under the
// user's cache directory. Thumbnails are regenerable, so they live
// in UserCacheDir rather than UserConfigDir — a wiped cache just
// means Kestrel rebuilds on next scan.
func ThumbsPackPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache dir: %w", err)
	}
	return filepath.Join(dir, appDirName, "thumbs.pack"), nil
}

// FileOpsJournalPath returns the absolute path of fileops.journal
// under the user's config directory. The journal is a durable record
// of destructive operations, so it lives alongside library_meta.gob
// in config rather than cache — wiping it would erase crash-recovery
// state for in-flight moves/deletes.
func FileOpsJournalPath() (string, error) {
	return appDataPath("fileops.journal")
}

// WatchedRootsPath returns the absolute path of watched_roots.json
// under the user's config directory. This file is the durable list of
// folders the background rescanner should keep in sync with disk —
// small, editable by advanced users, and intentionally kept separate
// from library_meta.gob so its schema can evolve independently.
func WatchedRootsPath() (string, error) {
	return appDataPath("watched_roots.json")
}

// TrashRootPath returns the absolute path of the Kestrel-managed
// trash directory. Like library_meta.gob this is user data the app
// must not silently lose; a future purge/retention job can prune it.
func TrashRootPath() (string, error) {
	return appDataPath("trash")
}

// appDataPath joins the per-user config directory with an app-owned
// sub-path. Shared by LibraryMetaPath and any future config-dir files.
func appDataPath(rel string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, appDirName, rel), nil
}
