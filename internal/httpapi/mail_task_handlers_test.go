package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskMailOptionsAreOwnerScopedAndCredentialFree(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("task-mail-options-secret")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "Task Mail Options",
		"subject_template": "{{task.title}}", "body_text_template": "hello",
	})
	binding := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
		"default_template_id": template["id"], "approval_mode": "always", "enabled": true,
	}, f.csrf)
	if binding["_status"].(float64) != http.StatusCreated {
		t.Fatalf("binding = %#v", binding)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	draftAttachment := domain.NewID("attachment")
	f.execRaw(`INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, draftAttachment, f.task.ID, "", "report.zip",
		"application/zip", 4096, strings.Repeat("d", 64), now)
	if _, err := f.database.CreateBuildArtifact(f.ctx, domain.BuildArtifact{
		ID: "build_artifact_task_mail", TaskID: f.task.ID, Label: "firmware",
		PackageName: "firmware.zip", Version: "1.2.3", SizeBytes: 4096,
		SHA256: strings.Repeat("d", 64), StorageType: domain.BuildArtifactStorageAHAAttachment,
		AttachmentID: draftAttachment,
	}); err != nil {
		t.Fatal(err)
	}
	message := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "template_id": template["id"],
		"to": []string{"qa@example.com"}, "submit": true, "idempotency_key": "task-mail-options-1",
	})
	if message["_status"].(float64) != http.StatusCreated {
		t.Fatalf("message = %#v", message)
	}
	messageID := message["message"].(map[string]any)["id"].(string)

	body := f.request(http.MethodGet, "/api/v1/tasks/"+f.task.ID+"/mail", nil, "")
	if body["_status"].(float64) != http.StatusOK {
		t.Fatalf("task mail options = %#v", body)
	}
	accounts := body["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("accounts = %#v", accounts)
	}
	accountItem := accounts[0].(map[string]any)
	if accountItem["account_id"] != account["id"] || accountItem["binding_scope"] != "task" ||
		accountItem["default_template_id"] != template["id"] || accountItem["credential_configured"] != true {
		t.Fatalf("account projection = %#v", accountItem)
	}
	for _, forbidden := range []string{"credential_ref", "smtp_username", "smtp_host"} {
		if _, ok := accountItem[forbidden]; ok {
			t.Fatalf("task mail options exposed %q: %#v", forbidden, accountItem)
		}
	}
	attachments := body["attachments"].([]any)
	if len(attachments) != 1 || attachments[0].(map[string]any)["id"] != draftAttachment {
		t.Fatalf("attachments = %#v", attachments)
	}
	artifacts := body["build_artifacts"].([]any)
	if len(artifacts) != 1 || artifacts[0].(map[string]any)["id"] != "build_artifact_task_mail" ||
		artifacts[0].(map[string]any)["external_url"] != nil {
		t.Fatalf("build artifacts = %#v", artifacts)
	}
	messages := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	card := messages[0].(map[string]any)
	if card["message"].(map[string]any)["id"] != messageID {
		t.Fatalf("card message = %#v", card)
	}
	if _, ok := card["deliveries"]; !ok {
		t.Fatalf("card missing deliveries: %#v", card)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "task-mail-options-secret") ||
		strings.Contains(string(raw), "mail-account/") ||
		strings.Contains(string(raw), "credential_ref") {
		t.Fatalf("task mail options leaked credentials: %s", raw)
	}

	anonymous := &http.Client{}
	response := requestJSON(t, anonymous, http.MethodGet, f.server.URL+"/api/v1/tasks/"+f.task.ID+"/mail", nil, "")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous task mail options status = %d", response.StatusCode)
	}
}

func TestConversationCategoryMailIsVisible(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("conversation-mail-secret")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "Conversation Mail",
		"subject_template": "{{task.title}}", "body_text_template": "hello",
	})
	f.bindTaskMail(account["id"].(string), template["id"].(string))
	message := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "template_id": template["id"],
		"to": []string{"qa@example.com"}, "submit": true, "idempotency_key": "conversation-mail-1",
	})
	if message["_status"].(float64) != http.StatusCreated {
		t.Fatalf("message = %#v", message)
	}

	withMail := f.request(http.MethodGet,
		"/api/v1/tasks/"+f.task.ID+"/agents/main/conversation?categories=chat,mail&limit=20", nil, "")
	if withMail["_status"].(float64) != http.StatusOK {
		t.Fatalf("conversation with mail = %#v", withMail)
	}
	items := withMail["conversation"].(map[string]any)["items"].([]any)
	found := false
	for _, item := range items {
		if item.(map[string]any)["category"] == "mail" && item.(map[string]any)["kind"] == "mail_message" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mail card missing from conversation: %#v", items)
	}

	withoutMail := f.request(http.MethodGet,
		"/api/v1/tasks/"+f.task.ID+"/agents/main/conversation?categories=chat&limit=20", nil, "")
	items = withoutMail["conversation"].(map[string]any)["items"].([]any)
	for _, item := range items {
		if item.(map[string]any)["category"] == "mail" {
			t.Fatalf("mail card leaked into chat-only filter: %#v", item)
		}
	}
}

func (f *mailAPIFixture) bindTaskMail(accountID, templateID string) {
	f.t.Helper()
	body := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": accountID,
		"default_template_id": templateID, "approval_mode": "always", "enabled": true,
	}, f.csrf)
	if body["_status"].(float64) != http.StatusCreated {
		f.t.Fatalf("bind task mail = %#v", body)
	}
}
