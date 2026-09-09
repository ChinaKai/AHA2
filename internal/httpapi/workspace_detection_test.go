package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func TestWorkspaceSSHHostKeyRequiresExplicitFingerprintConfirmation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-workspace-host-key", Name: "SSH", CreatedAt: now, UpdatedAt: now}
	workspaceItem := domain.Workspace{
		ID: "workspace-host-key", ProjectID: project.ID, Name: "Remote", Locality: "remote", Transport: "ssh",
		RootPath: `C:\workspace`, SSHHost: "192.0.2.50", SSHUser: "owner", SSHPort: 22,
		Health: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspaceItem); err != nil {
		t.Fatal(err)
	}
	info := hardware.SSHHostKeyInfo{Endpoint: "192.0.2.50:22", Algorithm: "ssh-ed25519", Fingerprint: "SHA256:workspace-test"}
	trusted := ""
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}),
		DetectWorkspace: func(_ context.Context, item domain.Workspace) (domain.Workspace, error) {
			return item, fmt.Errorf("ssh: handshake failed: knownhosts: key is unknown")
		},
		ProbeSSHHostKey: func(context.Context, string) (hardware.SSHHostKeyInfo, error) { return info, nil },
		TrustSSHHostKey: func(_ context.Context, _ string, fingerprint string) (hardware.SSHHostKeyInfo, error) {
			if fingerprint != info.Fingerprint {
				return hardware.SSHHostKeyInfo{}, fmt.Errorf("fingerprint changed")
			}
			trusted = fingerprint
			return info, nil
		},
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspaceItem.ID+"/detect", nil, csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusConflict || payload["error"] != "ssh_host_key_unknown" {
		t.Fatalf("unknown host key status=%d payload=%#v", response.StatusCode, payload)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/workspaces/"+workspaceItem.ID+"/host-key", nil, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["host_key"].(map[string]any)["fingerprint"] != info.Fingerprint {
		t.Fatalf("host key probe status=%d payload=%#v", response.StatusCode, payload)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspaceItem.ID+"/host-key/trust", map[string]any{"fingerprint": "SHA256:wrong"}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict || trusted != "" {
		t.Fatalf("wrong fingerprint status=%d trusted=%q", response.StatusCode, trusted)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspaceItem.ID+"/host-key/trust", map[string]any{"fingerprint": info.Fingerprint}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || trusted != info.Fingerprint {
		t.Fatalf("trusted fingerprint status=%d trusted=%q", response.StatusCode, trusted)
	}
}

func TestWorkspaceDetectionFailurePersistsFreshStructuredResult(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-detect-error", Name: "Detect", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspaceItem := domain.Workspace{
		ID: "workspace-detect-error", ProjectID: project.ID, Name: "Missing", Locality: "local", Transport: "native",
		RootPath: filepath.Join(t.TempDir(), "missing"), Platform: "stale-platform", Health: "ready",
		Capabilities: map[string]any{"codex": map[string]any{"status": "ready"}}, Repository: map[string]any{"is_git": true},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspaceItem); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), DetectWorkspace: workspacepkg.Detect,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspaceItem.ID+"/detect", nil, csrf)
	defer response.Body.Close()
	var body struct {
		Error     string           `json:"error"`
		Workspace domain.Workspace `json:"workspace"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || body.Error != "workspace_unavailable" {
		t.Fatalf("status=%d error=%q", response.StatusCode, body.Error)
	}
	if body.Workspace.Platform != "" || body.Workspace.Health != "error" || len(body.Workspace.Repository) != 0 {
		t.Fatalf("response retained stale detection: %#v", body.Workspace)
	}
	stored, err := database.Workspace(ctx, workspaceItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Platform != "" || stored.Health != "error" || len(stored.Repository) != 0 {
		t.Fatalf("store retained stale detection: %#v", stored)
	}
	probe, ok := stored.Capabilities["workspace"].(map[string]any)
	if !ok || probe["status"] != "unavailable" || probe["error"] == "" {
		t.Fatalf("structured workspace error missing: %#v", stored.Capabilities)
	}
}
