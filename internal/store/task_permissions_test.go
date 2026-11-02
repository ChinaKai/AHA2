package store

import (
	"context"
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
