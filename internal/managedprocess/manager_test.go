package managedprocess

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type blockingRunner struct {
	started chan workspace.Command
	mu      sync.Mutex
	stopped bool
}

func (runner *blockingRunner) Run(ctx context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.started <- command
	if onLine != nil {
		onLine("ready")
	}
	<-ctx.Done()
	runner.mu.Lock()
	runner.stopped = true
	runner.mu.Unlock()
	return workspace.Result{ExitCode: -1}, ctx.Err()
}

func TestManagedProcessOutlivesCallerAndCanBeStopped(t *testing.T) {
	runner := &blockingRunner{started: make(chan workspace.Command, 1)}
	manager := NewManager()
	defer manager.Close()
	status, err := manager.Start("task-1", "main", runner, StartRequest{
		Name: "dev-server", Executable: "server", Args: []string{"--port", "9000"}, Dir: "/repo",
	})
	if err != nil || status.State != "starting" {
		t.Fatalf("start: %#v %v", status, err)
	}
	select {
	case command := <-runner.started:
		if command.OutputLimit != managedOutputLimit || !command.KillTree || command.Dir != "/repo" {
			t.Fatalf("unexpected command: %#v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("managed command did not start")
	}
	if _, err := manager.Start("task-1", "main", runner, StartRequest{Name: "dev-server", Executable: "server"}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate start error = %v", err)
	}
	status, err = manager.Stop("task-1", "dev-server")
	if err != nil || status.State != "stopping" {
		t.Fatalf("stop: %#v %v", status, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, _ = manager.Status("task-1", "dev-server")
		if status.State == "stopped" {
			if status.OutputTail != "ready\n" {
				t.Fatalf("output tail = %q", status.OutputTail)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process did not stop: %#v", status)
}
