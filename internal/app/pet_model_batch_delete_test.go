package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func enablePetModelManagement(t *testing.T, fixture *petTaskFixture) {
	t.Helper()
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowManageModels: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
}

func createPetTestModel(t *testing.T, fixture *petTaskFixture, name string) string {
	t.Helper()
	model, err := fixture.service.PetCreateModel(fixture.ctx, PetModelInput{
		DisplayName: name, ProviderID: fixture.provider.ID, Backend: "codex",
		WireModel: "wire-" + strings.ToLower(strings.ReplaceAll(name, " ", "-")), WireAPI: "chat",
		ContextWindow: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return model.ID
}

func TestPetBatchDeleteModelsCreatesOneApprovalAndDeletesAtomically(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetModelManagement(t, fixture)
	modelA := createPetTestModel(t, fixture, "批量模型 A")
	modelB := createPetTestModel(t, fixture, "批量模型 B")

	if _, err := fixture.service.PetRequestDeleteModels(fixture.ctx, []string{modelA, modelB}); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("batch delete without reason was accepted: %v", err)
	}
	if _, err := fixture.service.PetRequestDeleteModels(fixture.ctx, []string{"model-missing"}, "清理不用的批量模型测试"); err == nil || !strings.Contains(err.Error(), "model-missing") {
		t.Fatalf("batch delete with an unknown model was accepted: %v", err)
	}

	approval, err := fixture.service.PetRequestDeleteModels(fixture.ctx, []string{modelA, modelB}, "清理不用的批量模型测试")
	if err != nil {
		t.Fatal(err)
	}
	if approval.Kind != "delete_models" || approval.Status != petApprovalPending {
		t.Fatalf("approval=%#v", approval)
	}
	var payload petModelDeleteBatchPayload
	if err := json.Unmarshal([]byte(approval.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || len(payload.ModelIDs) != 2 || payload.ModelIDs[0] != modelA || payload.ModelIDs[1] != modelB {
		t.Fatalf("payload=%#v", payload)
	}
	if len(payload.ModelNames) != 2 || payload.ModelNames[0] != "批量模型 A" || payload.ModelNames[1] != "批量模型 B" {
		t.Fatalf("payload names=%#v", payload.ModelNames)
	}
	if !strings.Contains(approval.Summary, "2 个模型") || !strings.Contains(approval.Summary, "批量模型 A") || !strings.Contains(approval.Summary, "批量模型 B") {
		t.Fatalf("summary does not show the batch size and names: %q", approval.Summary)
	}

	// Repeating the same request (even with a different order) reuses the one
	// pending approval instead of creating a second card.
	again, err := fixture.service.PetRequestDeleteModels(fixture.ctx, []string{modelB, modelA}, "再次提交同一批删除请求")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != approval.ID {
		t.Fatalf("duplicate batch request created a new approval: %s vs %s", again.ID, approval.ID)
	}
	pending, err := fixture.database.ListPendingPetApprovalsByKind(fixture.ctx, "delete_models")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected exactly one pending batch approval, got %d", len(pending))
	}

	resolved, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != petApprovalApproved {
		t.Fatalf("resolved=%#v", resolved)
	}
	for _, id := range []string{modelA, modelB} {
		if _, err := fixture.database.Model(fixture.ctx, id); err == nil {
			t.Fatalf("approved batch delete left model %s visible", id)
		}
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err == nil || !strings.Contains(err.Error(), "已经处理过") {
		t.Fatalf("double approve was accepted: %v", err)
	}
}

func TestPetBatchDeleteModelsFailsWholeBatchWhenOneModelIsGone(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetModelManagement(t, fixture)
	modelC := createPetTestModel(t, fixture, "批量模型 C")
	modelD := createPetTestModel(t, fixture, "批量模型 D")

	approval, err := fixture.service.PetRequestDeleteModels(fixture.ctx, []string{modelC, modelD}, "清理不用的批量模型测试")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the model disappearing between the request and the approval.
	if err := fixture.database.DeleteModel(fixture.ctx, modelD); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err == nil || !strings.Contains(err.Error(), "模型不存在或已被删除") {
		t.Fatalf("partial batch delete did not report the missing model: %v", err)
	}
	if _, err := fixture.database.Model(fixture.ctx, modelC); err != nil {
		t.Fatalf("failed batch delete partially removed a valid model: %v", err)
	}
	stored, err := fixture.database.PetApproval(fixture.ctx, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != petApprovalPending {
		t.Fatalf("failed batch approval should stay pending, got %s", stored.Status)
	}
}
