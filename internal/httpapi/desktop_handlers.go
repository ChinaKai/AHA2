package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ChinaKai/AHA2/internal/desktop"
)

func (s *Server) CloseDesktop() error {
	if s.desktop == nil {
		return nil
	}
	return s.desktop.Close()
}

func (s *Server) registerDesktopRoutes(mux *http.ServeMux) {
	prefix := "/api/v1/tasks/{id}/desktop"
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET " + prefix, s.desktopStatus},
		{"GET " + prefix + "/windows", s.desktopWindows},
		{"GET " + prefix + "/targets", s.desktopTargets},
		{"POST " + prefix + "/session", s.desktopOpen},
		{"POST " + prefix + "/control", s.desktopControl},
		{"POST " + prefix + "/stop", s.desktopStop},
		{"POST " + prefix + "/switch", s.desktopSwitch},
		{"GET " + prefix + "/observation", s.desktopObserve},
		{"GET " + prefix + "/stream", s.desktopStream},
		{"POST " + prefix + "/actions", s.desktopAct},
	} {
		mux.Handle(route.pattern, s.withAuth(route.handler))
	}
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /api/v1/agent/desktop", s.desktopStatus},
		{"GET /api/v1/agent/desktop/observation", s.desktopObserve},
		{"POST /api/v1/agent/desktop/control", s.desktopControl},
		{"POST /api/v1/agent/desktop/actions", s.desktopAct},
	} {
		mux.Handle(route.pattern, s.withAgentCapability(route.handler))
	}
}

func (s *Server) desktopTarget(w http.ResponseWriter, r *http.Request) (taskID, actor string, ok bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	taskID, actor = r.PathValue("id"), "owner"
	if claims, agent := agentClaimsFromContext(r.Context()); agent {
		if s.app == nil {
			writeError(w, http.StatusServiceUnavailable, "desktop_unavailable")
			return "", "", false
		}
		call, err := s.app.AgentCallContext(r.Context(), claims, true)
		if err != nil {
			writeAgentControlError(w, err)
			return "", "", false
		}
		taskID, actor = call.Task.ID, "agent:"+call.Turn.ID
	}
	task, err := s.store.Task(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.desktop.Revoke(taskID)
			writeError(w, http.StatusNotFound, "task_not_found")
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			desktopError(w, err)
		} else {
			writeError(w, http.StatusServiceUnavailable, "desktop_state_unavailable")
		}
		return "", "", false
	}
	if task.Status.Terminal() {
		s.desktop.Revoke(taskID)
		writeError(w, http.StatusForbidden, "desktop_task_read_only")
		return "", "", false
	}
	if s.rejectRetiredChannelTaskWrite(w, r, taskID) {
		s.desktop.Revoke(taskID)
		return "", "", false
	}
	return taskID, actor, true
}

func desktopJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "desktop_invalid_request")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "desktop_invalid_request")
		return false
	}
	return true
}

func desktopError(w http.ResponseWriter, err error) {
	code := desktop.ErrorCode(err)
	status := http.StatusConflict
	switch code {
	case "desktop_unsupported", "desktop_unavailable":
		status = http.StatusServiceUnavailable
	case "desktop_invalid_request", "desktop_invalid_action", "desktop_invalid_target", "desktop_invalid_control",
		"desktop_invalid_mode", "desktop_invalid_coordinates", "desktop_invalid_key", "desktop_invalid_view", "desktop_invalid_preview":
		status = http.StatusBadRequest
	case "desktop_owner_control", "desktop_agent_control", "desktop_claim_required", "desktop_invalid_actor":
		status = http.StatusForbidden
	case "desktop_foreground_confirmation_required":
		status = http.StatusForbidden
	case "desktop_timeout":
		status = http.StatusGatewayTimeout
	}
	writeError(w, status, code)
}

func (s *Server) desktopStatus(w http.ResponseWriter, r *http.Request) {
	taskID, _, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": s.desktop.Status(taskID)})
}

func (s *Server) desktopWindows(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.desktopTarget(w, r); !ok {
		return
	}
	windows, err := s.desktop.Windows(r.Context())
	if err != nil {
		desktopError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "windows": windows})
}

func (s *Server) desktopTargets(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.desktopTarget(w, r); !ok {
		return
	}
	targets, err := s.desktop.Targets(r.Context())
	if err != nil {
		desktopError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "targets": targets})
}

func (s *Server) desktopOpen(w http.ResponseWriter, r *http.Request) {
	taskID, _, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	var payload struct {
		WindowID          string                   `json:"window_id"`
		Mode              string                   `json:"mode"`
		ConfirmForeground bool                     `json:"confirm_foreground"`
		ConfirmShared     bool                     `json:"confirm_shared"`
		Target            *desktop.TargetSelection `json:"target,omitempty"`
	}
	if !desktopJSON(w, r, &payload) {
		return
	}
	if payload.Target != nil && payload.WindowID != "" {
		writeError(w, http.StatusBadRequest, "desktop_invalid_target")
		return
	}
	if payload.ConfirmShared && payload.Target == nil {
		writeError(w, http.StatusBadRequest, "desktop_invalid_target")
		return
	}
	if payload.Target == nil && payload.WindowID == "" && payload.Mode == "foreground" {
		payload.WindowID = "new-desktop"
	}
	var status desktop.Status
	var err error
	if payload.ConfirmShared {
		status, err = s.desktop.OpenSharedTarget(r.Context(), taskID, *payload.Target, payload.Mode, payload.ConfirmForeground)
	} else if payload.Target != nil {
		status, err = s.desktop.OpenTarget(r.Context(), taskID, *payload.Target, payload.Mode, payload.ConfirmForeground)
	} else {
		status, err = s.desktop.OpenWithMode(r.Context(), taskID, payload.WindowID, payload.Mode, payload.ConfirmForeground)
	}
	if err != nil {
		desktopError(w, err)
		return
	}
	// Window titles can contain private document names; audit IDs only.
	s.audit(r, "task.desktop.share", "task", taskID, map[string]any{"session_id": status.Session.ID,
		"mode": status.Session.Mode, "target_kind": status.Session.Window.Kind, "shared": payload.ConfirmShared})
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "status": status})
}

func (s *Server) desktopSwitch(w http.ResponseWriter, r *http.Request) {
	taskID, _, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	var payload struct {
		SessionID         string                  `json:"session_id"`
		Revision          uint64                  `json:"revision"`
		Target            desktop.TargetSelection `json:"target"`
		Mode              string                  `json:"mode"`
		ConfirmForeground bool                    `json:"confirm_foreground"`
		ConfirmShared     bool                    `json:"confirm_shared"`
	}
	if !desktopJSON(w, r, &payload) {
		return
	}
	var status desktop.Status
	var err error
	if payload.ConfirmShared {
		status, err = s.desktop.SwitchSharedTarget(r.Context(), taskID, payload.SessionID, payload.Revision, payload.Target, payload.Mode, payload.ConfirmForeground)
	} else {
		status, err = s.desktop.SwitchTarget(r.Context(), taskID, payload.SessionID, payload.Revision, payload.Target, payload.Mode, payload.ConfirmForeground)
	}
	if err != nil {
		s.audit(r, "task.desktop.switch_failed", "task", taskID, map[string]any{"session_id": payload.SessionID, "error": desktop.ErrorCode(err)})
		desktopError(w, err)
		return
	}
	s.audit(r, "task.desktop.switch", "task", taskID, map[string]any{"session_id": status.Session.ID, "mode": status.Session.Mode, "target_kind": status.Session.Window.Kind})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}
func (s *Server) desktopControl(w http.ResponseWriter, r *http.Request) {
	taskID, actor, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	var payload struct {
		SessionID  string `json:"session_id"`
		Revision   uint64 `json:"revision"`
		Controller string `json:"controller"`
	}
	if !desktopJSON(w, r, &payload) {
		return
	}
	var status desktop.Status
	var err error
	if actor == "owner" {
		status, err = s.desktop.Control(taskID, payload.SessionID, payload.Revision, payload.Controller)
	} else {
		if payload.Controller != "agent" {
			writeError(w, http.StatusForbidden, "desktop_invalid_control")
			return
		}
		status, err = s.claimDesktop(r, taskID, payload.SessionID, payload.Revision)
	}
	if err != nil {
		desktopError(w, err)
		return
	}
	s.audit(r, "task.desktop.control", "task", taskID, map[string]any{
		"session_id": payload.SessionID, "controller": payload.Controller,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (s *Server) claimDesktop(r *http.Request, taskID, sessionID string, revision uint64) (desktop.Status, error) {
	claims, _ := agentClaimsFromContext(r.Context())
	return s.desktop.Claim(r.Context(), taskID, sessionID, revision, claims.TurnID, func(window desktop.Window) error {
		current := s.desktop.Status(taskID).Session
		message := fmt.Sprintf("即将控制已共享的软件窗口：%s（%s）。你可以停止共享。",
			desktopNoticeText(window.Title), desktopNoticeText(window.Process))
		if current != nil && current.ID == sessionID {
			if current.Controller == "shared" {
				message = fmt.Sprintf("Agent 开始使用共享目标：%s（%s）。Owner 可同时协助，无需转交控制权；随时可以停止共享。",
					desktopNoticeText(window.Title), desktopNoticeText(window.Process))
			}
			if current.Mode == "foreground" {
				message += " 前台操作会影响宿主鼠标、键盘和输入焦点。"
			}
		}
		return s.app.AddAgentProgress(r.Context(), claims, message, nil)
	})
}

// Joint access is explicit in the Owner-created grant. Registering the active
// Turn's notice does not transfer control or reset the Owner's preview stream.
func (s *Server) ensureSharedDesktopClaim(w http.ResponseWriter, r *http.Request, taskID, sessionID string, revision uint64) bool {
	if _, agent := agentClaimsFromContext(r.Context()); !agent {
		return true
	}
	session := s.desktop.Status(taskID).Session
	if session == nil || session.ID != sessionID || session.Controller != "shared" {
		return true
	}
	if revision != 0 && revision != session.Revision {
		desktopError(w, &desktop.Error{Code: "desktop_stale_session"})
		return false
	}
	if _, err := s.claimDesktop(r, taskID, sessionID, session.Revision); err != nil {
		desktopError(w, err)
		return false
	}
	return true
}

func desktopNoticeText(value string) string {
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500])
	}
	return strings.NewReplacer("\\", "\\\\", "\r", " ", "\n", " ", "`", "\\`",
		"*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)",
		"!", "\\!", "<", "&lt;", ">", "&gt;", "#", "\\#").Replace(value)
}

func (s *Server) desktopStop(w http.ResponseWriter, r *http.Request) {
	// Stop remains available after task completion, so it cannot be blocked by
	// the normal read-only gate. It never grants or performs native operations.
	w.Header().Set("Cache-Control", "no-store")
	taskID := r.PathValue("id")
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if !desktopJSON(w, r, &payload) {
		return
	}
	status, err := s.desktop.Stop(taskID, payload.SessionID)
	if err != nil {
		desktopError(w, err)
		return
	}
	s.audit(r, "task.desktop.stop", "task", taskID, map[string]any{"session_id": payload.SessionID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (s *Server) desktopObserve(w http.ResponseWriter, r *http.Request) {
	taskID, actor, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	actor, err := desktop.ViewActor(actor, r.URL.Query().Get("view_id"))
	if err != nil {
		desktopError(w, err)
		return
	}
	options := desktop.FrameOptions{}
	for key, target := range map[string]*int{"max_width": &options.MaxWidth, "max_height": &options.MaxHeight, "quality": &options.Quality} {
		if value := r.URL.Query().Get(key); value != "" {
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "desktop_invalid_preview")
				return
			}
			*target = n
		}
	}
	var observation desktop.Observation
	if !s.ensureSharedDesktopClaim(w, r, taskID, r.URL.Query().Get("session_id"), 0) {
		return
	}
	if strings.HasPrefix(actor, "agent:") && options == (desktop.FrameOptions{}) {
		// Existing Agent turns describe screenshot-pixel input. Keep their full
		// size observations unless they explicitly opt into preview dimensions.
		observation, err = s.desktop.Observe(r.Context(), taskID, r.URL.Query().Get("session_id"), actor)
	} else {
		observation, err = s.desktop.ObservePreview(r.Context(), taskID, r.URL.Query().Get("session_id"), actor, options)
	}
	if err != nil {
		desktopError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "observation": observation})
}

func (s *Server) desktopAct(w http.ResponseWriter, r *http.Request) {
	taskID, actor, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	var payload desktop.ActionRequest
	if !desktopJSON(w, r, &payload) {
		return
	}
	if !s.ensureSharedDesktopClaim(w, r, taskID, payload.SessionID, payload.Revision) {
		return
	}
	status, err := s.desktop.Act(r.Context(), taskID, actor, payload)
	if err != nil {
		s.audit(r, "task.desktop.action_failed", "task", taskID, map[string]any{
			"session_id": payload.SessionID, "error": desktop.ErrorCode(err),
		})
		desktopError(w, err)
		return
	}
	s.audit(r, "task.desktop.action", "task", taskID, map[string]any{
		"session_id": payload.SessionID, "kind": payload.Kind, "mode": status.Session.Mode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}
