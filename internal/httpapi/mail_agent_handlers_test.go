package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
)

type agentMailFixture struct {
	*mailAPIFixture
	caps  *agentapi.Capabilities
	token string
	turn  domain.Turn
}

func newAgentMailFixture(t *testing.T) *agentMailFixture {
	t.Helper()
	caps := agentapi.NewCapabilities()
	f := newMailAPIFixtureWith(t, func(config *Config) {
		config.AgentCapabilities = caps
	})
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: f.task.ID, Role: "user", Sender: "owner",
		Content: "send a mail", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: f.task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: f.task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: f.task.RuntimeConfigSnapshotID,
		InboxBatchID:            "batch-agent-mail", Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := f.database.CreateMessageRoundAndTurn(f.ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	token, err := caps.Issue(f.task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &agentMailFixture{mailAPIFixture: f, caps: caps, token: token, turn: turn}
}

func (f *agentMailFixture) agent(method, path string, payload any) (int, map[string]any) {
	f.t.Helper()
	response := agentRequest(f.t, f.server.URL+path, method, f.token, payload)
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		f.t.Fatalf("%s %s: decode: %v", method, path, err)
	}
	return response.StatusCode, body
}

func (f *agentMailFixture) bind(accountID, templateID string) map[string]any {
	f.t.Helper()
	body := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": accountID,
		"default_template_id": templateID, "approval_mode": "always", "enabled": true,
	}, f.csrf)
	if body["_status"].(float64) != http.StatusCreated {
		f.t.Fatalf("create binding = %#v", body)
	}
	return body
}

func (f *agentMailFixture) createScopedTemplate() (map[string]any, map[string]any) {
	f.t.Helper()
	account := f.createAccount("agent-secret-authorization-code")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "Agent 提测",
		"subject_template":   "[AHA] {{task.title}} {{test.scope}}",
		"body_text_template": "测试范围：{{test.scope}}\n备注：{{test.notes}}",
		"required_variables": []string{"test.scope"},
	})
	f.bind(account["id"].(string), template["id"].(string))
	return account, template
}

func TestAgentMailListsSafeScopedMetadata(t *testing.T) {
	f := newAgentMailFixture(t)
	account, template := f.createScopedTemplate()

	status, body := f.agent(http.MethodGet, "/api/v1/agent/mail/accounts", nil)
	if status != http.StatusOK {
		t.Fatalf("accounts status=%d body=%#v", status, body)
	}
	accounts := body["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("accounts = %#v", accounts)
	}
	item := accounts[0].(map[string]any)
	if item["account_id"] != account["id"] || item["from_address"] != "noreply@example.com" ||
		item["credential_configured"] != true || item["default_template_id"] != template["id"] {
		t.Fatalf("account metadata = %#v", item)
	}
	for _, forbidden := range []string{"credential_ref", "smtp_username", "smtp_host", "smtp_port", "name"} {
		if _, ok := item[forbidden]; ok {
			t.Fatalf("account metadata exposed %q: %#v", forbidden, item)
		}
	}
	encoded, _ := json.Marshal(body)
	if strings.Contains(string(encoded), "agent-secret-authorization-code") ||
		strings.Contains(string(encoded), "mail-account/") {
		t.Fatalf("credential leaked through accounts: %s", encoded)
	}

	status, body = f.agent(http.MethodGet, "/api/v1/agent/mail/templates", nil)
	if status != http.StatusOK {
		t.Fatalf("templates status=%d body=%#v", status, body)
	}
	templates := body["templates"].([]any)
	if len(templates) != 1 {
		t.Fatalf("templates = %#v", templates)
	}
	templateItem := templates[0].(map[string]any)
	if templateItem["template_id"] != template["id"] || templateItem["version"] != float64(1) {
		t.Fatalf("template metadata = %#v", templateItem)
	}
	for _, forbidden := range []string{"subject_template", "body_text_template", "body_html_template"} {
		if _, ok := templateItem[forbidden]; ok {
			t.Fatalf("template metadata exposed %q: %#v", forbidden, templateItem)
		}
	}
}

func TestAgentMailPreviewCreatesNoMessage(t *testing.T) {
	f := newAgentMailFixture(t)
	account, template := f.createScopedTemplate()

	status, body := f.agent(http.MethodPost, "/api/v1/agent/mail/preview", map[string]any{
		"account_id": account["id"], "template_id": template["id"],
		"to":        []string{"qa@example.com"},
		"variables": map[string]string{"test.scope": "整机回归", "test.notes": "验证回滚"},
	})
	if status != http.StatusOK {
		t.Fatalf("preview status=%d body=%#v", status, body)
	}
	preview := body["preview"].(map[string]any)
	if !strings.Contains(preview["subject"].(string), "整机回归") || preview["approval_required"] != true {
		t.Fatalf("preview = %#v", preview)
	}
	messages, err := f.database.MailMessagesByTask(f.ctx, f.task.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("preview created %d message(s)", len(messages))
	}
}

func TestAgentMailDirectWithoutTemplate(t *testing.T) {
	f := newAgentMailFixture(t)
	account, _ := f.createScopedTemplate()

	status, body := f.agent(http.MethodPost, "/api/v1/agent/mail/preview", map[string]any{
		"account_id": account["id"], "subject": "直接预览",
		"body_html": "<p>直接 HTML 正文</p>", "to": []string{"qa@example.com"},
	})
	if status != http.StatusOK {
		t.Fatalf("direct preview status=%d body=%#v", status, body)
	}
	preview := body["preview"].(map[string]any)
	if preview["subject"] != "直接预览" || !strings.Contains(preview["body_html"].(string), "直接 HTML 正文") ||
		!strings.Contains(preview["body_text"].(string), "直接 HTML 正文") {
		t.Fatalf("direct preview = %#v", preview)
	}

	for name, payload := range map[string]map[string]any{
		"missing subject": {
			"account_id": account["id"], "body_text": "x", "to": []string{"qa@example.com"},
		},
		"missing body": {
			"account_id": account["id"], "subject": "x", "to": []string{"qa@example.com"},
		},
		"empty body": {
			"account_id": account["id"], "subject": "x", "body_text": "", "body_html": "",
			"to": []string{"qa@example.com"},
		},
		"unsafe html": {
			"account_id": account["id"], "subject": "x", "body_html": `<p onclick="alert(1)">x</p>`,
			"to": []string{"qa@example.com"},
		},
	} {
		status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/preview", payload)
		if status != http.StatusUnprocessableEntity || mailErrorCode(t, body) != string(domain.MailErrTemplateInvalid) {
			t.Fatalf("%s status=%d body=%#v", name, status, body)
		}
	}

	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", map[string]any{
		"account_id": account["id"], "subject": "直接发送",
		"body_text": "直接正文", "to": []string{"qa@example.com"},
		"idempotency_key": "agent-direct-1",
	})
	if status != http.StatusCreated || body["status"] != string(domain.MailMessagePendingApproval) ||
		body["approval_required"] != true {
		t.Fatalf("direct send status=%d body=%#v", status, body)
	}
	messages, err := f.database.MailMessagesByTask(f.ctx, f.task.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("direct messages = %#v", messages)
	}
	stored := messages[0]
	if stored.TemplateID != "" || stored.TemplateVersion != 0 ||
		stored.Subject != "直接发送" || stored.BodyText != "直接正文" {
		t.Fatalf("direct stored = %#v", stored)
	}
}

func TestAgentMailSendIsAlwaysPendingAndIdempotent(t *testing.T) {
	f := newAgentMailFixture(t)
	account, template := f.createScopedTemplate()

	payload := map[string]any{
		"account_id": account["id"], "template_id": template["id"],
		"to": []string{"qa@example.com"}, "cc": []string{"release@example.com"},
		"variables":       map[string]string{"test.scope": "整机回归", "test.notes": "验证回滚"},
		"idempotency_key": "task-mail-agent-1",
	}
	status, body := f.agent(http.MethodPost, "/api/v1/agent/mail/messages", payload)
	if status != http.StatusCreated {
		t.Fatalf("send status=%d body=%#v", status, body)
	}
	messageID, _ := body["message_id"].(string)
	if messageID == "" || body["status"] != string(domain.MailMessagePendingApproval) ||
		body["approval_required"] != true || body["deduplicated"] != false {
		t.Fatalf("send response = %#v", body)
	}
	messages, err := f.database.MailMessagesByTask(f.ctx, f.task.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Source != domain.MailMessageSourceAgent ||
		messages[0].TurnID != f.turn.ID || messages[0].Status != domain.MailMessagePendingApproval {
		t.Fatalf("stored message = %#v", messages)
	}

	// Same idempotency key returns the original message, not a second one.
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", payload)
	if status != http.StatusOK || body["deduplicated"] != true || body["message_id"] != messageID {
		t.Fatalf("deduplicated send status=%d body=%#v", status, body)
	}
	messages, err = f.database.MailMessagesByTask(f.ctx, f.task.ID, "", 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("dedup created another message: %d err=%v", len(messages), err)
	}

	status, body = f.agent(http.MethodGet, "/api/v1/agent/mail/messages/"+messageID, nil)
	if status != http.StatusOK || body["status"] != string(domain.MailMessagePendingApproval) ||
		body["approval_required"] != true {
		t.Fatalf("status response status=%d body=%#v", status, body)
	}
	statusJSON, _ := json.Marshal(body)
	if strings.Contains(strings.ToLower(string(statusJSON)), "smtp") ||
		strings.Contains(string(statusJSON), "credential") ||
		strings.Contains(string(statusJSON), "agent-secret-authorization-code") {
		t.Fatalf("status leaked SMTP/credential detail: %s", statusJSON)
	}

	// There is no Agent approval route at all.
	response := agentRequest(t, f.server.URL+"/api/v1/agent/mail/messages/"+messageID+"/approve",
		http.MethodPost, f.token, map[string]any{"expected_digest": "x"})
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("agent approve route status = %d", response.StatusCode)
	}
	stored, err := f.database.MailMessage(f.ctx, messageID)
	if err != nil || stored.Status != domain.MailMessagePendingApproval {
		t.Fatalf("agent approval changed status: %#v err=%v", stored, err)
	}
}

func TestAgentMailCrossTaskAndProjectAreRejected(t *testing.T) {
	f := newAgentMailFixture(t)
	account, template := f.createScopedTemplate()

	// An account that exists but is bound to a different Task must be refused.
	otherTask := createHardwareAPITask(t, f.database)
	otherAccount := f.createAccountWithAddress("other-task-code", "other@example.com")
	otherBinding := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": otherTask.ID, "account_id": otherAccount["id"],
		"approval_mode": "always", "enabled": true,
	}, f.csrf)
	if otherBinding["_status"].(float64) != http.StatusCreated {
		t.Fatalf("other binding = %#v", otherBinding)
	}
	status, body := f.agent(http.MethodPost, "/api/v1/agent/mail/messages", map[string]any{
		"account_id": otherAccount["id"], "template_id": template["id"],
		"to": []string{"qa@example.com"}, "idempotency_key": "cross-task-account",
	})
	if status != http.StatusForbidden || mailErrorCode(t, body) != string(domain.MailErrAccountNotBound) {
		t.Fatalf("cross-task account status=%d body=%#v", status, body)
	}

	// A template in another Project is not visible to this Task.
	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", CreatedAt: now, UpdatedAt: now}
	if err := f.database.CreateProject(f.ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	foreignTemplate := f.request(http.MethodPost, "/api/v1/mail-templates", map[string]any{
		"scope": "project", "project_id": otherProject.ID, "name": "Foreign",
		"subject_template": "Foreign", "body_text_template": "Foreign body",
	}, f.csrf)
	if foreignTemplate["_status"].(float64) != http.StatusCreated {
		t.Fatalf("foreign template = %#v", foreignTemplate)
	}
	foreignID := foreignTemplate["template"].(map[string]any)["id"].(string)
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/preview", map[string]any{
		"account_id": account["id"], "template_id": foreignID, "to": []string{"qa@example.com"},
	})
	if status != http.StatusNotFound || mailErrorCode(t, body) != string(domain.MailErrTemplateNotFound) {
		t.Fatalf("cross-project template status=%d body=%#v", status, body)
	}

	// A message created for another Task is not readable through this Task's
	// Agent capability.
	otherMessage := f.createMessage(map[string]any{
		"task_id": otherTask.ID, "account_id": otherAccount["id"], "to": []string{"qa@example.com"},
		"subject": "other task", "body_text": "other", "submit": true,
		"idempotency_key": "other-task-message",
	})
	if otherMessage["_status"].(float64) != http.StatusCreated {
		t.Fatalf("other message = %#v", otherMessage)
	}
	otherID := otherMessage["message"].(map[string]any)["id"].(string)
	status, body = f.agent(http.MethodGet, "/api/v1/agent/mail/messages/"+otherID, nil)
	if status != http.StatusForbidden || mailErrorCode(t, body) != string(domain.MailErrForbidden) {
		t.Fatalf("cross-task status=%d body=%#v", status, body)
	}
}

func TestAgentMailRejectsBCCAndUnsafeAttachments(t *testing.T) {
	f := newAgentMailFixture(t)
	account, template := f.createScopedTemplate()
	base := map[string]any{
		"account_id": account["id"], "template_id": template["id"],
		"to":        []string{"qa@example.com"},
		"variables": map[string]string{"test.scope": "范围"}, "idempotency_key": "unsafe-1",
	}

	bcc := cloneMap(base)
	bcc["bcc"] = []string{"hidden@example.com"}
	status, body := f.agent(http.MethodPost, "/api/v1/agent/mail/messages", bcc)
	if status != http.StatusForbidden || mailErrorCode(t, body) != string(domain.MailErrForbidden) {
		t.Fatalf("bcc status=%d body=%#v", status, body)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	otherTask := createHardwareAPITask(t, f.database)
	foreignAttachment := domain.NewID("attachment")
	f.execRaw(`INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, foreignAttachment, otherTask.ID, "", "report.zip",
		"application/zip", 1024, strings.Repeat("b", 64), now)
	foreign := cloneMap(base)
	foreign["attachment_ids"] = []string{foreignAttachment}
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", foreign)
	if status != http.StatusNotFound || mailErrorCode(t, body) != string(domain.MailErrAttachmentNotFound) {
		t.Fatalf("foreign attachment status=%d body=%#v", status, body)
	}

	localPath := cloneMap(base)
	localPath["attachment_ids"] = []string{`C:\Users\owner\secret.txt`}
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", localPath)
	if status != http.StatusNotFound || mailErrorCode(t, body) != string(domain.MailErrAttachmentNotFound) {
		t.Fatalf("local path status=%d body=%#v", status, body)
	}
	urlID := cloneMap(base)
	urlID["attachment_ids"] = []string{"https://example.com/file.zip"}
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", urlID)
	if status != http.StatusNotFound || mailErrorCode(t, body) != string(domain.MailErrAttachmentNotFound) {
		t.Fatalf("external URL status=%d body=%#v", status, body)
	}

	executable := domain.NewID("attachment")
	f.execRaw(`INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, executable, f.task.ID, "", "setup.exe",
		"application/octet-stream", 2048, strings.Repeat("c", 64), now)
	exePayload := cloneMap(base)
	exePayload["attachment_ids"] = []string{executable}
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", exePayload)
	if status != http.StatusUnprocessableEntity || mailErrorCode(t, body) != string(domain.MailErrAttachmentUnavailable) {
		t.Fatalf("executable attachment status=%d body=%#v", status, body)
	}

	// Binary JSON cannot be smuggled through an attachment object.
	binary := cloneMap(base)
	binary["attachments"] = []map[string]any{{
		"name": "x.zip", "content_base64": "AAAA",
	}}
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", binary)
	if status != http.StatusBadRequest {
		t.Fatalf("binary JSON status=%d body=%#v", status, body)
	}
}

func TestAgentMailCIDMustMatchAttachmentSnapshot(t *testing.T) {
	f := newAgentMailFixture(t)
	account, _ := f.createScopedTemplate()
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "CID 模板",
		"subject_template":   "CID",
		"body_text_template": "CID",
		"body_html_template": `<p>图片</p><img src="cid:mail_not_in_snapshot" alt="x">`,
	})
	status, body := f.agent(http.MethodPost, "/api/v1/agent/mail/preview", map[string]any{
		"account_id": account["id"], "template_id": template["id"],
		"to": []string{"qa@example.com"},
	})
	if status != http.StatusUnprocessableEntity || mailErrorCode(t, body) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("unknown cid status=%d body=%#v", status, body)
	}
}

func TestAgentMailMCPTools(t *testing.T) {
	f := newAgentMailFixture(t)
	account, template := f.createScopedTemplate()

	mcp := func(method string, params any) (int, map[string]any) {
		return f.agent(http.MethodPost, "/api/v1/agent/mail/mcp", map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
		})
	}
	status, body := mcp("initialize", map[string]any{})
	if status != http.StatusOK || body["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "aha2-mail" {
		t.Fatalf("initialize status=%d body=%#v", status, body)
	}
	status, body = mcp("tools/list", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("tools/list status=%d body=%#v", status, body)
	}
	tools := body["result"].(map[string]any)["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	for _, want := range []string{"mail_list_accounts", "mail_list_templates", "mail_template_detail", "mail_preview", "mail_send", "mail_get_status"} {
		if !agentMailContains(names, want) {
			t.Fatalf("tool %q missing from %#v", want, names)
		}
	}
	if len(names) != 6 {
		t.Fatalf("tools = %#v", names)
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		toolName := tool["name"].(string)
		if toolName != "mail_preview" && toolName != "mail_send" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, field := range []string{"subject", "body_text", "body_html"} {
			if _, ok := properties[field]; !ok {
				t.Fatalf("%s schema missing %s: %#v", toolName, field, properties)
			}
		}
		required := schema["required"].([]any)
		for _, field := range required {
			if field == "template_id" {
				t.Fatalf("%s schema still requires template_id: %#v", toolName, required)
			}
		}
		if toolName == "mail_send" {
			if len(required) != 2 {
				t.Fatalf("mail_send required = %#v", required)
			}
		} else if len(required) != 1 {
			t.Fatalf("mail_preview required = %#v", required)
		}
	}

	status, body = mcp("tools/call", map[string]any{"name": "mail_list_accounts", "arguments": map[string]any{}})
	if status != http.StatusOK {
		t.Fatalf("list accounts status=%d body=%#v", status, body)
	}
	text := mcpText(t, body)
	if !strings.Contains(text, account["id"].(string)) || strings.Contains(text, "agent-secret-authorization-code") {
		t.Fatalf("MCP account text = %q", text)
	}

	status, body = mcp("tools/call", map[string]any{
		"name": "mail_send",
		"arguments": map[string]any{
			"account_id": account["id"], "template_id": template["id"],
			"to":              []string{"qa@example.com"},
			"variables":       map[string]string{"test.scope": "MCP", "test.notes": "MCP"},
			"idempotency_key": "mcp-send-1",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("MCP send status=%d body=%#v", status, body)
	}
	sendText := mcpText(t, body)
	if !strings.Contains(sendText, `"status":"pending_approval"`) || !strings.Contains(sendText, `"deduplicated":false`) {
		t.Fatalf("MCP send text = %q", sendText)
	}

	// Direct (no-template) send uses the same tool surface and stays pending.
	status, body = mcp("tools/call", map[string]any{
		"name": "mail_send",
		"arguments": map[string]any{
			"account_id": account["id"], "subject": "MCP 直发",
			"body_html": `<p>MCP <strong>直发</strong></p>`, "to": []string{"qa@example.com"},
			"idempotency_key": "mcp-send-direct-1",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("MCP direct send status=%d body=%#v", status, body)
	}
	sendText = mcpText(t, body)
	if !strings.Contains(sendText, `"status":"pending_approval"`) {
		t.Fatalf("MCP direct send text = %q", sendText)
	}

	// BCC is not part of the tool schema; strict argument decoding rejects it
	// instead of silently dropping or honoring it.
	status, body = mcp("tools/call", map[string]any{
		"name": "mail_send",
		"arguments": map[string]any{
			"account_id": account["id"], "template_id": template["id"],
			"to": []string{"qa@example.com"}, "bcc": []string{"hidden@example.com"},
			"idempotency_key": "mcp-send-bcc",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("MCP bcc transport status=%d body=%#v", status, body)
	}
	result := body["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("MCP bcc was accepted: %#v", result)
	}
}

func mcpText(t *testing.T, body map[string]any) string {
	t.Helper()
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %#v", body)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing MCP content: %#v", result)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	return text
}

func cloneMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func agentMailContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAgentMailGuideTemplateDetailPreviewAndSend(t *testing.T) {
	f := newAgentMailFixture(t)
	account := f.createAccount("guide-secret-authorization-code")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "Guide 提测", "mode": "guide",
		"description":        "检查仓库列表、包名、版本和每个修改点；缺失项不要编造。",
		"subject_template":   "[AHA] {{task.title}} 提测通知（示例）",
		"body_text_template": "示例正文：{{task.title}}",
		"body_html_template": "<p>示例 HTML：{{task.title}}</p>",
	})
	templateID := template["id"].(string)
	f.bind(account["id"].(string), templateID)

	// The list stays lightweight but exposes mode/description/version.
	status, body := f.agent(http.MethodGet, "/api/v1/agent/mail/templates", nil)
	if status != http.StatusOK {
		t.Fatalf("templates status=%d body=%#v", status, body)
	}
	items := body["templates"].([]any)
	if len(items) != 1 {
		t.Fatalf("templates = %#v", items)
	}
	meta := items[0].(map[string]any)
	if meta["mode"] != "guide" || meta["description"] == "" || meta["version"] != float64(1) {
		t.Fatalf("guide metadata = %#v", meta)
	}
	if _, ok := meta["variables"]; ok {
		t.Fatalf("guide metadata advertised variables: %#v", meta)
	}
	for _, forbidden := range []string{"subject_template", "body_text_template", "body_html_template"} {
		if _, ok := meta[forbidden]; ok {
			t.Fatalf("list exposed %q: %#v", forbidden, meta)
		}
	}

	// HTTP detail returns the full reference content.
	status, body = f.agent(http.MethodGet, "/api/v1/agent/mail/templates/"+templateID, nil)
	if status != http.StatusOK {
		t.Fatalf("detail status=%d body=%#v", status, body)
	}
	if body["subject"] != "[AHA] {{task.title}} 提测通知（示例）" ||
		!strings.Contains(body["body_text"].(string), "示例正文") ||
		!strings.Contains(body["body_html"].(string), "示例 HTML") ||
		body["mode"] != "guide" || body["version"] != float64(1) || body["scope"] != "global" ||
		!strings.Contains(body["description"].(string), "检查仓库列表") {
		t.Fatalf("guide detail = %#v", body)
	}

	// MCP detail is equivalent and remains scoped to the Task.
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "mail_template_detail", "arguments": map[string]any{"template_id": templateID}},
	})
	if status != http.StatusOK {
		t.Fatalf("mcp detail status=%d body=%#v", status, body)
	}
	if text := mcpText(t, body); !strings.Contains(text, "Guide 提测") || !strings.Contains(text, `"mode":"guide"`) {
		t.Fatalf("mcp detail text = %q", text)
	}

	// Guide preview requires caller final content; the example is not a fallback.
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/preview", map[string]any{
		"account_id": account["id"], "template_id": templateID,
		"to": []string{"qa@example.com"},
	})
	if status != http.StatusUnprocessableEntity || mailErrorCode(t, body) != string(domain.MailErrTemplateContentMissing) {
		t.Fatalf("missing guide content status=%d body=%#v", status, body)
	}

	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/preview", map[string]any{
		"account_id": account["id"], "template_id": templateID,
		"subject": "最终主题 v1.0.0", "body_html": "<p>最终正文 <strong>v1.0.0</strong></p>",
		"variables": map[string]string{"task.title": "ignored"},
		"to":        []string{"qa@example.com"},
	})
	if status != http.StatusOK {
		t.Fatalf("guide preview status=%d body=%#v", status, body)
	}
	preview := body["preview"].(map[string]any)
	if preview["subject"] != "最终主题 v1.0.0" || preview["template_mode"] != "guide" ||
		preview["template_id"] != templateID || preview["template_version"] != float64(1) {
		t.Fatalf("guide preview = %#v", preview)
	}
	if strings.Contains(preview["body_html"].(string), "{{") || !strings.Contains(preview["body_html"].(string), "<strong>v1.0.0</strong>") {
		t.Fatalf("guide preview html = %#v", preview)
	}

	// Leftover placeholders and unsafe HTML are rejected by the same pipeline.
	for name, payload := range map[string]map[string]any{
		"placeholder": {
			"account_id": account["id"], "template_id": templateID,
			"subject": "{{task.title}}", "body_text": "x", "to": []string{"qa@example.com"},
		},
		"unsafe html": {
			"account_id": account["id"], "template_id": templateID,
			"subject": "s", "body_html": `<p onclick="alert(1)">x</p>`, "to": []string{"qa@example.com"},
		},
	} {
		status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/preview", payload)
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("%s status=%d body=%#v", name, status, body)
		}
		wantCode := string(domain.MailErrTemplateInvalid)
		if name == "placeholder" {
			wantCode = string(domain.MailErrTemplatePlaceholderUnresolved)
		}
		if mailErrorCode(t, body) != wantCode {
			t.Fatalf("%s code=%s body=%#v", name, mailErrorCode(t, body), body)
		}
	}

	// Send records the reference template/version and ignores variables.
	status, body = f.agent(http.MethodPost, "/api/v1/agent/mail/messages", map[string]any{
		"account_id": account["id"], "template_id": templateID,
		"subject": "最终发送主题", "body_text": "最终发送正文",
		"variables":       map[string]string{"task.title": "ignored", "build.version": "v1"},
		"to":              []string{"qa@example.com"},
		"idempotency_key": "agent-guide-send-1",
	})
	if status != http.StatusCreated || body["status"] != string(domain.MailMessagePendingApproval) {
		t.Fatalf("guide send status=%d body=%#v", status, body)
	}
	messages, err := f.database.MailMessagesByTask(f.ctx, f.task.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	stored := messages[0]
	if stored.TemplateID != templateID || stored.TemplateVersion != 1 {
		t.Fatalf("reference template snapshot = %#v", stored)
	}
	if len(stored.Variables) != 0 {
		t.Fatalf("guide message kept variables: %#v", stored.Variables)
	}
	if stored.Subject != "最终发送主题" || stored.BodyText != "最终发送正文" {
		t.Fatalf("guide message content = %#v", stored)
	}
}
