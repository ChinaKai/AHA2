package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func createDeliveryTestInstance(t *testing.T, database *Store) (domain.ChannelInstance, domain.ChannelConversation) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	task := createStoreTestTask(t, database)
	if err := database.CreateOwner(ctx, domain.Owner{
		ID: "delivery-owner", Username: "delivery-owner", PasswordHash: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: domain.NewID("plugin"), ProviderKey: "feishu", DisplayName: "Feishu",
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
		ID: domain.NewID("instance"), PluginID: plugin.ID, OwnerID: "delivery-owner",
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
		ScopeKeyVersion: 2, ScopeKey: "delivery-scope", ExternalChatID: "oc_delivery",
		DisplayName: "Delivery Group", HostTaskID: task.ID, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{
		ID: domain.NewID("session"), ConversationID: conversation.ID, Generation: 1,
		Mode: "group_qa", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return instance, conversation
}

func enqueueTestDelivery(t *testing.T, database *Store, instance domain.ChannelInstance, conversation domain.ChannelConversation, streamSequence int64, availableAt time.Time) domain.ChannelDelivery {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	sourceID := domain.NewID("channel_source_event")
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_source_events(id,source_key,source_revision,task_id,round_id,turn_id,conversation_item_id,event_class,event_type,semantic_payload_json,occurred_at) VALUES(?,?,1,?,'','','','message','agent_reply','{}',?)`,
		sourceID, sourceID, conversation.HostTaskID, timeString(now)); err != nil {
		t.Fatal(err)
	}
	var sourceSequence int64
	if err := database.db.QueryRowContext(ctx, `SELECT sequence FROM channel_source_events WHERE id=?`, sourceID).Scan(&sourceSequence); err != nil {
		t.Fatal(err)
	}
	item, err := database.EnqueueChannelDelivery(ctx, domain.ChannelDelivery{
		ID: domain.NewID("channel_delivery"), InstanceID: instance.ID, ConversationID: conversation.ID,
		SourceEventSequence: sourceSequence, StreamSequence: streamSequence, ReplayGeneration: 0,
		PayloadVersion: 1, IdempotencyKey: "delivery-test:" + sourceID, SemanticPayload: map[string]any{"text": "line"},
		State: "pending", AvailableAt: availableAt, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestChannelDeliveryClaimUnblocksAfterDeadLetter(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	first := enqueueTestDelivery(t, database, instance, conversation, 1, now.Add(-time.Minute))
	second := enqueueTestDelivery(t, database, instance, conversation, 2, now.Add(-time.Minute))
	third := enqueueTestDelivery(t, database, instance, conversation, 3, now.Add(-time.Minute))

	claimed, err := database.ClaimChannelDeliveries(ctx, instance.ID, 10, now, 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].ID != first.ID {
		t.Fatalf("ordered claim=%#v err=%v", claimed, err)
	}
	if err := database.NackChannelDelivery(ctx, instance.ID, first.ID, claimed[0].LeaseID, "feishu_target_unavailable", "confirmed_failure", 0, true, "req-dead", now); err != nil {
		t.Fatal(err)
	}
	claimed, err = database.ClaimChannelDeliveries(ctx, instance.ID, 10, now.Add(time.Second), 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].ID != second.ID || claimed[0].Attempts != 1 {
		t.Fatalf("claim after dead letter=%#v err=%v", claimed, err)
	}
	if err := database.AckChannelDelivery(ctx, instance.ID, second.ID, claimed[0].LeaseID, "om_accepted", "req-accepted", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	accepted, err := database.ChannelDelivery(ctx, second.ID)
	if err != nil || accepted.State != "delivered" || accepted.OutcomeCertainty != "provider_accepted" || accepted.ProviderMessageID != "om_accepted" {
		t.Fatalf("delivered semantics=%#v err=%v", accepted, err)
	}
	attempts, err := database.ChannelDeliveryAttempts(ctx, second.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Outcome != "provider_accepted" || attempts[0].ProviderRequestID != "req-accepted" {
		t.Fatalf("accepted attempts=%#v err=%v", attempts, err)
	}
	claimed, err = database.ClaimChannelDeliveries(ctx, instance.ID, 10, now.Add(3*time.Second), 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].ID != third.ID {
		t.Fatalf("claim after accepted=%#v err=%v", claimed, err)
	}

	dead, err := database.ChannelDelivery(ctx, first.ID)
	if err != nil || dead.State != "dead_letter" {
		t.Fatalf("dead letter=%#v err=%v", dead, err)
	}
	deadAttempts, err := database.ChannelDeliveryAttempts(ctx, first.ID)
	if err != nil || len(deadAttempts) != 1 || deadAttempts[0].Outcome != "dead_letter" || deadAttempts[0].ErrorCode != "feishu_target_unavailable" || deadAttempts[0].ProviderRequestID != "req-dead" {
		t.Fatalf("dead letter attempts=%#v err=%v", deadAttempts, err)
	}
}

func TestChannelOutboxHealthSummarizesQueue(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()

	oldPending := enqueueTestDelivery(t, database, instance, conversation, 1, now.Add(-time.Minute))
	enqueueTestDelivery(t, database, instance, conversation, 2, now.Add(-time.Minute))
	dead := enqueueTestDelivery(t, database, instance, conversation, 3, now.Add(-time.Minute))
	delivered := enqueueTestDelivery(t, database, instance, conversation, 4, now.Add(-time.Minute))
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-9*time.Minute)), timeString(now.Add(-9*time.Minute)), oldPending.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='dead_letter',last_error_code=?,updated_at=? WHERE id=?`,
		"feishu_target_unavailable", timeString(now.Add(-time.Minute)), dead.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='delivered',updated_at=? WHERE id=?`,
		timeString(now), delivered.ID); err != nil {
		t.Fatal(err)
	}

	health, err := database.ChannelOutboxHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := health[instance.ID]
	if !ok {
		t.Fatalf("instance missing from outbox health: %#v", health)
	}
	if got.Pending != 2 || got.StuckPending != 2 || got.DeadLetter != 1 || got.LastErrorCode != "feishu_target_unavailable" {
		t.Fatalf("outbox health=%#v", got)
	}
	if got.OldestPendingAt.IsZero() || now.Sub(got.OldestPendingAt) < 8*time.Minute {
		t.Fatalf("oldest pending not preserved: %#v", got)
	}
	if _, ok := health["missing-instance"]; ok {
		t.Fatalf("healthy instance should have no health row: %#v", health)
	}
}

func TestChannelDeliveryCertaintyMigrationNormalizesConfirmed(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	converted := enqueueTestDelivery(t, database, instance, conversation, 1, now)
	confirmedFailure := enqueueTestDelivery(t, database, instance, conversation, 2, now)
	unknown := enqueueTestDelivery(t, database, instance, conversation, 3, now)
	pending := enqueueTestDelivery(t, database, instance, conversation, 4, now)
	for _, update := range []struct {
		id, state, certainty string
	}{
		{converted.ID, "delivered", "confirmed"},
		{confirmedFailure.ID, "delivered", "confirmed_failure"},
		{unknown.ID, "delivered", "unknown"},
		{pending.ID, "pending", "confirmed"},
	} {
		if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state=?,outcome_certainty=? WHERE id=?`, update.state, update.certainty, update.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_delivery_attempts(id,delivery_id,attempt_no,lease_id,outcome,error_code,retry_after_ms,provider_request_id,started_at,finished_at) VALUES(?,?,1,'lease','delivered','',0,'',?, '')`,
		"attempt-converted", converted.ID, timeString(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_delivery_attempts(id,delivery_id,attempt_no,lease_id,outcome,error_code,retry_after_ms,provider_request_id,started_at,finished_at) VALUES(?,?,1,'lease','pending','',0,'',?, '')`,
		"attempt-pending", pending.ID, timeString(now)); err != nil {
		t.Fatal(err)
	}

	// Re-run the migration exactly as an existing pre-v115 database would.
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=115`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}

	for _, scenario := range []struct {
		id, state, certainty string
	}{
		{converted.ID, "delivered", "provider_accepted"},
		{confirmedFailure.ID, "delivered", "confirmed_failure"},
		{unknown.ID, "delivered", "unknown"},
		{pending.ID, "pending", "confirmed"},
	} {
		item, err := database.ChannelDelivery(ctx, scenario.id)
		if err != nil || item.State != scenario.state || item.OutcomeCertainty != scenario.certainty {
			t.Fatalf("delivery %s state=%q certainty=%q err=%v", scenario.id, item.State, item.OutcomeCertainty, err)
		}
	}
	convertedAttempts, err := database.ChannelDeliveryAttempts(ctx, converted.ID)
	if err != nil || len(convertedAttempts) != 1 || convertedAttempts[0].Outcome != "provider_accepted" {
		t.Fatalf("converted attempt=%#v err=%v", convertedAttempts, err)
	}
	pendingAttempts, err := database.ChannelDeliveryAttempts(ctx, pending.ID)
	if err != nil || len(pendingAttempts) != 1 || pendingAttempts[0].Outcome != "pending" {
		t.Fatalf("pending attempt=%#v err=%v", pendingAttempts, err)
	}
}

func TestChannelDeliveryRetryBackoffAndLeaseRecovery(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	item := enqueueTestDelivery(t, database, instance, conversation, 1, now.Add(-time.Minute))

	claimed, err := database.ClaimChannelDeliveries(ctx, instance.ID, 10, now, 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("initial claim=%#v err=%v", claimed, err)
	}
	if err := database.NackChannelDelivery(ctx, instance.ID, item.ID, claimed[0].LeaseID, "feishu_rate_limited", "confirmed_failure", 30*time.Second, false, "req-rate", now); err != nil {
		t.Fatal(err)
	}
	if claimed, err = database.ClaimChannelDeliveries(ctx, instance.ID, 10, now.Add(10*time.Second), 30*time.Second); err != nil || len(claimed) != 0 {
		t.Fatalf("claimed before backoff elapsed=%#v err=%v", claimed, err)
	}
	claimed, err = database.ClaimChannelDeliveries(ctx, instance.ID, 10, now.Add(31*time.Second), 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("claim after backoff=%#v err=%v", claimed, err)
	}
	attempts, err := database.ChannelDeliveryAttempts(ctx, item.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Outcome != "pending" || attempts[0].RetryAfterMS != 30000 || attempts[0].ProviderRequestID != "req-rate" || attempts[1].Outcome != "leased" {
		t.Fatalf("retry attempts=%#v err=%v", attempts, err)
	}

	// An expired lease from a crashed worker is recovered and counted as a new
	// attempt instead of stranding the delivery in leased forever.
	claimed, err = database.ClaimChannelDeliveries(ctx, instance.ID, 10, now.Add(62*time.Second), 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 3 {
		t.Fatalf("lease recovery claim=%#v err=%v", claimed, err)
	}
}
