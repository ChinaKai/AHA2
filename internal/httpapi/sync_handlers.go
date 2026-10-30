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

const syncRunTimeout = 10 * time.Minute

type syncRunProgress struct {
	Running    bool            `json:"running"`
	Phase      string          `json:"phase"`
	Detail     string          `json:"detail,omitempty"`
	Completed  int             `json:"completed"`
	Total      int             `json:"total"`
	Error      string          `json:"error,omitempty"`
	Summary    *syncer.Preview `json:"summary,omitempty"`
	StartedAt  time.Time       `json:"started_at,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at,omitempty"`
	FinishedAt time.Time       `json:"finished_at,omitempty"`
}

func (s *Server) syncRunSnapshot() syncRunProgress {
	s.syncRunMu.RLock()
	defer s.syncRunMu.RUnlock()
	return s.syncRun
}

func (s *Server) syncNextRunSnapshot() time.Time {
	s.syncRunMu.RLock()
	defer s.syncRunMu.RUnlock()
	return s.syncNextRunAt
}

func (s *Server) beginSyncRun(preview syncer.Preview) bool {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	if s.syncRun.Running {
		return false
	}
	now := time.Now().UTC()
	summary := preview
	s.syncRun = syncRunProgress{
		Running: true, Phase: "preparing", Detail: "分析差异", Summary: &summary,
		Total:     preview.Upserts + preview.Deletes + preview.RemoteUpserts + preview.RemoteDeletes,
		StartedAt: now, UpdatedAt: now,
	}
	return true
}

func (s *Server) setSyncRunTotal(preview syncer.Preview) {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	if !s.syncRun.Running {
		return
	}
	summary := preview
	s.syncRun.Total = preview.Upserts + preview.Deletes + preview.RemoteUpserts + preview.RemoteDeletes
	s.syncRun.Summary = &summary
	s.syncRun.UpdatedAt = time.Now().UTC()
}

func (s *Server) updateSyncRun(phase, detail string, completed int) {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	if !s.syncRun.Running {
		return
	}
	s.syncRun.Phase = phase
	if detail != "" {
		s.syncRun.Detail = detail
	}
	if s.syncRun.Total > 0 && completed > s.syncRun.Total {
		completed = s.syncRun.Total
	}
	s.syncRun.Completed = completed
	s.syncRun.UpdatedAt = time.Now().UTC()
}

func (s *Server) finishSyncRun(err error) {
	s.syncRunMu.Lock()
	defer s.syncRunMu.Unlock()
	now := time.Now().UTC()
	s.syncRun.Running = false
	s.syncRun.FinishedAt = now
	if err != nil {
		s.syncRun.Phase = "failed"
		s.syncRun.Error = err.Error()
	} else {
		s.syncRun.Phase = "complete"
		s.syncRun.Completed = s.syncRun.Total
		s.syncRun.Error = ""
	}
	s.syncRun.UpdatedAt = now
}

// StartSyncLoop starts the scheduled sync runner. Manual and scheduled runs
// share the same in-memory run state and concurrency guard, so a page reload
// can always reattach to whatever is running.
func (s *Server) StartSyncLoop(ctx context.Context) {
	s.syncRunMu.Lock()
	if s.syncLoopStarted {
		s.syncRunMu.Unlock()
		return
	}
	s.syncLoopStarted = true
	s.syncRunMu.Unlock()
	go func() {
		for {
			settings, err := s.store.SyncSettings(ctx, localSyncScope)
			interval := 5 * time.Minute
			enabled := false
			if err == nil {
				if settings.IntervalSeconds >= 10 {
					interval = time.Duration(settings.IntervalSeconds) * time.Second
				}
				enabled = settings.Enabled
			}
			s.syncRunMu.Lock()
			s.syncNextRunAt = time.Now().Add(interval)
			s.syncRunMu.Unlock()
			if enabled {
				s.startSyncRun(ctx, "")
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (s *Server) startSyncRun(appCtx context.Context, ownerID string) bool {
	if !s.beginSyncRun(syncer.Preview{}) {
		return false
	}
	go s.executeSyncRun(appCtx, ownerID)
	return true
}

func (s *Server) executeSyncRun(appCtx context.Context, ownerID string) {
	ctx, cancel := context.WithTimeout(appCtx, syncRunTimeout)
	defer cancel()
	fail := func(err error) {
		s.finishSyncRun(err)
		state, stateErr := s.store.SyncState(context.Background(), localSyncScope)
		if stateErr == nil {
			state.LastError = err.Error()
			state.UpdatedAt = time.Now().UTC()
			_ = s.store.UpdateSyncState(context.Background(), state)
		}
	}
	if _, err := s.store.SyncSettings(ctx, localSyncScope); err != nil {
		fail(err)
		return
	}
	if s.secrets == nil {
		fail(errors.New("secret store is unavailable"))
		return
	}
	token, ok := s.secrets.Get(localSyncTokenRef)
	if !ok || token == "" {
		fail(errors.New("sync token is not configured"))
		return
	}
	runner := syncer.Runner{Store: s.store, Secrets: s.secrets, Scope: localSyncScope, TokenRef: localSyncTokenRef}
	preview, err := runner.Preview(ctx)
	if err != nil {
		fail(err)
		return
	}
	s.setSyncRunTotal(preview)
	completed := 0
	runner.Progress = func(phase, detail string, delta int) {
		completed += delta
		s.updateSyncRun(phase, detail, completed)
	}
	if err := runner.RunOnce(ctx); err != nil {
		fail(err)
		return
	}
	state, stateErr := s.store.SyncState(context.Background(), localSyncScope)
	if stateErr == nil {
		state.LastError = ""
		state.UpdatedAt = time.Now().UTC()
		_ = s.store.UpdateSyncState(context.Background(), state)
	}
	s.finishSyncRun(nil)
	if ownerID != "" {
		_ = s.store.AppendAudit(context.Background(), ownerID, "sync.run", "settings", "sync", nil, time.Now().UTC().Format(time.RFC3339Nano))
	}
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
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state, "pending": pending, "run": s.syncRunSnapshot(), "next_run_at": s.syncNextRunSnapshot()})
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
	if _, err := s.store.SyncSettings(request.Context(), localSyncScope); err != nil {
		writeError(writer, http.StatusBadRequest, "sync_not_configured")
		return
	}
	if s.secrets == nil {
		writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
		return
	}
	if token, ok := s.secrets.Get(localSyncTokenRef); !ok || token == "" {
		writeError(writer, http.StatusBadRequest, "sync_token_not_configured")
		return
	}
	session, _ := sessionFromContext(request.Context())
	if !s.startSyncRun(context.Background(), session.OwnerID) {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "sync_already_running", "message": "同步正在进行中", "run": s.syncRunSnapshot()})
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "run": s.syncRunSnapshot()})
}
