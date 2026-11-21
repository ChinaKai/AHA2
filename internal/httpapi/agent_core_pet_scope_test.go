package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, true)
	h := f.h
	snapshot, err := h.database.RuntimeSnapshot(h.ctx, f.task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{ID: domain.NewID("task"), ProjectID: f.task.ProjectID, WorkspaceID: f.task.WorkspaceID, Title: "INTERNAL-PEER-MARKER", Request: "fixture", ModelID: snapshot.ModelID, AllowSystemProject: true, Isolation: "inplace", Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1})
	if err != nil {
		t.Fatal(err)
	}
	peerTurn := createCoreTurn(t, h.database, peer, "main", domain.NewID("inbox_batch"))
	peerToken := h.issue(peer.ID, "main", peerTurn.ID)
	_, _, _, err = h.database.EnqueueOwnerMessage(h.ctx, domain.Message{ID: domain.NewID("message"), TaskID: peer.ID, Role: "user", Sender: "owner", Content: "PEER-INBOX-MARKER", CreatedAt: time.Now()}, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	assistant, _ := h.database.EnsureOwnerAssistant(h.ctx, time.Now())
	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	url := f.server.URL + "/api/v1/agent/core/mcp"
	for _, op := range []struct {
		name string
		args map[string]any
	}{
		{"task_get", map[string]any{}},
		{"task_list", map[string]any{}},
		{"inbox_wait", map[string]any{"timeout_seconds": 1}},
	} {
		result := coreMCPCallTool(t, url, f.token, op.name, op.args)
		if result.Status != 200 || result.IsError || strings.Contains(result.Text, "INTERNAL-PEER-MARKER") || strings.Contains(result.Text, "PEER-INBOX-MARKER") {
			t.Fatalf("own-Turn %s escaped scope or was denied", op.name)
		}
	}
	denied := coreMCPCallTool(t, url, f.token, "task_get", map[string]any{"task_id": peer.ID})
	if denied.Status != http.StatusForbidden {
		t.Fatal("pet core read another internal Task")
	}
	for _, path := range []string{"/project/workspaces", "/project/runtimes", "/tasks/" + peer.ID} {
		response := agentRequest(t, f.server.URL+"/api/v1/agent"+path, http.MethodGet, f.token, nil)
		status := response.StatusCode
		response.Body.Close()
		if status != 403 {
			t.Fatalf("pet direct HTTP escaped live/scope gate at %s: %d", path, status)
		}
	}
	response := agentMultipartRequest(t, f.server.URL+"/api/v1/agent/turn/attachments", peerToken, "peer.txt", []byte("peer fixture"))
	var uploaded struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	decodeResponse(t, response, &uploaded)
	if response.StatusCode != http.StatusCreated {
		t.Fatal("peer attachment fixture upload failed")
	}
	denied = coreMCPCallTool(t, url, f.token, "attachment_bind", map[string]any{"attachment_ids": []string{uploaded.Attachment.ID}})
	if !denied.IsError {
		t.Fatal("pet bound another Task's attachment")
	}
	stored, err := h.database.Attachment(h.ctx, peer.ID, uploaded.Attachment.ID)
	if err != nil || stored.MessageID != "" {
		t.Fatal("denied bind changed another Task's attachment")
	}
}
