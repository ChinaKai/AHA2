package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestFetchMCPToolsAndExactAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		writer.Header().Set("Content-Type", "application/json")
		if payload.Method == "tools/list" {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"echo back","inputSchema":{"type":"object"}}]}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer server.Close()

	tools, err := fetchMCPTools(context.Background(), domain.MCPServer{URL: server.URL, Transport: "http"})
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}

	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	item := domain.MCPServer{ID: "mcp_test", Name: "test", Transport: "http", URL: server.URL, Enabled: true, Tools: tools}
	if err := database.UpsertMCPServer(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	config, allowed := service.mcpRuntimeForTask(context.Background(), domain.Task{ID: "task-test", MCPServerIDs: []string{"mcp_test"}})
	if !strings.Contains(config, "mcp_test") || !strings.Contains(config, server.URL) {
		t.Fatalf("config=%s", config)
	}
	if len(allowed) != 1 || allowed[0] != "mcp__mcp_test__echo" {
		t.Fatalf("allowed=%#v", allowed)
	}
}

func TestStdioMCPServerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &request)
		switch request.Method {
		case "initialize":
			fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{}}`)
		case "tools/list":
			fmt.Println(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"stdio_echo","description":"stdio"}]}}`)
		}
	}
}

func TestFetchStdioMCPTools(t *testing.T) {
	command := os.Args[0]
	item := domain.MCPServer{Command: command, Args: []string{"-test.run=TestStdioMCPServerHelper"}, Transport: "stdio", Env: map[string]string{"GO_WANT_MCP_STDIO_HELPER": "1"}}
	tools, err := fetchStdioTools(context.Background(), item)
	if err != nil || len(tools) != 1 || tools[0].Name != "stdio_echo" {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}
}
