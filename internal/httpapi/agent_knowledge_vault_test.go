package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestAgentKnowledgeVaultWriteAPI exercises the Phase 1 endpoint end to end:
// the write gate defaults off, a vault-reading project refuses to fall back to
// the legacy tables, enabling the gate writes the vault, the new entry is
// readable through the Agent API, feedback marks it stale, and removing the
// allowlist rolls writes back to the legacy proposal pipeline.
func TestAgentKnowledgeVaultWriteAPI(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	now := time.Now().UTC()

	root, err := database.EnsureKnowledgeRoot(ctx, "project", task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge_vault_write_seed", Scope: "project", ProjectID: task.ProjectID,
		ParentID: root.ID, Slug: "vault-write-seed", Type: "practice",
		Title: "Vault write seed", Body: "Seed body for the exported vault.",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}), done: make(chan struct{})}
	service := app.NewService(database, nil, executor)
	turn, err := service.SubmitMessage(ctx, task.ID, "exercise the vault write API")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}

	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Export(ctx, vaultConfig, databasePath, knowledgevault.ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	index, err := knowledgevault.OpenIndex(ctx, vaultConfig.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, vaultConfig.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.SetRuntimeWrite(false)
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", task.ProjectID)
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(false)

	capabilities := agentapi.NewCapabilities()
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store: database, App: service, AgentCapabilities: capabilities, KnowledgeVault: manager,
	}).Handler())
	defer server.Close()

	createPayload := map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title":        "Vault HTTP 写入测试",
		"summary":      "这是通过 HTTP Agent API 写入知识库的摘要内容，长度足够通过校验。",
		"body":         "这是通过 HTTP Agent API 写入知识库的正文内容，长度同样满足最小要求。",
		"search_query": "vault http write",
	}

	// Reading the vault with the write gate off must refuse, not silently split
	// brain onto the legacy tables.
	response := agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, createPayload)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusConflict || payload["error"] != "knowledge_vault_write_disabled" {
		t.Fatalf("write gate off status=%d payload=%#v", response.StatusCode, payload)
	}

	service.SetKnowledgeVaultRuntimeWrite(true)
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, createPayload)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusCreated || payload["source"] != "vault" {
		t.Fatalf("vault create status=%d payload=%#v", response.StatusCode, payload)
	}
	entry := payload["entry"].(map[string]any)
	entryID, _ := entry["id"].(string)
	entryPath, _ := entry["path"].(string)
	if entryID == "" || entryPath == "" || entry["revision"].(float64) != 1 {
		t.Fatalf("vault create entry=%#v", entry)
	}
	if _, statErr := os.Stat(filepath.Join(vaultConfig.VaultPath, filepath.FromSlash(entryPath))); statErr != nil {
		t.Fatalf("vault file missing: %v", statErr)
	}

	// The same Turn can read it back through the existing Agent knowledge API.
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge", http.MethodGet, token, nil)
	decodeResponse(t, response, &payload)
	found := false
	for _, raw := range payload["knowledge"].([]any) {
		if raw.(map[string]any)["id"] == entryID {
			found = true
		}
	}
	if !found {
		t.Fatalf("vault entry not visible to the Agent API: %#v", payload)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/feedback", http.MethodPost, token, map[string]any{
		"entry_id": entryID, "kind": "stale",
	})
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["source"] != "vault" {
		t.Fatalf("vault feedback status=%d payload=%#v", response.StatusCode, payload)
	}
	feedback := payload["feedback"].(map[string]any)
	if feedback["status"] != "stale" || feedback["revision"].(float64) != 1 || feedback["queued"] != true {
		t.Fatalf("vault feedback = %#v", feedback)
	}

	// Revise may omit scope: the server infers it from the entry and bumps the
	// revision instead of trusting a client path or scope.
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, map[string]any{
		"mode": "revise", "entry_id": entryID, "base_revision": 1, "type": "practice",
		"title":   "Vault HTTP 修订后的标题",
		"summary": "这是通过 HTTP Agent API 修订知识条目的摘要内容，长度足够。",
		"body":    "这是通过 HTTP Agent API 修订知识条目的正文内容，长度同样满足要求。",
	})
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["source"] != "vault" {
		t.Fatalf("vault revise status=%d payload=%#v", response.StatusCode, payload)
	}
	revised := payload["entry"].(map[string]any)
	if revised["revision"].(float64) != 2 || revised["path"] != entryPath || revised["status"] != "verified" {
		t.Fatalf("vault revise entry=%#v", revised)
	}

	// A client cannot redirect the write to another project.
	spoofed := map[string]any{"project_id": "project-other"}
	for key, value := range createPayload {
		spoofed[key] = value
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, spoofed)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("project spoof status=%d payload=%#v", response.StatusCode, payload)
	}

	// Roll the allowlist back while the write gate stays on: writes must follow
	// the read source and return to the legacy proposal pipeline.
	t.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", "project-elsewhere")
	legacyPayload := map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title":        "Legacy rollback 写入测试",
		"summary":      "这是回滚到旧知识表时使用的摘要内容，长度足够通过校验。",
		"body":         "这是回滚到旧知识表时使用的正文内容，长度同样满足最小要求。",
		"search_query": "legacy rollback write",
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, legacyPayload)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusCreated || payload["source"] != "legacy" {
		t.Fatalf("legacy rollback status=%d payload=%#v", response.StatusCode, payload)
	}
	if proposals, _ := payload["proposals"].([]any); len(proposals) != 1 {
		t.Fatalf("legacy rollback did not use the proposal pipeline: %#v", payload)
	}

	// The shared validation contract still applies on the legacy fallback.
	forbidden := map[string]any{}
	for key, value := range legacyPayload {
		forbidden[key] = value
	}
	forbidden["body"] = "这是回滚路径正文，但包含 password = supersecret123 这样的敏感内容。"
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, forbidden)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusBadRequest || payload["error"] != "knowledge_content_forbidden" {
		t.Fatalf("legacy forbidden content status=%d payload=%#v", response.StatusCode, payload)
	}

	// The legacy fallback also honours the 3-writes-per-Task/Turn budget.
	for index := 0; index < 2; index++ {
		request := map[string]any{
			"mode": "create", "scope": "project", "type": "practice",
			"title":        "Legacy 预算写入 " + string(rune('A'+index)),
			"summary":      "这是旧表回退路径的预算测试摘要，长度足够通过校验。",
			"body":         "这是旧表回退路径的预算测试正文，长度同样满足最小要求。",
			"search_query": "legacy budget",
		}
		response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, request)
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("legacy budget write %d status=%d payload=%#v", index, response.StatusCode, payload)
		}
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, legacyPayload)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusTooManyRequests || payload["error"] != "knowledge_write_budget_exceeded" {
		t.Fatalf("legacy budget status=%d payload=%#v", response.StatusCode, payload)
	}
}

// TestAgentKnowledgeFeedbackFollowsReadSourceAndNeverWritesLegacy covers the
// audit finding: both feedback endpoints wrote the legacy tables even while the
// Project read the vault. Feedback must be routed by the entry ID, and a global
// entry stays vault-backed even when the Project itself is unbound.
func TestAgentKnowledgeFeedbackFollowsReadSourceAndNeverWritesLegacy(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	now := time.Now().UTC()

	projectRoot, err := database.EnsureKnowledgeRoot(ctx, "project", task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	globalRoot, err := database.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	create := func(id, scope, projectID, parentID, title string) domain.KnowledgeEntry {
		item := domain.KnowledgeEntry{
			ID: id, Scope: scope, ProjectID: projectID, ParentID: parentID,
			Slug: "feedback-" + strings.ReplaceAll(id[len("knowledge_feedback_"):], "_", "-"), Type: "practice", Title: title,
			Body: "旧库反馈路由测试正文。", Status: domain.KnowledgeVerified, Revision: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := database.CreateKnowledge(ctx, item); err != nil {
			t.Fatal(err)
		}
		stored, err := database.Knowledge(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	projectEntry := create("knowledge_feedback_http_project", "project", task.ProjectID, projectRoot.ID, "旧库 HTTP 项目反馈行")
	globalEntry := create("knowledge_feedback_http_global", "global", "", globalRoot.ID, "旧库 HTTP 全局反馈行")

	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}), done: make(chan struct{})}
	service := app.NewService(database, nil, executor)
	turn, err := service.SubmitMessage(ctx, task.ID, "exercise feedback routing")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}

	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Export(ctx, vaultConfig, databasePath, knowledgevault.ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	index, err := knowledgevault.OpenIndex(ctx, vaultConfig.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, vaultConfig.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{projectEntry.ID, globalEntry.ID} {
		if err := manager.WithReader(func(reader *knowledgevault.Reader) error {
			found, lookupErr := reader.EntryByID(ctx, id)
			if lookupErr != nil || found.ID != id {
				t.Fatalf("exported vault is missing %s: found=%#v err=%v", id, found, lookupErr)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", task.ProjectID)
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)

	capabilities := agentapi.NewCapabilities()
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store: database, App: service, AgentCapabilities: capabilities, KnowledgeVault: manager,
	}).Handler())
	defer server.Close()

	assertLegacyUnchanged := func(id string, before domain.KnowledgeEntry) {
		t.Helper()
		after, err := database.Knowledge(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if after.FeedbackState != before.FeedbackState || after.HelpedCount != before.HelpedCount ||
			after.StaleCount != before.StaleCount || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("legacy row %s changed: before=%#v after=%#v", id, before, after)
		}
	}

	// The retired endpoint must follow the read source instead of writing the
	// legacy row whose ID it was given.
	var payload map[string]any
	response := agentRequest(t, server.URL+"/api/v1/agent/knowledge/"+projectEntry.ID+"/feedback", http.MethodPost, token, map[string]any{"kind": "stale"})
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("legacy feedback endpoint status=%d payload=%#v", response.StatusCode, payload)
	}
	if knowledge, _ := payload["knowledge"].(map[string]any); knowledge["status"] != "stale" {
		t.Fatalf("legacy feedback endpoint did not update the vault: %#v", payload)
	}
	assertLegacyUnchanged(projectEntry.ID, projectEntry)

	// The new endpoint writes the global vault entry while the Project is bound.
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/feedback", http.MethodPost, token, map[string]any{
		"entry_id": globalEntry.ID, "kind": "helped",
	})
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["source"] != "vault" {
		t.Fatalf("global feedback status=%d payload=%#v", response.StatusCode, payload)
	}
	assertLegacyUnchanged(globalEntry.ID, globalEntry)

	// A Project that is no longer bound still reads global knowledge from the
	// vault; global feedback must not fall back to the legacy row.
	t.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", "project-unbound-elsewhere")
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/feedback", http.MethodPost, token, map[string]any{
		"entry_id": globalEntry.ID, "kind": "helped",
	})
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["source"] != "vault" {
		t.Fatalf("unbound global feedback status=%d payload=%#v", response.StatusCode, payload)
	}
	assertLegacyUnchanged(globalEntry.ID, globalEntry)

	// With the write gate off the request is refused explicitly; it never falls
	// back to the legacy tables.
	service.SetKnowledgeVaultRuntimeWrite(false)
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/feedback", http.MethodPost, token, map[string]any{
		"entry_id": globalEntry.ID, "kind": "helped",
	})
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusConflict || payload["error"] != "knowledge_vault_write_disabled" {
		t.Fatalf("write-gate-off feedback status=%d payload=%#v", response.StatusCode, payload)
	}
	assertLegacyUnchanged(globalEntry.ID, globalEntry)
}
