package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	act     func(context.Context, Window, Action) error
	observe func(context.Context, Window) (Observation, error)
	calls   atomic.Int32
}

func (p *fakeProvider) Support() (bool, string) { return true, "" }
func (p *fakeProvider) Windows(context.Context) ([]Window, error) {
	return []Window{{ID: "w1", Title: "Fixture", Process: "fixture"}, {ID: "w2", Title: "Other"}}, nil
}
func (p *fakeProvider) Observe(ctx context.Context, window Window) (Observation, error) {
	if p.observe != nil {
		return p.observe(ctx, window)
	}
	return Observation{Elements: []Element{{ID: "e1", Name: "test", Actions: []string{"invoke", "set_value"}}}}, nil
}
func (p *fakeProvider) Act(ctx context.Context, window Window, action Action) error {
	p.calls.Add(1)
	if p.act != nil {
		return p.act(ctx, window, action)
	}
	return nil
}

func share(t *testing.T, manager *Manager, taskID, windowID string) Session {
	t.Helper()
	status, err := manager.Open(context.Background(), taskID, windowID)
	if err != nil {
		t.Fatal(err)
	}
	return *status.Session
}

func observe(t *testing.T, manager *Manager, session Session, actor string) ActionRequest {
	t.Helper()
	result, err := manager.Observe(context.Background(), session.TaskID, session.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	return ActionRequest{SessionID: session.ID, Revision: result.Revision, ObservationID: result.ID,
		Action: Action{Kind: "invoke", ElementID: "e1"}}
}

func wantError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || ErrorCode(err) != "desktop_"+code {
		t.Fatalf("error = %v, want desktop_%s", err, code)
	}
}

func TestSharingRequiresSelectedWindowAndExclusiveTask(t *testing.T) {
	m := New(&fakeProvider{})
	_, err := m.Open(context.Background(), "task1", "arbitrary-hwnd")
	wantError(t, err, "window_gone")
	session := share(t, m, "task1", "w1")
	_, err = m.Open(context.Background(), "task2", "w1")
	wantError(t, err, "window_in_use")
	_, err = m.Observe(context.Background(), "task2", session.ID, "owner")
	wantError(t, err, "not_shared")
	_, err = m.Control("task1", session.ID, session.Revision, "attacker")
	wantError(t, err, "invalid_control")
	_, err = m.Open(context.Background(), "task1", "w2")
	wantError(t, err, "session_exists")
}

func TestAgentGrantClaimAndNoticeAreRequired(t *testing.T) {
	p := &fakeProvider{}
	m := New(p)
	ctx := context.Background()
	session := share(t, m, "task1", "w1")
	_, err := m.Observe(ctx, session.TaskID, session.ID, "agent:turn1")
	wantError(t, err, "owner_control")
	_, err = m.Claim(ctx, session.TaskID, session.ID, session.Revision, "turn1", func(Window) error { return nil })
	wantError(t, err, "owner_control")
	status, err := m.Control(session.TaskID, session.ID, session.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	session = *status.Session
	request := observe(t, m, session, "agent:turn1")
	_, err = m.Act(ctx, session.TaskID, "agent:turn1", request)
	wantError(t, err, "claim_required")
	noticeErr := errors.New("cannot publish")
	_, err = m.Claim(ctx, session.TaskID, session.ID, session.Revision, "turn1", func(Window) error { return noticeErr })
	if !errors.Is(err, noticeErr) || m.Status(session.TaskID).Session.Claimed {
		t.Fatalf("failed notice permitted control: %v", err)
	}
	notices := 0
	for range 2 {
		_, err = m.Claim(ctx, session.TaskID, session.ID, session.Revision, "turn1", func(window Window) error {
			notices++
			if window.ID != "w1" || p.calls.Load() != 0 {
				t.Fatal("notice must precede action on the selected target")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if notices != 1 {
		t.Fatalf("duplicate notice: %d", notices)
	}
	// Owner previews must not invalidate the agent's observation.
	observe(t, m, session, "owner")
	if _, err = m.Act(ctx, session.TaskID, "agent:turn1", request); err != nil {
		t.Fatal(err)
	}
	_, err = m.Act(ctx, session.TaskID, "agent:turn1", request)
	wantError(t, err, "stale_observation")
	request = observe(t, m, session, "agent:turn2")
	_, err = m.Act(ctx, session.TaskID, "agent:turn2", request)
	wantError(t, err, "claim_required")
}

func TestOwnerTakeoverRevokesAgentAndStaleRequests(t *testing.T) {
	m := New(&fakeProvider{})
	ctx := context.Background()
	session := share(t, m, "t", "w1")
	request := observe(t, m, session, "owner")
	status, err := m.Control("t", session.ID, session.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Act(ctx, "t", "owner", request)
	wantError(t, err, "stale_session")
	request = observe(t, m, *status.Session, "owner")
	_, err = m.Act(ctx, "t", "owner", request)
	wantError(t, err, "agent_control")
	if _, err = m.Control("t", session.ID, status.Session.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Observe(ctx, "t", session.ID, "agent:turn")
	wantError(t, err, "owner_control")
	_, err = m.Control("t", session.ID, session.Revision, "agent")
	wantError(t, err, "stale_session")
}

func TestObservationTTLAndActionAllowlist(t *testing.T) {
	p := &fakeProvider{}
	m := New(p)
	now := time.Now()
	m.now = func() time.Time { return now }
	ctx := context.Background()
	session := share(t, m, "t", "w1")
	request := observe(t, m, session, "owner")
	for _, kind := range []string{"shell", "click", "send_keys", "toggle"} {
		changed := request
		changed.Kind = kind
		_, err := m.Act(ctx, "t", "owner", changed)
		wantError(t, err, "unsupported_action")
	}
	changed := request
	changed.ElementID = "not-observed"
	_, err := m.Act(ctx, "t", "owner", changed)
	wantError(t, err, "unsupported_action")
	now = now.Add(observationTTL)
	_, err = m.Act(ctx, "t", "owner", request)
	wantError(t, err, "stale_observation")
	if p.calls.Load() != 0 {
		t.Fatal("invalid actions reached the provider")
	}
}

func TestSessionExpiresAndRestartDoesNotRestoreGrant(t *testing.T) {
	p := &fakeProvider{}
	m := New(p)
	now := time.Now()
	m.now = func() time.Time { return now }
	session := share(t, m, "t", "w1")
	now = now.Add(sessionTTL)
	if m.Status("t").Session != nil {
		t.Fatal("expired session remains")
	}
	_, err := m.Observe(context.Background(), "t", session.ID, "owner")
	wantError(t, err, "not_shared")
	if New(p).Status("t").Session != nil {
		t.Fatal("grant restored across manager restart")
	}
}

func TestStopCancelsInFlightAndPreventsReopenOverlap(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &fakeProvider{act: func(ctx context.Context, _ Window, _ Action) error {
		close(entered)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}}
	m := New(p)
	session := share(t, m, "t", "w1")
	request := observe(t, m, session, "owner")
	done := make(chan error, 1)
	go func() { _, err := m.Act(context.Background(), "t", "owner", request); done <- err }()
	<-entered
	if _, err := m.Stop("t", session.ID); err != nil {
		t.Fatal(err)
	}
	_, err := m.Open(context.Background(), "other", "w1")
	wantError(t, err, "busy")
	close(release)
	wantError(t, <-done, "not_shared")
	reopened := share(t, m, "other", "w1")
	_, err = m.Stop("other", session.ID)
	wantError(t, err, "stale_session")
	if m.Status("other").Session.ID != reopened.ID {
		t.Fatal("stale stop revoked a replacement session")
	}
}

func TestConcurrentActionsDoNotQueueAndFailurePauses(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &fakeProvider{act: func(context.Context, Window, Action) error {
		close(entered)
		<-release
		return context.DeadlineExceeded
	}}
	m := New(p)
	session := share(t, m, "t", "w1")
	request := observe(t, m, session, "owner")
	done := make(chan error, 1)
	go func() { _, err := m.Act(context.Background(), "t", "owner", request); done <- err }()
	<-entered
	_, err := m.Observe(context.Background(), "t", session.ID, "owner")
	wantError(t, err, "busy")
	close(release)
	wantError(t, <-done, "timeout")
	status := m.Status("t")
	if status.Session.Controller != "owner" || status.Session.Revision <= session.Revision {
		t.Fatal("uncertain action must pause control and invalidate pending requests")
	}
	if p.calls.Load() != 1 {
		t.Fatal("action retried")
	}
}

func TestTakeoverWhileNoticeIsPublishingCannotRegrantAgent(t *testing.T) {
	m := New(&fakeProvider{})
	session := share(t, m, "t", "w1")
	status, _ := m.Control("t", session.ID, session.Revision, "agent")
	session = *status.Session
	_, err := m.Claim(context.Background(), "t", session.ID, session.Revision, "turn", func(Window) error {
		_, err := m.Control("t", session.ID, session.Revision, "owner")
		return err
	})
	wantError(t, err, "stale_session")
	if m.Status("t").Session.Controller != "owner" {
		t.Fatal("notice race restored agent permission")
	}
}

func TestNativeErrorsAreCodedWithoutLeakingContent(t *testing.T) {
	if got := ErrorCode(errors.New("private native exception: document content")); got != "desktop_native_failed" {
		t.Fatalf("unsafe error code: %q", got)
	}
	if got := ErrorCode(errors.New("desktop focus_side_effect")); got != "desktop_focus_side_effect" {
		t.Fatalf("lost native diagnostic: %q", got)
	}
}
