package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

type mailMessagePayload struct {
	TaskID            string            `json:"task_id"`
	AccountID         string            `json:"account_id"`
	BindingID         string            `json:"binding_id"`
	TemplateID        string            `json:"template_id"`
	FromAddress       string            `json:"from_address"`
	FromName          string            `json:"from_name"`
	ReplyTo           string            `json:"reply_to"`
	To                []string          `json:"to"`
	CC                []string          `json:"cc"`
	BCC               []string          `json:"bcc"`
	Subject           *string           `json:"subject"`
	BodyText          *string           `json:"body_text"`
	BodyHTML          *string           `json:"body_html"`
	Variables         map[string]string `json:"variables"`
	AttachmentIDs     []string          `json:"attachment_ids"`
	BuildArtifactIDs  []string          `json:"build_artifact_ids"`
	IdempotencyKey    string            `json:"idempotency_key"`
	Submit            bool              `json:"submit"`
	DuplicateOverride bool              `json:"duplicate_override"`
	OverrideReason    string            `json:"override_reason"`
}

type mailApprovePayload struct {
	ExpectedDigest string `json:"expected_digest"`
	Note           string `json:"note"`
}

type mailRejectPayload struct {
	Reason string `json:"reason"`
}

type mailRetryPayload struct {
	Reason string `json:"reason"`
}

func writeMailStoreFailure(writer http.ResponseWriter, err error, fallback string) {
	switch {
	case isNoRows(err):
		writeMailError(writer, http.StatusNotFound, domain.MailErrMessageNotFound, "邮件消息不存在")
	case errors.Is(err, store.ErrMailInvalidState):
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "当前状态不允许该操作")
	case errors.Is(err, store.ErrMailDigestMismatch):
		writeMailError(writer, http.StatusConflict, domain.MailErrApprovalDigestMismatch, "审批摘要与提交内容不一致")
	default:
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", fallback)
	}
}

func newMailToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func mailLinkTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func mailContentFingerprint(taskID, accountID, fromAddress, fromName string, to, cc, bcc []string, subject, bodyText, bodyHTML string, attachments []domain.MailAttachment) string {
	hashes := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		hashes = append(hashes, attachment.SHA256)
	}
	sortedBCC := append([]string{}, bcc...)
	sort.Strings(sortedBCC)
	payload := strings.Join([]string{
		taskID, accountID, fromAddress, fromName, strings.Join(to, "|"), strings.Join(cc, "|"),
		strings.Join(sortedBCC, "|"), subject, bodyText, bodyHTML, strings.Join(hashes, "|"),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func (s *Server) mailServerVariables(ctx context.Context, task domain.Task) map[string]string {
	variables := map[string]string{
		"task.id": task.ID, "task.title": task.Title,
	}
	if taskURL, ok := s.mailTaskURL(task.ID); ok {
		variables["task.url"] = taskURL
	}
	if project, err := s.store.Project(ctx, task.ProjectID); err == nil {
		variables["project.name"] = project.Name
	}
	if workspace, err := s.store.Workspace(ctx, task.WorkspaceID); err == nil {
		variables["workspace.name"] = workspace.Name
	}
	return variables
}

func (s *Server) createMailMessage(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	var payload mailMessagePayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "task_id 必填")
		return
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrInvalidState, "任务不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取任务失败")
		return
	}
	key := strings.TrimSpace(payload.IdempotencyKey)
	if key == "" {
		key = "owner-" + domain.NewID("key")
	}
	if existing, err := s.store.MailMessageByTaskIdempotencyKey(ctx, taskID, key); err == nil {
		attachments, _ := s.store.MailAttachments(ctx, existing.ID, 100)
		writeJSON(writer, http.StatusOK, map[string]any{
			"ok": true, "message": existing, "attachments": attachments, "deduplicated": true,
		})
		return
	} else if !isNoRows(err) {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件消息失败")
		return
	}
	if task.Status.Terminal() {
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "任务已结束，不能新建邮件")
		return
	}

	accountID := strings.TrimSpace(payload.AccountID)
	account, err := s.store.MailAccount(ctx, accountID)
	if err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	if account.Status == domain.MailAccountDisabled {
		writeMailError(writer, http.StatusConflict, domain.MailErrAccountDisabled, "账号已停用")
		return
	}
	if !account.CredentialConfigured {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrCredentialMissing, "账号未配置授权码")
		return
	}
	fromAddress := strings.TrimSpace(payload.FromAddress)
	if fromAddress == "" {
		fromAddress = account.Address
	}
	if !account.AllowsFrom(fromAddress) {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrFromNotAllowed, "From 必须是账号主地址或已登记别名")
		return
	}
	if _, err := mailpkg.NormalizeRecipient(fromAddress); err != nil {
		writeMailFailure(writer, err, "发件地址不正确")
		return
	}
	fromName := strings.TrimSpace(payload.FromName)
	if fromName == "" {
		fromName = account.DisplayName
	}
	replyTo := strings.TrimSpace(payload.ReplyTo)
	if replyTo != "" {
		// Phase one does not support a custom Reply-To.
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrFromNotAllowed, "一期不支持自定义 Reply-To")
		return
	}

	bindingID := strings.TrimSpace(payload.BindingID)
	if bindingID != "" {
		binding, err := s.store.MailBinding(ctx, bindingID)
		if err != nil {
			if isNoRows(err) {
				writeMailError(writer, http.StatusNotFound, domain.MailErrBindingNotFound, "邮件绑定不存在")
				return
			}
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件绑定失败")
			return
		}
		if !binding.Enabled || binding.AccountID != account.ID || !bindingMatchesTask(binding, task) {
			writeMailError(writer, http.StatusForbidden, domain.MailErrAccountNotBound, "账号未绑定到当前任务范围")
			return
		}
	}

	templateID := strings.TrimSpace(payload.TemplateID)
	template := mailpkg.Template{}
	if templateID != "" {
		item, err := s.store.MailTemplate(ctx, templateID)
		if err != nil {
			if isNoRows(err) {
				writeMailError(writer, http.StatusNotFound, domain.MailErrTemplateNotFound, "邮件模板不存在")
				return
			}
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
			return
		}
		if item.Status != domain.MailTemplateStatusActive {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "模板已归档")
			return
		}
		if item.Scope == domain.MailTemplateScopeProject && item.ProjectID != task.ProjectID {
			writeMailError(writer, http.StatusNotFound, domain.MailErrTemplateNotFound, "模板不属于当前项目")
			return
		}
		template = mailpkg.Template{
			ID: item.ID, Version: item.CurrentVersion,
			SubjectTemplate: item.SubjectTemplate, BodyTextTemplate: item.BodyTextTemplate,
			BodyHTMLTemplate: item.BodyHTMLTemplate, RequiredVariables: item.RequiredVariables,
			DefaultTo: item.DefaultTo, DefaultCC: item.DefaultCC,
		}
		if version, err := s.store.MailTemplateVersion(ctx, item.ID, item.CurrentVersion); err == nil {
			template.SubjectTemplate = version.SubjectTemplate
			template.BodyTextTemplate = version.BodyTextTemplate
			template.BodyHTMLTemplate = version.BodyHTMLTemplate
			template.RequiredVariables = version.RequiredVariables
		}
	} else {
		if payload.Subject == nil || (payload.BodyText == nil && payload.BodyHTML == nil) {
			writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "未选择模板时必须提供主题与正文")
			return
		}
		template.SubjectTemplate = *payload.Subject
		if payload.BodyText != nil {
			template.BodyTextTemplate = *payload.BodyText
		}
		if payload.BodyHTML != nil {
			template.BodyHTMLTemplate = *payload.BodyHTML
		}
	}
	serverVariables := s.mailServerVariables(ctx, task)
	attachments, packageVariables, err := s.mailPreviewAttachments(ctx, task.ID, payload.AttachmentIDs, payload.BuildArtifactIDs)
	if err != nil {
		writeMailFailure(writer, err, "邮件附件校验失败")
		return
	}
	callerVariables := make(map[string]string, len(payload.Variables))
	for name, value := range payload.Variables {
		callerVariables[name] = value
	}
	for name, value := range packageVariables {
		if name == "build.version" {
			if strings.TrimSpace(callerVariables[name]) == "" {
				callerVariables[name] = value
			}
			continue
		}
		serverVariables[name] = value
	}
	to := payload.To
	if len(to) == 0 {
		to = template.DefaultTo
	}
	cc := payload.CC
	if len(cc) == 0 {
		cc = template.DefaultCC
	}
	preview, err := mailpkg.Render(template, mailpkg.Input{
		Variables: callerVariables, ServerVariables: serverVariables,
		To: to, CC: cc, Attachments: attachments,
	})
	if err != nil {
		writeMailFailure(writer, err, "邮件渲染失败")
		return
	}
	bcc, err := mailpkg.NormalizeRecipients(payload.BCC)
	if err != nil {
		writeMailFailure(writer, err, "密送地址不正确")
		return
	}
	if err := validateRecipientUnion(preview.To, preview.CC, bcc); err != nil {
		writeMailFailure(writer, err, "收件人组合不正确")
		return
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
		switch item.DeliveryMode {
		case domain.MailAttachmentDirect:
		case domain.MailAttachmentLink:
			if strings.TrimSpace(s.mailDownloadBaseURL) == "" || !validHTTPSBase(s.mailDownloadBaseURL) {
				writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTotalTooLarge,
					"受控下载入口未配置，无法发送大附件")
				return
			}
			if strings.Contains(preview.BodyHTML, "cid:"+attachment.SourceID) {
				writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid,
					"受控链接附件不能作为内嵌图片引用")
				return
			}
			token, err := newMailToken()
			if err != nil {
				writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "生成下载令牌失败")
				return
			}
			item.LinkTokenRef = domain.MailAttachmentLinkTokenRef(mailAttachmentID)
			item.LinkTokenHash = mailLinkTokenHash(token)
			item.LinkExpiresAt = now.Add(domain.MailLinkDefaultTTL)
			item.LinkMaxDownloads = domain.MailLinkDefaultMaxDownloads
			linkSecrets[item.LinkTokenRef] = token
		default:
			writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAttachmentUnavailable, "附件投递方式不正确")
			return
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
	status := domain.MailMessageDraft
	submittedAt := time.Time{}
	if payload.Submit {
		status = domain.MailMessagePendingApproval
		submittedAt = now
	}
	message := domain.MailMessage{
		ID: messageID, TaskID: task.ID, ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID,
		AccountID: account.ID, BindingID: bindingID, TemplateID: template.ID,
		TemplateVersion: template.Version, Source: domain.MailMessageSourceOwner,
		IdempotencyKey: key, FromAddress: fromAddress, FromName: fromName,
		To: preview.To, CC: preview.CC, BCC: bcc, Subject: preview.Subject,
		BodyText: bodyText, BodyHTML: bodyHTML, Variables: callerVariables,
		ContentRevision: 1, Status: status, SubmittedAt: submittedAt,
		CreatedAt: now, UpdatedAt: now,
	}
	message.ContentFingerprint = mailContentFingerprint(message.TaskID, message.AccountID,
		message.FromAddress, message.FromName, message.To, message.CC, message.BCC,
		message.Subject, message.BodyText, message.BodyHTML, attachmentsSnapshot)
	message.ApprovalDigest = mailpkg.ApprovalDigest(message, attachmentsSnapshot)

	if duplicate, err := s.store.MailMessageByFingerprintSince(ctx, task.ID, message.ContentFingerprint, now.Add(-10*time.Minute)); err == nil {
		if !payload.DuplicateOverride {
			writeMailError(writer, http.StatusConflict, domain.MailErrDuplicateMessage,
				"10 分钟内已存在相同内容的邮件；Owner 需显式 override")
			return
		}
		if strings.TrimSpace(payload.OverrideReason) == "" {
			writeMailError(writer, http.StatusConflict, domain.MailErrDuplicateMessage,
				"duplicate_override 需要 override_reason")
			return
		}
		_ = duplicate
	} else if !isNoRows(err) {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "重复内容检查失败")
		return
	}

	if len(linkSecrets) > 0 {
		if s.secrets == nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "Secret Store 不可用")
			return
		}
		if err := s.secrets.PutMany(linkSecrets); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "保存下载令牌失败")
			return
		}
	}
	var timelineEvents []domain.Event
	if err := s.store.WithMailTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.store.CreateMailMessageInTx(ctx, tx, message); err != nil {
			return err
		}
		for _, attachment := range attachmentsSnapshot {
			if _, err := s.store.CreateMailAttachmentInTx(ctx, tx, attachment); err != nil {
				return err
			}
		}
		if status == domain.MailMessagePendingApproval {
			event, err := store.AppendMailTransitionInTx(ctx, tx, store.MailTransition{
				TaskID: task.ID, MessageID: message.ID, TurnID: message.TurnID, AgentID: message.AgentID,
				EventType: store.MailEventApprovalRequested, Status: status, OccurredAt: now,
			})
			if err != nil {
				return err
			}
			timelineEvents = append(timelineEvents, event)
		}
		return nil
	}); err != nil {
		if len(linkSecrets) > 0 && s.secrets != nil {
			keys := make([]string, 0, len(linkSecrets))
			for ref := range linkSecrets {
				keys = append(keys, ref)
			}
			_ = s.secrets.DeleteMany(keys)
		}
		writeMailStoreFailure(writer, err, "创建邮件消息失败")
		return
	}
	s.publishMailEvents(timelineEvents)
	action := "mail.message.create"
	if payload.Submit {
		action = "mail.message.submit"
	}
	s.audit(request, action, "mail_message", message.ID, map[string]any{
		"account_id": account.ID, "attachment_count": len(attachmentsSnapshot),
		"status": string(status),
	})
	if payload.DuplicateOverride {
		s.audit(request, "mail.message.duplicate_override", "mail_message", message.ID, map[string]any{
			"reason": strings.TrimSpace(payload.OverrideReason),
		})
	}
	for _, attachment := range attachmentsSnapshot {
		if attachment.DeliveryMode == domain.MailAttachmentLink {
			s.audit(request, "mail.link.generate", "mail_attachment", attachment.ID, map[string]any{
				"message_id": message.ID, "expires_at": attachment.LinkExpiresAt.Format(time.RFC3339),
				"max_downloads": attachment.LinkMaxDownloads,
			})
		}
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ok": true, "message": message, "attachments": attachmentsSnapshot, "deduplicated": false,
	})
}

func bindingMatchesTask(binding domain.MailBinding, task domain.Task) bool {
	switch binding.ScopeType {
	case domain.MailBindingScopeTask:
		return binding.ScopeID == task.ID
	case domain.MailBindingScopeWorkspace:
		return binding.ScopeID == task.WorkspaceID
	case domain.MailBindingScopeProject:
		return binding.ScopeID == task.ProjectID
	default:
		return false
	}
}

func validHTTPSBase(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return true
}

func validateRecipientUnion(to, cc, bcc []string) error {
	seen := make(map[string]bool, len(to)+len(cc)+len(bcc))
	for _, list := range [][]string{to, cc, bcc} {
		for _, address := range list {
			key := strings.ToLower(address)
			if seen[key] {
				return &mailpkg.Error{Code: domain.MailErrRecipientInvalid, Message: "duplicate recipient"}
			}
			seen[key] = true
		}
	}
	if len(seen) > mailpkg.MaxRecipients {
		return &mailpkg.Error{Code: domain.MailErrRecipientInvalid, Message: "too many recipients"}
	}
	return nil
}

func (s *Server) loadMailMessageWithAttachments(ctx context.Context, id string) (domain.MailMessage, []domain.MailAttachment, error) {
	message, err := s.store.MailMessage(ctx, id)
	if err != nil {
		return domain.MailMessage{}, nil, err
	}
	attachments, err := s.store.MailAttachments(ctx, id, 100)
	if err != nil {
		return domain.MailMessage{}, nil, err
	}
	return message, attachments, nil
}

func (s *Server) submitMailMessage(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	message, attachments, err := s.loadMailMessageWithAttachments(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	if message.Status != domain.MailMessageDraft {
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "只有草稿可以提交审批")
		return
	}
	task, err := s.store.Task(ctx, message.TaskID)
	if err != nil {
		writeMailStoreFailure(writer, err, "读取任务失败")
		return
	}
	if task.Status.Terminal() {
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "任务已结束，草稿不能提交")
		return
	}
	digest := mailpkg.ApprovalDigest(message, attachments)
	updated, events, err := s.store.SubmitMailMessage(ctx, message.ID, digest, nowUTC())
	if err != nil {
		writeMailStoreFailure(writer, err, "提交审批失败")
		return
	}
	s.publishMailEvents(events)
	s.audit(request, "mail.message.submit", "mail_message", message.ID, map[string]any{"digest": digest})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "message": updated})
}

func (s *Server) approveMailMessage(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	var payload mailApprovePayload
	if request.ContentLength != 0 {
		if err := decodeJSON(request, &payload); err != nil {
			writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
			return
		}
	}
	message, attachments, err := s.loadMailMessageWithAttachments(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	current := mailpkg.ApprovalDigest(message, attachments)
	if current != message.ApprovalDigest {
		writeMailError(writer, http.StatusConflict, domain.MailErrApprovalDigestMismatch, "审批摘要与当前内容不一致")
		return
	}
	expected := strings.TrimSpace(payload.ExpectedDigest)
	if expected == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrApprovalDigestMismatch, "expected_digest 必填")
		return
	}
	if expected != current {
		writeMailError(writer, http.StatusConflict, domain.MailErrApprovalDigestMismatch, "审批摘要已变化，请刷新")
		return
	}
	updated, delivery, events, err := s.store.ApproveMailMessage(
		ctx, message.ID, current, mailOwnerID(request), payload.Note, nowUTC())
	if err != nil {
		writeMailStoreFailure(writer, err, "批准邮件失败")
		return
	}
	s.publishMailEvents(events)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "message": updated, "delivery": delivery})
}

func (s *Server) rejectMailMessage(writer http.ResponseWriter, request *http.Request) {
	var payload mailRejectPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "拒绝原因必填")
		return
	}
	message, err := s.store.MailMessage(request.Context(), request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	events, err := s.store.RejectMailMessage(request.Context(), message.ID, reason, nowUTC())
	if err != nil {
		writeMailStoreFailure(writer, err, "拒绝邮件失败")
		return
	}
	s.publishMailEvents(events)
	s.audit(request, "mail.message.reject", "mail_message", message.ID, map[string]any{"reason": reason})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cancelMailMessage(writer http.ResponseWriter, request *http.Request) {
	message, events, err := s.store.CancelMailMessage(request.Context(), request.PathValue("id"), mailOwnerID(request), nowUTC())
	if err != nil {
		writeMailStoreFailure(writer, err, "取消邮件失败")
		return
	}
	s.publishMailEvents(events)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "message": message})
}

func (s *Server) retryMailMessage(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	var payload mailRetryPayload
	if request.ContentLength != 0 {
		if err := decodeJSON(request, &payload); err != nil {
			writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
			return
		}
	}
	message, attachments, err := s.loadMailMessageWithAttachments(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	digest := mailpkg.ApprovalDigest(message, attachments)
	if digest != message.ApprovalDigest {
		writeMailError(writer, http.StatusConflict, domain.MailErrApprovalDigestMismatch, "快照内容与审批摘要不一致")
		return
	}
	delivery, events, err := s.store.RetryMailMessage(
		ctx, message.ID, digest, mailOwnerID(request), payload.Reason, nowUTC())
	if err != nil {
		writeMailStoreFailure(writer, err, "重试邮件失败")
		return
	}
	s.publishMailEvents(events)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "delivery": delivery})
}

func (s *Server) listMailMessages(writer http.ResponseWriter, request *http.Request) {
	filter := store.MailMessageFilter{
		TaskID:    request.URL.Query().Get("task_id"),
		AccountID: request.URL.Query().Get("account_id"),
		Status:    domain.MailMessageStatus(strings.TrimSpace(request.URL.Query().Get("status"))),
		Limit:     int(queryInt(request, "limit", 100)),
	}
	messages, err := s.store.ListMailMessages(request.Context(), filter)
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "messages": messages})
}

func (s *Server) mailMessageDetail(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	message, attachments, err := s.loadMailMessageWithAttachments(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	deliveries, err := s.store.MailDeliveriesByMessage(ctx, message.ID, 20)
	if err != nil {
		writeMailStoreFailure(writer, err, "读取投递记录失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "message": message, "attachments": attachments, "deliveries": deliveries,
	})
}

func (s *Server) mailMessageAttempts(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	message, err := s.store.MailMessage(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	deliveries, err := s.store.MailDeliveriesByMessage(ctx, message.ID, 10)
	if err != nil {
		writeMailStoreFailure(writer, err, "读取投递记录失败")
		return
	}
	type deliveryAttempts struct {
		Delivery domain.MailDelivery          `json:"delivery"`
		Attempts []domain.MailDeliveryAttempt `json:"attempts"`
	}
	result := make([]deliveryAttempts, 0, len(deliveries))
	for _, delivery := range deliveries {
		attempts, err := s.store.MailDeliveryAttempts(ctx, delivery.ID, 10)
		if err != nil {
			writeMailStoreFailure(writer, err, "读取投递尝试失败")
			return
		}
		result = append(result, deliveryAttempts{Delivery: delivery, Attempts: attempts})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deliveries": result})
}

func (s *Server) mailMessageDownloadLinks(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	message, err := s.store.MailMessage(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	attachments, err := s.store.MailAttachments(ctx, message.ID, 100)
	if err != nil {
		writeMailStoreFailure(writer, err, "读取附件失败")
		return
	}
	links := make([]domain.MailAttachment, 0)
	for _, attachment := range attachments {
		if attachment.DeliveryMode == domain.MailAttachmentLink {
			links = append(links, attachment)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "links": links})
}

func (s *Server) revokeMailMessageDownloadLink(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	message, err := s.store.MailMessage(ctx, request.PathValue("id"))
	if err != nil {
		writeMailStoreFailure(writer, err, "读取邮件消息失败")
		return
	}
	attachment, err := s.store.MailAttachment(ctx, request.PathValue("attachment_id"))
	if err != nil || attachment.MessageID != message.ID || attachment.DeliveryMode != domain.MailAttachmentLink {
		writeMailError(writer, http.StatusNotFound, domain.MailErrAttachmentNotFound, "受控下载链接不存在")
		return
	}
	if err := s.store.RevokeMailAttachmentLink(ctx, attachment.ID, nowUTC()); err != nil {
		writeMailStoreFailure(writer, err, "撤销下载链接失败")
		return
	}
	s.audit(request, "mail.link.revoke", "mail_attachment", attachment.ID, map[string]any{
		"message_id": message.ID,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
