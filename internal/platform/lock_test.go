package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireLock_FreshSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kestrel.lock")
	info := LockInfo{PID: os.Getpid(), URL: "http://127.0.0.1:1234"}

	ok, got, err := AcquireLock(path, info)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !ok {
		t.Fatal("AcquireLock returned ok=false on a fresh slot")
	}
	if got.URL != info.URL {
		t.Fatalf("got %+v, want %+v", got, info)
	}
}

func TestAcquireLock_LiveInstanceRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kestrel.lock")
	first := LockInfo{PID: os.Getpid(), URL: "http://127.0.0.1:1111"}

	if ok, _, err := AcquireLock(path, first); err != nil || !ok {
		t.Fatalf("first AcquireLock: ok=%v err=%v", ok, err)
	}

	second := LockInfo{PID: os.Getpid(), URL: "http://127.0.0.1:2222"}
	ok, existing, err := AcquireLock(path, second)
	if err != nil {
		t.Fatalf("second AcquireLock: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false on a live lock")
	}
	if existing.URL != first.URL {
		t.Fatalf("expected handoff URL %q, got %q", first.URL, existing.URL)
	}
}

func TestAcquireLock_StaleLockReclaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kestrel.lock")

	// Plant a lock pointing at a definitely-dead PID. PID 1 is init
	// on Unix and is always alive, so use a very large PID we don't
	// expect to exist on the test host.
	stale := LockInfo{PID: 0x7FFFFFFE, URL: "http://stale"}
	if ok, _, err := AcquireLock(path, stale); err != nil || !ok {
		t.Fatalf("planting stale lock: ok=%v err=%v", ok, err)
	}

	fresh := LockInfo{PID: os.Getpid(), URL: "http://fresh"}
	ok, got, err := AcquireLock(path, fresh)
	if err != nil {
		t.Fatalf("AcquireLock over stale: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true when reclaiming stale lock")
	}
	if got.URL != fresh.URL {
		t.Fatalf("got %+v, want %+v", got, fresh)
	}
}

func TestReleaseLock_MissingFileIsOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kestrel.lock")
	if err := ReleaseLock(path); err != nil {
		t.Fatalf("ReleaseLock on missing file: %v", err)
	}
}
