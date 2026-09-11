package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
)

type sessionContextKey struct{}
type agentClaimsContextKey struct{}

func sessionFromContext(ctx context.Context) (domain.Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(domain.Session)
	return session, ok
}

func agentClaimsFromContext(ctx context.Context) (agentapi.Claims, bool) {
	claims, ok := ctx.Value(agentClaimsContextKey{}).(agentapi.Claims)
	return claims, ok
}

func (s *Server) withAgentCapability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if s.agentCapabilities == nil {
			writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
			return
		}
		scheme, token, ok := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			writeError(writer, http.StatusUnauthorized, "agent_capability_required")
			return
		}
		claims, err := s.agentCapabilities.Authenticate(strings.TrimSpace(token))
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "agent_capability_invalid")
			return
		}
		ctx := context.WithValue(request.Context(), agentClaimsContextKey{}, claims)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "authentication_required")
			return
		}
		session, err := s.auth.Authenticate(request.Context(), cookie.Value)
		if err != nil {
			clearSessionCookie(writer, s.secureCookie)
			writeError(writer, http.StatusUnauthorized, "authentication_required")
			return
		}
		if unsafeMethod(request.Method) {
			if !s.sameOrigin(request) {
				writeError(writer, http.StatusForbidden, "origin_rejected")
				return
			}
			if request.Header.Get("X-CSRF-Token") != session.CSRFToken {
				writeError(writer, http.StatusForbidden, "csrf_rejected")
				return
			}
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, session)))
	})
}

func unsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func (s *Server) sameOrigin(request *http.Request) bool {
	if !s.originValidationEnabled() {
		return true
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	value, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(value.Host, request.Host)
}

func (s *Server) originValidationEnabled() bool {
	s.originPolicyMu.RLock()
	defer s.originPolicyMu.RUnlock()
	return s.validateOrigin
}

func (s *Server) setOriginValidation(enabled bool) {
	s.originPolicyMu.Lock()
	s.validateOrigin = enabled && !s.originStartupOverride
	s.originPolicyMu.Unlock()
}

func decodeJSON(request *http.Request, destination any) error {
	return decodeJSONLimit(request, destination, 1<<20)
}

func decodeJSONLimit(request *http.Request, destination any, limit int64) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]any{"ok": false, "error": code})
}

func setSessionCookie(writer http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	http.SetCookie(writer, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", Expires: expiresAt,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(writer http.ResponseWriter, secure bool) {
	http.SetCookie(writer, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}
