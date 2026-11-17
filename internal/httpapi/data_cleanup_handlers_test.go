package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func newDataManagementTestServer(t *testing.T) (*httptest.Server, *store.Store, *sql.DB, string) {
	t.Helper()
	return newDataManagementTestServerWithConfig(t, nil)
}

func newDataManagementTestServerWithConfig(t *testing.T, configure func(*Config)) (*httptest.Server, *store.Store, *sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	authService := auth.NewService(database, "setup-test", time.Hour)
	config := Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}
	if configure != nil {
		configure(&config)
	}
	server := httptest.NewServer(New(config).Handler())
	t.Cleanup(server.Close)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	return server, database, raw, path
}

func TestDataManagementCleanupRoutesRequireAuth(t *testing.T) {
	t.Parallel()
	server, _, _, _ := newDataManagementTestServer(t)
	anonymous := newCookieClient(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system/data/backups"},
		{http.MethodPut, "/api/v1/system/data/conversation/retention"},
		{http.MethodPost, "/api/v1/system/data/conversation/preview"},
		{http.MethodPost, "/api/v1/system/data/conversation/purge"},
		{http.MethodGet, "/api/v1/system/data/database/offline-compaction"},
	} {
		response := requestJSON(t, anonymous, route.method, server.URL+route.path, map[string]any{"confirm": true}, "")
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status=%d", route.method, route.path, response.StatusCode)
		}
	}
}

func TestBackupCleanupPreviewAndConfirmKeepNewest(t *testing.T) {
	t.Parallel()
	server, _, _, path := newDataManagementTestServer(t)
	backupRoot := filepath.Join(filepath.Dir(path), "backups")
	base := time.Now().Add(-72 * time.Hour)
	for index, name := range []string{"user-installed-1", "user-installed-2", "user-installed-3"} {
		writeTestFile(t, filepath.Join(backupRoot, name, "aha2.exe"), 10)
		modTime := base.Add(time.Duration(index) * time.Hour)
		if err := os.Chtimes(filepath.Join(backupRoot, name), modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/system/data/backups", nil, "")
	var preview struct {
		Preview struct {
			Keep     int `json:"keep"`
			Retained struct {
				Name string `json:"name"`
			} `json:"retained"`
			Candidates []struct {
				Name string `json:"name"`
			} `json:"candidates"`
			FreedBytes int64 `json:"freed_bytes"`
			CanClean   bool  `json:"can_clean"`
		} `json:"preview"`
	}
	decodeResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Preview.Keep != 1 || len(preview.Preview.Candidates) != 2 ||
		preview.Preview.Retained.Name != "user-installed-3" || preview.Preview.FreedBytes != 20 || !preview.Preview.CanClean {
		t.Fatalf("preview status=%d body=%#v", response.StatusCode, preview.Preview)
	}
	if _, err := os.Stat(filepath.Join(backupRoot, "user-installed-1")); err != nil {
		t.Fatalf("preview deleted a backup: %v", err)
	}

	// The keep value is no longer part of the payload: the strict decoder
	// rejects it instead of silently honoring a client-chosen retention count.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/cleanup",
		map[string]any{"category": "backups", "keep": 5, "confirm": true}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("keep field status=%d, want 400", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/cleanup",
		map[string]any{"category": "backups", "confirm": true}, csrf)
	var cleaned struct {
		Result DataCleanupResult `json:"result"`
	}
	decodeResponse(t, response, &cleaned)
	if response.StatusCode != http.StatusOK || cleaned.Result.Removed != 2 || cleaned.Result.FreedBytes != 20 {
		t.Fatalf("cleanup status=%d body=%#v", response.StatusCode, cleaned.Result)
	}
	if _, err := os.Stat(filepath.Join(backupRoot, "user-installed-3")); err != nil {
		t.Fatalf("newest backup removed: %v", err)
	}
	for _, name := range []string{"user-installed-1", "user-installed-2"} {
		if _, err := os.Stat(filepath.Join(backupRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("old backup %s survived: %v", name, err)
		}
	}
	var audits int
	if err := rawCount(t, path, `SELECT COUNT(*) FROM audit_events WHERE action='data.cleanup'`, &audits); err != nil || audits != 1 {
		t.Fatalf("backup cleanup audit=%d err=%v", audits, err)
	}
}

func TestConversationCleanupEndpointsProtectActiveApprovalsAndReplies(t *testing.T) {
	ctx := context.Background()
	server, database, raw, _ := newDataManagementTestServer(t)
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)

	insertTask := func(status domain.TaskStatus) string {
		task := createHardwareAPITask(t, database)
		if status != domain.TaskActive {
			if _, err := raw.ExecContext(ctx, `UPDATE tasks SET status=? WHERE id=?`, status, task.ID); err != nil {
				t.Fatal(err)
			}
		}
		return task.ID
	}
	insertItem := func(taskID, category, kind string) string {
		id := domain.NewID("conversation")
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO conversation_items(
				id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
				category,kind,summary,payload_json,created_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, taskID, "", "", "main", "main", "main", "main", "", category, kind, kind, "{}", old.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		return id
	}

	terminalTask := insertTask(domain.TaskCompleted)
	eligible := insertItem(terminalTask, "tool", "agent_command_finished")
	updateItem := insertItem(terminalTask, "update", "agent_progress")
	chatItem := insertItem(terminalTask, "chat", "agent_message")
	activeTask := insertTask(domain.TaskActive)
	activeItem := insertItem(activeTask, "tool", "agent_command_finished")
	approvalTask := insertTask(domain.TaskCompleted)
	approvalItem := insertItem(approvalTask, "tool", "agent_command_finished")
	if _, err := raw.ExecContext(ctx, `INSERT INTO task_permission_requests(
		id,task_id,action,status,created_at,updated_at,expires_at
	) VALUES(?,?,?,?,?,?,?)`,
		domain.NewID("permission"), approvalTask, "shell", "pending", old.Format(time.RFC3339Nano), old.Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	// Default settings: tool only.
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/conversation/preview", map[string]any{}, csrf)
	var preview struct {
		Plan domain.ConversationPurgePlan `json:"plan"`
	}
	decodeResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Plan.EligibleRows != 1 || preview.Plan.PurgeUpdate {
		t.Fatalf("preview status=%d plan=%#v", response.StatusCode, preview.Plan)
	}

	// At least one of tool/update must stay enabled.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/system/data/conversation/retention",
		map[string]any{"purge_tool": false}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty selection status=%d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/system/data/conversation/retention",
		map[string]any{"retention_days": 30, "purge_tool": true, "purge_update": true}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save settings status=%d", response.StatusCode)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/conversation/preview", map[string]any{}, csrf)
	decodeResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Plan.EligibleRows != 2 || !preview.Plan.PurgeUpdate {
		t.Fatalf("tool+update preview status=%d plan=%#v", response.StatusCode, preview.Plan)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/conversation/purge", map[string]any{"confirm": true}, csrf)
	var purged struct {
		Result domain.ConversationPurgeResult `json:"result"`
		Audit  map[string]any                 `json:"audit"`
	}
	decodeResponse(t, response, &purged)
	if response.StatusCode != http.StatusOK || purged.Result.DeletedRows != 2 || purged.Audit["action"] != "data.conversation.purge" {
		t.Fatalf("purge status=%d body=%#v", response.StatusCode, purged)
	}
	if purged.Result.BackupPath == "" {
		t.Fatal("conversation purge did not report a pre-delete backup")
	}
	if _, err := os.Stat(purged.Result.BackupPath); err != nil {
		t.Fatalf("conversation purge backup missing: %v", err)
	}
	for label, id := range map[string]string{
		"eligible tool": eligible, "update": updateItem,
	} {
		if conversationItemPresent(t, raw, id) {
			t.Fatalf("%s was not deleted", label)
		}
	}
	for label, id := range map[string]string{
		"chat/final answer": chatItem, "active task": activeItem, "pending approval": approvalItem,
	} {
		if !conversationItemPresent(t, raw, id) {
			t.Fatalf("%s was deleted", label)
		}
	}
	var auditData string
	if err := raw.QueryRowContext(ctx, `SELECT data_json FROM audit_events WHERE action='data.conversation.purge'`).Scan(&auditData); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(auditData), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["deleted_rows"] != float64(2) || payload["purge_update"] != true {
		t.Fatalf("audit=%#v", payload)
	}
	var settingsAudits int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='data.conversation.retention.update'`).Scan(&settingsAudits); err != nil || settingsAudits != 1 {
		t.Fatalf("settings audit=%d err=%v", settingsAudits, err)
	}
}

func TestOfflineCompactionInfoIsReadOnlyAndReadsTheLastResult(t *testing.T) {
	ctx := context.Background()
	server, _, raw, path := newDataManagementTestServer(t)
	client := newCookieClient(t)
	registerOwner(t, client, server.URL)
	dataDir := filepath.Dir(path)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/system/data/database/offline-compaction", nil, "")
	var info struct {
		Info domain.OfflineCompactionInfo `json:"info"`
	}
	decodeResponse(t, response, &info)
	if response.StatusCode != http.StatusOK || !info.Info.DatabaseExists || info.Info.FileBytes == 0 {
		t.Fatalf("info status=%d info=%#v", response.StatusCode, info.Info)
	}
	if !strings.Contains(info.Info.Command, "aha2 db compact") || !strings.Contains(info.Info.Command, dataDir) {
		t.Fatalf("command=%q", info.Info.Command)
	}
	if info.Info.LatestResult != nil {
		t.Fatalf("unexpected latest result before the CLI ran: %#v", info.Info.LatestResult)
	}

	// The old online entry point is gone in both directions: POST must not
	// reach a handler that could VACUUM while the service is serving.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/database/offline-compaction", map[string]any{"confirm": true}, "")
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed && response.StatusCode != http.StatusNotFound {
		t.Fatalf("POST offline-compaction status=%d, want 404 or 405", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/database/compaction", map[string]any{"confirm": true}, "")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("removed online compaction status=%d, want 404", response.StatusCode)
	}

	backupPath := filepath.Join(dataDir, "backups", "offline-compact-test")
	result := domain.OfflineCompactionResult{
		Status: domain.OfflineCompactionStatusOK, DataDir: dataDir, DatabasePath: path,
		BackupPath: backupPath, BackupKept: true, KeepBackup: true,
		BeforeBytes: 1000, AfterBytes: 800, FreedBytes: 200,
		IntegrityCheck: "ok", IntegrityOK: true, RowCountsOK: true,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, domain.OfflineCompactionResultFile), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/system/data/database/offline-compaction", nil, "")
	decodeResponse(t, response, &info)
	if response.StatusCode != http.StatusOK || info.Info.LatestResult == nil {
		t.Fatalf("latest result missing: status=%d info=%#v", response.StatusCode, info.Info)
	}
	if info.Info.LatestResult.BackupPath != backupPath || info.Info.LatestResult.FreedBytes != 200 {
		t.Fatalf("latest result=%#v", info.Info.LatestResult)
	}

	// The endpoint is read-only: no audit event and no schema/VACUUM writes.
	var audits int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='data.database.compact'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("compaction audit=%d err=%v", audits, err)
	}
}

func rawCount(t *testing.T, path, query string, target *int) error {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer raw.Close()
	return raw.QueryRow(query).Scan(target)
}

func conversationItemPresent(t *testing.T, raw *sql.DB, id string) bool {
	t.Helper()
	var exists bool
	if err := raw.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversation_items WHERE id=?)`, id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}
