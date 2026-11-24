package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
	"github.com/coder/websocket"
)

func (s *Server) registerWorkspaceTerminalRoutes(mux *http.ServeMux) {
	const base = "/api/v1/tasks/{id}/workspace-terminal"
	mux.Handle("GET "+base, s.withWorkspaceTerminalOwner(s.workspaceTerminalState))
	mux.Handle("POST "+base, s.withWorkspaceTerminalOwner(s.workspaceTerminalCreate))
	mux.Handle("DELETE "+base+"/{terminal}", s.withWorkspaceTerminalOwner(s.workspaceTerminalDelete))
	mux.Handle("GET "+base+"/{terminal}/ws", s.withWorkspaceTerminalOwner(s.workspaceTerminalSocket))
	mux.Handle("PUT /api/v1/settings/security/workspace-terminal", s.withWorkspaceTerminalOwner(s.updateWorkspaceTerminalLAN))
}

func (s *Server) CloseWorkspaceTerminals() {
	s.workspaceTerminals.Close()
}

func (s *Server) withWorkspaceTerminalOwner(handler http.HandlerFunc) http.Handler {
	return s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			writeError(w, http.StatusForbidden, "workspace_terminal_owner_required")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); (site != "" && site != "same-origin" && site != "none") ||
			(r.Header.Get("Origin") != "" && !workspaceTerminalOrigin(r)) {
			writeError(w, http.StatusForbidden, "origin_rejected")
			return
		}
		handler(w, r)
	}))
}

func workspaceTerminalOrigin(r *http.Request) bool {
	if len(r.Header.Values("Origin")) != 1 || !desktopStreamOrigin(r) {
		return false
	}
	origin, _ := url.Parse(r.Header.Get("Origin"))
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	scheme := "http"
	if r.TLS != nil || net.ParseIP(peer).IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return !origin.ForceQuery && origin.Scheme == scheme
}

func loopbackTerminalRequest(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	// A local reverse proxy can hide the browser's peer address. Treat
	// non-loopback Hosts or forwarded clients as LAN rather than trusting them.
	if !terminalLoopbackHost(r.Host) || r.Header.Get("Forwarded") != "" {
		return false
	}
	for _, host := range r.Header.Values("X-Forwarded-Host") {
		if !terminalLoopbackHost(host) {
			return false
		}
	}
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		for _, address := range r.Header.Values(header) {
			for _, ip := range strings.Split(address, ",") {
				if !net.ParseIP(strings.TrimSpace(ip)).IsLoopback() {
					return false
				}
			}
		}
	}
	return true
}

func terminalLoopbackHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(strings.Trim(host, "[]")).IsLoopback()
}

func (s *Server) workspaceTerminalLANAllowed(ctx context.Context, r *http.Request) bool {
	if loopbackTerminalRequest(r) {
		return true
	}
	settings, err := s.store.SecuritySettings(ctx)
	return err == nil && settings.AccessScope == domain.AccessScopeLAN && settings.WorkspaceTerminalLAN
}

func (s *Server) workspaceTerminalAccess(w http.ResponseWriter, r *http.Request) bool {
	if !s.workspaceTerminalLANAllowed(r.Context(), r) {
		writeError(w, http.StatusForbidden, "workspace_terminal_lan_locked")
		return false
	}
	return true
}

func (s *Server) updateWorkspaceTerminalLAN(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Enabled *bool `json:"workspace_terminal_lan"`
	}
	if decodeJSON(r, &payload) != nil || payload.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	settings, err := s.store.UpdateWorkspaceTerminalLAN(r.Context(), *payload.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update_security_settings_failed")
		return
	}
	settings.ValidateOrigin = s.originValidationEnabled()
	settings.StartupOverride = s.originStartupOverride
	s.audit(r, "security.workspace_terminal_lan.update", "settings", "security", map[string]any{
		"workspace_terminal_lan": settings.WorkspaceTerminalLAN,
	})
	writeJSON(w, http.StatusOK, map[string]any{"security": settings})
}

func workspaceTerminalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "task_not_found")
	case errors.Is(err, app.ErrViewerUnavailable):
		writeError(w, http.StatusNotFound, "workspace_terminal_unavailable")
	case errors.Is(err, app.ErrWorkspaceTerminalUnsupported):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, workspaceterminal.ErrLimit), errors.Is(err, workspaceterminal.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, workspaceterminal.ErrMissing):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "workspace_terminal_start_failed")
	}
}

func (s *Server) workspaceTerminalTarget(w http.ResponseWriter, r *http.Request) (app.WorkspaceTerminalTarget, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["workspace"]) > 1 || len(query) > 1 || len(query) == 1 && !query.Has("workspace") {
		writeError(w, http.StatusBadRequest, "workspace_terminal_invalid_target")
		return app.WorkspaceTerminalTarget{}, false
	}
	target, err := s.app.WorkspaceTerminalTarget(r.Context(), r.PathValue("id"), query.Get("workspace"))
	if err != nil {
		workspaceTerminalError(w, err)
		return app.WorkspaceTerminalTarget{}, false
	}
	return target, true
}

func (s *Server) workspaceTerminalState(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceTerminalLANAllowed(r.Context(), r) {
		writeJSON(w, http.StatusOK, map[string]any{"locked": true})
		return
	}
	target, ok := s.workspaceTerminalTarget(w, r)
	if !ok {
		return
	}
	login, _ := sessionFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": s.workspaceTerminals.List(target.Scope(login)), "cwd": target.Directory(), "ssh": target.UsesSSH(),
	})
}

func validTerminalSize(cols, rows int) bool {
	return cols >= 2 && cols <= 500 && rows >= 1 && rows <= 250
}

func (s *Server) workspaceTerminalCreate(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceTerminalAccess(w, r) {
		return
	}
	var size struct {
		Cols    int  `json:"cols"`
		Rows    int  `json:"rows"`
		Initial bool `json:"initial"`
	}
	if decodeJSONLimit(r, &size, 1024) != nil || !validTerminalSize(size.Cols, size.Rows) {
		writeError(w, http.StatusBadRequest, "workspace_terminal_invalid_size")
		return
	}
	target, ok := s.workspaceTerminalTarget(w, r)
	if !ok {
		return
	}
	login, _ := sessionFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	scope := target.Scope(login)
	info, err := s.workspaceTerminals.Create(ctx, scope, login.ExpiresAt, size.Initial, func(ctx context.Context) (workspaceterminal.Process, error) {
		return s.app.StartWorkspaceTerminal(ctx, target, size.Cols, size.Rows)
	})
	if err != nil {
		workspaceTerminalError(w, err)
		return
	}
	// Logout or an access change may have raced the SSH setup.
	allowed, cancelCheck := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancelCheck()
	if !s.workspaceTerminalLoginValid(allowed, login) || !s.workspaceTerminalLANAllowed(allowed, r) {
		_ = s.workspaceTerminals.Delete(scope, info.ID)
		writeError(w, http.StatusForbidden, "workspace_terminal_access_revoked")
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) workspaceTerminalDelete(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceTerminalAccess(w, r) {
		return
	}
	target, ok := s.workspaceTerminalTarget(w, r)
	if !ok {
		return
	}
	login, _ := sessionFromContext(r.Context())
	if err := s.workspaceTerminals.Delete(target.Scope(login), r.PathValue("terminal")); err != nil {
		workspaceTerminalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) workspaceTerminalLoginValid(ctx context.Context, login domain.Session) bool {
	current, err := s.store.SessionByTokenHash(ctx, login.TokenHash)
	return err == nil && current.ID == login.ID && current.RevokedAt.IsZero() && current.ExpiresAt.After(time.Now())
}

func (s *Server) workspaceTerminalSocket(w http.ResponseWriter, r *http.Request) {
	if !workspaceTerminalOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_rejected")
		return
	}
	if !s.workspaceTerminalAccess(w, r) {
		return
	}
	target, ok := s.workspaceTerminalTarget(w, r)
	if !ok {
		return
	}
	login, _ := sessionFromContext(r.Context())
	link, err := s.workspaceTerminals.Attach(target.Scope(login), r.PathValue("terminal"))
	if err != nil {
		workspaceTerminalError(w, err)
		return
	}
	defer s.workspaceTerminals.Detach(link)
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		defer cancel()
		for {
			kind, data, err := connection.Read(ctx)
			if err != nil {
				return
			}
			if !s.workspaceTerminalLANAllowed(ctx, r) {
				return
			}
			switch kind {
			case websocket.MessageBinary:
				if len(data) == 0 {
					continue
				}
				err = s.workspaceTerminals.Input(link, data, 0, 0)
			case websocket.MessageText:
				err = s.resizeWorkspaceTerminal(link, data)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(ctx, 2*time.Second)
				allowed := s.workspaceTerminalLoginValid(check, login) && s.workspaceTerminalLANAllowed(check, r)
				stop()
				if !allowed {
					cancel()
					return
				}
			case <-ping.C:
				check, stop := context.WithTimeout(ctx, 3*time.Second)
				err := connection.Ping(check)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	for cursor := uint64(0); ; {
		if ctx.Err() != nil || !login.ExpiresAt.After(time.Now()) {
			return
		}
		data, end, changed, closed := link.Session.Output(cursor)
		if len(data) != 0 {
			writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			err := connection.Write(writeCtx, websocket.MessageBinary, data)
			stop()
			if err != nil {
				return
			}
			cursor = end
			continue
		}
		if closed {
			_ = connection.Close(websocket.StatusNormalClosure, "terminal ended")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-link.Session.Done():
		}
	}
}

func (s *Server) resizeWorkspaceTerminal(link *workspaceterminal.Attachment, data []byte) error {
	var size struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&size); err != nil || decoder.Decode(new(any)) != io.EOF ||
		!validTerminalSize(size.Cols, size.Rows) {
		return errors.New("invalid terminal size")
	}
	return s.workspaceTerminals.Input(link, nil, size.Cols, size.Rows)
}
