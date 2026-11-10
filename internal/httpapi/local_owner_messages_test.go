package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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
	return f.postPath(t, "/api/v1/local/owner-messages", secret, body)
}

func (f *localIngestFixture) postPath(t *testing.T, path, secret, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.server.URL+path, strings.NewReader(body))
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
			if message.Source != "voice" || message.ClientRequestID != "voice-request-1" {
				t.Fatalf("ListMessages lost v129 metadata: %#v", message)
			}
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

func localIngestHTTPRequest(t *testing.T, body, remoteAddr, host string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/local/owner-messages", bytes.NewBufferString(body))
	request.RemoteAddr = remoteAddr
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+localOwnerIngestTestSecret)
	return request
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
		{name: "mapped lan remote addr", remoteAddr: "[::ffff:192.168.1.20]:5555", host: "127.0.0.1:8766"},
		{name: "lan host", remoteAddr: "127.0.0.1:5555", host: "192.168.1.10:8766"},
		{name: "forwarded header", remoteAddr: "127.0.0.1:5555", host: "127.0.0.1:8766", headers: map[string]string{"X-Forwarded-For": "192.168.1.20"}},
		{name: "rfc forwarded header", remoteAddr: "127.0.0.1:5555", host: "127.0.0.1:8766", headers: map[string]string{"Forwarded": "for=192.168.1.20"}},
		{name: "real ip header", remoteAddr: "127.0.0.1:5555", host: "127.0.0.1:8766", headers: map[string]string{"X-Real-IP": "192.168.1.20"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := localIngestHTTPRequest(t, body, testCase.remoteAddr, testCase.host)
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
	t.Run("duplicate proxy headers", func(t *testing.T) {
		request := localIngestHTTPRequest(t, body, "127.0.0.1:5555", "127.0.0.1:8766")
		request.Header.Add("X-Forwarded-For", "10.0.0.2")
		request.Header.Add("X-Forwarded-For", "192.168.1.20")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
	for index, testCase := range []struct {
		name       string
		remoteAddr string
		host       string
	}{
		{name: "ipv4 loopback", remoteAddr: "127.0.0.1:5555", host: "127.0.0.1:8766"},
		{name: "ipv6 loopback", remoteAddr: "[::1]:5555", host: "[::1]:8766"},
		{name: "mapped loopback", remoteAddr: "[::ffff:127.0.0.1]:5555", host: "127.0.0.1:8766"},
		{name: "localhost host", remoteAddr: "127.0.0.1:5555", host: "localhost:8766"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requestBody := localIngestBody(fixture.petTaskID, "loopback", "voice", fmt.Sprintf("loopback-%d", index))
			request := localIngestHTTPRequest(t, requestBody, testCase.remoteAddr, testCase.host)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusAccepted {
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
	t.Run("authenticated requests are limited", func(t *testing.T) {
		fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
		body := localIngestBody(fixture.petTaskID, "", "voice", "")
		for attempt := 0; attempt < localOwnerMessageRateLimit; attempt++ {
			response := fixture.post(t, localOwnerIngestTestSecret, body)
			response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("attempt %d status=%d", attempt+1, response.StatusCode)
			}
		}
		response := fixture.post(t, localOwnerIngestTestSecret, body)
		result := decodeLocalIngestResponse(t, response)
		if response.StatusCode != http.StatusTooManyRequests || result["error"] != "rate_limited" {
			t.Fatalf("status=%d result=%#v", response.StatusCode, result)
		}
	})
	t.Run("failed auth does not consume quota", func(t *testing.T) {
		fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
		body := localIngestBody(fixture.petTaskID, "hi", "voice", "")
		for attempt := 0; attempt < localOwnerMessageRateLimit+1; attempt++ {
			response := fixture.post(t, "wrong-secret", body)
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("attempt %d status=%d", attempt+1, response.StatusCode)
			}
		}
		response := fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "ok", "voice", "auth-does-not-count"))
		result := decodeLocalIngestResponse(t, response)
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("valid request was locked out: status=%d result=%#v", response.StatusCode, result)
		}
	})
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

func localIngestResult(t *testing.T, response *http.Response) (int, map[string]any) {
	t.Helper()
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

func waitForTaskStatus(t *testing.T, database *store.Store, taskID string, want domain.TaskStatus) domain.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		task, err := database.Task(context.Background(), taskID)
		if err == nil && task.Status == want {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s status=%v err=%v, want %v", taskID, task.Status, err, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countMessagesWithContent(t *testing.T, database *store.Store, taskID, content string) int {
	t.Helper()
	messages, err := database.ListMessages(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.Content == content {
			count++
		}
	}
	return count
}

func TestLocalOwnerIngestRejectsQuerySecret(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	for _, query := range []string{"secret=leak", "token=leak", "access_token=leak", "api_key=leak", "password=leak"} {
		t.Run(query, func(t *testing.T) {
			response := fixture.postPath(t, "/api/v1/local/owner-messages?"+query, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "hi", "voice", ""))
			status, result := localIngestResult(t, response)
			if status != http.StatusBadRequest || result["error"] != "query_secret_forbidden" {
				t.Fatalf("status=%d result=%#v", status, result)
			}
		})
	}
	if count := countMessagesWithContent(t, fixture.database, fixture.petTaskID, "hi"); count != 0 {
		t.Fatalf("query-secret request inserted %d messages", count)
	}
}

func TestLocalOwnerIngestConcurrentIdempotency(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	body := localIngestBody(fixture.petTaskID, "并发幂等", "voice", "concurrent-request-1")
	const workers = 8
	type concurrentResult struct {
		status    int
		messageID string
		duplicate bool
		err       error
	}
	start := make(chan struct{})
	results := make(chan concurrentResult, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			request, err := http.NewRequest(http.MethodPost, fixture.server.URL+"/api/v1/local/owner-messages", strings.NewReader(body))
			if err != nil {
				results <- concurrentResult{err: err}
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+localOwnerIngestTestSecret)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				results <- concurrentResult{err: err}
				return
			}
			defer response.Body.Close()
			var payload struct {
				MessageID string `json:"message_id"`
				Duplicate bool   `json:"duplicate"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				results <- concurrentResult{err: err}
				return
			}
			results <- concurrentResult{status: response.StatusCode, messageID: payload.MessageID, duplicate: payload.Duplicate}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	firstID := ""
	inserted := 0
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if item.status != http.StatusAccepted {
			t.Fatalf("status=%d", item.status)
		}
		if item.messageID == "" {
			t.Fatal("duplicate response lost the committed message_id")
		}
		if firstID == "" {
			firstID = item.messageID
		}
		if item.messageID != firstID {
			t.Fatalf("responses reported different message ids: %s vs %s", item.messageID, firstID)
		}
		if !item.duplicate {
			inserted++
		}
	}
	if inserted != 1 {
		t.Fatalf("inserted=%d, want exactly 1", inserted)
	}
	waitForTaskStatus(t, fixture.database, fixture.petTaskID, domain.TaskWaitingUser)
	if count := countMessagesWithContent(t, fixture.database, fixture.petTaskID, "并发幂等"); count != 1 {
		t.Fatalf("message inserted %d times", count)
	}
	turns, err := fixture.database.TaskTurnCount(context.Background(), fixture.petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 {
		t.Fatalf("turn count=%d, want 1", turns)
	}
}

func TestLocalOwnerIngestRetryFingerprintAndTerminalTask(t *testing.T) {
	fixture := newLocalIngestFixture(t, true, localOwnerIngestTestSecret)
	firstBody := localIngestBody(fixture.petTaskID, "第一次", "voice", "retry-request-1")
	firstStatus, first := localIngestResult(t, fixture.post(t, localOwnerIngestTestSecret, firstBody))
	if firstStatus != http.StatusAccepted || first["duplicate"] != false {
		t.Fatalf("first status=%d result=%#v", firstStatus, first)
	}
	waitForTaskStatus(t, fixture.database, fixture.petTaskID, domain.TaskWaitingUser)

	mismatchStatus, mismatch := localIngestResult(t, fixture.post(t, localOwnerIngestTestSecret, localIngestBody(fixture.petTaskID, "第二次", "voice", "retry-request-1")))
	if mismatchStatus != http.StatusConflict || mismatch["error"] != "client_request_id_conflict" {
		t.Fatalf("fingerprint mismatch status=%d result=%#v", mismatchStatus, mismatch)
	}

	retryStatus, retry := localIngestResult(t, fixture.post(t, localOwnerIngestTestSecret, firstBody))
	if retryStatus != http.StatusAccepted || retry["duplicate"] != true || retry["message_id"] != first["message_id"] {
		t.Fatalf("retry status=%d result=%#v first=%#v", retryStatus, retry, first)
	}

	task := waitForTaskStatus(t, fixture.database, fixture.petTaskID, domain.TaskWaitingUser)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := fixture.database.UpdateTaskStatus(context.Background(), fixture.petTaskID, task.Status, domain.TaskCompleted, now, now); err != nil {
		t.Fatal(err)
	}
	terminalStatus, terminal := localIngestResult(t, fixture.post(t, localOwnerIngestTestSecret, firstBody))
	if terminalStatus != http.StatusConflict || terminal["error"] != "task_not_runnable" {
		t.Fatalf("terminal retry status=%d result=%#v", terminalStatus, terminal)
	}

	if count := countMessagesWithContent(t, fixture.database, fixture.petTaskID, "第一次"); count != 1 {
		t.Fatalf("first message inserted %d times", count)
	}
	if count := countMessagesWithContent(t, fixture.database, fixture.petTaskID, "第二次"); count != 0 {
		t.Fatalf("mismatched content inserted %d times", count)
	}
	turns, err := fixture.database.TaskTurnCount(context.Background(), fixture.petTaskID)
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
