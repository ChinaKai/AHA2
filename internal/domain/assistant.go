package domain

import "time"

// OwnerAssistant is the device-local assistant behind the Web pet. It is not a
// Task: it has no workspace or shell, and only reads control-plane state in P0.
type OwnerAssistant struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	ModelID         string    `json:"model_id"`
	Character       string    `json:"character"`
	PetEnabled      bool      `json:"pet_enabled"`
	BrainModelID    string    `json:"brain_model_id"`
	PetActions      []string  `json:"pet_actions"`
	PetIdentity     string    `json:"pet_identity"`
	PetPrinciples   string    `json:"pet_principles"`
	PetIdleMinutes  int       `json:"pet_idle_minutes"`
	PetQuietEnabled bool      `json:"pet_quiet_enabled"`
	PetQuietStart   int       `json:"pet_quiet_start"`
	PetQuietEnd     int       `json:"pet_quiet_end"`
	PetHourlyLimit  int       `json:"pet_hourly_limit"`
	PetTaskID       string    `json:"pet_task_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
