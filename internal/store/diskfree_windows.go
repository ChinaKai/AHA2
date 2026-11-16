//go:build windows

package store

import "golang.org/x/sys/windows"

func diskSpaceFree(path string) (int64, error) {
	var free, total, available uint64
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(pointer, &free, &total, &available); err != nil {
		return 0, err
	}
	return int64(free), nil
}
