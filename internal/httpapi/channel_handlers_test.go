package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestChannelOwnerAndRuntimeAPIBoundaries(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(dataDir, "plugins", "channels", "feishu")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(pluginDir, "plugin.exe")
	content := []byte("plugin")
	if err := os.WriteFile(executable, content, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	manifest := map[string]any{"manifest_version": 1, "plugin_id": "feishu", "provider_key": "feishu", "display_name": "飞书", "package_version": "1", "protocol_versions": []string{"channel-runtime/v1"}, "endpoints": []string{"assistant_dm", "group_digital_human"}, "executable": "plugin.exe", "sha256": hex.EncodeToString(digest[:])}
	rawManifest, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), rawManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	channels := channel.New(channel.Config{Store: database, App: appService, Secrets: secretStore, DataDir: dataDir})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService, Secrets: secretStore, Channels: channels}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{"setup_token": "setup-test", "username": "owner", "password": "correct-horse-battery"}, "")
	var registered map[string]any
	decodeResponse(t, response, &registered)
	csrf := registered["csrf_token"].(string)
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/channel-providers", nil, "")
	var providers map[string]any
	decodeResponse(t, response, &providers)
	if response.StatusCode != http.StatusOK || len(providers["providers"].([]any)) != 1 {
		t.Fatalf("providers=%#v", providers)
	}
	response = channelJSON(t, client, http.MethodPost, server.URL+"/api/v1/channel-instances", map[string]any{"plugin_id": "feishu", "name": "Team"}, map[string]string{"X-CSRF-Token": csrf, "Idempotency-Key": "create-team"})
	var created map[string]any
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("created=%#v", created)
	}
	instance := created["instance"].(map[string]any)
	instanceID, projectID := instance["id"].(string), instance["host_project_id"].(string)
	if _, ok := instance["credential_ref"]; ok {
		t.Fatalf("credential ref leaked: %#v", instance)
	}
	response = channelJSON(t, client, http.MethodPut, server.URL+"/api/v1/channel-instances/"+instanceID+"/credentials", map[string]any{"app_id": "cli_test", "app_secret": "secret-value"}, map[string]string{"X-CSRF-Token": csrf, "If-Match": `"1"`})
	var credentials map[string]any
	decodeResponse(t, response, &credentials)
	encoded, _ := json.Marshal(credentials)
	if response.StatusCode != http.StatusOK || strings.Contains(string(encoded), "secret-value") {
		t.Fatalf("credentials response=%s", encoded)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/projects/"+projectID, nil, csrf)
	var deletion map[string]any
	decodeResponse(t, response, &deletion)
	if response.StatusCode != http.StatusConflict || deletion["error"] != "managed_channel_resource" {
		t.Fatalf("deletion=%#v", deletion)
	}
	rawCapability, _, err := channels.IssueCapability(ctx, instanceID, []string{"channel.health.write"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response = channelJSON(t, client, http.MethodPost, server.URL+"/api/channel-runtime/v1/handshake", map[string]any{"schema_version": 1, "instance_id": instanceID, "plugin_id": "feishu", "supported_protocols": []string{"channel-runtime/v1"}, "boot_id": "boot"}, map[string]string{"Authorization": "Bearer " + rawCapability})
	if response.StatusCode != http.StatusOK {
		var body map[string]any
		decodeResponse(t, response, &body)
		t.Fatalf("handshake=%#v", body)
	}
	response.Body.Close()
	response = channelJSON(t, client, http.MethodPut, server.URL+"/api/channel-runtime/v1/instances/not-this-instance/health", map[string]any{"schema_version": 1, "status": "ready"}, map[string]string{"Authorization": "Bearer " + rawCapability})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross instance status=%d", response.StatusCode)
	}
	response.Body.Close()
}

func channelJSON(t *testing.T, client *http.Client, method, url string, payload any, headers map[string]string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
