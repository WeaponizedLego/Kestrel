package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

func TestFolders_TreeFromLivePhotos(t *testing.T) {
	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/one.jpg", Name: "one.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/two.jpg", Name: "two.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/sub/three.jpg", Name: "three.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/b/four.jpg", Name: "four.jpg"})

	h := NewLibraryHandler(lib, nil)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/folders", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var nodes []FolderNode
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	byPath := make(map[string]FolderNode, len(nodes))
	for _, n := range nodes {
		byPath[n.Path] = n
	}

	// /mnt/a has two direct photos and one in sub/, so Total = 3.
	a, ok := byPath["/mnt/a"]
	if !ok {
		t.Fatalf("expected /mnt/a in tree, got %+v", nodes)
	}
	if a.Count != 2 || a.Total != 3 {
		t.Errorf("/mnt/a: Count=%d Total=%d, want Count=2 Total=3", a.Count, a.Total)
	}

	sub, ok := byPath["/mnt/a/sub"]
	if !ok {
		t.Fatalf("expected /mnt/a/sub in tree")
	}
	if sub.Count != 1 || sub.Total != 1 {
		t.Errorf("/mnt/a/sub: Count=%d Total=%d, want 1/1", sub.Count, sub.Total)
	}

	b := byPath["/mnt/b"]
	if b.Count != 1 || b.Total != 1 {
		t.Errorf("/mnt/b: Count=%d Total=%d, want 1/1", b.Count, b.Total)
	}

	// /mnt aggregates all four photos across its subtrees.
	mnt := byPath["/mnt"]
	if mnt.Total != 4 {
		t.Errorf("/mnt: Total=%d, want 4", mnt.Total)
	}

	// The highest-level node in the tree should have Parent=""
	// because nothing above it is in the tree, even if filepath.Dir
	// would return "/".
	root, ok := byPath["/"]
	if ok && root.Parent != "" {
		t.Errorf("/ has Parent=%q, want empty", root.Parent)
	}
}

func TestPhotos_FolderFilter(t *testing.T) {
	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/one.jpg", Name: "one.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/sub/two.jpg", Name: "two.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/b/three.jpg", Name: "three.jpg"})

	h := NewLibraryHandler(lib, nil)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/photos?folder=/mnt/a", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var photos []*library.Photo
	if err := json.Unmarshal(rec.Body.Bytes(), &photos); err != nil {
		t.Fatal(err)
	}
	// /mnt/a filter should pick up the direct photo AND the subfolder
	// photo, but not /mnt/b.
	if len(photos) != 2 {
		t.Fatalf("got %d photos under /mnt/a, want 2", len(photos))
	}
	for _, p := range photos {
		if p.Path == "/mnt/b/three.jpg" {
			t.Errorf("folder filter leaked unrelated photo: %s", p.Path)
		}
	}
}

func TestPhotos_FolderFilterNoPrefixCollision(t *testing.T) {
	// Guard against "/foo" matching "/foobar" via naive string
	// prefix. We add a separator before comparing.
	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: "/p/a.jpg", Name: "a.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/pub/b.jpg", Name: "b.jpg"})

	h := NewLibraryHandler(lib, nil)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/photos?folder=/p", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var photos []*library.Photo
	if err := json.Unmarshal(rec.Body.Bytes(), &photos); err != nil {
		t.Fatal(err)
	}
	if len(photos) != 1 || photos[0].Path != "/p/a.jpg" {
		t.Fatalf("expected only /p/a.jpg under /p, got %+v", photos)
	}
}
