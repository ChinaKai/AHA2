package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
	"github.com/coder/websocket"
)

func (s *Server) registerWorkspaceTerminalRoutes(mux *http.ServeMux) {
	const base = "/api/v1/tasks/{id}/workspace-terminal"
	mux.Handle("GET "+base, s.withWorkspaceTerminalOwner(s.workspaceTerminalState, false))
	mux.Handle("POST "+base, s.withWorkspaceTerminalOwner(s.workspaceTerminalCreate, false))
	mux.Handle("DELETE "+base+"/{terminal}", s.withWorkspaceTerminalOwner(s.workspaceTerminalDelete, false))
	mux.Handle("GET "+base+"/{terminal}/ws", s.withWorkspaceTerminalOwner(s.workspaceTerminalSocket, true))
	mux.Handle("PUT /api/v1/settings/security/workspace-terminal", s.withAuth(http.HandlerFunc(s.updateWorkspaceTerminalSettings)))
}

func (s *Server) CloseWorkspaceTerminals() {
	s.workspaceTerminals.Close()
}

func (s *Server) withWorkspaceTerminalOwner(handler http.HandlerFunc, requireOrigin bool) http.Handler {
	return s.withOwnerSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A configured terminal origin can also satisfy the origin check for
		// unsafe methods when the proxy rewrites Host.
		checkBeforeCSRF := unsafeMethod(r.Method) && !s.sameOrigin(r)
		if checkBeforeCSRF && !s.checkWorkspaceTerminalOrigin(w, r, requireOrigin) {
			return
		}
		if !requireCSRF(w, r) {
			return
		}
		if r.Header.Get("Authorization") != "" {
			writeError(w, http.StatusForbidden, "workspace_terminal_owner_required")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			s.rejectWorkspaceTerminalOrigin(w, r, "fetch_site")
			return
		}
		if !checkBeforeCSRF && !s.checkWorkspaceTerminalOrigin(w, r, requireOrigin) {
			return
		}
		handler(w, r)
	}))
}

func workspaceTerminalDirectOrigin(r *http.Request) bool {
	if !desktopStreamOrigin(r) {
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

func workspaceTerminalSingleOrigin(r *http.Request) bool {
	return len(r.Header.Values("Origin")) <= 1 && !strings.Contains(r.Header.Get("Origin"), ",")
}

func workspaceTerminalDirectOrNoOrigin(r *http.Request) bool {
	return workspaceTerminalSingleOrigin(r) && (r.Header.Get("Origin") == "" || workspaceTerminalDirectOrigin(r))
}

func (s *Server) checkWorkspaceTerminalOrigin(w http.ResponseWriter, r *http.Request, required bool) bool {
	origin := r.Header.Get("Origin")
	switch {
	case !workspaceTerminalSingleOrigin(r):
		return s.rejectWorkspaceTerminalOrigin(w, r, "multiple_origins")
	case origin == "":
		if required {
			return s.rejectWorkspaceTerminalOrigin(w, r, "missing_origin")
		}
		return true
	case workspaceTerminalDirectOrigin(r):
		return true
	}
	normalized, ok := normalizeWorkspaceTerminalOrigin(origin)
	if !ok {
		return s.rejectWorkspaceTerminalOrigin(w, r, "invalid_origin")
	}
	settings, err := s.store.SecuritySettings(r.Context())
	if err != nil {
		return s.rejectWorkspaceTerminalOrigin(w, r, "external_origin_unavailable")
	}
	if normalized != settings.WorkspaceTerminalExternalOrigin {
		return s.rejectWorkspaceTerminalOrigin(w, r, "origin_mismatch")
	}
	return true
}

var terminalOriginHostname = regexp.MustCompile(`^[a-z0-9_.-]+$`)

func normalizeWorkspaceTerminalOrigin(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" ||
		u.ForceQuery || u.RawQuery != "" || strings.Contains(value, "#") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasPrefix(u.Host, "[") {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is6() || ip.Zone() != "" {
			return "", false
		}
		host = "[" + ip.String() + "]"
	} else if !terminalOriginHostname.MatchString(host) {
		return "", false
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", false
		}
		if !(u.Scheme == "http" && number == 80 || u.Scheme == "https" && number == 443) {
			host += ":" + strconv.FormatUint(number, 10)
		}
	}
	return u.Scheme + "://" + host, true
}

func (s *Server) rejectWorkspaceTerminalOrigin(w http.ResponseWriter, r *http.Request, reason string) bool {
	// Only log origin components so malformed headers cannot inject
	// user information, paths, or query parameters containing credentials.
	origin := r.Header.Get("Origin")
	if origin != "" {
		if normalized, ok := normalizeWorkspaceTerminalOrigin(origin); ok {
			origin = normalized
		} else {
			origin = "<invalid>"
		}
	}
	host := "<invalid>"
	if _, ok := normalizeWorkspaceTerminalOrigin("http://" + r.Host); ok && !strings.Contains(r.Host, "/") {
		host = r.Host
	}
	s.logger.Warn("workspace terminal request rejected", "stage", "origin_rejected",
		"peer", r.RemoteAddr, "origin", origin, "host", host, "reason", reason)
	writeError(w, http.StatusForbidden, "origin_rejected")
	return false
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

var (
	errWorkspaceTerminalAccessRevoked = errors.New("workspace_terminal_access_revoked")
	errWorkspaceTerminalLANLocked     = errors.New("workspace_terminal_lan_locked")
)

type workspaceTerminalCheckError struct{ error }

func (e workspaceTerminalCheckError) Unwrap() error { return e.error }

func (s *Server) workspaceTerminalLogger(r *http.Request, target app.WorkspaceTerminalTarget) *slog.Logger {
	transport := "unresolved"
	if target.WorkspaceID() != "" {
		transport = "local"
		if target.UsesSSH() {
			transport = "ssh"
		}
	}
	return s.logger.With("task", r.PathValue("id"), "workspace", target.WorkspaceID(), "transport", transport)
}

func (s *Server) workspaceTerminalLANCheck(ctx context.Context, r *http.Request) error {
	// A same-host proxy can make both the peer and upstream Host look local.
	if loopbackTerminalRequest(r) && workspaceTerminalDirectOrNoOrigin(r) {
		return nil
	}
	settings, err := s.store.SecuritySettings(ctx)
	if err != nil {
		return workspaceTerminalCheckError{fmt.Errorf("read terminal LAN settings: %w", err)}
	}
	if settings.AccessScope != domain.AccessScopeLAN || !settings.WorkspaceTerminalLAN {
		return errWorkspaceTerminalLANLocked
	}
	return nil
}

func (s *Server) workspaceTerminalAccess(w http.ResponseWriter, r *http.Request) bool {
	if err := s.workspaceTerminalLANCheck(r.Context(), r); err != nil {
		if !errors.Is(err, errWorkspaceTerminalLANLocked) {
			s.workspaceTerminalLogger(r, app.WorkspaceTerminalTarget{}).
				Warn("workspace terminal request failed", "stage", "check_lan", "error", err)
		}
		workspaceTerminalError(w, err)
		return false
	}
	return true
}

func (s *Server) updateWorkspaceTerminalSettings(w http.ResponseWriter, r *http.Request) {
	if !workspaceTerminalDirectOrNoOrigin(r) {
		s.rejectWorkspaceTerminalOrigin(w, r, "direct_origin_required")
		return
	}
	var payload struct {
		Enabled        *bool   `json:"workspace_terminal_lan"`
		ExternalOrigin *string `json:"workspace_terminal_external_origin"`
	}
	if decodeJSON(r, &payload) != nil || payload.Enabled == nil && payload.ExternalOrigin == nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	// Enabling a remote shell requires the same direct loopback origin that
	// can use the terminal while LAN access is locked.
	if payload.Enabled != nil && *payload.Enabled && !loopbackTerminalRequest(r) {
		writeError(w, http.StatusForbidden, "workspace_terminal_lan_enable_requires_loopback")
		return
	}
	if payload.ExternalOrigin != nil {
		value := strings.TrimSpace(*payload.ExternalOrigin)
		if value != "" {
			normalized, ok := normalizeWorkspaceTerminalOrigin(value)
			if !ok {
				writeError(w, http.StatusBadRequest, "workspace_terminal_invalid_external_origin")
				return
			}
			value = normalized
		}
		payload.ExternalOrigin = &value
	}
	settings, err := s.store.UpdateWorkspaceTerminalSettings(r.Context(), payload.Enabled, payload.ExternalOrigin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update_security_settings_failed")
		return
	}
	settings.ValidateOrigin = s.originValidationEnabled()
	settings.StartupOverride = s.originStartupOverride
	if payload.Enabled != nil {
		s.audit(r, "security.workspace_terminal_lan.update", "settings", "security", map[string]any{
			"workspace_terminal_lan": settings.WorkspaceTerminalLAN,
		})
	}
	if payload.ExternalOrigin != nil {
		s.audit(r, "security.workspace_terminal_external_origin.update", "settings", "security", map[string]any{
			"workspace_terminal_external_origin": settings.WorkspaceTerminalExternalOrigin,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"security": settings})
}

func workspaceTerminalError(w http.ResponseWriter, err error) {
	var checkFailed workspaceTerminalCheckError
	switch {
	case errors.As(err, &checkFailed):
		writeError(w, http.StatusServiceUnavailable, "workspace_terminal_check_failed")
	case errors.Is(err, errWorkspaceTerminalAccessRevoked):
		writeError(w, http.StatusForbidden, errWorkspaceTerminalAccessRevoked.Error())
	case errors.Is(err, errWorkspaceTerminalLANLocked):
		writeError(w, http.StatusForbidden, errWorkspaceTerminalLANLocked.Error())
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
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, app.ErrViewerUnavailable) && !errors.Is(err, app.ErrWorkspaceTerminalUnsupported) {
			s.workspaceTerminalLogger(r, target).Warn("workspace terminal request failed", "stage", "resolve_target", "error", err)
		}
		workspaceTerminalError(w, err)
		return app.WorkspaceTerminalTarget{}, false
	}
	return target, true
}

func (s *Server) workspaceTerminalState(w http.ResponseWriter, r *http.Request) {
	if err := s.workspaceTerminalLANCheck(r.Context(), r); errors.Is(err, errWorkspaceTerminalLANLocked) {
		writeJSON(w, http.StatusOK, map[string]any{"locked": true})
		return
	} else if err != nil {
		s.workspaceTerminalLogger(r, app.WorkspaceTerminalTarget{}).
			Warn("workspace terminal request failed", "stage", "check_lan", "error", err)
		workspaceTerminalError(w, err)
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
	scope := target.Scope(login)
	logger := s.workspaceTerminalLogger(r, target)
	stage := "create"
	info, err := s.workspaceTerminals.Create(r.Context(), scope, login.ExpiresAt, size.Initial, func(ctx context.Context) (workspaceterminal.Process, error) {
		stage = "start_local"
		if target.UsesSSH() {
			stage = "start_ssh"
		}
		start, cancel := context.WithTimeout(ctx, 20*time.Second)
		process, err := s.app.StartWorkspaceTerminal(start, target, size.Cols, size.Rows)
		startErr := start.Err()
		cancel()
		if err != nil {
			return nil, err
		}
		if startErr != nil {
			return process, startErr
		}
		process = workspaceterminal.ObserveEarlyExit(process, func(code int64, bytes uint64, err error) {
			logger.Info("workspace terminal shell ended within five seconds", "stage", "shell_exit",
				"exit_code", code, "output_bytes", bytes, "error", err)
		})
		// Keep the shell private until the login and LAN policy are confirmed.
		// A failed check leaves cleanup to Manager.Create.
		stage = "recheck_access"
		check, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if err := s.workspaceTerminalRecheck(check, r, login); err != nil {
			return process, err
		}
		stage = "publish"
		return process, nil
	})
	if err != nil {
		if !errors.Is(err, workspaceterminal.ErrExists) && !errors.Is(err, workspaceterminal.ErrLimit) {
			logger.Warn("workspace terminal request failed", "stage", stage, "error", err)
		}
		workspaceTerminalError(w, err)
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
		s.workspaceTerminalLogger(r, target).Warn("workspace terminal request failed", "stage", "delete", "error", err)
		if errors.Is(err, workspaceterminal.ErrMissing) {
			workspaceTerminalError(w, err)
		} else {
			writeError(w, http.StatusServiceUnavailable, "workspace_terminal_close_failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) workspaceTerminalRecheck(ctx context.Context, r *http.Request, login domain.Session) error {
	current, err := s.store.SessionByTokenHash(ctx, login.TokenHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errWorkspaceTerminalAccessRevoked
	case err != nil:
		return workspaceTerminalCheckError{fmt.Errorf("read terminal login: %w", err)}
	case current.ID != login.ID || !current.RevokedAt.IsZero() || !current.ExpiresAt.After(time.Now()):
		return errWorkspaceTerminalAccessRevoked
	}
	err = s.workspaceTerminalLANCheck(ctx, r)
	if errors.Is(err, errWorkspaceTerminalLANLocked) {
		return fmt.Errorf("%w: %w", errWorkspaceTerminalAccessRevoked, err)
	}
	return err
}

func (s *Server) workspaceTerminalSocket(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceTerminalAccess(w, r) {
		return
	}
	target, ok := s.workspaceTerminalTarget(w, r)
	if !ok {
		return
	}
	login, _ := sessionFromContext(r.Context())
	logger := s.workspaceTerminalLogger(r, target)
	link, err := s.workspaceTerminals.Attach(target.Scope(login), r.PathValue("terminal"))
	if err != nil {
		logger.Warn("workspace terminal request failed", "stage", "attach", "error", err)
		workspaceTerminalError(w, err)
		return
	}
	defer s.workspaceTerminals.Detach(link)
	// The route already requires a single direct or configured Origin.
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		// Accept errors can quote raw handshake headers, including the key.
		logger.Warn("workspace terminal request failed", "stage", "upgrade", "error", "WebSocket handshake rejected")
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
			if err := s.workspaceTerminalLANCheck(ctx, r); err != nil {
				if ctx.Err() == nil {
					logger.Warn("workspace terminal input stopped", "stage", "input_check_lan", "error", err)
				}
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
				logger.Debug("workspace terminal input stopped", "stage", "input", "error", err)
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
				err := s.workspaceTerminalRecheck(check, r, login)
				stop()
				if err != nil {
					if ctx.Err() == nil {
						logger.Warn("workspace terminal socket closed", "stage", "socket_recheck_access", "error", err)
					}
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
