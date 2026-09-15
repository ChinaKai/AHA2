//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Tests only handles created by this helper. No global enumeration, capture,
// activation, keyboard input or inspection of existing user windows.
func TestNativePickerOwnedWindows(t *testing.T) {
	if os.Getenv("AHA_NATIVE_PICKER_TEST") != "1" {
		t.Skip("opt-in non-activating owned-window picker fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	const script = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$setup = [Console]::In.ReadToEnd() | ConvertFrom-Json
Add-Type -TypeDefinition $setup.source -ReferencedAssemblies @(
    'Accessibility', 'UIAutomationClient', 'UIAutomationTypes', 'WindowsBase',
    'System.Drawing', 'System.Web.Extensions'
) -ErrorAction Stop
[AHADesktop.Native]::TestPickerWindows()
`
	input, err := json.Marshal(map[string]string{"source": nativeSource + pickerFixtureSource})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden",
		"-EncodedCommand", encodeNativeCommand(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + dir}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Stdin = strings.NewReader(string(input))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("owned picker helper failed: %v\n%s", err, output)
	}
	var result struct {
		OK             bool     `json:"ok"`
		Cases          []string `json:"cases"`
		FocusUnchanged bool     `json:"focus_unchanged"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid fixture output: %v", err)
	}
	if !result.OK || !result.FocusUnchanged || len(result.Cases) != 13 {
		t.Fatalf("unexpected owned-window result: %+v", result)
	}
	t.Logf("owned picker cases=%d, foreground unchanged; no user-window enumeration or input", len(result.Cases))
	p := NativeProvider().(*nativeProvider)
	defer p.Close()
	_, err = p.SelectTarget(ctx, TargetSelection{Kind: "window", WindowID: "invalid-fixture-id"})
	if err == nil || !strings.Contains(err.Error(), "stale_target") {
		t.Fatalf("structured target worker did not reject malformed fixture ID: %v", err)
	}
}

const pickerFixtureSource = `
namespace AHADesktop {
    using System;
    using System.Collections.Generic;
    using System.Runtime.InteropServices;
    public static partial class Native {
        [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "CreateWindowExW")]
        static extern IntPtr PickerCreateWindow(int extended, string className, string title, uint style,
            int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr parameter);
        [DllImport("user32.dll", EntryPoint = "ShowWindow")] static extern bool PickerShowWindow(IntPtr hwnd, int command);
        [DllImport("user32.dll", EntryPoint = "DestroyWindow")] static extern bool PickerDestroyWindow(IntPtr hwnd);

        static IntPtr PickerMake(List<IntPtr> owned, int extended, uint style, int width, int height,
                string title, IntPtr parent, bool show) {
            IntPtr hwnd = PickerCreateWindow(extended, "STATIC", title, style,
                24, 24, width, height, parent, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero);
            if (hwnd == IntPtr.Zero) throw new Exception("fixture_create_failed");
            owned.Add(hwnd);
            if (show) PickerShowWindow(hwnd, 4);
            return hwnd;
        }
        static void PickerExpect(List<string> cases, IntPtr hwnd, bool expected, string name) {
            bool allowed = false;
            try { allowed = InspectPickerWindow(hwnd) != null; } catch (NativeFailure) { }
            if (allowed != expected) throw new Exception("picker_case_" + name);
            cases.Add(name);
        }
        public static string TestPickerWindows() {
            if (SetThreadDpiAwarenessContext(new IntPtr(-4)) == IntPtr.Zero)
                throw new Exception("fixture_dpi_failed");
            IntPtr foreground = GetForegroundWindow();
            var owned = new List<IntPtr>();
            var cases = new List<string>();
            const uint Normal = 0x00CF0000, Popup = 0x80000000, Child = 0x40000000;
            try {
                IntPtr normal = PickerMake(owned, 0, Normal, 320, 220, "Picker fixture", IntPtr.Zero, true);
                PickerExpect(cases, normal, true, "normal");
                PickerExpect(cases, PickerMake(owned, 0, Normal, 320, 220, "", IntPtr.Zero, true), true, "untitled");
                PickerExpect(cases, PickerMake(owned, 0, Popup, 320, 220, "", IntPtr.Zero, true), true, "borderless");
                PickerExpect(cases, PickerMake(owned, 0x80, Normal, 320, 220, "Tool", IntPtr.Zero, true), false, "tool");
                PickerExpect(cases, PickerMake(owned, 0x08000000, Popup, 320, 220, "Passive", IntPtr.Zero, true), false, "no_activate");
                PickerExpect(cases, PickerMake(owned, 0x08040080, Normal, 320, 220, "Explicit app", IntPtr.Zero, true), true, "explicit_app");
                PickerExpect(cases, PickerMake(owned, 0, Popup, 0, 220, "Zero width", IntPtr.Zero, true), false, "zero_width");
                PickerExpect(cases, PickerMake(owned, 0, Popup, 320, 0, "Zero height", IntPtr.Zero, true), false, "zero_height");
                PickerExpect(cases, PickerMake(owned, 0, Popup, 0, 0, "Zero area", IntPtr.Zero, true), false, "zero_area");
                PickerExpect(cases, PickerMake(owned, 0, Child, 120, 80, "Child", normal, true), false, "child");
                PickerExpect(cases, PickerMake(owned, 0, Normal, 320, 220, "Hidden", IntPtr.Zero, false), false, "hidden");
                PickerExpect(cases, PickerMake(owned, 0, Popup | 0x00C80000, 320, 220, "Dialog", normal, true), true, "owned_dialog");
                PickerShowWindow(normal, 7);
                if (!IsIconic(normal)) throw new Exception("fixture_minimize_failed");
                PickerExpect(cases, normal, true, "minimized");
            } finally {
                for (int i = owned.Count - 1; i >= 0; i--) PickerDestroyWindow(owned[i]);
            }
            return JSON.Serialize(Obj("ok", true, "cases", cases,
                "focus_unchanged", GetForegroundWindow() == foreground));
        }
    }
}
`
