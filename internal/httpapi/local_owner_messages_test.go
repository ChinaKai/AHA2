package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const localOwnerIngestTestSecret = "test-local-ingest-secret-0123456789abcdef"

type localIngestFixture struct {
	ctx       context.Context
	database  *store.Store
	service   *app.Service
	secrets   *fakeSecretStore
	server    *httptest.Server
	petTaskID string
}

func newLocalIngestFixture(t *testing.T, enabled bool, secret string) *localIngestFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{
		ID: "project-local-ingest", Name: "Local Ingest", ProjectType: "system",
		KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
	}
	workspace := domain.Workspace{
		ID: "workspace-local-ingest", ProjectID: project.ID, Name: "pet-workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), SSHPort: 22,
		Health: "ready", AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
	}
	environment := domain.EnvGroup{
		ID: "env-local-ingest", Name: "Env", ProviderID: "stub", Backend: "stub",
		Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{},
		CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-local-ingest", DisplayName: "Model", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: environment.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, environment) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	secretStore := &fakeSecretStore{values: map[string]string{}}
	if secret != "" {
		secretStore.values[app.LocalOwnerIngestSecretRef] = secret
	}
	service := app.NewService(database, nil, app.StubExecutor{})
	t.Cleanup(func() {
		_ = service.Shutdown(context.Background())
		_ = database.Close()
	})
	task, err := service.CreateTask(ctx, app.CreateTaskInput{
		ID: "task_pet", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Pet", Request: "test", ModelID: model.ID,
		Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
		AllowSystemProject: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store: database, App: service, Secrets: secretStore, LocalOwnerIngest: enabled,
	}).Handler())
	t.Cleanup(server.Close)
	return &localIngestFixture{
		ctx: ctx, database: database, service: service, secrets: secretStore,
		server: server, petTaskID: task.ID,
	}
}

func (f *localIngestFixture) post(t *testing.T, secret, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.server.URL+"/api/v1/local/owner-messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeLocalIngestResponse(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func localIngestBody(taskID, content, source, requestID string) string {
	payload := map[string]any{"task_id": taskID, "content": content}
	if source != "" {
		payload["source"] = source
	}
	if requestID != "" {
		payload["client_request_id"] = requestID
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func TestLocalOwnerIngestWritesOwnerMessageAndAuditsSource(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	response := fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "  你好，语音  ", "voice", "voice-request-1"))
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	result := decodeLocalIngestResponse(t, response)
	if result["ok"] != true || result["duplicate"] != false || result["message_id"] == "" {
		t.Fatalf("result=%#v", result)
	}
	messages, err := fixture.database.ListMessages(fixture.ctx, fixture.petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.Content == "你好，语音" && message.Role == "user" && message.Sender == "owner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("owner message not found in task conversation: %#v", messages)
	}
	stored, ok, err := fixture.database.LocalOwnerMessageByClientRequestID(fixture.ctx, "voice-request-1")
	if err != nil || !ok {
		t.Fatalf("idempotency lookup: ok=%v err=%v", ok, err)
	}
	if stored.Source != "voice" || stored.TaskID != fixture.petTaskID {
		t.Fatalf("stored metadata=%#v", stored)
	}
}

func TestLocalOwnerIngestRequiredSecretAndWrongSecret(t *testing.T) {
	t.Run("missing secret", func(t *testing.T) {
		fixture := newLocalIngestFixture(t, true, "")
		response := fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "hi", "voice", ""))
		defer response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status=%d", response.StatusCode)
		}
	})
	t.Run("wrong secret", func(t *testing.T) {
		fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
		response := fixture.post(t, "wrong-secret", localIngestBody(fixture.petTaskID, "hi", "voice", ""))
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d", response.StatusCode)
		}
	})
}

func TestLocalOwnerIngestLoopbackOnly(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	handler := New(Config{
		Store: fixture.database, App: fixture.service, Secrets: fixture.secrets, LocalOwnerIngest: true,
	}).Handler()
	body := localIngestBody(fixture.petTaskID, "hi", "voice", "")
	for _, testCase := range []struct {
		name       string
		remoteAddr string
		host       string
		headers    map[string]string
	}{
		{name: "lan remote addr", remoteAddr: "192.168.1.20:5555", host: "127.0.0.1:8766"},
		{name: "lan host", remoteAddr: "127.0.0.1:5555", host: "192.168.1.10:8766"},
		{name: "forwarded header", remoteAddr: "127.0.0.1:5555", host: "127.0.0.1:8766", headers: map[string]string{"X-Forwarded-For": "192.168.1.20"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/local/owner-messages", bytes.NewBufferString(body))
			request.RemoteAddr = testCase.remoteAddr
			request.Host = testCase.host
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+localOwnerIngestTestSecret)
			for key, value := range testCase.headers {
				request.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLocalOwnerIngestValidatesPayload(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	controlPayload, _ := json.Marshal(map[string]any{
		"task_id": fixture.petTaskID, "content": "bad\x01text", "source": "voice",
	})
	for _, testCase := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "empty content", body: localIngestBody(fixture.petTaskID, "   ", "voice", ""), status: http.StatusBadRequest, code: "content_required"},
		{name: "too long", body: localIngestBody(fixture.petTaskID, strings.Repeat("字", 4001), "voice", ""), status: http.StatusBadRequest, code: "content_too_long"},
		{name: "control character", body: string(controlPayload), status: http.StatusBadRequest, code: "content_control_characters"},
		{name: "source", body: localIngestBody(fixture.petTaskID, "hi", "cli", ""), status: http.StatusBadRequest, code: "source_invalid"},
		{name: "task whitelist", body: localIngestBody("task_other", "hi", "voice", ""), status: http.StatusForbidden, code: "task_not_allowed"},
		{name: "body limit", body: `{"task_id":"task_pet","content":"` + strings.Repeat("a", localOwnerMessageMaxBodyBytes) + `"}`, status: http.StatusRequestEntityTooLarge, code: "request_too_large"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := fixture.post(t, localOwnerIngestTestSecret, testCase.body)
			result := decodeLocalIngestResponse(t, response)
			if response.StatusCode != testCase.status || result["error"] != testCase.code {
				t.Fatalf("status=%d error=%v, want %d %s", response.StatusCode, result["error"], testCase.status, testCase.code)
			}
		})
	}
}

func TestLocalOwnerIngestRateLimit(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	for attempt := 0; attempt < localOwnerMessageRateLimit; attempt++ {
		response := fixture.post(t, "wrong-secret", localIngestBody(fixture.petTaskID, "hi", "voice", ""))
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d", attempt+1, response.StatusCode)
		}
	}
	response := fixture.post(t, "wrong-secret", localIngestBody(fixture.petTaskID, "hi", "voice", ""))
	result := decodeLocalIngestResponse(t, response)
	if response.StatusCode != http.StatusTooManyRequests || result["error"] != "rate_limited" {
		t.Fatalf("status=%d result=%#v", response.StatusCode, result)
	}
}

func TestLocalOwnerIngestIsIdempotent(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	body := localIngestBody(fixture.petTaskID, "幂等测试", "voice", "voice-request-2")
	first := decodeLocalIngestResponse(t, fixture.post(t, localOwnerIngestTestSecret, body))
	second := decodeLocalIngestResponse(t, fixture.post(t, localOwnerIngestTestSecret, body))
	if first["duplicate"] != false || first["message_id"] == "" {
		t.Fatalf("first=%#v", first)
	}
	if second["duplicate"] != true || second["message_id"] != first["message_id"] {
		t.Fatalf("second=%#v first=%#v", second, first)
	}
	messages, err := fixture.database.ListMessages(fixture.ctx, fixture.petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.Content == "幂等测试" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("message inserted %d times", count)
	}
	turns, err := fixture.database.TaskTurnCount(fixture.ctx, fixture.petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 {
		t.Fatalf("turn count=%d, want 1", turns)
	}
}

func TestLocalOwnerIngestFeatureOffIsNotRouted(t *testing.T) {
	fixture := newLocalIngestFixture(t, false, localOwnerIngestTestSecret)
	response := fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "hi", "voice", ""))
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestLocalOwnerIngestSecretIsNotEchoed(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	response := fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "hi", "voice", ""))
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if bytes.Contains(body, []byte(localOwnerIngestTestSecret)) {
		t.Fatal("response echoed the secret")
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}

// Keep the test helper honest about using the real Secret Store type in
// production wiring: the fake must satisfy the same interface.
var _ SecretStore = (*fakeSecretStore)(nil)

// The endpoint must keep accepting UTF-8 Chinese text after the shared
// encoding-repair middleware runs.
func TestLocalOwnerIngestAcceptsChineseText(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	response := fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "打开灯光", "voice", ""))
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	response.Body.Close()
}
