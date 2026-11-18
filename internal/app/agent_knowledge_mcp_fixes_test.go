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
	insertInbox := func(batchID string, payload map[string]any) {
		t.Helper()
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO agent_inbox(
				id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
				content,payload_json,status,batch_id,created_at,claimed_at,processed_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			domain.NewID("inbox"), task.ID, "round-runtime-mcp", "main", "channel", "channel_message",
			"", "", "hello", string(encoded), "claimed", batchID, now, now, "",
		); err != nil {
			t.Fatal(err)
		}
	}
	hasKnowledge := func(servers []backend.MCPServer, allowed []string) bool {
		for _, server := range servers {
			if server.Name == knowledgeMCPName {
				return true
			}
		}
		for _, name := range allowed {
			if strings.HasPrefix(name, "mcp__"+knowledgeMCPName+"__") {
				return true
			}
		}
		return false
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

	for _, mode := range []string{"conversation_host", "task_route"} {
		batchID := "batch-runtime-" + strings.ReplaceAll(mode, "_", "-")
		insertInbox(batchID, map[string]any{
			"channel_context": map[string]any{
				"schema":          "aha2.channel.v1",
				"instance_id":     "instance-runtime",
				"provider":        "test",
				"endpoint":        domain.ChannelEndpointGroupDigitalHuman,
				"conversation_id": "conversation-runtime",
				"route":           map[string]any{"mode": mode},
			},
		})
		servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", batchID)
		if hasKnowledge(servers, allowed) {
			t.Fatalf("%s batch received knowledge MCP: %#v %#v", mode, servers, allowed)
		}
	}
}
