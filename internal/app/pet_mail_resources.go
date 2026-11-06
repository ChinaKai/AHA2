package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

// PetMailAccountView is the pet-facing mail account shape. CredentialRef and
// plaintext credentials are never included; password_configured is the only
// credential-derived field.
type PetMailAccountView struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	FromAddress        string    `json:"from_address"`
	DisplayName        string    `json:"display_name"`
	Provider           string    `json:"provider"`
	SMTPHost           string    `json:"smtp_host"`
	SMTPPort           int       `json:"smtp_port"`
	SMTPSecurity       string    `json:"smtp_security"`
	SMTPUsername       string    `json:"smtp_username"`
	PasswordConfigured bool      `json:"password_configured"`
	Status             string    `json:"status"`
	Aliases            []string  `json:"aliases,omitempty"`
	LastTestAt         time.Time `json:"last_test_at,omitempty"`
	LastTestErrorCode  string    `json:"last_test_error_code,omitempty"`
	IMAPHost           string    `json:"imap_host,omitempty"`
	IMAPPort           int       `json:"imap_port,omitempty"`
	IMAPEnabled        bool      `json:"imap_enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type PetMailResourcesView struct {
	Accounts     []PetMailAccountView  `json:"accounts"`
	Templates    []domain.MailTemplate `json:"templates"`
	Bindings     []domain.MailBinding  `json:"bindings"`
	TaskID       string                `json:"task_id,omitempty"`
	TaskBindings []domain.MailBinding  `json:"task_bindings,omitempty"`
}

type PetMailResourcesInput struct {
	TaskID string `json:"task_id"`
}

type PetTaskMailView struct {
	TaskID    string               `json:"task_id"`
	Bindings  []domain.MailBinding `json:"bindings"`
	Effective []domain.MailBinding `json:"effective_bindings"`
}

type PetMailAccountInput struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id"`
	Name         *string   `json:"name"`
	FromAddress  *string   `json:"from_address"`
	DisplayName  *string   `json:"display_name"`
	Provider     *string   `json:"provider"`
	SMTPHost     *string   `json:"smtp_host"`
	SMTPPort     *int      `json:"smtp_port"`
	SMTPSecurity *string   `json:"smtp_security"`
	SMTPUsername *string   `json:"smtp_username"`
	Aliases      *[]string `json:"aliases"`
}

type PetMailTemplateInput struct {
	ID                string    `json:"id"`
	TemplateID        string    `json:"template_id"`
	Scope             string    `json:"scope"`
	ProjectID         string    `json:"project_id"`
	Name              *string   `json:"name"`
	Description       *string   `json:"description"`
	Mode              *string   `json:"mode"`
	SubjectTemplate   *string   `json:"subject_template"`
	BodyTextTemplate  *string   `json:"body_text_template"`
	BodyHTMLTemplate  *string   `json:"body_html_template"`
	DefaultTo         *[]string `json:"default_to"`
	DefaultCC         *[]string `json:"default_cc"`
	RequiredVariables *[]string `json:"required_variables"`
	ExpectedVersion   int       `json:"expected_version"`
}

type PetMailBindingInput struct {
	ID                string    `json:"id"`
	BindingID         string    `json:"binding_id"`
	ScopeType         string    `json:"scope_type"`
	ScopeID           string    `json:"scope_id"`
	AccountID         string    `json:"account_id"`
	DefaultTemplateID *string   `json:"default_template_id"`
	ApprovalMode      *string   `json:"approval_mode"`
	AllowedRecipients *[]string `json:"allowed_recipients"`
	AutoSendEnabled   *bool     `json:"auto_send_enabled"`
	Enabled           *bool     `json:"enabled"`
}

type PetUpdateTaskMailInput struct {
	TaskID            string    `json:"task_id"`
	AccountID         string    `json:"account_id"`
	BindingID         string    `json:"binding_id"`
	DefaultTemplateID *string   `json:"default_template_id"`
	ApprovalMode      *string   `json:"approval_mode"`
	AllowedRecipients *[]string `json:"allowed_recipients"`
	Enabled           *bool     `json:"enabled"`
}

type PetDeleteMailResourceInput struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Reason     string `json:"reason"`
}

// petMailCredentialFields are Owner/Secret-Store-only inputs. They are rejected
// at every nesting level before decoding so a pet can never write a credential
// through a mail tool, even if the field is otherwise ignored by the struct.
var petMailCredentialFields = map[string]bool{
	"password": true, "auth_code": true, "credential_ref": true, "clear_secret": true,
	"authorization_code": true, "clear_password": true,
}

var errPetMailCredentialOwnerOnly = fmt.Errorf(
	"mail_credential_owner_only: 宠物不能设置邮箱凭据，请由 Owner 在 Web/Secret Store 配置",
)

var errPetMailCredentialTargetLocked = fmt.Errorf(
	"mail_credential_owner_only: 该账号已有 Owner 配置的凭据，宠物不能修改 SMTP 目标或发件地址；请由 Owner 在 Web/Secret Store 中调整",
)

func rejectPetMailCredentialValue(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for field, child := range typed {
			if petMailCredentialFields[strings.ToLower(field)] {
				return errPetMailCredentialOwnerOnly
			}
			if err := rejectPetMailCredentialValue(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rejectPetMailCredentialValue(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodePetMailArgs rejects owner-only credential fields (including nested
// objects) before normal decoding.
func decodePetMailArgs(args json.RawMessage, target any) error {
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return fmt.Errorf("mail_request_invalid: 请求格式无效")
	}
	if err := rejectPetMailCredentialValue(value); err != nil {
		return err
	}
	if err := json.Unmarshal(args, target); err != nil {
		return fmt.Errorf("mail_request_invalid: 请求格式无效")
	}
	return nil
}

func petMailAccountView(item domain.MailAccount) PetMailAccountView {
	aliases := make([]string, 0, len(item.Aliases))
	for _, alias := range item.Aliases {
		if address := strings.TrimSpace(alias.Address); address != "" {
			aliases = append(aliases, address)
		}
	}
	return PetMailAccountView{
		ID: item.ID, Name: item.Name, FromAddress: item.Address,
		DisplayName: item.DisplayName, Provider: item.Provider,
		SMTPHost: item.SMTPHost, SMTPPort: item.SMTPPort,
		SMTPSecurity: item.SMTPSecurity, SMTPUsername: item.SMTPUsername,
		PasswordConfigured: item.CredentialConfigured, Status: string(item.Status),
		Aliases: aliases, LastTestAt: item.LastTestAt,
		LastTestErrorCode: item.LastTestErrorCode, IMAPHost: item.IMAPHost,
		IMAPPort: item.IMAPPort, IMAPEnabled: item.IMAPEnabled,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func (input PetMailAccountInput) applyTo(base domain.MailAccount) domain.MailAccount {
	if input.Name != nil {
		base.Name = strings.TrimSpace(*input.Name)
	}
	if input.FromAddress != nil {
		base.Address = strings.TrimSpace(*input.FromAddress)
	}
	if input.DisplayName != nil {
		base.DisplayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Provider != nil {
		base.Provider = strings.TrimSpace(*input.Provider)
	}
	if input.SMTPHost != nil {
		base.SMTPHost = strings.TrimSpace(*input.SMTPHost)
	}
	if input.SMTPPort != nil {
		base.SMTPPort = *input.SMTPPort
	}
	if input.SMTPSecurity != nil {
		base.SMTPSecurity = strings.TrimSpace(*input.SMTPSecurity)
	}
	if input.SMTPUsername != nil {
		base.SMTPUsername = strings.TrimSpace(*input.SMTPUsername)
	}
	if input.Aliases != nil {
		// The pet-facing alias list only carries addresses. Keep Owner-set
		// display names for addresses that survive the edit so a same-set
		// reorder never silently rewrites visible sender metadata.
		displayNames := make(map[string]string, len(base.Aliases))
		for _, alias := range base.Aliases {
			displayNames[strings.ToLower(strings.TrimSpace(alias.Address))] = alias.DisplayName
		}
		aliases := make([]domain.MailAccountAlias, 0, len(*input.Aliases))
		for _, raw := range *input.Aliases {
			address := strings.TrimSpace(raw)
			aliases = append(aliases, domain.MailAccountAlias{
				Address: address, DisplayName: displayNames[strings.ToLower(address)],
			})
		}
		base.Aliases = aliases
	}
	return base
}

// validatePetMailAccount mirrors the Owner HTTP validation so a pet cannot
// create an account the Owner UI could never save.
func validatePetMailAccount(item domain.MailAccount) (domain.MailAccount, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.Address = strings.TrimSpace(item.Address)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	if item.Name == "" {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 账号名称必填")
	}
	if _, err := mailpkg.NormalizeRecipient(item.Address); err != nil {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 发件地址格式不正确")
	}
	item.Provider = strings.TrimSpace(item.Provider)
	if item.Provider == "" {
		item.Provider = domain.MailProviderNeteaseQiye
	}
	if item.Provider != domain.MailProviderNeteaseQiye {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 一期只支持网易企业邮箱")
	}
	item.SMTPSecurity = strings.TrimSpace(item.SMTPSecurity)
	if item.SMTPSecurity == "" {
		item.SMTPSecurity = domain.MailSMTPSecuritySSL
	}
	if item.SMTPSecurity != domain.MailSMTPSecuritySSL {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 只允许隐式 TLS 连接")
	}
	if item.SMTPPort == 0 {
		item.SMTPPort = domain.MailDefaultSMTPPort
	}
	if item.SMTPPort != domain.MailDefaultSMTPPort {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 网易企业邮箱仅允许 465 端口")
	}
	rawHost := strings.TrimSpace(item.SMTPHost)
	if rawHost == "" {
		rawHost = domain.MailDefaultSMTPHost
	}
	host, ok := mailpkg.NormalizeSMTPHost(rawHost)
	if !ok {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: SMTP 主机必须是网易企业邮箱的主用或备用主机")
	}
	item.SMTPHost = host
	item.SMTPUsername = strings.TrimSpace(item.SMTPUsername)
	if item.SMTPUsername == "" {
		item.SMTPUsername = item.Address
	}
	if _, err := mailpkg.NormalizeRecipient(item.SMTPUsername); err != nil {
		return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: SMTP 用户名格式不正确")
	}
	aliases := make([]domain.MailAccountAlias, 0, len(item.Aliases))
	seen := map[string]bool{}
	for _, alias := range item.Aliases {
		address := strings.TrimSpace(alias.Address)
		if address == "" {
			return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 别名地址不能为空")
		}
		if _, err := mailpkg.NormalizeRecipient(address); err != nil {
			return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 别名地址格式不正确")
		}
		if strings.EqualFold(address, item.Address) {
			return domain.MailAccount{}, fmt.Errorf("mail_account_invalid: 别名不能与发件地址相同")
		}
		key := strings.ToLower(address)
		if seen[key] {
			continue
		}
		seen[key] = true
		aliases = append(aliases, domain.MailAccountAlias{
			Address: address, DisplayName: strings.TrimSpace(alias.DisplayName),
		})
	}
	item.Aliases = aliases
	return item, nil
}

func petMailAccountHasOwnerCredential(item domain.MailAccount, credentialRef string) bool {
	return item.CredentialConfigured || strings.TrimSpace(credentialRef) != ""
}

// petMailAccountTargetChanged reports whether an update would point the stored
// Owner credential at a different SMTP endpoint or sender identity. Aliases are
// part of the allowed From identity, so adding/removing one on a credentialed
// account is a target change too. The comparison is order-insensitive: the same
// address set in a different order is not an identity change.
func petMailAccountTargetChanged(before, after domain.MailAccount) bool {
	return !strings.EqualFold(before.Provider, after.Provider) ||
		!strings.EqualFold(before.Address, after.Address) ||
		!strings.EqualFold(before.SMTPHost, after.SMTPHost) ||
		before.SMTPPort != after.SMTPPort ||
		!strings.EqualFold(before.SMTPSecurity, after.SMTPSecurity) ||
		!strings.EqualFold(before.SMTPUsername, after.SMTPUsername) ||
		!sameMailAliasSet(before.Aliases, after.Aliases)
}

// sameMailAliasSet compares alias addresses case-insensitively, ignoring order
// and multiplicity beyond the stored set. Store reads are already deduplicated,
// but the pet input is compared before normalization, so check defensively.
func sameMailAliasSet(left, right []domain.MailAccountAlias) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, alias := range left {
		counts[strings.ToLower(strings.TrimSpace(alias.Address))]++
	}
	for _, alias := range right {
		key := strings.ToLower(strings.TrimSpace(alias.Address))
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}

func petMailAccountChangedFields(before, after domain.MailAccount) []string {
	changed := make([]string, 0, 10)
	if before.Name != after.Name {
		changed = append(changed, "name")
	}
	if before.Address != after.Address {
		changed = append(changed, "from_address")
	}
	if before.DisplayName != after.DisplayName {
		changed = append(changed, "display_name")
	}
	if before.Provider != after.Provider {
		changed = append(changed, "provider")
	}
	if before.SMTPHost != after.SMTPHost {
		changed = append(changed, "smtp_host")
	}
	if before.SMTPPort != after.SMTPPort {
		changed = append(changed, "smtp_port")
	}
	if before.SMTPSecurity != after.SMTPSecurity {
		changed = append(changed, "smtp_security")
	}
	if before.SMTPUsername != after.SMTPUsername {
		changed = append(changed, "smtp_username")
	}
	if !sameMailAliases(before.Aliases, after.Aliases) {
		changed = append(changed, "aliases")
	}
	if before.Status != after.Status {
		changed = append(changed, "status")
	}
	return changed
}

func sameMailAliases(left, right []domain.MailAccountAlias) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Address != right[index].Address ||
			left[index].DisplayName != right[index].DisplayName {
			return false
		}
	}
	return true
}

func petMailTemplateChangedFields(before, after domain.MailTemplate) []string {
	changed := make([]string, 0, 10)
	if before.Name != after.Name {
		changed = append(changed, "name")
	}
	if before.Description != after.Description {
		changed = append(changed, "description")
	}
	if before.Mode != after.Mode {
		changed = append(changed, "mode")
	}
	if before.SubjectTemplate != after.SubjectTemplate {
		changed = append(changed, "subject_template")
	}
	if before.BodyTextTemplate != after.BodyTextTemplate {
		changed = append(changed, "body_text_template")
	}
	if before.BodyHTMLTemplate != after.BodyHTMLTemplate {
		changed = append(changed, "body_html_template")
	}
	if !sameStringSlices(before.DefaultTo, after.DefaultTo) {
		changed = append(changed, "default_to")
	}
	if !sameStringSlices(before.DefaultCC, after.DefaultCC) {
		changed = append(changed, "default_cc")
	}
	if !sameStringSlices(before.RequiredVariables, after.RequiredVariables) {
		changed = append(changed, "required_variables")
	}
	if before.CurrentVersion != after.CurrentVersion {
		changed = append(changed, "current_version")
	}
	return changed
}

func sameStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func petMailBindingChangedFields(before, after domain.MailBinding) []string {
	changed := make([]string, 0, 8)
	if before.ScopeType != after.ScopeType {
		changed = append(changed, "scope_type")
	}
	if before.ScopeID != after.ScopeID {
		changed = append(changed, "scope_id")
	}
	if before.AccountID != after.AccountID {
		changed = append(changed, "account_id")
	}
	if before.DefaultTemplateID != after.DefaultTemplateID {
		changed = append(changed, "default_template_id")
	}
	if before.ApprovalMode != after.ApprovalMode {
		changed = append(changed, "approval_mode")
	}
	if !sameStringSlices(before.AllowedRecipients, after.AllowedRecipients) {
		changed = append(changed, "allowed_recipients")
	}
	if before.AutoSendEnabled != after.AutoSendEnabled {
		changed = append(changed, "auto_send_enabled")
	}
	if before.Enabled != after.Enabled {
		changed = append(changed, "enabled")
	}
	return changed
}

func (s *Service) auditPetMailResource(ctx context.Context, action string, details map[string]any) {
	_ = s.store.AppendAudit(
		ctx, "pet", action, "mail_resource", "", details,
		s.now().UTC().Format(time.RFC3339Nano),
	)
}

func (s *Service) PetMailResources(ctx context.Context, input PetMailResourcesInput) (PetMailResourcesView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetMailResourcesView{}, err
	}
	accounts, err := s.store.MailAccounts(ctx, 100)
	if err != nil {
		return PetMailResourcesView{}, err
	}
	templates, err := s.store.MailTemplates(ctx, "", "", 100)
	if err != nil {
		return PetMailResourcesView{}, err
	}
	bindings, err := s.store.MailBindingsRecent(ctx, 100)
	if err != nil {
		return PetMailResourcesView{}, err
	}
	view := PetMailResourcesView{
		Accounts:  make([]PetMailAccountView, 0, len(accounts)),
		Templates: templates, Bindings: bindings,
	}
	for _, account := range accounts {
		view.Accounts = append(view.Accounts, petMailAccountView(account))
	}
	taskID := strings.TrimSpace(input.TaskID)
	if taskID != "" {
		task, err := s.store.Task(ctx, taskID)
		if err != nil {
			return PetMailResourcesView{}, fmt.Errorf("mail_task_not_found: Task 不存在")
		}
		taskBindings, err := s.store.TaskMailBindings(ctx, task.ID, task.WorkspaceID, task.ProjectID, 200)
		if err != nil {
			return PetMailResourcesView{}, err
		}
		view.TaskID = task.ID
		view.TaskBindings = taskBindings
	}
	return view, nil
}

func (s *Service) PetCreateMailAccount(ctx context.Context, input PetMailAccountInput) (PetMailAccountView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetMailAccountView{}, err
	}
	item := input.applyTo(domain.MailAccount{
		ID: strings.TrimSpace(input.ID), Status: domain.MailAccountUnverified,
	})
	// A pet-created account never carries a credential and always starts
	// unverified; Store.CreateMailAccount derives the configured flag from the
	// (empty) Secret reference.
	item.CredentialRef = ""
	item.CredentialConfigured = false
	item.Status = domain.MailAccountUnverified
	validated, err := validatePetMailAccount(item)
	if err != nil {
		return PetMailAccountView{}, err
	}
	if validated.ID != "" {
		if _, err := s.store.MailAccount(ctx, validated.ID); err == nil {
			return PetMailAccountView{}, fmt.Errorf("mail_account_exists: 邮箱账号 %s 已存在", validated.ID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 读取邮箱账号失败")
		}
	}
	created, err := s.store.CreateMailAccount(ctx, validated)
	if err != nil {
		if errors.Is(err, store.ErrMailAddressInUse) {
			return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 发件地址或别名已被其他账号使用")
		}
		return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 创建邮箱账号失败")
	}
	s.auditPetMailResource(ctx, "pet.mail_account.create", map[string]any{
		"account_id": created.ID, "changed_fields": petMailAccountChangedFields(domain.MailAccount{}, created),
	})
	return petMailAccountView(created), nil
}

func (s *Service) PetUpdateMailAccount(ctx context.Context, input PetMailAccountInput) (PetMailAccountView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetMailAccountView{}, err
	}
	accountID := strings.TrimSpace(input.AccountID)
	if accountID == "" {
		return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: account_id 不能为空")
	}
	existing, err := s.store.MailAccount(ctx, accountID)
	if err != nil {
		return PetMailAccountView{}, fmt.Errorf("mail_account_not_found: 邮箱账号 %s 不存在", accountID)
	}
	credentialRef, refErr := s.store.MailAccountCredentialRef(ctx, accountID)
	if refErr != nil && !errors.Is(refErr, sql.ErrNoRows) {
		return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 读取邮箱账号失败")
	}
	candidate := input.applyTo(existing)
	candidate.Status = existing.Status
	candidate.CredentialRef = ""
	candidate.CredentialConfigured = existing.CredentialConfigured
	// Reject a credential target change before validation and before any write,
	// so a malformed target cannot produce a different error and the old
	// resource plus Owner credential stay intact.
	if petMailAccountHasOwnerCredential(existing, credentialRef) &&
		petMailAccountTargetChanged(existing, candidate) {
		return PetMailAccountView{}, errPetMailCredentialTargetLocked
	}
	validated, err := validatePetMailAccount(candidate)
	if err != nil {
		return PetMailAccountView{}, err
	}
	hostChanged := !strings.EqualFold(existing.SMTPHost, validated.SMTPHost) ||
		!strings.EqualFold(existing.SMTPUsername, validated.SMTPUsername)
	updated, err := s.store.UpdateMailAccount(ctx, validated)
	if err != nil {
		if errors.Is(err, store.ErrMailAddressInUse) {
			return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 发件地址或别名已被其他账号使用")
		}
		if errors.Is(err, sql.ErrNoRows) {
			return PetMailAccountView{}, fmt.Errorf("mail_account_not_found: 邮箱账号 %s 不存在", accountID)
		}
		return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 更新邮箱账号失败")
	}
	if hostChanged {
		// A different SMTP endpoint or principal must not inherit the old "ok".
		if err := s.store.ResetMailAccountVerification(ctx, accountID, s.now().UTC()); err != nil {
			return PetMailAccountView{}, fmt.Errorf("mail_account_invalid: 重置账号验证状态失败")
		}
	}
	stored, err := s.store.MailAccount(ctx, accountID)
	if err == nil {
		updated = stored
	}
	s.auditPetMailResource(ctx, "pet.mail_account.update", map[string]any{
		"account_id": accountID, "changed_fields": petMailAccountChangedFields(existing, updated),
	})
	return petMailAccountView(updated), nil
}

func (s *Service) PetCreateMailTemplate(ctx context.Context, input PetMailTemplateInput) (domain.MailTemplate, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return domain.MailTemplate{}, err
	}
	scope := domain.MailTemplateScope(strings.TrimSpace(input.Scope))
	if scope == "" {
		scope = domain.MailTemplateScopeGlobal
	}
	if !scope.Valid() {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 模板作用域不正确")
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if scope == domain.MailTemplateScopeProject {
		if projectID == "" {
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 项目模板需要 project_id")
		}
		if _, err := s.store.Project(ctx, projectID); err != nil {
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 项目不存在")
		}
	} else {
		projectID = ""
	}
	item := input.applyTo(domain.MailTemplate{
		ID: strings.TrimSpace(input.ID), Scope: scope, ProjectID: projectID,
		Mode:   domain.MailTemplateModeGuide,
		Status: domain.MailTemplateStatusActive, CurrentVersion: 1, CreatedBy: "pet",
	})
	if !item.Mode.Valid() {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 模板模式不正确")
	}
	if item.ID != "" {
		if _, err := s.store.MailTemplate(ctx, item.ID); err == nil {
			return domain.MailTemplate{}, fmt.Errorf("mail_template_exists: 邮件模板 %s 已存在", item.ID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 读取邮件模板失败")
		}
	}
	if strings.TrimSpace(item.Name) == "" {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 模板名称必填")
	}
	if err := mailpkg.ValidateTemplate(petMailTemplateContent(item)); err != nil {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: %w", err)
	}
	created, err := s.store.CreateMailTemplate(ctx, item)
	if err != nil {
		if isUniqueMailNameError(err) {
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 同作用域下已存在同名模板")
		}
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 创建邮件模板失败")
	}
	s.auditPetMailResource(ctx, "pet.mail_template.create", map[string]any{
		"template_id": created.ID, "changed_fields": petMailTemplateChangedFields(domain.MailTemplate{}, created),
	})
	return created, nil
}

func (input PetMailTemplateInput) applyTo(base domain.MailTemplate) domain.MailTemplate {
	if input.Name != nil {
		base.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		base.Description = strings.TrimSpace(*input.Description)
	}
	if input.Mode != nil && strings.TrimSpace(*input.Mode) != "" {
		// Empty mode means "leave unchanged"; the create path already defaults
		// new templates to guide.
		base.Mode = domain.MailTemplateMode(strings.TrimSpace(*input.Mode))
	}
	if input.SubjectTemplate != nil {
		base.SubjectTemplate = *input.SubjectTemplate
	}
	if input.BodyTextTemplate != nil {
		base.BodyTextTemplate = *input.BodyTextTemplate
	}
	if input.BodyHTMLTemplate != nil {
		base.BodyHTMLTemplate = *input.BodyHTMLTemplate
	}
	if input.DefaultTo != nil {
		base.DefaultTo = append([]string{}, (*input.DefaultTo)...)
	}
	if input.DefaultCC != nil {
		base.DefaultCC = append([]string{}, (*input.DefaultCC)...)
	}
	if input.RequiredVariables != nil {
		base.RequiredVariables = append([]string{}, (*input.RequiredVariables)...)
	}
	return base
}

func petMailTemplateContent(item domain.MailTemplate) mailpkg.Template {
	return mailpkg.Template{
		ID: item.ID, Version: item.CurrentVersion, Mode: item.Mode,
		SubjectTemplate: item.SubjectTemplate, BodyTextTemplate: item.BodyTextTemplate,
		BodyHTMLTemplate: item.BodyHTMLTemplate, RequiredVariables: item.RequiredVariables,
		DefaultTo: item.DefaultTo, DefaultCC: item.DefaultCC,
	}
}

func isUniqueMailNameError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Service) PetUpdateMailTemplate(ctx context.Context, input PetMailTemplateInput) (domain.MailTemplate, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return domain.MailTemplate{}, err
	}
	templateID := strings.TrimSpace(input.TemplateID)
	if templateID == "" {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: template_id 不能为空")
	}
	existing, err := s.store.MailTemplate(ctx, templateID)
	if err != nil {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_not_found: 邮件模板 %s 不存在", templateID)
	}
	// Scope and project are immutable: moving a template would change history
	// visibility. The pet can only edit content and defaults.
	item := input.applyTo(existing)
	if item.Mode == "" {
		// Legacy rows predating the mode column keep render behavior.
		item.Mode = domain.MailTemplateModeRender
	}
	if !item.Mode.Valid() {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 模板模式不正确")
	}
	if strings.TrimSpace(item.Name) == "" {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 模板名称必填")
	}
	if err := mailpkg.ValidateTemplate(petMailTemplateContent(item)); err != nil {
		return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: %w", err)
	}
	expectedVersion := input.ExpectedVersion
	if expectedVersion <= 0 {
		expectedVersion = existing.CurrentVersion
	}
	updated, err := s.store.UpdateMailTemplate(ctx, item, expectedVersion)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMailTemplateVersionConflict):
			return domain.MailTemplate{}, fmt.Errorf("mail_template_version_conflict: 模板已被其他编辑更新，请刷新后重试")
		case errors.Is(err, store.ErrMailTemplateArchived):
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 已归档模板不能修改")
		case errors.Is(err, sql.ErrNoRows):
			return domain.MailTemplate{}, fmt.Errorf("mail_template_not_found: 邮件模板 %s 不存在", templateID)
		case isUniqueMailNameError(err):
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 同作用域下已存在同名模板")
		default:
			return domain.MailTemplate{}, fmt.Errorf("mail_template_invalid: 更新邮件模板失败")
		}
	}
	s.auditPetMailResource(ctx, "pet.mail_template.update", map[string]any{
		"template_id": templateID, "changed_fields": petMailTemplateChangedFields(existing, updated),
	})
	return updated, nil
}

func normalizePetMailApprovalMode(value string, fallback domain.MailApprovalMode) (domain.MailApprovalMode, error) {
	mode := domain.MailApprovalMode(strings.TrimSpace(value))
	if mode == "" {
		mode = fallback
	}
	if mode == "" {
		mode = domain.MailApprovalAlways
	}
	if mode != domain.MailApprovalAlways {
		return "", fmt.Errorf("mail_binding_invalid: 一期只支持每封审批")
	}
	return mode, nil
}

func (s *Service) validatePetMailBindingTarget(ctx context.Context, scopeType domain.MailBindingScopeType, scopeID, accountID, templateID string) (string, domain.TaskStatus, error) {
	if !scopeType.Valid() {
		return "", "", fmt.Errorf("mail_binding_invalid: scope_type 不正确")
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return "", "", fmt.Errorf("mail_binding_invalid: scope_id 不能为空")
	}
	var projectID string
	var taskStatus domain.TaskStatus
	switch scopeType {
	case domain.MailBindingScopeProject:
		project, err := s.store.Project(ctx, scopeID)
		if err != nil {
			return "", "", fmt.Errorf("mail_binding_invalid: 绑定作用域不存在")
		}
		projectID = project.ID
	case domain.MailBindingScopeWorkspace:
		workspace, err := s.store.Workspace(ctx, scopeID)
		if err != nil {
			return "", "", fmt.Errorf("mail_binding_invalid: 绑定作用域不存在")
		}
		projectID = workspace.ProjectID
	case domain.MailBindingScopeTask:
		task, err := s.store.Task(ctx, scopeID)
		if err != nil {
			return "", "", fmt.Errorf("mail_binding_invalid: 绑定作用域不存在")
		}
		projectID = task.ProjectID
		taskStatus = task.Status
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", "", fmt.Errorf("mail_binding_invalid: account_id 不能为空")
	}
	if _, err := s.store.MailAccount(ctx, accountID); err != nil {
		return "", "", fmt.Errorf("mail_account_not_found: 邮箱账号 %s 不存在", accountID)
	}
	if err := s.validatePetMailTemplateVisible(ctx, templateID, projectID); err != nil {
		return "", "", err
	}
	return projectID, taskStatus, nil
}

func (s *Service) validatePetMailTemplateVisible(ctx context.Context, templateID, projectID string) error {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return nil
	}
	template, err := s.store.MailTemplate(ctx, templateID)
	if err != nil {
		return fmt.Errorf("mail_template_not_found: 邮件模板 %s 不存在", templateID)
	}
	if template.Status != domain.MailTemplateStatusActive {
		return fmt.Errorf("mail_template_invalid: 邮件模板已归档")
	}
	if template.Scope == domain.MailTemplateScopeProject && template.ProjectID != projectID {
		return fmt.Errorf("mail_template_invalid: 邮件模板不在该作用域可见")
	}
	return nil
}

func (s *Service) PetCreateMailBinding(ctx context.Context, input PetMailBindingInput) (domain.MailBinding, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return domain.MailBinding{}, err
	}
	if input.AutoSendEnabled != nil && *input.AutoSendEnabled {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 一期不允许自动发送")
	}
	scopeType := domain.MailBindingScopeType(strings.TrimSpace(input.ScopeType))
	scopeID := strings.TrimSpace(input.ScopeID)
	accountID := strings.TrimSpace(input.AccountID)
	templateID := ""
	if input.DefaultTemplateID != nil {
		templateID = strings.TrimSpace(*input.DefaultTemplateID)
	}
	_, taskStatus, err := s.validatePetMailBindingTarget(ctx, scopeType, scopeID, accountID, templateID)
	if err != nil {
		return domain.MailBinding{}, err
	}
	if scopeType == domain.MailBindingScopeTask && taskStatus.Terminal() {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 任务已结束，不能新增邮件绑定")
	}
	mode := domain.MailApprovalAlways
	if input.ApprovalMode != nil {
		if mode, err = normalizePetMailApprovalMode(*input.ApprovalMode, ""); err != nil {
			return domain.MailBinding{}, err
		}
	}
	recipients := []string{}
	if input.AllowedRecipients != nil {
		if recipients, err = mailpkg.NormalizeRecipients(*input.AllowedRecipients); err != nil {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 白名单收件人格式不正确")
		}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	item := domain.MailBinding{
		ID: strings.TrimSpace(input.ID), ScopeType: scopeType, ScopeID: scopeID,
		AccountID: accountID, DefaultTemplateID: templateID, ApprovalMode: mode,
		AllowedRecipients: recipients, AutoSendEnabled: false, Enabled: enabled,
		CreatedBy: "pet",
	}
	if item.ID != "" {
		if _, err := s.store.MailBinding(ctx, item.ID); err == nil {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_exists: 邮件绑定 %s 已存在", item.ID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 读取邮件绑定失败")
		}
	}
	created, err := s.store.CreateMailBinding(ctx, item)
	if err != nil {
		if isUniqueMailNameError(err) {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 该作用域已绑定此账号")
		}
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 创建邮件绑定失败")
	}
	s.auditPetMailResource(ctx, "pet.mail_binding.create", map[string]any{
		"binding_id": created.ID, "changed_fields": petMailBindingChangedFields(domain.MailBinding{}, created),
	})
	return created, nil
}

func (s *Service) PetUpdateMailBinding(ctx context.Context, input PetMailBindingInput) (domain.MailBinding, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return domain.MailBinding{}, err
	}
	bindingID := strings.TrimSpace(input.BindingID)
	if bindingID == "" {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: binding_id 不能为空")
	}
	existing, err := s.store.MailBinding(ctx, bindingID)
	if err != nil {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_not_found: 邮件绑定 %s 不存在", bindingID)
	}
	return s.updatePetMailBinding(ctx, existing, input)
}

func (s *Service) updatePetMailBinding(ctx context.Context, existing domain.MailBinding, input PetMailBindingInput) (domain.MailBinding, error) {
	if input.AutoSendEnabled != nil && *input.AutoSendEnabled {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 一期不允许自动发送")
	}
	scopeType := existing.ScopeType
	scopeID := existing.ScopeID
	accountID := strings.TrimSpace(input.AccountID)
	if accountID == "" {
		accountID = existing.AccountID
	}
	templateID := existing.DefaultTemplateID
	if input.DefaultTemplateID != nil {
		templateID = strings.TrimSpace(*input.DefaultTemplateID)
	}
	_, taskStatus, err := s.validatePetMailBindingTarget(ctx, scopeType, scopeID, accountID, templateID)
	if err != nil {
		return domain.MailBinding{}, err
	}
	if scopeType == domain.MailBindingScopeTask && taskStatus.Terminal() {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 任务已结束，不能修改邮件绑定")
	}
	mode := existing.ApprovalMode
	if input.ApprovalMode != nil {
		if mode, err = normalizePetMailApprovalMode(*input.ApprovalMode, existing.ApprovalMode); err != nil {
			return domain.MailBinding{}, err
		}
	}
	recipients := existing.AllowedRecipients
	if input.AllowedRecipients != nil {
		if recipients, err = mailpkg.NormalizeRecipients(*input.AllowedRecipients); err != nil {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 白名单收件人格式不正确")
		}
	}
	enabled := existing.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	item := domain.MailBinding{
		ID: existing.ID, ScopeType: scopeType, ScopeID: scopeID, AccountID: accountID,
		DefaultTemplateID: templateID, ApprovalMode: mode,
		AllowedRecipients: recipients, AutoSendEnabled: existing.AutoSendEnabled,
		Enabled: enabled, CreatedBy: existing.CreatedBy, CreatedAt: existing.CreatedAt,
	}
	if err := s.store.UpdateMailBinding(ctx, item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_not_found: 邮件绑定 %s 不存在", item.ID)
		}
		if isUniqueMailNameError(err) {
			return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 该作用域已绑定此账号")
		}
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 更新邮件绑定失败")
	}
	updated, err := s.store.MailBinding(ctx, item.ID)
	if err != nil {
		return domain.MailBinding{}, fmt.Errorf("mail_binding_invalid: 读取邮件绑定失败")
	}
	s.auditPetMailResource(ctx, "pet.mail_binding.update", map[string]any{
		"binding_id": updated.ID, "changed_fields": petMailBindingChangedFields(existing, updated),
	})
	return updated, nil
}

func (s *Service) PetTaskMail(ctx context.Context, taskID string) (PetTaskMailView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetTaskMailView{}, err
	}
	task, err := s.store.Task(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return PetTaskMailView{}, fmt.Errorf("Task 不存在")
	}
	bindings, err := s.store.MailBindings(ctx, domain.MailBindingScopeTask, task.ID, false, 100)
	if err != nil {
		return PetTaskMailView{}, err
	}
	effective, err := s.store.TaskMailBindings(ctx, task.ID, task.WorkspaceID, task.ProjectID, 200)
	if err != nil {
		return PetTaskMailView{}, err
	}
	return PetTaskMailView{TaskID: task.ID, Bindings: bindings, Effective: effective}, nil
}

func (s *Service) PetUpdateTaskMail(ctx context.Context, input PetUpdateTaskMailInput) (PetTaskMailView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetTaskMailView{}, err
	}
	task, err := s.store.Task(ctx, strings.TrimSpace(input.TaskID))
	if err != nil {
		return PetTaskMailView{}, fmt.Errorf("Task 不存在")
	}
	if task.Status.Terminal() {
		return PetTaskMailView{}, fmt.Errorf("mail_binding_invalid: 任务已结束，不能修改邮件绑定")
	}
	bindingID := strings.TrimSpace(input.BindingID)
	var existing domain.MailBinding
	found := false
	if bindingID != "" {
		existing, err = s.store.MailBinding(ctx, bindingID)
		if err != nil {
			return PetTaskMailView{}, fmt.Errorf("mail_binding_not_found: 邮件绑定 %s 不存在", bindingID)
		}
		if existing.ScopeType != domain.MailBindingScopeTask || existing.ScopeID != task.ID {
			return PetTaskMailView{}, fmt.Errorf("mail_binding_invalid: 该绑定不属于此 Task")
		}
		found = true
	} else {
		accountID := strings.TrimSpace(input.AccountID)
		if accountID == "" {
			return PetTaskMailView{}, fmt.Errorf("mail_binding_invalid: account_id 不能为空")
		}
		bindings, err := s.store.MailBindings(ctx, domain.MailBindingScopeTask, task.ID, false, 100)
		if err != nil {
			return PetTaskMailView{}, err
		}
		for _, binding := range bindings {
			if binding.AccountID == accountID {
				existing, found = binding, true
				break
			}
		}
	}
	if found {
		if _, err := s.updatePetMailBinding(ctx, existing, PetMailBindingInput{
			AccountID: input.AccountID, DefaultTemplateID: input.DefaultTemplateID,
			ApprovalMode: input.ApprovalMode, AllowedRecipients: input.AllowedRecipients,
			Enabled: input.Enabled,
		}); err != nil {
			return PetTaskMailView{}, err
		}
	} else {
		if _, err := s.PetCreateMailBinding(ctx, PetMailBindingInput{
			ScopeType: string(domain.MailBindingScopeTask), ScopeID: task.ID,
			AccountID: input.AccountID, DefaultTemplateID: input.DefaultTemplateID,
			ApprovalMode: input.ApprovalMode, AllowedRecipients: input.AllowedRecipients,
			Enabled: input.Enabled,
		}); err != nil {
			return PetTaskMailView{}, err
		}
	}
	return s.PetTaskMail(ctx, task.ID)
}

func (s *Service) PetRequestDeleteMailResource(ctx context.Context, kind, resourceID, reason string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.PetApproval{}, err
	}
	if !assistant.PetAllowManageSystem {
		return domain.PetApproval{}, fmt.Errorf("主人未开启管理系统设置权限")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return domain.PetApproval{}, fmt.Errorf("mail_resource_invalid: resource_id 不能为空")
	}
	var title, summary string
	switch kind {
	case "account":
		account, err := s.store.MailAccount(ctx, resourceID)
		if err != nil {
			return domain.PetApproval{}, fmt.Errorf("mail_account_not_found: 邮箱账号 %s 不存在", resourceID)
		}
		title = "删除邮箱账号：" + firstNonEmptyTrimmed(account.Name, account.ID)
		summary = fmt.Sprintf("宠物申请删除邮箱账号「%s」（%s）。批准后才执行。", firstNonEmptyTrimmed(account.Name, account.ID), account.ID)
	case "template":
		template, err := s.store.MailTemplate(ctx, resourceID)
		if err != nil {
			return domain.PetApproval{}, fmt.Errorf("mail_template_not_found: 邮件模板 %s 不存在", resourceID)
		}
		title = "删除邮件模板：" + firstNonEmptyTrimmed(template.Name, template.ID)
		summary = fmt.Sprintf("宠物申请删除邮件模板「%s」（%s）。模板按现有规则归档，历史版本保留。批准后才执行。", firstNonEmptyTrimmed(template.Name, template.ID), template.ID)
	case "binding":
		binding, err := s.store.MailBinding(ctx, resourceID)
		if err != nil {
			return domain.PetApproval{}, fmt.Errorf("mail_binding_not_found: 邮件绑定 %s 不存在", resourceID)
		}
		title = "删除邮件绑定：" + binding.ID
		summary = fmt.Sprintf("宠物申请删除 %s 级邮件绑定（%s）。批准后才执行。", binding.ScopeType, binding.ID)
	default:
		return domain.PetApproval{}, fmt.Errorf("mail_resource_invalid: kind 必须是 account、template 或 binding")
	}
	payload, _ := json.Marshal(map[string]string{"kind": kind, "resource_id": resourceID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_mail_resource", Title: title, Summary: summary,
		Reason: reasonText, Risk: "high", Payload: string(payload),
	})
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *Service) deleteMailResourceNow(ctx context.Context, kind, resourceID string) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	resourceID = strings.TrimSpace(resourceID)
	switch kind {
	case "account":
		credentialRef, _ := s.store.MailAccountCredentialRef(ctx, resourceID)
		if err := s.store.DeleteMailAccount(ctx, resourceID); err != nil {
			if errors.Is(err, store.ErrMailAccountInUse) {
				return fmt.Errorf("邮箱账号已有历史邮件或绑定，只能停用")
			}
			return err
		}
		if s.secrets != nil && strings.TrimSpace(credentialRef) != "" {
			_ = s.secrets.DeleteMany([]string{credentialRef})
		}
	case "template":
		// Templates are never physically deleted; archiving preserves immutable
		// versions and message history, matching the Owner API.
		return s.store.ArchiveMailTemplate(ctx, resourceID, s.now().UTC())
	case "binding":
		return s.store.DeleteMailBinding(ctx, resourceID)
	default:
		return fmt.Errorf("mail_resource_invalid: kind 必须是 account、template 或 binding")
	}
	return nil
}
