package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func newModelBatchDeleteTestServer(t *testing.T) (*store.Store, *httptest.Server, *http.Client, string, string) {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	if err := database.UpsertProvider(ctx, domain.Provider{
		ID: "provider-batch-http", Name: "Batch HTTP Provider", BaseURL: "https://example.test/v1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"model-batch-http-a", "model-batch-http-b"} {
		if err := database.UpsertModel(ctx, domain.Model{
			ID: id, DisplayName: id, ProviderID: "provider-batch-http", Backend: "codex",
			WireModel: "wire-" + id, WireAPI: "chat", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return database, server, client, csrf, databasePath
}

func batchDeleteAuditRows(t *testing.T, databasePath, action string) []map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data_json FROM audit_events WHERE action=? ORDER BY sequence`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatalf("audit data %q: %v", raw, err)
		}
		result = append(result, data)
	}
	return result
}

func TestBatchDeleteModelsEndpointCreatesOneApproval(t *testing.T) {
	database, server, client, csrf, databasePath := newModelBatchDeleteTestServer(t)
	modelIDs := []string{"model-batch-http-a", "model-batch-http-b"}

	// Blank and duplicate ids must not inflate the audit or the approval count.
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models/batch-delete", map[string]any{
		"model_ids": []string{modelIDs[0], "", modelIDs[1], modelIDs[0]},
	}, csrf)
	var body struct {
		OK       bool               `json:"ok"`
		Approval domain.PetApproval `json:"approval"`
	}
	decodeResponse(t, response, &body)
	if response.StatusCode != http.StatusAccepted || !body.OK {
		t.Fatalf("status=%d body=%#v", response.StatusCode, body)
	}
	if body.Approval.Kind != "delete_models" || body.Approval.Status != "pending" {
		t.Fatalf("approval=%#v", body.Approval)
	}
	var payload struct {
		ModelIDs   []string `json:"model_ids"`
		ModelNames []string `json:"model_names"`
		Count      int      `json:"count"`
	}
	if err := json.Unmarshal([]byte(body.Approval.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || len(payload.ModelIDs) != 2 || len(payload.ModelNames) != 2 {
		t.Fatalf("payload=%#v", payload)
	}
	audits := batchDeleteAuditRows(t, databasePath, "model.batch_delete_request")
	if len(audits) != 1 {
		t.Fatalf("submit audit rows = %d, want 1", len(audits))
	}
	auditIDs, _ := audits[0]["model_ids"].([]any)
	if len(auditIDs) != 2 || auditIDs[0] != modelIDs[0] || auditIDs[1] != modelIDs[1] {
		t.Fatalf("submit audit model_ids = %#v", audits[0]["model_ids"])
	}
	if count, _ := audits[0]["count"].(float64); int(count) != 2 {
		t.Fatalf("submit audit count = %#v", audits[0]["count"])
	}
	if audits[0]["approval_id"] != body.Approval.ID {
		t.Fatalf("submit audit approval_id = %#v", audits[0]["approval_id"])
	}
	// Requesting again before approval must reuse the pending card.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models/batch-delete", map[string]any{
		"model_ids": []string{modelIDs[1], modelIDs[0]},
	}, csrf)
	var second struct {
		Approval domain.PetApproval `json:"approval"`
	}
	decodeResponse(t, response, &second)
	if response.StatusCode != http.StatusAccepted || second.Approval.ID != body.Approval.ID {
		t.Fatalf("duplicate request status=%d approval=%#v", response.StatusCode, second.Approval)
	}
	pending, err := database.ListPendingPetApprovalsByKind(context.Background(), "delete_models")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending batch approval, got %d", len(pending))
	}
}

func TestBatchDeleteModelsEndpointRejectsInvalidInput(t *testing.T) {
	_, server, client, csrf, _ := newModelBatchDeleteTestServer(t)
	for _, test := range []struct {
		name    string
		payload map[string]any
	}{
		{"empty", map[string]any{"model_ids": []string{}}},
		{"unknown", map[string]any{"model_ids": []string{"model-missing"}}},
	} {
		response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models/batch-delete", test.payload, csrf)
		var body map[string]any
		decodeResponse(t, response, &body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%v", test.name, response.StatusCode, body)
		}
		if message, _ := body["message"].(string); message == "" {
			t.Fatalf("%s: missing clear error message: %v", test.name, body)
		}
	}
}
