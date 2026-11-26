package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestFeishuGroupReceptionMigratesLegacyAllowlist(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	route, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, conversation.HostTaskID, now)
	if err != nil {
		t.Fatal(err)
	}
	// Restore the actual v164 storage layout before writing legacy data.
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE channel_feishu_groups;
		DROP TABLE channel_group_listening;
		DROP TABLE channel_group_receipts;
		DELETE FROM schema_migrations WHERE version=165;`+schemaChannelTopics); err != nil {
		t.Fatal(err)
	}
	const missingChat = "oc_not_yet_listed"
	instance, err = database.ChannelInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	instance.Config["feishu_group_message_allowlist"] = []string{missingChat, conversation.ExternalChatID, missingChat}
	if _, err := database.UpdateChannelInstance(ctx, instance, instance.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_group_listening
		(id,conversation_id,route_id,target_task_id,message_id,thread_id,sender_name,sender_type,content,occurred_at,received_ms)
		VALUES('legacy-listening',?,?,?,'om_legacy','','Test user','user','legacy background',?,?)`,
		conversation.ID, route.ID, route.TargetTaskID, timeString(now), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	wake := domain.ChannelInboxReceipt{
		ID: "legacy-wake", InstanceID: instance.ID, ExternalEventID: "ev_legacy", EventType: "message",
		State: "received", PayloadDigest: "legacy", ReceivedAt: now, OccurredAt: now,
	}
	if _, _, err := database.ReceiveChannelInbox(ctx, wake); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_group_receipts
		(receipt_id,conversation_id,route_id,target_task_id,thread_id,batch_id,ready_ms)
		VALUES(?,?,?,?,'omt_legacy',?,?)`, wake.ID, conversation.ID, route.ID, route.TargetTaskID, wake.ID, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	database = reopenAfterMigration(t, database, path)
	instance, err = database.ChannelInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := instance.Config["feishu_group_message_allowlist"]; ok {
		t.Fatal("legacy allowlist still present after migration")
	}
	if got, err := database.FeishuGroupMessageAllowlist(ctx, instance); err != nil || !slices.Equal(got, []string{conversation.ExternalChatID, missingChat}) {
		t.Fatalf("plugin bootstrap lost a migrated group: %v, %v", got, err)
	}
	for _, chatID := range []string{conversation.ExternalChatID, missingChat} {
		if allowed, err := database.FeishuGroupMessageAllowed(ctx, instance, chatID, false); err != nil || !allowed {
			t.Fatalf("ingress denied migrated group %s: %v, %v", chatID, allowed, err)
		}
	}
	if got, err := database.ChannelListeningContext(ctx, route.TargetTaskID, conversation.ID, "", "", now); err != nil || got == "" {
		t.Fatalf("migration lost bound listening history: %q, %v", got, err)
	}
	if group, err := database.ChannelGroupReceipt(ctx, wake.ID); err != nil || group.RouteID != route.ID || group.TargetTaskID != route.TargetTaskID {
		t.Fatalf("migration lost queued topic routing: %+v, %v", group, err)
	}
	refresh := func(id string, groups ...domain.FeishuGroup) {
		t.Helper()
		if _, err := database.EnqueueChannelCommand(ctx, domain.ChannelPluginCommand{
			ID: id, InstanceID: instance.ID, Kind: FeishuGroupRefreshCommand, IdempotencyKey: id,
			State: "pending", CreatedAt: now, AvailableAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		leased, err := database.ClaimChannelCommands(ctx, instance.ID, 1, now, time.Minute)
		if err != nil || len(leased) != 1 {
			t.Fatalf("claim refresh: %v, %v", leased, err)
		}
		if err := database.CompleteFeishuGroupRefresh(ctx, instance.ID, id, leased[0].LeaseID, groups, now); err != nil {
			t.Fatal(err)
		}
	}
	knownGroup := domain.FeishuGroup{ChatID: conversation.ExternalChatID, DisplayName: "Known group"}
	refresh("refresh-one", knownGroup)
	listed, err := database.ChannelConversationSummaries(ctx, instance.OwnerID, instance)
	if err != nil || len(listed) != 1 || listed[0].Group == nil || !listed[0].Group.ReceiveAllMessages {
		t.Fatalf("first bot group list: %+v, %v", listed, err)
	}
	if allowed, err := database.FeishuGroupMessageAllowed(ctx, instance, missingChat, false); err != nil || !allowed {
		t.Fatalf("refresh disabled the missing migrated group: %v, %v", allowed, err)
	}
	refresh("refresh-two", knownGroup, domain.FeishuGroup{ChatID: missingChat, DisplayName: "Newly discovered"},
		domain.FeishuGroup{ChatID: "oc_new_default", DisplayName: "New default"})
	groups, _, err := database.feishuConversationGroups(ctx, instance.ID)
	migrated := groups[missingChat]
	if err != nil || !migrated.ReceiveAllMessages || migrated.DisplayName != "Newly discovered" || migrated.UpdatedAt == "" || migrated.UpdatedBy != "" ||
		groups["oc_new_default"].ReceiveAllMessages || groups["oc_new_default"].UpdatedAt != "" {
		t.Fatalf("refresh changed retention settings or invented an operator: %+v, %v", groups, err)
	}
	if _, _, err := database.SetFeishuGroupReception(ctx, instance.ID, "foreign-owner", missingChat, false, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("another owner can edit the group: %v", err)
	}
	updated, changed, err := database.SetFeishuGroupReception(ctx, instance.ID, instance.OwnerID, missingChat, false, now)
	if err != nil || !changed || updated.ReceiveAllMessages || updated.UpdatedBy != instance.OwnerID || updated.UpdatedAt != timeString(now) {
		t.Fatalf("toggle did not save the owner and time: %+v, changed=%v, %v", updated, changed, err)
	}
	repeated, changed, err := database.SetFeishuGroupReception(ctx, instance.ID, instance.OwnerID, missingChat, false, now.Add(time.Minute))
	if err != nil || changed || repeated.UpdatedAt != updated.UpdatedAt {
		t.Fatalf("idempotent toggle changed audit data: %+v, changed=%v, %v", repeated, changed, err)
	}

	database = reopenAfterMigration(t, database, path)
	t.Cleanup(func() { _ = database.Close() })
	if allowed, err := database.FeishuGroupMessageAllowed(ctx, instance, missingChat, false); err != nil || allowed {
		t.Fatalf("reopening replayed the legacy setting: %v, %v", allowed, err)
	}
}
