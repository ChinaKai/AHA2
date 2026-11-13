package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// A conversation refresh used to commit an UPDATE of last_read_sequence even
// when the reader was already at the newest item. The audit trigger is the
// only way to observe a write that changes no value: the stored number looks
// identical either way, so asserting it alone would pass for both versions.
func TestMarkAgentConversationReadWritesOnlyWhenAdvanced(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	add := func() int64 {
		t.Helper()
		item, addErr := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, AgentID: "main", StreamAgentID: "main",
			FromAgentID: "main", ToAgentID: "owner", Category: "chat", Kind: "agent_message",
			Summary: "hello", CreatedAt: now,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return item.Sequence
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TABLE task_agent_read_writes(task_id TEXT NOT NULL, agent_id TEXT NOT NULL, value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER audit_task_agent_read
		AFTER UPDATE OF last_read_sequence ON task_agents
		BEGIN
			INSERT INTO task_agent_read_writes(task_id, agent_id, value)
			VALUES (NEW.task_id, NEW.agent_id, NEW.last_read_sequence);
		END`); err != nil {
		t.Fatal(err)
	}
	writes := func() []int64 {
		t.Helper()
		rows, queryErr := database.db.QueryContext(ctx, `SELECT value FROM task_agent_read_writes ORDER BY rowid`)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer rows.Close()
		var values []int64
		for rows.Next() {
			var value int64
			if scanErr := rows.Scan(&value); scanErr != nil {
				t.Fatal(scanErr)
			}
			values = append(values, value)
		}
		return values
	}
	lastRead := func() int64 {
		t.Helper()
		var value int64
		if scanErr := database.db.QueryRowContext(ctx,
			`SELECT last_read_sequence FROM task_agents WHERE task_id=? AND agent_id='main'`, task.ID,
		).Scan(&value); scanErr != nil {
			t.Fatal(scanErr)
		}
		return value
	}

	firstSequence := add()
	secondSequence := add()
	if err := database.MarkAgentConversationRead(ctx, task.ID, "main"); err != nil {
		t.Fatal(err)
	}
	thirdSequence := add()
	if err := database.MarkAgentConversationRead(ctx, task.ID, "main"); err != nil {
		t.Fatal(err)
	}
	// Two refreshes with nothing new must not write again.
	for repeat := 0; repeat < 3; repeat++ {
		if err := database.MarkAgentConversationRead(ctx, task.ID, "main"); err != nil {
			t.Fatal(err)
		}
	}
	if got := writes(); len(got) != 2 || got[0] != secondSequence || got[1] != thirdSequence {
		t.Fatalf("read-position writes = %#v, want [%d %d]", got, secondSequence, thirdSequence)
	}
	if got := lastRead(); got != thirdSequence {
		t.Fatalf("last_read_sequence = %d, want %d", got, thirdSequence)
	}
	if firstSequence >= secondSequence || secondSequence >= thirdSequence {
		t.Fatalf("fixture did not advance: %d/%d/%d", firstSequence, secondSequence, thirdSequence)
	}
}

// A page that mixes route cards with ordinary messages must resolve delivery
// state for the cards and leave every other payload untouched. The page no
// longer joins the outbox for all rows, so this pins the per-row outcome.
func TestConversationPageDeliveryStateOnlyForCards(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	delivery := enqueueTestDelivery(t, database, instance, conversation, 1, now)
	taskID := conversation.HostTaskID
	add := func(payload map[string]any) int64 {
		t.Helper()
		item, addErr := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: taskID, AgentID: "main", StreamAgentID: "main",
			FromAgentID: "main", ToAgentID: "owner", Category: "update", Kind: "agent_channel_route",
			Summary: "route", Payload: payload, CreatedAt: now,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return item.Sequence
	}
	plainSequence := add(map[string]any{"text": "plain"})
	unknownCardSequence := add(map[string]any{"delivery_id": "delivery-missing"})
	cardSequence := add(map[string]any{"delivery_id": delivery.ID, "delivery_state": "pending"})
	emptyCardSequence := add(map[string]any{"delivery_id": ""})

	page, err := database.ConversationPageForAgent(ctx, taskID, "main", 0, 0, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := map[int64]any{}
	for _, item := range page.Items {
		state[item.Sequence] = item.Payload["delivery_state"]
	}
	if got := state[plainSequence]; got != nil {
		t.Fatalf("plain item gained delivery_state=%v", got)
	}
	if got := state[unknownCardSequence]; got != nil {
		t.Fatalf("card with unknown delivery gained delivery_state=%v", got)
	}
	if got := state[cardSequence]; got != "pending" {
		t.Fatalf("live card state=%v, want pending", got)
	}
	if got := state[emptyCardSequence]; got != nil {
		t.Fatalf("empty delivery id gained delivery_state=%v", got)
	}
}
