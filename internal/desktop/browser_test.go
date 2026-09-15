package desktop

import (
	"context"
	"strings"
	"testing"
)

func TestBrowserAdapterGuardsAndScopedActions(t *testing.T) {
	for _, guard := range []string{
		`"msedge"`, `"Chrome_WidgetWin_"`, "GetAncestor(hwnd, 2) != target.Handle",
		"InspectWindowProcessIdentity(hwnd, false)", "BrowserProtected",
		"!state.HostEnabled", "BrowserDisabled | BrowserReadOnly | BrowserInvisible",
		"if (state.Protected ||", "browserReceipts.Remove(id)",
		"receipt.Target != target.ID", `Text(target.Window, "desktop_id")`,
		"receipt.Expires <= DateTime.UtcNow", "CheckBackgroundEpoch(receipt.Epoch)",
		"SameBrowserState(receipt.State, state)", `Fail("browser_page_unavailable")`,
		".accDoDefaultAction(", ".set_accValue(", "target.Revalidate();",
	} {
		if !strings.Contains(nativeBrowserSource, guard) {
			t.Errorf("missing browser scope/state guard: %s", guard)
		}
	}
	for _, forbidden := range []string{"SendInput", "SetForegroundWindow", "SetFocus", "accSelect(", "SendKeys",
		"SwitchVirtualDesktop", "Process.Start", "Clipboard", "InvokePattern", "ValuePattern"} {
		if strings.Contains(nativeBrowserSource, forbidden) {
			t.Errorf("browser adapter has unsafe fallback: %s", forbidden)
		}
	}
	capture := strings.Index(nativeBackgroundSource, "string image = Capture")
	validate := strings.LastIndex(nativeBackgroundSource, "ValidateBackgroundBrowser(target, browser)")
	if capture < 0 || validate <= capture {
		t.Fatal("browser metadata/password geometry must be revalidated after capture")
	}
}

func TestBrowserErrorsAreSafeAndNotRootClosed(t *testing.T) {
	for _, code := range []string{"browser_accessibility_unavailable", "browser_scope_changed", "browser_page_unavailable"} {
		p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
			return []byte(`{"ok":false,"error":"` + code + `"}`), nil
		}}
		_, err := p.Observe(context.Background(), Window{ID: "owned", DesktopID: "desk"})
		wantError(t, err, code)
	}
}
