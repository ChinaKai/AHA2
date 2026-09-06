package centersync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.PutDeviceToken(context.Background(), "device-a", "token-a"); err != nil {
		t.Fatal(err)
	}
	return s
}

func request(t *testing.T, h http.Handler, method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&raw).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, &raw)
	if authenticated {
		r.Header.Set("X-Device-ID", "device-a")
		r.Header.Set("Authorization", "Bearer token-a")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealthAndAuthentication(t *testing.T) {
	s := testStore(t)
	if got := request(t, s.Handler(), "GET", "/healthz", nil, false).Code; got != 200 {
		t.Fatalf("health=%d", got)
	}
	if got := request(t, s.Handler(), "GET", "/v1/sync/pull", nil, false).Code; got != 401 {
		t.Fatalf("unauthenticated=%d", got)
	}
	if !s.Authenticate(context.Background(), "device-a", "token-a") || s.Authenticate(context.Background(), "device-a", "wrong") {
		t.Fatal("unexpected authentication result")
	}
}

func TestPushIsIdempotentAndPullIsIncremental(t *testing.T) {
	s := testStore(t)
	h := s.Handler()
	object := domain.SyncObject{ID: "object-1", Type: "knowledge", Operation: "upsert", Payload: json.RawMessage(`{"name":"demo"}`), IdempotencyKey: "event-1"}
	for i := 0; i < 2; i++ {
		w := request(t, h, "POST", "/v1/sync/push", map[string]any{"scope": "default", "device_id": "device-a", "objects": []domain.SyncObject{object}}, true)
		if w.Code != 200 {
			t.Fatalf("push %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
	w := request(t, h, "GET", "/v1/sync/pull?after=0&limit=10", nil, true)
	if w.Code != 200 {
		t.Fatalf("pull: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Objects []domain.SyncObject `json:"objects"`
		Cursor  string              `json:"cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Objects) != 1 || response.Cursor != "1" || response.Objects[0].IdempotencyKey != "event-1" || !strings.HasPrefix(response.Objects[0].EventID, "center:") || response.Objects[0].EventID == "center:" {
		t.Fatalf("response=%+v", response)
	}
	w = request(t, h, "GET", "/v1/sync/pull?cursor=1", nil, true)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Objects) != 0 || response.Cursor != "1" {
		t.Fatalf("incremental response=%+v", response)
	}
	var cursor int64
	if err := s.db.QueryRow(`SELECT cursor FROM device_cursors WHERE device_id='device-a'`).Scan(&cursor); err != nil || cursor != 1 {
		t.Fatalf("stored cursor=%d err=%v", cursor, err)
	}
}

func TestConcurrentDevicePushKeepsObjectAndEventVersionsAligned(t *testing.T) {
	s := testStore(t)
	if err := s.PutDeviceToken(context.Background(), "device-b", "token-b"); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for _, test := range []struct {
		device, token, key, payload string
	}{
		{"device-a", "token-a", "event-a", `{"source":"a"}`},
		{"device-b", "token-b", "event-b", `{"source":"b"}`},
	} {
		test := test
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			body, _ := json.Marshal(map[string]any{"scope": "default", "device_id": test.device, "objects": []domain.SyncObject{{ID: "shared", Type: "knowledge", Operation: "upsert", Payload: json.RawMessage(test.payload), IdempotencyKey: test.key}}})
			request := httptest.NewRequest(http.MethodPost, "/v1/sync/push", bytes.NewReader(body))
			request.Header.Set("X-Device-ID", test.device)
			request.Header.Set("Authorization", "Bearer "+test.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				errorsFound <- fmt.Errorf("%s push status=%d body=%s", test.device, response.Code, response.Body.String())
				return
			}
			errorsFound <- nil
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	var objectPayload string
	var objectVersion int64
	if err := s.db.QueryRow(`SELECT payload,version FROM sync_objects WHERE object_type='knowledge' AND object_id='shared'`).Scan(&objectPayload, &objectVersion); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT payload,version FROM sync_events WHERE object_type='knowledge' AND object_id='shared' ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var eventPayload string
	var eventVersion int64
	versions := []int64{}
	for rows.Next() {
		if err := rows.Scan(&eventPayload, &eventVersion); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, eventVersion)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 || objectVersion != 2 || objectPayload != eventPayload {
		t.Fatalf("versions=%v object=%s/%d last_event=%s", versions, objectPayload, objectVersion, eventPayload)
	}
}

func TestSecretBundleMustBeOpaqueCiphertext(t *testing.T) {
	s := testStore(t)
	h := s.Handler()
	bad := domain.SyncObject{ID: "secret-1", Type: "secret_bundle", Operation: "upsert", Payload: json.RawMessage(`{"password":"plain"}`), IdempotencyKey: "bad"}
	if got := request(t, h, "POST", "/v1/sync/push", map[string]any{"scope": "default", "objects": []domain.SyncObject{bad}}, true).Code; got != 400 {
		t.Fatalf("plaintext secret status=%d", got)
	}
	good := domain.SyncObject{ID: "secret-1", Type: "secret_bundle", Operation: "upsert", Payload: json.RawMessage(`"age1:opaque-ciphertext"`), IdempotencyKey: "good"}
	if w := request(t, h, "POST", "/v1/sync/push", map[string]any{"scope": "default", "objects": []domain.SyncObject{good}}, true); w.Code != 200 {
		t.Fatalf("encrypted secret: %d %s", w.Code, w.Body.String())
	}
	var payload, bundle string
	if err := s.db.QueryRow(`SELECT payload, encrypted_bundle FROM sync_objects WHERE object_id='secret-1'`).Scan(&payload, &bundle); err != nil {
		t.Fatal(err)
	}
	if payload != "" || bundle != "age1:opaque-ciphertext" {
		t.Fatalf("payload=%q bundle=%q", payload, bundle)
	}
}

func TestOlderVersionDoesNotReplaceObject(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	newer := Event{EventID: "new", ObjectID: "o", ObjectType: "object", Operation: "upsert", Payload: json.RawMessage(`{"v":2}`), Version: 2}
	older := Event{EventID: "old", ObjectID: "o", ObjectType: "object", Operation: "upsert", Payload: json.RawMessage(`{"v":1}`), Version: 1}
	if _, err := s.Push(ctx, "device-a", []Event{newer, older}); err != nil {
		t.Fatal(err)
	}
	var payload string
	var version int64
	if err := s.db.QueryRow(`SELECT payload,version FROM sync_objects WHERE object_id='o'`).Scan(&payload, &version); err != nil {
		t.Fatal(err)
	}
	if payload != `{"v":2}` || version != 2 {
		t.Fatalf("payload=%s version=%d", payload, version)
	}
}

func TestOneTimeDeviceRegistrationStoresOnlyHashes(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	code, created, err := s.EnsureRegistrationCode(ctx, time.Hour)
	if err != nil || !created || code == "" {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if again, created, err := s.EnsureRegistrationCode(ctx, time.Hour); err != nil || created || again != "" {
		t.Fatalf("second ensure created=%v code=%q err=%v", created, again, err)
	}
	var storedCode string
	if err := s.db.QueryRow(`SELECT code_hash FROM registration_codes`).Scan(&storedCode); err != nil {
		t.Fatal(err)
	}
	if storedCode == code || storedCode != tokenHash(code) {
		t.Fatal("registration code was not hash-only")
	}
	bootstrapRequest := httptest.NewRequest(http.MethodPost, "/v1/devices/bootstrap-code", nil)
	bootstrapRequest.RemoteAddr = "127.0.0.1:12345"
	bootstrapResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(bootstrapResponse, bootstrapRequest)
	if bootstrapResponse.Code != http.StatusOK || bootstrapResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bootstrap=%d", bootstrapResponse.Code)
	}
	var bootstrap struct {
		RegistrationCode string `json:"registration_code"`
	}
	if err := json.Unmarshal(bootstrapResponse.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap.RegistrationCode != code {
		t.Fatal("unexpected bootstrap code")
	}
	secondBootstrap := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/v1/devices/bootstrap-code", nil)
	secondRequest.RemoteAddr = "127.0.0.1:12345"
	s.Handler().ServeHTTP(secondBootstrap, secondRequest)
	if secondBootstrap.Code != http.StatusNotFound {
		t.Fatalf("bootstrap code returned twice: %d", secondBootstrap.Code)
	}
	remoteBootstrap := httptest.NewRecorder()
	remoteRequest := httptest.NewRequest(http.MethodPost, "/v1/devices/bootstrap-code", nil)
	remoteRequest.RemoteAddr = "192.0.2.10:12345"
	s.Handler().ServeHTTP(remoteBootstrap, remoteRequest)
	if remoteBootstrap.Code != http.StatusNotFound {
		t.Fatalf("remote bootstrap=%d", remoteBootstrap.Code)
	}
	w := request(t, s.Handler(), http.MethodPost, "/v1/devices/register", map[string]string{"device_id": "device-new", "registration_code": code}, false)
	if w.Code != http.StatusCreated {
		t.Fatalf("register=%d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("registration response is cacheable")
	}
	var response struct {
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		Token      string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.DeviceID == "device-new" || !strings.HasPrefix(response.DeviceID, "dev_") || response.DeviceName != "device-new" || response.Token == "" || !s.Authenticate(ctx, response.DeviceID, response.Token) {
		t.Fatal("returned token does not authenticate")
	}
	var storedToken string
	if err := s.db.QueryRow(`SELECT token_hash FROM device_tokens WHERE device_id=?`, response.DeviceID).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if storedToken == response.Token || storedToken != tokenHash(response.Token) {
		t.Fatal("device token was not hash-only")
	}
	w = request(t, s.Handler(), http.MethodPost, "/v1/devices/register", map[string]string{"device_id": "device-other", "registration_code": code}, false)
	if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), code) {
		t.Fatalf("reused code=%d %q", w.Code, w.Body.String())
	}
}

func TestDeviceRenameKeepsCenterGeneratedID(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	code, err := s.CreateRegistrationCode(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.RegisterNamedDevice(ctx, "Laptop", code)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"device_name": "Work Laptop"}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPatch, "/v1/devices/self", bytes.NewReader(raw))
	r.Header.Set("X-Device-ID", credential.DeviceID)
	r.Header.Set("Authorization", "Bearer "+credential.Token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rename=%d %s", w.Code, w.Body.String())
	}
	var id, name string
	if err := s.db.QueryRow(`SELECT device_id,device_name FROM device_tokens WHERE device_id=?`, credential.DeviceID).Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	if id != credential.DeviceID || name != "Work Laptop" {
		t.Fatalf("id=%q name=%q", id, name)
	}
}

func TestLegacyCenterDeviceIDGetsCompatibleName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`CREATE TABLE device_tokens(device_id TEXT PRIMARY KEY,token_hash TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1); INSERT INTO device_tokens(device_id,token_hash,enabled) VALUES('legacy-device','hash',1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var name string
	if err := s.db.QueryRow(`SELECT device_name FROM device_tokens WHERE device_id='legacy-device'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "legacy-device" {
		t.Fatalf("name=%q", name)
	}
}

func TestDeviceTokenRevocationAndRotation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.RevokeDevice(ctx, "device-a"); err != nil {
		t.Fatal(err)
	}
	if s.Authenticate(ctx, "device-a", "token-a") {
		t.Fatal("revoked token authenticated")
	}
	rotated, err := s.RotateDeviceToken(ctx, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	if rotated == "" || s.Authenticate(ctx, "device-a", "token-a") || !s.Authenticate(ctx, "device-a", rotated) {
		t.Fatal("rotation did not replace token")
	}
	var generation int
	var revoked string
	if err := s.db.QueryRow(`SELECT generation,revoked_at FROM device_tokens WHERE device_id='device-a'`).Scan(&generation, &revoked); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || revoked != "" {
		t.Fatalf("generation=%d revoked=%q", generation, revoked)
	}
}

func TestRegistrationRejectsExpiredCodeAndInvalidDeviceID(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	code, err := s.CreateRegistrationCode(ctx, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := s.RegisterNamedDevice(ctx, "device-a", code); err == nil {
		t.Fatal("expired code accepted")
	}
	code, err = s.CreateRegistrationCode(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterNamedDevice(ctx, "bad\ndevice", code); err == nil {
		t.Fatal("invalid device name accepted")
	}
}
