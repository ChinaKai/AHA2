package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestOwnerAssistantSingletonAndMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	first, err := database.EnsureOwnerAssistant(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.EnsureOwnerAssistant(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Name != "小A" || first.Character != "gugugaga" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	for _, item := range []domain.AssistantMessage{
		{ID: "m1", AssistantID: first.ID, Role: domain.AssistantRoleUser, Content: "hello", CreatedAt: now},
		{ID: "m2", AssistantID: first.ID, Role: domain.AssistantRoleAssistant, Content: "hi", CreatedAt: now.Add(time.Second)},
	} {
		if err := database.AppendAssistantMessage(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := database.AssistantMessages(ctx, first.ID, 10)
	if err != nil || len(messages) != 2 || messages[0].ID != "m1" || messages[1].ID != "m2" {
		t.Fatalf("messages=%#v err=%v", messages, err)
	}
}

func TestAssistantHintDismissalsExpireAndFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	assistant, err := database.EnsureOwnerAssistant(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DismissAssistantHint(ctx, assistant.ID, "task_failed", "task-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	dismissals, err := database.AssistantHintDismissals(ctx, assistant.ID, now)
	if err != nil || len(dismissals) != 1 {
		t.Fatalf("dismissals=%#v err=%v", dismissals, err)
	}
	expired, err := database.AssistantHintDismissals(ctx, assistant.ID, now.Add(2*time.Minute))
	if err != nil || len(expired) != 0 {
		t.Fatalf("expired=%#v err=%v", expired, err)
	}
}
