package sync

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func secretBundleFixture(t *testing.T) (context.Context, *store.Store, *secrets.FileStore) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db, secretStore
}

func TestSecretBundleIsSelectiveEncryptedAndRejectsWrongPassphrase(t *testing.T) {
	ctx, source, sourceSecrets := secretBundleFixture(t)
	now := time.Now().UTC()
	provider := domain.Provider{ID: "provider-one", Name: "Provider", BaseURL: "https://example.test", CredentialRef: "provider/provider-one/credential", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}
	if err := source.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{ID: "env-one", Name: "Env", ProviderID: provider.ID, Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{"API_TOKEN": "env/env-one/API_TOKEN"}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now}
	if err := source.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := sourceSecrets.PutMany(map[string]string{provider.CredentialRef: "provider-plaintext", group.SecretRefs["API_TOKEN"]: "env-plaintext", "hardware/task/board/credential": "hardware-plaintext", "sync/default/token": "sync-local-only"}); err != nil {
		t.Fatal(err)
	}
	obj, err := ExportSecretBundle(ctx, source, sourceSecrets, SecretSelection{ProviderIDs: []string{provider.ID}, EnvGroupIDs: []string{group.ID}}, "bundle password")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(obj)
	for _, plain := range []string{"provider-plaintext", "env-plaintext", "hardware-plaintext", "sync-local-only"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("bundle leaked plaintext %q", plain)
		}
	}
	_, destination, destinationSecrets := secretBundleFixture(t)
	if err := destination.UpsertProvider(ctx, domain.Provider{ID: provider.ID, Name: "Local", BaseURL: "https://local.test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := destination.UpsertEnvGroup(ctx, domain.EnvGroup{ID: group.ID, Name: "Local Env", ProviderID: provider.ID, Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := ApplySecretBundle(ctx, destination, destinationSecrets, obj, "wrong password"); err == nil {
		t.Fatal("wrong passphrase unexpectedly succeeded")
	}
	if _, ok := destinationSecrets.Get("provider/provider-one/credential"); ok {
		t.Fatal("wrong passphrase mutated Secret Store")
	}
	if err := ApplySecretBundle(ctx, destination, destinationSecrets, obj, "bundle password"); err != nil {
		t.Fatal(err)
	}
	if value, _ := destinationSecrets.Get("provider/provider-one/credential"); value != "provider-plaintext" {
		t.Fatalf("provider secret=%q", value)
	}
	if value, _ := destinationSecrets.Get("env/env-one/API_TOKEN"); value != "env-plaintext" {
		t.Fatalf("env secret=%q", value)
	}
}

func TestApplySecretBundlePreservesLocalReferencesAndRestoresCodexAccount(t *testing.T) {
	ctx, source, sourceSecrets := secretBundleFixture(t)
	now := time.Now().UTC()
	provider := domain.Provider{ID: "provider-one", Name: "Provider", BaseURL: "https://example.test", CredentialRef: "provider/provider-one/credential", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}
	_ = source.UpsertProvider(ctx, provider)
	group := domain.EnvGroup{ID: "env-one", Name: "Env", ProviderID: provider.ID, Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{"API_TOKEN": "env/env-one/API_TOKEN"}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now}
	_ = source.UpsertEnvGroup(ctx, group)
	account := domain.CodexAccount{ID: "codex-one", Label: "Work", Email: "owner@example.test", AccountID: "account-remote", PlanType: "plus", Status: "ready", ProxyEnabled: true, CredentialRef: "codex-account/codex-one/auth", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}
	if err := source.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := sourceSecrets.PutMany(map[string]string{provider.CredentialRef: "provider-secret", group.SecretRefs["API_TOKEN"]: "env-secret", account.CredentialRef: `{"auth_mode":"chatgpt","tokens":{"access_token":"access-secret","refresh_token":"refresh-secret"}}`}); err != nil {
		t.Fatal(err)
	}
	obj, err := ExportSecretBundle(ctx, source, sourceSecrets, SecretSelection{ProviderIDs: []string{provider.ID}, EnvGroupIDs: []string{group.ID}, CodexAccountIDs: []string{account.ID}}, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	_, destination, destinationSecrets := secretBundleFixture(t)
	localProviderRef := "provider/local-provider-ref/credential"
	localEnvRef := "env/local-env-ref/API_TOKEN"
	if err := destination.UpsertProvider(ctx, domain.Provider{ID: provider.ID, Name: "Local", BaseURL: "https://local.test", CredentialRef: localProviderRef, CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := destination.UpsertEnvGroup(ctx, domain.EnvGroup{ID: group.ID, Name: "Local Env", ProviderID: provider.ID, Backend: "codex", Revision: 4, Environment: map[string]string{}, SecretRefs: map[string]string{"API_TOKEN": localEnvRef}, SecretConfigured: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := ApplySecretBundle(ctx, destination, destinationSecrets, obj, "passphrase"); err != nil {
		t.Fatal(err)
	}
	restoredProvider, _ := destination.Provider(ctx, provider.ID)
	restoredGroup, _ := destination.EnvGroup(ctx, group.ID)
	if restoredProvider.CredentialRef != localProviderRef || restoredGroup.SecretRefs["API_TOKEN"] != localEnvRef {
		t.Fatalf("local refs changed: provider=%q env=%q", restoredProvider.CredentialRef, restoredGroup.SecretRefs["API_TOKEN"])
	}
	restored, err := destination.CodexAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Label != "Work" || restored.AccountID != "account-remote" || !restored.CredentialConfigured {
		t.Fatalf("Codex account not restored: %#v", restored)
	}
	credential, ok := destinationSecrets.Get(restored.CredentialRef)
	if !ok || !strings.Contains(credential, "refresh-secret") {
		t.Fatal("Codex credential not restored")
	}
}

func TestApplySecretBundleSkipsDeletedProviderAndEnvSecrets(t *testing.T) {
	ctx, destination, destinationSecrets := secretBundleFixture(t)
	bundle := portableSecretBundle{Version: 1,
		Providers: []portableProvider{{ID: "provider_deleted", Value: "provider-secret"}},
		EnvGroups: []portableEnvGroup{{ID: "env_deleted", Values: map[string]string{"API_TOKEN": "env-secret"}}},
	}
	plain, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := EncryptBundle(plain, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(string(encrypted))
	object := domain.SyncObject{Type: TypeSecretBundle, Operation: "upsert", Payload: payload}
	if err := ApplySecretBundle(ctx, destination, destinationSecrets, object, "passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, ok := destinationSecrets.Get("provider/provider_deleted/credential"); ok {
		t.Fatal("deleted provider secret was written")
	}
	if _, ok := destinationSecrets.Get("env/env_deleted/API_TOKEN"); ok {
		t.Fatal("deleted env secret was written")
	}
}

func TestSecretBundleRejectsNonPortableReferences(t *testing.T) {
	for name, ref := range map[string]string{
		"sync token":       "sync/default/token",
		"SSH credential":   "workspace/remote/ssh/credential",
		"hardware secret":  "hardware/task/board/credential",
		"arbitrary secret": "custom/arbitrary",
		"prefix confusion": "provider/provider-one/credential/extra",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, db, secretStore := secretBundleFixture(t)
			now := time.Now().UTC()
			provider := domain.Provider{ID: "provider-one", Name: "P", BaseURL: "https://example.test", CredentialRef: ref, CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}
			if err := db.UpsertProvider(ctx, provider); err != nil {
				t.Fatal(err)
			}
			if err := secretStore.PutMany(map[string]string{provider.CredentialRef: "must-not-export"}); err != nil {
				t.Fatal(err)
			}
			_, err := ExportSecretBundle(ctx, db, secretStore, SecretSelection{ProviderIDs: []string{provider.ID}}, "passphrase")
			if !errors.Is(err, ErrNoPortableSecrets) {
				t.Fatalf("non-portable reference returned %v", err)
			}
		})
	}
}
