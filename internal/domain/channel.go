package domain

import "time"

const (
	ChannelEndpointAssistantDM       = "assistant_dm"
	ChannelEndpointGroupDigitalHuman = "group_digital_human"
)

type ChannelPlugin struct {
	ID               string         `json:"id"`
	ProviderKey      string         `json:"provider_key"`
	DisplayName      string         `json:"display_name"`
	ManifestVersion  int            `json:"manifest_version"`
	PackageVersion   string         `json:"package_version"`
	ProtocolMin      int            `json:"protocol_min"`
	ProtocolMax      int            `json:"protocol_max"`
	ExecutablePath   string         `json:"-"`
	ExecutableSHA256 string         `json:"executable_sha256,omitempty"`
	Manifest         map[string]any `json:"manifest,omitempty"`
	InstallState     string         `json:"install_state"`
	Enabled          bool           `json:"enabled"`
	Revision         int            `json:"revision"`
	LastError        string         `json:"last_error,omitempty"`
	DiscoveredAt     time.Time      `json:"discovered_at,omitempty"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Available        bool           `json:"available"`
}

type ChannelInstance struct {
	ID                    string         `json:"id"`
	PluginID              string         `json:"plugin_id"`
	ProviderKey           string         `json:"provider_key,omitempty"`
	OwnerID               string         `json:"owner_id"`
	RuntimeDeviceID       string         `json:"runtime_device_id"`
	Name                  string         `json:"name"`
	Status                string         `json:"status"`
	EffectiveAvailability string         `json:"effective_availability"`
	Revision              int            `json:"revision"`
	AppID                 string         `json:"app_id,omitempty"`
	ProviderTenantID      string         `json:"provider_tenant_id,omitempty"`
	CredentialRef         string         `json:"-"`
	CredentialConfigured  bool           `json:"credential_configured"`
	OwnerBound            bool           `json:"owner_bound"`
	HostProjectID         string         `json:"host_project_id"`
	HostWorkspaceID       string         `json:"host_workspace_id"`
	Config                map[string]any `json:"config,omitempty"`
	LastSeenAt            time.Time      `json:"last_seen_at,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type ChannelEndpoint struct {
	ID         string         `json:"id"`
	InstanceID string         `json:"instance_id"`
	Kind       string         `json:"kind"`
	Enabled    bool           `json:"enabled"`
	Config     map[string]any `json:"config,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type ChannelIdentityLink struct {
	ID               string    `json:"id"`
	InstanceID       string    `json:"instance_id"`
	OwnerID          string    `json:"owner_id,omitempty"`
	ProviderTenantID string    `json:"provider_tenant_id,omitempty"`
	ExternalUserID   string    `json:"-"`
	UnionID          string    `json:"-"`
	Role             string    `json:"role"`
	DisplayName      string    `json:"display_name,omitempty"`
	Status           string    `json:"status"`
	LinkedAt         time.Time `json:"linked_at"`
	RevokedAt        time.Time `json:"revoked_at,omitempty"`
}

type ChannelConversation struct {
	ID                  string    `json:"id"`
	InstanceID          string    `json:"instance_id"`
	EndpointID          string    `json:"endpoint_id"`
	ScopeKeyVersion     int       `json:"scope_key_version"`
	ScopeKey            string    `json:"-"`
	ExternalChatID      string    `json:"-"`
	ExternalSenderID    string    `json:"-"`
	OwnerIdentityLinkID string    `json:"owner_identity_link_id,omitempty"`
	HostTaskID          string    `json:"host_task_id"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ChannelSession struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Generation     int       `json:"generation"`
	Mode           string    `json:"mode"`
	Status         string    `json:"status"`
	InboundCursor  int64     `json:"inbound_cursor"`
	OutboundCursor int64     `json:"outbound_cursor"`
	StartedAt      time.Time `json:"started_at"`
	ClosedAt       time.Time `json:"closed_at,omitempty"`
}

type ChannelTaskRoute struct {
	ID              string    `json:"id"`
	InstanceID      string    `json:"instance_id"`
	ConversationID  string    `json:"conversation_id"`
	TargetTaskID    string    `json:"target_task_id"`
	State           string    `json:"state"`
	Revision        int       `json:"revision"`
	PendingActionID string    `json:"pending_action_id,omitempty"`
	ActivatedAt     time.Time `json:"activated_at,omitempty"`
	ExitedAt        time.Time `json:"exited_at,omitempty"`
	ExitReason      string    `json:"exit_reason,omitempty"`
}

type ChannelSubscription struct {
	ID             string         `json:"id"`
	InstanceID     string         `json:"instance_id"`
	ConversationID string         `json:"conversation_id"`
	RouteID        string         `json:"route_id,omitempty"`
	SourceTaskID   string         `json:"source_task_id,omitempty"`
	Kind           string         `json:"kind"`
	FilterVersion  int            `json:"filter_version"`
	Filter         map[string]any `json:"filter,omitempty"`
	SourceCursor   int64          `json:"source_cursor"`
	State          string         `json:"state"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type ChannelPendingAction struct {
	ID                  string         `json:"id"`
	InstanceID          string         `json:"instance_id"`
	ConversationID      string         `json:"conversation_id"`
	ActorIdentityLinkID string         `json:"actor_identity_link_id"`
	Operation           string         `json:"operation"`
	TargetType          string         `json:"target_type"`
	TargetID            string         `json:"target_id,omitempty"`
	Intent              map[string]any `json:"intent"`
	Preview             map[string]any `json:"preview"`
	Precondition        map[string]any `json:"precondition"`
	PreconditionHash    string         `json:"-"`
	Status              string         `json:"status"`
	ProviderMessageID   string         `json:"-"`
	ExpiresAt           time.Time      `json:"expires_at"`
	ConsumedAt          time.Time      `json:"consumed_at,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type ChannelKnowledgePolicy struct {
	ID                string    `json:"id"`
	InstanceID        string    `json:"instance_id"`
	EndpointID        string    `json:"endpoint_id"`
	FixedIndexEntryID string    `json:"fixed_index_entry_id"`
	DefaultVisibility string    `json:"default_visibility"`
	Revision          int       `json:"revision"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ChannelKnowledgeGrant struct {
	ID               string    `json:"id"`
	PolicyID         string    `json:"policy_id"`
	KnowledgeEntryID string    `json:"knowledge_entry_id"`
	GrantScope       string    `json:"grant_scope"`
	GrantedByOwnerID string    `json:"granted_by_owner_id"`
	CreatedAt        time.Time `json:"created_at"`
	RevokedAt        time.Time `json:"revoked_at,omitempty"`
}

type ChannelKnowledgeRecord struct {
	ID                      string         `json:"id"`
	InstanceID              string         `json:"instance_id"`
	ConversationID          string         `json:"conversation_id"`
	KnowledgeEntryID        string         `json:"knowledge_entry_id"`
	RequesterIdentityLinkID string         `json:"requester_identity_link_id"`
	Question                string         `json:"question"`
	Answer                  string         `json:"answer"`
	Source                  map[string]any `json:"source"`
	Visibility              string         `json:"visibility"`
	AuthorityStatus         string         `json:"authority_status"`
	OccurredAt              time.Time      `json:"occurred_at"`
	CreatedAt               time.Time      `json:"created_at"`
}

type ChannelInboxReceipt struct {
	ID                string         `json:"id"`
	InstanceID        string         `json:"instance_id"`
	ExternalEventID   string         `json:"external_event_id"`
	EventType         string         `json:"event_type"`
	PayloadDigest     string         `json:"payload_digest"`
	NormalizedPayload map[string]any `json:"normalized_payload"`
	State             string         `json:"state"`
	LeaseID           string         `json:"-"`
	LeaseUntil        time.Time      `json:"lease_until,omitempty"`
	ConversationID    string         `json:"conversation_id,omitempty"`
	AHAMessageID      string         `json:"aha_message_id,omitempty"`
	Outcome           string         `json:"outcome,omitempty"`
	OccurredAt        time.Time      `json:"occurred_at"`
	ReceivedAt        time.Time      `json:"received_at"`
	ProcessedAt       time.Time      `json:"processed_at,omitempty"`
}

type ChannelInboundEnvelope struct {
	SchemaVersion     int              `json:"schema_version"`
	RequestID         string           `json:"request_id"`
	InstanceID        string           `json:"instance_id"`
	ExternalEventID   string           `json:"external_event_id"`
	EventType         string           `json:"event_type"`
	OccurredAt        time.Time        `json:"occurred_at"`
	ChatType          string           `json:"chat_type"`
	ExternalChatID    string           `json:"external_chat_id"`
	ExternalSenderID  string           `json:"external_sender_id"`
	ExternalMessageID string           `json:"external_message_id"`
	Content           string           `json:"content"`
	ChatDisplayName   string           `json:"chat_display_name,omitempty"`
	SenderDisplayName string           `json:"sender_display_name,omitempty"`
	MentionedBot      bool             `json:"mentioned_bot"`
	Resources         []map[string]any `json:"resources,omitempty"`
	CardAction        map[string]any   `json:"card_action,omitempty"`
	MenuAction        map[string]any   `json:"menu_action,omitempty"`
}

type ChannelSourceEvent struct {
	Sequence           int64          `json:"sequence"`
	ID                 string         `json:"id"`
	SourceKey          string         `json:"source_key"`
	SourceRevision     int            `json:"source_revision"`
	TaskID             string         `json:"task_id"`
	RoundID            string         `json:"round_id,omitempty"`
	TurnID             string         `json:"turn_id,omitempty"`
	ConversationItemID string         `json:"conversation_item_id,omitempty"`
	EventClass         string         `json:"event_class"`
	EventType          string         `json:"event_type"`
	SemanticPayload    map[string]any `json:"semantic_payload"`
	OccurredAt         time.Time      `json:"occurred_at"`
}

type ChannelDelivery struct {
	ID                  string            `json:"id"`
	InstanceID          string            `json:"instance_id"`
	ConversationID      string            `json:"conversation_id"`
	SubscriptionID      string            `json:"subscription_id,omitempty"`
	SourceEventSequence int64             `json:"source_event_sequence"`
	StreamSequence      int64             `json:"stream_sequence"`
	ReplayGeneration    int               `json:"replay_generation"`
	ReplayOfID          string            `json:"replay_of_id,omitempty"`
	IdempotencyKey      string            `json:"idempotency_key"`
	CoalesceKey         string            `json:"coalesce_key,omitempty"`
	PayloadVersion      int               `json:"payload_version"`
	SemanticPayload     map[string]any    `json:"semantic_payload"`
	Target              map[string]string `json:"target,omitempty"`
	State               string            `json:"state"`
	Attempts            int               `json:"attempts"`
	FirstAttemptAt      time.Time         `json:"first_attempt_at,omitempty"`
	AvailableAt         time.Time         `json:"available_at"`
	LeaseID             string            `json:"lease_id,omitempty"`
	LeaseUntil          time.Time         `json:"lease_until,omitempty"`
	ProviderMessageID   string            `json:"provider_message_id,omitempty"`
	LastErrorCode       string            `json:"last_error_code,omitempty"`
	OutcomeCertainty    string            `json:"outcome_certainty,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	DeliveredAt         time.Time         `json:"delivered_at,omitempty"`
}

type ChannelDeliveryAttempt struct {
	ID                string    `json:"id"`
	DeliveryID        string    `json:"delivery_id"`
	AttemptNo         int       `json:"attempt_no"`
	LeaseID           string    `json:"-"`
	Outcome           string    `json:"outcome"`
	ErrorCode         string    `json:"error_code,omitempty"`
	RetryAfterMS      int64     `json:"retry_after_ms,omitempty"`
	ProviderRequestID string    `json:"provider_request_id,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
}

type ChannelServiceCapability struct {
	ID             string    `json:"id"`
	PluginID       string    `json:"plugin_id"`
	InstanceID     string    `json:"instance_id"`
	TokenHash      string    `json:"-"`
	TokenSecretRef string    `json:"-"`
	Scopes         []string  `json:"scopes"`
	Status         string    `json:"status"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	LastUsedAt     time.Time `json:"last_used_at,omitempty"`
	RevokedAt      time.Time `json:"revoked_at,omitempty"`
	RotatedFromID  string    `json:"rotated_from_id,omitempty"`
}

type ChannelHandoff struct {
	ID                      string         `json:"id"`
	InstanceID              string         `json:"instance_id"`
	OriginConversationID    string         `json:"origin_conversation_id"`
	OriginInboxID           string         `json:"origin_inbox_id"`
	RequesterIdentityLinkID string         `json:"requester_identity_link_id"`
	OwnerConversationID     string         `json:"owner_conversation_id,omitempty"`
	Summary                 string         `json:"summary"`
	Details                 string         `json:"details,omitempty"`
	Source                  map[string]any `json:"source,omitempty"`
	State                   string         `json:"state"`
	Decision                string         `json:"decision,omitempty"`
	AcceptedActionID        string         `json:"accepted_action_id,omitempty"`
	CreatedTaskID           string         `json:"created_task_id,omitempty"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
	ResolvedAt              time.Time      `json:"resolved_at,omitempty"`
}

type ChannelPluginCommand struct {
	ID             string         `json:"id"`
	InstanceID     string         `json:"instance_id"`
	Kind           string         `json:"kind"`
	IdempotencyKey string         `json:"idempotency_key"`
	Payload        map[string]any `json:"payload"`
	Progress       map[string]any `json:"progress,omitempty"`
	State          string         `json:"state"`
	Attempts       int            `json:"attempts"`
	AvailableAt    time.Time      `json:"available_at"`
	LeaseID        string         `json:"lease_id,omitempty"`
	LeaseUntil     time.Time      `json:"lease_until,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	LastErrorCode  string         `json:"last_error_code,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	CompletedAt    time.Time      `json:"completed_at,omitempty"`
}

type ChannelOnboardingSession struct {
	ID                    string    `json:"id"`
	InstanceID            string    `json:"instance_id"`
	OwnerSessionID        string    `json:"-"`
	Mode                  string    `json:"mode"`
	RegistrationCommandID string    `json:"registration_command_id"`
	VerificationURLRef    string    `json:"-"`
	VerificationURL       string    `json:"verification_url,omitempty"`
	Status                string    `json:"status"`
	Step                  string    `json:"step"`
	SecretStageRef        string    `json:"-"`
	ScannerExternalUserID string    `json:"-"`
	ExpiresAt             time.Time `json:"expires_at"`
	ConsumedAt            time.Time `json:"consumed_at,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}
