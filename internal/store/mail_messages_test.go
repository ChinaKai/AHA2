package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailMessagePersistenceAndIdempotencyLookup(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, projectID, workspaceID := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Messages", "messages@example.com")

	message, err := database.CreateMailMessage(ctx, domain.MailMessage{
		TaskID: taskID, ProjectID: projectID, WorkspaceID: workspaceID,
		AccountID: account.ID, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: "message-1", FromAddress: "noreply@example.com",
		FromName: "AHA", To: []string{"qa@example.com", "dev@example.com"},
		CC: []string{"cc@example.com"}, BCC: []string{"bcc@example.com"},
		Subject: "Subject", BodyText: "text", BodyHTML: "<p>text</p>",
		Variables: map[string]string{"build.version": "1.2.3"},
		Status:    domain.MailMessageDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.ContentRevision != 1 || message.Status != domain.MailMessageDraft {
		t.Fatalf("created message = %+v", message)
	}

	stored, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.To) != 2 || stored.To[0] != "qa@example.com" ||
		len(stored.CC) != 1 || len(stored.BCC) != 1 ||
		stored.Variables["build.version"] != "1.2.3" ||
		stored.BodyHTML != "<p>text</p>" {
		t.Fatalf("stored message = %+v", stored)
	}

	byKey, err := database.MailMessageByTaskIdempotencyKey(ctx, taskID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if byKey.ID != message.ID {
		t.Fatalf("idempotency lookup = %s, want %s", byKey.ID, message.ID)
	}
	if _, err := database.MailMessageByTaskIdempotencyKey(ctx, taskID, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing idempotency key error = %v", err)
	}

	listed, err := database.MailMessagesByTask(ctx, taskID, domain.MailMessageDraft, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != message.ID {
		t.Fatalf("draft messages = %+v", listed)
	}
	none, err := database.MailMessagesByTask(ctx, taskID, domain.MailMessageSent, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("sent messages = %+v", none)
	}
}

func TestMailMessageConditionalStatusUpdates(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Status", "status@example.com")
	message := createMailMessageFixture(t, database, taskID, account.ID, "status-1")
	now := time.Now().UTC()

	ok, err := database.UpdateMailMessageStatus(ctx, message.ID,
		[]domain.MailMessageStatus{domain.MailMessageDraft},
		domain.MailMessagePendingApproval, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("draft -> pending_approval was not applied")
	}

	// The row is no longer draft, so a second writer expecting draft must lose
	// the race instead of overwriting pending_approval.
	ok, err = database.UpdateMailMessageStatus(ctx, message.ID,
		[]domain.MailMessageStatus{domain.MailMessageDraft},
		domain.MailMessageCancelled, now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("stale draft transition overwrote a newer state")
	}
	stored, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailMessagePendingApproval {
		t.Fatalf("status = %q, want pending_approval", stored.Status)
	}

	if _, err := database.UpdateMailMessageStatus(ctx, message.ID,
		[]domain.MailMessageStatus{domain.MailMessagePendingApproval},
		"not_a_status", now); err == nil {
		t.Fatal("invalid target status was accepted")
	}
	if _, err := database.UpdateMailMessageStatus(ctx, message.ID, nil,
		domain.MailMessageCancelled, now); err == nil {
		t.Fatal("empty expected-status list was accepted")
	}
}
