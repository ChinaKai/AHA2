package store

import (
	"context"
	"database/sql"
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
	replaced, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-1", instance, boolPointer(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 1 || replaced[0].SpaceProject != project.ID || replaced[0].AccessMode != "read_write" {
		t.Fatalf("instance bindings = %#v", replaced)
	}
	// Endpoint bindings override the instance default, not merge with it.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint,
		ChannelEndpointConsumerID("chan-1", "assistant_dm"),
		[]domain.KnowledgeBinding{{SpaceProject: other.ID}}, boolPointer(false)); err != nil {
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
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-1", nil, boolPointer(false)); err != nil {
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

func TestKnowledgeGlobalBindingMigrationAndResolution(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var migrated int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=146`).Scan(&migrated); err != nil || migrated != 1 {
		t.Fatalf("schema v146 missing: count=%d err=%v", migrated, err)
	}
	endpointID := ChannelEndpointConsumerID("chan-global", "group_digital_human")
	// Instance on, endpoint absent: the group falls back to the instance value.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-global", nil, boolPointer(true)); err != nil {
		t.Fatal(err)
	}
	enabled, found, err := database.KnowledgeGlobalBinding(ctx, domain.KnowledgeBindingChannelInstance, "chan-global")
	if err != nil || !found || !enabled {
		t.Fatalf("instance global = enabled:%v found:%v err=%v", enabled, found, err)
	}
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, "chan-global", "group_digital_human"); err != nil || !enabled {
		t.Fatalf("resolved instance fallback = %v err=%v", enabled, err)
	}
	// An explicit endpoint off overrides the instance default.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint, endpointID, nil, boolPointer(false)); err != nil {
		t.Fatal(err)
	}
	enabled, err = database.ResolveChannelKnowledgeGlobalBinding(ctx, "chan-global", "group_digital_human")
	if err != nil || enabled {
		t.Fatalf("endpoint off did not override instance: enabled=%v err=%v", enabled, err)
	}
	// Turning it back on at the endpoint keeps the instance row untouched.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint, endpointID, nil, boolPointer(true)); err != nil {
		t.Fatal(err)
	}
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, "chan-global", "group_digital_human"); err != nil || !enabled {
		t.Fatalf("endpoint on = %v err=%v", enabled, err)
	}
	// No rows at all is an explicit empty selection, not an error.
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, "chan-none", "group_digital_human"); err != nil || enabled {
		t.Fatalf("unconfigured global = %v err=%v", enabled, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopening replays the migration guard without duplicating rows and keeps
	// the stored selection.
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=146`).Scan(&migrated); err != nil || migrated != 1 {
		t.Fatalf("schema v146 replay: count=%d err=%v", migrated, err)
	}
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, "chan-global", "group_digital_human"); err != nil || !enabled {
		t.Fatalf("global selection after reopen = %v err=%v", enabled, err)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestKnowledgeGlobalBindingNilPreservesAndGroupReplacementClearsInstance(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-group-atomic", Name: "Atomic", ProjectType: "folder", KnowledgeState: domain.KnowledgeStateReady, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	endpointID := ChannelEndpointConsumerID("chan-atomic", domain.ChannelEndpointGroupDigitalHuman)
	// An omitted include_global must preserve the existing row.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint, endpointID, nil, boolPointer(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint, endpointID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, "chan-atomic", domain.ChannelEndpointGroupDigitalHuman); err != nil || !enabled {
		t.Fatalf("nil include_global did not preserve the row: enabled=%v err=%v", enabled, err)
	}
	// Seed an instance default, then save a global-only group selection.
	if _, err := database.ReplaceKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-atomic",
		[]domain.KnowledgeBinding{{SpaceProject: project.ID}}, boolPointer(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplaceChannelGroupKnowledgeBindings(ctx, "chan-atomic", nil, true); err != nil {
		t.Fatal(err)
	}
	instanceBindings, err := database.ListKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, "chan-atomic")
	if err != nil || len(instanceBindings) != 0 {
		t.Fatalf("instance bindings after atomic save = %#v err=%v", instanceBindings, err)
	}
	if _, found, err := database.KnowledgeGlobalBinding(ctx, domain.KnowledgeBindingChannelInstance, "chan-atomic"); err != nil || found {
		t.Fatalf("instance global row after atomic save: found=%v err=%v", found, err)
	}
	selection, err := database.ResolveChannelKnowledgeBindingSelection(ctx, "chan-atomic", domain.ChannelEndpointGroupDigitalHuman)
	if err != nil || len(selection.Bindings) != 0 || !selection.IncludeGlobal {
		t.Fatalf("atomic global-only selection = %#v err=%v", selection, err)
	}
}

func TestKnowledgeGlobalBindingMigrationFromV145(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a database that stopped at v145: the table and version row from
	// v146 are absent, and reopening must apply exactly one migration.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DROP TABLE knowledge_global_bindings`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=146`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrated, tableCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=146`).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='knowledge_global_bindings'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if migrated != 1 || tableCount != 1 {
		t.Fatalf("v146 upgrade = migrations:%d tables:%d", migrated, tableCount)
	}
}
