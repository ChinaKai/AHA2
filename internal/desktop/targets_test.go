package desktop

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type selectionFixture struct {
	foregroundFixture
	selectTarget func(context.Context, TargetSelection) (Window, error)
	capture      func(context.Context, Window) (Observation, error)
	selections   atomic.Int32
}

func (p *selectionFixture) Targets(context.Context) (Targets, error) {
	return Targets{Windows: []Window{{ID: "w1"}, {ID: "w2"}},
		Desktops:               []VirtualDesktop{{ID: "desk-1", Name: "Desktop 1", Current: true}, {ID: "desk-2", Name: "Desktop 2"}},
		Monitors:               []Monitor{{ID: "primary", Width: 1920, Height: 1080, Primary: true}, {ID: "left", X: -1280, Width: 1280, Height: 1024}},
		DesktopSwitchSupported: true}, nil
}

func (p *selectionFixture) SelectTarget(ctx context.Context, target TargetSelection) (Window, error) {
	p.selections.Add(1)
	if p.selectTarget != nil {
		return p.selectTarget(ctx, target)
	}
	if target.Kind == "window" {
		return Window{ID: target.WindowID, Kind: "window"}, nil
	}
	desktopID, monitorID := target.DesktopID, target.MonitorID
	if target.Kind == "new-desktop" {
		desktopID = "desk-new"
	}
	if monitorID == "" {
		monitorID = "primary"
	}
	return Window{ID: "opaque:" + desktopID + ":" + monitorID, Kind: "desktop", DesktopID: desktopID, MonitorID: monitorID}, nil
}

func (p *selectionFixture) ObserveForeground(ctx context.Context, w Window) (Observation, error) {
	if p.capture != nil {
		return p.capture(ctx, w)
	}
	return p.foregroundFixture.ObserveForeground(ctx, w)
}

func TestTargetCatalogAndSelection(t *testing.T) {
	p := &selectionFixture{}
	m := New(p)
	ctx := context.Background()
	catalog, err := m.Targets(ctx)
	if err != nil || len(catalog.Desktops) != 2 || len(catalog.Monitors) != 2 || catalog.Monitors[1].X != -1280 {
		t.Fatal("target metadata lost", catalog, err)
	}
	if !m.Status("t").TargetsSupported {
		t.Fatal("target provider not advertised")
	}
	target := TargetSelection{Kind: "desktop", DesktopID: "desk-2", MonitorID: "left"}
	status, err := m.OpenTarget(ctx, "t", target, "foreground", true)
	if err != nil || status.Session.Window.DesktopID != "desk-2" || status.Session.Window.MonitorID != "left" {
		t.Fatal("selected monitor/desktop lost", err)
	}
	fallback, err := New(&fakeProvider{}).Targets(ctx)
	if err != nil || len(fallback.Windows) != 2 || fallback.DesktopSwitchSupported || fallback.Desktops == nil || fallback.Monitors == nil {
		t.Fatal("window-only compatibility broken", err)
	}
}

func TestTargetSwitchReplacesSessionWithoutAgentInheritance(t *testing.T) {
	p := &selectionFixture{}
	m := New(p)
	ctx := context.Background()
	old := shareForeground(t, m, "t", "w1")
	state, err := m.Control("t", old.ID, old.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	old = *state.Session
	if _, err := m.Claim(ctx, "t", old.ID, old.Revision, "turn", func(Window) error { return nil }); err != nil {
		t.Fatal(err)
	}
	action := foregroundRequest(t, m, old, "agent:turn", Action{Kind: "key", Keys: []string{"A"}})
	next, err := m.SwitchTarget(ctx, "t", old.ID, old.Revision, TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", true)
	if err != nil {
		t.Fatal(err)
	}
	if next.Session.ID == old.ID || next.Session.Revision != 1 || next.Session.Controller != "owner" ||
		next.Session.Claimed || next.Session.Switching || next.Session.Window.ID != "w2" {
		t.Fatal("replacement inherited old grant", next)
	}
	_, err = m.Act(ctx, "t", "agent:turn", action)
	wantError(t, err, "stale_session")
	_, err = m.Claim(ctx, "t", next.Session.ID, 1, "turn", func(Window) error { t.Fatal("ungranted claim published"); return nil })
	wantError(t, err, "owner_control")
	if p.inputs.Load() != 0 {
		t.Fatal("old input reached replacement target")
	}
}

func TestTargetSwitchInvalidRequestsDoNotRevoke(t *testing.T) {
	p := &selectionFixture{}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	for _, test := range []struct {
		target    TargetSelection
		mode      string
		confirmed bool
	}{
		{TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", false},
		{TargetSelection{Kind: "desktop", DesktopID: "desk-2"}, "background", true},
		{TargetSelection{Kind: "window", WindowID: "w2", MonitorID: "left"}, "background", false},
		{TargetSelection{Kind: "new-desktop", DesktopID: "desk-2"}, "foreground", true},
		{TargetSelection{Kind: "desktop"}, "foreground", true},
		{TargetSelection{Kind: "window", WindowID: "w2"}, "bad-mode", true},
		{TargetSelection{Kind: "window", WindowID: strings.Repeat("x", 129)}, "foreground", true},
		{TargetSelection{Kind: "desktop", DesktopID: strings.Repeat("x", 65)}, "foreground", true},
		{TargetSelection{Kind: "desktop", DesktopID: "desk-1", MonitorID: strings.Repeat("x", 129)}, "foreground", true},
	} {
		_, err := m.SwitchTarget(context.Background(), "t", s.ID, s.Revision, test.target, test.mode, test.confirmed)
		if err == nil {
			t.Fatal("invalid switch accepted")
		}
		now := m.Status("t").Session
		if now == nil || now.ID != s.ID || now.Revision != s.Revision || now.Switching {
			t.Fatal("invalid request revoked grant")
		}
	}
	if p.selections.Load() != 0 {
		t.Fatal("invalid selection reached native provider")
	}
}

func TestTargetSwitchFailureDoesNotRestoreOldGrant(t *testing.T) {
	for _, fail := range []func(context.Context, TargetSelection) (Window, error){
		func(context.Context, TargetSelection) (Window, error) {
			return Window{}, errors.New("native private detail")
		},
		func(context.Context, TargetSelection) (Window, error) {
			return Window{ID: "other", Kind: "desktop", DesktopID: "wrong", MonitorID: "primary"}, nil
		},
		func(context.Context, TargetSelection) (Window, error) {
			return Window{ID: "ambiguous", Kind: "desktop", DesktopID: "desk-2"}, nil
		},
	} {
		p := &selectionFixture{selectTarget: fail}
		m := New(p)
		s := shareForeground(t, m, "t", "w1")
		_, err := m.SwitchTarget(context.Background(), "t", s.ID, 1, TargetSelection{Kind: "desktop", DesktopID: "desk-2"}, "foreground", true)
		if err == nil || m.Status("t").Session != nil {
			t.Fatal("failed selection restored/retained authorization")
		}
	}
}

func TestTargetSwitchWaitsForCanceledInputAndStopCancelsTransition(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p := &selectionFixture{}
	p.input = func(ctx context.Context, _ Window, _ Action, _ Observation) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	action := foregroundRequest(t, m, s, "owner", Action{Kind: "key", Keys: []string{"A"}})
	actionDone := make(chan error, 1)
	go func() { _, err := m.Act(context.Background(), "t", "owner", action); actionDone <- err }()
	<-started
	switchDone := make(chan error, 1)
	go func() {
		_, err := m.SwitchTarget(context.Background(), "t", s.ID, 1, TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", true)
		switchDone <- err
	}()
	<-canceled
	state := m.Status("t").Session
	if state == nil || !state.Switching || state.Revision != 2 || p.selections.Load() != 0 {
		t.Fatal("switch selected before input cleanup")
	}
	_, err := m.Observe(context.Background(), "t", s.ID, "owner")
	wantError(t, err, "switching")
	_, err = m.Control("t", s.ID, state.Revision, "agent")
	wantError(t, err, "switching")
	action.Revision = state.Revision
	_, err = m.Act(context.Background(), "t", "owner", action)
	wantError(t, err, "switching")
	if _, err := m.Stop("t", s.ID); err != nil {
		t.Fatal(err)
	}
	_, err = m.OpenWithMode(context.Background(), "t", "w2", "foreground", true)
	wantError(t, err, "busy")
	close(release)
	if err := <-actionDone; err == nil {
		t.Fatal("canceled old input succeeded")
	}
	if err := <-switchDone; err == nil {
		t.Fatal("stopped switch published")
	}
	if p.selections.Load() != 0 || m.Status("t").Session != nil {
		t.Fatal("stop did not prevent selection")
	}
	shareForeground(t, m, "t", "w2")
}

func TestTargetSwitchWaitsForCaptureBeforeSelection(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p := &selectionFixture{capture: func(ctx context.Context, _ Window) (Observation, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return Observation{}, ctx.Err()
	}}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	captureDone := make(chan error, 1)
	go func() { _, err := m.Observe(context.Background(), "t", s.ID, "owner"); captureDone <- err }()
	<-started
	switchDone := make(chan error, 1)
	go func() {
		_, err := m.SwitchTarget(context.Background(), "t", s.ID, 1, TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", true)
		switchDone <- err
	}()
	<-canceled
	if p.selections.Load() != 0 {
		t.Fatal("selection overlapped capture")
	}
	close(release)
	if err := <-captureDone; err == nil {
		t.Fatal("old capture published")
	}
	if err := <-switchDone; err != nil {
		t.Fatal(err)
	}
	if m.Status("t").Session.Window.ID != "w2" {
		t.Fatal("switch did not finish")
	}
}

func TestStopDuringNativeSelectionCannotPublishOrOverlapReopen(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &selectionFixture{selectTarget: func(ctx context.Context, t TargetSelection) (Window, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return Window{ID: t.WindowID}, nil
	}}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	done := make(chan error, 1)
	go func() {
		_, err := m.SwitchTarget(context.Background(), "t", s.ID, 1, TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", true)
		done <- err
	}()
	<-entered
	if _, err := m.Stop("t", s.ID); err != nil {
		t.Fatal(err)
	}
	_, err := m.Open(context.Background(), "t", "w1")
	wantError(t, err, "busy")
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("late selection revived grant")
		}
	case <-time.After(time.Second):
		t.Fatal("switch cancellation stuck")
	}
	if m.Status("t").Session != nil {
		t.Fatal("stopped transition revived session")
	}
}

func TestBackgroundWindowCanSwitchWithoutForegroundUpgrade(t *testing.T) {
	p := &selectionFixture{}
	m := New(p)
	s := share(t, m, "t", "w1")
	next, err := m.SwitchTarget(context.Background(), "t", s.ID, 1, TargetSelection{Kind: "window", WindowID: "w2"}, "background", false)
	if err != nil || next.Session.Mode != "background" || next.Session.Window.ID != "w2" || p.selections.Load() != 0 {
		t.Fatal("background switch called physical foreground selector", err)
	}
}
