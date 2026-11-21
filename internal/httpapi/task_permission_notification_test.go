package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func permissionResultMessages(t *testing.T, f *mailAPIFixture, text string) int {
	t.Helper()
	page, err := f.database.ConversationPage(f.ctx, f.task.ID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range page.Items {
		if item.Kind == "user_message" && strings.Contains(item.Summary, text) {
			count += 1
		}
	}
	return count
}

// A Web approval without a channel context and a Web rejection both deliver one
// explicit result to the Task; repeating the same decision is a conflict and
// must not add a second message or Turn.
func TestWebPermissionDecisionsNotifyTaskOnce(t *testing.T) {
	f := newMailAPIFixture(t)
	request, err := f.api.app.CreateTaskPermissionRequest(f.ctx, app.TaskPermissionRequestInput{
		TaskID: f.task.ID, Action: "push main", Reason: "联调", Risk: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/tasks/" + f.task.ID + "/permission-requests/" + request.ID + "/resolve"
	approved := f.request(http.MethodPost, path, map[string]any{"decision": "approve"}, f.csrf)
	if approved["_status"].(float64) != http.StatusOK || approved["continuation_error"] != nil || approved["notification_error"] != nil {
		t.Fatalf("approve = %#v", approved)
	}
	if count := permissionResultMessages(t, f, "渠道权限申请「push main」"); count != 1 {
		t.Fatalf("approval result messages = %d, want 1", count)
	}
	if count := permissionResultMessages(t, f, "owner 已批准"); count != 1 {
		t.Fatalf("approval decision messages = %d, want 1", count)
	}
	if count := permissionResultMessages(t, f, "处理人："); count != 1 {
		t.Fatalf("approval result is missing the decider: %d", count)
	}
	repeated := f.request(http.MethodPost, path, map[string]any{"decision": "approve"}, f.csrf)
	if repeated["_status"].(float64) != http.StatusConflict {
		t.Fatalf("repeated approve = %#v", repeated)
	}
	if count := permissionResultMessages(t, f, "渠道权限申请「push main」"); count != 1 {
		t.Fatalf("repeated approve produced %d result messages, want 1", count)
	}

	rejected, err := f.api.app.CreateTaskPermissionRequest(f.ctx, app.TaskPermissionRequestInput{
		TaskID: f.task.ID, Action: "delete branch", Reason: "危险", Risk: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	rejectPath := "/api/v1/tasks/" + f.task.ID + "/permission-requests/" + rejected.ID + "/resolve"
	body := f.request(http.MethodPost, rejectPath, map[string]any{"decision": "reject", "reason": "不是这个"}, f.csrf)
	if body["_status"].(float64) != http.StatusOK || body["notification_error"] != nil {
		t.Fatalf("reject = %#v", body)
	}
	if count := permissionResultMessages(t, f, "渠道权限申请「delete branch」"); count != 1 {
		t.Fatalf("rejection result messages = %d, want 1", count)
	}
	if count := permissionResultMessages(t, f, "owner 已拒绝"); count != 1 {
		t.Fatalf("rejection decision messages = %d, want 1", count)
	}
	if count := permissionResultMessages(t, f, "原因：不是这个"); count != 1 {
		t.Fatalf("rejection reason messages = %d, want 1", count)
	}
	repeatedReject := f.request(http.MethodPost, rejectPath, map[string]any{"decision": "reject", "reason": "again"}, f.csrf)
	if repeatedReject["_status"].(float64) != http.StatusConflict {
		t.Fatalf("repeated reject = %#v", repeatedReject)
	}
	if count := permissionResultMessages(t, f, "渠道权限申请「delete branch」"); count != 1 {
		t.Fatalf("repeated reject produced %d result messages, want 1", count)
	}
}

func TestWebPermissionOnTerminalTaskRecordsWithoutWaking(t *testing.T) {
	f := newMailAPIFixture(t)
	task, err := f.database.Task(f.ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskCompleted {
		if err := f.database.UpdateTaskStatus(f.ctx, task.ID, task.Status, domain.TaskCompleted,
			time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	request, err := f.api.app.CreateTaskPermissionRequest(f.ctx, app.TaskPermissionRequestInput{
		TaskID: f.task.ID, Action: "push main", Reason: "联调", Risk: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/tasks/" + f.task.ID + "/permission-requests/" + request.ID + "/resolve"
	body := f.request(http.MethodPost, path, map[string]any{"decision": "approve"}, f.csrf)
	if body["_status"].(float64) != http.StatusOK || body["notification_error"] != nil {
		t.Fatalf("terminal approve = %#v", body)
	}
	if count := permissionResultMessages(t, f, "渠道权限申请「push main」"); count != 0 {
		t.Fatalf("terminal Task received a waking message: %d", count)
	}
	page, err := f.database.ConversationPage(f.ctx, f.task.ID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundHistory := false
	for _, item := range page.Items {
		if item.Kind == "agent_message" && strings.Contains(item.Summary, "owner 已批准") {
			foundHistory = true
		}
	}
	if !foundHistory {
		t.Fatal("terminal Task did not keep the approval result as history")
	}
	stored, err := f.database.Task(f.ctx, f.task.ID)
	if err != nil || stored.Status != domain.TaskCompleted {
		t.Fatalf("terminal Task status changed: %#v err=%v", stored.Status, err)
	}
}
