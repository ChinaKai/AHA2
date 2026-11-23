package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPetOptInRequiredAfterModelChangesAndRestart(t *testing.T) {
	for _, version := range []struct {
		name   string
		legacy int
	}{
		{name: "fresh"},
		{name: "v158", legacy: 158},
		{name: "v159", legacy: 159},
	} {
		t.Run(version.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "aha2.db")
			db, service := openPetOptInTestService(t, path)
			if version.legacy != 0 {
				if _, err := db.EnsureOwnerAssistant(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				closePetOptInTestService(t, service, db)
				seedLegacyPetOptIn(t, path, version.legacy, true)
				db, service = openPetOptInTestService(t, path)
			}

			assertOff := func(stage, modelID string) {
				t.Helper()
				view, err := service.OwnerAssistant(ctx)
				if err != nil || view.Assistant.PetEnabled || view.PetModelID != modelID ||
					view.PetModelAvailable != (modelID != "") {
					t.Fatalf("%s: assistant readback enabled=%v model=%q available=%v err=%v",
						stage, view.Assistant.PetEnabled, view.PetModelID, view.PetModelAvailable, err)
				}
				assertPetOptInTestDisabled(t, service, stage)
				if _, err := db.Task(ctx, petTaskID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("%s: expected no pet Task, got %v", stage, err)
				}
			}
			assertOff("before adding a model", "")

			now := time.Now().UTC()
			provider := domain.Provider{ID: "provider", Name: "Provider", BaseURL: "http://127.0.0.1:1", CreatedAt: now, UpdatedAt: now}
			if err := db.UpsertProvider(ctx, provider); err != nil {
				t.Fatal(err)
			}
			model := domain.Model{
				ID: "first_model", DisplayName: "First model", Backend: "codex", ProviderID: provider.ID,
				Source: domain.ModelSourceProvider, WireModel: "first", WireAPI: "responses", CreatedAt: now, UpdatedAt: now,
			}
			if err := db.UpsertModel(ctx, model); err != nil {
				t.Fatal(err)
			}
			assertOff("after adding a model", model.ID)

			replacedID := model.ID
			model.ID, model.WireModel = "replacement_model", "replacement"
			if err := db.UpsertModel(ctx, model); err != nil {
				t.Fatal(err)
			}
			if err := db.DeleteModel(ctx, replacedID); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateOwnerAssistantModel(ctx, ownerAssistantID, model.ID, now); err != nil {
				t.Fatal(err)
			}
			assertOff("after replacing the model", model.ID)

			closePetOptInTestService(t, service, db)
			db, service = openPetOptInTestService(t, path)
			assertOff("after restart", model.ID)

			view, err := service.UpdateOwnerPetSettings(ctx, PetSettingsInput{Enabled: true, Actions: []string{"walk"}})
			if err != nil || !view.Assistant.PetEnabled || view.Assistant.PetTaskID != petTaskID {
				t.Fatalf("explicit opt-in: %+v, %v", view.Assistant, err)
			}
			waitForTask(t, db, petTaskID, domain.TaskWaitingUser)
			turns, err := db.ListTurns(ctx, petTaskID)
			if err != nil || len(turns) != 1 || turns[0].Status != domain.TurnSucceeded {
				t.Fatalf("explicit opt-in should run one introduction: %+v, %v", turns, err)
			}
		})
	}
}

func TestPetLegacyUpgradePreservesExistingTaskSetting(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
	}{
		{name: "enabled", enabled: true},
		{name: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPetTaskFixtureWithEnabled(t, &stubExecutor{}, false)
			task := fixture.createPetDraft(t, "Legacy introduction")
			if _, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			closePetOptInTestService(t, fixture.service, fixture.database)
			seedLegacyPetOptIn(t, fixture.databasePath, 158, test.enabled)
			db, service := openPetOptInTestService(t, fixture.databasePath)

			view, err := service.OwnerAssistant(fixture.ctx)
			if err != nil || view.Assistant.PetEnabled != test.enabled {
				t.Fatalf("upgraded assistant: %+v, %v", view.Assistant, err)
			}
			reloaded, err := db.Task(fixture.ctx, petTaskID)
			if err != nil || reloaded.Code != task.Code {
				t.Fatalf("legacy pet Task was not preserved: %+v, %v", reloaded, err)
			}
			if test.enabled {
				waitForTask(t, db, petTaskID, domain.TaskWaitingUser)
			} else {
				assertPetOptInTestDisabled(t, service, "legacy disabled pet")
				if count, err := db.TaskTurnCount(fixture.ctx, petTaskID); err != nil || count != 0 {
					t.Fatalf("disabled legacy pet created %d Turns, err=%v", count, err)
				}
			}
		})
	}
}

func assertPetOptInTestDisabled(t *testing.T, service *Service, stage string) {
	t.Helper()
	ctx := context.Background()
	// Make the first scheduler tick eligible to run if the guard is broken.
	if service.petBrainAt == nil {
		service.petBrainAt = make(map[string]time.Time)
	}
	service.petBrainAt["server_autonomy_schedule"] = service.now().Add(-24 * time.Hour)
	service.runPetAutonomyOnce(ctx)
	if _, err := service.AskOwnerPetBrain(ctx, PetBrainContext{DeviceID: stage, IdleSeconds: 900}); !errors.Is(err, errPetDisabled) {
		t.Fatalf("%s: direct autonomy request = %v, want pet disabled", stage, err)
	}
}

func openPetOptInTestService(t *testing.T, path string) (*store.Store, *Service) {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, nil, &stubExecutor{})
	t.Cleanup(func() {
		_ = service.Shutdown(context.Background())
		_ = db.Close()
	})
	return db, service
}

func closePetOptInTestService(t *testing.T, service *Service, db *store.Store) {
	t.Helper()
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func seedLegacyPetOptIn(t *testing.T, path string, version int, enabled bool) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE owner_assistants SET pet_enabled=? WHERE id=?`, enabled, ownerAssistantID); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM schema_migrations WHERE version>?`, version); err != nil {
		t.Fatal(err)
	}
	if version == 158 {
		if _, err := raw.Exec(`ALTER TABLE general_settings DROP COLUMN onboarding_dismissed_at`); err != nil {
			t.Fatal(err)
		}
	}
}
