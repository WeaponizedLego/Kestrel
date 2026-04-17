// Package platform provides OS-specific helpers that the rest of the
// application shouldn't need to care about — browser launching today,
// config-dir paths and memory detection in later phases.
package platform

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser opens url in the user's default browser. The call returns
// as soon as the OS handler is invoked; errors from the child process
// itself are not surfaced here.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("opening browser for %s: unsupported platform %q", url, runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening browser for %s: %w", url, err)
	}
	return nil
}
