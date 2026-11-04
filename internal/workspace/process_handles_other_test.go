//go:build !linux && !windows

package workspace

func countOpenProcessHandles() (int, bool) { return 0, false }
