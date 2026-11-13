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
	if err != nil || settings.URL != "" || settings.MigrationNote != "" {
		t.Fatalf("default settings=%#v err=%v", settings, err)
	}
	now := time.Now().UTC()
	settings, err = database.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "https://aha.example.test", UpdatedAt: now})
	if err != nil || settings.URL != "https://aha.example.test" {
		t.Fatalf("updated settings=%#v err=%v", settings, err)
	}
	project := domain.Project{ID: "project-agent-api", Name: "Project", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-agent-api", ProjectID: project.ID, Name: "WSL", Locality: "local", Transport: "wsl", RootPath: "/repo",
		AgentAPIMode: "manual", AgentAPIURL: "https://manual.example.test", AgentAPIResolvedURL: "https://manual.example.test",
		AgentAPIStatus: "ready", AgentAPIMigrationNote: "旧版地址已迁移", AgentAPILastCheckedAt: now, Health: "ready", Capabilities: map[string]any{"agent_api": map[string]any{"status": "ready"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	stored, err := database.Workspace(ctx, workspace.ID)
	if err != nil || stored.AgentAPIMode != "manual" || stored.AgentAPIURL != workspace.AgentAPIURL || stored.AgentAPIResolvedURL != workspace.AgentAPIResolvedURL || stored.AgentAPIStatus != "ready" || stored.AgentAPIMigrationNote != workspace.AgentAPIMigrationNote || stored.AgentAPILastCheckedAt.IsZero() {
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

// TestAgentAPISettingsLeaveLegacyStdioColumnUntouched covers a row written while
// SSH stdio was an opt-in switch. The value is no longer read, so the upgraded
// backend uses stdio regardless; the column keeps its old value so a downgrade
// does not silently re-enable a transport the operator had switched off.
func TestAgentAPISettingsLeaveLegacyStdioColumnUntouched(t *testing.T) {
	t.Parallel()
	for _, legacy := range []int{0, 1} {
		ctx := context.Background()
		database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE agent_api_settings SET stdio_proxy=? WHERE id=1`, legacy); err != nil {
			database.Close()
			t.Fatal(err)
		}
		settings, err := database.AgentAPISettings(ctx)
		if err != nil || settings.URL != "" {
			database.Close()
			t.Fatalf("settings=%#v err=%v", settings, err)
		}
		if _, err := database.UpdateAgentAPISettings(ctx, domain.AgentAPISettings{URL: "https://aha.example.test"}); err != nil {
			database.Close()
			t.Fatal(err)
		}
		var stdioProxy int
		if err := database.db.QueryRowContext(ctx, `SELECT stdio_proxy FROM agent_api_settings WHERE id=1`).Scan(&stdioProxy); err != nil {
			database.Close()
			t.Fatal(err)
		}
		if stdioProxy != legacy {
			database.Close()
			t.Fatalf("legacy stdio_proxy=%d was rewritten to %d", legacy, stdioProxy)
		}
		database.Close()
	}
}
