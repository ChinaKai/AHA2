package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestChannelMCPIsListedAsAutoInjectedBuiltin(t *testing.T) {
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
	var channel domain.MCPServer
	found := false
	for _, item := range servers {
		if item.ID == "mcp_channel" {
			channel = item
			found = true
		}
	}
	if !found {
		t.Fatal("channel MCP is missing from the list")
	}
	if !channel.Builtin || !channel.AutoInject || !channel.Enabled || channel.Health != "ok" {
		t.Fatalf("unexpected channel MCP: %#v", channel)
	}
	if len(channel.Tools) != 3 {
		t.Fatalf("channel MCP tools=%#v", channel.Tools)
	}

	detail, err := service.MCPServerDetail(ctx, "mcp_channel")
	if err != nil || len(detail.Tools) != 3 {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
		ID: "mcp_channel", Name: "x", Transport: "http", URL: "https://example.test",
	}); err == nil {
		t.Fatal("channel MCP save was allowed")
	}
	if err := service.DeleteMCPServer(ctx, "mcp_channel"); err == nil {
		t.Fatal("channel MCP delete was allowed")
	}

	normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{"mcp_channel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 0 {
		t.Fatalf("channel MCP was assignable to a task: %#v", normalized)
	}
}

func TestMailMCPIsListedAsAutoInjectedBuiltin(t *testing.T) {
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
	var mail domain.MCPServer
	found := false
	for _, item := range servers {
		if item.ID == "mcp_mail" {
			mail = item
			found = true
		}
	}
	if !found {
		t.Fatal("mail MCP is missing from the list")
	}
	if !mail.Builtin || !mail.AutoInject || !mail.Enabled || mail.Health != "ok" {
		t.Fatalf("unexpected mail MCP: %#v", mail)
	}
	if len(mail.Tools) != 5 {
		t.Fatalf("mail MCP tools=%#v", mail.Tools)
	}
	detail, err := service.MCPServerDetail(ctx, "mcp_mail")
	if err != nil || len(detail.Tools) != 5 {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
		ID: "mcp_mail", Name: "x", Transport: "http", URL: "https://example.test",
	}); err == nil {
		t.Fatal("mail MCP save was allowed")
	}
	if err := service.DeleteMCPServer(ctx, "mcp_mail"); err == nil {
		t.Fatal("mail MCP delete was allowed")
	}
	normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{"mcp_mail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 0 {
		t.Fatalf("mail MCP was assignable to a task: %#v", normalized)
	}
}
