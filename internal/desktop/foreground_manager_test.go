package desktop

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

type foregroundFixture struct {
	fakeProvider
	creates atomic.Int32
	inputs  atomic.Int32
	create  func(context.Context) (Window, error)
	input   func(context.Context, Window, Action, Observation) error
}

func (p *foregroundFixture) NewDesktop(ctx context.Context) (Window, error) {
	p.creates.Add(1)
	if p.create != nil {
		return p.create(ctx)
	}
	return Window{ID: "desktop:fixture", Kind: "desktop", Title: "Fixture desktop", Process: "fixture"}, nil
}

func (p *foregroundFixture) ObserveForeground(context.Context, Window) (Observation, error) {
	return Observation{Width: 800, Height: 600, Surface: "trusted:800:600",
		InputActions: []string{"click", "double_click", "drag", "scroll", "text", "key", "focus"}}, nil
}

func (p *foregroundFixture) ActForeground(ctx context.Context, target Window, action Action, capture Observation) error {
	p.inputs.Add(1)
	if p.input != nil {
		return p.input(ctx, target, action, capture)
	}
	return nil
}

func shareForeground(t *testing.T, m *Manager, task, target string) Session {
	t.Helper()
	state, err := m.OpenWithMode(context.Background(), task, target, "foreground", true)
	if err != nil {
		t.Fatal(err)
	}
	return *state.Session
}

func foregroundRequest(t *testing.T, m *Manager, session Session, actor string, action Action) ActionRequest {
	t.Helper()
	o, err := m.Observe(context.Background(), session.TaskID, session.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	action.ElementID = "$surface"
	return ActionRequest{SessionID: session.ID, Revision: o.Revision, ObservationID: o.ID, Action: action}
}

func TestForegroundRequiresExplicitConsentAndProvider(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	for _, target := range []string{"new-desktop", "w1"} {
		_, err := m.OpenWithMode(context.Background(), "t", target, "foreground", false)
		wantError(t, err, "foreground_confirmation_required")
	}
	_, err := m.Open(context.Background(), "t", "new-desktop")
	wantError(t, err, "foreground_confirmation_required")
	if p.creates.Load() != 0 {
		t.Fatal("unconfirmed request created a desktop")
	}
	_, err = New(&fakeProvider{}).OpenWithMode(context.Background(), "t", "w1", "foreground", true)
	wantError(t, err, "unsupported")
	s := shareForeground(t, m, "t", "new-desktop")
	if s.Mode != "foreground" || s.Window.Kind != "desktop" || p.creates.Load() != 1 || !m.Status("t").ForegroundSupported {
		t.Fatalf("new desktop state wrong: %+v", s)
	}
}

func TestForegroundGlobalExclusionAndExpiry(t *testing.T) {
	m := New(&foregroundFixture{})
	now := time.Now()
	m.now = func() time.Time { return now }
	s := shareForeground(t, m, "t1", "w1")
	_, err := m.OpenWithMode(context.Background(), "t2", "w2", "foreground", true)
	wantError(t, err, "foreground_in_use")
	if _, err := m.Control("t1", s.ID, s.Revision, "agent"); err != nil {
		t.Fatal(err)
	}
	_, err = m.OpenWithMode(context.Background(), "t2", "new-desktop", "foreground", true)
	wantError(t, err, "foreground_in_use")
	now = now.Add(sessionTTL)
	shareForeground(t, m, "t2", "w2")
}

func TestForegroundPendingCreationIsReserved(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &foregroundFixture{create: func(context.Context) (Window, error) {
		close(entered)
		<-release
		return Window{}, errors.New("creation failed")
	}}
	m := New(p)
	done := make(chan error, 1)
	go func() {
		_, err := m.OpenWithMode(context.Background(), "t1", "new-desktop", "foreground", true)
		done <- err
	}()
	<-entered
	_, err := m.OpenWithMode(context.Background(), "t2", "w2", "foreground", true)
	wantError(t, err, "foreground_in_use")
	_, err = m.Open(context.Background(), "t1", "w1")
	wantError(t, err, "busy")
	close(release)
	if <-done == nil {
		t.Fatal("failed creation returned success")
	}
	shareForeground(t, m, "t2", "w2")
}

func TestForegroundRevokeDuringCreateCannotPublishGrant(t *testing.T) {
	entered := make(chan struct{})
	p := &foregroundFixture{create: func(ctx context.Context) (Window, error) {
		close(entered)
		<-ctx.Done()
		// Even a provider reporting success after cancellation cannot restore permission.
		return Window{ID: "desktop:late", Kind: "desktop"}, nil
	}}
	m := New(p)
	done := make(chan error, 1)
	go func() {
		_, err := m.OpenWithMode(context.Background(), "t", "new-desktop", "foreground", true)
		done <- err
	}()
	<-entered
	m.Revoke("t")
	wantError(t, <-done, "cancelled")
	if m.Status("t").Session != nil {
		t.Fatal("canceled desktop creation published a grant")
	}
	shareForeground(t, m, "t", "w1")
}

func TestForegroundUsesTrustedSurfaceAndRejectsReplay(t *testing.T) {
	p := &foregroundFixture{input: func(_ context.Context, w Window, a Action, o Observation) error {
		if w.ID != "w1" || o.Surface != "trusted:800:600" || o.Width != 800 || o.Height != 600 || a.X != 50 {
			t.Fatalf("untrusted surface reached provider: %+v %+v %+v", w, a, o)
		}
		return nil
	}}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	request := foregroundRequest(t, m, s, "owner", Action{Kind: "click", X: 50, Y: 60, Button: "left"})
	if _, err := m.Act(context.Background(), "t", "owner", request); err != nil {
		t.Fatal(err)
	}
	_, err := m.Act(context.Background(), "t", "owner", request)
	wantError(t, err, "stale_observation")
	if p.inputs.Load() != 1 || p.calls.Load() != 0 {
		t.Fatal("foreground input replayed or dispatched as background")
	}
}

func TestForegroundInvalidInputsNeverReachProvider(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	for _, action := range []Action{
		{Kind: "click", X: -1}, {Kind: "click", X: 800}, {Kind: "click", Y: 600},
		{Kind: "click", X: math.NaN()}, {Kind: "scroll", DeltaY: math.Inf(1)},
		{Kind: "scroll", DeltaX: 2401}, {Kind: "drag", EndX: 800},
		{Kind: "key", Keys: []string{"unknown"}}, {Kind: "key", Keys: []string{"CTRL", "CTRL"}},
		{Kind: "key", Keys: []string{"CTRL", "ALT", "DELETE"}},
		{Kind: "key", Keys: []string{"WIN", "CTRL", "F4"}},
		{Kind: "key", Keys: []string{"WIN", "CTRL", "D"}},
		{Kind: "key", Keys: []string{"WIN", "CTRL", "LEFT"}},
		{Kind: "key", Keys: []string{"WIN", "CTRL", "RIGHT"}},
		{Kind: "key", Keys: []string{"WIN", "L"}}, {Kind: "text"},
		{Kind: "click", Button: "unknown"}, {Kind: "invoke"},
		{Kind: "click", Keys: []string{"CTRL"}}, {Kind: "focus", X: 1},
		{Kind: "key", Keys: []string{"A", "B"}}, {Kind: "scroll"},
		{Kind: "text", Value: "invalid\x01text"},
	} {
		r := foregroundRequest(t, m, s, "owner", action)
		if _, err := m.Act(context.Background(), "t", "owner", r); err == nil {
			t.Fatalf("invalid action accepted: %+v", action)
		}
	}
	if p.inputs.Load() != 0 || p.calls.Load() != 0 {
		t.Fatal("invalid input reached a provider")
	}
}

func TestForegroundTextValidationKeepsNewlinesAndTabs(t *testing.T) {
	for _, text := range []string{"first\nsecond", "first\r\nsecond", "first\tsecond", "中文 😀"} {
		if err := validateForegroundInput(Action{Kind: "text", Value: text}, 800, 600); err != nil {
			t.Fatalf("valid Unicode text rejected: %v", err)
		}
	}
	if ErrorCode(errors.New("desktop desktop_changed")) != "desktop_changed" {
		t.Fatal("desktop scope error was not preserved")
	}
}

func TestForegroundClaimAndTakeover(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	state, _ := m.Control("t", s.ID, s.Revision, "agent")
	s = *state.Session
	request := foregroundRequest(t, m, s, "agent:turn", Action{Kind: "key", Keys: []string{"CTRL", "L"}})
	_, err := m.Act(context.Background(), "t", "agent:turn", request)
	wantError(t, err, "claim_required")
	if _, err := m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", func(Window) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Act(context.Background(), "t", "agent:turn", request); err != nil {
		t.Fatal(err)
	}
	request = foregroundRequest(t, m, s, "agent:turn", Action{Kind: "text", Value: "https://github.com"})
	state, _ = m.Control("t", s.ID, s.Revision, "owner")
	_, err = m.Act(context.Background(), "t", "agent:turn", request)
	wantError(t, err, "stale_session")
	owner := foregroundRequest(t, m, *state.Session, "owner", Action{Kind: "focus"})
	if _, err = m.Act(context.Background(), "t", "owner", owner); err != nil {
		t.Fatal(err)
	}
	if p.inputs.Load() != 2 {
		t.Fatalf("incorrect input count: %d", p.inputs.Load())
	}
}

func TestForegroundStopKeepsGlobalReservationUntilInputReturns(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &foregroundFixture{input: func(ctx context.Context, _ Window, _ Action, _ Observation) error {
		close(entered)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}}
	m := New(p)
	s := shareForeground(t, m, "t1", "w1")
	r := foregroundRequest(t, m, s, "owner", Action{Kind: "text", Value: "fixture"})
	done := make(chan error, 1)
	go func() { _, err := m.Act(context.Background(), "t1", "owner", r); done <- err }()
	<-entered
	if _, err := m.Stop("t1", s.ID); err != nil {
		t.Fatal(err)
	}
	_, err := m.OpenWithMode(context.Background(), "t2", "w2", "foreground", true)
	wantError(t, err, "foreground_in_use")
	close(release)
	wantError(t, <-done, "not_shared")
	shareForeground(t, m, "t2", "w2")
}

func TestForegroundNeverFallsBackFromBackgroundGrant(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	s := share(t, m, "t", "w1")
	r := observe(t, m, s, "owner")
	r.ElementID, r.Kind, r.Keys = "$surface", "key", []string{"WIN", "R"}
	_, err := m.Act(context.Background(), "t", "owner", r)
	wantError(t, err, "unsupported_action")
	if p.inputs.Load() != 0 || p.calls.Load() != 0 {
		t.Fatal("background grant upgraded to foreground input")
	}
}
