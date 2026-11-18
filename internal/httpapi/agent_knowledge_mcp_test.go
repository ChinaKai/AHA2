package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func seedKnowledgeForMCP(t *testing.T, h *coreMCPHarness, scope, projectID, id, title, body string) domain.KnowledgeEntry {
	t.Helper()
	root, err := h.database.EnsureKnowledgeRoot(h.ctx, scope, projectID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entry := domain.KnowledgeEntry{
		ID: id, Scope: scope, ProjectID: projectID, ParentID: root.ID,
		Slug: strings.ReplaceAll(id, "_", "-"), Type: "practice", Title: title, Body: body,
		Status: domain.KnowledgeVerified, Revision: 1,
		CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if err := h.database.CreateKnowledge(h.ctx, entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func knowledgeMCPURL(h *coreMCPHarness) string {
	return h.server.URL + "/api/v1/agent/knowledge/mcp"
}

func waitForKnowledgeMCPAudit(t *testing.T, path, tool string) string {
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
			`SELECT data_json FROM audit_events WHERE action='agent.knowledge_mcp.call' AND data_json LIKE ? ORDER BY sequence DESC LIMIT 1`,
			`%"tool":"`+tool+`"%`,
		).Scan(&data)
		if err == nil && data != "" {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no knowledge MCP audit row: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-auth")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	response := agentRequest(t, url, http.MethodPost, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, "not-a-real-token", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid capability status=%d body=%s", response.StatusCode, readBody(t, response))
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
	if response.StatusCode != http.StatusOK || initialized.Result.ServerInfo.Name != "aha2-knowledge" {
		t.Fatalf("initialize status=%d payload=%#v", response.StatusCode, initialized)
	}

	status, names, schemas := coreMCPToolsList(t, url, token)
	want := app.KnowledgeMCPToolNames(true)
	if status != http.StatusOK || len(names) != len(want) {
		t.Fatalf("tools/list status=%d names=%#v want=%#v", status, names, want)
	}
	for _, name := range want {
		schema, ok := schemas[name]
		if !ok || schema["additionalProperties"] != false {
			t.Fatalf("tool %s schema=%#v", name, schema)
		}
	}

	disabled, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{
		ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID, Title: "disabled knowledge",
		Request: "none", ModelID: taskRuntimeModelID(t, h, task), CollaborationMode: "single",
		MaxAgents: 1, KnowledgePolicy: "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledTurn := createCoreTurn(t, h.database, disabled, "main", "batch-knowledge-disabled")
	disabledToken := h.issue(disabled.ID, "main", disabledTurn.ID)
	status, _, _ = coreMCPToolsList(t, url, disabledToken)
	if status != http.StatusForbidden {
		t.Fatalf("knowledge disabled tools/list status=%d want 403", status)
	}
	if call := coreMCPCallTool(t, url, disabledToken, "knowledge_list", map[string]any{}); call.Status != http.StatusForbidden {
		t.Fatalf("knowledge disabled call status=%d payload=%#v", call.Status, call)
	}

	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Forwarded", "for=203.0.113.9")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("forwarded status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	oversized := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":"` +
		strings.Repeat("x", int(agentKnowledgeMCPMaxRequestBytes)+1024) + `"}`)
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

func taskRuntimeModelID(t *testing.T, h *coreMCPHarness, task domain.Task) string {
	t.Helper()
	snapshot, err := h.database.RuntimeSnapshot(h.ctx, task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.ModelID
}

func TestAgentKnowledgeMCPReadScopeAndHTTPEquivalence(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-read")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	current := seedKnowledgeForMCP(t, h, "project", task.ProjectID, "knowledge-current",
		"Current project practice", "Current project body lives only in this project and nowhere else.")
	global := seedKnowledgeForMCP(t, h, "global", "", "knowledge-global",
		"Global reference practice", "Global body is visible to every project and must be listed.")
	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := h.database.CreateProject(h.ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	foreign := seedKnowledgeForMCP(t, h, "project", otherProject.ID, "knowledge-foreign",
		"Foreign secret practice", "Foreign body must never appear in this Task's list or get.")

	list := coreMCPCallTool(t, url, token, "knowledge_list", map[string]any{"limit": 100})
	if list.Status != http.StatusOK || list.IsError {
		t.Fatalf("knowledge_list status=%d payload=%#v", list.Status, list)
	}
	if !strings.Contains(list.Text, current.ID) || !strings.Contains(list.Text, global.ID) {
		t.Fatalf("knowledge_list missing readable entries: %s", list.Text)
	}
	if strings.Contains(list.Text, foreign.ID) || strings.Contains(list.Text, "Foreign body") {
		t.Fatalf("knowledge_list leaked another project: %s", list.Text)
	}
	if strings.Contains(list.Text, "Current project body") {
		t.Fatalf("knowledge_list returned a body: %s", list.Text)
	}

	filtered := coreMCPCallTool(t, url, token, "knowledge_list", map[string]any{
		"query": "GLOBAL", "type": "practice", "limit": 1, "offset": 0,
	})
	if filtered.Status != http.StatusOK || filtered.IsError ||
		!strings.Contains(filtered.Text, global.ID) || strings.Contains(filtered.Text, current.ID) {
		t.Fatalf("filtered knowledge_list status=%d payload=%#v", filtered.Status, filtered)
	}

	got := coreMCPCallTool(t, url, token, "knowledge_get", map[string]any{"id": current.ID})
	if got.Status != http.StatusOK || got.IsError ||
		!strings.Contains(got.Text, `"source":"legacy"`) || !strings.Contains(got.Text, "Current project body") {
		t.Fatalf("knowledge_get status=%d payload=%#v", got.Status, got)
	}
	foreignGet := coreMCPCallTool(t, url, token, "knowledge_get", map[string]any{"id": foreign.ID})
	if foreignGet.Status != http.StatusOK || !foreignGet.IsError || !strings.Contains(foreignGet.Text, "knowledge_not_found") {
		t.Fatalf("cross-project knowledge_get status=%d payload=%#v", foreignGet.Status, foreignGet)
	}
	if strings.Contains(foreignGet.Text, "Foreign body") {
		t.Fatalf("cross-project error leaked a body: %s", foreignGet.Text)
	}

	guide := coreMCPCallTool(t, url, token, "knowledge_guide", map[string]any{})
	if guide.Status != http.StatusOK || guide.IsError || !strings.Contains(guide.Text, `"available":false`) {
		t.Fatalf("knowledge_guide status=%d payload=%#v", guide.Status, guide)
	}
	skills := coreMCPCallTool(t, url, token, "skill_list", map[string]any{})
	if skills.Status != http.StatusOK || skills.IsError {
		t.Fatalf("skill_list status=%d payload=%#v", skills.Status, skills)
	}
	missingSkill := coreMCPCallTool(t, url, token, "skill_get", map[string]any{"skill_id": "vault-skill:missing"})
	if missingSkill.Status != http.StatusOK || !missingSkill.IsError || !strings.Contains(missingSkill.Text, "skill_not_found") {
		t.Fatalf("missing skill_get payload=%#v", missingSkill)
	}

	// The HTTP knowledge API and the MCP list must agree on the readable range.
	response := agentRequest(t, h.server.URL+"/api/v1/agent/knowledge", http.MethodGet, token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("http knowledge status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	var httpPayload struct {
		Knowledge []domain.KnowledgeEntry `json:"knowledge"`
	}
	decodeResponse(t, response, &httpPayload)
	var httpIDs []string
	for _, item := range httpPayload.Knowledge {
		httpIDs = append(httpIDs, item.ID)
	}
	if !containsString(httpIDs, current.ID) || !containsString(httpIDs, global.ID) || containsString(httpIDs, foreign.ID) {
		t.Fatalf("http knowledge IDs=%#v", httpIDs)
	}

	// A non-Main Agent can read but never write.
	sub := createCoreTaskInProject(t, h, task, "")
	subTurn := createCoreTurn(t, h.database, sub, "reviewer", "batch-knowledge-sub")
	subToken := h.issue(sub.ID, "reviewer", subTurn.ID)
	status, names, _ := coreMCPToolsList(t, url, subToken)
	if status != http.StatusOK || len(names) != len(app.KnowledgeMCPToolNames(false)) {
		t.Fatalf("sub tools status=%d names=%#v", status, names)
	}
	if call := coreMCPCallTool(t, url, subToken, "knowledge_submit", map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title": "Sub write", "summary": "A sub agent may never write knowledge.",
		"body":         "This body is long enough to pass validation but the call must be denied.",
		"search_query": "sub",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("sub knowledge_submit status=%d payload=%#v", call.Status, call)
	}

	readOnly := createCoreTaskInProject(t, h, task, "read-only")
	readOnlyTurn := createCoreTurn(t, h.database, readOnly, "main", "batch-knowledge-readonly")
	readOnlyToken := h.issue(readOnly.ID, "main", readOnlyTurn.ID)
	status, names, _ = coreMCPToolsList(t, url, readOnlyToken)
	if status != http.StatusOK || len(names) != len(app.KnowledgeMCPToolNames(false)) {
		t.Fatalf("read-only tools status=%d names=%#v", status, names)
	}
	if call := coreMCPCallTool(t, url, readOnlyToken, "knowledge_submit", map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title": "Read only write", "summary": "A read-only task may never write knowledge.",
		"body":         "This body is long enough to pass validation but the call must be denied.",
		"search_query": "read-only",
	}); call.Status != http.StatusForbidden {
		t.Fatalf("read-only knowledge_submit status=%d payload=%#v", call.Status, call)
	}
	if call := coreMCPCallTool(t, url, readOnlyToken, "knowledge_list", map[string]any{}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("read-only knowledge_list payload=%#v", call)
	}
}

func TestAgentKnowledgeMCPWriteEquivalenceAndValidation(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-write")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)
	current := seedKnowledgeForMCP(t, h, "project", task.ProjectID, "knowledge-write-current",
		"Editable practice", "Editable body content long enough to pass any validation.")
	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := h.database.CreateProject(h.ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	foreign := seedKnowledgeForMCP(t, h, "project", otherProject.ID, "knowledge-write-foreign",
		"Foreign practice", "Foreign feedback must be refused without leaking its body.")

	created := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title": "MCP written practice", "summary": "A reusable MCP knowledge summary for this project.",
		"body":         "MCP written body content that is long enough to satisfy the knowledge contract.",
		"search_query": "mcp written practice",
	})
	if created.Status != http.StatusOK || created.IsError ||
		!strings.Contains(created.Text, `"source":"legacy"`) || !strings.Contains(created.Text, `"proposal":true`) {
		t.Fatalf("knowledge_submit create status=%d payload=%#v", created.Status, created)
	}
	auditJSON := waitForKnowledgeMCPAudit(t, h.dbPath, "knowledge_submit")
	if strings.Contains(auditJSON, "MCP written body") || strings.Contains(auditJSON, token) ||
		strings.Contains(auditJSON, "mcp written practice") {
		t.Fatalf("audit leaked content or token: %s", auditJSON)
	}
	var audit map[string]any
	if err := json.Unmarshal([]byte(auditJSON), &audit); err != nil {
		t.Fatal(err)
	}
	if audit["tool"] != "knowledge_submit" || audit["turn_id"] != turn.ID || audit["result"] != "ok" {
		t.Fatalf("audit fields=%#v", audit)
	}
	if _, ok := audit["duration_ms"]; !ok {
		t.Fatalf("audit missing duration: %#v", audit)
	}
	keys, _ := audit["argument_keys"].([]any)
	for _, key := range keys {
		switch key {
		case "mode", "scope", "type", "title", "summary", "body", "search_query":
		default:
			t.Fatalf("unexpected audited key %v in %#v", key, keys)
		}
	}
	if len(keys) == 0 {
		t.Fatalf("audit recorded no argument keys: %#v", audit)
	}

	if call := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title": "Missing search", "summary": "A create without a duplicate search must fail.",
		"body": "This body is long enough to satisfy the minimum length but search is missing.",
	}); call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, "knowledge_write_invalid") {
		t.Fatalf("missing search payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "create", "scope": "global", "type": "practice",
		"title": "Global write", "summary": "Global scope must never be writable through this MCP.",
		"body":         "This body is long enough to satisfy the minimum length and the scope must fail.",
		"search_query": "global",
	}); call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, "knowledge_scope_forbidden") {
		t.Fatalf("global scope payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title": "Unknown field", "summary": "Unknown fields must be rejected before business logic.",
		"body":         "This body is long enough to satisfy the minimum length but has an unknown field.",
		"search_query": "unknown", "project_id": otherProject.ID,
	}); call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, "knowledge_write_invalid") {
		t.Fatalf("unknown field payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "revise", "scope": "project", "entry_id": current.ID, "base_revision": 999,
		"type": "practice", "title": "Stale revision", "summary": "A stale base revision must be an explicit conflict.",
		"body": "This replacement body is long enough but the base revision is deliberately stale.",
	}); call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, "knowledge_revision_conflict") {
		t.Fatalf("stale revision payload=%#v", call)
	}

	feedback := coreMCPCallTool(t, url, token, "knowledge_feedback", map[string]any{
		"entry_id": current.ID, "kind": "helped",
	})
	if feedback.Status != http.StatusOK || feedback.IsError || !strings.Contains(feedback.Text, `"source":"legacy"`) {
		t.Fatalf("knowledge_feedback payload=%#v", feedback)
	}
	if call := coreMCPCallTool(t, url, token, "knowledge_feedback", map[string]any{
		"entry_id": current.ID, "kind": "published",
	}); call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, "knowledge_write_invalid") {
		t.Fatalf("illegal feedback kind payload=%#v", call)
	}
	if call := coreMCPCallTool(t, url, token, "knowledge_feedback", map[string]any{
		"entry_id": foreign.ID, "kind": "helped",
	}); call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, "knowledge_not_found") {
		t.Fatalf("cross-project feedback payload=%#v", call)
	}

	// HTTP equivalence: the same create and the same global-scope rejection.
	httpTask := createCoreTaskInProject(t, h, task, "")
	httpTurn := createCoreTurn(t, h.database, httpTask, "main", "batch-knowledge-http")
	httpToken := h.issue(httpTask.ID, "main", httpTurn.ID)
	response := agentRequest(t, h.server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, httpToken, map[string]any{
		"mode": "create", "scope": "project", "type": "practice",
		"title": "HTTP written practice", "summary": "An HTTP knowledge summary used for equivalence.",
		"body":         "HTTP written body content that is long enough to satisfy the knowledge contract.",
		"search_query": "http written practice",
	})
	var httpCreated struct {
		Source string `json:"source"`
	}
	decodeResponse(t, response, &httpCreated)
	if response.StatusCode != http.StatusCreated || httpCreated.Source != "legacy" {
		t.Fatalf("http create status=%d payload=%#v", response.StatusCode, httpCreated)
	}
	response = agentRequest(t, h.server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, httpToken, map[string]any{
		"mode": "revise", "scope": "project", "entry_id": current.ID, "base_revision": 999,
		"type": "practice", "title": "HTTP stale revision",
		"summary": "The same stale base revision must be an explicit conflict over HTTP.",
		"body":    "This replacement body is long enough but the base revision is deliberately stale.",
	})
	var httpScope struct {
		Error string `json:"error"`
	}
	decodeResponse(t, response, &httpScope)
	if response.StatusCode != http.StatusConflict || httpScope.Error != "knowledge_revision_conflict" {
		t.Fatalf("http stale revision status=%d payload=%#v", response.StatusCode, httpScope)
	}
}
