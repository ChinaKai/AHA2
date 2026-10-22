package domain

import "time"

const (
	CollaborationBatchPending      = "pending"
	CollaborationBatchComplete     = "complete"
	CollaborationBatchFallback     = "fallback"
	CollaborationBatchConsumed     = "consumed"
	CollaborationMemberQueued      = "queued"
	CollaborationMemberRunning     = "running"
	CollaborationMemberSucceeded   = "succeeded"
	CollaborationMemberFailed      = "failed"
	CollaborationMemberInterrupted = "interrupted"
	CollaborationMemberBlocked     = "blocked"
)

type CollaborationBatch struct {
	ID                      string    `json:"id"`
	TaskID                  string    `json:"task_id"`
	RoundID                 string    `json:"round_id"`
	ParentTurnID            string    `json:"parent_turn_id"`
	IntegrationInstructions string    `json:"integration_instructions,omitempty"`
	Status                  string    `json:"status"`
	DeliveredCursor         int64     `json:"delivered_cursor"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	ConsumedAt              time.Time `json:"consumed_at,omitempty"`
}

type CollaborationMember struct {
	BatchID     string    `json:"batch_id"`
	AgentID     string    `json:"agent_id"`
	Required    bool      `json:"required"`
	ChildTurnID string    `json:"child_turn_id,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CollaborationEvent struct {
	Sequence    int64     `json:"sequence"`
	ID          string    `json:"id"`
	BatchID     string    `json:"batch_id"`
	ChildTurnID string    `json:"child_turn_id"`
	AgentID     string    `json:"agent_id"`
	Status      string    `json:"status"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

type CollaborationProgress struct {
	BatchID         string               `json:"batch_id"`
	RoundID         string               `json:"round_id"`
	ParentTurnID    string               `json:"parent_turn_id"`
	Instructions    string               `json:"integration_instructions,omitempty"`
	Status          string               `json:"status"`
	Complete        bool                 `json:"complete"`
	Results         []CollaborationEvent `json:"results"`
	PendingAgentIDs []string             `json:"pending_agent_ids"`
	NextCursor      int64                `json:"next_cursor"`
	DeliveredCursor int64                `json:"delivered_cursor"`
}
