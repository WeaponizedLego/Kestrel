package fileops

import (
	"fmt"
	"os"

	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
	"github.com/WeaponizedLego/kestrel/internal/fileops/trash"
	"github.com/WeaponizedLego/kestrel/internal/library"
)

// RecoveryReport summarises what Recover did on startup so the caller
// can log it or surface it to the user. Exposed as a plain struct
// rather than a series of side effects because the recovery log is
// the one piece of evidence that data-safety was actually maintained
// across a crash.
type RecoveryReport struct {
	Rolled    []journal.Entry
	Forwarded []journal.Entry
	Skipped   []journal.Entry
}

// Recover replays journalPath at startup — before the HTTP server
// starts accepting requests. For each pending entry we inspect the
// filesystem and decide one of:
//
//  1. The op had fully landed on disk but we crashed before marking
//     complete → forward the library to match (Forwarded).
//  2. The op hadn't landed → roll back: undo any partial state
//     (Rolled).
//  3. Ambiguous → leave it alone and log it (Skipped); the library
//     will be reconciled by the next resync/scan.
//
// After the decision, Recover appends a matching complete/failed
// record so the next replay sees the op as settled.
//
// The library is expected to be populated (loaded from
// library_meta.gob) before Recover runs. Trash bin must be open.
func Recover(journalPath string, lib *library.Library, bin *trash.Bin) (*RecoveryReport, error) {
	pending, err := journal.Replay(journalPath)
	if err != nil {
		return nil, fmt.Errorf("replaying journal %s: %w", journalPath, err)
	}
	if len(pending) == 0 {
		return &RecoveryReport{}, nil
	}

	j, err := journal.Open(journalPath)
	if err != nil {
		return nil, fmt.Errorf("reopening journal for recovery %s: %w", journalPath, err)
	}
	defer j.Close()

	report := &RecoveryReport{}
	for _, e := range pending {
		switch e.Kind {
		case journal.KindMove:
			recoverMove(e, lib, j, report)
		case journal.KindDelete:
			recoverDelete(e, lib, bin, j, report)
		case journal.KindPermDel:
			recoverPermDel(e, lib, j, report)
		case journal.KindRestore:
			// Restores are never partially-applied in a way we can
			// safely auto-finish — leave for the user to re-undo.
			report.Skipped = append(report.Skipped, e)
		default:
			report.Skipped = append(report.Skipped, e)
		}
	}
	return report, nil
}

// recoverMove finishes or rolls back a pending move.
func recoverMove(e journal.Entry, lib *library.Library, j *journal.Journal, report *RecoveryReport) {
	srcExists := fileExists(e.OldPath)
	dstExists := fileExists(e.NewPath)

	switch {
	case !srcExists && dstExists:
		// File is at the destination → op landed, we just didn't log
		// complete. Forward the library to match.
		if _, err := lib.GetPhoto(e.NewPath); err != nil {
			// Library still thinks it's at the old path; re-key.
			_ = lib.RenamePhoto(e.OldPath, e.NewPath)
		}
		_ = j.Append(journal.Entry{
			ID: e.ID, Kind: e.Kind, State: journal.StateComplete,
			OldPath: e.OldPath, NewPath: e.NewPath, PhotoHash: e.PhotoHash,
		})
		report.Forwarded = append(report.Forwarded, e)

	case srcExists && !dstExists:
		// Nothing happened — just mark failed so replay doesn't keep
		// re-considering it. Library is still consistent.
		_ = j.Append(journal.Entry{
			ID: e.ID, Kind: e.Kind, State: journal.StateFailed,
			OldPath: e.OldPath, NewPath: e.NewPath, PhotoHash: e.PhotoHash,
			Error: "crashed before move started",
		})
		report.Rolled = append(report.Rolled, e)

	case srcExists && dstExists:
		// Both exist — the cross-FS copy completed but the source
		// delete didn't. Delete the destination (the source is the
		// canonical original) so we're back to the pre-op state.
		_ = os.Remove(e.NewPath)
		_ = j.Append(journal.Entry{
			ID: e.ID, Kind: e.Kind, State: journal.StateFailed,
			OldPath: e.OldPath, NewPath: e.NewPath,
			Error: "partial cross-FS copy, destination removed",
		})
		report.Rolled = append(report.Rolled, e)

	default:
		// Neither exists — data is likely lost (or the user moved the
		// file themselves). Remove the library entry so it's not a
		// dangling reference, and log the anomaly.
		_, _ = lib.RemovePhoto(e.OldPath)
		_ = j.Append(journal.Entry{
			ID: e.ID, Kind: e.Kind, State: journal.StateFailed,
			OldPath: e.OldPath, NewPath: e.NewPath,
			Error: "both src and dst missing on recovery",
		})
		report.Skipped = append(report.Skipped, e)
	}
}

// recoverDelete finishes or rolls back a pending trash-delete.
func recoverDelete(e journal.Entry, lib *library.Library, bin *trash.Bin, j *journal.Journal, report *RecoveryReport) {
	// If the file is still at its original path, nothing happened —
	// leave library alone and mark failed.
	if fileExists(e.Path) {
		_ = j.Append(journal.Entry{
			ID: e.ID, Kind: e.Kind, State: journal.StateFailed,
			Path: e.Path, Error: "crashed before delete started",
		})
		report.Rolled = append(report.Rolled, e)
		return
	}

	// File is gone from original path. If it's in the trash, drop the
	// library entry to match. Search the trash by original path.
	list, err := bin.List()
	if err != nil {
		report.Skipped = append(report.Skipped, e)
		return
	}
	for _, info := range list {
		if info.OriginalPath == e.Path {
			_, _ = lib.RemovePhoto(e.Path)
			_ = j.Append(journal.Entry{
				ID: e.ID, Kind: e.Kind, State: journal.StateComplete,
				Path: e.Path, TrashPath: info.TrashPath, PhotoHash: e.PhotoHash,
			})
			report.Forwarded = append(report.Forwarded, e)
			return
		}
	}
	// File disappeared but isn't in the trash — probably user moved
	// it externally. Drop the library entry and skip.
	_, _ = lib.RemovePhoto(e.Path)
	_ = j.Append(journal.Entry{
		ID: e.ID, Kind: e.Kind, State: journal.StateFailed,
		Path: e.Path, Error: "file missing and not in trash on recovery",
	})
	report.Skipped = append(report.Skipped, e)
}

// recoverPermDel finishes a pending permanent delete. If the file is
// gone we drop the library entry; otherwise we mark failed.
func recoverPermDel(e journal.Entry, lib *library.Library, j *journal.Journal, report *RecoveryReport) {
	if fileExists(e.Path) {
		_ = j.Append(journal.Entry{
			ID: e.ID, Kind: e.Kind, State: journal.StateFailed,
			Path: e.Path, Error: "crashed before permanent delete",
		})
		report.Rolled = append(report.Rolled, e)
		return
	}
	_, _ = lib.RemovePhoto(e.Path)
	_ = j.Append(journal.Entry{
		ID: e.ID, Kind: e.Kind, State: journal.StateComplete,
		Path: e.Path, PhotoHash: e.PhotoHash,
	})
	report.Forwarded = append(report.Forwarded, e)
}

// fileExists reports whether path resolves to something stattable.
// Transient errors (permission) count as "exists" to avoid data loss
// when a drive is temporarily unreadable.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}
