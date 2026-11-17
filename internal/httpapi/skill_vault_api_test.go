package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func TestSkillsAPIWritesOnlyVaultPackages(t *testing.T) {
	manager := knowledgeVaultSkillManagerFixture(t)
	dataDir := t.TempDir()
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, dataDir)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, project := range []domain.Project{
		{ID: "project_pet", Name: "Pet", KnowledgeState: domain.KnowledgeStateReady, CreatedAt: now, UpdatedAt: now},
		{ID: "project_other", Name: "Other", KnowledgeState: domain.KnowledgeStateReady, CreatedAt: now, UpdatedAt: now},
	} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
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

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/skills", map[string]any{
		"scope": "project", "project_id": "project_pet", "name": "Pet Review",
		"description": "Pet review skill", "instructions": "Review pet changes.", "slug": "pet-review-new",
	}, csrf)
	var created map[string]any
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d payload=%#v", response.StatusCode, created)
	}
	skill := created["skill"].(map[string]any)
	skillID := skill["id"].(string)
	if !strings.HasPrefix(skillID, "vault-skill:project:project_pet:") || skill["version"] != float64(1) {
		t.Fatalf("created skill = %#v", skill)
	}
	legacyAfter, err := database.ListSkills(ctx, "", "", false)
	if err != nil || len(legacyAfter) != 1 || legacyAfter[0].ID != legacy.ID {
		t.Fatalf("legacy skills changed after create: %#v err=%v", legacyAfter, err)
	}
	skillPath := filepath.Join(manager.Config().VaultPath, "projects", "pet", "skills", "pet-review-new", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("SKILL.md was not written: %v", err)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/skills/"+skillID, map[string]any{
		"scope": "project", "project_id": "project_pet", "name": "Pet Review",
		"description": "Updated", "instructions": "Review everything.", "status": "active", "enabled": true,
	}, csrf)
	var updated map[string]any
	decodeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated["skill"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("update status=%d payload=%#v", response.StatusCode, updated)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/skills", map[string]any{
		"scope": "project", "project_id": "project_pet", "name": "Pet Review",
		"description": "Duplicate", "instructions": "Duplicate.", "slug": "pet-review-new",
	}, csrf)
	var duplicate map[string]any
	decodeResponse(t, response, &duplicate)
	if response.StatusCode != http.StatusConflict || duplicate["error"] != "skill_already_exists" {
		t.Fatalf("duplicate status=%d payload=%#v", response.StatusCode, duplicate)
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/skills/"+skillID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	response.Body.Close()
	if _, err := os.Lstat(filepath.Join(manager.Config().VaultPath, "projects", "pet", "skills", "pet-review-new")); !os.IsNotExist(err) {
		t.Fatalf("package still exists after delete: %v", err)
	}
	trash, err := os.ReadDir(filepath.Join(manager.Config().VaultPath, ".trash", "skills", "project-project_pet"))
	if err != nil || len(trash) != 1 {
		t.Fatalf("trash = %#v err=%v", trash, err)
	}
	if _, err := database.Skill(ctx, legacy.ID); err != nil {
		t.Fatalf("legacy row was touched by vault skill delete: %v", err)
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
