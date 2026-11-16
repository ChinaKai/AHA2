package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
