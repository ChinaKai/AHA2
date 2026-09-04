package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMigrationV8BackfillsRoundsAndConversation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "aha2.db")
	ctx := context.Background()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7} {
		if _, err := raw.ExecContext(ctx, schema); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	for version := 1; version <= 7; version++ {
		if _, err := raw.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, "2026-09-02T00:00:00Z"); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	now := "2026-09-02T00:00:00Z"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO projects(id,name,description,repository_identity,default_workspace_id,default_branch,created_at,updated_at,project_type) VALUES(?,?,?,?,?,?,?,?,?)`, []any{"project-v7", "P", "", "", "", "", now, now, "git"}},
		{`INSERT INTO workspaces(id,project_id,name,locality,transport,root_path,ssh_host,ssh_user,ssh_port,platform,health,capabilities_json,repository_json,last_detected_at,created_at,updated_at,distro,isolation,worktree_dir) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"workspace-v7", "project-v7", "W", "local", "native", "/tmp", "", "", 22, "", "ready", "{}", "{}", "", now, now, "", "worktree", "/tmp/task-roots"}},
		{`INSERT INTO models(id,display_name,provider_id,backend,wire_model,wire_api,context_window,max_output_tokens,default_effort,capabilities_json,default_env_group_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"model-v7", "M", "p", "stub", "stub", "", 1000, 0, "", "{}", "env-v7", now, now}},
		{`INSERT INTO env_groups(id,name,provider_id,backend,revision,environment_json,secret_names_json,secret_configured,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"env-v7", "E", "p", "stub", 1, "{}", "[]", 0, now, now}},
		{`INSERT INTO runtime_config_snapshots(id,workspace_id,backend,backend_version,model_id,wire_model,env_group_id,env_group_revision,reasoning_effort,permissions_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, []any{"runtime-v7", "workspace-v7", "stub", "", "model-v7", "stub", "env-v7", 1, "", "{}", now}},
		{`INSERT INTO tasks(id,project_id,workspace_id,title,original_request,current_goal,status,target_branch,base_commit,task_branch,task_workspace_path,runtime_config_snapshot_id,created_at,updated_at,completed_at,code) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"task-v7", "project-v7", "workspace-v7", "T", "request", "request", "waiting_user", "", "", "", "", "runtime-v7", now, now, "", "task-001"}},
		{`INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at) VALUES(?,?,?,?,?,?,?)`, []any{"message-v7", "task-v7", "turn-v7", "user", "owner", "hello", now}},
		{`INSERT INTO turns(id,task_id,agent_id,sequence,input_message_id,status,waiting_reason,backend_session_id,runtime_config_snapshot_id,queued_at,prepared_at,started_at,finished_at,exit_code,result,error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"turn-v7", "task-v7", "main", 1, "message-v7", "succeeded", "", "", "runtime-v7", now, now, now, now, 0, "done", ""}},
	}
	for _, statement := range statements {
		if _, err := raw.ExecContext(ctx, statement.query, statement.args...); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	round, err := database.LatestRound(ctx, "task-v7")
	if err != nil || round.Status != domain.RoundCompleted {
		t.Fatalf("legacy round was not backfilled: %#v %v", round, err)
	}
	turns, err := database.TurnsForRound(ctx, round.ID)
	if err != nil || len(turns) != 1 || turns[0].AgentID != "main" {
		t.Fatalf("legacy turn was not linked: %#v %v", turns, err)
	}
	page, err := database.ConversationPage(ctx, "task-v7", 0, 0, 50, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].Summary != "hello" {
		t.Fatalf("legacy conversation was not backfilled: %#v %v", page, err)
	}
	task, err := database.Task(ctx, "task-v7")
	if err != nil || task.Isolation != "worktree" || task.WorktreeDir != "/tmp/task-roots" {
		t.Fatalf("legacy workspace isolation was not migrated to task: %#v %v", task, err)
	}
	var legacyIsolation, legacyWorktreeDir string
	if err := database.db.QueryRowContext(ctx, `SELECT isolation,worktree_dir FROM workspaces WHERE id='workspace-v7'`).Scan(&legacyIsolation, &legacyWorktreeDir); err != nil {
		t.Fatal(err)
	}
	if legacyIsolation != "" || legacyWorktreeDir != "" {
		t.Fatalf("legacy workspace config was retained: isolation=%q worktree_dir=%q", legacyIsolation, legacyWorktreeDir)
	}
	var sshAuth, sshCredentialRef string
	var sshPasswordConfigured bool
	if err := database.db.QueryRowContext(
		ctx,
		`SELECT ssh_auth,ssh_credential_ref,ssh_password_configured FROM workspaces WHERE id='workspace-v7'`,
	).Scan(&sshAuth, &sshCredentialRef, &sshPasswordConfigured); err != nil {
		t.Fatal(err)
	}
	if sshAuth != "auto" || sshCredentialRef != "" || sshPasswordConfigured {
		t.Fatalf(
			"workspace SSH migration defaults are invalid: auth=%q ref=%q configured=%v",
			sshAuth, sshCredentialRef, sshPasswordConfigured,
		)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("repeat migration failed: %v", err)
	}
	var legacyIndex int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_turns_one_active'`).Scan(&legacyIndex); err != nil {
		t.Fatal(err)
	}
	if legacyIndex != 0 {
		t.Fatal("legacy task-level active turn index was recreated")
	}
}

func TestStorePersistsProject(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "aha2.db")
	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	item := domain.Project{ID: "project-1", Name: "AHA2", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.Project(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "AHA2" {
		t.Fatalf("unexpected project: %#v", stored)
	}
}
