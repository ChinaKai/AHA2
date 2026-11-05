package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func openMailTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// createMailTaskFixture builds the minimal Project/Workspace/snapshot/Task
// chain required by the mail foreign keys.
func createMailTaskFixture(t *testing.T, database *Store) (taskID, projectID, workspaceID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := domain.NewID("mailtest")
	project := domain.Project{
		ID: "project_" + suffix, Name: "Mail project",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace_" + suffix, ProjectID: project.ID, Name: "Mail workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{
		ID: "provider_" + suffix, Name: "Mail provider", BaseURL: "https://example.test/v1",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{
		ID: "env_" + suffix, Name: "Mail env", ProviderID: provider.ID,
		Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model_" + suffix, DisplayName: "Mail model", ProviderID: provider.ID,
		Backend: "codex", WireModel: "model", DefaultEnvGroupID: group.ID,
		Capabilities: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "snapshot_" + suffix, WorkspaceID: workspace.ID, Backend: "codex",
		ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: group.ID,
		EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{
		ID: "task_" + suffix, ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Mail task", OriginalRequest: "test", CurrentGoal: "test",
		Status: domain.TaskDraft, RuntimeConfigSnapshotID: snapshot.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	return task.ID, project.ID, workspace.ID
}

func createMailAccountFixture(t *testing.T, database *Store, name, address string) domain.MailAccount {
	t.Helper()
	id := domain.NewID("mail_account")
	account, err := database.CreateMailAccount(context.Background(), domain.MailAccount{
		ID: id, Name: name, Address: address, DisplayName: name,
		CredentialRef:        domain.MailAccountCredentialRef(id),
		CredentialConfigured: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func createMailMessageFixture(t *testing.T, database *Store, taskID, accountID, idempotencyKey string) domain.MailMessage {
	t.Helper()
	message, err := database.CreateMailMessage(context.Background(), domain.MailMessage{
		ID:             domain.NewID("mail_message"),
		TaskID:         taskID,
		AccountID:      accountID,
		Source:         domain.MailMessageSourceAgent,
		IdempotencyKey: idempotencyKey,
		FromAddress:    "noreply@example.com",
		FromName:       "AHA",
		To:             []string{"qa@example.com"},
		Subject:        "Build ready",
		BodyText:       "Build ready",
		Status:         domain.MailMessageDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}
