package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type coreMCPHarness struct {
	ctx      context.Context
	dbPath   string
	database *store.Store
	service  *app.Service
	caps     *agentapi.Capabilities
	server   *httptest.Server
}

func newCoreMCPHarness(t *testing.T) *coreMCPHarness {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service := app.NewService(database, nil, app.StubExecutor{})
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, App: service, AgentCapabilities: capabilities,
	}).Handler())
	t.Cleanup(server.Close)
	return &coreMCPHarness{ctx: ctx, dbPath: path, database: database, service: service, caps: capabilities, server: server}
}

func (h *coreMCPHarness) issue(taskID, agentID, turnID string) string {
	token, err := h.caps.Issue(taskID, agentID, turnID, time.Hour)
	if err != nil {
		panic(err)
	}
	return token
}

func createCoreTurn(t *testing.T, database *store.Store, task domain.Task, agentID, batchID string) domain.Turn {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if agentID != "main" {
		if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
			TaskID: task.ID, AgentID: agentID, Role: "sub", Status: "idle", Title: agentID,
			RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InheritMain: true, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "start core turn", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: agentID,
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID:            batchID, Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	return turn
}

func createCoreTaskInProject(t *testing.T, h *coreMCPHarness, base domain.Task, filesystem string) domain.Task {
	t.Helper()
	snapshot, err := h.database.RuntimeSnapshot(h.ctx, base.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{
		ProjectID: base.ProjectID, WorkspaceID: base.WorkspaceID,
		Title: "Core peer", Request: "peer request", ModelID: snapshot.ModelID,
		Filesystem: filesystem, Approval: "never", CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

type coreMCPToolCall struct {
	Status  int
	IsError bool
	Text    string
	Code    int
	Message string
}

func coreMCPCallTool(t *testing.T, url, token, name string, arguments any) coreMCPToolCall {
	t.Helper()
	response := agentRequest(t, url, http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	var payload struct {
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	decodeResponse(t, response, &payload)
	call := coreMCPToolCall{Status: response.StatusCode}
	if payload.Result != nil {
		call.IsError = payload.Result.IsError
		if len(payload.Result.Content) > 0 {
			call.Text = payload.Result.Content[0].Text
		}
	}
	if len(payload.Error) > 0 {
		var rpcError struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(payload.Error, &rpcError) == nil {
			call.Code, call.Message = rpcError.Code, rpcError.Message
		}
	}
	return call
}

func coreMCPToolsList(t *testing.T, url, token string) (int, []string, map[string]map[string]any) {
	t.Helper()
	response := agentRequest(t, url, http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/list",
	})
	var payload struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if response.StatusCode == http.StatusOK {
		decodeResponse(t, response, &payload)
		names := make([]string, 0, len(payload.Result.Tools))
		schemas := make(map[string]map[string]any, len(payload.Result.Tools))
		for _, tool := range payload.Result.Tools {
			names = append(names, tool.Name)
			schemas[tool.Name] = tool.InputSchema
		}
		return response.StatusCode, names, schemas
	}
	response.Body.Close()
	return response.StatusCode, nil, nil
}

func TestAgentCoreMCPRequiresCapabilityAndRunningTurn(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-core-auth")
	token := h.issue(task.ID, "main", turn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

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
	if response.StatusCode != http.StatusOK || initialized.Result.ServerInfo.Name != "aha2-core" {
		t.Fatalf("initialize status=%d payload=%#v", response.StatusCode, initialized)
	}

	status, names, schemas := coreMCPToolsList(t, url, token)
	want := app.CoreMCPToolNames(true)
	if status != http.StatusOK || len(names) != len(want) {
		t.Fatalf("tools/list status=%d names=%#v want=%#v", status, names, want)
	}
	for _, name := range want {
		schema, ok := schemas[name]
		if !ok || schema["additionalProperties"] != false {
			t.Fatalf("tool %s schema=%#v", name, schema)
		}
	}

	turn.Status = domain.TurnSucceeded
	turn.FinishedAt = time.Now().UTC()
	if err := h.database.UpdateTurn(h.ctx, turn, domain.TurnRunning); err != nil {
		t.Fatal(err)
	}
	status, _, _ = coreMCPToolsList(t, url, token)
	if status != http.StatusConflict {
		t.Fatalf("finished turn status=%d want 409", status)
	}
}

func TestAgentCoreMCPRejectsForwardedHeadersAndOversizedBody(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-core-transport")
	token := h.issue(task.ID, "main", turn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("forwarded status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	oversized := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":"` +
		strings.Repeat("x", int(agentCoreMCPMaxRequestBytes)+1024) + `"}`)
	response = agentRequestRaw(t, url, http.MethodPost, token, oversized)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

func TestAgentCoreMCPRequestBoundaryAndTrailingData(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-core-body")
	token := h.issue(task.ID, "main", turn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"
	valid := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)

	exact := append(append([]byte{}, valid...),
		bytes.Repeat([]byte(" "), int(agentCoreMCPMaxRequestBytes)-len(valid))...)
	response := agentRequestRaw(t, url, http.MethodPost, token, exact)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("exact limit status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	over := append(append([]byte{}, exact...), ' ')
	response = agentRequestRaw(t, url, http.MethodPost, token, over)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("one byte over limit status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	for name, body := range map[string][]byte{
		"second value":        []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}{"jsonrpc":"2.0","id":2,"method":"ping"}`),
		"trailing garbage":    []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}x`),
		"empty body":          {},
		"whitespace only":     []byte("  \n\t"),
		"truncated json":      []byte(`{"jsonrpc":"2.0"`),
		"null body":           []byte(`null`),
		"array body":          []byte(`[]`),
		"top-level primitive": []byte(`42`),
	} {
		response = agentRequestRaw(t, url, http.MethodPost, token, body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", name, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}

	chunked := coreMCPChunkedRequest(t, h.server.URL, token, valid)
	if chunked.StatusCode != http.StatusOK {
		t.Fatalf("chunked body status=%d body=%s", chunked.StatusCode, readBody(t, chunked))
	}
	chunked.Body.Close()
}

func coreMCPChunkedRequest(t *testing.T, serverURL, token string, body []byte) *http.Response {
	t.Helper()
	host := strings.TrimPrefix(serverURL, "http://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	path := "/api/v1/agent/core/mcp"
	if _, err := fmt.Fprintf(conn,
		"POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n"+
			"Content-Type: application/json\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n",
		path, host, token,
	); err != nil {
		t.Fatal(err)
	}
	half := len(body) / 2
	for _, chunk := range [][]byte{body[:half], body[half:]} {
		if _, err := fmt.Fprintf(conn, "%x\r\n", len(chunk)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte("\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Write([]byte("0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestAgentCoreMCPReadOnlyAndNonMainWriteRefusal(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	baseTurn := createCoreTurn(t, h.database, base, "main", "batch-core-main")
	baseToken := h.issue(base.ID, "main", baseTurn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

	sub := createCoreTaskInProject(t, h, base, "")
	subTurn := createCoreTurn(t, h.database, sub, "reviewer", "batch-core-sub")
	subToken := h.issue(sub.ID, "reviewer", subTurn.ID)
	status, names, _ := coreMCPToolsList(t, url, subToken)
	if status != http.StatusOK || len(names) != len(app.CoreMCPToolNames(false)) {
		t.Fatalf("sub tools status=%d names=%#v", status, names)
	}
	if call := coreMCPCallTool(t, url, subToken, "progress", map[string]any{"message": "nope"}); call.Status != http.StatusForbidden {
		t.Fatalf("sub progress status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, subToken, "memory_patch", map[string]any{"facts": []string{"nope"}}); call.Status != http.StatusForbidden {
		t.Fatalf("sub memory status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, subToken, "task_get", map[string]any{}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("sub task_get status=%d payload=%#v", call.Status, call)
	}

	readOnly := createCoreTaskInProject(t, h, base, "read-only")
	readOnlyTurn := createCoreTurn(t, h.database, readOnly, "main", "batch-core-readonly")
	readOnlyToken := h.issue(readOnly.ID, "main", readOnlyTurn.ID)
	status, names, _ = coreMCPToolsList(t, url, readOnlyToken)
	if status != http.StatusOK || len(names) != len(app.CoreMCPToolNames(false)) {
		t.Fatalf("read-only tools status=%d names=%#v", status, names)
	}
	if call := coreMCPCallTool(t, url, readOnlyToken, "progress", map[string]any{"message": "nope"}); call.Status != http.StatusForbidden {
		t.Fatalf("read-only progress status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, readOnlyToken, "task_list", map[string]any{"limit": 5}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("read-only task_list status=%d payload=%#v", call.Status, call)
	}

	// Keep the writable Main fixture alive for the negative assertions above;
	// it must still see the full tool set.
	status, names, _ = coreMCPToolsList(t, url, baseToken)
	if status != http.StatusOK || len(names) != len(app.CoreMCPToolNames(true)) {
		t.Fatalf("main tools status=%d names=%#v", status, names)
	}
}

func TestAgentCoreMCPPermissionSnapshotFailClosed(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	url := h.server.URL + "/api/v1/agent/core/mcp"
	scenarios := []struct {
		name        string
		permissions string
		writable    bool
	}{
		{name: "workspace write", permissions: `{"filesystem":"workspace-write","approval":"never"}`, writable: true},
		{name: "danger full access", permissions: `{"approval":"auto","filesystem":"danger-full-access"}`, writable: true},
		{name: "read only", permissions: `{"filesystem":"read-only","approval":"never"}`},
		{name: "invalid json", permissions: `{"filesystem":`},
		{name: "null document", permissions: `null`},
		{name: "wrong type", permissions: `{"filesystem":1,"approval":"never"}`},
		{name: "missing filesystem", permissions: `{"approval":"never"}`},
		{name: "unknown filesystem", permissions: `{"filesystem":"readonly","approval":"never"}`},
		{name: "case variant", permissions: `{"filesystem":"Read-Only","approval":"never"}`},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			task := createCoreTaskInProject(t, h, base, "")
			turn := createCoreTurn(t, h.database, task, "main", domain.NewID("batch"))
			setCoreSnapshotPermissions(t, h.dbPath, task.RuntimeConfigSnapshotID, scenario.permissions)
			token := h.issue(task.ID, "main", turn.ID)

			wantTools := len(app.CoreMCPToolNames(false))
			if scenario.writable {
				wantTools = len(app.CoreMCPToolNames(true))
			}
			status, names, _ := coreMCPToolsList(t, url, token)
			if status != http.StatusOK || len(names) != wantTools {
				t.Fatalf("tools/list status=%d names=%#v want=%d", status, names, wantTools)
			}
			call := coreMCPCallTool(t, url, token, "progress", map[string]any{"message": "permission probe"})
			if scenario.writable {
				if call.Status != http.StatusOK || call.IsError {
					t.Fatalf("writable snapshot progress status=%d payload=%#v", call.Status, call)
				}
			} else if call.Status != http.StatusForbidden {
				t.Fatalf("abnormal snapshot progress status=%d payload=%#v", call.Status, call)
			}
		})
	}
}

func TestAgentCoreMCPTaskScopeAndPeerMessageSource(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	baseTurn := createCoreTurn(t, h.database, base, "main", "batch-core-scope")
	token := h.issue(base.ID, "main", baseTurn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

	peer := createCoreTaskInProject(t, h, base, "")
	snapshot, err := h.database.RuntimeSnapshot(h.ctx, base.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", CreatedAt: now, UpdatedAt: now}
	otherWorkspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: otherProject.ID, Name: "Other",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.database.CreateProject(h.ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	if err := h.database.CreateWorkspace(h.ctx, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	foreign, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{
		ProjectID: otherProject.ID, WorkspaceID: otherWorkspace.ID, Title: "Foreign", Request: "foreign",
		ModelID: snapshot.ModelID, CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if call := coreMCPCallTool(t, url, token, "task_get", map[string]any{"task_id": peer.ID}); call.Status != http.StatusOK || call.IsError || !strings.Contains(call.Text, peer.ID) {
		t.Fatalf("same project task_get status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, token, "task_get", map[string]any{"task_id": foreign.ID}); call.Status != http.StatusForbidden {
		t.Fatalf("cross project task_get status=%d payload=%#v", call.Status, call)
	}
	// A capability from another Task cannot read this Task either. The project
	// scope check must hold in both directions, not only for the caller's reads.
	foreignTurn := createCoreTurn(t, h.database, foreign, "main", "batch-core-foreign")
	foreignToken := h.issue(foreign.ID, "main", foreignTurn.ID)
	if call := coreMCPCallTool(t, url, foreignToken, "task_get", map[string]any{"task_id": base.ID}); call.Status != http.StatusForbidden {
		t.Fatalf("foreign token task_get status=%d payload=%#v", call.Status, call)
	}
	list := coreMCPCallTool(t, url, token, "task_list", map[string]any{"limit": 50})
	if list.Status != http.StatusOK || list.IsError {
		t.Fatalf("task_list status=%d payload=%#v", list.Status, list)
	}
	if strings.Contains(list.Text, foreign.ID) || !strings.Contains(list.Text, peer.ID) {
		t.Fatalf("task_list leaked or missed tasks: %s", list.Text)
	}

	sent := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": peer.ID, "content": "hello peer",
	})
	if sent.Status != http.StatusOK || sent.IsError || !strings.Contains(sent.Text, peer.ID) {
		t.Fatalf("task_message status=%d payload=%#v", sent.Status, sent)
	}
	waitForTaskStatus(t, h.database, peer.ID, domain.TaskWaitingUser)
	page, err := h.database.ConversationPageForAgent(h.ctx, peer.ID, "main", 0, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.RouteKind == "peer_task_message" {
			if item.FromAgentID != base.ID || item.Summary != "hello peer" {
				t.Fatalf("peer message item=%#v", item)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("peer task conversation has no peer_task_message item: %#v", page.Items)
	}

	if call := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": base.ID, "content": "self",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("self task_message status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": foreign.ID, "content": "cross",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("cross project task_message status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": peer.ID, "content": "strict", "attachment_ids": []string{"a"},
	}); !call.IsError || call.Status != http.StatusOK {
		t.Fatalf("unknown argument status=%d payload=%#v", call.Status, call)
	}
}

func TestAgentCoreMCPPeerMessageLiveDelivery(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	baseTurn := createCoreTurn(t, h.database, base, "main", "batch-core-peer-source")
	token := h.issue(base.ID, "main", baseTurn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

	peer := createCoreTaskInProject(t, h, base, "")
	peerTurn := createCoreTurn(t, h.database, peer, "main", "batch-core-peer-target")
	peerToken := h.issue(peer.ID, "main", peerTurn.ID)

	if sent := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": peer.ID, "content": "peer-live-first",
	}); sent.Status != http.StatusOK || sent.IsError {
		t.Fatalf("send first peer message status=%d payload=%#v", sent.Status, sent)
	}
	// A Task in another Project must not receive a peer message.
	foreign := createHardwareAPITask(t, h.database)
	if refused := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": foreign.ID, "content": "cross-project",
	}); refused.Status != http.StatusForbidden {
		t.Fatalf("cross-project peer message status=%d payload=%#v", refused.Status, refused)
	}

	// The live-injection path used by DSH/Claude tool boundaries must claim the
	// peer message for the already running target Turn.
	response := agentRequest(t, h.server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, peerToken, map[string]any{
		"limit": 10,
	})
	var claimed struct {
		Messages   []domain.LiveInboxMessage `json:"messages"`
		NextCursor int64                     `json:"next_cursor"`
	}
	decodeResponse(t, response, &claimed)
	if response.StatusCode != http.StatusOK || len(claimed.Messages) != 1 {
		t.Fatalf("live claim status=%d payload=%#v", response.StatusCode, claimed)
	}
	live := claimed.Messages[0]
	if live.Content != "peer-live-first" || live.SourceKind != "peer_task_message" ||
		live.Source.Kind != "task" || live.Source.Authority != "task_peer" || live.Source.AgentID != base.ID {
		t.Fatalf("live peer message source=%#v payload=%#v", live.Source, live)
	}
	if claimed.NextCursor != live.Sequence {
		t.Fatalf("live claim cursor=%d message sequence=%d", claimed.NextCursor, live.Sequence)
	}

	if sent := coreMCPCallTool(t, url, token, "task_message", map[string]any{
		"target_task_id": peer.ID, "content": "peer-live-second",
	}); sent.Status != http.StatusOK || sent.IsError {
		t.Fatalf("send second peer message status=%d payload=%#v", sent.Status, sent)
	}
	now := time.Now().UTC()
	if _, _, _, err := h.database.EnqueueOwnerMessage(h.ctx, domain.Message{
		ID: domain.NewID("message"), TaskID: peer.ID, Role: "user", Sender: "owner",
		Content: "owner-live-control", CreatedAt: now,
	}, "main", nil); err != nil {
		t.Fatal(err)
	}

	// inbox/wait is the model-driven counterpart. It must see the pending peer
	// message as well as the owner message, keep sequence order, honour the
	// cursor from the live claim, and not re-deliver already claimed messages.
	response = agentRequest(t, h.server.URL+"/api/v1/agent/turn/inbox/wait", http.MethodPost, peerToken, map[string]any{
		"after_cursor": claimed.NextCursor, "timeout_seconds": 1,
	})
	var waited struct {
		Messages   []domain.LiveInboxMessage `json:"messages"`
		NextCursor int64                     `json:"next_cursor"`
	}
	decodeResponse(t, response, &waited)
	if response.StatusCode != http.StatusOK || len(waited.Messages) != 2 {
		t.Fatalf("inbox_wait status=%d payload=%#v", response.StatusCode, waited)
	}
	peerMessage, ownerMessage := waited.Messages[0], waited.Messages[1]
	if peerMessage.Content != "peer-live-second" || peerMessage.Source.Kind != "task" ||
		peerMessage.Source.Authority != "task_peer" || peerMessage.Source.AgentID != base.ID {
		t.Fatalf("waited peer message=%#v", peerMessage)
	}
	if ownerMessage.Content != "owner-live-control" || ownerMessage.Source.Kind != "owner" ||
		ownerMessage.Source.Authority != "owner" {
		t.Fatalf("waited owner message=%#v", ownerMessage)
	}
	if !(peerMessage.Sequence < ownerMessage.Sequence) {
		t.Fatalf("inbox_wait did not preserve sequence order: %#v", waited.Messages)
	}

	response = agentRequest(t, h.server.URL+"/api/v1/agent/turn/inbox/wait", http.MethodPost, peerToken, map[string]any{
		"after_cursor": waited.NextCursor, "timeout_seconds": 1,
	})
	decodeResponse(t, response, &waited)
	if response.StatusCode != http.StatusOK || len(waited.Messages) != 0 {
		t.Fatalf("duplicate inbox_wait status=%d payload=%#v", response.StatusCode, waited)
	}
}

func TestAgentCoreMCPAttachmentBinding(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-core-attach")
	token := h.issue(task.ID, "main", turn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

	response := agentMultipartRequest(t, h.server.URL+"/api/v1/agent/turn/attachments", token, "note.txt", []byte("attached body"))
	var uploaded struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	decodeResponse(t, response, &uploaded)
	if response.StatusCode != http.StatusCreated || uploaded.Attachment.ID == "" {
		t.Fatalf("upload status=%d payload=%#v", response.StatusCode, uploaded)
	}

	bound := coreMCPCallTool(t, url, token, "attachment_bind", map[string]any{
		"attachment_ids": []string{uploaded.Attachment.ID}, "message": "see attachment",
	})
	if bound.Status != http.StatusOK || bound.IsError || !strings.Contains(bound.Text, uploaded.Attachment.ID) {
		t.Fatalf("attachment_bind status=%d payload=%#v", bound.Status, bound)
	}
	item, err := h.database.Attachment(h.ctx, task.ID, uploaded.Attachment.ID)
	if err != nil || item.MessageID == "" {
		t.Fatalf("attachment not bound: item=%#v err=%v", item, err)
	}
	if repeated := coreMCPCallTool(t, url, token, "attachment_bind", map[string]any{
		"attachment_ids": []string{uploaded.Attachment.ID},
	}); !repeated.IsError {
		t.Fatalf("double bind status=%d payload=%#v", repeated.Status, repeated)
	}
	if missing := coreMCPCallTool(t, url, token, "attachment_bind", map[string]any{
		"attachment_ids": []string{"attachment-missing"},
	}); !missing.IsError {
		t.Fatalf("missing attachment status=%d payload=%#v", missing.Status, missing)
	}
}

func TestAgentCoreMCPMemoryAndProgressMatchHTTP(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-core-equivalence")
	token := h.issue(task.ID, "main", turn.ID)
	url := h.server.URL + "/api/v1/agent/core/mcp"

	if call := coreMCPCallTool(t, url, token, "memory_patch", map[string]any{
		"decisions": []string{"mcp-decision"}, "facts": []string{"do-not-log-this-value"},
	}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("mcp memory status=%d payload=%#v", call.Status, call)
	}
	response := agentRequest(t, h.server.URL+"/api/v1/agent/turn/memory", http.MethodPatch, token, map[string]any{
		"append": map[string]any{"facts": []string{"http-fact"}},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("http memory status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	memory, err := h.database.TaskMemory(h.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(memory.Decisions, "mcp-decision") || !containsString(memory.Facts, "http-fact") {
		t.Fatalf("memory=%#v", memory)
	}

	if call := coreMCPCallTool(t, url, token, "progress", map[string]any{"message": "mcp-progress"}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("mcp progress status=%d payload=%#v", call.Status, call)
	}
	response = agentRequest(t, h.server.URL+"/api/v1/agent/turn/messages", http.MethodPost, token, map[string]any{
		"message": "http-progress",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("http progress status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	page, err := h.database.ConversationPageForAgent(h.ctx, task.ID, "main", 0, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mcpFound, httpFound bool
	for _, item := range page.Items {
		if item.Summary == "mcp-progress" {
			mcpFound = true
		}
		if item.Summary == "http-progress" {
			httpFound = true
		}
	}
	if !mcpFound || !httpFound {
		t.Fatalf("progress items not equivalent: %#v", page.Items)
	}

	// The audit row records the tool and keys only. The memory value itself must
	// never appear in audit data.
	auditJSON := waitForCoreAudit(t, h.dbPath, "memory_patch")
	if strings.Contains(auditJSON, "do-not-log-this-value") || strings.Contains(auditJSON, token) {
		t.Fatalf("audit leaked a value or token: %s", auditJSON)
	}
	var audit map[string]any
	if err := json.Unmarshal([]byte(auditJSON), &audit); err != nil {
		t.Fatal(err)
	}
	if audit["tool"] != "memory_patch" || audit["turn_id"] != turn.ID || audit["result"] != "ok" {
		t.Fatalf("audit fields=%#v", audit)
	}
	if _, ok := audit["duration_ms"]; !ok {
		t.Fatalf("audit missing duration: %#v", audit)
	}
	keys, _ := audit["argument_keys"].([]any)
	if len(keys) != 2 || keys[0] != "decisions" || keys[1] != "facts" {
		t.Fatalf("audit keys=%#v", audit)
	}
}

func setCoreSnapshotPermissions(t *testing.T, path, snapshotID, permissions string) {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE runtime_config_snapshots SET permissions_json=? WHERE id=?`,
		permissions, snapshotID,
	); err != nil {
		t.Fatal(err)
	}
}

func waitForCoreAudit(t *testing.T, path, tool string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var data string
		err := db.QueryRowContext(context.Background(),
			`SELECT data_json FROM audit_events WHERE action='agent.core_mcp.call' AND data_json LIKE ? ORDER BY sequence DESC LIMIT 1`,
			`%"tool":"`+tool+`"%`,
		).Scan(&data)
		if err == nil && data != "" {
			var leaked int
			if scanErr := db.QueryRowContext(context.Background(),
				`SELECT COUNT(*) FROM audit_events WHERE data_json LIKE '%do-not-log-this-value%'`,
			).Scan(&leaked); scanErr != nil {
				t.Fatal(scanErr)
			}
			if leaked != 0 {
				t.Fatalf("a value leaked into %d audit rows", leaked)
			}
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no core MCP audit row: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
