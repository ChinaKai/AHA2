package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type desktopFixture struct{ calls atomic.Int32 }

func (*desktopFixture) Support() (bool, string) { return true, "" }
func (*desktopFixture) Windows(context.Context) ([]desktop.Window, error) {
	return []desktop.Window{{ID: "fixture", Title: "Fixture window", Process: "fixture"}}, nil
}
func (*desktopFixture) Observe(context.Context, desktop.Window) (desktop.Observation, error) {
	return desktop.Observation{Elements: []desktop.Element{
		{ID: "button", Name: "Fixture button", Actions: []string{"invoke"}},
	}}, nil
}
func (p *desktopFixture) Act(context.Context, desktop.Window, desktop.Action) error {
	p.calls.Add(1)
	return nil
}

type foregroundHTTPFixture struct {
	desktopFixture
	created atomic.Int32
}

func (p *foregroundHTTPFixture) NewDesktop(context.Context) (desktop.Window, error) {
	p.created.Add(1)
	return desktop.Window{ID: "desktop:test", Kind: "desktop", Title: "Test desktop", Process: "Windows"}, nil
}
func (*foregroundHTTPFixture) ObserveForeground(context.Context, desktop.Window) (desktop.Observation, error) {
	return desktop.Observation{Width: 800, Height: 600, Surface: "fixture-surface", InputActions: []string{"click", "text", "key", "focus"}}, nil
}
func (p *foregroundHTTPFixture) ActForeground(_ context.Context, _ desktop.Window, _ desktop.Action, capture desktop.Observation) error {
	if capture.Surface != "fixture-surface" || capture.Width != 800 {
		return &desktop.Error{Code: "desktop_stale_surface"}
	}
	p.calls.Add(1)
	return nil
}

func TestDesktopForegroundOwnerConsentAndGenericInput(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createHardwareAPITask(t, db)
	other := createHardwareAPITask(t, db)
	p := &foregroundHTTPFixture{}
	m := desktop.New(p)
	server := httptest.NewServer(New(Config{
		Store: db, Auth: auth.NewService(db, "setup-test", time.Hour),
		App: app.NewService(db, nil, app.StubExecutor{}), Desktop: m,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	base := server.URL + "/api/v1/tasks/" + task.ID + "/desktop"
	response := requestJSON(t, client, http.MethodPost, base+"/session",
		map[string]any{"window_id": "new-desktop", "mode": "foreground"}, csrf)
	if response.StatusCode != http.StatusForbidden || p.created.Load() != 0 {
		t.Fatal("unconfirmed foreground creation allowed")
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/session",
		map[string]any{"mode": "foreground", "confirm_foreground": true}, csrf)
	var state struct {
		Status desktop.Status `json:"status"`
		Error  string         `json:"error"`
	}
	decodeResponse(t, response, &state)
	if response.StatusCode != http.StatusCreated || state.Status.Session == nil ||
		state.Status.Session.Mode != "foreground" || state.Status.Session.Window.Kind != "desktop" ||
		!state.Status.ForegroundSupported || p.created.Load() != 1 {
		t.Fatalf("default new desktop failed: %d %+v", response.StatusCode, state)
	}
	s := *state.Status.Session
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+other.ID+"/desktop/session",
		map[string]any{"window_id": "fixture", "mode": "foreground", "confirm_foreground": true}, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("global foreground lock bypassed: %d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, base+"/observation?session_id="+s.ID, nil, "")
	var observation struct {
		Observation desktop.Observation `json:"observation"`
	}
	decodeResponse(t, response, &observation)
	if response.StatusCode != http.StatusOK || observation.Observation.ID == "" {
		t.Fatal("foreground observe failed")
	}
	action := desktop.ActionRequest{SessionID: s.ID, Revision: s.Revision, ObservationID: observation.Observation.ID,
		Action: desktop.Action{Kind: "click", ElementID: "$surface", X: 30, Y: 40}}
	response = requestJSON(t, client, http.MethodPost, base+"/actions", action, csrf)
	if response.StatusCode != http.StatusOK || p.calls.Load() != 1 {
		t.Fatalf("generic foreground input failed: %d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/actions", action, csrf)
	if response.StatusCode != http.StatusConflict || p.calls.Load() != 1 {
		t.Fatal("generic input replayed")
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/control",
		map[string]any{"session_id": s.ID, "revision": s.Revision, "controller": "agent", "mode": "background"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("mode change through control endpoint accepted")
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/stop", map[string]any{"session_id": s.ID}, csrf)
	if response.StatusCode != http.StatusOK || m.Status(task.ID).Session != nil {
		t.Fatal("foreground stop failed")
	}
	response.Body.Close()
}

func TestDesktopOwnerAndAgentSharedSession(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createHardwareAPITask(t, db)
	otherTask := createHardwareAPITask(t, db)
	caps := agentapi.NewCapabilities()
	service := app.NewService(db, nil, app.StubExecutor{})
	fixture := &desktopFixture{}
	manager := desktop.New(fixture)
	server := httptest.NewServer(New(Config{
		Store: db, Auth: auth.NewService(db, "setup-test", time.Hour), App: service,
		Desktop: manager, AgentCapabilities: caps,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	base := server.URL + "/api/v1/tasks/" + task.ID + "/desktop"

	var state struct {
		OK     bool           `json:"ok"`
		Status desktop.Status `json:"status"`
		Error  string         `json:"error"`
	}
	response := requestJSON(t, client, http.MethodPost, base+"/session", map[string]any{"window_id": "fixture"}, "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/session", map[string]any{"window_id": "fixture"}, csrf)
	decodeResponse(t, response, &state)
	if response.StatusCode != http.StatusCreated || state.Status.Session.Controller != "owner" {
		t.Fatalf("share failed: %d %+v", response.StatusCode, state)
	}
	session := *state.Status.Session
	issue := func(taskID, agentID string, status domain.TurnStatus) string {
		t.Helper()
		record, err := db.Task(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		message := domain.Message{ID: domain.NewID("message"), TaskID: taskID, Role: "user",
			Sender: "owner", Content: "Fixture", CreatedAt: time.Now().UTC()}
		if err := db.AddMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
		turn, err := db.CreateAgentTurn(ctx, domain.Turn{
			ID: domain.NewID("turn"), TaskID: taskID, AgentID: agentID,
			RoundID: domain.NewID("round"),
			Status:  status, QueuedAt: time.Now().UTC(),
			InputMessageID: message.ID, RuntimeConfigSnapshotID: record.RuntimeConfigSnapshotID,
		})
		if err != nil {
			t.Fatal(err)
		}
		token, err := caps.Issue(taskID, agentID, turn.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	active := issue(task.ID, "main", domain.TurnRunning)
	inactive := issue(task.ID, "main", domain.TurnSucceeded)
	sub := issue(task.ID, "sub-001", domain.TurnRunning)
	other := issue(otherTask.ID, "main", domain.TurnRunning)
	agentBase := server.URL + "/api/v1/agent/desktop"
	for _, rejected := range []string{inactive, sub, "bad-token"} {
		response = agentRequest(t, agentBase, http.MethodGet, rejected, nil)
		if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusUnauthorized &&
			response.StatusCode != http.StatusConflict {
			t.Fatalf("invalid actor permitted: %d", response.StatusCode)
		}
		response.Body.Close()
	}
	for _, path := range []string{"/windows", "/session"} {
		response = agentRequest(t, agentBase+path, http.MethodPost, active, map[string]any{"window_id": "fixture"})
		if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("agent may enumerate/select windows: %d", response.StatusCode)
		}
		response.Body.Close()
	}
	response = agentRequest(t, agentBase+"/observation?session_id="+session.ID, http.MethodGet, other, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("cross task read permitted: %d", response.StatusCode)
	}
	response.Body.Close()
	control := map[string]any{"session_id": session.ID, "revision": session.Revision, "controller": "agent"}
	response = agentRequest(t, agentBase+"/control", http.MethodPost, active, control)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("agent self-granted access: %d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/control", control, csrf)
	decodeResponse(t, response, &state)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner grant failed: %+v", state)
	}
	session = *state.Status.Session
	control["revision"] = session.Revision
	response = agentRequest(t, agentBase+"/control", http.MethodPost, active, control)
	decodeResponse(t, response, &state)
	if response.StatusCode != http.StatusOK || !state.Status.Session.Claimed {
		t.Fatalf("claim failed: %d %+v", response.StatusCode, state)
	}
	var observation struct {
		Observation desktop.Observation `json:"observation"`
	}
	response = agentRequest(t, agentBase+"/observation?session_id="+session.ID, http.MethodGet, active, nil)
	decodeResponse(t, response, &observation)
	if response.StatusCode != http.StatusOK || observation.Observation.ID == "" {
		t.Fatalf("observe failed: %d", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("observation is cacheable")
	}
	action := desktop.ActionRequest{SessionID: session.ID, Revision: session.Revision,
		ObservationID: observation.Observation.ID, Action: desktop.Action{Kind: "invoke", ElementID: "button"}}
	response = agentRequest(t, agentBase+"/actions", http.MethodPost, active, action)
	if response.StatusCode != http.StatusOK || fixture.calls.Load() != 1 {
		t.Fatalf("valid action failed: %d calls=%d", response.StatusCode, fixture.calls.Load())
	}
	response.Body.Close()
	response = agentRequest(t, agentBase+"/actions", http.MethodPost, active, action)
	if response.StatusCode != http.StatusConflict || fixture.calls.Load() != 1 {
		t.Fatal("replayed action reached provider")
	}
	response.Body.Close()
	control["controller"] = "owner"
	response = requestJSON(t, client, http.MethodPost, base+"/control", control, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatal("takeover failed")
	}
	response.Body.Close()
	response = agentRequest(t, agentBase+"/observation?session_id="+session.ID, http.MethodGet, active, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("agent observes after owner takeover")
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, base+"/stop", map[string]any{"session_id": session.ID}, csrf)
	if response.StatusCode != http.StatusOK || manager.Status(task.ID).Session != nil {
		t.Fatal("stop did not revoke grant")
	}
	response.Body.Close()
}

func TestDesktopRejectsUnknownFieldsAndOversizedInput(t *testing.T) {
	for _, raw := range []string{
		`{"window_id":"w","command":"run"}`,
		`{"window_id":"w"} {}`,
		`{"window_id":"` + strings.Repeat("x", 70*1024) + `"}`,
	} {
		writer := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
		var payload struct {
			WindowID string `json:"window_id"`
		}
		if desktopJSON(writer, request, &payload) || writer.Code != http.StatusBadRequest {
			t.Fatal("invalid input accepted")
		}
	}
}
