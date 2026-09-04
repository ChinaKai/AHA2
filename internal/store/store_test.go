package store

import (
	"context"
	"database/sql"
	"errors"
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
	model, err := database.Model(ctx, "model-v7")
	if err != nil {
		t.Fatal(err)
	}
	if model.Source != "provider" || model.CodexAccountID != "" {
		t.Fatalf("Codex account migration defaults are invalid: %#v", model)
	}
	proxy, err := database.ProxySettings(ctx)
	if err != nil || proxy.HTTPProxy != "http://127.0.0.1:7897" || proxy.HTTPSProxy != proxy.HTTPProxy || proxy.NoProxy == "" {
		t.Fatalf("proxy migration defaults are invalid: %#v %v", proxy, err)
	}
	account := domain.CodexAccount{
		ID: "codex-account-1", Label: "Work", Email: "owner@example.com", AccountID: "account-1",
		PlanType: "plus", Status: "ready", CredentialRef: "codex-account/codex-account-1/auth",
		ProxyEnabled: true, CredentialConfigured: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Usage: &domain.CodexUsage{
			RateLimits: []domain.CodexRateLimit{{
				ID: "codex", Name: "Codex", Allowed: true,
				PrimaryWindow: &domain.CodexUsageWindow{UsedPercent: 7, LimitWindowSeconds: 604800},
			}},
			Credits: domain.CodexCredits{HasCredits: true, Balance: "10"},
		},
		UsageUpdatedAt: time.Now().UTC(), AvailableModels: []domain.CodexModelOption{{
			WireModel: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", MaxContextWindow: 872000,
		}}, ModelsUpdatedAt: time.Now().UTC(),
	}
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	storedAccount, err := database.CodexAccount(ctx, account.ID)
	if err != nil || storedAccount.Email != account.Email || !storedAccount.CredentialConfigured || !storedAccount.ProxyEnabled ||
		storedAccount.Usage == nil || storedAccount.Usage.RateLimits[0].PrimaryWindow.UsedPercent != 7 ||
		len(storedAccount.AvailableModels) != 1 || storedAccount.AvailableModels[0].WireModel != "gpt-5.6-sol" {
		t.Fatalf("Codex account was not persisted: %#v %v", storedAccount, err)
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

func TestCatalogUsageIgnoresOrphanRuntimeSnapshots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateProject(ctx, domain.Project{
		ID: "project-usage", Name: "Usage", ProjectType: "local", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-usage", ProjectID: "project-usage", Name: "Usage", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertProvider(ctx, domain.Provider{
		ID: "provider-usage", Name: "Usage Provider", BaseURL: "https://example.invalid", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertEnvGroup(ctx, domain.EnvGroup{
		ID: "env-usage", Name: "Usage", ProviderID: "provider-usage", Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, domain.Model{
		ID: "model-usage", DisplayName: "Usage", ProviderID: "provider-usage", Backend: "codex",
		WireModel: "gpt-test", DefaultEnvGroupID: "env-usage", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	models, err := database.ListModels(ctx)
	if err != nil || len(models) != 1 || models[0].ProviderName != "Usage Provider" {
		t.Fatalf("model provider display missing: %#v %v", models, err)
	}
	if err := database.UpsertCodexAccount(ctx, domain.CodexAccount{
		ID: "account-usage", Status: "ready", CredentialRef: "codex/account/auth",
		CredentialConfigured: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRuntimeSnapshot(ctx, domain.RuntimeConfigSnapshot{
		ID: "snapshot-usage", WorkspaceID: "workspace-usage", Backend: "codex", ModelID: "model-usage",
		WireModel: "gpt-test", EnvGroupID: "env-usage", EnvGroupRevision: 1,
		CodexAccountID: "account-usage", PermissionsJSON: "{}", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	assertUsage := func(want bool) {
		t.Helper()
		modelInUse, err := database.ModelInUse(ctx, "model-usage")
		if err != nil || modelInUse != want {
			t.Fatalf("ModelInUse() = %v, %v; want %v", modelInUse, err, want)
		}
		envInUse, err := database.EnvGroupInUse(ctx, "env-usage")
		if err != nil || envInUse != want {
			t.Fatalf("EnvGroupInUse() = %v, %v; want %v", envInUse, err, want)
		}
		accountInUse, err := database.CodexAccountInUse(ctx, "account-usage")
		if err != nil || accountInUse != want {
			t.Fatalf("CodexAccountInUse() = %v, %v; want %v", accountInUse, err, want)
		}
	}
	assertUsage(false)
	if err := database.CreateTask(ctx, domain.Task{
		ID: "task-usage", ProjectID: "project-usage", WorkspaceID: "workspace-usage", Title: "Usage",
		OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskDraft,
		RuntimeConfigSnapshotID: "snapshot-usage", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	assertUsage(true)
	if err := database.DeleteModel(ctx, "model-usage"); err != nil {
		t.Fatalf("DeleteModel() with active task failed: %v", err)
	}
	if _, err := database.Model(ctx, "model-usage"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted model is still selectable: %v", err)
	}
	var snapshotCount, deletedCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_config_snapshots WHERE id='snapshot-usage'`).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE id='model-usage' AND deleted_at<>''`).Scan(&deletedCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 1 || deletedCount != 1 {
		t.Fatalf("deleted model did not preserve task snapshot: snapshots=%d deleted=%d", snapshotCount, deletedCount)
	}
	assertUsage(true)
	if err := database.DeleteTask(ctx, "task-usage"); err != nil {
		t.Fatal(err)
	}
	assertUsage(false)
}

func TestOfficialCodexProviderIsInternal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	for _, group := range []domain.EnvGroup{
		{ID: "env-official", Name: "Official Codex", ProviderID: domain.OfficialCodexProviderID, Backend: "codex"},
		{ID: "env-visible", Name: "Visible / Model", ProviderID: "visible", Backend: "codex"},
	} {
		group.Revision = 1
		group.Environment = map[string]string{}
		group.SecretRefs = map[string]string{}
		group.CreatedAt, group.UpdatedAt = now, now
		if err := database.UpsertEnvGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
	}
	created, err := database.BackfillProviders(ctx)
	if err != nil || created != 1 {
		t.Fatalf("BackfillProviders() = %d, %v; want 1", created, err)
	}
	if err := database.UpsertProvider(ctx, domain.Provider{
		ID: domain.OfficialCodexProviderID, Name: domain.OfficialCodexProviderID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	providers, err := database.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].ID != "visible" {
		t.Fatalf("internal Codex provider leaked into list: %#v", providers)
	}
}

func TestMigrationV20RemovesUnusedAccountBoundOfficialModels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateProject(ctx, domain.Project{
		ID: "project-v20", Name: "V20", ProjectType: "folder", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-v20", ProjectID: "project-v20", Name: "V20", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertEnvGroup(ctx, domain.EnvGroup{
		ID: "env-v20", Name: "Official", ProviderID: domain.OfficialCodexProviderID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, domain.Model{
		ID: "model-v20", DisplayName: "Legacy Official", ProviderID: domain.OfficialCodexProviderID,
		Source: domain.ModelSourceOfficial, CodexAccountID: "account-v20", Backend: "codex",
		WireModel: "gpt-legacy", DefaultEnvGroupID: "env-v20", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRuntimeSnapshot(ctx, domain.RuntimeConfigSnapshot{
		ID: "snapshot-v20", WorkspaceID: "workspace-v20", Backend: "codex", ModelID: "model-v20",
		WireModel: "gpt-legacy", EnvGroupID: "env-v20", EnvGroupRevision: 1,
		CodexAccountID: "account-v20", PermissionsJSON: "{}", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertProvider(ctx, domain.Provider{
		ID: domain.OfficialCodexProviderID, Name: domain.OfficialCodexProviderID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=20`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Model(ctx, "model-v20"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy official model was retained: %v", err)
	}
	var snapshotCount, providerCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_config_snapshots WHERE id='snapshot-v20'`).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers WHERE id=?`, domain.OfficialCodexProviderID).Scan(&providerCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 0 || providerCount != 0 {
		t.Fatalf("legacy official rows were retained: snapshots=%d providers=%d", snapshotCount, providerCount)
	}
}

func TestKnowledgeCatalogV22(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=22)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v22 missing: migrated=%t err=%v", migrated, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=23)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v23 missing: migrated=%t err=%v", migrated, err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-kb", Name: "Knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	line := domain.ProductLine{ID: "line-main", ProjectID: project.ID, Name: "Main", BranchPattern: "main", Default: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProductLine(ctx, line); err != nil {
		t.Fatal(err)
	}
	lines, err := database.ListProductLines(ctx, project.ID)
	if err != nil || len(lines) != 1 || !lines[0].Default {
		t.Fatalf("product lines = %#v, %v", lines, err)
	}
	skill := domain.Skill{ID: "skill-review", Scope: "project", ProjectID: project.ID, Name: "Review", Description: "Review changes", Instructions: "Run focused tests.", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	skills, err := database.ListSkills(ctx, "project", project.ID, true)
	if err != nil || len(skills) != 1 || skills[0].Name != skill.Name {
		t.Fatalf("skills = %#v, %v", skills, err)
	}
	entries := []domain.KnowledgeEntry{
		{ID: "knowledge-common", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Common", Body: "Shared", Status: domain.KnowledgeVerified, Confidence: .9, CreatedAt: now, UpdatedAt: now},
		{ID: "knowledge-main", Scope: "project", ProjectID: project.ID, ProductLineID: line.ID, Type: "navigation", Title: "Main", Body: "Main route", Status: domain.KnowledgeVerified, Confidence: .9, CreatedAt: now, UpdatedAt: now},
		{ID: "knowledge-other", Scope: "project", ProjectID: project.ID, ProductLineID: "line-other", Type: "navigation", Title: "Other", Body: "Other route", Status: domain.KnowledgeVerified, Confidence: .9, CreatedAt: now, UpdatedAt: now},
	}
	for _, entry := range entries {
		if err := database.CreateKnowledge(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	applicable, err := database.ListApplicableKnowledge(ctx, project.ID, line.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
	if err != nil || len(applicable) != 2 {
		t.Fatalf("applicable knowledge = %#v, %v", applicable, err)
	}
	updated, err := database.FeedbackKnowledge(ctx, "knowledge-main", "helped", now.Add(time.Second).Format(time.RFC3339Nano))
	if err != nil || updated.HelpedCount != 1 || updated.FeedbackState != "helped" {
		t.Fatalf("helped feedback = %#v, %v", updated, err)
	}
	updated, err = database.FeedbackKnowledge(ctx, "knowledge-main", "stale", now.Add(2*time.Second).Format(time.RFC3339Nano))
	if err != nil || updated.StaleCount != 1 || updated.Status != domain.KnowledgeStale {
		t.Fatalf("stale feedback = %#v, %v", updated, err)
	}
	skill.Description = "Updated"
	skill.Version = 2
	skill.Enabled = false
	skill.UpdatedAt = now.Add(time.Second)
	if err := database.UpdateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	if item, err := database.Skill(ctx, skill.ID); err != nil || item.Enabled || item.Version != 2 {
		t.Fatalf("updated skill = %#v, %v", item, err)
	}
}
