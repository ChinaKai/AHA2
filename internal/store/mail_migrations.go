package store

import (
	"context"
	"fmt"
	"strings"
)

// mailSchemaV120Statements is the schema v120 migration from
// docs/email-resource-v1-design.md section 2.2. It only adds tables and
// indexes, so it can run repeatedly against a live v119 database. Time columns
// use the fixed-width layout from mailTimeString so lease and availability
// comparisons are safe as string comparisons.
var mailSchemaV120Statements = []string{
	`CREATE TABLE IF NOT EXISTS mail_accounts (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		address TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT 'netease_qiye'
			CHECK(provider IN ('netease_qiye')),
		smtp_host TEXT NOT NULL DEFAULT 'smtp.qiye.163.com',
		smtp_port INTEGER NOT NULL DEFAULT 465,
		smtp_security TEXT NOT NULL DEFAULT 'ssl'
			CHECK(smtp_security IN ('ssl')),
		smtp_username TEXT NOT NULL,
		credential_ref TEXT NOT NULL DEFAULT '',
		credential_configured INTEGER NOT NULL DEFAULT 0
			CHECK(credential_configured IN (0,1)),
		status TEXT NOT NULL DEFAULT 'unverified'
			CHECK(status IN ('unverified','ok','error','disabled')),
		last_test_at TEXT NOT NULL DEFAULT '',
		last_test_error_code TEXT NOT NULL DEFAULT '',
		imap_host TEXT NOT NULL DEFAULT '',
		imap_port INTEGER NOT NULL DEFAULT 0,
		imap_enabled INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_accounts_address
		ON mail_accounts(lower(address))`,
	`CREATE INDEX IF NOT EXISTS idx_mail_accounts_status
		ON mail_accounts(status, updated_at DESC)`,

	`CREATE TABLE IF NOT EXISTS mail_account_aliases (
		account_id TEXT NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
		address TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		position INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(account_id, address)
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_aliases_address
		ON mail_account_aliases(lower(address))`,

	`CREATE TABLE IF NOT EXISTS mail_templates (
		id TEXT PRIMARY KEY,
		scope TEXT NOT NULL CHECK(scope IN ('global','project')),
		project_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		subject_template TEXT NOT NULL,
		body_text_template TEXT NOT NULL DEFAULT '',
		body_html_template TEXT NOT NULL DEFAULT '',
		default_to_json TEXT NOT NULL DEFAULT '[]',
		default_cc_json TEXT NOT NULL DEFAULT '[]',
		required_variables_json TEXT NOT NULL DEFAULT '[]',
		current_version INTEGER NOT NULL DEFAULT 1 CHECK(current_version >= 1),
		status TEXT NOT NULL DEFAULT 'active'
			CHECK(status IN ('active','archived')),
		created_by TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		CHECK((scope = 'global' AND project_id = '')
		   OR (scope = 'project' AND project_id <> ''))
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_templates_active_name
		ON mail_templates(scope, project_id, name) WHERE status = 'active'`,
	`CREATE INDEX IF NOT EXISTS idx_mail_templates_project
		ON mail_templates(project_id, status, updated_at DESC)`,

	`CREATE TABLE IF NOT EXISTS mail_template_versions (
		template_id TEXT NOT NULL REFERENCES mail_templates(id) ON DELETE CASCADE,
		version INTEGER NOT NULL CHECK(version >= 1),
		subject_template TEXT NOT NULL,
		body_text_template TEXT NOT NULL,
		body_html_template TEXT NOT NULL,
		required_variables_json TEXT NOT NULL DEFAULT '[]',
		created_by TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		PRIMARY KEY(template_id, version)
	)`,

	`CREATE TABLE IF NOT EXISTS mail_bindings (
		id TEXT PRIMARY KEY,
		scope_type TEXT NOT NULL CHECK(scope_type IN ('project','workspace','task')),
		scope_id TEXT NOT NULL,
		account_id TEXT NOT NULL REFERENCES mail_accounts(id) ON DELETE RESTRICT,
		default_template_id TEXT REFERENCES mail_templates(id) ON DELETE SET NULL,
		approval_mode TEXT NOT NULL DEFAULT 'always'
			CHECK(approval_mode IN ('always','allowlist_auto')),
		allowed_recipients_json TEXT NOT NULL DEFAULT '[]',
		auto_send_enabled INTEGER NOT NULL DEFAULT 0 CHECK(auto_send_enabled IN (0,1)),
		enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
		created_by TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(scope_type, scope_id, account_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_bindings_scope
		ON mail_bindings(scope_type, scope_id, enabled)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_bindings_account
		ON mail_bindings(account_id, enabled)`,

	`CREATE TABLE IF NOT EXISTS build_artifacts (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		label TEXT NOT NULL,
		package_name TEXT NOT NULL DEFAULT '',
		version TEXT NOT NULL DEFAULT '',
		size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
		sha256 TEXT NOT NULL,
		storage_type TEXT NOT NULL DEFAULT 'aha_attachment'
			CHECK(storage_type IN ('aha_attachment','external_url')),
		attachment_id TEXT REFERENCES attachments(id) ON DELETE RESTRICT,
		external_url TEXT NOT NULL DEFAULT '',
		registered_by TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		CHECK((storage_type = 'aha_attachment' AND attachment_id IS NOT NULL AND external_url = '')
		   OR (storage_type = 'external_url' AND attachment_id IS NULL AND external_url <> ''))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_build_artifacts_task
		ON build_artifacts(task_id, created_at DESC)`,

	`CREATE TABLE IF NOT EXISTS mail_messages (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		project_id TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		turn_id TEXT NOT NULL DEFAULT '',
		agent_id TEXT NOT NULL DEFAULT '',
		account_id TEXT NOT NULL REFERENCES mail_accounts(id) ON DELETE RESTRICT,
		binding_id TEXT REFERENCES mail_bindings(id) ON DELETE SET NULL,
		template_id TEXT REFERENCES mail_templates(id) ON DELETE SET NULL,
		template_version INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL CHECK(source IN ('agent','owner')),
		idempotency_key TEXT NOT NULL,
		from_address TEXT NOT NULL,
		from_name TEXT NOT NULL DEFAULT '',
		to_json TEXT NOT NULL DEFAULT '[]',
		cc_json TEXT NOT NULL DEFAULT '[]',
		bcc_json TEXT NOT NULL DEFAULT '[]',
		reply_to TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL,
		body_text TEXT NOT NULL DEFAULT '',
		body_html TEXT NOT NULL DEFAULT '',
		variables_json TEXT NOT NULL DEFAULT '{}',
		content_fingerprint TEXT NOT NULL DEFAULT '',
		content_revision INTEGER NOT NULL DEFAULT 1 CHECK(content_revision >= 1),
		approval_digest TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'draft'
			CHECK(status IN ('draft','pending_approval','queued','sending','sent',
							'failed','dead_letter','rejected','cancelled')),
		approved_by TEXT NOT NULL DEFAULT '',
		approved_at TEXT NOT NULL DEFAULT '',
		rejected_reason TEXT NOT NULL DEFAULT '',
		submitted_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(task_id, idempotency_key)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_messages_task
		ON mail_messages(task_id, status, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_messages_pending
		ON mail_messages(status, created_at) WHERE status = 'pending_approval'`,
	`CREATE INDEX IF NOT EXISTS idx_mail_messages_account
		ON mail_messages(account_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_messages_dedup
		ON mail_messages(task_id, account_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_messages_fingerprint
		ON mail_messages(task_id, content_fingerprint, created_at DESC)`,

	`CREATE TABLE IF NOT EXISTS mail_attachments (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		position INTEGER NOT NULL DEFAULT 0,
		source_type TEXT NOT NULL CHECK(source_type IN ('task_attachment','build_artifact')),
		source_id TEXT NOT NULL,
		file_name TEXT NOT NULL,
		media_type TEXT NOT NULL DEFAULT 'application/octet-stream',
		size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
		sha256 TEXT NOT NULL,
		delivery_mode TEXT NOT NULL CHECK(delivery_mode IN ('direct','link')),
		disposition TEXT NOT NULL DEFAULT 'attachment'
			CHECK(disposition IN ('attachment','inline')),
		link_token_ref TEXT NOT NULL DEFAULT '',
		link_token_hash TEXT NOT NULL DEFAULT '',
		link_expires_at TEXT NOT NULL DEFAULT '',
		link_max_downloads INTEGER NOT NULL DEFAULT 0,
		link_download_count INTEGER NOT NULL DEFAULT 0,
		link_revoked_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		UNIQUE(message_id, position)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_attachments_message
		ON mail_attachments(message_id, position)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_attachments_source
		ON mail_attachments(source_type, source_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_attachments_link_hash
		ON mail_attachments(link_token_hash) WHERE link_token_hash <> ''`,

	`CREATE TABLE IF NOT EXISTS mail_deliveries (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
		generation INTEGER NOT NULL DEFAULT 0 CHECK(generation >= 0),
		replay_of_id TEXT REFERENCES mail_deliveries(id) ON DELETE SET NULL,
		account_id TEXT NOT NULL REFERENCES mail_accounts(id) ON DELETE RESTRICT,
		state TEXT NOT NULL DEFAULT 'queued'
			CHECK(state IN ('queued','sending','sent','failed','dead_letter','cancelled')),
		attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
		available_at TEXT NOT NULL,
		lease_id TEXT NOT NULL DEFAULT '',
		lease_until TEXT NOT NULL DEFAULT '',
		smtp_phase TEXT NOT NULL DEFAULT ''
			CHECK(smtp_phase IN ('','connect','tls','auth','mail_from','rcpt_to','data','finished')),
		message_id_header TEXT NOT NULL DEFAULT '',
		smtp_response TEXT NOT NULL DEFAULT '',
		last_error_code TEXT NOT NULL DEFAULT '',
		last_error_detail TEXT NOT NULL DEFAULT '',
		outcome_certainty TEXT NOT NULL DEFAULT ''
			CHECK(outcome_certainty IN ('','provider_accepted','permanent_failure','unknown')),
		approved_digest TEXT NOT NULL DEFAULT '',
		sent_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(message_id, generation)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_deliveries_claim
		ON mail_deliveries(state, available_at) WHERE state = 'queued'`,
	`CREATE INDEX IF NOT EXISTS idx_mail_deliveries_reclaim
		ON mail_deliveries(state, lease_until) WHERE state = 'sending'`,
	`CREATE INDEX IF NOT EXISTS idx_mail_deliveries_account
		ON mail_deliveries(account_id, state, updated_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_deliveries_claim_account
		ON mail_deliveries(account_id, state, available_at) WHERE state = 'queued'`,
	// At most one queued/sending generation per message. Owner retries create a
	// new generation only after the previous one is sent/failed/dead/cancelled.
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_mail_deliveries_active_message
		ON mail_deliveries(message_id) WHERE state IN ('queued','sending')`,

	`CREATE TABLE IF NOT EXISTS mail_delivery_attempts (
		id TEXT PRIMARY KEY,
		delivery_id TEXT NOT NULL REFERENCES mail_deliveries(id) ON DELETE CASCADE,
		attempt_no INTEGER NOT NULL CHECK(attempt_no >= 1),
		lease_id TEXT NOT NULL,
		outcome TEXT NOT NULL
			CHECK(outcome IN ('sent','transient_failure','permanent_failure','unknown','cancelled')),
		error_code TEXT NOT NULL DEFAULT '',
		smtp_code INTEGER NOT NULL DEFAULT 0,
		backoff_ms INTEGER NOT NULL DEFAULT 0,
		started_at TEXT NOT NULL,
		finished_at TEXT NOT NULL DEFAULT '',
		UNIQUE(delivery_id, attempt_no)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_mail_delivery_attempts_delivery
		ON mail_delivery_attempts(delivery_id, attempt_no)`,
}

func (s *Store) applyMailSchemaV120(ctx context.Context) error {
	var applied bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=120)`).Scan(&applied)
	if !applied {
		for _, statement := range mailSchemaV120Statements {
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema v120: %w", err)
			}
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(120, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v120: %w", err)
	}
	return nil
}

// applyMailSchemaV121 adds the account revision counter used as the
// optimistic-concurrency token for connection tests. It is a separate
// migration because schema v120 is already part of the mail data layer.
func (s *Store) applyMailSchemaV121(ctx context.Context) error {
	var applied bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=121)`).Scan(&applied)
	if !applied {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE mail_accounts ADD COLUMN revision INTEGER NOT NULL DEFAULT 0`); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("apply schema v121: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(121, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v121: %w", err)
	}
	return nil
}
