package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestSyncSettingsAPIKeepsTokenOutOfResponses(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore := &fakeSecretStore{}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), Secrets: secretStore}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/sync", map[string]any{"enabled": true, "endpoint": "https://sync.example", "device_id": "device-one", "interval_seconds": 60, "token": "top-secret-token", "passphrase": "long-secret-passphrase", "provider_ids": []string{"provider-one"}}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update failed: %d %s", response.StatusCode, body)
	}
	if strings.Contains(body, "top-secret-token") || strings.Contains(body, "long-secret-passphrase") {
		t.Fatal("sync token leaked in update response")
	}
	if secretStore.values[localSyncTokenRef] != "top-secret-token" {
		t.Fatal("sync token was not written to secret store")
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync", nil, "")
	body = readBody(t, response)
	if response.StatusCode != http.StatusOK || strings.Contains(body, "top-secret-token") {
		t.Fatalf("unsafe settings response: %d %s", response.StatusCode, body)
	}
	var payload struct {
		Sync struct {
			TokenConfigured      bool   `json:"token_configured"`
			PassphraseConfigured bool   `json:"passphrase_configured"`
			DeviceID             string `json:"device_id"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Sync.TokenConfigured || !payload.Sync.PassphraseConfigured || payload.Sync.DeviceID != "device-one" || strings.Contains(body, "provider_ids") {
		t.Fatalf("unexpected settings: %#v", payload.Sync)
	}
	stored, err := database.SyncSettings(ctx, localSyncScope)
	if err != nil || len(stored.ProviderIDs)+len(stored.EnvGroupIDs)+len(stored.CodexAccountIDs) != 0 {
		t.Fatalf("legacy credential selection remained active: %#v err=%v", stored, err)
	}
}

func TestSyncPreviewCountsLocalTombstonesBeforeRunning(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	entry := domain.KnowledgeEntry{ID: "knowledge-delete-preview", Scope: "global", Type: "practice", Title: "Delete", Body: "delete", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteKnowledge(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Endpoint: "https://sync.example", DeviceID: "preview-device", IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	_ = registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/preview", nil, "")
	defer response.Body.Close()
	var payload struct {
		Preview struct {
			Upserts   int `json:"upserts"`
			Deletes   int `json:"deletes"`
			Pending   int `json:"pending"`
			Conflicts int `json:"conflicts"`
		} `json:"preview"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || payload.Preview.Deletes != 1 || payload.Preview.Pending != 0 || payload.Preview.Conflicts != 0 {
		t.Fatalf("status=%d preview=%#v", response.StatusCode, payload.Preview)
	}
}

func TestSyncPreviewIncludesUnappliedRemoteDeletes(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	if err := center.PutDeviceToken(ctx, "preview-device", "preview-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := center.Push(ctx, "other-device", []centersync.Event{{EventID: "delete-remote", ObjectID: "knowledge-remote", ObjectType: "knowledge", Operation: "delete", Version: 1, SourceVersion: "1"}}); err != nil {
		t.Fatal(err)
	}
	centerServer := httptest.NewServer(center.Handler())
	defer centerServer.Close()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Endpoint: centerServer.URL, DeviceID: "preview-device", IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkSyncApplied(ctx, "default", domain.SyncObject{Type: "knowledge", ID: "knowledge-remote", Operation: "delete", IdempotencyKey: "delete-remote"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	secretStore := &fakeSecretStore{values: map[string]string{localSyncTokenRef: "preview-token"}}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), Secrets: secretStore}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	_ = registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/preview", nil, "")
	defer response.Body.Close()
	var payload struct {
		Preview syncer.Preview `json:"preview"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || payload.Preview.RemoteDeletes != 1 || payload.Preview.RemoteUpserts != 0 {
		t.Fatalf("status=%d preview=%#v", response.StatusCode, payload.Preview)
	}
}

func TestSyncRunProgressTracksPhasesAndRejectsConcurrentRun(t *testing.T) {
	server := &Server{}
	preview := syncer.Preview{Upserts: 3, Deletes: 1, RemoteUpserts: 2, RemoteDeletes: 1}
	if !server.beginSyncRun(preview) || server.beginSyncRun(preview) {
		t.Fatal("sync run concurrency guard failed")
	}
	server.updateSyncRun("pulling", preview)
	progress := server.syncRunSnapshot()
	if !progress.Running || progress.Total != 7 || progress.Completed != 4 || progress.Phase != "pulling" {
		t.Fatalf("pull progress=%#v", progress)
	}
	server.finishSyncRun(nil)
	progress = server.syncRunSnapshot()
	if progress.Running || progress.Completed != 7 || progress.Phase != "complete" {
		t.Fatalf("final progress=%#v", progress)
	}
}

func TestRemoteTaskMirrorIsListedAndReadOnly(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-shared", Name: "Shared", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "source-task", ProjectID: project.ID, WorkspaceID: "source-workspace", Title: "Remote task", Status: domain.TaskWaitingUser, Isolation: "inplace", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit", CreatedAt: now, UpdatedAt: now, OwnerDeviceID: "dev_remote", ReadOnly: true}
	payload, _ := json.Marshal(task)
	if err := database.UpsertRemoteTaskObject(ctx, store.RemoteTaskObject{OwnerDeviceID: "dev_remote", ObjectType: "task", ObjectID: task.ID, TaskID: task.ID, ProjectID: project.ID, Payload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	remoteWorkspace := domain.Workspace{ID: "remote-workspace-mirror", ProjectID: project.ID, Name: "Remote workspace", OwnerDeviceID: "dev_remote", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertSyncedWorkspace(ctx, remoteWorkspace); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks", nil, "")
	var list struct {
		Tasks []domain.Task `json:"tasks"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(list.Tasks) != 1 || !list.Tasks[0].ReadOnly || list.Tasks[0].OwnerDeviceID != "dev_remote" {
		t.Fatalf("tasks=%#v", list.Tasks)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+list.Tasks[0].ID, nil, "")
	var detail struct {
		Task domain.Task `json:"task"`
	}
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !detail.Task.ReadOnly || detail.Task.Title != "Remote task" {
		t.Fatalf("detail=%#v", detail.Task)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tasks/"+list.Tasks[0].ID, nil, csrf)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("remote task delete status=%d", response.StatusCode)
	}
	response.Body.Close()
	if found, err := database.RemoteTaskObjectExists(ctx, "dev_remote", "task", task.ID); err != nil || !found {
		t.Fatalf("remote task mirror was deleted: found=%t err=%v", found, err)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/workspaces/"+remoteWorkspace.ID, nil, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("remote workspace delete status=%d", response.StatusCode)
	}
	response.Body.Close()
	if _, err := database.Workspace(ctx, remoteWorkspace.ID); err != nil {
		t.Fatalf("remote workspace mirror was deleted: %v", err)
	}
}

func TestOwnerTaskAndWorkspaceDeletesCreateGraphTombstones(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tasks/"+task.ID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owned task delete status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	if _, err := database.SyncTombstone(ctx, "task", "device-owner:"+task.ID); err != nil {
		t.Fatalf("task delete did not create tombstone: %v", err)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/workspaces/"+task.WorkspaceID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owned workspace delete status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	if _, err := database.SyncTombstone(ctx, "workspace", "device-owner:"+task.WorkspaceID); err != nil {
		t.Fatalf("workspace delete did not create tombstone: %v", err)
	}
}

func TestRunSyncUsesStoredTokenAndAuthenticatedRoutes(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-token" || r.Header.Get("X-Device-ID") != "device-one" {
			t.Errorf("missing remote credentials")
		}
		switch r.URL.Path {
		case "/v1/sync/push":
			var request struct {
				Objects []struct {
					IdempotencyKey string `json:"idempotency_key"`
				} `json:"objects"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			acked := make([]string, 0, len(request.Objects))
			for _, object := range request.Objects {
				acked = append(acked, object.IdempotencyKey)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"acked_keys": acked})
		case "/v1/sync/pull":
			_ = json.NewEncoder(w).Encode(map[string]any{"objects": []any{}, "cursor": "7", "has_more": false})
		default:
			t.Errorf("unexpected remote path %s", r.URL.Path)
		}
	}))
	defer remote.Close()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore := &fakeSecretStore{values: map[string]string{localSyncTokenRef: "stored-token"}}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), Secrets: secretStore}).Handler())
	defer server.Close()
	unauthorized := requestJSON(t, http.DefaultClient, http.MethodGet, server.URL+"/api/v1/settings/sync/status", nil, "")
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status route is not authenticated: %d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/sync", map[string]any{"enabled": true, "endpoint": remote.URL, "device_id": "device-one", "interval_seconds": 60}, csrf)
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/sync/run", map[string]any{}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run failed: %d %s", response.StatusCode, body)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/status", nil, "")
	body = readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `"cursor":"7"`) {
		t.Fatalf("status failed: %d %s", response.StatusCode, body)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/conflicts", nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("conflicts failed: %d", response.StatusCode)
	}
	response.Body.Close()
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var value json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return string(value)
}
