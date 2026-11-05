package httpapi

import (
	"context"
	"errors"
	"net/http"
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
	SubjectTemplate   *string   `json:"subject_template"`
	BodyTextTemplate  *string   `json:"body_text_template"`
	BodyHTMLTemplate  *string   `json:"body_html_template"`
	DefaultTo         *[]string `json:"default_to"`
	DefaultCC         *[]string `json:"default_cc"`
	RequiredVariables *[]string `json:"required_variables"`
	ExpectedVersion   int       `json:"expected_version"`
}

type mailPreviewPayload struct {
	Variables        map[string]string `json:"variables"`
	To               []string          `json:"to"`
	CC               []string          `json:"cc"`
	TaskID           string            `json:"task_id"`
	AttachmentIDs    []string          `json:"attachment_ids"`
	BuildArtifactIDs []string          `json:"build_artifact_ids"`
}

func mailTemplateContent(item domain.MailTemplate) mailpkg.Template {
	return mailpkg.Template{
		ID:                item.ID,
		Version:           item.CurrentVersion,
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
		Status: domain.MailTemplateStatusActive, CurrentVersion: 1,
		CreatedBy: mailOwnerID(request),
	}
	item = applyMailTemplatePayload(item, payload)
	if strings.TrimSpace(item.Name) == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "模板名称必填")
		return
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
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "template": created})
}

func applyMailTemplatePayload(item domain.MailTemplate, payload mailTemplatePayload) domain.MailTemplate {
	if payload.Name != nil {
		item.Name = strings.TrimSpace(*payload.Name)
	}
	if payload.Description != nil {
		item.Description = strings.TrimSpace(*payload.Description)
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
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "template": updated})
}

func (s *Server) archiveMailTemplate(writer http.ResponseWriter, request *http.Request) {
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
	if existing.Status == domain.MailTemplateStatusArchived {
		writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "模板已归档")
		return
	}
	if err := s.store.ArchiveMailTemplate(request.Context(), id, nowUTC()); err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "归档邮件模板失败")
		return
	}
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
	var payload mailPreviewPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	variables := make(map[string]string, len(payload.Variables)+8)
	for name, value := range payload.Variables {
		variables[name] = value
	}
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID != "" {
		task, err := s.store.Task(ctx, taskID)
		if err != nil {
			if isNoRows(err) {
				writeError(writer, http.StatusNotFound, "task_not_found")
				return
			}
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取任务失败")
			return
		}
		// Server-resolved values win over caller input, matching design 5.1.
		variables["task.id"] = task.ID
		variables["task.title"] = task.Title
		variables["task.url"] = "/tasks/" + task.ID
		if project, err := s.store.Project(ctx, task.ProjectID); err == nil {
			variables["project.name"] = project.Name
		}
		if workspace, err := s.store.Workspace(ctx, task.WorkspaceID); err == nil {
			variables["workspace.name"] = workspace.Name
		}
	}
	attachments, packageVariables, err := s.mailPreviewAttachments(ctx, taskID, payload.AttachmentIDs, payload.BuildArtifactIDs)
	if err != nil {
		writeMailFailure(writer, err, "预览附件校验失败")
		return
	}
	if len(packageVariables) == 0 {
		// package.* values are server computed from a selected build artifact;
		// accepting caller supplied values would let them forge size/SHA256.
		for name := range variables {
			if strings.HasPrefix(name, "package.") {
				writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrTemplateInvalid, "package 变量必须由所选构建产物解析")
				return
			}
		}
	}
	for name, value := range packageVariables {
		// package.* is server authoritative. build.version may come from the
		// registered artifact but the caller may override it (design 5.1).
		if name == "build.version" && strings.TrimSpace(variables[name]) != "" {
			continue
		}
		variables[name] = value
	}
	to := payload.To
	if len(to) == 0 {
		to = item.DefaultTo
	}
	cc := payload.CC
	if len(cc) == 0 {
		cc = item.DefaultCC
	}
	preview, err := mailpkg.Render(mailTemplateContent(item), mailpkg.Input{
		Variables: variables, To: to, CC: cc, Attachments: attachments,
	})
	if err != nil {
		writeMailFailure(writer, err, "邮件预览校验失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

// mailPreviewAttachments turns controlled attachment references into metadata
// only. It never reads file bytes and never resolves a client supplied path.
func (s *Server) mailPreviewAttachments(ctx context.Context, taskID string, attachmentIDs, buildArtifactIDs []string) ([]mailpkg.Attachment, map[string]string, error) {
	total := len(attachmentIDs) + len(buildArtifactIDs)
	if total == 0 {
		return []mailpkg.Attachment{}, nil, nil
	}
	if total > mailpkg.MaxAttachments {
		return nil, nil, mailpkg.ErrAttachmentLimit()
	}
	if taskID == "" {
		return nil, nil, mailpkg.ErrAttachmentTaskRequired()
	}
	seen := make(map[string]bool, total)
	result := make([]mailpkg.Attachment, 0, total)
	for _, id := range attachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, nil, mailpkg.ErrAttachmentNotFound()
		}
		seen[id] = true
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
		if id == "" || seen[id] {
			return nil, nil, mailpkg.ErrAttachmentNotFound()
		}
		seen[id] = true
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
		result = append(result, mailpkg.Attachment{
			SourceType: string(domain.MailAttachmentSourceBuildArtifact),
			SourceID:   artifact.ID,
			FileName:   source.Name,
			MediaType:  source.MediaType,
			SizeBytes:  artifact.SizeBytes,
			SHA256:     artifact.SHA256,
			ContentID:  source.ID,
		})
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
