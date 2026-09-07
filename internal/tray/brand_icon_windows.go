//go:build windows

package tray

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	iconResourceVersion = 0x00030000
	lrDefaultColor      = 0x00000000
)

var (
	brandIconUser32              = windows.NewLazySystemDLL("user32.dll")
	procCreateIconFromResourceEx = brandIconUser32.NewProc("CreateIconFromResourceEx")
	procDestroyBrandIcon         = brandIconUser32.NewProc("DestroyIcon")
)

func createBrandIcon() uintptr {
	resource := brandIconResource()
	if len(resource) == 0 {
		return 0
	}
	icon, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&resource[0])),
		uintptr(len(resource)),
		1,
		iconResourceVersion,
		brandIconSize,
		brandIconSize,
		lrDefaultColor,
	)
	return icon
}

func destroyBrandIcon(icon uintptr) {
	if icon != 0 {
		procDestroyBrandIcon.Call(icon)
	}
}
