package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func knowledgeVaultSettingsSnapshot(t *testing.T, client *http.Client, baseURL string) map[string]any {
	t.Helper()
	response := requestJSON(t, client, http.MethodGet, baseURL+"/api/v1/settings/knowledge-vault", nil, "")
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("settings status=%d payload=%#v", response.StatusCode, payload)
	}
	return payload["settings"].(map[string]any)
}

func TestKnowledgeVaultSettingsToggleInitAndRebuild(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	config := knowledgevault.DefaultConfig(dataDir)
	manager, err := knowledgevault.NewManager(config, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, dataDir)

	settings := knowledgeVaultSettingsSnapshot(t, client, server.URL)
	if settings["enabled"] != false || settings["vault_initialized"] != false || settings["source"] != "startup" {
		t.Fatalf("initial settings = %#v", settings)
	}

	// Turning the switch on before the vault exists keeps the empty state.
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/knowledge-vault", map[string]any{"enabled": true}, csrf)
	var toggled map[string]any
	decodeResponse(t, response, &toggled)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("toggle status=%d payload=%#v", response.StatusCode, toggled)
	}
	settings = toggled["settings"].(map[string]any)
	if settings["enabled"] != true || settings["source"] != "database" || settings["vault_ready"] != false {
		t.Fatalf("settings after enabling = %#v", settings)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/libraries", nil, "")
	var libraries map[string]any
	decodeResponse(t, response, &libraries)
	if response.StatusCode != http.StatusOK || libraries["enabled"] != true || libraries["vault_ready"] != false {
		t.Fatalf("libraries with an empty vault status=%d payload=%#v", response.StatusCode, libraries)
	}

	// Initialize seeds the fixed samples and enables the switch.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/init", nil, csrf)
	var initialized map[string]any
	decodeResponse(t, response, &initialized)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("init status=%d payload=%#v", response.StatusCode, initialized)
	}
	if initialized["samples"].(float64) != 4 || len(initialized["created"].([]any)) != 4 {
		t.Fatalf("init samples = %#v", initialized)
	}
	settings = initialized["settings"].(map[string]any)
	if settings["enabled"] != true || settings["vault_initialized"] != true || settings["index_ready"] != true {
		t.Fatalf("settings after init = %#v", settings)
	}
	if settings["entry_count"].(float64) < 5 {
		t.Fatalf("entry_count = %v, want at least 5", settings["entry_count"])
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/doc/"+knowledgevault.SampleWelcomeID, nil, "")
	var doc map[string]any
	decodeResponse(t, response, &doc)
	if response.StatusCode != http.StatusOK || !strings.Contains(doc["doc"].(map[string]any)["body"].(string), "文件是知识正本") {
		t.Fatalf("sample doc status=%d payload=%#v", response.StatusCode, doc)
	}

	// Off hides the data API but keeps the samples on disk; on restores them.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/knowledge-vault", map[string]any{"enabled": false}, csrf)
	var disabled map[string]any
	decodeResponse(t, response, &disabled)
	if response.StatusCode != http.StatusOK || disabled["settings"].(map[string]any)["enabled"] != false {
		t.Fatalf("disable status=%d payload=%#v", response.StatusCode, disabled)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/doc/"+knowledgevault.SampleWelcomeID, nil, "")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("doc while off status=%d, want 404", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/knowledge-vault", map[string]any{"enabled": true}, csrf)
	var enabled map[string]any
	decodeResponse(t, response, &enabled)
	if response.StatusCode != http.StatusOK || enabled["settings"].(map[string]any)["index_ready"] != true {
		t.Fatalf("re-enable status=%d payload=%#v", response.StatusCode, enabled)
	}

	// Editing a file and rebuilding makes the new text searchable.
	samplePath := filepath.Join(config.VaultPath, "global", "general", "示例", "欢迎使用.md")
	raw, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(samplePath, append(raw, []byte("\n重建验证标记\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/rebuild", nil, csrf)
	var rebuilt map[string]any
	decodeResponse(t, response, &rebuilt)
	if response.StatusCode != http.StatusOK || rebuilt["rebuild"].(map[string]any)["entries"].(float64) == 0 {
		t.Fatalf("rebuild status=%d payload=%#v", response.StatusCode, rebuilt)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/search?q=%E9%87%8D%E5%BB%BA%E9%AA%8C%E8%AF%81%E6%A0%87%E8%AE%B0", nil, "")
	var search map[string]any
	decodeResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || len(search["results"].([]any)) != 1 {
		t.Fatalf("search after rebuild status=%d payload=%#v", response.StatusCode, search)
	}

	// A second init refuses with 409 and leaves the edited file alone.
	before, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/init", map[string]any{"samples": true}, csrf)
	var conflict map[string]any
	decodeResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict["error"] != "knowledge_vault_exists" {
		t.Fatalf("second init status=%d payload=%#v", response.StatusCode, conflict)
	}
	after, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a rejected init modified the existing sample")
	}

	// The toggle is persisted for the next process start.
	stored, err := database.KnowledgeVaultSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || !stored.Configured {
		t.Fatalf("persisted settings = %+v, want enabled and configured", stored)
	}
}

func TestKnowledgeVaultRebuildRequiresAnEnabledInitializedVault(t *testing.T) {
	manager := knowledgeVaultDisabledManager(t)
	server, client, csrf := knowledgeVaultServer(t, manager)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/rebuild", nil, csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusConflict || payload["error"] != "knowledge_vault_disabled" {
		t.Fatalf("rebuild while disabled status=%d payload=%#v", response.StatusCode, payload)
	}
}
