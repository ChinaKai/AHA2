package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// The snapshot carries the accelerated-tier choice, and it is read back by
// position: a column added out of step with the insert, the select, or the scan
// would mis-map values without failing to compile. Reading a stored row back is
// what catches that.
func TestRuntimeSnapshotRoundTripsTheFastTier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	// The snapshot references a workspace, so one has to exist first.
	if err := database.CreateProject(ctx, domain.Project{ID: "project-fast", Name: "Fast", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-fast", ProjectID: "project-fast", Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "env-fast", Name: "Fast", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretNames: []string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-fast", DisplayName: "Fast", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	stored := domain.RuntimeConfigSnapshot{
		ID: "runtime-fast", WorkspaceID: "workspace-fast", Backend: "codex",
		ModelID: "model-fast", WireModel: "stub", EnvGroupID: "env-fast", EnvGroupRevision: 1,
		ReasoningEffort: "high", FastMode: true, StreamIdleTimeoutMS: 120000, StreamMaxRetries: 2,
		PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, stored); err != nil {
		t.Fatal(err)
	}
	read, err := database.RuntimeSnapshot(ctx, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !read.FastMode {
		t.Fatalf("fast mode was not persisted: %#v", read)
	}
	// The neighbours must survive too, or the value shifted a column.
	if read.ReasoningEffort != "high" || read.StreamIdleTimeoutMS != 120000 || read.StreamMaxRetries != 2 {
		t.Fatalf("neighbouring columns shifted: %#v", read)
	}
	// The default is off: an upgrade must not switch the tier on by itself.
	plain := stored
	plain.ID, plain.FastMode = "runtime-plain", false
	if err := database.CreateRuntimeSnapshot(ctx, plain); err != nil {
		t.Fatal(err)
	}
	readPlain, err := database.RuntimeSnapshot(ctx, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readPlain.FastMode {
		t.Fatalf("fast mode defaulted on: %#v", readPlain)
	}
}
