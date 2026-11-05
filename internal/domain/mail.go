package domain

import (
	"strings"
	"time"
)

// Mail resources are Task-bindable external capabilities, not channel
// instances. See docs/email-resource-v1-design.md. Only the data shapes,
// states and stable error codes live here; rendering, approval and SMTP
// delivery are separate tasks.
const (
	MailProviderNeteaseQiye = "netease_qiye"
	MailSMTPSecuritySSL     = "ssl"
	MailDefaultSMTPHost     = "smtp.qiye.163.com"
	MailBackupSMTPHost      = "smtphz.qiye.163.com"
	MailDefaultSMTPPort     = 465

	MailCredentialRefPrefix = "mail-account/"
	MailCredentialRefSuffix = "/smtp/credential"

	MailLinkTokenRefPrefix = "mail-download/"
	MailLinkTokenRefSuffix = "/token"

	// MailDeliveryMaxAttempts is the total SMTP attempt budget from design
	// section 3.4: one initial attempt plus seven retries.
	MailDeliveryMaxAttempts = 8

	// MailLinkDefaultTTL and MailLinkDefaultMaxDownloads are the controlled
	// download defaults from design section 7.3.
	MailLinkDefaultTTL          = 7 * 24 * time.Hour
	MailLinkDefaultMaxDownloads = 20
)

// MailAccountCredentialRef returns the Secret Store key for an account's SMTP
// authorization code. The code itself is never stored in the database.
func MailAccountCredentialRef(accountID string) string {
	return MailCredentialRefPrefix + strings.TrimSpace(accountID) + MailCredentialRefSuffix
}

// MailAttachmentLinkTokenRef returns the Secret Store key holding the original
// controlled-download token. The database only stores its SHA-256 digest.
func MailAttachmentLinkTokenRef(attachmentID string) string {
	return MailLinkTokenRefPrefix + strings.TrimSpace(attachmentID) + MailLinkTokenRefSuffix
}

// MailFromAddresses returns the From allowlist for an account: the primary
// address plus every registered alias.
func (account MailAccount) MailFromAddresses() []string {
	result := make([]string, 0, len(account.Aliases)+1)
	if strings.TrimSpace(account.Address) != "" {
		result = append(result, strings.TrimSpace(account.Address))
	}
	for _, alias := range account.Aliases {
		if strings.TrimSpace(alias.Address) != "" {
			result = append(result, strings.TrimSpace(alias.Address))
		}
	}
	return result
}

// AllowsFrom reports whether address may be used as the visible From address.
func (account MailAccount) AllowsFrom(address string) bool {
	address = strings.TrimSpace(address)
	for _, allowed := range account.MailFromAddresses() {
		if strings.EqualFold(allowed, address) {
			return true
		}
	}
	return false
}

type MailAccountStatus string

const (
	MailAccountUnverified MailAccountStatus = "unverified"
	MailAccountOK         MailAccountStatus = "ok"
	MailAccountError      MailAccountStatus = "error"
	MailAccountDisabled   MailAccountStatus = "disabled"
)

func (status MailAccountStatus) Valid() bool {
	switch status {
	case MailAccountUnverified, MailAccountOK, MailAccountError, MailAccountDisabled:
		return true
	}
	return false
}

// MailAccount is an Owner-maintained sending account. CredentialRef is a write
// path only: store reads leave it empty so no Store result can leak the Secret
// reference. Use the dedicated credential-reference accessor when a worker or
// connection test needs it. CredentialConfigured is the only credential-related
// value exposed to APIs, logs and events.
type MailAccount struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Address              string            `json:"address"`
	DisplayName          string            `json:"display_name"`
	Provider             string            `json:"provider"`
	SMTPHost             string            `json:"smtp_host"`
	SMTPPort             int               `json:"smtp_port"`
	SMTPSecurity         string            `json:"smtp_security"`
	SMTPUsername         string            `json:"smtp_username"`
	CredentialRef        string            `json:"-"`
	CredentialConfigured bool              `json:"credential_configured"`
	Status               MailAccountStatus `json:"status"`
	// Revision is an internal optimistic-concurrency counter. It is never
	// exposed through the API and must not be treated as a display timestamp.
	Revision          int64              `json:"-"`
	LastTestAt        time.Time          `json:"last_test_at,omitempty"`
	LastTestErrorCode string             `json:"last_test_error_code,omitempty"`
	IMAPHost          string             `json:"imap_host,omitempty"`
	IMAPPort          int                `json:"imap_port,omitempty"`
	IMAPEnabled       bool               `json:"imap_enabled"`
	Aliases           []MailAccountAlias `json:"aliases,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// MailAccountAlias is an additional From address registered for an account.
type MailAccountAlias struct {
	AccountID   string `json:"-"`
	Address     string `json:"address"`
	DisplayName string `json:"display_name,omitempty"`
	Position    int    `json:"position"`
}

type MailTemplateScope string

const (
	MailTemplateScopeGlobal  MailTemplateScope = "global"
	MailTemplateScopeProject MailTemplateScope = "project"
)

func (scope MailTemplateScope) Valid() bool {
	return scope == MailTemplateScopeGlobal || scope == MailTemplateScopeProject
}

type MailTemplateStatus string

const (
	MailTemplateStatusActive   MailTemplateStatus = "active"
	MailTemplateStatusArchived MailTemplateStatus = "archived"
)

type MailTemplate struct {
	ID                string             `json:"id"`
	Scope             MailTemplateScope  `json:"scope"`
	ProjectID         string             `json:"project_id,omitempty"`
	Name              string             `json:"name"`
	Description       string             `json:"description,omitempty"`
	SubjectTemplate   string             `json:"subject_template"`
	BodyTextTemplate  string             `json:"body_text_template"`
	BodyHTMLTemplate  string             `json:"body_html_template"`
	DefaultTo         []string           `json:"default_to"`
	DefaultCC         []string           `json:"default_cc"`
	RequiredVariables []string           `json:"required_variables"`
	CurrentVersion    int                `json:"current_version"`
	Status            MailTemplateStatus `json:"status"`
	CreatedBy         string             `json:"created_by,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// MailTemplateVersion is an immutable content snapshot. Creating or updating a
// template writes the snapshot; later template edits never rewrite it.
type MailTemplateVersion struct {
	TemplateID        string    `json:"template_id"`
	Version           int       `json:"version"`
	SubjectTemplate   string    `json:"subject_template"`
	BodyTextTemplate  string    `json:"body_text_template"`
	BodyHTMLTemplate  string    `json:"body_html_template"`
	RequiredVariables []string  `json:"required_variables"`
	CreatedBy         string    `json:"created_by,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type MailBindingScopeType string

const (
	MailBindingScopeProject   MailBindingScopeType = "project"
	MailBindingScopeWorkspace MailBindingScopeType = "workspace"
	MailBindingScopeTask      MailBindingScopeType = "task"
)

func (scope MailBindingScopeType) Valid() bool {
	switch scope {
	case MailBindingScopeProject, MailBindingScopeWorkspace, MailBindingScopeTask:
		return true
	}
	return false
}

type MailApprovalMode string

const (
	MailApprovalAlways        MailApprovalMode = "always"
	MailApprovalAllowlistAuto MailApprovalMode = "allowlist_auto"
)

// MailBinding grants a Project, Workspace or Task the ability to send with an
// account. It never copies account credentials.
type MailBinding struct {
	ID                string               `json:"id"`
	ScopeType         MailBindingScopeType `json:"scope_type"`
	ScopeID           string               `json:"scope_id"`
	AccountID         string               `json:"account_id"`
	DefaultTemplateID string               `json:"default_template_id,omitempty"`
	ApprovalMode      MailApprovalMode     `json:"approval_mode"`
	AllowedRecipients []string             `json:"allowed_recipients"`
	AutoSendEnabled   bool                 `json:"auto_send_enabled"`
	Enabled           bool                 `json:"enabled"`
	CreatedBy         string               `json:"created_by,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type MailMessageSource string

const (
	MailMessageSourceAgent MailMessageSource = "agent"
	MailMessageSourceOwner MailMessageSource = "owner"
)

type MailMessageStatus string

const (
	MailMessageDraft           MailMessageStatus = "draft"
	MailMessagePendingApproval MailMessageStatus = "pending_approval"
	MailMessageQueued          MailMessageStatus = "queued"
	MailMessageSending         MailMessageStatus = "sending"
	MailMessageSent            MailMessageStatus = "sent"
	MailMessageFailed          MailMessageStatus = "failed"
	MailMessageDeadLetter      MailMessageStatus = "dead_letter"
	MailMessageRejected        MailMessageStatus = "rejected"
	MailMessageCancelled       MailMessageStatus = "cancelled"
)

func (status MailMessageStatus) Valid() bool {
	switch status {
	case MailMessageDraft, MailMessagePendingApproval, MailMessageQueued, MailMessageSending,
		MailMessageSent, MailMessageFailed, MailMessageDeadLetter, MailMessageRejected, MailMessageCancelled:
		return true
	}
	return false
}

// MailMessage is the authoritative outbound mail record and approval snapshot.
// It is not a chat message and does not participate in channel conversations.
type MailMessage struct {
	ID                 string            `json:"id"`
	TaskID             string            `json:"task_id"`
	ProjectID          string            `json:"project_id,omitempty"`
	WorkspaceID        string            `json:"workspace_id,omitempty"`
	TurnID             string            `json:"turn_id,omitempty"`
	AgentID            string            `json:"agent_id,omitempty"`
	AccountID          string            `json:"account_id"`
	BindingID          string            `json:"binding_id,omitempty"`
	TemplateID         string            `json:"template_id,omitempty"`
	TemplateVersion    int               `json:"template_version"`
	Source             MailMessageSource `json:"source"`
	IdempotencyKey     string            `json:"idempotency_key"`
	FromAddress        string            `json:"from_address"`
	FromName           string            `json:"from_name,omitempty"`
	To                 []string          `json:"to"`
	CC                 []string          `json:"cc,omitempty"`
	BCC                []string          `json:"bcc,omitempty"`
	ReplyTo            string            `json:"reply_to,omitempty"`
	Subject            string            `json:"subject"`
	BodyText           string            `json:"body_text,omitempty"`
	BodyHTML           string            `json:"body_html,omitempty"`
	Variables          map[string]string `json:"variables,omitempty"`
	ContentFingerprint string            `json:"content_fingerprint,omitempty"`
	ContentRevision    int               `json:"content_revision"`
	ApprovalDigest     string            `json:"approval_digest,omitempty"`
	Status             MailMessageStatus `json:"status"`
	ApprovedBy         string            `json:"approved_by,omitempty"`
	ApprovedAt         time.Time         `json:"approved_at,omitempty"`
	RejectedReason     string            `json:"rejected_reason,omitempty"`
	SubmittedAt        time.Time         `json:"submitted_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type MailAttachmentSourceType string

const (
	MailAttachmentSourceTaskAttachment MailAttachmentSourceType = "task_attachment"
	MailAttachmentSourceBuildArtifact  MailAttachmentSourceType = "build_artifact"
)

func (source MailAttachmentSourceType) Valid() bool {
	return source == MailAttachmentSourceTaskAttachment || source == MailAttachmentSourceBuildArtifact
}

type MailAttachmentDeliveryMode string

const (
	MailAttachmentDirect MailAttachmentDeliveryMode = "direct"
	MailAttachmentLink   MailAttachmentDeliveryMode = "link"
)

func (mode MailAttachmentDeliveryMode) Valid() bool {
	return mode == MailAttachmentDirect || mode == MailAttachmentLink
}

type MailAttachmentDisposition string

const (
	MailAttachmentDispositionAttachment MailAttachmentDisposition = "attachment"
	MailAttachmentDispositionInline     MailAttachmentDisposition = "inline"
)

// MailAttachment is a snapshot of a controlled file reference. It never holds
// file bytes, local paths or URLs. LinkTokenRef is the Secret Store key for the
// download token and is intentionally hidden from JSON; normal store reads do
// not populate it.
type MailAttachment struct {
	ID                string                     `json:"id"`
	MessageID         string                     `json:"message_id"`
	Position          int                        `json:"position"`
	SourceType        MailAttachmentSourceType   `json:"source_type"`
	SourceID          string                     `json:"source_id"`
	FileName          string                     `json:"file_name"`
	MediaType         string                     `json:"media_type"`
	SizeBytes         int64                      `json:"size_bytes"`
	SHA256            string                     `json:"sha256"`
	DeliveryMode      MailAttachmentDeliveryMode `json:"delivery_mode"`
	Disposition       MailAttachmentDisposition  `json:"disposition"`
	LinkTokenRef      string                     `json:"-"`
	LinkTokenHash     string                     `json:"link_token_hash,omitempty"`
	LinkExpiresAt     time.Time                  `json:"link_expires_at,omitempty"`
	LinkMaxDownloads  int                        `json:"link_max_downloads,omitempty"`
	LinkDownloadCount int                        `json:"link_download_count,omitempty"`
	LinkRevokedAt     time.Time                  `json:"link_revoked_at,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
}

type MailDeliveryState string

const (
	MailDeliveryQueued     MailDeliveryState = "queued"
	MailDeliverySending    MailDeliveryState = "sending"
	MailDeliverySent       MailDeliveryState = "sent"
	MailDeliveryFailed     MailDeliveryState = "failed"
	MailDeliveryDeadLetter MailDeliveryState = "dead_letter"
	MailDeliveryCancelled  MailDeliveryState = "cancelled"
)

func (state MailDeliveryState) Valid() bool {
	switch state {
	case MailDeliveryQueued, MailDeliverySending, MailDeliverySent,
		MailDeliveryFailed, MailDeliveryDeadLetter, MailDeliveryCancelled:
		return true
	}
	return false
}

type MailSMTPPhase string

const (
	MailSMTPPhaseNone     MailSMTPPhase = ""
	MailSMTPPhaseConnect  MailSMTPPhase = "connect"
	MailSMTPPhaseTLS      MailSMTPPhase = "tls"
	MailSMTPPhaseAuth     MailSMTPPhase = "auth"
	MailSMTPPhaseMailFrom MailSMTPPhase = "mail_from"
	MailSMTPPhaseRcptTo   MailSMTPPhase = "rcpt_to"
	MailSMTPPhaseData     MailSMTPPhase = "data"
	MailSMTPPhaseFinished MailSMTPPhase = "finished"
)

func (phase MailSMTPPhase) Valid() bool {
	switch phase {
	case MailSMTPPhaseNone, MailSMTPPhaseConnect, MailSMTPPhaseTLS, MailSMTPPhaseAuth,
		MailSMTPPhaseMailFrom, MailSMTPPhaseRcptTo, MailSMTPPhaseData, MailSMTPPhaseFinished:
		return true
	}
	return false
}

type MailOutcomeCertainty string

const (
	MailOutcomeUnspecified      MailOutcomeCertainty = ""
	MailOutcomeProviderAccepted MailOutcomeCertainty = "provider_accepted"
	MailOutcomePermanentFailure MailOutcomeCertainty = "permanent_failure"
	MailOutcomeUnknown          MailOutcomeCertainty = "unknown"
)

type MailAttemptOutcome string

const (
	MailAttemptSent             MailAttemptOutcome = "sent"
	MailAttemptTransientFailure MailAttemptOutcome = "transient_failure"
	MailAttemptPermanentFailure MailAttemptOutcome = "permanent_failure"
	MailAttemptUnknown          MailAttemptOutcome = "unknown"
	MailAttemptCancelled        MailAttemptOutcome = "cancelled"
)

// MailDelivery is the durable outbox record. Lease fields are the database
// mutex that prevents two workers from sending the same generation.
type MailDelivery struct {
	ID               string               `json:"id"`
	MessageID        string               `json:"message_id"`
	Generation       int                  `json:"generation"`
	ReplayOfID       string               `json:"replay_of_id,omitempty"`
	AccountID        string               `json:"account_id"`
	State            MailDeliveryState    `json:"state"`
	Attempts         int                  `json:"attempts"`
	AvailableAt      time.Time            `json:"available_at"`
	LeaseID          string               `json:"-"`
	LeaseUntil       time.Time            `json:"-"`
	SMTPPhase        MailSMTPPhase        `json:"smtp_phase,omitempty"`
	MessageIDHeader  string               `json:"message_id_header,omitempty"`
	SMTPResponse     string               `json:"smtp_response,omitempty"`
	LastErrorCode    string               `json:"last_error_code,omitempty"`
	LastErrorDetail  string               `json:"last_error_detail,omitempty"`
	OutcomeCertainty MailOutcomeCertainty `json:"outcome_certainty,omitempty"`
	ApprovedDigest   string               `json:"approved_digest,omitempty"`
	SentAt           time.Time            `json:"sent_at,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// MailDeliveryAttempt is one SMTP attempt. It never stores AUTH lines or
// authorization codes.
type MailDeliveryAttempt struct {
	ID         string             `json:"id"`
	DeliveryID string             `json:"delivery_id"`
	AttemptNo  int                `json:"attempt_no"`
	LeaseID    string             `json:"-"`
	Outcome    MailAttemptOutcome `json:"outcome"`
	ErrorCode  string             `json:"error_code,omitempty"`
	SMTPCode   int                `json:"smtp_code,omitempty"`
	SMTPPhase  MailSMTPPhase      `json:"smtp_phase,omitempty"`
	BackoffMS  int64              `json:"backoff_ms,omitempty"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at,omitempty"`
}

type BuildArtifactStorageType string

const (
	BuildArtifactStorageAHAAttachment BuildArtifactStorageType = "aha_attachment"
	BuildArtifactStorageExternalURL   BuildArtifactStorageType = "external_url"
)

// BuildArtifact is a registered build output usable as a mail attachment
// source. Phase one only enables AHA attachment storage.
type BuildArtifact struct {
	ID           string                   `json:"id"`
	TaskID       string                   `json:"task_id"`
	Label        string                   `json:"label"`
	PackageName  string                   `json:"package_name,omitempty"`
	Version      string                   `json:"version,omitempty"`
	SizeBytes    int64                    `json:"size_bytes"`
	SHA256       string                   `json:"sha256"`
	StorageType  BuildArtifactStorageType `json:"storage_type"`
	AttachmentID string                   `json:"attachment_id,omitempty"`
	ExternalURL  string                   `json:"external_url,omitempty"`
	RegisteredBy string                   `json:"registered_by,omitempty"`
	CreatedAt    time.Time                `json:"created_at"`
}

// Stable API error codes from the design's section 4.4.
type MailErrorCode string

const (
	MailErrAccountNotFound         MailErrorCode = "mail_account_not_found"
	MailErrAccountInvalid          MailErrorCode = "mail_account_invalid"
	MailErrMessageNotFound         MailErrorCode = "mail_message_not_found"
	MailErrBindingNotFound         MailErrorCode = "mail_binding_not_found"
	MailErrAccountNotBound         MailErrorCode = "mail_account_not_bound"
	MailErrAccountDisabled         MailErrorCode = "mail_account_disabled"
	MailErrAccountChanged          MailErrorCode = "mail_account_changed"
	MailErrCredentialMissing       MailErrorCode = "mail_credential_missing"
	MailErrTemplateNotFound        MailErrorCode = "mail_template_not_found"
	MailErrTemplateVersionConflict MailErrorCode = "mail_template_version_conflict"
	MailErrRecipientInvalid        MailErrorCode = "mail_recipient_invalid"
	MailErrRecipientNotAllowed     MailErrorCode = "mail_recipient_not_allowed"
	MailErrFromNotAllowed          MailErrorCode = "mail_from_not_allowed"
	MailErrAttachmentNotFound      MailErrorCode = "mail_attachment_not_found"
	MailErrAttachmentTooLarge      MailErrorCode = "mail_attachment_too_large"
	MailErrTotalTooLarge           MailErrorCode = "mail_total_too_large"
	MailErrAttachmentUnavailable   MailErrorCode = "mail_attachment_unavailable"
	MailErrTemplateVariableMissing MailErrorCode = "mail_template_variable_missing"
	MailErrTemplateVariableUnknown MailErrorCode = "mail_template_variable_unknown"
	MailErrTemplateInvalid         MailErrorCode = "mail_template_invalid"
	MailErrDuplicateMessage        MailErrorCode = "mail_duplicate_message"
	MailErrApprovalDigestMismatch  MailErrorCode = "mail_approval_digest_mismatch"
	MailErrInvalidState            MailErrorCode = "mail_invalid_state"
	MailErrRateLimited             MailErrorCode = "mail_rate_limited"
	MailErrForbidden               MailErrorCode = "mail_forbidden"
)

// Stable SMTP error codes from the design's section 3.4. These are stored on
// deliveries and attempts; raw SMTP sessions and AUTH lines are never kept.
const (
	MailErrSMTPDNSFailed                       MailErrorCode = "smtp_dns_failed"
	MailErrSMTPConnectTimeout                  MailErrorCode = "smtp_connect_timeout"
	MailErrSMTPTLSFailed                       MailErrorCode = "smtp_tls_failed"
	MailErrSMTPAuthFailed                      MailErrorCode = "smtp_auth_failed"
	MailErrSMTPServiceUnavailable              MailErrorCode = "smtp_service_unavailable"
	MailErrSMTPSenderRejected                  MailErrorCode = "smtp_sender_rejected"
	MailErrSMTPTemporaryFailure                MailErrorCode = "smtp_temporary_failure"
	MailErrSMTPRecipientRejected               MailErrorCode = "smtp_recipient_rejected"
	MailErrSMTPRecipientTemporarilyUnavailable MailErrorCode = "smtp_recipient_temporarily_unavailable"
	MailErrSMTPMessageTooLarge                 MailErrorCode = "smtp_message_too_large"
	MailErrSMTPContentRejected                 MailErrorCode = "smtp_content_rejected"
	MailErrSMTPPermanentFailure                MailErrorCode = "smtp_permanent_failure"
	MailErrSMTPProtocolError                   MailErrorCode = "smtp_protocol_error"
	MailErrSMTPConnectionLost                  MailErrorCode = "smtp_connection_lost"
	MailErrSMTPAmbiguousAfterData              MailErrorCode = "smtp_ambiguous_after_data"
	MailErrSMTPAttemptsExhausted               MailErrorCode = "smtp_attempts_exhausted"
	MailErrSMTPLeaseExpiredBeforeData          MailErrorCode = "smtp_lease_expired_before_data"
)
