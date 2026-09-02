package store

import (
	"context"
	"fmt"
)

const schemaV1 = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS owners (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_login_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    csrf_token TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token_hash);

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    repository_identity TEXT NOT NULL DEFAULT '',
    default_workspace_id TEXT NOT NULL DEFAULT '',
    default_branch TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    locality TEXT NOT NULL,
    transport TEXT NOT NULL,
    root_path TEXT NOT NULL,
    ssh_host TEXT NOT NULL DEFAULT '',
    ssh_user TEXT NOT NULL DEFAULT '',
    ssh_port INTEGER NOT NULL DEFAULT 22,
    platform TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT 'unknown',
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    repository_json TEXT NOT NULL DEFAULT '{}',
    last_detected_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workspaces_project ON workspaces(project_id);

CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    backend TEXT NOT NULL,
    wire_model TEXT NOT NULL,
    wire_api TEXT NOT NULL DEFAULT '',
    context_window INTEGER NOT NULL DEFAULT 0,
    max_output_tokens INTEGER NOT NULL DEFAULT 0,
    default_effort TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    default_env_group_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS env_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    backend TEXT NOT NULL,
    revision INTEGER NOT NULL,
    environment_json TEXT NOT NULL DEFAULT '{}',
    secret_names_json TEXT NOT NULL DEFAULT '[]',
    secret_configured INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime_config_snapshots (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    backend TEXT NOT NULL,
    backend_version TEXT NOT NULL DEFAULT '',
    model_id TEXT NOT NULL REFERENCES models(id),
    wire_model TEXT NOT NULL,
    env_group_id TEXT NOT NULL REFERENCES env_groups(id),
    env_group_revision INTEGER NOT NULL,
    reasoning_effort TEXT NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    title TEXT NOT NULL,
    original_request TEXT NOT NULL,
    current_goal TEXT NOT NULL,
    status TEXT NOT NULL,
    target_branch TEXT NOT NULL DEFAULT '',
    base_commit TEXT NOT NULL DEFAULT '',
    task_branch TEXT NOT NULL DEFAULT '',
    task_workspace_path TEXT NOT NULL DEFAULT '',
    runtime_config_snapshot_id TEXT NOT NULL REFERENCES runtime_config_snapshots(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    turn_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    sender TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_task ON messages(task_id, created_at);

CREATE TABLE IF NOT EXISTS turns (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    input_message_id TEXT NOT NULL REFERENCES messages(id),
    status TEXT NOT NULL,
    waiting_reason TEXT NOT NULL DEFAULT '',
    backend_session_id TEXT NOT NULL DEFAULT '',
    runtime_config_snapshot_id TEXT NOT NULL REFERENCES runtime_config_snapshots(id),
    queued_at TEXT NOT NULL,
    prepared_at TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT '',
    exit_code INTEGER,
    result TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    UNIQUE(task_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_turns_task ON turns(task_id, sequence);

CREATE TABLE IF NOT EXISTS backend_sessions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    backend TEXT NOT NULL,
    model_id TEXT NOT NULL REFERENCES models(id),
    env_group_revision INTEGER NOT NULL,
    provider_session_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    context_usage_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_backend_sessions_identity
ON backend_sessions(task_id, agent_id, workspace_id, backend, model_id, env_group_revision);

CREATE TABLE IF NOT EXISTS task_memory (
    task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    current_goal TEXT NOT NULL DEFAULT '',
    decisions_json TEXT NOT NULL DEFAULT '[]',
    facts_json TEXT NOT NULL DEFAULT '[]',
    excluded_json TEXT NOT NULL DEFAULT '[]',
    progress_json TEXT NOT NULL DEFAULT '[]',
    verification_json TEXT NOT NULL DEFAULT '[]',
    next_actions_json TEXT NOT NULL DEFAULT '[]',
    extra_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS knowledge_entries (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL,
    branch_scope TEXT NOT NULL DEFAULT '',
    evidence_json TEXT NOT NULL DEFAULT '{}',
    confidence REAL NOT NULL DEFAULT 0,
    source_task_id TEXT NOT NULL DEFAULT '',
    source_turn_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_verified_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_knowledge_scope ON knowledge_entries(scope, project_id, status);

CREATE TABLE IF NOT EXISTS events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    type TEXT NOT NULL,
    data_json TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_aggregate ON events(aggregate_type, aggregate_id, sequence);

CREATE TABLE IF NOT EXISTS audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    owner_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    data_json TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);
`

const schemaV2 = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_turns_one_active
ON turns(task_id)
WHERE status IN ('queued', 'preparing', 'starting', 'running', 'waiting');
`

const schemaV3 = `
ALTER TABLE projects ADD COLUMN project_type TEXT NOT NULL DEFAULT 'folder';
`

const schemaV4 = `
CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    auth_style TEXT NOT NULL DEFAULT 'auto',
    credential_ref TEXT NOT NULL DEFAULT '',
    credential_configured INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
`

const schemaV5 = `
ALTER TABLE providers ADD COLUMN anthropic_base_url TEXT NOT NULL DEFAULT '';
`

const schemaV6 = `
ALTER TABLE workspaces ADD COLUMN distro TEXT NOT NULL DEFAULT '';
`

const schemaV7 = `
ALTER TABLE workspaces ADD COLUMN isolation TEXT NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN worktree_dir TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN code TEXT NOT NULL DEFAULT '';
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("apply schema v1: %w", err)
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v1: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, schemaV2); err != nil {
		return fmt.Errorf("apply schema v2: %w", err)
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(2, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v2: %w", err)
	}
	var hasV3 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=3)`).Scan(&hasV3)
	if !hasV3 {
		if _, err := s.db.ExecContext(ctx, schemaV3); err != nil {
			return fmt.Errorf("apply schema v3: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(3, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v3: %w", err)
	}
	var hasV4 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=4)`).Scan(&hasV4)
	if !hasV4 {
		if _, err := s.db.ExecContext(ctx, schemaV4); err != nil {
			return fmt.Errorf("apply schema v4: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(4, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v4: %w", err)
	}
	var hasV5 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=5)`).Scan(&hasV5)
	if !hasV5 {
		if _, err := s.db.ExecContext(ctx, schemaV5); err != nil {
			return fmt.Errorf("apply schema v5: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(5, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v5: %w", err)
	}
	var hasV6 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=6)`).Scan(&hasV6)
	if !hasV6 {
		if _, err := s.db.ExecContext(ctx, schemaV6); err != nil {
			return fmt.Errorf("apply schema v6: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(6, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v6: %w", err)
	}
	var hasV7 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=7)`).Scan(&hasV7)
	if !hasV7 {
		if _, err := s.db.ExecContext(ctx, schemaV7); err != nil {
			return fmt.Errorf("apply schema v7: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(7, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v7: %w", err)
	}
	return nil
}
