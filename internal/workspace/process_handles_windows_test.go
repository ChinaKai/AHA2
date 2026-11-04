//go:build windows

package workspace

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var getProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func countOpenProcessHandles() (int, bool) {
	var count uint32
	ok, _, _ := getProcessHandleCount.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
	if ok == 0 {
		return 0, false
	}
	return int(count), true
}
