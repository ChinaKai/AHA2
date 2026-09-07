package store

import (
	"context"
	"fmt"
	"strings"
)

const schemaV31 = `
ALTER TABLE knowledge_entries ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_entries ADD COLUMN slug TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_entries ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE knowledge_entries ADD COLUMN is_index INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX idx_knowledge_one_root ON knowledge_entries(scope,project_id) WHERE is_index=1;
CREATE UNIQUE INDEX idx_knowledge_sibling_slug ON knowledge_entries(scope,project_id,parent_id,slug) WHERE slug<>'';
CREATE INDEX idx_knowledge_parent_order ON knowledge_entries(scope,project_id,parent_id,sort_order,slug);
`

const schemaV32 = `
CREATE TABLE IF NOT EXISTS attachments (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    media_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_attachments_task_message ON attachments(task_id,message_id,created_at);
CREATE INDEX IF NOT EXISTS idx_attachments_sha256 ON attachments(sha256);
`

const schemaV34 = `
CREATE TABLE IF NOT EXISTS knowledge_proposals (
    id TEXT PRIMARY KEY,
    entry_id TEXT NOT NULL,
    base_revision INTEGER NOT NULL DEFAULT 0,
    proposed_json TEXT NOT NULL,
    source_task_id TEXT NOT NULL DEFAULT '',
    source_turn_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    decided_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_proposals_one_pending
ON knowledge_proposals(entry_id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS idx_knowledge_proposals_status_created
ON knowledge_proposals(status,created_at DESC);
`

const schemaV35 = `
ALTER TABLE sync_state ADD COLUMN replay_required INTEGER NOT NULL DEFAULT 0;
DELETE FROM sync_applied
WHERE idempotency_key LIKE 'center:%' OR idempotency_key LIKE 'center-object:%';
UPDATE sync_state
SET cursor='',last_pull_at='',last_error='',replay_required=1,
    updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE cursor<>'';
`

const schemaV36 = `
DELETE FROM sync_remote_task_objects
WHERE owner_device_id=COALESCE((SELECT device_id FROM sync_settings WHERE scope='default'),'');
DELETE FROM workspaces
WHERE read_only=1
  AND owner_device_id=COALESCE((SELECT device_id FROM sync_settings WHERE scope='default'),'');
`

const schemaV37 = `
CREATE TABLE IF NOT EXISTS sync_tombstones (
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    version TEXT NOT NULL,
    sync_key TEXT NOT NULL UNIQUE,
    deleted_at TEXT NOT NULL,
    PRIMARY KEY(object_type,object_id)
);
CREATE INDEX IF NOT EXISTS idx_sync_tombstones_deleted
ON sync_tombstones(deleted_at,object_type,object_id);
`

const schemaV38 = `
CREATE TABLE IF NOT EXISTS knowledge_libraries (
    id TEXT PRIMARY KEY,
    container_project_id TEXT NOT NULL UNIQUE REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source_identity TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_knowledge_bindings (
    library_id TEXT PRIMARY KEY REFERENCES knowledge_libraries(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK(project_id <> '')
);
CREATE INDEX IF NOT EXISTS idx_project_knowledge_bindings_project
ON project_knowledge_bindings(project_id,library_id);

UPDATE projects
SET project_type='knowledge'
WHERE project_type<>'knowledge' AND (
    description LIKE 'Imported AHA1 knowledge archive for legacy project %'
    OR description='Isolated AHA1 personal knowledge, captures, and pending notes.'
);

INSERT OR IGNORE INTO knowledge_libraries(id,container_project_id,name,description,source_identity,created_at,updated_at)
SELECT 'library_' || id,id,name,description,repository_identity,created_at,updated_at
FROM projects
WHERE project_type='knowledge';
`

const schemaV40 = `
CREATE TABLE IF NOT EXISTS security_settings (
    id INTEGER PRIMARY KEY CHECK(id=1),
    validate_origin INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL
);
INSERT OR IGNORE INTO security_settings(id,validate_origin,updated_at)
VALUES(1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
`

const schemaV27 = `
CREATE TABLE IF NOT EXISTS sync_settings (
    scope TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0,
    endpoint TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    interval_seconds INTEGER NOT NULL DEFAULT 300,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sync_state (
    scope TEXT PRIMARY KEY,
    cursor TEXT NOT NULL DEFAULT '',
    last_push_at TEXT NOT NULL DEFAULT '',
    last_pull_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sync_outbox (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    base_version TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sync_outbox_pending ON sync_outbox(scope,status,next_attempt_at,created_at);
CREATE TABLE IF NOT EXISTS sync_conflicts (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    local_payload_json TEXT NOT NULL DEFAULT '{}',
    remote_payload_json TEXT NOT NULL DEFAULT '{}',
    local_version TEXT NOT NULL DEFAULT '',
    remote_version TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    created_at TEXT NOT NULL,
    resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sync_conflicts_open ON sync_conflicts(scope,status,created_at);
CREATE TABLE IF NOT EXISTS sync_applied (
    idempotency_key TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    remote_version TEXT NOT NULL DEFAULT '',
    applied_at TEXT NOT NULL
);
`

const schemaV29 = `
ALTER TABLE workspaces ADD COLUMN owner_device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_workspaces_owner_device ON workspaces(owner_device_id,read_only);
CREATE TABLE IF NOT EXISTS sync_remote_task_objects (
    owner_device_id TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    task_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(owner_device_id,object_type,object_id)
);
CREATE INDEX IF NOT EXISTS idx_sync_remote_task_objects_task ON sync_remote_task_objects(owner_device_id,task_id,object_type);
`

const schemaV28 = `
ALTER TABLE sync_settings ADD COLUMN secret_selection_json TEXT NOT NULL DEFAULT '{}';
`

const schemaV30 = `
ALTER TABLE sync_settings ADD COLUMN device_name TEXT NOT NULL DEFAULT '';
UPDATE sync_settings SET device_name=device_id WHERE device_name='';
`

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

const schemaV8 = `
DROP INDEX IF EXISTS idx_turns_one_active;

CREATE TABLE IF NOT EXISTS task_rounds (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    input_message_id TEXT NOT NULL REFERENCES messages(id),
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT '',
    UNIQUE(task_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_rounds_task ON task_rounds(task_id, sequence);
CREATE UNIQUE INDEX IF NOT EXISTS idx_rounds_one_active
ON task_rounds(task_id)
WHERE status IN ('running', 'waiting');

ALTER TABLE turns ADD COLUMN round_id TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN parent_turn_id TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1;
ALTER TABLE turns ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN required INTEGER NOT NULL DEFAULT 1;
ALTER TABLE turns ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN instruction TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN prompt_chars INTEGER NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN prompt_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN usage_json TEXT NOT NULL DEFAULT '{}';

INSERT OR IGNORE INTO task_rounds(id,task_id,sequence,input_message_id,status,created_at,started_at,finished_at)
SELECT
    'round-' || id,
    task_id,
    sequence,
    input_message_id,
    CASE
        WHEN status='succeeded' THEN 'completed'
        WHEN status='failed' OR status='blocked' THEN 'failed'
        WHEN status='interrupted' THEN 'interrupted'
        ELSE 'running'
    END,
    queued_at,
    started_at,
    finished_at
FROM turns;

UPDATE turns SET round_id='round-' || id WHERE round_id='';

CREATE INDEX IF NOT EXISTS idx_turns_round ON turns(round_id, sequence);
CREATE INDEX IF NOT EXISTS idx_turns_agent ON turns(task_id, agent_id, sequence);
CREATE UNIQUE INDEX IF NOT EXISTS idx_turns_one_active_agent
ON turns(round_id, agent_id)
WHERE status IN ('queued', 'preparing', 'starting', 'running', 'waiting');

CREATE TABLE IF NOT EXISTS conversation_items (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    round_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL,
    kind TEXT NOT NULL,
    summary TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversation_task
ON conversation_items(task_id, sequence DESC);
CREATE INDEX IF NOT EXISTS idx_conversation_category
ON conversation_items(task_id, category, sequence DESC);
CREATE INDEX IF NOT EXISTS idx_conversation_turn
ON conversation_items(turn_id, sequence);

INSERT OR IGNORE INTO conversation_items(
    id,task_id,round_id,turn_id,agent_id,category,kind,summary,payload_json,created_at
)
SELECT
    'conversation-' || messages.id,
    messages.task_id,
    COALESCE(turns.round_id, ''),
    messages.turn_id,
    messages.sender,
    'chat',
    CASE WHEN messages.role='user' THEN 'user_message' ELSE 'agent_message' END,
    messages.content,
    '{}',
    messages.created_at
FROM messages
LEFT JOIN turns ON turns.id=messages.turn_id
ORDER BY messages.created_at,messages.id;
`

const schemaV9 = `
ALTER TABLE tasks ADD COLUMN collaboration_mode TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE tasks ADD COLUMN max_agents INTEGER NOT NULL DEFAULT 3;
ALTER TABLE turns ADD COLUMN inbox_batch_id TEXT NOT NULL DEFAULT '';

ALTER TABLE conversation_items ADD COLUMN stream_agent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE conversation_items ADD COLUMN from_agent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE conversation_items ADD COLUMN to_agent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE conversation_items ADD COLUMN route_kind TEXT NOT NULL DEFAULT '';

UPDATE conversation_items
SET stream_agent_id=CASE WHEN agent_id='' OR agent_id='owner' THEN 'main' ELSE agent_id END
WHERE stream_agent_id='';
UPDATE conversation_items
SET from_agent_id=agent_id
WHERE from_agent_id='';
UPDATE conversation_items
SET to_agent_id=CASE WHEN agent_id='owner' THEN 'main' ELSE agent_id END
WHERE to_agent_id='';

CREATE INDEX IF NOT EXISTS idx_conversation_stream
ON conversation_items(task_id, stream_agent_id, sequence DESC);

CREATE TABLE IF NOT EXISTS task_agents (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    role TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'idle',
    title TEXT NOT NULL DEFAULT '',
    runtime_config_snapshot_id TEXT NOT NULL REFERENCES runtime_config_snapshots(id),
    inherit_main INTEGER NOT NULL DEFAULT 0,
    last_read_sequence INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(task_id, agent_id)
);
CREATE INDEX IF NOT EXISTS idx_task_agents_task
ON task_agents(task_id, created_at);

INSERT OR IGNORE INTO task_agents(
    task_id,agent_id,role,status,title,runtime_config_snapshot_id,inherit_main,last_read_sequence,created_at,updated_at
)
SELECT
    id,'main','main','idle','Main agent',runtime_config_snapshot_id,0,0,created_at,updated_at
FROM tasks;

CREATE TABLE IF NOT EXISTS agent_inbox (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    round_id TEXT NOT NULL REFERENCES task_rounds(id) ON DELETE CASCADE,
    target_agent_id TEXT NOT NULL,
    source_agent_id TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_turn_id TEXT NOT NULL DEFAULT '',
    message_id TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending',
    batch_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    claimed_at TEXT NOT NULL DEFAULT '',
    processed_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(task_id,target_agent_id) REFERENCES task_agents(task_id,agent_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_agent_inbox_pending
ON agent_inbox(task_id,target_agent_id,status,sequence);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_inbox_routed_turn
ON agent_inbox(task_id,target_agent_id,source_turn_id,source_kind)
WHERE source_turn_id<>'';
`

const schemaV10 = `
CREATE TABLE IF NOT EXISTS agent_session_handoffs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    source_backend_session_id TEXT NOT NULL REFERENCES backend_sessions(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    summary TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    consumed_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(task_id,agent_id) REFERENCES task_agents(task_id,agent_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_agent_session_handoffs_pending
ON agent_session_handoffs(task_id,agent_id,status,created_at);
`

const schemaV11 = `
CREATE TABLE IF NOT EXISTS prompt_template_overrides (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL
);
`

const schemaV12 = `
DROP TABLE IF EXISTS prompt_route_overrides;
`

const schemaV13 = `
CREATE TABLE IF NOT EXISTS hardware_groups (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    description TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'off',
    serial_device TEXT NOT NULL DEFAULT '',
    serial_baudrate INTEGER NOT NULL DEFAULT 115200,
    network_host TEXT NOT NULL DEFAULT '',
    network_port INTEGER NOT NULL DEFAULT 23,
    network_protocol TEXT NOT NULL DEFAULT 'telnet',
    username TEXT NOT NULL DEFAULT '',
    credential_ref TEXT NOT NULL DEFAULT '',
    password_configured INTEGER NOT NULL DEFAULT 0,
    access TEXT NOT NULL DEFAULT 'read_write',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(task_id,id)
);
CREATE INDEX IF NOT EXISTS idx_hardware_groups_task
ON hardware_groups(task_id,position);

CREATE TABLE IF NOT EXISTS hardware_io (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hardware_id TEXT NOT NULL,
    transport TEXT NOT NULL,
    direction TEXT NOT NULL,
    data TEXT NOT NULL,
    encoding TEXT NOT NULL DEFAULT 'text',
    source TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hardware_io_stream
ON hardware_io(task_id,hardware_id,transport,sequence);
`

const schemaV14 = `
ALTER TABLE hardware_groups ADD COLUMN ssh_auth TEXT NOT NULL DEFAULT 'auto';
`

const schemaV15 = `
ALTER TABLE tasks ADD COLUMN isolation TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN worktree_dir TEXT NOT NULL DEFAULT '';

UPDATE tasks
SET isolation = CASE
        WHEN (SELECT isolation FROM workspaces WHERE workspaces.id=tasks.workspace_id) IN ('worktree','inplace')
            THEN (SELECT isolation FROM workspaces WHERE workspaces.id=tasks.workspace_id)
        WHEN (SELECT project_type FROM projects WHERE projects.id=tasks.project_id)='git' THEN 'worktree'
        ELSE 'inplace'
    END,
    worktree_dir = CASE
        WHEN (SELECT isolation FROM workspaces WHERE workspaces.id=tasks.workspace_id)='worktree'
            THEN COALESCE((SELECT worktree_dir FROM workspaces WHERE workspaces.id=tasks.workspace_id),'')
        ELSE ''
    END
WHERE isolation='';

UPDATE workspaces SET isolation='', worktree_dir='';
`

const schemaV16 = `
ALTER TABLE workspaces ADD COLUMN ssh_auth TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE workspaces ADD COLUMN ssh_credential_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN ssh_password_configured INTEGER NOT NULL DEFAULT 0;
`

const schemaV17 = `
CREATE TABLE codex_accounts (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    account_id TEXT NOT NULL DEFAULT '',
    plan_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ready',
    credential_ref TEXT NOT NULL,
    credential_configured INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_codex_accounts_email ON codex_accounts(email);

ALTER TABLE models ADD COLUMN source TEXT NOT NULL DEFAULT 'provider';
ALTER TABLE models ADD COLUMN codex_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_config_snapshots ADD COLUMN codex_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE backend_sessions ADD COLUMN codex_account_id TEXT NOT NULL DEFAULT '';
`

const schemaV18 = `
CREATE TABLE proxy_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    http_proxy TEXT NOT NULL DEFAULT '',
    https_proxy TEXT NOT NULL DEFAULT '',
    no_proxy TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO proxy_settings(id,http_proxy,https_proxy,no_proxy,updated_at)
VALUES(1,'http://127.0.0.1:7897','http://127.0.0.1:7897','localhost,127.0.0.1,::1',strftime('%Y-%m-%dT%H:%M:%fZ','now'));

ALTER TABLE runtime_config_snapshots ADD COLUMN proxy_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE codex_accounts ADD COLUMN proxy_enabled INTEGER NOT NULL DEFAULT 0;
`

const schemaV19 = `
ALTER TABLE codex_accounts ADD COLUMN usage_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE codex_accounts ADD COLUMN usage_updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE codex_accounts ADD COLUMN usage_error TEXT NOT NULL DEFAULT '';
ALTER TABLE codex_accounts ADD COLUMN models_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE codex_accounts ADD COLUMN models_updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE codex_accounts ADD COLUMN models_error TEXT NOT NULL DEFAULT '';
`

const schemaV20 = `
DELETE FROM runtime_config_snapshots
WHERE model_id IN (SELECT id FROM models WHERE source='official' AND codex_account_id<>'')
  AND NOT EXISTS(SELECT 1 FROM tasks WHERE runtime_config_snapshot_id=runtime_config_snapshots.id)
  AND NOT EXISTS(SELECT 1 FROM task_agents WHERE runtime_config_snapshot_id=runtime_config_snapshots.id)
  AND NOT EXISTS(SELECT 1 FROM turns WHERE runtime_config_snapshot_id=runtime_config_snapshots.id);

DELETE FROM models
WHERE source='official' AND codex_account_id<>''
  AND NOT EXISTS(SELECT 1 FROM runtime_config_snapshots WHERE model_id=models.id)
  AND NOT EXISTS(SELECT 1 FROM backend_sessions WHERE model_id=models.id);

DELETE FROM providers WHERE id='official-codex';
`

const schemaV21 = `
ALTER TABLE models ADD COLUMN deleted_at TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_models_visible ON models(deleted_at,provider_id,display_name);
`

const schemaV22 = `
ALTER TABLE projects ADD COLUMN knowledge_policy TEXT NOT NULL DEFAULT 'enabled';
ALTER TABLE projects ADD COLUMN knowledge_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN knowledge_policy TEXT NOT NULL DEFAULT 'inherit';

ALTER TABLE knowledge_entries ADD COLUMN product_line_id TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_entries ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE knowledge_entries ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_entries ADD COLUMN verified_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_entries ADD COLUMN helped_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE knowledge_entries ADD COLUMN stale_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE knowledge_entries ADD COLUMN feedback_state TEXT NOT NULL DEFAULT '';

CREATE TABLE product_lines (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    branch_pattern TEXT NOT NULL DEFAULT '',
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_product_lines_project ON product_lines(project_id,is_default,name);

CREATE TABLE skills (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'active',
    enabled INTEGER NOT NULL DEFAULT 1,
    source_path TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_skills_scope ON skills(scope,project_id,status,enabled,name);
`

const schemaV23 = `
ALTER TABLE tasks ADD COLUMN skill_ids_json TEXT NOT NULL DEFAULT '[]';
`

const schemaV24 = `
ALTER TABLE skills ADD COLUMN package_slug TEXT NOT NULL DEFAULT '';
UPDATE skills SET package_slug=id WHERE package_slug='';
CREATE UNIQUE INDEX idx_skills_package_slug ON skills(package_slug);
`

const schemaV25 = `
ALTER TABLE tasks ADD COLUMN agent_capabilities_json TEXT NOT NULL DEFAULT '{}';
`

const schemaV26 = `
ALTER TABLE turns ADD COLUMN context_ready_at TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN session_ready_at TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN first_event_at TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN last_activity_at TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN stalled_at TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN backend_finished_at TEXT NOT NULL DEFAULT '';
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
	var hasV8 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=8)`).Scan(&hasV8)
	if !hasV8 {
		if _, err := s.db.ExecContext(ctx, schemaV8); err != nil {
			return fmt.Errorf("apply schema v8: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(8, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v8: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_turns_one_active`); err != nil {
		return fmt.Errorf("remove legacy single-turn index: %w", err)
	}
	var hasV9 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=9)`).Scan(&hasV9)
	if !hasV9 {
		if _, err := s.db.ExecContext(ctx, schemaV9); err != nil {
			return fmt.Errorf("apply schema v9: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(9, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v9: %w", err)
	}
	var hasV10 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=10)`).Scan(&hasV10)
	if !hasV10 {
		if _, err := s.db.ExecContext(ctx, schemaV10); err != nil {
			return fmt.Errorf("apply schema v10: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(10, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v10: %w", err)
	}
	var hasV11 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=11)`).Scan(&hasV11)
	if !hasV11 {
		if _, err := s.db.ExecContext(ctx, schemaV11); err != nil {
			return fmt.Errorf("apply schema v11: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(11, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v11: %w", err)
	}
	var hasV12 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=12)`).Scan(&hasV12)
	if !hasV12 {
		if _, err := s.db.ExecContext(ctx, schemaV12); err != nil {
			return fmt.Errorf("apply schema v12: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(12, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v12: %w", err)
	}
	var hasV13 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=13)`).Scan(&hasV13)
	if !hasV13 {
		if _, err := s.db.ExecContext(ctx, schemaV13); err != nil {
			return fmt.Errorf("apply schema v13: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(13, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v13: %w", err)
	}
	var hasV14 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=14)`).Scan(&hasV14)
	if !hasV14 {
		if _, err := s.db.ExecContext(ctx, schemaV14); err != nil {
			return fmt.Errorf("apply schema v14: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(14, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v14: %w", err)
	}
	var hasV15 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=15)`).Scan(&hasV15)
	if !hasV15 {
		if _, err := s.db.ExecContext(ctx, schemaV15); err != nil {
			return fmt.Errorf("apply schema v15: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(15, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v15: %w", err)
	}
	var hasV16 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=16)`).Scan(&hasV16)
	if !hasV16 {
		if _, err := s.db.ExecContext(ctx, schemaV16); err != nil {
			return fmt.Errorf("apply schema v16: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(16, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v16: %w", err)
	}
	var hasV17 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=17)`).Scan(&hasV17)
	if !hasV17 {
		if _, err := s.db.ExecContext(ctx, schemaV17); err != nil {
			return fmt.Errorf("apply schema v17: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(17, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v17: %w", err)
	}
	var hasV18 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=18)`).Scan(&hasV18)
	if !hasV18 {
		if _, err := s.db.ExecContext(ctx, schemaV18); err != nil {
			return fmt.Errorf("apply schema v18: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(18, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v18: %w", err)
	}
	var hasV19 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=19)`).Scan(&hasV19)
	if !hasV19 {
		if _, err := s.db.ExecContext(ctx, schemaV19); err != nil {
			return fmt.Errorf("apply schema v19: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(19, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v19: %w", err)
	}
	var hasV20 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=20)`).Scan(&hasV20)
	if !hasV20 {
		if _, err := s.db.ExecContext(ctx, schemaV20); err != nil {
			return fmt.Errorf("apply schema v20: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(20, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v20: %w", err)
	}
	var hasV21 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=21)`).Scan(&hasV21)
	if !hasV21 {
		if _, err := s.db.ExecContext(ctx, schemaV21); err != nil {
			return fmt.Errorf("apply schema v21: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(21, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v21: %w", err)
	}
	var hasV22 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=22)`).Scan(&hasV22)
	if !hasV22 {
		if _, err := s.db.ExecContext(ctx, schemaV22); err != nil {
			return fmt.Errorf("apply schema v22: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(22, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v22: %w", err)
	}
	var hasV23 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=23)`).Scan(&hasV23)
	if !hasV23 {
		if _, err := s.db.ExecContext(ctx, schemaV23); err != nil {
			return fmt.Errorf("apply schema v23: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(23, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v23: %w", err)
	}
	var hasV24 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=24)`).Scan(&hasV24)
	if !hasV24 {
		if _, err := s.db.ExecContext(ctx, schemaV24); err != nil {
			return fmt.Errorf("apply schema v24: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(24, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v24: %w", err)
	}
	var hasV25 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=25)`).Scan(&hasV25)
	if !hasV25 {
		if _, err := s.db.ExecContext(ctx, schemaV25); err != nil {
			return fmt.Errorf("apply schema v25: %w", err)
		}
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(25, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		return fmt.Errorf("record schema v25: %w", err)
	}
	var hasV26 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=26)`).Scan(&hasV26)
	if !hasV26 {
		if _, err := s.db.ExecContext(ctx, schemaV26); err != nil {
			return fmt.Errorf("apply schema v26: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(26, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v26: %w", err)
	}
	var hasV27 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=27)`).Scan(&hasV27)
	if !hasV27 {
		if _, err := s.db.ExecContext(ctx, schemaV27); err != nil {
			return fmt.Errorf("apply schema v27: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(27, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v27: %w", err)
	}
	var hasV28 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=28)`).Scan(&hasV28)
	if !hasV28 {
		if _, err := s.db.ExecContext(ctx, schemaV28); err != nil {
			return fmt.Errorf("apply schema v28: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(28, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v28: %w", err)
	}
	var hasV29 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=29)`).Scan(&hasV29)
	if !hasV29 {
		if _, err := s.db.ExecContext(ctx, schemaV29); err != nil {
			return fmt.Errorf("apply schema v29: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(29, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v29: %w", err)
	}
	var hasV30 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=30)`).Scan(&hasV30)
	if !hasV30 {
		if _, err := s.db.ExecContext(ctx, schemaV30); err != nil {
			return fmt.Errorf("apply schema v30: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(30, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v30: %w", err)
	}
	var hasV31 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=31)`).Scan(&hasV31)
	if !hasV31 {
		if _, err := s.db.ExecContext(ctx, schemaV31); err != nil {
			return fmt.Errorf("apply schema v31: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(31, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v31: %w", err)
	}
	var hasV32 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=32)`).Scan(&hasV32)
	if !hasV32 {
		if _, err := s.db.ExecContext(ctx, schemaV32); err != nil {
			return fmt.Errorf("apply schema v32: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(32, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v32: %w", err)
	}
	var hasV33 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=33)`).Scan(&hasV33)
	if !hasV33 {
		if err := s.EnsureKnowledgeRoots(ctx); err != nil {
			return fmt.Errorf("apply schema v33: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(33, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v33: %w", err)
	}
	var hasV34 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=34)`).Scan(&hasV34)
	if !hasV34 {
		if _, err := s.db.ExecContext(ctx, schemaV34); err != nil {
			return fmt.Errorf("apply schema v34: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(34, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v34: %w", err)
	}
	var hasV35 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=35)`).Scan(&hasV35)
	if !hasV35 {
		if _, err := s.db.ExecContext(ctx, schemaV35); err != nil {
			return fmt.Errorf("apply schema v35: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(35, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v35: %w", err)
	}
	var hasV36 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=36)`).Scan(&hasV36)
	if !hasV36 {
		if _, err := s.db.ExecContext(ctx, schemaV36); err != nil {
			return fmt.Errorf("apply schema v36: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(36, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v36: %w", err)
	}
	var hasV37 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=37)`).Scan(&hasV37)
	if !hasV37 {
		if _, err := s.db.ExecContext(ctx, schemaV37); err != nil {
			return fmt.Errorf("apply schema v37: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE sync_outbox ADD COLUMN source_version TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("apply schema v37 outbox source version: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(37, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v37: %w", err)
	}
	var hasV38 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=38)`).Scan(&hasV38)
	if !hasV38 {
		if _, err := s.db.ExecContext(ctx, schemaV38); err != nil {
			return fmt.Errorf("apply schema v38: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(38, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v38: %w", err)
	}
	var hasV39 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=39)`).Scan(&hasV39)
	if !hasV39 {
		if _, err := s.PruneImportedAHA1Worklogs(ctx); err != nil {
			return fmt.Errorf("apply schema v39 AHA1 worklog cleanup: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(39, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v39: %w", err)
	}
	var hasV40 bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=40)`).Scan(&hasV40)
	if !hasV40 {
		if _, err := s.db.ExecContext(ctx, schemaV40); err != nil {
			return fmt.Errorf("apply schema v40: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(40, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("record schema v40: %w", err)
	}
	return nil
}
