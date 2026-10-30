package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
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

var errSyncConflictRemoteChanged = errors.New("远端又更新了；请刷新冲突后重试")

func (s *Server) resolveSyncConflict(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action != "local" && action != "remote" && action != "ignore" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_sync_conflict_action", "message": "action must be local, remote, or ignore"})
		return
	}
	conflict, err := s.store.SyncConflict(request.Context(), localSyncScope, strings.TrimSpace(request.PathValue("id")))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(writer, http.StatusNotFound, "sync_conflict_not_found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_conflict_failed")
		return
	}
	localPayload := bytes.TrimSpace(conflict.LocalPayload)
	remotePayload := bytes.TrimSpace(conflict.RemotePayload)
	if action == "local" && (len(localPayload) == 0 || string(localPayload) == "null") {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "sync_conflict_local_missing", "message": "本机没有可推送的内容；请选择采用远端或忽略"})
		return
	}
	if action == "remote" && (len(remotePayload) == 0 || string(remotePayload) == "null") {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "sync_conflict_remote_missing", "message": "远端没有可应用的内容；请选择保留本机或忽略"})
		return
	}
	switch action {
	case "local":
		if err := s.resolveConflictWithLocal(request.Context(), conflict); err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, errSyncConflictRemoteChanged) {
				status = http.StatusConflict
			}
			writeJSON(writer, status, map[string]any{"ok": false, "error": "sync_conflict_resolve_failed", "message": err.Error()})
			return
		}
	case "remote":
		if err := s.resolveConflictWithRemote(request.Context(), conflict); err != nil {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "sync_conflict_apply_failed", "message": err.Error()})
			return
		}
	case "ignore":
		if err := s.store.DropSyncOutboxObject(request.Context(), localSyncScope, conflict.ObjectType, conflict.ObjectID); err != nil {
			writeError(writer, http.StatusInternalServerError, "sync_conflict_resolve_failed")
			return
		}
		// A remote-only conflict has no local object to re-export; skip the
		// export walk for those stale entries.
		if len(localPayload) > 0 {
			if settings, settingsErr := s.store.SyncSettings(request.Context(), localSyncScope); settingsErr == nil {
				s.markLocalExportApplied(request.Context(), settings, conflict.ObjectType, conflict.ObjectID)
			}
		}
	}
	if _, err := s.store.ResolveSyncConflictsForObject(request.Context(), localSyncScope, conflict.ObjectType, conflict.ObjectID, time.Now().UTC()); err != nil {
		writeError(writer, http.StatusInternalServerError, "sync_conflict_resolve_failed")
		return
	}
	s.audit(request, "sync.conflict.resolve", "settings", "sync", map[string]any{"action": action, "object_type": conflict.ObjectType, "object_id": conflict.ObjectID})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "action": action, "object_type": conflict.ObjectType, "object_id": conflict.ObjectID})
}

func (s *Server) resolveConflictWithLocal(ctx context.Context, conflict domain.SyncConflict) error {
	settings, err := s.store.SyncSettings(ctx, localSyncScope)
	if err != nil {
		return fmt.Errorf("同步尚未配置")
	}
	if s.secrets == nil {
		return fmt.Errorf("Secret Store 不可用")
	}
	token, ok := s.secrets.Get(localSyncTokenRef)
	if !ok || token == "" {
		return fmt.Errorf("同步 Token 未配置")
	}
	object := domain.SyncObject{
		Type: conflict.ObjectType, ID: conflict.ObjectID, Operation: "upsert", Payload: conflict.LocalPayload,
		IdempotencyKey: domain.NewID("sync"),
	}
	if item, itemErr := s.store.SyncOutboxForObject(ctx, localSyncScope, conflict.ObjectType, conflict.ObjectID); itemErr == nil {
		// "Keep local" means force the remote to this payload. Leaving the base
		// version empty makes the center skip the optimistic version check, so a
		// stale remote version cannot turn this into another conflict (and does
		// not depend on the center encoding a conflict response).
		object = item.Object
	} else if !errors.Is(itemErr, sql.ErrNoRows) {
		return itemErr
	}
	object.BaseVersion = ""
	client := &syncer.Client{BaseURL: settings.Endpoint, DeviceID: settings.DeviceID, Credential: func(context.Context) (string, error) { return token, nil }}
	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	acked := false
	for attempt := 0; attempt < 2; attempt++ {
		response, err := client.Push(pushCtx, syncer.PushRequest{Scope: localSyncScope, DeviceID: settings.DeviceID, Objects: []domain.SyncObject{object}})
		if err != nil {
			return err
		}
		if len(response.Conflicts) == 0 {
			for _, key := range response.AckedKeys {
				if key == object.IdempotencyKey {
					acked = true
					break
				}
			}
			break
		}
		latest := response.Conflicts[0]
		_ = s.store.AddSyncConflict(ctx, latest)
		if attempt == 1 {
			return errSyncConflictRemoteChanged
		}
		// The remote moved again since the conflict was recorded. Rebase the
		// same local payload on the newest remote version and try once more.
		object.BaseVersion = latest.RemoteVersion
	}
	if !acked {
		return fmt.Errorf("中心没有确认本机变更")
	}
	now := time.Now().UTC()
	_ = s.store.MarkSyncApplied(ctx, localSyncScope, object, now)
	if err := s.store.DropSyncOutboxObject(ctx, localSyncScope, object.Type, object.ID); err != nil {
		return err
	}
	s.markLocalExportApplied(ctx, settings, object.Type, object.ID)
	state, stateErr := s.store.SyncState(ctx, localSyncScope)
	if stateErr == nil {
		state.LastPushAt = now
		state.LastError = ""
		state.UpdatedAt = now
		_ = s.store.UpdateSyncState(ctx, state)
	}
	return nil
}

func (s *Server) resolveConflictWithRemote(ctx context.Context, conflict domain.SyncConflict) error {
	settings, err := s.store.SyncSettings(ctx, localSyncScope)
	if err != nil {
		return fmt.Errorf("同步尚未配置")
	}
	engine := &syncer.Engine{Store: s.store, Scope: localSyncScope, DeviceID: settings.DeviceID, Now: time.Now}
	syncer.RegisterBusinessHandlersForDevice(engine, s.store, settings.DeviceID)
	if s.secrets != nil {
		syncer.RegisterCodexAccountTombstoneHandler(engine, s.store, s.secrets)
		if passphrase, ok := s.secrets.Get(localSyncPassphraseRef); ok && passphrase != "" {
			syncer.RegisterSecretBundleHandler(engine, s.store, s.secrets, passphrase)
		}
	}
	object := domain.SyncObject{
		Type: conflict.ObjectType, ID: conflict.ObjectID, Operation: "upsert", Payload: conflict.RemotePayload,
		RemoteVersion: conflict.RemoteVersion, IdempotencyKey: "conflict:" + conflict.ID, EventID: "conflict:" + conflict.ID,
	}
	applyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := engine.Apply(applyCtx, object); err != nil {
		return err
	}
	if err := s.store.DropSyncOutboxObject(ctx, localSyncScope, conflict.ObjectType, conflict.ObjectID); err != nil {
		return err
	}
	s.markLocalExportApplied(ctx, settings, conflict.ObjectType, conflict.ObjectID)
	return nil
}

// markLocalExportApplied records the object's export identity as applied so the
// next run does not enqueue the same divergent object again.
func (s *Server) markLocalExportApplied(ctx context.Context, settings domain.SyncSettings, objectType, objectID string) {
	objects, err := syncer.ExportBusinessObjectsForDevice(ctx, s.store, settings.DeviceID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, object := range objects {
		if object.Type == objectType && object.ID == objectID {
			_ = s.store.MarkSyncApplied(ctx, localSyncScope, object, now)
			return
		}
	}
}
