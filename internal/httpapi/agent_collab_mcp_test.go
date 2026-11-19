package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func collabMCPURL(h *coreMCPHarness) string {
	return h.server.URL + "/api/v1/agent/collab/mcp"
}

// collabMCPTask creates an ordinary Task and switches it to the requested
// collaboration mode. createHardwareAPITask supplies the Project, Workspace,
// env group and model the rest of the harness expects.
func collabMCPTask(t *testing.T, h *coreMCPHarness, mode string, maxAgents int) domain.Task {
	t.Helper()
	task := createHardwareAPITask(t, h.database)
	updated, err := h.service.UpdateTaskCollaboration(h.ctx, task.ID, mode, maxAgents)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestAgentCollabMCPRequiresCapabilityAndDomainGate(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := collabMCPTask(t, h, "auto", 4)
	turn := createCoreTurn(t, h.database, task, "main", "batch-collab-auth")
	token := h.issue(task.ID, "main", turn.ID)
	url := collabMCPURL(h)

	response := agentRequest(t, url, http.MethodPost, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, "not-a-real-token", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid capability status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	missingTurnToken := h.issue(task.ID, "main", "turn-does-not-exist")
	response = agentRequest(t, url, http.MethodPost, missingTurnToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing turn status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	otherTask := collabMCPTask(t, h, "auto", 4)
	otherTurn := createCoreTurn(t, h.database, otherTask, "main", "batch-collab-other-task")
	mismatchedToken := h.issue(task.ID, "main", otherTurn.ID)
	response = agentRequest(t, url, http.MethodPost, mismatchedToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("other-task turn status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "initialize",
	})
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeResponse(t, response, &initialized)
	if response.StatusCode != http.StatusOK || initialized.Result.ServerInfo.Name != "aha2-collab" {
		t.Fatalf("initialize status=%d payload=%#v", response.StatusCode, initialized)
	}

	status, names, schemas := coreMCPToolsList(t, url, token)
	want := app.CollabMCPToolNames()
	if status != http.StatusOK || len(names) != len(want) {
		t.Fatalf("tools/list status=%d names=%#v want=%#v", status, names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("tools/list names=%#v want=%#v", names, want)
		}
		schema, ok := schemas[name]
		if !ok || schema["additionalProperties"] != false {
			t.Fatalf("tool %s schema=%#v", name, schema)
		}
	}
	submitSchema := schemas["collab_batch_submit"]
	actions, _ := submitSchema["properties"].(map[string]any)["actions"].(map[string]any)
	item, _ := actions["items"].(map[string]any)
	if item["additionalProperties"] != false {
		t.Fatalf("action schema accepts unknown fields: %#v", item)
	}
	required, _ := item["required"].([]any)
	if len(required) != 3 {
		t.Fatalf("action required fields=%#v", required)
	}

	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		request, err := http.NewRequest(http.MethodPost, url,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(header, "203.0.113.9")
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s status=%d body=%s", header, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}

	oversized := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":"` +
		strings.Repeat("x", int(agentCollabMCPMaxRequestBytes)+1024) + `"}`)
	response = agentRequestRaw(t, url, http.MethodPost, token, oversized)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequestRaw(t, url, http.MethodPost, token, []byte(`null`))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("null body status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

// TestAgentCollabMCPRejectsSingleModeAndNonMain proves the refusal is
// server-side, not just hidden tools: a single-agent Task and a non-Main Turn
// both fail initialize and tools/call, and the shared HTTP submit endpoint
// refuses the single-agent Task with the same classification.
func TestAgentCollabMCPRejectsSingleModeAndNonMain(t *testing.T) {
	h := newCoreMCPHarness(t)
	url := collabMCPURL(h)

	single := createHardwareAPITask(t, h.database)
	singleTurn := createCoreTurn(t, h.database, single, "main", "batch-collab-single")
	singleToken := h.issue(single.ID, "main", singleTurn.ID)
	if call := coreMCPCallTool(t, url, singleToken, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-001", "title": "Worker", "assignment": "work",
		}},
		"integration_instructions": "integrate",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("single-mode MCP submit payload=%#v", call)
	}
	response := agentRequest(t, url, http.MethodPost, singleToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("single-mode initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, h.server.URL+"/api/v1/agent/collaboration/batches", http.MethodPost, singleToken, map[string]any{
		"actions": []map[string]any{{"agent_id": "sub-001", "title": "Worker", "assignment": "work"}},
	})
	var rejected struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	decodeResponse(t, response, &rejected)
	if response.StatusCode != http.StatusForbidden || rejected.OK || rejected.Error != "collaboration_requires_auto_mode" {
		t.Fatalf("single-mode HTTP submit=%d %#v", response.StatusCode, rejected)
	}

	auto := collabMCPTask(t, h, "auto", 4)
	subTurn := createCoreTurn(t, h.database, auto, "sub-001", "batch-collab-non-main")
	subToken := h.issue(auto.ID, "sub-001", subTurn.ID)
	if call := coreMCPCallTool(t, url, subToken, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-002", "title": "Worker", "assignment": "work",
		}},
		"integration_instructions": "integrate",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("non-main submit payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, subToken, "collab_batch_wait", map[string]any{
		"batch_id": "batch-collab-non-main",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("non-main wait payload=%#v", call)
	}
	response = agentRequest(t, url, http.MethodPost, subToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-main initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

func TestAgentCollabMCPSubmitAndWait(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := collabMCPTask(t, h, "auto", 4)
	turn := createCoreTurn(t, h.database, task, "main", "batch-collab-submit")
	token := h.issue(task.ID, "main", turn.ID)
	url := collabMCPURL(h)
	const assignment = "implement the approved collab slice"
	const instructions = "integrate the approved collab result"

	call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-001", "title": "Worker", "assignment": assignment, "required": true,
		}},
		"integration_instructions": instructions,
	})
	if call.Status != http.StatusOK || call.IsError {
		t.Fatalf("submit payload=%#v", call)
	}
	var submitted struct {
		BatchID       string `json:"batch_id"`
		CreatedAgents int    `json:"created_agents"`
	}
	if err := json.Unmarshal([]byte(call.Text), &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.BatchID == "" || submitted.CreatedAgents != 1 {
		t.Fatalf("submit result=%#v", submitted)
	}

	call = coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
		"batch_id": submitted.BatchID, "after_cursor": 0, "timeout_seconds": 2,
	})
	if call.Status != http.StatusOK || call.IsError {
		t.Fatalf("wait payload=%#v", call)
	}
	var progress struct {
		BatchID    string            `json:"batch_id"`
		Complete   bool              `json:"complete"`
		Results    []json.RawMessage `json:"results"`
		NextCursor int64             `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(call.Text), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.BatchID != submitted.BatchID {
		t.Fatalf("wait batch=%q want=%q", progress.BatchID, submitted.BatchID)
	}

	audit := waitForCollabMCPAudit(t, h.dbPath, "collab_batch_submit")
	for _, leak := range []string{assignment, instructions, "sub-001", "integrate"} {
		if strings.Contains(audit, leak) {
			t.Fatalf("audit leaked %q: %s", leak, audit)
		}
	}
	if !strings.Contains(audit, `"argument_keys":["actions","integration_instructions"]`) {
		t.Fatalf("audit shape=%s", audit)
	}
	waitAudit := waitForCollabMCPAudit(t, h.dbPath, "collab_batch_wait")
	if !strings.Contains(waitAudit, `"argument_keys":["after_cursor","batch_id","timeout_seconds"]`) ||
		strings.Contains(waitAudit, submitted.BatchID) || strings.Contains(waitAudit, `"results"`) {
		t.Fatalf("wait audit shape=%s", waitAudit)
	}

	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-001", "title": "Again", "assignment": "duplicate",
		}},
		"integration_instructions": "integrate",
	}); call.Status != http.StatusOK || !call.IsError ||
		!strings.Contains(call.Text, app.CollaborationAssignmentDuplicate) {
		t.Fatalf("duplicate submit payload=%#v", call)
	}

	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-002", "title": "Unknown field", "assignment": "work", "model_id": "model-2",
		}},
		"integration_instructions": "integrate",
	}); call.Status != http.StatusOK || !call.IsError ||
		!strings.Contains(call.Text, "invalid_arguments") {
		t.Fatalf("unknown field payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{}, "integration_instructions": "integrate",
	}); !call.IsError || !strings.Contains(call.Text, "collaboration_input_invalid") {
		t.Fatalf("empty actions payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions":                  []map[string]any{{"agent_id": "sub-002", "title": "Missing assignment"}},
		"integration_instructions": "integrate",
	}); !call.IsError || !strings.Contains(call.Text, "collaboration_input_invalid") {
		t.Fatalf("missing assignment payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{"agent_id": "sub-002", "title": "No instructions", "assignment": "work"}},
	}); !call.IsError || !strings.Contains(call.Text, "collaboration_input_invalid") {
		t.Fatalf("missing instructions payload=%#v", call)
	}
	tooMany := make([]map[string]any, 0, 9)
	for index := 0; index < 9; index++ {
		tooMany = append(tooMany, map[string]any{
			"agent_id": "sub-00" + string(rune('1'+index)), "title": "Worker", "assignment": "work",
		})
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": tooMany, "integration_instructions": "integrate",
	}); !call.IsError || !strings.Contains(call.Text, "collaboration_input_invalid") {
		t.Fatalf("oversized actions payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_submit", map[string]any{
		"actions": []any{"not-an-object"}, "integration_instructions": "integrate",
	}); !call.IsError || !strings.Contains(call.Text, "invalid_arguments") {
		t.Fatalf("malformed action payload=%#v", call)
	}

	capped := collabMCPTask(t, h, "auto", 1)
	cappedTurn := createCoreTurn(t, h.database, capped, "main", "batch-collab-capped")
	cappedToken := h.issue(capped.ID, "main", cappedTurn.ID)
	if call := coreMCPCallTool(t, url, cappedToken, "collab_batch_submit", map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-001", "title": "Capped", "assignment": "work",
		}},
		"integration_instructions": "integrate",
	}); !call.IsError || !strings.Contains(call.Text, "collaboration_no_agents_queued") {
		t.Fatalf("max agents payload=%#v", call)
	}

	if call := coreMCPCallTool(t, url, token, "unknown_tool", map[string]any{}); call.Code != -32602 {
		t.Fatalf("unknown tool payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
		"batch_id": submitted.BatchID, "after_cursor": -1,
	}); !call.IsError || !strings.Contains(call.Text, "invalid_arguments") {
		t.Fatalf("negative cursor payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
		"batch_id": submitted.BatchID, "timeout_seconds": 31,
	}); !call.IsError || !strings.Contains(call.Text, "invalid_arguments") {
		t.Fatalf("oversized timeout payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
		"batch_id": "  ",
	}); !call.IsError || !strings.Contains(call.Text, "invalid_arguments") {
		t.Fatalf("empty batch id payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
		"batch_id": submitted.BatchID, "results": []any{},
	}); !call.IsError || !strings.Contains(call.Text, "invalid_arguments") {
		t.Fatalf("unknown wait field payload=%#v", call)
	}
}

// TestAgentCollabMCPWaitScopesBatches covers an unknown id, another Task's
// batch and an owned batch that never completes, and compares the MCP and HTTP
// classification for the same identifiers.
func TestAgentCollabMCPWaitScopesBatches(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := collabMCPTask(t, h, "auto", 4)
	turn := createCoreTurn(t, h.database, task, "main", "batch-collab-wait")
	token := h.issue(task.ID, "main", turn.ID)
	url := collabMCPURL(h)
	now := time.Now().UTC()
	for _, batch := range []domain.CollaborationBatch{
		{ID: "batch-collab-foreign", TaskID: "task-foreign", RoundID: "round-foreign", ParentTurnID: "turn-foreign"},
		{ID: "batch-collab-unknown", TaskID: "task-unknown", RoundID: "round-unknown", ParentTurnID: "turn-unknown"},
	} {
		batch.Status = domain.CollaborationBatchPending
		batch.CreatedAt, batch.UpdatedAt = now, now
		if err := h.database.CreateCollaborationBatch(h.ctx, batch, []domain.CollaborationMember{{
			BatchID: batch.ID, AgentID: "sub-001", Required: true,
			Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, batchID := range []string{"batch-collab-foreign", "batch-collab-unknown"} {
		if call := coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
			"batch_id": batchID, "timeout_seconds": 1,
		}); call.Status != http.StatusForbidden {
			t.Fatalf("%s MCP wait payload=%#v", batchID, call)
		}
		response := agentRequest(t, h.server.URL+"/api/v1/agent/collaboration/batches/"+batchID+"/wait",
			http.MethodPost, token, map[string]any{"after_cursor": 0, "timeout_seconds": 1})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s HTTP wait status=%d body=%s", batchID, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}

	if err := h.database.CreateCollaborationBatch(h.ctx, domain.CollaborationBatch{
		ID: "batch-collab-pending", TaskID: task.ID, RoundID: turn.RoundID, ParentTurnID: turn.ID,
		Status: domain.CollaborationBatchPending, CreatedAt: now, UpdatedAt: now,
	}, []domain.CollaborationMember{{
		BatchID: "batch-collab-pending", AgentID: "sub-001", Required: true,
		Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	call := coreMCPCallTool(t, url, token, "collab_batch_wait", map[string]any{
		"batch_id": "batch-collab-pending", "timeout_seconds": 1,
	})
	if call.Status != http.StatusOK || call.IsError {
		t.Fatalf("pending wait payload=%#v", call)
	}
	var progress struct {
		BatchID  string `json:"batch_id"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal([]byte(call.Text), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.Complete {
		t.Fatalf("pending batch reported complete: %s", call.Text)
	}
	if time.Since(started) < 900*time.Millisecond {
		t.Fatalf("wait returned before its timeout: %s", time.Since(started))
	}
	if progress.BatchID != "batch-collab-pending" {
		t.Fatalf("pending batch id=%q", progress.BatchID)
	}
}

// TestAgentCollabMCPRejectsChannelBoundTurns covers the shared three-state
// channel determination: a mixed plain+channel batch is denied in either order,
// a malformed payload fails closed, and an active task route denies even a
// plain batch.
func TestAgentCollabMCPRejectsChannelBoundTurns(t *testing.T) {
	h := newCoreMCPHarness(t)
	url := collabMCPURL(h)
	submit := map[string]any{
		"actions": []map[string]any{{
			"agent_id": "sub-002", "title": "Channel", "assignment": "work",
		}},
		"integration_instructions": "integrate",
	}
	newMainTurn := func(batchID string) (domain.Task, domain.Turn, string) {
		t.Helper()
		task := collabMCPTask(t, h, "auto", 4)
		turn := createCoreTurn(t, h.database, task, "main", batchID)
		return task, turn, h.issue(task.ID, "main", turn.ID)
	}

	for _, order := range []struct {
		name  string
		kinds []string
	}{
		{name: "plain-first", kinds: []string{"plain", "channel:task_route"}},
		{name: "channel-first", kinds: []string{"channel:task_route", "plain"}},
	} {
		task, turn, token := newMainTurn("batch-collab-channel-" + order.name)
		seedKnowledgeMCPInbox(t, h.dbPath, task, turn, order.kinds...)
		if call := coreMCPCallTool(t, url, token, "collab_batch_submit", submit); call.Status != http.StatusForbidden {
			t.Fatalf("%s mixed batch payload=%#v", order.name, call)
		}
		response := agentRequest(t, url, http.MethodPost, token, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
		})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s initialize status=%d body=%s", order.name, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}

	malformedTask, malformed, malformedToken := newMainTurn("batch-collab-channel-malformed")
	seedKnowledgeMCPInbox(t, h.dbPath, malformedTask, malformed, "raw:not-json")
	if call := coreMCPCallTool(t, url, malformedToken, "collab_batch_submit", submit); call.Status != http.StatusForbidden {
		t.Fatalf("malformed batch payload=%#v", call)
	}

	plainTask, plain, plainToken := newMainTurn("batch-collab-channel-plain")
	seedKnowledgeMCPInbox(t, h.dbPath, plainTask, plain, "plain")
	raw, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(h.ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	instance := "collab-mcp-" + plainTask.ID
	for _, statement := range []string{
		`INSERT INTO channel_endpoints(id,instance_id,kind,enabled,config_json,created_at,updated_at) VALUES('` + instance + `-endpoint','` + instance + `','group_digital_human',1,'{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_conversations(id,instance_id,endpoint_id,scope_key_version,scope_key,host_task_id,status,created_at,updated_at) VALUES('` + instance + `-conversation','` + instance + `','` + instance + `-endpoint',1,'` + instance + `-scope','` + plainTask.ID + `','active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_task_routes(id,instance_id,conversation_id,target_task_id,state,revision,activated_at,exited_at,exit_reason) VALUES('` + instance + `-route','` + instance + `','` + instance + `-conversation','` + plainTask.ID + `','active',1,'2026-01-01T00:00:00Z','','')`,
	} {
		if _, err := raw.ExecContext(h.ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if call := coreMCPCallTool(t, url, plainToken, "collab_batch_submit", submit); call.Status != http.StatusForbidden {
		t.Fatalf("active route payload=%#v", call)
	}
}

func waitForCollabMCPAudit(t *testing.T, path, tool string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var data string
		err := db.QueryRow(
			`SELECT data_json FROM audit_events WHERE action='agent.collab_mcp.call' AND data_json LIKE ? ORDER BY sequence DESC LIMIT 1`,
			`%"tool":"`+tool+`"%`,
		).Scan(&data)
		if err == nil && data != "" {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no collab MCP audit row: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
