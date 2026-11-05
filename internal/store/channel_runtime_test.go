package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
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

	unattempted := enqueueTestDelivery(t, database, instance, conversation, 1, now.Add(-time.Minute))
	attempted := enqueueTestDelivery(t, database, instance, conversation, 2, now.Add(-time.Minute))
	dead := enqueueTestDelivery(t, database, instance, conversation, 3, now.Add(-time.Minute))
	delivered := enqueueTestDelivery(t, database, instance, conversation, 4, now.Add(-time.Minute))
	activeLease := enqueueTestDelivery(t, database, instance, conversation, 5, now.Add(-time.Minute))
	expiredLease := enqueueTestDelivery(t, database, instance, conversation, 6, now.Add(-time.Minute))
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-9*time.Minute)), timeString(now.Add(-9*time.Minute)), unattempted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET attempts=2,created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-25*time.Minute)), timeString(now.Add(-25*time.Minute)), attempted.ID); err != nil {
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
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='leased',attempts=1,lease_id='lease-active',lease_until=? WHERE id=?`,
		timeString(now.Add(5*time.Minute)), activeLease.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='leased',attempts=1,lease_id='lease-expired',lease_until=? WHERE id=?`,
		timeString(now.Add(-3*time.Minute)), expiredLease.ID); err != nil {
		t.Fatal(err)
	}

	health, err := database.ChannelOutboxHealth(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := health[instance.ID]
	if !ok {
		t.Fatalf("instance missing from outbox health: %#v", health)
	}
	if got.Pending != 2 || got.StuckPending != 1 || got.QueuedPending != 0 || got.AttemptedPending != 1 || got.DeadLetter != 1 || got.ExpiredLeases != 1 {
		t.Fatalf("outbox health counts=%#v", got)
	}
	if got.LastErrorCode != "feishu_target_unavailable" {
		t.Fatalf("last error=%q", got.LastErrorCode)
	}
	if now.Sub(got.OldestPendingAt) < 24*time.Minute {
		t.Fatalf("oldest pending not preserved: %#v", got)
	}
	if now.Sub(got.OldestUnattemptedPendingAt) < 8*time.Minute || now.Sub(got.OldestUnattemptedPendingAt) > 10*time.Minute {
		t.Fatalf("oldest unattempted pending=%v", got.OldestUnattemptedPendingAt)
	}
	if now.Sub(got.OldestAttemptedPendingAt) < 24*time.Minute {
		t.Fatalf("oldest attempted pending=%v", got.OldestAttemptedPendingAt)
	}
	if now.Sub(got.OldestExpiredLeaseAt) < 2*time.Minute || now.Sub(got.OldestExpiredLeaseAt) > 4*time.Minute {
		t.Fatalf("expired lease=%v", got.OldestExpiredLeaseAt)
	}
	if _, ok := health["missing-instance"]; ok {
		t.Fatalf("healthy instance should have no health row: %#v", health)
	}
}

func TestChannelOutboxHealthClassifiesQueuedUnattemptedRows(t *testing.T) {
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
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET attempts=2,created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-10*time.Minute)), timeString(now.Add(-10*time.Minute)), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET attempts=0,created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-10*time.Minute)), timeString(now.Add(-10*time.Minute)), second.ID); err != nil {
		t.Fatal(err)
	}

	health, err := database.ChannelOutboxHealth(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	got := health[instance.ID]
	if got.Pending != 2 || got.StuckPending != 0 || got.QueuedPending != 1 || got.AttemptedPending != 1 {
		t.Fatalf("queued classification=%#v", got)
	}
	if !got.OldestUnattemptedPendingAt.IsZero() {
		t.Fatalf("queued row leaked into head-of-line bucket: %#v", got)
	}
	if now.Sub(got.OldestQueuedPendingAt) < 9*time.Minute {
		t.Fatalf("oldest queued=%v", got.OldestQueuedPendingAt)
	}

	// The queued row uses the wider 20-minute threshold, not the 5-minute one.
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-25*time.Minute)), timeString(now.Add(-25*time.Minute)), second.ID); err != nil {
		t.Fatal(err)
	}
	health, err = database.ChannelOutboxHealth(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	got = health[instance.ID]
	if got.StuckPending != 0 || got.QueuedPending != 1 || now.Sub(got.OldestQueuedPendingAt) < 24*time.Minute {
		t.Fatalf("long queued classification=%#v", got)
	}

	// A head-of-line attempts=0 row still counts as stuck; the row behind it
	// counts as queued.
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET attempts=0,created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-10*time.Minute)), timeString(now.Add(-10*time.Minute)), first.ID); err != nil {
		t.Fatal(err)
	}
	health, err = database.ChannelOutboxHealth(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	got = health[instance.ID]
	if got.StuckPending != 1 || got.QueuedPending != 1 || got.AttemptedPending != 0 {
		t.Fatalf("head and queued classification=%#v", got)
	}
}

func TestChannelOutboxHealthQueryUsesPartialIndex(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	for sequence := int64(1); sequence <= 5; sequence++ {
		enqueueTestDelivery(t, database, instance, conversation, sequence, now.Add(-time.Minute))
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='dead_letter',last_error_code='feishu_target_unavailable' WHERE stream_sequence=3`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='delivered' WHERE stream_sequence=4`); err != nil {
		t.Fatal(err)
	}
	rows, err := database.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+channelOutboxHealthQuery, timeString(now), timeString(now))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	outerScan := false
	errorSearch := false
	for _, detail := range details {
		if strings.Contains(detail, "idx_channel_delivery_claim") {
			t.Fatalf("outbox health query used the claim index instead of the health index:\n%s", plan)
		}
		if strings.Contains(detail, "SCAN o USING INDEX idx_channel_delivery_health_error") {
			t.Fatalf("outer scan used the error index:\n%s", plan)
		}
		if strings.HasPrefix(detail, "SCAN o USING INDEX idx_channel_delivery_health") {
			outerScan = true
		}
		if strings.HasPrefix(detail, "SEARCH e USING INDEX idx_channel_delivery_health_error") {
			errorSearch = true
		}
	}
	if !outerScan {
		t.Fatalf("outer aggregate did not scan idx_channel_delivery_health:\n%s", plan)
	}
	if !errorSearch {
		t.Fatalf("last-error subquery did not search idx_channel_delivery_health_error:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_channel_delivery_blocking") {
		t.Fatalf("queued-behind lookup did not use idx_channel_delivery_blocking:\n%s", plan)
	}
}

// The INDEXED BY hints steer the planner, not correctness: if the v117/v118
// partial indexes disappear the same counters must come from the equivalent
// unhinted query, while unrelated SQL errors still surface.
func TestChannelOutboxHealthFallsBackWhenIndexMissing(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	head := enqueueTestDelivery(t, database, instance, conversation, 1, now.Add(-time.Minute))
	queued := enqueueTestDelivery(t, database, instance, conversation, 2, now.Add(-time.Minute))
	dead := enqueueTestDelivery(t, database, instance, conversation, 3, now.Add(-time.Minute))
	delivered := enqueueTestDelivery(t, database, instance, conversation, 4, now.Add(-time.Minute))
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET attempts=2,created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-10*time.Minute)), timeString(now.Add(-10*time.Minute)), head.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET created_at=?,updated_at=? WHERE id=?`,
		timeString(now.Add(-10*time.Minute)), timeString(now.Add(-10*time.Minute)), queued.ID); err != nil {
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

	indexed, err := database.ChannelOutboxHealth(ctx, now)
	if err != nil {
		t.Fatalf("outbox health with indexes: %v", err)
	}
	for _, index := range []string{"idx_channel_delivery_health", "idx_channel_delivery_health_error", "idx_channel_delivery_blocking"} {
		if _, err := database.db.ExecContext(ctx, `DROP INDEX `+index); err != nil {
			t.Fatalf("drop %s: %v", index, err)
		}
	}
	fallback, err := database.ChannelOutboxHealth(ctx, now)
	if err != nil {
		t.Fatalf("outbox health without indexes: %v", err)
	}
	if !reflect.DeepEqual(indexed, fallback) {
		t.Fatalf("fallback health differs: indexed=%#v fallback=%#v", indexed, fallback)
	}
	got, ok := fallback[instance.ID]
	if !ok {
		t.Fatalf("instance missing from fallback health: %#v", fallback)
	}
	if got.Pending != 2 || got.StuckPending != 0 || got.QueuedPending != 1 || got.AttemptedPending != 1 ||
		got.DeadLetter != 1 || got.LastErrorCode != "feishu_target_unavailable" {
		t.Fatalf("fallback health counts=%#v", got)
	}
}

// Only a missing index may trigger the fallback. A different SQL error must be
// returned unchanged even when the unhinted query would have succeeded.
func TestQueryWithIndexFallbackIgnoresNonIndexErrors(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.queryWithIndexFallback(ctx,
		`SELECT 1 FROM channel_delivery_outbox WHERE missing_column=1`,
		`SELECT 1`)
	if rows != nil {
		rows.Close()
		t.Fatal("non-index error returned rows, want the error")
	}
	if err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Fatalf("non-index error = %v, want no such column", err)
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

func TestConversationPageReadsLiveChannelDeliveryState(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	item := enqueueTestDelivery(t, database, instance, conversation, 1, now.Add(-time.Minute))

	// The route card snapshots delivery_state=pending when the outbox row is
	// created.
	itemID := domain.NewID("conversation")
	payload := encodeJSON(map[string]any{
		"status_card": true, "message_status": "replied",
		"delivery_id": item.ID, "delivery_state": "pending",
		"inbound_receipt_id": "receipt-live-state",
	})
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
			route_kind,category,kind,summary,payload_json,created_at
		) VALUES(?,?,'','', 'aha','main','aha','main','channel_route','update','agent_channel_route','reply',?,?)`,
		itemID, conversation.HostTaskID, payload, timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	readState := func() string {
		t.Helper()
		page, err := database.ConversationPage(ctx, conversation.HostTaskID, 0, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, conversationItem := range page.Items {
			if conversationItem.ID == itemID {
				return stringValue(conversationItem.Payload["delivery_state"])
			}
		}
		t.Fatalf("conversation item %s missing from page", itemID)
		return ""
	}

	if state := readState(); state != "pending" {
		t.Fatalf("pending card state=%q", state)
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE channel_delivery_outbox SET state='delivered',outcome_certainty='provider_accepted' WHERE id=?`, item.ID,
	); err != nil {
		t.Fatal(err)
	}
	// The card must not claim client delivery: stored "delivered" is only the
	// provider accepting the message, so it reads back as provider_accepted.
	if state := readState(); state != "provider_accepted" {
		t.Fatalf("provider accepted card state=%q", state)
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE channel_delivery_outbox SET state='dead_letter',outcome_certainty='confirmed_failure' WHERE id=?`, item.ID,
	); err != nil {
		t.Fatal(err)
	}
	if state := readState(); state != "dead_letter" {
		t.Fatalf("dead letter card state=%q", state)
	}
}

func TestChannelRouteCardFollowsLiveDeliveryAndReplayChain(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	// Use a private assistant endpoint so an ordinary agent_reply is mirrored
	// through the real channel_events path instead of the group task-route one.
	if _, err := database.db.ExecContext(ctx,
		`UPDATE channel_endpoints SET kind=? WHERE id=(SELECT endpoint_id FROM channel_conversations WHERE id=?)`,
		domain.ChannelEndpointAssistantDM, conversation.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, conversation.HostTaskID, now); err != nil {
		t.Fatal(err)
	}
	source := domain.ChannelSourceEvent{
		ID: domain.NewID("source"), SourceKey: "route-card-live-delivery", TaskID: conversation.HostTaskID,
		EventClass: "message", EventType: "agent_reply",
		SemanticPayload: map[string]any{"task_id": conversation.HostTaskID, "text": "route card body"},
		OccurredAt:      now,
	}
	if err := database.AppendChannelSourceAndProject(ctx, source, ""); err != nil {
		t.Fatal(err)
	}

	var cardID, payloadJSON string
	if err := database.db.QueryRowContext(ctx, `
		SELECT id,payload_json FROM conversation_items
		WHERE task_id=? AND kind='agent_channel_route'
		ORDER BY sequence DESC LIMIT 1`, conversation.HostTaskID,
	).Scan(&cardID, &payloadJSON); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSON(payloadJSON, map[string]any{})
	deliveryID := stringValue(payload["delivery_id"])
	if deliveryID == "" {
		t.Fatalf("route card has no delivery_id: %#v", payload)
	}
	readState := func() string {
		t.Helper()
		page, err := database.ConversationPage(ctx, conversation.HostTaskID, 0, 0, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.ID == cardID {
				return stringValue(item.Payload["delivery_state"])
			}
		}
		t.Fatalf("route card %s missing from conversation page", cardID)
		return ""
	}
	if state := readState(); state != "pending" {
		t.Fatalf("card state before send=%q", state)
	}

	claim := func(at time.Time) domain.ChannelDelivery {
		t.Helper()
		claimed, err := database.ClaimChannelDeliveries(ctx, instance.ID, 10, at, 30*time.Second)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claimed=%#v err=%v", claimed, err)
		}
		return claimed[0]
	}
	claimed := claim(now)
	if claimed.ID != deliveryID {
		t.Fatalf("claimed %s, want card delivery %s", claimed.ID, deliveryID)
	}
	if err := database.NackChannelDelivery(ctx, instance.ID, deliveryID, claimed.LeaseID,
		"feishu_target_unavailable", "confirmed_failure", 0, true, "req-dead", now); err != nil {
		t.Fatal(err)
	}
	if state := readState(); state != "dead_letter" {
		t.Fatalf("card state after dead letter=%q", state)
	}

	// Replaying keeps the original row in state='replayed' and appends a new
	// pending generation; the card must follow replay_of_id.
	replay, err := database.ReplayChannelDelivery(ctx, deliveryID, instance.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state := readState(); state != "pending" {
		t.Fatalf("card state after replay=%q", state)
	}
	claimed = claim(now.Add(2 * time.Second))
	if claimed.ID != replay.ID {
		t.Fatalf("claimed replay %s, want %s", claimed.ID, replay.ID)
	}
	if err := database.NackChannelDelivery(ctx, instance.ID, replay.ID, claimed.LeaseID,
		"feishu_target_unavailable", "confirmed_failure", 0, true, "req-dead-2", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state := readState(); state != "dead_letter" {
		t.Fatalf("card state after replay dead letter=%q", state)
	}

	secondReplay, err := database.ReplayChannelDelivery(ctx, replay.ID, instance.ID, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state := readState(); state != "pending" {
		t.Fatalf("card state after second replay=%q", state)
	}
	claimed = claim(now.Add(4 * time.Second))
	if claimed.ID != secondReplay.ID {
		t.Fatalf("claimed second replay %s, want %s", claimed.ID, secondReplay.ID)
	}
	if err := database.AckChannelDelivery(ctx, instance.ID, secondReplay.ID, claimed.LeaseID,
		"om_second_replay", "req-second-replay", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state := readState(); state != "provider_accepted" {
		t.Fatalf("card state after replay delivery=%q", state)
	}
}
