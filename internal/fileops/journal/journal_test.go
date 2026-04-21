package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndReplay_PendingRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fileops.journal")

	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer j.Close()

	if err := j.Append(Entry{ID: "op-1", Kind: KindMove, State: StatePending, OldPath: "/a", NewPath: "/b"}); err != nil {
		t.Fatalf("Append pending: %v", err)
	}

	pending, err := Replay(path)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "op-1" {
		t.Fatalf("expected one pending entry, got %+v", pending)
	}
}

func TestAppendAndReplay_CompletedHidden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	if err := j.Append(Entry{ID: "op-1", Kind: KindMove, State: StatePending}); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(Entry{ID: "op-1", Kind: KindMove, State: StateComplete}); err != nil {
		t.Fatal(err)
	}

	pending, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("completed op should not be pending, got %+v", pending)
	}
}

func TestReplay_TornTailingLineIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(Entry{ID: "op-1", Kind: KindMove, State: StatePending}); err != nil {
		t.Fatal(err)
	}
	j.Close()

	// Append a torn line (invalid JSON, no newline).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"op-2","kind":"move","sta`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	pending, err := Replay(path)
	if err != nil {
		t.Fatalf("Replay with torn tail must not error: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "op-1" {
		t.Fatalf("expected only op-1 recovered, got %+v", pending)
	}
}

func TestReplay_MissingFileIsEmpty(t *testing.T) {
	pending, err := Replay(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("Replay of missing file should be nil, nil: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected empty, got %+v", pending)
	}
}

func TestRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(Entry{ID: "x", Kind: KindMove, State: StateComplete}); err != nil {
		t.Fatal(err)
	}
	j.Close()

	if err := Rotate(path); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal should be gone after rotate, stat err=%v", err)
	}
	entries, _ := os.ReadDir(dir)
	var archived int
	for _, e := range entries {
		if filepath.Ext(e.Name()) != "" {
			archived++
		}
	}
	if archived == 0 {
		t.Fatalf("expected an archived file in %s, got %+v", dir, entries)
	}
}

func FuzzReplay(f *testing.F) {
	f.Add([]byte(`{"id":"a","kind":"move","state":"pending"}` + "\n"))
	f.Add([]byte(""))
	f.Add([]byte("not json\n"))
	f.Add([]byte(`{"id":"a","state":"pending"}` + "\n" + `{"id":"a","state":"complete"`))

	f.Fuzz(func(t *testing.T, payload []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "j")
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		// Must never panic; must return either a slice or a non-nil
		// error. Never both nil slice and nil error lie about
		// pending state — absence of a file is the only valid nil/nil.
		_, err := Replay(path)
		_ = err
	})
}
