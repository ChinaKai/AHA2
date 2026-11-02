package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	appService := app.NewService(database, nil, nil)
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, App: appService, AgentCapabilities: capabilities,
	}).Handler())
	defer server.Close()

	response := agentRequest(t, server.URL+"/api/v1/agent/channel/mcp", http.MethodPost, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	token, err := capabilities.Issue("task-mcp", "main", "turn-mcp", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/mcp", http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "initialize",
	})
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeResponse(t, response, &initialized)
	if response.StatusCode != http.StatusOK || initialized.Result.ServerInfo.Name != "aha2-channel" {
		t.Fatalf("initialize status=%d payload=%#v", response.StatusCode, initialized)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/channel/mcp", http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/list",
	})
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	decodeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Result.Tools) != 3 {
		t.Fatalf("tools status=%d payload=%#v", response.StatusCode, listed)
	}
	if listed.Result.Tools[0].Name != "channel_reply" ||
		listed.Result.Tools[1].Name != "channel_request_permission" ||
		listed.Result.Tools[2].Name != "channel_reply_decision" {
		t.Fatalf("narrow MCP exposed unexpected tools: %#v", listed.Result.Tools)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/channel/mcp", http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// A token without a live group-routed Turn cannot silently reply. The call
	// reports a tool error instead of writing a delivery.
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/mcp", http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{
			"name": "channel_reply", "arguments": map[string]any{"message": "不应发送"},
		},
	})
	var called struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeResponse(t, response, &called)
	if response.StatusCode != http.StatusOK || !called.Result.IsError {
		t.Fatalf("unrouted reply status=%d payload=%#v", response.StatusCode, called)
	}
}
