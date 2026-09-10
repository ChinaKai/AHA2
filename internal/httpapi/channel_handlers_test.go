package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	instanceID, projectID, workspaceID := instance["id"].(string), instance["host_project_id"].(string), instance["host_workspace_id"].(string)
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
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/channel-runtime/v1/commands/command/attachment"},
		{http.MethodGet, "/api/channel-runtime/v1/deliveries/delivery/attachment"},
		{http.MethodPost, "/api/channel-runtime/v1/deliveries/delivery/media"},
	} {
		response = channelJSON(t, client, target.method, server.URL+target.path, map[string]any{}, map[string]string{"Authorization": "Bearer " + rawCapability})
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("media endpoint accepted health-only capability: %d", response.StatusCode)
		}
		response.Body.Close()
	}
	credentialInstance := credentials["instance"].(map[string]any)
	revision := int(credentialInstance["revision"].(float64))
	response = channelJSON(t, client, http.MethodPost, server.URL+"/api/v1/channel-instances/"+instanceID+"/archive", map[string]any{}, map[string]string{"X-CSRF-Token": csrf, "If-Match": fmt.Sprintf(`"%d"`, revision)})
	var archivedResponse map[string]any
	decodeResponse(t, response, &archivedResponse)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("archive=%#v", archivedResponse)
	}
	archived := archivedResponse["instance"].(map[string]any)
	if archived["retired"] != true || archived["credential_configured"] != false {
		t.Fatalf("archived instance=%#v", archived)
	}
	archivedRevision := int(archived["revision"].(float64))
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/projects", nil, "")
	var projects map[string]any
	decodeResponse(t, response, &projects)
	foundArchivedProject := false
	for _, raw := range projects["projects"].([]any) {
		project := raw.(map[string]any)
		if project["id"] == projectID {
			foundArchivedProject = project["channel_retired"] == true && project["read_only_reason"] == "channel_retired" && project["channel_instance_id"] == instanceID
		}
	}
	if !foundArchivedProject {
		t.Fatalf("archived channel project was not decorated: %#v", projects)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/workspaces?project_id="+projectID, nil, "")
	var workspaces map[string]any
	decodeResponse(t, response, &workspaces)
	archivedWorkspace := workspaces["workspaces"].([]any)[0].(map[string]any)
	if archivedWorkspace["id"] != workspaceID || archivedWorkspace["read_only"] != true || archivedWorkspace["read_only_reason"] != "channel_retired" {
		t.Fatalf("archived channel workspace was not decorated: %#v", archivedWorkspace)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/projects/"+projectID, map[string]any{"name": "changed"}, csrf)
	var readOnlyResponse map[string]any
	decodeResponse(t, response, &readOnlyResponse)
	if response.StatusCode != http.StatusForbidden || readOnlyResponse["error"] != "channel_archive_read_only" {
		t.Fatalf("archived project accepted mutation: %#v", readOnlyResponse)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/workspaces/"+workspaceID, map[string]any{"name": "changed"}, csrf)
	decodeResponse(t, response, &readOnlyResponse)
	if response.StatusCode != http.StatusForbidden || readOnlyResponse["error"] != "channel_archive_read_only" {
		t.Fatalf("archived workspace accepted mutation: %#v", readOnlyResponse)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/projects/"+projectID, nil, csrf)
	decodeResponse(t, response, &deletion)
	if response.StatusCode != http.StatusConflict || deletion["error"] != "managed_channel_resource" {
		t.Fatalf("archived project bypassed channel purge: %#v", deletion)
	}
	response = channelJSON(t, client, http.MethodGet, server.URL+"/api/v1/channel-instances/"+instanceID+"/purge-preview", nil, map[string]string{})
	var previewResponse map[string]any
	decodeResponse(t, response, &previewResponse)
	if response.StatusCode != http.StatusOK || previewResponse["preview"].(map[string]any)["name"] != "Team" {
		t.Fatalf("purge preview=%#v", previewResponse)
	}
	response = channelJSON(t, client, http.MethodPost, server.URL+"/api/v1/channel-instances/"+instanceID+"/purge", map[string]any{"confirmation_name": "wrong"}, map[string]string{"X-CSRF-Token": csrf, "If-Match": fmt.Sprintf(`"%d"`, archivedRevision)})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("purge accepted wrong confirmation: %d", response.StatusCode)
	}
	response.Body.Close()
	response = channelJSON(t, client, http.MethodPost, server.URL+"/api/v1/channel-instances/"+instanceID+"/purge", map[string]any{"confirmation_name": "Team"}, map[string]string{"X-CSRF-Token": csrf, "If-Match": fmt.Sprintf(`"%d"`, archivedRevision)})
	if response.StatusCode != http.StatusOK {
		var body map[string]any
		decodeResponse(t, response, &body)
		t.Fatalf("purge=%#v", body)
	}
	response.Body.Close()
	if _, err := database.Project(ctx, projectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("purge retained project: %v", err)
	}
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
