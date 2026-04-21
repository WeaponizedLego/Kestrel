package fileops

// invariants_test.go holds the named invariants that define Kestrel's
// data-safety contract for file operations. Each Test_Invariant_* is a
// single-sentence contract encoded as a test. If any of these fails,
// a regression has reached the branch — merging is blocked on CI until
// the invariant is restored.
//
// When adding a feature that touches this package, add (or update) the
// relevant invariant here. Do not weaken an invariant to make a test
// pass without discussing it with the team.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
	"github.com/WeaponizedLego/kestrel/internal/fileops/trash"
	"github.com/WeaponizedLego/kestrel/internal/library"
)

// testEnv bundles a ready-to-use Manager with fault-injection hooks.
type testEnv struct {
	t       *testing.T
	dir     string
	lib     *library.Library
	journal *journal.Journal
	trash   *trash.Bin
	fs      *testFS
	mgr     *Manager
	persistCalls *int
	persistErr   *error
}

// newTestEnv wires up a Manager over temp dirs. The caller can tweak
// env.fs.* or env.persistErr before invoking Manager methods.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()

	lib := library.New()
	j, err := journal.Open(filepath.Join(dir, "fileops.journal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })

	bin, err := trash.Open(filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}

	fs := &testFS{}
	persistCalls := 0
	var persistErr error

	mgr := New(Config{
		Library: lib,
		Journal: j,
		Trash:   bin,
		FS:      fs,
		Persist: func() error {
			persistCalls++
			return persistErr
		},
	})

	return &testEnv{
		t:            t,
		dir:          dir,
		lib:          lib,
		journal:      j,
		trash:        bin,
		fs:           fs,
		mgr:          mgr,
		persistCalls: &persistCalls,
		persistErr:   &persistErr,
	}
}

// seedPhoto creates a real file on disk and a matching library entry.
func (env *testEnv) seedPhoto(relPath, content string) *library.Photo {
	env.t.Helper()
	abs := filepath.Join(env.dir, "library", relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		env.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		env.t.Fatal(err)
	}
	p := &library.Photo{
		Path:      abs,
		Name:      filepath.Base(abs),
		SizeBytes: int64(len(content)),
		Hash:      "hash-" + relPath,
	}
	env.lib.AddPhoto(p)
	return p
}

// destDir makes a fresh subdirectory to serve as a move target.
func (env *testEnv) destDir(name string) string {
	env.t.Helper()
	d := filepath.Join(env.dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		env.t.Fatal(err)
	}
	return d
}

// -----------------------------------------------------------------
// Invariant 1: no filesystem unlink/rename happens without a matching
// *-pending journal entry written first.
// -----------------------------------------------------------------
func Test_Invariant_NoUnlinkWithoutJournal(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "hello")
	origPath := p.Path // capture before Move mutates p in place

	_, err := env.mgr.Move([]string{origPath}, MoveOptions{Dest: env.destDir("out")})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}

	// The journal file must contain a pending record with OldPath=origPath
	// dated before the file stopped existing at its old location. We
	// verify the weaker structural claim: a replay of the journal sees
	// a complete record whose OldPath matches.
	data, err := os.ReadFile(env.journal.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(string(data), `"state":"pending"`, `"old_path":"`+origPath+`"`) {
		t.Fatalf("journal missing pending record for %q:\n%s", origPath, data)
	}
	if !containsAll(string(data), `"state":"complete"`, `"old_path":"`+origPath+`"`) {
		t.Fatalf("journal missing complete record for %q:\n%s", origPath, data)
	}
}

// -----------------------------------------------------------------
// Invariant 2: the library never removes or re-keys a photo unless
// the filesystem op has returned success.
// -----------------------------------------------------------------
func Test_Invariant_NoLibraryForgetWithoutFile(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "hello")

	env.fs.FailRenameOn = 1 // same-FS rename will fail

	results, err := env.mgr.Move([]string{p.Path}, MoveOptions{Dest: env.destDir("out")})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Fatalf("expected failure, got %+v", results)
	}
	// Library must still see the photo at its original path.
	if _, err := env.lib.GetPhoto(p.Path); err != nil {
		t.Fatalf("library forgot photo despite filesystem failure: %v", err)
	}
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatalf("source file missing despite failed move: %v", err)
	}
}

// -----------------------------------------------------------------
// Invariant 3: after any successful op, there is no file on disk
// that the library no longer knows about. Covered for move + trash
// delete + permanent delete.
// -----------------------------------------------------------------
func Test_Invariant_NoOrphanedFile(t *testing.T) {
	t.Run("move", func(t *testing.T) {
		env := newTestEnv(t)
		p := env.seedPhoto("a.jpg", "hello")
		origPath := p.Path
		res, err := env.mgr.Move([]string{origPath}, MoveOptions{Dest: env.destDir("out")})
		if err != nil || !res[0].Success {
			t.Fatalf("move failed: %+v %v", res, err)
		}
		// Source gone; destination known to library.
		if _, err := os.Stat(origPath); !os.IsNotExist(err) {
			t.Fatalf("source still exists after move: %v", err)
		}
		if _, err := env.lib.GetPhoto(res[0].NewPath); err != nil {
			t.Fatalf("library doesn't know about moved file: %v", err)
		}
	})
	t.Run("trash", func(t *testing.T) {
		env := newTestEnv(t)
		p := env.seedPhoto("a.jpg", "hello")
		origPath := p.Path
		res, err := env.mgr.Delete([]string{origPath}, DeleteOptions{})
		if err != nil || !res[0].Success {
			t.Fatalf("delete failed: %+v %v", res, err)
		}
		if _, err := os.Stat(origPath); !os.IsNotExist(err) {
			t.Fatalf("source still exists after trash: %v", err)
		}
	})
	t.Run("permanent", func(t *testing.T) {
		env := newTestEnv(t)
		p := env.seedPhoto("a.jpg", "hello")
		origPath := p.Path
		res, err := env.mgr.Delete([]string{origPath}, DeleteOptions{Permanent: true})
		if err != nil || !res[0].Success {
			t.Fatalf("perm delete failed: %+v %v", res, err)
		}
		if _, err := os.Stat(origPath); !os.IsNotExist(err) {
			t.Fatalf("source still exists after permanent delete: %v", err)
		}
	})
}

// -----------------------------------------------------------------
// Invariant 4: after any successful op, no library entry points to a
// path that doesn't exist on disk.
// -----------------------------------------------------------------
func Test_Invariant_NoOrphanedLibraryEntry(t *testing.T) {
	env := newTestEnv(t)
	p1 := env.seedPhoto("a.jpg", "1")
	p2 := env.seedPhoto("b.jpg", "2")

	// Mix of successful move and successful delete.
	_, _ = env.mgr.Move([]string{p1.Path}, MoveOptions{Dest: env.destDir("out")})
	_, _ = env.mgr.Delete([]string{p2.Path}, DeleteOptions{})

	for _, photo := range env.lib.AllPhotos() {
		if _, err := os.Stat(photo.Path); err != nil {
			t.Fatalf("library references non-existent path %q: %v", photo.Path, err)
		}
	}
}

// -----------------------------------------------------------------
// Invariant 5: killing the process mid-op (simulated by running the
// op with an injected failure after the FS step and before the
// complete-journal step) leaves the system in a recoverable state.
// Recover() must reconcile without losing data.
// -----------------------------------------------------------------
func Test_Invariant_CrashMidOpIsRecoverable(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "payload")
	origPath := p.Path
	dst := env.destDir("out")
	newPath := filepath.Join(dst, "a.jpg")

	// Pre-journal a pending move manually, then perform the FS op
	// ourselves, then close+recover. This simulates "journal + fs
	// landed, SIGKILL before complete."
	pendingID := "crash-1"
	if err := env.journal.Append(journal.Entry{
		ID: pendingID, Kind: journal.KindMove, State: journal.StatePending,
		OldPath: origPath, NewPath: newPath, PhotoHash: p.Hash,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(origPath, newPath); err != nil {
		t.Fatal(err)
	}
	env.journal.Close()

	// Now a fresh startup: library is still at the old key, journal
	// has a pending. Recover should forward the library.
	report, err := Recover(filepath.Join(env.dir, "fileops.journal"), env.lib, env.trash)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(report.Forwarded) != 1 {
		t.Fatalf("expected 1 forwarded, got %+v", report)
	}
	if _, err := env.lib.GetPhoto(newPath); err != nil {
		t.Fatalf("library not forwarded to new path: %v", err)
	}
	if _, err := env.lib.GetPhoto(origPath); err == nil {
		t.Fatalf("library still holds old path after recovery")
	}
}

// -----------------------------------------------------------------
// Invariant 6: cross-FS move with verify detects a corrupted copy
// and refuses to delete the source.
// -----------------------------------------------------------------
func Test_Invariant_ChecksumVerifiedCopyIsByteIdentical(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "important-data")

	env.fs.ForceEXDEV = true  // force the copy path
	env.fs.CorruptCopy = true // corrupt the destination

	results, err := env.mgr.Move([]string{p.Path}, MoveOptions{
		Dest:   env.destDir("out"),
		Verify: true,
	})
	if err != nil {
		t.Fatalf("Move returned batch error: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Fatalf("expected verify to fail, got %+v", results)
	}
	// Source must be untouched.
	data, err := os.ReadFile(p.Path)
	if err != nil {
		t.Fatalf("source file missing after failed verify: %v", err)
	}
	if string(data) != "important-data" {
		t.Fatalf("source corrupted: %q", data)
	}
	// Library must still reference the source path.
	if _, err := env.lib.GetPhoto(p.Path); err != nil {
		t.Fatalf("library lost photo despite verify failure: %v", err)
	}
}

// -----------------------------------------------------------------
// Invariant 7: permanent delete requires an explicit flag. A default
// Delete call must never unlink a file.
// -----------------------------------------------------------------
func Test_Invariant_PermanentDeleteRequiresExplicitFlag(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "hello")

	res, err := env.mgr.Delete([]string{p.Path}, DeleteOptions{})
	if err != nil || !res[0].Success {
		t.Fatalf("default delete failed: %+v %v", res, err)
	}
	// The file must still exist somewhere (in the trash bin), not be
	// unlinked.
	list, _ := env.trash.List()
	if len(list) != 1 {
		t.Fatalf("default delete should route to trash, got %+v", list)
	}
	if _, err := os.Stat(list[0].TrashPath); err != nil {
		t.Fatalf("trashed file should exist at TrashPath: %v", err)
	}
}

// -----------------------------------------------------------------
// Invariant 8: file ops are rejected while a scan is active.
// -----------------------------------------------------------------
func Test_Invariant_ScanAndFileOpsAreExclusive(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "hello")

	active := true
	env.mgr.cfg.ScanActive = func() bool { return active }

	_, err := env.mgr.Move([]string{p.Path}, MoveOptions{Dest: env.destDir("out")})
	if err == nil {
		t.Fatalf("expected ErrScanInProgress, got nil")
	}
	_, err = env.mgr.Delete([]string{p.Path}, DeleteOptions{})
	if err == nil {
		t.Fatalf("expected ErrScanInProgress on delete, got nil")
	}
	_, err = env.mgr.Undo()
	if err == nil {
		t.Fatalf("expected ErrScanInProgress on undo, got nil")
	}

	active = false
	res, err := env.mgr.Move([]string{p.Path}, MoveOptions{Dest: env.destDir("out")})
	if err != nil || !res[0].Success {
		t.Fatalf("move after scan idle should succeed: %+v %v", res, err)
	}
}

// -----------------------------------------------------------------
// Invariant 9: a partial-batch failure does not corrupt the
// successful items' state; the batch continues past the failure.
// -----------------------------------------------------------------
func Test_Invariant_BatchPartialFailureDoesNotPoison(t *testing.T) {
	env := newTestEnv(t)
	p1 := env.seedPhoto("a.jpg", "1")
	p2 := env.seedPhoto("b.jpg", "2")
	p3 := env.seedPhoto("c.jpg", "3")
	orig1, orig2, orig3 := p1.Path, p2.Path, p3.Path

	// Inject a failure on the 2nd rename call.
	env.fs.FailRenameOn = 2

	res, err := env.mgr.Move([]string{orig1, orig2, orig3}, MoveOptions{
		Dest: env.destDir("out"),
	})
	if err != nil {
		t.Fatalf("batch should not error wholesale: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("expected 3 results, got %d", len(res))
	}
	if !res[0].Success {
		t.Fatalf("first item should succeed: %+v", res[0])
	}
	if res[1].Success {
		t.Fatalf("second item should fail: %+v", res[1])
	}
	if !res[2].Success {
		t.Fatalf("third item should still succeed past the failure: %+v", res[2])
	}
	// Failed item's source must still exist; successful items' sources
	// must be gone.
	if _, err := os.Stat(orig2); err != nil {
		t.Fatalf("failed item's source missing: %v", err)
	}
	if _, err := os.Stat(orig1); !os.IsNotExist(err) {
		t.Fatalf("succeeded item's source still exists")
	}
	if _, err := os.Stat(orig3); !os.IsNotExist(err) {
		t.Fatalf("third succeeded item's source still exists")
	}
}

// -----------------------------------------------------------------
// Invariant 10: undo of a move returns the library and filesystem to
// a state equivalent to pre-move (path + library entry).
// -----------------------------------------------------------------
func Test_Invariant_UndoIsInverse(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPhoto("a.jpg", "payload")
	origPath := p.Path

	res, err := env.mgr.Move([]string{origPath}, MoveOptions{Dest: env.destDir("out")})
	if err != nil || !res[0].Success {
		t.Fatalf("move failed: %+v %v", res, err)
	}

	summary, err := env.mgr.Undo()
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(summary.Results) != 1 || !summary.Results[0].Success {
		t.Fatalf("undo failed: %+v", summary)
	}
	// Back to original: library entry at origPath, file bytes intact.
	if _, err := env.lib.GetPhoto(origPath); err != nil {
		t.Fatalf("library not restored to original path: %v", err)
	}
	data, err := os.ReadFile(origPath)
	if err != nil {
		t.Fatalf("file not restored at original path: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("file content changed after undo: %q", data)
	}
}

// containsAll reports whether haystack contains every needle.
func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !contains(haystack, n) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
