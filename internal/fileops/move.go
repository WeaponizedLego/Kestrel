package fileops

import (
	"errors"
	"fmt"

	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
	"github.com/WeaponizedLego/kestrel/internal/library"
)

// MoveOptions tune a Move call.
type MoveOptions struct {
	// Dest is the absolute destination directory. Files are moved in
	// preserving their basename; collisions fail per-file rather than
	// being silently renamed.
	Dest string
	// Verify enables SHA-256 verification on every cross-filesystem
	// copy. Same-filesystem moves use os.Rename and aren't affected
	// by this flag — they're already atomic at the kernel level.
	Verify bool
}

// Move relocates every path in paths into opts.Dest. Each file is
// moved in its own journaled transaction; a failure on one file does
// not abort the batch. Returns a Result per input path, in input
// order, and an error if the whole batch was rejected at the
// precondition stage (scan active, missing inputs, etc.).
func (m *Manager) Move(paths []string, opts MoveOptions) ([]Result, error) {
	if err := m.ensureScanIdle(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("paths is required")
	}
	if opts.Dest == "" {
		return nil, errors.New("dest is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	results := make([]Result, 0, len(paths))
	undoItems := make([]OperationItem, 0, len(paths))

	for _, src := range paths {
		res, item, ok := m.moveOne(src, opts)
		results = append(results, res)
		if ok {
			undoItems = append(undoItems, item)
		}
	}

	// Persist once at the end of the batch. If persist fails, log it
	// but don't roll back — the filesystem is the source of truth, and
	// the next auto-save tick or shutdown flush will catch up. The
	// journal carries the pending→complete trail regardless.
	if err := m.cfg.Persist(); err != nil {
		m.publish("fileops:persist-warning", map[string]any{
			"error": err.Error(),
		})
	}

	if len(undoItems) > 0 {
		op := Operation{
			ID:    newOpID(),
			Kind:  journal.KindMove,
			Items: undoItems,
		}
		m.pushUndo(op)
	}

	successes, failures := countResults(results)
	m.publish("fileops:done", map[string]any{
		"kind":    "move",
		"moved":   successes,
		"failed":  failures,
		"results": results,
	})
	return results, nil
}

// moveOne executes one file's move-and-journal cycle. Returns the
// user-visible Result, the undo record (only valid when ok), and a
// success flag. Never returns an error: per-file failures surface via
// Result.Error so a batch can continue.
func (m *Manager) moveOne(src string, opts MoveOptions) (Result, OperationItem, bool) {
	res := Result{Path: src}

	photo, err := m.cfg.Library.GetPhoto(src)
	if err != nil {
		res.Error = fmt.Sprintf("not in library: %v", err)
		return res, OperationItem{}, false
	}

	dst, err := m.destinationFor(src, opts.Dest)
	if err != nil {
		res.Error = err.Error()
		return res, OperationItem{}, false
	}

	opID := newOpID()
	pending := journal.Entry{
		ID:        opID,
		Kind:      journal.KindMove,
		State:     journal.StatePending,
		OldPath:   src,
		NewPath:   dst,
		PhotoHash: photo.Hash,
	}
	if err := m.cfg.Journal.Append(pending); err != nil {
		res.Error = fmt.Sprintf("journaling: %v", err)
		return res, OperationItem{}, false
	}

	// Fast path: atomic rename on same filesystem. On EXDEV we fall
	// through to the streaming copy path with optional checksum
	// verification.
	moveErr := m.fs.Rename(src, dst)
	if moveErr != nil && IsCrossDevice(moveErr) {
		moveErr = crossFSMove(m.fs, src, dst, opts.Verify)
	} else if moveErr == nil && opts.Verify {
		// Same-FS rename with verify toggle: the source is already gone
		// so there's nothing to compare against. We treat verify on a
		// rename as "trust the kernel" rather than complicating the
		// path with a pre-move checksum.
	}

	if moveErr != nil {
		m.logFailed(pending, moveErr)
		res.Error = moveErr.Error()
		return res, OperationItem{}, false
	}

	// Filesystem succeeded → re-key the library. A collision here is
	// effectively impossible (we checked Stat before the rename) but
	// we still handle it: roll the filesystem back, mark failed.
	if err := m.cfg.Library.RenamePhoto(src, dst); err != nil {
		// Roll forward: move the file back to its original home.
		if rbErr := m.fs.Rename(dst, src); rbErr != nil {
			// Both worlds are now broken. Log loudly; the journal
			// keeps the record so an operator can reconcile.
			m.logFailed(pending, fmt.Errorf("rollback failed after library error %v: %w", err, rbErr))
			res.Error = fmt.Sprintf("library+rollback: %v (rollback: %v)", err, rbErr)
			return res, OperationItem{}, false
		}
		m.logFailed(pending, err)
		res.Error = err.Error()
		return res, OperationItem{}, false
	}

	if err := m.cfg.Journal.Append(journal.Entry{
		ID:        opID,
		Kind:      journal.KindMove,
		State:     journal.StateComplete,
		OldPath:   src,
		NewPath:   dst,
		PhotoHash: photo.Hash,
	}); err != nil {
		// Library + FS succeeded but we couldn't mark complete.
		// That's survivable — replay will see a "pending" with the
		// library already in the new state, and can no-op. Log but
		// don't fail the user-visible op.
		m.publish("fileops:journal-warning", map[string]any{
			"id":    opID,
			"error": err.Error(),
		})
	}

	m.publish("photo:moved", map[string]any{
		"old_path": src,
		"new_path": dst,
	})
	res.Success = true
	res.NewPath = dst
	return res, OperationItem{OldPath: src, NewPath: dst, PhotoHash: photo.Hash}, true
}

// logFailed writes a "failed" journal record so post-mortem tooling
// can see the full transition. Swallows its own error — a broken
// journal at this point doesn't change the outcome for the user.
func (m *Manager) logFailed(pending journal.Entry, cause error) {
	_ = m.cfg.Journal.Append(journal.Entry{
		ID:        pending.ID,
		Kind:      pending.Kind,
		State:     journal.StateFailed,
		OldPath:   pending.OldPath,
		NewPath:   pending.NewPath,
		Path:      pending.Path,
		TrashPath: pending.TrashPath,
		PhotoHash: pending.PhotoHash,
		Error:     cause.Error(),
	})
}

// countResults sums successes and failures for event payloads.
func countResults(results []Result) (success, failure int) {
	for _, r := range results {
		if r.Success {
			success++
		} else {
			failure++
		}
	}
	return success, failure
}

// ensureLibraryUntouched is a helper for recovery: when we hit a
// filesystem error, we need to guarantee the library wasn't mutated.
// The library methods don't mutate on their own errors, so this is a
// cheap sanity-check rather than a rollback — kept for documentation.
//
//nolint:unused // kept for future refactors that might change ordering
func ensureLibraryUntouched(lib *library.Library, path string) bool {
	_, err := lib.GetPhoto(path)
	return err == nil
}
