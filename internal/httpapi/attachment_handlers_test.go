package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestTaskAttachmentLimitIs200MiB(t *testing.T) {
	t.Parallel()
	if maxAttachmentBytes != 200<<20 {
		t.Fatalf("maxAttachmentBytes = %d, want %d", maxAttachmentBytes, int64(200<<20))
	}
	const mib = int64(1 << 20)
	for _, testCase := range []struct {
		name string
		size int64
		want bool
	}{
		{name: "empty", size: 0, want: false},
		{name: "one byte", size: 1, want: true},
		{name: "exactly 200MiB", size: 200 * mib, want: true},
		{name: "200MiB plus one", size: 200*mib + 1, want: false},
	} {
		if got := taskAttachmentSizeValid(testCase.size); got != testCase.want {
			t.Fatalf("%s: taskAttachmentSizeValid(%d) = %v, want %v", testCase.name, testCase.size, got, testCase.want)
		}
	}
}

func TestAttachmentMultipartMemoryStaysBelowUploadCap(t *testing.T) {
	t.Parallel()
	if maxMultipartMemory != 32<<20 {
		t.Fatalf("maxMultipartMemory = %d, want %d", maxMultipartMemory, int64(32<<20))
	}
	if maxMultipartMemory >= maxAttachmentBytes {
		t.Fatalf("multipart memory threshold %d must stay below the %d attachment cap", maxMultipartMemory, maxAttachmentBytes)
	}
}

func TestTaskAttachmentUploadDownloadAndDelete(t *testing.T) {
	t.Parallel()
	ctx, now := context.Background(), time.Now().UTC()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-attachment-api", Name: "Attachments", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-attachment-api", ProjectID: project.ID, Name: "Local", Locality: "local", Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now}
	environment := domain.EnvGroup{ID: "env-attachment-api", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-attachment-api", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: environment.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, environment) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := app.NewService(database, secretStore, app.StubExecutor{})
	// The test submits a message, which starts a Turn that writes runtime files
	// under the workspace root. Waiting here keeps those writes out of the
	// t.TempDir cleanup window.
	defer func() { _ = service.Shutdown(context.Background()) }()
	task, err := service.CreateTask(ctx, app.CreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Attachment", Request: "test", ModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: service}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, _ := writer.CreateFormFile("file", "screen.png")
	content := []byte("\x89PNG\r\napi-content")
	_, _ = part.Write(content)
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/attachments", &upload)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded struct {
		Attachment domain.Attachment `json:"attachment"`
	}
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || uploaded.Attachment.ID == "" {
		t.Fatalf("upload failed: %d %#v", response.StatusCode, uploaded)
	}

	response, err = client.Get(server.URL + "/api/v1/tasks/" + task.ID + "/attachments/" + uploaded.Attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Equal(downloaded, content) || response.Header.Get("Content-Disposition") == "" {
		t.Fatalf("download mismatch: status=%d body=%q", response.StatusCode, downloaded)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/agents/main/messages", map[string]any{"content": "", "attachment_ids": []string{uploaded.Attachment.ID}}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("attachment-only message failed: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/conversation", nil, "")
	var conversation map[string]any
	decodeResponse(t, response, &conversation)
	items := conversation["conversation"].(map[string]any)["items"].([]any)
	found := false
	for _, raw := range items {
		item := raw.(map[string]any)
		payload, _ := item["payload"].(map[string]any)
		if attachments, _ := payload["attachments"].([]any); len(attachments) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("conversation attachment metadata missing: %#v", conversation)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tasks/"+task.ID+"/attachments/"+uploaded.Attachment.ID, nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("sent attachment deletion status=%d", response.StatusCode)
	}
	draft, err := database.CreateAttachment(ctx, task.ID, "draft.txt", "text/plain", []byte("draft"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tasks/"+task.ID+"/attachments/"+draft.ID, nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete failed: %d", response.StatusCode)
	}
}

func TestTaskAttachmentDownloadByAgentCapabilityIsScopedToTask(t *testing.T) {
	t.Parallel()
	ctx, now := context.Background(), time.Now().UTC()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	project := domain.Project{ID: "project-agent-attachment", Name: "Attachments", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-agent-attachment", ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	environment := domain.EnvGroup{ID: "env-agent-attachment", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-agent-attachment", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: environment.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, environment); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "runtime-agent-attachment", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID,
		WireModel: "stub", EnvGroupID: environment.ID, EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	taskA := domain.Task{
		ID: "task-agent-attachment-a", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "A", Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now,
	}
	taskB := domain.Task{
		ID: "task-agent-attachment-b", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "B", Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, task := range []domain.Task{taskA, taskB} {
		if err := database.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	contentA := []byte("attachment body for task A")
	attachmentA, err := database.CreateAttachment(ctx, taskA.ID, "report.txt", "text/plain", contentA, now)
	if err != nil {
		t.Fatal(err)
	}
	attachmentB, err := database.CreateAttachment(ctx, taskB.ID, "secret.txt", "text/plain", []byte("other task only"), now)
	if err != nil {
		t.Fatal(err)
	}
	// Larger than the 4MiB inline threshold, so a regression to the inline path
	// would either truncate the body or fail outright.
	largeContent := make([]byte, (5<<20)+37)
	for index := range largeContent {
		largeContent[index] = byte(index % 251)
	}
	largeAttachment, err := database.CreateAttachment(ctx, taskA.ID, "p0-log.zip", "application/zip", largeContent, now)
	if err != nil {
		t.Fatal(err)
	}

	capabilities := agentapi.NewCapabilities()
	tokenA, err := capabilities.Issue(taskA.ID, "main", "turn-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenB, err := capabilities.Issue(taskB.ID, "main", "turn-b", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, AgentCapabilities: capabilities}).Handler())
	defer server.Close()
	attachmentURL := func(taskID, attachmentID string) string {
		return server.URL + "/api/v1/tasks/" + taskID + "/attachments/" + attachmentID
	}
	readBody := func(response *http.Response) []byte {
		t.Helper()
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}

	// Same Task: 200, bytes and the stored sha256 match.
	response := agentRequest(t, attachmentURL(taskA.ID, attachmentA.ID), http.MethodGet, tokenA, nil)
	body := readBody(response)
	digest := sha256.Sum256(body)
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, contentA) {
		t.Fatalf("same-task download: status=%d body=%q", response.StatusCode, body)
	}
	if hex.EncodeToString(digest[:]) != attachmentA.SHA256 {
		t.Fatalf("same-task sha256 = %s, want %s", hex.EncodeToString(digest[:]), attachmentA.SHA256)
	}

	// Another Task's capability cannot download this attachment.
	response = agentRequest(t, attachmentURL(taskA.ID, attachmentA.ID), http.MethodGet, tokenB, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-task capability download status = %d, want 404", response.StatusCode)
	}

	// A capability pointed at its own Task still cannot read another Task's
	// attachment: the path Task matches the capability, so only the SQL
	// attachment lookup (id AND task_id) can reject it.
	response = agentRequest(t, attachmentURL(taskA.ID, attachmentB.ID), http.MethodGet, tokenA, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign attachment download status = %d, want 404", response.StatusCode)
	}

	// Missing attachment ids are 404, not 401 or 500.
	response = agentRequest(t, attachmentURL(taskA.ID, "attachment_missing"), http.MethodGet, tokenA, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing attachment status = %d, want 404", response.StatusCode)
	}

	// Too-large files stream in full.
	response = agentRequest(t, attachmentURL(taskA.ID, largeAttachment.ID), http.MethodGet, tokenA, nil)
	largeBody := readBody(response)
	largeDigest := sha256.Sum256(largeBody)
	if response.StatusCode != http.StatusOK || len(largeBody) != len(largeContent) || !bytes.Equal(largeBody, largeContent) {
		t.Fatalf("large download: status=%d size=%d want=%d", response.StatusCode, len(largeBody), len(largeContent))
	}
	if hex.EncodeToString(largeDigest[:]) != largeAttachment.SHA256 {
		t.Fatalf("large download sha256 = %s, want %s", hex.EncodeToString(largeDigest[:]), largeAttachment.SHA256)
	}

	// No credential and an invalid capability both stay 401.
	response, err = http.Get(attachmentURL(taskA.ID, attachmentA.ID))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous download status = %d, want 401", response.StatusCode)
	}
	response = agentRequest(t, attachmentURL(taskA.ID, attachmentA.ID), http.MethodGet, "invalid", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid capability download status = %d, want 401", response.StatusCode)
	}

	// Owner session downloads keep working, including for attachments the
	// agent capability path also serves.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	registerOwner(t, client, server.URL)

	// A valid capability takes precedence over the Owner cookie, so attaching
	// one does not let it read another Task's attachment.
	request, err := http.NewRequest(http.MethodGet, attachmentURL(taskB.ID, attachmentB.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+tokenA)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("owner cookie plus cross-task capability status = %d, want 404", response.StatusCode)
	}

	response, err = client.Get(attachmentURL(taskA.ID, attachmentA.ID))
	if err != nil {
		t.Fatal(err)
	}
	ownerBody := readBody(response)
	if response.StatusCode != http.StatusOK || !bytes.Equal(ownerBody, contentA) {
		t.Fatalf("owner download: status=%d body=%q", response.StatusCode, ownerBody)
	}

	// A revoked capability is rejected even though it has not expired yet.
	capabilities.Revoke(tokenA)
	response = agentRequest(t, attachmentURL(taskA.ID, attachmentA.ID), http.MethodGet, tokenA, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked capability download status = %d, want 401", response.StatusCode)
	}
}
