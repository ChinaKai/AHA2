package app

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestKnowledgeMCPRuntimeExcludesChannelTurnContexts proves the runtime
// injection uses the Turn's actual inbox batch, not only the Task route: a
// conversation_host or task_route batch must not receive the knowledge server.
func TestKnowledgeMCPRuntimeExcludesChannelTurnContexts(t *testing.T) {
	ctx, database, service, task, _ := newStreamCompatFixture(t)
	raw, err := sql.Open("sqlite", filepath.Join(database.DataDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	insertInbox := func(batchID string, payloads ...map[string]any) {
		t.Helper()
		for _, payload := range payloads {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			sourceAgent, sourceKind := "owner", "owner_message"
			if _, channel := payload["channel_context"]; channel {
				sourceAgent, sourceKind = "channel", "channel_message"
			}
			if _, err := raw.ExecContext(ctx, `
				INSERT INTO agent_inbox(
					id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
					content,payload_json,status,batch_id,created_at,claimed_at,processed_at
				) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				domain.NewID("inbox"), task.ID, "round-runtime-mcp", "main", sourceAgent, sourceKind,
				"", "", "hello", string(encoded), "claimed", batchID, now, now, "",
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	knowledgeToolCount := func(allowed []string) int {
		count := 0
		for _, name := range allowed {
			if strings.HasPrefix(name, "mcp__"+knowledgeMCPName+"__") {
				count++
			}
		}
		return count
	}
	hasKnowledge := func(servers []backend.MCPServer, allowed []string) bool {
		for _, server := range servers {
			if server.Name == knowledgeMCPName {
				return true
			}
		}
		return knowledgeToolCount(allowed) > 0
	}

	servers, allowed := service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", "")
	if !hasKnowledge(servers, allowed) {
		t.Fatalf("plain Turn lost knowledge MCP: %#v %#v", servers, allowed)
	}

	insertInbox("batch-runtime-plain", map[string]any{})
	servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", "batch-runtime-plain")
	if !hasKnowledge(servers, allowed) {
		t.Fatalf("non-channel batch lost knowledge MCP: %#v %#v", servers, allowed)
	}

	channelPayload := func(mode string) map[string]any {
		return map[string]any{
			"channel_context": map[string]any{
				"schema":          "aha2.channel.v1",
				"instance_id":     "instance-runtime",
				"provider":        "test",
				"endpoint":        domain.ChannelEndpointGroupDigitalHuman,
				"conversation_id": "conversation-runtime",
				"route":           map[string]any{"mode": mode},
			},
		}
	}
	plainPayload := map[string]any{}
	// A mixed batch is channel-bound in either order: the aggregate context
	// lookup returns sql.ErrNoRows when a plain message is present.
	orders := []struct {
		name     string
		payloads func(mode string) []map[string]any
	}{
		{name: "plain-first", payloads: func(mode string) []map[string]any {
			return []map[string]any{plainPayload, channelPayload(mode)}
		}},
		{name: "channel-first", payloads: func(mode string) []map[string]any {
			return []map[string]any{channelPayload(mode), plainPayload}
		}},
	}
	for _, mode := range []string{"conversation_host", "task_route"} {
		for _, order := range orders {
			batchID := "batch-runtime-" + strings.ReplaceAll(mode, "_", "-") + "-" + order.name
			insertInbox(batchID, order.payloads(mode)...)
			servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", batchID)
			if hasKnowledge(servers, allowed) {
				t.Fatalf("%s mixed %s batch received knowledge MCP: %#v %#v", mode, order.name, servers, allowed)
			}
			if count := knowledgeToolCount(allowed); count != 0 {
				t.Fatalf("%s mixed %s batch received %d knowledge tools: %#v", mode, order.name, count, allowed)
			}
		}
	}
}
