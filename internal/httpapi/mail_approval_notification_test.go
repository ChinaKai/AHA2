package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func mailResultMessages(t *testing.T, f *mailAPIFixture, kind, text string) int {
	t.Helper()
	page, err := f.database.ConversationPage(f.ctx, f.task.ID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range page.Items {
		if item.Kind == kind && strings.Contains(item.Summary, text) {
			count += 1
		}
	}
	return count
}

func createSubmittedMail(t *testing.T, f *mailAPIFixture, accountID, key, subject string) map[string]any {
	t.Helper()
	body := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": accountID, "to": []string{"qa@example.com"},
		"subject": subject, "body_text": "正文", "submit": true, "idempotency_key": key,
	})
	if body["_status"].(float64) != http.StatusCreated {
		t.Fatalf("create mail = %#v", body)
	}
	return body["message"].(map[string]any)
}

// Approve, reject and cancel each deliver one approval result to the Task that
// created the mail message. Repeating an idempotent approval must not duplicate
// the result.
func TestMailApprovalDecisionsNotifyTaskOnce(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("notify-code")

	approved := createSubmittedMail(t, f, account["id"].(string), "notify-approve", "构建完成")
	digest := approved["approval_digest"].(string)
	body := f.approve(approved["id"].(string), digest)
	if body["_status"].(float64) != http.StatusOK {
		t.Fatalf("approve = %#v", body)
	}
	if count := mailResultMessages(t, f, "user_message", "邮件「构建完成」"); count != 1 {
		t.Fatalf("mail approval result = %d, want 1", count)
	}
	if count := mailResultMessages(t, f, "user_message", "owner 已批准"); count != 1 {
		t.Fatalf("mail approval decision = %d, want 1", count)
	}
	repeated := f.approve(approved["id"].(string), digest)
	if repeated["_status"].(float64) != http.StatusOK {
		t.Fatalf("repeated approve = %#v", repeated)
	}
	if count := mailResultMessages(t, f, "user_message", "邮件「构建完成」"); count != 1 {
		t.Fatalf("repeated approve produced %d results, want 1", count)
	}

	rejected := createSubmittedMail(t, f, account["id"].(string), "notify-reject", "危险邮件")
	body = f.request(http.MethodPost, "/api/v1/mail-messages/"+rejected["id"].(string)+"/reject",
		map[string]any{"reason": "收件人不对"}, f.csrf)
	if body["_status"].(float64) != http.StatusOK {
		t.Fatalf("reject = %#v", body)
	}
	if count := mailResultMessages(t, f, "user_message", "邮件「危险邮件」"); count != 1 {
		t.Fatalf("mail rejection result = %d, want 1", count)
	}
	if count := mailResultMessages(t, f, "user_message", "owner 已拒绝"); count != 1 {
		t.Fatalf("mail rejection decision = %d, want 1", count)
	}
	if count := mailResultMessages(t, f, "user_message", "原因：收件人不对"); count != 1 {
		t.Fatalf("mail rejection reason = %d, want 1", count)
	}

	cancelled := createSubmittedMail(t, f, account["id"].(string), "notify-cancel", "先别发")
	body = f.request(http.MethodPost, "/api/v1/mail-messages/"+cancelled["id"].(string)+"/cancel", nil, f.csrf)
	if body["_status"].(float64) != http.StatusOK {
		t.Fatalf("cancel = %#v", body)
	}
	if count := mailResultMessages(t, f, "user_message", "邮件「先别发」"); count != 1 {
		t.Fatalf("mail cancellation result = %d, want 1", count)
	}
	if count := mailResultMessages(t, f, "user_message", "owner 已取消"); count != 1 {
		t.Fatalf("mail cancellation decision = %d, want 1", count)
	}
}

func TestMailApprovalOnTerminalTaskRecordsWithoutWaking(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("terminal-code")
	message := createSubmittedMail(t, f, account["id"].(string), "terminal-approve", "终态邮件")
	task, err := f.database.Task(f.ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskCompleted {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err := f.database.UpdateTaskStatus(f.ctx, task.ID, task.Status, domain.TaskCompleted, now, now); err != nil {
			t.Fatal(err)
		}
	}
	body := f.approve(message["id"].(string), message["approval_digest"].(string))
	if body["_status"].(float64) != http.StatusOK {
		t.Fatalf("terminal approve = %#v", body)
	}
	if count := mailResultMessages(t, f, "user_message", "邮件「终态邮件」"); count != 0 {
		t.Fatalf("terminal Task received a waking message: %d", count)
	}
	if count := mailResultMessages(t, f, "agent_message", "owner 已批准"); count != 1 {
		t.Fatalf("terminal Task approval history = %d, want 1", count)
	}
	stored, err := f.database.Task(f.ctx, f.task.ID)
	if err != nil || stored.Status != domain.TaskCompleted {
		t.Fatalf("terminal Task status changed: %#v err=%v", stored.Status, err)
	}
}
