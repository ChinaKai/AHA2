package sync

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestInfrastructureSecretBundleIsEncryptedAndStoredOnlyAsMirrorSecrets(t *testing.T) {
	ctx, source, sourceSecrets := secretBundleFixture(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-infra", Name: "Infra", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-infra", ProjectID: project.ID, Name: "Remote", Locality: "remote", Transport: "ssh",
		RootPath: "/srv/private", SSHHost: "host.example", SSHUser: "root", SSHPort: 22, SSHAuth: "password",
		SSHCredentialRef: localWorkspaceSecretRef("workspace-infra"), SSHPasswordConfigured: true,
		OwnerDeviceID: "device-a", Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := source.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "provider-infra", Name: "Provider", BaseURL: "https://example.test", CreatedAt: now, UpdatedAt: now}
	env := domain.EnvGroup{ID: "env-infra", Name: "Env", ProviderID: provider.ID, Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-infra", DisplayName: "Model", ProviderID: provider.ID, Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{func() error { return source.UpsertProvider(ctx, provider) }, func() error { return source.UpsertEnvGroup(ctx, env) }, func() error { return source.UpsertModel(ctx, model) }} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := domain.RuntimeConfigSnapshot{ID: "runtime-infra", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now}
	task := domain.Task{ID: "task-infra", ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Task", OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskWaitingUser, Isolation: "inplace", RuntimeConfigSnapshotID: snapshot.ID, CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := source.CreateTaskWithSnapshot(ctx, snapshot, task); err != nil {
		t.Fatal(err)
	}
	hardware := domain.HardwareGroup{TaskID: task.ID, ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.2", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword}, Username: "root", CredentialRef: localHardwareSecretRef(task.ID, "board"), PasswordConfigured: true, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
	if err := source.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{hardware}); err != nil {
		t.Fatal(err)
	}
	if err := sourceSecrets.PutMany(map[string]string{
		workspace.SSHCredentialRef: "workspace-secret",
		hardware.CredentialRef:     "hardware-secret",
		DefaultTokenRef:            "must-never-sync",
	}); err != nil {
		t.Fatal(err)
	}

	object, err := ExportInfrastructureSecretBundle(ctx, source, sourceSecrets, "device-a", "shared-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(object)
	for _, forbidden := range []string{"workspace-secret", "hardware-secret", "must-never-sync", "/srv/private"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("encrypted bundle leaked %q", forbidden)
		}
	}

	_, destination, destinationSecrets := secretBundleFixture(t)
	if err := ApplySecretBundle(context.Background(), destination, destinationSecrets, object, "shared-passphrase"); err != nil {
		t.Fatal(err)
	}
	workspaceRef := MirrorWorkspaceSecretRef(syncedWorkspaceID("device-a", workspace.ID))
	if value, ok := destinationSecrets.Get(workspaceRef); !ok || value != "workspace-secret" {
		t.Fatalf("workspace mirror secret missing: ok=%t value=%q", ok, value)
	}
	hardwareRef := MirrorHardwareSecretRef("device-a", task.ID, hardware.ID)
	if value, ok := destinationSecrets.Get(hardwareRef); !ok || value != "hardware-secret" {
		t.Fatalf("hardware mirror secret missing: ok=%t value=%q", ok, value)
	}
	if _, ok := destinationSecrets.Get(DefaultTokenRef); ok {
		t.Fatal("sync token crossed the device boundary")
	}
}
