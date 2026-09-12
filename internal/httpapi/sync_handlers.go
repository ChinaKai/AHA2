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

type syncRunProgress struct {
	Running   bool      `json:"running"`
	Phase     string    `json:"phase"`
	Completed int       `json:"completed"`
	Total     int       `json:"total"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (s *Server) syncRunSnapshot() syncRunProgress {
	s.syncRunMu.RLock()
	defer s.syncRunMu.RUnlock()
	return s.syncRun
}

func (s *Server) beginSyncRun(preview syncer.Preview) bool {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	if s.syncRun.Running {
		return false
	}
	now := time.Now().UTC()
	s.syncRun = syncRunProgress{Running: true, Phase: "preparing", Total: preview.Upserts + preview.Deletes + preview.RemoteUpserts + preview.RemoteDeletes, StartedAt: now, UpdatedAt: now}
	return true
}

func (s *Server) updateSyncRun(phase string, preview syncer.Preview) {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	remote := preview.RemoteUpserts + preview.RemoteDeletes
	local := preview.Upserts + preview.Deletes
	s.syncRun.Phase = phase
	switch phase {
	case "preparing", "pushing":
		s.syncRun.Completed = 0
	case "pulling":
		s.syncRun.Completed = local
	case "finalizing", "complete":
		s.syncRun.Completed = remote + local
	}
	s.syncRun.UpdatedAt = time.Now().UTC()
}

func (s *Server) finishSyncRun(err error) {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	s.syncRun.Running = false
	if err != nil {
		s.syncRun.Phase = "failed"
		s.syncRun.Error = err.Error()
	} else {
		s.syncRun.Phase = "complete"
		s.syncRun.Completed = s.syncRun.Total
		s.syncRun.Error = ""
	}
	s.syncRun.UpdatedAt = time.Now().UTC()
}

type syncSettingsPayload struct {
	Enabled          bool     `json:"enabled"`
	Endpoint         string   `json:"endpoint"`
	DeviceID         string   `json:"device_id"`
	DeviceName       string   `json:"device_name"`
	IntervalSeconds  int      `json:"interval_seconds"`
	Token            string   `json:"token"`
	ClearToken       bool     `json:"clear_token"`
	Passphrase       string   `json:"passphrase"`
	ClearPassphrase  bool     `json:"clear_passphrase"`
	RegistrationCode string   `json:"registration_code"`
	ProviderIDs      []string `json:"provider_ids"`      // accepted for backward compatibility; ignored
	EnvGroupIDs      []string `json:"env_group_ids"`     // accepted for backward compatibility; ignored
	CodexAccountIDs  []string `json:"codex_account_ids"` // accepted for backward compatibility; ignored
}

func publicSyncSettings(item domain.SyncSettings, tokenConfigured, passphraseConfigured bool) map[string]any {
	return map[string]any{"scope": item.Scope, "enabled": item.Enabled, "endpoint": item.Endpoint, "device_id": item.DeviceID, "device_name": item.DeviceName, "interval_seconds": item.IntervalSeconds, "token_configured": tokenConfigured, "passphrase_configured": passphraseConfigured, "updated_at": item.UpdatedAt}
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
	item := domain.SyncSettings{Scope: localSyncScope, Enabled: payload.Enabled, Endpoint: strings.TrimSpace(payload.Endpoint), DeviceID: strings.TrimSpace(payload.DeviceID), DeviceName: strings.TrimSpace(payload.DeviceName), IntervalSeconds: payload.IntervalSeconds, UpdatedAt: time.Now().UTC()}
	if item.DeviceName == "" {
		item.DeviceName = item.DeviceID
	}
	if item.IntervalSeconds < 10 || item.IntervalSeconds > 86400 {
		return item, syncValidationError("interval_seconds must be between 10 and 86400")
	}
	parsed, err := url.Parse(item.Endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return item, syncValidationError("endpoint must be an http(s) URL without embedded credentials")
	}
	if item.DeviceID == "" && payload.RegistrationCode == "" {
		return item, syncValidationError("device_id is required")
	}
	if payload.RegistrationCode != "" && item.DeviceName == "" {
		return item, syncValidationError("device_name is required for registration")
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
	previous, _ := s.store.SyncSettings(request.Context(), localSyncScope)
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
		registered, registerErr := (&syncer.Client{BaseURL: item.Endpoint}).Register(ctx, item.DeviceName, payload.RegistrationCode)
		cancel()
		if registerErr != nil || registered.Token == "" {
			writeError(writer, http.StatusBadGateway, "sync_registration_failed")
			return
		}
		payload.Token = registered.Token
		item.DeviceID = registered.DeviceID
		item.DeviceName = registered.DeviceName
	} else if item.DeviceID != "" && item.DeviceName != "" && previous.DeviceName != "" && item.DeviceName != previous.DeviceName && s.secrets != nil {
		if token, ok := s.secrets.Get(localSyncTokenRef); ok && token != "" {
			client := &syncer.Client{BaseURL: item.Endpoint, DeviceID: item.DeviceID, Credential: func(context.Context) (string, error) { return token, nil }}
			ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
			renameErr := client.RenameDevice(ctx, item.DeviceName)
			cancel()
			if renameErr != nil {
				writeError(writer, http.StatusBadGateway, "sync_device_rename_failed")
				return
			}
		}
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
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state, "pending": pending, "run": s.syncRunSnapshot()})
}
func (s *Server) syncConflicts(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.SyncConflicts(request.Context(), localSyncScope)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_conflicts_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "conflicts": items})
}

func (s *Server) syncPreview(writer http.ResponseWriter, request *http.Request) {
	if _, err := s.store.SyncSettings(request.Context(), localSyncScope); errors.Is(err, sql.ErrNoRows) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": syncer.Preview{}})
		return
	} else if err != nil {
		writeError(writer, http.StatusBadRequest, "sync_not_configured")
		return
	}
	preview, err := (syncer.Runner{Store: s.store, Secrets: s.secrets, Scope: localSyncScope, TokenRef: localSyncTokenRef}).Preview(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_preview_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
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
	runner := syncer.Runner{Store: s.store, Secrets: s.secrets, Scope: localSyncScope, TokenRef: localSyncTokenRef}
	preview, err := runner.Preview(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_preview_failed")
		return
	}
	if !s.beginSyncRun(preview) {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "sync_already_running", "message": "同步正在进行中"})
		return
	}
	runner.Progress = func(phase string) { s.updateSyncRun(phase, preview) }
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Minute)
	defer cancel()
	_ = token
	err = runner.RunOnce(ctx)
	s.finishSyncRun(err)
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
	conflicts, _ := s.store.SyncConflicts(request.Context(), localSyncScope)
	preview.Pending, preview.Conflicts = pending, len(conflicts)
	s.audit(request, "sync.run", "settings", "sync", nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state, "pending": pending, "summary": preview})
}
