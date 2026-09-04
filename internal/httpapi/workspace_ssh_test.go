package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestWorkspaceSSHCredentialsStayInSecretStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretsStore := &fakeSecretStore{}
	passwordSeen := make(chan string, 1)
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}),
		Secrets: secretsStore,
		DetectWorkspace: func(_ context.Context, item domain.Workspace) (domain.Workspace, error) {
			passwordSeen <- item.SSHPassword
			item.Health = "ready"
			return item, nil
		},
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/projects", map[string]any{
		"name": "SSH project",
	}, csrf)
	var projectResponse map[string]any
	decodeResponse(t, response, &projectResponse)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create project: %d %#v", response.StatusCode, projectResponse)
	}
	projectID := projectResponse["project"].(map[string]any)["id"].(string)

	workspaceID := createSSHWorkspace(t, client, server.URL, csrf, projectID, "secret-password")
	credentialRef := workspaceSSHCredentialRef(workspaceID)
	if secretsStore.values[credentialRef] != "secret-password" {
		t.Fatalf("SSH password was not stored out of band: %#v", secretsStore.values)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/workspaces/"+workspaceID, map[string]any{
		"name": "SSH edited", "locality": "remote", "transport": "ssh", "root_path": "/workspace",
		"ssh_host": "example.test", "ssh_user": "root", "ssh_port": 22, "ssh_auth": "password",
		"ssh_password": "",
	}, csrf)
	var updateResponse map[string]any
	decodeResponse(t, response, &updateResponse)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preserve SSH password: %d %#v", response.StatusCode, updateResponse)
	}
	assertWorkspaceCredentialResponse(t, updateResponse["workspace"].(map[string]any), true)
	if secretsStore.values[credentialRef] != "secret-password" {
		t.Fatalf("blank edit replaced SSH password: %#v", secretsStore.values)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspaceID+"/detect", nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detect SSH workspace: %d", response.StatusCode)
	}
	if password := <-passwordSeen; password != "secret-password" {
		t.Fatalf("detect did not receive resolved SSH password: %q", password)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/workspaces/"+workspaceID, map[string]any{
		"name": "SSH key", "locality": "remote", "transport": "ssh", "root_path": "/workspace",
		"ssh_host": "example.test", "ssh_user": "root", "ssh_port": 22, "ssh_auth": "key",
		"clear_ssh_password": true,
	}, csrf)
	decodeResponse(t, response, &updateResponse)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("clear SSH password: %d %#v", response.StatusCode, updateResponse)
	}
	assertWorkspaceCredentialResponse(t, updateResponse["workspace"].(map[string]any), false)
	if _, exists := secretsStore.values[credentialRef]; exists {
		t.Fatalf("cleared SSH password remains in Secret Store: %#v", secretsStore.values)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/workspaces/"+workspaceID, map[string]any{
		"name": "SSH invalid", "locality": "remote", "transport": "ssh", "root_path": "/workspace",
		"ssh_host": "example.test", "ssh_user": "root", "ssh_port": 22, "ssh_auth": "password",
	}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("password mode without a password returned %d", response.StatusCode)
	}
	if len(secretsStore.values) != 0 {
		t.Fatalf("invalid update changed Secret Store: %#v", secretsStore.values)
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/workspaces/"+workspaceID, nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete workspace: %d", response.StatusCode)
	}

	secondWorkspaceID := createSSHWorkspace(t, client, server.URL, csrf, projectID, "project-secret")
	secondCredentialRef := workspaceSSHCredentialRef(secondWorkspaceID)
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/projects/"+projectID, nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete project: %d", response.StatusCode)
	}
	if _, exists := secretsStore.values[secondCredentialRef]; exists {
		t.Fatalf("project deletion retained Workspace SSH password: %#v", secretsStore.values)
	}
}

func createSSHWorkspace(
	t *testing.T,
	client *http.Client,
	baseURL string,
	csrf string,
	projectID string,
	password string,
) string {
	t.Helper()
	response := requestJSON(t, client, http.MethodPost, baseURL+"/api/v1/workspaces", map[string]any{
		"project_id": projectID, "name": "SSH", "locality": "remote", "transport": "ssh",
		"root_path": "/workspace", "ssh_host": "example.test", "ssh_user": "root",
		"ssh_port": 22, "ssh_auth": "password", "ssh_password": password,
		"clear_ssh_password": false,
	}, csrf)
	var workspaceResponse map[string]any
	decodeResponse(t, response, &workspaceResponse)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create SSH workspace: %d %#v", response.StatusCode, workspaceResponse)
	}
	workspace := workspaceResponse["workspace"].(map[string]any)
	assertWorkspaceCredentialResponse(t, workspace, true)
	return workspace["id"].(string)
}

func assertWorkspaceCredentialResponse(t *testing.T, workspace map[string]any, configured bool) {
	t.Helper()
	if workspace["ssh_password_configured"] != configured {
		t.Fatalf("unexpected SSH password state: %#v", workspace)
	}
	if _, exposed := workspace["ssh_password"]; exposed {
		t.Fatalf("SSH password leaked through API: %#v", workspace)
	}
	if _, exposed := workspace["ssh_credential_ref"]; exposed {
		t.Fatalf("SSH credential reference leaked through API: %#v", workspace)
	}
}
