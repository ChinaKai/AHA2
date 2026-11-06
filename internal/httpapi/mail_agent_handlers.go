package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

// The Agent mail surface is deliberately narrow: it can inspect the accounts
// and templates bound to the current Task, render a preview, create a message
// that is always pending Owner approval, and read status. It cannot approve,
// reject, retry, cancel, read credentials or act on another Task's message.
type agentMailDraftInput struct {
	AccountID        string            `json:"account_id"`
	TemplateID       string            `json:"template_id"`
	To               []string          `json:"to"`
	CC               []string          `json:"cc"`
	BCC              []string          `json:"bcc"`
	Variables        map[string]string `json:"variables"`
	AttachmentIDs    []string          `json:"attachment_ids"`
	BuildArtifactIDs []string          `json:"build_artifact_ids"`
}

type agentMailSendInput struct {
	AccountID        string            `json:"account_id"`
	TemplateID       string            `json:"template_id"`
	To               []string          `json:"to"`
	CC               []string          `json:"cc"`
	BCC              []string          `json:"bcc"`
	Variables        map[string]string `json:"variables"`
	AttachmentIDs    []string          `json:"attachment_ids"`
	BuildArtifactIDs []string          `json:"build_artifact_ids"`
	IdempotencyKey   string            `json:"idempotency_key"`
}

type agentMailFailure struct {
	Status  int
	Code    domain.MailErrorCode
	Message string
}

func (failure *agentMailFailure) Error() string {
	if failure.Message == "" {
		return string(failure.Code)
	}
	return string(failure.Code) + ": " + failure.Message
}

func newAgentMailFailure(status int, code domain.MailErrorCode, message string) error {
	return &agentMailFailure{Status: status, Code: code, Message: message}
}

func writeAgentMailFailure(writer http.ResponseWriter, err error) {
	var failure *agentMailFailure
	if errors.As(err, &failure) {
		writeMailError(writer, failure.Status, failure.Code, failure.Message)
		return
	}
	var mailErr *mailpkg.Error
	if errors.As(err, &mailErr) {
		writeMailError(writer, mailErrorStatus(mailErr.Code), mailErr.Code, mailErr.Message)
		return
	}
	switch {
	case isNoRows(err):
		writeMailError(writer, http.StatusNotFound, domain.MailErrMessageNotFound, "邮件消息不存在")
	case errors.Is(err, store.ErrMailInvalidState):
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "当前状态不允许该操作")
	case errors.Is(err, store.ErrMailDigestMismatch):
		writeMailError(writer, http.StatusConflict, domain.MailErrApprovalDigestMismatch, "审批摘要与提交内容不一致")
	case errors.Is(err, app.ErrAgentCallForbidden), errors.Is(err, app.ErrAgentTurnInactive):
		writeMailError(writer, http.StatusForbidden, domain.MailErrForbidden, "当前 Agent 能力不能执行该操作")
	default:
		// Never echo an internal error: it can contain storage details.
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "邮件操作失败")
	}
}

func agentMailErrorText(err error) string {
	var failure *agentMailFailure
	if errors.As(err, &failure) {
		if failure.Message == "" {
			return string(failure.Code)
		}
		return string(failure.Code) + ": " + failure.Message
	}
	var mailErr *mailpkg.Error
	if errors.As(err, &mailErr) {
		return mailErr.Error()
	}
	switch {
	case errors.Is(err, store.ErrMailInvalidState):
		return string(domain.MailErrInvalidState)
	case errors.Is(err, app.ErrAgentCallForbidden), errors.Is(err, app.ErrAgentTurnInactive):
		return string(domain.MailErrForbidden)
	default:
		return "mail_internal_error"
	}
}

func (s *Server) agentMailCall(request *http.Request) (app.AgentCallContext, error) {
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok || strings.TrimSpace(claims.TaskID) == "" || s.app == nil {
		return app.AgentCallContext{}, newAgentMailFailure(http.StatusUnauthorized, domain.MailErrForbidden, "Agent 能力无效")
	}
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		return app.AgentCallContext{}, err
	}
	return call, nil
}

func (s *Server) agentMailBindings(ctx context.Context, call app.AgentCallContext) ([]domain.MailBinding, error) {
	return s.store.TaskMailBindings(ctx, call.Task.ID, call.Task.WorkspaceID, call.Task.ProjectID, 200)
}

func (s *Server) agentMailAccount(ctx context.Context, call app.AgentCallContext, accountID string) (domain.MailAccount, domain.MailBinding, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return domain.MailAccount{}, domain.MailBinding{}, newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrAccountNotFound, "account_id 必填")
	}
	bindings, err := s.agentMailBindings(ctx, call)
	if err != nil {
		return domain.MailAccount{}, domain.MailBinding{}, err
	}
	for _, binding := range bindings {
		if binding.AccountID != accountID {
			continue
		}
		account, err := s.store.MailAccount(ctx, accountID)
		if err != nil {
			if isNoRows(err) {
				return domain.MailAccount{}, domain.MailBinding{}, newAgentMailFailure(http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			}
			return domain.MailAccount{}, domain.MailBinding{}, err
		}
		return account, binding, nil
	}
	// Distinguish a missing account from one that exists but is outside this
	// Task's Task/Workspace/Project bindings.
	if _, err := s.store.MailAccount(ctx, accountID); err != nil {
		if isNoRows(err) {
			return domain.MailAccount{}, domain.MailBinding{}, newAgentMailFailure(http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
		}
		return domain.MailAccount{}, domain.MailBinding{}, err
	}
	return domain.MailAccount{}, domain.MailBinding{}, newAgentMailFailure(http.StatusForbidden, domain.MailErrAccountNotBound, "账号未绑定到当前任务范围")
}

func (s *Server) agentMailTemplate(ctx context.Context, call app.AgentCallContext, templateID string) (domain.MailTemplate, mailpkg.Template, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return domain.MailTemplate{}, mailpkg.Template{}, newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "template_id 必填")
	}
	item, err := s.store.MailTemplate(ctx, templateID)
	if err != nil {
		if isNoRows(err) {
			return domain.MailTemplate{}, mailpkg.Template{}, newAgentMailFailure(http.StatusNotFound, domain.MailErrTemplateNotFound, "邮件模板不存在")
		}
		return domain.MailTemplate{}, mailpkg.Template{}, err
	}
	if item.Status != domain.MailTemplateStatusActive {
		return domain.MailTemplate{}, mailpkg.Template{}, newAgentMailFailure(http.StatusNotFound, domain.MailErrTemplateNotFound, "邮件模板不存在或已归档")
	}
	if item.Scope == domain.MailTemplateScopeProject && item.ProjectID != call.Task.ProjectID {
		return domain.MailTemplate{}, mailpkg.Template{}, newAgentMailFailure(http.StatusNotFound, domain.MailErrTemplateNotFound, "邮件模板不属于当前项目")
	}
	content := mailTemplateContent(item)
	if version, err := s.store.MailTemplateVersion(ctx, item.ID, item.CurrentVersion); err == nil {
		content.SubjectTemplate = version.SubjectTemplate
		content.BodyTextTemplate = version.BodyTextTemplate
		content.BodyHTMLTemplate = version.BodyHTMLTemplate
		content.RequiredVariables = version.RequiredVariables
	}
	return item, content, nil
}

func (s *Server) agentMailServerVariables(ctx context.Context, call app.AgentCallContext) map[string]string {
	variables := s.mailServerVariables(ctx, call.Task)
	if agent, err := s.store.TaskAgent(ctx, call.Task.ID, call.Turn.AgentID); err == nil {
		name := strings.TrimSpace(agent.Title)
		if name == "" {
			name = strings.TrimSpace(agent.AgentID)
		}
		if name != "" {
			variables["agent.name"] = name
		}
	}
	return variables
}

func (s *Server) agentMailRender(ctx context.Context, call app.AgentCallContext, input agentMailDraftInput) (domain.MailAccount, domain.MailBinding, domain.MailTemplate, mailpkg.Preview, error) {
	if len(input.BCC) > 0 {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{},
			newAgentMailFailure(http.StatusForbidden, domain.MailErrForbidden, "Agent 不能设置 BCC")
	}
	account, binding, err := s.agentMailAccount(ctx, call, input.AccountID)
	if err != nil {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{}, err
	}
	if account.Status == domain.MailAccountDisabled {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{},
			newAgentMailFailure(http.StatusConflict, domain.MailErrAccountDisabled, "邮箱账号已停用")
	}
	if !account.CredentialConfigured {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{},
			newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrCredentialMissing, "邮箱账号未配置授权码")
	}
	template, content, err := s.agentMailTemplate(ctx, call, input.TemplateID)
	if err != nil {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{}, err
	}
	attachments, packageVariables, err := s.mailPreviewAttachments(ctx, call.Task.ID, input.AttachmentIDs, input.BuildArtifactIDs)
	if err != nil {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{}, err
	}
	callerVariables := make(map[string]string, len(input.Variables))
	for name, value := range input.Variables {
		callerVariables[name] = value
	}
	serverVariables := s.agentMailServerVariables(ctx, call)
	for name, value := range packageVariables {
		if name == "build.version" {
			if strings.TrimSpace(callerVariables[name]) == "" {
				callerVariables[name] = value
			}
			continue
		}
		serverVariables[name] = value
	}
	to := input.To
	if len(to) == 0 {
		to = content.DefaultTo
	}
	cc := input.CC
	if len(cc) == 0 {
		cc = content.DefaultCC
	}
	preview, err := mailpkg.Render(content, mailpkg.Input{
		Variables: callerVariables, ServerVariables: serverVariables,
		To: to, CC: cc, Attachments: attachments,
	})
	if err != nil {
		return domain.MailAccount{}, domain.MailBinding{}, domain.MailTemplate{}, mailpkg.Preview{}, err
	}
	return account, binding, template, preview, nil
}

func (s *Server) agentMailAccountsData(ctx context.Context, call app.AgentCallContext) map[string]any {
	accounts, _, defaultTemplates := s.app.MailTaskResources(ctx, call.Task)
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		aliases := make([]string, 0, len(account.Aliases))
		for _, alias := range account.Aliases {
			if strings.TrimSpace(alias.Address) != "" {
				aliases = append(aliases, strings.TrimSpace(alias.Address))
			}
		}
		item := map[string]any{
			"account_id":            account.ID,
			"from_address":          account.Address,
			"display_name":          account.DisplayName,
			"aliases":               aliases,
			"credential_configured": account.CredentialConfigured,
			"status":                string(account.Status),
		}
		if templateID := defaultTemplates[account.ID]; templateID != "" {
			item["default_template_id"] = templateID
		}
		items = append(items, item)
	}
	return map[string]any{"ok": true, "accounts": items}
}

func (s *Server) agentMailTemplatesData(ctx context.Context, call app.AgentCallContext) map[string]any {
	_, templates, _ := s.app.MailTaskResources(ctx, call.Task)
	items := make([]map[string]any, 0, len(templates))
	for _, template := range templates {
		items = append(items, map[string]any{
			"template_id": template.ID,
			"name":        template.Name,
			"version":     template.CurrentVersion,
			"scope":       string(template.Scope),
			"variables":   template.RequiredVariables,
		})
	}
	return map[string]any{"ok": true, "templates": items}
}

func (s *Server) agentMailPreviewData(ctx context.Context, call app.AgentCallContext, input agentMailDraftInput) (map[string]any, error) {
	account, _, _, preview, err := s.agentMailRender(ctx, call, input)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"account_id": account.ID,
		"preview":    preview,
	}, nil
}

func (s *Server) agentMailSendData(ctx context.Context, request *http.Request, call app.AgentCallContext, input agentMailSendInput) (map[string]any, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" || len([]rune(key)) > 128 {
		return nil, newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrInvalidState, "idempotency_key 必填且不超过 128 字符")
	}
	if existing, err := s.store.MailMessageByTaskIdempotencyKey(ctx, call.Task.ID, key); err == nil {
		return agentMailSendResult(existing, true), nil
	} else if !isNoRows(err) {
		return nil, err
	}
	if call.Task.Status.Terminal() {
		return nil, newAgentMailFailure(http.StatusConflict, domain.MailErrInvalidState, "任务已结束，不能新建邮件")
	}

	account, binding, template, preview, err := s.agentMailRender(ctx, call, agentMailDraftInput{
		AccountID: input.AccountID, TemplateID: input.TemplateID,
		To: input.To, CC: input.CC, BCC: input.BCC, Variables: input.Variables,
		AttachmentIDs: input.AttachmentIDs, BuildArtifactIDs: input.BuildArtifactIDs,
	})
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	messageID := domain.NewID("mail_message")
	attachmentsSnapshot := make([]domain.MailAttachment, 0, len(preview.Attachments))
	mailAttachmentIDs := make(map[string]string, len(preview.Attachments))
	linkSecrets := make(map[string]string)
	for index, attachment := range preview.Attachments {
		mailAttachmentID := domain.NewID("mail_attachment")
		mailAttachmentIDs[attachment.ContentID] = mailAttachmentID
		item := domain.MailAttachment{
			ID: mailAttachmentID, MessageID: messageID, Position: index,
			SourceType: domain.MailAttachmentSourceType(attachment.SourceType),
			SourceID:   attachment.SourceID,
			FileName:   attachment.FileName, MediaType: attachment.MediaType,
			SizeBytes: attachment.SizeBytes, SHA256: attachment.SHA256,
			DeliveryMode: domain.MailAttachmentDeliveryMode(attachment.DeliveryMode),
			Disposition:  domain.MailAttachmentDispositionAttachment, CreatedAt: now,
		}
		if item.DeliveryMode == domain.MailAttachmentLink {
			if strings.TrimSpace(s.mailDownloadBaseURL) == "" || !validHTTPSBase(s.mailDownloadBaseURL) {
				return nil, newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrTotalTooLarge,
					"受控下载入口未配置，无法发送大附件")
			}
			if strings.Contains(preview.BodyHTML, "cid:"+attachment.SourceID) {
				return nil, newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid,
					"受控链接附件不能作为内嵌图片引用")
			}
			token, tokenErr := newMailToken()
			if tokenErr != nil {
				return nil, tokenErr
			}
			item.LinkTokenRef = domain.MailAttachmentLinkTokenRef(mailAttachmentID)
			item.LinkTokenHash = mailLinkTokenHash(token)
			item.LinkExpiresAt = now.Add(domain.MailLinkDefaultTTL)
			item.LinkMaxDownloads = domain.MailLinkDefaultMaxDownloads
			linkSecrets[item.LinkTokenRef] = token
		}
		attachmentsSnapshot = append(attachmentsSnapshot, item)
	}
	bodyText := preview.BodyText
	bodyHTML := preview.BodyHTML
	for contentID, mailAttachmentID := range mailAttachmentIDs {
		placeholder := "%%MAIL_LINK:" + contentID + "%%"
		bodyText = strings.ReplaceAll(bodyText, placeholder, "%%MAIL_LINK:"+mailAttachmentID+"%%")
		bodyHTML = strings.ReplaceAll(bodyHTML, placeholder, "%%MAIL_LINK:"+mailAttachmentID+"%%")
	}
	variables := make(map[string]string, len(input.Variables))
	for name, value := range input.Variables {
		variables[name] = value
	}
	message := domain.MailMessage{
		ID: messageID, TaskID: call.Task.ID, ProjectID: call.Task.ProjectID, WorkspaceID: call.Task.WorkspaceID,
		TurnID: call.Turn.ID, AgentID: call.Turn.AgentID,
		AccountID: account.ID, BindingID: binding.ID, TemplateID: template.ID,
		TemplateVersion: template.CurrentVersion, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: key, FromAddress: account.Address, FromName: strings.TrimSpace(account.DisplayName),
		To: preview.To, CC: preview.CC, BCC: []string{}, Subject: preview.Subject,
		BodyText: bodyText, BodyHTML: bodyHTML, Variables: variables,
		ContentRevision: 1, Status: domain.MailMessagePendingApproval, SubmittedAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	message.ContentFingerprint = mailContentFingerprint(message.TaskID, message.AccountID,
		message.FromAddress, message.FromName, message.To, message.CC, message.BCC,
		message.Subject, message.BodyText, message.BodyHTML, attachmentsSnapshot)
	message.ApprovalDigest = mailpkg.ApprovalDigest(message, attachmentsSnapshot)

	if _, err := s.store.MailMessageByFingerprintSince(ctx, call.Task.ID, message.ContentFingerprint, now.Add(-10*time.Minute)); err == nil {
		return nil, newAgentMailFailure(http.StatusConflict, domain.MailErrDuplicateMessage,
			"10 分钟内已存在相同内容的邮件")
	} else if !isNoRows(err) {
		return nil, err
	}

	if len(linkSecrets) > 0 {
		if s.secrets == nil {
			return nil, errors.New("secret store unavailable")
		}
		if err := s.secrets.PutMany(linkSecrets); err != nil {
			return nil, err
		}
	}
	var timelineEvents []domain.Event
	err = s.store.WithMailTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.store.CreateMailMessageInTx(ctx, tx, message); err != nil {
			return err
		}
		for _, attachment := range attachmentsSnapshot {
			if _, err := s.store.CreateMailAttachmentInTx(ctx, tx, attachment); err != nil {
				return err
			}
		}
		event, err := store.AppendMailTransitionInTx(ctx, tx, store.MailTransition{
			TaskID: call.Task.ID, MessageID: message.ID, TurnID: call.Turn.ID, AgentID: call.Turn.AgentID,
			EventType: store.MailEventApprovalRequested, Status: message.Status, OccurredAt: now,
		})
		if err != nil {
			return err
		}
		timelineEvents = append(timelineEvents, event)
		return nil
	})
	if err != nil {
		if len(linkSecrets) > 0 && s.secrets != nil {
			keys := make([]string, 0, len(linkSecrets))
			for ref := range linkSecrets {
				keys = append(keys, ref)
			}
			_ = s.secrets.DeleteMany(keys)
		}
		// A concurrent retry of the same idempotency key can win the unique
		// insert; return that original message with deduplicated=true.
		if existing, lookupErr := s.store.MailMessageByTaskIdempotencyKey(ctx, call.Task.ID, key); lookupErr == nil {
			return agentMailSendResult(existing, true), nil
		}
		return nil, err
	}
	s.publishMailEvents(timelineEvents)
	s.audit(request, "mail.message.create", "mail_message", message.ID, map[string]any{
		"account_id": account.ID, "attachment_count": len(attachmentsSnapshot),
		"status": string(message.Status), "source": string(message.Source),
	})
	s.audit(request, "mail.message.submit", "mail_message", message.ID, map[string]any{"digest": message.ApprovalDigest})
	for _, attachment := range attachmentsSnapshot {
		if attachment.DeliveryMode == domain.MailAttachmentLink {
			s.audit(request, "mail.link.generate", "mail_attachment", attachment.ID, map[string]any{
				"message_id": message.ID, "expires_at": attachment.LinkExpiresAt.Format(time.RFC3339),
				"max_downloads": attachment.LinkMaxDownloads,
			})
		}
	}
	return agentMailSendResult(message, false), nil
}

func agentMailSendResult(message domain.MailMessage, deduplicated bool) map[string]any {
	return map[string]any{
		"ok":                true,
		"message_id":        message.ID,
		"status":            string(message.Status),
		"approval_required": true,
		"deduplicated":      deduplicated,
	}
}

func (s *Server) agentMailStatusData(ctx context.Context, call app.AgentCallContext, messageID string) (map[string]any, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, newAgentMailFailure(http.StatusUnprocessableEntity, domain.MailErrMessageNotFound, "message_id 必填")
	}
	message, err := s.store.MailMessage(ctx, messageID)
	if err != nil {
		if isNoRows(err) {
			return nil, newAgentMailFailure(http.StatusNotFound, domain.MailErrMessageNotFound, "邮件消息不存在")
		}
		return nil, err
	}
	if message.TaskID != call.Task.ID {
		return nil, newAgentMailFailure(http.StatusForbidden, domain.MailErrForbidden, "邮件消息不属于当前任务")
	}
	result := map[string]any{
		"ok":                true,
		"message_id":        message.ID,
		"status":            string(message.Status),
		"approval_required": message.Status == domain.MailMessagePendingApproval,
		"deduplicated":      false,
	}
	deliveries, err := s.store.MailDeliveriesByMessage(ctx, message.ID, 10)
	if err != nil {
		return nil, err
	}
	if len(deliveries) > 0 {
		delivery := deliveries[0]
		result["delivery"] = map[string]any{
			"state":             string(delivery.State),
			"attempts":          delivery.Attempts,
			"outcome_certainty": string(delivery.OutcomeCertainty),
			"last_error_code":   delivery.LastErrorCode,
			"sent_at":           delivery.SentAt,
			"updated_at":        delivery.UpdatedAt,
		}
	}
	return result, nil
}

func (s *Server) agentMailAccounts(writer http.ResponseWriter, request *http.Request) {
	call, err := s.agentMailCall(request)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.agentMailAccountsData(request.Context(), call))
}

func (s *Server) agentMailTemplates(writer http.ResponseWriter, request *http.Request) {
	call, err := s.agentMailCall(request)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.agentMailTemplatesData(request.Context(), call))
}

func (s *Server) agentMailPreview(writer http.ResponseWriter, request *http.Request) {
	call, err := s.agentMailCall(request)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	var input agentMailDraftInput
	if err := decodeJSON(request, &input); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	result, err := s.agentMailPreviewData(request.Context(), call, input)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) agentMailSend(writer http.ResponseWriter, request *http.Request) {
	call, err := s.agentMailCall(request)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	var input agentMailSendInput
	if err := decodeJSON(request, &input); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	result, err := s.agentMailSendData(request.Context(), request, call, input)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	status := http.StatusCreated
	if deduplicated, _ := result["deduplicated"].(bool); deduplicated {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (s *Server) agentMailMessageStatus(writer http.ResponseWriter, request *http.Request) {
	call, err := s.agentMailCall(request)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	result, err := s.agentMailStatusData(request.Context(), call, request.PathValue("id"))
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// --- MCP surface -----------------------------------------------------------

func mailMCPTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "mail_list_accounts",
			Description: "List mail accounts bound to the current Task. No credential or Secret reference is returned.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
		{
			Name:        "mail_list_templates",
			Description: "List active mail templates visible to the current Task (global plus its Project).",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
		{
			Name:        "mail_preview",
			Description: "Render and validate a mail draft without creating a message. Every send still requires Owner approval.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"account_id":         map[string]any{"type": "string"},
					"template_id":        map[string]any{"type": "string"},
					"to":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"cc":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"variables":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"attachment_ids":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"build_artifact_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required":             []string{"account_id", "template_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "mail_send",
			Description: "Create a mail message that is always pending Owner approval. Reusing the idempotency key returns the original message.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"account_id":         map[string]any{"type": "string"},
					"template_id":        map[string]any{"type": "string"},
					"to":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"cc":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"variables":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"attachment_ids":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"build_artifact_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"idempotency_key":    map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				},
				"required":             []string{"account_id", "template_id", "idempotency_key"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "mail_get_status",
			Description: "Read approval and delivery status for a message created by the current Task. Raw SMTP transcripts are never returned.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{"type": "string"},
				},
				"required":             []string{"message_id"},
				"additionalProperties": false,
			},
		},
	}
}

func (s *Server) agentMailMCP(writer http.ResponseWriter, request *http.Request) {
	call, err := s.agentMailCall(request)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	var payload mcpRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": payload.ID, "result": result})
	}
	respondError := func(message string) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": message},
		})
	}
	switch payload.Method {
	case "initialize":
		respond(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "aha2-mail", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": mailMCPTools()})
	case "tools/call":
		s.callAgentMailMCPTool(writer, request, call, payload)
	default:
		respondError("method not found")
	}
}

func (s *Server) callAgentMailMCPTool(writer http.ResponseWriter, request *http.Request, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": payload.ID, "result": result})
	}
	toolError := func(err error) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": agentMailErrorText(err)}},
		})
	}
	toolResult := func(value map[string]any) {
		encoded, err := json.Marshal(value)
		if err != nil {
			toolError(err)
			return
		}
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(encoded)}},
		})
	}
	var callPayload struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(payload.Params, &callPayload); err != nil || strings.TrimSpace(callPayload.Name) == "" {
		respond(map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "invalid tool call"},
		})
		return
	}
	if len(callPayload.Arguments) == 0 {
		callPayload.Arguments = json.RawMessage(`{}`)
	}
	decodeArguments := func(destination any) bool {
		decoder := json.NewDecoder(bytes.NewReader(callPayload.Arguments))
		decoder.DisallowUnknownFields()
		return decoder.Decode(destination) == nil
	}
	switch callPayload.Name {
	case "mail_list_accounts":
		toolResult(s.agentMailAccountsData(request.Context(), call))
	case "mail_list_templates":
		toolResult(s.agentMailTemplatesData(request.Context(), call))
	case "mail_preview":
		var input agentMailDraftInput
		if !decodeArguments(&input) {
			toolError(newAgentMailFailure(http.StatusBadRequest, domain.MailErrForbidden, "invalid mail_preview arguments"))
			return
		}
		result, err := s.agentMailPreviewData(request.Context(), call, input)
		if err != nil {
			toolError(err)
			return
		}
		toolResult(result)
	case "mail_send":
		var input agentMailSendInput
		if !decodeArguments(&input) {
			toolError(newAgentMailFailure(http.StatusBadRequest, domain.MailErrForbidden, "invalid mail_send arguments"))
			return
		}
		result, err := s.agentMailSendData(request.Context(), request, call, input)
		if err != nil {
			toolError(err)
			return
		}
		toolResult(result)
	case "mail_get_status":
		var input struct {
			MessageID string `json:"message_id"`
		}
		if !decodeArguments(&input) {
			toolError(newAgentMailFailure(http.StatusBadRequest, domain.MailErrForbidden, "invalid mail_get_status arguments"))
			return
		}
		result, err := s.agentMailStatusData(request.Context(), call, input.MessageID)
		if err != nil {
			toolError(err)
			return
		}
		toolResult(result)
	default:
		respond(map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}
