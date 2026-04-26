package fileops

import (
	"fmt"

	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
	"github.com/WeaponizedLego/kestrel/internal/library"
)

// Undo reverses the most recent reversible operation. For moves that
// means renaming the files back to their origin. For trash deletes it
// means restoring each file from the trash bin. Each sub-item is
// attempted independently; a per-item failure surfaces in Results but
// doesn't abort the undo of the others.
//
// Returns ErrNothingToUndo when the stack is empty. The UI should
// check UndoDepth before offering an Undo button.
func (m *Manager) Undo() (UndoSummary, error) {
	if err := m.ensureScanIdle(); err != nil {
		return UndoSummary{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	op, ok := m.popUndo()
	if !ok {
		return UndoSummary{}, ErrNothingToUndo
	}

	results := make([]Result, 0, len(op.Items))
	kind := undoKindLabel(op.Kind)
	total := len(op.Items)
	m.publishStarted(kind, total)
	for i, item := range op.Items {
		switch op.Kind {
		case journal.KindMove:
			results = append(results, m.undoMoveItem(item))
		case journal.KindDelete:
			results = append(results, m.undoDeleteItem(item))
		default:
			results = append(results, Result{
				Path:  describeItem(item),
				Error: fmt.Sprintf("undo not supported for kind %q", op.Kind),
			})
		}
		m.publishProgress(kind, i+1, total)
	}

	if err := m.cfg.Persist(); err != nil {
		m.publish("fileops:persist-warning", map[string]any{
			"error": err.Error(),
		})
	}

	m.publish("fileops:undone", map[string]any{
		"kind":      op.Kind,
		"remaining": len(m.undoStack),
		"results":   results,
	})

	return UndoSummary{
		Undone:    op,
		Results:   results,
		Remaining: len(m.undoStack),
	}, nil
}

// undoMoveItem reverses a single-file move: rename dst → src, re-key
// the library. If the file is no longer where the move landed (e.g.
// the user moved it again outside Kestrel) the undo fails loudly
// rather than guessing.
func (m *Manager) undoMoveItem(item OperationItem) Result {
	res := Result{Path: item.NewPath, NewPath: item.OldPath}

	opID := newOpID()
	if err := m.cfg.Journal.Append(journal.Entry{
		ID:        opID,
		Kind:      journal.KindRestore,
		State:     journal.StatePending,
		OldPath:   item.NewPath,
		NewPath:   item.OldPath,
		PhotoHash: item.PhotoHash,
	}); err != nil {
		res.Error = fmt.Sprintf("journaling: %v", err)
		return res
	}

	if err := m.fs.Rename(item.NewPath, item.OldPath); err != nil {
		if IsCrossDevice(err) {
			if fallbackErr := crossFSMove(m.fs, item.NewPath, item.OldPath, true); fallbackErr != nil {
				m.logFailed(journal.Entry{
					ID: opID, Kind: journal.KindRestore,
					OldPath: item.NewPath, NewPath: item.OldPath,
				}, fallbackErr)
				res.Error = fallbackErr.Error()
				return res
			}
		} else {
			m.logFailed(journal.Entry{
				ID: opID, Kind: journal.KindRestore,
				OldPath: item.NewPath, NewPath: item.OldPath,
			}, err)
			res.Error = err.Error()
			return res
		}
	}

	if err := m.cfg.Library.RenamePhoto(item.NewPath, item.OldPath); err != nil {
		// Best-effort: put the file back where we found it.
		_ = m.fs.Rename(item.OldPath, item.NewPath)
		res.Error = fmt.Sprintf("library rekey: %v", err)
		return res
	}

	_ = m.cfg.Journal.Append(journal.Entry{
		ID:      opID,
		Kind:    journal.KindRestore,
		State:   journal.StateComplete,
		OldPath: item.NewPath,
		NewPath: item.OldPath,
	})
	res.Success = true
	return res
}

// undoDeleteItem restores a trashed file: pull it back out of the
// trash bin and re-add it to the library. The library entry we saved
// at delete time had a live pointer; we can't reuse it after a
// library.RemovePhoto, so we reconstruct a new Photo from the
// restored file's metadata via the library's AddPhoto. Fields we
// don't know (EXIF, dimensions) are left zero — a subsequent resync
// or scan will repopulate them. For immediate UX it's enough that the
// path+hash are present so the grid sees the file again.
func (m *Manager) undoDeleteItem(item OperationItem) Result {
	res := Result{Path: item.OriginalPath}

	opID := newOpID()
	if err := m.cfg.Journal.Append(journal.Entry{
		ID:        opID,
		Kind:      journal.KindRestore,
		State:     journal.StatePending,
		Path:      item.OriginalPath,
		PhotoHash: item.PhotoHash,
	}); err != nil {
		res.Error = fmt.Sprintf("journaling: %v", err)
		return res
	}

	info, err := m.cfg.Trash.Restore(item.TrashID)
	if err != nil {
		m.logFailed(journal.Entry{ID: opID, Kind: journal.KindRestore, Path: item.OriginalPath}, err)
		res.Error = err.Error()
		return res
	}

	m.cfg.Library.AddPhoto(&library.Photo{
		Path: info.OriginalPath,
		Name: baseName(info.OriginalPath),
		Hash: item.PhotoHash,
	})

	_ = m.cfg.Journal.Append(journal.Entry{
		ID:      opID,
		Kind:    journal.KindRestore,
		State:   journal.StateComplete,
		Path:    info.OriginalPath,
	})
	res.Success = true
	return res
}

// undoKindLabel maps a journal Kind to the user-visible string used
// in fileops:started / fileops:progress events. Unknown kinds fall
// back to a generic "undo" so the StatusBar still has something to
// render.
func undoKindLabel(k journal.Kind) string {
	switch k {
	case journal.KindMove:
		return "undo-move"
	case journal.KindDelete:
		return "undo-delete"
	default:
		return "undo"
	}
}

// describeItem returns a human-readable label for an OperationItem,
// used in error results when we can't classify the op kind.
func describeItem(item OperationItem) string {
	if item.OriginalPath != "" {
		return item.OriginalPath
	}
	if item.OldPath != "" {
		return item.OldPath
	}
	return item.NewPath
}

// baseName is filepath.Base without the import cycle worry; kept
// here so undoDeleteItem doesn't import path/filepath just for one
// call. Safe for absolute paths on all supported platforms.
func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
