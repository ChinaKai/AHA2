package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type backgroundFixture struct {
	fakeProvider
	selectWindow func(context.Context, string) (Window, error)
}

func (p *backgroundFixture) SelectBackgroundWindow(ctx context.Context, id string) (Window, error) {
	if p.selectWindow != nil {
		return p.selectWindow(ctx, id)
	}
	return Window{ID: id, Kind: "window", DesktopID: "bound-desktop", OtherDesktop: true}, nil
}

func TestBackgroundSelectionPinsServerDesktopAcrossSharedAccessAndSwitch(t *testing.T) {
	p := &backgroundFixture{}
	m := New(p)
	defer m.Close()
	ctx := context.Background()
	if !m.Status("t").BackgroundDesktopSupported || New(&fakeProvider{}).Status("t").BackgroundDesktopSupported {
		t.Fatal("background capability does not reflect provider interface")
	}
	status, err := m.OpenSharedTarget(ctx, "t", TargetSelection{Kind: "window", WindowID: "other-desktop-window"}, "background", false)
	if err != nil {
		t.Fatal(err)
	}
	s := *status.Session
	if s.Controller != "shared" || s.Window.DesktopID != "bound-desktop" || s.Mode != "background" {
		t.Fatal("background shared binding lost")
	}
	verify := func(w Window) {
		t.Helper()
		if w.ID != s.Window.ID || w.DesktopID != "bound-desktop" {
			t.Fatal("provider did not receive server-held binding")
		}
	}
	p.observe = func(_ context.Context, w Window) (Observation, error) {
		verify(w)
		return Observation{Elements: []Element{{ID: "e1", Actions: []string{"invoke"}}}}, nil
	}
	p.act = func(_ context.Context, w Window, _ Action) error { verify(w); return nil }
	action := observe(t, m, s, "owner")
	if _, err := m.Act(ctx, "t", "owner", action); err != nil {
		t.Fatal(err)
	}
	next, err := m.SwitchSharedTarget(ctx, "t", s.ID, s.Revision, TargetSelection{Kind: "window", WindowID: "second-window"}, "background", false)
	if err != nil || next.Session.ID == s.ID || next.Session.Window.DesktopID != "bound-desktop" {
		t.Fatalf("background switch lost binding or old session identity: %v", err)
	}
}

func TestBackgroundSelectionRejectsDesktopOverridesAndMalformedProviderResults(t *testing.T) {
	p := &backgroundFixture{selectWindow: func(context.Context, string) (Window, error) {
		t.Fatal("invalid client selection reached provider")
		return Window{}, nil
	}}
	for _, selection := range []TargetSelection{
		{Kind: "window", WindowID: "w", DesktopID: "injected"},
		{Kind: "desktop", DesktopID: "other"},
		{Kind: "new-desktop"},
	} {
		if _, err := New(p).OpenSharedTarget(context.Background(), "t", selection, "background", false); err == nil {
			t.Fatal("background desktop override accepted")
		}
	}
	for _, w := range []Window{
		{ID: "w"}, {ID: "other", DesktopID: "desktop"},
		{ID: "w", Kind: "desktop", DesktopID: "desktop"},
		{ID: "w", DesktopID: strings.Repeat("a", 65)},
	} {
		p.selectWindow = func(context.Context, string) (Window, error) { return w, nil }
		m := New(p)
		_, err := m.OpenSharedTarget(context.Background(), "t", TargetSelection{Kind: "window", WindowID: "w"}, "background", false)
		wantError(t, err, "target_mismatch")
		if m.Status("t").Session != nil {
			t.Fatal("failed background selection published grant")
		}
		m.Close()
	}
}

func TestBackgroundObservationContextErrorsRevokeOldSnapshots(t *testing.T) {
	for _, code := range []string{"background_desktop_changed", "background_context_changed",
		"background_control_changed", "background_child_unverifiable", "browser_accessibility_unavailable",
		"browser_scope_changed", "browser_page_unavailable"} {
		t.Run(code, func(t *testing.T) {
			p := &backgroundFixture{}
			m := New(p)
			defer m.Close()
			s := share(t, m, "t", "w")
			action := observe(t, m, s, "owner")
			p.observe = func(context.Context, Window) (Observation, error) {
				return Observation{}, errors.New("desktop " + code)
			}
			_, err := m.Observe(context.Background(), "t", s.ID, "owner")
			wantError(t, err, code)
			_, err = m.Act(context.Background(), "t", "owner", action)
			wantError(t, err, "stale_session")
			if p.calls.Load() != 0 {
				t.Fatal("stale observation triggered a background operation")
			}
		})
	}
}

func TestBackgroundChildTrustAndReadOnlyGuards(t *testing.T) {
	for _, guard := range []string{"InspectWindowProcessIdentity(hwnd, false)", "child.PID != pid",
		"ProcessIdentity == after.ProcessIdentity", "!control.ForeignProcess && !control.Password",
		"if (selected.ForeignProcess) Fail(\"unsupported_action\")",
		"background_child_unverifiable", "background_control_changed"} {
		if !strings.Contains(nativeBackgroundSource, guard) {
			t.Errorf("missing multiprocess child guard: %s", guard)
		}
	}
	for _, code := range []string{"background_control_changed", "background_child_unverifiable"} {
		p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
			return []byte(`{"ok":false,"error":"` + code + `"}`), nil
		}}
		_, err := p.Observe(context.Background(), Window{ID: "w", DesktopID: "desk"})
		wantError(t, err, code)
	}
}

func TestBackgroundNativeRequestsBindDesktopAndUseSeparateOperations(t *testing.T) {
	var requests []nativeRequest
	p := &nativeProvider{run: func(_ context.Context, raw []byte) ([]byte, error) {
		var packet struct{ Request nativeRequest }
		if err := json.Unmarshal(raw, &packet); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, packet.Request)
		return []byte(`{"ok":true,"window":{"id":"w","kind":"window","desktop_id":"desk"},"observation":{"window":{"id":"w","desktop_id":"desk"}}}`), nil
	}}
	window, err := p.SelectBackgroundWindow(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Observe(context.Background(), window); err != nil {
		t.Fatal(err)
	}
	if err := p.Act(context.Background(), window, Action{Kind: "invoke", ElementID: "e"}); err != nil {
		t.Fatal(err)
	}
	for i, operation := range []string{"background_select", "background_observe", "background_act"} {
		if requests[i].Operation != operation || i > 0 && requests[i].DesktopID != "desk" ||
			i == 0 && requests[i].DesktopID != "" {
			t.Fatal("native background operation or trusted binding lost")
		}
	}
	window.DesktopID = "wrong"
	if _, err := p.Observe(context.Background(), window); err == nil {
		t.Fatal("native response from wrong desktop accepted")
	}
}

func TestBackgroundNativeSourceNeverSwitchesOrUsesUIAChildProxies(t *testing.T) {
	for _, forbidden := range []string{"SendInput", "SetForegroundWindow", "SetFocus(", "KeyChord(", "SwitchVirtualDesktop(",
		"CreateDesktop(", "FromHandle(", "Observe(target)", "Act(target,", "CopyFromScreen", "Clipboard", "BM_CLICK"} {
		if strings.Contains(nativeBackgroundSource, forbidden) {
			t.Errorf("unsafe background path: %s", forbidden)
		}
	}
	for _, guard := range []string{"TryWindowDesktop", "VerifiedDesktopOrder", "background_context_changed",
		"background_desktop_changed", "focus.Check()", "target.Revalidate()", "Password = edit &&",
		"GetAncestor(hwnd, 2) != target.Handle", "current ? 0 : 2", "target.ID != id",
		"if (selected.Password)", "if (!selected.Enabled)", "if (selected.ReadOnly)"} {
		if !strings.Contains(nativeBackgroundSource, guard) {
			t.Errorf("missing background guard: %s", guard)
		}
	}
	for _, guard := range []string{"Guid.NewGuid()", "backgroundReceipts.Remove(id)", "receipt.Target != target.ID",
		"CheckBackgroundEpoch(receipt.Epoch)", "Expires <= DateTime.UtcNow", "SetWinEventHook", "watched.TryGetValue(hwnd"} {
		if !strings.Contains(nativeBackgroundLifetimeSource, guard) {
			t.Errorf("missing native receipt/lifecycle guard: %s", guard)
		}
	}
}
