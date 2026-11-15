package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
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
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, dataDir)

	settings := knowledgeVaultSettingsSnapshot(t, client, server.URL)
	if settings["enabled"] != false || settings["vault_initialized"] != false || settings["source"] != "startup" {
		t.Fatalf("initial settings = %#v", settings)
	}
	readiness := settings["readiness"].(map[string]any)
	if readiness["switch_visible"] != false || readiness["default_enabled"] != true || readiness["status"] != "needs_rebuild" {
		t.Fatalf("fresh-install readiness = %#v", readiness)
	}
	if settings["legacy"].(map[string]any)["entries"].(float64) != 0 {
		t.Fatalf("fresh install should have no legacy knowledge: %#v", settings["legacy"])
	}

	// The switch cannot be enabled before the vault/index exist, and the backend
	// must say exactly what is missing instead of accepting a no-op toggle.
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/knowledge-vault", map[string]any{"enabled": true}, csrf)
	var blocked map[string]any
	decodeResponse(t, response, &blocked)
	if response.StatusCode != http.StatusConflict || blocked["error"] != "knowledge_vault_not_ready" {
		t.Fatalf("toggle before init status=%d payload=%#v", response.StatusCode, blocked)
	}
	if len(blocked["missing"].([]any)) == 0 {
		t.Fatalf("blocked toggle should report missing checks: %#v", blocked)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/libraries", nil, "")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("libraries while off status=%d, want 404", response.StatusCode)
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

func TestKnowledgeVaultRebuildWhileDisabledNeedsAnInitializedVault(t *testing.T) {
	manager := knowledgeVaultDisabledManager(t)
	server, client, csrf := knowledgeVaultServer(t, manager)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/rebuild", nil, csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusConflict || payload["error"] != "knowledge_vault_not_initialized" {
		t.Fatalf("rebuild without a vault status=%d payload=%#v", response.StatusCode, payload)
	}
	// Rebuild must work while the switch is off so migration/import and Git pull
	// can refresh the index before the owner enables the vault.
	if _, err := manager.Initialize(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/rebuild", nil, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("rebuild while disabled status=%d payload=%#v", response.StatusCode, payload)
	}
	if payload["settings"].(map[string]any)["enabled"] != false {
		t.Fatalf("rebuild while disabled silently enabled the vault: %#v", payload)
	}
}

func TestKnowledgeVaultMigrateEndpointBlocksUntilVerified(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	manager, err := knowledgevault.NewManager(knowledgevault.DefaultConfig(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, dataDir)
	now := time.Now().UTC()
	if err := database.EnsureGlobalKnowledgeCategories(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge_global_general_legacy_doc", Scope: "global",
		ParentID: store.GlobalGeneralKnowledgeID, Slug: "legacy-doc", Type: "practice",
		Title: "旧知识文档", Body: "升级前写入的旧知识正文。", Status: domain.KnowledgeVerified,
		Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings := knowledgeVaultSettingsSnapshot(t, client, server.URL)
	readiness := settings["readiness"].(map[string]any)
	if readiness["status"] != "needs_migration" || readiness["can_enable"] != false || readiness["switch_visible"] != true {
		t.Fatalf("upgrade readiness = %#v", readiness)
	}
	if settings["legacy"].(map[string]any)["entries"].(float64) != 1 {
		t.Fatalf("legacy inventory = %#v", settings["legacy"])
	}
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/knowledge-vault", map[string]any{"enabled": true}, csrf)
	var blockedEnable map[string]any
	decodeResponse(t, response, &blockedEnable)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("enable before migration status=%d payload=%#v", response.StatusCode, blockedEnable)
	}
	// Creating an empty vault would hide the legacy document, so init is refused.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/init", map[string]any{"samples": false}, csrf)
	var rejected map[string]any
	decodeResponse(t, response, &rejected)
	if response.StatusCode != http.StatusConflict || rejected["error"] != "knowledge_vault_migration_required" {
		t.Fatalf("init on upgrade status=%d payload=%#v", response.StatusCode, rejected)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/migrate", nil, csrf)
	var migrated map[string]any
	decodeResponse(t, response, &migrated)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("migrate status=%d payload=%#v", response.StatusCode, migrated)
	}
	report := migrated["migration"].(map[string]any)
	if report["verified_entries"].(float64) == 0 || report["backup_path"].(string) == "" {
		t.Fatalf("migration report = %#v", report)
	}
	settings = migrated["settings"].(map[string]any)
	if settings["enabled"] != true || settings["source"] != "database" || settings["import_source"] != "legacy-migration" {
		t.Fatalf("settings after migration = %#v", settings)
	}
	if !strings.HasPrefix(settings["migration_verified_at"].(string), "20") {
		t.Fatalf("migration_verified_at = %v", settings["migration_verified_at"])
	}
	if settings["readiness"].(map[string]any)["status"] != "ready" {
		t.Fatalf("readiness after migration = %#v", settings["readiness"])
	}
	inventory, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil || inventory.Entries != 1 {
		t.Fatalf("legacy data after migration = %+v err=%v", inventory, err)
	}
}

// A 0.5.x install can already be running on the vault while the legacy
// database is still present and no migration/import is verified. That state
// must read as ready (legacy is a rollback copy), and the migration endpoint
// must refuse instead of exporting the old database over the live pages.
func TestKnowledgeVaultEnabledUpgradeStaysReadyAndRefusesMigrationOverwrite(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	manager, err := knowledgevault.NewManager(knowledgevault.DefaultConfig(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, dataDir)
	if _, err := manager.Initialize(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := database.SetKnowledgeVaultSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureGlobalKnowledgeCategories(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge_global_general_legacy_overwrite", Scope: "global",
		ParentID: store.GlobalGeneralKnowledgeID, Slug: "legacy-overwrite", Type: "practice",
		Title: "旧知识", Body: "如果迁移覆盖就会写入的文件。", Status: domain.KnowledgeVerified,
		Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	settings := knowledgeVaultSettingsSnapshot(t, client, server.URL)
	if settings["enabled"] != true || settings["configured"] != true {
		t.Fatalf("enabled upgrade settings = %#v", settings)
	}
	if settings["entry_count"].(float64) == 0 {
		t.Fatalf("enabled upgrade index is empty: %#v", settings)
	}
	readiness := settings["readiness"].(map[string]any)
	if status := readiness["status"]; status != "ready" && status != "degraded" {
		t.Fatalf("enabled upgrade readiness = %#v, want ready or degraded", readiness)
	}
	migrationOK := false
	for _, raw := range readiness["checks"].([]any) {
		check := raw.(map[string]any)
		if check["id"] == "migration" && check["status"] == "ok" &&
			strings.Contains(fmt.Sprint(check["message"]), "只读回滚") {
			migrationOK = true
		}
	}
	if !migrationOK {
		t.Fatalf("enabled upgrade must explain the legacy rollback copy: %#v", readiness["checks"])
	}

	samplePath := filepath.Join(manager.Config().VaultPath, filepath.FromSlash(knowledgevault.SampleDocuments(time.Now())[0].Path))
	before, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/migrate", nil, csrf)
	var refused map[string]any
	decodeResponse(t, response, &refused)
	if response.StatusCode != http.StatusConflict || refused["error"] != "knowledge_vault_target_not_empty" {
		t.Fatalf("migrate over the live vault status=%d payload=%#v", response.StatusCode, refused)
	}
	after, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("live page disappeared after refused migration: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("refused migration overwrote a live vault page")
	}
	inventory, err := database.LegacyKnowledgeInventory(ctx)
	if err != nil || inventory.Entries != 1 {
		t.Fatalf("legacy data after refused migration = %+v err=%v", inventory, err)
	}
	stored, err := database.KnowledgeVaultSettings(ctx)
	if err != nil || !stored.Enabled || stored.VaultPopulated() {
		t.Fatalf("settings after refused migration = %+v err=%v", stored, err)
	}
}

func TestKnowledgeVaultImportEndpointLocalAndCredentialSafety(t *testing.T) {
	dataDir := t.TempDir()
	manager, err := knowledgevault.NewManager(knowledgevault.DefaultConfig(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, csrf, _ := knowledgeVaultServerAt(t, manager, dataDir)

	sourceConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(sourceConfig, knowledgevault.InitOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	// The vault exists but the switch is still off, so import must be reachable
	// from settings without enabling the data API first.
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/import", map[string]any{
		"kind": "local", "path": sourceConfig.VaultPath,
	}, csrf)
	var imported map[string]any
	decodeResponse(t, response, &imported)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("import status=%d payload=%#v", response.StatusCode, imported)
	}
	settings := imported["settings"].(map[string]any)
	if settings["enabled"] != true || settings["import_source"] != "existing-vault" {
		t.Fatalf("settings after import = %#v", settings)
	}
	if imported["import"].(map[string]any)["verified_at"].(string) == "0001-01-01T00:00:00Z" {
		t.Fatalf("import was not verified: %#v", imported["import"])
	}

	const secret = "supersecret-token"
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/knowledge-vault/import", map[string]any{
		"kind": "git", "remote": "ssh://user:" + secret + "@example.com/repo.git",
	}, csrf)
	var failed map[string]any
	decodeResponse(t, response, &failed)
	if response.StatusCode != http.StatusBadRequest || failed["error"] != "invalid_git_remote" {
		t.Fatalf("credential import status=%d payload=%#v", response.StatusCode, failed)
	}
	if strings.Contains(strings.ToLower(fmt.Sprint(failed)), strings.ToLower(secret)) {
		t.Fatalf("credential import response leaked the token: %#v", failed)
	}
}
