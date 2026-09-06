package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestFinalizeTurnReplyIsIdempotentAndPreservesProgressAndRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	turnID := "turn-finalize-main"
	for _, text := range []string{"partial", "the final answer"} {
		if _, err := database.UpsertBackendStreamItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: "round-main", TurnID: turnID,
			AgentID: "main", Category: "update", Kind: "agent_message_update", Summary: text,
			Payload: map[string]any{"text": text}, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: "round-main", TurnID: turnID,
		AgentID: "main", StreamAgentID: "main", FromAgentID: "main", ToAgentID: "owner",
		RouteKind: "agent_progress", Category: "update", Kind: "agent_message_update", Summary: "durable progress", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: "round-main", TurnID: turnID,
		AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
		RouteKind: "agent_route", Category: "update", Kind: "agent_result", Summary: "route remains", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	finalize := func(content string) error {
		_, err := database.FinalizeTurnReply(ctx, domain.Message{
			ID: domain.NewID("message"), TaskID: task.ID, TurnID: turnID, Role: "assistant", Sender: "main", Content: content, CreatedAt: now,
		}, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: "round-main", TurnID: turnID,
			AgentID: "main", Category: "chat", Kind: "agent_message", Summary: content,
			Payload: map[string]any{"attempt": 1}, CreatedAt: now,
		})
		return err
	}
	if err := finalize("the final answer"); err != nil {
		t.Fatal(err)
	}
	if err := finalize("the final answer"); err != nil {
		t.Fatal(err)
	}

	page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	finals, streamPlaceholders, progress, routes := 0, 0, 0, 0
	for _, item := range page.Items {
		if item.TurnID != turnID {
			continue
		}
		switch item.RouteKind {
		case "turn_result":
			finals++
			if item.Summary != "the final answer" || item.Kind != "agent_message" {
				t.Fatalf("unexpected final item: %#v", item)
			}
		case "backend_stream":
			streamPlaceholders++
		case "agent_progress":
			progress++
		case "agent_route":
			routes++
		}
	}
	if finals != 1 || streamPlaceholders != 0 || progress != 1 || routes != 1 {
		t.Fatalf("conversation counts final=%d stream=%d progress=%d routes=%d items=%#v", finals, streamPlaceholders, progress, routes, page.Items)
	}
	messages, err := database.ListMessages(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistantMessages := 0
	for _, message := range messages {
		if message.TurnID == turnID && message.Role == "assistant" {
			assistantMessages++
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("assistant message count=%d messages=%#v", assistantMessages, messages)
	}

	secondTurn := "turn-finalize-second"
	if _, err := database.FinalizeTurnReply(ctx, domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, TurnID: secondTurn, Role: "assistant", Sender: "main", Content: "the final answer", CreatedAt: now,
	}, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: "round-second", TurnID: secondTurn,
		AgentID: "main", Category: "chat", Kind: "agent_message", Summary: "the final answer", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	page, err = database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	identicalFinals := 0
	for _, item := range page.Items {
		if item.RouteKind == "turn_result" && item.Summary == "the final answer" {
			identicalFinals++
		}
	}
	if identicalFinals != 2 {
		t.Fatalf("identical replies from distinct Turns were collapsed: %#v", page.Items)
	}
}

func TestBackendStreamUpsertIsRaceSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	const turnID = "turn-stream-race"
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func(value int) {
			defer group.Done()
			_, err := database.UpsertBackendStreamItem(ctx, domain.ConversationItem{
				ID: domain.NewID("conversation"), TaskID: task.ID, TurnID: turnID, AgentID: "main",
				Category: "update", Kind: "agent_message_update", Summary: "partial", Payload: map[string]any{"value": value}, CreatedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Errorf("stream upsert %d: %v", value, err)
			}
		}(index)
	}
	group.Wait()
	page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	placeholders := 0
	for _, item := range page.Items {
		if item.TurnID == turnID && item.RouteKind == "backend_stream" {
			placeholders++
		}
	}
	if placeholders != 1 {
		t.Fatalf("stream placeholder count=%d items=%#v", placeholders, page.Items)
	}
}
