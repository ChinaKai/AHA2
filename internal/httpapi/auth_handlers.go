package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/auth"
)

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "service": "aha2"})
}

func (s *Server) authStatus(writer http.ResponseWriter, request *http.Request) {
	registrationOpen, err := s.auth.RegistrationOpen(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "auth_status_failed")
		return
	}
	result := map[string]any{"ok": true, "registration_open": registrationOpen, "authenticated": false}
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		if session, err := s.auth.Authenticate(request.Context(), cookie.Value); err == nil {
			result["authenticated"] = true
			result["owner_id"] = session.OwnerID
			result["csrf_token"] = session.CSRFToken
			if owner, err := s.store.OwnerByID(request.Context(), session.OwnerID); err == nil {
				result["username"] = owner.Username
			}
		}
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) authRegister(writer http.ResponseWriter, request *http.Request) {
	clientKey, allowed := enforceAuthRateLimit(writer, request, s.authLimiter)
	if !allowed {
		return
	}
	if !s.sameOrigin(request) {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusForbidden, "origin_rejected")
		return
	}
	var payload struct {
		SetupToken string `json:"setup_token"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	setupToken := strings.TrimSpace(request.Header.Get("X-Setup-Token"))
	if setupToken == "" {
		setupToken = payload.SetupToken
	}
	result, err := s.auth.Register(request.Context(), setupToken, payload.Username, payload.Password)
	if err != nil {
		s.authLimiter.failure(clientKey)
		switch {
		case errors.Is(err, auth.ErrOwnerExists):
			writeError(writer, http.StatusConflict, "owner_exists")
		case errors.Is(err, auth.ErrInvalidSetupToken):
			writeError(writer, http.StatusForbidden, "invalid_setup_token")
		default:
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "registration_failed", "message": err.Error()})
		}
		return
	}
	s.authLimiter.success(clientKey)
	setSessionCookie(writer, result.SessionToken, result.Session.ExpiresAt, s.secureCookie)
	_ = s.store.AppendAudit(request.Context(), result.Owner.ID, "owner.register", "owner", result.Owner.ID, nil, result.Owner.CreatedAt.Format(time.RFC3339Nano))
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ok": true, "owner": result.Owner, "csrf_token": result.Session.CSRFToken,
	})
}

func (s *Server) authLogin(writer http.ResponseWriter, request *http.Request) {
	clientKey, allowed := enforceAuthRateLimit(writer, request, s.authLimiter)
	if !allowed {
		return
	}
	if !s.sameOrigin(request) {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusForbidden, "origin_rejected")
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	result, err := s.auth.Login(request.Context(), payload.Username, payload.Password)
	if err != nil {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	s.authLimiter.success(clientKey)
	setSessionCookie(writer, result.SessionToken, result.Session.ExpiresAt, s.secureCookie)
	_ = s.store.AppendAudit(request.Context(), result.Owner.ID, "owner.login", "owner", result.Owner.ID, nil, result.Owner.LastLoginAt.Format(time.RFC3339Nano))
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "owner": result.Owner, "csrf_token": result.Session.CSRFToken,
	})
}

func (s *Server) authLogout(writer http.ResponseWriter, request *http.Request) {
	session, _ := sessionFromContext(request.Context())
	_ = s.auth.Logout(request.Context(), session.ID)
	clearSessionCookie(writer, s.secureCookie)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) authChangePassword(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if payload.CurrentPassword == "" || payload.NewPassword == "" {
		writeError(writer, http.StatusBadRequest, "missing_required_fields")
		return
	}
	if len(payload.NewPassword) < 10 {
		writeError(writer, http.StatusBadRequest, "invalid_new_password")
		return
	}
	session, _ := sessionFromContext(request.Context())
	if err := s.auth.ChangePassword(
		request.Context(), session.OwnerID, session.ID, payload.CurrentPassword, payload.NewPassword,
	); err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			writeError(writer, http.StatusUnauthorized, "invalid_current_password")
			return
		}
		writeError(writer, http.StatusInternalServerError, "password_change_failed")
		return
	}
	_ = s.store.AppendAudit(
		request.Context(), session.OwnerID, "owner.password.change", "owner", session.OwnerID, nil,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) authRecover(writer http.ResponseWriter, request *http.Request) {
	clientKey, allowed := enforceAuthRateLimit(writer, request, s.authLimiter)
	if !allowed {
		return
	}
	if !s.sameOrigin(request) {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusForbidden, "origin_rejected")
		return
	}
	var payload struct {
		SetupToken  string `json:"setup_token"`
		Username    string `json:"username"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if payload.SetupToken == "" || strings.TrimSpace(payload.Username) == "" || payload.NewPassword == "" {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusBadRequest, "missing_required_fields")
		return
	}
	if len(payload.NewPassword) < 10 {
		s.authLimiter.failure(clientKey)
		writeError(writer, http.StatusBadRequest, "invalid_new_password")
		return
	}
	result, err := s.auth.Recover(request.Context(), strings.TrimSpace(payload.SetupToken), payload.Username, payload.NewPassword)
	if err != nil {
		s.authLimiter.failure(clientKey)
		if errors.Is(err, auth.ErrInvalidSetupToken) || errors.Is(err, auth.ErrUnauthorized) {
			writeError(writer, http.StatusUnauthorized, "recovery_failed")
			return
		}
		writeError(writer, http.StatusInternalServerError, "recovery_failed")
		return
	}
	s.authLimiter.success(clientKey)
	setSessionCookie(writer, result.SessionToken, result.Session.ExpiresAt, s.secureCookie)
	_ = s.store.AppendAudit(
		request.Context(), result.Owner.ID, "owner.password.recover", "owner", result.Owner.ID, nil,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "owner": result.Owner, "csrf_token": result.Session.CSRFToken,
	})
}
