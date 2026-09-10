package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSafeChannelMediaErrorCode(t *testing.T) {
	for _, code := range []string{"resource_size_invalid", "resource_permission_denied", "media_download_provider_99991672", "media_transfer_http_403", "media_send_failed"} {
		if got := SafeChannelMediaErrorCode(code); got != code {
			t.Fatalf("safe code lost: %s", got)
		}
	}
	for _, code := range []string{"", "private-url secret", "media_download_provider_secret", "media_transfer_http_403\nprivate", "media_download_provider_1234567890"} {
		if got := SafeChannelMediaErrorCode(code); got != "resource_download_failed" {
			t.Fatalf("unsafe code retained: %s", got)
		}
	}
}

func TestChannelAttachmentRetriesAreIdempotent(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	taskID := attachmentTestTask(t, database)
	first, err := database.CreateChannelAttachment(ctx, "media-command", taskID, "../report.txt", "text/plain", []byte("report"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateChannelAttachment(ctx, "media-command", taskID, "../report.txt", "text/plain", []byte("report"), time.Now())
	if err != nil || first.ID != second.ID || first.Name != "report.txt" {
		t.Fatalf("retry mismatch: %#v %v", second, err)
	}
	if _, err := database.CreateChannelAttachment(ctx, "media-command", taskID, "report.txt", "text/plain", []byte("different"), time.Now()); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	var count int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM attachments WHERE task_id=?", taskID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("attachment rows=%d err=%v", count, err)
	}
}

func TestChannelDeliveryPartsKeepFilesOutOfTextCoalescing(t *testing.T) {
	payload := map[string]any{"task_id": "task", "text": "result", "attachments": []domain.Attachment{
		{ID: "first", TaskID: "task", Name: "image.png", MediaType: "image/png", Size: 20},
		{ID: "second", TaskID: "task", Name: "report.pdf", MediaType: "application/pdf", Size: 30},
		{ID: "foreign", TaskID: "other", Name: "private.txt"},
	}}
	parts := channelDeliveryParts(payload)
	if len(parts) != 3 || parts[0]["text"] != "result" || parts[0]["attachments"] != nil {
		t.Fatalf("parts=%#v", parts)
	}
	if parts[1]["attachment_id"] != "first" || parts[2]["attachment_id"] != "second" {
		t.Fatalf("attachment order=%#v", parts)
	}
	if payload["attachments"] == nil {
		t.Fatal("original source payload mutated")
	}
}

func TestTurnOutputAttachmentsOnlyIncludesCurrentMainOutput(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	taskID := attachmentTestTask(t, database)
	for _, item := range []struct{ id, turn, agent string }{{"output", "turn-current", "main"}, {"old", "turn-old", "main"}, {"child", "turn-current", "sub"}} {
		attachment, err := database.CreateAttachment(ctx, taskID, item.id+".txt", "text/plain", []byte(item.id), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.AddConversationItemWithAttachments(ctx, domain.ConversationItem{ID: item.id, TaskID: taskID, TurnID: item.turn, AgentID: item.agent, Category: "update", Kind: "agent_progress", CreatedAt: time.Now()}, []string{attachment.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	attachments, err := database.TurnOutputAttachments(ctx, taskID, "turn-current")
	if err != nil || len(attachments) != 1 || attachments[0].Name != "output.txt" {
		t.Fatalf("attachments=%#v err=%v", attachments, err)
	}
}
