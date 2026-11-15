package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func knowledgeRouteManager(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Route Vault"}); err != nil {
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
	write("projects/other/index.md", "---\nid: knowledge_root_project_other\ntitle: 其他项目\nscope: project\nproject_id: project_other\ntype: navigation\nstatus: verified\nis_index: true\n---\n其他入口。\n")
	write("projects/other/other-note.md", "---\nid: knowledge_other_note\ntitle: 其他文档\nscope: project\nproject_id: project_other\ntype: practice\nstatus: verified\n---\n其他正文。\n")
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

func knowledgeRouteHTTPServer(t *testing.T, manager *knowledgevault.Manager) (*httptest.Server, *http.Client, string, *store.Store, *app.Service) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(database, secretStore, app.StubExecutor{})
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
		App: service, KnowledgeVault: manager,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return server, client, csrf, database, service
}

func vaultDocumentExists(t *testing.T, manager *knowledgevault.Manager, id string) bool {
	t.Helper()
	found := false
	if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
		_, lookupErr := reader.EntryByID(context.Background(), id)
		found = lookupErr == nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func seedRouteProject(t *testing.T, database *store.Store, id string) domain.Project {
	t.Helper()
	now := time.Now().UTC()
	project := domain.Project{
		ID: id, Name: id, ProjectType: "folder", KnowledgePolicy: "enabled",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestWebKnowledgeAPIWritesVaultAndRefusesVaultDelete(t *testing.T) {
	manager := knowledgeRouteManager(t)
	server, client, csrf, database, _ := knowledgeRouteHTTPServer(t, manager)
	project := seedRouteProject(t, database, "project_other")

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge", map[string]any{
		"scope": "project", "project_id": project.ID, "type": "practice",
		"title": "Web 路由创建", "body": "写入路由测试正文，必须超过二十个字符才能满足文件知识库的正文长度要求。",
	}, csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d payload=%#v", response.StatusCode, payload)
	}
	created := payload["knowledge"].(map[string]any)
	entryID := created["id"].(string)
	if payload["source"] != "vault" {
		t.Fatalf("create source=%#v", payload["source"])
	}
	if _, err := database.Knowledge(context.Background(), entryID); err == nil {
		t.Fatal("HTTP vault create wrote the legacy table")
	}
	if !vaultDocumentExists(t, manager, entryID) {
		t.Fatal("vault create was not indexed")
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge/"+entryID, map[string]any{
		"scope": "project", "project_id": project.ID, "type": "practice",
		"title": "Web 路由更新", "body": "更新后的正文仍然需要超过二十个字符，以符合文件知识库的正文长度要求。",
	}, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["source"] != "vault" {
		t.Fatalf("update status=%d payload=%#v", response.StatusCode, payload)
	}
	updated := payload["knowledge"].(map[string]any)
	if updated["revision"].(float64) != 2 {
		t.Fatalf("updated revision=%#v", updated["revision"])
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/knowledge/"+entryID, nil, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("vault delete status=%d", response.StatusCode)
	}
	response.Body.Close()
	if !vaultDocumentExists(t, manager, entryID) {
		t.Fatal("refused delete removed the vault document")
	}
}

func TestWebKnowledgeAPIUnboundProjectStaysLegacyWithWarning(t *testing.T) {
	manager := knowledgeRouteManager(t)
	server, client, csrf, database, _ := knowledgeRouteHTTPServer(t, manager)
	project := seedRouteProject(t, database, "project_unbound")

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge", map[string]any{
		"scope": "project", "project_id": project.ID, "type": "practice",
		"title": "未绑定 Web 旧库", "body": "未绑定项目的正文也需要超过二十个字符，以便测试旧知识库回退。",
	}, csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d payload=%#v", response.StatusCode, payload)
	}
	if payload["source"] != "legacy" {
		t.Fatalf("create source=%#v", payload["source"])
	}
	if warning, _ := payload["warning"].(string); warning == "" {
		t.Fatalf("legacy fallback did not warn: %#v", payload)
	}
	entryID := payload["knowledge"].(map[string]any)["id"].(string)
	if _, err := database.Knowledge(context.Background(), entryID); err != nil {
		t.Fatalf("legacy entry missing: %v", err)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/knowledge/"+entryID, nil, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["source"] != "legacy" {
		t.Fatalf("legacy delete status=%d payload=%#v", response.StatusCode, payload)
	}
}
