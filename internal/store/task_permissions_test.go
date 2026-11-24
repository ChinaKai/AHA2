package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskPermissionRequestResolvesOnce(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	request := domain.TaskPermissionRequest{
		ID: "task_permission_test", TaskID: task.ID, TurnID: "turn-1",
		ChannelInstanceID: "instance-1", ChannelConversationID: "conversation-1",
		ApplicantIdentityLinkID: "member-1", ApplicantName: "张三", ApplicantIsBot: true,
		Action: "push main", Scope: "origin/main", Reason: "联调需要", Risk: "high",
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateTaskPermissionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	listed, err := database.ListTaskPermissionRequests(ctx, task.ID, 10)
	if err != nil || len(listed) != 1 || !listed[0].ApplicantIsBot || listed[0].Status != "pending" {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}

	resolved, changed, err := database.ResolveTaskPermissionRequest(
		ctx, request.ID, "approved", "owner-1", "Owner", "同意", now.Add(time.Minute),
	)
	if err != nil || !changed || resolved.Status != "approved" || resolved.DeciderName != "Owner" {
		t.Fatalf("resolved=%#v changed=%v err=%v", resolved, changed, err)
	}
	again, changed, err := database.ResolveTaskPermissionRequest(
		ctx, request.ID, "rejected", "owner-1", "Owner", "重复", now.Add(2*time.Minute),
	)
	if err != nil || changed || again.Status != "approved" {
		t.Fatalf("second resolve=%#v changed=%v err=%v", again, changed, err)
	}
}

func TestLegacyTaskPermissionRequestAndCardSurviveUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	task := createStoreTestTask(t, database)
	channel := createChannelHistoryFixture(t, database, task.ID, task.ID)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	identity, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{
		ID: "owner-identity", InstanceID: channel.instance.ID, OwnerID: channel.instance.OwnerID,
		ExternalUserID: "owner-open", Role: "owner", Status: "active", LinkedAt: past,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "expired"} {
		requestID, cardID := "permission-"+status, "card-"+status
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO task_permission_requests(id,task_id,channel_instance_id,channel_conversation_id,channel_action_id,action,status,created_at,updated_at,expires_at)
			VALUES(?,?,?,?,?,'push',?,?,?,?)`,
			requestID, task.ID, channel.instance.ID, channel.conversation.ID, cardID, status,
			timeString(past), timeString(past), timeString(past)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{cardID, cardID + "-unlinked"} {
			if _, err := database.CreateChannelPendingAction(ctx, domain.ChannelPendingAction{
				ID: id, InstanceID: channel.instance.ID, ConversationID: channel.conversation.ID,
				ActorIdentityLinkID: identity.ID, Operation: "task_permission", TargetType: "task_permission",
				TargetID: requestID, Status: "superseded", ProviderMessageID: id + "-message",
				CreatedAt: past, UpdatedAt: past, ExpiresAt: past,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=162`); err != nil {
		t.Fatal(err)
	}
	reopened := reopenAfterMigration(t, database, path)
	defer reopened.Close()

	listed, err := reopened.ListTaskPermissionRequests(ctx, task.ID, 10)
	if err != nil || len(listed) != 2 || listed[0].Status != "pending" || listed[1].Status != "expired" {
		t.Fatalf("legacy requests = %#v, err = %v", listed, err)
	}
	if err := reopened.CreateTaskPermissionRequest(ctx, domain.TaskPermissionRequest{
		ID: "duplicate", TaskID: task.ID, Action: "push", Status: "pending", CreatedAt: now, UpdatedAt: now,
	}); !errors.Is(err, ErrTaskPermissionPending) {
		t.Fatalf("duplicate of legacy pending request: %v", err)
	}
	for _, id := range []string{"card-expired", "card-pending-unlinked"} {
		card, err := reopened.ChannelPendingAction(ctx, id)
		if err != nil || card.Status != "superseded" {
			t.Fatalf("unavailable legacy card = %#v, err = %v", card, err)
		}
	}
	card, claimed, err := reopened.BeginChannelPendingAction(ctx,
		"card-pending", channel.instance.ID, channel.conversation.ID, identity.ID, "card-pending-message", now)
	if err != nil || !claimed || card.Status != "executing" || !card.ExpiresAt.Equal(past) {
		t.Fatalf("claim legacy card = %#v, claimed = %v, err = %v", card, claimed, err)
	}
	resolved, changed, err := reopened.ResolveTaskPermissionRequest(ctx, "permission-pending", "approved", identity.ID, "Owner", "", now)
	if err != nil || !changed || resolved.Status != "approved" || !resolved.ExpiresAt.Equal(past) {
		t.Fatalf("resolve legacy request = %#v, changed = %v, err = %v", resolved, changed, err)
	}
	history, changed, err := reopened.ResolveTaskPermissionRequest(ctx, "permission-expired", "approved", identity.ID, "Owner", "", now)
	if err != nil || changed || history.Status != "expired" {
		t.Fatalf("resolve expired history = %#v, changed = %v, err = %v", history, changed, err)
	}
}

func TestCreateTaskPermissionRequestRejectsConcurrentDuplicate(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"request-one", "request-two"} {
		go func() {
			<-start
			results <- database.CreateTaskPermissionRequest(ctx, domain.TaskPermissionRequest{
				ID: id, TaskID: task.ID, Action: "push", Status: "pending", CreatedAt: now, UpdatedAt: now,
			})
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first != nil {
		first, second = second, first
	}
	if first != nil || !errors.Is(second, ErrTaskPermissionPending) {
		t.Fatalf("concurrent inserts: first = %v, second = %v", first, second)
	}
}

// Two Owner decisions racing on the same request: exactly one update wins.
func TestResolveTaskPermissionRequestIsAtomic(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	request := domain.TaskPermissionRequest{
		ID: "task_permission_race", TaskID: task.ID, Action: "push", Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateTaskPermissionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	changed := make(chan bool, 2)
	errs := make(chan error, 2)
	for _, decision := range []string{"approved", "rejected"} {
		decision := decision
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, resolveErr := database.ResolveTaskPermissionRequest(
				ctx, request.ID, decision, "owner-1", "Owner", decision, now.Add(time.Minute))
			changed <- ok
			if resolveErr != nil {
				errs <- resolveErr
			}
		}()
	}
	wg.Wait()
	close(changed)
	close(errs)
	wins := 0
	for ok := range changed {
		if ok {
			wins += 1
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent resolves changed the row %d times, want 1", wins)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent resolve error: %v", err)
		}
	}
	stored, err := database.TaskPermissionRequest(ctx, request.ID)
	if err != nil || (stored.Status != "approved" && stored.Status != "rejected") {
		t.Fatalf("stored request = %#v err=%v", stored, err)
	}
}
