package centersync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	db            *sql.DB
	bootstrapMu   sync.Mutex
	bootstrapCode string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS device_tokens (
 device_id TEXT PRIMARY KEY, device_name TEXT NOT NULL DEFAULT '', token_hash TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
 generation INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL DEFAULT '', rotated_at TEXT NOT NULL DEFAULT '', revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS registration_codes (
 code_hash TEXT PRIMARY KEY, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sync_objects (
 object_id TEXT NOT NULL, object_type TEXT NOT NULL, payload TEXT, encrypted_bundle TEXT,
 deleted INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY(object_type, object_id)
);
CREATE TABLE IF NOT EXISTS sync_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, event_id TEXT NOT NULL,
 object_id TEXT NOT NULL, object_type TEXT NOT NULL, operation TEXT NOT NULL,
 payload TEXT, encrypted_bundle TEXT, version INTEGER NOT NULL, created_at TEXT NOT NULL,
 UNIQUE(device_id, event_id)
);
CREATE TABLE IF NOT EXISTS device_cursors (
 device_id TEXT PRIMARY KEY, cursor INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sync_events_sequence ON sync_events(sequence);
`)
	if err != nil {
		return fmt.Errorf("migrate sync database: %w", err)
	}
	for _, migration := range []string{
		`ALTER TABLE device_tokens ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE device_tokens ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE device_tokens ADD COLUMN rotated_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE device_tokens ADD COLUMN revoked_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE device_tokens ADD COLUMN device_name TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.ExecContext(ctx, migration); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("migrate device tokens: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE device_tokens SET device_name=device_id WHERE device_name=''`); err != nil {
		return fmt.Errorf("migrate legacy device names: %w", err)
	}
	return nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) PutDeviceToken(ctx context.Context, deviceID, token string) error {
	if strings.TrimSpace(deviceID) == "" || token == "" {
		return errors.New("device id and token are required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO device_tokens(device_id,device_name,token_hash,enabled,generation,created_at) VALUES(?,?,?,1,1,?)
ON CONFLICT(device_id) DO UPDATE SET token_hash=excluded.token_hash,enabled=1,generation=device_tokens.generation+1,rotated_at=excluded.created_at,revoked_at=''`, deviceID, deviceID, tokenHash(token), now)
	return err
}

func (s *Store) Authenticate(ctx context.Context, deviceID, token string) bool {
	var expected string
	if deviceID == "" || token == "" || s.db.QueryRowContext(ctx, `SELECT token_hash FROM device_tokens WHERE device_id=? AND enabled=1`, deviceID).Scan(&expected) != nil {
		return false
	}
	actual := tokenHash(token)
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func randomCredential() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EnsureRegistrationCode creates the installation's first bootstrap code. The
// plaintext is returned only on creation and is never persisted by the Store.
func (s *Store) EnsureRegistrationCode(ctx context.Context, ttl time.Duration) (string, bool, error) {
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()
	if s.bootstrapCode != "" {
		return "", false, nil
	}
	var devices int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_tokens`).Scan(&devices); err != nil {
		return "", false, err
	}
	if devices > 0 {
		return "", false, nil
	}
	code, err := s.CreateRegistrationCode(ctx, ttl)
	if err == nil {
		s.bootstrapCode = code
	}
	return code, err == nil, err
}

// TakeRegistrationCode returns the in-memory bootstrap code once. It is meant
// for a loopback-only delivery endpoint and never reads plaintext from disk.
func (s *Store) TakeRegistrationCode() (string, bool) {
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()
	if s.bootstrapCode == "" {
		return "", false
	}
	code := s.bootstrapCode
	s.bootstrapCode = ""
	return code, true
}

// CreateRegistrationCode rotates the bootstrap code, invalidating every prior
// unconsumed code. It returns plaintext exactly once to the caller.
func (s *Store) CreateRegistrationCode(ctx context.Context, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	code, err := randomCredential()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE registration_codes SET consumed_at=? WHERE consumed_at=''`, now.Format(time.RFC3339Nano)); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO registration_codes(code_hash,created_at,expires_at,consumed_at) VALUES(?,?,?,'')`, tokenHash(code), now.Format(time.RFC3339Nano), now.Add(ttl).Format(time.RFC3339Nano)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return code, nil
}

func validDeviceName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 128 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

type DeviceCredential struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Token      string `json:"token"`
}

func newDeviceID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}
	return "dev_" + hex.EncodeToString(raw), nil
}

func (s *Store) RegisterNamedDevice(ctx context.Context, deviceName, code string) (DeviceCredential, error) {
	deviceName = strings.TrimSpace(deviceName)
	if !validDeviceName(deviceName) || code == "" {
		return DeviceCredential{}, errors.New("device name and registration code are required")
	}
	deviceID, err := newDeviceID()
	if err != nil {
		return DeviceCredential{}, err
	}
	token, err := randomCredential()
	if err != nil {
		return DeviceCredential{}, err
	}
	nowText := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeviceCredential{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE registration_codes SET consumed_at=? WHERE code_hash=? AND consumed_at='' AND expires_at>?`, nowText, tokenHash(code), nowText)
	if err != nil {
		return DeviceCredential{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return DeviceCredential{}, errors.New("invalid or expired registration code")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO device_tokens(device_id,device_name,token_hash,enabled,generation,created_at) VALUES(?,?,?,1,1,?)`, deviceID, deviceName, tokenHash(token), nowText); err != nil {
		return DeviceCredential{}, err
	}
	if err = tx.Commit(); err != nil {
		return DeviceCredential{}, err
	}
	return DeviceCredential{DeviceID: deviceID, DeviceName: deviceName, Token: token}, nil
}

func (s *Store) RenameDevice(ctx context.Context, deviceID, deviceName string) error {
	deviceName = strings.TrimSpace(deviceName)
	if !validDeviceName(deviceName) {
		return errors.New("valid device name is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE device_tokens SET device_name=? WHERE device_id=? AND enabled=1`, deviceName, deviceID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RevokeDevice(ctx context.Context, deviceID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE device_tokens SET enabled=0,revoked_at=? WHERE device_id=? AND enabled=1`, now, deviceID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RotateDeviceToken(ctx context.Context, deviceID string) (string, error) {
	token, err := randomCredential()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE device_tokens SET token_hash=?,enabled=1,generation=generation+1,rotated_at=?,revoked_at='' WHERE device_id=?`, tokenHash(token), now, deviceID)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", sql.ErrNoRows
	}
	return token, nil
}

type Event struct {
	EventID         string          `json:"event_id"`
	ObjectID        string          `json:"object_id"`
	ObjectType      string          `json:"object_type"`
	Operation       string          `json:"operation"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	EncryptedBundle string          `json:"encrypted_bundle,omitempty"`
	Version         int64           `json:"version"`
	Sequence        int64           `json:"sequence,omitempty"`
	CreatedAt       string          `json:"created_at,omitempty"`
}

func validateEvent(e Event) error {
	if e.EventID == "" || e.ObjectID == "" || e.Version < 1 {
		return errors.New("event_id, object_id, and positive version are required")
	}
	if strings.TrimSpace(e.ObjectType) == "" {
		return errors.New("object_type is required")
	}
	if e.Operation != "upsert" && e.Operation != "delete" {
		return errors.New("operation must be upsert or delete")
	}
	if e.Operation == "delete" {
		if len(e.Payload) != 0 || e.EncryptedBundle != "" {
			return errors.New("delete must not contain content")
		}
		return nil
	}
	if e.ObjectType == "secret_bundle" {
		if e.EncryptedBundle == "" || len(e.Payload) != 0 {
			return errors.New("secret_bundle requires encrypted_bundle and forbids payload")
		}
		return nil
	}
	if e.EncryptedBundle != "" || len(e.Payload) == 0 || !json.Valid(e.Payload) {
		return errors.New("object requires valid JSON payload and forbids encrypted_bundle")
	}
	return nil
}

func (s *Store) Push(ctx context.Context, deviceID string, events []Event) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seqs := make([]int64, 0, len(events))
	for _, e := range events {
		if err := validateEvent(e); err != nil {
			return nil, err
		}
		var existing int64
		err := tx.QueryRowContext(ctx, `SELECT sequence FROM sync_events WHERE device_id=? AND event_id=?`, deviceID, e.EventID).Scan(&existing)
		if err == nil {
			seqs = append(seqs, existing)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx, `INSERT INTO sync_events(device_id,event_id,object_id,object_type,operation,payload,encrypted_bundle,version,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, deviceID, e.EventID, e.ObjectID, e.ObjectType, e.Operation, string(e.Payload), e.EncryptedBundle, e.Version, now)
		if err != nil {
			return nil, err
		}
		seq, _ := result.LastInsertId()
		seqs = append(seqs, seq)
		deleted := 0
		if e.Operation == "delete" {
			deleted = 1
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sync_objects(object_id,object_type,payload,encrypted_bundle,deleted,version,updated_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(object_type,object_id) DO UPDATE SET payload=excluded.payload,encrypted_bundle=excluded.encrypted_bundle,deleted=excluded.deleted,version=excluded.version,updated_at=excluded.updated_at WHERE excluded.version > sync_objects.version`, e.ObjectID, e.ObjectType, string(e.Payload), e.EncryptedBundle, deleted, e.Version, now)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return seqs, nil
}

func (s *Store) Pull(ctx context.Context, deviceID string, after int64, limit int) ([]Event, int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,event_id,object_id,object_type,operation,payload,encrypted_bundle,version,created_at FROM sync_events WHERE sequence>? ORDER BY sequence LIMIT ?`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	events := []Event{}
	cursor := after
	for rows.Next() {
		var e Event
		var payload string
		if err := rows.Scan(&e.Sequence, &e.EventID, &e.ObjectID, &e.ObjectType, &e.Operation, &payload, &e.EncryptedBundle, &e.Version, &e.CreatedAt); err != nil {
			return nil, after, err
		}
		if payload != "" {
			e.Payload = json.RawMessage(payload)
		}
		events = append(events, e)
		cursor = e.Sequence
	}
	if err := rows.Err(); err != nil {
		return nil, after, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO device_cursors(device_id,cursor,updated_at) VALUES(?,?,?) ON CONFLICT(device_id) DO UPDATE SET cursor=MAX(device_cursors.cursor,excluded.cursor),updated_at=excluded.updated_at`, deviceID, cursor, time.Now().UTC().Format(time.RFC3339Nano))
	return events, cursor, err
}

func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.PingContext(r.Context()); err != nil {
			http.Error(w, "unhealthy", 503)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/devices/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DeviceID         string `json:"device_id"` // legacy clients used this as the requested name
			DeviceName       string `json:"device_name"`
			RegistrationCode string `json:"registration_code"`
		}
		if !decode(w, r, &req) {
			return
		}
		name := req.DeviceName
		if strings.TrimSpace(name) == "" {
			name = req.DeviceID
		}
		credential, err := s.RegisterNamedDevice(r.Context(), name, req.RegistrationCode)
		if err != nil {
			http.Error(w, "registration failed", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, credential)
	})
	mux.HandleFunc("PATCH /v1/devices/self", s.auth(func(w http.ResponseWriter, r *http.Request, device string) {
		var req struct {
			DeviceName string `json:"device_name"`
		}
		if !decode(w, r, &req) {
			return
		}
		if err := s.RenameDevice(r.Context(), device, req.DeviceName); err != nil {
			http.Error(w, "invalid device name", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"device_id": device, "device_name": strings.TrimSpace(req.DeviceName)})
	}))
	mux.HandleFunc("POST /v1/devices/bootstrap-code", func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		code, ok := s.TakeRegistrationCode()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"registration_code": code})
	})
	mux.HandleFunc("POST /v1/sync/push", s.auth(func(w http.ResponseWriter, r *http.Request, device string) {
		var req struct {
			Scope    string              `json:"scope"`
			DeviceID string              `json:"device_id"`
			Objects  []domain.SyncObject `json:"objects"`
		}
		if !decode(w, r, &req) {
			return
		}
		if req.DeviceID != "" && req.DeviceID != device {
			http.Error(w, "device identity mismatch", http.StatusForbidden)
			return
		}
		acked := make([]string, 0, len(req.Objects))
		conflicts := make([]domain.SyncConflict, 0)
		for _, object := range req.Objects {
			if object.IdempotencyKey == "" || object.ID == "" || object.Type == "" ||
				(object.Operation != "upsert" && object.Operation != "delete") {
				http.Error(w, "invalid sync object", http.StatusBadRequest)
				return
			}
			var existingSequence int64
			if err := s.db.QueryRowContext(r.Context(), `SELECT sequence FROM sync_events WHERE device_id=? AND event_id=?`, device, object.IdempotencyKey).Scan(&existingSequence); err == nil {
				acked = append(acked, object.IdempotencyKey)
				continue
			} else if !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "read sync object failed", http.StatusInternalServerError)
				return
			}
			var currentVersion int64
			var currentPayload string
			err := s.db.QueryRowContext(r.Context(), `SELECT version,payload FROM sync_objects WHERE object_type=? AND object_id=?`, object.Type, object.ID).Scan(&currentVersion, &currentPayload)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "read sync object failed", http.StatusInternalServerError)
				return
			}
			expected := int64(0)
			if object.BaseVersion != "" {
				expected, err = strconv.ParseInt(object.BaseVersion, 10, 64)
				if err != nil || expected < 0 {
					http.Error(w, "invalid base_version", http.StatusBadRequest)
					return
				}
			} else {
				expected = currentVersion
			}
			if currentVersion != expected {
				conflicts = append(conflicts, domain.SyncConflict{Scope: req.Scope, ObjectType: object.Type, ObjectID: object.ID, LocalPayload: object.Payload, RemotePayload: json.RawMessage(currentPayload), LocalVersion: object.BaseVersion, RemoteVersion: strconv.FormatInt(currentVersion, 10)})
				continue
			}
			event := Event{EventID: object.IdempotencyKey, ObjectID: object.ID, ObjectType: object.Type, Operation: object.Operation, Payload: object.Payload, Version: currentVersion + 1}
			if object.Type == "secret_bundle" && object.Operation == "upsert" {
				if json.Unmarshal(object.Payload, &event.EncryptedBundle) != nil || event.EncryptedBundle == "" {
					http.Error(w, "secret_bundle requires opaque ciphertext", http.StatusBadRequest)
					return
				}
				event.Payload = nil
			}
			if _, err := s.Push(r.Context(), device, []Event{event}); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			acked = append(acked, object.IdempotencyKey)
		}
		writeJSON(w, http.StatusOK, map[string]any{"acked_keys": acked, "conflicts": conflicts})
	}))
	mux.HandleFunc("GET /v1/sync/pull", s.auth(func(w http.ResponseWriter, r *http.Request, device string) {
		rawCursor := r.URL.Query().Get("cursor")
		after, err := strconv.ParseInt(rawCursor, 10, 64)
		if rawCursor == "" {
			after = 0
			err = nil
		}
		if err != nil || after < 0 {
			http.Error(w, "invalid after", 400)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		events, cursor, err := s.Pull(r.Context(), device, after, limit)
		if err != nil {
			http.Error(w, "pull failed", 500)
			return
		}
		objects := make([]domain.SyncObject, 0, len(events))
		for _, event := range events {
			payload := event.Payload
			if event.ObjectType == "secret_bundle" && event.EncryptedBundle != "" {
				payload, _ = json.Marshal(event.EncryptedBundle)
			}
			objects = append(objects, domain.SyncObject{Type: event.ObjectType, ID: event.ObjectID, Operation: event.Operation, Payload: payload, RemoteVersion: strconv.FormatInt(event.Version, 10), IdempotencyKey: event.EventID})
		}
		writeJSON(w, http.StatusOK, map[string]any{"objects": objects, "cursor": strconv.FormatInt(cursor, 10), "has_more": len(events) == limit && limit > 0})
	}))
	return mux
}

func (s *Store) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device := strings.TrimSpace(r.Header.Get("X-Device-ID"))
		auth := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if !s.Authenticate(r.Context(), device, token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r, device)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
