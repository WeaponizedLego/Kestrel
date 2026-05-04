package scanner

import "testing"

func TestShouldSkipDir(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"node_modules", true},
		{"__pycache__", true},
		{".git", true},
		{".cache", true},
		{"Pictures", false},
		{"2025-vacation", false},
		{"", false},
		{".", false},
		{"..", false},

		// Bundle suffixes: macOS treats these as opaque files.
		{"Battle.net-Setup.app", true},
		{"Shortcat.app", true},
		{"Some.Bundle", true},
		{"My.Framework", true},
		{"family.photoslibrary", true},
		{"library.musiclibrary", true},

		// Lookalikes that must not match.
		{"appendix", false},
		{"sapphire", false},
		{"frameworks-notes", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldSkipDir(c.name); got != c.want {
				t.Errorf("ShouldSkipDir(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestPathHasSkippedComponent(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/glen/Pictures/2025", false},
		{"/Users/glen/Downloads/Battle.net-Setup.app", true},
		{"/Users/glen/Downloads/Battle.net-Setup.app/Contents/Resources", true},
		{"/Users/glen/Pictures/family.photoslibrary/originals", true},
		{"/Users/glen/.cache/foo", true},
		{"/Users/glen/projects/node_modules/foo", true},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := PathHasSkippedComponent(c.path); got != c.want {
				t.Errorf("PathHasSkippedComponent(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}
