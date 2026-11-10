//go:build !windows

package tray

// restrictLogAccess is a no-op off Windows, where the tray does not run.
func restrictLogAccess(string) error { return nil }
