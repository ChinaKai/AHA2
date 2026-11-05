package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
)

type mailBindingPayload struct {
	ScopeType         string   `json:"scope_type"`
	ScopeID           string   `json:"scope_id"`
	AccountID         string   `json:"account_id"`
	DefaultTemplateID *string  `json:"default_template_id"`
	ApprovalMode      string   `json:"approval_mode"`
	AllowedRecipients []string `json:"allowed_recipients"`
	AutoSendEnabled   bool     `json:"auto_send_enabled"`
	Enabled           *bool    `json:"enabled"`
}

func (s *Server) listMailBindings(writer http.ResponseWriter, request *http.Request) {
	scopeType := domain.MailBindingScopeType(strings.TrimSpace(request.URL.Query().Get("scope_type")))
	scopeID := strings.TrimSpace(request.URL.Query().Get("scope_id"))
	if !scopeType.Valid() || scopeID == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "scope_type 与 scope_id 必填")
		return
	}
	bindings, err := s.store.MailBindings(request.Context(), scopeType, scopeID, false, 100)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件绑定失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "bindings": bindings})
}

// mailBindingScopeProjectID validates that the binding target exists and
// returns the Project id used for template visibility.
func (s *Server) mailBindingScopeProjectID(ctx context.Context, scopeType domain.MailBindingScopeType, scopeID string) (string, bool, error) {
	switch scopeType {
	case domain.MailBindingScopeProject:
		project, err := s.store.Project(ctx, scopeID)
		if err != nil {
			return "", false, err
		}
		return project.ID, true, nil
	case domain.MailBindingScopeWorkspace:
		workspace, err := s.store.Workspace(ctx, scopeID)
		if err != nil {
			return "", false, err
		}
		return workspace.ProjectID, true, nil
	case domain.MailBindingScopeTask:
		task, err := s.store.Task(ctx, scopeID)
		if err != nil {
			return "", false, err
		}
		return task.ProjectID, true, nil
	default:
		return "", false, nil
	}
}

func (s *Server) mailBindingTemplateVisible(ctx context.Context, templateID, projectID string) (bool, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return true, nil
	}
	template, err := s.store.MailTemplate(ctx, templateID)
	if err != nil {
		return false, err
	}
	if template.Status != domain.MailTemplateStatusActive {
		return false, nil
	}
	if template.Scope == domain.MailTemplateScopeProject && template.ProjectID != projectID {
		return false, nil
	}
	return true, nil
}

func validateMailBindingRecipients(recipients []string) ([]string, error) {
	return mailpkg.NormalizeRecipients(recipients)
}

func (s *Server) createMailBinding(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	var payload mailBindingPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	scopeType := domain.MailBindingScopeType(strings.TrimSpace(payload.ScopeType))
	scopeID := strings.TrimSpace(payload.ScopeID)
	accountID := strings.TrimSpace(payload.AccountID)
	if !scopeType.Valid() || scopeID == "" || accountID == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "作用域与账号必填")
		return
	}
	projectID, exists, err := s.mailBindingScopeProjectID(ctx, scopeType, scopeID)
	if err != nil || !exists {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "绑定作用域不存在")
		return
	}
	if _, err := s.store.MailAccount(ctx, accountID); err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	defaultTemplateID := ""
	if payload.DefaultTemplateID != nil {
		defaultTemplateID = strings.TrimSpace(*payload.DefaultTemplateID)
	}
	visible, err := s.mailBindingTemplateVisible(ctx, defaultTemplateID, projectID)
	if err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	if !visible {
		writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不在该作用域可见")
		return
	}
	mode := domain.MailApprovalMode(strings.TrimSpace(payload.ApprovalMode))
	if mode == "" {
		mode = domain.MailApprovalAlways
	}
	if mode != domain.MailApprovalAlways {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "一期只支持每封审批")
		return
	}
	if payload.AutoSendEnabled {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "一期不允许自动发送")
		return
	}
	recipients, err := validateMailBindingRecipients(payload.AllowedRecipients)
	if err != nil {
		writeMailFailure(writer, err, "白名单收件人格式不正确")
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item := domain.MailBinding{
		ID: domain.NewID("mail_binding"), ScopeType: scopeType, ScopeID: scopeID,
		AccountID: accountID, DefaultTemplateID: defaultTemplateID,
		ApprovalMode: mode, AllowedRecipients: recipients, AutoSendEnabled: false,
		Enabled: enabled, CreatedBy: mailOwnerID(request),
	}
	created, err := s.store.CreateMailBinding(ctx, item)
	if err != nil {
		if isUniqueNameError(err) {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "该作用域已绑定此账号")
			return
		}
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "邮件绑定参数不正确")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "binding": created})
}

func (s *Server) updateMailBinding(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	id := request.PathValue("id")
	existing, err := s.store.MailBinding(ctx, id)
	if err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrInvalidState, "邮件绑定不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件绑定失败")
		return
	}
	var payload mailBindingPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	projectID, exists, err := s.mailBindingScopeProjectID(ctx, existing.ScopeType, existing.ScopeID)
	if err != nil || !exists {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "绑定作用域不存在")
		return
	}
	accountID := strings.TrimSpace(payload.AccountID)
	if accountID == "" {
		accountID = existing.AccountID
	}
	if _, err := s.store.MailAccount(ctx, accountID); err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	templateID := existing.DefaultTemplateID
	if payload.DefaultTemplateID != nil {
		templateID = strings.TrimSpace(*payload.DefaultTemplateID)
	}
	visible, err := s.mailBindingTemplateVisible(ctx, templateID, projectID)
	if err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	if !visible {
		writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不在该作用域可见")
		return
	}
	mode := domain.MailApprovalMode(strings.TrimSpace(payload.ApprovalMode))
	if mode == "" {
		mode = existing.ApprovalMode
	}
	if mode != domain.MailApprovalAlways {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "一期只支持每封审批")
		return
	}
	if payload.AutoSendEnabled {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "一期不允许自动发送")
		return
	}
	recipients := existing.AllowedRecipients
	if payload.AllowedRecipients != nil {
		recipients, err = validateMailBindingRecipients(payload.AllowedRecipients)
		if err != nil {
			writeMailFailure(writer, err, "白名单收件人格式不正确")
			return
		}
	}
	enabled := existing.Enabled
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item := domain.MailBinding{
		ID: existing.ID, ScopeType: existing.ScopeType, ScopeID: existing.ScopeID,
		AccountID: accountID, DefaultTemplateID: templateID, ApprovalMode: mode,
		AllowedRecipients: recipients, AutoSendEnabled: false, Enabled: enabled,
		CreatedBy: existing.CreatedBy, CreatedAt: existing.CreatedAt,
	}
	if err := s.store.UpdateMailBinding(ctx, item); err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrInvalidState, "邮件绑定不存在")
			return
		}
		if isUniqueNameError(err) {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "该作用域已绑定此账号")
			return
		}
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "邮件绑定参数不正确")
		return
	}
	updated, err := s.store.MailBinding(ctx, id)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件绑定失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "binding": updated})
}

func (s *Server) deleteMailBinding(writer http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteMailBinding(request.Context(), request.PathValue("id")); err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrInvalidState, "邮件绑定不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "删除邮件绑定失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
