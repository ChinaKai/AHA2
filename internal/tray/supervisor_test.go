package tray

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeLauncher struct {
	mu       sync.Mutex
	commands []Command
	started  chan *fakeChild
}

func (l *fakeLauncher) Start(command Command) (Child, error) {
	child := &fakeChild{exit: make(chan error, 1)}
	l.mu.Lock()
	l.commands = append(l.commands, command)
	l.mu.Unlock()
	l.started <- child
	return child, nil
}

type fakeChild struct {
	exit chan error
	once sync.Once
}

func (c *fakeChild) Wait() error { return <-c.exit }
func (c *fakeChild) Stop(time.Duration) error {
	c.once.Do(func() { c.exit <- errors.New("stopped") })
	return nil
}

func TestSupervisorStopKeepsTrayAndExitStopsChild(t *testing.T) {
	launcher := &fakeLauncher{started: make(chan *fakeChild, 4)}
	command := Command{Path: "aha2.exe", Args: []string{"serve"}}
	supervisor := NewSupervisor(launcher, command, RetryPolicy{Delays: []time.Duration{time.Millisecond}, StopGrace: time.Second})
	supervisor.Run()
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	first := awaitChild(t, launcher.started)
	awaitState(t, supervisor, StateRunning)
	if err := supervisor.Stop(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, supervisor, StateStopped)
	select {
	case <-supervisor.Done():
		t.Fatal("stop exited the tray supervisor")
	default:
	}

	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	second := awaitChild(t, launcher.started)
	if first == second {
		t.Fatal("second start reused exited child")
	}
	awaitState(t, supervisor, StateRunning)
	if err := supervisor.Exit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-supervisor.Done():
	case <-time.After(time.Second):
		t.Fatal("exit did not stop the child and close the supervisor")
	}
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	if len(launcher.commands) != 2 {
		t.Fatalf("launches=%d", len(launcher.commands))
	}
}

func TestSupervisorAbnormalExitUsesFiniteRetries(t *testing.T) {
	launcher := &fakeLauncher{started: make(chan *fakeChild, 4)}
	supervisor := NewSupervisor(launcher, Command{Path: "aha2.exe"}, RetryPolicy{
		Delays: []time.Duration{time.Millisecond, time.Millisecond}, StopGrace: time.Second,
	})
	supervisor.Run()
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		child := awaitChild(t, launcher.started)
		awaitState(t, supervisor, StateRunning)
		child.exit <- errors.New("unexpected exit")
	}
	awaitState(t, supervisor, StateFailed)
	if got := supervisor.Snapshot(); got.RetryCount != 2 || got.LastError == "" {
		t.Fatalf("failed snapshot=%#v", got)
	}
	if err := supervisor.Exit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-supervisor.Done():
	case <-time.After(time.Second):
		t.Fatal("failed supervisor did not exit")
	}
}

func awaitChild(t *testing.T, children <-chan *fakeChild) *fakeChild {
	t.Helper()
	select {
	case child := <-children:
		return child
	case <-time.After(time.Second):
		t.Fatal("child did not start")
		return nil
	}
}

func awaitState(t *testing.T, supervisor *Supervisor, state State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if supervisor.Snapshot().State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state=%s want %s", supervisor.Snapshot().State, state)
}
