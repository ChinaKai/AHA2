package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailBindingsCleanupOnTaskWorkspaceProjectDelete(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, projectID, _ := createMailTaskFixture(t, database)

	// Deleting the Task itself must remove its task-scoped bindings.
	taskAccount := createMailAccountFixture(t, database, "Task account", "task-delete@example.com")
	taskBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeTask, ScopeID: taskID,
		AccountID: taskAccount.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteTask(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.MailBinding(ctx, taskBinding.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("task binding survived DeleteTask: %v", err)
	}
	if err := database.DeleteMailAccount(ctx, taskAccount.ID); err != nil {
		t.Fatalf("account remained undeletable after task delete: %v", err)
	}

	// Deleting a Workspace through the normal sync-tombstone path must remove
	// workspace-scoped bindings.
	now := time.Now().UTC()
	workspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: projectID, Name: "Empty workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	workspaceAccount := createMailAccountFixture(t, database, "Workspace account", "workspace-delete@example.com")
	workspaceBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeWorkspace, ScopeID: workspace.ID,
		AccountID: workspaceAccount.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.MailBinding(ctx, workspaceBinding.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace binding survived DeleteWorkspace: %v", err)
	}
	if err := database.DeleteMailAccount(ctx, workspaceAccount.ID); err != nil {
		t.Fatalf("account remained undeletable after workspace delete: %v", err)
	}

	// Deleting a Project must remove project-, workspace- and task-scoped
	// bindings even though scope_id has no foreign key.
	project := domain.Project{ID: domain.NewID("project"), Name: "Empty project", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	projectAccount := createMailAccountFixture(t, database, "Project account", "project-delete@example.com")
	projectBinding, err := database.CreateMailBinding(ctx, domain.MailBinding{
		ScopeType: domain.MailBindingScopeProject, ScopeID: project.ID,
		AccountID: projectAccount.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.MailBinding(ctx, projectBinding.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project binding survived DeleteProject: %v", err)
	}
	if err := database.DeleteMailAccount(ctx, projectAccount.ID); err != nil {
		t.Fatalf("account remained undeletable after project delete: %v", err)
	}

	// The original workspace/task project still exists and its task binding is
	// gone; this also proves the cleanup did not delete across scopes.
	if _, err := database.Project(ctx, projectID); err != nil {
		t.Fatalf("unrelated project was removed: %v", err)
	}
}

func TestDeleteTaskCascadesMailGraph(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Cascade", "cascade@example.com")
	now := time.Now().UTC()

	source, err := database.CreateAttachment(ctx, taskID, "firmware.bin",
		"application/octet-stream", []byte("cascade-bytes"), now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Firmware", SizeBytes: source.Size, SHA256: source.SHA256,
		StorageType: domain.BuildArtifactStorageAHAAttachment, AttachmentID: source.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := createMailMessageFixture(t, database, taskID, account.ID, "cascade-1")
	mailAttachment, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 0,
		SourceType: domain.MailAttachmentSourceBuildArtifact, SourceID: artifact.ID,
		FileName: "firmware.bin", SizeBytes: source.Size, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, State: domain.MailDeliveryQueued,
		AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMailDeliveryAttempt(ctx, domain.MailDeliveryAttempt{
		DeliveryID: delivery.ID, AttemptNo: 1, LeaseID: "cascade-lease",
		Outcome: domain.MailAttemptTransientFailure, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteTask(ctx, taskID); err != nil {
		t.Fatalf("DeleteTask with mail graph: %v", err)
	}

	counts := []struct {
		name  string
		query string
		args  []any
	}{
		{"task", `SELECT COUNT(*) FROM tasks WHERE id=?`, []any{taskID}},
		{"build artifacts", `SELECT COUNT(*) FROM build_artifacts WHERE task_id=?`, []any{taskID}},
		{"attachments", `SELECT COUNT(*) FROM attachments WHERE task_id=?`, []any{taskID}},
		{"mail messages", `SELECT COUNT(*) FROM mail_messages WHERE task_id=?`, []any{taskID}},
		{"mail attachments", `SELECT COUNT(*) FROM mail_attachments WHERE message_id=?`, []any{message.ID}},
		{"mail deliveries", `SELECT COUNT(*) FROM mail_deliveries WHERE message_id=?`, []any{message.ID}},
		{"delivery attempts", `SELECT COUNT(*) FROM mail_delivery_attempts WHERE delivery_id=?`, []any{delivery.ID}},
	}
	for _, check := range counts {
		var count int
		if err := database.db.QueryRowContext(ctx, check.query, check.args...).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Errorf("%s left %d rows after DeleteTask", check.name, count)
		}
	}
	if _, err := database.MailAttachment(ctx, mailAttachment.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mail attachment still readable: %v", err)
	}
	if err := database.DeleteMailAccount(ctx, account.ID); err != nil {
		t.Fatalf("account remained undeletable after task cascade: %v", err)
	}
}

func TestDeleteDraftAttachmentMapsBuildArtifactReference(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	now := time.Now().UTC()

	source, err := database.CreateAttachment(ctx, taskID, "referenced.bin",
		"application/octet-stream", []byte("referenced"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Referenced", SizeBytes: source.Size, SHA256: source.SHA256,
		StorageType: domain.BuildArtifactStorageAHAAttachment, AttachmentID: source.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteDraftAttachment(ctx, taskID, source.ID); !errors.Is(err, ErrAttachmentInUse) {
		t.Fatalf("delete referenced attachment error = %v, want ErrAttachmentInUse", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM build_artifacts WHERE task_id=?`, taskID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteDraftAttachment(ctx, taskID, source.ID); err != nil {
		t.Fatalf("delete unreferenced attachment: %v", err)
	}
}
