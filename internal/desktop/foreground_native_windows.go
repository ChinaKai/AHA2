//go:build windows

package desktop

import (
	"encoding/json"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The union is sized like MOUSEINPUT, the largest native INPUT member.
type foregroundCleanupInput struct {
	kind uint32
	data struct {
		x, y             int32
		mouseData, flags uint32
		time             uint32
		extra            uintptr
	}
}

func cleanupKeyboard(key, scan uint16, flags uint32) foregroundCleanupInput {
	input := foregroundCleanupInput{kind: 1}
	*(*uint16)(unsafe.Pointer(&input.data)) = key
	*(*uint16)(unsafe.Add(unsafe.Pointer(&input.data), 2)) = scan
	*(*uint32)(unsafe.Add(unsafe.Pointer(&input.data), 4)) = flags
	return input
}

func cleanupForegroundEvents(request nativeRequest) []foregroundCleanupInput {
	if request.Operation == "target_select" {
		return []foregroundCleanupInput{cleanupKeyboard(0x27, 0, 3), cleanupKeyboard(0x25, 0, 3),
			cleanupKeyboard('D', 0, 2), cleanupKeyboard(0x11, 0, 2), cleanupKeyboard(0x5B, 0, 3)}
	}
	if request.Operation == "foreground_new_desktop" {
		request.Action = Action{Kind: "key", Keys: []string{"WIN", "CTRL", "D"}}
	} else if request.Operation != "foreground_act" {
		return nil
	}
	a := request.Action
	if a.Kind == "click" || a.Kind == "double_click" || a.Kind == "drag" {
		input := foregroundCleanupInput{}
		input.data.flags = 4
		if a.Button == "right" {
			input.data.flags = 16
		} else if a.Button == "middle" {
			input.data.flags = 64
		}
		return []foregroundCleanupInput{input}
	}
	var result []foregroundCleanupInput
	if a.Kind == "text" && len(a.Value) <= 4096 {
		seen := map[uint16]bool{}
		for _, unit := range utf16.Encode([]rune(a.Value)) {
			if unit == '\r' || unit == '\n' {
				unit = 13
			}
			if !seen[unit] {
				if unit == 13 || unit == 9 {
					result = append(result, cleanupKeyboard(unit, 0, 2))
				} else {
					result = append(result, cleanupKeyboard(0, unit, 6))
				}
				seen[unit] = true
			}
		}
	} else if a.Kind == "key" && validateNativeKeys(a.Keys) == nil {
		named := map[string]uint16{
			"CTRL": 0x11, "ALT": 0x12, "SHIFT": 0x10, "WIN": 0x5B, "ENTER": 13, "ESC": 27, "ESCAPE": 27,
			"TAB": 9, "SPACE": 32, "BACKSPACE": 8, "DELETE": 0x2E, "INSERT": 0x2D, "HOME": 0x24, "END": 0x23,
			"PAGEUP": 0x21, "PAGEDOWN": 0x22, "LEFT": 0x25, "UP": 0x26, "RIGHT": 0x27, "DOWN": 0x28,
			"F1": 0x70, "F2": 0x71, "F3": 0x72, "F4": 0x73, "F5": 0x74, "F6": 0x75,
			"F7": 0x76, "F8": 0x77, "F9": 0x78, "F10": 0x79, "F11": 0x7A, "F12": 0x7B,
		}
		for i := len(a.Keys) - 1; i >= 0; i-- {
			key := named[a.Keys[i]]
			if len(a.Keys[i]) == 1 {
				key = uint16(a.Keys[i][0])
			}
			flags := uint32(2)
			if key == 0x5B || key >= 0x21 && key <= 0x2E {
				flags |= 1
			}
			result = append(result, cleanupKeyboard(key, 0, flags))
		}
	}
	return result
}

func cleanupForegroundInput(packet []byte) {
	var decoded struct {
		Request nativeRequest `json:"request"`
	}
	if json.Unmarshal(packet, &decoded) != nil {
		return
	}
	events := cleanupForegroundEvents(decoded.Request)
	if decoded.Request.Operation == "foreground_act" {
		// Activation may have issued an ALT tap or verified-caption click before
		// the requested action, including before a text/key request.
		events = append(events, cleanupKeyboard(0x12, 0, 2))
		leftUp := foregroundCleanupInput{}
		leftUp.data.flags = 4
		events = append(events, leftUp)
	}
	if len(events) == 0 {
		return
	}
	user := windows.NewLazySystemDLL("user32.dll")
	desktop, _, _ := user.NewProc("OpenInputDesktop").Call(0, 0, 1)
	if desktop == 0 {
		return
	}
	defer user.NewProc("CloseDesktop").Call(desktop)
	var name [256]uint16
	var needed uint32
	ok, _, _ := user.NewProc("GetUserObjectInformationW").Call(desktop, 2, uintptr(unsafe.Pointer(&name[0])),
		uintptr(len(name)*2), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 || !strings.EqualFold(windows.UTF16ToString(name[:]), "Default") {
		return
	}
	for len(events) > 0 {
		n := min(64, len(events))
		user.NewProc("SendInput").Call(uintptr(n), uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
		events = events[n:]
	}
}
