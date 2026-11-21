package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type blockingAgentAPIExecutor struct {
	started chan app.ExecutionRequest
	release chan struct{}
	// done closes when the first Execute returns. It only covers the backend
	// call: the service keeps writing the Turn result afterwards, and a Turn
	// may even schedule a follow-up one on the same executor, so tests that own
	// a TempDir must also wait for the Turn to settle and the run goroutines to
	// exit before returning.
	done     chan struct{}
	doneOnce sync.Once
}

func (executor *blockingAgentAPIExecutor) Execute(ctx context.Context, request app.ExecutionRequest, _ func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	defer executor.doneOnce.Do(func() { close(executor.done) })
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
		Store: database, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities, ManagedProcesses: processes,
	}).Handler())
	defer server.Close()
	turn := createCoreTurn(t, database, task, "main", domain.NewID("inbox_batch"))
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
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

func TestAgentADBExecUsesTaskDeviceAndReturnsBinary(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	now := time.Now().UTC()
	group := domain.HardwareGroup{
		TaskID: task.ID, ID: "phone", Description: "Android phone", Mode: domain.HardwareModeNetwork,
		Network: domain.HardwareNetworkConfig{Host: "192.0.2.90", Port: 5555, Protocol: domain.HardwareProtocolADB, SSHAuth: domain.HardwareSSHAuthAuto},
		Access:  domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{group}); err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities,
		ExecuteADB: func(_ context.Context, endpoint string, args []string, input []byte, timeout time.Duration) (hardware.ADBExecResult, error) {
			if endpoint != "192.0.2.90:5555" {
				t.Fatalf("unexpected ADB endpoint %q", endpoint)
			}
			if strings.Join(args, " ") != "exec-out screencap -p" || string(input) != "input" || timeout != 9*time.Second {
				t.Fatalf("unexpected ADB request args=%v input=%q timeout=%s", args, input, timeout)
			}
			return hardware.ADBExecResult{
				Endpoint: "192.0.2.90:41467", ExitCode: 0,
				Stdout: []byte("\x89PNG\r\n"), Stderr: []byte("warning"),
				Duration: 12 * time.Millisecond,
			}, nil
		},
	}).Handler())
	defer server.Close()
	turn := createCoreTurn(t, database, task, "main", domain.NewID("inbox_batch"))
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer capabilities.Revoke(token)
	response := agentRequest(t, server.URL+"/api/v1/agent/hardware/phone/adb/exec", http.MethodPost, token, map[string]any{
		"args":            []string{"exec-out", "screencap", "-p"},
		"stdin_base64":    base64.StdEncoding.EncodeToString([]byte("input")),
		"timeout_seconds": 9,
	})
	var payload struct {
		ExitCode      int    `json:"exit_code"`
		Endpoint      string `json:"endpoint"`
		StdoutBase64  string `json:"stdout_base64"`
		StderrBase64  string `json:"stderr_base64"`
		DurationMilli int64  `json:"duration_ms"`
	}
	decodeResponse(t, response, &payload)
	stdout, _ := base64.StdEncoding.DecodeString(payload.StdoutBase64)
	stderr, _ := base64.StdEncoding.DecodeString(payload.StderrBase64)
	if response.StatusCode != http.StatusOK || payload.Endpoint != "192.0.2.90:41467" || payload.ExitCode != 0 || string(stdout) != "\x89PNG\r\n" || string(stderr) != "warning" || payload.DurationMilli != 12 {
		t.Fatalf("ADB exec status=%d payload=%#v stdout=%q stderr=%q", response.StatusCode, payload, stdout, stderr)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/hardware/phone/adb/exec", http.MethodPost, token, map[string]any{
		"args": []string{"devices"},
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("host-level ADB command status=%d", response.StatusCode)
	}
	response.Body.Close()
	group.Access = domain.HardwareAccessReadOnly
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{group}); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/hardware/phone/adb/exec", http.MethodPost, token, map[string]any{
		"args": []string{"exec-out", "screencap", "-p"},
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only ADB exec status=%d", response.StatusCode)
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
	// Skills are vault packages. Bind the hardware task's project and seed the
	// global Build package the update step modifies.
	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(vaultConfig, knowledgevault.InitOptions{VaultName: "Agent API"}); err != nil {
		t.Fatal(err)
	}
	writeVault := func(relative, content string) {
		target := filepath.Join(vaultConfig.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeVault("projects/agent-api/index.md", "---\nid: knowledge_root_agent_api\ntitle: Agent API\nscope: project\nproject_id: "+task.ProjectID+"\ntype: navigation\nstatus: verified\nis_index: true\n---\nAgent API entry.\n")
	writeVault("global/skills/build/SKILL.md", "---\nname: Build\ndescription: Build\nversion: 1\n---\n\nBuild only.\n")
	index, err := knowledgevault.OpenIndex(ctx, vaultConfig.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, vaultConfig.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	skillID := "vault-skill:global:build"
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
	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}), done: make(chan struct{})}
	service := app.NewService(database, secretStore, executor)
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)
	if _, err := service.UpdateTaskSkills(ctx, task.ID, []string{skillID}); err != nil {
		t.Fatal(err)
	}
	// The knowledge section below still exercises the legacy tables: keep the
	// vault write engine available for skills but route knowledge reads/writes
	// to legacy until the skill API section re-enables vault reads.
	service.SetKnowledgeVault(manager, false)
	turn, err := service.SubmitMessage(ctx, task.ID, "exercise Agent API")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}
	activeTask, err := database.Task(ctx, task.ID)
	if err != nil || activeTask.CurrentGoal != "debug board" {
		t.Fatalf("owner message mechanically replaced current goal: task=%#v err=%v", activeTask, err)
	}
	activeMemory, err := database.TaskMemory(ctx, task.ID)
	if err != nil || activeMemory.CurrentGoal != activeTask.CurrentGoal {
		t.Fatalf("task memory goal diverged: memory=%#v err=%v", activeMemory, err)
	}
	capabilities := agentapi.NewCapabilities()
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: database, App: service, AgentCapabilities: capabilities, KnowledgeVault: manager}).Handler())
	defer server.Close()

	response := agentRequest(t, server.URL+"/api/v1/agent/capabilities", http.MethodGet, token, nil)
	var capabilitiesPayload map[string]any
	decodeResponse(t, response, &capabilitiesPayload)
	capabilityFlags := capabilitiesPayload["capabilities"].(map[string]any)
	if response.StatusCode != http.StatusOK || capabilityFlags["skill_create"] != true || capabilityFlags["live_inbox_wait"] != true {
		t.Fatalf("skill create capability status=%d payload=%#v", response.StatusCode, capabilitiesPayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/wait", http.MethodPost, token, map[string]any{
		"after_cursor": 0, "timeout_seconds": 1,
	})
	var inboxWaitPayload struct {
		Messages   []map[string]any `json:"messages"`
		NextCursor int64            `json:"next_cursor"`
	}
	decodeResponse(t, response, &inboxWaitPayload)
	if response.StatusCode != http.StatusOK || len(inboxWaitPayload.Messages) != 0 || inboxWaitPayload.NextCursor != 0 {
		t.Fatalf("inbox wait status=%d payload=%#v", response.StatusCode, inboxWaitPayload)
	}

	// The three project-level abilities are available to Main without any
	// persisted grant on the Task. The state flags keep reporting them for
	// older clients.
	if capabilityFlags["workspace_read"] != true || capabilityFlags["task_create"] != true || capabilityFlags["clone_hardware"] != true {
		t.Fatalf("project capabilities are not reported as available by default: %#v", capabilityFlags)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/project/workspaces", http.MethodGet, token, nil)
	var workspacesPayload map[string]any
	decodeResponse(t, response, &workspacesPayload)
	listed, _ := workspacesPayload["workspaces"].([]any)
	if response.StatusCode != http.StatusOK || len(listed) != 1 || listed[0].(map[string]any)["id"] != task.WorkspaceID {
		t.Fatalf("workspaces status=%d payload=%#v", response.StatusCode, workspacesPayload)
	}
	// Non-Main turns stay refused even though Main needs no grant.
	subTurn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "sub-001",
		Sequence: turn.Sequence + 1, InputMessageID: turn.InputMessageID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, Attempt: 1, Generation: 1, QueuedAt: time.Now().UTC(),
	}
	if err := database.CreateTurn(ctx, subTurn); err != nil {
		t.Fatal(err)
	}
	subToken, err := capabilities.Issue(task.ID, "sub-001", subTurn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer capabilities.Revoke(subToken)
	response = agentRequest(t, server.URL+"/api/v1/agent/project/workspaces", http.MethodGet, subToken, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-Main workspace list status=%d", response.StatusCode)
	}
	response.Body.Close()
	// A Task still cannot be created in another Project's Workspace.
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", CreatedAt: now, UpdatedAt: now}
	otherWorkspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: otherProject.ID, Name: "Other", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/tasks", http.MethodPost, token, map[string]any{
		"workspace_id": otherWorkspace.ID, "title": "cross project", "request": "must be refused",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-project task status=%d", response.StatusCode)
	}
	response.Body.Close()
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
	if err != nil || len(clonedGroups) != 1 || clonedGroups[0].ResourceID == "" || clonedGroups[0].CredentialRef != "hardware/"+task.ID+"/board/credential" {
		t.Fatalf("cloned groups=%#v err=%v", clonedGroups, err)
	}
	if value, ok := secretStore.Get(clonedGroups[0].CredentialRef); !ok || value != "test-secret" {
		t.Fatal("cloned binding lost the shared hardware credential")
	}

	// Without an explicit clone_hardware request the new Task must not inherit
	// the source hardware bindings.
	response = agentRequest(t, server.URL+"/api/v1/agent/tasks", http.MethodPost, token, map[string]any{
		"workspace_id": task.WorkspaceID, "title": "no hardware", "request": "test default hardware",
	})
	var noClonePayload map[string]any
	decodeResponse(t, response, &noClonePayload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("agent task without hardware status=%d payload=%#v", response.StatusCode, noClonePayload)
	}
	noCloneTaskID := noClonePayload["task"].(map[string]any)["id"].(string)
	if groups, err := database.HardwareGroups(ctx, noCloneTaskID); err != nil || len(groups) != 0 {
		t.Fatalf("hardware was copied without clone_hardware: groups=%#v err=%v", groups, err)
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

	memoryURL := server.URL + "/api/v1/agent/turn/memory"
	// Rejected updates must leave both the goal and the card untouched.
	for name, body := range map[string]map[string]any{
		"goal without reason": {"current_goal": "Unreasoned objective"},
		"empty update":        {},
		"status too long":     {"status": strings.Repeat("长", domain.TaskMemoryStatusMaxRunes+1)},
		"too many next steps": {"next_steps": []string{"1", "2", "3", "4", "5", "6", "7"}},
		"goal plus bad status": {
			"current_goal": "Must not be recorded", "goal_reason": "rejected with the status",
			"status": strings.Repeat("长", domain.TaskMemoryStatusMaxRunes+1),
		},
	} {
		response = agentRequest(t, memoryURL, http.MethodPatch, token, body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("memory %s status=%d body=%s", name, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	if untouched, _ := database.TaskMemory(ctx, task.ID); untouched.Status != "" || untouched.CurrentGoal == "Must not be recorded" {
		t.Fatalf("rejected memory update left a trace: %#v", untouched)
	}

	response = agentRequest(t, memoryURL, http.MethodPatch, token, map[string]any{
		"current_goal": "Refined active objective", "goal_change_type": "revise",
		"goal_reason":     "The requested objective changed",
		"status":          "Root cause found; fix written, integration tests pending.",
		"next_steps":      []string{"Run integration tests", "Run integration tests", "  "},
		"open_questions":  []string{"Also fix the log leak?"},
		"add_constraints": []string{"Do not change the public API", "Use option B"},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("memory status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	var memoryResponse struct {
		Memory domain.TaskMemory `json:"memory"`
	}
	if err := json.NewDecoder(response.Body).Decode(&memoryResponse); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if memoryResponse.Memory.Status == "" || len(memoryResponse.Memory.GoalHistory) == 0 || memoryResponse.Memory.Extra != nil {
		t.Fatalf("memory response is not the state card: %#v", memoryResponse.Memory)
	}
	memory, _ := database.TaskMemory(ctx, task.ID)
	updatedGoalTask, _ := database.Task(ctx, task.ID)
	if memory.CurrentGoal != "Refined active objective" || updatedGoalTask.CurrentGoal != memory.CurrentGoal ||
		len(memory.NextSteps) != 1 || memory.NextSteps[0] != "Run integration tests" ||
		len(memory.OpenQuestions) != 1 || len(memory.Constraints) != 2 ||
		memory.Constraints[0].Text != "Do not change the public API" || memory.Constraints[0].TurnID == "" ||
		memory.StatusTurnID == "" || memory.StatusUpdatedAt.IsZero() {
		t.Fatalf("memory=%#v", memory)
	}
	lastGoalChange := memory.GoalHistory[len(memory.GoalHistory)-1]
	if lastGoalChange.GoalAfter != "Refined active objective" || lastGoalChange.ChangeType != "revise" ||
		lastGoalChange.Reason != "The requested objective changed" || lastGoalChange.TurnID == "" {
		t.Fatalf("goal change=%#v", lastGoalChange)
	}
	// The "now" fields are replaced, an empty list clears, and a constraint is
	// removed by its exact text while the others stay.
	response = agentRequest(t, memoryURL, http.MethodPatch, token, map[string]any{
		"status": "Integration tests pass.", "next_steps": []string{}, "open_questions": []string{},
		"add_constraints": []string{"Use option B"}, "remove_constraints": []string{"Do not change the public API"},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("memory replacement status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	memory, _ = database.TaskMemory(ctx, task.ID)
	if memory.Status != "Integration tests pass." || len(memory.NextSteps) != 0 || len(memory.OpenQuestions) != 0 ||
		len(memory.Constraints) != 1 || memory.Constraints[0].Text != "Use option B" ||
		memory.CurrentGoal != "Refined active objective" {
		t.Fatalf("replaced memory=%#v", memory)
	}

	projectBindingContainer := domain.Project{ID: "project-agent-bound-write", Name: "Writable library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	externalBindingContainer := domain.Project{ID: "project-agent-bound-read", Name: "Read-only library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	for _, container := range []domain.Project{projectBindingContainer, externalBindingContainer} {
		if err := database.CreateProject(ctx, container); err != nil {
			t.Fatal(err)
		}
	}
	projectLibrary, _ := database.KnowledgeLibrary(ctx, "library_"+projectBindingContainer.ID)
	externalLibrary, _ := database.KnowledgeLibrary(ctx, "library_"+externalBindingContainer.ID)
	if _, err := database.BindKnowledgeLibrary(ctx, projectLibrary.ID, task.ProjectID, "project", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BindKnowledgeLibrary(ctx, externalLibrary.ID, task.ProjectID, "external", now); err != nil {
		t.Fatal(err)
	}
	projectLibraryRoot, _ := database.EnsureKnowledgeRoot(ctx, "project", projectBindingContainer.ID)
	externalLibraryRoot, _ := database.EnsureKnowledgeRoot(ctx, "project", externalBindingContainer.ID)
	projectBoundEntry := domain.KnowledgeEntry{
		ID: "knowledge-project-bound-api", Scope: "project", ProjectID: projectBindingContainer.ID, ParentID: projectLibraryRoot.ID, Slug: "project-bound-api",
		Type: "practice", Title: "Project-bound API knowledge", Body: "Original project-bound body", Status: domain.KnowledgeVerified,
		Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	externalBoundEntry := domain.KnowledgeEntry{
		ID: "knowledge-external-bound-api", Scope: "project", ProjectID: externalBindingContainer.ID, ParentID: externalLibraryRoot.ID, Slug: "external-bound-api",
		Type: "practice", Title: "External-bound API knowledge", Body: "Original external body", Status: domain.KnowledgeVerified,
		Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	for _, entry := range []domain.KnowledgeEntry{projectBoundEntry, externalBoundEntry} {
		if err := database.CreateKnowledge(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/capabilities", http.MethodGet, token, nil)
	decodeResponse(t, response, &capabilitiesPayload)
	if response.StatusCode != http.StatusOK || capabilitiesPayload["capabilities"].(map[string]any)["knowledge_contribute_bound"] != true {
		t.Fatalf("bound knowledge capability status=%d payload=%#v", response.StatusCode, capabilitiesPayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge", http.MethodGet, token, nil)
	var boundKnowledgePayload map[string]any
	decodeResponse(t, response, &boundKnowledgePayload)
	canPropose := map[string]bool{}
	for _, raw := range boundKnowledgePayload["knowledge"].([]any) {
		entry := raw.(map[string]any)
		if id, _ := entry["id"].(string); id == projectBoundEntry.ID || id == externalBoundEntry.ID {
			canPropose[id], _ = entry["can_propose_revision"].(bool)
		}
	}
	if !canPropose[projectBoundEntry.ID] || canPropose[externalBoundEntry.ID] {
		t.Fatalf("bound knowledge proposal flags=%#v", canPropose)
	}
	if _, err := database.UpdateKnowledgeReviewSettings(ctx, domain.KnowledgeReviewSettings{AutoApprove: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/candidates", http.MethodPost, token, map[string]any{"candidates": []map[string]any{{
		"entry_id": projectBoundEntry.ID, "base_revision": 1, "scope": "project", "type": "practice", "title": projectBoundEntry.Title, "body": "Proposed from the bound project.", "confidence": 0.9,
	}}})
	decodeResponse(t, response, &boundKnowledgePayload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("project-bound proposal status=%d payload=%#v", response.StatusCode, boundKnowledgePayload)
	}
	boundProposal := boundKnowledgePayload["proposals"].([]any)[0].(map[string]any)
	proposed := boundProposal["proposed"].(map[string]any)
	if proposed["project_id"] != projectBindingContainer.ID || boundProposal["review_mode"] != "manual" || boundProposal["status"] != "pending" {
		t.Fatalf("project-bound proposal lost source ownership: %#v", boundProposal)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/candidates", http.MethodPost, token, map[string]any{"candidates": []map[string]any{{
		"entry_id": externalBoundEntry.ID, "base_revision": 1, "scope": "project", "type": "practice", "title": externalBoundEntry.Title, "body": "Must remain read-only.", "confidence": 0.9,
	}}})
	var readOnlyPayload map[string]any
	decodeResponse(t, response, &readOnlyPayload)
	if response.StatusCode != http.StatusForbidden || readOnlyPayload["error"] != "knowledge_entry_read_only" {
		t.Fatalf("external-bound proposal status=%d payload=%#v", response.StatusCode, readOnlyPayload)
	}
	if _, err := database.UpdateKnowledgeReviewSettings(ctx, domain.KnowledgeReviewSettings{AutoApprove: false, UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	root, err := database.EnsureKnowledgeRoot(ctx, "project", task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	existingKnowledge := domain.KnowledgeEntry{
		ID: "knowledge-existing-api", Scope: "project", ProjectID: task.ProjectID, ParentID: root.ID, Slug: "existing-api",
		Type: "practice", Title: "Existing API knowledge", Body: "Published body", Status: domain.KnowledgeVerified,
		Revision: 2, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if err := database.CreateKnowledge(ctx, existingKnowledge); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/candidates", http.MethodPost, token, map[string]any{"candidates": []map[string]any{
		{"scope": "project", "slug": "api-knowledge", "sort_order": 4, "type": "practice", "title": "API knowledge", "body": "Submitted during the Turn.", "confidence": 0.9},
		{"scope": "project", "slug": "api-navigation", "sort_order": 5, "type": "navigation", "title": "API navigation", "body": "Navigate from the Turn.", "confidence": 0.9},
	}})
	var knowledgePayload map[string]any
	decodeResponse(t, response, &knowledgePayload)
	knowledgeItems, _ := knowledgePayload["knowledge"].([]any)
	proposalItems, _ := knowledgePayload["proposals"].([]any)
	if response.StatusCode != http.StatusCreated || len(knowledgeItems) != 2 || len(proposalItems) != 2 {
		t.Fatalf("knowledge status=%d payload=%#v", response.StatusCode, knowledgePayload)
	}
	knowledge := knowledgeItems[0].(map[string]any)
	if knowledge["parent_id"] != root.ID || knowledge["slug"] != "api-knowledge" || knowledge["sort_order"].(float64) != 4 || knowledge["is_index"] != false {
		t.Fatalf("knowledge status=%d payload=%#v", response.StatusCode, knowledgePayload)
	}
	navigation := knowledgeItems[1].(map[string]any)
	if navigation["parent_id"] != root.ID || navigation["type"] != "navigation" || navigation["is_index"] != false {
		t.Fatalf("navigation knowledge status=%d payload=%#v", response.StatusCode, knowledgePayload)
	}
	for _, raw := range proposalItems {
		if raw.(map[string]any)["status"] != "pending" {
			t.Fatalf("proposal was not pending: %#v", raw)
		}
	}
	if _, err := database.Knowledge(ctx, knowledge["id"].(string)); err == nil {
		t.Fatal("new candidate was auto-published")
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/candidates", http.MethodPost, token, map[string]any{"candidates": []map[string]any{{"entry_id": existingKnowledge.ID, "base_revision": existingKnowledge.Revision, "scope": "project", "type": "practice", "title": "Existing revised", "body": "Pending replacement.", "confidence": 0.9}}})
	decodeResponse(t, response, &knowledgePayload)
	knowledgeItems, _ = knowledgePayload["knowledge"].([]any)
	proposalItems, _ = knowledgePayload["proposals"].([]any)
	if response.StatusCode != http.StatusCreated || len(knowledgeItems) != 1 || len(proposalItems) != 1 {
		t.Fatalf("revision proposal status=%d payload=%#v", response.StatusCode, knowledgePayload)
	}
	staleKnowledge, err := database.Knowledge(ctx, existingKnowledge.ID)
	if err != nil || staleKnowledge.Status != domain.KnowledgeStale || staleKnowledge.Revision != existingKnowledge.Revision {
		t.Fatalf("existing knowledge=%#v err=%v", staleKnowledge, err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge", http.MethodGet, token, nil)
	decodeResponse(t, response, &knowledgePayload)
	knowledgeItems, _ = knowledgePayload["knowledge"].([]any)
	rootCount := 0
	pendingVisible := false
	for _, raw := range knowledgeItems {
		item := raw.(map[string]any)
		if item["is_index"] == true {
			rootCount++
		}
		pendingVisible = pendingVisible || item["title"] == "API navigation" || item["id"] == existingKnowledge.ID
	}
	if response.StatusCode != http.StatusOK || rootCount != 2 || !pendingVisible {
		t.Fatalf("applicable knowledge roots=%d payload=%#v", rootCount, knowledgePayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/knowledge/"+existingKnowledge.ID, http.MethodGet, token, nil)
	decodeResponse(t, response, &knowledgePayload)
	if response.StatusCode != http.StatusOK || knowledgePayload["knowledge"].(map[string]any)["status"] != "stale" {
		t.Fatalf("stale knowledge by id status=%d payload=%#v", response.StatusCode, knowledgePayload)
	}

	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)
	response = agentRequest(t, server.URL+"/api/v1/agent/skills", http.MethodPost, token, map[string]any{
		"name": "Release checks", "description": "Verify an AHA2 release", "instructions": "Run the release verification suite.",
	})
	var createdSkillPayload map[string]any
	decodeResponse(t, response, &createdSkillPayload)
	createdSkill := createdSkillPayload["skill"].(map[string]any)
	createdSkillID := createdSkill["id"].(string)
	if response.StatusCode != http.StatusCreated || createdSkill["scope"] != "project" || createdSkill["project_id"] != task.ProjectID || createdSkill["version"] != float64(1) {
		t.Fatalf("created skill status=%d payload=%#v", response.StatusCode, createdSkillPayload)
	}
	updatedTask, err := database.Task(ctx, task.ID)
	if err != nil || len(updatedTask.SkillIDs) != 2 || updatedTask.SkillIDs[1] != createdSkillID {
		t.Fatalf("created skill was not selected: task=%#v err=%v", updatedTask, err)
	}
	// A payload with a field the contract does not define is rejected, and the
	// response has to say which field -- the bare code left callers guessing.
	response = agentRequestRaw(t, server.URL+"/api/v1/agent/skills", http.MethodPost, token,
		[]byte(`{"name":"Global checks","instructions":"Run them.","scope_bogus":"global"}`))
	var rejectedPayload map[string]any
	decodeResponse(t, response, &rejectedPayload)
	if response.StatusCode != http.StatusBadRequest || rejectedPayload["error"] != "skill_create_invalid" {
		t.Fatalf("unknown field was not rejected: status=%d payload=%#v", response.StatusCode, rejectedPayload)
	}
	if message, _ := rejectedPayload["message"].(string); !strings.Contains(message, "scope_bogus") {
		t.Fatalf("the rejection does not name the offending field: %#v", rejectedPayload)
	}

	// scope defaults to project, and asking for global is honoured when allowed.
	response = agentRequest(t, server.URL+"/api/v1/agent/skills", http.MethodPost, token, map[string]any{
		"name": "Global checks", "description": "Applies everywhere", "instructions": "Run them.", "scope": "global",
	})
	var globalSkillPayload map[string]any
	decodeResponse(t, response, &globalSkillPayload)
	globalSkill := globalSkillPayload["skill"].(map[string]any)
	// project_id is omitempty, so a global skill carries no project at all.
	if response.StatusCode != http.StatusCreated || globalSkill["scope"] != "global" {
		t.Fatalf("global skill status=%d payload=%#v", response.StatusCode, globalSkillPayload)
	}
	if projectID, present := globalSkill["project_id"]; present && projectID != "" {
		t.Fatalf("global skill kept a project: %#v", globalSkill)
	}

	// An unknown scope is refused by name rather than silently treated as project.
	response = agentRequest(t, server.URL+"/api/v1/agent/skills", http.MethodPost, token, map[string]any{
		"name": "Bad scope", "instructions": "Run them.", "scope": "workspace",
	})
	var badScopePayload map[string]any
	decodeResponse(t, response, &badScopePayload)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown scope was accepted: status=%d payload=%#v", response.StatusCode, badScopePayload)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/skills/"+createdSkillID, http.MethodGet, token, nil)
	var selectedSkillPayload map[string]any
	decodeResponse(t, response, &selectedSkillPayload)
	if response.StatusCode != http.StatusOK || selectedSkillPayload["skill"].(map[string]any)["id"] != createdSkillID {
		t.Fatalf("created skill was not immediately readable: status=%d payload=%#v", response.StatusCode, selectedSkillPayload)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/skills/"+skillID, http.MethodPut, token, map[string]any{
		"base_version": 1, "name": "Build and deploy", "description": "Build and deploy AHA2",
		"files": []map[string]any{{"path": "SKILL.md", "content": "---\nname: build-and-deploy\ndescription: Build and deploy AHA2\n---\n\nDeploy safely.\n"}, {"path": "scripts/deploy.ps1", "content": "Write-Output 'deploy'\n"}},
	})
	var skillPayload map[string]any
	decodeResponse(t, response, &skillPayload)
	if response.StatusCode != http.StatusOK || skillPayload["skill"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("skill status=%d payload=%#v", response.StatusCode, skillPayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/skills/"+skillID, http.MethodGet, token, nil)
	var storedSkillPayload map[string]any
	decodeResponse(t, response, &storedSkillPayload)
	storedSkill := storedSkillPayload["skill"].(map[string]any)
	if response.StatusCode != http.StatusOK || storedSkill["version"] != float64(2) || len(storedSkillPayload["files"].([]any)) != 2 {
		t.Fatalf("stored skill status=%d payload=%#v", response.StatusCode, storedSkillPayload)
	}

	response = agentMultipartRequest(t, server.URL+"/api/v1/agent/turn/attachments", token, "generated.png", []byte("\x89PNG\r\ngenerated"))
	var attachmentPayload map[string]any
	decodeResponse(t, response, &attachmentPayload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("attachment status=%d payload=%#v", response.StatusCode, attachmentPayload)
	}
	attachmentID := attachmentPayload["attachment"].(map[string]any)["id"].(string)
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/messages", http.MethodPost, token, map[string]any{"message": "API progress", "attachment_ids": []string{attachmentID}})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("progress status=%d", response.StatusCode)
	}
	response.Body.Close()
	page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundAttachment := false
	for _, item := range page.Items {
		attachments, _ := item.Payload["attachments"].([]any)
		if item.Summary == "API progress" && len(attachments) == 1 {
			foundAttachment = true
		}
	}
	if !foundAttachment {
		t.Fatalf("Agent progress attachment missing: %#v", page.Items)
	}
	close(executor.release)
	select {
	case <-executor.done:
	case <-time.After(5 * time.Second):
		t.Fatal("backend execution did not return after release")
	}
	// Execute returning is not the end of the Turn: the service still records
	// the reply, closes the backend session, and settles the round. Wait for
	// that goroutine to finish before the test returns, otherwise its writes
	// keep landing in the TempDirs while the test framework removes them.
	settleDeadline := time.Now().Add(10 * time.Second)
	for {
		settledTurn, err := database.Turn(ctx, turn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if settledTurn.Status.Terminal() {
			break
		}
		if time.Now().After(settleDeadline) {
			t.Fatalf("turn %s did not reach a terminal state: %s", turn.ID, settledTurn.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown service: %v", err)
	}
}

// agentRequestRaw sends a body verbatim, which the map-based helper cannot do:
// a field that must not be accepted cannot be expressed through a Go map.
func agentRequestRaw(t *testing.T, url, method, token string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
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

func agentMultipartRequest(t *testing.T, url, token, name string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
