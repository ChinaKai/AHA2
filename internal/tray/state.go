package tray

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateBackoff  State = "backoff"
	StateFailed   State = "failed"
	StateExited   State = "exited"
)

type Event int

const (
	EventUserStart Event = iota
	EventUserStop
	EventUserRestart
	EventUserExit
	EventStartSucceeded
	EventStartFailed
	EventProcessExited
	EventRetryDue
)

type Effect int

const (
	EffectStartProcess Effect = iota + 1
	EffectStopProcess
	EffectScheduleRetry
	EffectExit
)

type Machine struct {
	State            State
	DesiredRunning   bool
	RestartAfterStop bool
	ExitRequested    bool
	RetryCount       int
	MaxRetries       int
}

func NewMachine(maxRetries int) Machine {
	if maxRetries < 0 {
		maxRetries = 0
	}
	return Machine{State: StateStopped, MaxRetries: maxRetries}
}

func (m *Machine) Apply(event Event) []Effect {
	switch event {
	case EventUserStart:
		if m.State == StateStopped || m.State == StateFailed {
			m.DesiredRunning, m.ExitRequested, m.RestartAfterStop, m.RetryCount = true, false, false, 0
			m.State = StateStarting
			return []Effect{EffectStartProcess}
		}
	case EventUserStop:
		m.DesiredRunning, m.RestartAfterStop = false, false
		switch m.State {
		case StateRunning:
			m.State = StateStopping
			return []Effect{EffectStopProcess}
		case StateStarting:
			m.State = StateStopping
		case StateBackoff, StateFailed:
			m.State = StateStopped
		}
	case EventUserRestart:
		m.DesiredRunning, m.ExitRequested, m.RetryCount = true, false, 0
		switch m.State {
		case StateRunning:
			m.RestartAfterStop, m.State = true, StateStopping
			return []Effect{EffectStopProcess}
		case StateStarting, StateStopping:
			m.RestartAfterStop, m.State = true, StateStopping
		default:
			m.RestartAfterStop, m.State = false, StateStarting
			return []Effect{EffectStartProcess}
		}
	case EventUserExit:
		m.DesiredRunning, m.RestartAfterStop, m.ExitRequested = false, false, true
		switch m.State {
		case StateRunning:
			m.State = StateStopping
			return []Effect{EffectStopProcess}
		case StateStarting:
			m.State = StateStopping
		case StateStopping:
		default:
			m.State = StateExited
			return []Effect{EffectExit}
		}
	case EventStartSucceeded:
		if m.State == StateStarting {
			m.State = StateRunning
		} else if m.State == StateStopping {
			return []Effect{EffectStopProcess}
		}
	case EventStartFailed:
		if m.ExitRequested {
			m.State = StateExited
			return []Effect{EffectExit}
		}
		if !m.DesiredRunning {
			m.State = StateStopped
			return nil
		}
		return m.retryOrFail()
	case EventProcessExited:
		if m.ExitRequested {
			m.State = StateExited
			return []Effect{EffectExit}
		}
		if m.State == StateStopping {
			if m.RestartAfterStop {
				m.RestartAfterStop, m.State = false, StateStarting
				return []Effect{EffectStartProcess}
			}
			m.State = StateStopped
			return nil
		}
		if m.State == StateRunning || m.State == StateStarting {
			return m.retryOrFail()
		}
	case EventRetryDue:
		if m.State == StateBackoff && m.DesiredRunning {
			m.State = StateStarting
			return []Effect{EffectStartProcess}
		}
	}
	return nil
}

func (m *Machine) retryOrFail() []Effect {
	if m.RetryCount >= m.MaxRetries {
		m.State = StateFailed
		return nil
	}
	m.RetryCount++
	m.State = StateBackoff
	return []Effect{EffectScheduleRetry}
}
