package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// petMCPToolCall calls one pet MCP tool through the real loopback HTTP route
// with an optional bearer token and returns the text plus the tool error flag.
func petMCPToolCall(t *testing.T, client *http.Client, baseURL, token, tool string, arguments any) (string, bool) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/pet/mcp", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	decodeResponse(t, response, &body)
	result, ok := body["result"].(map[string]any)
	if response.StatusCode != http.StatusOK || !ok {
		t.Fatalf("%s status=%d body=%#v", tool, response.StatusCode, body)
	}
	isError, _ := result["isError"].(bool)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("%s has no content: %#v", tool, result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("%s returned empty text: %#v", tool, result)
	}
	return text, isError
}

func petMCPToolText(t *testing.T, client *http.Client, baseURL, tool string, arguments any) string {
	t.Helper()
	text, isError := petMCPToolCall(t, client, baseURL, "", tool, arguments)
	if isError {
		t.Fatalf("%s returned error: %s", tool, text)
	}
	return text
}

func petMCPToolTextForToken(t *testing.T, client *http.Client, baseURL, token, tool string, arguments any) string {
	t.Helper()
	text, isError := petMCPToolCall(t, client, baseURL, token, tool, arguments)
	if isError {
		t.Fatalf("%s returned error: %s", tool, text)
	}
	return text
}

// petMCPToolHTTPStatus posts one MCP call and returns only the HTTP status.
// Authentication rejections answer before a JSON-RPC result exists, so they
// cannot use petMCPToolCall.
func petMCPToolHTTPStatus(t *testing.T, client *http.Client, baseURL, token, tool string, arguments any) int {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/pet/mcp", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func petWorkStateHTTP(t *testing.T, client *http.Client, baseURL string) app.PetWorkState {
	t.Helper()
	text := petMCPToolText(t, client, baseURL, "aha_pet_work_state", map[string]any{})
	var state app.PetWorkState
	if err := json.Unmarshal([]byte(text), &state); err != nil {
		t.Fatalf("decode work state: %v\n%s", err, text)
	}
	return state
}

func TestKnowledgeSyncNotificationsReachPetWorkStateAndClearOnlyAfterSyncRecovers(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	vaultConfig := knowledgevault.DefaultConfig(dataDir)
	vaultConfig.AllowLocalRemote = true
	if _, err := knowledgevault.Bootstrap(vaultConfig, knowledgevault.InitOptions{VaultName: "HTTP Vault", Commit: false}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.Git().Init(ctx); err != nil {
		t.Fatal(err)
	}

	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	appService.SetKnowledgeVault(manager, true)
	t.Cleanup(func() { _ = appService.Shutdown(context.Background()) })
	// Wire the syncer exactly like production: emitted events become pet
	// events and a successful sync closes stale notifications.
	syncer := knowledgevault.NewSyncer(manager, database, knowledgevault.SyncerConfig{
		Emit: func(event knowledgevault.SyncEvent) {
			appService.RecordKnowledgeSyncEvent(ctx, event.Kind, event.Detail)
		},
		Recovered: func() {
			_, _ = appService.ResolveKnowledgeSyncNotifications(ctx)
		},
	})
	authService := auth.NewService(database, "setup-test", time.Hour)
	if err := syncer.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	// The pet MCP client authenticates with the Turn capability so the ack
	// tool can verify the caller belongs to the pet Task.
	capabilities := agentapi.NewCapabilities()
	petToken, err := capabilities.Issue("task_pet", "main", "turn-pet", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := capabilities.Issue("task_other", "main", "turn-other", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService,
		KnowledgeVault: manager, KnowledgeVaultSync: syncer,
		AgentCapabilities: capabilities,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitHelper(t, t.TempDir(), "init", "--bare", "-b", "main", remote)
	if _, err := manager.Git().SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultConfig.VaultPath, "base.md"), []byte("# Base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Push(ctx); err != nil {
		t.Fatal(err)
	}
	// First cycle finishes the one-time preflight and syncs cleanly.
	syncer.RunOnce(ctx)
	if status := syncer.Status(); status.Paused {
		t.Fatalf("clean baseline paused: %+v", status)
	}

	// Diverge: the remote and the local vault both advance.
	clone := filepath.Join(t.TempDir(), "other-writer")
	gitHelper(t, t.TempDir(), "clone", remote, clone)
	if err := os.WriteFile(filepath.Join(clone, "remote.md"), []byte("# Remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHelper(t, clone, "add", "-A")
	gitHelper(t, clone, "commit", "-m", "remote side")
	gitHelper(t, clone, "push", "origin", "main")
	if err := os.WriteFile(filepath.Join(vaultConfig.VaultPath, "local.md"), []byte("# Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Commit(ctx, "local side"); err != nil {
		t.Fatal(err)
	}
	syncer.RunOnce(ctx)
	if status := syncer.Status(); !status.Paused || !status.Conflict || status.PauseReason != "conflict" {
		t.Fatalf("expected conflict pause: %+v", status)
	}

	state := petWorkStateHTTP(t, client, server.URL)
	if len(state.Notifications) != 1 {
		t.Fatalf("work state notifications=%#v", state.Notifications)
	}
	notification := state.Notifications[0]
	if notification.Kind != knowledgevault.SyncEventConflict || notification.Status != "open" ||
		notification.ID == "" || notification.At == "" || !strings.Contains(notification.Detail, "已分叉") {
		t.Fatalf("conflict notification=%#v", notification)
	}
	// Re-running the same (local HEAD, remote HEAD) pair must not create a
	// second notification even though the syncer re-enters the conflict.
	syncer.ResetPause()
	syncer.RunOnce(ctx)
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 1 {
		t.Fatalf("duplicate conflict notification: %#v", state.Notifications)
	}

	// Saving the same remote or re-running init while the branches still
	// diverge only retries the loop; it must not hide the unresolved conflict.
	// The syncer de-duplicates by commit pair, so a closed notification would
	// never be re-emitted.
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/git/remote",
		map[string]any{"remote": remote, "branch": "main"}, csrf)
	if response.StatusCode != http.StatusOK {
		var body map[string]any
		decodeResponse(t, response, &body)
		t.Fatalf("save remote status=%d body=%#v", response.StatusCode, body)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/git/init", nil, csrf)
	if response.StatusCode != http.StatusOK {
		var body map[string]any
		decodeResponse(t, response, &body)
		t.Fatalf("repeat init status=%d body=%#v", response.StatusCode, body)
	}
	response.Body.Close()
	syncer.RunOnce(ctx)
	if status := syncer.Status(); !status.Paused || !status.Conflict {
		t.Fatalf("divergence disappeared unexpectedly: %+v", status)
	}
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 1 ||
		state.Notifications[0].ID != notification.ID {
		t.Fatalf("remote/init hid unresolved conflict: %#v", state.Notifications)
	}

	// Acknowledging hides the notification from the next wake-up but must not
	// resume the paused sync or touch the divergence. An unidentified caller or
	// a capability for another Task must be rejected before any row changes.
	rejectText, rejected := petMCPToolCall(t, client, server.URL, "", "aha_ack_knowledge_sync_notification",
		map[string]any{"ids": []string{notification.ID}})
	if !rejected || !strings.Contains(rejectText, "宠物 Task") {
		t.Fatalf("unidentified ack text=%q rejected=%v", rejectText, rejected)
	}
	rejectText, rejected = petMCPToolCall(t, client, server.URL, otherToken, "aha_ack_knowledge_sync_notification",
		map[string]any{"ids": []string{notification.ID}})
	if !rejected || !strings.Contains(rejectText, "宠物 Task") {
		t.Fatalf("foreign-task ack text=%q rejected=%v", rejectText, rejected)
	}
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 1 {
		t.Fatalf("rejected ack changed notifications: %#v", state.Notifications)
	}
	// Read-only tools accept any identity; mutating and approval-creating tools
	// require the pet Task's capability. Neither a tokenless call nor a valid
	// capability issued for another Task may change records.
	approvalsBefore, err := appService.PetApprovals(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ label, token, want string }{
		{"no token", "", "只读工具"},
		{"foreign task", otherToken, "宠物 Task"},
	} {
		rejectText, rejected = petMCPToolCall(t, client, server.URL, tc.token, "pet_submit_idea",
			map[string]any{"title": "denied probe", "summary": "must not be created", "reason": "regression test"})
		if !rejected || !strings.Contains(rejectText, tc.want) {
			t.Fatalf("%s mutating call text=%q rejected=%v", tc.label, rejectText, rejected)
		}
	}
	approvalsAfter, err := appService.PetApprovals(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvalsAfter) != len(approvalsBefore) {
		t.Fatalf("rejected mutating calls created approvals: before=%d after=%d", len(approvalsBefore), len(approvalsAfter))
	}
	if text := petMCPToolText(t, client, server.URL, "aha_pet_work_state", map[string]any{}); !strings.Contains(text, notification.ID) {
		t.Fatalf("tokenless read-only call failed: %s", text)
	}
	if text := petMCPToolTextForToken(t, client, server.URL, otherToken, "aha_pet_work_state", map[string]any{}); !strings.Contains(text, notification.ID) {
		t.Fatalf("foreign-task read-only call failed: %s", text)
	}
	if status := petMCPToolHTTPStatus(t, client, server.URL, "not-a-valid-capability", "aha_pet_work_state", map[string]any{}); status != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status=%d want=%d", status, http.StatusUnauthorized)
	}
	ackText := petMCPToolTextForToken(t, client, server.URL, petToken, "aha_ack_knowledge_sync_notification",
		map[string]any{"ids": []string{notification.ID}})
	if !strings.Contains(ackText, `"acknowledged":1`) {
		t.Fatalf("ack response=%s", ackText)
	}
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 0 {
		t.Fatalf("acked notification still visible: %#v", state.Notifications)
	}
	if status := syncer.Status(); !status.Paused || !status.Conflict {
		t.Fatalf("ack must not resume auto-sync: %+v", status)
	}

	// A later pause writes a fresh open notification; resolving the divergence
	// and running a manual push must clear the pause and close the notification.
	if err := database.AppendPetEvent(ctx, domain.PetEvent{
		ID: "pet_event_http_paused", At: time.Now().UTC(), Actor: "aha",
		Kind: knowledgevault.SyncEventPaused, Detail: "知识库自动同步连续失败，已暂停：pull failed", Status: "open",
	}); err != nil {
		t.Fatal(err)
	}
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 1 ||
		state.Notifications[0].Kind != knowledgevault.SyncEventPaused {
		t.Fatalf("paused notification missing: %#v", state.Notifications)
	}
	gitHelper(t, vaultConfig.VaultPath, "fetch", "origin", "main")
	gitHelper(t, vaultConfig.VaultPath, "merge", "--no-edit", "origin/main")
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/git/push", nil, csrf)
	var pushed map[string]any
	decodeResponse(t, response, &pushed)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("manual push status=%d body=%#v", response.StatusCode, pushed)
	}
	if status := syncer.Status(); status.Paused || status.Conflict {
		t.Fatalf("manual push did not resume auto-sync: %+v", status)
	}
	if state = petWorkStateHTTP(t, client, server.URL); len(state.Notifications) != 0 {
		t.Fatalf("notifications survived recovery: %#v", state.Notifications)
	}
	// The rows are closed, not deleted: the pet audited them once and the
	// transition is still readable.
	events, err := database.ListPetEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, event := range events {
		statuses[event.ID] = event.Status
	}
	if statuses[notification.ID] != "acknowledged" || statuses["pet_event_http_paused"] != "resolved" {
		t.Fatalf("notification statuses=%#v", statuses)
	}
}
