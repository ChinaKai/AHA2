package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestKnowledgeVaultFilesHandlersReadRealTree(t *testing.T) {
	manager := knowledgeVaultManagerFixture(t)
	server := &Server{knowledgeVault: manager}

	recorder := httptest.NewRecorder()
	server.knowledgeVaultFiles(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-vault/files", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("root files status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range payload.Entries {
		names[entry.Name] = true
	}
	for _, expected := range []string{"global", "projects", "skills", "knowledge-base-guide.md"} {
		if !names[expected] {
			t.Fatalf("root listing missing %q: %#v", expected, payload.Entries)
		}
	}
	for _, hidden := range []string{".aha-vault.json", ".obsidian"} {
		if names[hidden] {
			t.Fatalf("root listing exposed %q", hidden)
		}
	}

	recorder = httptest.NewRecorder()
	server.knowledgeVaultFile(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-vault/file?path=global/general/deploy.md", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("file status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.knowledgeVaultFile(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-vault/file?path=.aha-vault.json", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("hidden file status=%d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	server.knowledgeVaultFile(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-vault/file?path=../outside.md", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("traversal status=%d", recorder.Code)
	}
}

func TestKnowledgeVaultRevealIsLoopbackOnly(t *testing.T) {
	manager := knowledgeVaultManagerFixture(t)
	called := false
	server := &Server{knowledgeVault: manager, revealPath: func(string) error { called = true; return nil }}

	remote := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-vault/reveal", nil)
	remote.RemoteAddr = "10.1.2.3:5555"
	recorder := httptest.NewRecorder()
	server.revealKnowledgeVaultPath(recorder, remote)
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("remote reveal status=%d called=%t", recorder.Code, called)
	}

	proxied := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-vault/reveal", nil)
	proxied.RemoteAddr = "127.0.0.1:5555"
	proxied.Header.Set("X-Forwarded-For", "203.0.113.9")
	recorder = httptest.NewRecorder()
	server.revealKnowledgeVaultPath(recorder, proxied)
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("proxied reveal status=%d called=%t", recorder.Code, called)
	}

	local := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-vault/reveal?path=global/general/deploy.md", nil)
	local.RemoteAddr = "127.0.0.1:5555"
	recorder = httptest.NewRecorder()
	server.revealKnowledgeVaultPath(recorder, local)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("loopback reveal status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}

	info := httptest.NewRecorder()
	server.knowledgeVaultOpenInfo(info, local)
	if info.Code != http.StatusOK {
		t.Fatalf("open-info status=%d", info.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(info.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["open_available"] != true {
		t.Fatalf("loopback open_available=%v", payload["open_available"])
	}
	remoteInfo := httptest.NewRecorder()
	server.knowledgeVaultOpenInfo(remoteInfo, remote)
	if err := json.Unmarshal(remoteInfo.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["open_available"] != false {
		t.Fatalf("remote open_available=%v", payload["open_available"])
	}
}

func TestKnowledgeCutoverRequiresExplicitConfirmation(t *testing.T) {
	manager := knowledgeVaultManagerFixture(t)
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service := app.NewService(database, nil, nil)
	service.SetKnowledgeVault(manager, true)
	server := &Server{store: database, app: service, knowledgeVault: manager}
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"project_id":"project_legacy","confirm":false}`)
	server.cutoverLegacyKnowledge(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/settings/knowledge-vault/cutover-legacy", body))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("cutover without confirmation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestKnowledgeVaultSearchIncludesArchiveByDefault(t *testing.T) {
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Archive HTTP"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(config.VaultPath, "archive", "old", "doc.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("---\nid: knowledge_archive_doc\nscope: archive\ntype: reference\nstatus: verified\ntitle: 归档文档\n---\n# 归档文档\n\n归档搜索内容。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	server := &Server{knowledgeVault: manager}

	recorder := httptest.NewRecorder()
	server.knowledgeVaultSearch(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-vault/search?q=%E5%BD%92%E6%A1%A3%E6%90%9C%E7%B4%A2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Results []struct {
			Path    string `json:"path"`
			Archive bool   `json:"archive"`
		} `json:"results"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 1 || !payload.Results[0].Archive || payload.Results[0].Path != "archive/old/doc.md" {
		t.Fatalf("archive-inclusive HTTP search = %#v", payload.Results)
	}

	recorder = httptest.NewRecorder()
	server.knowledgeVaultSearch(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-vault/search?q=%E5%BD%92%E6%A1%A3%E6%90%9C%E7%B4%A2&include_archives=false", nil))
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 0 {
		t.Fatalf("include_archives=false returned %#v", payload.Results)
	}
}
