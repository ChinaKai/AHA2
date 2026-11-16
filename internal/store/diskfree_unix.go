//go:build !windows

package store

import "golang.org/x/sys/unix"

// diskSpaceFree reports the bytes available to the current user on the
// filesystem that holds path.
func diskSpaceFree(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
