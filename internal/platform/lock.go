package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// LockInfo is the JSON payload written to the lock file. URL points
// at the running instance so a second-launch attempt can hand off
// ("re-open the browser at the running instance" per TASKS.md
// Phase 10).
type LockInfo struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

// LockPath returns the absolute path of kestrel.lock under the user's
// config directory. Co-located with library_meta.gob so config-dir
// permissions only need to be right in one place.
func LockPath() (string, error) {
	return appDataPath("kestrel.lock")
}

// AcquireLock tries to claim the single-instance slot. On success it
// writes info to path and returns acquired=true. On failure (another
// instance is alive) it returns acquired=false plus that instance's
// LockInfo so the caller can re-open the browser there. A stale lock
// (PID is dead) is silently overwritten.
func AcquireLock(path string, info LockInfo) (bool, LockInfo, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, LockInfo{}, fmt.Errorf("creating lock dir for %s: %w", path, err)
	}

	if ok, err := writeLock(path, info); err != nil {
		return false, LockInfo{}, err
	} else if ok {
		return true, info, nil
	}

	// Lock exists; figure out whether it's live or stale.
	existing, readErr := readLock(path)
	if readErr != nil {
		// Couldn't parse — treat as stale and try to overwrite once.
		if err := os.Remove(path); err != nil {
			return false, LockInfo{}, fmt.Errorf("removing unparseable lock %s: %w", path, err)
		}
		ok, err := writeLock(path, info)
		if err != nil {
			return false, LockInfo{}, err
		}
		return ok, info, nil
	}

	if isProcessAlive(existing.PID) {
		return false, existing, nil
	}

	// Stale lock from a crashed instance — claim it.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, LockInfo{}, fmt.Errorf("removing stale lock %s: %w", path, err)
	}
	ok, err := writeLock(path, info)
	if err != nil {
		return false, LockInfo{}, err
	}
	return ok, info, nil
}

// ReleaseLock removes the lock file. A missing file is not an error —
// double-release on shutdown paths is normal.
func ReleaseLock(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// writeLock attempts an exclusive create of path with info encoded as
// JSON. Returns ok=true on success, ok=false if the file already
// exists, error for anything else.
func writeLock(path string, info LockInfo) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(info); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// readLock parses a lock file. Returns an error for missing files so
// callers don't confuse "no lock" with "stale lock".
func readLock(path string) (LockInfo, error) {
	var info LockInfo
	body, err := os.ReadFile(path)
	if err != nil {
		return info, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return info, fmt.Errorf("decoding %s: %w", path, err)
	}
	return info, nil
}

// isProcessAlive reports whether the given PID belongs to a running
// process. On Unix this uses the standard "signal 0" probe; on
// Windows it falls back to os.FindProcess, which calls OpenProcess
// and is sufficient for the lock-file purpose.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// FindProcess succeeded, which on Windows means OpenProcess
		// returned a valid handle — the process exists.
		return true
	}
	return p.Signal(syscall.Signal(0)) == nil
}
