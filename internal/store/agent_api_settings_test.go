package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestAgentAPISettingsAndWorkspaceDetectionPersist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err := database.AgentAPISettings(ctx)
	if err != nil || settings.URL != "" || settings.AllowInsecure {
		t.Fatalf("default settings=%#v err=%v", settings, err)
	}
	now := time.Now().UTC()
	settings, err = database.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "https://aha.example.test", AllowInsecure: true, UpdatedAt: now})
	if err != nil || settings.URL != "https://aha.example.test" || !settings.AllowInsecure {
		t.Fatalf("updated settings=%#v err=%v", settings, err)
	}
	project := domain.Project{ID: "project-agent-api", Name: "Project", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-agent-api", ProjectID: project.ID, Name: "WSL", Locality: "local", Transport: "wsl", RootPath: "/repo",
		AgentAPIMode: "manual", AgentAPIURL: "https://manual.example.test", AgentAPIResolvedURL: "https://manual.example.test",
		AgentAPIStatus: "ready", AgentAPILastCheckedAt: now, Health: "ready", Capabilities: map[string]any{"agent_api": map[string]any{"status": "ready"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	stored, err := database.Workspace(ctx, workspace.ID)
	if err != nil || stored.AgentAPIMode != "manual" || stored.AgentAPIURL != workspace.AgentAPIURL || stored.AgentAPIResolvedURL != workspace.AgentAPIResolvedURL || stored.AgentAPIStatus != "ready" || stored.AgentAPILastCheckedAt.IsZero() {
		t.Fatalf("stored workspace=%#v err=%v", stored, err)
	}
	if err := database.ResetWorkspaceAgentAPIDetection(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err = database.Workspace(ctx, workspace.ID)
	if err != nil || stored.AgentAPIStatus != "unknown" || stored.AgentAPIResolvedURL != "" || !stored.AgentAPILastCheckedAt.IsZero() || stored.Capabilities["agent_api"] != nil {
		t.Fatalf("reset workspace=%#v err=%v", stored, err)
	}
}
