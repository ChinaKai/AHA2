package sync

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func seedSyncExportTask(t testing.TB, database *store.Store) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-sync-export", Name: "Sync", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-sync-export", ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{
		ID: "env-sync-export", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-sync-export", DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "runtime-sync-export", WorkspaceID: workspace.ID, Backend: "stub",
		ModelID: model.ID, WireModel: "stub", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now,
	}
	task := domain.Task{
		ID: "task-sync-export", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Sync export", OriginalRequest: "request", CurrentGoal: "goal",
		Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now,
	}
	created, err := database.CreateTaskWithSnapshot(ctx, snapshot, task)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func conversationObjects(t *testing.T, objects []domain.SyncObject) []domain.SyncObject {
	t.Helper()
	var result []domain.SyncObject
	for _, object := range objects {
		if object.Type == TypeConversation {
			result = append(result, object)
		}
	}
	return result
}

func TestPendingExportSkipsAppliedConversationItems(t *testing.T) {
	ctx := context.Background()
	database := businessStore(t)
	task := seedSyncExportTask(t, database)
	now := time.Now().UTC()
	for index := 0; index < 3; index++ {
		item, err := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: fmt.Sprintf("conversation_sync_%d", index), TaskID: task.ID,
			Category: "chat", Kind: "user_message", Summary: fmt.Sprintf("summary-%d", index),
			Payload: map[string]any{"runtime_only": "must not sync"}, CreatedAt: now.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		if item.ID == "" {
			t.Fatal("conversation item was not stored")
		}
	}

	full, err := ExportBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	fullConversation := conversationObjects(t, full)
	if len(fullConversation) != 3 {
		t.Fatalf("full conversation objects=%d", len(fullConversation))
	}
	if err := database.MarkSyncApplied(ctx, "default", fullConversation[0], now); err != nil {
		t.Fatal(err)
	}

	pending, err := ExportPendingBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	pendingConversation := conversationObjects(t, pending)
	if len(pendingConversation) != 2 {
		t.Fatalf("pending conversation objects=%d, want 2", len(pendingConversation))
	}
	for _, object := range pendingConversation {
		if object.ID == fullConversation[0].ID {
			t.Fatalf("applied conversation object was exported again: %#v", object)
		}
		if len(object.Payload) == 0 {
			t.Fatalf("pending conversation payload is empty")
		}
	}

	for _, object := range fullConversation {
		if err := database.MarkSyncApplied(ctx, "default", object, now); err != nil {
			t.Fatal(err)
		}
	}
	pending, err = ExportPendingBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	if remaining := conversationObjects(t, pending); len(remaining) != 0 {
		t.Fatalf("fully applied history exported %d conversation objects", len(remaining))
	}
}

// A conversation row can keep its id while its created_at version changes, and
// FinalizeTurnReply does exactly that when it rewrites a placeholder into the
// durable reply. The incremental filter must compare the full id+version
// identity, otherwise the updated summary is never exported again.
func TestPendingExportReexportsConversationWhenCreatedAtChanges(t *testing.T) {
	ctx := context.Background()
	database := businessStore(t)
	task := seedSyncExportTask(t, database)
	now := time.Now().UTC()
	if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
		ID: "conversation_versioned", TaskID: task.ID, TurnID: "turn-versioned",
		Category: "chat", Kind: "agent_message", Summary: "draft", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	full, err := ExportBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	var initial domain.SyncObject
	for _, object := range full {
		if object.ID == "device-sync:conversation_versioned" {
			initial = object
			break
		}
	}
	if initial.IdempotencyKey == "" {
		t.Fatal("seed conversation object was not exported")
	}
	if err := database.MarkSyncApplied(ctx, "default", initial, now); err != nil {
		t.Fatal(err)
	}
	pending, err := ExportPendingBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationObjects(t, pending); len(got) != 0 {
		t.Fatalf("applied conversation exported again: %#v", got)
	}

	finalAt := now.Add(time.Minute)
	if _, err := database.FinalizeTurnReply(ctx, domain.Message{
		ID: "message-versioned", TaskID: task.ID, TurnID: "turn-versioned", Role: "assistant",
		Sender: "main", Content: "final text", CreatedAt: finalAt,
	}, domain.ConversationItem{
		ID: "conversation_versioned", TaskID: task.ID, TurnID: "turn-versioned",
		Category: "chat", Kind: "agent_message", Summary: "final summary", CreatedAt: finalAt,
	}); err != nil {
		t.Fatal(err)
	}
	pending, err = ExportPendingBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	got := conversationObjects(t, pending)
	if len(got) != 1 {
		t.Fatalf("conversation objects after version change=%d, want 1", len(got))
	}
	if got[0].IdempotencyKey == initial.IdempotencyKey {
		t.Fatalf("conversation identity did not change: %s", got[0].IdempotencyKey)
	}
	if !strings.Contains(string(got[0].Payload), "final summary") {
		t.Fatalf("updated conversation summary was not exported: %s", got[0].Payload)
	}
}

func TestPendingExportSequentialPathForLargeConversationBacklog(t *testing.T) {
	ctx := context.Background()
	database := businessStore(t)
	task := seedSyncExportTask(t, database)
	now := time.Now().UTC()
	for index := 0; index < 2; index++ {
		if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: fmt.Sprintf("conversation_backlog_%d", index), TaskID: task.ID,
			Category: "chat", Kind: "user_message", Summary: fmt.Sprintf("backlog-%d", index), CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	previous := conversationFullScanThreshold
	conversationFullScanThreshold = 1
	t.Cleanup(func() { conversationFullScanThreshold = previous })

	pending, err := ExportPendingBusinessObjectsForDevice(ctx, database, "device-sync")
	if err != nil {
		t.Fatal(err)
	}
	conversations := conversationObjects(t, pending)
	if len(conversations) != 2 {
		t.Fatalf("large-backlog conversation objects=%d", len(conversations))
	}
	for _, object := range conversations {
		if len(object.Payload) == 0 {
			t.Fatalf("large-backlog conversation payload is empty")
		}
	}
}

func TestSyncTurnsReadsOnlyPortableColumns(t *testing.T) {
	ctx := context.Background()
	database := businessStore(t)
	task := seedSyncExportTask(t, database)
	now := time.Now().UTC()
	message := domain.Message{ID: "message-sync-turn", TaskID: task.ID, Role: "user", Sender: "owner", Content: "hello", CreatedAt: now}
	if err := database.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTurn(ctx, domain.Turn{
		ID: "turn-sync-portable", TaskID: task.ID, AgentID: "main", Sequence: 1,
		InputMessageID: message.ID, Status: domain.TurnSucceeded,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		Instruction:             "runtime-only instruction", PromptSnapshot: "runtime-only snapshot",
		QueuedAt: now, StartedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	turns, err := database.SyncTurns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns=%d", len(turns))
	}
	if turns[0].Instruction != "" || turns[0].PromptSnapshot != "" {
		t.Fatalf("runtime-only turn text was loaded: %#v", turns[0])
	}
	if turns[0].ID != "turn-sync-portable" || turns[0].InputMessageID != message.ID {
		t.Fatalf("portable turn fields changed: %#v", turns[0])
	}
}
