package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
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

func countBlobFiles(t *testing.T, dataDir string) int {
	t.Helper()
	root := filepath.Join(dataDir, "attachments", "blobs")
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return count
}

func TestCloneAttachmentKeepsSourceAndReusesBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	taskID := attachmentTestTask(t, database)
	now := time.Now().UTC()
	content := []byte("clone me\n")
	source, err := database.CreateAttachment(ctx, taskID, "report.txt", "text/plain", content, now)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.AddConversationItemWithAttachments(ctx, domain.ConversationItem{
		ID: "conversation-clone-source", TaskID: taskID, AgentID: "main", Category: "update",
		Kind: "agent_message_update", Summary: "sent", Payload: map[string]any{}, CreatedAt: now,
	}, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	blobsBefore := countBlobFiles(t, database.dataDir)
	clone, err := database.CloneAttachment(ctx, taskID, source.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if blobsBefore != countBlobFiles(t, database.dataDir) {
		t.Fatalf("clone wrote a new blob: before=%d after=%d", blobsBefore, countBlobFiles(t, database.dataDir))
	}
	if clone.ID == "" || clone.ID == source.ID {
		t.Fatalf("clone id = %q, source id = %q", clone.ID, source.ID)
	}
	if clone.MessageID != "" {
		t.Fatalf("clone is bound to a message: %#v", clone)
	}
	if clone.TaskID != taskID || clone.Name != source.Name || clone.MediaType != source.MediaType ||
		clone.Size != source.Size || clone.SHA256 != source.SHA256 {
		t.Fatalf("clone metadata mismatch: source=%#v clone=%#v", source, clone)
	}
	stillSent, err := database.Attachment(ctx, taskID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillSent.MessageID != conversation.ID {
		t.Fatalf("source binding changed: %#v", stillSent)
	}
	if err := database.ValidateDraftAttachments(ctx, taskID, []string{clone.ID}); err != nil {
		t.Fatalf("clone is not sendable: %v", err)
	}
	if err := database.ValidateDraftAttachments(ctx, taskID, []string{source.ID}); err == nil || err.Error() != "attachment is already sent" {
		t.Fatalf("source is no longer reported as sent: %v", err)
	}
	stored, err := database.AttachmentContent(clone)
	if err != nil || string(stored) != string(content) {
		t.Fatalf("clone content mismatch: %q %v", stored, err)
	}
}

func TestCloneAttachmentRejectsOtherTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	taskID := attachmentTestTask(t, database)
	now := time.Now().UTC()
	if err := database.CreateTask(ctx, domain.Task{
		ID: "task-attachment-other", ProjectID: "project-attachment", WorkspaceID: "workspace-attachment",
		Title: "Other", OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskDraft,
		RuntimeConfigSnapshotID: "runtime-attachment", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	source, err := database.CreateAttachment(ctx, taskID, "report.txt", "text/plain", []byte("owner bytes"), now)
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.ListTaskAttachmentsIncludingDrafts(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CloneAttachment(ctx, "task-attachment-other", source.ID, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-task clone error = %v, want sql.ErrNoRows", err)
	}
	if _, err := database.CloneAttachment(ctx, taskID, "attachment-missing", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing attachment error = %v, want sql.ErrNoRows", err)
	}
	after, err := database.ListTaskAttachmentsIncludingDrafts(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected clones wrote attachment rows: before=%#v after=%#v", before, after)
	}
	if other, err := database.ListTaskAttachmentsIncludingDrafts(ctx, "task-attachment-other"); err != nil || len(other) != 0 {
		t.Fatalf("cross-task clone wrote into the other task: %#v %v", other, err)
	}
}
