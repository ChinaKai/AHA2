package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestLiveInboxInjectionTextCarriesSourceAndAttachments(t *testing.T) {
	t.Parallel()
	messages := []domain.LiveInboxMessage{
		{
			ID: "inbox-1", Content: "please stop and check the screenshot",
			Payload: map[string]any{
				"channel_receipt_id": "channel_inbox_123",
				"attachments": []any{map[string]any{
					"id": "attachment-1", "name": "screen shot.png", "media_type": "image/png", "size": float64(2048),
				}},
			},
			Source: domain.LiveInboxSource{
				Kind: "channel", Authority: "task_channel_participant",
				DisplayName: "蒋开开", Role: "owner", IsBot: false,
			},
		},
		{ID: "inbox-2", Content: "second", Source: domain.LiveInboxSource{Kind: "owner", Authority: "owner"}},
	}
	text := liveInboxInjectionText(messages, map[string]string{"attachment-1": ".aha2-context/live-attachments/attachment-1/screen shot.png"})
	for _, expected := range []string{
		"[AHA live inbox] 2 new message(s)",
		"蒋开开 (channel, role=owner, authority=task_channel_participant)",
		"Channel reply receipt: channel_inbox_123",
		"please stop and check the screenshot",
		"screen shot.png (image/png, 2048 bytes)",
		".aha2-context/live-attachments/attachment-1/screen shot.png",
		"owner (authority=owner)",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("injection text missing %q:\n%s", expected, text)
		}
	}
	if !strings.Contains(text, "do not call inbox/wait for them") {
		t.Fatalf("injection text must tell the model not to poll again:\n%s", text)
	}
}

func TestLiveAttachmentNameRejectsTraversal(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]string{
		"../../etc/passwd":      "passwd",
		`..\..\windows\evil`:    "evil",
		"":                      "attachment",
		"..":                    "attachment",
		"normal screenshot.png": "normal screenshot.png",
	} {
		if got := liveAttachmentName(input); got != expected {
			t.Fatalf("liveAttachmentName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestWriteLiveAttachmentNative(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	content := []byte{0x89, 'P', 'N', 'G', 0x00, 0x01}
	if err := writeLiveAttachment(context.Background(), domain.Workspace{Transport: "native", RootPath: root}, root,
		".aha2-context/live-attachments/attachment-1/screen.png", content); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, ".aha2-context", "live-attachments", "attachment-1", "screen.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("materialized attachment bytes = %v", got)
	}
}

func TestLiveInboxInjectionTextEmptyMessages(t *testing.T) {
	t.Parallel()
	if got := liveInboxInjectionText(nil, nil); got != "" {
		t.Fatalf("empty injection text = %q", got)
	}
}
