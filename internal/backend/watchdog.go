package backend

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	defaultBackendTurnTimeout = 10 * time.Hour
	defaultBackendIdleTimeout = 10 * time.Minute
	defaultBackendIdleWarning = 90 * time.Second
	defaultBackendHeartbeat   = time.Minute
)

var ErrBackendIdleTimeout = errors.New("backend produced no activity before the idle timeout")

// ErrCodexIdleTimeout is kept as an alias for callers compiled against the
// original Codex-only watchdog. The watchdog now applies to every backend.
var ErrCodexIdleTimeout = ErrBackendIdleTimeout

func backendWatchdogDurations(warning, timeout, heartbeat time.Duration) (time.Duration, time.Duration, time.Duration) {
	if timeout <= 0 {
		timeout = defaultBackendIdleTimeout
	}
	if warning <= 0 {
		warning = defaultBackendIdleWarning
	}
	if warning >= timeout {
		warning = timeout / 2
	}
	if warning <= 0 {
		warning = time.Millisecond
	}
	if heartbeat <= 0 {
		heartbeat = defaultBackendHeartbeat
	}
	if heartbeat >= timeout {
		heartbeat = timeout / 2
	}
	if heartbeat <= 0 {
		heartbeat = time.Millisecond
	}
	return warning, timeout, heartbeat
}

func monitorBackendActivity(
	ctx context.Context,
	warning, timeout, heartbeat time.Duration,
	activity <-chan string,
	emit func(Event),
	cancel context.CancelCauseFunc,
	done chan<- struct{},
) {
	defer close(done)
	interval := warning / 4
	if interval <= 0 || interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastActivity := time.Now()
	lastEvent := "backend_start"
	stalled := false
	lastHeartbeat := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case eventType := <-activity:
			eventType = strings.TrimSpace(eventType)
			if eventType == "" {
				continue
			}
			lastEvent = eventType
			if !backendEventResetsIdle(eventType) {
				continue
			}
			now := time.Now()
			if stalled {
				emit(Event{Type: "agent_resumed", Data: map[string]any{"message": "Backend activity resumed", "last_event_type": lastEvent}})
			}
			lastActivity, stalled, lastHeartbeat = now, false, time.Time{}
		case now := <-ticker.C:
			idle := now.Sub(lastActivity)
			if idle >= timeout {
				emit(Event{Type: "agent_idle_timeout", Data: map[string]any{"message": "Backend idle timeout", "idle_ms": idle.Milliseconds(), "last_event_type": lastEvent}})
				cancel(ErrBackendIdleTimeout)
				return
			}
			if idle < warning {
				continue
			}
			if !stalled {
				stalled, lastHeartbeat = true, now
				emit(Event{Type: "agent_stalled", Data: map[string]any{"message": "Backend has produced no activity", "idle_ms": idle.Milliseconds(), "last_event_type": lastEvent}})
				continue
			}
			if now.Sub(lastHeartbeat) >= heartbeat {
				lastHeartbeat = now
				emit(Event{Type: "agent_heartbeat", Data: map[string]any{"message": "Backend is still stalled", "idle_ms": idle.Milliseconds(), "last_event_type": lastEvent}})
			}
		}
	}
}

func backendEventResetsIdle(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "", "agent_error", "agent_stalled", "agent_heartbeat", "agent_idle_timeout":
		return false
	default:
		return true
	}
}
