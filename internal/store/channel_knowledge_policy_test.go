package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func createVaultChannelPolicyFixture(t *testing.T) (*Store, context.Context, domain.Owner, domain.ChannelInstance) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	owner := domain.Owner{ID: "owner-policy-test", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "plugin-policy-test", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{
		ID: "project-policy-host", Name: "Channel", ProjectType: "channel",
		DefaultWorkspaceID: "workspace-policy-host", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	hostWorkspace := domain.Workspace{
		ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	instance := domain.ChannelInstance{
		ID: "instance-policy-test", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "local",
		Name: "Channel", Status: "ready", Revision: 1, HostProjectID: hostProject.ID,
		HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	endpoints := []domain.ChannelEndpoint{
		{ID: "endpoint-policy-dm", InstanceID: instance.ID, Kind: domain.ChannelEndpointAssistantDM, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
		{ID: "endpoint-policy-group", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
	}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, endpoints); err != nil {
		t.Fatal(err)
	}
	return database, ctx, owner, instance
}

// A channel instance created while the vault is the read source has no legacy
// policy. That is an explicit unconfigured state, not a bare ErrNoRows.
func TestChannelKnowledgeSelectionReportsUnconfigured(t *testing.T) {
	database, ctx, _, instance := createVaultChannelPolicyFixture(t)
	_, err := database.ChannelKnowledgeSelection(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if !errors.Is(err, ErrChannelKnowledgeUnconfigured) {
		t.Fatalf("unconfigured selection error=%v, want ErrChannelKnowledgeUnconfigured", err)
	}
	entries, err := database.ChannelAllowedKnowledge(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatalf("legacy resolver returned an error for an unconfigured channel: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unconfigured channel leaked entries: %#v", entries)
	}
}

// The first explicit selection creates the policy itself: there is no legacy
// root to use as the fixed index, so the policy is stored with an empty index
// and must not create any knowledge_entries row.
func TestReplaceChannelKnowledgeGrantsCreatesVaultPolicyWithoutLegacyIndex(t *testing.T) {
	database, ctx, owner, instance := createVaultChannelPolicyFixture(t)
	now := time.Now().UTC()
	var knowledgeRowsBefore int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries`).Scan(&knowledgeRowsBefore); err != nil {
		t.Fatal(err)
	}
	grants := []domain.ChannelKnowledgeGrant{{KnowledgeEntryID: "knowledge_root_project_vault", GrantScope: "subtree"}}
	if err := database.ReplaceChannelKnowledgeGrants(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman, owner.ID, "selected", 0, grants, map[string]bool{"knowledge_root_project_vault": true}, now); err != nil {
		t.Fatal(err)
	}
	selection, err := database.ChannelKnowledgeSelection(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Configured || selection.PolicyID == "" || selection.FixedIndexEntryID != "" || selection.ScopeMode != "selected" {
		t.Fatalf("created policy=%#v", selection)
	}
	if len(selection.Grants) != 1 || selection.Grants[0].KnowledgeEntryID != "knowledge_root_project_vault" {
		t.Fatalf("created grants=%#v", selection.Grants)
	}
	var knowledgeRows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries`).Scan(&knowledgeRows); err != nil {
		t.Fatal(err)
	}
	if knowledgeRows != knowledgeRowsBefore {
		t.Fatalf("creating a vault policy wrote %d legacy knowledge rows", knowledgeRows-knowledgeRowsBefore)
	}
	// A stale creation attempt (revision 0 again) must conflict instead of
	// silently replacing the owner's explicit selection.
	if err := database.ReplaceChannelKnowledgeGrants(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman, owner.ID, "selected", 0, nil, nil, now); !errors.Is(err, ErrChannelRevision) {
		t.Fatalf("second create error=%v, want ErrChannelRevision", err)
	}
}

// The channel host project is not a knowledge source: an empty fixed index
// stays empty and the policy is still queryable by instance.
func TestChannelKnowledgePoliciesListsVaultCreatedPolicy(t *testing.T) {
	database, ctx, owner, instance := createVaultChannelPolicyFixture(t)
	now := time.Now().UTC()
	if err := database.ReplaceChannelKnowledgeGrants(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman, owner.ID, "selected", 0, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	policies, err := database.ChannelKnowledgePolicies(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 1 || policies[0]["endpoint"] != domain.ChannelEndpointGroupDigitalHuman || policies[0]["fixed_index_entry_id"] != "" {
		t.Fatalf("policies=%#v", policies)
	}
}

// TestChannelKnowledgePolicyMigrationDropsLegacyIndexFK rewinds a live database
// to the pre-v141 constraint shape, then checks that the upgrade keeps every
// policy and grant while allowing an empty (vault) fixed index.
func TestChannelKnowledgePolicyMigrationDropsLegacyIndexFK(t *testing.T) {
	database, ctx, _, instance := createVaultChannelPolicyFixture(t)
	now := time.Now().UTC()
	root, err := database.EnsureKnowledgeRoot(ctx, "project", instance.HostProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureChannelKnowledgePolicies(ctx, instance.ID, root.ID, now); err != nil {
		t.Fatal(err)
	}
	var groupPolicyID string
	if err := database.db.QueryRowContext(ctx, `SELECT p.id FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? AND e.kind=?`, instance.ID, domain.ChannelEndpointGroupDigitalHuman).Scan(&groupPolicyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_knowledge_grants(id,policy_id,knowledge_entry_id,grant_scope,granted_by_owner_id,created_at,revoked_at) VALUES('grant-policy-test',?,?,'node','owner-policy-test',?,'')`, groupPolicyID, root.ID, timeString(now)); err != nil {
		t.Fatal(err)
	}
	var policiesBefore, grantsBefore int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_policies`).Scan(&policiesBefore); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_grants`).Scan(&grantsBefore); err != nil {
		t.Fatal(err)
	}

	// Rewind to the old table shape: fixed_index_entry_id references the legacy
	// knowledge_entries table again.
	conn, err := database.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	rewindTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DELETE FROM schema_migrations WHERE version=141`,
		`CREATE TABLE channel_knowledge_policies_v140 (
			id TEXT PRIMARY KEY,
			instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
			endpoint_id TEXT NOT NULL UNIQUE REFERENCES channel_endpoints(id) ON DELETE CASCADE,
			fixed_index_entry_id TEXT NOT NULL REFERENCES knowledge_entries(id) ON DELETE RESTRICT,
			default_visibility TEXT NOT NULL CHECK(default_visibility IN ('conversation_only','instance_shared')),
			revision INTEGER NOT NULL DEFAULT 1 CHECK(revision >= 1),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			scope_mode TEXT NOT NULL DEFAULT 'selected' CHECK(scope_mode IN ('all','selected'))
		)`,
		`INSERT INTO channel_knowledge_policies_v140(id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at,scope_mode)
		 SELECT id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at,scope_mode FROM channel_knowledge_policies`,
		`DROP TABLE channel_knowledge_policies`,
		`ALTER TABLE channel_knowledge_policies_v140 RENAME TO channel_knowledge_policies`,
	} {
		if _, err := rewindTx.ExecContext(ctx, statement); err != nil {
			_ = rewindTx.Rollback()
			_ = conn.Close()
			t.Fatalf("rewind statement failed: %v\n%s", err, statement)
		}
	}
	if err := rewindTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var references int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_list('channel_knowledge_policies') WHERE "table"='knowledge_entries'`).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if references == 0 {
		t.Fatal("fixture did not restore the pre-v141 foreign key")
	}

	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var policiesAfter, grantsAfter int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_policies`).Scan(&policiesAfter); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_grants`).Scan(&grantsAfter); err != nil {
		t.Fatal(err)
	}
	if policiesAfter != policiesBefore || grantsAfter != grantsBefore {
		t.Fatalf("migration changed rows: policies %d→%d grants %d→%d", policiesBefore, policiesAfter, grantsBefore, grantsAfter)
	}
	var survivingGrant string
	if err := database.db.QueryRowContext(ctx, `SELECT id FROM channel_knowledge_grants WHERE policy_id=? AND revoked_at=''`, groupPolicyID).Scan(&survivingGrant); err != nil {
		t.Fatalf("migration lost the existing grant: %v", err)
	}
	if survivingGrant != "grant-policy-test" {
		t.Fatalf("migration changed the grant id: %q", survivingGrant)
	}
	references = 0
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_list('channel_knowledge_policies') WHERE "table"='knowledge_entries'`).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if references != 0 {
		t.Fatalf("legacy knowledge_entries reference survived: %d", references)
	}
	var fixedIndex string
	if err := database.db.QueryRowContext(ctx, `SELECT fixed_index_entry_id FROM channel_knowledge_policies WHERE id=?`, groupPolicyID).Scan(&fixedIndex); err != nil {
		t.Fatal(err)
	}
	if fixedIndex != root.ID {
		t.Fatalf("migration changed fixed index %q, want %q", fixedIndex, root.ID)
	}
	for table, want := range map[string][]string{
		"channel_knowledge_policies": {"channel_instances", "channel_endpoints"},
		"channel_knowledge_grants":   {"channel_knowledge_policies", "owners"},
	} {
		seen := map[string]bool{}
		rows, err := database.db.QueryContext(ctx, `SELECT "table" FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var referenced string
			if err := rows.Scan(&referenced); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			seen[referenced] = true
		}
		rows.Close()
		for _, referenced := range want {
			if !seen[referenced] {
				t.Fatalf("%s lost its %s foreign key: %#v", table, referenced, seen)
			}
		}
	}
	// A fresh channel endpoint can now store an explicit empty fixed index;
	// before v141 the foreign key to knowledge_entries rejected that row.
	secondProject := domain.Project{
		ID: "project-policy-second", Name: "Channel 2", ProjectType: "channel",
		DefaultWorkspaceID: "workspace-policy-second", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	secondWorkspace := domain.Workspace{
		ID: secondProject.DefaultWorkspaceID, ProjectID: secondProject.ID, Name: "Channel 2",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	secondInstance := domain.ChannelInstance{
		ID: "instance-policy-second", PluginID: "plugin-policy-test", OwnerID: "owner-policy-test",
		RuntimeDeviceID: "local", Name: "Channel 2", Status: "ready", Revision: 1,
		HostProjectID: secondProject.ID, HostWorkspaceID: secondWorkspace.ID,
		Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	secondEndpoint := domain.ChannelEndpoint{
		ID: "endpoint-policy-second", InstanceID: secondInstance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman,
		Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelInstanceWithHost(ctx, secondProject, secondWorkspace, secondInstance, []domain.ChannelEndpoint{secondEndpoint}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_knowledge_policies(id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at,scope_mode)
		VALUES('policy-empty-index',?,?,'','conversation_only',1,?,?,'selected')`, secondInstance.ID, secondEndpoint.ID, timeString(now), timeString(now)); err != nil {
		t.Fatalf("empty fixed index was rejected after migration: %v", err)
	}
	// A second migrate must not rebuild or duplicate rows.
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var policiesAgain int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_policies`).Scan(&policiesAgain); err != nil {
		t.Fatal(err)
	}
	if policiesAgain != policiesAfter+1 {
		t.Fatalf("second migration changed policy rows: %d → %d", policiesAfter+1, policiesAgain)
	}
}
