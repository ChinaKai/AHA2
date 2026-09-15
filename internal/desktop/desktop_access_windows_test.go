//go:build windows

package desktop

import (
	"context"
	_ "embed"
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

//go:embed desktop_access_fixture.cs
var desktopAccessFixture string

func TestDesktopAccessWithOwnedWindowsAndSimulatedDesktopState(t *testing.T) {
	if os.Getenv("AHA_NATIVE_DESKTOP_ACCESS_TEST") != "1" {
		t.Skip("opt-in owned windows; simulated capture/desktop/input state")
	}
	source := nativeSource + "\n" + foregroundNativeSource + "\n" + nativePreviewSource
	hooks := [][2]string{
		{`[DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();`,
			`[DllImport("user32.dll", EntryPoint = "GetForegroundWindow")] static extern IntPtr FixtureRealForegroundWindow();`},
		{`[DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(IntPtr hwnd, int attr, out int value, int size);`,
			`[DllImport("dwmapi.dll", EntryPoint = "DwmGetWindowAttribute")] static extern int FixtureRealDwmGetWindowAttribute(IntPtr hwnd, int attr, out int value, int size);`},
		{"static Guid CurrentVirtualDesktop()", "static Guid NativeCurrentVirtualDesktop()"},
		{"static PhysicalMonitor ResolveMonitor(", "static PhysicalMonitor NativeResolveMonitor("},
		{"static string CaptureDesktop(", "static string NativeCaptureDesktop("},
		{"static string CapturePreview(", "static string NativeCapturePreview("},
		{"static void SendBatch(", "static void NativeSendBatch("},
		{"static Target InspectIdentity(", "static Target NativeInspectIdentity("},
		{"static Target InspectWindowProcessIdentity(", "static Target NativeInspectWindowProcessIdentity("},
		{"static extern int BrowserAccessibleWindow(", "static extern int NativeBrowserAccessibleWindow("},
		{"static bool TryWindowDesktop(", "static bool NativeTryWindowDesktop("},
		{"static List<Guid> VerifiedDesktopOrder(", "static List<Guid> NativeVerifiedDesktopOrder("},
		{"static string DesktopName(", "static string NativeDesktopName("},
		{"static void SwitchVirtualDesktop(", "static void NativeSwitchVirtualDesktop("},
	}
	for _, hook := range hooks {
		if strings.Count(source, hook[0]) != 1 {
			t.Fatalf("unsafe fixture hook mismatch: %s", hook[0])
		}
		source = strings.Replace(source, hook[0], hook[1], 1)
	}
	source += "\n" + desktopAccessFixture
	input, err := json.Marshal(map[string]string{"source": source})
	if err != nil {
		t.Fatal(err)
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
[AHADesktop.Native]::TestDesktopAccess()
`
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
		t.Fatalf("desktop fixture failed: %v\n%s", err, output)
	}
	var result struct {
		OK               bool     `json:"ok"`
		Cases            []string `json:"cases"`
		ForegroundSame   bool     `json:"foreground_unchanged"`
		NativeInputCalls int      `json:"native_input_calls"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal("invalid desktop fixture result", err)
	}
	if !result.OK || !result.ForegroundSame || result.NativeInputCalls != 0 || len(result.Cases) < 20 {
		t.Fatalf("fixture guards failed: %+v", result)
	}
	t.Logf("cases=%d; real owned window identity, simulated desktop/capture/protection; no physical input/switch/capture, actual foreground unchanged", len(result.Cases))
}
