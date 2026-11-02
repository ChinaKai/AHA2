package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskPermissionRequestLifecycle(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	project := domain.Project{ID: "project-permission", Name: "Permission", ProjectType: "local", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-permission", ProjectID: project.ID, Name: "Permission",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Permission task", Request: "request",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := fixture.service.CreateTaskPermissionRequest(fixture.ctx, TaskPermissionRequestInput{
		TaskID: task.ID, ChannelInstanceID: "instance-1", ChannelConversationID: "conversation-1",
		ApplicantIdentityLinkID: "member-1", ApplicantName: "张三", ApplicantIsBot: true,
		Action: "push main", Scope: "origin/main", Reason: "联调需要", Risk: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != "pending" || request.ApplicantName != "张三" {
		t.Fatalf("request=%#v", request)
	}
	if _, err := fixture.service.CreateTaskPermissionRequest(fixture.ctx, TaskPermissionRequestInput{
		TaskID: task.ID, Action: "second action", Reason: "should be blocked",
	}); err == nil {
		t.Fatal("second pending permission request was accepted")
	}
	resolved, changed, err := fixture.service.ResolveTaskPermissionRequest(
		fixture.ctx, request.ID, "approve", "owner-1", "Owner", "同意",
	)
	if err != nil || !changed || resolved.Status != "approved" {
		t.Fatalf("resolved=%#v changed=%v err=%v", resolved, changed, err)
	}
	if _, changed, err := fixture.service.ResolveTaskPermissionRequest(
		fixture.ctx, request.ID, "reject", "owner-1", "Owner", "重复",
	); err != nil || changed {
		t.Fatalf("second resolve changed=%v err=%v", changed, err)
	}
}
