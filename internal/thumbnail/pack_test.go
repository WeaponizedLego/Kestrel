package thumbnail

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func hash(s string) [packHashSize]byte { return sha256.Sum256([]byte(s)) }

func TestPack_PutGet(t *testing.T) {
	p := openFresh(t)

	want := []byte("pretend-jpeg-bytes-1")
	if err := p.Put(hash("a"), want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := p.Get(hash("a"))
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get returned %q, want %q", got, want)
	}

	if _, ok, _ := p.Get(hash("missing")); ok {
		t.Fatal("Get reported hit on missing hash")
	}
}

func TestPack_SaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thumbs.pack")

	p, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{
		"a": []byte("AAAA"),
		"b": bytes.Repeat([]byte{0xAB}, 100),
		"c": []byte{},
	}
	for k, v := range entries {
		if err := p.Put(hash(k), v); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Log file must be gone after a clean Close — before we reopen,
	// which would recreate an empty one.
	if _, err := os.Stat(path + ".log"); !os.IsNotExist(err) {
		t.Fatalf(".log should not exist after Close, stat err = %v", err)
	}

	// Reopen and verify everything survived as a pack-source entry.
	q, err := Open(path)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer q.Close()

	if q.Len() != len(entries) {
		t.Fatalf("Len = %d, want %d", q.Len(), len(entries))
	}
	for k, want := range entries {
		got, ok, err := q.Get(hash(k))
		if err != nil || !ok {
			t.Fatalf("Get %q: ok=%v err=%v", k, ok, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get %q returned %d bytes, want %d", k, len(got), len(want))
		}
	}
}

func TestPack_LogSurvivesCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thumbs.pack")

	// Write one Put then "crash" — skip Close so the log file stays.
	p, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("survive-me")
	if err := p.Put(hash("x"), want); err != nil {
		t.Fatal(err)
	}
	// Simulate crash: close file handles without Save/Close.
	p.packFile = nil
	if p.logFile != nil {
		p.logFile.Close()
		p.logFile = nil
	}

	if _, err := os.Stat(path + ".log"); err != nil {
		t.Fatalf("log file should exist after Put: %v", err)
	}

	// Recovery: Open should replay the log.
	q, err := Open(path)
	if err != nil {
		t.Fatalf("Reopen after crash: %v", err)
	}
	defer q.Close()

	got, ok, err := q.Get(hash("x"))
	if err != nil || !ok {
		t.Fatalf("Get after replay: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("replayed bytes = %q, want %q", got, want)
	}
}

func TestPack_OpenBadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thumbs.pack")
	// Write enough bytes to pass the header read but with wrong magic.
	if err := os.WriteFile(path, []byte("NOPE\x00\x00\x00\x01\x00\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("not a kestrel thumbnail pack")) {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
}

func TestPack_Overwrite(t *testing.T) {
	p := openFresh(t)
	defer p.Close()

	h := hash("k")
	if err := p.Put(h, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := p.Put(h, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, _, err := p.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("second")) {
		t.Fatalf("got %q, want %q", got, "second")
	}
	if p.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after overwrite", p.Len())
	}
}

func openFresh(t *testing.T) *Pack {
	t.Helper()
	path := filepath.Join(t.TempDir(), "thumbs.pack")
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}
