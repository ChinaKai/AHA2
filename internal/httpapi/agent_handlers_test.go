package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type blockingAgentAPIExecutor struct {
	started chan app.ExecutionRequest
	release chan struct{}
}

func (executor *blockingAgentAPIExecutor) Execute(ctx context.Context, request app.ExecutionRequest, _ func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	executor.started <- request
	select {
	case <-executor.release:
		return app.ExecutionResult{Reply: "done", ProviderSessionID: "agent-api-test"}, nil
	case <-ctx.Done():
		return app.ExecutionResult{}, ctx.Err()
	}
}

func TestAgentAPIUsesTurnCapabilityAndTaskScope(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	workspaceRecord, err := database.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
		TaskID: task.ID, ID: "board", Mode: domain.HardwareModeSerial,
		Serial: domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200},
		Access: domain.HardwareAccessReadOnly, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	processes := managedprocess.NewManager()
	defer processes.Close()
	server := httptest.NewServer(New(Config{
		Store: database, AgentCapabilities: capabilities, ManagedProcesses: processes,
	}).Handler())
	defer server.Close()
	token, err := capabilities.Issue(task.ID, "main", "turn-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	response := agentRequest(t, server.URL+"/api/v1/agent/hardware", http.MethodGet, token, nil)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || len(payload["groups"].([]any)) != 1 || payload["groups"].([]any)[0].(map[string]any)["id"] != "board" {
		t.Fatalf("task-scoped hardware response: %d %#v", response.StatusCode, payload)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/hardware", http.MethodGet, "invalid", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid capability status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = agentRequest(t, server.URL+"/api/v1/agent/processes", http.MethodPost, token, map[string]any{
		"name": "escape", "executable": "server", "cwd": filepath.Dir(workspaceRecord.RootPath),
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("workspace escape status = %d", response.StatusCode)
	}
	response.Body.Close()

	capabilities.Revoke(token)
	response = agentRequest(t, server.URL+"/api/v1/agent/processes", http.MethodGet, token, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked capability status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestAgentStateKnowledgeAndSkillAPIs(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	now := time.Now().UTC()
	skill := domain.Skill{ID: "skill-agent-api", Scope: "global", Name: "Build", Description: "Build", Instructions: "Build only.", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := secretStore.PutMany(map[string]string{"hardware/" + task.ID + "/board/credential": "test-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
		TaskID: task.ID, ID: "board", Mode: domain.HardwareModeSerial,
		Serial: domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200}, Username: "root",
		CredentialRef: "hardware/" + task.ID + "/board/credential", PasswordConfigured: true,
		Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	claudeEnv := domain.EnvGroup{
		ID: domain.NewID("env"), Name: "Claude Env", ProviderID: "claude-provider", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	claudeModel := domain.Model{
		ID: domain.NewID("model"), DisplayName: "Claude Test", ProviderID: "claude-provider", Source: domain.ModelSourceProvider,
		Backend: "claude", WireModel: "claude-test", DefaultEnvGroupID: claudeEnv.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, claudeEnv); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, claudeModel); err != nil {
		t.Fatal(err)
	}
	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{})}
	service := app.NewService(database, secretStore, executor)
	if _, err := service.UpdateTaskSkills(ctx, task.ID, []string{skill.ID}); err != nil {
		t.Fatal(err)
	}
	turn, err := service.SubmitMessage(ctx, task.ID, "exercise Agent API")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}
	capabilities := agentapi.NewCapabilities()
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: database, App: service, AgentCapabilities: capabilities}).Handler())
	defer server.Close()

	response := agentRequest(t, server.URL+"/api/v1/agent/project/workspaces", http.MethodGet, token, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("default project capability status=%d", response.StatusCode)
	}
	response.Body.Close()
	if err := database.UpdateTaskAgentCapabilities(ctx, task.ID, map[string]bool{"workspace_read": true, "task_create": true, "clone_hardware": true}, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/project/workspaces", http.MethodGet, token, nil)
	var workspacesPayload map[string]any
	decodeResponse(t, response, &workspacesPayload)
	if response.StatusCode != http.StatusOK || len(workspacesPayload["workspaces"].([]any)) != 1 {
		t.Fatalf("workspaces status=%d payload=%#v", response.StatusCode, workspacesPayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/project/runtimes", http.MethodGet, token, nil)
	var runtimesPayload map[string]any
	decodeResponse(t, response, &runtimesPayload)
	if response.StatusCode != http.StatusOK || len(runtimesPayload["runtimes"].([]any)) < 2 {
		t.Fatalf("runtimes status=%d payload=%#v", response.StatusCode, runtimesPayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/tasks", http.MethodPost, token, map[string]any{
		"workspace_id": task.WorkspaceID, "title": "cloned hardware", "request": "test cloned hardware", "clone_hardware": true,
	})
	var taskPayload map[string]any
	decodeResponse(t, response, &taskPayload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("agent task status=%d payload=%#v", response.StatusCode, taskPayload)
	}
	createdTaskID := taskPayload["task"].(map[string]any)["id"].(string)
	response = agentRequest(t, server.URL+"/api/v1/agent/tasks/"+createdTaskID, http.MethodGet, token, nil)
	var taskStatusPayload map[string]any
	decodeResponse(t, response, &taskStatusPayload)
	if response.StatusCode != http.StatusOK || taskStatusPayload["task"].(map[string]any)["id"] != createdTaskID || len(taskStatusPayload["turns"].([]any)) != 1 {
		t.Fatalf("agent task status=%d payload=%#v", response.StatusCode, taskStatusPayload)
	}
	clonedGroups, err := database.HardwareGroups(ctx, createdTaskID)
	if err != nil || len(clonedGroups) != 1 || clonedGroups[0].CredentialRef == "" || clonedGroups[0].CredentialRef == "hardware/"+task.ID+"/board/credential" {
		t.Fatalf("cloned groups=%#v err=%v", clonedGroups, err)
	}
	if value, ok := secretStore.Get(clonedGroups[0].CredentialRef); !ok || value != "test-secret" {
		t.Fatal("hardware credential was not cloned server-side")
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/tasks", http.MethodPost, token, map[string]any{
		"workspace_id": task.WorkspaceID, "title": "claude runtime", "request": "test alternate backend",
		"backend": "claude", "model_source": "provider", "model_id": claudeModel.ID,
	})
	var alternatePayload map[string]any
	decodeResponse(t, response, &alternatePayload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("alternate runtime task status=%d payload=%#v", response.StatusCode, alternatePayload)
	}
	alternateTaskID := alternatePayload["task"].(map[string]any)["id"].(string)
	alternateTask, err := database.Task(ctx, alternateTaskID)
	if err != nil {
		t.Fatal(err)
	}
	alternateSnapshot, err := database.RuntimeSnapshot(ctx, alternateTask.RuntimeConfigSnapshotID)
	if err != nil || alternateSnapshot.Backend != "claude" || alternateSnapshot.ModelID != claudeModel.ID {
		t.Fatalf("alternate snapshot=%#v err=%v", alternateSnapshot, err)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/turn/memory", http.MethodPatch, token, map[string]any{"append": map[string]any{"facts": []string{"API fact"}}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("memory status=%d", response.StatusCode)
	}
	response.Body.Close()
	memory, _ := database.TaskMemory(ctx, task.ID)
	if len(memory.Facts) != 1 || memory.Facts[0] != "API fact" {
		t.Fatalf("memory=%#v", memory)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/candidates", http.MethodPost, token, map[string]any{"candidates": []map[string]any{{"scope": "project", "type": "practice", "title": "API knowledge", "body": "Submitted during the Turn.", "confidence": 0.9}}})
	var knowledgePayload map[string]any
	decodeResponse(t, response, &knowledgePayload)
	if response.StatusCode != http.StatusCreated || len(knowledgePayload["knowledge"].([]any)) != 1 {
		t.Fatalf("knowledge status=%d payload=%#v", response.StatusCode, knowledgePayload)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/skills/"+skill.ID, http.MethodPut, token, map[string]any{
		"base_version": 1, "name": "Build and deploy", "description": "Build and deploy AHA2",
		"files": []map[string]any{{"path": "SKILL.md", "content": "---\nname: build-and-deploy\ndescription: Build and deploy AHA2\n---\n\nDeploy safely.\n"}, {"path": "scripts/deploy.ps1", "content": "Write-Output 'deploy'\n"}},
	})
	var skillPayload map[string]any
	decodeResponse(t, response, &skillPayload)
	if response.StatusCode != http.StatusOK || skillPayload["skill"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("skill status=%d payload=%#v", response.StatusCode, skillPayload)
	}
	storedSkill, err := database.Skill(ctx, skill.ID)
	if err != nil || storedSkill.Version != 2 || len(storedSkill.PackageFiles) != 2 {
		t.Fatalf("stored skill=%#v err=%v", storedSkill, err)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/turn/messages", http.MethodPost, token, map[string]any{"message": "API progress"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("progress status=%d", response.StatusCode)
	}
	response.Body.Close()
	close(executor.release)
}

func agentRequest(t *testing.T, url, method, token string, body any) *http.Response {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
