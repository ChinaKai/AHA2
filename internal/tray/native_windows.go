//go:build windows

package tray

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmNull          = 0x0000
	wmContextMenu   = 0x007B
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203
	wmApp           = 0x8000
	trayCallback    = wmApp + 1

	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfGrayed    = 0x00000001

	tpmRightButton = 0x0002

	commandOpen    = 1001
	commandStart   = 1002
	commandStop    = 1003
	commandRestart = 1004
	commandExit    = 1005

	swHide         = 0
	idiApplication = 32512
	csDoubleClicks = 0x0008
)

type point struct{ X, Y int32 }

type message struct {
	HWnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  uintptr
}

type notifyIconData struct {
	Size             uint32
	Window           uintptr
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	VersionOrTimeout uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUID             windows.GUID
	BalloonIcon      uintptr
}

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessage      = user32.NewProc("PostMessageW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenu       = user32.NewProc("AppendMenuW")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procLoadIcon         = user32.NewProc("LoadIconW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	procShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	procShellExecute     = shell32.NewProc("ShellExecuteW")
	windowInstances      sync.Map
	wndProcCallback      = syscall.NewCallback(windowProc)
)

type nativeTray struct {
	window     uintptr
	supervisor *Supervisor
	config     Config
}

func hideConsoleWindow() {
	window, _, _ := procGetConsoleWindow.Call()
	if window != 0 {
		procShowWindow.Call(window, swHide)
	}
}

func runNativeTray(supervisor *Supervisor, config Config) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className, _ := windows.UTF16PtrFromString("AHA2TrayWindow")
	title, _ := windows.UTF16PtrFromString("AHA2")
	instance, _, _ := procGetModuleHandle.Call(0)
	icon := createBrandIcon()
	brandIcon := icon != 0
	if !brandIcon {
		icon, _, _ = procLoadIcon.Call(0, idiApplication)
	}
	if brandIcon {
		defer destroyBrandIcon(icon)
	}
	class := wndClassEx{
		Size: uint32(unsafe.Sizeof(wndClassEx{})), Style: csDoubleClicks, WndProc: wndProcCallback,
		Instance: instance, Icon: icon, IconSmall: icon, ClassName: className,
	}
	if atom, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("register tray window: %w", callErr)
	}
	window, _, callErr := procCreateWindowEx.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0,
		0, 0, 0, 0, 0, 0, instance, 0,
	)
	if window == 0 {
		return fmt.Errorf("create tray window: %w", callErr)
	}
	tray := &nativeTray{window: window, supervisor: supervisor, config: config}
	windowInstances.Store(window, tray)
	defer windowInstances.Delete(window)
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: trayCallback, Icon: icon}
	copy(data.Tip[:], windows.StringToUTF16("AHA2"))
	if ok, _, callErr := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data))); ok == 0 {
		procDestroyWindow.Call(window)
		return fmt.Errorf("add tray icon: %w", callErr)
	}
	defer procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))

	var msg message
	for {
		result, _, callErr := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("read tray message: %w", callErr)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func windowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	value, ok := windowInstances.Load(window)
	if !ok {
		result, _, _ := procDefWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	}
	tray := value.(*nativeTray)
	switch message {
	case trayCallback:
		switch uint32(lParam) {
		case wmRButtonUp, wmContextMenu:
			tray.showMenu()
		case wmLButtonDblClk:
			tray.open()
		}
		return 0
	case wmCommand:
		tray.command(uint16(wParam & 0xffff))
		return 0
	case wmClose:
		procDestroyWindow.Call(window)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProc.Call(window, uintptr(message), wParam, lParam)
	return result
}

func (t *nativeTray) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	snapshot := t.supervisor.Snapshot()
	appendMenu(menu, mfString, commandOpen, "打开 AHA2")
	appendMenu(menu, menuFlags(snapshot.State != StateStopped && snapshot.State != StateFailed), commandStart, "启动")
	appendMenu(menu, menuFlags(snapshot.State == StateStopped || snapshot.State == StateExited), commandStop, "停止")
	appendMenu(menu, mfString, commandRestart, "重启")
	appendMenu(menu, mfSeparator, 0, "")
	status := "状态: " + string(snapshot.State)
	if snapshot.LastError != "" {
		status += " - " + truncate(snapshot.LastError, 60)
	}
	appendMenu(menu, mfString|mfGrayed, 0, status)
	version := strings.TrimSpace(t.config.Version)
	if version == "" {
		version = "dev"
	}
	appendMenu(menu, mfString|mfGrayed, 0, "版本: "+version)
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, commandExit, "退出 AHA2")
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForeground.Call(t.window)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(cursor.X), uintptr(cursor.Y), 0, t.window, 0)
	procPostMessage.Call(t.window, wmNull, 0, 0)
}

func menuFlags(disabled bool) uint32 {
	if disabled {
		return mfString | mfGrayed
	}
	return mfString
}

func appendMenu(menu uintptr, flags uint32, id uint16, label string) {
	text, _ := windows.UTF16PtrFromString(label)
	procAppendMenu.Call(menu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(text)))
}

func (t *nativeTray) command(id uint16) {
	switch id {
	case commandOpen:
		t.open()
	case commandStart:
		_ = t.supervisor.Start()
	case commandStop:
		_ = t.supervisor.Stop()
	case commandRestart:
		_ = t.supervisor.Restart()
	case commandExit:
		go func() {
			_ = t.supervisor.Exit()
			<-t.supervisor.Done()
			procPostMessage.Call(t.window, wmClose, 0, 0)
		}()
	}
}

func (t *nativeTray) open() {
	operation, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(ManagementURL(t.config.Listen))
	procShellExecute.Call(t.window, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
