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

	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func newConflictTestServer(t *testing.T, endpoint string) (*store.Store, *httptest.Server, *http.Client, string) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{
		Scope: "default", Enabled: true, Endpoint: endpoint, DeviceID: "device-conflict",
		DeviceName: "Conflict", IntervalSeconds: 300,
	}); err != nil {
		t.Fatal(err)
	}
	secretStore := &fakeSecretStore{values: map[string]string{localSyncTokenRef: "conflict-token"}}
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), Secrets: secretStore,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return database, server, client, csrf
}

func conflictProjectPayload(t *testing.T, name string, updated time.Time) (domain.Project, json.RawMessage) {
	t.Helper()
	project := domain.Project{ID: "project_conflict", Name: name, ProjectType: "folder", CreatedAt: updated, UpdatedAt: updated}
	value, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"owner_device_id": "device-conflict", "project_id": project.ID, "value": json.RawMessage(value)})
	if err != nil {
		t.Fatal(err)
	}
	return project, raw
}

func seedConflict(t *testing.T, database *store.Store, enqueue bool, localRaw, remoteRaw json.RawMessage) string {
	t.Helper()
	ctx := context.Background()
	if enqueue {
		if err := database.EnqueueSync(ctx, domain.SyncOutboxItem{Scope: "default", Object: domain.SyncObject{
			Type: "project", ID: "project_conflict", Operation: "upsert", Payload: localRaw,
			IdempotencyKey: "project:project_conflict:local",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.AddSyncConflict(ctx, domain.SyncConflict{
		Scope: "default", ObjectType: "project", ObjectID: "project_conflict",
		LocalPayload: localRaw, RemotePayload: remoteRaw, LocalVersion: "local", RemoteVersion: "4",
	}); err != nil {
		t.Fatal(err)
	}
	conflicts, err := database.SyncConflicts(ctx, "default")
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts=%#v err=%v", conflicts, err)
	}
	return conflicts[0].ID
}

func TestSyncConflictKeepLocalPushesRebasedObject(t *testing.T) {
	now := time.Now().UTC()
	_, localRaw := conflictProjectPayload(t, "Local Name", now)
	_, remoteRaw := conflictProjectPayload(t, "Remote Name", now.Add(time.Minute))
	var pushed syncer.PushRequest
	center := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sync/push" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&pushed); err != nil {
			t.Error(err)
			return
		}
		keys := make([]string, 0, len(pushed.Objects))
		for _, object := range pushed.Objects {
			keys = append(keys, object.IdempotencyKey)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"acked_keys": keys})
	}))
	defer center.Close()
	database, server, client, csrf := newConflictTestServer(t, center.URL)
	id := seedConflict(t, database, true, localRaw, remoteRaw)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/sync/conflicts/"+id+"/resolve", map[string]any{"action": "local"}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resolve failed: %d %s", response.StatusCode, body)
	}
	if len(pushed.Objects) != 1 || pushed.Objects[0].BaseVersion != "4" {
		t.Fatalf("pushed=%#v", pushed)
	}
	var pushedEnvelope struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(pushed.Objects[0].Payload, &pushedEnvelope); err != nil {
		t.Fatalf("pushed payload: %v", err)
	}
	var pushedProject domain.Project
	if err := json.Unmarshal(pushedEnvelope.Value, &pushedProject); err != nil || pushedProject.Name != "Local Name" {
		t.Fatalf("pushed project=%#v err=%v", pushedProject, err)
	}
	if pending, _ := database.SyncOutboxCount(context.Background(), "default"); pending != 0 {
		t.Fatalf("pending=%d", pending)
	}
	if conflicts, _ := database.SyncConflicts(context.Background(), "default"); len(conflicts) != 0 {
		t.Fatalf("conflicts=%#v", conflicts)
	}
}

func TestSyncConflictUseRemoteAppliesPayload(t *testing.T) {
	now := time.Now().UTC()
	localProject, localRaw := conflictProjectPayload(t, "Local Name", now)
	_, remoteRaw := conflictProjectPayload(t, "Remote Name", now.Add(time.Minute))
	database, server, client, csrf := newConflictTestServer(t, "http://127.0.0.1:1")
	ctx := context.Background()
	if err := database.CreateProject(ctx, localProject); err != nil {
		t.Fatal(err)
	}
	id := seedConflict(t, database, false, localRaw, remoteRaw)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/sync/conflicts/"+id+"/resolve", map[string]any{"action": "remote"}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resolve failed: %d %s", response.StatusCode, body)
	}
	project, err := database.Project(ctx, localProject.ID)
	if err != nil || project.Name != "Remote Name" {
		t.Fatalf("project=%#v err=%v", project, err)
	}
	if conflicts, _ := database.SyncConflicts(ctx, "default"); len(conflicts) != 0 {
		t.Fatalf("conflicts=%#v", conflicts)
	}
}

func TestSyncConflictIgnoreDropsPendingChange(t *testing.T) {
	now := time.Now().UTC()
	_, localRaw := conflictProjectPayload(t, "Local Name", now)
	_, remoteRaw := conflictProjectPayload(t, "Remote Name", now.Add(time.Minute))
	database, server, client, csrf := newConflictTestServer(t, "http://127.0.0.1:1")
	id := seedConflict(t, database, true, localRaw, remoteRaw)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/sync/conflicts/"+id+"/resolve", map[string]any{"action": "ignore"}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resolve failed: %d %s", response.StatusCode, body)
	}
	ctx := context.Background()
	if pending, _ := database.SyncOutboxCount(ctx, "default"); pending != 0 {
		t.Fatalf("pending=%d", pending)
	}
	if conflicts, _ := database.SyncConflicts(ctx, "default"); len(conflicts) != 0 {
		t.Fatalf("conflicts=%#v", conflicts)
	}
}

func TestSyncConflictsAPIEncodesEmptyPayload(t *testing.T) {
	database, server, client, _ := newConflictTestServer(t, "http://127.0.0.1:1")
	ctx := context.Background()
	if err := database.AddSyncConflict(ctx, domain.SyncConflict{
		Scope: "default", ObjectType: "knowledge", ObjectID: "knowledge_empty",
		RemotePayload: json.RawMessage(`{"id":"knowledge_empty","title":"Remote"}`), RemoteVersion: "1",
	}); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/conflicts", nil, "")
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `"object_id":"knowledge_empty"`) {
		t.Fatalf("conflicts response=%d %s", response.StatusCode, body)
	}
	if strings.Contains(body, `"local_payload":""`) {
		t.Fatalf("empty payload encoded as invalid JSON: %s", body)
	}
}
