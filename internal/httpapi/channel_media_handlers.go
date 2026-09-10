package httpapi

import (
	"bytes"
	"io"
	"mime"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/channel"
)

func (s *Server) channelRuntimeUploadMedia(writer http.ResponseWriter, request *http.Request) {
	claims := channelRuntimeClaims(request.Context())
	leaseID := request.Header.Get("X-AHA-Lease-ID")
	if _, err := s.channels.MediaCommand(request.Context(), claims, request.PathValue("id"), leaseID); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, channel.MaxMediaFileBytes+(1<<20))
	err := request.ParseMultipartForm(1 << 20)
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_attachment")
		return
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		writeError(writer, http.StatusBadRequest, "attachment_required")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, channel.MaxMediaFileBytes+1))
	if err != nil || len(content) == 0 || int64(len(content)) > channel.MaxMediaFileBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "resource_size_invalid")
		return
	}
	item, err := s.channels.ReceiveMediaAttachment(request.Context(), claims, request.PathValue("id"), leaseID, header.Filename, content)
	if err != nil {
		if err.Error() == "resource_size_invalid" || err.Error() == "resource_type_unsupported" {
			writeError(writer, http.StatusBadRequest, err.Error())
		} else {
			writeChannelRuntimeError(writer, err)
		}
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "attachment_id": item.ID})
}

func (s *Server) channelRuntimeMediaContent(writer http.ResponseWriter, request *http.Request) {
	item, content, err := s.channels.DeliveryAttachment(request.Context(), channelRuntimeClaims(request.Context()), request.PathValue("id"), request.Header.Get("X-AHA-Lease-ID"))
	if err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", item.MediaType)
	writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.Name}))
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, item.Name, item.CreatedAt, bytes.NewReader(content))
}

func (s *Server) channelRuntimeMediaUploaded(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		LeaseID       string `json:"lease_id"`
		ResourceType  string `json:"resource_type"`
		ResourceKey   string `json:"resource_key"`
	}
	if decodeJSON(request, &payload) != nil || payload.SchemaVersion != 1 || payload.LeaseID == "" {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope")
		return
	}
	if err := s.channels.RecordMediaUpload(request.Context(), channelRuntimeClaims(request.Context()), request.PathValue("id"), payload.LeaseID, payload.ResourceType, payload.ResourceKey); err != nil {
		writeChannelRuntimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
