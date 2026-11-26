package domain

import "time"

const (
	TaskLineMaxActive    = 8
	TaskLineMaxTextRunes = 200
	TaskLineIDPattern    = "^[a-z0-9][a-z0-9_-]{0,31}$"

	TaskLineOwnerSelf  = "self"
	TaskLineOwnerOwner = "owner"
	TaskLineOwnerOther = "other"

	TaskLineOpen    = "open"
	TaskLineWaiting = "waiting"
	TaskLineBlocked = "blocked"
	TaskLineClosed  = "closed"
)

// TaskLine is one independently actionable part of a Task's state card.
type TaskLine struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Owner     string     `json:"owner"`
	Who       string     `json:"who,omitempty"`
	State     string     `json:"state"`
	Step      string     `json:"step,omitempty"`
	Blocker   string     `json:"blocker,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
	OpenedAt  time.Time  `json:"opened_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// TaskLinePatch leaves omitted fields unchanged. Only the server sets timestamps.
type TaskLinePatch struct {
	ID      string  `json:"id"`
	Title   *string `json:"title,omitempty"`
	Owner   *string `json:"owner,omitempty"`
	Who     *string `json:"who,omitempty"`
	State   *string `json:"state,omitempty"`
	Step    *string `json:"step,omitempty"`
	Blocker *string `json:"blocker,omitempty"`
}

type TaskLineEvent struct {
	Sequence  int64     `json:"sequence"`
	TaskID    string    `json:"task_id"`
	LineID    string    `json:"line_id"`
	Kind      string    `json:"kind"`
	RoundID   string    `json:"round_id,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Before    *TaskLine `json:"before,omitempty"`
	After     TaskLine  `json:"after"`
}
