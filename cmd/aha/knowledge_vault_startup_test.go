package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestKnowledgeVaultStartupAllowsProjectsAndTasks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		projectState string
	}{
		{"fresh", domain.KnowledgeStateReady},
		{"vault path blocked", domain.KnowledgeStateUninitialized},
		{"index path blocked", domain.KnowledgeStateError},
		{"legacy upgrade", domain.KnowledgeStateUninitialized},
		{"configured vault missing", domain.KnowledgeStateUninitialized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			config := knowledgevault.DefaultConfig(dataDir)
			legacySkill := filepath.Join(dataDir, "knowledge", "skills", "retired", "SKILL.md")
			switch tc.name {
			case "vault path blocked":
				// A file blocks initialization even when the test runs as root.
				if err := os.WriteFile(config.VaultPath, []byte("obstruction"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "index path blocked":
				if err := os.Mkdir(config.IndexPath, 0o700); err != nil {
					t.Fatal(err)
				}
			case "legacy upgrade":
				if err := os.MkdirAll(filepath.Dir(legacySkill), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(legacySkill, []byte("# Legacy skill"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "configured vault missing":
				ctx := context.Background()
				database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
				if err != nil {
					t.Fatal(err)
				}
				switchErr := database.SetKnowledgeVaultSwitch(ctx, true)
				if closeErr := database.Close(); switchErr != nil || closeErr != nil {
					t.Fatalf("seed the configured switch: %v (close: %v)", switchErr, closeErr)
				}
			}

			baseURL, stop := startVaultTestControlPlane(t, dataDir)
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
			var csrf string
			request := func(method, path string, body any, wantStatus int, result any) {
				t.Helper()
				payload, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequest(method, baseURL+"/api/v1/"+path, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", csrf)
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				if res.StatusCode != wantStatus {
					var failure struct{ Error, Message string }
					_ = json.NewDecoder(res.Body).Decode(&failure)
					t.Fatalf("%s %s = %d, want %d (%s %s)", method, path, res.StatusCode, wantStatus, failure.Error, failure.Message)
				}
				if result != nil {
					if err := json.NewDecoder(res.Body).Decode(result); err != nil {
						t.Fatal(err)
					}
				}
			}
			var registration struct {
				CSRF string `json:"csrf_token"`
			}
			request(http.MethodPost, "auth/register", map[string]string{
				"setup_token": "test-setup", "username": "owner", "password": "test-owner-password",
			}, http.StatusCreated, &registration)
			csrf = registration.CSRF
			var status struct {
				Settings struct {
					knowledgevault.Status
					Configured bool
					Readiness  knowledgevault.Readiness
				}
			}
			request(http.MethodGet, "settings/knowledge-vault", nil, http.StatusOK, &status)
			if configured := tc.name == "configured vault missing"; status.Settings.Configured != configured {
				t.Fatalf("startup changed the owner switch configuration: got %t, want %t", status.Settings.Configured, configured)
			}
			switch tc.name {
			case "fresh":
				if !status.Settings.Enabled || !status.Settings.VaultInitialized || !status.Settings.IndexReady {
					t.Fatalf("initial vault = %+v", status.Settings.Status)
				}
				for _, relative := range []string{".aha-vault.json", "global/index.md"} {
					if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(relative))); err != nil {
						t.Fatal(err)
					}
				}
				paths := []string{"skills"}
				for _, sample := range knowledgevault.SampleDocuments(time.Now()) {
					paths = append(paths, sample.Path)
				}
				for _, relative := range paths {
					if _, err := os.Lstat(filepath.Join(config.VaultPath, filepath.FromSlash(relative))); !os.IsNotExist(err) {
						t.Fatalf("unexpected vault content %s: %v", relative, err)
					}
				}
				assertVaultHasOnlyScaffolding(t, config)
			case "vault path blocked", "index path blocked":
				if status.Settings.Readiness.Status != knowledgevault.ReadinessNeedsRebuild || status.Settings.IndexReady {
					t.Fatalf("broken vault status = %+v", status.Settings)
				}
			case "legacy upgrade":
				if status.Settings.Enabled || status.Settings.Readiness.Status != knowledgevault.ReadinessNeedsMigration {
					t.Fatalf("legacy vault status = %+v", status.Settings)
				}
				request(http.MethodPut, "settings/knowledge-vault", map[string]bool{"enabled": true}, http.StatusConflict, nil)
				request(http.MethodPost, "settings/knowledge-vault/init", map[string]bool{"samples": false}, http.StatusConflict, nil)
				if _, err := os.Stat(config.VaultPath); !os.IsNotExist(err) {
					t.Fatalf("legacy startup created a vault: %v", err)
				}
				if data, err := os.ReadFile(legacySkill); err != nil || string(data) != "# Legacy skill" {
					t.Fatalf("legacy skill changed: %v", err)
				}
			case "configured vault missing":
				if !status.Settings.Enabled || status.Settings.VaultInitialized || status.Settings.Source != knowledgevault.SourceDatabase {
					t.Fatalf("configured vault status = %+v", status.Settings.Status)
				}
				if _, err := os.Stat(config.VaultPath); !os.IsNotExist(err) {
					t.Fatalf("startup recreated a configured vault: %v", err)
				}
			}

			var project struct{ Project domain.Project }
			request(http.MethodPost, "projects", map[string]string{"name": "Test project"}, http.StatusCreated, &project)
			if project.Project.KnowledgeState != tc.projectState {
				t.Fatalf("project knowledge state = %q, want %q", project.Project.KnowledgeState, tc.projectState)
			}
			var workspace struct{ Workspace domain.Workspace }
			request(http.MethodPost, "workspaces", map[string]string{
				"project_id": project.Project.ID, "name": "Local", "root_path": t.TempDir(),
			}, http.StatusCreated, &workspace)
			var skillIDs []string
			if tc.projectState != domain.KnowledgeStateReady {
				skillIDs = []string{"vault-skill:global:unavailable"}
			}
			var task struct {
				Task    domain.Task
				Turn    domain.Turn
				Started bool
			}
			request(http.MethodPost, "tasks", map[string]any{
				"project_id": project.Project.ID, "workspace_id": workspace.Workspace.ID,
				"title": "Test task", "request": "Test startup", "model_source": "claude_native", "skill_ids": skillIDs,
			}, http.StatusCreated, &task)
			if task.Task.Status != domain.TaskActive || !task.Started || task.Turn.ID == "" || len(task.Task.SkillIDs) != 0 {
				t.Fatalf("task did not start with the skills it could load: %+v", task)
			}

			if tc.name == "index path blocked" {
				stop()
				if err := os.Remove(config.IndexPath); err != nil {
					t.Fatal(err)
				}
				baseURL, _ = startVaultTestControlPlane(t, dataDir)
				request(http.MethodGet, "settings/knowledge-vault", nil, http.StatusOK, &status)
				if !status.Settings.IndexReady || status.Settings.ProjectBindings[project.Project.ID] == "" {
					t.Fatalf("vault did not recover the existing project's space: %+v", status.Settings.Status)
				}
				var projects struct{ Projects []domain.Project }
				request(http.MethodGet, "projects", nil, http.StatusOK, &projects)
				if len(projects.Projects) != 1 || projects.Projects[0].ID != project.Project.ID || projects.Projects[0].KnowledgeState != domain.KnowledgeStateReady {
					t.Fatalf("project readiness was not recovered: %+v", projects.Projects)
				}
			}
		})
	}
}

func assertVaultHasOnlyScaffolding(t *testing.T, config knowledgevault.Config) {
	t.Helper()
	ctx := context.Background()
	reader, err := knowledgevault.OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries, err := reader.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsIndex && entry.Path != knowledgevault.GuideRelativePath {
			t.Fatalf("unexpected initial knowledge entry: %s", entry.Path)
		}
	}
}

func startVaultTestControlPlane(t *testing.T, dataDir string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = runControlPlane(ctx, serveOptions{
			listen: address, dataDir: dataDir, logLevel: "warn", setupToken: "test-setup",
			knowledgeVaultRuntimeRead: true, knowledgeVaultRuntimeWrite: true,
			claudeBinary: filepath.Join(dataDir, "missing-test-claude"),
		}, func() { close(ready) })
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("control plane: %v", runErr)
			}
		case <-time.After(30 * time.Second):
			t.Error("control plane did not stop")
		}
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-done:
		t.Fatalf("control plane stopped before ready: %v", runErr)
	case <-time.After(90 * time.Second):
		t.Fatal("control plane did not start")
	}
	return "http://" + address, stop
}
