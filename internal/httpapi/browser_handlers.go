package httpapi

import (
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/domain"
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
	mux.Handle("POST /api/v1/browser-grants/{id}/tabs/current", s.withAuth(http.HandlerFunc(s.adoptBrowserCurrentTab)))
	mux.Handle("DELETE /api/v1/browser-grants/{id}/tabs/{tab}", s.withAuth(http.HandlerFunc(s.closeBrowserTab)))
	mux.Handle("POST /api/v1/browser-grants/{id}/tabs/{tab}/activate", s.withAuth(http.HandlerFunc(s.activateBrowserTab)))
	mux.Handle("POST /api/v1/browser-grants/{id}/tabs/{tab}/navigate", s.withAuth(http.HandlerFunc(s.navigateBrowserTab)))
	mux.Handle("GET /api/v1/browser-grants/{id}/tabs/{tab}/snapshot", s.withAuth(http.HandlerFunc(s.snapshotBrowserTab)))
	mux.Handle("GET /api/v1/browser-grants/{id}/tabs/{tab}/frame", s.withAuth(http.HandlerFunc(s.browserTabFrame)))
	// Owner Web input: clicks, typing, keys, scrolling, right-click and drag.
	// It shares the Agent action endpoint's grant/lease/fence/input-slot path
	// and adds an explicit owner actor/source in the audit trail.
	mux.Handle("POST /api/v1/browser-grants/{id}/tabs/{tab}/actions", s.withAuth(http.HandlerFunc(s.ownerBrowserAction)))
	mux.Handle("GET /api/v1/browser-grants/{id}/bookmarks", s.withAuth(http.HandlerFunc(s.browserGrantBookmarks)))
	mux.Handle("POST /api/v1/browser/profiles/{id}/transfer", s.withAuth(http.HandlerFunc(s.transferBrowserProfile)))
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
		writeErrorDetail(writer, typed.Status, typed.Code, browser.PublicMessage(typed))
		return
	}
	writeError(writer, http.StatusInternalServerError, "browser_operation_failed")
}

func browserActor(request *http.Request) string {
	if session, ok := sessionFromContext(request.Context()); ok {
		return session.OwnerID
	}
	if claims, ok := agentClaimsFromContext(request.Context()); ok {
		actor := "agent:" + claims.AgentID
		if claims.TurnID != "" {
			actor += "@" + claims.TurnID
		}
		return actor
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
	// The Owner switch grants continuous control. expires_in_seconds is still
	// accepted from older callers, but omitting it means no expiry.
	ttl := time.Duration(0)
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

// adoptBrowserCurrentTab returns the page the Owner is looking at after syncing
// every visible page into the Task's exclusive tab list. It no longer creates a
// per-Task tab binding; the tab already exists in the Profile.
func (s *Server) adoptBrowserCurrentTab(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	tab, err := service.CurrentTab(request.Context(), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) activateBrowserTab(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	tab, err := service.ActivateTab(request.Context(), browserActor(request),
		request.PathValue("id"), request.PathValue("tab"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) browserGrantBookmarks(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	bookmarks, err := service.Bookmarks(request.Context(), browserActor(request), request.PathValue("id"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "bookmarks": bookmarks})
}

// transferBrowserProfile is an Owner-only action: it moves the Profile's
// exclusive control to a target Task. A Task capability cannot call this route.
func (s *Server) transferBrowserProfile(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload struct {
		TaskID        string   `json:"task_id"`
		Capabilities  []string `json:"capabilities"`
		ExpiresInSecs int      `json:"expires_in_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	// The Owner switch grants continuous control. expires_in_seconds is still
	// accepted from older callers, but omitting it means no expiry.
	ttl := time.Duration(0)
	if payload.ExpiresInSecs < 0 {
		writeErrorDetail(writer, http.StatusBadRequest, "browser_grant_invalid", "授权有效期不能为负数")
		return
	}
	if payload.ExpiresInSecs > 0 {
		ttl = time.Duration(payload.ExpiresInSecs) * time.Second
	}
	grant, err := service.TransferProfileControl(request.Context(), browserActor(request),
		request.PathValue("id"), payload.TaskID, payload.Capabilities, ttl)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "grant": grant, "session": grant.Session})
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

// browserTabFrame streams one read-only preview image. Frames are not audited,
// not cached by the browser and never written to disk; the client stops
// polling when the owner closes the panel.
func (s *Server) browserTabFrame(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	frame, err := service.TabFrame(request.Context(), browserActor(request),
		request.PathValue("id"), request.PathValue("tab"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeBrowserFrame(writer, frame)
}

// browserActionPayload is shared by the Owner Web route and the Agent route so
// both validate exactly the same wire shape. Which action kinds are accepted is
// decided by the service (Agent vs Owner), not by the decoder.
type browserActionPayload struct {
	Kind   string   `json:"kind"`
	Ref    string   `json:"ref"`
	X      *int     `json:"x"`
	Y      *int     `json:"y"`
	ToX    *int     `json:"to_x"`
	ToY    *int     `json:"to_y"`
	Text   string   `json:"text"`
	Keys   []string `json:"keys"`
	DeltaX int      `json:"delta_x"`
	DeltaY int      `json:"delta_y"`
}

func (p browserActionPayload) action() browser.HostAction {
	return browser.HostAction{
		Kind: p.Kind, Ref: p.Ref, X: p.X, Y: p.Y, ToX: p.ToX, ToY: p.ToY,
		Text: p.Text, Keys: p.Keys, DeltaX: p.DeltaX, DeltaY: p.DeltaY,
	}
}

func (s *Server) ownerBrowserAction(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	var payload browserActionPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := service.ActOwner(request.Context(), browserActor(request),
		request.PathValue("id"), request.PathValue("tab"), payload.action())
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "action": result})
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

// --- Task-scoped Agent browser API ---------------------------------------
//
// Agent capabilities are bound to one Task/Turn. Every grant-addressed handler
// re-checks grant.TaskID so a capability from Task A can never read or control
// Task B's page, even if it learns the grant id.

func (s *Server) agentBrowserState(writer http.ResponseWriter, request *http.Request) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok || claims.TaskID == "" {
		writeError(writer, http.StatusUnauthorized, "agent_capability_invalid")
		return
	}
	state, err := service.AgentBrowserState(request.Context(), claims.TaskID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "browser": state})
}

func (s *Server) agentBrowserGrantTarget(writer http.ResponseWriter, request *http.Request) (*browser.Service, browser.GrantView, bool) {
	service := s.browserServiceOrError(writer)
	if service == nil {
		return nil, browser.GrantView{}, false
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok || claims.TaskID == "" {
		writeError(writer, http.StatusUnauthorized, "agent_capability_invalid")
		return nil, browser.GrantView{}, false
	}
	grant, err := service.Grant(request.Context(), request.PathValue("grant"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return nil, browser.GrantView{}, false
	}
	if accessErr := agentBrowserGrantAccessError(grant, claims.TaskID); accessErr != nil {
		writeBrowserFailure(writer, accessErr)
		return nil, browser.GrantView{}, false
	}
	return service, grant, true
}

// agentBrowserGrantAccessError is the shared authorization check for every
// grant-addressed Agent surface (HTTP and MCP): a grant from another Task is
// forbidden, and a revoked or expired grant fails closed without a retry path.
func agentBrowserGrantAccessError(grant browser.GrantView, taskID string) *browser.Error {
	if grant.TaskID != taskID {
		return &browser.Error{
			Code: "browser_grant_forbidden", Message: "该浏览器授权不属于当前 Task",
			Status: http.StatusForbidden,
		}
	}
	switch grant.Status {
	case domain.BrowserGrantStatusRevoked:
		return &browser.Error{
			Code: "browser_grant_revoked", Message: "浏览器授权已撤销",
			Status: http.StatusForbidden,
		}
	case domain.BrowserGrantStatusExpired:
		return &browser.Error{
			Code: "browser_grant_expired", Message: "浏览器授权已过期",
			Status: http.StatusForbidden,
		}
	}
	return nil
}

func (s *Server) agentBrowserTabs(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	tabs, err := service.Tabs(request.Context(), grant.ID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tabs": tabs})
}

func (s *Server) agentBrowserOpenTab(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	tab, err := service.OpenTab(request.Context(), browserActor(request), grant.ID, payload.URL)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) agentBrowserBindCurrent(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	tab, err := service.CurrentTab(request.Context(), grant.ID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) agentBrowserActivate(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	tab, err := service.ActivateTab(request.Context(), browserActor(request), grant.ID,
		request.PathValue("tab"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) agentBrowserRelease(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	released, err := service.RevokeGrant(request.Context(), browserActor(request), grant.ID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "grant": released})
}

func (s *Server) agentBrowserBookmarks(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	bookmarks, err := service.Bookmarks(request.Context(), browserActor(request), grant.ID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "bookmarks": bookmarks})
}

func (s *Server) agentBrowserNavigate(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	tab, err := service.NavigateTab(request.Context(), browserActor(request), grant.ID,
		request.PathValue("tab"), payload.URL)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tab": tab})
}

func (s *Server) agentBrowserSnapshot(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	snapshot, err := service.SnapshotTab(request.Context(), browserActor(request), grant.ID,
		request.PathValue("tab"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "snapshot": snapshot})
}

func (s *Server) agentBrowserFrame(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	frame, err := service.TabFrame(request.Context(), browserActor(request), grant.ID,
		request.PathValue("tab"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeBrowserFrame(writer, frame)
}

func (s *Server) agentBrowserCloseTab(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	if err := service.CloseTab(request.Context(), browserActor(request), grant.ID,
		request.PathValue("tab")); err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) agentBrowserAction(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	var payload browserActionPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := service.Act(request.Context(), browserActor(request), grant.ID, request.PathValue("tab"),
		payload.action())
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "action": result})
}

// agentBrowserUpload accepts exactly one request body containing Agent-supplied
// bytes. A host path is never a field: bytes arrive as base64 JSON or one
// multipart file and are handed to the page through the host.
func (s *Server) agentBrowserUpload(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	if !grant.Has(domain.BrowserCapabilityFiles) {
		// Reject before parsing up to 32 MiB of upload body.
		writeErrorDetail(writer, http.StatusForbidden, "browser_capability_denied",
			"当前授权不包含能力：files")
		return
	}
	const maxBody = 32 << 20
	var (
		ref   string
		x, y  *int
		files []browser.UploadFile
	)
	if strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "multipart/form-data") {
		request.Body = http.MaxBytesReader(writer, request.Body, maxBody)
		if err := request.ParseMultipartForm(maxBody); err != nil {
			var tooLarge *http.MaxBytesError
			if !errors.As(err, &tooLarge) {
				writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "multipart 格式无效")
				return
			}
			writeErrorDetail(writer, http.StatusRequestEntityTooLarge, "browser_upload_invalid",
				"上传内容超过大小上限")
			return
		}
		if request.MultipartForm != nil {
			defer request.MultipartForm.RemoveAll()
		}
		ref = strings.TrimSpace(request.FormValue("ref"))
		var err error
		if x, err = optionalFormInt(request.FormValue("x")); err != nil {
			writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "坐标 x 无效")
			return
		}
		if y, err = optionalFormInt(request.FormValue("y")); err != nil {
			writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "坐标 y 无效")
			return
		}
		headers := request.MultipartForm.File["file"]
		if len(headers) != 1 {
			writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "请上传且只上传一个文件")
			return
		}
		file, err := headers[0].Open()
		if err != nil {
			writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "无法读取上传文件")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, browser.MaxUploadFileBytes+1))
		if err != nil {
			writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "无法读取上传文件")
			return
		}
		files = []browser.UploadFile{{Filename: headers[0].Filename, Data: data}}
	} else {
		request.Body = http.MaxBytesReader(writer, request.Body, maxBody)
		var payload struct {
			Ref   string `json:"ref"`
			X     *int   `json:"x"`
			Y     *int   `json:"y"`
			Files []struct {
				Name          string `json:"name"`
				ContentBase64 string `json:"content_base64"`
			} `json:"files"`
		}
		if err := decodeJSONLimit(request, &payload, maxBody); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeErrorDetail(writer, http.StatusRequestEntityTooLarge, "browser_upload_invalid",
					"上传内容超过大小上限")
				return
			}
			writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		ref, x, y = payload.Ref, payload.X, payload.Y
		for _, item := range payload.Files {
			data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(item.ContentBase64))
			if err != nil {
				writeErrorDetail(writer, http.StatusBadRequest, "browser_upload_invalid", "文件内容不是有效的 base64")
				return
			}
			files = append(files, browser.UploadFile{Filename: item.Name, Data: data})
		}
	}
	result, err := service.Upload(request.Context(), browserActor(request), grant.ID,
		request.PathValue("tab"), ref, x, y, files)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "upload": result})
}

func optionalFormInt(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Server) agentBrowserDownloads(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	downloads, err := service.Downloads(request.Context(), browserActor(request), grant.ID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "downloads": downloads})
}

func (s *Server) agentBrowserDownload(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	file, err := service.Download(request.Context(), browserActor(request), grant.ID,
		request.PathValue("download"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	if file.Body != nil {
		// The handler owns the host stream on every path, including a client
		// disconnect that cancels the copy.
		defer file.Body.Close()
	}
	filename := file.Download.Filename
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disposition == "" {
		disposition = "attachment"
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Disposition", disposition)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	// A known size lets the client show progress and detect a truncated
	// transfer. An unknown size is streamed chunked rather than buffered.
	if file.Download.Size >= 0 {
		writer.Header().Set("Content-Length", strconv.FormatInt(file.Download.Size, 10))
	}
	writer.WriteHeader(http.StatusOK)
	if file.Body == nil {
		return
	}
	if _, copyErr := io.Copy(writer, file.Body); copyErr != nil {
		// The status line and headers are already committed, so a JSON error
		// would corrupt the file. Abort the connection instead: net/http
		// suppresses this sentinel and the client sees a truncated transfer.
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) agentBrowserCookies(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	cookies, truncated, err := service.ExportCookies(request.Context(), browserActor(request), grant.ID,
		request.URL.Query().Get("url"), request.URL.Query().Get("domain"))
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "cookies": cookies, "count": len(cookies), "truncated": truncated,
	})
}

func (s *Server) agentBrowserImportCookies(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	if !grant.Has(domain.BrowserCapabilityCookies) {
		writeErrorDetail(writer, http.StatusForbidden, "browser_capability_denied",
			"当前授权不包含能力：cookies")
		return
	}
	var payload struct {
		Cookies []browser.HostCookie `json:"cookies"`
	}
	// Up to 300 cookies with 4 KiB values can exceed the default 1 MiB body.
	request.Body = http.MaxBytesReader(writer, request.Body, 2<<20)
	if err := decodeJSONLimit(request, &payload, 2<<20); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	count, err := service.ImportCookies(request.Context(), browserActor(request), grant.ID, payload.Cookies)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "imported": count})
}

func writeBrowserFrame(writer http.ResponseWriter, frame browser.HostFrame) {
	writer.Header().Set("Content-Type", frame.ContentType)
	// The CSS viewport that belongs to this image. The Web panel multiplies the
	// click position by viewport/image size, so DPI scaling and object-fit
	// letterboxing never change what the owner is aiming at.
	if frame.Width > 0 && frame.Height > 0 {
		writer.Header().Set("X-AHA2-Viewport-Width", strconv.Itoa(frame.Width))
		writer.Header().Set("X-AHA2-Viewport-Height", strconv.Itoa(frame.Height))
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", strconv.Itoa(len(frame.Data)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(frame.Data)
}
