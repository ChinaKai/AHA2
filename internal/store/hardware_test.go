package store

import (
	"context"
	"database/sql"
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
		Mode:   domain.HardwareModeSerial,
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
	if !stored[0].Supports("serial") || stored[0].Supports("network") || !stored[0].Writable() {
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

func TestADBPairedStateMigrationAndPersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var hasV76 bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=76)`).Scan(&hasV76); err != nil || !hasV76 {
		t.Fatalf("schema v76=%v err=%v", hasV76, err)
	}
	now := time.Now().UTC()
	pairedAt := now.Format(time.RFC3339)
	resource := domain.HardwareResource{
		ID: "android-phone", Description: "Android phone", Mode: domain.HardwareModeNetwork,
		Network: domain.HardwareNetworkConfig{
			Host: "192.0.2.81", Port: 5555, Protocol: domain.HardwareProtocolADB,
			SSHAuth: domain.HardwareSSHAuthAuto,
		},
		ADBPairedAt: pairedAt, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertHardwareResource(ctx, resource); err != nil {
		t.Fatal(err)
	}
	stored, err := database.HardwareResource(ctx, resource.ID)
	if err != nil || stored.ADBPairedAt != pairedAt || stored.Network.Port != 5555 {
		t.Fatalf("stored ADB resource=%#v err=%v", stored, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		DELETE FROM schema_migrations WHERE version=76;
		ALTER TABLE hardware_resources DROP COLUMN adb_paired_at;
		UPDATE hardware_resources SET adb_pair_port=37123 WHERE id=?`, resource.ID); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var pairPort int
	var pairedState string
	if err := database.db.QueryRowContext(ctx, `SELECT adb_pair_port,adb_paired_at FROM hardware_resources WHERE id=?`, resource.ID).Scan(&pairPort, &pairedState); err != nil || pairPort != 0 || pairedState != "" {
		t.Fatalf("migrated pair port=%d err=%v", pairPort, err)
	}
}

func TestHardwareResourceMigrationPreservesLegacyBindings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		DELETE FROM schema_migrations WHERE version=73;
		DELETE FROM task_hardware_bindings;
		DELETE FROM hardware_resources;
		INSERT INTO hardware_groups(
			task_id,id,position,description,mode,serial_device,serial_baudrate,
			network_host,network_port,network_protocol,ssh_auth,username,credential_ref,
			password_configured,access,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		task.ID, "legacy-board", 0, "Legacy Board", domain.HardwareModeSerial, "COM7", 115200,
		"", 23, domain.HardwareProtocolTelnet, domain.HardwareSSHAuthAuto, "", "hardware/legacy/credential",
		1, domain.HardwareAccessReadWrite, now, now,
	); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	resources, err := database.HardwareResources(ctx)
	if err != nil || len(resources) != 1 || resources[0].CredentialRef != "hardware/legacy/credential" {
		t.Fatalf("migrated resources=%#v err=%v", resources, err)
	}
	groups, err := database.HardwareGroups(ctx, task.ID)
	if err != nil || len(groups) != 1 || groups[0].ID != "legacy-board" || groups[0].ResourceID != resources[0].ID {
		t.Fatalf("migrated binding=%#v err=%v", groups, err)
	}
}

func TestDualTransportResourceSplitsIntoIndependentBindings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		DELETE FROM schema_migrations WHERE version=74;
		INSERT INTO hardware_resources(
			id,description,mode,serial_device,serial_baudrate,network_host,network_port,
			network_protocol,ssh_auth,username,credential_ref,password_configured,access,
			created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"dual", "Dual Board", "both", "COM8", 115200, "192.0.2.70", 23,
		"raw", "auto", "root", "hardware-resource/shared/credential", 1,
		domain.HardwareAccessReadWrite, now, now,
	); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO task_hardware_bindings(
			task_id,id,resource_id,position,access,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?)`,
		task.ID, "dual", "dual", 0, domain.HardwareAccessReadWrite, now, now,
	); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	var preSplitBindings int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM task_hardware_bindings WHERE task_id=?`, task.ID).Scan(&preSplitBindings); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if preSplitBindings != 1 {
		raw.Close()
		t.Fatalf("pre-split bindings=%d", preSplitBindings)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	resources, err := database.HardwareResources(ctx)
	if err != nil || len(resources) != 2 {
		t.Fatalf("split resources=%#v err=%v", resources, err)
	}
	modes := map[string]bool{}
	for _, resource := range resources {
		modes[resource.Mode] = true
		if resource.CredentialRef != "hardware-resource/shared/credential" {
			t.Fatalf("shared credential changed during split: %#v", resource)
		}
	}
	if !modes[domain.HardwareModeSerial] || !modes[domain.HardwareModeNetwork] {
		t.Fatalf("unexpected split modes: %#v", resources)
	}
	bindings, err := database.TaskHardwareBindings(ctx, task.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("split bindings=%#v err=%v", bindings, err)
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
