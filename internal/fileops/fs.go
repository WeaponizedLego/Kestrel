package fileops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// FileSystem is the thin abstraction every filesystem-touching
// operation goes through. In production it's backed by realFS (direct
// os.* calls); in tests the fileops package injects a wrapper that
// counts calls, fails on the nth invocation, or simulates EXDEV. The
// invariant tests rely on this seam — without it we couldn't prove
// that a mid-op crash leaves the library+fs consistent.
type FileSystem interface {
	Rename(old, new string) error
	Remove(path string) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Open(path string) (io.ReadCloser, error)
	Create(path string) (io.WriteCloser, error)
	// CopyFile streams src to dst with fsync. Kept on the interface
	// (rather than built from Open/Create in callers) so tests can
	// inject a mid-copy corruption to prove the checksum-verify path
	// catches it.
	CopyFile(src, dst string) error
	// Checksum returns the SHA-256 of the file at path as lowercase
	// hex. Used for verified cross-FS moves and invariant tests.
	Checksum(path string) (string, error)
}

// realFS is the production FileSystem. Every method is a direct pass
// through to os / io; nothing tricky lives here.
type realFS struct{}

// DefaultFS is the FileSystem used by Manager when no test override
// is supplied.
var DefaultFS FileSystem = realFS{}

func (realFS) Rename(old, new string) error { return os.Rename(old, new) }
func (realFS) Remove(path string) error     { return os.Remove(path) }
func (realFS) Stat(p string) (os.FileInfo, error) {
	return os.Stat(p)
}
func (realFS) MkdirAll(p string, m os.FileMode) error {
	return os.MkdirAll(p, m)
}
func (realFS) Open(p string) (io.ReadCloser, error) {
	return os.Open(p)
}
func (realFS) Create(p string) (io.WriteCloser, error) {
	return os.Create(p)
}
func (realFS) CopyFile(src, dst string) error {
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
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
func (realFS) Checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IsCrossDevice reports whether err is an EXDEV (cross-filesystem
// rename) error. Matches on syscall.EXDEV when available and on the
// error string for portable fallback — some wrapped LinkErrors lose
// the numeric code.
func IsCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		if errors.Is(linkErr.Err, syscall.EXDEV) {
			return true
		}
		msg := linkErr.Err.Error()
		return msg == "invalid cross-device link" ||
			msg == "cross-device link" ||
			msg == "The system cannot move the file to a different disk drive."
	}
	return errors.Is(err, syscall.EXDEV)
}

// crossFSMove performs a cross-filesystem rename via copy+verify+
// delete. verify controls whether a post-copy SHA-256 check is done;
// it's on by default for cross-FS because the copy is the risky part.
// If verification fails, the destination is removed and the source is
// left untouched.
func crossFSMove(fs FileSystem, src, dst string, verify bool) error {
	if err := fs.CopyFile(src, dst); err != nil {
		return fmt.Errorf("copying %s to %s: %w", src, dst, err)
	}
	if verify {
		a, err := fs.Checksum(src)
		if err != nil {
			_ = fs.Remove(dst)
			return fmt.Errorf("checksumming source %s: %w", src, err)
		}
		b, err := fs.Checksum(dst)
		if err != nil {
			_ = fs.Remove(dst)
			return fmt.Errorf("checksumming destination %s: %w", dst, err)
		}
		if a != b {
			_ = fs.Remove(dst)
			return fmt.Errorf("checksum mismatch copying %s: src=%s dst=%s", src, a, b)
		}
	}
	if err := fs.Remove(src); err != nil {
		return fmt.Errorf("removing source %s after copy: %w", src, err)
	}
	return nil
}
