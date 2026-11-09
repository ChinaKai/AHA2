package domain

import "time"

// OwnerAssistant is the device-local assistant behind the Web pet. It is not a
// Task: it has no workspace or shell, and only reads control-plane state in P0.
type OwnerAssistant struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ModelID         string   `json:"model_id"`
	Character       string   `json:"character"`
	PetEnabled      bool     `json:"pet_enabled"`
	BrainModelID    string   `json:"brain_model_id"`
	PetActions      []string `json:"pet_actions"`
	PetIdentity     string   `json:"pet_identity"`
	PetPrinciples   string   `json:"pet_principles"`
	PetIdleMinutes  int      `json:"pet_idle_minutes"`
	PetQuietEnabled bool     `json:"pet_quiet_enabled"`
	PetQuietStart   int      `json:"pet_quiet_start"`
	PetQuietEnd     int      `json:"pet_quiet_end"`
	PetHourlyLimit  int      `json:"pet_hourly_limit"`
	PetTaskID       string   `json:"pet_task_id"`

	// Owner-delegation permissions. The pet reads AHA2 through its own tools;
	// these switches decide how much of the owner's view it may use. Mutating
	// switches are reserved for the phase that adds owner-delegate write tools.
	PetAllowViewTasks        bool `json:"pet_allow_view_tasks"`
	PetAllowCreateTasks      bool `json:"pet_allow_create_tasks"`
	PetAllowCreateWorkspaces bool `json:"pet_allow_create_workspaces"`
	PetAllowMessageTasks     bool `json:"pet_allow_message_tasks"`
	PetAllowManageModels     bool `json:"pet_allow_manage_models"`
	PetAllowManageKnowledge  bool `json:"pet_allow_manage_knowledge"`
	PetAllowManageChannels   bool `json:"pet_allow_manage_channels"`
	PetAllowManageSystem     bool `json:"pet_allow_manage_system"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PetApproval is a pet-initiated request for an owner decision. It is persisted
// so the workbench can show pending and resolved requests even when the browser
// that saw the original Turn is gone.
type PetApproval struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"task_id"`
	Kind           string    `json:"kind"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	Risk           string    `json:"risk"`
	Reason         string    `json:"reason,omitempty"`
	Payload        string    `json:"payload,omitempty"`
	Status         string    `json:"status"`
	Resolution     string    `json:"resolution,omitempty"`
	DecisionReason string    `json:"decision_reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	ResolvedAt     time.Time `json:"resolved_at,omitempty"`
}

// PetApprovalCount is one kind/status bucket of the approval table. The
// workbench returns these groups so the browser can show an exact total for
// the kind/status filter it is currently displaying.
type PetApprovalCount struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

const (
	AssistantRoleUser      = "user"
	AssistantRoleAssistant = "assistant"
)

type AssistantMessage struct {
	ID          string    `json:"id"`
	AssistantID string    `json:"assistant_id"`
	Role        string    `json:"role"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

// AssistantHint is a read-only nudge derived from live control-plane state. It
// is not persisted; dismissals are stored separately for a short cooldown.
type AssistantHint struct {
	Kind       string `json:"kind"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Priority   int    `json:"priority"`
}

// PetTaskWatch is a Task the pet wants status notifications for. The watch is
// created automatically when the pet creates, starts, or messages a Task, and
// can also be managed manually.
type PetTaskWatch struct {
	TaskID             string    `json:"task_id"`
	Reason             string    `json:"reason"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	LastStatus         string    `json:"last_status,omitempty"`
	LastTurnID         string    `json:"last_turn_id,omitempty"`
	LastNotifiedStatus string    `json:"last_notified_status,omitempty"`
	LastNotifiedTurnID string    `json:"last_notified_turn_id,omitempty"`
	Ignored            bool      `json:"ignored,omitempty"`
	IgnoredAt          time.Time `json:"ignored_at,omitempty"`
}

type PetEvent struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	DeviceID string    `json:"device_id"`
	Actor    string    `json:"actor"`
	Kind     string    `json:"kind"`
	Page     string    `json:"page"`
	Detail   string    `json:"detail"`
	Status   string    `json:"status"`
}
