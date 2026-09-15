package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func cooperativeAgentToken(t *testing.T, h *streamHarness, taskID, agentID string, status domain.TurnStatus) string {
	t.Helper()
	ctx := context.Background()
	task, err := h.store.Task(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user",
		Sender: "owner", Content: "Cooperative fixture", CreatedAt: time.Now().UTC()}
	if err := h.store.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	turn, err := h.store.CreateAgentTurn(ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, AgentID: agentID, RoundID: domain.NewID("round"),
		Status: status, QueuedAt: time.Now().UTC(), InputMessageID: message.ID,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.caps.Issue(task.ID, agentID, turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func openSharedHTTP(t *testing.T, h *streamHarness) {
	t.Helper()
	h.manager.Stop(h.taskID, h.session.ID)
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/session", map[string]any{
		"target": map[string]string{"kind": "window", "window_id": "fixture"},
		"mode":   "foreground", "confirm_foreground": true, "confirm_shared": true,
	}, h.csrf)
	var body struct {
		Status desktop.Status `json:"status"`
	}
	decodeResponse(t, response, &body)
	if response.StatusCode != http.StatusCreated || body.Status.Session == nil ||
		body.Status.Session.Controller != "shared" || !body.Status.SharedControlSupported {
		t.Fatalf("shared HTTP creation failed: status=%d", response.StatusCode)
	}
	h.session = *body.Status.Session
}

func TestSharedHTTPAgentUseAndOwnerAssistanceWithoutHandoff(t *testing.T) {
	h := newStreamHarness(t)
	openSharedHTTP(t, h)
	token := cooperativeAgentToken(t, h, h.taskID, "main", domain.TurnRunning)
	c := h.connect(t, h.server.URL)
	startStream(t, h, c, "helper")
	ownerFrame := streamFrame(t, c)
	streamFrame(t, c)
	agentURL := h.server.URL + "/api/v1/agent/desktop"
	readAgent := func() desktop.Observation {
		response := agentRequest(t, agentURL+"/observation?session_id="+h.session.ID, http.MethodGet, token, nil)
		var result struct{ Observation desktop.Observation }
		decodeResponse(t, response, &result)
		if response.StatusCode != http.StatusOK || result.Observation.ID == "" {
			t.Fatalf("automatic shared Agent registration failed: %d", response.StatusCode)
		}
		return result.Observation
	}
	agentFrame := readAgent()
	readAgent()
	items, err := h.store.SyncConversation(context.Background(), h.taskID)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, item := range items {
		if item.Kind == "agent_message_update" && strings.Contains(item.Summary, "Agent 开始使用共享目标") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("notice count=%d, want one without /control request", notices)
	}
	current := h.manager.Status(h.taskID).Session
	if current == nil || current.Controller != "shared" || !current.Claimed || current.Revision != h.session.Revision {
		t.Fatal("Agent registration changed Owner grant")
	}
	action := desktop.ActionRequest{SessionID: h.session.ID, Revision: h.session.Revision,
		ObservationID: ownerFrame.ID, ViewID: "helper", ActionID: "help-one",
		Action: desktop.Action{Kind: "key", ElementID: "$surface", Keys: []string{"ENTER"}}}
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/actions", action, h.csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatal("Owner assistance after Agent registration failed", response.StatusCode)
	}
	response.Body.Close()
	agentAction := desktop.ActionRequest{SessionID: h.session.ID, Revision: h.session.Revision,
		ObservationID: agentFrame.ID, ActionID: "agent-one",
		Action: desktop.Action{Kind: "key", ElementID: "$surface", Keys: []string{"ENTER"}}}
	response = agentRequest(t, agentURL+"/actions", http.MethodPost, token, agentAction)
	if response.StatusCode != http.StatusConflict {
		t.Fatal("Agent input used its pre-assistance frame", response.StatusCode)
	}
	response.Body.Close()
	agentAction.ObservationID = readAgent().ID
	response = agentRequest(t, agentURL+"/actions", http.MethodPost, token, agentAction)
	if response.StatusCode != http.StatusOK {
		t.Fatal("Agent cannot continue after fresh observation", response.StatusCode)
	}
	response.Body.Close()
	if h.fixture.calls.Load() != 2 {
		t.Fatal("incorrect input count", h.fixture.calls.Load())
	}
	sendStream(t, c, desktopStreamMessage{Type: "ack", FrameID: ownerFrame.ID, IntervalMS: 100})
	freshOwner := streamFrame(t, c)
	if freshOwner.ID == ownerFrame.ID {
		t.Fatal("Owner stream did not continue after assistance")
	}
	current = h.manager.Status(h.taskID).Session
	if current.Controller != "shared" || current.Revision != h.session.Revision {
		t.Fatal("cooperative actions transferred control")
	}
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/stop", map[string]string{"session_id": h.session.ID}, h.csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatal("Owner stop failed", response.StatusCode)
	}
	response.Body.Close()
	response = agentRequest(t, agentURL+"/observation?session_id="+h.session.ID, http.MethodGet, token, nil)
	if response.StatusCode != http.StatusConflict || h.manager.Status(h.taskID).Session != nil {
		t.Fatal("Stop failed to revoke Agent access")
	}
	response.Body.Close()
}

func TestSharedHTTPConsentAndLegacyGrants(t *testing.T) {
	h := newStreamHarness(t)
	token := cooperativeAgentToken(t, h, h.taskID, "main", domain.TurnRunning)
	agentURL := h.server.URL + "/api/v1/agent/desktop"
	response := agentRequest(t, agentURL+"/observation?session_id="+h.session.ID, http.MethodGet, token, nil)
	if response.StatusCode != http.StatusForbidden || h.manager.Status(h.taskID).Session.Controller != "owner" {
		t.Fatal("legacy Owner grant silently upgraded")
	}
	response.Body.Close()
	h.manager.Stop(h.taskID, h.session.ID)
	body := map[string]any{"target": map[string]string{"kind": "window", "window_id": "fixture"},
		"mode": "foreground", "confirm_foreground": true, "confirm_shared": true}
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/session", body, "")
	if response.StatusCode != http.StatusForbidden || h.manager.Status(h.taskID).Session != nil {
		t.Fatal("shared creation accepted without Owner CSRF")
	}
	response.Body.Close()
	body["confirm_foreground"] = false
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/session", body, h.csrf)
	if response.StatusCode != http.StatusForbidden || h.manager.Status(h.taskID).Session != nil {
		t.Fatal("joint consent replaced foreground consent")
	}
	response.Body.Close()
	body["confirm_foreground"] = true
	body["confirm_shared"] = false
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/session", body, h.csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || h.manager.Status(h.taskID).Session.Controller != "owner" {
		t.Fatal("explicit lack of joint consent ignored")
	}
	h.session = *h.manager.Status(h.taskID).Session
	openSharedHTTP(t, h)
	for _, controller := range []string{"owner", "agent"} {
		response = requestJSON(t, h.client, http.MethodPost, h.base+"/control", map[string]any{
			"session_id": h.session.ID, "revision": h.session.Revision, "controller": controller,
		}, h.csrf)
		if response.StatusCode != http.StatusConflict {
			t.Fatal("exclusive handoff accepted for shared grant")
		}
		response.Body.Close()
	}
	if h.manager.Status(h.taskID).Session.Revision != h.session.Revision {
		t.Fatal("unsupported transfer mutated shared revision")
	}
}

func TestSharedHTTPStillRequiresActiveMainAndTaskScope(t *testing.T) {
	h := newStreamHarness(t)
	openSharedHTTP(t, h)
	other := createHardwareAPITask(t, h.store)
	for _, item := range []struct {
		taskID, agentID string
		status          domain.TurnStatus
	}{
		{h.taskID, "sub-001", domain.TurnRunning},
		{h.taskID, "main", domain.TurnSucceeded},
		{other.ID, "main", domain.TurnRunning},
	} {
		token := cooperativeAgentToken(t, h, item.taskID, item.agentID, item.status)
		response := agentRequest(t, h.server.URL+"/api/v1/agent/desktop/observation?session_id="+h.session.ID,
			http.MethodGet, token, nil)
		if response.StatusCode < 400 {
			t.Fatal("shared mode bypassed Agent capability scope")
		}
		response.Body.Close()
	}
	if h.manager.Status(h.taskID).Session.Claimed {
		t.Fatal("rejected Agent registered use")
	}
}

func TestSharedHTTPBackgroundConsentAndTargetSwitch(t *testing.T) {
	h := newStreamHarness(t)
	h.manager.Stop(h.taskID, h.session.ID)
	payload := map[string]any{"target": map[string]string{"kind": "window", "window_id": "fixture"},
		"mode": "background", "confirm_shared": true}
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/session", payload, h.csrf)
	var opened struct{ Status desktop.Status }
	decodeResponse(t, response, &opened)
	if response.StatusCode != http.StatusCreated || opened.Status.Session.Controller != "shared" {
		t.Fatal("joint background grant unavailable")
	}
	s := opened.Status.Session
	token := cooperativeAgentToken(t, h, h.taskID, "main", domain.TurnRunning)
	response = agentRequest(t, h.server.URL+"/api/v1/agent/desktop/observation?session_id="+s.ID, http.MethodGet, token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal("Agent cannot observe jointly shared background target")
	}
	response.Body.Close()
	payload["session_id"], payload["revision"] = s.ID, s.Revision
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/switch", payload, h.csrf)
	var switched struct{ Status desktop.Status }
	decodeResponse(t, response, &switched)
	if response.StatusCode != http.StatusOK || switched.Status.Session.ID == s.ID ||
		switched.Status.Session.Controller != "shared" || switched.Status.Session.Claimed {
		t.Fatal("switch inherited old access registration or lost explicit joint grant")
	}
	response = agentRequest(t, h.server.URL+"/api/v1/agent/desktop/observation?session_id="+s.ID, http.MethodGet, token, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatal("old session survived joint switch")
	}
	response.Body.Close()
}
