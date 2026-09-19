package desktop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func viewRequest(t *testing.T, m *Manager, s Session, view string) ActionRequest {
	t.Helper()
	actor, err := ViewActor("owner", view)
	if err != nil {
		t.Fatal(err)
	}
	o, err := m.Observe(context.Background(), s.TaskID, s.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	return ActionRequest{SessionID: s.ID, Revision: s.Revision, ObservationID: o.ID, ViewID: view,
		Action: Action{ElementID: "$surface", Kind: "click", X: 20, Y: 30}}
}

func TestFramesDelayedDeliveryViewsAndReplay(t *testing.T) {
	p := &foregroundFixture{}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	first := viewRequest(t, m, s, "first")
	second := viewRequest(t, m, s, "second")
	for range 10 {
		viewRequest(t, m, s, "first")
	}
	wrong := first
	wrong.ViewID = "second"
	_, err := m.Act(context.Background(), "t", "owner", wrong)
	wantError(t, err, "stale_observation")
	first.ActionID = "click-1"
	for _, id := range []string{"click-1", "click-2"} {
		first.ActionID = id
		if _, err := m.Act(context.Background(), "t", "owner", first); err != nil {
			t.Fatalf("delivered frame was invalidated by a newer frame/action: %v", err)
		}
	}
	_, err = m.Act(context.Background(), "t", "owner", first)
	wantError(t, err, "action_replayed")
	newFrame := viewRequest(t, m, s, "first")
	newFrame.ActionID = first.ActionID
	_, err = m.Act(context.Background(), "t", "owner", newFrame)
	wantError(t, err, "action_replayed")
	if _, err := m.Act(context.Background(), "t", "owner", second); err != nil {
		t.Fatal("another owner's view was invalidated", err)
	}
	_, err = m.Act(context.Background(), "t", "owner", second)
	wantError(t, err, "stale_observation")
	if p.inputs.Load() != 3 {
		t.Fatal("input count", p.inputs.Load())
	}
}

func TestFramesBoundedMetadataExpiryAndViews(t *testing.T) {
	m := New(&foregroundFixture{})
	now := time.Now()
	m.now = func() time.Time { return now }
	s := shareForeground(t, m, "t", "w1")
	first := viewRequest(t, m, s, "first")
	for range 64 {
		viewRequest(t, m, s, "first")
	}
	_, err := m.Act(context.Background(), "t", "owner", first)
	wantError(t, err, "stale_observation")
	if n := len(m.sessions["t"].observed["owner:first"]); n != 64 {
		t.Fatal("unbounded snapshots", n)
	}
	for i := range 7 {
		viewRequest(t, m, s, fmt.Sprint("v", i))
	}
	_, err = m.Observe(context.Background(), "t", s.ID, "owner:overflow")
	wantError(t, err, "view_limit")
	m.DropView("t", s.ID, "owner:v0")
	last := viewRequest(t, m, s, "last")
	now = now.Add(observationTTL)
	_, err = m.Act(context.Background(), "t", "owner", last)
	wantError(t, err, "stale_observation")
	viewRequest(t, m, s, "expired-views-freed")

	retained := New(&fakeProvider{})
	b := share(t, retained, "b", "w1")
	for range 5 {
		observe(t, retained, b, "owner")
	}
	frames := retained.sessions["b"].observed["owner"]
	if len(frames) != 5 || len(frames) > 64 {
		t.Fatalf("retained snapshot count = %d, want 5 (bounded)", len(frames))
	}
	for _, frame := range frames {
		for _, e := range frame.elements {
			if e.Name != "" || e.Value != "" {
				t.Fatal("unneeded UI contents retained")
			}
		}
	}
}

func TestFramesStreamLeaseAndRevocation(t *testing.T) {
	m := New(&foregroundFixture{})
	s := shareForeground(t, m, "t", "w1")
	release, err := m.RegisterStream("t", s.ID, "owner:view")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.RegisterStream("t", s.ID, "owner:view")
	wantError(t, err, "view_in_use")
	snapshot, err := m.ObserveStream(context.Background(), "t", s.ID, "owner:view", FrameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frame := ActionRequest{SessionID: s.ID, Revision: s.Revision, ViewID: "view", ObservationID: snapshot.ID,
		Action: Action{ElementID: "$surface", Kind: "click"}}
	fallback := viewRequest(t, m, s, "view")
	release()
	_, err = m.Act(context.Background(), "t", "owner", frame)
	wantError(t, err, "stale_observation")
	if _, err := m.Act(context.Background(), "t", "owner", fallback); err != nil {
		t.Fatal("late socket cleanup revoked a newer fallback frame", err)
	}
	frame = viewRequest(t, m, s, "view")
	if _, err := m.Control("t", s.ID, s.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Act(context.Background(), "t", "owner", frame)
	wantError(t, err, "stale_session")
}

func TestFramesReplayCapacityFailsClosed(t *testing.T) {
	m := New(&foregroundFixture{})
	s := shareForeground(t, m, "t", "w1")
	frame := viewRequest(t, m, s, "view")
	for i := range 4096 {
		m.sessions["t"].replayed[fmt.Sprint(i)] = time.Now()
	}
	frame.ActionID = "fresh"
	_, err := m.Act(context.Background(), "t", "owner", frame)
	wantError(t, err, "action_limit")
	if len(m.sessions["t"].replayed) != 4096 {
		t.Fatal("replay capacity was evicted")
	}
}

type blockingCaptureFixture struct {
	foregroundFixture
	mu      sync.Mutex
	block   bool
	entered chan struct{}
	release chan struct{}
}

func (p *blockingCaptureFixture) ObserveForeground(ctx context.Context, w Window) (Observation, error) {
	p.mu.Lock()
	block := p.block
	p.mu.Unlock()
	if block {
		close(p.entered)
		select {
		case <-ctx.Done():
			return Observation{}, ctx.Err()
		case <-p.release:
		}
	}
	return p.foregroundFixture.ObserveForeground(ctx, w)
}

func TestCaptureDoesNotBlockInputAndCrossEpochResultDropped(t *testing.T) {
	p := &blockingCaptureFixture{entered: make(chan struct{}), release: make(chan struct{})}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	frame := viewRequest(t, m, s, "view")
	p.mu.Lock()
	p.block = true
	p.mu.Unlock()
	result := make(chan error, 1)
	go func() { _, err := m.Observe(context.Background(), "t", s.ID, "owner:view"); result <- err }()
	<-p.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := m.Act(ctx, "t", "owner", frame); err != nil {
		t.Fatal("input waited behind capture", err)
	}
	close(p.release)
	wantError(t, <-result, "frame_superseded")
}

func TestStopCancelsCaptureAndBlocksReopenUntilReaped(t *testing.T) {
	p := &blockingCaptureFixture{entered: make(chan struct{}), release: make(chan struct{}), block: true}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	result := make(chan error, 1)
	go func() { _, err := m.Observe(context.Background(), "t", s.ID, "owner:view"); result <- err }()
	<-p.entered
	if _, err := m.Stop("t", s.ID); err != nil {
		t.Fatal(err)
	}
	wantError(t, <-result, "not_shared")
	if len(m.capturing) != 0 {
		t.Fatal("capture lane not released")
	}
}

func TestFramePreferencesAndActorValidation(t *testing.T) {
	for _, view := range []string{"a:b", "../owner", "a b", "\u4e2d\u6587", strings.Repeat("x", 65)} {
		if _, err := ViewActor("owner", view); err == nil {
			t.Fatal("invalid view", view)
		}
	}
	if _, err := ViewActor("agent:turn", "owner"); err == nil {
		t.Fatal("agent selected owner view")
	}
	for _, options := range []FrameOptions{{MaxWidth: -1}, {MaxWidth: 1921}, {MaxHeight: 179}, {Quality: 86}} {
		if _, err := NormalizeFrameOptions(options); err == nil {
			t.Fatal("invalid options", options)
		}
	}
	o, err := NormalizeFrameOptions(FrameOptions{})
	if err != nil || o.MaxWidth != 1280 || o.MaxHeight != 720 || o.Quality != 65 {
		t.Fatal("default preview", o, err)
	}
}

func TestCloseRevokesSessionsAndPreventsNewGrants(t *testing.T) {
	m := New(&foregroundFixture{})
	s := shareForeground(t, m, "t", "w1")
	frame := viewRequest(t, m, s, "view")
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if m.Status("t").Session != nil || m.Status("t").Supported {
		t.Fatal("closed manager still advertises access")
	}
	_, err := m.Act(context.Background(), "t", "owner", frame)
	wantError(t, err, "not_shared")
	_, err = m.OpenWithMode(context.Background(), "t", "w1", "foreground", true)
	wantError(t, err, "unsupported")
}
