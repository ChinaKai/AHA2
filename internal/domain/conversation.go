package domain

import "time"

type RoundStatus string

const (
	RoundRunning     RoundStatus = "running"
	RoundWaiting     RoundStatus = "waiting"
	RoundCompleted   RoundStatus = "completed"
	RoundFailed      RoundStatus = "failed"
	RoundInterrupted RoundStatus = "interrupted"
)

func (status RoundStatus) Terminal() bool {
	return status == RoundCompleted || status == RoundFailed || status == RoundInterrupted
}

type TaskRound struct {
	ID             string      `json:"id"`
	TaskID         string      `json:"task_id"`
	Sequence       int         `json:"sequence"`
	InputMessageID string      `json:"input_message_id"`
	Status         RoundStatus `json:"status"`
	CreatedAt      time.Time   `json:"created_at"`
	CreatedAtMS    int64       `json:"created_at_ms"`
	StartedAt      time.Time   `json:"started_at,omitempty"`
	StartedAtMS    int64       `json:"started_at_ms,omitempty"`
	FinishedAt     time.Time   `json:"finished_at,omitempty"`
	FinishedAtMS   int64       `json:"finished_at_ms,omitempty"`
	ElapsedMS      int64       `json:"elapsed_ms"`
}

type ConversationItem struct {
	Sequence      int64          `json:"sequence"`
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	RoundID       string         `json:"round_id,omitempty"`
	TurnID        string         `json:"turn_id,omitempty"`
	AgentID       string         `json:"agent_id,omitempty"`
	StreamAgentID string         `json:"stream_agent_id,omitempty"`
	FromAgentID   string         `json:"from_agent_id,omitempty"`
	ToAgentID     string         `json:"to_agent_id,omitempty"`
	RouteKind     string         `json:"route_kind,omitempty"`
	Category      string         `json:"category"`
	Kind          string         `json:"kind"`
	Summary       string         `json:"summary"`
	Payload       map[string]any `json:"payload,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

type ConversationPage struct {
	Items      []ConversationItem `json:"items"`
	HasMore    bool               `json:"has_more"`
	NextBefore int64              `json:"next_before,omitempty"`
	Latest     int64              `json:"latest_sequence,omitempty"`
}

type TaskAgent struct {
	TaskID                  string    `json:"task_id"`
	AgentID                 string    `json:"agent_id"`
	Role                    string    `json:"role"`
	Status                  string    `json:"status"`
	Title                   string    `json:"title"`
	RuntimeConfigSnapshotID string    `json:"runtime_config_snapshot_id"`
	InheritMain             bool      `json:"inherit_main"`
	Backend                 string    `json:"backend,omitempty"`
	ModelSource             string    `json:"model_source,omitempty"`
	ModelID                 string    `json:"model_id,omitempty"`
	ModelName               string    `json:"model_name,omitempty"`
	WireModel               string    `json:"wire_model,omitempty"`
	CodexAccountID          string    `json:"codex_account_id,omitempty"`
	ReasoningEffort         string    `json:"reasoning_effort,omitempty"`
	Filesystem              string    `json:"filesystem,omitempty"`
	Approval                string    `json:"approval,omitempty"`
	ProxyEnabled            bool      `json:"proxy_enabled"`
	UnreadCount             int       `json:"unread_count"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type AgentInboxItem struct {
	Sequence      int64          `json:"sequence"`
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	RoundID       string         `json:"round_id"`
	TargetAgentID string         `json:"target_agent_id"`
	SourceAgentID string         `json:"source_agent_id"`
	SourceKind    string         `json:"source_kind"`
	SourceTurnID  string         `json:"source_turn_id,omitempty"`
	MessageID     string         `json:"message_id,omitempty"`
	Content       string         `json:"content"`
	Payload       map[string]any `json:"payload,omitempty"`
	Status        string         `json:"status"`
	BatchID       string         `json:"batch_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	ClaimedAt     time.Time      `json:"claimed_at,omitempty"`
	ProcessedAt   time.Time      `json:"processed_at,omitempty"`
}

type AgentSessionHandoff struct {
	ID                     string    `json:"id"`
	TaskID                 string    `json:"task_id"`
	AgentID                string    `json:"agent_id"`
	SourceBackendSessionID string    `json:"source_backend_session_id"`
	Mode                   string    `json:"mode"`
	Summary                string    `json:"summary"`
	Status                 string    `json:"status"`
	CreatedAt              time.Time `json:"created_at"`
	ConsumedAt             time.Time `json:"consumed_at,omitempty"`
}
