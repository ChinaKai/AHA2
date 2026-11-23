package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type materializeAttachmentInput struct {
	AttachmentID string `json:"attachment_id"`
}

func (s *Server) materializeAgentAttachment(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	var input materializeAttachmentInput
	request.Body = http.MaxBytesReader(writer, request.Body, 4096)
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_attachment_request")
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	result, status, code := s.agentAttachmentToScratch(request, call, input.AttachmentID)
	if code != "ok" {
		writeError(writer, status, code)
		return
	}
	writeJSON(writer, status, result)
}

func (s *Server) agentAttachmentToScratch(request *http.Request, call app.AgentCallContext, attachmentID string) (app.AgentAttachmentMaterialization, int, string) {
	result, err := s.app.MaterializeAgentAttachment(request.Context(), call, attachmentID, s.attachmentLimit)
	status, code := attachmentMaterializeStatus(err)
	if !errors.Is(err, app.ErrAgentAttachmentInvalidID) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), time.Second)
		defer cancel()
		s.audit(request.WithContext(ctx), "agent.attachment.materialize", "attachment", attachmentID, map[string]any{
			"size": result.Size, "result": code,
		})
	}
	return result, status, code
}

func attachmentMaterializeStatus(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, "ok"
	case errors.Is(err, app.ErrAgentAttachmentInvalidID):
		return http.StatusBadRequest, "invalid_attachment_id"
	case errors.Is(err, app.ErrAgentAttachmentNotFound):
		return http.StatusNotFound, "attachment_not_found"
	case errors.Is(err, app.ErrAgentAttachmentBlob):
		return http.StatusNotFound, "attachment_content_not_found"
	case errors.Is(err, workspace.ErrScratchAttachmentTooLarge):
		return http.StatusRequestEntityTooLarge, "attachment_too_large"
	case errors.Is(err, workspace.ErrScratchAttachmentIntegrity):
		return http.StatusConflict, "attachment_integrity_failed"
	case errors.Is(err, workspace.ErrScratchAttachmentUnsupported):
		return http.StatusNotImplemented, "attachment_workspace_unsupported"
	case errors.Is(err, workspace.ErrScratchAttachmentDestination):
		return http.StatusConflict, "attachment_scratch_unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, io.ErrUnexpectedEOF):
		return http.StatusRequestTimeout, "attachment_transfer_interrupted"
	default:
		return http.StatusInternalServerError, "attachment_materialize_failed"
	}
}
