package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// buildTree creates a directory tree under base. spec maps relative
// directory paths to the supported-media filenames inside them.
func buildTree(t *testing.T, base string, spec map[string][]string) {
	t.Helper()
	for dir, files := range spec {
		full := filepath.Join(base, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		for _, name := range files {
			if err := os.WriteFile(filepath.Join(full, name), []byte("x"), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
}

func collectFiles(t *testing.T, ctx context.Context, root string, opts walkOptions) ([]string, []string, error) {
	t.Helper()
	paths := make(chan string, 256)
	var dirs []string
	var dmu sync.Mutex
	opts.OnDirsFound = func(batch []string) {
		dmu.Lock()
		dirs = append(dirs, batch...)
		dmu.Unlock()
	}
	type result struct {
		count int
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		count, err := walkPathsBFS(ctx, root, paths, opts)
		close(paths)
		resCh <- result{count, err}
	}()
	var files []string
	for p := range paths {
		files = append(files, p)
	}
	r := <-resCh
	sort.Strings(files)
	dmu.Lock()
	sort.Strings(dirs)
	dmu.Unlock()
	if r.count != len(files) {
		t.Errorf("walker reported %d files, channel delivered %d", r.count, len(files))
	}
	return files, dirs, r.err
}

func TestWalkPathsBFS_FindsAllFilesAndDirs(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, map[string][]string{
		".":         {"a.jpg", "ignored.txt"},
		"sub":       {"b.png"},
		"sub/deep":  {"c.gif"},
		"empty":     nil,
	})
	files, dirs, err := collectFiles(t, context.Background(), root, walkOptions{})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("files: want 3, got %d (%v)", len(files), files)
	}
	want := []string{root, filepath.Join(root, "empty"), filepath.Join(root, "sub"), filepath.Join(root, "sub", "deep")}
	if len(dirs) != len(want) {
		t.Fatalf("dirs: want %d, got %d (%v)", len(want), len(dirs), dirs)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("dirs[%d]: want %q, got %q", i, want[i], dirs[i])
		}
	}
}

func TestWalkPathsBFS_SingleThreadParity(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, map[string][]string{
		".":           {"a.jpg"},
		"x":           {"b.jpg", "c.png"},
		"x/y":         {"d.webp"},
		"x/y/z":       {"e.mov"},
	})
	parFiles, _, err := collectFiles(t, context.Background(), root, walkOptions{})
	if err != nil {
		t.Fatalf("parallel walk: %v", err)
	}
	seqFiles, _, err := collectFiles(t, context.Background(), root, walkOptions{SingleThread: true})
	if err != nil {
		t.Fatalf("single walk: %v", err)
	}
	if len(parFiles) != len(seqFiles) {
		t.Fatalf("file count differs: parallel=%d single=%d", len(parFiles), len(seqFiles))
	}
	for i := range parFiles {
		if parFiles[i] != seqFiles[i] {
			t.Errorf("file[%d] differs: parallel=%q single=%q", i, parFiles[i], seqFiles[i])
		}
	}
}

func TestWalkPathsBFS_ContextCancel(t *testing.T) {
	root := t.TempDir()
	// Build a deep enough tree that cancellation has something to bite.
	dirs := map[string][]string{}
	for i := 0; i < 50; i++ {
		dir := filepath.Join("sub", "x")
		for j := 0; j < i; j++ {
			dir = filepath.Join(dir, "y")
		}
		dirs[dir] = []string{"f.jpg"}
	}
	buildTree(t, root, dirs)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate

	paths := make(chan string, 256)
	go func() {
		_, _ = walkPathsBFS(ctx, root, paths, walkOptions{})
		close(paths)
	}()
	// Drain — must terminate without hanging.
	done := make(chan struct{})
	go func() {
		for range paths {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("walk did not terminate after cancel")
	}
}

func TestWalkPathsBFS_MissingRoot(t *testing.T) {
	paths := make(chan string, 1)
	_, err := walkPathsBFS(context.Background(), "/definitely/does/not/exist/kestrel-test", paths, walkOptions{})
	close(paths)
	if err == nil {
		t.Fatal("expected error for missing root")
	}
}
