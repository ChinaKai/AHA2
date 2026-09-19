package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/desktop"
)

func (p *desktopStreamFixture) Targets(context.Context) (desktop.Targets, error) {
	return desktop.Targets{
		Windows:                []desktop.Window{{ID: "fixture", Kind: "window"}, {ID: "second", Kind: "window"}},
		Desktops:               []desktop.VirtualDesktop{{ID: "desk-1", Current: true}, {ID: "desk-2"}},
		Monitors:               []desktop.Monitor{{ID: "primary", Primary: true, Width: 1920, Height: 1080}, {ID: "left", X: -1280, Width: 1280, Height: 1024}},
		DesktopSwitchSupported: true,
	}, nil
}

func (p *desktopStreamFixture) SelectTarget(_ context.Context, target desktop.TargetSelection) (desktop.Window, error) {
	if target.Kind == "window" && (target.WindowID == "fixture" || target.WindowID == "second") {
		return desktop.Window{ID: target.WindowID, Kind: "window"}, nil
	}
	if target.Kind == "desktop" && (target.DesktopID == "desk-1" || target.DesktopID == "desk-2") &&
		(target.MonitorID == "left" || target.MonitorID == "primary" || target.MonitorID == "") {
		monitor := target.MonitorID
		if monitor == "" {
			monitor = "primary"
		}
		return desktop.Window{ID: "opaque:" + target.DesktopID + ":" + monitor, Kind: "desktop",
			DesktopID: target.DesktopID, MonitorID: monitor}, nil
	}
	return desktop.Window{}, &desktop.Error{Code: "desktop_target_gone"}
}

func TestDesktopTargetsRequireOwnerAndExposeMetadata(t *testing.T) {
	h := newStreamHarness(t)
	response := requestJSON(t, &http.Client{}, http.MethodGet, h.base+"/targets", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated target enumeration allowed")
	}
	response.Body.Close()
	response = requestJSON(t, h.client, http.MethodGet, h.base+"/targets", nil, "")
	var body struct {
		Targets desktop.Targets `json:"targets"`
	}
	decodeResponse(t, response, &body)
	if response.StatusCode != http.StatusOK || len(body.Targets.Desktops) != 2 || body.Targets.Monitors[1].X != -1280 ||
		!body.Targets.DesktopSwitchSupported || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("invalid owner catalog", response.StatusCode, body)
	}
	for _, path := range []string{"/targets", "/switch"} {
		response = agentRequest(t, h.server.URL+"/api/v1/agent/desktop"+path, http.MethodPost, "not-a-capability", map[string]any{})
		if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatal("agent target selection endpoint exposed", response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestDesktopSwitchCSRFConfirmationAndStaleInput(t *testing.T) {
	h := newStreamHarness(t)
	ctx := context.Background()
	frame, err := h.manager.Observe(ctx, h.taskID, h.session.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"session_id": h.session.ID, "revision": h.session.Revision, "mode": "foreground",
		"target": map[string]any{"kind": "desktop", "desktop_id": "desk-2", "monitor_id": "left"}, "confirm_foreground": true}
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/switch", payload, "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("switch missing CSRF accepted")
	}
	response.Body.Close()
	payload["confirm_foreground"] = false
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/switch", payload, h.csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("unconfirmed physical switch accepted")
	}
	response.Body.Close()
	if s := h.manager.Status(h.taskID).Session; s == nil || s.ID != h.session.ID || s.Revision != h.session.Revision {
		t.Fatal("invalid switch revoked grant")
	}
	payload["confirm_foreground"] = true
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/switch", payload, h.csrf)
	var body struct {
		Status desktop.Status `json:"status"`
	}
	decodeResponse(t, response, &body)
	next := body.Status.Session
	if response.StatusCode != http.StatusOK || next == nil || next.ID == h.session.ID || next.Controller != "owner" || next.Claimed ||
		next.Window.DesktopID != "desk-2" || next.Window.MonitorID != "left" || next.Switching {
		t.Fatal("incorrect replacement target", response.StatusCode, body)
	}
	old := desktop.ActionRequest{SessionID: h.session.ID, Revision: h.session.Revision, ObservationID: frame.ID,
		Action: desktop.Action{ElementID: "$surface", Kind: "click", X: 20, Y: 20}}
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/actions", old, h.csrf)
	if response.StatusCode != http.StatusConflict || h.fixture.calls.Load() != 0 {
		t.Fatal("old action reached switched target")
	}
	response.Body.Close()
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/switch", payload, h.csrf)
	if response.StatusCode != http.StatusConflict || h.manager.Status(h.taskID).Session.ID != next.ID {
		t.Fatal("stale switch replaced newer session")
	}
	response.Body.Close()
}

func TestDesktopSwitchClosesOldStreamAndDropsOldView(t *testing.T) {
	h := newStreamHarness(t)
	connection := h.connect(t, h.server.URL)
	startStream(t, h, connection, "view")
	streamFrame(t, connection)
	streamFrame(t, connection)
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/switch", map[string]any{
		"session_id": h.session.ID, "revision": h.session.Revision, "mode": "foreground", "confirm_foreground": true,
		"target": map[string]any{"kind": "window", "window_id": "second"},
	}, h.csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatal("switch failed", response.StatusCode)
	}
	response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("old stream survived switch", err)
	}
}

func TestDesktopOpenAcceptsStructuredTargetAndRejectsConflictingFields(t *testing.T) {
	h := newStreamHarness(t)
	h.manager.Stop(h.taskID, h.session.ID)
	payload := map[string]any{"mode": "foreground", "confirm_foreground": true,
		"target": map[string]any{"kind": "desktop", "desktop_id": "desk-1", "monitor_id": "primary"}}
	payload["window_id"] = "new-desktop"
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/session", payload, h.csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("conflicting selectors allowed")
	}
	response.Body.Close()
	delete(payload, "window_id")
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/session", payload, h.csrf)
	if response.StatusCode != http.StatusCreated {
		t.Fatal("structured target not accepted", response.StatusCode)
	}
	response.Body.Close()
	state := h.manager.Status(h.taskID).Session
	if state == nil || state.Window.DesktopID != "desk-1" || state.Window.MonitorID != "primary" {
		t.Fatal("structured target lost")
	}
}
