package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestHardwareGroupsAndIOPersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	groups := []domain.HardwareGroup{{
		TaskID: task.ID, ID: "board-console", Position: 0, Description: "Main board",
		Mode:   domain.HardwareModeBoth,
		Serial: domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200},
		Network: domain.HardwareNetworkConfig{
			Host: "192.0.2.10", Port: 23, Protocol: domain.HardwareProtocolTelnet, SSHAuth: domain.HardwareSSHAuthAuto,
		},
		Username: "root", CredentialRef: "hardware/test/credential", PasswordConfigured: true,
		Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}}
	if err := database.ReplaceHardwareGroups(ctx, task.ID, groups); err != nil {
		t.Fatal(err)
	}
	stored, err := database.HardwareGroups(ctx, task.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("groups: %#v %v", stored, err)
	}
	if !stored[0].Supports("serial") || !stored[0].Supports("network") || !stored[0].Writable() {
		t.Fatalf("unexpected group: %#v", stored[0])
	}
	if stored[0].Network.SSHAuth != domain.HardwareSSHAuthAuto {
		t.Fatalf("unexpected SSH auth mode: %#v", stored[0].Network)
	}
	for index, data := range []string{"boot\r\n", "login: "} {
		sequence, err := database.AppendHardwareIO(ctx, domain.HardwareIOEvent{
			ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: stored[0].ID,
			Transport: domain.HardwareTransportSerial, Direction: "rx", Data: data,
			Encoding: "text", Source: "bridge", CreatedAt: now.Add(time.Duration(index) * time.Second),
		})
		if err != nil || sequence == 0 {
			t.Fatalf("append io: %d %v", sequence, err)
		}
	}
	page, err := database.HardwareIOPage(ctx, task.ID, stored[0].ID, domain.HardwareTransportSerial, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Data != "login: " || page.HasMore {
		t.Fatalf("tail page: %#v", page)
	}
	page, err = database.HardwareIOPage(ctx, task.ID, stored[0].ID, domain.HardwareTransportSerial, page.Items[0].Sequence-1, 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("incremental page: %#v %v", page, err)
	}
}

func createStoreTestTask(t *testing.T, database *Store) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	project := domain.Project{ID: domain.NewID("project"), Name: "Hardware", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{
		ID: domain.NewID("env"), Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: domain.NewID("model"), DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: domain.NewID("runtime"), WorkspaceID: workspace.ID, Backend: "stub",
		ModelID: model.ID, WireModel: "stub", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now,
	}
	task := domain.Task{
		ID: domain.NewID("task"), ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Hardware", OriginalRequest: "debug board", CurrentGoal: "debug board",
		Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error {
			_, err := database.CreateTaskWithSnapshot(ctx, snapshot, task)
			return err
		},
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	return task
}
