package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

const localSyncScope = "default"
const localSyncTokenRef = syncer.DefaultTokenRef
const localSyncPassphraseRef = syncer.DefaultPassphraseRef

type syncSettingsPayload struct {
	Enabled          bool     `json:"enabled"`
	Endpoint         string   `json:"endpoint"`
	DeviceID         string   `json:"device_id"`
	IntervalSeconds  int      `json:"interval_seconds"`
	Token            string   `json:"token"`
	ClearToken       bool     `json:"clear_token"`
	Passphrase       string   `json:"passphrase"`
	ClearPassphrase  bool     `json:"clear_passphrase"`
	RegistrationCode string   `json:"registration_code"`
	ProviderIDs      []string `json:"provider_ids"`
	EnvGroupIDs      []string `json:"env_group_ids"`
	CodexAccountIDs  []string `json:"codex_account_ids"`
}

func publicSyncSettings(item domain.SyncSettings, tokenConfigured, passphraseConfigured bool) map[string]any {
	return map[string]any{"scope": item.Scope, "enabled": item.Enabled, "endpoint": item.Endpoint, "device_id": item.DeviceID, "interval_seconds": item.IntervalSeconds, "provider_ids": item.ProviderIDs, "env_group_ids": item.EnvGroupIDs, "codex_account_ids": item.CodexAccountIDs, "token_configured": tokenConfigured, "passphrase_configured": passphraseConfigured, "updated_at": item.UpdatedAt}
}

func (s *Server) syncSettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.SyncSettings(request.Context(), localSyncScope)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(writer, http.StatusInternalServerError, "sync_settings_failed")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		item = domain.SyncSettings{Scope: localSyncScope, IntervalSeconds: 300}
	}
	configured := false
	passphraseConfigured := false
	if s.secrets != nil {
		_, configured = s.secrets.Get(localSyncTokenRef)
		_, passphraseConfigured = s.secrets.Get(localSyncPassphraseRef)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "sync": publicSyncSettings(item, configured, passphraseConfigured)})
}

type syncValidationError string

func (e syncValidationError) Error() string { return string(e) }

func normalizeSyncSettings(payload syncSettingsPayload) (domain.SyncSettings, error) {
	item := domain.SyncSettings{Scope: localSyncScope, Enabled: payload.Enabled, Endpoint: strings.TrimSpace(payload.Endpoint), DeviceID: strings.TrimSpace(payload.DeviceID), IntervalSeconds: payload.IntervalSeconds, ProviderIDs: payload.ProviderIDs, EnvGroupIDs: payload.EnvGroupIDs, CodexAccountIDs: payload.CodexAccountIDs, UpdatedAt: time.Now().UTC()}
	if item.IntervalSeconds < 10 || item.IntervalSeconds > 86400 {
		return item, syncValidationError("interval_seconds must be between 10 and 86400")
	}
	parsed, err := url.Parse(item.Endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return item, syncValidationError("endpoint must be an http(s) URL without embedded credentials")
	}
	if item.DeviceID == "" {
		return item, syncValidationError("device_id is required")
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		if host != "localhost" && !net.ParseIP(host).IsLoopback() {
			return item, syncValidationError("non-loopback sync endpoint requires HTTPS")
		}
	}
	return item, nil
}

func (s *Server) updateSyncSettings(writer http.ResponseWriter, request *http.Request) {
	var payload syncSettingsPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := normalizeSyncSettings(payload)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_sync_settings", "message": err.Error()})
		return
	}
	if payload.RegistrationCode != "" && payload.Token == "" {
		ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		registered, registerErr := (&syncer.Client{BaseURL: item.Endpoint}).Register(ctx, item.DeviceID, payload.RegistrationCode)
		cancel()
		if registerErr != nil || registered.Token == "" {
			writeError(writer, http.StatusBadGateway, "sync_registration_failed")
			return
		}
		payload.Token = registered.Token
	}
	if payload.Token != "" {
		if s.secrets == nil {
			writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
			return
		}
		if err := s.secrets.PutMany(map[string]string{localSyncTokenRef: payload.Token}); err != nil {
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if payload.Passphrase != "" {
		if len(payload.Passphrase) < 12 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_sync_passphrase", "message": "passphrase must be at least 12 characters"})
			return
		}
		if err := s.secrets.PutMany(map[string]string{localSyncPassphraseRef: payload.Passphrase}); err != nil {
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if err := s.store.PutSyncSettings(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_sync_failed")
		return
	}
	if payload.ClearToken && payload.Token == "" && s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{localSyncTokenRef})
	}
	if payload.ClearPassphrase && payload.Passphrase == "" && s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{localSyncPassphraseRef})
	}
	configured := false
	passphraseConfigured := false
	if s.secrets != nil {
		_, configured = s.secrets.Get(localSyncTokenRef)
		_, passphraseConfigured = s.secrets.Get(localSyncPassphraseRef)
	}
	s.audit(request, "sync.update", "settings", "sync", nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "sync": publicSyncSettings(item, configured, passphraseConfigured)})
}

func (s *Server) syncStatus(writer http.ResponseWriter, request *http.Request) {
	state, err := s.store.SyncState(request.Context(), localSyncScope)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_status_failed")
		return
	}
	pending, err := s.store.SyncOutboxCount(request.Context(), localSyncScope)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_status_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state, "pending": pending})
}
func (s *Server) syncConflicts(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.SyncConflicts(request.Context(), localSyncScope)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_conflicts_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "conflicts": items})
}

func (s *Server) runSync(writer http.ResponseWriter, request *http.Request) {
	_, err := s.store.SyncSettings(request.Context(), localSyncScope)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "sync_not_configured")
		return
	}
	if s.secrets == nil {
		writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
		return
	}
	token, ok := s.secrets.Get(localSyncTokenRef)
	if !ok || token == "" {
		writeError(writer, http.StatusBadRequest, "sync_token_not_configured")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 45*time.Second)
	defer cancel()
	_ = token
	err = (syncer.Runner{Store: s.store, Secrets: s.secrets, Scope: localSyncScope, TokenRef: localSyncTokenRef}).RunOnce(ctx)
	if err != nil {
		state, _ := s.store.SyncState(request.Context(), localSyncScope)
		state.LastError = err.Error()
		state.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateSyncState(request.Context(), state)
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": "sync_failed", "message": err.Error()})
		return
	}
	state, _ := s.store.SyncState(request.Context(), localSyncScope)
	pending, _ := s.store.SyncOutboxCount(request.Context(), localSyncScope)
	s.audit(request, "sync.run", "settings", "sync", nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state, "pending": pending})
}
