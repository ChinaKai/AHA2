package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

type fakeKnowledgeSyncRecovery struct {
	status     knowledgevault.AutoSyncStatus
	resetCalls int
}

func (f *fakeKnowledgeSyncRecovery) StatusLive(ctx context.Context) knowledgevault.AutoSyncStatus {
	return f.status
}

func (f *fakeKnowledgeSyncRecovery) ResetPause() {
	f.resetCalls++
	f.status.Paused = false
	f.status.PauseReason = ""
	f.status.Conflict = false
	f.status.LastError = ""
}

func decodeKnowledgeSyncRecoveryResult(t *testing.T, raw string) KnowledgeSyncRecoveryResult {
	t.Helper()
	var result KnowledgeSyncRecoveryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode recovery result: %v\n%s", err, raw)
	}
	return result
}

func TestPetRecoverKnowledgeSyncRejectsNonPetTask(t *testing.T) {
	fixture := newPetTaskFixture(t)
	recovery := &fakeKnowledgeSyncRecovery{status: knowledgevault.AutoSyncStatus{
		Enabled: true, Paused: true, PauseReason: "conflict", Conflict: true,
	}}
	fixture.service.SetKnowledgeSyncRecovery(recovery)

	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_recover_knowledge_sync", json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "宠物 Task") {
		t.Fatalf("unidentified recovery err=%v", err)
	}
	if _, err := fixture.service.PetMCPToolForTask(fixture.ctx, "task_someone_else", "aha_recover_knowledge_sync",
		json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "宠物 Task") {
		t.Fatalf("foreign-task recovery err=%v", err)
	}
	if recovery.resetCalls != 0 || !recovery.status.Paused {
		t.Fatalf("rejected recovery changed state: calls=%d status=%+v", recovery.resetCalls, recovery.status)
	}
}

func TestPetRecoverKnowledgeSyncClearsPauseAndReportsBeforeAfter(t *testing.T) {
	fixture := newPetTaskFixture(t)
	recovery := &fakeKnowledgeSyncRecovery{status: knowledgevault.AutoSyncStatus{
		Enabled: true, Paused: true, PauseReason: "conflict", Conflict: true,
		Pending: 3, PendingKnown: true, LastError: "pull failed",
	}}
	fixture.service.SetKnowledgeSyncRecovery(recovery)

	raw, err := fixture.service.PetMCPToolForTask(fixture.ctx, petTaskID, "aha_recover_knowledge_sync", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeKnowledgeSyncRecoveryResult(t, raw)
	if !result.OK || !result.Recovered {
		t.Fatalf("recovery result=%+v", result)
	}
	if recovery.resetCalls != 1 {
		t.Fatalf("ResetPause calls=%d want 1", recovery.resetCalls)
	}
	if !result.Before.Paused || result.Before.PauseReason != "conflict" || !result.Before.Conflict ||
		result.Before.Pending != 3 || !result.Before.PendingKnown || result.Before.LastError != "pull failed" {
		t.Fatalf("before state=%+v", result.Before)
	}
	if result.After.Paused || result.After.PauseReason != "" || result.After.Conflict || result.After.LastError != "" {
		t.Fatalf("after state=%+v", result.After)
	}
	if !result.After.Enabled {
		t.Fatalf("recovery must not disable auto-sync: %+v", result.After)
	}
	if !strings.Contains(result.Note, "再次暂停") || !strings.Contains(result.Note, "通知") {
		t.Fatalf("note=%q", result.Note)
	}
}

func TestPetRecoverKnowledgeSyncIsNoOpWhenNotPaused(t *testing.T) {
	fixture := newPetTaskFixture(t)
	recovery := &fakeKnowledgeSyncRecovery{status: knowledgevault.AutoSyncStatus{
		Enabled: true, Paused: false, Pending: 2, PendingKnown: true,
	}}
	fixture.service.SetKnowledgeSyncRecovery(recovery)

	raw, err := fixture.service.PetMCPToolForTask(fixture.ctx, petTaskID, "aha_recover_knowledge_sync", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeKnowledgeSyncRecoveryResult(t, raw)
	if !result.OK || result.Recovered {
		t.Fatalf("no-op result=%+v", result)
	}
	if recovery.resetCalls != 0 {
		t.Fatalf("no-op called ResetPause %d times", recovery.resetCalls)
	}
	if result.Before != result.After || result.After.Pending != 2 || !result.After.PendingKnown {
		t.Fatalf("no-op changed state: before=%+v after=%+v", result.Before, result.After)
	}
}

func TestPetRecoverKnowledgeSyncKeepsAutoSyncDisabled(t *testing.T) {
	fixture := newPetTaskFixture(t)
	recovery := &fakeKnowledgeSyncRecovery{status: knowledgevault.AutoSyncStatus{
		Enabled: false, Paused: true, PauseReason: "conflict", Conflict: true,
	}}
	fixture.service.SetKnowledgeSyncRecovery(recovery)

	raw, err := fixture.service.PetMCPToolForTask(fixture.ctx, petTaskID, "aha_recover_knowledge_sync", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeKnowledgeSyncRecoveryResult(t, raw)
	if !result.Recovered || result.After.Paused {
		t.Fatalf("recovery result=%+v", result)
	}
	// Clearing the stale pause is allowed, but the owner's switch must stay off:
	// the loop still refuses to run until the owner enables it.
	if result.Before.Enabled || result.After.Enabled {
		t.Fatalf("recovery changed enabled: before=%+v after=%+v", result.Before, result.After)
	}
}

func TestPetRecoverKnowledgeSyncUnavailable(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.PetMCPToolForTask(fixture.ctx, petTaskID, "aha_recover_knowledge_sync",
		json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("unavailable recovery err=%v", err)
	}
}

func TestPetRecoverKnowledgeSyncIsInPetMCPAllowlist(t *testing.T) {
	allowlisted := false
	for _, name := range petMCPAllowedTools(domain.Task{ID: petTaskID}) {
		if name == "mcp__aha2-pet__aha_recover_knowledge_sync" {
			allowlisted = true
			break
		}
	}
	if !allowlisted {
		t.Fatal("recovery tool missing from the pet MCP allowlist")
	}
}
