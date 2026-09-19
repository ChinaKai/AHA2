package desktop

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestNativeOtherUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows behavior")
	}
	p := NativeProvider()
	if supported, reason := p.Support(); supported || reason == "" {
		t.Fatalf("Support = %v, %q", supported, reason)
	}
	if _, err := p.Windows(context.Background()); err == nil {
		t.Fatal("expected unsupported error")
	}
}

// The native helper must not reach for anything that injects input into the
// user's session. Control is granted per operation and dispatched explicitly.
func TestNativeSourceHasNoPhysicalInputFallback(t *testing.T) {
	for _, forbidden := range []string{
		"SendInput", "SendKeys", "SetForegroundWindow", "SetFocus", "Clipboard",
		"keybd_event", "mouse_event", "WM_KEY", "PostMessage", ".SetValue(value)",
		".Invoke()", ".Toggle()", ".Select()", ".Expand()", ".Collapse()",
		"CopyFromScreen", "GetDesktopWindow", "GetDC(", "Invoke-Expression",
	} {
		if strings.Contains(nativeSource, forbidden) || strings.Contains(nativeBootstrap, forbidden) {
			t.Errorf("native helper contains forbidden API %q", forbidden)
		}
	}
	for _, required := range []string{"IsPassword", "GetProcessTimes", "TokenSID", "process.SessionId", "PrintWindow", "focus.Check()"} {
		if !strings.Contains(nativeSource, required) {
			t.Errorf("missing native guard %q", required)
		}
	}
}

// Text writes must stay scoped to the selected target rather than acting as a
// generic UI-automation input proxy.
func TestNativeTextWritesAreScopedAndNotUIAInputProxies(t *testing.T) {
	for _, required := range []string{
		"NativeEdit(element)", "pid != target.PID", "GetAncestor(handle, 2) != target.Handle",
		"SendTextTimeout(handle, 0x000C", "element.Current.IsPassword", "element_read_only",
	} {
		if !strings.Contains(nativeSource, required) {
			t.Errorf("missing scoped text guard %q", required)
		}
	}
}

// The removed background mode must not linger as a reachable code path: with it
// gone, control is only ever granted through the foreground provider contract.
func TestBackgroundControlIsGone(t *testing.T) {
	for _, source := range []string{nativeSource, foregroundNativeSource} {
		for _, forbidden := range []string{"RunBackground", "ObserveBackgroundNative", "ActBackgroundNative", "background_observe", "background_act"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("background control source still present: %q", forbidden)
			}
		}
	}
}
