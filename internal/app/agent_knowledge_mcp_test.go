package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestKnowledgeMCPIsListedAsAutoInjectedBuiltin(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)

	servers, err := service.MCPServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var knowledge domain.MCPServer
	found := false
	for _, item := range servers {
		if item.ID == knowledgeMCPName {
			knowledge = item
			found = true
		}
	}
	if !found {
		t.Fatal("knowledge MCP is missing from the catalog")
	}
	if !knowledge.Builtin || !knowledge.AutoInject || !knowledge.Enabled || knowledge.Health != "ok" {
		t.Fatalf("unexpected knowledge MCP: %#v", knowledge)
	}
	if len(knowledge.Tools) != len(KnowledgeMCPToolNames(true)) {
		t.Fatalf("knowledge MCP tools=%#v", knowledge.Tools)
	}

	detail, err := service.MCPServerDetail(ctx, knowledgeMCPName)
	if err != nil || len(detail.Tools) != len(KnowledgeMCPToolNames(true)) {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
		ID: knowledgeMCPName, Name: "x", Transport: "http", URL: "https://example.test",
	}); err == nil {
		t.Fatal("reserved knowledge MCP save was allowed")
	}
	if err := service.DeleteMCPServer(ctx, knowledgeMCPName); err == nil {
		t.Fatal("knowledge MCP delete was allowed")
	}
	normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{knowledgeMCPName})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 0 {
		t.Fatalf("knowledge MCP was assignable to a task: %#v", normalized)
	}
}
