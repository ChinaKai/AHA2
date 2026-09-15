package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitQueue(t *testing.T, q *captureQueue, count int) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for {
		q.mu.Lock()
		n := len(q.waiting)
		q.mu.Unlock()
		if n == count {
			return
		}
		if time.Now().After(until) {
			t.Fatalf("capture queue length %d, want %d", n, count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCaptureQueuePriorityAndPreviewProgress(t *testing.T) {
	var q captureQueue
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := q.acquire(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	order := make(chan string, 7)
	for i, item := range []struct {
		name     string
		priority bool
	}{
		{"preview1", false}, {"shot1", true}, {"shot2", true}, {"shot3", true}, {"shot4", true}, {"shot5", true}, {"preview2", false},
	} {
		go func(name string, priority bool) {
			done, err := q.acquire(ctx, priority)
			if err != nil {
				order <- "error"
				return
			}
			order <- name
			done()
		}(item.name, item.priority)
		waitQueue(t, &q, i+1)
	}
	release()
	for _, want := range []string{"shot1", "shot2", "shot3", "preview1", "shot4", "shot5", "preview2"} {
		select {
		case got := <-order:
			if got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestCaptureQueueCanceledAndBounded(t *testing.T) {
	var q captureQueue
	release, err := q.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var bad atomic.Int32
	for range captureQueueLimit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, err := q.acquire(ctx, true)
			if err == nil {
				done()
				bad.Add(1)
			} else if !errors.Is(err, context.Canceled) {
				bad.Add(1)
			}
		}()
	}
	waitQueue(t, &q, captureQueueLimit)
	bounded, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	_, err = q.acquire(bounded, true)
	wantError(t, err, "busy")
	cancel()
	wg.Wait()
	waitQueue(t, &q, 0)
	if bad.Load() != 0 {
		t.Fatal("canceled waiters were dispatched")
	}
	release()
	done, err := q.acquire(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	done()
}

type queuedCaptureFixture struct {
	foregroundFixture
	capture func(context.Context, bool) (Observation, error)
}

func (p *queuedCaptureFixture) ObserveForeground(ctx context.Context, w Window) (Observation, error) {
	if p.capture != nil {
		return p.capture(ctx, false)
	}
	return p.foregroundFixture.ObserveForeground(ctx, w)
}
func (p *queuedCaptureFixture) ObservePreview(ctx context.Context, w Window, _ FrameOptions) (Observation, error) {
	if p.capture != nil {
		return p.capture(ctx, true)
	}
	return p.foregroundFixture.ObserveForeground(ctx, w)
}

func TestLivePreviewCannotStarveSingleAgentObservation(t *testing.T) {
	first, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	order := make(chan string, 3)
	p := &queuedCaptureFixture{}
	p.capture = func(ctx context.Context, preview bool) (Observation, error) {
		if preview {
			order <- "preview"
		} else {
			order <- "agent"
		}
		if calls.Add(1) == 1 {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return Observation{}, ctx.Err()
			}
		}
		return p.foregroundFixture.ObserveForeground(ctx, Window{})
	}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	state, err := m.Control("t", s.ID, 1, "agent")
	if err != nil {
		t.Fatal(err)
	}
	s = *state.Session
	drop, err := m.RegisterStream("t", s.ID, "owner:view")
	if err != nil {
		t.Fatal(err)
	}
	defer drop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan error, 3)
	go func() { _, err := m.ObserveStream(ctx, "t", s.ID, "owner:view", FrameOptions{}); results <- err }()
	<-first
	go func() { _, err := m.ObserveStream(ctx, "t", s.ID, "owner:view", FrameOptions{}); results <- err }()
	waitQueue(t, &m.captureQueue, 1)
	go func() { _, err := m.Observe(ctx, "t", s.ID, "agent:turn"); results <- err }()
	waitQueue(t, &m.captureQueue, 2)
	close(release)
	for range 3 {
		if err := <-results; err != nil {
			t.Fatalf("one request should wait instead of returning busy: %v", err)
		}
	}
	for _, want := range []string{"preview", "agent", "preview"} {
		if got := <-order; got != want {
			t.Fatalf("native dispatch=%s, want %s", got, want)
		}
	}
}

func TestCancelQueuedObservationDoesNotCancelPreviewOrGrant(t *testing.T) {
	first, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := &queuedCaptureFixture{}
	p.capture = func(ctx context.Context, _ bool) (Observation, error) {
		if calls.Add(1) == 1 {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return Observation{}, ctx.Err()
			}
		}
		return p.foregroundFixture.ObserveForeground(ctx, Window{})
	}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	state, _ := m.Control("t", s.ID, 1, "agent")
	s = *state.Session
	drop, _ := m.RegisterStream("t", s.ID, "owner:view")
	defer drop()
	preview := make(chan error, 1)
	go func() {
		_, err := m.ObserveStream(context.Background(), "t", s.ID, "owner:view", FrameOptions{})
		preview <- err
	}()
	<-first
	ctx, cancel := context.WithCancel(context.Background())
	shot := make(chan error, 1)
	go func() { _, err := m.Observe(ctx, "t", s.ID, "agent:turn"); shot <- err }()
	waitQueue(t, &m.captureQueue, 1)
	cancel()
	select {
	case err := <-shot:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancel waited for preview")
	}
	if now := m.Status("t").Session; now == nil || now.ID != s.ID || now.Revision != s.Revision {
		t.Fatal("read cancellation revoked grant")
	}
	if calls.Load() != 1 {
		t.Fatal("canceled snapshot reached provider")
	}
	close(release)
	if err := <-preview; err != nil {
		t.Fatal("unrelated preview canceled", err)
	}
}

func TestTakeoverCancelsQueuedAgentBeforeNativeWork(t *testing.T) {
	first, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := &queuedCaptureFixture{}
	p.capture = func(ctx context.Context, _ bool) (Observation, error) {
		calls.Add(1)
		close(first)
		<-release
		return Observation{}, ctx.Err()
	}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	state, _ := m.Control("t", s.ID, 1, "agent")
	s = *state.Session
	drop, _ := m.RegisterStream("t", s.ID, "owner:view")
	defer drop()
	preview := make(chan error, 1)
	go func() {
		_, err := m.ObserveStream(context.Background(), "t", s.ID, "owner:view", FrameOptions{})
		preview <- err
	}()
	<-first
	shot := make(chan error, 1)
	go func() { _, err := m.Observe(context.Background(), "t", s.ID, "agent:turn"); shot <- err }()
	waitQueue(t, &m.captureQueue, 1)
	if _, err := m.Control("t", s.ID, s.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-shot:
		if err == nil {
			t.Fatal("revoked queued read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("queued read not revoked promptly")
	}
	close(release)
	if err := <-preview; err == nil {
		t.Fatal("revoked preview published")
	}
	if calls.Load() != 1 {
		t.Fatal("queued read captured under new controller")
	}
}

func TestCaptureFairnessUnderContinuousPreview(t *testing.T) {
	p := &queuedCaptureFixture{}
	p.capture = func(ctx context.Context, _ bool) (Observation, error) {
		select {
		case <-time.After(15 * time.Millisecond):
		case <-ctx.Done():
			return Observation{}, ctx.Err()
		}
		return p.foregroundFixture.ObserveForeground(ctx, Window{})
	}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	state, _ := m.Control("t", s.ID, 1, "agent")
	s = *state.Session
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := range 4 {
		actor := fmt.Sprintf("owner:v%d", i)
		drop, err := m.RegisterStream("t", s.ID, actor)
		if err != nil {
			t.Fatal(err)
		}
		defer drop()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_, _ = m.ObserveStream(ctx, "t", s.ID, actor, FrameOptions{})
			}
		}()
	}
	defer func() { cancel(); wg.Wait() }()
	for range 10 {
		shotCtx, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := m.Observe(shotCtx, "t", s.ID, "agent:turn")
		stop()
		if err != nil {
			t.Fatalf("single Agent request failed while four viewers loop: %v", err)
		}
	}
}

func TestQueuedObservationRevokedBeforeNativeWork(t *testing.T) {
	for _, operation := range []string{"stop", "close", "expiry", "switch"} {
		t.Run(operation, func(t *testing.T) {
			p := &queuedCaptureFixture{}
			var calls atomic.Int32
			p.capture = func(ctx context.Context, _ bool) (Observation, error) {
				calls.Add(1)
				return p.foregroundFixture.ObserveForeground(ctx, Window{})
			}
			m := New(p)
			s := shareForeground(t, m, "t", "w1")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			release, err := m.captureQueue.acquire(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			result := make(chan error, 1)
			go func() { _, err := m.Observe(ctx, "t", s.ID, "owner"); result <- err }()
			waitQueue(t, &m.captureQueue, 1)
			switchResult := make(chan error, 1)
			switch operation {
			case "stop":
				_, err = m.Stop("t", s.ID)
			case "close":
				err = m.Close()
			case "expiry":
				m.mu.Lock()
				m.sessions["t"].expires = time.Now().Add(-time.Second)
				m.mu.Unlock()
				m.Status("t")
			case "switch":
				go func() {
					_, err := m.SwitchTarget(ctx, "t", s.ID, s.Revision, TargetSelection{Kind: "window", WindowID: "w2"}, "foreground", true)
					switchResult <- err
				}()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("revoked waiting observation succeeded")
				}
			case <-ctx.Done():
				t.Fatal("revoked waiter did not cancel before capture lane was released")
			}
			if calls.Load() != 0 {
				t.Fatal("revoked request reached native provider")
			}
			release()
			if operation == "switch" {
				if err := <-switchResult; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestQueuedStreamCannotCrossLeaseReplacement(t *testing.T) {
	p := &queuedCaptureFixture{}
	var calls atomic.Int32
	p.capture = func(ctx context.Context, _ bool) (Observation, error) {
		calls.Add(1)
		return p.foregroundFixture.ObserveForeground(ctx, Window{})
	}
	m := New(p)
	s := shareForeground(t, m, "t", "w1")
	drop, err := m.RegisterStream("t", s.ID, "owner:view")
	if err != nil {
		t.Fatal(err)
	}
	defer drop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := m.captureQueue.acquire(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result := make(chan error, 1)
	go func() { _, err := m.ObserveStream(ctx, "t", s.ID, "owner:view", FrameOptions{}); result <- err }()
	waitQueue(t, &m.captureQueue, 1)
	drop()
	dropNew, err := m.RegisterStream("t", s.ID, "owner:view")
	if err != nil {
		t.Fatal(err)
	}
	defer dropNew()
	release()
	wantError(t, <-result, "stream_closed")
	if calls.Load() != 0 {
		t.Fatal("old lease captured under replacement stream")
	}
	if _, err := m.ObserveStream(ctx, "t", s.ID, "owner:view", FrameOptions{}); err != nil {
		t.Fatal("replacement stream cannot capture", err)
	}
}

func TestQueuedCaptureDoesNotBlockForegroundInput(t *testing.T) {
	m := New(&queuedCaptureFixture{})
	s := shareForeground(t, m, "t", "w1")
	request := foregroundRequest(t, m, s, "owner", Action{Kind: "key", Keys: []string{"ENTER"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := m.captureQueue.acquire(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result := make(chan error, 1)
	go func() { _, err := m.Observe(ctx, "t", s.ID, "owner"); result <- err }()
	waitQueue(t, &m.captureQueue, 1)
	if _, err := m.Act(ctx, "t", "owner", request); err != nil {
		t.Fatal("input blocked behind queued observation", err)
	}
	release()
	if err := <-result; err != nil {
		t.Fatal("fresh capture after input failed", err)
	}
}

type queuedTargetsFixture struct {
	queuedCaptureFixture
	enumerations atomic.Int32
}

func (p *queuedTargetsFixture) Windows(ctx context.Context) ([]Window, error) {
	p.enumerations.Add(1)
	return p.fakeProvider.Windows(ctx)
}

func (p *queuedTargetsFixture) Targets(context.Context) (Targets, error) {
	p.enumerations.Add(1)
	return Targets{}, nil
}

func (*queuedTargetsFixture) SelectTarget(context.Context, TargetSelection) (Window, error) {
	return Window{}, errors.New("unused target selection")
}

func TestEnumerationsShareCaptureScheduler(t *testing.T) {
	for _, closeManager := range []bool{false, true} {
		t.Run(fmt.Sprint(closeManager), func(t *testing.T) {
			p := &queuedTargetsFixture{}
			m := New(p)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			release, err := m.captureQueue.acquire(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			result := make(chan error, 2)
			go func() { _, err := m.Windows(ctx); result <- err }()
			go func() { _, err := m.Targets(ctx); result <- err }()
			waitQueue(t, &m.captureQueue, 2)
			if p.enumerations.Load() != 0 {
				t.Fatal("enumeration bypassed capture scheduler")
			}
			if closeManager {
				m.Close()
			}
			release()
			for range 2 {
				err := <-result
				if closeManager {
					wantError(t, err, "unsupported")
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if closeManager && p.enumerations.Load() != 0 {
				t.Fatal("enumeration dispatched after manager close")
			}
		})
	}
}

func TestObservationWaitIncludesGrantExpiry(t *testing.T) {
	m := New(&queuedCaptureFixture{})
	s := shareForeground(t, m, "t", "w1")
	m.mu.Lock()
	m.sessions["t"].expires = time.Now().Add(50 * time.Millisecond)
	m.mu.Unlock()
	release, err := m.captureQueue.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = m.Observe(ctx, "t", s.ID, "owner")
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatal("capture wait did not stop at grant expiry", err)
	}
	waitQueue(t, &m.captureQueue, 0)
	if m.Status("t").Session != nil {
		t.Fatal("expired grant survived")
	}
}

func TestObservationPerSessionQueueBound(t *testing.T) {
	m := New(&queuedCaptureFixture{})
	s := shareForeground(t, m, "t", "w1")
	release, err := m.captureQueue.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results := make(chan error, 16)
	for i := range 16 {
		go func() { _, err := m.Observe(ctx, "t", s.ID, "owner"); results <- err }()
		waitQueue(t, &m.captureQueue, i+1)
	}
	_, err = m.Observe(ctx, "t", s.ID, "owner")
	wantError(t, err, "busy")
	cancel()
	for range 16 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatal("waiting observation was not canceled", err)
		}
	}
	waitQueue(t, &m.captureQueue, 0)
	m.mu.Lock()
	waiters := len(m.sessions["t"].captureWaiters)
	m.mu.Unlock()
	if waiters != 0 {
		t.Fatal("session leaked canceled capture waiters")
	}
}
