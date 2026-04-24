package platform

import (
	"fmt"
	"os/exec"
	"strings"
)

// copyFileToClipboard on macOS uses osascript to set the clipboard
// to a POSIX file reference. That's the same format Finder's Copy
// produces, so pasting into Finder, Mail, Messages, or any app that
// accepts file drops works identically.
func copyFileToClipboard(path string) error {
	script := fmt.Sprintf(`set the clipboard to (POSIX file %q)`, path)
	cmd := exec.Command("osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copying %s to clipboard via osascript: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
