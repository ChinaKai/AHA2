package domain

import "time"

// TaskPermissionRequest is an owner decision requested by a group participant
// before the Task performs an action that needs explicit permission.
type TaskPermissionRequest struct {
	ID                      string    `json:"id"`
	TaskID                  string    `json:"task_id"`
	TurnID                  string    `json:"turn_id,omitempty"`
	InboundReceiptID        string    `json:"inbound_receipt_id,omitempty"`
	ChannelInstanceID       string    `json:"channel_instance_id,omitempty"`
	ChannelConversationID   string    `json:"channel_conversation_id,omitempty"`
	ChannelActionID         string    `json:"channel_action_id,omitempty"`
	ApplicantIdentityLinkID string    `json:"applicant_identity_link_id,omitempty"`
	ApplicantName           string    `json:"applicant_name,omitempty"`
	ApplicantIsBot          bool      `json:"applicant_is_bot,omitempty"`
	Action                  string    `json:"action"`
	Scope                   string    `json:"scope,omitempty"`
	Reason                  string    `json:"reason,omitempty"`
	Risk                    string    `json:"risk,omitempty"`
	Status                  string    `json:"status"`
	DeciderIdentityLinkID   string    `json:"decider_identity_link_id,omitempty"`
	DeciderName             string    `json:"decider_name,omitempty"`
	DecisionReason          string    `json:"decision_reason,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	DecidedAt               time.Time `json:"decided_at,omitempty"`
	ExpiresAt               time.Time `json:"expires_at"`
}
