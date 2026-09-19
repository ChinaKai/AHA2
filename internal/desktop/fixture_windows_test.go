//go:build windows

package desktop

import (
	_ "embed"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The fixture script and the input-state probe are shared by the native worker
// and foreground tests. Background control was removed, so only these helpers
// remain from what used to be a background-provider test.
//
//go:embed fixture.ps1
var fixtureScript string

func fixtureInputState() [2]uintptr {
	user32 := windows.NewLazySystemDLL("user32.dll")
	foreground, _, _ := user32.NewProc("GetForegroundWindow").Call()
	var info struct {
		Size, Flags                                   uint32
		Active, Focus, Capture, Menu, MoveSize, Caret uintptr
		Left, Top, Right, Bottom                      int32
	}
	info.Size = uint32(unsafe.Sizeof(info))
	user32.NewProc("GetGUIThreadInfo").Call(0, uintptr(unsafe.Pointer(&info)))
	return [2]uintptr{foreground, info.Focus}
}
