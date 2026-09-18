package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestListTasksPageWithCreationOrderIsCompleteAndDuplicateFree walks a list one
// page at a time using the returned cursor.
//
// A cursor that compares against a different column than the ORDER BY silently
// skips or repeats rows, and the symptom is a task quietly missing from the UI
// rather than an error. This asserts the union of the pages is exactly the set,
// with creation and update times deliberately in opposite orders so a cursor
// keyed on the wrong column mis-pages.
func TestListTasksPageWithCreationOrderIsCompleteAndDuplicateFree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	project := domain.Project{ID: "project-page", Name: "Page", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: base, UpdatedAt: base}
	workspace := domain.Workspace{ID: "workspace-page", ProjectID: project.ID, Name: "Local", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: base, UpdatedAt: base}
	env := domain.EnvGroup{ID: "env-page", Name: "Page", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretNames: []string{}, CreatedAt: base, UpdatedAt: base}
	model := domain.Model{ID: "model-page", DisplayName: "Page", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: base, UpdatedAt: base}
	snapshot := domain.RuntimeConfigSnapshot{ID: "runtime-page", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: base}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.CreateRuntimeSnapshot(ctx, snapshot) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}

	const total = 25
	expected := map[string]bool{}
	for i := 0; i < total; i++ {
		id := "task-page-" + time.Duration(i).String()
		created := base.Add(time.Duration(i) * time.Minute)
		// Update time runs opposite to creation time: only a creation-time sort and
		// cursor agree with each other.
		updated := base.Add(time.Duration(total-i) * time.Hour)
		task := domain.Task{
			ID: id, ProjectID: project.ID, WorkspaceID: workspace.ID, Title: id,
			OriginalRequest: "r", CurrentGoal: "r", Status: domain.TaskActive,
			Isolation: "inplace", RuntimeConfigSnapshotID: snapshot.ID,
			CreatedAt: created, UpdatedAt: updated,
		}
		if err := database.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		expected[id] = true
	}

	seen := map[string]int{}
	var cursorAt time.Time
	var cursorID string
	for pageCount := 0; pageCount < 20; pageCount++ {
		page, more, err := database.ListTasksPage(ctx, project.ID, cursorAt, cursorID, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page {
			seen[item.ID]++
		}
		if !more || len(page) == 0 {
			break
		}
		last := page[len(page)-1]
		cursorAt, cursorID = last.CreatedAt, last.ID
	}

	if len(seen) != total {
		t.Fatalf("walked %d distinct tasks, want %d", len(seen), total)
	}
	for id := range expected {
		if seen[id] != 1 {
			t.Fatalf("%s appeared %d times across pages, want exactly once", id, seen[id])
		}
	}
}
