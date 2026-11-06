package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

type mailTemplatePayload struct {
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

type mailPreviewPayload struct {
	Variables map[string]string `json:"variables"`
	// SampleVariables are trusted preview-only sample values. Server-resolved
	// namespaces (task/project/workspace/package) are accepted here so an editor
	// can render without a real Task; they never reach message creation.
	SampleVariables map[string]string `json:"sample_variables"`
	// Subject, BodyText and BodyHTML are caller final content for a guide
	// template. Previewing a guide template without them shows its literal
	// example; a real send never uses that fallback.
	Subject  *string `json:"subject"`
	BodyText *string `json:"body_text"`
	BodyHTML *string `json:"body_html"`
	// ExampleOnly is preview-only. It shows a guide template's literal sample
	// even when the caller supplies final content.
	ExampleOnly      bool     `json:"preview_example"`
	To               []string `json:"to"`
	CC               []string `json:"cc"`
	TaskID           string   `json:"task_id"`
	AttachmentIDs    []string `json:"attachment_ids"`
	BuildArtifactIDs []string `json:"build_artifact_ids"`
}

// mailDraftPreviewPayload renders editor content that has not been saved yet.
// It deliberately reuses the same render pipeline as message creation.
type mailDraftPreviewPayload struct {
	Mode             string            `json:"mode"`
	Description      string            `json:"description"`
	SubjectTemplate  string            `json:"subject_template"`
	BodyTextTemplate string            `json:"body_text_template"`
	BodyHTMLTemplate string            `json:"body_html_template"`
	DefaultTo        []string          `json:"default_to"`
	DefaultCC        []string          `json:"default_cc"`
	Variables        map[string]string `json:"variables"`
	SampleVariables  map[string]string `json:"sample_variables"`
	// ExampleOnly previews a guide draft as its literal reference sample
	// (the template editor path). Compose previews omit it and are validated
	// like a real send.
	ExampleOnly      bool     `json:"preview_example"`
	To               []string `json:"to"`
	CC               []string `json:"cc"`
	TaskID           string   `json:"task_id"`
	AttachmentIDs    []string `json:"attachment_ids"`
	BuildArtifactIDs []string `json:"build_artifact_ids"`
}

var (
	errMailPreviewTaskNotFound = errors.New("mail preview task not found")
	errMailTaskURLUnconfigured = errors.New("mail task url is unconfigured")
)

func mailTemplateContent(item domain.MailTemplate) mailpkg.Template {
	return mailpkg.Template{
		ID:                item.ID,
		Version:           item.CurrentVersion,
		Mode:              item.Mode,
		SubjectTemplate:   item.SubjectTemplate,
		BodyTextTemplate:  item.BodyTextTemplate,
		BodyHTMLTemplate:  item.BodyHTMLTemplate,
		RequiredVariables: item.RequiredVariables,
		DefaultTo:         item.DefaultTo,
		DefaultCC:         item.DefaultCC,
	}
}

func (s *Server) listMailTemplates(writer http.ResponseWriter, request *http.Request) {
	scope := domain.MailTemplateScope(strings.TrimSpace(request.URL.Query().Get("scope")))
	projectID := strings.TrimSpace(request.URL.Query().Get("project_id"))
	if scope != "" && !scope.Valid() {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板作用域不正确")
		return
	}
	templates, err := s.store.MailTemplates(request.Context(), scope, projectID, 100)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "templates": templates})
}

func (s *Server) createMailTemplate(writer http.ResponseWriter, request *http.Request) {
	var payload mailTemplatePayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	scope := domain.MailTemplateScope(strings.TrimSpace(payload.Scope))
	if scope == "" {
		scope = domain.MailTemplateScopeGlobal
	}
	if !scope.Valid() {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板作用域不正确")
		return
	}
	projectID := strings.TrimSpace(payload.ProjectID)
	if scope == domain.MailTemplateScopeProject {
		if projectID == "" {
			writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "项目模板需要 project_id")
			return
		}
		if _, err := s.store.Project(request.Context(), projectID); err != nil {
			writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "项目不存在")
			return
		}
	} else {
		projectID = ""
	}
	item := domain.MailTemplate{
		ID: domain.NewID("mail_template"), Scope: scope, ProjectID: projectID,
		// New templates are guide mode by default. render remains available for
		// explicitly requested legacy-style templates.
		Mode:   domain.MailTemplateModeGuide,
		Status: domain.MailTemplateStatusActive, CurrentVersion: 1,
		CreatedBy: mailOwnerID(request),
	}
	item = applyMailTemplatePayload(item, payload)
	if strings.TrimSpace(item.Name) == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板名称必填")
		return
	}
	if !item.Mode.Valid() {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板模式不正确")
		return
	}
	if item.Mode == domain.MailTemplateModeRender {
		// required_variables is derived from the template text; a client can no
		// longer persist a manual list that disagrees with the placeholders.
		required, err := mailpkg.CallerVariables(mailTemplateContent(item))
		if err != nil {
			writeMailFailure(writer, err, "邮件模板校验失败")
			return
		}
		item.RequiredVariables = required
	} else {
		item.RequiredVariables = []string{}
	}
	if err := mailpkg.ValidateTemplate(mailTemplateContent(item)); err != nil {
		writeMailFailure(writer, err, "邮件模板校验失败")
		return
	}
	created, err := s.store.CreateMailTemplate(request.Context(), item)
	if err != nil {
		if isUniqueNameError(err) {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "同作用域下已存在同名模板")
			return
		}
		writeMailFailure(writer, err, "创建邮件模板失败")
		return
	}
	s.audit(request, "mail.template.create", "mail_template", created.ID, map[string]any{
		"scope": string(created.Scope), "version": created.CurrentVersion,
	})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "template": created})
}

func applyMailTemplatePayload(item domain.MailTemplate, payload mailTemplatePayload) domain.MailTemplate {
	if payload.Name != nil {
		item.Name = strings.TrimSpace(*payload.Name)
	}
	if payload.Description != nil {
		item.Description = strings.TrimSpace(*payload.Description)
	}
	if payload.Mode != nil && strings.TrimSpace(*payload.Mode) != "" {
		// An empty mode means "leave unchanged"; create paths already start at
		// guide so an omitted mode still defaults correctly.
		item.Mode = domain.MailTemplateMode(strings.TrimSpace(*payload.Mode))
	}
	if payload.SubjectTemplate != nil {
		item.SubjectTemplate = *payload.SubjectTemplate
	}
	if payload.BodyTextTemplate != nil {
		item.BodyTextTemplate = *payload.BodyTextTemplate
	}
	if payload.BodyHTMLTemplate != nil {
		item.BodyHTMLTemplate = *payload.BodyHTMLTemplate
	}
	if payload.DefaultTo != nil {
		item.DefaultTo = append([]string{}, (*payload.DefaultTo)...)
	}
	if payload.DefaultCC != nil {
		item.DefaultCC = append([]string{}, (*payload.DefaultCC)...)
	}
	if payload.RequiredVariables != nil {
		item.RequiredVariables = append([]string{}, (*payload.RequiredVariables)...)
	}
	return item
}

func isUniqueNameError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Server) mailTemplateDetail(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.MailTemplate(request.Context(), request.PathValue("id"))
	if err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "template": item})
}

func (s *Server) updateMailTemplate(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	existing, err := s.store.MailTemplate(request.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	var payload mailTemplatePayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	if payload.ExpectedVersion <= 0 {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "expected_version 必填")
		return
	}
	// Scope is immutable; changing it would move history between projects.
	item := applyMailTemplatePayload(existing, payload)
	if payload.Name == nil {
		item.Name = existing.Name
	}
	if strings.TrimSpace(item.Name) == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板名称必填")
		return
	}
	if item.Mode == "" {
		// A legacy row loaded before the mode column existed behaves as render.
		item.Mode = domain.MailTemplateModeRender
	}
	if !item.Mode.Valid() {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板模式不正确")
		return
	}
	if item.Mode == domain.MailTemplateModeRender {
		required, err := mailpkg.CallerVariables(mailTemplateContent(item))
		if err != nil {
			writeMailFailure(writer, err, "邮件模板校验失败")
			return
		}
		item.RequiredVariables = required
	} else {
		item.RequiredVariables = []string{}
	}
	if err := mailpkg.ValidateTemplate(mailTemplateContent(item)); err != nil {
		writeMailFailure(writer, err, "邮件模板校验失败")
		return
	}
	updated, err := s.store.UpdateMailTemplate(request.Context(), item, payload.ExpectedVersion)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMailTemplateVersionConflict):
			writeMailError(writer, http.StatusConflict, domain.MailErrTemplateVersionConflict, "模板已被其他编辑更新，请刷新后重试")
		case errors.Is(err, store.ErrMailTemplateArchived):
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "已归档模板不能修改")
		case isNoRows(err):
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
		case isUniqueNameError(err):
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "同作用域下已存在同名模板")
		default:
			writeMailFailure(writer, err, "更新邮件模板失败")
		}
		return
	}
	s.audit(request, "mail.template.update", "mail_template", id, map[string]any{
		"version": updated.CurrentVersion,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "template": updated})
}

func (s *Server) deleteMailTemplate(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	purge := request.URL.Query().Get("purge")
	if purge == "1" || strings.EqualFold(purge, "true") {
		if err := s.store.PurgeMailTemplate(request.Context(), id); err != nil {
			switch {
			case errors.Is(err, store.ErrMailTemplateInUse):
				writeMailError(writer, http.StatusConflict, domain.MailErrTemplateInUse,
					"模板已被邮件或绑定引用，只能归档；如仅被绑定引用，请先解除绑定")
			case isNoRows(err):
				writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			default:
				writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "彻底删除邮件模板失败")
			}
			return
		}
		s.audit(request, "mail.template.purge", "mail_template", id, nil)
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "purged": true})
		return
	}
	existing, err := s.store.MailTemplate(request.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	if existing.Status == domain.MailTemplateStatusArchived {
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "模板已归档")
		return
	}
	if err := s.store.ArchiveMailTemplate(request.Context(), id, nowUTC()); err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "归档邮件模板失败")
		return
	}
	s.audit(request, "mail.template.archive", "mail_template", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) mailTemplateVersions(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if _, err := s.store.MailTemplate(request.Context(), id); err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	versions, err := s.store.MailTemplateVersions(request.Context(), id, 100)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取模板版本失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "versions": versions})
}

func (s *Server) previewMailTemplate(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	item, err := s.store.MailTemplate(ctx, request.PathValue("id"))
	if err != nil {
		if isNoRows(err) {
			writeMailNotFound(writer, domain.MailErrTemplateNotFound, "邮件模板不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮件模板失败")
		return
	}
	if item.Status != domain.MailTemplateStatusActive {
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "已归档模板不能预览")
		return
	}
	var payload mailPreviewPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	s.previewMailTemplateWith(writer, request, item, payload)
}

// previewMailTemplateContent renders an unsaved editor draft through the same
// pipeline used for message creation, so the Owner sees sanitized HTML and the
// real text fallback before saving.
func (s *Server) previewMailTemplateContent(writer http.ResponseWriter, request *http.Request) {
	var payload mailDraftPreviewPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	mode := domain.MailTemplateMode(strings.TrimSpace(payload.Mode))
	if mode == "" {
		// Older clients did not send a mode; keep the legacy render behavior for
		// their unsaved draft preview.
		mode = domain.MailTemplateModeRender
	}
	item := domain.MailTemplate{
		Scope: domain.MailTemplateScopeGlobal, Status: domain.MailTemplateStatusActive,
		Mode: mode, CurrentVersion: 1, SubjectTemplate: payload.SubjectTemplate,
		BodyTextTemplate: payload.BodyTextTemplate, BodyHTMLTemplate: payload.BodyHTMLTemplate,
		Description: payload.Description, DefaultTo: payload.DefaultTo, DefaultCC: payload.DefaultCC,
	}
	if mode == domain.MailTemplateModeRender {
		required, err := mailpkg.CallerVariables(mailTemplateContent(item))
		if err != nil {
			writeMailFailure(writer, err, "邮件模板校验失败")
			return
		}
		item.RequiredVariables = required
	}
	s.previewMailTemplateWith(writer, request, item, payload.previewPayload())
}

func (p mailDraftPreviewPayload) previewPayload() mailPreviewPayload {
	subject, bodyText, bodyHTML := p.SubjectTemplate, p.BodyTextTemplate, p.BodyHTMLTemplate
	return mailPreviewPayload{
		Variables: p.Variables, SampleVariables: p.SampleVariables,
		Subject: &subject, BodyText: &bodyText, BodyHTML: &bodyHTML,
		ExampleOnly: p.ExampleOnly,
		To:          p.To, CC: p.CC, TaskID: p.TaskID,
		AttachmentIDs: p.AttachmentIDs, BuildArtifactIDs: p.BuildArtifactIDs,
	}
}

func (s *Server) previewMailTemplateWith(writer http.ResponseWriter, request *http.Request, item domain.MailTemplate, payload mailPreviewPayload) {
	preview, err := s.renderMailTemplatePreview(request.Context(), item, payload)
	if err != nil {
		switch {
		case errors.Is(err, errMailPreviewTaskNotFound):
			writeError(writer, http.StatusNotFound, "task_not_found")
		case errors.Is(err, errMailTaskURLUnconfigured):
			writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "task.url 未配置外部 https 基地址")
		default:
			writeMailFailure(writer, err, "邮件预览校验失败")
		}
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

func (s *Server) renderMailTemplatePreview(ctx context.Context, item domain.MailTemplate, payload mailPreviewPayload) (mailpkg.Preview, error) {
	if item.Mode == domain.MailTemplateModeGuide {
		return s.renderGuideMailTemplatePreview(ctx, item, payload)
	}
	variables := make(map[string]string, len(payload.Variables)+len(payload.SampleVariables))
	serverVariables := make(map[string]string, 8)
	// Samples are preview-only values. Explicit caller variables are applied
	// after them so an editor input wins over its prefilled sample.
	for name, value := range payload.SampleVariables {
		if mailpkg.ServerVariableAllowed(name) {
			serverVariables[name] = value
			continue
		}
		variables[name] = value
	}
	for name, value := range payload.Variables {
		variables[name] = value
	}
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID != "" {
		task, err := s.store.Task(ctx, taskID)
		if err != nil {
			if isNoRows(err) {
				return mailpkg.Preview{}, errMailPreviewTaskNotFound
			}
			return mailpkg.Preview{}, err
		}
		serverVariables["task.id"] = task.ID
		serverVariables["task.title"] = task.Title
		if taskURL, ok := s.mailTaskURL(task.ID); ok {
			serverVariables["task.url"] = taskURL
		} else if templateReferencesVariable(item, "task.url") {
			return mailpkg.Preview{}, errMailTaskURLUnconfigured
		}
		if project, err := s.store.Project(ctx, task.ProjectID); err == nil {
			serverVariables["project.name"] = project.Name
		}
		if workspace, err := s.store.Workspace(ctx, task.WorkspaceID); err == nil {
			serverVariables["workspace.name"] = workspace.Name
		}
	}
	attachments, packageVariables, err := s.mailPreviewAttachments(ctx, taskID, payload.AttachmentIDs, payload.BuildArtifactIDs)
	if err != nil {
		return mailpkg.Preview{}, err
	}
	for name, value := range packageVariables {
		// package.* is server authoritative. build.version is a caller
		// overridable build field even when it originated from the artifact.
		if name == "build.version" {
			if strings.TrimSpace(variables[name]) == "" {
				variables[name] = value
			}
			continue
		}
		serverVariables[name] = value
	}
	to := payload.To
	if len(to) == 0 {
		to = item.DefaultTo
	}
	cc := payload.CC
	if len(cc) == 0 {
		cc = item.DefaultCC
	}
	return mailpkg.Render(mailTemplateContent(item), mailpkg.Input{
		Variables: variables, ServerVariables: serverVariables,
		To: to, CC: cc, Attachments: attachments,
	})
}

// renderGuideMailTemplatePreview previews a guide template. With caller final
// content it behaves exactly like a real send; without it (or when the editor
// explicitly asks for the example) it displays the template's literal sample.
// The sample path exists only here and never in message creation.
func (s *Server) renderGuideMailTemplatePreview(ctx context.Context, item domain.MailTemplate, payload mailPreviewPayload) (mailpkg.Preview, error) {
	attachments, _, err := s.mailPreviewAttachments(ctx, strings.TrimSpace(payload.TaskID),
		payload.AttachmentIDs, payload.BuildArtifactIDs)
	if err != nil {
		return mailpkg.Preview{}, err
	}
	to := payload.To
	if len(to) == 0 {
		to = item.DefaultTo
	}
	cc := payload.CC
	if len(cc) == 0 {
		cc = item.DefaultCC
	}
	input := mailpkg.Input{To: to, CC: cc, Attachments: attachments}
	content := mailTemplateContent(item)
	if payload.ExampleOnly {
		return mailpkg.PreviewGuideExample(content, input)
	}
	input.Subject = stringValue(payload.Subject)
	input.BodyText = stringValue(payload.BodyText)
	input.BodyHTML = stringValue(payload.BodyHTML)
	if strings.TrimSpace(input.Subject) == "" && strings.TrimSpace(input.BodyText) == "" &&
		strings.TrimSpace(input.BodyHTML) == "" {
		return mailpkg.PreviewGuideExample(content, input)
	}
	return mailpkg.Render(content, input)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// mailTaskURL builds an absolute external task link. Relative, non-https or
// otherwise malformed base URLs are treated as unconfigured.
func (s *Server) mailTaskURL(taskID string) (string, bool) {
	base := strings.TrimRight(strings.TrimSpace(s.mailTaskBaseURL), "/")
	if base == "" {
		return "", false
	}
	parsed, err := url.Parse(base)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", false
	}
	for _, r := range parsed.Hostname() {
		if r > 0x7f {
			return "", false
		}
	}
	return base + "/tasks/" + url.PathEscape(taskID), true
}

func templateReferencesVariable(item domain.MailTemplate, name string) bool {
	token := "{{" + name + "}}"
	return strings.Contains(item.SubjectTemplate, token) ||
		strings.Contains(item.BodyTextTemplate, token) ||
		strings.Contains(item.BodyHTMLTemplate, token)
}

// mailPreviewAttachments turns controlled attachment references into metadata
// only. It never reads file bytes and never resolves a client supplied path.
// The same underlying task attachment is snapshotted at most once.
func (s *Server) mailPreviewAttachments(ctx context.Context, taskID string, attachmentIDs, buildArtifactIDs []string) ([]mailpkg.Attachment, map[string]string, error) {
	if len(attachmentIDs) > mailpkg.MaxAttachments || len(buildArtifactIDs) > mailpkg.MaxAttachments {
		return nil, nil, mailpkg.ErrAttachmentLimit()
	}
	total := len(attachmentIDs) + len(buildArtifactIDs)
	if total == 0 {
		return []mailpkg.Attachment{}, nil, nil
	}
	if taskID == "" {
		return nil, nil, mailpkg.ErrAttachmentTaskRequired()
	}
	seenRefs := make(map[string]bool, total)
	seenSources := make(map[string]bool, total)
	result := make([]mailpkg.Attachment, 0, total)
	for _, id := range attachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenRefs[id] {
			return nil, nil, mailpkg.ErrAttachmentNotFound()
		}
		seenRefs[id] = true
		item, err := s.store.Attachment(ctx, taskID, id)
		if err != nil {
			if isNoRows(err) {
				return nil, nil, mailpkg.ErrAttachmentNotFound()
			}
			return nil, nil, err
		}
		if item.Size > mailpkg.MaxAttachmentBytes {
			return nil, nil, mailpkg.ErrAttachmentTooLarge()
		}
		if mailpkg.DisallowedAttachmentName(item.Name) {
			return nil, nil, mailpkg.ErrAttachmentTypeForbidden(item.Name)
		}
		if seenSources[item.ID] {
			continue
		}
		seenSources[item.ID] = true
		result = append(result, mailpkg.Attachment{
			SourceType: string(domain.MailAttachmentSourceTaskAttachment),
			SourceID:   item.ID,
			FileName:   item.Name,
			MediaType:  item.MediaType,
			SizeBytes:  item.Size,
			SHA256:     item.SHA256,
			ContentID:  item.ID,
		})
	}
	packageVariables := map[string]string{}
	for index, id := range buildArtifactIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenRefs[id] {
			return nil, nil, mailpkg.ErrAttachmentNotFound()
		}
		seenRefs[id] = true
		artifact, err := s.store.BuildArtifact(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return nil, nil, mailpkg.ErrAttachmentNotFound()
			}
			return nil, nil, err
		}
		if artifact.TaskID != taskID {
			return nil, nil, mailpkg.ErrAttachmentNotFound()
		}
		source, err := s.store.Attachment(ctx, taskID, artifact.AttachmentID)
		if err != nil {
			if isNoRows(err) {
				return nil, nil, mailpkg.ErrAttachmentUnavailable()
			}
			return nil, nil, err
		}
		if source.Size != artifact.SizeBytes || !strings.EqualFold(source.SHA256, artifact.SHA256) {
			return nil, nil, mailpkg.ErrAttachmentUnavailable()
		}
		if artifact.SizeBytes > mailpkg.MaxAttachmentBytes {
			return nil, nil, mailpkg.ErrAttachmentTooLarge()
		}
		if mailpkg.DisallowedAttachmentName(source.Name) {
			return nil, nil, mailpkg.ErrAttachmentTypeForbidden(source.Name)
		}
		if !seenSources[source.ID] {
			seenSources[source.ID] = true
			result = append(result, mailpkg.Attachment{
				SourceType: string(domain.MailAttachmentSourceBuildArtifact),
				SourceID:   artifact.ID,
				FileName:   source.Name,
				MediaType:  source.MediaType,
				SizeBytes:  artifact.SizeBytes,
				SHA256:     artifact.SHA256,
				ContentID:  source.ID,
			})
		}
		if index == 0 {
			packageVariables["package.name"] = firstNonEmpty(artifact.PackageName, source.Name)
			packageVariables["package.size"] = strconv.FormatInt(artifact.SizeBytes, 10)
			packageVariables["package.sha256"] = artifact.SHA256
			if artifact.Version != "" {
				packageVariables["build.version"] = artifact.Version
			}
		}
	}
	return result, packageVariables, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
