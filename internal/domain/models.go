package domain

import "time"

const (
	OfficialCodexProviderID = "official-codex"
	ModelSourceProvider     = "provider"
	ModelSourceOfficial     = "official"
)

type Owner struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	LastLoginAt  time.Time `json:"last_login_at,omitempty"`
}

type Session struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	TokenHash string    `json:"-"`
	CSRFToken string    `json:"csrf_token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen_at"`
	ExpiresAt time.Time `json:"expires_at"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

type Project struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	ProjectType        string    `json:"project_type,omitempty"`
	RepositoryIdentity string    `json:"repository_identity,omitempty"`
	DefaultWorkspaceID string    `json:"default_workspace_id,omitempty"`
	DefaultBranch      string    `json:"default_branch,omitempty"`
	KnowledgePolicy    string    `json:"knowledge_policy"`
	KnowledgeRevision  int       `json:"knowledge_revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ProductLine struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	BranchPattern string    `json:"branch_pattern"`
	Default       bool      `json:"default"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Workspace struct {
	ID                    string         `json:"id"`
	ProjectID             string         `json:"project_id"`
	Name                  string         `json:"name"`
	Locality              string         `json:"locality"`
	Transport             string         `json:"transport"`
	RootPath              string         `json:"root_path"`
	SSHHost               string         `json:"ssh_host,omitempty"`
	SSHUser               string         `json:"ssh_user,omitempty"`
	SSHPort               int            `json:"ssh_port,omitempty"`
	SSHAuth               string         `json:"ssh_auth,omitempty"`
	SSHCredentialRef      string         `json:"-"`
	SSHPasswordConfigured bool           `json:"ssh_password_configured"`
	SSHPassword           string         `json:"-"`
	Distro                string         `json:"distro,omitempty"`
	Platform              string         `json:"platform,omitempty"`
	Health                string         `json:"health"`
	Capabilities          map[string]any `json:"capabilities,omitempty"`
	Repository            map[string]any `json:"repository,omitempty"`
	LastDetectedAt        time.Time      `json:"last_detected_at,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type Model struct {
	ID                string         `json:"id"`
	DisplayName       string         `json:"display_name"`
	ProviderID        string         `json:"provider_id"`
	ProviderName      string         `json:"provider_name,omitempty"`
	Source            string         `json:"source"`
	CodexAccountID    string         `json:"codex_account_id,omitempty"`
	Backend           string         `json:"backend"`
	WireModel         string         `json:"wire_model"`
	WireAPI           string         `json:"wire_api,omitempty"`
	ContextWindow     int64          `json:"context_window,omitempty"`
	MaxOutputTokens   int64          `json:"max_output_tokens,omitempty"`
	DefaultEffort     string         `json:"default_reasoning_effort,omitempty"`
	Capabilities      map[string]any `json:"capabilities,omitempty"`
	DefaultEnvGroupID string         `json:"-"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type CodexAccount struct {
	ID                   string             `json:"id"`
	Label                string             `json:"label"`
	Email                string             `json:"email,omitempty"`
	AccountID            string             `json:"account_id,omitempty"`
	PlanType             string             `json:"plan_type,omitempty"`
	Status               string             `json:"status"`
	ProxyEnabled         bool               `json:"proxy_enabled"`
	CredentialRef        string             `json:"-"`
	CredentialConfigured bool               `json:"credential_configured"`
	Usage                *CodexUsage        `json:"usage,omitempty"`
	UsageUpdatedAt       time.Time          `json:"usage_updated_at,omitempty"`
	UsageError           string             `json:"usage_error,omitempty"`
	AvailableModels      []CodexModelOption `json:"available_models,omitempty"`
	ModelsUpdatedAt      time.Time          `json:"models_updated_at,omitempty"`
	ModelsError          string             `json:"models_error,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
	LastUsedAt           time.Time          `json:"last_used_at,omitempty"`
}

type CodexUsage struct {
	RateLimits            []CodexRateLimit `json:"rate_limits"`
	Credits               CodexCredits     `json:"credits"`
	ResetCreditsAvailable int              `json:"reset_credits_available,omitempty"`
}

type CodexRateLimit struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Allowed         bool              `json:"allowed"`
	LimitReached    bool              `json:"limit_reached"`
	PrimaryWindow   *CodexUsageWindow `json:"primary_window,omitempty"`
	SecondaryWindow *CodexUsageWindow `json:"secondary_window,omitempty"`
}

type CodexUsageWindow struct {
	UsedPercent        int   `json:"used_percent"`
	LimitWindowSeconds int64 `json:"limit_window_seconds"`
	ResetAt            int64 `json:"reset_at,omitempty"`
}

type CodexCredits struct {
	HasCredits          bool   `json:"has_credits"`
	Unlimited           bool   `json:"unlimited"`
	OverageLimitReached bool   `json:"overage_limit_reached"`
	Balance             string `json:"balance,omitempty"`
}

type CodexModelOption struct {
	WireModel        string   `json:"wire_model"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description,omitempty"`
	ContextWindow    int64    `json:"context_window,omitempty"`
	MaxContextWindow int64    `json:"max_context_window,omitempty"`
	DefaultEffort    string   `json:"default_reasoning_effort,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

type ProxySettings struct {
	HTTPProxy  string    `json:"http_proxy"`
	HTTPSProxy string    `json:"https_proxy"`
	NoProxy    string    `json:"no_proxy"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type Provider struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	BaseURL              string    `json:"base_url"`
	AnthropicBaseURL     string    `json:"anthropic_base_url,omitempty"`
	AuthStyle            string    `json:"auth_style,omitempty"`
	CredentialRef        string    `json:"-"`
	CredentialConfigured bool      `json:"credential_configured"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type EnvGroup struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	ProviderID       string            `json:"provider_id"`
	Backend          string            `json:"backend"`
	Revision         int               `json:"revision"`
	Environment      map[string]string `json:"environment"`
	SecretNames      []string          `json:"secret_names"`
	SecretRefs       map[string]string `json:"-"`
	SecretConfigured bool              `json:"secret_configured"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type RuntimeConfigSnapshot struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	Backend          string    `json:"backend"`
	BackendVersion   string    `json:"backend_version,omitempty"`
	ModelID          string    `json:"model_id"`
	WireModel        string    `json:"wire_model"`
	EnvGroupID       string    `json:"env_group_id"`
	EnvGroupRevision int       `json:"env_group_revision"`
	CodexAccountID   string    `json:"codex_account_id,omitempty"`
	ProxyEnabled     bool      `json:"proxy_enabled"`
	ReasoningEffort  string    `json:"reasoning_effort"`
	PermissionsJSON  string    `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
}

type Task struct {
	ID                      string          `json:"id"`
	Code                    string          `json:"code,omitempty"`
	ProjectID               string          `json:"project_id"`
	WorkspaceID             string          `json:"workspace_id"`
	Title                   string          `json:"title"`
	OriginalRequest         string          `json:"original_request"`
	CurrentGoal             string          `json:"current_goal"`
	Status                  TaskStatus      `json:"status"`
	TargetBranch            string          `json:"target_branch,omitempty"`
	BaseCommit              string          `json:"base_commit,omitempty"`
	TaskBranch              string          `json:"task_branch,omitempty"`
	Isolation               string          `json:"isolation"`
	WorktreeDir             string          `json:"worktree_dir,omitempty"`
	TaskWorkspacePath       string          `json:"task_workspace_path,omitempty"`
	RuntimeConfigSnapshotID string          `json:"runtime_config_snapshot_id"`
	CollaborationMode       string          `json:"collaboration_mode"`
	MaxAgents               int             `json:"max_agents"`
	KnowledgePolicy         string          `json:"knowledge_policy"`
	SkillIDs                []string        `json:"skill_ids"`
	AgentCapabilities       map[string]bool `json:"agent_capabilities"`
	TotalTokens             int64           `json:"total_tokens"`
	CreatedAt               time.Time       `json:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at"`
	CompletedAt             time.Time       `json:"completed_at,omitempty"`
}

type Turn struct {
	ID                       string         `json:"id"`
	TaskID                   string         `json:"task_id"`
	RoundID                  string         `json:"round_id"`
	AgentID                  string         `json:"agent_id"`
	Sequence                 int            `json:"sequence"`
	ParentTurnID             string         `json:"parent_turn_id,omitempty"`
	Attempt                  int            `json:"attempt"`
	Generation               int            `json:"generation"`
	Required                 bool           `json:"required"`
	Title                    string         `json:"title,omitempty"`
	Instruction              string         `json:"-"`
	InputMessageID           string         `json:"input_message_id"`
	Status                   TurnStatus     `json:"status"`
	WaitingReason            string         `json:"waiting_reason,omitempty"`
	BackendSessionID         string         `json:"backend_session_id,omitempty"`
	RuntimeConfigSnapshotID  string         `json:"runtime_config_snapshot_id"`
	ContextWindow            int64          `json:"context_window,omitempty"`
	PromptChars              int            `json:"prompt_chars,omitempty"`
	PromptSnapshot           string         `json:"-"`
	InboxBatchID             string         `json:"inbox_batch_id,omitempty"`
	Usage                    map[string]any `json:"usage,omitempty"`
	QueuedAt                 time.Time      `json:"queued_at"`
	QueuedAtMS               int64          `json:"queued_at_ms"`
	PreparedAt               time.Time      `json:"prepared_at,omitempty"`
	PreparedAtMS             int64          `json:"prepared_at_ms,omitempty"`
	ContextReadyAt           time.Time      `json:"context_ready_at,omitempty"`
	ContextReadyAtMS         int64          `json:"context_ready_at_ms,omitempty"`
	SessionReadyAt           time.Time      `json:"session_ready_at,omitempty"`
	SessionReadyAtMS         int64          `json:"session_ready_at_ms,omitempty"`
	StartedAt                time.Time      `json:"started_at,omitempty"`
	StartedAtMS              int64          `json:"started_at_ms,omitempty"`
	FirstEventAt             time.Time      `json:"first_event_at,omitempty"`
	FirstEventAtMS           int64          `json:"first_event_at_ms,omitempty"`
	LastActivityAt           time.Time      `json:"last_activity_at,omitempty"`
	LastActivityAtMS         int64          `json:"last_activity_at_ms,omitempty"`
	StalledAt                time.Time      `json:"stalled_at,omitempty"`
	StalledAtMS              int64          `json:"stalled_at_ms,omitempty"`
	BackendFinishedAt        time.Time      `json:"backend_finished_at,omitempty"`
	BackendFinishedAtMS      int64          `json:"backend_finished_at_ms,omitempty"`
	FinishedAt               time.Time      `json:"finished_at,omitempty"`
	FinishedAtMS             int64          `json:"finished_at_ms,omitempty"`
	ElapsedMS                int64          `json:"elapsed_ms"`
	QueueDurationMS          int64          `json:"queue_duration_ms"`
	PrepareDurationMS        int64          `json:"prepare_duration_ms"`
	RunDurationMS            int64          `json:"run_duration_ms"`
	ContextPrepareDurationMS int64          `json:"context_prepare_duration_ms"`
	SessionWakeDurationMS    int64          `json:"session_wake_duration_ms"`
	BackendStartDurationMS   int64          `json:"backend_start_duration_ms"`
	ActiveDurationMS         int64          `json:"active_duration_ms"`
	FinalizeDurationMS       int64          `json:"finalize_duration_ms"`
	ExitCode                 *int           `json:"exit_code,omitempty"`
	Result                   string         `json:"result,omitempty"`
	Error                    string         `json:"error,omitempty"`
}

type Message struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	TurnID    string    `json:"turn_id,omitempty"`
	Role      string    `json:"role"`
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type BackendSession struct {
	ID               string    `json:"id"`
	TaskID           string    `json:"task_id"`
	AgentID          string    `json:"agent_id"`
	WorkspaceID      string    `json:"workspace_id"`
	Backend          string    `json:"backend"`
	ModelID          string    `json:"model_id"`
	EnvGroupRevision int       `json:"env_group_revision"`
	CodexAccountID   string    `json:"codex_account_id,omitempty"`
	ProviderSession  string    `json:"provider_session_id,omitempty"`
	Status           string    `json:"status"`
	ContextUsageJSON string    `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	LastUsedAt       time.Time `json:"last_used_at"`
}

type TaskMemory struct {
	TaskID       string         `json:"task_id"`
	CurrentGoal  string         `json:"current_goal"`
	Decisions    []string       `json:"decisions"`
	Facts        []string       `json:"facts"`
	Excluded     []string       `json:"excluded"`
	Progress     []string       `json:"progress"`
	Verification []string       `json:"verification"`
	NextActions  []string       `json:"next_actions"`
	Extra        map[string]any `json:"extra,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type KnowledgeEntry struct {
	ID             string          `json:"id"`
	Scope          string          `json:"scope"`
	ProjectID      string          `json:"project_id,omitempty"`
	Type           string          `json:"type"`
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	Status         KnowledgeStatus `json:"status"`
	BranchScope    string          `json:"branch_scope,omitempty"`
	ProductLineID  string          `json:"product_line_id,omitempty"`
	EvidenceJSON   string          `json:"-"`
	Confidence     float64         `json:"confidence"`
	Revision       int             `json:"revision"`
	ContentHash    string          `json:"content_hash"`
	VerifiedCommit string          `json:"verified_commit,omitempty"`
	HelpedCount    int             `json:"helped_count"`
	StaleCount     int             `json:"stale_count"`
	FeedbackState  string          `json:"feedback_state,omitempty"`
	SourceTaskID   string          `json:"source_task_id,omitempty"`
	SourceTurnID   string          `json:"source_turn_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	LastVerifiedAt time.Time       `json:"last_verified_at,omitempty"`
}

type Skill struct {
	ID           string      `json:"id"`
	PackageSlug  string      `json:"package_slug"`
	Scope        string      `json:"scope"`
	ProjectID    string      `json:"project_id,omitempty"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Instructions string      `json:"instructions"`
	Version      int         `json:"version"`
	Status       string      `json:"status"`
	Enabled      bool        `json:"enabled"`
	SourcePath   string      `json:"source_path,omitempty"`
	Files        []string    `json:"files,omitempty"`
	PackageFiles []SkillFile `json:"-"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Event struct {
	Sequence      int64          `json:"sequence"`
	ID            string         `json:"id"`
	AggregateType string         `json:"aggregate_type"`
	AggregateID   string         `json:"aggregate_id"`
	Type          string         `json:"type"`
	Data          map[string]any `json:"data"`
	OccurredAt    time.Time      `json:"occurred_at"`
}
