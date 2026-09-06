package sync_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestOwnedTaskAndWorkspaceDeletesConvergeWithoutReplayResurrection(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	type device struct {
		store  *store.Store
		runner syncer.Runner
	}
	makeDevice := func(id, token string) device {
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
		if err := secretStore.PutMany(map[string]string{syncer.DefaultTokenRef: token}); err != nil {
			t.Fatal(err)
		}
		if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: id, IntervalSeconds: 60}); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return device{store: database, runner: syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}}
	}
	source := makeDevice("graph-owner", "owner-token")
	destination := makeDevice("graph-reader", "reader-token")
	now := time.Now().UTC()
	project := domain.Project{ID: "graph-delete-project", Name: "Graph delete", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "graph-delete-workspace", ProjectID: project.ID, Name: "SSH", Locality: "remote", Transport: "ssh", RootPath: t.TempDir(), SSHHost: "owner.example", SSHUser: "builder", SSHPort: 2222, SSHAuth: "password", SSHCredentialRef: "private/workspace/credential", SSHPasswordConfigured: true, Health: "ready", CreatedAt: now, UpdatedAt: now}
	env := domain.EnvGroup{ID: "graph-delete-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "graph-delete-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	snapshot := domain.RuntimeConfigSnapshot{ID: "graph-delete-runtime", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, WireModel: "stub", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now}
	task := domain.Task{ID: "graph-delete-task", ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Task", OriginalRequest: "sync", CurrentGoal: "sync", Status: domain.TaskWaitingUser, RuntimeConfigSnapshotID: snapshot.ID, CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return source.store.CreateProject(ctx, project) },
		func() error { return source.store.CreateWorkspace(ctx, workspace) },
		func() error { return source.store.UpsertEnvGroup(ctx, env) },
		func() error { return source.store.UpsertModel(ctx, model) },
		func() error { _, err := source.store.CreateTaskWithSnapshot(ctx, snapshot, task); return err },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.store.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
		TaskID: task.ID, ID: "board", Description: "Board", Mode: domain.HardwareModeBoth,
		Serial:   domain.HardwareSerialConfig{Device: "/dev/cu.owner", Baudrate: 115200},
		Network:  domain.HardwareNetworkConfig{Host: "board.owner", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword},
		Username: "root", CredentialRef: "private/hardware/credential", PasswordConfigured: true,
		Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	exported, err := syncer.ExportBusinessObjectsForDevice(ctx, source.store, "graph-owner")
	if err != nil {
		t.Fatal(err)
	}
	exportedJSON, _ := json.Marshal(exported)
	for _, secretOrLocal := range []string{workspace.RootPath, workspace.SSHCredentialRef, "private/hardware/credential"} {
		if strings.Contains(string(exportedJSON), secretOrLocal) {
			t.Fatalf("task graph leaked local path or credential reference %q", secretOrLocal)
		}
	}
	if err := source.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := destination.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mirrors, err := destination.store.RemoteTaskMirrors(ctx, project.ID)
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("initial task mirror=%#v err=%v", mirrors, err)
	}
	detail, err := destination.store.RemoteTaskMirror(ctx, mirrors[0].Task.ID)
	if err != nil || len(detail.Hardware) != 1 {
		t.Fatalf("remote task hardware=%#v err=%v", detail.Hardware, err)
	}
	remoteHardware := detail.Hardware[0]
	if remoteHardware.Access != domain.HardwareAccessReadOnly || remoteHardware.CredentialRef != "" || !remoteHardware.PasswordConfigured || remoteHardware.Serial.Device != "/dev/cu.owner" || remoteHardware.Network.Host != "board.owner" {
		t.Fatalf("unsafe or incomplete remote hardware mirror: %#v", remoteHardware)
	}
	remoteWorkspaces, err := destination.store.ListWorkspaces(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteWorkspaceCount := 0
	for _, item := range remoteWorkspaces {
		if item.ReadOnly && item.OwnerDeviceID == "graph-owner" {
			remoteWorkspaceCount++
			if item.RootPath != "" || item.SSHCredentialRef != "" || item.Transport != "ssh" || item.SSHHost != workspace.SSHHost || item.SSHUser != workspace.SSHUser || !item.SSHPasswordConfigured {
				t.Fatalf("unsafe or incomplete remote SSH workspace: %#v", item)
			}
		}
	}
	if remoteWorkspaceCount != 1 {
		t.Fatalf("initial workspace mirrors=%#v", remoteWorkspaces)
	}

	if err := source.store.DeleteTaskWithSyncTombstone(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := source.store.DeleteRuntimeSnapshot(ctx, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if err := source.store.DeleteWorkspaceWithSyncTombstone(ctx, workspace.ID); err != nil {
		t.Fatal(err)
	}
	if err := source.runner.RunOnce(ctx); err != nil {
		t.Fatalf("owner delete push failed: %v", err)
	}
	if err := destination.runner.RunOnce(ctx); err != nil {
		t.Fatalf("remote delete pull failed: %v", err)
	}
	if mirrors, err := destination.store.RemoteTaskMirrors(ctx, project.ID); err != nil || len(mirrors) != 0 {
		t.Fatalf("task mirror survived tombstone: %#v err=%v", mirrors, err)
	}
	remoteWorkspaces, err = destination.store.ListWorkspaces(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range remoteWorkspaces {
		if item.ReadOnly && item.OwnerDeviceID == "graph-owner" {
			t.Fatalf("workspace mirror survived tombstone: %#v", item)
		}
	}

	newDevice := makeDevice("graph-new", "new-token")
	if err := newDevice.runner.RunOnce(ctx); err != nil {
		t.Fatalf("new device replay failed: %v", err)
	}
	if mirrors, err := newDevice.store.RemoteTaskMirrors(ctx, project.ID); err != nil || len(mirrors) != 0 {
		t.Fatalf("old task events resurrected on new device: %#v err=%v", mirrors, err)
	}
	for _, item := range mustWorkspaces(t, ctx, newDevice.store, project.ID) {
		if item.ReadOnly && item.OwnerDeviceID == "graph-owner" {
			t.Fatalf("old workspace event resurrected on new device: %#v", item)
		}
	}
}

func mustWorkspaces(t *testing.T, ctx context.Context, database *store.Store, projectID string) []domain.Workspace {
	t.Helper()
	items, err := database.ListWorkspaces(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return items
}
