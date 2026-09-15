//go:build windows

package desktop

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed fixture.ps1
var fixtureScript string

// This test opens one non-activating fixture window. It never enumerates,
// screenshots or controls the user's existing applications.
func TestNativeOwnWindowFixture(t *testing.T) {
	if os.Getenv("AHA_DESKTOP_FIXTURE_TEST") != "1" {
		t.Skip("opt-in native fixture; opens a non-activating test window")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden",
		"-EncodedCommand", encodeNativeCommand(fixtureScript))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + dir}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	var window Window
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &window) != nil || window.ID == "" {
		t.Fatal("fixture did not produce its window ID")
	}
	provider := NativeProvider()
	t.Cleanup(func() { _ = provider.(*nativeProvider).Close() })
	if supported, reason := provider.Support(); !supported {
		t.Fatalf("native provider unsupported: %s", reason)
	}
	observation, err := provider.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Image == "" {
		t.Fatalf("fixture has no image: %s", observation.CaptureError)
	}
	imageBytes, err := base64.StdEncoding.DecodeString(observation.Image)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(imageBytes))
	if err != nil || image.Bounds().Dx() < 100 || image.Bounds().Dy() < 100 {
		t.Fatalf("invalid fixture image: %v", err)
	}
	var editID, buttonID, toggleID, radioID, nativeEditID, nativeButtonID string
	var masked bool
	for _, element := range observation.Elements {
		if strings.Contains(element.Value, "fixture-private-sample") {
			t.Fatal("password sample was exposed as element text")
		}
		if element.Value == "initial-fixture" {
			editID = element.ID
		}
		if element.Value == "native-initial" {
			nativeEditID = element.ID
		}
		if element.Name == "Fixture Invoke" {
			buttonID = element.ID
		}
		if element.Name == "Fixture Toggle" {
			toggleID = element.ID
			t.Logf("fixture toggle actions: %v", element.Actions)
		}
		if element.Name == "Fixture Radio" {
			radioID = element.ID
		}
		if element.Name == "Native Invoke" {
			nativeButtonID = element.ID
		}
		if element.Role == "password" {
			if element.Value != "" || element.Name != "" || len(element.Actions) != 0 {
				t.Fatal("password element metadata was exposed")
			}
			x, y := int(element.X+element.Width/2), int(element.Y+element.Height/2)
			r, g, b, _ := image.At(x, y).RGBA()
			masked = r == 0 && g == 0 && b == 0
			if err := provider.Act(ctx, window, Action{Kind: "set_value", ElementID: element.ID, Value: "not-allowed"}); err == nil {
				t.Fatal("password action accepted")
			}
		}
	}
	if editID == "" || buttonID == "" || toggleID == "" || radioID == "" || !masked {
		t.Fatalf("fixture controls missing: edit=%t button=%t toggle=%t radio=%t masked=%t", editID != "", buttonID != "", toggleID != "", radioID != "", masked)
	}
	if nativeEditID == "" || nativeButtonID == "" {
		t.Fatal("native Win32 fixture controls were not observed")
	}
	for _, action := range []Action{
		{Kind: "set_value", ElementID: editID, Value: "updated-fixture"},
		{Kind: "toggle", ElementID: toggleID},
		{Kind: "select", ElementID: radioID},
		{Kind: "invoke", ElementID: buttonID},
		{Kind: "set_value", ElementID: nativeEditID, Value: "native-updated"},
		{Kind: "invoke", ElementID: nativeButtonID},
	} {
		before := fixtureInputState()
		if err := provider.Act(ctx, window, action); err != nil {
			after := fixtureInputState()
			t.Fatalf("fixture %s: %v (foreground_changed=%t, foreground_focus_changed=%t)",
				action.Kind, err, before[0] != after[0], before[1] != after[1])
		}
		if after := fixtureInputState(); before != after {
			t.Fatalf("fixture %s changed real foreground input", action.Kind)
		}
		if action.Kind == "set_value" {
			check, err := provider.Observe(ctx, window)
			if err != nil {
				t.Fatal(err)
			}
			var changed bool
			for _, element := range check.Elements {
				changed = changed || element.ID == action.ElementID && element.Value == action.Value
			}
			if !changed {
				t.Fatal("set_value did not update the target")
			}
		}
	}
	after, err := provider.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	var clicked, toggled, selected, nativeInvoked bool
	for _, element := range after.Elements {
		if element.Value == "invoked-fixture" {
			clicked = true
		}
		if element.Name == "Fixture Toggle" || element.Name == "Fixture Radio" {
			t.Logf("fixture state %s: role=%s value=%q", element.Name, element.Role, element.Value)
		}
		toggled = toggled || element.Name == "Fixture Toggle" && element.Value == "On"
		selected = selected || element.Name == "Fixture Radio" && element.Value == "Selected"
		nativeInvoked = nativeInvoked || element.Name == "native-invoked"
	}
	if !clicked || !toggled || !selected || !nativeInvoked {
		t.Fatalf("native state verification: invoked=%t toggled=%t selected=%t native-invoked=%t",
			clicked, toggled, selected, nativeInvoked)
	}
	if err := provider.Act(ctx, window, Action{Kind: "send_keys", ElementID: editID, Value: "x"}); err == nil {
		t.Fatal("raw input fallback accepted")
	}
	t.Logf("native fixture: PNG %dx%d, %d controls, password masked, set_value/toggle/select/invoke verified without foreground changes",
		image.Bounds().Dx(), image.Bounds().Dy(), len(observation.Elements))
}

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
