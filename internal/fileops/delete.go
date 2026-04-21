package fileops

import (
	"errors"
	"fmt"

	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
)

// DeleteOptions tune a Delete call. Permanent=false is the default
// and routes to the Kestrel trash bin; Permanent=true os.Removes the
// file. The HTTP layer gates Permanent behind explicit confirmation.
type DeleteOptions struct {
	Permanent bool
}

// Delete removes every path in paths. When Permanent is false (the
// default) files are moved into the trash bin and become
// undo-restorable. When Permanent is true the files are unlinked and
// cannot be undone — the undo stack is NOT updated.
//
// Per-file failures are reported via Result.Error; the batch
// continues to the next path.
func (m *Manager) Delete(paths []string, opts DeleteOptions) ([]Result, error) {
	if err := m.ensureScanIdle(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("paths is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	results := make([]Result, 0, len(paths))
	undoItems := make([]OperationItem, 0, len(paths))

	for _, path := range paths {
		if opts.Permanent {
			res := m.deleteOnePermanent(path)
			results = append(results, res)
			continue
		}
		res, item, ok := m.deleteOneToTrash(path)
		results = append(results, res)
		if ok {
			undoItems = append(undoItems, item)
		}
	}

	if err := m.cfg.Persist(); err != nil {
		m.publish("fileops:persist-warning", map[string]any{
			"error": err.Error(),
		})
	}

	// Only trash deletes are undoable. Permanent deletes deliberately
	// produce no undo record — surfacing "undo" after an unlink would
	// be a dangerous UX lie.
	if len(undoItems) > 0 {
		m.pushUndo(Operation{
			ID:    newOpID(),
			Kind:  journal.KindDelete,
			Items: undoItems,
		})
	}

	successes, failures := countResults(results)
	kind := "delete"
	if opts.Permanent {
		kind = "permanent-delete"
	}
	m.publish("fileops:done", map[string]any{
		"kind":    kind,
		"deleted": successes,
		"failed":  failures,
		"results": results,
	})
	return results, nil
}

// deleteOneToTrash trashes a single file. The transaction order is:
// journal pending → move file into trash bin → remove from library
// → journal complete. A failure at any step rolls the earlier ones
// back so the four stores (journal / fs / library / persistence) stay
// consistent.
func (m *Manager) deleteOneToTrash(path string) (Result, OperationItem, bool) {
	res := Result{Path: path}

	photo, err := m.cfg.Library.GetPhoto(path)
	if err != nil {
		res.Error = fmt.Sprintf("not in library: %v", err)
		return res, OperationItem{}, false
	}

	opID := newOpID()
	pending := journal.Entry{
		ID:        opID,
		Kind:      journal.KindDelete,
		State:     journal.StatePending,
		Path:      path,
		PhotoHash: photo.Hash,
	}
	if err := m.cfg.Journal.Append(pending); err != nil {
		res.Error = fmt.Sprintf("journaling: %v", err)
		return res, OperationItem{}, false
	}

	info, err := m.cfg.Trash.Put(path, photo.Hash)
	if err != nil {
		m.logFailed(pending, err)
		res.Error = err.Error()
		return res, OperationItem{}, false
	}

	// Filesystem is committed to trash. Drop from library; if it
	// fails, restore from trash so the library and FS stay in sync.
	if _, err := m.cfg.Library.RemovePhoto(path); err != nil {
		if _, rbErr := m.cfg.Trash.Restore(info.ID); rbErr != nil {
			m.logFailed(pending, fmt.Errorf("library error %v, trash restore failed: %w", err, rbErr))
			res.Error = fmt.Sprintf("library+restore: %v (restore: %v)", err, rbErr)
			return res, OperationItem{}, false
		}
		m.logFailed(pending, err)
		res.Error = err.Error()
		return res, OperationItem{}, false
	}

	if err := m.cfg.Journal.Append(journal.Entry{
		ID:        opID,
		Kind:      journal.KindDelete,
		State:     journal.StateComplete,
		Path:      path,
		TrashPath: info.TrashPath,
		PhotoHash: photo.Hash,
	}); err != nil {
		m.publish("fileops:journal-warning", map[string]any{
			"id":    opID,
			"error": err.Error(),
		})
	}

	m.publish("photo:deleted", map[string]any{
		"path":     path,
		"trash_id": info.ID,
	})
	res.Success = true
	res.TrashID = info.ID
	return res, OperationItem{
		OriginalPath: path,
		TrashID:      info.ID,
		PhotoHash:    photo.Hash,
	}, true
}

// deleteOnePermanent is the unrecoverable path. We still journal the
// intent so an operator can see what happened, but there is no undo
// and no rollback — once the filesystem unlink returns, the file is
// gone.
func (m *Manager) deleteOnePermanent(path string) Result {
	res := Result{Path: path}

	photo, err := m.cfg.Library.GetPhoto(path)
	if err != nil {
		res.Error = fmt.Sprintf("not in library: %v", err)
		return res
	}

	opID := newOpID()
	pending := journal.Entry{
		ID:        opID,
		Kind:      journal.KindPermDel,
		State:     journal.StatePending,
		Path:      path,
		PhotoHash: photo.Hash,
	}
	if err := m.cfg.Journal.Append(pending); err != nil {
		res.Error = fmt.Sprintf("journaling: %v", err)
		return res
	}

	if err := m.fs.Remove(path); err != nil {
		m.logFailed(pending, err)
		res.Error = err.Error()
		return res
	}

	if _, err := m.cfg.Library.RemovePhoto(path); err != nil {
		// The file is gone — we can't undo that. Log failed but keep
		// going; the library will be rewoven on next resync/scan.
		m.logFailed(pending, fmt.Errorf("file removed but library update failed: %w", err))
		res.Error = fmt.Sprintf("library: %v", err)
		return res
	}

	if err := m.cfg.Journal.Append(journal.Entry{
		ID:        opID,
		Kind:      journal.KindPermDel,
		State:     journal.StateComplete,
		Path:      path,
		PhotoHash: photo.Hash,
	}); err != nil {
		m.publish("fileops:journal-warning", map[string]any{
			"id":    opID,
			"error": err.Error(),
		})
	}

	m.publish("photo:deleted", map[string]any{
		"path":      path,
		"permanent": true,
	})
	res.Success = true
	return res
}
