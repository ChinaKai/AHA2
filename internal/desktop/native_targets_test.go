package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNativeTargetsMetadataAndSelection(t *testing.T) {
	p := &nativeProvider{run: func(_ context.Context, data []byte) ([]byte, error) {
		var packet struct {
			Request nativeRequest `json:"request"`
		}
		if err := json.Unmarshal(data, &packet); err != nil {
			t.Fatal(err)
		}
		switch packet.Request.Operation {
		case "targets":
			return []byte(`{"ok":true,"targets":{"monitors":[{"id":"monitor","x":-1920,"y":-200,"width":1920,"height":1080,"primary":true}],"desktops":[{"id":"guid","current":true}],"desktop_switch_supported":true}}`), nil
		case "target_select":
			if packet.Request.Selection == nil || packet.Request.Selection.MonitorID != "monitor" {
				t.Fatal("selection scope not forwarded")
			}
			return []byte(`{"ok":true,"window":{"id":"desktop:guid@monitor","kind":"desktop","desktop_id":"guid","monitor_id":"monitor"}}`), nil
		default:
			t.Fatal("unexpected native operation")
			return nil, nil
		}
	}}
	targets, err := p.Targets(context.Background())
	if err != nil || len(targets.Monitors) != 1 || targets.Monitors[0].X != -1920 || targets.Windows == nil {
		t.Fatalf("targets not preserved: %+v %v", targets, err)
	}
	window, err := p.SelectTarget(context.Background(), TargetSelection{Kind: "desktop", DesktopID: "guid", MonitorID: "monitor"})
	if err != nil || window.MonitorID != "monitor" || window.DesktopID != "guid" {
		t.Fatalf("selection: %+v %v", window, err)
	}
}

func TestTargetErrorsRemainSpecificAndSanitized(t *testing.T) {
	for _, code := range []string{"monitor_changed", "monitor_required", "monitor_unavailable", "foreground_outside_monitor",
		"desktop_enumeration_unavailable", "desktop_state_inconsistent", "desktop_switch_unverified", "desktop_switch_limit"} {
		expected := code
		if !strings.HasPrefix(code, "desktop_") {
			expected = "desktop_" + code
		}
		if got := ErrorCode(errors.New("desktop " + code)); got != expected {
			t.Fatalf("%s mapped to %s instead of %s", code, got, expected)
		}
	}
	if got := ErrorCode(errors.New("private window contents")); got != "desktop_native_failed" {
		t.Fatal("native exception text leaked")
	}
}

func TestNativeTargetsRejectMalformedSelection(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		t.Fatal("invalid selection reached helper")
		return nil, nil
	}}
	for _, selection := range []TargetSelection{
		{}, {Kind: "window"}, {Kind: "window", WindowID: "fixture", MonitorID: "monitor"},
		{Kind: "desktop"}, {Kind: "desktop", DesktopID: "guid", WindowID: "fixture"},
		{Kind: "new-desktop", DesktopID: "guid"}, {Kind: "new-desktop", MonitorID: strings.Repeat("x", 129)},
	} {
		if _, err := p.SelectTarget(context.Background(), selection); err == nil {
			t.Fatalf("accepted %+v", selection)
		}
	}
}

func TestNativeTargetsFailClosedBoundaries(t *testing.T) {
	for _, required := range []string{
		"EnumDisplayMonitors", "EnumDisplayDevices", "MonitorIdentity", "OpenSubKey(DesktopRegistryPath, false)",
		"VerifiedDesktopOrder", "!SameOrder(initial, order)", "after != expected", "desktop_switch_unverified",
		"MonitorFromWindow(surface.Target.Handle, 0)", "foreground_outside_monitor",
	} {
		if !strings.Contains(nativeTargetsSource, required) {
			t.Errorf("missing %s", required)
		}
	}
	for _, forbidden := range []string{"SetValue(", "CreateSubKey(", "IVirtualDesktopManagerInternal", "IServiceProvider", "SwitchDesktop("} {
		if strings.Contains(nativeTargetsSource, forbidden) {
			t.Errorf("unexpected mutation/undocumented COM: %s", forbidden)
		}
	}
	if !strings.Contains(foregroundBaseSource, "surface.Bounds = surface.Monitor.Bounds;") ||
		!strings.Contains(foregroundBaseSource, "ResolveMonitor(scope[1], false)") {
		t.Fatal("desktop capture not pinned to monitor")
	}
}

func TestBothWindowPickersUseVisualWindowEligibility(t *testing.T) {
	if !strings.Contains(nativeSource, "windows.Add(InspectPickerWindow(hwnd).Window)") ||
		!strings.Contains(nativeTargetsSource, "var window = InspectTargetWindow(hwnd, active, known).Window;") ||
		!strings.Contains(nativeTargetsSource, "Target target = InspectPickerWindow(hwnd, other ? 2 : 0);") {
		t.Fatal("both legacy Windows and structured Targets must use the same picker filter")
	}
	start := strings.Index(nativeSource, "static Target InspectPickerWindow")
	end := strings.Index(nativeSource, "static Target Resolve")
	if start < 0 || end <= start {
		t.Fatal("picker filter missing")
	}
	filter := nativeSource[start:end]
	for _, required := range []string{"InspectVisualWindow(hwnd, allowedCloaking)", "GetShellWindow()", "ToolWindow | NoActivate",
		"AppWindow", "GetClientRect", "IsIconic", "GetWindowPlacement", "NormalPosition",
		"bounds.Right <= bounds.Left", "bounds.Bottom <= bounds.Top"} {
		if !strings.Contains(filter, required) {
			t.Errorf("picker eligibility missing %s", required)
		}
	}
	for _, forbidden := range []string{"ProcessName", "GetWindowText", "SetForegroundWindow", "ShowWindow", "PrintWindow"} {
		if strings.Contains(filter, forbidden) {
			t.Errorf("picker must not classify by process/title or activate/capture windows: %s", forbidden)
		}
	}
	if !strings.Contains(nativeSource[end:], "Target target = Inspect(WindowHandle(id));") {
		t.Fatal("picker-only filtering must not replace existing grant identity validation")
	}
}
