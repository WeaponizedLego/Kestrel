package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

func newTagTestHandler(t *testing.T, photos ...*library.Photo) *http.ServeMux {
	t.Helper()
	lib := library.New()
	for _, p := range photos {
		lib.AddPhoto(p)
	}
	mux := http.NewServeMux()
	NewLibraryHandler(lib, nil, nil, nil).Register(mux)
	return mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	} else {
		rdr = bytes.NewReader(nil)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, rdr)
	mux.ServeHTTP(rec, req)
	return rec
}

func TestTags_List(t *testing.T) {
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"cats", "cute"}, AutoTags: []string{"camera:nikon"}},
		&library.Photo{Path: "/2.jpg", Tags: []string{"cats"}},
	)

	rec := doJSON(t, mux, http.MethodGet, "/tags/list", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var stats []library.TagStat
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want 2 entries", stats)
	}
	if stats[0].Name != "cats" || stats[0].Count != 2 {
		t.Errorf("stats[0] = %+v, want cats:2", stats[0])
	}
}

func TestTags_List_RejectsNonGET(t *testing.T) {
	mux := newTagTestHandler(t)
	rec := doJSON(t, mux, http.MethodPost, "/tags/list", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestTags_Rename(t *testing.T) {
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"cts"}},
		&library.Photo{Path: "/2.jpg", Tags: []string{"cts", "cats"}},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags/rename", map[string]string{
		"from": "cts", "to": "cats",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["renamed"] != 1 || out["absorbed"] != 1 {
		t.Fatalf("out = %+v, want renamed:1 absorbed:1", out)
	}
}

func TestTags_Rename_RejectsIdentical(t *testing.T) {
	mux := newTagTestHandler(t)
	rec := doJSON(t, mux, http.MethodPost, "/tags/rename", map[string]string{
		"from": "Cats", "to": "cats",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTags_Rename_RejectsMissingField(t *testing.T) {
	mux := newTagTestHandler(t)
	rec := doJSON(t, mux, http.MethodPost, "/tags/rename", map[string]string{"from": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTags_Merge(t *testing.T) {
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"cats"}},
		&library.Photo{Path: "/2.jpg", Tags: []string{"cats", "feline"}},
		&library.Photo{Path: "/3.jpg", Tags: []string{"dog"}},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags/merge", map[string]string{
		"source": "cats", "target": "feline",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]int
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["renamed"] != 1 || out["absorbed"] != 1 {
		t.Fatalf("out = %+v, want renamed:1 absorbed:1", out)
	}
}

func TestTags_Delete(t *testing.T) {
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"a", "b"}},
		&library.Photo{Path: "/2.jpg", Tags: []string{"b"}},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags/delete", map[string]string{"name": "b"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]int
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["affected"] != 2 {
		t.Fatalf("affected = %d, want 2", out["affected"])
	}
}

func TestTags_Delete_RejectsBlank(t *testing.T) {
	mux := newTagTestHandler(t)
	rec := doJSON(t, mux, http.MethodPost, "/tags/delete", map[string]string{"name": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
