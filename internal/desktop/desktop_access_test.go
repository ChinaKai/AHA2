package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type crossDesktopSelectionFixture struct{ selectionFixture }

func (*crossDesktopSelectionFixture) Windows(context.Context) ([]Window, error) {
	return []Window{{ID: "w1", Kind: "window"}}, nil
}

func TestOtherDesktopWindowSelectionRequiresConfirmedForeground(t *testing.T) {
	p := &crossDesktopSelectionFixture{}
	p.selectTarget = func(_ context.Context, selection TargetSelection) (Window, error) {
		return Window{ID: selection.WindowID, Kind: "window", DesktopID: "other", DesktopName: "Review"}, nil
	}
	m := New(p)
	target := TargetSelection{Kind: "window", WindowID: "other-window"}
	_, err := m.OpenTarget(context.Background(), "t", target, "background", false)
	wantError(t, err, "window_gone")
	_, err = m.OpenTarget(context.Background(), "t", target, "foreground", false)
	wantError(t, err, "foreground_confirmation_required")
	if p.selections.Load() != 0 {
		t.Fatal("unconfirmed/background request invoked cross-desktop selector")
	}
	state, err := m.OpenTarget(context.Background(), "t", target, "foreground", true)
	if err != nil || p.selections.Load() != 1 || state.Session.Controller != "owner" ||
		state.Session.Window.DesktopID != "other" {
		t.Fatal("confirmed selector did not create an Owner-controlled window grant", err)
	}
}

func TestNativeCrossDesktopWindowMetadataRoundTrip(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		return []byte(`{"ok":true,"targets":{"windows":[{"id":"other-window","desktop_id":"other","desktop_name":"Review","other_desktop":true}]}}`), nil
	}}
	targets, err := p.Targets(context.Background())
	if err != nil || len(targets.Windows) != 1 {
		t.Fatal("missing window metadata", err)
	}
	window := targets.Windows[0]
	if !window.OtherDesktop || window.DesktopID != "other" || window.DesktopName != "Review" {
		t.Fatal("lost cross-desktop metadata")
	}
	body, err := json.Marshal(targets)
	if err != nil || !strings.Contains(string(body), `"other_desktop":true`) || !strings.Contains(string(body), `"desktop_name":"Review"`) {
		t.Fatal("HTTP metadata fields missing", err)
	}
}

func TestDesktopCaptureRetainsTrustAndSeparatesVisualInputEligibility(t *testing.T) {
	for _, guard := range []string{"InspectIdentity(hwnd)", "captureOnly ? InspectIdentity(hwnd) : Inspect(hwnd)",
		"surface.ControlError = \"desktop_foreground_unavailable\"", "ReadSurface(id, false)",
		"before.DesktopID == after.DesktopID", "before.Monitor.ID == after.Monitor.ID",
		"before.ControlError = \"desktop_foreground_changed\"", "if (surface.ControlError != \"\") return new string[0]"} {
		if !strings.Contains(nativeSource+foregroundNativeSource, guard) {
			t.Errorf("missing desktop trust/input boundary %s", guard)
		}
	}
	for _, guard := range []string{"process.SessionId != self.SessionId", "owner != identity.User.Value",
		"targetIntegrity > ownIntegrity", "target_token_unavailable", "GetProcessTimes(handle"} {
		if !strings.Contains(nativeSource, guard) {
			t.Errorf("lost identity guard %s", guard)
		}
	}
}

func TestDesktopIdentityErrorsRemainSpecific(t *testing.T) {
	for _, code := range []string{"target_state_unavailable", "target_process_unavailable", "target_token_unavailable",
		"target_foreign_session", "target_foreign_user", "foreground_unavailable", "elevated_target"} {
		if got := ErrorCode(errors.New("desktop " + code)); got != "desktop_"+code {
			t.Errorf("%s became %s", code, got)
		}
	}
}

func TestReadOnlyDesktopFrameCannotAuthorizeInput(t *testing.T) {
	p := &selectionFixture{capture: func(context.Context, Window) (Observation, error) {
		return Observation{Image: "fixture-image", Width: 800, Height: 600, Surface: "fixture-surface",
			ControlError: "desktop_foreground_unavailable", InputActions: []string{}}, nil
	}}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	o, err := m.Observe(context.Background(), "t", s.ID, "owner")
	if err != nil || o.Image == "" || o.ControlError == "" {
		t.Fatal("read-only frame was lost", err)
	}
	_, err = m.Act(context.Background(), "t", "owner", ActionRequest{SessionID: s.ID, Revision: s.Revision,
		ObservationID: o.ID, Action: Action{ElementID: "$surface", Kind: "key", Keys: []string{"ENTER"}}})
	wantError(t, err, "unsupported_action")
	if p.inputs.Load() != 0 || m.Status("t").Session == nil {
		t.Fatal("read-only frame dispatched input or revoked grant")
	}
}

func TestCrossDesktopEnumerationNeverSwitchesOrRelaxesAppCloaking(t *testing.T) {
	start := strings.Index(nativeTargetsSource, "static object EnumerateTargets()")
	end := strings.Index(nativeTargetsSource, "static void SwitchVirtualDesktop")
	if start < 0 || end <= start || strings.Contains(nativeTargetsSource[start:end], "SwitchVirtualDesktop(") {
		t.Fatal("enumeration may switch desktop")
	}
	for _, guard := range []string{"manager.IsWindowOnCurrentVirtualDesktop", "manager.GetWindowDesktopId",
		"!known.Contains(desktop)", "InspectPickerWindow(hwnd, other ? 2 : 0)",
		"stillCurrent != current", "candidate.ID != window", "CurrentVirtualDesktop() != active",
		"after != belonging", "selected.ID != window"} {
		if !strings.Contains(nativeTargetsSource, guard) {
			t.Errorf("missing cross-desktop guard %s", guard)
		}
	}
}
