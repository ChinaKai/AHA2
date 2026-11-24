package workspaceterminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	Limit           = 6
	frameSize       = 16 << 10
	outputCapacity  = 256 << 10
	reconnectWindow = 10 * time.Minute
	idleTimeout     = 2 * time.Hour
)

var (
	ErrExists  = errors.New("workspace_terminal_already_exists")
	ErrLimit   = errors.New("workspace_terminal_limit")
	ErrMissing = errors.New("workspace_terminal_not_found")
)

type Process interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// Scope binds a terminal to the login and exact checkout that opened it.
// WorkspaceID is also the quota key across checkouts of the same workspace.
type Scope struct {
	OwnerID, LoginID, WorkspaceID string
	Target                        [32]byte
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	now      func() time.Time
	closed   bool
	stop     chan struct{}
}

type Info struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
}

type Session struct {
	Info
	scope   Scope
	expires time.Time

	mu          sync.Mutex
	process     Process
	output      []byte
	end         uint64
	activity    time.Time
	detached    time.Time
	notify      chan struct{}
	done        chan struct{}
	attached    int
	cancelStart context.CancelFunc
}

type Attachment struct {
	Session  *Session
	detached bool // guarded by Session.mu
}

func New() *Manager {
	return &Manager{sessions: make(map[string]*Session), now: time.Now}
}

func (m *Manager) runReaper(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reap()
		case <-stop:
			return
		}
	}
}

func (m *Manager) Create(ctx context.Context, scope Scope, expires time.Time, initial bool, start func(context.Context) (Process, error)) (Info, error) {
	m.reap()
	startCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Info{}, ErrMissing
	}
	var used [Limit + 1]bool
	count := 0
	for _, s := range m.sessions {
		if s.scope.WorkspaceID == scope.WorkspaceID {
			count++
			if s.scope == scope {
				if initial {
					m.mu.Unlock()
					return Info{}, ErrExists
				}
				used[s.Number] = true
			}
		}
	}
	if count >= Limit {
		m.mu.Unlock()
		return Info{}, ErrLimit
	}
	number := 1
	for used[number] {
		number++
	}
	var id [24]byte
	_, _ = rand.Read(id[:])
	s := &Session{
		Info: Info{ID: hex.EncodeToString(id[:]), Number: number}, scope: scope, expires: expires,
		notify: make(chan struct{}), done: make(chan struct{}), cancelStart: cancel,
	}
	m.sessions[s.ID] = s
	if m.stop == nil {
		m.stop = make(chan struct{})
		go m.runReaper(m.stop)
	}
	m.mu.Unlock()

	process, err := start(startCtx)
	s.mu.Lock()
	if err != nil || startCtx.Err() != nil {
		s.mu.Unlock()
		if process != nil {
			_ = process.Close()
		}
		m.remove(s.ID, s, false)
		if err != nil {
			return Info{}, err
		}
		return Info{}, ErrMissing
	}
	s.process = process
	s.activity = m.now()
	s.detached = s.activity
	s.cancelStart = nil
	s.mu.Unlock()
	go m.read(s, process)
	return s.Info, nil
}

func (m *Manager) read(s *Session, process Process) {
	defer m.remove(s.ID, s, true)
	buffer := make([]byte, frameSize)
	for {
		n, err := process.Read(buffer)
		if n != 0 {
			s.appendOutput(buffer[:n], m.now())
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) appendOutput(data []byte, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return
	default:
	}
	if s.output == nil {
		s.output = make([]byte, outputCapacity)
	}
	cursor := s.end
	s.end += uint64(len(data))
	if len(data) > outputCapacity {
		cursor = s.end - outputCapacity
		data = data[len(data)-outputCapacity:]
	}
	n := copy(s.output[cursor%outputCapacity:], data)
	copy(s.output, data[n:])
	s.activity = now
	close(s.notify)
	s.notify = make(chan struct{})
}

// Output copies at most one frame. The absolute cursor makes the buffered
// replay and live stream contiguous, even when output advances during attach.
func (s *Session) Output(cursor uint64) ([]byte, uint64, <-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	first := s.end - min(s.end, outputCapacity)
	cursor = max(first, min(cursor, s.end))
	end := min(s.end, cursor+frameSize)
	data := make([]byte, end-cursor)
	if len(data) > 0 {
		n := copy(data, s.output[cursor%outputCapacity:])
		copy(data[n:], s.output)
	}
	select {
	case <-s.done:
		return data, end, s.notify, true
	default:
		return data, end, s.notify, false
	}
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (m *Manager) List(scope Scope) []Info {
	m.reap()
	result := []Info{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.scope != scope {
			continue
		}
		s.mu.Lock()
		if s.process != nil {
			result = append(result, s.Info)
		}
		s.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result
}

func (m *Manager) Attach(scope Scope, id string) (*Attachment, error) {
	m.reap()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.scope != scope {
		return nil, ErrMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.process == nil {
		return nil, ErrMissing
	}
	s.attached++
	s.detached = time.Time{}
	return &Attachment{Session: s}, nil
}

func (m *Manager) Detach(link *Attachment) {
	s := link.Session
	s.mu.Lock()
	defer s.mu.Unlock()
	if !link.detached {
		link.detached = true
		s.attached--
		if s.attached == 0 {
			s.detached = m.now()
		}
	}
}

func (m *Manager) Input(link *Attachment, data []byte, cols, rows int) error {
	s := link.Session
	s.mu.Lock()
	if link.detached || s.process == nil || !s.expires.After(m.now()) {
		s.mu.Unlock()
		return ErrMissing
	}
	process := s.process
	if len(data) > 0 {
		s.activity = m.now()
	}
	s.mu.Unlock()
	if len(data) > 0 {
		_, err := process.Write(data)
		return err
	}
	return process.Resize(cols, rows)
}

func (m *Manager) Delete(scope Scope, id string) error {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil || s.scope != scope {
		return ErrMissing
	}
	return m.remove(id, s, false)
}

func (m *Manager) remove(id string, s *Session, flush bool) error {
	m.mu.Lock()
	if m.sessions[id] != s {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	return s.close(flush)
}

func (s *Session) close(flush bool) error {
	s.mu.Lock()
	close(s.done)
	if !flush {
		s.end, s.output = 0, nil
	}
	if s.cancelStart != nil {
		s.cancelStart()
	}
	process := s.process
	s.process = nil
	s.mu.Unlock()
	if process != nil {
		return process.Close()
	}
	return nil
}

func (m *Manager) CloseOwner(ownerID string) {
	m.closeMatching(func(s *Session) bool { return s.scope.OwnerID == ownerID })
}

func (m *Manager) CloseOtherLogins(ownerID, loginID string) {
	m.closeMatching(func(s *Session) bool { return s.scope.OwnerID == ownerID && s.scope.LoginID != loginID })
}

func (m *Manager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.stop != nil {
			close(m.stop)
		}
	}
	m.mu.Unlock()
	m.closeMatching(func(*Session) bool { return true })
}

func (m *Manager) reap() {
	now := m.now()
	m.closeMatching(func(s *Session) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.expires.After(now) || (!s.detached.IsZero() && now.Sub(s.detached) >= reconnectWindow) ||
			(!s.activity.IsZero() && now.Sub(s.activity) >= idleTimeout)
	})
}

func (m *Manager) closeMatching(match func(*Session) bool) {
	m.mu.Lock()
	var closed []*Session
	for id, s := range m.sessions {
		if match(s) {
			delete(m.sessions, id)
			closed = append(closed, s)
		}
	}
	m.mu.Unlock()
	for _, s := range closed {
		s.close(false)
	}
}
