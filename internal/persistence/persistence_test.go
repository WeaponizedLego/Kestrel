package persistence

import (
	"errors"
	"encoding/gob"
	"os"
	"path/filepath"
	"reflect"
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
	wantHidden := []string{"secret", "wip"}
	wantDismissed := []string{"fp-aaa", "fp-bbb"}

	if err := Save(path, want, wantHidden, wantDismissed); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, hidden, dismissed, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(*got[i], *want[i]) {
			t.Errorf("photo %d: got %+v, want %+v", i, *got[i], *want[i])
		}
	}
	if !reflect.DeepEqual(hidden, wantHidden) {
		t.Errorf("hidden tags: got %v, want %v", hidden, wantHidden)
	}
	if !reflect.DeepEqual(dismissed, wantDismissed) {
		t.Errorf("dismissed clusters: got %v, want %v", dismissed, wantDismissed)
	}
}

func TestLoad_MissingFileReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.gob")
	got, hidden, dismissed, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing): unexpected error %v", err)
	}
	if got != nil || hidden != nil || dismissed != nil {
		t.Fatalf("Load(missing): got %v / %v / %v, want nil / nil / nil", got, hidden, dismissed)
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

	if _, _, _, err := Load(path); !errors.Is(err, ErrBadMagic) {
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

	if _, _, _, err := Load(path); !errors.Is(err, ErrUnknownVersion) {
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

	if err := Save(path, first, nil, nil); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := Save(path, second, nil, nil); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	got, _, _, err := Load(path)
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

// TestLoad_V2IsForwardCompat ensures a v2-shaped file (header + photos,
// no hidden-tag slice) still decodes cleanly — the hidden set comes
// back empty, not an error.
func TestLoad_V2IsForwardCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library_meta.gob")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := gob.NewEncoder(f)
	if err := enc.Encode(header{Magic: magic, Version: 2}); err != nil {
		t.Fatal(err)
	}
	photos := []*library.Photo{{Path: "/p/a.jpg", Name: "a.jpg"}}
	if err := enc.Encode(photos); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, hidden, dismissed, err := Load(path)
	if err != nil {
		t.Fatalf("Load v2: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("photos: got %d, want 1", len(got))
	}
	if len(hidden) != 0 {
		t.Fatalf("hidden: got %v, want empty", hidden)
	}
	if len(dismissed) != 0 {
		t.Fatalf("dismissed: got %v, want empty", dismissed)
	}
}

// TestLoad_V3IsForwardCompat verifies a v3 file (header + photos +
// hidden tags, no dismissed-cluster slice) still decodes cleanly under
// the v4 reader — the dismissed set comes back empty.
func TestLoad_V3IsForwardCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library_meta.gob")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := gob.NewEncoder(f)
	if err := enc.Encode(header{Magic: magic, Version: 3}); err != nil {
		t.Fatal(err)
	}
	photos := []*library.Photo{{Path: "/p/a.jpg", Name: "a.jpg"}}
	if err := enc.Encode(photos); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode([]string{"wip"}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, hidden, dismissed, err := Load(path)
	if err != nil {
		t.Fatalf("Load v3: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("photos: got %d, want 1", len(got))
	}
	if len(hidden) != 1 || hidden[0] != "wip" {
		t.Fatalf("hidden: got %v, want [wip]", hidden)
	}
	if len(dismissed) != 0 {
		t.Fatalf("dismissed: got %v, want empty", dismissed)
	}
}
