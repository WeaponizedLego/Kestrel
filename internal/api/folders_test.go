package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/watchroots"
)

func TestFolders_TreeFromLivePhotos(t *testing.T) {
	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/one.jpg", Name: "one.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/two.jpg", Name: "two.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/a/sub/three.jpg", Name: "three.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/mnt/b/four.jpg", Name: "four.jpg"})

	h := NewLibraryHandler(lib, nil, nil, nil, nil)
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

	h := NewLibraryHandler(lib, nil, nil, nil, nil)
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

func TestCreateFolder(t *testing.T) {
	tmp := t.TempDir()
	roots, err := watchroots.Open(filepath.Join(t.TempDir(), "watchroots.json"))
	if err != nil {
		t.Fatalf("open watchroots: %v", err)
	}
	if err := roots.Upsert(tmp); err != nil {
		t.Fatalf("upsert root: %v", err)
	}

	h := NewLibraryHandler(library.New(), nil, nil, nil, roots)
	mux := http.NewServeMux()
	h.Register(mux)

	post := func(body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/folder/create", bytes.NewReader(buf))
		mux.ServeHTTP(rec, req)
		return rec
	}

	t.Run("success", func(t *testing.T) {
		rec := post(map[string]string{"parent": tmp, "name": "fresh"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(tmp, "fresh")); err != nil {
			t.Fatalf("expected fresh dir to exist: %v", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		rec := post(map[string]string{"parent": tmp, "name": "fresh"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("missing parent", func(t *testing.T) {
		rec := post(map[string]string{"parent": "", "name": "x"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("relative parent", func(t *testing.T) {
		rec := post(map[string]string{"parent": "rel/path", "name": "x"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		rec := post(map[string]string{"parent": tmp, "name": "  "})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("name with separator", func(t *testing.T) {
		rec := post(map[string]string{"parent": tmp, "name": "a/b"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("traversal name", func(t *testing.T) {
		rec := post(map[string]string{"parent": tmp, "name": ".."})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("dot prefix", func(t *testing.T) {
		rec := post(map[string]string{"parent": tmp, "name": ".hidden"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("outside watched root", func(t *testing.T) {
		other := t.TempDir()
		rec := post(map[string]string{"parent": other, "name": "x"})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}

func TestCreateFolder_KnownByLibraryPhoto(t *testing.T) {
	// Parent isn't a watched root, but a photo lives under it — that's
	// enough to count as "known", which matches what the sidebar shows.
	tmp := t.TempDir()
	indexed := filepath.Join(tmp, "indexed")
	if err := os.MkdirAll(indexed, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: filepath.Join(indexed, "p.jpg"), Name: "p.jpg"})

	roots, err := watchroots.Open(filepath.Join(t.TempDir(), "watchroots.json"))
	if err != nil {
		t.Fatalf("open watchroots: %v", err)
	}
	// Deliberately empty watched-root list.

	h := NewLibraryHandler(lib, nil, nil, nil, roots)
	mux := http.NewServeMux()
	h.Register(mux)

	body, _ := json.Marshal(map[string]string{"parent": indexed, "name": "new"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/folder/create", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(indexed, "new")); err != nil {
		t.Fatalf("expected new dir: %v", err)
	}
}

func TestBrowse_HasChildren(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "leaf")
	parent := filepath.Join(root, "parent")
	nested := filepath.Join(parent, "nested")
	for _, d := range []string{leaf, parent, nested} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	h := NewLibraryHandler(library.New(), nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/browse?path="+root, nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Entries []browseEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]bool{}
	for _, e := range resp.Entries {
		got[e.Name] = e.HasChildren
	}
	if hc, ok := got["leaf"]; !ok || hc {
		t.Errorf("leaf: has_children=%v ok=%v, want false present", hc, ok)
	}
	if hc, ok := got["parent"]; !ok || !hc {
		t.Errorf("parent: has_children=%v ok=%v, want true present", hc, ok)
	}
}

func TestPhotos_FolderFilterNoPrefixCollision(t *testing.T) {
	// Guard against "/foo" matching "/foobar" via naive string
	// prefix. We add a separator before comparing.
	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: "/p/a.jpg", Name: "a.jpg"})
	lib.AddPhoto(&library.Photo{Path: "/pub/b.jpg", Name: "b.jpg"})

	h := NewLibraryHandler(lib, nil, nil, nil, nil)
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
