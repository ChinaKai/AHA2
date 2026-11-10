package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) PutSyncSettings(ctx context.Context, value domain.SyncSettings) error {
	if value.Scope == "" || value.IntervalSeconds < 1 {
		return fmt.Errorf("sync scope and positive interval are required")
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = time.Now().UTC()
	}
	selection := encodeJSON(map[string]any{"provider_ids": value.ProviderIDs, "env_group_ids": value.EnvGroupIDs, "codex_account_ids": value.CodexAccountIDs})
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_settings(scope,enabled,endpoint,device_id,device_name,interval_seconds,secret_selection_json,updated_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET enabled=excluded.enabled,endpoint=excluded.endpoint,
		device_id=excluded.device_id,device_name=excluded.device_name,interval_seconds=excluded.interval_seconds,secret_selection_json=excluded.secret_selection_json,updated_at=excluded.updated_at`,
		value.Scope, value.Enabled, value.Endpoint, value.DeviceID, value.DeviceName, value.IntervalSeconds, selection, timeString(value.UpdatedAt))
	return err
}

func (s *Store) SyncSettings(ctx context.Context, scope string) (domain.SyncSettings, error) {
	var v domain.SyncSettings
	var enabled bool
	var updated, selection string
	err := s.db.QueryRowContext(ctx, `SELECT scope,enabled,endpoint,device_id,device_name,interval_seconds,secret_selection_json,updated_at FROM sync_settings WHERE scope=?`, scope).
		Scan(&v.Scope, &enabled, &v.Endpoint, &v.DeviceID, &v.DeviceName, &v.IntervalSeconds, &selection, &updated)
	selected := decodeJSON(selection, struct {
		ProviderIDs     []string `json:"provider_ids"`
		EnvGroupIDs     []string `json:"env_group_ids"`
		CodexAccountIDs []string `json:"codex_account_ids"`
	}{})
	v.ProviderIDs, v.EnvGroupIDs, v.CodexAccountIDs = selected.ProviderIDs, selected.EnvGroupIDs, selected.CodexAccountIDs
	v.Enabled, v.UpdatedAt = enabled, parseTime(updated)
	return v, err
}

func (s *Store) SyncState(ctx context.Context, scope string) (domain.SyncState, error) {
	var v domain.SyncState
	var replayRequired bool
	var push, pull, updated string
	err := s.db.QueryRowContext(ctx, `SELECT scope,cursor,last_push_at,last_pull_at,last_error,replay_required,updated_at FROM sync_state WHERE scope=?`, scope).
		Scan(&v.Scope, &v.Cursor, &push, &pull, &v.LastError, &replayRequired, &updated)
	if err == sql.ErrNoRows {
		return domain.SyncState{Scope: scope}, nil
	}
	v.LastPushAt, v.LastPullAt, v.ReplayRequired, v.UpdatedAt = parseTime(push), parseTime(pull), replayRequired, parseTime(updated)
	return v, err
}

func (s *Store) UpdateSyncState(ctx context.Context, v domain.SyncState) error {
	if v.Scope == "" {
		return fmt.Errorf("sync scope is required")
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_state(scope,cursor,last_push_at,last_pull_at,last_error,updated_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET cursor=excluded.cursor,last_push_at=excluded.last_push_at,
		last_pull_at=excluded.last_pull_at,last_error=excluded.last_error,updated_at=excluded.updated_at`,
		v.Scope, v.Cursor, timeString(v.LastPushAt), timeString(v.LastPullAt), v.LastError, timeString(v.UpdatedAt))
	return err
}

func (s *Store) CompleteSyncReplay(ctx context.Context, scope string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sync_state SET replay_required=0,updated_at=? WHERE scope=?`, timeString(time.Now().UTC()), scope)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("sync state is not configured")
	}
	return nil
}

func (s *Store) EnqueueSync(ctx context.Context, item domain.SyncOutboxItem) error {
	if item.ID == "" {
		item.ID = domain.NewID("sync")
	}
	if item.Scope == "" || item.Object.Type == "" || item.Object.ID == "" || item.Object.IdempotencyKey == "" {
		return fmt.Errorf("sync identity fields are required")
	}
	if item.Object.Operation != "upsert" && item.Object.Operation != "delete" {
		return fmt.Errorf("unsupported sync operation %q", item.Object.Operation)
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	payload := item.Object.Payload
	if len(payload) == 0 && item.Object.Operation != "delete" {
		payload = json.RawMessage(`{}`)
	}
	sourceVersion := item.Object.SourceVersion
	if sourceVersion == "" {
		sourceVersion = item.Object.RemoteVersion
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO sync_outbox(id,scope,object_type,object_id,operation,payload_json,base_version,source_version,idempotency_key,status,attempts,next_attempt_at,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Scope, item.Object.Type, item.Object.ID, item.Object.Operation, string(payload), item.Object.BaseVersion, sourceVersion, item.Object.IdempotencyKey, "pending", 0, "", "", timeString(item.CreatedAt), timeString(item.UpdatedAt))
	return err
}

func (s *Store) PendingSync(ctx context.Context, scope string, limit int, now time.Time) ([]domain.SyncOutboxItem, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,scope,object_type,object_id,operation,payload_json,base_version,source_version,idempotency_key,status,attempts,next_attempt_at,last_error,created_at,updated_at FROM sync_outbox WHERE scope=? AND status='pending' AND (next_attempt_at='' OR next_attempt_at<=?) ORDER BY created_at,id LIMIT ?`, scope, timeString(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.SyncOutboxItem
	for rows.Next() {
		var v domain.SyncOutboxItem
		var payload, next, created, updated string
		if err := rows.Scan(&v.ID, &v.Scope, &v.Object.Type, &v.Object.ID, &v.Object.Operation, &payload, &v.Object.BaseVersion, &v.Object.SourceVersion, &v.Object.IdempotencyKey, &v.Status, &v.Attempts, &next, &v.LastError, &created, &updated); err != nil {
			return nil, err
		}
		v.Object.Payload = storedRawMessage(payload)
		v.NextAttemptAt = parseTime(next)
		v.CreatedAt = parseTime(created)
		v.UpdatedAt = parseTime(updated)
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Store) SyncOutboxCount(ctx context.Context, scope string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_outbox WHERE scope=? AND status='pending'`, scope).Scan(&count)
	return count, err
}

func (s *Store) AckSync(ctx context.Context, scope string, ids []string, pushedAt time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `DELETE FROM sync_outbox WHERE scope=? AND id=?`, scope, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sync_state(scope,last_push_at,updated_at) VALUES(?,?,?) ON CONFLICT(scope) DO UPDATE SET last_push_at=excluded.last_push_at,last_error='',updated_at=excluded.updated_at`, scope, timeString(pushedAt), timeString(pushedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailSync(ctx context.Context, id, message string, retryAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_outbox SET attempts=attempts+1,last_error=?,next_attempt_at=?,updated_at=? WHERE id=?`, message, timeString(retryAt), timeString(time.Now().UTC()), id)
	return err
}

func (s *Store) SyncWasApplied(ctx context.Context, key string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sync_applied WHERE idempotency_key=?)`, key).Scan(&found)
	return found, err
}

// syncAppliedLookupBatch stays below SQLite's historical 999-variable limit so
// the same statement works on every bundled SQLite version.
const syncAppliedLookupBatch = 500

// SyncAppliedKeys reports which of the given idempotency keys already exist.
// Callers that filter an export used to issue one point query per object; on
// large histories that is hundreds of thousands of round trips. This batches
// the same primary-key lookups, and duplicate keys in the input are queried
// once.
func (s *Store) SyncAppliedKeys(ctx context.Context, keys []string) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	if len(keys) == 0 {
		return result, nil
	}
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	// Sorted input turns the primary-key probes into a mostly sequential walk
	// of the index, which matters when the applied history has many pages.
	sort.Strings(unique)
	for start := 0; start < len(unique); start += syncAppliedLookupBatch {
		end := start + syncAppliedLookupBatch
		if end > len(unique) {
			end = len(unique)
		}
		chunk := unique[start:end]
		placeholders := make([]byte, 0, len(chunk)*2)
		args := make([]any, len(chunk))
		for index, key := range chunk {
			if index > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args[index] = key
		}
		rows, err := s.db.QueryContext(ctx, `SELECT idempotency_key FROM sync_applied WHERE idempotency_key IN (`+string(placeholders)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			result[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

// ForEachSyncAppliedKeyRange walks applied identities with the given prefix in
// primary-key order without materializing the whole key set.
func (s *Store) ForEachSyncAppliedKeyRange(ctx context.Context, prefix string, visit func(string) error) error {
	if prefix == "" {
		return nil
	}
	query := `SELECT idempotency_key FROM sync_applied WHERE idempotency_key >= ?`
	args := []any{prefix}
	if upper := prefixSuccessor(prefix); upper != "" {
		query += ` AND idempotency_key < ?`
		args = append(args, upper)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return err
		}
		if err := visit(key); err != nil {
			return err
		}
	}
	return rows.Err()
}

// prefixSuccessor returns the smallest string greater than every string with
// the given prefix. An all-0xff prefix has no successor.
func prefixSuccessor(prefix string) string {
	bytes := []byte(prefix)
	for index := len(bytes) - 1; index >= 0; index-- {
		if bytes[index] < 0xff {
			bytes[index]++
			return string(bytes[:index+1])
		}
	}
	return ""
}

func (s *Store) MarkSyncApplied(ctx context.Context, scope string, obj domain.SyncObject, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO sync_applied(idempotency_key,scope,object_type,object_id,remote_version,applied_at) VALUES(?,?,?,?,?,?)`, obj.IdempotencyKey, scope, obj.Type, obj.ID, obj.RemoteVersion, timeString(at))
	return err
}

func (s *Store) AddSyncConflict(ctx context.Context, v domain.SyncConflict) error {
	if v.ID == "" {
		v.ID = domain.NewID("conflict")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.Status == "" {
		v.Status = "open"
	}
	var existingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM sync_conflicts WHERE scope=? AND object_type=? AND object_id=? AND status='open' ORDER BY created_at DESC, id DESC LIMIT 1`,
		v.Scope, v.ObjectType, v.ObjectID).Scan(&existingID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE sync_conflicts SET local_payload_json=?,remote_payload_json=?,local_version=?,remote_version=? WHERE id=?`,
			string(v.LocalPayload), string(v.RemotePayload), v.LocalVersion, v.RemoteVersion, existingID)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sync_conflicts(id,scope,object_type,object_id,local_payload_json,remote_payload_json,local_version,remote_version,status,created_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.Scope, v.ObjectType, v.ObjectID, string(v.LocalPayload), string(v.RemotePayload), v.LocalVersion, v.RemoteVersion, v.Status, timeString(v.CreatedAt), timeString(v.ResolvedAt))
	return err
}

func (s *Store) SyncConflicts(ctx context.Context, scope string) ([]domain.SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,scope,object_type,object_id,local_payload_json,remote_payload_json,local_version,remote_version,status,created_at,resolved_at FROM sync_conflicts WHERE scope=? AND status='open' ORDER BY created_at,id`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.SyncConflict
	for rows.Next() {
		var v domain.SyncConflict
		var local, remote, created, resolved string
		if err := rows.Scan(&v.ID, &v.Scope, &v.ObjectType, &v.ObjectID, &local, &remote, &v.LocalVersion, &v.RemoteVersion, &v.Status, &created, &resolved); err != nil {
			return nil, err
		}
		v.LocalPayload = storedRawMessage(local)
		v.RemotePayload = storedRawMessage(remote)
		v.CreatedAt = parseTime(created)
		v.ResolvedAt = parseTime(resolved)
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Store) SyncConflict(ctx context.Context, scope, id string) (domain.SyncConflict, error) {
	var v domain.SyncConflict
	var local, remote, created, resolved string
	err := s.db.QueryRowContext(ctx, `SELECT id,scope,object_type,object_id,local_payload_json,remote_payload_json,local_version,remote_version,status,created_at,resolved_at FROM sync_conflicts WHERE id=? AND scope=? AND status='open'`, id, scope).
		Scan(&v.ID, &v.Scope, &v.ObjectType, &v.ObjectID, &local, &remote, &v.LocalVersion, &v.RemoteVersion, &v.Status, &created, &resolved)
	if err != nil {
		return domain.SyncConflict{}, err
	}
	v.LocalPayload = storedRawMessage(local)
	v.RemotePayload = storedRawMessage(remote)
	v.CreatedAt = parseTime(created)
	v.ResolvedAt = parseTime(resolved)
	return v, nil
}

func (s *Store) SyncOutboxForObject(ctx context.Context, scope, objectType, objectID string) (domain.SyncOutboxItem, error) {
	var v domain.SyncOutboxItem
	var payload, next, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,scope,object_type,object_id,operation,payload_json,base_version,source_version,idempotency_key,status,attempts,next_attempt_at,last_error,created_at,updated_at FROM sync_outbox WHERE scope=? AND object_type=? AND object_id=? ORDER BY created_at,id LIMIT 1`,
		scope, objectType, objectID).
		Scan(&v.ID, &v.Scope, &v.Object.Type, &v.Object.ID, &v.Object.Operation, &payload, &v.Object.BaseVersion, &v.Object.SourceVersion, &v.Object.IdempotencyKey, &v.Status, &v.Attempts, &next, &v.LastError, &created, &updated)
	if err != nil {
		return domain.SyncOutboxItem{}, err
	}
	v.Object.Payload = storedRawMessage(payload)
	v.NextAttemptAt = parseTime(next)
	v.CreatedAt = parseTime(created)
	v.UpdatedAt = parseTime(updated)
	return v, nil
}

func (s *Store) RebaseSyncOutbox(ctx context.Context, id, baseVersion, idempotencyKey string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_outbox SET base_version=?,idempotency_key=?,attempts=0,next_attempt_at='',last_error='',updated_at=? WHERE id=?`,
		baseVersion, idempotencyKey, timeString(time.Now().UTC()), id)
	return err
}

func (s *Store) DropSyncOutboxObject(ctx context.Context, scope, objectType, objectID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sync_outbox WHERE scope=? AND object_type=? AND object_id=?`, scope, objectType, objectID)
	return err
}

func (s *Store) ResolveSyncConflictsForObject(ctx context.Context, scope, objectType, objectID string, at time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE sync_conflicts SET status='resolved',resolved_at=? WHERE scope=? AND object_type=? AND object_id=? AND status='open'`,
		timeString(at), scope, objectType, objectID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// storedRawMessage keeps an empty stored payload as JSON null; an empty
// json.RawMessage is not valid JSON and would make encoding fail.
func storedRawMessage(value string) json.RawMessage {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return json.RawMessage(value)
}
