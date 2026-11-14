package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ActivityPhase is the server-derived phase of the newest Turn in a Round.
// It exists so the Web UI can show live progress without timing a spinner or
// guessing from raw events: the AHA service owns the Turn lifecycle and the
// backend tool stream, so it is the only component that can answer this.
type ActivityPhase string

const (
	ActivityQueued         ActivityPhase = "queued"
	ActivityPreparing      ActivityPhase = "preparing"
	ActivitySessionWake    ActivityPhase = "session_wake"
	ActivityThinking       ActivityPhase = "thinking"
	ActivityToolPhase      ActivityPhase = "tool"
	ActivityWaitingBackend ActivityPhase = "waiting_backend"
	ActivityRetrying       ActivityPhase = "retrying"
	ActivityFinalizing     ActivityPhase = "finalizing"
	ActivityDone           ActivityPhase = "done"
	ActivityFailed         ActivityPhase = "failed"
)

// ActivityToolCall is the currently open backend tool call for one Turn. Only the
// tool name and its start time are kept; arguments, commands, output and any
// secret carried by those payloads never enter this structure.
type ActivityToolCall struct {
	ID    string
	Name  string
	Since time.Time
}

// TurnActivityState is the in-memory tool state for one running Turn. It is not
// persisted: after a service restart the Turn timestamps still answer the
// phase, while the tool name (which only exists in the event stream) becomes
// unavailable and the UI falls back to a generic tool label.
type TurnActivityState struct {
	OpenTools     []ActivityToolCall
	ThinkingSince time.Time
}

// TaskActivity is a lightweight snapshot shared by Task detail responses and
// SSE events. Old clients ignore the field.
type TaskActivity struct {
	Phase     ActivityPhase `json:"phase"`
	ToolName  string        `json:"tool_name,omitempty"`
	Since     time.Time     `json:"since"`
	SinceMS   int64         `json:"since_ms"`
	ElapsedMS int64         `json:"elapsed_ms"`
	Attempt   int           `json:"attempt"`
	RoundID   string        `json:"round_id"`
	TurnID    string        `json:"turn_id"`
	AgentID   string        `json:"agent_id"`
}

// NormalizeToolName keeps only a display-safe tool identifier. A backend tool
// name is a single token; if a provider ever puts arguments after whitespace,
// everything after that whitespace is dropped instead of being shown. The cap
// bounds the payload a malformed event can add to every detail/SSE response.
func NormalizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if index := strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '('
	}); index >= 0 {
		name = name[:index]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if utf8.RuneCountInString(name) > 64 {
		runes := []rune(name)
		name = string(runes[:64])
	}
	return name
}

// DeriveActivity maps a Turn's stored lifecycle timestamps plus the live tool
// snapshot to one activity phase. It is pure so the HTTP detail path and the
// SSE emit path produce exactly the same answer for the same Turn state.
func DeriveActivity(turn Turn, state *TurnActivityState, now time.Time) TaskActivity {
	activity := TaskActivity{
		Attempt: turn.Attempt,
		RoundID: turn.RoundID,
		TurnID:  turn.ID,
		AgentID: turn.AgentID,
	}
	phase := ActivityThinking
	since := fallbackTime(turn.FirstEventAt, turn.StartedAt, turn.SessionReadyAt, turn.QueuedAt)
	switch {
	case turn.Status == TurnSucceeded:
		phase, since = ActivityDone, fallbackTime(turn.FinishedAt, turn.BackendFinishedAt, turn.StartedAt)
	case turn.Status.Terminal():
		phase, since = ActivityFailed, fallbackTime(turn.FinishedAt, turn.BackendFinishedAt, turn.StartedAt)
	case !turn.BackendFinishedAt.IsZero():
		phase, since = ActivityFinalizing, turn.BackendFinishedAt
	case !turn.StalledAt.IsZero():
		phase, since = ActivityWaitingBackend, turn.StalledAt
	case turn.Attempt > 1 && turn.FirstEventAt.IsZero():
		phase, since = ActivityRetrying, fallbackTime(turn.QueuedAt, turn.PreparedAt)
	case turn.Status == TurnQueued:
		phase, since = ActivityQueued, fallbackTime(turn.QueuedAt, turn.PreparedAt)
	case turn.Status == TurnPreparing:
		phase, since = ActivityPreparing, fallbackTime(turn.PreparedAt, turn.QueuedAt)
	case turn.Status == TurnStarting:
		phase, since = ActivitySessionWake, fallbackTime(turn.ContextReadyAt, turn.PreparedAt, turn.QueuedAt)
	case state != nil && len(state.OpenTools) > 0:
		phase, since = ActivityToolPhase, fallbackTime(state.OpenTools[len(state.OpenTools)-1].Since, turn.LastActivityAt, turn.StartedAt)
		activity.ToolName = state.OpenTools[len(state.OpenTools)-1].Name
	default:
		phase = ActivityThinking
		if state != nil {
			since = fallbackTime(state.ThinkingSince, since)
		}
	}
	activity.Phase = phase
	if !since.IsZero() {
		activity.Since = since.UTC()
		activity.SinceMS = activity.Since.UnixMilli()
		activity.ElapsedMS = now.Sub(activity.Since).Milliseconds()
		if activity.ElapsedMS < 0 {
			activity.ElapsedMS = 0
		}
	}
	return activity
}

func fallbackTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
