package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestCreateTaskSelectsOfficialCodexModelFromAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateProject(ctx, domain.Project{
		ID: "project-official", Name: "Official", ProjectType: "folder", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-official", ProjectID: "project-official", Name: "Official", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertCodexAccount(ctx, domain.CodexAccount{
		ID: "account-official", Label: "Work", Status: "ready", CredentialRef: "codex/account/auth",
		CredentialConfigured: true, AvailableModels: []domain.CodexModelOption{{
			WireModel: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", MaxContextWindow: 1050000,
			DefaultEffort: "high", ReasoningEfforts: []string{"medium", "high"},
		}}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: "project-official", WorkspaceID: "workspace-official", Title: "Official", Request: "test",
		Isolation: "inplace", Backend: "codex", ModelSource: domain.ModelSourceOfficial,
		CodexAccountID: "account-official", WireModel: "gpt-5.6-sol",
		ReasoningEffort: "high", Filesystem: "workspace-write", Approval: "never",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CodexAccountID != "account-official" || snapshot.WireModel != "gpt-5.6-sol" || snapshot.Backend != "codex" {
		t.Fatalf("unexpected official runtime snapshot: %#v", snapshot)
	}
	model, err := database.Model(ctx, snapshot.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if model.Source != domain.ModelSourceOfficial || model.CodexAccountID != "" || model.ContextWindow != 1050000 {
		t.Fatalf("unexpected hidden runtime model: %#v", model)
	}
}

func TestOfficialCodexSelectionRejectsModelOutsideAccountCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.UpsertCodexAccount(ctx, domain.CodexAccount{
		ID: "account-limited", Status: "ready", CredentialRef: "codex/account/auth", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-5.6-sol"}}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	if _, _, _, err := service.resolveRuntimeSelection(ctx, runtimeSelectionInput{
		Backend: "codex", ModelSource: domain.ModelSourceOfficial,
		CodexAccountID: "account-limited", WireModel: "gpt-not-available",
	}); err == nil {
		t.Fatal("expected unavailable account model to be rejected")
	}
}
