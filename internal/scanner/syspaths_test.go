package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsSystemPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}

	type tc struct {
		name string
		path string
		want bool
	}

	var cases []tc
	switch runtime.GOOS {
	case "darwin":
		cases = []tc{
			{"library", filepath.Join(home, "Library"), true},
			{"library_subdir", filepath.Join(home, "Library", "Calendars"), true},
			{"trash", filepath.Join(home, ".Trash"), true},
			{"system_root", "/System/Library", true},
			{"private_var", "/private/var/folders/x/y", true},
			{"system_library_root", "/Library/Preferences", true},
			{"pictures", filepath.Join(home, "Pictures"), false},
			{"user_home", home, false},
			{"volumes_drive", "/Volumes/PhotoDrive", false},
			{"library_lookalike", filepath.Join(home, "LibraryNotes"), false},
		}
	case "linux":
		cases = []tc{
			{"proc", "/proc/1234", true},
			{"sys", "/sys/devices", true},
			{"dev", "/dev/null", true},
			{"var", "/var/log", true},
			{"home", filepath.Join(home, "Pictures"), false},
			{"proc_lookalike", "/procfs", false},
		}
	case "windows":
		cases = []tc{
			{"pictures", filepath.Join(home, "Pictures"), false},
			{"user_home", home, false},
		}
		if v := os.Getenv("WINDIR"); v != "" {
			cases = append(cases, tc{"windir", filepath.Join(v, "System32"), true})
		}
		if v := os.Getenv("APPDATA"); v != "" {
			cases = append(cases, tc{"appdata", filepath.Join(v, "Microsoft"), true})
		}
	default:
		t.Skip("unsupported platform")
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsSystemPath(c.path)
			if got != c.want {
				t.Errorf("IsSystemPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestIsSystemPath_Empty(t *testing.T) {
	if IsSystemPath("") {
		t.Error("empty path should not be a system path")
	}
}
