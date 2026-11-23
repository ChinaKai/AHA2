package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

const agentAttachmentMaterializeRoute = "/api/v1/agent/turn/attachments/materialize"

func seedAgentAttachmentMaterialize(t *testing.T, h *coreMCPHarness, task domain.Task, agentID, content string) (domain.Attachment, string, domain.Turn, string) {
	t.Helper()
	turn := createCoreTurn(t, h.database, task, agentID, domain.NewID("batch"))
	bound, err := h.database.Workspace(h.ctx, task.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	workDir := task.TaskWorkspacePath
	if workDir == "" {
		workDir = bound.RootPath
	}
	scratch, err := workspace.EnsureScratchDir(h.ctx, bound, workDir, task.ID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := h.database.CreateAttachment(h.ctx, task.ID, "same.zip", "application/zip", []byte(content), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return item, scratch, turn, h.issue(task.ID, agentID, turn.ID)
}

func TestAgentAttachmentMaterializeHTTPAndPetMCP(t *testing.T) {
	h := newCoreMCPHarness(t)
	ordinary := createHardwareAPITask(t, h.database)
	pet := createPetTaskInStore(t, h.database, ordinary)
	setPetSharedControlPermissions(t, h.database, false, false)
	const content = "test payload that must not be sent in the result or written in the audit log"
	digest := sha256.Sum256([]byte(content))
	item, scratch, _, token := seedAgentAttachmentMaterialize(t, h, ordinary, "collaborator", content)
	petItem, petScratch, _, petToken := seedAgentAttachmentMaterialize(t, h, pet, "main", content)
	message := domain.Message{ID: domain.NewID("message"), TaskID: pet.ID, Role: "user", Sender: "owner", Content: "diagnose this", CreatedAt: time.Now()}
	if _, _, _, err := h.database.EnqueueOwnerMessage(h.ctx, message, "main", []string{petItem.ID}); err != nil {
		t.Fatal(err)
	}
	verify := func(result app.AgentAttachmentMaterialization, directory string) {
		t.Helper()
		if result.Path == "" || filepath.Dir(result.Path) != directory || filepath.Base(result.Path) == "same.zip" || result.Size != int64(len(content)) || result.SHA256 != hex.EncodeToString(digest[:]) || result.MediaType != "application/zip" {
			t.Fatalf("bad result: %+v", result)
		}
		readback, err := os.ReadFile(result.Path)
		if err != nil || string(readback) != content {
			t.Fatalf("path does not contain source bytes: %v", err)
		}
	}
	httpCall := func(attachmentID string) app.AgentAttachmentMaterialization {
		t.Helper()
		response := agentRequest(t, h.server.URL+agentAttachmentMaterializeRoute, http.MethodPost, token, materializeAttachmentInput{AttachmentID: attachmentID})
		var result app.AgentAttachmentMaterialization
		decodeResponse(t, response, &result)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("HTTP status=%d", response.StatusCode)
		}
		verify(result, scratch)
		return result
	}
	first := httpCall(item.ID)
	secondItem, err := h.database.CreateAttachment(h.ctx, ordinary.ID, item.Name, item.MediaType, []byte(content), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second := httpCall(secondItem.ID)
	third := httpCall(item.ID)
	if first.Path == second.Path || second.Path == third.Path || first.Path == third.Path {
		t.Fatal("same original name or repeat call reused an output path")
	}
	verify(first, scratch)

	url := h.server.URL + "/api/v1/agent/core/mcp"
	status, _, schemas := coreMCPToolsList(t, url, petToken)
	if status != http.StatusOK || schemas["attachment_materialize"] == nil {
		t.Fatalf("pet cannot discover tool: status=%d", status)
	}
	call := coreMCPCallTool(t, url, petToken, "attachment_materialize", map[string]any{"attachment_id": petItem.ID})
	var petResult app.AgentAttachmentMaterialization
	if call.Status != http.StatusOK || call.IsError || json.Unmarshal([]byte(call.Text), &petResult) != nil {
		t.Fatalf("pet MCP failed: %+v", call)
	}
	verify(petResult, petScratch)
	stored, err := h.database.Attachment(h.ctx, pet.ID, petItem.ID)
	if err != nil || stored.MessageID != message.ID {
		t.Fatalf("attachment binding changed: %+v; err=%v", stored, err)
	}
	if foreign := coreMCPCallTool(t, url, petToken, "attachment_materialize", map[string]any{"attachment_id": item.ID}); !foreign.IsError || foreign.Text != "attachment_not_found" {
		t.Fatalf("pet MCP did not refuse a foreign attachment: %+v", foreign)
	}
	if invalid := coreMCPCallTool(t, url, petToken, "attachment_materialize", map[string]any{"attachment_id": petItem.ID, "path": "../sibling"}); !invalid.IsError || invalid.Text != "invalid_attachment_request" {
		t.Fatalf("pet MCP accepted destination input: %+v", invalid)
	}

	auditDB, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer auditDB.Close()
	var raw string
	if err := auditDB.QueryRow(`SELECT data_json FROM audit_events WHERE action='agent.attachment.materialize' AND resource_id=? AND owner_id='agent:main'`, petItem.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var audit map[string]any
	if err := json.Unmarshal([]byte(raw), &audit); err != nil || len(audit) != 2 || audit["size"] != float64(len(content)) || audit["result"] != "ok" {
		t.Fatalf("wrong materialization audit: %s; err=%v", raw, err)
	}
}

func TestAgentAttachmentMaterializeRefusesForeignUnauthorizedAndUnsafe(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	other := createCoreTaskInProject(t, h, task, "")
	item, scratch, turn, token := seedAgentAttachmentMaterialize(t, h, task, "main", "large relative to configured limit")
	foreign, err := h.database.CreateAttachment(h.ctx, other.ID, item.Name, item.MediaType, []byte("foreign"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, ScratchAttachmentLimit: 8}).Handler())
	defer server.Close()
	for _, test := range []struct {
		name   string
		token  string
		body   map[string]string
		status int
		code   string
	}{
		{"no capability", "", map[string]string{"attachment_id": item.ID}, 401, "agent_capability_required"},
		{"invalid capability", "invalid", map[string]string{"attachment_id": item.ID}, 401, "agent_capability_invalid"},
		{"mismatched turn scope", h.issue(other.ID, "main", turn.ID), map[string]string{"attachment_id": item.ID}, 403, "agent_operation_forbidden"},
		{"foreign attachment", token, map[string]string{"attachment_id": foreign.ID}, 404, "attachment_not_found"},
		{"missing attachment", token, map[string]string{"attachment_id": domain.NewID("attachment")}, 404, "attachment_not_found"},
		{"task override", token, map[string]string{"attachment_id": foreign.ID, "task_id": other.ID}, 400, "invalid_attachment_request"},
		{"path as id", token, map[string]string{"attachment_id": "../../foreign"}, 400, "invalid_attachment_id"},
		{"url as id", token, map[string]string{"attachment_id": "https://example.test/payload"}, 400, "invalid_attachment_id"},
		{"oversize", token, map[string]string{"attachment_id": item.ID}, 413, "attachment_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := agentRequest(t, server.URL+agentAttachmentMaterializeRoute, http.MethodPost, test.token, test.body)
			var payload map[string]any
			decodeResponse(t, response, &payload)
			if response.StatusCode != test.status || payload["error"] != test.code || payload["path"] != nil {
				t.Fatalf("status=%d payload=%v, want %d %s without path", response.StatusCode, payload, test.status, test.code)
			}
		})
	}
	tooLarge := coreMCPCallTool(t, server.URL+"/api/v1/agent/core/mcp", token, "attachment_materialize", map[string]any{"attachment_id": item.ID})
	if !tooLarge.IsError || tooLarge.Text != "attachment_too_large" {
		t.Fatalf("oversize MCP error: %+v", tooLarge)
	}
	turn.Status = domain.TurnFailed
	if err := h.database.UpdateTurn(h.ctx, turn, domain.TurnRunning); err != nil {
		t.Fatal(err)
	}
	response := agentRequest(t, server.URL+agentAttachmentMaterializeRoute, http.MethodPost, token, materializeAttachmentInput{AttachmentID: item.ID})
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusConflict || payload["error"] != "agent_turn_inactive" {
		t.Fatalf("inactive status=%d payload=%v", response.StatusCode, payload)
	}
	if entries, err := os.ReadDir(scratch); err != nil || len(entries) != 0 {
		t.Fatalf("denied operations left files: %v; err=%v", entries, err)
	}
}
