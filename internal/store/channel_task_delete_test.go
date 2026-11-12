package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type channelHistoryFixture struct {
	instance     domain.ChannelInstance
	conversation domain.ChannelConversation
	routeID      string
	handoffID    string
}

// createChannelHistoryFixture builds the channel history that blocked the
// production Workspace delete: a conversation hosted by hostTaskID plus a route
// and its subscription that were activated for routeTaskID and then exited, and
// a handoff that created hostTaskID.
func createChannelHistoryFixture(t *testing.T, database *Store, hostTaskID, routeTaskID string) channelHistoryFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.Owner{ID: domain.NewID("owner"), Username: domain.NewID("owner-name"), PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: domain.NewID("plugin"), ProviderKey: domain.NewID("provider"), DisplayName: "Test",
		ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{
		ID: domain.NewID("project"), Name: "Channel", ProjectType: "channel",
		DefaultWorkspaceID: domain.NewID("workspace"), KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	hostWorkspace := domain.Workspace{
		ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	instance := domain.ChannelInstance{
		ID: domain.NewID("instance"), PluginID: plugin.ID, OwnerID: owner.ID,
		RuntimeDeviceID: "local", Name: "Channel", Status: "ready", Revision: 1,
		HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID,
		Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: domain.NewID("endpoint"), InstanceID: instance.ID,
		Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: domain.NewID("conversation"), InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 2, ScopeKey: domain.NewID("scope"), ExternalChatID: "chat",
		DisplayName: "Group", HostTaskID: hostTaskID, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{
		ID: domain.NewID("session"), ConversationID: conversation.ID, Generation: 1,
		Mode: "group_qa", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	route, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, routeTaskID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ExitOwnerChannelTaskRoute(ctx, route.ID, routeTaskID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	identity, err := database.UpsertObservedChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: domain.NewID("identity"), InstanceID: instance.ID, ExternalUserID: domain.NewID("user"),
		Role: "participant", DisplayName: "Member", Status: "active", LinkedAt: now,
	}, "Member")
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := database.ReceiveChannelInbox(ctx, domain.ChannelInboxReceipt{
		ID: domain.NewID("inbox"), InstanceID: instance.ID, ExternalEventID: domain.NewID("event"),
		EventType: "message", PayloadDigest: "digest", State: "processed", ConversationID: conversation.ID,
		OccurredAt: now, ReceivedAt: now, ProcessedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := database.CreateChannelHandoff(ctx, domain.ChannelHandoff{
		ID: domain.NewID("handoff"), InstanceID: instance.ID, OriginConversationID: conversation.ID,
		OriginInboxID: receipt.ID, RequesterIdentityLinkID: identity.ID, Summary: "handoff",
		State: "task_created", CreatedTaskID: hostTaskID, CreatedAt: now, UpdatedAt: now, ResolvedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channelHistoryFixture{instance: instance, conversation: conversation, routeID: route.ID, handoffID: handoff.ID}
}

func assertTaskChannelHistoryCleared(t *testing.T, database *Store, fixture channelHistoryFixture, taskID string) {
	t.Helper()
	ctx := context.Background()
	for _, check := range []struct {
		name  string
		query string
	}{
		{"task routes", `SELECT COUNT(*) FROM channel_task_routes WHERE target_task_id=?`},
		{"task subscriptions", `SELECT COUNT(*) FROM channel_subscriptions WHERE source_task_id=?`},
	} {
		var count int
		if err := database.db.QueryRowContext(ctx, check.query, taskID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s left behind: count=%d err=%v", check.name, count, err)
		}
	}
	var createdTask sql.NullString
	if err := database.db.QueryRowContext(ctx, `SELECT created_task_id FROM channel_handoffs WHERE id=?`, fixture.handoffID).Scan(&createdTask); err != nil {
		t.Fatalf("handoff row was removed: %v", err)
	}
	if createdTask.Valid {
		t.Fatalf("handoff still references the deleted task: %q", createdTask.String)
	}
	conversation, err := database.ChannelConversation(ctx, fixture.conversation.ID)
	if err != nil {
		t.Fatalf("host conversation was deleted: %v", err)
	}
	if conversation.HostTaskID != "" {
		t.Fatalf("conversation still has host %q", conversation.HostTaskID)
	}
	if conversation.Status != "active" {
		t.Fatalf("detached conversation status=%q, want active so it can be re-bound", conversation.Status)
	}
	if _, err := database.ChannelInstance(ctx, fixture.instance.ID); err != nil {
		t.Fatalf("channel instance was deleted: %v", err)
	}
}

func TestDeleteTaskWithChannelHistorySucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, task.ID, task.ID)
	var foreignKeys int
	if err := database.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d err=%v, want enforcement on", foreignKeys, err)
	}
	if err := database.DeleteTaskWithSyncTombstone(ctx, task.ID); err != nil {
		t.Fatalf("delete task with channel history: %v", err)
	}
	if _, err := database.Task(ctx, task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("task survived: %v", err)
	}
	assertTaskChannelHistoryCleared(t, database, fixture, task.ID)
}

func TestDeleteWorkspaceWithChannelHistorySucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, task.ID, task.ID)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, task.WorkspaceID); err != nil {
		t.Fatalf("delete workspace with channel history: %v", err)
	}
	if _, err := database.Workspace(ctx, task.WorkspaceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace survived: %v", err)
	}
	assertTaskChannelHistoryCleared(t, database, fixture, task.ID)
}

// A channel conversation hosted by a Task that is being deleted must not take
// another Task's live route down with it.
func TestDeleteTaskKeepsConversationRoutedToAnotherTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	hostTask := createStoreTestTask(t, database)
	routedTask := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, hostTask.ID, hostTask.ID)
	now := time.Now().UTC()
	route, err := database.ActivateOwnerChannelTaskRoute(ctx, fixture.instance.ID, fixture.conversation.ID, routedTask.ID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteTaskWithSyncTombstone(ctx, hostTask.ID); err != nil {
		t.Fatalf("delete host task: %v", err)
	}
	conversation, err := database.ChannelConversation(ctx, fixture.conversation.ID)
	if err != nil {
		t.Fatalf("conversation was deleted: %v", err)
	}
	if conversation.HostTaskID != "" {
		t.Fatalf("conversation host=%q, want detached", conversation.HostTaskID)
	}
	if conversation.Status != "active" {
		t.Fatalf("conversation status=%q, want active while another task is routed", conversation.Status)
	}
	var activeRoute string
	if err := database.db.QueryRowContext(ctx, `SELECT target_task_id FROM channel_task_routes WHERE id=? AND state='active'`, route.ID).Scan(&activeRoute); err != nil || activeRoute != routedTask.ID {
		t.Fatalf("routed task's route=%q err=%v", activeRoute, err)
	}
	var activeSubscription int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_subscriptions WHERE route_id=? AND state='active'`, route.ID).Scan(&activeSubscription); err != nil || activeSubscription != 1 {
		t.Fatalf("routed task's subscription=%d err=%v", activeSubscription, err)
	}
	if _, err := database.Task(ctx, routedTask.ID); err != nil {
		t.Fatalf("routed task was deleted: %v", err)
	}
}

func TestDeleteWorkspaceKeepsOtherWorkspaceChannelState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	deletedTask := createStoreTestTask(t, database)
	otherTask := createStoreTestTask(t, database)
	deletedFixture := createChannelHistoryFixture(t, database, deletedTask.ID, deletedTask.ID)
	otherFixture := createChannelHistoryFixture(t, database, otherTask.ID, otherTask.ID)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, deletedTask.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	assertTaskChannelHistoryCleared(t, database, deletedFixture, deletedTask.ID)

	if _, err := database.Task(ctx, otherTask.ID); err != nil {
		t.Fatalf("other workspace task was deleted: %v", err)
	}
	otherConversation, err := database.ChannelConversation(ctx, otherFixture.conversation.ID)
	if err != nil {
		t.Fatalf("other workspace conversation was deleted: %v", err)
	}
	if otherConversation.HostTaskID != otherTask.ID || otherConversation.Status != "active" {
		t.Fatalf("other workspace conversation changed: host=%q status=%q", otherConversation.HostTaskID, otherConversation.Status)
	}
	var otherRoutes int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_task_routes WHERE id=? AND state='exited'`, otherFixture.routeID).Scan(&otherRoutes); err != nil || otherRoutes != 1 {
		t.Fatalf("other workspace route=%d err=%v", otherRoutes, err)
	}
	var otherHandoffTask sql.NullString
	if err := database.db.QueryRowContext(ctx, `SELECT created_task_id FROM channel_handoffs WHERE id=?`, otherFixture.handoffID).Scan(&otherHandoffTask); err != nil || !otherHandoffTask.Valid || otherHandoffTask.String != otherTask.ID {
		t.Fatalf("other workspace handoff=%v err=%v", otherHandoffTask, err)
	}
}

// A Workspace-in-use refusal must roll the channel cleanup back with the Task
// rows, otherwise a refused delete would still unbind channel history.
func TestDeleteWorkspaceRollsBackChannelCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	target := createStoreTestTask(t, database)
	sharing := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, target.ID, target.ID)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	targetSnapshot, err := database.RuntimeSnapshot(ctx, target.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET runtime_config_snapshot_id=? WHERE id=?`, targetSnapshot.ID, sharing.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, target.WorkspaceID); !errors.Is(err, ErrWorkspaceInUse) {
		t.Fatalf("delete error=%v, want ErrWorkspaceInUse", err)
	}
	if _, err := database.Task(ctx, target.ID); err != nil {
		t.Fatalf("target task is gone: %v", err)
	}
	var routes int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_task_routes WHERE id=? AND state='exited'`, fixture.routeID).Scan(&routes); err != nil || routes != 1 {
		t.Fatalf("route cleanup was not rolled back: count=%d err=%v", routes, err)
	}
	var subscriptions int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_subscriptions WHERE source_task_id=? AND state='closed'`, target.ID).Scan(&subscriptions); err != nil || subscriptions != 1 {
		t.Fatalf("subscription cleanup was not rolled back: count=%d err=%v", subscriptions, err)
	}
	var handoffTask sql.NullString
	if err := database.db.QueryRowContext(ctx, `SELECT created_task_id FROM channel_handoffs WHERE id=?`, fixture.handoffID).Scan(&handoffTask); err != nil || !handoffTask.Valid || handoffTask.String != target.ID {
		t.Fatalf("handoff cleanup was not rolled back: %v err=%v", handoffTask, err)
	}
	conversation, err := database.ChannelConversation(ctx, fixture.conversation.ID)
	if err != nil || conversation.HostTaskID != target.ID || conversation.Status != "active" {
		t.Fatalf("conversation cleanup was not rolled back: %#v err=%v", conversation, err)
	}
}

func TestChannelConversationHostIsNullable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var notNull int
	if err := database.db.QueryRowContext(ctx,
		`SELECT "notnull" FROM pragma_table_info('channel_conversations') WHERE name='host_task_id'`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 0 {
		t.Fatalf("channel_conversations.host_task_id is NOT NULL; deleting a host Task would be blocked")
	}
}

// The v132 rebuild must keep every conversation row and its host while making
// the column nullable. Roll a populated database back to the v131 table shape
// and let Open run the real migration.
func TestChannelConversationHostMigrationPreservesRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	hostTask := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, hostTask.ID, hostTask.ID)

	conn, err := database.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE channel_conversations_v131 (
			id TEXT PRIMARY KEY,
			instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
			endpoint_id TEXT NOT NULL REFERENCES channel_endpoints(id) ON DELETE RESTRICT,
			scope_key_version INTEGER NOT NULL DEFAULT 1 CHECK(scope_key_version >= 1),
			scope_key TEXT NOT NULL,
			external_chat_id TEXT NOT NULL DEFAULT '',
			external_sender_id TEXT NOT NULL DEFAULT '',
			owner_identity_link_id TEXT REFERENCES channel_identity_links(id) ON DELETE RESTRICT,
			host_task_id TEXT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE RESTRICT,
			status TEXT NOT NULL CHECK(status IN ('active','closed')),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			members_synced_at TEXT NOT NULL DEFAULT '',
			UNIQUE(endpoint_id,scope_key_version,scope_key)
		)`,
		`INSERT INTO channel_conversations_v131(
			id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
			owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at)
		SELECT id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
			owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at
		FROM channel_conversations`,
		`DROP TABLE channel_conversations`,
		`ALTER TABLE channel_conversations_v131 RENAME TO channel_conversations`,
		`CREATE INDEX IF NOT EXISTS idx_channel_conversations_instance ON channel_conversations(instance_id,status)`,
		`DELETE FROM schema_migrations WHERE version=132`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			conn.Close()
			t.Fatalf("roll back to v131 shape: %v", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen with v132 migration: %v", err)
	}
	defer reopened.Close()
	conversation, err := reopened.ChannelConversation(ctx, fixture.conversation.ID)
	if err != nil {
		t.Fatalf("conversation row was lost by the rebuild: %v", err)
	}
	if conversation.HostTaskID != hostTask.ID || conversation.DisplayName != "Group" || conversation.Status != "active" {
		t.Fatalf("conversation changed during rebuild: %#v", conversation)
	}
	var notNull int
	if err := reopened.db.QueryRowContext(ctx,
		`SELECT "notnull" FROM pragma_table_info('channel_conversations') WHERE name='host_task_id'`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 0 {
		t.Fatalf("rebuilt host_task_id is still NOT NULL")
	}
	var applied int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=132`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("schema v132 record=%d err=%v", applied, err)
	}
}

// A conversation whose host and every routed Task are removed by the same
// Workspace delete stays as a detached record that the Owner can re-bind.
func TestDeleteWorkspaceDetachesConversationAfterRoutedTasksAreGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	hostTask := createStoreTestTask(t, database)
	routedTask := createStoreTestTask(t, database)
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET workspace_id=? WHERE id=?`, hostTask.WorkspaceID, routedTask.ID); err != nil {
		t.Fatal(err)
	}
	fixture := createChannelHistoryFixture(t, database, hostTask.ID, hostTask.ID)
	now := time.Now().UTC()
	if _, err := database.ActivateOwnerChannelTaskRoute(ctx, fixture.instance.ID, fixture.conversation.ID, routedTask.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, hostTask.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	conversation, err := database.ChannelConversation(ctx, fixture.conversation.ID)
	if err != nil {
		t.Fatalf("conversation was deleted: %v", err)
	}
	if conversation.HostTaskID != "" || conversation.Status != "active" {
		t.Fatalf("conversation host=%q status=%q, want detach-only", conversation.HostTaskID, conversation.Status)
	}
	var remaining int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_task_routes WHERE conversation_id=?`, conversation.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("routes left=%d err=%v", remaining, err)
	}
}
