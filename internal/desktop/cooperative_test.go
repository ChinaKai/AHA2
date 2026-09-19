package desktop

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func shareCooperative(t *testing.T, m *Manager) Session {
	t.Helper()
	status, err := m.OpenSharedTarget(context.Background(), "t", TargetSelection{Kind: "window", WindowID: "w1"}, "foreground", true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.SharedControlSupported || status.Session.Controller != "shared" || status.Session.Claimed {
		t.Fatal("joint grant was not explicit and unclaimed")
	}
	return *status.Session
}

func TestSharedOwnerAndAgentUseSameGrantWithoutTransfer(t *testing.T) {
	{
		t.Run("foreground", func(t *testing.T) {
			p := &foregroundFixture{}
			m := New(p)
			s := shareCooperative(t, m)
			var notices int
			claim := func(Window) error { notices++; return nil }
			if _, err := m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", claim); err != nil {
				t.Fatal(err)
			}
			observe := func(actor string) ActionRequest {
				o, err := m.Observe(context.Background(), "t", s.ID, actor)
				if err != nil {
					t.Fatal(err)
				}
				return ActionRequest{SessionID: s.ID, Revision: s.Revision, ObservationID: o.ID,
					Action: Action{Kind: "key", ElementID: "$surface", Keys: []string{"ENTER"}}}
			}
			owner := observe("owner")
			agent := observe("agent:turn")
			if _, err := m.Act(context.Background(), "t", "agent:turn", agent); err != nil {
				t.Fatal("Agent cannot act while Owner has a view", err)
			}
			_, err := m.Act(context.Background(), "t", "owner", owner)
			wantError(t, err, "stale_observation")
			owner = observe("owner")
			agent = observe("agent:turn")
			if _, err := m.Act(context.Background(), "t", "owner", owner); err != nil {
				t.Fatal("Owner cannot assist after Agent claim", err)
			}
			_, err = m.Act(context.Background(), "t", "agent:turn", agent)
			wantError(t, err, "stale_observation")
			if _, err := m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", claim); err != nil {
				t.Fatal(err)
			}
			now := m.Status("t").Session
			if notices != 1 || now.ID != s.ID || now.Revision != s.Revision || now.Controller != "shared" || !now.Claimed {
				t.Fatal("assistance changed grant, role, revision or repeated notice")
			}
			for _, actor := range []string{"owner", "agent:turn"} {
				if _, err := m.Observe(context.Background(), "t", s.ID, actor); err != nil {
					t.Fatal("participant cannot continue viewing", err)
				}
			}
		})
	}
}

func TestSharedGrantDoesNotBroadenLegacyOrAcceptTransfer(t *testing.T) {
	m := New(&foregroundFixture{})
	legacy := shareForeground(t, m, "t", "w1")
	_, err := m.Observe(context.Background(), "t", legacy.ID, "agent:turn")
	wantError(t, err, "owner_control")
	_, err = m.Claim(context.Background(), "t", legacy.ID, legacy.Revision, "turn", func(Window) error { return nil })
	wantError(t, err, "owner_control")
	m.Stop("t", legacy.ID)
	s := shareCooperative(t, m)
	for _, role := range []string{"owner", "agent", "shared"} {
		_, err := m.Control("t", s.ID, s.Revision, role)
		if role == "shared" {
			wantError(t, err, "invalid_control")
		} else {
			wantError(t, err, "shared_control")
		}
	}
	now := m.Status("t").Session
	if now.Controller != "shared" || now.Revision != s.Revision || now.ID != s.ID {
		t.Fatal("legacy transfer mutated a shared session")
	}
}

func TestSharedConcurrentClaimsPublishOneNotice(t *testing.T) {
	m := New(&foregroundFixture{})
	s := shareCooperative(t, m)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var notices atomic.Int32
	callback := func(Window) error {
		if notices.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	results := make(chan error, 8)
	for range 8 {
		go func() { _, err := m.Claim(ctx, "t", s.ID, s.Revision, "turn", callback); results <- err }()
	}
	<-started
	close(release)
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if notices.Load() != 1 {
		t.Fatal("same Turn published duplicate notices")
	}
}

func TestSharedClaimFailureStopAndCancellationFailClosed(t *testing.T) {
	for _, operation := range []string{"notice_failure", "stop", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			m := New(&foregroundFixture{})
			s := shareCooperative(t, m)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := m.Claim(ctx, "t", s.ID, s.Revision, "turn", func(Window) error {
				switch operation {
				case "notice_failure":
					return errors.New("notice not persisted")
				case "stop":
					m.Stop("t", s.ID)
				case "cancel":
					cancel()
				}
				return nil
			})
			if err == nil {
				t.Fatal("failed/revoked claim succeeded")
			}
			if current := m.Status("t").Session; current != nil && current.Claimed {
				t.Fatal("failed claim retained access")
			}
		})
	}
}

func TestSharedInputRemainsSingleFlightAndOwnerStopCancels(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	s := shareCooperative(t, m)
	if _, err := m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", func(Window) error { return nil }); err != nil {
		t.Fatal(err)
	}
	owner := foregroundRequest(t, m, s, "owner", Action{Kind: "key", Keys: []string{"ENTER"}})
	agent := foregroundRequest(t, m, s, "agent:turn", Action{Kind: "key", Keys: []string{"ENTER"}})
	entered := make(chan struct{})
	p.input = func(ctx context.Context, _ Window, _ Action, _ Observation) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := m.Act(context.Background(), "t", "agent:turn", agent); result <- err }()
	<-entered
	_, err := m.Act(context.Background(), "t", "owner", owner)
	if err == nil {
		t.Fatal("overlapping owner input dispatched")
	}
	if p.inputs.Load() != 1 {
		t.Fatal("native inputs overlapped")
	}
	if _, err := m.Stop("t", s.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stopped input succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Owner Stop did not cancel Agent input")
	}
	if m.Status("t").Session != nil {
		t.Fatal("Stop did not revoke joint grant")
	}
}

func TestSharedErrorRevokesFramesButDoesNotTransferOrReplay(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	s := shareCooperative(t, m)
	m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", func(Window) error { return nil })
	request := foregroundRequest(t, m, s, "agent:turn", Action{Kind: "key", Keys: []string{"ENTER"}})
	request.ActionID = "uncertain-action"
	p.input = func(context.Context, Window, Action, Observation) error { return context.DeadlineExceeded }
	if _, err := m.Act(context.Background(), "t", "agent:turn", request); err == nil {
		t.Fatal("native error ignored")
	}
	current := m.Status("t").Session
	if current == nil || current.Controller != "shared" || current.Claimed || current.Revision != s.Revision+1 {
		t.Fatal("uncertain input transferred control or retained authorization frames")
	}
	p.input = nil
	m.Claim(context.Background(), "t", s.ID, current.Revision, "turn", func(Window) error { return nil })
	fresh := foregroundRequest(t, m, *current, "agent:turn", Action{Kind: "key", Keys: []string{"ENTER"}})
	fresh.ActionID = request.ActionID
	_, err := m.Act(context.Background(), "t", "agent:turn", fresh)
	wantError(t, err, "action_replayed")
	if p.inputs.Load() != 1 {
		t.Fatal("uncertain input was replayed")
	}
}

func TestSharedSwitchAndExpiryRevokeBothParticipants(t *testing.T) {
	p := &selectionFixture{}
	m := New(p)
	s := shareCooperative(t, m)
	owner := foregroundRequest(t, m, s, "owner", Action{Kind: "key", Keys: []string{"ENTER"}})
	m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", func(Window) error { return nil })
	agent := foregroundRequest(t, m, s, "agent:turn", Action{Kind: "key", Keys: []string{"ENTER"}})
	next, err := m.SwitchSharedTarget(context.Background(), "t", s.ID, s.Revision, TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", true)
	if err != nil || next.Session.ID == s.ID || next.Session.Controller != "shared" || next.Session.Claimed {
		t.Fatal("joint target switch did not replace the grant", err)
	}
	for actor, request := range map[string]ActionRequest{"owner": owner, "agent:turn": agent} {
		_, err := m.Act(context.Background(), "t", actor, request)
		wantError(t, err, "stale_session")
	}
	m.now = func() time.Time { return time.Now().Add(sessionTTL + time.Second) }
	for _, actor := range []string{"owner", "agent:turn"} {
		_, err := m.Observe(context.Background(), "t", next.Session.ID, actor)
		wantError(t, err, "not_shared")
	}
}

func TestSharedClaimsWaitWithoutHoldingManagerLock(t *testing.T) {
	m := New(&foregroundFixture{})
	s := shareCooperative(t, m)
	entered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", func(Window) error {
			close(entered)
			<-release
			return nil
		})
	}()
	defer wg.Wait()
	defer close(release)
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Claim(ctx, "t", s.ID, s.Revision, "turn", func(Window) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("waiting canceled request did not terminate")
	}
	if _, err := m.Stop("t", s.ID); err != nil {
		t.Fatal("pending claim blocked Owner stop", err)
	}
}

func TestSharedControlErrorCannotAuthorizeEitherParticipant(t *testing.T) {
	p := &selectionFixture{capture: func(context.Context, Window) (Observation, error) {
		return Observation{Image: "fixture", Width: 800, Height: 600, Surface: "fixture",
			ControlError: "desktop_foreground_unavailable", InputActions: []string{"key"}}, nil
	}}
	m := New(p)
	s := shareCooperative(t, m)
	m.Claim(context.Background(), "t", s.ID, s.Revision, "turn", func(Window) error { return nil })
	for _, actor := range []string{"owner", "agent:turn"} {
		o, err := m.Observe(context.Background(), "t", s.ID, actor)
		if err != nil || o.Image == "" || len(o.InputActions) != 0 {
			t.Fatal("control error did not produce a read-only image", err)
		}
		_, err = m.Act(context.Background(), "t", actor, ActionRequest{SessionID: s.ID, Revision: s.Revision,
			ObservationID: o.ID, Action: Action{ElementID: "$surface", Kind: "key", Keys: []string{"ENTER"}}})
		wantError(t, err, "unsupported_action")
	}
	if p.inputs.Load() != 0 {
		t.Fatal("quarantined observation enabled input")
	}
}
