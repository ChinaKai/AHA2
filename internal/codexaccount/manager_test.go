package codexaccount

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type fakeAccountStore struct {
	items map[string]domain.CodexAccount
	proxy domain.ProxySettings
}

func (f *fakeAccountStore) UpsertCodexAccount(_ context.Context, item domain.CodexAccount) error {
	f.items[item.ID] = item
	return nil
}
func (f *fakeAccountStore) CodexAccount(_ context.Context, id string) (domain.CodexAccount, error) {
	item, ok := f.items[id]
	if !ok {
		return domain.CodexAccount{}, sql.ErrNoRows
	}
	return item, nil
}
func (f *fakeAccountStore) ListCodexAccounts(context.Context) ([]domain.CodexAccount, error) {
	result := make([]domain.CodexAccount, 0, len(f.items))
	for _, item := range f.items {
		result = append(result, item)
	}
	return result, nil
}
func (f *fakeAccountStore) DeleteCodexAccount(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}
func (f *fakeAccountStore) CodexAccountInUse(context.Context, string) (bool, error) {
	return false, nil
}
func (f *fakeAccountStore) ProxySettings(context.Context) (domain.ProxySettings, error) {
	if f.proxy.HTTPProxy != "" || f.proxy.HTTPSProxy != "" {
		return f.proxy, nil
	}
	return domain.ProxySettings{
		HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7897",
		NoProxy: "localhost,127.0.0.1,::1",
	}, nil
}

func TestOAuthTokenExchangeUsesConfiguredProxy(t *testing.T) {
	proxyHit := false
	claims := map[string]any{
		"email":                       "proxy@example.com",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "account-proxy"},
	}
	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyHit = true
		if request.URL.Host != "auth.openai.invalid" || request.Header.Get("User-Agent") != "codex_cli_rs" {
			t.Errorf("unexpected proxied request: %s %s", request.URL.String(), request.Header.Get("User-Agent"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"access_token": "access-proxy", "refresh_token": "refresh-proxy", "id_token": unsignedJWT(t, claims),
		})
	}))
	defer proxyServer.Close()
	store := &fakeAccountStore{
		items: map[string]domain.CodexAccount{},
		proxy: domain.ProxySettings{HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL},
	}
	manager := New(context.Background(), store, &fakeSecretStore{items: map[string]string{}}, "", "")
	manager.tokenURL = "http://auth.openai.invalid/oauth/token"
	login, err := manager.StartOAuthLogin(true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(login.AuthURL)
	callback := oauthRedirectURI + "?code=proxy-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
	completed, err := manager.SubmitCallback(context.Background(), login.ID, callback, "Proxy")
	if err != nil {
		t.Fatal(err)
	}
	if !proxyHit || completed.Account == nil || !completed.Account.ProxyEnabled {
		t.Fatalf("OAuth proxy was not persisted: hit=%v login=%+v", proxyHit, completed)
	}
}

type fakeSecretStore struct {
	items map[string]string
}

func (f *fakeSecretStore) PutMany(values map[string]string) error {
	for key, value := range values {
		f.items[key] = value
	}
	return nil
}
func (f *fakeSecretStore) DeleteMany(keys []string) error {
	for _, key := range keys {
		delete(f.items, key)
	}
	return nil
}
func (f *fakeSecretStore) Get(key string) (string, bool) {
	value, ok := f.items[key]
	return value, ok
}

func TestOAuthLoginUsesPKCEAndManualCallback(t *testing.T) {
	store := &fakeAccountStore{items: map[string]domain.CodexAccount{}}
	secrets := &fakeSecretStore{items: map[string]string{}}
	manager := New(context.Background(), store, secrets, "", "")
	login, err := manager.StartOAuthLogin()
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := url.Parse(login.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	query := authURL.Query()
	for key, expected := range map[string]string{
		"response_type": "code", "client_id": oauthClientID, "redirect_uri": oauthRedirectURI,
		"code_challenge_method": "S256", "originator": "codex_cli_rs",
		"id_token_add_organizations": "true", "codex_cli_simplified_flow": "true",
	} {
		if query.Get(key) != expected {
			t.Fatalf("%s=%q, want %q", key, query.Get(key), expected)
		}
	}
	if query.Get("state") == "" || query.Get("code_challenge") == "" || strings.Contains(login.AuthURL, "code_verifier") {
		t.Fatalf("invalid PKCE authorization URL: %s", login.AuthURL)
	}

	if _, err := manager.SubmitCallback(context.Background(), login.ID,
		oauthRedirectURI+"?code=test-code&state=wrong", ""); err == nil {
		t.Fatal("expected state mismatch")
	}
	current, _ := manager.Login(login.ID)
	if current.Status != "awaiting_callback" {
		t.Fatalf("state mismatch consumed login: %s", current.Status)
	}

	claims := map[string]any{
		"email": "owner@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-123", "chatgpt_plan_type": "plus",
		},
	}
	idToken := unsignedJWT(t, claims)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("code") != "test-code" || request.Form.Get("redirect_uri") != oauthRedirectURI {
			t.Errorf("unexpected token request: %v", request.Form)
		}
		challenge := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(challenge[:]) != query.Get("code_challenge") {
			t.Error("code_verifier does not match authorization challenge")
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"access_token": "access-secret", "refresh_token": "refresh-secret", "id_token": idToken,
		})
	}))
	defer tokenServer.Close()
	manager.tokenURL = tokenServer.URL

	callback := oauthRedirectURI + "?code=test-code&state=" + url.QueryEscape(query.Get("state"))
	completed, err := manager.SubmitCallback(context.Background(), login.ID, callback, "工作账号")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "succeeded" || completed.Account == nil || completed.Account.Email != "owner@example.com" {
		t.Fatalf("unexpected completed login: %+v", completed)
	}
	encoded, _ := json.Marshal(completed)
	if strings.Contains(string(encoded), "access-secret") || strings.Contains(string(encoded), "refresh-secret") {
		t.Fatalf("login response leaked credentials: %s", encoded)
	}
	credential := secrets.items[completed.Account.CredentialRef]
	if !strings.Contains(credential, "refresh-secret") || !strings.Contains(credential, `"auth_mode":"chatgpt"`) {
		t.Fatalf("credential was not stored as Codex auth.json: %s", credential)
	}
	if _, err := manager.SubmitCallback(context.Background(), login.ID, callback, ""); err == nil {
		t.Fatal("expected completed login to reject replay")
	}
}

func TestOAuthTokenErrorDoesNotExposeResponseBody(t *testing.T) {
	store := &fakeAccountStore{items: map[string]domain.CodexAccount{}}
	secrets := &fakeSecretStore{items: map[string]string{}}
	manager := New(context.Background(), store, secrets, "", "")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"refresh_token":"must-not-leak"}`))
	}))
	defer server.Close()
	manager.tokenURL = server.URL
	login, _ := manager.StartOAuthLogin()
	parsed, _ := url.Parse(login.AuthURL)
	callback := oauthRedirectURI + "?code=bad&state=" + url.QueryEscape(parsed.Query().Get("state"))
	_, err := manager.SubmitCallback(context.Background(), login.ID, callback, "")
	if err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("unsafe token error: %v", err)
	}
}

func TestOAuthTokenErrorReturnsOnlySafeFields(t *testing.T) {
	message := oauthErrorMessage(strings.NewReader(`{"error":{"code":"token_expired","type":"invalid_request_error","message":"Please sign in again"},"refresh_token":"must-not-leak"}`))
	if message != "token_expired: invalid_request_error: Please sign in again" {
		t.Fatalf("unexpected OAuth error: %q", message)
	}
	if strings.Contains(message, "must-not-leak") {
		t.Fatal("OAuth error leaked credential fields")
	}
}

func TestRefreshAccountFetchesQuotaModelsAndRefreshesExpiredToken(t *testing.T) {
	claims := map[string]any{
		"email": "updated@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-updated", "chatgpt_plan_type": "pro",
		},
	}
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/usage":
			if request.Header.Get("Authorization") == "Bearer expired-access" {
				writer.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": "token_expired"}})
				return
			}
			if request.Header.Get("Authorization") != "Bearer fresh-access" ||
				request.Header.Get("ChatGPT-Account-Id") != "account-updated" {
				t.Errorf("unexpected refreshed request headers")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"plan_type": "pro",
				"rate_limit": map[string]any{
					"allowed": true,
					"primary_window": map[string]any{
						"used_percent": 8, "limit_window_seconds": 604800, "reset_at": 1789099897,
					},
				},
				"additional_rate_limits": []map[string]any{{
					"limit_name": "Spark", "metered_feature": "spark",
					"rate_limit": map[string]any{
						"allowed":        true,
						"primary_window": map[string]any{"used_percent": 20, "limit_window_seconds": 18000},
					},
				}},
				"credits": map[string]any{"has_credits": true, "balance": "12.5"},
			})
		case "/models":
			if request.URL.Query().Get("client_version") == "" {
				t.Error("client_version was not sent")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"models": []map[string]any{
				{"slug": "hidden", "display_name": "Hidden", "visibility": "hide"},
				{
					"slug": "gpt-5.6-sol", "display_name": "GPT-5.6-Sol", "visibility": "list",
					"context_window": 272000, "max_context_window": 872000,
					"default_reasoning_level":    "low",
					"supported_reasoning_levels": []map[string]string{{"effort": "low"}, {"effort": "high"}},
				},
			}})
		case "/oauth/token":
			refreshes++
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["grant_type"] != "refresh_token" || payload["refresh_token"] != "refresh-secret" {
				t.Errorf("unexpected refresh payload: %v", payload)
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"access_token": "fresh-access", "refresh_token": "fresh-refresh",
				"id_token": unsignedJWT(t, claims),
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	account := domain.CodexAccount{
		ID: "codex-account-refresh", Label: "Work", AccountID: "account-old", PlanType: "plus",
		Status: "ready", CredentialRef: "codex-account/codex-account-refresh/auth",
		CredentialConfigured: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	store := &fakeAccountStore{items: map[string]domain.CodexAccount{account.ID: account}}
	secrets := &fakeSecretStore{items: map[string]string{
		account.CredentialRef: `{"auth_mode":"chatgpt","tokens":{"access_token":"expired-access","refresh_token":"refresh-secret","account_id":"account-old"}}`,
	}}
	manager := New(context.Background(), store, secrets, "", "")
	manager.usageURL = server.URL + "/usage"
	manager.modelsURL = server.URL + "/models"
	manager.tokenURL = server.URL + "/oauth/token"

	result, err := manager.RefreshAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected refresh result: refreshes=%d result=%+v", refreshes, result)
	}
	got := result.Account
	if got.PlanType != "pro" || got.AccountID != "account-updated" || got.Email != "updated@example.com" {
		t.Fatalf("account metadata was not refreshed: %+v", got)
	}
	if got.Usage == nil || len(got.Usage.RateLimits) != 2 ||
		got.Usage.RateLimits[0].PrimaryWindow.UsedPercent != 8 || got.Usage.Credits.Balance != "12.5" {
		t.Fatalf("quota was not normalized: %+v", got.Usage)
	}
	if len(got.AvailableModels) != 1 || got.AvailableModels[0].WireModel != "gpt-5.6-sol" ||
		got.AvailableModels[0].MaxContextWindow != 872000 {
		t.Fatalf("model catalog was not normalized: %+v", got.AvailableModels)
	}
	if !strings.Contains(secrets.items[account.CredentialRef], "fresh-refresh") ||
		strings.Contains(secrets.items[account.CredentialRef], "expired-access") {
		t.Fatal("refreshed credentials were not persisted")
	}
}

func TestPrepareLocalProfileUsesIsolatedCodexHome(t *testing.T) {
	store := &fakeAccountStore{items: map[string]domain.CodexAccount{}}
	secrets := &fakeSecretStore{items: map[string]string{}}
	manager := New(context.Background(), store, secrets, "", "")
	auth := `{"auth_mode":"chatgpt","tokens":{"access_token":"access","refresh_token":"refresh","account_id":"account-1"}}`
	account, err := manager.Import(context.Background(), []byte(auth), "Local")
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	profile, err := manager.PrepareProfile(context.Background(), account.ID, domain.Workspace{Transport: "native"}, workDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workDir, ".aha2-context", "runtime", "codex-auth", "session-1")
	if profile != want {
		t.Fatalf("profile=%q, want %q", profile, want)
	}
	stored, err := os.ReadFile(filepath.Join(profile, "auth.json"))
	if err != nil || !strings.Contains(string(stored), "refresh") {
		t.Fatalf("isolated auth.json missing: %s %v", stored, err)
	}
	config, err := os.ReadFile(filepath.Join(profile, "config.toml"))
	if err != nil || !strings.Contains(string(config), `cli_auth_credentials_store = "file"`) {
		t.Fatalf("isolated config.toml missing: %s %v", config, err)
	}
}

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
