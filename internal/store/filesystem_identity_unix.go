//go:build !windows

package store

import "golang.org/x/sys/unix"

// sameFilesystemByPlatform compares the filesystem device IDs. known is false
// when either stat fails, so the caller can fall back to the conservative
// same-filesystem assumption.
func sameFilesystemByPlatform(first, second string) (same, known bool) {
	var firstStat, secondStat unix.Stat_t
	if err := unix.Stat(first, &firstStat); err != nil {
		return false, false
	}
	if err := unix.Stat(second, &secondStat); err != nil {
		return false, false
	}
	return firstStat.Dev == secondStat.Dev, true
}
