package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

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
