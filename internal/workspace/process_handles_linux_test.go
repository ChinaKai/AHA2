//go:build linux

package workspace

import "os"

// countOpenProcessHandles reports the number of open file descriptors in this
// process. Listing /proc/self/fd opens one descriptor itself, which is present
// in both the before and after measurements.
func countOpenProcessHandles() (int, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return len(entries), true
}
