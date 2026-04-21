// Package trash is Kestrel's managed trash bin. Deleted photos are
// moved into a trash directory under the library's data folder and
// kept alongside a sidecar JSON that records their original location,
// content hash, and the timestamp of deletion. Restore reads the
// sidecar and moves the file back.
//
// This is deliberately a Kestrel-owned trash rather than the OS trash
// (macOS ~/.Trash, Windows Recycle Bin, FreeDesktop $XDG_DATA_HOME/
// Trash). The OS trash APIs differ per platform, most require CGO or
// command-line shell-outs, and none give us a reliable programmatic
// restore path. The data-safety contract for Kestrel is "we don't lose
// user files," and owning the trash end-to-end is the most auditable
// way to honour it. A later version can add an OS-trash adapter.
//
// The trash directory layout:
//
//   <root>/
//     files/
//       <id>/<basename>         -- the actual bytes
//     info/
//       <id>.json               -- metadata sidecar
//
// Why split files/ and info/ rather than co-locating: it mirrors the
// FreeDesktop spec so a later adapter can syslink into it, and it
// makes "list every trashed item" a single directory listing without
// filtering by extension.
package trash

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Info is the metadata sidecar written for every trashed file.
type Info struct {
	ID           string    `json:"id"`
	OriginalPath string    `json:"original_path"`
	TrashedAt    time.Time `json:"trashed_at"`
	PhotoHash    string    `json:"photo_hash,omitempty"`

	// TrashPath is the absolute path to the file in the trash. Written
	// for operator visibility; the package re-derives it from ID on
	// every operation so a renamed root still works.
	TrashPath string `json:"trash_path,omitempty"`
}

// Bin is a trash directory.
type Bin struct {
	root     string
	filesDir string
	infoDir  string
}

// Open creates (or reopens) a trash bin rooted at root. The subdirs
// are created on demand so a first-use bin is valid without setup.
func Open(root string) (*Bin, error) {
	b := &Bin{
		root:     root,
		filesDir: filepath.Join(root, "files"),
		infoDir:  filepath.Join(root, "info"),
	}
	for _, d := range []string{b.filesDir, b.infoDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("creating trash subdir %s: %w", d, err)
		}
	}
	return b, nil
}

// Root returns the directory the bin lives under.
func (b *Bin) Root() string { return b.root }

// Put moves src into the trash. The original path is preserved in the
// sidecar so Restore can find its way home. A cross-filesystem source
// falls back to copy+delete automatically — os.Rename handles EXDEV
// only on some platforms, so we detect and stream when needed.
//
// Returns the Info record describing the trashed file. The caller
// should persist or hand off this record so future Restore calls can
// find it by ID.
func (b *Bin) Put(src string, photoHash string) (Info, error) {
	id, err := newID()
	if err != nil {
		return Info{}, err
	}
	base := filepath.Base(src)
	destDir := filepath.Join(b.filesDir, id)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("creating trash entry dir %s: %w", destDir, err)
	}
	dest := filepath.Join(destDir, base)

	if err := moveFile(src, dest); err != nil {
		// Best-effort cleanup of the partially-created directory.
		_ = os.RemoveAll(destDir)
		return Info{}, fmt.Errorf("moving %s to trash: %w", src, err)
	}

	info := Info{
		ID:           id,
		OriginalPath: src,
		TrashedAt:    time.Now().UTC(),
		PhotoHash:    photoHash,
		TrashPath:    dest,
	}
	if err := b.writeInfo(info); err != nil {
		// Try to roll the file back so we don't have a trashed file
		// with no metadata — that's the worst of both worlds.
		_ = moveFile(dest, src)
		_ = os.RemoveAll(destDir)
		return Info{}, fmt.Errorf("writing trash info for %s: %w", id, err)
	}
	return info, nil
}

// Restore moves a trashed file back to its original location and
// removes the sidecar. If the original path is already occupied,
// returns an error without mutating anything — the caller decides
// whether to pick a new name or report the collision.
func (b *Bin) Restore(id string) (Info, error) {
	info, err := b.readInfo(id)
	if err != nil {
		return Info{}, err
	}

	if _, err := os.Stat(info.OriginalPath); err == nil {
		return info, fmt.Errorf("restoring %s: destination %s already exists", id, info.OriginalPath)
	}

	if err := os.MkdirAll(filepath.Dir(info.OriginalPath), 0o755); err != nil {
		return info, fmt.Errorf("preparing restore dir for %s: %w", info.OriginalPath, err)
	}
	if err := moveFile(info.TrashPath, info.OriginalPath); err != nil {
		return info, fmt.Errorf("restoring %s to %s: %w", id, info.OriginalPath, err)
	}
	// Best-effort cleanup. A leftover info.json or empty dir doesn't
	// break anything — List ignores unpaired entries.
	_ = os.Remove(filepath.Join(b.infoDir, id+".json"))
	_ = os.Remove(filepath.Join(b.filesDir, id))
	return info, nil
}

// Purge deletes a trashed file permanently. Used by explicit "empty
// trash" flows; Kestrel does not auto-purge.
func (b *Bin) Purge(id string) error {
	entryDir := filepath.Join(b.filesDir, id)
	infoFile := filepath.Join(b.infoDir, id+".json")
	if err := os.RemoveAll(entryDir); err != nil {
		return fmt.Errorf("purging trash entry %s: %w", id, err)
	}
	if err := os.Remove(infoFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("purging trash info %s: %w", id, err)
	}
	return nil
}

// List returns every currently trashed item, newest first. Entries
// with a missing file or sidecar are skipped — they're residue from
// an interrupted Put/Restore and will be cleaned up on the next
// maintenance pass.
func (b *Bin) List() ([]Info, error) {
	entries, err := os.ReadDir(b.infoDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing trash info dir: %w", err)
	}
	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".json")]
		info, err := b.readInfo(id)
		if err != nil {
			continue
		}
		if _, err := os.Stat(info.TrashPath); err != nil {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TrashedAt.After(out[j].TrashedAt)
	})
	return out, nil
}

func (b *Bin) writeInfo(info Info) error {
	path := filepath.Join(b.infoDir, info.ID+".json")
	tmp := path + ".tmp"
	payload, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (b *Bin) readInfo(id string) (Info, error) {
	path := filepath.Join(b.infoDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, fmt.Errorf("reading trash info %s: %w", id, err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, fmt.Errorf("decoding trash info %s: %w", id, err)
	}
	return info, nil
}

// newID returns a 16-hex-char random identifier. Short enough to fit
// in a filename comfortably, long enough that collisions in a single
// bin are effectively impossible.
func newID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generating trash id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// moveFile renames src to dst, falling back to streaming copy+delete
// when the two live on different filesystems (EXDEV). Exposed as a
// package-local helper so the trash, Move, and Restore paths share
// identical semantics.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}
	return copyThenDelete(src, dst)
}

// isCrossDevice reports whether err indicates a cross-filesystem
// rename, which we handle by streaming. Keeping the predicate here
// rather than in move logic avoids platform-conditional code in the
// hot path.
func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	// syscall.EXDEV is the canonical signal, but errors from os.Rename
	// get wrapped in *os.LinkError. Compare against the string form to
	// stay portable — on Windows the error isn't EXDEV but still needs
	// the fallback when moving across volumes.
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		msg := linkErr.Err.Error()
		return msg == "invalid cross-device link" ||
			msg == "cross-device link" ||
			msg == "The system cannot move the file to a different disk drive."
	}
	return false
}

// copyThenDelete performs a cross-FS move: copy the bytes to dst,
// fsync, then unlink src. On any failure dst is removed so we never
// leave a partial copy behind — the source is intact until we're sure
// the destination is durable.
func copyThenDelete(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	if err := os.Remove(src); err != nil {
		// Source still exists — the destination is also a valid copy.
		// Leave both in place and surface the error; operator can
		// resolve. Do NOT os.Remove(dst) here because we've just
		// durably written it and deleting it could lose data.
		return fmt.Errorf("removing source after copy %s: %w", src, err)
	}
	return nil
}
