package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
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

// requestModelBatchDelete must validate before it creates anything: a missing
// model leaves no pending approval behind.
func TestPetBatchDeleteModelsCreatesNoApprovalWhenAnyModelIsMissing(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetModelManagement(t, fixture)
	modelA := createPetTestModel(t, fixture, "批量模型 存在")
	if _, err := fixture.service.RequestModelBatchDelete(fixture.ctx, []string{modelA, "model-missing"}); err == nil || !strings.Contains(err.Error(), "model-missing") {
		t.Fatalf("mixed valid/missing batch was accepted: %v", err)
	}
	if _, err := fixture.database.Model(fixture.ctx, modelA); err != nil {
		t.Fatalf("rejected batch deleted a valid model: %v", err)
	}
	pending, err := fixture.database.ListPendingPetApprovalsByKind(fixture.ctx, "delete_models")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("rejected batch left %d pending approvals", len(pending))
	}
}

func TestPetBatchDeleteModelsRejectsMoreThanOneHundredIDs(t *testing.T) {
	fixture := newPetTaskFixture(t)
	ids := make([]string, 0, 101)
	for index := 0; index < 101; index++ {
		ids = append(ids, fmt.Sprintf("model-batch-%03d", index))
	}
	if _, err := fixture.service.RequestModelBatchDelete(fixture.ctx, ids); err == nil || !strings.Contains(err.Error(), "100") {
		t.Fatalf("over-limit batch was accepted: %v", err)
	}
}

// Eight concurrent submissions of the same set must still produce one card.
// This is the regression test for the list-then-create race.
func TestPetBatchDeleteModelsConcurrentSubmissionsCreateOnePendingApproval(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetModelManagement(t, fixture)
	modelA := createPetTestModel(t, fixture, "并发模型 A")
	modelB := createPetTestModel(t, fixture, "并发模型 B")
	const goroutines = 8
	approvalIDs := make([]string, goroutines)
	errs := make([]error, goroutines)
	var wait sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			// Opposite orders prove the dedupe key sorts the id set.
			ids := []string{modelA, modelB}
			if index%2 == 1 {
				ids = []string{modelB, modelA}
			}
			approval, err := fixture.service.RequestModelBatchDelete(fixture.ctx, ids)
			approvalIDs[index], errs[index] = approval.ID, err
		}(index)
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", index, err)
		}
	}
	for index := 1; index < goroutines; index++ {
		if approvalIDs[index] != approvalIDs[0] {
			t.Fatalf("concurrent submissions returned different approvals: %q vs %q", approvalIDs[index], approvalIDs[0])
		}
	}
	pending, err := fixture.database.ListPendingPetApprovalsByKind(fixture.ctx, "delete_models")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("concurrent submissions created %d pending approvals, want 1", len(pending))
	}
}

func batchAuditRows(t *testing.T, databasePath, action string) []map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data_json FROM audit_events WHERE action=? ORDER BY sequence`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatalf("audit data %q: %v", raw, err)
		}
		result = append(result, data)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// The approved batch must remove the env group and its per-env secret, and the
// execution audit must name the model ids and the cleanup result.
func TestPetBatchDeleteModelsCleansEnvGroupSecretAndAuditsExecution(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetModelManagement(t, fixture)
	now := time.Now().UTC()
	group := domain.EnvGroup{
		ID: "env-batch-app", Name: "Batch App", ProviderID: fixture.provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{"TOKEN": "env/env-batch-app/TOKEN"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertEnvGroup(fixture.ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.secrets.PutMany(map[string]string{"env/env-batch-app/TOKEN": "secret-value"}); err != nil {
		t.Fatal(err)
	}
	modelA, err := fixture.service.PetCreateModel(fixture.ctx, PetModelInput{
		DisplayName: "清理模型 A", ProviderID: fixture.provider.ID, Backend: "codex",
		WireModel: "wire-cleanup-a", WireAPI: "chat", DefaultEnvGroupID: group.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelB, err := fixture.service.PetCreateModel(fixture.ctx, PetModelInput{
		DisplayName: "清理模型 B", ProviderID: fixture.provider.ID, Backend: "codex",
		WireModel: "wire-cleanup-b", WireAPI: "chat", DefaultEnvGroupID: group.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.service.RequestModelBatchDelete(fixture.ctx, []string{modelA.ID, modelB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	groups, err := fixture.database.ListEnvGroups(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range groups {
		if item.ID == group.ID {
			t.Fatal("approved batch delete left its env group visible")
		}
	}
	if _, ok := fixture.service.secrets.Get("env/env-batch-app/TOKEN"); ok {
		t.Fatal("approved batch delete left its per-env secret behind")
	}
	audits := batchAuditRows(t, fixture.databasePath, "model.batch_delete")
	if len(audits) != 1 {
		t.Fatalf("model.batch_delete audit rows = %d, want 1", len(audits))
	}
	ids, _ := audits[0]["model_ids"].([]any)
	if len(ids) != 2 || ids[0] != modelA.ID || ids[1] != modelB.ID {
		t.Fatalf("audit model_ids = %#v", audits[0]["model_ids"])
	}
	if count, _ := audits[0]["count"].(float64); int(count) != 2 {
		t.Fatalf("audit count = %#v", audits[0]["count"])
	}
	auditGroups, _ := audits[0]["deleted_env_group_ids"].([]any)
	if len(auditGroups) != 1 || auditGroups[0] != group.ID {
		t.Fatalf("audit deleted_env_group_ids = %#v", audits[0]["deleted_env_group_ids"])
	}
	if deleted, _ := audits[0]["deleted_secret_count"].(float64); int(deleted) != 1 {
		t.Fatalf("audit deleted_secret_count = %#v", audits[0]["deleted_secret_count"])
	}
}
