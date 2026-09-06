package sync_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestRunnerAutomaticallySynchronizesEveryConfiguredPortableCredential(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = center.Close() })
	server := httptest.NewServer(center.Handler())
	t.Cleanup(server.Close)

	makeDevice := func(id, token string, settings domain.SyncSettings) (*store.Store, *secrets.FileStore) {
		t.Helper()
		dir := t.TempDir()
		database, err := store.Open(ctx, filepath.Join(dir, "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		secretStore, err := secrets.Open(filepath.Join(dir, "secrets.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := secretStore.PutMany(map[string]string{
			syncer.DefaultTokenRef:      token,
			syncer.DefaultPassphraseRef: "shared-passphrase",
		}); err != nil {
			t.Fatal(err)
		}
		settings.Scope = "default"
		settings.Enabled = true
		settings.Endpoint = server.URL
		settings.DeviceID = id
		settings.IntervalSeconds = 60
		if err := database.PutSyncSettings(ctx, settings); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return database, secretStore
	}

	// These legacy selection fields intentionally name only the first item. The
	// default Runner path must ignore them and discover every configured item.
	source, sourceSecrets := makeDevice("credential-device-a", "source-sync-token", domain.SyncSettings{
		ProviderIDs: []string{"provider-one"}, EnvGroupIDs: []string{"env-one"}, CodexAccountIDs: []string{"codex-one"},
	})
	destination, destinationSecrets := makeDevice("credential-device-b", "destination-sync-token", domain.SyncSettings{})
	now := time.Now().UTC()

	providers := []domain.Provider{
		{ID: "provider-one", Name: "Provider One", BaseURL: "https://one.example.test", CredentialRef: "provider/provider-one/credential", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now},
		{ID: "provider-two", Name: "Provider Two", BaseURL: "https://two.example.test", CredentialRef: "provider/provider-two/credential", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now},
		{ID: "provider-disabled", Name: "Disabled", BaseURL: "https://disabled.example.test", CredentialRef: "provider/provider-disabled/credential", CredentialConfigured: false, CreatedAt: now, UpdatedAt: now},
		{ID: "provider-invalid", Name: "Invalid", BaseURL: "https://invalid.example.test", CredentialRef: "provider/provider-invalid/credential/extra", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, provider := range providers {
		if err := source.UpsertProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
	}

	envGroups := []domain.EnvGroup{
		{ID: "env-one", Name: "Env One", ProviderID: "provider-one", Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{"FIRST_TOKEN": "env/env-one/FIRST_TOKEN"}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now},
		{ID: "env-two", Name: "Env Two", ProviderID: "provider-two", Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{"SECOND_TOKEN": "env/env-two/SECOND_TOKEN"}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now},
		{ID: "env-mixed", Name: "Env Mixed", ProviderID: "provider-one", Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{
			"SAFE_TOKEN": "env/env-mixed/SAFE_TOKEN", "HARDWARE_TOKEN": "hardware/task/board/credential", "SSH_TOKEN": "workspace/remote/ssh/credential", "ARBITRARY_TOKEN": "custom/arbitrary",
		}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, group := range envGroups {
		if err := source.UpsertEnvGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
	}

	accounts := []domain.CodexAccount{
		{ID: "codex-one", Label: "Codex One", Status: "ready", CredentialRef: "codex-account/codex-one/auth", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now},
		{ID: "codex-two", Label: "Codex Two", Status: "ready", CredentialRef: "codex-account/codex-two/auth", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now},
		{ID: "codex-invalid", Label: "Codex Invalid", Status: "ready", CredentialRef: "codex-account/codex-invalid/auth/extra", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, account := range accounts {
		if err := source.UpsertCodexAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	project := domain.Project{ID: "credential-project", Name: "Credentials", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "credential-workspace", ProjectID: project.ID, Name: "SSH", Locality: "remote", Transport: "ssh", RootPath: "/srv/private", SSHHost: "host.example", SSHUser: "root", SSHPort: 22, SSHAuth: "password", SSHCredentialRef: "workspace/credential-workspace/ssh/credential", SSHPasswordConfigured: true, Health: "ready", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := source.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}

	if err := sourceSecrets.PutMany(map[string]string{
		"provider/provider-one/credential":           "provider-one-secret",
		"provider/provider-two/credential":           "provider-two-secret",
		"provider/provider-disabled/credential":      "disabled-secret",
		"provider/provider-invalid/credential/extra": "invalid-provider-secret",
		"env/env-one/FIRST_TOKEN":                    "env-one-secret",
		"env/env-two/SECOND_TOKEN":                   "env-two-secret",
		"env/env-mixed/SAFE_TOKEN":                   "env-safe-secret",
		"hardware/task/board/credential":             "hardware-secret",
		"workspace/remote/ssh/credential":            "ssh-secret",
		"custom/arbitrary":                           "arbitrary-secret",
		"codex-account/codex-one/auth":               `{"auth_mode":"chatgpt","tokens":{"access_token":"one"}}`,
		"codex-account/codex-two/auth":               `{"auth_mode":"chatgpt","tokens":{"access_token":"two"}}`,
		"codex-account/codex-invalid/auth/extra":     `{"auth_mode":"chatgpt","tokens":{"access_token":"invalid"}}`,
		workspace.SSHCredentialRef:                   "workspace-mirror-secret",
	}); err != nil {
		t.Fatal(err)
	}

	if err := (syncer.Runner{Store: source, Secrets: sourceSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := (syncer.Runner{Store: destination, Secrets: destinationSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	assertSecret := func(ref, expected string) {
		t.Helper()
		actual, ok := destinationSecrets.Get(ref)
		if !ok || actual != expected {
			t.Fatalf("portable secret %q was not synchronized", ref)
		}
	}
	assertSecret("provider/provider-one/credential", "provider-one-secret")
	assertSecret("provider/provider-two/credential", "provider-two-secret")
	assertSecret("env/env-one/FIRST_TOKEN", "env-one-secret")
	assertSecret("env/env-two/SECOND_TOKEN", "env-two-secret")
	assertSecret("env/env-mixed/SAFE_TOKEN", "env-safe-secret")
	assertSecret("codex-account/codex-one/auth", `{"auth_mode":"chatgpt","tokens":{"access_token":"one"}}`)
	assertSecret("codex-account/codex-two/auth", `{"auth_mode":"chatgpt","tokens":{"access_token":"two"}}`)
	remoteWorkspaces, err := destination.ListWorkspaces(ctx, project.ID)
	if err != nil || len(remoteWorkspaces) != 1 || !remoteWorkspaces[0].ReadOnly {
		t.Fatalf("remote workspace mirror=%#v err=%v", remoteWorkspaces, err)
	}
	assertSecret(syncer.MirrorWorkspaceSecretRef(remoteWorkspaces[0].ID), "workspace-mirror-secret")

	if token, _ := destinationSecrets.Get(syncer.DefaultTokenRef); token != "destination-sync-token" {
		t.Fatal("destination sync token was overwritten")
	}
	for _, ref := range []string{
		"provider/provider-disabled/credential",
		"provider/provider-invalid/credential/extra",
		"hardware/task/board/credential",
		"workspace/remote/ssh/credential",
		"custom/arbitrary",
		"codex-account/codex-invalid/auth/extra",
	} {
		if _, ok := destinationSecrets.Get(ref); ok {
			t.Fatalf("non-portable secret %q was synchronized", ref)
		}
	}
	if _, err := destination.CodexAccount(ctx, "codex-invalid"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("non-portable Codex account unexpectedly restored: %v", err)
	}
}
