package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestOwnerAssistantViewAndHints(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-assistant", Name: "AHA2", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-assistant", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-assistant", Name: "stub", ProviderID: "provider-assistant", Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-assistant", DisplayName: "Assistant Model", ProviderID: "provider-assistant",
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "assistant", WireAPI: "chat",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, secretStore, &stubExecutor{})
	create := func(title string) domain.Task {
		task, createErr := service.CreateTask(ctx, CreateTaskInput{
			ProjectID: project.ID, WorkspaceID: workspace.ID, Title: title,
			Request: "request for " + title, ModelID: model.ID, StartMode: "manual",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return task
	}
	transition := func(task domain.Task, from, to domain.TaskStatus) {
		if err := database.UpdateTaskStatus(ctx, task.ID, from, to, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
	}
	failed := create("failed task")
	transition(failed, domain.TaskDraft, domain.TaskPreparing)
	transition(failed, domain.TaskPreparing, domain.TaskFailed)
	waiting := create("waiting task")
	transition(waiting, domain.TaskDraft, domain.TaskPreparing)
	transition(waiting, domain.TaskPreparing, domain.TaskActive)
	transition(waiting, domain.TaskActive, domain.TaskWaitingUser)

	view, err := service.OwnerAssistant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.ID != ownerAssistantID || view.Assistant.ModelID != model.ID || view.Assistant.Character != "gugugaga" {
		t.Fatalf("assistant=%#v", view.Assistant)
	}
	if view.Assistant.PetTaskID == "" {
		t.Fatal("pet task was not created")
	}
	if _, err := database.Task(ctx, view.Assistant.PetTaskID); err != nil {
		t.Fatalf("pet task missing: %v", err)
	}
	updated, err := service.UpdateOwnerAssistant(ctx, "", "", "deepseek_fish")
	if err != nil || updated.Assistant.Character != "deepseek_fish" {
		t.Fatalf("updated=%#v err=%v", updated.Assistant, err)
	}
	if _, err := service.UpdateOwnerAssistant(ctx, "", "", "not-a-character"); err == nil {
		t.Fatal("invalid character was accepted")
	}
	if len(view.Models) != 1 || view.Models[0].ID != model.ID || view.ModelName != model.DisplayName {
		t.Fatalf("models=%#v name=%q", view.Models, view.ModelName)
	}
	hints, err := service.OwnerAssistantHints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 2 || hints[0].Kind != "task_failed" || hints[0].TargetID != failed.ID {
		t.Fatalf("hints=%#v", hints)
	}
	if err := service.DismissOwnerAssistantHint(ctx, hints[0].Kind, hints[0].TargetID); err != nil {
		t.Fatal(err)
	}
	hints, err = service.OwnerAssistantHints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 1 || hints[0].Kind != "task_waiting" {
		t.Fatalf("hints after dismiss=%#v", hints)
	}
	snapshot, err := service.assistantSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AHA2", "failed task"} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("snapshot missing %q:\n%s", want, snapshot)
		}
	}
	if !strings.Contains(snapshot, "Assistant Model") {
		t.Fatalf("snapshot missing model display name:\n%s", snapshot)
	}
}

func TestAskOwnerAssistantUsesProviderModel(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer assistant-secret" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(request.Body)
		requestBody = string(body)
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"现在有一个等待中的任务。"}}]}`))
	}))
	defer server.Close()

	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-assistant-chat", Name: "AHA2", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-assistant-chat", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	provider := domain.Provider{
		ID: "provider-assistant-chat", Name: "Test Provider", BaseURL: server.URL,
		AuthStyle: "bearer", CredentialRef: "provider/test/credential", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-assistant-chat", Name: "stub", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-assistant-chat", DisplayName: "Assistant Chat", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "assistant", WireAPI: "chat",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error {
			return secretStore.PutMany(map[string]string{provider.CredentialRef: "assistant-secret"})
		},
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, secretStore, &stubExecutor{})
	if _, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "assistant task",
		Request: "waiting task", ModelID: model.ID, StartMode: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	reply, err := service.AskOwnerAssistant(ctx, "现在有哪些任务？")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Reply.Content != "现在有一个等待中的任务。" {
		t.Fatalf("reply=%q", reply.Reply.Content)
	}
	if !strings.Contains(requestBody, "只读快照") || !strings.Contains(requestBody, "assistant task") {
		t.Fatalf("request did not include snapshot: %s", requestBody)
	}
	messages, err := database.AssistantMessages(ctx, ownerAssistantID, 10)
	if err != nil || len(messages) != 2 || messages[0].Role != domain.AssistantRoleUser || messages[1].Role != domain.AssistantRoleAssistant {
		t.Fatalf("messages=%#v err=%v", messages, err)
	}
}
