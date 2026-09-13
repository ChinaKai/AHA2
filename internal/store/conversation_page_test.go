package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestConversationPageLatestDoesNotSkipUnreturnedItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	add := func(category, summary string) int64 {
		t.Helper()
		item, addErr := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, TurnID: "turn-page",
			AgentID: "main", StreamAgentID: "main", FromAgentID: "main", ToAgentID: "owner",
			RouteKind: "agent_progress", Category: category, Kind: "agent_message_update",
			Summary: summary, CreatedAt: now,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return item.Sequence
	}

	firstSequence := add("update", "first")
	add("tool", "hidden")
	initial, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 20, []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Items) != 1 || initial.Latest != firstSequence {
		t.Fatalf("initial page advanced beyond returned items: %#v", initial)
	}

	secondSequence := add("update", "second")
	thirdSequence := add("update", "third")
	add("update", "fourth")
	incremental, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, initial.Latest, 2, []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if len(incremental.Items) != 2 || !incremental.HasMore || incremental.Latest != thirdSequence {
		t.Fatalf("incremental page skipped its remaining item: %#v", incremental)
	}
	if incremental.Items[0].Sequence != secondSequence || incremental.Items[1].Sequence != thirdSequence {
		t.Fatalf("incremental page order is invalid: %#v", incremental.Items)
	}

	remaining, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, incremental.Latest, 2, []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Items) != 1 || remaining.Items[0].Summary != "fourth" || remaining.Latest != remaining.Items[0].Sequence {
		t.Fatalf("remaining item was not reachable: %#v", remaining)
	}
}
