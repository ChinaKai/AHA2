package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Stream input is stored on the frozen runtime snapshot the next Turn reads.
// The column is positional in every SELECT/scan, so reading a stored row back
// is what catches an insert or scan that drifted out of step.
func TestRuntimeSnapshotRoundTripsStreamInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateProject(ctx, domain.Project{
		ID: "project-stream-input", Name: "Stream", ProjectType: "folder", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-stream-input", ProjectID: "project-stream-input", Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{
		ID: "env-stream-input", Name: "Claude", ProviderID: "stub", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, SecretNames: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-stream-input", DisplayName: "Claude", ProviderID: "stub", Backend: "claude",
		WireModel: "claude-sonnet", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}

	// The migration must have added the column to an existing-style schema.
	var column bool
	if err := database.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pragma_table_info('runtime_config_snapshots') WHERE name='stream_input')`,
	).Scan(&column); err != nil || !column {
		t.Fatalf("stream_input column missing: exists=%t err=%v", column, err)
	}
	var migrated int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=125`,
	).Scan(&migrated); err != nil || migrated != 1 {
		t.Fatalf("schema v125 not recorded: count=%d err=%v", migrated, err)
	}

	stored := domain.RuntimeConfigSnapshot{
		ID: "runtime-stream-on", WorkspaceID: "workspace-stream-input", Backend: "claude",
		ModelID: "model-stream-input", WireModel: "claude-sonnet", EnvGroupID: "env-stream-input",
		EnvGroupRevision: 1, ReasoningEffort: "high", StreamInput: true,
		StreamIdleTimeoutMS: 120000, StreamMaxRetries: 2, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, stored); err != nil {
		t.Fatal(err)
	}
	read, err := database.RuntimeSnapshot(ctx, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !read.StreamInput {
		t.Fatalf("stream input was not persisted: %#v", read)
	}
	// The neighbouring columns must survive too, or the value shifted a column.
	if read.FastMode || read.ReasoningEffort != "high" || read.StreamIdleTimeoutMS != 120000 || read.StreamMaxRetries != 2 {
		t.Fatalf("neighbouring columns shifted: %#v", read)
	}

	// The default is off: an upgrade must not switch the experimental path on.
	plain := stored
	plain.ID, plain.StreamInput = "runtime-stream-default", false
	if err := database.CreateRuntimeSnapshot(ctx, plain); err != nil {
		t.Fatal(err)
	}
	readPlain, err := database.RuntimeSnapshot(ctx, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readPlain.StreamInput {
		t.Fatalf("stream input defaulted on: %#v", readPlain)
	}
	if readPlain.FastMode || readPlain.ReasoningEffort != "high" {
		t.Fatalf("neighbouring columns shifted for the default row: %#v", readPlain)
	}

	// The list-level resolver has its own column list; it must agree too.
	bulk, err := database.RuntimeSnapshotsByIDs(ctx, []string{stored.ID, plain.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !bulk[stored.ID].StreamInput || bulk[plain.ID].StreamInput {
		t.Fatalf("bulk snapshots mis-mapped stream input: %#v", bulk)
	}
}
