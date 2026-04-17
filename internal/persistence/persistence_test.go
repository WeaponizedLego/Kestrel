package persistence

import (
	"errors"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library_meta.gob")

	want := []*library.Photo{
		{
			Path:       "/p/a.jpg",
			Hash:       "deadbeef",
			Name:       "a.jpg",
			SizeBytes:  1024,
			ModTime:    time.Unix(1_700_000_000, 0).UTC(),
			Width:      640,
			Height:     480,
			TakenAt:    time.Unix(1_600_000_000, 0).UTC(),
			CameraMake: "Canon",
		},
		{Path: "/p/b.png", Hash: "cafef00d", Name: "b.png", SizeBytes: 2048},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if *got[i] != *want[i] {
			t.Errorf("photo %d: got %+v, want %+v", i, *got[i], *want[i])
		}
	}
}

func TestLoad_MissingFileReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.gob")
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing): unexpected error %v", err)
	}
	if got != nil {
		t.Fatalf("Load(missing): got %v, want nil", got)
	}
}

func TestLoad_BadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library_meta.gob")
	// Encode a header with the wrong magic so the file is structurally
	// a gob stream but not a Kestrel one.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(header{Magic: "NOPE", Version: CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := Load(path); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
}

func TestLoad_UnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library_meta.gob")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(header{Magic: magic, Version: CurrentVersion + 99}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := Load(path); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("expected ErrUnknownVersion, got %v", err)
	}
}

func TestSave_AtomicReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library_meta.gob")
	first := []*library.Photo{{Path: "/p/a.jpg", Name: "a.jpg", SizeBytes: 1}}
	second := []*library.Photo{
		{Path: "/p/x.jpg", Name: "x.jpg", SizeBytes: 9},
		{Path: "/p/y.jpg", Name: "y.jpg", SizeBytes: 10},
	}

	if err := Save(path, first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := Save(path, second); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 || got[0].Path != "/p/x.jpg" {
		t.Fatalf("Save did not replace contents; got %+v", got)
	}
	// The .tmp sidecar must not linger.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file should be gone after rename, stat err = %v", err)
	}
}
