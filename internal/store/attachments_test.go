package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func attachmentTestTask(t *testing.T, database *Store) string {
	t.Helper()
	ctx, now := context.Background(), time.Now().UTC()
	projectID, workspaceID, taskID := "project-attachment", "workspace-attachment", "task-attachment"
	if err := database.CreateProject(ctx, domain.Project{ID: projectID, Name: "Attachments", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, ProjectID: projectID, Name: "Local", Locality: "local", Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertEnvGroup(ctx, domain.EnvGroup{ID: "env-attachment", Name: "Env", ProviderID: "provider", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, domain.Model{ID: "model-attachment", DisplayName: "Model", ProviderID: "provider", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: "env-attachment", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRuntimeSnapshot(ctx, domain.RuntimeConfigSnapshot{ID: "runtime-attachment", WorkspaceID: workspaceID, Backend: "stub", ModelID: "model-attachment", WireModel: "stub", EnvGroupID: "env-attachment", EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTask(ctx, domain.Task{ID: taskID, ProjectID: projectID, WorkspaceID: workspaceID, Title: "Task", OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskDraft, RuntimeConfigSnapshotID: "runtime-attachment", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func TestAttachmentLifecycleAndBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	taskID := attachmentTestTask(t, database)
	content := []byte("\x89PNG\r\nattachment-content")
	item, err := database.CreateAttachment(ctx, taskID, "../screen.png", "image/png", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "screen.png" || item.Size != int64(len(content)) || len(item.SHA256) != 64 {
		t.Fatalf("unexpected attachment metadata: %#v", item)
	}
	stored, err := database.AttachmentContent(item)
	if err != nil || string(stored) != string(content) {
		t.Fatalf("attachment content mismatch: %q %v", stored, err)
	}
	if err := database.ValidateDraftAttachments(ctx, taskID, []string{item.ID}); err != nil {
		t.Fatal(err)
	}
	conversation, err := database.AddConversationItemWithAttachments(ctx, domain.ConversationItem{
		ID: "conversation-attachment", TaskID: taskID, AgentID: "main", Category: "update",
		Kind: "agent_message_update", Summary: "preview", Payload: map[string]any{"api": true}, CreatedAt: time.Now().UTC(),
	}, []string{item.ID})
	if err != nil {
		t.Fatal(err)
	}
	if attachments, ok := conversation.Payload["attachments"].([]domain.Attachment); !ok || len(attachments) != 1 {
		t.Fatalf("conversation attachments missing: %#v", conversation.Payload)
	}
	bound, err := database.Attachment(ctx, taskID, item.ID)
	if err != nil || bound.MessageID != conversation.ID {
		t.Fatalf("attachment was not bound: %#v %v", bound, err)
	}
	if err := database.DeleteDraftAttachment(ctx, taskID, item.ID); err == nil {
		t.Fatal("sent attachment was deleted")
	}
}
