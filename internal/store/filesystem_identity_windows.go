//go:build windows

package store

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// sameFilesystemByPlatform compares Windows volume serial numbers when both
// paths can be opened. If a handle is unavailable it falls back to the volume
// name; an empty name reports unknown so the caller requires the conservative
// combined space.
func sameFilesystemByPlatform(first, second string) (same, known bool) {
	firstSerial, firstErr := volumeSerial(first)
	secondSerial, secondErr := volumeSerial(second)
	if firstErr == nil && secondErr == nil {
		return firstSerial == secondSerial, true
	}
	firstVolume := strings.ToUpper(strings.TrimRight(filepath.VolumeName(filepath.Clean(first)), `\/`))
	secondVolume := strings.ToUpper(strings.TrimRight(filepath.VolumeName(filepath.Clean(second)), `\/`))
	if firstVolume == "" || secondVolume == "" {
		return false, false
	}
	return firstVolume == secondVolume, true
}

func volumeSerial(path string) (uint32, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, err
	}
	return info.VolumeSerialNumber, nil
}
