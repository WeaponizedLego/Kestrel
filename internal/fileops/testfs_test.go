package fileops

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
	"syscall"
)

// testFS wraps a real filesystem with fault-injection hooks. Tests
// set FailOn* to trigger a simulated syscall failure after N
// successful calls; ForceEXDEV causes every Rename to masquerade as
// a cross-device link so we can exercise the copy+verify path
// without a real multi-mount setup.
//
// One testFS per test. Every field is pointer-based (atomic counters)
// so concurrent use is fine, but the tests don't need it.
type testFS struct {
	real realFS

	// Counters incremented on every call, regardless of outcome.
	RenameCalls int64
	RemoveCalls int64
	CopyCalls   int64

	// Fail the Nth call (1-indexed). Zero means "don't fail."
	FailRenameOn int64
	FailRemoveOn int64
	FailCopyOn   int64

	// ForceEXDEV makes every Rename return syscall.EXDEV. Used to
	// prove the cross-FS fallback path works without a real cross-FS
	// setup.
	ForceEXDEV bool

	// CorruptCopy flips one byte in each CopyFile destination to
	// prove the checksum-verify path catches corruption. Only active
	// when ForceEXDEV is also true (corruption only matters on the
	// cross-FS path which runs verification).
	CorruptCopy bool
}

func (t *testFS) Rename(old, new string) error {
	n := atomic.AddInt64(&t.RenameCalls, 1)
	if t.FailRenameOn != 0 && n == t.FailRenameOn {
		return &os.LinkError{Op: "rename", Old: old, New: new, Err: errors.New("injected rename failure")}
	}
	if t.ForceEXDEV {
		return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
	}
	return t.real.Rename(old, new)
}

func (t *testFS) Remove(path string) error {
	n := atomic.AddInt64(&t.RemoveCalls, 1)
	if t.FailRemoveOn != 0 && n == t.FailRemoveOn {
		return errors.New("injected remove failure")
	}
	return t.real.Remove(path)
}

func (t *testFS) Stat(p string) (os.FileInfo, error) { return t.real.Stat(p) }
func (t *testFS) MkdirAll(p string, m os.FileMode) error {
	return t.real.MkdirAll(p, m)
}
func (t *testFS) Open(p string) (io.ReadCloser, error)   { return t.real.Open(p) }
func (t *testFS) Create(p string) (io.WriteCloser, error) { return t.real.Create(p) }

func (t *testFS) CopyFile(src, dst string) error {
	n := atomic.AddInt64(&t.CopyCalls, 1)
	if t.FailCopyOn != 0 && n == t.FailCopyOn {
		return errors.New("injected copy failure")
	}
	if err := t.real.CopyFile(src, dst); err != nil {
		return err
	}
	if t.CorruptCopy {
		// Flip a byte in the destination after a successful copy so
		// Checksum-based verification fails.
		f, err := os.OpenFile(dst, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		buf := []byte{0}
		if _, err := f.ReadAt(buf, 0); err != nil {
			return err
		}
		buf[0] ^= 0xFF
		if _, err := f.WriteAt(buf, 0); err != nil {
			return err
		}
	}
	return nil
}

func (t *testFS) Checksum(p string) (string, error) { return t.real.Checksum(p) }
