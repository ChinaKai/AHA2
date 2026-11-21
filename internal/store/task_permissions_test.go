package store

import (
	"context"
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
		Status: "pending", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
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

// Listing expires stale pending rows as a side effect. The read path now
// probes before writing, so this pins that the expiry still happens when there
// is something to expire.
func TestListTaskPermissionRequestsExpiresStalePending(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	request := domain.TaskPermissionRequest{
		ID: "task_permission_stale", TaskID: task.ID, Action: "push", Scope: "origin/main",
		Reason: "stale", Risk: "low", Status: "pending", CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}
	if err := database.CreateTaskPermissionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	listed, err := database.ListTaskPermissionRequests(ctx, task.ID, 10)
	if err != nil || len(listed) != 1 || listed[0].Status != "expired" {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
}

// ExpireTaskPermissionRequests returns only the rows it actually changed, so
// concurrent readers cannot each notify the Task about the same expiry.
func TestExpireTaskPermissionRequestsIsOneShot(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	for _, item := range []domain.TaskPermissionRequest{
		{ID: "task_permission_due", TaskID: task.ID, Action: "push", Status: "pending", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)},
		{ID: "task_permission_future", TaskID: task.ID, Action: "later", Status: "pending", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := database.CreateTaskPermissionRequest(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	expired, err := database.ExpireTaskPermissionRequests(ctx, task.ID, now)
	if err != nil || len(expired) != 1 || expired[0].ID != "task_permission_due" || expired[0].Status != "expired" {
		t.Fatalf("first expiry = %#v err=%v", expired, err)
	}
	again, err := database.ExpireTaskPermissionRequests(ctx, task.ID, now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second expiry = %#v err=%v", again, err)
	}
	future, err := database.TaskPermissionRequest(ctx, "task_permission_future")
	if err != nil || future.Status != "pending" {
		t.Fatalf("future request = %#v err=%v", future, err)
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
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
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
