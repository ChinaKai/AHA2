package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func TestSkillsListOnlyVaultPackages(t *testing.T) {
	manager := knowledgeVaultSkillManagerFixture(t)
	server, client, _, database := knowledgeVaultServerAt(t, manager, t.TempDir())
	ctx := context.Background()
	now := time.Now().UTC()
	legacy := domain.Skill{
		ID: "skill_legacy_review", Scope: "global", Name: "Legacy Review", Instructions: "legacy",
		Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateSkill(ctx, legacy); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/skills", nil, "")
	var list map[string]any
	decodeResponse(t, response, &list)
	if response.StatusCode != http.StatusOK || list["source"] != "vault" {
		t.Fatalf("list status=%d payload=%#v", response.StatusCode, list)
	}
	for _, raw := range list["skills"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == legacy.ID || item["name"] == "Legacy Review" {
			t.Fatalf("legacy skill leaked into the vault-only list: %#v", item)
		}
	}
}

func TestKnowledgeVaultRootLibrariesAPI(t *testing.T) {
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Roots", Commit: false}); err != nil {
		t.Fatal(err)
	}
	write := func(relative, content string) {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes/doc.md", "---\nid: knowledge_root_note\ntitle: Root note\nscope: root\ntype: reference\nstatus: verified\n---\nRoot searchable body.\n")
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, _ := knowledgeVaultServer(t, manager)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/libraries", nil, "")
	var libraries map[string]any
	decodeResponse(t, response, &libraries)
	found := false
	for _, raw := range libraries["libraries"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == "root:notes" {
			found = true
			if item["kind"] != "root" || item["project_id"] != nil && item["project_id"] != "" {
				t.Fatalf("root library shape = %#v", item)
			}
		}
	}
	if response.StatusCode != http.StatusOK || !found {
		t.Fatalf("libraries status=%d payload=%#v", response.StatusCode, libraries)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/tree?library=all", nil, "")
	var tree map[string]any
	decodeResponse(t, response, &tree)
	rootFound := false
	for _, raw := range tree["nodes"].([]any) {
		item := raw.(map[string]any)
		if item["path"] == "notes/index.md" {
			rootFound = true
		}
	}
	if response.StatusCode != http.StatusOK || !rootFound {
		t.Fatalf("all tree status=%d payload=%#v", response.StatusCode, tree)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/search?q=Root+searchable&library=root:notes", nil, "")
	var search map[string]any
	decodeResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || len(search["results"].([]any)) != 1 {
		t.Fatalf("root search status=%d payload=%#v", response.StatusCode, search)
	}
}
