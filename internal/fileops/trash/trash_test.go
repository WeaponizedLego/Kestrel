package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPutAndRestore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photos", "a.jpg")
	writeFile(t, src, "image-bytes")

	bin, err := Open(filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}

	info, err := bin.Put(src, "deadbeef")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source should be gone after Put, stat err=%v", err)
	}
	if _, err := os.Stat(info.TrashPath); err != nil {
		t.Fatalf("trash path should exist: %v", err)
	}

	restored, err := bin.Restore(info.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.OriginalPath != src {
		t.Fatalf("OriginalPath = %q, want %q", restored.OriginalPath, src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("content changed on round trip: %q", data)
	}
}

func TestRestore_DestinationExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.jpg")
	writeFile(t, src, "hello")

	bin, err := Open(filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := bin.Put(src, "")
	if err != nil {
		t.Fatal(err)
	}

	// Something else re-created a file at the original path.
	writeFile(t, src, "squatter")

	if _, err := bin.Restore(info.ID); err == nil {
		t.Fatalf("Restore should fail when destination exists")
	}
	// The trashed file must still be present so the caller can retry.
	if _, err := os.Stat(info.TrashPath); err != nil {
		t.Fatalf("trashed file should still exist after failed restore: %v", err)
	}
}

func TestList_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	bin, err := Open(filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.jpg", "second.jpg", "third.jpg"} {
		p := filepath.Join(dir, name)
		writeFile(t, p, name)
		if _, err := bin.Put(p, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := bin.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 trashed items, got %d", len(got))
	}
	// Newest-first means the last inserted is at index 0.
	if filepath.Base(got[0].OriginalPath) != "third.jpg" {
		t.Fatalf("wrong order: %+v", got)
	}
}

func TestPurge(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.jpg")
	writeFile(t, src, "x")
	bin, err := Open(filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := bin.Put(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := bin.Purge(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.TrashPath); !os.IsNotExist(err) {
		t.Fatalf("purged file should be gone: %v", err)
	}
	list, _ := bin.List()
	if len(list) != 0 {
		t.Fatalf("list should be empty, got %+v", list)
	}
}
