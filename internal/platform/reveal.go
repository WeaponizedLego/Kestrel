package platform

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RevealInFileManager opens the user's default file manager pointed at
// path. macOS uses `open -R` to select the file in Finder; Linux falls
// back to opening the parent directory via xdg-open because file
// managers across desktop environments disagree on the "select file"
// flag. Windows is intentionally unsupported for now.
func RevealInFileManager(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("revealing %s: path must be absolute", path)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "linux":
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	default:
		return fmt.Errorf("revealing %s: unsupported platform %q", path, runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("revealing %s in file manager: %w", path, err)
	}
	return nil
}
