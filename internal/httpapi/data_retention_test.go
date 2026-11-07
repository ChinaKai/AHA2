package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestHardwareIORetentionEndpoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	taskID := createHardwareAPITask(t, database).ID
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()

	// Unauthenticated callers are rejected on every new route.
	anonymous := newCookieClient(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system/data/database"},
		{http.MethodPut, "/api/v1/system/data/hardware-io/retention"},
		{http.MethodPost, "/api/v1/system/data/hardware-io/preview"},
		{http.MethodPost, "/api/v1/system/data/hardware-io/purge"},
	} {
		response := requestJSON(t, anonymous, route.method, server.URL+route.path, map[string]any{"confirm": true}, "")
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status=%d", route.method, route.path, response.StatusCode)
		}
	}

	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for index := 0; index < 50; index++ {
		// Half-day offsets keep every row away from a cutoff computed a moment
		// later by the server.
		created := now.Add(-time.Duration(60-index)*24*time.Hour + 12*time.Hour)
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO hardware_io(id,task_id,hardware_id,transport,direction,data,encoding,source,created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			domain.NewID("hardware_io"), taskID, "board", "serial", "rx", "boot line\r\n", "text", "bridge",
			created.Format(time.RFC3339Nano),
		); err != nil {
			t.Fatal(err)
		}
	}
	// Rows 1..30 are 59.5..30.5 days old; rows 31..50 are within 30 days.

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/system/data/database", nil, "")
	var stats struct {
		Database domain.DatabaseStats `json:"database"`
	}
	decodeResponse(t, response, &stats)
	if response.StatusCode != http.StatusOK || stats.Database.HardwareIO.EstimatedRows != 50 || len(stats.Database.Categories) != 11 {
		t.Fatalf("stats status=%d body=%#v", response.StatusCode, stats.Database)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/system/data/hardware-io/retention",
		map[string]any{"retention_days": 0}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid retention status=%d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/system/data/hardware-io/retention",
		map[string]any{"retention_days": 45, "max_delete_rows": 5000}, csrf)
	var saved struct {
		Retention domain.HardwareIORetentionSettings `json:"retention"`
	}
	decodeResponse(t, response, &saved)
	if response.StatusCode != http.StatusOK || saved.Retention.RetentionDays != 45 || saved.Retention.MaxDeleteRows != 5000 {
		t.Fatalf("save status=%d body=%#v", response.StatusCode, saved)
	}

	// Preview with the stored 45 days: rows 59.5..45.5 days old (15 rows).
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/preview", map[string]any{}, csrf)
	var preview struct {
		Plan domain.HardwareIOPurgePlan `json:"plan"`
	}
	decodeResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Plan.RunRows != 15 || preview.Plan.RetentionDays != 45 {
		t.Fatalf("preview status=%d plan=%#v", response.StatusCode, preview.Plan)
	}
	// A preview override does not change the stored settings.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/preview", map[string]any{"retention_days": 30}, csrf)
	decodeResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Plan.RunRows != 30 {
		t.Fatalf("override preview status=%d plan=%#v", response.StatusCode, preview.Plan)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/preview", map[string]any{"max_delete_rows": 10}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid preview limit status=%d", response.StatusCode)
	}
	if hardwareIORows(t, raw) != 50 {
		t.Fatal("preview deleted rows")
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/purge", map[string]any{"retention_days": 30}, csrf)
	var rejected map[string]any
	decodeResponse(t, response, &rejected)
	if response.StatusCode != http.StatusBadRequest || rejected["error"] != "confirmation_required" || hardwareIORows(t, raw) != 50 {
		t.Fatalf("unconfirmed purge status=%d body=%#v", response.StatusCode, rejected)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/purge", map[string]any{"retention_days": 30, "confirm": true}, "")
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || hardwareIORows(t, raw) != 50 {
		t.Fatalf("purge without CSRF status=%d", response.StatusCode)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/purge", map[string]any{"retention_days": 30, "confirm": true}, csrf)
	var purged struct {
		Result   domain.HardwareIOPurgeResult `json:"result"`
		Audit    map[string]any               `json:"audit"`
		Database *domain.DatabaseStats        `json:"database"`
	}
	decodeResponse(t, response, &purged)
	if response.StatusCode != http.StatusOK || purged.Result.DeletedRows != 30 || purged.Audit["action"] != "data.hardware_io.purge" || purged.Database == nil {
		t.Fatalf("purge status=%d body=%#v", response.StatusCode, purged)
	}
	if hardwareIORows(t, raw) != 20 {
		t.Fatalf("rows after purge=%d", hardwareIORows(t, raw))
	}
	var oldest string
	if err := raw.QueryRowContext(ctx, `SELECT created_at FROM hardware_io ORDER BY sequence LIMIT 1`).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if parsed, _ := time.Parse(time.RFC3339Nano, oldest); parsed.Before(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("old row survived: %s", oldest)
	}
	var taskStillExists bool
	if err := raw.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=?)`, taskID).Scan(&taskStillExists); err != nil || !taskStillExists {
		t.Fatalf("purge touched tasks: %v %v", taskStillExists, err)
	}

	// The confirmed purge and the settings change are both audited.
	var ownerID, dataJSON string
	if err := raw.QueryRowContext(ctx, `
		SELECT owner_id,data_json FROM audit_events WHERE action='data.hardware_io.purge'`).Scan(&ownerID, &dataJSON); err != nil {
		t.Fatal(err)
	}
	var auditData map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &auditData); err != nil {
		t.Fatal(err)
	}
	if ownerID == "" || auditData["deleted_rows"] != float64(30) || auditData["retention_days"] != float64(30) {
		t.Fatalf("purge audit owner=%q data=%#v", ownerID, auditData)
	}
	var settingAudits int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='data.hardware_io.retention.update'`).Scan(&settingAudits); err != nil || settingAudits != 1 {
		t.Fatalf("settings audit=%d err=%v", settingAudits, err)
	}

	// Nothing left to delete is a reported skip, not an error.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/system/data/hardware-io/purge", map[string]any{"retention_days": 30, "confirm": true}, csrf)
	decodeResponse(t, response, &purged)
	if response.StatusCode != http.StatusOK || purged.Result.DeletedRows != 0 || purged.Result.SkipReason != domain.HardwareIOPurgeSkipNothingEligible {
		t.Fatalf("second purge status=%d body=%#v", response.StatusCode, purged.Result)
	}
}

func hardwareIORows(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM hardware_io`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
