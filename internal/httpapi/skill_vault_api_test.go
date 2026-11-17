package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
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

// TestRemovedSkillWriteRoutesReturnMethodNotAllowed locks the compatibility
// contract for the retired Web skill write API: the routes answer 405 and touch
// neither the vault packages nor the legacy skills table.
func TestRemovedSkillWriteRoutesReturnMethodNotAllowed(t *testing.T) {
	manager := knowledgeVaultSkillManagerFixture(t)
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, t.TempDir())
	ctx := context.Background()
	now := time.Now().UTC()
	legacy := domain.Skill{
		ID: "skill_legacy_review", Scope: "global", Name: "Legacy Review", Instructions: "legacy",
		Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateSkill(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	demoSkill := filepath.Join(manager.Config().VaultPath, "global", "skills", "demo", "SKILL.md")
	before, err := os.ReadFile(demoSkill)
	if err != nil {
		t.Fatal(err)
	}
	skillIDs := func() []string {
		response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/skills", nil, "")
		var list map[string]any
		decodeResponse(t, response, &list)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("list status=%d payload=%#v", response.StatusCode, list)
		}
		ids := make([]string, 0, len(list["skills"].([]any)))
		for _, raw := range list["skills"].([]any) {
			ids = append(ids, raw.(map[string]any)["id"].(string))
		}
		sort.Strings(ids)
		return ids
	}
	beforeIDs := skillIDs()

	requests := []struct {
		method  string
		path    string
		payload any
	}{
		{http.MethodPost, "/api/v1/skills", map[string]any{
			"scope": "global", "name": "Nope", "description": "Nope", "instructions": "Nope.", "slug": "nope",
		}},
		{http.MethodPut, "/api/v1/skills/vault-skill:global:demo", map[string]any{
			"name": "Rewritten", "instructions": "Rewritten body.",
		}},
		{http.MethodDelete, "/api/v1/skills/vault-skill:global:demo", nil},
	}
	for _, item := range requests {
		response := requestJSON(t, client, item.method, server.URL+item.path, item.payload, csrf)
		var payload map[string]any
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusMethodNotAllowed || payload["error"] != "skill_write_api_removed" {
			t.Fatalf("%s %s status=%d payload=%#v", item.method, item.path, response.StatusCode, payload)
		}
	}

	after, err := os.ReadFile(demoSkill)
	if err != nil {
		t.Fatalf("demo skill disappeared after rejected writes: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("demo skill was rewritten by a rejected write")
	}
	if _, err := os.Lstat(filepath.Join(manager.Config().VaultPath, "global", "skills", "nope")); !os.IsNotExist(err) {
		t.Fatalf("rejected create left a package behind: %v", err)
	}
	if got, want := strings.Join(skillIDs(), ","), strings.Join(beforeIDs, ","); got != want {
		t.Fatalf("skill list changed after rejected writes: got %s want %s", got, want)
	}
	if _, err := database.Skill(ctx, legacy.ID); err != nil {
		t.Fatalf("legacy row was touched by rejected writes: %v", err)
	}
}
