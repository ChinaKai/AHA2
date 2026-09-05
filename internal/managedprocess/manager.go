package managedprocess

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

const (
	maxProcessesPerTask = 16
	managedOutputLimit  = 256 << 10
)

var (
	processNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	environmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	ErrAlreadyRunning  = errors.New("managed process is already running")
	ErrNotFound        = errors.New("managed process not found")
)

type StartRequest struct {
	Name       string            `json:"name"`
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Dir        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
}

type Status struct {
	Name       string     `json:"name"`
	TaskID     string     `json:"-"`
	AgentID    string     `json:"agent_id"`
	State      string     `json:"state"`
	Executable string     `json:"executable"`
	Args       []string   `json:"args"`
	Dir        string     `json:"cwd"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Error      string     `json:"error,omitempty"`
	OutputTail string     `json:"output_tail,omitempty"`
}

type process struct {
	status Status
	cancel context.CancelFunc
}

// Manager owns background commands independently from an Agent Turn context.
// Processes remain attached to the AHA2 service and are stopped when it closes.
type Manager struct {
	mu        sync.Mutex
	processes map[string]*process
	now       func() time.Time
	closed    bool
}

func NewManager() *Manager {
	return &Manager{processes: make(map[string]*process), now: time.Now}
}

func (m *Manager) Start(taskID, agentID string, runner workspace.Runner, request StartRequest) (Status, error) {
	if err := validateStartRequest(request); err != nil {
		return Status{}, err
	}
	key := processKey(taskID, request.Name)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Status{}, errors.New("managed process manager is closed")
	}
	if existing, ok := m.processes[key]; ok && (existing.status.State == "starting" || existing.status.State == "running" || existing.status.State == "stopping") {
		m.mu.Unlock()
		return Status{}, ErrAlreadyRunning
	}
	active := 0
	for _, item := range m.processes {
		if item.status.TaskID == taskID && (item.status.State == "starting" || item.status.State == "running" || item.status.State == "stopping") {
			active++
		}
	}
	if active >= maxProcessesPerTask {
		m.mu.Unlock()
		return Status{}, fmt.Errorf("a task may run at most %d managed processes", maxProcessesPerTask)
	}
	ctx, cancel := context.WithCancel(context.Background())
	item := &process{cancel: cancel, status: Status{
		Name: request.Name, TaskID: taskID, AgentID: agentID, State: "starting",
		Executable: request.Executable, Args: append([]string(nil), request.Args...), Dir: request.Dir,
		StartedAt: m.now().UTC(),
	}}
	m.processes[key] = item
	initial := cloneStatus(item.status)
	m.mu.Unlock()

	go m.run(ctx, key, runner, request)
	return initial, nil
}

func (m *Manager) run(ctx context.Context, key string, runner workspace.Runner, request StartRequest) {
	m.mu.Lock()
	item, ok := m.processes[key]
	if !ok {
		m.mu.Unlock()
		return
	}
	item.status.State = "running"
	m.mu.Unlock()

	result, err := runner.Run(ctx, workspace.Command{
		Executable: request.Executable, Args: request.Args, Dir: request.Dir,
		Env: request.Env, OutputLimit: managedOutputLimit, KillTree: true,
	}, func(line string) {
		m.appendOutput(key, line+"\n")
	})

	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok = m.processes[key]
	if !ok {
		return
	}
	item.status.FinishedAt = &now
	item.status.ExitCode = &result.ExitCode
	if ctx.Err() != nil {
		item.status.State = "stopped"
	} else if err != nil {
		item.status.State = "failed"
		item.status.Error = err.Error()
	} else if result.ExitCode != 0 {
		item.status.State = "failed"
	} else {
		item.status.State = "exited"
	}
	if strings.TrimSpace(result.Stderr) != "" {
		item.status.OutputTail = appendTail(item.status.OutputTail, result.Stderr, managedOutputLimit)
	}
}

func (m *Manager) List(taskID string) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Status, 0)
	for _, item := range m.processes {
		if item.status.TaskID == taskID {
			result = append(result, cloneStatus(item.status))
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func (m *Manager) Status(taskID, name string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.processes[processKey(taskID, name)]
	if !ok {
		return Status{}, ErrNotFound
	}
	return cloneStatus(item.status), nil
}

func (m *Manager) Stop(taskID, name string) (Status, error) {
	m.mu.Lock()
	item, ok := m.processes[processKey(taskID, name)]
	if !ok {
		m.mu.Unlock()
		return Status{}, ErrNotFound
	}
	if item.status.State == "starting" || item.status.State == "running" {
		item.status.State = "stopping"
		item.cancel()
	}
	status := cloneStatus(item.status)
	m.mu.Unlock()
	return status, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, item := range m.processes {
		if item.status.State == "starting" || item.status.State == "running" {
			item.status.State = "stopping"
			item.cancel()
		}
	}
	m.mu.Unlock()
}

func (m *Manager) appendOutput(key, value string) {
	m.mu.Lock()
	if item, ok := m.processes[key]; ok {
		item.status.OutputTail = appendTail(item.status.OutputTail, value, managedOutputLimit)
	}
	m.mu.Unlock()
}

func appendTail(current, value string, limit int) string {
	combined := current + value
	if len(combined) <= limit {
		return combined
	}
	return combined[len(combined)-limit:]
}

func cloneStatus(value Status) Status {
	value.Args = append([]string(nil), value.Args...)
	return value
}

func processKey(taskID, name string) string { return taskID + "\x00" + name }

func validateStartRequest(request StartRequest) error {
	if !processNamePattern.MatchString(request.Name) {
		return errors.New("name must be 1-64 letters, numbers, dot, underscore, or dash")
	}
	if strings.TrimSpace(request.Executable) == "" {
		return errors.New("executable is required")
	}
	if len(request.Args) > 128 {
		return errors.New("too many arguments")
	}
	if len(request.Env) > 64 {
		return errors.New("too many environment values")
	}
	for key := range request.Env {
		if !environmentPattern.MatchString(key) {
			return fmt.Errorf("invalid environment name %q", key)
		}
	}
	return nil
}
