package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
)

func (s *Server) registerBrowserRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/browser/host", s.withAuth(http.HandlerFunc(s.browserHostHealth)))
	mux.Handle("GET /api/v1/browser/profiles", s.withAuth(http.HandlerFunc(s.listBrowserProfiles)))
	mux.Handle("POST /api/v1/browser/profiles", s.withAuth(http.HandlerFunc(s.createBrowserProfile)))
	mux.Handle("GET /api/v1/browser/profiles/{id}", s.withAuth(http.HandlerFunc(s.browserProfileDetail)))
	mux.Handle("PATCH /api/v1/browser/profiles/{id}", s.withAuth(http.HandlerFunc(s.renameBrowserProfile)))
	mux.Handle("DELETE /api/v1/browser/profiles/{id}", s.withAuth(http.HandlerFunc(s.deleteBrowserProfile)))
	mux.Handle("POST /api/v1/browser/profiles/{id}/login-window", s.withAuth(http.HandlerFunc(s.openBrowserLoginWindow)))
	mux.Handle("GET /api/v1/browser/profiles/{id}/session", s.withAuth(http.HandlerFunc(s.browserProfileSession)))
	mux.Handle("DELETE /api/v1/browser/profiles/{id}/session", s.withAuth(http.HandlerFunc(s.closeBrowserProfileSession)))
	mux.Handle("POST /api/v1/browser-sessions/{id}/close", s.withAuth(http.HandlerFunc(s.closeBrowserSession)))

	mux.Handle("GET /api/v1/tasks/{id}/browser", s.withAuth(http.HandlerFunc(s.taskBrowser)))
	mux.Handle("GET /api/v1/tasks/{id}/browser-sessions", s.withAuth(http.HandlerFunc(s.listTaskBrowserGrants)))
	mux.Handle("POST /api/v1/tasks/{id}/browser-sessions", s.withAuth(http.HandlerFunc(s.createTaskBrowserGrant)))
	mux.Handle("GET /api/v1/tasks/{id}/browser-grants", s.withAuth(http.HandlerFunc(s.listTaskBrowserGrants)))
	mux.Handle("POST /api/v1/tasks/{id}/browser-grants", s.withAuth(http.HandlerFunc(s.createTaskBrowserGrant)))
	mux.Handle("GET /api/v1/browser-grants/{id}", s.withAuth(http.HandlerFunc(s.browserGrantDetail)))
	mux.Handle("DELETE /api/v1/browser-grants/{id}", s.withAuth(http.HandlerFunc(s.revokeBrowserGrant)))
	mux.Handle("GET /api/v1/browser-grants/{id}/tabs", s.withAuth(http.HandlerFunc(s.listBrowserGrantTabs)))
	mux.Handle("POST /api/v1/browser-grants/{id}/tabs", s.withAuth(http.HandlerFunc(s.openBrowserTab)))
	mux.Handle("DELETE /api/v1/browser-grants/{id}/tabs/{tab}", s.withAuth(http.HandlerFunc(s.closeBrowserTab)))
	mux.Handle("POST /api/v1/browser-grants/{id}/tabs/{tab}/navigate", s.withAuth(http.HandlerFunc(s.navigateBrowserTab)))
	mux.Handle("GET /api/v1/browser-grants/{id}/tabs/{tab}/snapshot", s.withAuth(http.HandlerFunc(s.snapshotBrowserTab)))
}

func (s *Server) browserServiceOrError(writer http.ResponseWriter) *browser.Service {
	if s.browser == nil {
		writeBrowserFailure(writer, newUnavailable())
		return nil
	}
	return s.browser
}

func newUnavailable() *browser.Error {
	return &browser.Error{Code: "browser_unavailable", Message: "共享浏览器未初始化", Status: http.StatusServiceUnavailable}
}

func writeBrowserFailure(writer http.ResponseWriter, err error) {
	var typed *browser.Error
	if errors.As(err, &typed) {
		writeErrorDetail(writer, typed.Status, typed.Code, typed.Message)
		return
	}
	writeError(writer, http.StatusInternalServerError, "browser_operation_failed")
}

func browserActor(request *http.Request) string {
	if session, ok := sessionFromContext(request.Context()); ok {
		return session.OwnerID
	}
	if claims, ok := agentClaimsFromContext(request.Context()); ok {
		return "agent:" + claims.AgentID
	}
	return "system"
}

func (s *Server) browserHostHealth(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "host": service.HostStatus(request.Context()),
	})
}

func (s *Server) listBrowserProfiles(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	profiles, err := service.Profiles(request.Context())
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "profiles": profiles})
}

func (s *Server) createBrowserProfile(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload struct {
		Name        string `json:"name"`
		BrowserKind string `json:"browser_kind"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	profile, err := service.CreateProfile(request.Context(), browserActor(request), payload.Name, payload.BrowserKind)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "profile": profile})
}

func (s *Server) browserProfileDetail(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	profile, err := service.Profile(request.Context(), request.PathValue("id"), true)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "profile": profile})
}

func (s *Server) renameBrowserProfile(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	profile, err := service.RenameProfile(request.Context(), browserActor(request), request.PathValue("id"), payload.Name)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "profile": profile})
}

func (s *Server) deleteBrowserProfile(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	// This mirrors the existing owner-confirmation resource deletion semantics:
	// the Web UI asks before sending, and the server rejects a request that
	// skipped the explicit confirmation plus any deletion of an in-use profile.
	confirm := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("confirm")))
	if confirm != "1" && confirm != "true" {
		writeErrorDetail(writer, http.StatusBadRequest, "browser_profile_delete_confirmation_required",
			"删除浏览器 Profile 需要 Owner 明确确认")
		return
	}
	if err := service.DeleteProfile(request.Context(), browserActor(request), request.PathValue("id")); err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) openBrowserLoginWindow(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	session, reused, err := service.StartSession(request.Context(), browserActor(request), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "session": session, "reused": reused})
}

func (s *Server) browserProfileSession(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	session, err := service.SessionStatus(request.Context(), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (s *Server) closeBrowserProfileSession(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	session, err := service.CloseProfileSession(request.Context(), browserActor(request), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (s *Server) closeBrowserSession(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	session, err := service.CloseSession(request.Context(), browserActor(request), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (s *Server) taskBrowser(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	state, err := service.TaskBrowserState(request.Context(), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "browser": state})
}

func (s *Server) listTaskBrowserGrants(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	grants, err := service.Grants(request.Context(), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "grants": grants})
}

func (s *Server) createTaskBrowserGrant(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload struct {
		ProfileID     string   `json:"profile_id"`
		Capabilities  []string `json:"capabilities"`
		ExpiresInSecs int      `json:"expires_in_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	ttl := browser.DefaultGrantTTL
	if payload.ExpiresInSecs < 0 {
		writeErrorDetail(writer, http.StatusBadRequest, "browser_grant_invalid", "授权有效期不能为负数")
		return
	}
	if payload.ExpiresInSecs > 0 {
		ttl = time.Duration(payload.ExpiresInSecs) * time.Second
	}
	grant, err := service.CreateGrant(request.Context(), browserActor(request), request.PathValue("id"),
		payload.ProfileID, payload.Capabilities, ttl)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ok": true, "grant": grant, "session": grant.Session,
	})
}

func (s *Server) browserGrantDetail(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	grant, err := service.Grant(request.Context(), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "grant": grant})
}

func (s *Server) revokeBrowserGrant(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	grant, err := service.RevokeGrant(request.Context(), browserActor(request), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "grant": grant})
}

func (s *Server) listBrowserGrantTabs(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	tabs, err := service.Tabs(request.Context(), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tabs": tabs})
}

func (s *Server) openBrowserTab(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	tab, err := service.OpenTab(request.Context(), browserActor(request), request.PathValue("id"), payload.URL)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) navigateBrowserTab(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	tab, err := service.NavigateTab(request.Context(), browserActor(request),
		request.PathValue("id"), request.PathValue("tab"), payload.URL)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) snapshotBrowserTab(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	snapshot, err := service.SnapshotTab(request.Context(), browserActor(request),
		request.PathValue("id"), request.PathValue("tab"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "snapshot": snapshot})
}

func (s *Server) closeBrowserTab(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	if err := service.CloseTab(request.Context(), browserActor(request),
		request.PathValue("id"), request.PathValue("tab")); err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
