package domain

import (
	"encoding/json"
	"time"
)

type SyncSettings struct {
	Scope           string    `json:"scope"`
	Enabled         bool      `json:"enabled"`
	Endpoint        string    `json:"endpoint"`
	DeviceID        string    `json:"device_id"`
	DeviceName      string    `json:"device_name"`
	IntervalSeconds int       `json:"interval_seconds"`
	ProviderIDs     []string  `json:"provider_ids,omitempty"`
	EnvGroupIDs     []string  `json:"env_group_ids,omitempty"`
	CodexAccountIDs []string  `json:"codex_account_ids,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SyncState struct {
	Scope          string    `json:"scope"`
	Cursor         string    `json:"cursor"`
	LastPushAt     time.Time `json:"last_push_at"`
	LastPullAt     time.Time `json:"last_pull_at"`
	LastError      string    `json:"last_error"`
	ReplayRequired bool      `json:"-"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SyncObject struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	Operation      string          `json:"operation"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	BaseVersion    string          `json:"base_version,omitempty"`
	RemoteVersion  string          `json:"remote_version,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
	EventID        string          `json:"event_id,omitempty"`
}

type SyncOutboxItem struct {
	ID            string     `json:"id"`
	Scope         string     `json:"scope"`
	Object        SyncObject `json:"object"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     string     `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type SyncConflict struct {
	ID            string          `json:"id"`
	Scope         string          `json:"scope"`
	ObjectType    string          `json:"object_type"`
	ObjectID      string          `json:"object_id"`
	LocalPayload  json.RawMessage `json:"local_payload"`
	RemotePayload json.RawMessage `json:"remote_payload"`
	LocalVersion  string          `json:"local_version"`
	RemoteVersion string          `json:"remote_version"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	ResolvedAt    time.Time       `json:"resolved_at"`
}
