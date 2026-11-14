package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// knowledgeVaultManagerFixture builds a real Phase 1 vault on disk, rebuilds
// its index, and returns the same enabled runtime manager the server uses.
func knowledgeVaultManagerFixture(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "HTTP Vault", Commit: false}); err != nil {
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
	write("global/general/deploy.md", `---
id: knowledge_http_deploy
type: practice
status: verified
title: 部署指南
tags: [deploy, ops]
---
# 部署

先验证候选版本。
`)
	write("global/inbox/idea.md", `---
id: knowledge_http_inbox
type: practice
status: unverified
title: 收件想法
---
# 想法

待整理。
`)
	if err := os.MkdirAll(filepath.Join(config.VaultPath, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("skills/demo/SKILL.md", "---\nname: Demo\ndescription: A demo skill\n---\n\n# Demo\n")
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

// knowledgeVaultDisabledManager is the "manager exists, switch off" case: the
// routes stay registered and every read answers 404.
func knowledgeVaultDisabledManager(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	manager, err := knowledgevault.NewManager(knowledgevault.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func knowledgeVaultServer(t *testing.T, manager *knowledgevault.Manager) (*httptest.Server, *http.Client, string) {
	t.Helper()
	server, client, csrf, _ := knowledgeVaultServerAt(t, manager, t.TempDir())
	return server, client, csrf
}

func knowledgeVaultServerAt(t *testing.T, manager *knowledgevault.Manager, dataDir string) (*httptest.Server, *http.Client, string, *store.Store) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	config := Config{Store: database, Auth: authService, App: app.NewService(database, secretStore, app.StubExecutor{})}
	if manager != nil {
		config.KnowledgeVault = manager
	}
	server := httptest.NewServer(New(config).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return server, client, csrf, database
}

func TestKnowledgeVaultAPIReportsDisabledWhenTheSwitchIsOff(t *testing.T) {
	server, client, _ := knowledgeVaultServer(t, knowledgeVaultDisabledManager(t))
	for _, path := range []string{
		"/api/v1/knowledge-vault/libraries",
		"/api/v1/knowledge-vault/tree?library=global",
		"/api/v1/knowledge-vault/doc/knowledge_http_deploy",
		"/api/v1/knowledge-vault/search?q=deploy",
		"/api/v1/knowledge-vault/recent",
		"/api/v1/knowledge-vault/inbox",
		"/api/v1/knowledge-vault/skills",
	} {
		response := requestJSON(t, client, http.MethodGet, server.URL+path, nil, "")
		var payload map[string]any
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusNotFound || payload["error"] != "knowledge_vault_disabled" {
			t.Fatalf("%s status = %d payload=%#v, want 404 knowledge_vault_disabled", path, response.StatusCode, payload)
		}
	}
	// The settings endpoint works while the data API is off.
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/knowledge-vault", nil, "")
	var settings map[string]any
	decodeResponse(t, response, &settings)
	if response.StatusCode != http.StatusOK || settings["settings"].(map[string]any)["enabled"] != false {
		t.Fatalf("settings status=%d payload=%#v", response.StatusCode, settings)
	}
	// The legacy API remains available and untouched.
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge/libraries", nil, "")
	var legacy map[string]any
	decodeResponse(t, response, &legacy)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("legacy knowledge API status = %d payload=%#v", response.StatusCode, legacy)
	}
}

func TestKnowledgeVaultAPIReadsLibrariesTreeDocumentSearchRecentInboxAndSkills(t *testing.T) {
	manager := knowledgeVaultManagerFixture(t)
	server, client, _ := knowledgeVaultServer(t, manager)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/libraries", nil, "")
	var libraries map[string]any
	decodeResponse(t, response, &libraries)
	if response.StatusCode != http.StatusOK || libraries["enabled"] != true || libraries["index_ready"] != true {
		t.Fatalf("libraries status=%d payload=%#v", response.StatusCode, libraries)
	}
	if libraries["vault_name"] != "HTTP Vault" || libraries["obsidian_vault"] != "HTTP Vault" {
		t.Fatalf("libraries metadata = %#v", libraries)
	}
	if len(libraries["libraries"].([]any)) == 0 {
		t.Fatalf("libraries = %#v", libraries)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/tree?library=global", nil, "")
	var tree map[string]any
	decodeResponse(t, response, &tree)
	if response.StatusCode != http.StatusOK || tree["count"].(float64) == 0 {
		t.Fatalf("tree status=%d payload=%#v", response.StatusCode, tree)
	}
	assertNoTreePath(t, tree["nodes"].([]any), "inbox")

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/doc/knowledge_http_deploy", nil, "")
	var doc map[string]any
	decodeResponse(t, response, &doc)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("doc status=%d payload=%#v", response.StatusCode, doc)
	}
	document := doc["doc"].(map[string]any)
	if document["title"] != "部署指南" || document["obsidian_ready"] != true {
		t.Fatalf("doc = %#v", document)
	}
	if len(document["outline"].([]any)) != 1 {
		t.Fatalf("outline = %#v", document["outline"])
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/search?q=%E9%83%A8%E7%BD%B2&library=global&type=practice&status=verified&tag=deploy", nil, "")
	var search map[string]any
	decodeResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || len(search["results"].([]any)) != 1 {
		t.Fatalf("search status=%d payload=%#v", response.StatusCode, search)
	}
	if search["results"].([]any)[0].(map[string]any)["id"] != "knowledge_http_deploy" {
		t.Fatalf("search result = %#v", search["results"])
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/recent?limit=5", nil, "")
	var recent map[string]any
	decodeResponse(t, response, &recent)
	if response.StatusCode != http.StatusOK || len(recent["items"].([]any)) == 0 {
		t.Fatalf("recent status=%d payload=%#v", response.StatusCode, recent)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/inbox", nil, "")
	var inbox map[string]any
	decodeResponse(t, response, &inbox)
	if response.StatusCode != http.StatusOK || inbox["count"].(float64) != 1 {
		t.Fatalf("inbox status=%d payload=%#v", response.StatusCode, inbox)
	}
	item := inbox["items"].([]any)[0].(map[string]any)
	if item["id"] != "knowledge_http_inbox" || item["path"] != "global/inbox/idea.md" {
		t.Fatalf("inbox item = %#v", item)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/skills", nil, "")
	var skills map[string]any
	decodeResponse(t, response, &skills)
	if response.StatusCode != http.StatusOK || len(skills["skills"].([]any)) != 1 {
		t.Fatalf("skills status=%d payload=%#v", response.StatusCode, skills)
	}
	skill := skills["skills"].([]any)[0].(map[string]any)
	if skill["name"] != "Demo" || skill["file_count"].(float64) != 1 {
		t.Fatalf("skill = %#v", skill)
	}
}

func TestKnowledgeVaultAPIRejectsWritesAndUnknownDocuments(t *testing.T) {
	manager := knowledgeVaultManagerFixture(t)
	server, client, csrf := knowledgeVaultServer(t, manager)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/doc/knowledge_http_deploy", map[string]any{"body": "changed"}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("write status = %d, want 404/405 (read-only API)", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/doc/missing", nil, "")
	var missing map[string]any
	decodeResponse(t, response, &missing)
	if response.StatusCode != http.StatusNotFound || missing["error"] != "knowledge_vault_document_not_found" {
		t.Fatalf("missing status=%d payload=%#v", response.StatusCode, missing)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/tree?library=missing:lib", nil, "")
	var invalid map[string]any
	decodeResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid["error"] != "invalid_knowledge_vault_library" {
		t.Fatalf("invalid status=%d payload=%#v", response.StatusCode, invalid)
	}
}

func assertNoTreePath(t *testing.T, nodes []any, forbidden string) {
	t.Helper()
	for _, raw := range nodes {
		node := raw.(map[string]any)
		path, _ := node["path"].(string)
		if path == forbidden || (len(path) > len(forbidden) && path[:len(forbidden)+1] == forbidden+"/") {
			t.Fatalf("reserved tree path %q leaked into the API", path)
		}
		if children, ok := node["children"].([]any); ok {
			assertNoTreePath(t, children, forbidden)
		}
	}
}

func TestAgentKnowledgeGuideHandler(t *testing.T) {
	manager := knowledgeVaultManagerFixture(t)
	server := &Server{knowledgeVault: manager}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/knowledge/guide", nil)
	recorder := httptest.NewRecorder()
	server.agentKnowledgeGuide(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("guide status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK    bool                 `json:"ok"`
		Guide knowledgevault.Guide `json:"guide"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Guide.Path != knowledgevault.GuideRelativePath || payload.Guide.Prompt == "" {
		t.Fatalf("guide payload = %#v", payload)
	}

	recorder = httptest.NewRecorder()
	server.agentKnowledgeGuide(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second guide status = %d", recorder.Code)
	}

	empty := &Server{}
	recorder = httptest.NewRecorder()
	empty.agentKnowledgeGuide(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing manager status = %d", recorder.Code)
	}
}
