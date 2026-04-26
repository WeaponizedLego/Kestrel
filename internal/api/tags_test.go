package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/library/cluster"
)

func newTagTestHandler(t *testing.T, photos ...*library.Photo) *http.ServeMux {
	t.Helper()
	mux, _ := newTagTestHandlerWithLib(t, photos...)
	return mux
}

func newTagTestHandlerWithLib(t *testing.T, photos ...*library.Photo) (*http.ServeMux, *library.Library) {
	t.Helper()
	lib := library.New()
	for _, p := range photos {
		lib.AddPhoto(p)
	}
	mux := http.NewServeMux()
	NewLibraryHandler(lib, nil, nil, nil, nil).Register(mux)
	return mux, lib
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
	// Synthetic "untagged" system entry leads the list, then user tags
	// sorted by name.
	if len(stats) != 3 {
		t.Fatalf("stats = %+v, want 3 entries (untagged + cats + cute)", stats)
	}
	if stats[0].Name != library.UntaggedTag || stats[0].Kind != library.TagKindSystem {
		t.Errorf("stats[0] = %+v, want untagged system entry", stats[0])
	}
	if stats[1].Name != "cats" || stats[1].Count != 2 {
		t.Errorf("stats[1] = %+v, want cats:2", stats[1])
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

func TestTags_Hidden_HideUnhide(t *testing.T) {
	mux, lib := newTagTestHandlerWithLib(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"wip"}, AutoTags: []string{"camera:nikon"}},
	)

	rec := doJSON(t, mux, http.MethodPost, "/tags/hidden", map[string]any{"name": "wip", "hidden": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !lib.IsTagHidden("wip") {
		t.Fatalf("lib should report wip hidden")
	}

	// Default list excludes the hidden tag; only the synthetic untagged
	// entry remains.
	rec = doJSON(t, mux, http.MethodGet, "/tags/list", nil)
	var stats []library.TagStat
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if len(stats) != 1 || stats[0].Kind != library.TagKindSystem {
		t.Fatalf("default list should be only the system entry when only tag is hidden: %+v", stats)
	}

	// include_hidden=1 shows it with Hidden=true (alongside the system entry).
	rec = doJSON(t, mux, http.MethodGet, "/tags/list?include_hidden=1", nil)
	stats = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if len(stats) != 2 {
		t.Fatalf("include_hidden list = %+v, want system + wip", stats)
	}
	wip := stats[1]
	if wip.Name != "wip" || !wip.Hidden {
		t.Fatalf("include_hidden list = %+v", stats)
	}

	// include_auto=1 also surfaces auto-tags.
	rec = doJSON(t, mux, http.MethodGet, "/tags/list?include_hidden=1&include_auto=1", nil)
	stats = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	foundAuto := false
	for _, s := range stats {
		if s.Name == "camera:nikon" && s.Kind == library.TagKindAuto {
			foundAuto = true
		}
	}
	if !foundAuto {
		t.Fatalf("auto tag missing from include_auto listing: %+v", stats)
	}

	// Unhide.
	rec = doJSON(t, mux, http.MethodPost, "/tags/hidden", map[string]any{"name": "wip", "hidden": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("unhide status = %d", rec.Code)
	}
	if lib.IsTagHidden("wip") {
		t.Fatalf("wip should no longer be hidden")
	}
}

func TestTags_Hidden_RejectsLiteralHiddenTag(t *testing.T) {
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{library.HiddenTag}},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags/hidden", map[string]any{"name": library.HiddenTag, "hidden": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListPhotos_UntaggedToken(t *testing.T) {
	// Three photos: one with a user tag, one with only an auto-tag, one
	// with nothing. "untagged" should return the latter two.
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/tagged.jpg", Name: "tagged.jpg", Tags: []string{"cats"}},
		&library.Photo{Path: "/auto-only.jpg", Name: "auto-only.jpg", AutoTags: []string{"camera:nikon"}},
		&library.Photo{Path: "/bare.jpg", Name: "bare.jpg"},
	)
	rec := doJSON(t, mux, http.MethodGet, "/photos?q=untagged", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var photos []library.Photo
	if err := json.Unmarshal(rec.Body.Bytes(), &photos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(photos) != 2 {
		t.Fatalf("got %d photos, want 2 (auto-only + bare): %+v", len(photos), photos)
	}
	for _, p := range photos {
		if p.Path == "/tagged.jpg" {
			t.Fatalf("tagged photo leaked into untagged result: %+v", p)
		}
	}
}

func TestListPhotos_UntaggedTokenIntersectsName(t *testing.T) {
	// "untagged" composes with another token under the default match=all.
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/IMG_2023_a.jpg", Name: "IMG_2023_a.jpg"},
		&library.Photo{Path: "/IMG_2024_b.jpg", Name: "IMG_2024_b.jpg"},
		&library.Photo{Path: "/IMG_2023_c.jpg", Name: "IMG_2023_c.jpg", Tags: []string{"keep"}},
	)
	rec := doJSON(t, mux, http.MethodGet, "/photos?q=untagged+2023", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var photos []library.Photo
	if err := json.Unmarshal(rec.Body.Bytes(), &photos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(photos) != 1 || photos[0].Path != "/IMG_2023_a.jpg" {
		t.Fatalf("got %+v, want only IMG_2023_a.jpg", photos)
	}
}

func TestTags_Rename_RejectsReservedTarget(t *testing.T) {
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"cats"}},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags/rename", map[string]string{
		"from": "cats", "to": library.UntaggedTag,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (reserved target): %s", rec.Code, rec.Body.String())
	}
}

func TestTags_SetTags_StripsReserved(t *testing.T) {
	// NormalizeTags drops reserved names, so /api/tags can never store
	// the literal "untagged" on a photo even if a client tries to.
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg"},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags", map[string]any{
		"path": "/1.jpg",
		"tags": []string{"cats", library.UntaggedTag},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct{ Tags []string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Tags) != 1 || out.Tags[0] != "cats" {
		t.Fatalf("tags = %+v, want only [cats]", out.Tags)
	}
}

func TestClusters_DismissAndUndismiss(t *testing.T) {
	// Two photos with the same SHA-256 — they form an exact cluster.
	// Dismissing the cluster should hide it from /api/clusters?kind=exact;
	// undismissing should bring it back.
	lib := library.New()
	lib.AddPhoto(&library.Photo{Path: "/a.jpg", Hash: "h1", PHash: 1})
	lib.AddPhoto(&library.Photo{Path: "/b.jpg", Hash: "h1", PHash: 1})
	clusters := cluster.NewManager(lib)
	mux := http.NewServeMux()
	NewTaggingHandler(lib, clusters, nil).Register(mux)

	// Cluster initially visible under exact view.
	rec := doJSON(t, mux, http.MethodGet, "/clusters?kind=exact", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Kind     string            `json:"kind"`
		Clusters []cluster.Cluster `json:"clusters"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if listed.Kind != "exact" || len(listed.Clusters) != 1 {
		t.Fatalf("initial listing = %+v, want one exact cluster", listed)
	}

	// Dismiss it.
	rec = doJSON(t, mux, http.MethodPost, "/clusters/dismiss", map[string]any{
		"members": []string{"/a.jpg", "/b.jpg"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Now hidden under every kind.
	for _, kind := range []string{"exact", "duplicate", "similar"} {
		rec = doJSON(t, mux, http.MethodGet, "/clusters?kind="+kind, nil)
		_ = json.Unmarshal(rec.Body.Bytes(), &listed)
		if len(listed.Clusters) != 0 {
			t.Fatalf("after dismiss, kind=%s = %+v, want empty", kind, listed.Clusters)
		}
	}

	// Undismiss restores it.
	rec = doJSON(t, mux, http.MethodPost, "/clusters/undismiss", map[string]any{
		"members": []string{"/a.jpg", "/b.jpg"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("undismiss status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, mux, http.MethodGet, "/clusters?kind=exact", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Clusters) != 1 {
		t.Fatalf("after undismiss, exact = %+v, want one cluster", listed.Clusters)
	}
}

func TestClusters_DismissRejectsTooFewMembers(t *testing.T) {
	lib := library.New()
	clusters := cluster.NewManager(lib)
	mux := http.NewServeMux()
	NewTaggingHandler(lib, clusters, nil).Register(mux)

	rec := doJSON(t, mux, http.MethodPost, "/clusters/dismiss", map[string]any{
		"members": []string{"/only.jpg"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTags_Hidden_RejectsAutoOnly(t *testing.T) {
	// "camera:nikon" only exists as an auto-tag — no photo has it as a
	// user tag — so the handler should refuse.
	mux := newTagTestHandler(t,
		&library.Photo{Path: "/1.jpg", Tags: []string{"cats"}, AutoTags: []string{"camera:nikon"}},
	)
	rec := doJSON(t, mux, http.MethodPost, "/tags/hidden", map[string]any{"name": "camera:nikon", "hidden": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
}
