package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type channelClaimsContextKey struct{}

func (s *Server) withChannelCapability(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-AHA-Channel-Protocol", "1")
		if s.channels == nil {
			writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
			return
		}
		scheme, token, ok := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			writeError(writer, http.StatusUnauthorized, "capability_invalid")
			return
		}
		claims, err := s.channels.AuthenticateCapability(request.Context(), strings.TrimSpace(token), scope)
		if err != nil {
			if strings.Contains(err.Error(), "scope_denied") {
				writeError(writer, http.StatusForbidden, "capability_scope_denied")
			} else {
				writeError(writer, http.StatusUnauthorized, "capability_invalid")
			}
			return
		}
		if instanceID := request.PathValue("instance_id"); instanceID != "" && instanceID != claims.InstanceID {
			writeError(writer, http.StatusForbidden, "capability_scope_denied")
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), channelClaimsContextKey{}, claims)))
	})
}

func channelRuntimeClaims(ctx context.Context) channel.RuntimeClaims {
	claims, _ := ctx.Value(channelClaimsContextKey{}).(channel.RuntimeClaims)
	return claims
}

func (s *Server) channelRuntimeHandshake(writer http.ResponseWriter, request *http.Request) {
	claims := channelRuntimeClaims(request.Context())
	var payload struct {
		SchemaVersion      int      `json:"schema_version"`
		InstanceID         string   `json:"instance_id"`
		PluginID           string   `json:"plugin_id"`
		SupportedProtocols []string `json:"supported_protocols"`
		BootID             string   `json:"boot_id"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 || payload.InstanceID != claims.InstanceID || payload.PluginID != claims.PluginID || !containsString(payload.SupportedProtocols, "channel-runtime/v1") {
		writeError(writer, http.StatusUpgradeRequired, "channel_protocol_incompatible")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "schema_version": 1, "selected_protocol": "channel-runtime/v1", "instance_id": claims.InstanceID,
		"command_batch_limit": 50, "delivery_batch_limit": 50, "lease_seconds": 30, "capability_expires_at": claims.ExpiresAt,
	})
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (s *Server) channelRuntimeHealth(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
		ErrorCode     string `json:"error_code"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	if err := s.channels.UpdateHealth(request.Context(), channelRuntimeClaims(request.Context()), payload.Status, payload.ErrorCode); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) channelRuntimeClaimCommands(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Limit         int `json:"limit"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	items, err := s.channels.ClaimCommands(request.Context(), channelRuntimeClaims(request.Context()), payload.Limit)
	if err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "commands": items})
}

func (s *Server) channelRuntimeInbound(writer http.ResponseWriter, request *http.Request) {
	var payload domain.ChannelInboundEnvelope
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	receipt, duplicate, err := s.channels.ReceiveInbound(request.Context(), channelRuntimeClaims(request.Context()), payload)
	if err != nil {
		if errors.Is(err, store.ErrChannelInboxDigest) {
			writeError(writer, http.StatusConflict, "inbound_digest_conflict")
		} else {
			writeChannelRuntimeError(writer, err)
		}
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(writer, status, map[string]any{"ok": true, "receipt_id": receipt.ID, "duplicate": duplicate, "state": receipt.State})
}

func (s *Server) channelRuntimeCommandProgress(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int            `json:"schema_version"`
		LeaseID       string         `json:"lease_id"`
		Progress      map[string]any `json:"progress"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 || payload.LeaseID == "" {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	if err := s.channels.CommandProgress(request.Context(), channelRuntimeClaims(request.Context()), request.PathValue("id"), payload.LeaseID, payload.Progress); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) channelRuntimeCommandComplete(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int            `json:"schema_version"`
		LeaseID       string         `json:"lease_id"`
		Success       bool           `json:"success"`
		Result        map[string]any `json:"result"`
		ErrorCode     string         `json:"error_code"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 || payload.LeaseID == "" {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	if err := s.channels.CompleteCommand(request.Context(), channelRuntimeClaims(request.Context()), request.PathValue("id"), payload.LeaseID, payload.Success, payload.Result, payload.ErrorCode); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) channelRuntimeClaimDeliveries(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Limit         int `json:"limit"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	items, err := s.channels.ClaimDeliveries(request.Context(), channelRuntimeClaims(request.Context()), payload.Limit)
	if err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deliveries": items})
}

func (s *Server) channelRuntimeDeliveryAck(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion     int    `json:"schema_version"`
		LeaseID           string `json:"lease_id"`
		ProviderMessageID string `json:"provider_message_id"`
		ProviderRequestID string `json:"provider_request_id"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 || payload.LeaseID == "" || payload.ProviderMessageID == "" {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	if err := s.channels.AckDelivery(request.Context(), channelRuntimeClaims(request.Context()), request.PathValue("id"), payload.LeaseID, payload.ProviderMessageID, payload.ProviderRequestID); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) channelRuntimeDeliveryNack(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		LeaseID       string `json:"lease_id"`
		ErrorCode     string `json:"error_code"`
		Certainty     string `json:"outcome_certainty"`
		RetryAfterMS  int64  `json:"retry_after_ms"`
		Permanent     bool   `json:"permanent"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.SchemaVersion != 1 || payload.LeaseID == "" || payload.ErrorCode == "" || payload.RetryAfterMS < 0 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	if err := s.channels.NackDelivery(request.Context(), channelRuntimeClaims(request.Context()), request.PathValue("id"), payload.LeaseID, payload.ErrorCode, payload.Certainty, time.Duration(payload.RetryAfterMS)*time.Millisecond, payload.Permanent); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func writeChannelRuntimeError(writer http.ResponseWriter, err error) {
	switch {
	case strings.Contains(err.Error(), "scope_denied"):
		writeError(writer, http.StatusForbidden, "capability_scope_denied")
	case strings.Contains(err.Error(), "invalid_envelope"), strings.Contains(err.Error(), "sensitive"):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
	case errors.Is(err, store.ErrChannelRevision):
		writeError(writer, http.StatusConflict, "lease_or_state_conflict")
	default:
		writeError(writer, http.StatusInternalServerError, "channel_runtime_failed")
	}
}
