package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func createApprovalResultTask(t *testing.T, fixture *petTaskFixture) domain.Task {
	t.Helper()
	now := fixture.service.now().UTC()
	project := domain.Project{ID: "project-approval-result", Name: "Approval result", ProjectType: "local", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-approval-result", ProjectID: project.ID, Name: "Approval result",
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
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Approval result", Request: "request",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	return task
}

func conversationHasText(t *testing.T, fixture *petTaskFixture, taskID, text string) bool {
	t.Helper()
	page, err := fixture.database.ConversationPage(fixture.ctx, taskID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if strings.Contains(item.Summary, text) {
			return true
		}
	}
	return false
}

func conversationHasKindText(t *testing.T, fixture *petTaskFixture, taskID, kind, text string) bool {
	t.Helper()
	page, err := fixture.database.ConversationPage(fixture.ctx, taskID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Kind == kind && strings.Contains(item.Summary, text) {
			return true
		}
	}
	return false
}

func TestTaskApprovalResultContentNamesTypeDecisionOwnerAndReason(t *testing.T) {
	at := time.Date(2026, 11, 21, 14, 35, 0, 0, time.UTC)
	content := TaskApprovalResultContent(TaskApprovalResultInput{
		TaskID: "task-1", Kind: TaskApprovalKindChannelPermission,
		Subject: "push main", ReferenceID: "task_permission_1",
		Decision: "rejected", Decider: "owner", Reason: "风险太高", At: at,
	})
	for _, want := range []string{"[审批结果]", "渠道权限申请", "push main", "task_permission_1", "owner 已拒绝", "处理人：owner", "2026-11-21", "原因：风险太高"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content %q missing %q", content, want)
		}
	}
	mail := TaskApprovalResultContent(TaskApprovalResultInput{
		Kind: TaskApprovalKindMail, Subject: "构建完成", ReferenceID: "mail-1",
		Decision: "approved", Decider: "owner", At: at,
	})
	if !strings.Contains(mail, "邮件「构建完成」") || !strings.Contains(mail, "owner 已批准") {
		t.Fatalf("mail content = %q", mail)
	}
}

func TestNotifyTaskApprovalResultWakesActiveTaskOnce(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := createApprovalResultTask(t, fixture)
	at := fixture.service.now().UTC()
	if err := fixture.service.NotifyTaskApprovalResult(fixture.ctx, TaskApprovalResultInput{
		TaskID: task.ID, Kind: TaskApprovalKindMail, Subject: "构建完成",
		ReferenceID: "mail-1", Decision: "rejected", Decider: "owner", Reason: "正文不对", At: at,
	}); err != nil {
		t.Fatal(err)
	}
	if !conversationHasText(t, fixture, task.ID, "原因：正文不对") || !conversationHasText(t, fixture, task.ID, "邮件「构建完成」") {
		t.Fatal("the result message did not reach the task conversation")
	}
	// An active Task receives the result through the normal message pipeline.
	if !conversationHasKindText(t, fixture, task.ID, "user_message", "owner 已拒绝") {
		t.Fatal("the active task got the result as history instead of a message turn")
	}
}

func TestExpiredPermissionRequestNotifiesTaskOnce(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := createApprovalResultTask(t, fixture)
	base := fixture.service.now().UTC()
	request, err := fixture.service.CreateTaskPermissionRequest(fixture.ctx, TaskPermissionRequestInput{
		TaskID: task.ID, Action: "push main", Reason: "联调需要", Risk: "medium", ExpiresInSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.now = func() time.Time { return base.Add(16 * time.Minute) }
	t.Cleanup(func() { fixture.service.now = time.Now })
	if _, err := fixture.service.TaskPermissionRequests(fixture.ctx, task.ID, 50); err != nil {
		t.Fatal(err)
	}
	if !conversationHasKindText(t, fixture, task.ID, "user_message", "owner 已过期") {
		t.Fatal("expired permission request did not notify the task")
	}
	if !conversationHasKindText(t, fixture, task.ID, "user_message", request.ID) {
		t.Fatal("expiry result did not include the request id")
	}
	// Reading the list again must not produce a second expiry message.
	before := countConversationText(t, fixture, task.ID, "owner 已过期")
	if _, err := fixture.service.TaskPermissionRequests(fixture.ctx, task.ID, 50); err != nil {
		t.Fatal(err)
	}
	if after := countConversationText(t, fixture, task.ID, "owner 已过期"); after != before || after != 1 {
		t.Fatalf("expiry notification count = %d -> %d, want 1", before, after)
	}
}

func countConversationText(t *testing.T, fixture *petTaskFixture, taskID, text string) int {
	t.Helper()
	page, err := fixture.database.ConversationPage(fixture.ctx, taskID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range page.Items {
		if strings.Contains(item.Summary, text) {
			count += 1
		}
	}
	return count
}

func TestNotifyTaskApprovalResultRecordsTerminalTaskWithoutWaking(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := createApprovalResultTask(t, fixture)
	if err := fixture.service.CompleteTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.NotifyTaskApprovalResult(fixture.ctx, TaskApprovalResultInput{
		TaskID: task.ID, Kind: TaskApprovalKindChannelPermission, Subject: "push main",
		ReferenceID: "task_permission_1", Decision: "expired", Decider: "owner",
		At: fixture.service.now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Task(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TaskCompleted {
		t.Fatalf("task status = %s, want completed", stored.Status)
	}
	if !conversationHasKindText(t, fixture, task.ID, "agent_message", "owner 已过期") {
		t.Fatal("terminal task did not keep the result as history")
	}
	if conversationHasKindText(t, fixture, task.ID, "user_message", "owner 已过期") {
		t.Fatal("terminal task received a waking message instead of a history record")
	}
}
