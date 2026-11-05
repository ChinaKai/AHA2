package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

type fakeMailTester struct {
	result      mailpkg.TestResult
	credentials []string
	accounts    []domain.MailAccount
}

func (f *fakeMailTester) Test(_ context.Context, account domain.MailAccount, credential string) mailpkg.TestResult {
	f.accounts = append(f.accounts, account)
	f.credentials = append(f.credentials, credential)
	if f.result.ErrorCode == "" && !f.result.OK {
		return mailpkg.TestResult{
			OK: true, Level: "connect", Host: account.SMTPHost, Port: account.SMTPPort,
			TLSVersion: "TLS1.3", Auth: "ok", Capabilities: []string{"AUTH", "SIZE"},
		}
	}
	return f.result
}

type mailAPIFixture struct {
	t        *testing.T
	ctx      context.Context
	dbPath   string
	database *store.Store
	server   *httptest.Server
	client   *http.Client
	csrf     string
	secrets  *fakeSecretStore
	tester   *fakeMailTester
	task     domain.Task
}

func newMailAPIFixture(t *testing.T) *mailAPIFixture {
	return newMailAPIFixtureWith(t, nil)
}

func newMailAPIFixtureWith(t *testing.T, mutate func(*Config)) *mailAPIFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	task := createHardwareAPITask(t, database)
	secretsStore := &fakeSecretStore{}
	tester := &fakeMailTester{}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, nil, app.StubExecutor{})
	config := Config{
		Store: database, Auth: authService, App: appService,
		Secrets: secretsStore, MailTester: tester,
		MailTaskBaseURL: "https://aha.example.com",
	}
	if mutate != nil {
		mutate(&config)
	}
	server := httptest.NewServer(New(config).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	return &mailAPIFixture{
		t: t, ctx: ctx, dbPath: path, database: database, server: server, client: client,
		csrf: registerOwner(t, client, server.URL), secrets: secretsStore,
		tester: tester, task: task,
	}
}

func (f *mailAPIFixture) rawDB() *sql.DB {
	f.t.Helper()
	raw, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { raw.Close() })
	return raw
}

func (f *mailAPIFixture) request(method, path string, payload any, csrf string) map[string]any {
	f.t.Helper()
	response := requestJSON(f.t, f.client, method, f.server.URL+path, payload, csrf)
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		f.t.Fatalf("%s %s: decode: %v", method, path, err)
	}
	body["_status"] = float64(response.StatusCode)
	return body
}

func (f *mailAPIFixture) createAccount(code string) map[string]any {
	f.t.Helper()
	body := f.request(http.MethodPost, "/api/v1/mail-accounts", map[string]any{
		"name": "构建通知", "address": "noreply@example.com", "display_name": "AHA",
		"provider": "netease_qiye", "smtp_host": "smtp.qiye.163.com",
		"smtp_port": 465, "smtp_security": "ssl",
		"smtp_username": "noreply@example.com", "authorization_code": code,
		"aliases": []string{"build-bot@example.com"},
	}, f.csrf)
	if body["_status"].(float64) != http.StatusCreated {
		f.t.Fatalf("create account = %#v", body)
	}
	return body["account"].(map[string]any)
}

func (f *mailAPIFixture) createTemplate(payload map[string]any) map[string]any {
	f.t.Helper()
	body := f.request(http.MethodPost, "/api/v1/mail-templates", payload, f.csrf)
	if body["_status"].(float64) != http.StatusCreated {
		f.t.Fatalf("create template = %#v", body)
	}
	return body["template"].(map[string]any)
}

func mailErrorCode(t *testing.T, body map[string]any) string {
	t.Helper()
	errorBody, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured error: %#v", body)
	}
	code, _ := errorBody["code"].(string)
	return code
}

func (f *mailAPIFixture) auditCount(action, resourceID string) int {
	f.t.Helper()
	var count int
	query := `SELECT COUNT(*) FROM audit_events WHERE action=?`
	args := []any{action}
	if resourceID != "" {
		query += ` AND resource_id=?`
		args = append(args, resourceID)
	}
	if err := f.rawDB().QueryRowContext(f.ctx, query, args...).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

func (f *mailAPIFixture) countAuditContaining(needle string) int {
	f.t.Helper()
	var count int
	if err := f.rawDB().QueryRowContext(f.ctx,
		`SELECT COUNT(*) FROM audit_events WHERE data_json LIKE ?`, "%"+needle+"%").Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

func (f *mailAPIFixture) execRaw(query string, args ...any) {
	f.t.Helper()
	if _, err := f.rawDB().ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func TestMailOwnerRoutesRequireSessionAndCSRF(t *testing.T) {
	f := newMailAPIFixture(t)

	anonymousJar, _ := cookiejar.New(nil)
	anonymous := &http.Client{Jar: anonymousJar}
	response := requestJSON(t, anonymous, http.MethodGet, f.server.URL+"/api/v1/mail-accounts", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET status = %d", response.StatusCode)
	}
	response.Body.Close()

	body := f.request(http.MethodPost, "/api/v1/mail-accounts", map[string]any{
		"address": "noreply@example.com",
	}, "")
	if body["_status"].(float64) != http.StatusForbidden || body["error"] != "csrf_rejected" {
		t.Fatalf("missing CSRF = %#v", body)
	}

	// An Agent capability Bearer token is not a browser session and must not
	// reach Owner mail routes.
	request, _ := http.NewRequest(http.MethodGet, f.server.URL+"/api/v1/mail-accounts", nil)
	request.Header.Set("Authorization", "Bearer agent-capability-token")
	response, err := anonymous.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("agent capability status = %d", response.StatusCode)
	}

	body = f.request(http.MethodGet, "/api/v1/mail-accounts", nil, "")
	if body["_status"].(float64) != http.StatusOK {
		t.Fatalf("owner GET = %#v", body)
	}
}

func TestMailAccountCRUDDoesNotEchoCredential(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("super-secret-authorization-code")
	if account["credential_configured"] != true {
		t.Fatalf("credential_configured = %#v", account["credential_configured"])
	}
	ref := domain.MailAccountCredentialRef(account["id"].(string))
	if f.secrets.values[ref] != "super-secret-authorization-code" {
		t.Fatalf("secret not stored: %#v", f.secrets.values)
	}
	encoded, _ := json.Marshal(account)
	if strings.Contains(string(encoded), "super-secret-authorization-code") ||
		strings.Contains(string(encoded), "credential_ref") ||
		strings.Contains(string(encoded), ref) {
		t.Fatalf("credential leaked in response: %s", encoded)
	}

	detail := f.request(http.MethodGet, "/api/v1/mail-accounts/"+account["id"].(string), nil, "")
	encoded, _ = json.Marshal(detail)
	if strings.Contains(string(encoded), "super-secret-authorization-code") ||
		strings.Contains(string(encoded), "credential_ref") {
		t.Fatalf("credential leaked in detail: %s", encoded)
	}

	rotated := f.request(http.MethodPut, "/api/v1/mail-accounts/"+account["id"].(string), map[string]any{
		"authorization_code": "rotated-secret-code",
	}, f.csrf)
	encoded, _ = json.Marshal(rotated)
	if strings.Contains(string(encoded), "rotated-secret-code") ||
		strings.Contains(string(encoded), "credential_ref") {
		t.Fatalf("rotated credential leaked: %s", encoded)
	}
	if f.secrets.values[ref] != "rotated-secret-code" {
		t.Fatalf("secret not rotated: %#v", f.secrets.values)
	}
	account = rotated["account"].(map[string]any)
	if account["status"] != string(domain.MailAccountUnverified) || account["credential_configured"] != true {
		t.Fatalf("rotated account = %#v", account)
	}

	disabled := f.request(http.MethodPut, "/api/v1/mail-accounts/"+account["id"].(string), map[string]any{
		"status": "disabled",
	}, f.csrf)
	if disabled["account"].(map[string]any)["status"] != string(domain.MailAccountDisabled) {
		t.Fatalf("disable = %#v", disabled)
	}

	deleted := f.request(http.MethodDelete, "/api/v1/mail-accounts/"+account["id"].(string), nil, f.csrf)
	if deleted["_status"].(float64) != http.StatusOK {
		t.Fatalf("delete = %#v", deleted)
	}
	if _, exists := f.secrets.values[ref]; exists {
		t.Fatalf("secret still present after delete: %#v", f.secrets.values)
	}
	missing := f.request(http.MethodGet, "/api/v1/mail-accounts/"+account["id"].(string), nil, "")
	if missing["_status"].(float64) != http.StatusNotFound || mailErrorCode(t, missing) != string(domain.MailErrAccountNotFound) {
		t.Fatalf("deleted detail = %#v", missing)
	}
}

func TestMailAccountTagDeleteBlockedByBinding(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("code-one")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "通知", "subject_template": "主题",
		"body_text_template": "正文",
	})
	binding := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID,
		"account_id": account["id"], "default_template_id": template["id"],
	}, f.csrf)
	if binding["_status"].(float64) != http.StatusCreated {
		t.Fatalf("binding = %#v", binding)
	}
	blocked := f.request(http.MethodDelete, "/api/v1/mail-accounts/"+account["id"].(string), nil, f.csrf)
	if blocked["_status"].(float64) != http.StatusConflict {
		t.Fatalf("delete with binding = %#v", blocked)
	}
	if _, exists := f.secrets.values[domain.MailAccountCredentialRef(account["id"].(string))]; !exists {
		t.Fatal("secret should remain when account cannot be deleted")
	}
}

func TestMailConnectionTestOutcomes(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("connection-test-code")
	id := account["id"].(string)
	if f.tester.result.ErrorCode != "" || f.tester.result.OK {
		t.Fatal("fake tester default should be success")
	}

	success := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
	if success["ok"] != true {
		t.Fatalf("success = %#v", success)
	}
	if f.tester.credentials[len(f.tester.credentials)-1] != "connection-test-code" {
		t.Fatal("connection test did not resolve the stored credential")
	}
	if success["account"].(map[string]any)["status"] != string(domain.MailAccountOK) {
		t.Fatalf("success account = %#v", success["account"])
	}
	encoded, _ := json.Marshal(success)
	if strings.Contains(string(encoded), "connection-test-code") {
		t.Fatalf("credential leaked in test response: %s", encoded)
	}

	tests := []struct {
		name   string
		result mailpkg.TestResult
	}{
		{"auth", mailpkg.TestResult{OK: false, Level: "auth", ErrorCode: domain.MailErrSMTPAuthFailed, Message: "SMTP 认证失败"}},
		{"tls", mailpkg.TestResult{OK: false, Level: "tls", ErrorCode: domain.MailErrSMTPTLSFailed, Message: "TLS 握手失败"}},
		{"timeout", mailpkg.TestResult{OK: false, Level: "connect", ErrorCode: domain.MailErrSMTPConnectTimeout, Retryable: true, Message: "连接超时"}},
	}
	for _, test := range tests {
		f.tester.result = test.result
		body := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
		if body["ok"] != false {
			t.Fatalf("%s: ok = %#v", test.name, body["ok"])
		}
		resultBody := body["result"].(map[string]any)
		if resultBody["error_code"] != string(test.result.ErrorCode) {
			t.Fatalf("%s: result = %#v", test.name, resultBody)
		}
		updated := body["account"].(map[string]any)
		if updated["status"] != string(domain.MailAccountError) ||
			updated["last_test_error_code"] != string(test.result.ErrorCode) {
			t.Fatalf("%s: account = %#v", test.name, updated)
		}
	}

	f.tester.result = mailpkg.TestResult{}
	disabled := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{"status": "disabled"}, f.csrf)
	if disabled["_status"].(float64) != http.StatusOK {
		t.Fatalf("disable = %#v", disabled)
	}
	blocked := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
	if blocked["_status"].(float64) != http.StatusConflict || mailErrorCode(t, blocked) != string(domain.MailErrAccountDisabled) {
		t.Fatalf("disabled test = %#v", blocked)
	}

	unconfigured := f.request(http.MethodPost, "/api/v1/mail-accounts", map[string]any{
		"name": "未配置", "address": "other@example.com",
	}, f.csrf)
	if unconfigured["_status"].(float64) != http.StatusCreated {
		t.Fatalf("unconfigured create = %#v", unconfigured)
	}
	missing := f.request(http.MethodPost, "/api/v1/mail-accounts/"+unconfigured["account"].(map[string]any)["id"].(string)+"/test", map[string]any{}, f.csrf)
	if missing["_status"].(float64) != http.StatusUnprocessableEntity || mailErrorCode(t, missing) != string(domain.MailErrCredentialMissing) {
		t.Fatalf("missing credential = %#v", missing)
	}
}

func TestMailTemplateVersioningAndValidation(t *testing.T) {
	f := newMailAPIFixture(t)
	created := f.createTemplate(map[string]any{
		"scope": "global", "name": "提测通知",
		"subject_template":   "[AHA] {{build.version}} 提测通知",
		"body_text_template": "任务：{{task.title}}\n版本：{{build.version}}",
		"body_html_template": `<h1>{{build.version}}</h1><p>{{task.title}}</p><a href="https://example.com/tasks/{{task.id}}">任务</a>`,
		"default_to":         []string{"qa-team@example.com"},
		"required_variables": []string{"build.version"},
	})
	id := created["id"].(string)
	if created["current_version"].(float64) != 1 {
		t.Fatalf("current_version = %#v", created["current_version"])
	}

	preview := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id":   f.task.ID,
		"variables": map[string]string{"build.version": "v1.2.3"},
	}, f.csrf)
	if preview["_status"].(float64) != http.StatusOK {
		t.Fatalf("preview = %#v", preview)
	}
	previewBody := preview["preview"].(map[string]any)
	if !strings.Contains(previewBody["subject"].(string), "v1.2.3") ||
		!strings.Contains(previewBody["body_text"].(string), f.task.Title) {
		t.Fatalf("preview content = %#v", previewBody)
	}
	if len(previewBody["to"].([]any)) != 1 || previewBody["to"].([]any)[0] != "qa-team@example.com" {
		t.Fatalf("default recipients = %#v", previewBody["to"])
	}

	missing := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id": f.task.ID, "variables": map[string]string{},
	}, f.csrf)
	if missing["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, missing) != string(domain.MailErrTemplateVariableMissing) {
		t.Fatalf("missing variable = %#v", missing)
	}

	invalidRecipient := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id": f.task.ID, "variables": map[string]string{"build.version": "v1"},
		"to": []string{"not-an-email"},
	}, f.csrf)
	if invalidRecipient["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, invalidRecipient) != string(domain.MailErrRecipientInvalid) {
		t.Fatalf("invalid recipient = %#v", invalidRecipient)
	}

	unknown := f.request(http.MethodPost, "/api/v1/mail-templates", map[string]any{
		"scope": "global", "name": "坏模板", "subject_template": "{{secret.value}}",
		"body_text_template": "x",
	}, f.csrf)
	if unknown["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, unknown) != string(domain.MailErrTemplateVariableUnknown) {
		t.Fatalf("unknown variable = %#v", unknown)
	}

	xss := f.request(http.MethodPost, "/api/v1/mail-templates", map[string]any{
		"scope": "global", "name": "XSS", "subject_template": "x",
		"body_text_template": "x", "body_html_template": `<p onmouseover="alert(1)">x</p>`,
	}, f.csrf)
	if xss["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, xss) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("xss template = %#v", xss)
	}

	overlong := f.request(http.MethodPost, "/api/v1/mail-templates", map[string]any{
		"scope": "global", "name": "超长", "subject_template": strings.Repeat("s", 201),
		"body_text_template": "x",
	}, f.csrf)
	if overlong["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, overlong) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("overlong subject = %#v", overlong)
	}
	overlongBody := f.request(http.MethodPost, "/api/v1/mail-templates", map[string]any{
		"scope": "global", "name": "超长正文", "subject_template": "s",
		"body_text_template": strings.Repeat("b", mailpkg.MaxBodyTextBytes+1),
	}, f.csrf)
	if overlongBody["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, overlongBody) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("overlong body = %#v", overlongBody)
	}

	updated := f.request(http.MethodPut, "/api/v1/mail-templates/"+id, map[string]any{
		"expected_version": 1, "subject_template": "[AHA] v2 {{build.version}}",
		"body_text_template": "v2 {{task.title}}", "body_html_template": "",
	}, f.csrf)
	if updated["_status"].(float64) != http.StatusOK || updated["template"].(map[string]any)["current_version"].(float64) != 2 {
		t.Fatalf("update = %#v", updated)
	}
	versions := f.request(http.MethodGet, "/api/v1/mail-templates/"+id+"/versions", nil, "")
	list := versions["versions"].([]any)
	if len(list) != 2 {
		t.Fatalf("versions = %#v", versions)
	}
	version1 := list[1].(map[string]any)
	if !strings.Contains(version1["subject_template"].(string), "提测通知") {
		t.Fatalf("version 1 was rewritten: %#v", version1)
	}
	conflict := f.request(http.MethodPut, "/api/v1/mail-templates/"+id, map[string]any{
		"expected_version": 1, "subject_template": "stale",
		"body_text_template": "stale",
	}, f.csrf)
	if conflict["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, conflict) != string(domain.MailErrTemplateVersionConflict) {
		t.Fatalf("stale update = %#v", conflict)
	}
}

func TestMailTemplatePreviewXSSAndCIDMapping(t *testing.T) {
	f := newMailAPIFixture(t)
	attachment, err := f.database.CreateAttachment(f.ctx, f.task.ID, "screenshot.png", "image/png", []byte("png-bytes"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	otherTask := createHardwareAPITask(t, f.database)
	otherAttachment, err := f.database.CreateAttachment(f.ctx, otherTask.ID, "other.png", "image/png", []byte("other"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "内嵌图", "subject_template": "截图",
		"body_text_template": "截图见正文",
		"body_html_template": `<p><img src="cid:{{extra.image}}" alt="shot"></p>`,
		"required_variables": []string{"extra.image"},
	})
	id := template["id"].(string)
	variables := map[string]string{"extra.image": attachment.ID}

	preview := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id": f.task.ID, "variables": variables, "attachment_ids": []string{attachment.ID},
	}, f.csrf)
	if preview["_status"].(float64) != http.StatusOK {
		t.Fatalf("cid preview = %#v", preview)
	}
	previewBody := preview["preview"].(map[string]any)
	if !strings.Contains(previewBody["body_html"].(string), "cid:"+attachment.ID) {
		t.Fatalf("body html = %#v", previewBody["body_html"])
	}
	attachments := previewBody["attachments"].([]any)
	if len(attachments) != 1 || attachments[0].(map[string]any)["content_id"] != attachment.ID {
		t.Fatalf("attachments = %#v", attachments)
	}
	encoded, _ := json.Marshal(previewBody)
	if strings.Contains(string(encoded), "png-bytes") {
		t.Fatal("attachment bytes leaked into preview JSON")
	}

	wrongCID := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id":        f.task.ID,
		"variables":      map[string]string{"extra.image": otherAttachment.ID},
		"attachment_ids": []string{attachment.ID},
	}, f.csrf)
	if wrongCID["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, wrongCID) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("wrong cid = %#v", wrongCID)
	}

	noAttachmentTemplate := f.createTemplate(map[string]any{
		"scope": "global", "name": "无附件", "subject_template": "s",
		"body_text_template": "t", "body_html_template": `<img src="cid:{{extra.image}}">`,
	})
	noAttachment := f.request(http.MethodPost, "/api/v1/mail-templates/"+noAttachmentTemplate["id"].(string)+"/preview", map[string]any{
		"task_id": f.task.ID, "variables": map[string]string{"extra.image": attachment.ID},
	}, f.csrf)
	if noAttachment["_status"].(float64) != http.StatusUnprocessableEntity {
		t.Fatalf("cid without attachment = %#v", noAttachment)
	}

	foreign := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id":        f.task.ID,
		"variables":      map[string]string{"extra.image": otherAttachment.ID},
		"attachment_ids": []string{otherAttachment.ID},
	}, f.csrf)
	if foreign["_status"].(float64) != http.StatusNotFound ||
		mailErrorCode(t, foreign) != string(domain.MailErrAttachmentNotFound) {
		t.Fatalf("foreign attachment = %#v", foreign)
	}
}

func TestMailBuildArtifactPreviewUsesServerPackageValues(t *testing.T) {
	f := newMailAPIFixture(t)
	source, err := f.database.CreateAttachment(f.ctx, f.task.ID, "firmware.bin", "application/octet-stream", []byte("firmware"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := f.database.CreateBuildArtifact(f.ctx, domain.BuildArtifact{
		TaskID: f.task.ID, Label: "firmware", PackageName: "firmware.bin",
		Version: "v9.9.9", SizeBytes: source.Size, SHA256: source.SHA256,
		AttachmentID: source.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "构建产物", "subject_template": "{{package.name}}",
		"body_text_template": "{{package.name}} {{package.size}} {{package.sha256}}",
	})
	preview := f.request(http.MethodPost, "/api/v1/mail-templates/"+template["id"].(string)+"/preview", map[string]any{
		"task_id": f.task.ID, "variables": map[string]string{},
		"build_artifact_ids": []string{artifact.ID},
	}, f.csrf)
	if preview["_status"].(float64) != http.StatusOK {
		t.Fatalf("artifact preview = %#v", preview)
	}
	body := preview["preview"].(map[string]any)
	if !strings.Contains(body["body_text"].(string), source.SHA256) ||
		!strings.Contains(body["body_text"].(string), "firmware.bin") {
		t.Fatalf("package variables not resolved: %#v", body["body_text"])
	}

	forged := f.request(http.MethodPost, "/api/v1/mail-templates/"+template["id"].(string)+"/preview", map[string]any{
		"task_id":   f.task.ID,
		"variables": map[string]string{"package.sha256": strings.Repeat("0", 64)},
	}, f.csrf)
	if forged["_status"].(float64) != http.StatusForbidden ||
		mailErrorCode(t, forged) != string(domain.MailErrForbidden) {
		t.Fatalf("forged package value = %#v", forged)
	}
}

func TestMailMessageSnapshotDoesNotFollowTemplateUpdate(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("snapshot-code")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "快照", "subject_template": "版本 1 {{task.title}}",
		"body_text_template": "正文 1 {{task.title}}",
	})
	rendered, err := mailpkg.Render(mailpkg.Template{
		ID: template["id"].(string), Version: 1,
		SubjectTemplate: "版本 1 {{task.title}}", BodyTextTemplate: "正文 1 {{task.title}}",
	}, mailpkg.Input{ServerVariables: map[string]string{"task.title": f.task.Title}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.database.CreateMailMessage(f.ctx, domain.MailMessage{
		TaskID: f.task.ID, ProjectID: f.task.ProjectID, WorkspaceID: f.task.WorkspaceID,
		AccountID: account["id"].(string), TemplateID: template["id"].(string),
		TemplateVersion: 1, Source: domain.MailMessageSourceOwner,
		IdempotencyKey: "snapshot-1", FromAddress: "noreply@example.com",
		To: []string{"qa-team@example.com"}, Subject: rendered.Subject,
		BodyText: rendered.BodyText, Status: domain.MailMessageDraft,
	})
	if err != nil {
		t.Fatal(err)
	}

	updated := f.request(http.MethodPut, "/api/v1/mail-templates/"+template["id"].(string), map[string]any{
		"expected_version": 1, "subject_template": "版本 2 {{task.title}}",
		"body_text_template": "正文 2 {{task.title}}",
	}, f.csrf)
	if updated["_status"].(float64) != http.StatusOK {
		t.Fatalf("update = %#v", updated)
	}
	stored, err := f.database.MailMessage(f.ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Subject != "版本 1 "+f.task.Title || stored.BodyText != "正文 1 "+f.task.Title {
		t.Fatalf("message snapshot changed: %#v", stored)
	}
	if stored.TemplateVersion != 1 {
		t.Fatalf("template version = %d", stored.TemplateVersion)
	}
}

func TestMailBindingsCRUDAndVisibility(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("binding-code")
	globalTemplate := f.createTemplate(map[string]any{
		"scope": "global", "name": "全局", "subject_template": "s", "body_text_template": "t",
	})
	projectTemplate := f.createTemplate(map[string]any{
		"scope": "project", "project_id": f.task.ProjectID, "name": "项目",
		"subject_template": "s", "body_text_template": "t",
	})
	otherTask := createHardwareAPITask(t, f.database)
	otherProjectTemplate := f.createTemplate(map[string]any{
		"scope": "project", "project_id": otherTask.ProjectID, "name": "别的项目",
		"subject_template": "s", "body_text_template": "t",
	})

	created := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
		"default_template_id": projectTemplate["id"],
		"allowed_recipients":  []string{"qa-team@example.com"},
	}, f.csrf)
	if created["_status"].(float64) != http.StatusCreated {
		t.Fatalf("create binding = %#v", created)
	}
	binding := created["binding"].(map[string]any)
	if binding["approval_mode"] != string(domain.MailApprovalAlways) || binding["enabled"] != true {
		t.Fatalf("binding defaults = %#v", binding)
	}

	duplicate := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
	}, f.csrf)
	if duplicate["_status"].(float64) != http.StatusConflict {
		t.Fatalf("duplicate binding = %#v", duplicate)
	}

	listed := f.request(http.MethodGet, "/api/v1/mail-bindings?scope_type=task&scope_id="+f.task.ID, nil, "")
	if len(listed["bindings"].([]any)) != 1 {
		t.Fatalf("listed bindings = %#v", listed)
	}

	disabled := false
	updated := f.request(http.MethodPut, "/api/v1/mail-bindings/"+binding["id"].(string), map[string]any{
		"enabled": disabled,
	}, f.csrf)
	if updated["_status"].(float64) != http.StatusOK || updated["binding"].(map[string]any)["enabled"] != false {
		t.Fatalf("update binding = %#v", updated)
	}

	auto := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
		"approval_mode": "allowlist_auto",
	}, f.csrf)
	if auto["_status"].(float64) != http.StatusUnprocessableEntity {
		t.Fatalf("auto binding = %#v", auto)
	}

	foreignTemplate := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
		"default_template_id": otherProjectTemplate["id"],
	}, f.csrf)
	if foreignTemplate["_status"].(float64) != http.StatusNotFound ||
		mailErrorCode(t, foreignTemplate) != string(domain.MailErrTemplateNotFound) {
		t.Fatalf("foreign template = %#v", foreignTemplate)
	}

	globalBinding := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "workspace", "scope_id": f.task.WorkspaceID, "account_id": account["id"],
		"default_template_id": globalTemplate["id"],
	}, f.csrf)
	if globalBinding["_status"].(float64) != http.StatusCreated {
		t.Fatalf("workspace binding = %#v", globalBinding)
	}

	deleted := f.request(http.MethodDelete, "/api/v1/mail-bindings/"+binding["id"].(string), nil, f.csrf)
	if deleted["_status"].(float64) != http.StatusOK {
		t.Fatalf("delete binding = %#v", deleted)
	}
}
