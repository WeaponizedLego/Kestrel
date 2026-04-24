package platform

import (
	"fmt"
	"os/exec"
	"strings"
)

// copyFileToClipboard on Windows uses PowerShell's Set-Clipboard
// -Path, which writes a CF_HDROP "file drop" to the clipboard — the
// same format Explorer's Copy produces. Apps that accept
// dragged/copied files (Explorer, Outlook, chat apps, image viewers)
// pick it up directly.
func copyFileToClipboard(path string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Set-Clipboard", "-Path", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copying %s to clipboard via powershell: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
