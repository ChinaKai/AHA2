package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestSaveMCPServerRejectsNormalizedReservedAndCollisions(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)
	save := func(id, name string) error {
		_, err := service.SaveMCPServer(ctx, domain.MCPServer{
			ID: id, Name: name, Transport: "http", URL: "https://example.test/mcp",
		})
		return err
	}

	for _, identifier := range []string{"aha2-core", "aha2.core", "AHA2-core", "aha2_core"} {
		err := save(identifier, "reserved ID")
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("reserved ID %q err=%v", identifier, err)
		}
	}
	for _, identifier := range []string{"mcp-core", "mcp.channel", "MCP-PET"} {
		if err := save(identifier, "builtin alias"); err == nil {
			t.Fatalf("builtin alias %q was accepted", identifier)
		}
	}
	if err := save("mcp-name-reserved", "AHA2.Core"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved display name err=%v", err)
	}

	if err := save("mcp_local", "local"); err != nil {
		t.Fatal(err)
	}
	if err := save("MCP-Local", "collision"); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("normalized collision err=%v", err)
	}
	// Editing the same server must not collide with itself.
	if err := save("mcp_local", "local updated"); err != nil {
		t.Fatalf("self update err=%v", err)
	}
}

func TestLegacyMCPNamespaceConflictsAreDiagnosedAndSkipped(t *testing.T) {
	ctx, database, service, task, model := newStreamCompatFixture(t)
	now := time.Now().UTC()
	legacyItems := []domain.MCPServer{
		{ID: "aha2-core", Name: "legacy core", Transport: "http", URL: "https://example.test/core",
			Enabled: true, CreatedAt: now, UpdatedAt: now, LastCheckedAt: now},
		{ID: "mcp-local", Name: "local one", Transport: "http", URL: "https://example.test/one",
			Enabled: true, CreatedAt: now, UpdatedAt: now, LastCheckedAt: now},
		{ID: "mcp_local", Name: "local two", Transport: "http", URL: "https://example.test/two",
			Enabled: true, CreatedAt: now, UpdatedAt: now, LastCheckedAt: now},
		{ID: "mcp-core", Name: "legacy core alias", Transport: "http", URL: "https://example.test/core-alias",
			Enabled: true, CreatedAt: now, UpdatedAt: now, LastCheckedAt: now},
	}
	for _, item := range legacyItems {
		if err := database.UpsertMCPServer(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	conflictTask, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID, Title: "legacy mcp", Request: "legacy mcp",
		ModelID: model.ID, Backend: "claude", Isolation: "inplace", StartMode: "manual",
		MCPServerIDs: []string{"aha2-core", "mcp-local", "mcp_local", "mcp-core"},
	})
	if err != nil {
		t.Fatal(err)
	}
	servers, allowed := service.mcpRuntimeForTurn(ctx, conflictTask, "main", "http://127.0.0.1:8766")
	coreCount, localCount := 0, 0
	for _, server := range servers {
		switch normalizedMCPIdentifier(server.Name) {
		case normalizedMCPIdentifier(coreMCPName):
			coreCount++
		case "mcp_local":
			localCount++
		}
	}
	if coreCount != 1 || localCount != 1 {
		t.Fatalf("runtime namespaces core=%d local=%d servers=%#v", coreCount, localCount, servers)
	}
	localTools := 0
	for _, name := range allowed {
		if strings.HasPrefix(name, "mcp__aha2-core__") || strings.HasPrefix(name, "mcp__mcp-core__") {
			t.Fatalf("conflicting namespace leaked into allowed tools: %#v", allowed)
		}
		if strings.HasPrefix(name, "mcp__mcp-local__") || strings.HasPrefix(name, "mcp__mcp_local__") {
			localTools++
		}
	}
	if localTools != 1 {
		t.Fatalf("normalized duplicate namespaces in allowed tools: %#v", allowed)
	}

	listed, err := service.MCPServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diagnosed := 0
	for _, item := range listed {
		if item.ID != "aha2-core" && item.ID != "mcp-core" && item.ID != "mcp_local" && item.ID != "mcp-local" {
			continue
		}
		if item.Health == "error" && item.LastError != "" {
			diagnosed++
		}
	}
	// The reserved legacy server, the built-in alias and the second normalized
	// duplicate are diagnosed; the first duplicate remains a normal selectable
	// server.
	if diagnosed != 3 {
		t.Fatalf("diagnosed=%d want 3: %#v", diagnosed, listed)
	}
}
