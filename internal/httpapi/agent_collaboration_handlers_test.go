package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAgentCollaborationDuplicateAssignmentReturnsConflict(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := app.NewService(database, nil, app.StubExecutor{})
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	task := createHardwareAPITask(t, database)
	if _, err := service.UpdateTaskCollaboration(ctx, task.ID, "auto", 4); err != nil {
		t.Fatal(err)
	}
	task, err = database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "delegate", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { capabilities.Revoke(token) })
	server := httptest.NewServer(New(Config{
		Store: database, App: service, AgentCapabilities: capabilities,
	}).Handler())
	defer server.Close()

	post := func(actions []map[string]any) (*http.Response, map[string]any) {
		t.Helper()
		response := agentRequest(t, server.URL+"/api/v1/agent/collaboration/batches", http.MethodPost, token,
			map[string]any{"actions": actions})
		var payload map[string]any
		decodeResponse(t, response, &payload)
		return response, payload
	}

	response, payload := post([]map[string]any{
		{"agent_id": "sub-001", "title": "First", "assignment": "implement the task", "required": true},
	})
	if response.StatusCode != http.StatusAccepted || payload["ok"] != true || payload["batch_id"] == "" {
		t.Fatalf("first dispatch: %d %#v", response.StatusCode, payload)
	}

	// The same agent in the same Main Turn must not look like a successful
	// empty dispatch: it is a conflict carrying the rejected agent_id.
	response, payload = post([]map[string]any{
		{"agent_id": "sub-001", "title": "Again", "assignment": "implement it again", "required": true},
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate dispatch status = %d payload=%#v", response.StatusCode, payload)
	}
	if payload["ok"] != false || payload["error"] != app.CollaborationAssignmentDuplicate {
		t.Fatalf("duplicate dispatch body = %#v", payload)
	}
	rejected, _ := payload["rejected_agents"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("rejected agents = %#v", payload["rejected_agents"])
	}
	item, _ := rejected[0].(map[string]any)
	if item["agent_id"] != "sub-001" || item["code"] != app.CollaborationAssignmentDuplicate ||
		item["reason"] != app.CollaborationAssignmentDuplicateInTurn {
		t.Fatalf("rejected detail = %#v", item)
	}

	// A partial batch keeps the accepted work visible instead of swallowing the
	// duplicate action.
	response, payload = post([]map[string]any{
		{"agent_id": "sub-002", "title": "Second", "assignment": "second assignment", "required": true},
		{"agent_id": "sub-002", "title": "Duplicate", "assignment": "duplicate assignment", "required": true},
		{"agent_id": "sub-003", "title": "Third", "assignment": "third assignment", "required": true},
	})
	if response.StatusCode != http.StatusConflict || payload["ok"] != false {
		t.Fatalf("partial dispatch: %d %#v", response.StatusCode, payload)
	}
	if payload["batch_id"] == "" || payload["created_agents"] != float64(2) {
		t.Fatalf("partial dispatch lost accepted work: %#v", payload)
	}
	accepted, _ := payload["accepted_agents"].([]any)
	if len(accepted) != 2 {
		t.Fatalf("accepted agents = %#v", payload["accepted_agents"])
	}
	rejected, _ = payload["rejected_agents"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("partial rejected agents = %#v", payload["rejected_agents"])
	}
	item, _ = rejected[0].(map[string]any)
	if item["agent_id"] != "sub-002" || item["reason"] != app.CollaborationAssignmentDuplicateInBatch {
		t.Fatalf("partial rejected detail = %#v", item)
	}

	// A repeated agent_id is a batch-level error even when the first action
	// fails its configuration before reaching the queue: the second action
	// must still be rejected instead of dispatching alone.
	response, payload = post([]map[string]any{
		{"agent_id": "sub-004", "title": "Broken", "assignment": "first assignment", "model_id": "model-missing", "required": true},
		{"agent_id": "sub-004", "title": "Valid", "assignment": "second assignment", "required": true},
	})
	if response.StatusCode != http.StatusConflict || payload["ok"] != false ||
		payload["error"] != app.CollaborationAssignmentDuplicate {
		t.Fatalf("failed-first duplicate dispatch: %d %#v", response.StatusCode, payload)
	}
	if payload["created_agents"] != float64(0) {
		t.Fatalf("failed-first duplicate created work: %#v", payload)
	}
	if _, ok := payload["batch_id"]; ok {
		t.Fatalf("failed-first duplicate reported a batch: %#v", payload)
	}
	rejected, _ = payload["rejected_agents"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("failed-first rejected agents = %#v", payload["rejected_agents"])
	}
	item, _ = rejected[0].(map[string]any)
	if item["agent_id"] != "sub-004" || item["code"] != app.CollaborationAssignmentDuplicate ||
		item["reason"] != app.CollaborationAssignmentDuplicateInBatch {
		t.Fatalf("failed-first rejected detail = %#v", item)
	}
}
