package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func copyFileToClipboard(path string) error {
	return copyLinux(fileURI(path) + "\r\n")
}

// copyLinux picks the first available CLI clipboard tool. On Wayland
// wl-copy is preferred; xclip is a fallback (works for many apps via
// XWayland's clipboard bridge).
func copyLinux(uriList string) error {
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	tried := []string{}
	if wayland {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return runWlCopy(uriList)
		}
		tried = append(tried, "wl-copy")
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return runXclip(uriList)
	}
	tried = append(tried, "xclip")
	if !wayland {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return runWlCopy(uriList)
		}
	}
	hint := "install wl-clipboard (Wayland) or xclip (X11)"
	return fmt.Errorf("copying to clipboard: no clipboard tool found in PATH (tried %s); %s", strings.Join(tried, ", "), hint)
}

// runWlCopy pipes uriList into wl-copy as a text/uri-list target.
// Stdout/stderr are intentionally left at /dev/null: wl-copy forks a
// background daemon that owns the selection until another client
// copies, and capturing those pipes would hang Run(). Setsid moves
// the daemon into its own session so a signal to kestrel doesn't
// tear the clipboard down.
func runWlCopy(uriList string) error {
	cmd := exec.Command("wl-copy", "--type", "text/uri-list")
	cmd.Stdin = strings.NewReader(uriList)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying to clipboard via wl-copy: %w", err)
	}
	return nil
}

// runXclip pipes uriList into xclip. xclip stays resident to serve
// paste requests, so we Start (not Run) and detach it from kestrel's
// session so it survives independently.
func runXclip(uriList string) error {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "text/uri-list")
	cmd.Stdin = strings.NewReader(uriList)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("copying to clipboard via xclip: %w", err)
	}
	return nil
}
