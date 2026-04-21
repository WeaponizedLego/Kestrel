package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/fileops"
	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
	"github.com/WeaponizedLego/kestrel/internal/fileops/trash"
	"github.com/WeaponizedLego/kestrel/internal/library"
)

// newFileOpsTestMux wires a real fileops.Manager over temp dirs and
// returns a mux with the handler registered. Mirrors the pattern in
// tags_test.go — real collaborators in, fake only where necessary.
func newFileOpsTestMux(t *testing.T) (*http.ServeMux, *library.Library, string) {
	t.Helper()
	dir := t.TempDir()

	lib := library.New()
	j, err := journal.Open(filepath.Join(dir, "j"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	bin, err := trash.Open(filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := fileops.New(fileops.Config{
		Library: lib, Journal: j, Trash: bin,
	})

	mux := http.NewServeMux()
	NewFileOpsHandler(mgr).Register(mux)
	return mux, lib, dir
}

func TestFileOps_Move_RoundTrip(t *testing.T) {
	mux, lib, dir := newFileOpsTestMux(t)

	src := filepath.Join(dir, "src", "a.jpg")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	_ = os.WriteFile(src, []byte("hello"), 0o644)
	lib.AddPhoto(&library.Photo{Path: src, Name: "a.jpg"})

	dest := filepath.Join(dir, "dst")
	rec := doJSON(t, mux, http.MethodPost, "/files/move", map[string]any{
		"paths": []string{src},
		"dest":  dest,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got, _ := resp["moved"].(float64); got != 1 {
		t.Fatalf("moved = %v, want 1", resp["moved"])
	}
}

func TestFileOps_Delete_DefaultIsTrash(t *testing.T) {
	mux, lib, dir := newFileOpsTestMux(t)
	src := filepath.Join(dir, "a.jpg")
	_ = os.WriteFile(src, []byte("x"), 0o644)
	lib.AddPhoto(&library.Photo{Path: src, Name: "a.jpg"})

	rec := doJSON(t, mux, http.MethodPost, "/files/delete", map[string]any{
		"paths": []string{src},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// File gone from original path, still present in trash dir tree.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source should be gone after delete: %v", err)
	}
	// Trash dir has files/ populated.
	entries, _ := os.ReadDir(filepath.Join(dir, "trash", "files"))
	if len(entries) == 0 {
		t.Fatalf("trash files dir should contain the deleted photo")
	}
}

func TestFileOps_Undo_NothingToUndo(t *testing.T) {
	mux, _, _ := newFileOpsTestMux(t)
	rec := doJSON(t, mux, http.MethodPost, "/files/undo", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no ops to undo, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFileOps_UndoDepth(t *testing.T) {
	mux, lib, dir := newFileOpsTestMux(t)
	src := filepath.Join(dir, "a.jpg")
	_ = os.WriteFile(src, []byte("x"), 0o644)
	lib.AddPhoto(&library.Photo{Path: src, Name: "a.jpg"})

	// Before any op: depth 0.
	rec := doJSON(t, mux, http.MethodGet, "/files/undo/depth", nil)
	var depthResp map[string]int
	_ = json.Unmarshal(rec.Body.Bytes(), &depthResp)
	if depthResp["depth"] != 0 {
		t.Fatalf("initial depth = %d, want 0", depthResp["depth"])
	}

	// After a trash delete: depth 1.
	_ = doJSON(t, mux, http.MethodPost, "/files/delete", map[string]any{"paths": []string{src}})
	rec = doJSON(t, mux, http.MethodGet, "/files/undo/depth", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &depthResp)
	if depthResp["depth"] != 1 {
		t.Fatalf("post-delete depth = %d, want 1", depthResp["depth"])
	}
}
