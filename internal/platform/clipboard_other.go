//go:build !linux && !darwin && !windows

package platform

func copyFileToClipboard(path string) error {
	return copyFileToClipboardUnsupported(path)
}
