package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailAccountPartialUpdatePreservesName(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("partial-code")
	id := account["id"].(string)

	disabled := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"status": "disabled",
	}, f.csrf)
	if disabled["_status"].(float64) != http.StatusOK {
		t.Fatalf("disable = %#v", disabled)
	}
	if disabled["account"].(map[string]any)["name"] != "构建通知" {
		t.Fatalf("partial update cleared name: %#v", disabled["account"])
	}

	rotated := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"authorization_code": "partial-rotated-code",
	}, f.csrf)
	if rotated["_status"].(float64) != http.StatusOK {
		t.Fatalf("rotate = %#v", rotated)
	}
	if rotated["account"].(map[string]any)["name"] != "构建通知" {
		t.Fatalf("rotation cleared name: %#v", rotated["account"])
	}
}

func TestMailAccountSMTPHostWhitelistAndStatusReset(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("host-code")
	id := account["id"].(string)
	success := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
	if success["account"].(map[string]any)["status"] != string(domain.MailAccountOK) {
		t.Fatalf("test did not set ok: %#v", success)
	}

	invalidHosts := []string{
		"169.254.169.254",
		"evil.example.com",
		"smtp.qiye.163.com.",
		"smtp.qiye.163.com.evil.example",
		"smtp.qiye.163.com:465",
	}
	for _, host := range invalidHosts {
		body := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{"smtp_host": host}, f.csrf)
		if body["_status"].(float64) != http.StatusUnprocessableEntity ||
			mailErrorCode(t, body) != string(domain.MailErrAccountInvalid) {
			t.Fatalf("host %q = %#v", host, body)
		}
	}

	backup := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"smtp_host": "SMTPHZ.QIYE.163.COM",
	}, f.csrf)
	if backup["_status"].(float64) != http.StatusOK {
		t.Fatalf("backup host = %#v", backup)
	}
	updated := backup["account"].(map[string]any)
	if updated["smtp_host"] != domain.MailBackupSMTPHost || updated["status"] != string(domain.MailAccountUnverified) {
		t.Fatalf("host change did not reset status: %#v", updated)
	}
	if code, present := updated["last_test_error_code"]; present && code != "" {
		t.Fatalf("last test error not cleared: %#v", updated)
	}

	if again := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf); again["account"].(map[string]any)["status"] != string(domain.MailAccountOK) {
		t.Fatalf("retest after host change = %#v", again)
	}
	username := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"smtp_username": "another@example.com",
	}, f.csrf)
	if username["account"].(map[string]any)["status"] != string(domain.MailAccountUnverified) {
		t.Fatalf("username change did not reset status: %#v", username)
	}
}

func TestMailConnectionTestRejectsTamperedHost(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("tampered-code")
	id := account["id"].(string)
	f.execRaw(`UPDATE mail_accounts SET smtp_host=? WHERE id=?`, "169.254.169.254", id)
	body := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
	if body["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, body) != string(domain.MailErrAccountInvalid) {
		t.Fatalf("tampered host test = %#v", body)
	}
	if len(f.tester.credentials) != 0 {
		t.Fatal("tester was called for a non-allowlisted host")
	}
}

func TestMailPreviewTaskURLAndServerVariables(t *testing.T) {
	f := newMailAPIFixture(t)
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "任务链接",
		"subject_template": "{{task.url}}", "body_text_template": "{{task.url}}",
	})
	preview := f.request(http.MethodPost, "/api/v1/mail-templates/"+template["id"].(string)+"/preview", map[string]any{
		"task_id": f.task.ID,
	}, f.csrf)
	if preview["_status"].(float64) != http.StatusOK {
		t.Fatalf("task url preview = %#v", preview)
	}
	wantURL := "https://aha.example.com/tasks/" + f.task.ID
	if preview["preview"].(map[string]any)["subject"] != wantURL {
		t.Fatalf("task.url = %#v", preview["preview"])
	}

	for _, name := range []string{"task.url", "project.name", "package.sha256"} {
		body := f.request(http.MethodPost, "/api/v1/mail-templates/"+template["id"].(string)+"/preview", map[string]any{
			"variables": map[string]string{name: "https://evil.example/forged"},
		}, f.csrf)
		if body["_status"].(float64) != http.StatusForbidden ||
			mailErrorCode(t, body) != string(domain.MailErrForbidden) {
			t.Fatalf("caller %s = %#v", name, body)
		}
	}

	missing := f.request(http.MethodPost, "/api/v1/mail-templates/"+template["id"].(string)+"/preview", map[string]any{}, f.csrf)
	if missing["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, missing) != string(domain.MailErrTemplateVariableMissing) {
		t.Fatalf("missing server variable = %#v", missing)
	}

	noBase := newMailAPIFixtureWith(t, func(config *Config) { config.MailTaskBaseURL = "" })
	noBaseTemplate := noBase.createTemplate(map[string]any{
		"scope": "global", "name": "无基地址",
		"subject_template": "{{task.url}}", "body_text_template": "x",
	})
	noBasePreview := noBase.request(http.MethodPost, "/api/v1/mail-templates/"+noBaseTemplate["id"].(string)+"/preview", map[string]any{
		"task_id": noBase.task.ID,
	}, noBase.csrf)
	if noBasePreview["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, noBasePreview) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("unconfigured base = %#v", noBasePreview)
	}
}

func TestMailBuildArtifactCrossTaskHashAndDedup(t *testing.T) {
	f := newMailAPIFixture(t)
	source, err := f.database.CreateAttachment(f.ctx, f.task.ID, "fw.bin", "application/octet-stream", []byte("fw"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := f.database.CreateBuildArtifact(f.ctx, domain.BuildArtifact{
		TaskID: f.task.ID, Label: "fw", PackageName: "fw.bin", Version: "v1",
		SizeBytes: source.Size, SHA256: source.SHA256, AttachmentID: source.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherTask := createHardwareAPITask(t, f.database)
	otherSource, err := f.database.CreateAttachment(f.ctx, otherTask.ID, "other.bin", "application/octet-stream", []byte("other"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	otherArtifact, err := f.database.CreateBuildArtifact(f.ctx, domain.BuildArtifact{
		TaskID: otherTask.ID, Label: "other", SizeBytes: otherSource.Size,
		SHA256: otherSource.SHA256, AttachmentID: otherSource.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "产物校验", "subject_template": "s", "body_text_template": "t",
	})
	id := template["id"].(string)

	foreign := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id": f.task.ID, "build_artifact_ids": []string{otherArtifact.ID},
	}, f.csrf)
	if foreign["_status"].(float64) != http.StatusNotFound ||
		mailErrorCode(t, foreign) != string(domain.MailErrAttachmentNotFound) {
		t.Fatalf("foreign artifact = %#v", foreign)
	}

	deduped := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id": f.task.ID, "attachment_ids": []string{source.ID},
		"build_artifact_ids": []string{artifact.ID},
	}, f.csrf)
	if deduped["_status"].(float64) != http.StatusOK {
		t.Fatalf("dedupe preview = %#v", deduped)
	}
	attachments := deduped["preview"].(map[string]any)["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("duplicate source snapshots = %#v", attachments)
	}

	f.execRaw(`UPDATE build_artifacts SET size_bytes=size_bytes+1 WHERE id=?`, artifact.ID)
	mismatch := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"task_id": f.task.ID, "build_artifact_ids": []string{artifact.ID},
	}, f.csrf)
	if mismatch["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, mismatch) != string(domain.MailErrAttachmentUnavailable) {
		t.Fatalf("hash/size mismatch = %#v", mismatch)
	}
}

func TestMailTemplateUpdateEmptyNameAndArchivedPreview(t *testing.T) {
	f := newMailAPIFixture(t)
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "名称校验", "subject_template": "s", "body_text_template": "t",
	})
	id := template["id"].(string)
	emptyName := f.request(http.MethodPut, "/api/v1/mail-templates/"+id, map[string]any{
		"expected_version": 1, "name": "",
	}, f.csrf)
	if emptyName["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, emptyName) != string(domain.MailErrTemplateInvalid) {
		t.Fatalf("empty name = %#v", emptyName)
	}
	archived := f.request(http.MethodDelete, "/api/v1/mail-templates/"+id, nil, f.csrf)
	if archived["_status"].(float64) != http.StatusOK {
		t.Fatalf("archive = %#v", archived)
	}
	preview := f.request(http.MethodPost, "/api/v1/mail-templates/"+id+"/preview", map[string]any{
		"variables": map[string]string{},
	}, f.csrf)
	if preview["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, preview) != string(domain.MailErrInvalidState) {
		t.Fatalf("archived preview = %#v", preview)
	}
}

func TestMailBindingsRejectTerminalTaskButAllowDelete(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("terminal-code")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "终态", "subject_template": "s", "body_text_template": "t",
	})
	created := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
		"default_template_id": template["id"],
	}, f.csrf)
	if created["_status"].(float64) != http.StatusCreated {
		t.Fatalf("binding = %#v", created)
	}
	bindingID := created["binding"].(map[string]any)["id"].(string)
	f.execRaw(`UPDATE tasks SET status='completed' WHERE id=?`, f.task.ID)

	update := f.request(http.MethodPut, "/api/v1/mail-bindings/"+bindingID, map[string]any{
		"enabled": false,
	}, f.csrf)
	if update["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, update) != string(domain.MailErrInvalidState) {
		t.Fatalf("terminal update = %#v", update)
	}
	create := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "workspace", "scope_id": f.task.WorkspaceID, "account_id": account["id"],
	}, f.csrf)
	if create["_status"].(float64) != http.StatusCreated {
		t.Fatalf("workspace binding should remain allowed = %#v", create)
	}
	deleted := f.request(http.MethodDelete, "/api/v1/mail-bindings/"+bindingID, nil, f.csrf)
	if deleted["_status"].(float64) != http.StatusOK {
		t.Fatalf("terminal delete = %#v", deleted)
	}
	if count := f.auditCount("mail.binding.delete", bindingID); count != 1 {
		t.Fatalf("binding delete audits = %d", count)
	}

	recreate := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": account["id"],
	}, f.csrf)
	if recreate["_status"].(float64) != http.StatusConflict {
		t.Fatalf("terminal task binding create = %#v", recreate)
	}
}

func TestMailOwnerMutationsAreAuditedWithoutCredential(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("audit-secret-code")
	accountID := account["id"].(string)
	f.request(http.MethodPut, "/api/v1/mail-accounts/"+accountID, map[string]any{"authorization_code": "audit-rotated-code"}, f.csrf)
	f.request(http.MethodPost, "/api/v1/mail-accounts/"+accountID+"/test", map[string]any{}, f.csrf)
	f.request(http.MethodPut, "/api/v1/mail-accounts/"+accountID, map[string]any{"status": "disabled"}, f.csrf)

	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "审计", "subject_template": "s", "body_text_template": "t",
	})
	templateID := template["id"].(string)
	f.request(http.MethodPut, "/api/v1/mail-templates/"+templateID, map[string]any{
		"expected_version": 1, "description": "audited",
	}, f.csrf)
	f.request(http.MethodDelete, "/api/v1/mail-templates/"+templateID, nil, f.csrf)

	binding := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{
		"scope_type": "task", "scope_id": f.task.ID, "account_id": accountID,
	}, f.csrf)
	bindingID := binding["binding"].(map[string]any)["id"].(string)
	f.request(http.MethodPut, "/api/v1/mail-bindings/"+bindingID, map[string]any{"enabled": false}, f.csrf)
	f.request(http.MethodDelete, "/api/v1/mail-bindings/"+bindingID, nil, f.csrf)
	f.request(http.MethodDelete, "/api/v1/mail-accounts/"+accountID, nil, f.csrf)

	expectations := map[string]int{
		"mail.account.create": 1, "mail.account.update": 2, "mail.account.credential_rotate": 1,
		"mail.account.test": 1, "mail.account.disable": 1, "mail.account.delete": 1,
		"mail.template.create": 1, "mail.template.update": 1, "mail.template.archive": 1,
		"mail.binding.create": 1, "mail.binding.update": 1, "mail.binding.delete": 1,
	}
	for action, want := range expectations {
		if got := f.auditCount(action, ""); got != want {
			t.Fatalf("%s audits = %d, want %d", action, got, want)
		}
	}
	if count := f.countAuditContaining("audit-secret-code"); count != 0 {
		t.Fatalf("authorization code leaked to audit: %d", count)
	}
	if count := f.countAuditContaining("audit-rotated-code"); count != 0 {
		t.Fatalf("rotated code leaked to audit: %d", count)
	}
}

func TestMailBindingNotFoundUsesStableCode(t *testing.T) {
	f := newMailAPIFixture(t)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var body map[string]any
		if method == http.MethodPut {
			body = f.request(method, "/api/v1/mail-bindings/mail_binding_missing", map[string]any{}, f.csrf)
		} else {
			body = f.request(method, "/api/v1/mail-bindings/mail_binding_missing", nil, f.csrf)
		}
		if body["_status"].(float64) != http.StatusNotFound ||
			mailErrorCode(t, body) != string(domain.MailErrBindingNotFound) {
			t.Fatalf("%s missing binding = %#v", method, body)
		}
	}
}

func TestMailValidationErrorsUseAccountInvalid(t *testing.T) {
	f := newMailAPIFixture(t)
	body := f.request(http.MethodPost, "/api/v1/mail-accounts", map[string]any{
		"name": "坏地址", "address": "not-an-email",
	}, f.csrf)
	if body["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, body) != string(domain.MailErrAccountInvalid) {
		t.Fatalf("invalid account = %#v", body)
	}
	missingName := f.request(http.MethodPost, "/api/v1/mail-accounts", map[string]any{
		"address": "qa@example.com",
	}, f.csrf)
	if missingName["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, missingName) != string(domain.MailErrAccountInvalid) {
		t.Fatalf("missing name = %#v", missingName)
	}
}

func TestMailPreviewAllowsBracesFromVariableValues(t *testing.T) {
	f := newMailAPIFixture(t)
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "花括号",
		"subject_template": "{{test.scope}}", "body_text_template": "{{test.scope}}",
		"body_html_template": `<p>{{test.scope}}</p>`,
	})
	preview := f.request(http.MethodPost, "/api/v1/mail-templates/"+template["id"].(string)+"/preview", map[string]any{
		"variables": map[string]string{"test.scope": "literal {{braces}}"},
	}, f.csrf)
	if preview["_status"].(float64) != http.StatusOK {
		t.Fatalf("braces preview = %#v", preview)
	}
	body := preview["preview"].(map[string]any)
	if !strings.Contains(body["body_text"].(string), "{{braces}}") ||
		!strings.Contains(body["body_html"].(string), "{{braces}}") {
		t.Fatalf("braces lost = %#v", body)
	}
}

// asyncRequest performs one Owner API call without calling testing.T from the
// goroutine, so a blocked connection test can race with a configuration change.
func (f *mailAPIFixture) asyncRequest(method, path string, payload any) <-chan map[string]any {
	result := make(chan map[string]any, 1)
	go func() {
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		request, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(body))
		if err != nil {
			result <- map[string]any{"_error": err.Error()}
			return
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("X-CSRF-Token", f.csrf)
		response, err := f.client.Do(request)
		if err != nil {
			result <- map[string]any{"_error": err.Error()}
			return
		}
		defer response.Body.Close()
		decoded := map[string]any{}
		_ = json.NewDecoder(response.Body).Decode(&decoded)
		decoded["_status"] = float64(response.StatusCode)
		result <- decoded
	}()
	return result
}

func TestMailConnectionTestDiscardsStaleResult(t *testing.T) {
	tests := []struct {
		name           string
		mutation       map[string]any
		expectedStatus domain.MailAccountStatus
	}{
		{"disable", map[string]any{"status": "disabled"}, domain.MailAccountDisabled},
		{"host", map[string]any{"smtp_host": domain.MailBackupSMTPHost}, domain.MailAccountUnverified},
		{"username", map[string]any{"smtp_username": "changed@example.com"}, domain.MailAccountUnverified},
		{"rotate", map[string]any{"authorization_code": "rotated-during-test"}, domain.MailAccountUnverified},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newMailAPIFixture(t)
			account := f.createAccount("race-code")
			id := account["id"].(string)
			started := make(chan struct{})
			release := make(chan struct{})
			f.tester.beforeTest = func() {
				close(started)
				<-release
			}
			done := f.asyncRequest(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{})
			<-started
			mutation := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, test.mutation, f.csrf)
			if mutation["_status"].(float64) != http.StatusOK {
				t.Fatalf("mutation = %#v", mutation)
			}
			close(release)
			body := <-done
			if body["_status"].(float64) != http.StatusOK {
				t.Fatalf("test response = %#v", body)
			}
			if body["applied"] != false || body["ok"] != false {
				t.Fatalf("stale test result was applied = %#v", body)
			}
			responseStatus := body["account"].(map[string]any)["status"]
			if responseStatus != string(test.expectedStatus) {
				t.Fatalf("response status = %#v, want %s", responseStatus, test.expectedStatus)
			}
			stored, err := f.database.MailAccount(f.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != test.expectedStatus {
				t.Fatalf("stored status = %s, want %s", stored.Status, test.expectedStatus)
			}
		})
	}
}

func TestDisabledMailAccountCannotBeImplicitlyReenabled(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("disabled-rotate-code")
	id := account["id"].(string)
	disabled := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{"status": "disabled"}, f.csrf)
	if disabled["account"].(map[string]any)["status"] != string(domain.MailAccountDisabled) {
		t.Fatalf("disable = %#v", disabled)
	}

	rotated := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"authorization_code": "disabled-rotated-code",
	}, f.csrf)
	if rotated["_status"].(float64) != http.StatusOK ||
		rotated["account"].(map[string]any)["status"] != string(domain.MailAccountDisabled) {
		t.Fatalf("rotation re-enabled account = %#v", rotated)
	}
	blocked := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
	if blocked["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, blocked) != string(domain.MailErrAccountDisabled) {
		t.Fatalf("disabled test after rotation = %#v", blocked)
	}

	hostChanged := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"smtp_host": domain.MailBackupSMTPHost,
	}, f.csrf)
	if hostChanged["account"].(map[string]any)["status"] != string(domain.MailAccountDisabled) {
		t.Fatalf("host change re-enabled account = %#v", hostChanged)
	}
	if again := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf); again["_status"].(float64) != http.StatusConflict {
		t.Fatalf("disabled test after host change = %#v", again)
	}

	usernameChanged := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{
		"smtp_username": "disabled-alt@example.com",
	}, f.csrf)
	if usernameChanged["account"].(map[string]any)["status"] != string(domain.MailAccountDisabled) {
		t.Fatalf("username change re-enabled account = %#v", usernameChanged)
	}
	if again := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf); again["_status"].(float64) != http.StatusConflict {
		t.Fatalf("disabled test after username change = %#v", again)
	}

	enabled := f.request(http.MethodPut, "/api/v1/mail-accounts/"+id, map[string]any{"status": "enabled"}, f.csrf)
	if enabled["_status"].(float64) != http.StatusOK ||
		enabled["account"].(map[string]any)["status"] != string(domain.MailAccountUnverified) {
		t.Fatalf("explicit enable = %#v", enabled)
	}
	success := f.request(http.MethodPost, "/api/v1/mail-accounts/"+id+"/test", map[string]any{}, f.csrf)
	if success["ok"] != true || success["account"].(map[string]any)["status"] != string(domain.MailAccountOK) {
		t.Fatalf("test after explicit enable = %#v", success)
	}
}
