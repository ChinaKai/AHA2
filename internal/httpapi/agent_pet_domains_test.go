package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
)

func createPetDomainHTTPTask(t *testing.T, h *coreMCPHarness, base domain.Task) domain.Task {
	t.Helper()
	now := time.Now().UTC()
	if err := h.database.CreateProject(h.ctx, domain.Project{ID: "project_pet", Name: "Pet", ProjectType: "system", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := h.database.CreateWorkspace(h.ctx, domain.Workspace{ID: "workspace_pet", ProjectID: "project_pet", Name: "Pet", Transport: "native", Locality: "local", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.database.RuntimeSnapshot(h.ctx, base.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{ID: "task_pet", ProjectID: "project_pet", WorkspaceID: "workspace_pet", Title: "Pet", Request: "fixture only", ModelID: snapshot.ModelID, Filesystem: "danger-full-access", Approval: "never", CollaborationMode: "single", MaxAgents: 1, AllowSystemProject: true, StartMode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := h.database.EnsureOwnerAssistant(h.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks = true, true
	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks, false, false, false, true, false, false, now); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	task := createPetDomainHTTPTask(t, h, base)
	turn := createCoreTurn(t, h.database, task, "main", "")
	token := h.issue(task.ID, "main", turn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"
	status, names, _ := coreMCPToolsList(t, url, token)
	if status != http.StatusOK || !coreMCPNameIncluded(names, "memory_update") || coreMCPNameIncluded(names, "task_message") {
		t.Fatalf("discovery status=%d names=%v", status, names)
	}
	for _, item := range []struct {
		name string
		args any
	}{
		{"memory_update", map[string]any{"status": "Pet persisted state", "next_steps": []string{"continue"}, "open_questions": []string{}}},
		{"progress", map[string]any{"message": "Pet structured progress"}},
		{"capabilities", map[string]any{}},
	} {
		result := coreMCPCallTool(t, url, token, item.name, item.args)
		if result.Status != http.StatusOK || result.IsError || result.Code != 0 {
			t.Fatalf("%s: %#v", item.name, result)
		}
	}

	knowledgeURL := h.server.URL + "/api/v1/agent/knowledge/mcp"
	_, knowledgeNames, _ := coreMCPToolsList(t, knowledgeURL, token)
	if len(knowledgeNames) != 2 || !coreMCPNameIncluded(knowledgeNames, "knowledge_guide") || !coreMCPNameIncluded(knowledgeNames, "knowledge_feedback") {
		t.Fatalf("pet duplicates or misses knowledge operations: %v", knowledgeNames)
	}
	entry := domain.KnowledgeEntry{ID: "pet-feedback-fixture", Scope: "global", Type: "practice", Title: "Fixture", Body: "BODY-MUST-NOT-BE-ECHOED", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := h.database.CreateKnowledge(h.ctx, entry); err != nil {
		t.Fatal(err)
	}
	feedback := coreMCPCallTool(t, knowledgeURL, token, "knowledge_feedback", map[string]any{"entry_id": entry.ID, "kind": "helped"})
	if feedback.Status != http.StatusOK || feedback.IsError || strings.Contains(feedback.Text, entry.Body) {
		t.Fatalf("feedback=%#v", feedback)
	}
	persisted, err := h.database.Knowledge(h.ctx, entry.ID)
	if err != nil || persisted.HelpedCount != 1 {
		t.Fatalf("feedback was not persisted: count=%d err=%v", persisted.HelpedCount, err)
	}
	guide := coreMCPCallTool(t, knowledgeURL, token, "knowledge_guide", map[string]any{})
	if guide.Status != http.StatusOK || guide.IsError {
		t.Fatalf("guide=%#v", guide)
	}
	memory, err := h.database.TaskMemory(h.ctx, task.ID)
	if err != nil || memory.Status != "Pet persisted state" {
		t.Fatalf("memory=%#v err=%v", memory, err)
	}
	denied := coreMCPCallTool(t, url, token, "task_message", map[string]any{"target_task_id": base.ID, "content": "must not cross projects"})
	if denied.Status != http.StatusForbidden {
		t.Fatalf("pet management bypass: %#v", denied)
	}
	turn.Status = domain.TurnSucceeded
	if err := h.database.UpdateTurn(h.ctx, turn, domain.TurnRunning); err != nil {
		t.Fatal(err)
	}
	sub := turn
	sub.ID, sub.AgentID, sub.Status = domain.NewID("turn"), "sub", domain.TurnRunning
	if _, err := h.database.UpsertTaskAgent(h.ctx, domain.TaskAgent{TaskID: task.ID, AgentID: "sub", Role: "sub", Status: "idle", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := h.database.CreateTurn(h.ctx, sub); err != nil {
		t.Fatal(err)
	}
	subToken := h.issue(task.ID, "sub", sub.ID)
	denied = coreMCPCallTool(t, url, subToken, "memory_update", map[string]any{"status": "forbidden"})
	if denied.Status != http.StatusForbidden {
		t.Fatalf("sub changed state: %#v", denied)
	}
	after, _ := h.database.TaskMemory(h.ctx, task.ID)
	if after.Status != memory.Status {
		t.Fatalf("denied call changed memory: %#v", after)
	}
}

func TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	task := createPetDomainHTTPTask(t, h, base)
	now := time.Now().UTC()
	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, []domain.HardwareGroup{{TaskID: task.ID, ID: "device", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.24", Port: 5555, Protocol: domain.HardwareProtocolADB}, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, ExecuteADB: func(_ctx context.Context, endpoint string, args []string, stdin []byte, timeout time.Duration) (hardware.ADBExecResult, error) {
		executions.Add(1)
		if endpoint != "192.0.2.24:5555" || strings.Join(args, " ") != "shell id" {
			t.Errorf("wrong bound target or command")
		}
		return hardware.ADBExecResult{Stdout: []byte("fixture-result"), ExitCode: 0}, nil
	}}).Handler())
	t.Cleanup(server.Close)
	turn := createCoreTurn(t, h.database, task, "main", "")
	token := h.issue(task.ID, "main", turn.ID)
	url := server.URL + "/api/v1/agent/hardware/mcp"
	status, names, _ := coreMCPToolsList(t, url, token)
	if status != http.StatusOK || !coreMCPNameIncluded(names, "hardware_adb_exec") {
		t.Fatalf("tools=%v status=%d", names, status)
	}
	args := map[string]any{"hardware_id": "device", "args": []string{"shell", "id"}}
	result := coreMCPCallTool(t, url, token, "hardware_adb_exec", args)
	if result.Status != http.StatusOK || result.IsError || !strings.Contains(result.Text, "Zml4dHVyZS1yZXN1bHQ=") || strings.Contains(result.Text, "192.0.2.24") || executions.Load() != 1 {
		t.Fatalf("exec result=%#v count=%d", result, executions.Load())
	}
	for _, invalid := range []map[string]any{
		{"hardware_id": "other-task-device", "args": []string{"shell", "id"}},
		{"hardware_id": "device", "args": []string{"pair"}},
		{"hardware_id": "device", "args": []string{"shell", "id"}, "password": "MCP-SECRET-MARKER"},
		{"hardware_id": "device", "args": []string{"shell", "id"}, "task_id": base.ID},
	} {
		denied := coreMCPCallTool(t, url, token, "hardware_adb_exec", invalid)
		if !denied.IsError && denied.Status == http.StatusOK {
			t.Fatalf("invalid call succeeded: %#v", denied)
		}
		if strings.Contains(denied.Text, "MCP-SECRET-MARKER") {
			t.Fatal("secret echoed")
		}
	}
	if executions.Load() != 1 {
		t.Fatalf("rejected calls executed: %d", executions.Load())
	}
	assistant, _ := h.database.EnsureOwnerAssistant(h.ctx, now)
	assistant.PetAllowCreateTasks = false
	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks, false, false, false, false, false, false, now); err != nil {
		t.Fatal(err)
	}
	_, names, _ = coreMCPToolsList(t, url, token)
	if coreMCPNameIncluded(names, "hardware_adb_exec") {
		t.Fatal("revoked write tool still visible")
	}
	denied := coreMCPCallTool(t, url, token, "hardware_adb_exec", args)
	if denied.Status != http.StatusForbidden || executions.Load() != 1 {
		t.Fatalf("revoked permission executed: %#v", denied)
	}
	assistant.PetAllowViewTasks = false
	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks, false, false, false, false, false, false, now); err != nil {
		t.Fatal(err)
	}
	denied = coreMCPCallTool(t, url, token, "hardware_list", map[string]any{})
	if denied.Status != http.StatusForbidden {
		t.Fatalf("revoked read still works: %#v", denied)
	}
}

func TestAgentMCPOperationRejectsInvalidBeforeHandlerAndSanitizesResults(t *testing.T) {
	called := 0
	op := agentMCPOperation{Tool: mcpTool{InputSchema: mcpObject(map[string]any{"n": mcpInteger(1, 2)}, "n")}, Method: http.MethodPost, ResultFields: []string{"ok"}, Handler: func(w http.ResponseWriter, r *http.Request) {
		called++
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "password": "SECRET-IN-HANDLER"})
	}}
	request := httptest.NewRequest(http.MethodPost, "/fixed", nil)
	for _, raw := range []string{`null`, `[]`, `{"n":null}`, `{"n":1.5}`, `{"n":3}`, `{"n":1,"url":"http://localhost"}`, `{"n":1} {}`} {
		if _, err := runAgentMCPOperation(request, op, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if called != 0 {
		t.Fatal("invalid input reached handler")
	}
	value, err := runAgentMCPOperation(request, op, json.RawMessage(`{"n":1}`))
	encoded, _ := json.Marshal(value)
	if err != nil || called != 1 || strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("value=%s err=%v calls=%d", encoded, err, called)
	}
}
