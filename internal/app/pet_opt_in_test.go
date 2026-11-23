package app

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPetExplicitOptInAcrossModelChanges(t *testing.T) {
	block := make(chan struct{})
	fixture := newPetTaskFixtureWithEnabled(t, &stubExecutor{block: block}, false)
	defer func() {
		select {
		case <-block:
		default:
			close(block)
		}
	}()
	ctx, service, db := fixture.ctx, fixture.service, fixture.database

	assertNoTask := func() {
		t.Helper()
		if _, err := db.Task(ctx, petTaskID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("pet Task should not exist, lookup error = %v", err)
		}
	}

	if err := db.DeleteModel(ctx, fixture.model.ID); err != nil {
		t.Fatal(err)
	}
	view, err := service.OwnerAssistant(ctx)
	if err != nil || view.Assistant.PetEnabled || view.PetModelAvailable {
		t.Fatalf("fresh assistant: %+v, %v", view.Assistant, err)
	}
	assertNoTask()

	if err := db.UpsertModel(ctx, fixture.model); err != nil {
		t.Fatal(err)
	}
	view, err = service.OwnerAssistant(ctx)
	if err != nil || view.Assistant.PetEnabled || !view.PetModelAvailable {
		t.Fatalf("assistant with a newly added model: %+v, %v", view.Assistant, err)
	}
	service.runPetAutonomyOnce(ctx)
	assertNoTask()

	view, err = service.UpdateOwnerPetSettings(ctx, PetSettingsInput{Enabled: true, Actions: view.Assistant.PetActions})
	if err != nil || view.Assistant.PetTaskID != petTaskID || fixture.turnCount(t) != 1 {
		t.Fatalf("explicit enable: %+v, %v", view.Assistant, err)
	}
	introduction, err := db.ActiveTurn(ctx, petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, db, introduction.ID, domain.TurnRunning)

	// Queue a wake while the introduction is still running, then disable.
	if err := service.submitPetWake(ctx, "Background wake"); err != nil {
		t.Fatal(err)
	}
	if pending, err := db.PendingAgentInbox(ctx, petTaskID, "main"); err != nil || len(pending) != 1 || pending[0].SourceKind != domain.PetWakeSourceKind {
		t.Fatalf("expected one queued wake: %+v, %v", pending, err)
	}
	view, err = service.UpdateOwnerPetSettings(ctx, PetSettingsInput{Enabled: false, Actions: view.Assistant.PetActions})
	if err != nil || view.Assistant.PetEnabled {
		t.Fatalf("disable: %+v, %v", view.Assistant, err)
	}
	if pending, err := db.PendingAgentInbox(ctx, petTaskID, "main"); err != nil || len(pending) != 0 {
		t.Fatalf("disable left a queued wake: %+v, %v", pending, err)
	}
	if _, err := service.AskOwnerPetBrain(ctx, PetBrainContext{DeviceID: "disabled", IdleSeconds: 900}); !errors.Is(err, errPetDisabled) {
		t.Fatalf("direct brain call after disable = %v", err)
	}
	if err := service.submitPetWake(ctx, "Delayed notification"); !errors.Is(err, errPetDisabled) {
		t.Fatalf("delayed wake after disable = %v", err)
	}

	if err := db.DeleteModel(ctx, fixture.model.ID); err != nil {
		t.Fatal(err)
	}
	replacement := fixture.model
	replacement.ID = "replacement"
	if err := db.UpsertModel(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	view, err = service.UpdateOwnerPetSettings(ctx, PetSettingsInput{
		Enabled: false, Actions: view.Assistant.PetActions, PetModelSource: domain.ModelSourceProvider, PetModelID: replacement.ID,
	})
	if err != nil || view.Assistant.PetEnabled || view.PetModelID != replacement.ID {
		t.Fatalf("disabled settings with replacement model: %+v, %v", view.Assistant, err)
	}
	service.runPetAutonomyOnce(ctx)
	close(block)
	waitForTask(t, db, petTaskID, domain.TaskWaitingUser)
	if task := fixture.petTask(t); task.ID != petTaskID || fixture.turnCount(t) != 1 {
		t.Fatalf("disabled pet did not keep its single Task and Turn: %+v", task)
	}
}

func TestPetOptInWithoutModelSurvivesRestartAndCreatesTaskAfterModelAdded(t *testing.T) {
	fixture := newPetTaskFixtureWithEnabled(t, &stubExecutor{}, false)
	ctx, db := fixture.ctx, fixture.database
	if err := db.DeleteModel(ctx, fixture.model.ID); err != nil {
		t.Fatal(err)
	}
	view, err := fixture.service.UpdateOwnerPetSettings(ctx, PetSettingsInput{Enabled: true, Actions: []string{"walk"}})
	if err != nil || !view.Assistant.PetEnabled || view.Assistant.PetTaskID != "" {
		t.Fatalf("enable without model: %+v, %v", view.Assistant, err)
	}
	if err := db.UpsertModel(ctx, fixture.model); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := NewService(reopened, nil, &stubExecutor{})
	defer restarted.Shutdown(ctx)
	view, err = restarted.OwnerAssistant(ctx)
	if err != nil || !view.Assistant.PetEnabled || view.Assistant.PetTaskID != petTaskID {
		t.Fatalf("saved setting after restart: %+v, %v", view.Assistant, err)
	}
}
