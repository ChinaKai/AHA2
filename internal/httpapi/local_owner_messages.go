package httpapi

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	localOwnerMessageMaxBodyBytes = 16 << 10
	localOwnerMessageRateLimit    = 30
	localOwnerMessageRateWindow   = time.Minute
)

type localOwnerMessageLimiter struct {
	mu          sync.Mutex
	windowStart time.Time
	count       int
	limit       int
	window      time.Duration
}

func newLocalOwnerMessageLimiter() *localOwnerMessageLimiter {
	return &localOwnerMessageLimiter{limit: localOwnerMessageRateLimit, window: localOwnerMessageRateWindow}
}

func (l *localOwnerMessageLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= l.window {
		l.windowStart = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

// localOwnerMessageIngest accepts owner text from the loopback voice bridge.
// It deliberately does not use the Web session or CSRF path: the caller has no
// browser session, and a local bearer secret keeps the capability separate.
func (s *Server) localOwnerMessageIngest(writer http.ResponseWriter, request *http.Request) {
	if !isLoopbackIngestRequest(request) {
		writeError(writer, http.StatusForbidden, "local_only")
		return
	}
	if localOwnerIngestQueryHasSecret(request.URL.Query()) {
		writeError(writer, http.StatusBadRequest, "query_secret_forbidden")
		return
	}
	if s.secrets == nil {
		writeError(writer, http.StatusServiceUnavailable, "local_ingest_unavailable")
		return
	}
	expected, configured := s.secrets.Get(app.LocalOwnerIngestSecretRef)
	if !configured || strings.TrimSpace(expected) == "" {
		writeError(writer, http.StatusServiceUnavailable, "local_ingest_unavailable")
		return
	}
	if !localOwnerIngestAuthorized(request, expected) {
		writeError(writer, http.StatusUnauthorized, "local_ingest_unauthorized")
		return
	}
	// Count only authenticated callers. A misconfigured bridge or local brute
	// force must not exhaust the quota that the real voice bridge depends on.
	if !s.localOwnerMessageLimiter.allow(time.Now()) {
		writeError(writer, http.StatusTooManyRequests, "rate_limited")
		return
	}
	if !localOwnerIngestJSON(request.Header.Get("Content-Type")) {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	payload, status, code := decodeLocalOwnerMessageRequest(request)
	if code != "" {
		writeError(writer, status, code)
		return
	}
	result, err := s.app.SubmitLocalOwnerMessage(request.Context(), app.LocalOwnerMessageInput{
		TaskID: payload.TaskID, Content: payload.Content,
		Source: payload.Source, ClientRequestID: payload.ClientRequestID,
	})
	if err != nil {
		status, code := localOwnerMessageErrorStatus(err)
		writeError(writer, status, code)
		return
	}
	s.auditLocalOwnerMessage(request, payload, result)
	response := map[string]any{
		"ok":         true,
		"queued":     true,
		"message_id": result.MessageID,
		"started":    result.Turn.ID != "",
		"duplicate":  result.Duplicate,
	}
	if result.Turn.ID != "" {
		response["turn_id"] = result.Turn.ID
	}
	writeJSON(writer, http.StatusAccepted, response)
}

type localOwnerMessageRequest struct {
	TaskID          string `json:"task_id"`
	Content         string `json:"content"`
	Source          string `json:"source"`
	ClientRequestID string `json:"client_request_id"`
}

func decodeLocalOwnerMessageRequest(request *http.Request) (localOwnerMessageRequest, int, string) {
	var payload localOwnerMessageRequest
	if request.Body == nil {
		return payload, http.StatusBadRequest, "invalid_json"
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, localOwnerMessageMaxBodyBytes+1))
	_ = request.Body.Close()
	if err != nil {
		return payload, http.StatusBadRequest, "invalid_json"
	}
	if len(body) > localOwnerMessageMaxBodyBytes {
		return payload, http.StatusRequestEntityTooLarge, "request_too_large"
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, http.StatusBadRequest, "invalid_json"
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return payload, http.StatusBadRequest, "invalid_json"
	}
	return payload, 0, ""
}

func localOwnerIngestQueryHasSecret(query map[string][]string) bool {
	markers := []string{"secret", "token", "password", "passwd", "api_key", "apikey", "credential"}
	for key := range query {
		normalized := strings.ToLower(strings.TrimSpace(key))
		for _, marker := range markers {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	return false
}

func localOwnerIngestJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return false
	}
	return strings.EqualFold(mediaType, "application/json")
}

// isLoopbackIngestRequest refuses every request that did not arrive directly
// over a loopback socket. Proxy headers are rejected because a same-host
// reverse proxy would otherwise make a LAN request look local.
func isLoopbackIngestRequest(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	if request.Header.Get("Forwarded") != "" ||
		request.Header.Get("X-Forwarded-For") != "" ||
		request.Header.Get("X-Real-IP") != "" {
		return false
	}
	requestHost := request.Host
	if value, _, splitErr := net.SplitHostPort(requestHost); splitErr == nil {
		requestHost = value
	}
	requestHost = strings.Trim(requestHost, "[]")
	switch strings.ToLower(requestHost) {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}

func localOwnerIngestAuthorized(request *http.Request, expected string) bool {
	scheme, token, ok := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	providedHash := sha256.Sum256([]byte(strings.TrimSpace(token)))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

func localOwnerMessageErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, app.ErrLocalIngestTaskRequired):
		return http.StatusBadRequest, "task_required"
	case errors.Is(err, app.ErrLocalIngestTaskNotAllowed):
		return http.StatusForbidden, "task_not_allowed"
	case errors.Is(err, app.ErrLocalIngestTaskNotFound):
		return http.StatusNotFound, "task_not_found"
	case errors.Is(err, app.ErrLocalIngestTaskNotRunnable):
		return http.StatusConflict, "task_not_runnable"
	case errors.Is(err, app.ErrLocalIngestContentEmpty):
		return http.StatusBadRequest, "content_required"
	case errors.Is(err, app.ErrLocalIngestContentTooLong):
		return http.StatusBadRequest, "content_too_long"
	case errors.Is(err, app.ErrLocalIngestContentControlChar):
		return http.StatusBadRequest, "content_control_characters"
	case errors.Is(err, app.ErrLocalIngestSourceInvalid):
		return http.StatusBadRequest, "source_invalid"
	case errors.Is(err, app.ErrLocalIngestRequestIDInvalid):
		return http.StatusBadRequest, "client_request_id_invalid"
	case errors.Is(err, store.ErrLocalIngestRequestConflict):
		return http.StatusConflict, "client_request_id_conflict"
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "task_not_found"
	default:
		return http.StatusConflict, "ingest_failed"
	}
}

func (s *Server) auditLocalOwnerMessage(request *http.Request, payload localOwnerMessageRequest, result app.LocalOwnerMessageResult) {
	ownerID, _ := s.store.PrimaryOwnerID(request.Context())
	source := strings.TrimSpace(payload.Source)
	if source == "" {
		source = app.LocalOwnerIngestSourceVoice
	}
	details := map[string]any{
		"task_id":           strings.TrimSpace(payload.TaskID),
		"source":            source,
		"content_length":    utf8.RuneCountInString(strings.TrimSpace(payload.Content)),
		"message_id":        result.MessageID,
		"client_request_id": strings.TrimSpace(payload.ClientRequestID),
		"duplicate":         result.Duplicate,
		"started":           result.Turn.ID != "",
	}
	_ = s.store.AppendAudit(
		request.Context(), ownerID, "local.owner_message.ingest", "task",
		strings.TrimSpace(payload.TaskID), details, time.Now().UTC().Format(time.RFC3339Nano),
	)
}
