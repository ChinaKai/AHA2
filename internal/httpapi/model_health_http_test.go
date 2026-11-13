package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type modelHealthUpstream struct {
	server *httptest.Server
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
	auth   []string
}

func newModelHealthUpstream(t *testing.T, respond func(writer http.ResponseWriter, request *http.Request)) *modelHealthUpstream {
	t.Helper()
	upstream := &modelHealthUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		upstream.mu.Lock()
		upstream.paths = append(upstream.paths, request.URL.Path)
		upstream.bodies = append(upstream.bodies, payload)
		upstream.auth = append(upstream.auth, request.Header.Get("Authorization"))
		upstream.mu.Unlock()
		respond(writer, request)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func newModelHealthTestServer(t *testing.T, upstreamURL, apiKey string) (*store.Store, *httptest.Server, *http.Client, string, domain.Model) {
	t.Helper()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := domain.Provider{
		ID: "provider-health-test", Name: "Provider Health Test", BaseURL: upstreamURL + "/v1", AuthStyle: "bearer",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	values := map[string]string{}
	if apiKey != "" {
		provider.CredentialRef = "provider/" + provider.ID + "/credential"
		provider.CredentialConfigured = true
		values[provider.CredentialRef] = apiKey
	}
	if err := database.UpsertProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-health-test", DisplayName: "Health Model", ProviderID: provider.ID,
		Backend: "codex", WireModel: "wire-model", WireAPI: "chat_completions",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := database.UpsertModel(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	apiServer := New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), Secrets: &fakeSecretStore{values: values}})
	server := httptest.NewServer(apiServer.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return database, server, client, csrf, model
}

func TestModelHealthCheckEndpointClassifiesAndRedacts(t *testing.T) {
	t.Parallel()
	upstream := newModelHealthUpstream(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Retry-After", "17")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"message":"rate limited","api_key":"health-secret"}}`))
	})
	database, server, client, csrf, model := newModelHealthTestServer(t, upstream.server.URL, "health-secret")

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models/"+model.ID+"/health-check", nil, csrf)
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, raw)
	}
	if strings.Contains(string(raw), "health-secret") {
		t.Fatalf("response leaked the API key: %s", raw)
	}
	var payload struct {
		Health struct {
			Status            string `json:"status"`
			HTTPStatus        int    `json:"http_status"`
			Endpoint          string `json:"endpoint"`
			RetryAfterSeconds int    `json:"retry_after_seconds"`
			Detail            string `json:"detail"`
		} `json:"health"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Health.Status != "rate_limited" || payload.Health.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("health=%#v want rate_limited/429", payload.Health)
	}
	if payload.Health.RetryAfterSeconds != 17 {
		t.Fatalf("retry_after_seconds=%d want 17", payload.Health.RetryAfterSeconds)
	}
	if !strings.HasSuffix(payload.Health.Endpoint, "/v1/chat/completions") {
		t.Fatalf("endpoint=%q", payload.Health.Endpoint)
	}
	if !strings.Contains(payload.Health.Detail, "rate limited") {
		t.Fatalf("detail lost the evidence: %q", payload.Health.Detail)
	}

	upstream.mu.Lock()
	if len(upstream.bodies) != 1 {
		upstream.mu.Unlock()
		t.Fatalf("upstream requests=%d want 1", len(upstream.bodies))
	}
	body := upstream.bodies[0]
	upstreamAuth := upstream.auth[0]
	upstream.mu.Unlock()
	if wireModel, _ := body["model"].(string); wireModel != "wire-model" {
		t.Fatalf("wire model=%#v", body["model"])
	}
	if maxTokens, _ := body["max_tokens"].(float64); maxTokens < 16 {
		t.Fatalf("max_tokens=%#v", body["max_tokens"])
	}
	if upstreamAuth != "Bearer health-secret" {
		t.Fatalf("authorization header=%q", upstreamAuth)
	}

	// A health check must not modify the model record or its provider.
	stored, err := database.Model(context.Background(), model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WireModel != model.WireModel || stored.WireAPI != model.WireAPI || !stored.UpdatedAt.Equal(model.UpdatedAt) {
		t.Fatalf("health check mutated the model: %#v", stored)
	}
}

func TestModelHealthCheckMissingModelAndCredential(t *testing.T) {
	t.Parallel()
	upstream := newModelHealthUpstream(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	})
	_, server, client, csrf, model := newModelHealthTestServer(t, upstream.server.URL, "")

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models/model-does-not-exist/health-check", nil, csrf)
	var missing map[string]any
	decodeResponse(t, response, &missing)
	if response.StatusCode != http.StatusNotFound || missing["error"] != "model_not_found" {
		t.Fatalf("missing model status=%d payload=%v", response.StatusCode, missing)
	}

	// No credential stored: the account cannot call anything, and the handler
	// still answers 200 with a classified health result (no global error).
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models/"+model.ID+"/health-check", nil, csrf)
	var payload struct {
		Health struct {
			Status     string `json:"status"`
			HTTPStatus int    `json:"http_status"`
		} `json:"health"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Health.Status != "unauthorized" {
		t.Fatalf("missing credential status=%d payload=%#v", response.StatusCode, payload)
	}
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if len(upstream.paths) != 0 {
		t.Fatalf("a missing credential must not reach the gateway: %v", upstream.paths)
	}
}

func TestModelHealthCheckRequiresOwnerSession(t *testing.T) {
	t.Parallel()
	upstream := newModelHealthUpstream(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	_, server, _, _, model := newModelHealthTestServer(t, upstream.server.URL, "health-secret")
	response, err := http.Post(server.URL+"/api/v1/models/"+model.ID+"/health-check", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated health check status=%d", response.StatusCode)
	}
}
