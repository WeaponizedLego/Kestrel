package library

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLibrary_AddGetAll(t *testing.T) {
	lib := New()
	p := &Photo{Path: "/a/b.jpg", Name: "b.jpg", SizeBytes: 42, ModTime: time.Unix(0, 0)}
	lib.AddPhoto(p)

	got, err := lib.GetPhoto("/a/b.jpg")
	if err != nil {
		t.Fatalf("GetPhoto(existing) returned error: %v", err)
	}
	if got != p {
		t.Fatalf("GetPhoto returned %+v, want %+v", got, p)
	}

	if n := lib.Len(); n != 1 {
		t.Fatalf("Len() = %d, want 1", n)
	}

	all := lib.AllPhotos()
	if len(all) != 1 || all[0] != p {
		t.Fatalf("AllPhotos() = %+v, want [%+v]", all, p)
	}
}

func TestLibrary_GetPhotoMissing(t *testing.T) {
	lib := New()
	_, err := lib.GetPhoto("/no/such/file.jpg")
	if !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("expected ErrPhotoNotFound, got %v", err)
	}
}

func TestLibrary_ReplaceAll(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", SizeBytes: 1})

	replacement := []*Photo{
		{Path: "/x.jpg", Name: "x.jpg", SizeBytes: 2},
		{Path: "/y.jpg", Name: "y.jpg", SizeBytes: 3},
	}
	lib.ReplaceAll(replacement)

	if n := lib.Len(); n != 2 {
		t.Fatalf("Len() after ReplaceAll = %d, want 2", n)
	}
	if _, err := lib.GetPhoto("/a.jpg"); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("old entry still present after ReplaceAll")
	}
}

// TestLibrary_ConcurrentAccess is a smoke test: many goroutines reading
// while one writes must not race or panic. Run with `go test -race`.
func TestLibrary_ConcurrentAccess(t *testing.T) {
	lib := New()
	for i := 0; i < 100; i++ {
		lib.AddPhoto(&Photo{Path: pathN(i), SizeBytes: int64(i)})
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = lib.AllPhotos()
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 100; i < 500; i++ {
			lib.AddPhoto(&Photo{Path: pathN(i), SizeBytes: int64(i)})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		close(stop)
	}()

	wg.Wait()
}

func pathN(i int) string {
	return "/p/" + string(rune('a'+i%26)) + "-" + itoa(i) + ".jpg"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
