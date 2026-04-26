package library

import (
	"sync"
	"testing"
	"time"
)

func newAudio(path string, tags ...string) *Audio {
	return &Audio{
		Path:      path,
		Hash:      "h-" + path,
		Name:      path,
		SizeBytes: int64(len(path)),
		ModTime:   time.Unix(1_700_000_000, 0).UTC(),
		Tags:      append([]string(nil), tags...),
	}
}

func TestAddGetRemoveAudio(t *testing.T) {
	l := New()
	a := newAudio("/m/song.mp3")
	l.AddAudio(a)

	got, err := l.GetAudio(a.Path)
	if err != nil {
		t.Fatalf("GetAudio: %v", err)
	}
	if got != a {
		t.Fatalf("GetAudio returned different pointer")
	}
	if l.LenAudio() != 1 {
		t.Fatalf("LenAudio = %d, want 1", l.LenAudio())
	}

	removed, err := l.RemoveAudio(a.Path)
	if err != nil {
		t.Fatalf("RemoveAudio: %v", err)
	}
	if removed != a {
		t.Fatalf("RemoveAudio returned different pointer")
	}
	if _, err := l.GetAudio(a.Path); err == nil {
		t.Fatalf("expected GetAudio to fail after remove")
	}
}

func TestRenameAudio(t *testing.T) {
	l := New()
	l.AddAudio(newAudio("/m/old.mp3"))
	if err := l.RenameAudio("/m/old.mp3", "/m/new.mp3"); err != nil {
		t.Fatalf("RenameAudio: %v", err)
	}
	if _, err := l.GetAudio("/m/old.mp3"); err == nil {
		t.Fatalf("old key still resolves")
	}
	got, err := l.GetAudio("/m/new.mp3")
	if err != nil {
		t.Fatalf("new key: %v", err)
	}
	if got.Path != "/m/new.mp3" || got.Name != "new.mp3" {
		t.Fatalf("unexpected fields: %+v", got)
	}
}

func TestSortedAudio(t *testing.T) {
	l := New()
	l.AddAudio(newAudio("/m/b.mp3"))
	l.AddAudio(newAudio("/m/a.mp3"))
	out := l.SortedAudio(SortName, false)
	if len(out) != 2 || out[0].Path != "/m/a.mp3" {
		t.Fatalf("SortedAudio order wrong: %+v", out)
	}
}

func TestTagOpsCoverAudio(t *testing.T) {
	l := New()
	l.AddPhoto(&Photo{Path: "/p/a.jpg", Tags: []string{"sky"}})
	l.AddAudio(newAudio("/m/a.mp3", "sky"))

	if err := l.SetTags("/m/a.mp3", []string{"music", "sky"}); err != nil {
		t.Fatalf("SetTags on audio: %v", err)
	}
	a, _ := l.GetAudio("/m/a.mp3")
	if len(a.Tags) != 2 || a.Tags[0] != "music" {
		t.Fatalf("audio tags after SetTags: %v", a.Tags)
	}

	renamed, _, err := l.RenameTag("sky", "outdoors")
	if err != nil {
		t.Fatalf("RenameTag: %v", err)
	}
	if renamed != 2 {
		t.Fatalf("RenameTag should have hit photo+audio, got %d", renamed)
	}

	if affected := l.DeleteTag("outdoors"); affected != 2 {
		t.Fatalf("DeleteTag should hit both, got %d", affected)
	}
}

func TestConcurrentPhotoAudioMutations(t *testing.T) {
	l := New()
	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			l.AddPhoto(&Photo{Path: "/p/" + audioItoa(i) + ".jpg", Hash: "h"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			l.AddAudio(newAudio("/m/" + audioItoa(i) + ".mp3"))
		}
	}()
	wg.Wait()
	if l.Len() != n || l.LenAudio() != n {
		t.Fatalf("expected %d photos and %d audios, got %d / %d", n, n, l.Len(), l.LenAudio())
	}
}

func audioItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
