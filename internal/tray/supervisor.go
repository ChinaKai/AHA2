package tray

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type Child interface {
	Wait() error
	Stop(time.Duration) error
}

type Launcher interface {
	Start(Command) (Child, error)
}

type RetryPolicy struct {
	Delays    []time.Duration
	StopGrace time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Delays: []time.Duration{time.Second, 3 * time.Second, 10 * time.Second}, StopGrace: 10 * time.Second}
}

type Snapshot struct {
	State      State
	RetryCount int
	LastError  string
}

type supervisorEvent struct {
	kind       Event
	child      Child
	generation uint64
	err        error
}

type Supervisor struct {
	launcher Launcher
	command  Command
	policy   RetryPolicy
	events   chan supervisorEvent
	done     chan struct{}

	mu       sync.RWMutex
	snapshot Snapshot
}

func NewSupervisor(launcher Launcher, command Command, policy RetryPolicy) *Supervisor {
	if policy.StopGrace <= 0 {
		policy.StopGrace = 10 * time.Second
	}
	return &Supervisor{
		launcher: launcher, command: command, policy: policy,
		events: make(chan supervisorEvent, 16), done: make(chan struct{}),
		snapshot: Snapshot{State: StateStopped},
	}
}

func (s *Supervisor) Run()                  { go s.loop() }
func (s *Supervisor) Done() <-chan struct{} { return s.done }

func (s *Supervisor) Start() error   { return s.send(EventUserStart) }
func (s *Supervisor) Stop() error    { return s.send(EventUserStop) }
func (s *Supervisor) Restart() error { return s.send(EventUserRestart) }
func (s *Supervisor) Exit() error    { return s.send(EventUserExit) }

func (s *Supervisor) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *Supervisor) send(kind Event) error {
	select {
	case <-s.done:
		return errors.New("tray supervisor has exited")
	default:
	}
	select {
	case <-s.done:
		return errors.New("tray supervisor has exited")
	case s.events <- supervisorEvent{kind: kind}:
		return nil
	}
}

func (s *Supervisor) loop() {
	machine := NewMachine(len(s.policy.Delays))
	var child Child
	var generation uint64
	defer close(s.done)
	s.publish(machine, nil)
	for event := range s.events {
		if event.generation != 0 && event.generation != generation {
			continue
		}
		if event.kind == EventStartSucceeded {
			child = event.child
			currentGeneration := generation
			go func(current Child) {
				err := current.Wait()
				s.deliver(supervisorEvent{kind: EventProcessExited, generation: currentGeneration, err: err})
			}(child)
		}
		if event.kind == EventProcessExited {
			child = nil
		}
		effects := machine.Apply(event.kind)
		s.publish(machine, event.err)
		for _, effect := range effects {
			switch effect {
			case EffectStartProcess:
				generation++
				currentGeneration := generation
				go func() {
					started, err := s.launcher.Start(s.command)
					kind := EventStartSucceeded
					if err != nil {
						kind = EventStartFailed
					}
					s.deliver(supervisorEvent{kind: kind, child: started, generation: currentGeneration, err: err})
				}()
			case EffectStopProcess:
				if child != nil {
					go func(current Child) {
						if err := current.Stop(s.policy.StopGrace); err != nil {
							s.recordError(fmt.Errorf("stop AHA2: %w", err))
						}
					}(child)
				}
			case EffectScheduleRetry:
				delay := s.policy.Delays[machine.RetryCount-1]
				time.AfterFunc(delay, func() { s.deliver(supervisorEvent{kind: EventRetryDue}) })
			case EffectExit:
				return
			}
		}
	}
}

func (s *Supervisor) deliver(event supervisorEvent) {
	select {
	case <-s.done:
	case s.events <- event:
	}
}

func (s *Supervisor) publish(machine Machine, eventErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.State = machine.State
	s.snapshot.RetryCount = machine.RetryCount
	if machine.State == StateStopped || machine.State == StateExited {
		s.snapshot.LastError = ""
	} else if eventErr != nil {
		s.snapshot.LastError = eventErr.Error()
	} else if machine.State == StateRunning {
		s.snapshot.LastError = ""
	}
}

func (s *Supervisor) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.LastError = err.Error()
}
