package store

import (
	"context"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestOnboardingVisibilityUpgradeAndDismissal(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"fresh", "pet", "project", "task", "model", "mirror"} {
		t.Run(kind, func(t *testing.T) {
			db, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Now().UTC()
			project := domain.Project{ID: "project_" + kind, Name: kind, ProjectType: "system", CreatedAt: now, UpdatedAt: now}
			if kind == "project" {
				project.ProjectType = "folder"
			}
			if kind == "pet" {
				project.ID = "project_pet"
			}
			if err := db.CreateProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			workspace := domain.Workspace{ID: "workspace", ProjectID: project.ID, Name: kind, Locality: "local", Transport: "native",
				RootPath: t.TempDir(), ReadOnly: kind == "mirror", CreatedAt: now, UpdatedAt: now}
			if err := db.CreateWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			if kind == "model" || kind == "pet" || kind == "task" {
				if err := db.UpsertProvider(ctx, domain.Provider{ID: "provider", Name: kind, CreatedAt: now, UpdatedAt: now}); err == nil {
					err = db.UpsertModel(ctx, domain.Model{ID: "model", ProviderID: "provider", DisplayName: kind, Backend: "codex", CreatedAt: now, UpdatedAt: now})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "pet" || kind == "task" {
				if err := db.UpsertEnvGroup(ctx, domain.EnvGroup{ID: "env", ProviderID: "provider", Name: "Env", Backend: "codex",
					CreatedAt: now, UpdatedAt: now, Revision: 1}); err != nil {
					t.Fatal(err)
				}
				snapshot := domain.RuntimeConfigSnapshot{ID: "runtime", WorkspaceID: workspace.ID, ModelID: "model", EnvGroupID: "env", CreatedAt: now}
				_, err := db.CreateTaskWithSnapshot(ctx, snapshot, domain.Task{ID: "task_" + kind, ProjectID: project.ID, WorkspaceID: workspace.ID,
					RuntimeConfigSnapshotID: snapshot.ID, Title: kind, Status: domain.TaskDraft, CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.DeleteModel(ctx, "model"); err != nil {
					t.Fatal(err)
				}
			}
			// Restore the schema as it was immediately before v159.
			if _, err := db.db.ExecContext(ctx, `ALTER TABLE general_settings DROP COLUMN onboarding_dismissed_at;
				DELETE FROM schema_migrations WHERE version=159`); err != nil {
				t.Fatal(err)
			}
			if err := db.migrateOnboarding(ctx); err != nil {
				t.Fatal(err)
			}
			if kind == "pet" {
				assistant, err := db.EnsureOwnerAssistant(ctx, now)
				if err != nil || !assistant.PetEnabled {
					t.Fatalf("legacy pet Task lost its enabled state: %+v, %v", assistant, err)
				}
			}
			at, automatic, err := db.OnboardingVisibility(ctx)
			wantAuto := kind == "fresh" || kind == "pet"
			if err != nil || automatic != wantAuto || (at == nil) != wantAuto {
				t.Fatalf("visibility for %s = %v, %v, %v", kind, at, automatic, err)
			}
			if wantAuto {
				if err := db.DismissOnboarding(ctx, now); err != nil {
					t.Fatal(err)
				}
				if err := db.DismissOnboarding(ctx, now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				at, automatic, err := db.OnboardingVisibility(ctx)
				if err != nil || automatic || at == nil || !at.Equal(now) {
					t.Fatalf("dismissal = %v, %v, %v", at, automatic, err)
				}
			}
		})
	}
}

func TestOnboardingSyncMirrorArrivingAfterUpgradeSuppressesAutomaticCard(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if err := db.UpsertRemoteTaskObject(ctx, RemoteTaskObject{
		OwnerDeviceID: "remote", ObjectType: "task", ObjectID: "task_remote", TaskID: "task_remote",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	at, automatic, err := db.OnboardingVisibility(ctx)
	if err != nil || automatic || at != nil {
		t.Fatalf("remote mirror visibility = %v, %v, %v", at, automatic, err)
	}
}
