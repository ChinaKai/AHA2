package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func knowledgeVaultSkillManagerFixture(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Skill Vault", Commit: false}); err != nil {
		t.Fatal(err)
	}
	write := func(relative, content string) {
		path := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/pet/index.md", "---\nid: knowledge_root_project_pet\ntitle: Pet\nscope: project\nproject_id: project_pet\ntype: navigation\nstatus: verified\nis_index: true\n---\nPet entry.\n")
	write("global/skills/demo/SKILL.md", "---\nname: Demo\ndescription: A global demo skill\n---\n\n# Demo\n")
	write("global/skills/demo/scripts/check.sh", "#!/bin/sh\necho demo\n")
	write("projects/pet/skills/pet-review/SKILL.md", "---\nname: Pet Review\ndescription: A project skill\n---\n\n# Pet Review\n")
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
	return manager
}

func vaultSkillByKey(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()
	for _, raw := range payload["skills"].([]any) {
		item := raw.(map[string]any)
		if item["key"] == key {
			return item
		}
	}
	t.Fatalf("skill %s not found in %#v", key, payload["skills"])
	return nil
}

func TestKnowledgeVaultSkillAutoAttachAPIAndGlobalConfirmation(t *testing.T) {
	manager := knowledgeVaultSkillManagerFixture(t)
	server, client, csrf := knowledgeVaultServer(t, manager)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/skills", nil, "")
	var catalog map[string]any
	decodeResponse(t, response, &catalog)
	if response.StatusCode != http.StatusOK || catalog["runtime_read"] != true {
		t.Fatalf("catalog status=%d payload=%#v", response.StatusCode, catalog)
	}
	if ids := catalog["bound_project_ids"].([]any); len(ids) != 1 || ids[0] != "project_pet" {
		t.Fatalf("bound projects = %#v", ids)
	}
	demo := vaultSkillByKey(t, catalog, "vault-skill:global:demo")
	if demo["source"] != "vault" || demo["auto_attach"] != false || demo["can_update"] != true || demo["project_bound"] != true {
		t.Fatalf("global vault skill = %#v", demo)
	}

	// A global enable without the explicit second confirmation is rejected and
	// writes nothing.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/skills/auto-attach",
		map[string]any{"key": "vault-skill:global:demo", "auto_attach": true}, csrf)
	var rejected map[string]any
	decodeResponse(t, response, &rejected)
	if response.StatusCode != http.StatusConflict || rejected["error"] != "global_confirmation_required" {
		t.Fatalf("unconfirmed global enable status=%d payload=%#v", response.StatusCode, rejected)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/skills", nil, "")
	decodeResponse(t, response, &catalog)
	if vaultSkillByKey(t, catalog, "vault-skill:global:demo")["auto_attach"] != false {
		t.Fatal("rejected global enable persisted a preference")
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/skills/auto-attach",
		map[string]any{"key": "vault-skill:global:demo", "auto_attach": true, "confirm_global": true}, csrf)
	var accepted map[string]any
	decodeResponse(t, response, &accepted)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("confirmed global enable status=%d payload=%#v", response.StatusCode, accepted)
	}
	item := accepted["skill"].(map[string]any)
	if item["auto_attach"] != true || item["auto_attach_confirmed"] != true || item["auto_attach_effective"] != true {
		t.Fatalf("confirmed global view = %#v", item)
	}

	// Project skills need no confirmation and are effective for their project.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/skills/auto-attach",
		map[string]any{"key": "vault-skill:project:project_pet:pet-review", "auto_attach": true}, csrf)
	decodeResponse(t, response, &accepted)
	if response.StatusCode != http.StatusOK || accepted["skill"].(map[string]any)["auto_attach"] != true {
		t.Fatalf("project enable status=%d payload=%#v", response.StatusCode, accepted)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/skills?project_id=project_pet", nil, "")
	decodeResponse(t, response, &catalog)
	if vaultSkillByKey(t, catalog, "vault-skill:global:demo")["auto_attach_effective"] != true {
		t.Fatalf("global skill not effective for a bound project: %#v", catalog)
	}
	if vaultSkillByKey(t, catalog, "vault-skill:project:project_pet:pet-review")["auto_attach_effective"] != true {
		t.Fatalf("project skill not effective for its project: %#v", catalog)
	}

	// An unbound project degrades explicitly: global skill is listed but not
	// effective, and another project's skill stays hidden.
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/skills?project_id=project_other", nil, "")
	decodeResponse(t, response, &catalog)
	otherDemo := vaultSkillByKey(t, catalog, "vault-skill:global:demo")
	if otherDemo["project_bound"] != false || otherDemo["auto_attach_effective"] != false || otherDemo["unavailable_reason"] != "project_not_bound" {
		t.Fatalf("unbound project global skill = %#v", otherDemo)
	}
	for _, raw := range catalog["skills"].([]any) {
		if raw.(map[string]any)["key"] == "vault-skill:project:project_pet:pet-review" {
			t.Fatal("another project's vault skill leaked into an unbound project")
		}
	}

	// Disabling writes the preference off and clears the effective flag.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/skills/auto-attach",
		map[string]any{"key": "vault-skill:global:demo", "auto_attach": false}, csrf)
	decodeResponse(t, response, &accepted)
	if response.StatusCode != http.StatusOK || accepted["skill"].(map[string]any)["auto_attach"] != false {
		t.Fatalf("disable status=%d payload=%#v", response.StatusCode, accepted)
	}

	// Unknown keys are a 404; archive/other scopes never accept auto-attach.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/skills/auto-attach",
		map[string]any{"key": "vault-skill:global:missing", "auto_attach": true, "confirm_global": true}, csrf)
	decodeResponse(t, response, &rejected)
	if response.StatusCode != http.StatusNotFound || rejected["error"] != "vault_skill_not_found" {
		t.Fatalf("unknown key status=%d payload=%#v", response.StatusCode, rejected)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/skills/auto-attach",
		map[string]any{"key": "vault-skill:global:demo"}, csrf)
	decodeResponse(t, response, &rejected)
	if response.StatusCode != http.StatusBadRequest || rejected["error"] != "invalid_auto_attach" {
		t.Fatalf("missing flag status=%d payload=%#v", response.StatusCode, rejected)
	}
}

func TestKnowledgeVaultSkillsStayDisabledWithTheSwitchOff(t *testing.T) {
	server, client, _ := knowledgeVaultServer(t, knowledgeVaultDisabledManager(t))
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/skills", nil, "")
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusNotFound || payload["error"] != "knowledge_vault_disabled" {
		t.Fatalf("disabled catalog status=%d payload=%#v", response.StatusCode, payload)
	}
}
