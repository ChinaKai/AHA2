package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ChinaKai/AHA2/internal/desktop"
)

func TestDesktopMCPUsesExistingShareAndSingleUseObservation(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	provider := &desktopFixture{}
	manager := desktop.New(provider)
	t.Cleanup(func() { _ = manager.Close() })
	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, Desktop: manager}).Handler())
	t.Cleanup(server.Close)
	turn := createCoreTurn(t, h.database, task, "main", "")
	token := h.issue(task.ID, "main", turn.ID)
	url := server.URL + "/api/v1/agent/desktop/mcp"
	status, names, _ := coreMCPToolsList(t, url, token)
	if status != http.StatusOK || len(names) != 5 || coreMCPNameIncluded(names, "desktop_open") {
		t.Fatalf("discovery=%v status=%d", names, status)
	}
	denied := coreMCPCallTool(t, url, token, "desktop_observe", map[string]any{"session_id": "not-granted"})
	if !denied.IsError || provider.calls.Load() != 0 {
		t.Fatal("unshared desktop was used")
	}
	shared, err := manager.OpenSharedTarget(h.ctx, task.ID, desktop.TargetSelection{Kind: "window", WindowID: "fixture"}, "foreground", true)
	if err != nil {
		t.Fatal(err)
	}
	session := shared.Session

	visible := coreMCPCallTool(t, url, token, "desktop_status", map[string]any{})
	if visible.IsError {
		t.Fatalf("status=%#v", visible)
	}
	wrongController := coreMCPCallTool(t, url, token, "desktop_control", map[string]any{"session_id": session.ID, "revision": session.Revision, "controller": "owner"})
	if !wrongController.IsError {
		t.Fatal("MCP claimed owner controller")
	}
	control := coreMCPCallTool(t, url, token, "desktop_control", map[string]any{"session_id": session.ID, "revision": session.Revision, "controller": "agent"})
	if control.IsError {
		t.Fatalf("control=%#v", control)
	}
	observed := coreMCPCallTool(t, url, token, "desktop_observe", map[string]any{"session_id": session.ID})
	if observed.IsError {
		t.Fatalf("observe=%#v", observed)
	}
	var result struct {
		Observation desktop.Observation `json:"observation"`
	}
	if err := json.Unmarshal([]byte(observed.Text), &result); err != nil || result.Observation.ID == "" {
		t.Fatalf("observation unavailable: %#v err=%v", result, err)
	}
	args := map[string]any{"session_id": session.ID, "revision": result.Observation.Revision, "observation_id": result.Observation.ID, "kind": "click", "element_id": "$surface", "x": 10, "y": 10, "button": "left"}
	action := coreMCPCallTool(t, url, token, "desktop_action", args)
	if action.IsError || provider.calls.Load() != 1 {
		t.Fatalf("action=%#v count=%d", action, provider.calls.Load())
	}
	replay := coreMCPCallTool(t, url, token, "desktop_action", args)
	if !replay.IsError || provider.calls.Load() != 1 {
		t.Fatal("observation replay reached provider")
	}
	args["target"] = map[string]any{"window_id": "different"}
	denied = coreMCPCallTool(t, url, token, "desktop_action", args)
	if !denied.IsError || provider.calls.Load() != 1 {
		t.Fatal("tool expanded shared target")
	}
	delete(args, "target")
	other := createCoreTaskInProject(t, h, task, "workspace-write")
	otherTurn := createCoreTurn(t, h.database, other, "main", "")
	otherToken := h.issue(other.ID, "main", otherTurn.ID)
	denied = coreMCPCallTool(t, url, otherToken, "desktop_stop", map[string]any{"session_id": session.ID})
	if !denied.IsError || manager.Status(task.ID).Session == nil {
		t.Fatal("other Task stopped current share")
	}
	stopped := coreMCPCallTool(t, url, token, "desktop_stop", map[string]any{"session_id": session.ID})
	if stopped.IsError || manager.Status(task.ID).Session != nil {
		t.Fatalf("stop=%#v", stopped)
	}
	denied = coreMCPCallTool(t, url, token, "desktop_action", args)
	if !denied.IsError || provider.calls.Load() != 1 {
		t.Fatal("revoked share reached provider")
	}
	readOnly := createCoreTaskInProject(t, h, task, "read-only")
	readTurn := createCoreTurn(t, h.database, readOnly, "main", "")
	readToken := h.issue(readOnly.ID, "main", readTurn.ID)
	_, names, _ = coreMCPToolsList(t, url, readToken)
	if len(names) != 2 {
		t.Fatalf("read-only tools=%v", names)
	}
	denied = coreMCPCallTool(t, url, readToken, "desktop_control", map[string]any{"session_id": session.ID, "revision": 1, "controller": "agent"})
	if denied.Status != http.StatusForbidden {
		t.Fatal("read-only Task gained control")
	}
}
