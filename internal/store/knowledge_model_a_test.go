package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestKnowledgeModelASchemaAndBindings(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var migrated int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=142`).Scan(&migrated); err != nil || migrated != 1 {
		t.Fatalf("schema v142 missing: count=%d err=%v", migrated, err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-space", Name: "Space", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	stored, err := database.Project(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.KnowledgeState != domain.KnowledgeStateUninitialized {
		t.Fatalf("new project knowledge_state = %q, want uninitialized", stored.KnowledgeState)
	}
	if err := database.SetProjectKnowledgeState(ctx, project.ID, domain.KnowledgeStateReady); err != nil {
		t.Fatal(err)
	}
	stored, err = database.Project(ctx, project.ID)
	if err != nil || stored.KnowledgeState != domain.KnowledgeStateReady {
		t.Fatalf("project state after update = %q err=%v", stored.KnowledgeState, err)
	}
	if err := database.SetProjectKnowledgeState(ctx, "missing-project", domain.KnowledgeStateReady); err == nil {
		t.Fatal("setting state on a missing project must report not found")
	}

	other := domain.Project{ID: "project-other", Name: "Other", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, other); err != nil {
		t.Fatal(err)
	}
	instance := []domain.KnowledgeBinding{{SpaceProject: project.ID, AccessMode: "read_write"}}
	replaced, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-1", instance)
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 1 || replaced[0].SpaceProject != project.ID || replaced[0].AccessMode != "read_write" {
		t.Fatalf("instance bindings = %#v", replaced)
	}
	// Endpoint bindings override the instance default, not merge with it.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint,
		ChannelEndpointConsumerID("chan-1", "assistant_dm"),
		[]domain.KnowledgeBinding{{SpaceProject: other.ID}}); err != nil {
		t.Fatal(err)
	}
	resolved, err := database.ResolveChannelKnowledgeBindings(ctx, "chan-1", "assistant_dm")
	if err != nil || len(resolved) != 1 || resolved[0].SpaceProject != other.ID {
		t.Fatalf("endpoint resolution = %#v err=%v", resolved, err)
	}
	resolved, err = database.ResolveChannelKnowledgeBindings(ctx, "chan-1", "group_digital_human")
	if err != nil || len(resolved) != 1 || resolved[0].SpaceProject != project.ID {
		t.Fatalf("instance fallback = %#v err=%v", resolved, err)
	}
	// An empty replacement clears the binding: no binding means an empty
	// knowledge selection, never an implicit host-project grant.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-1", nil); err != nil {
		t.Fatal(err)
	}
	resolved, err = database.ResolveChannelKnowledgeBindings(ctx, "chan-1", "group_digital_human")
	if err != nil || len(resolved) != 0 {
		t.Fatalf("cleared instance binding = %#v err=%v", resolved, err)
	}
}

func TestKnowledgeModelAClassifiesLegacyDedicatedLibrary(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project_2d75fd0e267704c87d20", Name: "Dedicated", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	lookalike := domain.Project{ID: "project_2d75other", Name: "Lookalike", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, lookalike); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=144`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := database.Project(ctx, project.ID)
	if err != nil || stored.ProjectType != "knowledge" {
		t.Fatalf("project type = %q err=%v", stored.ProjectType, err)
	}
	other, err := database.Project(ctx, lookalike.ID)
	if err != nil || other.ProjectType != "folder" {
		t.Fatalf("same-prefix project type = %q err=%v", other.ProjectType, err)
	}
}
