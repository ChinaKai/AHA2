package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestKnowledgeVaultStartupAllowsProjectsAndTasks(t *testing.T) {
	claude := copyVaultTestClaude(t)
	for _, tc := range []struct {
		name         string
		projectState string
	}{
		{"fresh", domain.KnowledgeStateReady},
		{"vault path blocked", domain.KnowledgeStateUninitialized},
		{"index path blocked", domain.KnowledgeStateError},
		{"legacy upgrade", domain.KnowledgeStateUninitialized},
		{"configured vault missing", domain.KnowledgeStateUninitialized},
		{"ready project vault missing", domain.KnowledgeStateUninitialized},
		{"automatic vault missing", domain.KnowledgeStateUninitialized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			config := knowledgevault.DefaultConfig(dataDir)
			backupVault := filepath.Join(dataDir, "original-vault")
			const existingProjectID = "project-ready-before-startup"
			var existingSlug string
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
			case "configured vault missing", "ready project vault missing":
				ctx := context.Background()
				database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "configured vault missing" {
					err = database.SetKnowledgeVaultSwitch(ctx, true)
				} else {
					now := time.Now().UTC()
					err = database.CreateProject(ctx, domain.Project{
						ID: existingProjectID, Name: "Previously bound", ProjectType: "folder",
						KnowledgeState: domain.KnowledgeStateReady, CreatedAt: now, UpdatedAt: now,
					})
				}
				if closeErr := database.Close(); err != nil || closeErr != nil {
					t.Fatalf("seed the existing database: %v (close: %v)", err, closeErr)
				}
				original := config
				original.VaultPath = backupVault
				if _, err := knowledgevault.Bootstrap(original, knowledgevault.InitOptions{}); err != nil {
					t.Fatal(err)
				}
				if tc.name == "ready project vault missing" {
					space, err := knowledgevault.EnsureProjectSpace(original, existingProjectID, "Previously bound", time.Now())
					if err != nil {
						t.Fatal(err)
					}
					existingSlug = space.Slug
				}
			case "automatic vault missing":
				_, stop := startVaultTestControlPlane(t, dataDir, claude)
				stop()
				if err := os.Rename(config.VaultPath, backupVault); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(config.IndexPath); err != nil {
					t.Fatal(err)
				}
			}

			baseURL, stop := startVaultTestControlPlane(t, dataDir, claude)
			request := newVaultStartupTestClient(t, &baseURL)
			var status struct {
				Settings struct {
					knowledgevault.Status
					Configured             bool
					Readiness              knowledgevault.Readiness
					CanImportOverBootstrap bool `json:"can_import_over_bootstrap"`
				}
			}
			request(http.MethodGet, "settings/knowledge-vault", nil, http.StatusOK, &status)
			if configured := tc.name == "configured vault missing"; status.Settings.Configured != configured {
				t.Fatalf("startup changed the owner switch configuration: got %t, want %t", status.Settings.Configured, configured)
			}
			switch tc.name {
			case "fresh":
				if !status.Settings.Enabled || !status.Settings.VaultInitialized || !status.Settings.IndexReady || !status.Settings.CanImportOverBootstrap {
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
			case "configured vault missing", "ready project vault missing", "automatic vault missing":
				if !status.Settings.Enabled || status.Settings.VaultInitialized || status.Settings.Readiness.Status != knowledgevault.ReadinessNeedsRebuild {
					t.Fatalf("missing vault status = %+v", status.Settings)
				}
				if _, err := os.Stat(config.VaultPath); !os.IsNotExist(err) {
					t.Fatalf("startup recreated a configured vault: %v", err)
				}
				if tc.name == "ready project vault missing" {
					request(http.MethodPost, "projects/"+existingProjectID+"/knowledge-space", nil, http.StatusConflict, nil)
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
			if task.Task.Status != domain.TaskActive || !task.Started || task.Turn.ID == "" || !slices.Equal(task.Task.SkillIDs, skillIDs) {
				t.Fatalf("task creation did not retain the skill selection: %+v", task)
			}
			for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(100 * time.Millisecond) {
				var detail struct {
					Task  domain.Task
					Turns []domain.Turn
				}
				request(http.MethodGet, "tasks/"+task.Task.ID, nil, http.StatusOK, &detail)
				if len(detail.Turns) == 1 {
					turn := detail.Turns[0]
					if turn.Status == domain.TurnSucceeded {
						if turn.Result != "context reached test backend" || !slices.Equal(detail.Task.SkillIDs, skillIDs) {
							t.Fatalf("unexpected Turn result or persisted skill selection: %+v", detail)
						}
						break
					}
					if turn.Status == domain.TurnFailed {
						t.Fatalf("first Turn failed: %s", turn.Error)
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("first Turn did not finish: %+v", detail.Turns)
				}
			}

			if tc.name != "fresh" && tc.name != "legacy upgrade" {
				stop()
				var err error
				switch tc.name {
				case "index path blocked":
					err = os.Remove(config.IndexPath)
				case "vault path blocked":
					err = os.Remove(config.VaultPath)
				default:
					err = os.Rename(backupVault, config.VaultPath)
				}
				if err != nil {
					t.Fatal(err)
				}
				baseURL, _ = startVaultTestControlPlane(t, dataDir, claude)
				var retried struct{ Project domain.Project }
				request(http.MethodPost, "projects/"+project.Project.ID+"/knowledge-space", nil, http.StatusOK, &retried)
				if retried.Project.KnowledgeState != domain.KnowledgeStateReady {
					t.Fatalf("project did not recover after restart: %+v", retried.Project)
				}
				request(http.MethodGet, "settings/knowledge-vault", nil, http.StatusOK, &status)
				if !status.Settings.IndexReady || status.Settings.ProjectBindings[project.Project.ID] == "" {
					t.Fatalf("vault did not recover the existing project's space: %+v", status.Settings.Status)
				}
				if existingSlug != "" && status.Settings.ProjectBindings[existingProjectID] != existingSlug {
					t.Fatalf("previous project lost its binding: %+v", status.Settings.ProjectBindings)
				}
			}
		})
	}
}

func TestKnowledgeVaultStartupKeepsBootstrapImportableAfterAutoSync(t *testing.T) {
	for _, tc := range []struct {
		name       string
		blockIndex bool
	}{
		{name: "fresh"},
		{name: "pending index recovery", blockIndex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			config := knowledgevault.DefaultConfig(dataDir)
			if tc.blockIndex {
				if err := os.Mkdir(config.IndexPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			baseURL, stop := startVaultTestControlPlane(t, dataDir, "")
			request := newVaultStartupTestClient(t, &baseURL)
			syncState := filepath.Join(".aha", knowledgevault.AutoSyncStateFile)
			for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
				raw, err := os.ReadFile(filepath.Join(config.VaultPath, syncState))
				if err == nil {
					var state struct {
						PauseReason string `json:"pause_reason"`
					}
					if err := json.Unmarshal(raw, &state); err != nil || state.PauseReason != "not_git_repository" {
						t.Fatalf("auto-sync did not record its first preflight: %+v, err = %v", state, err)
					}
					break
				}
				if !os.IsNotExist(err) || time.Now().After(deadline) {
					t.Fatalf("waiting for auto-sync state: %v", err)
				}
			}

			assertSettings := func(wantImport bool) {
				t.Helper()
				var result struct {
					Settings struct {
						knowledgevault.Status
						CanImportOverBootstrap bool `json:"can_import_over_bootstrap"`
					}
				}
				request(http.MethodGet, "settings/knowledge-vault", nil, http.StatusOK, &result)
				if !result.Settings.VaultInitialized || result.Settings.IndexReady != wantImport || result.Settings.CanImportOverBootstrap != wantImport {
					t.Fatalf("vault status after auto-sync = %+v, want index ready and importable = %t", result.Settings, wantImport)
				}
			}
			assertSettings(!tc.blockIndex)
			stop()
			if tc.blockIndex {
				if err := os.Remove(config.IndexPath); err != nil {
					t.Fatal(err)
				}
			}
			baseURL, _ = startVaultTestControlPlane(t, dataDir, "")
			assertSettings(true)

			source := knowledgevault.DefaultConfig(t.TempDir())
			init, err := knowledgevault.Bootstrap(source, knowledgevault.InitOptions{VaultName: "Existing knowledge"})
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Import knowledgevault.ExistingVaultImportReport
			}
			request(http.MethodPost, "settings/knowledge-vault/import", map[string]string{
				"kind": "local", "path": source.VaultPath,
			}, http.StatusOK, &result)
			manifest, err := knowledgevault.ReadManifest(config.VaultPath)
			if err != nil || manifest.VaultID != init.VaultID || result.Import.VerifiedAt.IsZero() || result.Import.BackupPath == "" {
				t.Fatalf("vault import did not install the verified source: %+v, manifest = %+v, err = %v", result.Import, manifest, err)
			}
			if _, err := os.Stat(filepath.Join(result.Import.BackupPath, syncState)); err != nil {
				t.Fatalf("old runtime state was not retained in the scaffold backup: %v", err)
			}
		})
	}
}

func newVaultStartupTestClient(t *testing.T, baseURL *string) func(method, path string, body any, wantStatus int, result any) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	var registration struct {
		CSRF string `json:"csrf_token"`
	}
	request := func(method, path string, body any, wantStatus int, result any) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, *baseURL+"/api/v1/"+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", registration.CSRF)
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
	request(http.MethodPost, "auth/register", map[string]string{
		"setup_token": "test-setup", "username": "owner", "password": "test-owner-password",
	}, http.StatusCreated, &registration)
	return request
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

func startVaultTestControlPlane(t *testing.T, dataDir, claude string) (string, func()) {
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
			claudeBinary: claude,
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

const vaultTestClaudeName = "aha-vault-test-claude.exe"

// The copied test binary acts as a local Claude CLI for the end-to-end Turn.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == vaultTestClaudeName {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "Test startup") {
				fmt.Println(`{"type":"result","subtype":"success","result":"context reached test backend","session_id":"vault-test-session"}`)
				os.Exit(0)
			}
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func copyVaultTestClaude(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), vaultTestClaudeName)
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	copy, err := os.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(copy, source)
	if err := copy.Close(); copyErr != nil || err != nil {
		t.Fatalf("copy test backend: %v (close: %v)", copyErr, err)
	}
	return base
}
