package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	qrcode "github.com/skip2/go-qrcode"
)

func (s *Server) channelProviders(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "providers": []any{}})
		return
	}
	items, err := s.channels.Providers(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_channel_providers_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "providers": items})
}

func (s *Server) updateChannelPlugin(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(request, &payload); err != nil || payload.Enabled == nil {
		writeError(writer, http.StatusBadRequest, "invalid_channel_plugin_update")
		return
	}
	item, err := s.channels.SetPluginEnabled(request.Context(), request.PathValue("id"), *payload.Enabled, revision)
	if err != nil {
		writeChannelError(writer, err, "update_channel_plugin_failed")
		return
	}
	writeChannelResource(writer, http.StatusOK, item.Revision, map[string]any{"ok": true, "plugin": item})
}

func (s *Server) channelInstances(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "instances": []any{}})
		return
	}
	session, _ := sessionFromContext(request.Context())
	items, err := s.channels.Instances(request.Context(), session.OwnerID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_channel_instances_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "instances": items})
}

func (s *Server) createChannelInstance(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		writeError(writer, http.StatusBadRequest, "idempotency_key_required")
		return
	}
	var payload struct {
		PluginID string `json:"plugin_id"`
		Name     string `json:"name"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.CreateInstance(request.Context(), session.OwnerID, payload.PluginID, payload.Name, idempotencyKey)
	if err != nil {
		writeChannelError(writer, err, "create_channel_instance_failed")
		return
	}
	s.audit(request, "channel.instance.create", "channel_instance", item.ID, map[string]any{"plugin_id": item.PluginID})
	writeChannelResource(writer, http.StatusCreated, item.Revision, map[string]any{"ok": true, "instance": item})
}

func (s *Server) channelInstance(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusNotFound, "channel_instance_not_found")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, endpoints, err := s.channels.Instance(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "get_channel_instance_failed")
		return
	}
	writeChannelResource(writer, http.StatusOK, item.Revision, map[string]any{"ok": true, "instance": item, "endpoints": endpoints})
}

func (s *Server) updateChannelInstance(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Name    string         `json:"name"`
		Config  map[string]any `json:"config"`
		Enabled *bool          `json:"enabled"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	session, _ := sessionFromContext(request.Context())
	var item domain.ChannelInstance
	var err error
	if payload.Enabled != nil {
		item, err = s.channels.SetInstanceEnabled(request.Context(), session.OwnerID, request.PathValue("id"), *payload.Enabled, revision)
	} else {
		item, err = s.channels.UpdateInstance(request.Context(), session.OwnerID, request.PathValue("id"), payload.Name, payload.Config, revision)
	}
	if err != nil {
		writeChannelError(writer, err, "update_channel_instance_failed")
		return
	}
	s.audit(request, "channel.instance.update", "channel_instance", item.ID, nil)
	writeChannelResource(writer, http.StatusOK, item.Revision, map[string]any{"ok": true, "instance": item})
}

func (s *Server) channelInstanceHasActiveTurn(ctx context.Context, ownerID, id string) (bool, error) {
	item, _, err := s.channels.Instance(ctx, ownerID, id)
	if err != nil {
		return false, err
	}
	tasks, err := s.store.ListTasks(ctx, item.HostProjectID)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		active, err := s.taskHasActiveTurn(ctx, task.ID)
		if err != nil {
			return false, err
		}
		if active {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) resetChannelBinding(writer http.ResponseWriter, request *http.Request) {
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.ResetBinding(request.Context(), session.OwnerID, request.PathValue("id"), revision)
	if err != nil {
		writeChannelError(writer, err, "reset_channel_binding_failed")
		return
	}
	s.audit(request, "channel.instance.reset_binding", "channel_instance", item.ID, nil)
	writeChannelResource(writer, http.StatusOK, item.Revision, map[string]any{"ok": true, "instance": item})
}

func (s *Server) archiveChannelInstance(writer http.ResponseWriter, request *http.Request) {
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	session, _ := sessionFromContext(request.Context())
	active, err := s.channelInstanceHasActiveTurn(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "archive_channel_instance_failed")
		return
	}
	if active {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "active_turn_exists", "message": "渠道宿主仍有执行中的 Turn，请先中断或等待完成"})
		return
	}
	item, err := s.channels.ArchiveInstance(request.Context(), session.OwnerID, request.PathValue("id"), revision)
	if err != nil {
		writeChannelError(writer, err, "archive_channel_instance_failed")
		return
	}
	s.audit(request, "channel.instance.archive", "channel_instance", item.ID, map[string]any{"remote_application_deleted": false})
	writeChannelResource(writer, http.StatusOK, item.Revision, map[string]any{"ok": true, "instance": item})
}

func (s *Server) channelPurgePreview(writer http.ResponseWriter, request *http.Request) {
	session, _ := sessionFromContext(request.Context())
	preview, err := s.channels.PurgePreview(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "channel_purge_preview_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

func (s *Server) purgeChannelInstance(writer http.ResponseWriter, request *http.Request) {
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ConfirmationName string `json:"confirmation_name"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	session, _ := sessionFromContext(request.Context())
	preview, err := s.channels.PurgePreview(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "purge_channel_instance_failed")
		return
	}
	if strings.TrimSpace(payload.ConfirmationName) != preview.Name {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "channel_purge_confirmation_mismatch", "message": "实例名称确认不匹配"})
		return
	}
	item, err := s.channels.PurgeInstance(request.Context(), session.OwnerID, request.PathValue("id"), revision)
	if err != nil {
		writeChannelError(writer, err, "purge_channel_instance_failed")
		return
	}
	s.audit(request, "channel.instance.purge", "channel_instance", item.ID, map[string]any{"tasks": preview.Tasks, "conversations": preview.Conversations, "messages": preview.Messages, "attachments": preview.Attachments, "remote_application_deleted": false})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "purged": preview})
}

func (s *Server) updateChannelCredentials(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	var payload struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.StoreExistingCredential(request.Context(), session.OwnerID, request.PathValue("id"), payload.AppID, payload.AppSecret, revision)
	if err != nil {
		writeChannelError(writer, err, "update_channel_credentials_failed")
		return
	}
	s.audit(request, "channel.credentials.update", "channel_instance", item.ID, map[string]any{"credential_configured": true})
	writeChannelResource(writer, http.StatusOK, item.Revision, map[string]any{"ok": true, "instance": item})
}

func (s *Server) startChannelOnboarding(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(writer, http.StatusBadRequest, "idempotency_key_required")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.StartOnboarding(request.Context(), session.OwnerID, session.ID, request.PathValue("id"), idempotencyKey)
	if err != nil {
		writeChannelError(writer, err, "start_channel_onboarding_failed")
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	s.audit(request, "channel.onboarding.start", "channel_instance", item.InstanceID, nil)
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "onboarding": item})
}

func (s *Server) channelOnboarding(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusNotFound, "channel_onboarding_not_found")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.OwnerOnboarding(request.Context(), session.OwnerID, session.ID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "get_channel_onboarding_failed")
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "onboarding": item})
}

func (s *Server) channelOnboardingQR(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusNotFound, "channel_onboarding_not_found")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.OwnerOnboarding(request.Context(), session.OwnerID, session.ID, request.PathValue("id"))
	if err != nil || item.VerificationURL == "" || item.Status != "qr_ready" {
		writeError(writer, http.StatusNotFound, "channel_onboarding_qr_not_ready")
		return
	}
	png, err := qrcode.Encode(item.VerificationURL, qrcode.Medium, 288)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "channel_onboarding_qr_failed")
		return
	}
	writer.Header().Set("Content-Type", "image/png")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(png)
}

func (s *Server) cancelChannelOnboarding(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusNotFound, "channel_onboarding_not_found")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.CancelOnboarding(request.Context(), session.OwnerID, session.ID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "cancel_channel_onboarding_failed")
		return
	}
	s.audit(request, "channel.onboarding.cancel", "channel_instance", item.InstanceID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "onboarding": item})
}

func (s *Server) channelHandoffs(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "handoffs": []any{}})
		return
	}
	session, _ := sessionFromContext(request.Context())
	items, err := s.channels.OwnerHandoffs(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "list_channel_handoffs_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "handoffs": items})
}

func (s *Server) channelDeliveries(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deliveries": []any{}})
		return
	}
	session, _ := sessionFromContext(request.Context())
	items, err := s.channels.OwnerDeliveries(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "list_channel_deliveries_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deliveries": items})
}

func (s *Server) replayChannelDelivery(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.ReplayOwnerDelivery(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "replay_channel_delivery_failed")
		return
	}
	s.audit(request, "channel.delivery.replay", "channel_delivery", item.ID, map[string]any{"replay_of_id": item.ReplayOfID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "delivery": item})
}

func (s *Server) skipChannelDelivery(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	session, _ := sessionFromContext(request.Context())
	if err := s.channels.SkipOwnerDelivery(request.Context(), session.OwnerID, request.PathValue("id")); err != nil {
		writeChannelError(writer, err, "skip_channel_delivery_failed")
		return
	}
	s.audit(request, "channel.delivery.skip", "channel_delivery", request.PathValue("id"), nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) channelKnowledgePolicy(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "policies": []any{}})
		return
	}
	session, _ := sessionFromContext(request.Context())
	items, err := s.channels.OwnerKnowledgePolicies(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "get_channel_knowledge_policy_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "policies": items})
}

func (s *Server) updateChannelKnowledgePolicy(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	revision, ok := channelIfMatch(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Endpoint  string                        `json:"endpoint"`
		ScopeMode string                        `json:"scope_mode"`
		Grants    []channel.KnowledgeGrantInput `json:"grants"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	session, _ := sessionFromContext(request.Context())
	items, err := s.channels.ReplaceOwnerKnowledgeGrants(request.Context(), session.OwnerID, request.PathValue("id"), payload.Endpoint, payload.ScopeMode, revision, payload.Grants)
	if err != nil {
		writeChannelError(writer, err, "update_channel_knowledge_policy_failed")
		return
	}
	s.audit(request, "channel.knowledge_policy.update", "channel_instance", request.PathValue("id"), map[string]any{"endpoint": payload.Endpoint, "scope_mode": payload.ScopeMode, "grant_count": len(payload.Grants)})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "policies": items})
}

func (s *Server) channelKnowledgeRecords(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "records": []any{}})
		return
	}
	session, _ := sessionFromContext(request.Context())
	items, err := s.channels.OwnerKnowledgeRecords(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeChannelError(writer, err, "list_channel_knowledge_records_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "records": items})
}

func (s *Server) promoteChannelKnowledgeRecord(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	var payload struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	session, _ := sessionFromContext(request.Context())
	item, err := s.channels.PromoteOwnerKnowledgeRecord(request.Context(), session.OwnerID, request.PathValue("id"), payload.Title, payload.Body)
	if err != nil {
		writeChannelError(writer, err, "promote_channel_knowledge_record_failed")
		return
	}
	s.audit(request, "channel.knowledge_record.promote", "channel_knowledge_record", item.ID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "record": item})
}

func channelIfMatch(writer http.ResponseWriter, request *http.Request) (int, bool) {
	value := strings.TrimSpace(request.Header.Get("If-Match"))
	value = strings.TrimPrefix(value, "W/")
	value = strings.Trim(value, `"`)
	revision, err := strconv.Atoi(value)
	if err != nil || revision < 1 {
		writeError(writer, http.StatusPreconditionRequired, "if_match_required")
		return 0, false
	}
	return revision, true
}

func writeChannelResource(writer http.ResponseWriter, status, revision int, payload any) {
	writer.Header().Set("ETag", `"`+strconv.Itoa(revision)+`"`)
	writeJSON(writer, status, payload)
}

func writeChannelError(writer http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(writer, http.StatusNotFound, "channel_resource_not_found")
	case channel.IsRevisionConflict(err):
		writeError(writer, http.StatusPreconditionFailed, "channel_revision_conflict")
	case errors.Is(err, store.ErrChannelNotRetired):
		writeError(writer, http.StatusConflict, "channel_instance_not_archived")
	case errors.Is(err, store.ErrActiveTurn):
		writeError(writer, http.StatusConflict, "active_turn_exists")
	case strings.Contains(err.Error(), "unavailable"):
		writeError(writer, http.StatusConflict, "channel_provider_unavailable")
	case strings.Contains(err.Error(), "idempotency key"):
		writeError(writer, http.StatusConflict, "idempotency_key_conflict")
	default:
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": fallback, "message": err.Error()})
	}
}
