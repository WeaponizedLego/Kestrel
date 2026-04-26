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

func TestLibrary_RemovePhotosInFolder(t *testing.T) {
	seed := []*Photo{
		{Path: "/root/a/one.jpg", Name: "one.jpg"},
		{Path: "/root/a/sub/two.jpg", Name: "two.jpg"},
		{Path: "/root/b/three.jpg", Name: "three.jpg"},
		// Prefix-collision guard: "/root/a" must not match "/root/abc".
		{Path: "/root/abc/four.jpg", Name: "four.jpg"},
	}

	tests := []struct {
		name    string
		folder  string
		removed []string
		remain  []string
	}{
		{
			name:    "sub-tree removed transitively",
			folder:  "/root/a",
			removed: []string{"/root/a/one.jpg", "/root/a/sub/two.jpg"},
			remain:  []string{"/root/abc/four.jpg", "/root/b/three.jpg"},
		},
		{
			name:    "trailing separator tolerated",
			folder:  "/root/b/",
			removed: []string{"/root/b/three.jpg"},
			remain:  []string{"/root/a/one.jpg", "/root/a/sub/two.jpg", "/root/abc/four.jpg"},
		},
		{
			name:    "no match returns nil",
			folder:  "/not/in/library",
			removed: nil,
			remain:  []string{"/root/a/one.jpg", "/root/a/sub/two.jpg", "/root/abc/four.jpg", "/root/b/three.jpg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lib := New()
			for _, p := range seed {
				lib.AddPhoto(p)
			}

			got := lib.RemovePhotosInFolder(tc.folder)
			if !sameSet(got, tc.removed) {
				t.Fatalf("removed = %v, want %v", got, tc.removed)
			}
			if n := lib.Len(); n != len(tc.remain) {
				t.Fatalf("Len() = %d, want %d", n, len(tc.remain))
			}
			for _, path := range tc.remain {
				if _, err := lib.GetPhoto(path); err != nil {
					t.Fatalf("expected %s to remain, got %v", path, err)
				}
			}
			for _, path := range tc.removed {
				if _, err := lib.GetPhoto(path); !errors.Is(err, ErrPhotoNotFound) {
					t.Fatalf("expected %s removed, got %v", path, err)
				}
			}
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
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

func TestLibrary_AllTags(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"cats", "cute"}, AutoTags: []string{"camera:nikon"}})
	lib.AddPhoto(&Photo{Path: "/b.jpg", Name: "b.jpg", Tags: []string{"cats"}})
	lib.AddPhoto(&Photo{Path: "/c.jpg", Name: "c.jpg", Tags: []string{"dog"}})

	stats := lib.AllTags()
	// Synthetic "untagged" system entry leads, followed by sorted user
	// tags. No untagged photos in this fixture, so its count is zero.
	if len(stats) != 4 {
		t.Fatalf("AllTags() returned %d entries, want 4", len(stats))
	}
	if stats[0].Name != UntaggedTag || stats[0].Kind != TagKindSystem || stats[0].Count != 0 {
		t.Fatalf("stats[0] = %+v, want untagged:0 system", stats[0])
	}
	if stats[1].Name != "cats" || stats[1].Count != 2 {
		t.Fatalf("stats[1] = %+v, want cats:2", stats[1])
	}
	if stats[2].Name != "cute" || stats[2].Count != 1 {
		t.Fatalf("stats[2] = %+v, want cute:1", stats[2])
	}
	if stats[3].Name != "dog" || stats[3].Count != 1 {
		t.Fatalf("stats[3] = %+v, want dog:1", stats[3])
	}
	// AutoTags must not leak into the count.
	for _, s := range stats {
		if s.Name == "camera:nikon" {
			t.Fatalf("AutoTags leaked into AllTags: %+v", s)
		}
	}
}

func TestLibrary_AllTagsFiltered_HiddenAndAuto(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"cats", "wip"}, AutoTags: []string{"camera:nikon", "year:2024"}})
	lib.AddPhoto(&Photo{Path: "/b.jpg", Name: "b.jpg", Tags: []string{"cats"}, AutoTags: []string{"year:2024"}})
	if err := lib.SetTagHidden("wip", true); err != nil {
		t.Fatalf("SetTagHidden: %v", err)
	}

	// Default view: wip is hidden, auto is excluded. The synthetic
	// "untagged" system entry always rides at index 0.
	stats := lib.AllTagsFiltered(false, false)
	if len(stats) != 2 || stats[0].Kind != TagKindSystem || stats[1].Name != "cats" {
		t.Fatalf("default: got %+v, want untagged + cats", stats)
	}

	// Include hidden only.
	stats = lib.AllTagsFiltered(true, false)
	if len(stats) != 3 {
		t.Fatalf("hidden-on: got %d, want 3 (%+v)", len(stats), stats)
	}
	for _, s := range stats {
		if s.Kind == TagKindSystem {
			continue
		}
		if s.Kind != TagKindUser {
			t.Errorf("expected Kind=user, got %q", s.Kind)
		}
		if s.Name == "wip" && !s.Hidden {
			t.Errorf("wip should report Hidden=true")
		}
		if s.Name == "cats" && s.Hidden {
			t.Errorf("cats should report Hidden=false")
		}
	}

	// Include hidden + auto.
	stats = lib.AllTagsFiltered(true, true)
	kinds := map[string]string{}
	for _, s := range stats {
		kinds[s.Name] = s.Kind
	}
	if kinds["camera:nikon"] != TagKindAuto || kinds["year:2024"] != TagKindAuto {
		t.Fatalf("auto kinds wrong: %+v", kinds)
	}
	if kinds["cats"] != TagKindUser || kinds["wip"] != TagKindUser {
		t.Fatalf("user kinds wrong: %+v", kinds)
	}
}

func TestLibrary_SetTagHidden_Persistence(t *testing.T) {
	lib := New()
	if err := lib.SetTagHidden("  WIP ", true); err != nil {
		t.Fatalf("SetTagHidden: %v", err)
	}
	if !lib.IsTagHidden("wip") {
		t.Fatalf("IsTagHidden should be true after normalization")
	}
	snap := lib.HiddenTagSnapshot()
	if len(snap) != 1 || snap[0] != "wip" {
		t.Fatalf("snapshot = %v, want [wip]", snap)
	}

	// Reload round-trips.
	lib2 := New()
	lib2.LoadHiddenTags(snap)
	if !lib2.IsTagHidden("wip") {
		t.Fatalf("LoadHiddenTags lost the set")
	}

	if err := lib.SetTagHidden("wip", false); err != nil {
		t.Fatalf("unhide: %v", err)
	}
	if lib.IsTagHidden("wip") {
		t.Fatalf("IsTagHidden should be false after unhide")
	}
}

func TestLibrary_DeleteTag_ClearsHidden(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"wip"}})
	_ = lib.SetTagHidden("wip", true)
	lib.DeleteTag("wip")
	if lib.IsTagHidden("wip") {
		t.Fatalf("DeleteTag should drop the hidden flag")
	}
}

func TestLibrary_RenameTag_CarriesHidden(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"wip"}})
	_ = lib.SetTagHidden("wip", true)
	if _, _, err := lib.RenameTag("wip", "draft"); err != nil {
		t.Fatalf("RenameTag: %v", err)
	}
	if lib.IsTagHidden("wip") {
		t.Fatalf("rename source should be cleared from hidden set")
	}
	if !lib.IsTagHidden("draft") {
		t.Fatalf("rename target should pick up hidden flag")
	}
}

func TestLibrary_MergeTags_DropsSourceHidden(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"wip"}})
	lib.AddPhoto(&Photo{Path: "/b.jpg", Name: "b.jpg", Tags: []string{"draft"}})
	_ = lib.SetTagHidden("wip", true)
	if _, _, err := lib.MergeTags("wip", "draft"); err != nil {
		t.Fatalf("MergeTags: %v", err)
	}
	if lib.IsTagHidden("wip") {
		t.Fatalf("merge should clear the source's hidden flag")
	}
	if lib.IsTagHidden("draft") {
		t.Fatalf("merge should not auto-hide the target")
	}
}

func TestLibrary_RenameTag(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"red", "cts", "blue"}})
	lib.AddPhoto(&Photo{Path: "/b.jpg", Name: "b.jpg", Tags: []string{"cts", "cats"}})
	lib.AddPhoto(&Photo{Path: "/c.jpg", Name: "c.jpg", Tags: []string{"unrelated"}})

	renamed, absorbed, err := lib.RenameTag("cts", "cats")
	if err != nil {
		t.Fatalf("RenameTag: unexpected error: %v", err)
	}
	if renamed != 1 {
		t.Fatalf("renamed = %d, want 1", renamed)
	}
	if absorbed != 1 {
		t.Fatalf("absorbed = %d, want 1", absorbed)
	}

	a, _ := lib.GetPhoto("/a.jpg")
	if got := a.Tags; !sameOrder(got, []string{"red", "cats", "blue"}) {
		t.Fatalf("/a.jpg tags = %v, want [red cats blue] (order preserved)", got)
	}
	b, _ := lib.GetPhoto("/b.jpg")
	if got := b.Tags; !sameOrder(got, []string{"cats"}) {
		t.Fatalf("/b.jpg tags = %v, want [cats] (source absorbed)", got)
	}
}

func TestLibrary_RenameTag_Invalid(t *testing.T) {
	lib := New()
	if _, _, err := lib.RenameTag("", "new"); err == nil {
		t.Fatalf("expected error when renaming empty tag")
	}
	if _, _, err := lib.RenameTag("old", ""); err == nil {
		t.Fatalf("expected error when target is empty")
	}
	if _, _, err := lib.RenameTag("Same", "same"); err == nil {
		t.Fatalf("expected error when from and to normalize to the same value")
	}
}

func TestLibrary_MergeTags(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/1.jpg", Tags: []string{"cats"}})
	lib.AddPhoto(&Photo{Path: "/2.jpg", Tags: []string{"feline"}})
	lib.AddPhoto(&Photo{Path: "/3.jpg", Tags: []string{"cats", "feline"}})
	lib.AddPhoto(&Photo{Path: "/4.jpg", Tags: []string{"dog"}})

	renamed, absorbed, err := lib.MergeTags("cats", "feline")
	if err != nil {
		t.Fatalf("MergeTags: unexpected error: %v", err)
	}
	if renamed != 1 {
		t.Fatalf("renamed = %d, want 1 (photo /1 had only cats)", renamed)
	}
	if absorbed != 1 {
		t.Fatalf("absorbed = %d, want 1 (photo /3 had both)", absorbed)
	}

	p1, _ := lib.GetPhoto("/1.jpg")
	if !sameOrder(p1.Tags, []string{"feline"}) {
		t.Fatalf("/1 tags = %v, want [feline]", p1.Tags)
	}
	p3, _ := lib.GetPhoto("/3.jpg")
	if !sameOrder(p3.Tags, []string{"feline"}) {
		t.Fatalf("/3 tags = %v, want [feline] (cats dropped, no dup)", p3.Tags)
	}
	p4, _ := lib.GetPhoto("/4.jpg")
	if !sameOrder(p4.Tags, []string{"dog"}) {
		t.Fatalf("/4 tags = %v, want [dog] (untouched)", p4.Tags)
	}
}

func TestLibrary_DeleteTag(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/1.jpg", Tags: []string{"a", "b", "c"}})
	lib.AddPhoto(&Photo{Path: "/2.jpg", Tags: []string{"b"}})
	lib.AddPhoto(&Photo{Path: "/3.jpg", Tags: []string{"c"}})

	affected := lib.DeleteTag("b")
	if affected != 2 {
		t.Fatalf("affected = %d, want 2", affected)
	}
	p1, _ := lib.GetPhoto("/1.jpg")
	if !sameOrder(p1.Tags, []string{"a", "c"}) {
		t.Fatalf("/1 tags = %v, want [a c]", p1.Tags)
	}
	p2, _ := lib.GetPhoto("/2.jpg")
	if len(p2.Tags) != 0 {
		t.Fatalf("/2 tags = %v, want []", p2.Tags)
	}

	// Deleting an absent tag is a no-op.
	if n := lib.DeleteTag("nope"); n != 0 {
		t.Fatalf("DeleteTag(absent) = %d, want 0", n)
	}
	// Blank input is a no-op without error.
	if n := lib.DeleteTag("   "); n != 0 {
		t.Fatalf("DeleteTag(blank) = %d, want 0", n)
	}
}

func sameOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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

func TestLibrary_RenamePhoto(t *testing.T) {
	lib := New()
	p := &Photo{Path: "/a/b.jpg", Name: "b.jpg", SizeBytes: 42}
	lib.AddPhoto(p)

	if err := lib.RenamePhoto("/a/b.jpg", "/x/y/c.jpg"); err != nil {
		t.Fatalf("RenamePhoto returned error: %v", err)
	}

	if _, err := lib.GetPhoto("/a/b.jpg"); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("old path still in library after rename")
	}
	got, err := lib.GetPhoto("/x/y/c.jpg")
	if err != nil {
		t.Fatalf("new path not in library: %v", err)
	}
	if got != p {
		t.Fatalf("same pointer should be re-keyed, got different instance")
	}
	if got.Path != "/x/y/c.jpg" || got.Name != "c.jpg" {
		t.Fatalf("Path/Name not updated: got %q / %q", got.Path, got.Name)
	}
}

func TestLibrary_RenamePhoto_MissingSource(t *testing.T) {
	lib := New()
	err := lib.RenamePhoto("/missing.jpg", "/new.jpg")
	if !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("expected ErrPhotoNotFound, got %v", err)
	}
}

func TestLibrary_RenamePhoto_DestinationExists(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg"})
	lib.AddPhoto(&Photo{Path: "/b.jpg", Name: "b.jpg"})

	err := lib.RenamePhoto("/a.jpg", "/b.jpg")
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected ErrDestinationExists, got %v", err)
	}
	// Library must be unchanged on rejection.
	if _, err := lib.GetPhoto("/a.jpg"); err != nil {
		t.Fatalf("source should remain after rejected rename: %v", err)
	}
	if _, err := lib.GetPhoto("/b.jpg"); err != nil {
		t.Fatalf("destination should remain after rejected rename: %v", err)
	}
}

func TestLibrary_RenamePhoto_Noop(t *testing.T) {
	lib := New()
	p := &Photo{Path: "/a.jpg", Name: "a.jpg"}
	lib.AddPhoto(p)
	if err := lib.RenamePhoto("/a.jpg", "/a.jpg"); err != nil {
		t.Fatalf("same-path rename should be a no-op, got error: %v", err)
	}
	if lib.Len() != 1 {
		t.Fatalf("noop rename changed Len")
	}
}

func TestLibrary_RemovePhoto(t *testing.T) {
	lib := New()
	p := &Photo{Path: "/a.jpg", Name: "a.jpg", Tags: []string{"x"}}
	lib.AddPhoto(p)

	removed, err := lib.RemovePhoto("/a.jpg")
	if err != nil {
		t.Fatalf("RemovePhoto returned error: %v", err)
	}
	if removed != p {
		t.Fatalf("RemovePhoto returned different pointer than inserted")
	}
	if _, err := lib.GetPhoto("/a.jpg"); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("photo still present after RemovePhoto")
	}
}

func TestLibrary_RemovePhoto_Missing(t *testing.T) {
	lib := New()
	_, err := lib.RemovePhoto("/missing.jpg")
	if !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("expected ErrPhotoNotFound, got %v", err)
	}
}

func TestLibrary_RenamePhoto_MarksIndicesDirty(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/a.jpg", Name: "a.jpg", SizeBytes: 1})
	// Force a rebuild so dirty is false.
	_ = lib.Sorted(SortName, false)
	if lib.dirty {
		t.Fatalf("precondition: dirty should be false after Sorted")
	}
	if err := lib.RenamePhoto("/a.jpg", "/b.jpg"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !lib.dirty {
		t.Fatalf("RenamePhoto must mark indices dirty")
	}
}

func TestLibrary_UntaggedByFolder(t *testing.T) {
	lib := New()
	lib.AddPhoto(&Photo{Path: "/root/a/two.jpg", Name: "two.jpg"})
	lib.AddPhoto(&Photo{Path: "/root/a/one.jpg", Name: "one.jpg"})
	lib.AddPhoto(&Photo{Path: "/root/b/three.jpg", Name: "three.jpg"})
	lib.AddPhoto(&Photo{Path: "/root/a/tagged.jpg", Name: "tagged.jpg", Tags: []string{"holiday"}})
	// AutoTags alone must not exclude a photo from the untagged view.
	lib.AddPhoto(&Photo{Path: "/root/b/auto.jpg", Name: "auto.jpg", AutoTags: []string{"camera:canon"}})

	buckets := lib.UntaggedByFolder()
	if len(buckets) != 2 {
		t.Fatalf("UntaggedByFolder returned %d buckets, want 2", len(buckets))
	}
	if buckets[0].Folder != "/root/a" || buckets[1].Folder != "/root/b" {
		t.Fatalf("folders not sorted ascending: %+v", []string{buckets[0].Folder, buckets[1].Folder})
	}

	wantA := []string{"one.jpg", "two.jpg"}
	if len(buckets[0].Photos) != len(wantA) {
		t.Fatalf("bucket[a] has %d photos, want %d", len(buckets[0].Photos), len(wantA))
	}
	for i, want := range wantA {
		if buckets[0].Photos[i].Name != want {
			t.Fatalf("bucket[a].Photos[%d].Name = %q, want %q", i, buckets[0].Photos[i].Name, want)
		}
	}

	if len(buckets[1].Photos) != 2 {
		t.Fatalf("bucket[b] should contain auto-tagged and plain photo, got %d", len(buckets[1].Photos))
	}
}
