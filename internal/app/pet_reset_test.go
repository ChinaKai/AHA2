package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestResetOwnerPetRequiresConfirmationAndClearsPetData(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "factory reset test")

	// The pet project can also contain a read-only Workspace mirror owned by
	// another device. Reset must not try to tombstone that remote-owned row.
	mirror := domain.Workspace{
		ID: "workspace_pet_mirror", ProjectID: petProjectID, Name: "pet-workspace",
		Locality: "remote", Transport: "native", Health: "remote", ReadOnly: true,
		OwnerDeviceID: "work-windows", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, mirror); err != nil {
		t.Fatal(err)
	}

	workspace, err := fixture.database.Workspace(fixture.ctx, petWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"core/identity.md", "diary/2026-11-01.md", "worklog/2026-11-01.md"} {
		path := filepath.Join(workspace.RootPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("pet data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetIdentity(fixture.ctx, assistant.ID, "identity", "principles", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.AppendAssistantMessage(fixture.ctx, domain.AssistantMessage{
		ID: "message-reset", AssistantID: assistant.ID, Role: "user", Content: "hello", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "test", Title: "approval", Summary: "summary", Risk: "low",
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpsertPetTaskWatch(fixture.ctx, petTaskID, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.EnsurePetState(fixture.ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.ResetOwnerPet(fixture.ctx, "wrong"); err == nil {
		t.Fatal("reset accepted the wrong confirmation")
	}
	if _, err := fixture.database.Task(fixture.ctx, petTaskID); err != nil {
		t.Fatalf("wrong confirmation removed the pet task: %v", err)
	}

	result, err := fixture.service.ResetOwnerPet(fixture.ctx, petResetConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if result.Tasks != 1 || result.Workspaces != 1 || result.Approvals != 1 || result.Watches != 1 || result.Messages != 1 {
		t.Fatalf("unexpected reset counts: %#v", result)
	}

	if _, err := fixture.database.Task(fixture.ctx, petTaskID); err == nil {
		t.Fatal("pet task still exists")
	}
	if _, err := fixture.database.Project(fixture.ctx, petProjectID); err != nil {
		t.Fatalf("pet project was removed: %v", err)
	}
	if _, err := fixture.database.Workspace(fixture.ctx, petWorkspaceID); err == nil {
		t.Fatal("local pet workspace still exists")
	}
	if _, err := fixture.database.Workspace(fixture.ctx, mirror.ID); err != nil {
		t.Fatalf("read-only pet workspace mirror was removed: %v", err)
	}
	for _, item := range []struct{ objectType, objectID string }{
		{"project", petProjectID}, {"workspace", petWorkspaceID}, {"task", petTaskID},
	} {
		if _, err := fixture.database.SyncTombstone(fixture.ctx, item.objectType, item.objectID); err == nil {
			t.Fatalf("local-only reset emitted a %s tombstone", item.objectType)
		}
	}
	if approvals, err := fixture.database.ListPetApprovals(fixture.ctx, 10); err != nil || len(approvals) != 0 {
		t.Fatalf("pet approvals=%#v err=%v", approvals, err)
	}
	if events, err := fixture.database.ListPetEvents(fixture.ctx, 10); err != nil || len(events) != 0 {
		t.Fatalf("pet events=%#v err=%v", events, err)
	}
	if watches, err := fixture.database.PetTaskWatches(fixture.ctx); err != nil || len(watches) != 0 {
		t.Fatalf("pet watches=%#v err=%v", watches, err)
	}
	if messages, err := fixture.database.AssistantMessages(fixture.ctx, assistant.ID, 10); err != nil || len(messages) != 0 {
		t.Fatalf("assistant messages=%#v err=%v", messages, err)
	}
	for _, relative := range []string{"core/identity.md", "diary/2026-11-01.md", "worklog/2026-11-01.md"} {
		if _, err := os.Stat(filepath.Join(workspace.RootPath, relative)); !os.IsNotExist(err) {
			t.Fatalf("pet file %s still exists (err=%v)", relative, err)
		}
	}

	updated, err := fixture.database.OwnerAssistant(fixture.ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PetEnabled || updated.PetTaskID != "" || updated.PetIdentity != "" || updated.PetPrinciples != "" {
		t.Fatalf("assistant was not reset: %#v", updated)
	}

	view, err := fixture.service.OwnerAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.PetEnabled || view.Assistant.PetTaskID != "" {
		t.Fatalf("disabled pet was recreated: %#v", view.Assistant)
	}
	if _, err := fixture.database.Task(fixture.ctx, petTaskID); err == nil {
		t.Fatal("opening the assistant recreated the pet task")
	}
}
