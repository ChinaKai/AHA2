package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func petMailToolCall(t *testing.T, fixture *petTaskFixture, name, args string) string {
	t.Helper()
	raw, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	return raw
}

func petMailToolError(t *testing.T, fixture *petTaskFixture, name, args string) error {
	t.Helper()
	_, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(args))
	if err == nil {
		t.Fatalf("%s(%s) unexpectedly succeeded", name, args)
	}
	return err
}

func decodePetMailAccountView(t *testing.T, raw string) PetMailAccountView {
	t.Helper()
	var view PetMailAccountView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode mail account view %q: %v", raw, err)
	}
	return view
}

func decodePetMailTemplate(t *testing.T, raw string) domain.MailTemplate {
	t.Helper()
	var item domain.MailTemplate
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("decode mail template %q: %v", raw, err)
	}
	return item
}

func decodePetMailBinding(t *testing.T, raw string) domain.MailBinding {
	t.Helper()
	var item domain.MailBinding
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("decode mail binding %q: %v", raw, err)
	}
	return item
}

func decodePetTaskMailView(t *testing.T, raw string) PetTaskMailView {
	t.Helper()
	var view PetTaskMailView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode task mail view %q: %v", raw, err)
	}
	return view
}

type petMailAuditRow struct {
	Action string
	Data   string
}

func petMailAuditRows(t *testing.T, databasePath string) []petMailAuditRow {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT action, data_json FROM audit_events WHERE action LIKE 'pet.mail%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]petMailAuditRow, 0)
	for rows.Next() {
		var row petMailAuditRow
		if err := rows.Scan(&row.Action, &row.Data); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func createPetMailTestTask(t *testing.T, fixture *petTaskFixture, name string) domain.Task {
	t.Helper()
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: name + " Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: name + " Task",
		Request: "等待邮件", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPetMailResourcesRequireManageSystem(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, false)

	tools := []string{
		"aha_mail_resources", "aha_create_mail_account", "aha_update_mail_account",
		"aha_create_mail_template", "aha_update_mail_template",
		"aha_create_mail_binding", "aha_update_mail_binding",
		"aha_delete_mail_resource", "aha_task_mail", "aha_update_task_mail",
	}
	for _, name := range tools {
		_, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "主人未开启") {
			t.Fatalf("%s without manage_system err=%v", name, err)
		}
	}
	if _, err := fixture.service.PetMailResources(fixture.ctx, PetMailResourcesInput{}); err == nil ||
		!strings.Contains(err.Error(), "管理系统设置权限") {
		t.Fatalf("service mail resources without manage_system err=%v", err)
	}
	if _, err := fixture.service.PetCreateMailAccount(fixture.ctx, PetMailAccountInput{}); err == nil ||
		!strings.Contains(err.Error(), "管理系统设置权限") {
		t.Fatalf("service create account without manage_system err=%v", err)
	}
	if _, err := fixture.service.PetTaskMail(fixture.ctx, "missing"); err == nil ||
		!strings.Contains(err.Error(), "管理系统设置权限") {
		t.Fatalf("service task mail without manage_system err=%v", err)
	}
}

func TestPetMailResourcesAccountLifecycle(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	createdRaw := petMailToolCall(t, fixture, "aha_create_mail_account",
		`{"id":"mail-acc-test","name":"测试邮箱","from_address":"pet-test@example.com","display_name":"宠物邮箱","aliases":["alias-test@example.com"]}`)
	for _, forbidden := range []string{"credential_ref", `"password":`, "authorization_code", "auth_code"} {
		if strings.Contains(createdRaw, forbidden) {
			t.Fatalf("create response leaked %s: %s", forbidden, createdRaw)
		}
	}
	created := decodePetMailAccountView(t, createdRaw)
	if created.ID != "mail-acc-test" || created.FromAddress != "pet-test@example.com" ||
		created.DisplayName != "宠物邮箱" || created.Status != string(domain.MailAccountUnverified) ||
		created.PasswordConfigured || created.SMTPHost != domain.MailDefaultSMTPHost ||
		created.SMTPPort != domain.MailDefaultSMTPPort || created.SMTPSecurity != domain.MailSMTPSecuritySSL {
		t.Fatalf("created account=%#v", created)
	}
	if len(created.Aliases) != 1 || created.Aliases[0] != "alias-test@example.com" {
		t.Fatalf("created aliases=%v", created.Aliases)
	}

	listRaw := petMailToolCall(t, fixture, "aha_mail_resources", `{}`)
	for _, forbidden := range []string{"credential_ref", `"password":`, "authorization_code", "auth_code"} {
		if strings.Contains(listRaw, forbidden) {
			t.Fatalf("mail list leaked %s: %s", forbidden, listRaw)
		}
	}
	var listed PetMailResourcesView
	if err := json.Unmarshal([]byte(listRaw), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Accounts) != 1 || listed.Accounts[0].ID != "mail-acc-test" ||
		listed.Accounts[0].PasswordConfigured {
		t.Fatalf("listed accounts=%#v", listed.Accounts)
	}

	updated := decodePetMailAccountView(t, petMailToolCall(t, fixture,
		"aha_update_mail_account",
		`{"account_id":"mail-acc-test","display_name":"改名后的邮箱"}`))
	if updated.DisplayName != "改名后的邮箱" || updated.FromAddress != "pet-test@example.com" {
		t.Fatalf("updated account=%#v", updated)
	}

	// A credential-free account may still change its allowlisted SMTP target;
	// that resets verification but keeps the account usable.
	updated = decodePetMailAccountView(t, petMailToolCall(t, fixture,
		"aha_update_mail_account",
		`{"account_id":"mail-acc-test","smtp_host":"smtphz.qiye.163.com"}`))
	if updated.SMTPHost != domain.MailBackupSMTPHost || updated.Status != string(domain.MailAccountUnverified) {
		t.Fatalf("host update=%#v", updated)
	}

	audits := petMailAuditRows(t, fixture.databasePath)
	foundCreate, foundUpdate := false, false
	for _, row := range audits {
		if strings.Contains(row.Data, "credential_ref") || strings.Contains(row.Data, `"password"`) {
			t.Fatalf("audit %s leaked credential material: %s", row.Action, row.Data)
		}
		if row.Action == "pet.mail_account.create" {
			foundCreate = true
		}
		if row.Action == "pet.mail_account.update" {
			foundUpdate = true
		}
	}
	if !foundCreate || !foundUpdate {
		t.Fatalf("account audit actions missing: %#v", audits)
	}
}

func TestPetMailResourcesTemplateVersions(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	created := decodePetMailTemplate(t, petMailToolCall(t, fixture, "aha_create_mail_template",
		`{"id":"mail-tpl-test","scope":"global","name":"测试模板","subject_template":"任务 {{task.title}}","body_text_template":"第一版 {{task.id}}","default_to":["dest@example.com"]}`))
	if created.ID != "mail-tpl-test" || created.CurrentVersion != 1 ||
		created.Scope != domain.MailTemplateScopeGlobal || created.Status != domain.MailTemplateStatusActive {
		t.Fatalf("created template=%#v", created)
	}

	updated := decodePetMailTemplate(t, petMailToolCall(t, fixture, "aha_update_mail_template",
		`{"template_id":"mail-tpl-test","expected_version":1,"body_text_template":"第二版 {{task.id}}"}`))
	if updated.CurrentVersion != 2 || updated.BodyTextTemplate != "第二版 {{task.id}}" {
		t.Fatalf("updated template=%#v", updated)
	}
	versionOne, err := fixture.database.MailTemplateVersion(fixture.ctx, "mail-tpl-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	if versionOne.BodyTextTemplate != "第一版 {{task.id}}" || versionOne.Version != 1 {
		t.Fatalf("immutable version 1=%#v", versionOne)
	}

	// A stale expected_version must not overwrite the newer version.
	staleErr := petMailToolError(t, fixture, "aha_update_mail_template",
		`{"template_id":"mail-tpl-test","expected_version":1,"body_text_template":"冲突版本"}`)
	if !strings.Contains(staleErr.Error(), "mail_template_version_conflict") {
		t.Fatalf("stale update err=%v", staleErr)
	}
	stored, err := fixture.database.MailTemplate(fixture.ctx, "mail-tpl-test")
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentVersion != 2 || stored.BodyTextTemplate != "第二版 {{task.id}}" {
		t.Fatalf("stale update changed template: %#v", stored)
	}

	// Template bodies are not credentials and are allowed in responses/audits as
	// field names only.
	for _, row := range petMailAuditRows(t, fixture.databasePath) {
		if strings.Contains(row.Data, "第一版") || strings.Contains(row.Data, "第二版") {
			t.Fatalf("template audit %s leaked body content: %s", row.Action, row.Data)
		}
		if strings.Contains(row.Data, "credential_ref") {
			t.Fatalf("template audit %s leaked credential_ref: %s", row.Action, row.Data)
		}
	}
}

func TestPetMailResourcesBindingsAndTaskMail(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	petMailToolCall(t, fixture, "aha_create_mail_account",
		`{"id":"mail-bind-acc","name":"绑定邮箱","from_address":"bind@example.com"}`)
	template := decodePetMailTemplate(t, petMailToolCall(t, fixture, "aha_create_mail_template",
		`{"id":"mail-bind-tpl","scope":"global","name":"绑定模板","subject_template":"通知","body_text_template":"正文"}`))
	task := createPetMailTestTask(t, fixture, "邮件绑定")

	binding := decodePetMailBinding(t, petMailToolCall(t, fixture, "aha_create_mail_binding",
		fmt.Sprintf(`{"scope_type":"project","scope_id":%q,"account_id":"mail-bind-acc","default_template_id":%q,"allowed_recipients":["ops@example.com"]}`,
			task.ProjectID, template.ID)))
	if binding.ScopeType != domain.MailBindingScopeProject || binding.ScopeID != task.ProjectID ||
		binding.AccountID != "mail-bind-acc" || binding.DefaultTemplateID != template.ID ||
		binding.ApprovalMode != domain.MailApprovalAlways || !binding.Enabled {
		t.Fatalf("created binding=%#v", binding)
	}

	updated := decodePetMailBinding(t, petMailToolCall(t, fixture, "aha_update_mail_binding",
		fmt.Sprintf(`{"binding_id":%q,"enabled":false}`, binding.ID)))
	if updated.Enabled {
		t.Fatalf("binding was not disabled: %#v", updated)
	}

	taskView := decodePetTaskMailView(t, petMailToolCall(t, fixture, "aha_update_task_mail",
		fmt.Sprintf(`{"task_id":%q,"account_id":"mail-bind-acc","default_template_id":%q}`, task.ID, template.ID)))
	if taskView.TaskID != task.ID || len(taskView.Bindings) != 1 || len(taskView.Effective) != 1 {
		t.Fatalf("task mail after create=%#v", taskView)
	}
	if taskView.Bindings[0].ScopeType != domain.MailBindingScopeTask ||
		taskView.Bindings[0].ScopeID != task.ID || taskView.Bindings[0].AccountID != "mail-bind-acc" {
		t.Fatalf("task binding=%#v", taskView.Bindings[0])
	}

	readBack := decodePetTaskMailView(t, petMailToolCall(t, fixture, "aha_task_mail",
		fmt.Sprintf(`{"task_id":%q}`, task.ID)))
	if len(readBack.Bindings) != 1 || len(readBack.Effective) != 1 {
		t.Fatalf("task mail readback=%#v", readBack)
	}

	disabled := decodePetTaskMailView(t, petMailToolCall(t, fixture, "aha_update_task_mail",
		fmt.Sprintf(`{"task_id":%q,"binding_id":%q,"enabled":false}`, task.ID, readBack.Bindings[0].ID)))
	if len(disabled.Bindings) != 1 || disabled.Bindings[0].Enabled {
		t.Fatalf("task binding was not disabled: %#v", disabled)
	}
	if len(disabled.Effective) != 0 {
		t.Fatalf("disabled task binding still effective: %#v", disabled.Effective)
	}

	// Listing with task_id returns the effective bindings and never credentials.
	listRaw := petMailToolCall(t, fixture, "aha_mail_resources", fmt.Sprintf(`{"task_id":%q}`, task.ID))
	if !strings.Contains(listRaw, `"task_id"`) || strings.Contains(listRaw, "credential_ref") {
		t.Fatalf("task mail list=%s", listRaw)
	}

	// Binding to a missing scope/account is rejected.
	for _, args := range []string{
		fmt.Sprintf(`{"scope_type":"project","scope_id":"missing-project","account_id":"mail-bind-acc"}`),
		fmt.Sprintf(`{"scope_type":"task","scope_id":"missing-task","account_id":"mail-bind-acc"}`),
		fmt.Sprintf(`{"scope_type":"workspace","scope_id":%q,"account_id":"missing-account"}`, task.WorkspaceID),
	} {
		err := petMailToolError(t, fixture, "aha_create_mail_binding", args)
		if !strings.Contains(err.Error(), "mail_") {
			t.Fatalf("invalid binding %s err=%v", args, err)
		}
	}
}

func TestPetMailResourcesRejectOwnerCredentialFields(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	const secret = "super-secret-credential"
	fields := []string{"password", "auth_code", "credential_ref", "clear_secret", "Password", "Credential_Ref"}
	for _, field := range fields {
		args := fmt.Sprintf(
			`{"id":"mail-cred-%s","name":"x","from_address":"cred-%s@example.com","%s":%q}`,
			field, strings.ToLower(field), field, secret,
		)
		err := petMailToolError(t, fixture, "aha_create_mail_account", args)
		if !strings.Contains(err.Error(), "mail_credential_owner_only") {
			t.Fatalf("create with %s err=%v", field, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error for %s leaked secret: %v", field, err)
		}
	}

	petMailToolCall(t, fixture, "aha_create_mail_account",
		`{"id":"mail-cred-target","name":"目标账号","from_address":"target@example.com"}`)
	for _, field := range fields {
		args := fmt.Sprintf(`{"account_id":"mail-cred-target",%q:%q}`, field, secret)
		err := petMailToolError(t, fixture, "aha_update_mail_account", args)
		if !strings.Contains(err.Error(), "mail_credential_owner_only") {
			t.Fatalf("update with %s err=%v", field, err)
		}
	}

	nested := []struct {
		name string
		tool string
		args string
	}{
		{"account-nested", "aha_create_mail_account",
			fmt.Sprintf(`{"name":"x","from_address":"nested@example.com","extra":{"Password":%q}}`, secret)},
		{"template-nested", "aha_create_mail_template",
			fmt.Sprintf(`{"name":"嵌套模板","subject_template":"s","body_text_template":"b","meta":{"credential_ref":%q}}`, secret)},
		{"binding-nested", "aha_create_mail_binding",
			fmt.Sprintf(`{"scope_type":"project","scope_id":"p","account_id":"a","options":[{"clear_secret":%q}]}`, secret)},
		{"delete-nested", "aha_delete_mail_resource",
			fmt.Sprintf(`{"kind":"binding","resource_id":"x","reason":"清理测试绑定","extra":{"auth_code":%q}}`, secret)},
	}
	for _, item := range nested {
		err := petMailToolError(t, fixture, item.tool, item.args)
		if !strings.Contains(err.Error(), "mail_credential_owner_only") {
			t.Fatalf("%s err=%v", item.name, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s leaked secret: %v", item.name, err)
		}
	}

	listRaw := petMailToolCall(t, fixture, "aha_mail_resources", `{}`)
	for _, forbidden := range []string{secret, "mail-cred-password", "mail-cred-credential_ref"} {
		if strings.Contains(listRaw, forbidden) {
			t.Fatalf("mail list contains %q: %s", forbidden, listRaw)
		}
	}
	if !strings.Contains(listRaw, "mail-cred-target") {
		t.Fatalf("expected surviving account in list: %s", listRaw)
	}
	for _, row := range petMailAuditRows(t, fixture.databasePath) {
		if strings.Contains(row.Data, secret) {
			t.Fatalf("audit %s leaked secret: %s", row.Action, row.Data)
		}
	}
}

func TestPetMailResourcesLockedCredentialTarget(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	created := decodePetMailAccountView(t, petMailToolCall(t, fixture, "aha_create_mail_account",
		`{"id":"mail-locked","name":"锁定邮箱","from_address":"locked@example.com"}`))
	if created.PasswordConfigured {
		t.Fatalf("new account unexpectedly has a credential: %#v", created)
	}
	if err := fixture.database.SetMailAccountCredential(fixture.ctx, "mail-locked", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	credentialRef, err := fixture.database.MailAccountCredentialRef(fixture.ctx, "mail-locked")
	if err != nil || credentialRef == "" {
		t.Fatalf("credential ref=%q err=%v", credentialRef, err)
	}
	before, err := fixture.database.MailAccount(fixture.ctx, "mail-locked")
	if err != nil {
		t.Fatal(err)
	}
	if !before.CredentialConfigured {
		t.Fatalf("credential was not recorded: %#v", before)
	}

	blocked := []string{
		`{"account_id":"mail-locked","smtp_host":"smtphz.qiye.163.com"}`,
		`{"account_id":"mail-locked","from_address":"other@example.com"}`,
		`{"account_id":"mail-locked","smtp_username":"other@example.com"}`,
	}
	for _, args := range blocked {
		err := petMailToolError(t, fixture, "aha_update_mail_account", args)
		if !strings.Contains(err.Error(), "mail_credential_owner_only") {
			t.Fatalf("%s err=%v", args, err)
		}
		if strings.Contains(err.Error(), credentialRef) {
			t.Fatalf("%s leaked credential_ref: %v", args, err)
		}
		stored, err := fixture.database.MailAccount(fixture.ctx, "mail-locked")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored, before) {
			t.Fatalf("%s changed the account: before=%#v after=%#v", args, before, stored)
		}
		storedRef, err := fixture.database.MailAccountCredentialRef(fixture.ctx, "mail-locked")
		if err != nil || storedRef != credentialRef {
			t.Fatalf("%s changed credential_ref to %q (err=%v)", args, storedRef, err)
		}
	}

	// Non-target fields stay editable and the Owner credential survives.
	updated := decodePetMailAccountView(t, petMailToolCall(t, fixture, "aha_update_mail_account",
		`{"account_id":"mail-locked","display_name":"允许改名","name":"锁定邮箱（改名）"}`))
	if updated.DisplayName != "允许改名" || updated.Name != "锁定邮箱（改名）" ||
		!updated.PasswordConfigured {
		t.Fatalf("control update=%#v", updated)
	}
	stored, err := fixture.database.MailAccount(fixture.ctx, "mail-locked")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.CredentialConfigured || stored.Address != "locked@example.com" ||
		stored.SMTPHost != domain.MailDefaultSMTPHost {
		t.Fatalf("control update changed credential target: %#v", stored)
	}
	storedRef, err := fixture.database.MailAccountCredentialRef(fixture.ctx, "mail-locked")
	if err != nil || storedRef != credentialRef {
		t.Fatalf("control update lost credential_ref: %q err=%v", storedRef, err)
	}
	for _, row := range petMailAuditRows(t, fixture.databasePath) {
		if strings.Contains(row.Data, credentialRef) || strings.Contains(row.Data, "credential_ref") {
			t.Fatalf("audit %s leaked credential_ref: %s", row.Action, row.Data)
		}
	}
}

func TestPetMailResourcesDeleteRequiresApproval(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	petMailToolCall(t, fixture, "aha_create_mail_account",
		`{"id":"mail-del-acc","name":"待删邮箱","from_address":"delete@example.com"}`)
	template := decodePetMailTemplate(t, petMailToolCall(t, fixture, "aha_create_mail_template",
		`{"id":"mail-del-tpl","scope":"global","name":"待删模板","subject_template":"通知","body_text_template":"正文"}`))
	task := createPetMailTestTask(t, fixture, "邮件删除")
	binding := decodePetMailBinding(t, petMailToolCall(t, fixture, "aha_create_mail_binding",
		fmt.Sprintf(`{"scope_type":"project","scope_id":%q,"account_id":"mail-del-acc"}`, task.ProjectID)))

	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_delete_mail_resource",
		json.RawMessage(`{"kind":"account","resource_id":"mail-del-acc"}`)); err == nil ||
		!strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete without reason err=%v", err)
	}
	if _, err := fixture.database.MailAccount(fixture.ctx, "mail-del-acc"); err != nil {
		t.Fatalf("account disappeared without approval: %v", err)
	}
	if _, err := fixture.database.MailBinding(fixture.ctx, binding.ID); err != nil {
		t.Fatalf("binding disappeared without approval: %v", err)
	}
	if stored, err := fixture.database.MailTemplate(fixture.ctx, template.ID); err != nil ||
		stored.Status != domain.MailTemplateStatusActive {
		t.Fatalf("template changed without approval: %#v err=%v", stored, err)
	}

	// An in-use account cannot be physically deleted; the approval fails cleanly
	// and the account remains. Remove the binding first.
	blockedApprovalRaw := petMailToolCall(t, fixture, "aha_delete_mail_resource",
		`{"kind":"account","resource_id":"mail-del-acc","reason":"清理测试邮箱账号"}`)
	var blockedApproval domain.PetApproval
	if err := json.Unmarshal([]byte(blockedApprovalRaw), &blockedApproval); err != nil {
		t.Fatal(err)
	}
	if blockedApproval.Kind != "delete_mail_resource" || blockedApproval.Status != petApprovalPending {
		t.Fatalf("in-use delete approval=%#v", blockedApproval)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, blockedApproval.ID, "approve", ""); err == nil ||
		!strings.Contains(err.Error(), "只能停用") {
		t.Fatalf("in-use account delete err=%v", err)
	}
	if _, err := fixture.database.MailAccount(fixture.ctx, "mail-del-acc"); err != nil {
		t.Fatalf("in-use account was deleted: %v", err)
	}

	bindingApprovalRaw := petMailToolCall(t, fixture, "aha_delete_mail_resource",
		fmt.Sprintf(`{"kind":"binding","resource_id":%q,"reason":"清理测试邮件绑定"}`, binding.ID))
	var bindingApproval domain.PetApproval
	if err := json.Unmarshal([]byte(bindingApprovalRaw), &bindingApproval); err != nil {
		t.Fatal(err)
	}
	if bindingApproval.Kind != "delete_mail_resource" || bindingApproval.Status != petApprovalPending {
		t.Fatalf("binding delete approval=%#v", bindingApproval)
	}
	if _, err := fixture.database.MailBinding(fixture.ctx, binding.ID); err != nil {
		t.Fatalf("binding was deleted before approval: %v", err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, bindingApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.MailBinding(fixture.ctx, binding.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("approved binding deletion left binding: err=%v", err)
	}

	accountApprovalRaw := petMailToolCall(t, fixture, "aha_delete_mail_resource",
		`{"kind":"account","resource_id":"mail-del-acc","reason":"清理测试邮箱账号"}`)
	var accountApproval domain.PetApproval
	if err := json.Unmarshal([]byte(accountApprovalRaw), &accountApproval); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.MailAccount(fixture.ctx, "mail-del-acc"); err != nil {
		t.Fatalf("account was deleted before approval: %v", err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, accountApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.MailAccount(fixture.ctx, "mail-del-acc"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("approved account deletion left account: err=%v", err)
	}

	templateApprovalRaw := petMailToolCall(t, fixture, "aha_delete_mail_resource",
		`{"kind":"template","resource_id":"mail-del-tpl","reason":"清理测试邮件模板"}`)
	var templateApproval domain.PetApproval
	if err := json.Unmarshal([]byte(templateApprovalRaw), &templateApproval); err != nil {
		t.Fatal(err)
	}
	if stored, err := fixture.database.MailTemplate(fixture.ctx, "mail-del-tpl"); err != nil ||
		stored.Status != domain.MailTemplateStatusActive {
		t.Fatalf("template changed before approval: %#v err=%v", stored, err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, templateApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	// Templates are archived, never physically deleted, so history survives.
	archived, err := fixture.database.MailTemplate(fixture.ctx, "mail-del-tpl")
	if err != nil || archived.Status != domain.MailTemplateStatusArchived {
		t.Fatalf("template after approval=%#v err=%v", archived, err)
	}

	actions := map[string]bool{}
	for _, row := range petMailAuditRows(t, fixture.databasePath) {
		actions[row.Action] = true
		if strings.Contains(row.Data, "正文") || strings.Contains(row.Data, "delete@example.com") ||
			strings.Contains(row.Data, "credential_ref") {
			t.Fatalf("delete audit %s leaked content: %s", row.Action, row.Data)
		}
	}
	if !actions["pet.mail_resource.delete"] {
		t.Fatalf("missing delete audit: %v", actions)
	}
}

func TestPetMailResourcesToolRegistrationAndSchema(t *testing.T) {
	names := []string{
		"aha_mail_resources", "aha_create_mail_account", "aha_update_mail_account",
		"aha_create_mail_template", "aha_update_mail_template",
		"aha_create_mail_binding", "aha_update_mail_binding",
		"aha_delete_mail_resource", "aha_task_mail", "aha_update_task_mail",
	}
	registered := map[string]bool{}
	for _, tool := range ahaPetTools() {
		registered[tool.Name] = true
	}
	allowedTools := map[string]bool{}
	for _, name := range petMCPAllowedTools(domain.Task{ID: petTaskID}) {
		allowedTools[name] = true
	}
	allowed := domain.OwnerAssistant{PetAllowManageSystem: true}
	denied := domain.OwnerAssistant{
		PetAllowViewTasks: true, PetAllowCreateTasks: true,
		PetAllowCreateWorkspaces: true, PetAllowMessageTasks: true,
	}
	for _, name := range names {
		if !registered[name] {
			t.Fatalf("%s is not registered", name)
		}
		if !isAhaPetTool(name) {
			t.Fatalf("%s is not recognized as an AHA pet tool", name)
		}
		if !petToolAllowed(allowed, name) {
			t.Fatalf("%s is not allowed with manage_system", name)
		}
		if petToolAllowed(denied, name) {
			t.Fatalf("%s is allowed without manage_system", name)
		}
		if !allowedTools["mcp__aha2-pet__"+name] {
			t.Fatalf("%s is not exposed through the pet MCP allowlist", name)
		}
	}
	if allowedTools["mcp__aha2-pet__aha_send_mail"] {
		t.Fatal("aha_send_mail must not be registered in this round")
	}

	required := map[string][]string{
		"aha_create_mail_account":  {"name", "from_address"},
		"aha_update_mail_account":  {"account_id"},
		"aha_create_mail_template": {"name", "subject_template"},
		"aha_update_mail_template": {"template_id"},
		"aha_create_mail_binding":  {"scope_type", "scope_id", "account_id"},
		"aha_update_mail_binding":  {"binding_id"},
		"aha_delete_mail_resource": {"kind", "resource_id", "reason"},
		"aha_task_mail":            {"task_id"},
		"aha_update_task_mail":     {"task_id"},
	}
	tools := map[string]map[string]any{}
	for _, tool := range ahaPetTools() {
		tools[tool.Name] = tool.Parameters
	}
	for name, want := range required {
		params := tools[name]
		got, _ := params["required"].([]string)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s required=%v want %v", name, got, want)
		}
	}
	if _, ok := tools["aha_create_mail_account"]["properties"]; !ok {
		t.Fatal("create account schema has no properties")
	}
}
