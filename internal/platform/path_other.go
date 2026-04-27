//go:build !darwin

package platform

// EnsureToolPath is a no-op on non-Darwin platforms; their GUI launchers
// typically inherit a usable PATH from the desktop session.
func EnsureToolPath() {}
