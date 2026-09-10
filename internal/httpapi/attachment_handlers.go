package httpapi

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const maxAttachmentBytes int64 = 25 << 20

func (s *Server) uploadTaskAttachment(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if s.rejectRetiredChannelTaskWrite(writer, request, taskID) {
		return
	}
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	s.createTaskAttachment(writer, request, taskID, "attachment.upload")
}

func (s *Server) uploadAgentAttachment(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.createTaskAttachment(writer, request, call.Task.ID, "agent.attachment.upload")
}

func (s *Server) createTaskAttachment(writer http.ResponseWriter, request *http.Request, taskID, auditAction string) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxAttachmentBytes+(1<<20))
	if err := request.ParseMultipartForm(maxAttachmentBytes + (1 << 20)); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_attachment")
		return
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		writeError(writer, http.StatusBadRequest, "attachment_required")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil || len(content) == 0 || int64(len(content)) > maxAttachmentBytes {
		writeError(writer, http.StatusBadRequest, "invalid_attachment_size")
		return
	}
	mediaType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if value, _, parseErr := mime.ParseMediaType(mediaType); parseErr == nil {
		mediaType = value
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(content)
	}
	item, err := s.store.CreateAttachment(request.Context(), taskID, header.Filename, mediaType, content, time.Now().UTC())
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "create_attachment_failed", "message": err.Error()})
		return
	}
	s.audit(request, auditAction, "attachment", item.ID, map[string]any{"task_id": taskID, "media_type": item.MediaType, "size": item.Size})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "attachment": item})
}

func (s *Server) taskAttachmentContent(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.Attachment(request.Context(), request.PathValue("id"), request.PathValue("attachment"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "attachment_not_found")
		return
	}
	content, err := s.store.AttachmentContent(item)
	if err != nil {
		writeError(writer, http.StatusNotFound, "attachment_content_not_found")
		return
	}
	disposition := "attachment"
	switch item.MediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		disposition = "inline"
	}
	writer.Header().Set("Content-Type", item.MediaType)
	writer.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Name}))
	writer.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(writer, request, item.Name, item.CreatedAt, bytes.NewReader(content))
}

func (s *Server) deleteTaskAttachment(writer http.ResponseWriter, request *http.Request) {
	if s.rejectRetiredChannelTaskWrite(writer, request, request.PathValue("id")) {
		return
	}
	err := s.store.DeleteDraftAttachment(request.Context(), request.PathValue("id"), request.PathValue("attachment"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "delete_attachment_failed", "message": err.Error()})
		return
	}
	s.audit(request, "attachment.delete", "attachment", request.PathValue("attachment"), map[string]any{"task_id": request.PathValue("id")})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
