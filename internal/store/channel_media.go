package store

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var channelMediaErrorPattern = regexp.MustCompile(`^media_(download|transfer|read|upload_image|upload_file|record|send|progress)_(failed|http_[1-5][0-9]{2}|provider_[0-9]{1,9})$`)

func SafeChannelMediaErrorCode(code string) string {
	switch code {
	case "resource_size_invalid", "resource_type_unsupported", "resource_permission_denied":
		return code
	}
	if channelMediaErrorPattern.MatchString(code) {
		return code
	}
	return "resource_download_failed"
}

func (s *Store) ChannelCommand(ctx context.Context, instanceID, id string) (domain.ChannelPluginCommand, error) {
	return scanChannelCommand(s.db.QueryRowContext(ctx, "SELECT "+channelCommandColumns+" FROM channel_plugin_commands WHERE instance_id=? AND id=?", instanceID, id))
}

func (s *Store) ChannelInboxReceipt(ctx context.Context, instanceID, id string) (domain.ChannelInboxReceipt, error) {
	return scanChannelInbox(s.db.QueryRowContext(ctx, "SELECT "+channelInboxColumns+" FROM channel_inbox_dedup WHERE instance_id=? AND id=?", instanceID, id))
}

func (s *Store) DeferChannelInbox(ctx context.Context, id, leaseID, conversationID string, until time.Time) error {
	result, err := s.db.ExecContext(ctx, "UPDATE channel_inbox_dedup SET conversation_id=?,lease_until=? WHERE id=? AND state='processing' AND lease_id=?", conversationID, timeString(until), id, leaseID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) RetryChannelMediaCommand(ctx context.Context, instanceID, id, leaseID, errorCode string, at time.Time) error {
	item, err := s.ChannelCommand(ctx, instanceID, id)
	if err != nil || item.Kind != "download_resource" {
		return fmt.Errorf("invalid_media_command")
	}
	errorCode = SafeChannelMediaErrorCode(errorCode)
	permanent := errorCode == "resource_size_invalid" || errorCode == "resource_type_unsupported" || errorCode == "resource_permission_denied"
	if permanent || item.Attempts >= 5 {
		return s.CompleteChannelCommand(ctx, instanceID, id, leaseID, false, map[string]any{}, errorCode, at)
	}
	delay := time.Duration(1<<min(item.Attempts, 6)) * time.Second
	result, err := s.db.ExecContext(ctx, "UPDATE channel_plugin_commands SET state='pending',available_at=?,lease_id='',lease_until='',last_error_code=? WHERE id=? AND instance_id=? AND state='leased' AND lease_id=?", timeString(at.Add(delay)), errorCode, id, instanceID, leaseID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) SaveChannelMediaUpload(ctx context.Context, instanceID, id, leaseID, resourceType, resourceKey string, at time.Time) error {
	item, err := s.ChannelDelivery(ctx, id)
	if err != nil || item.InstanceID != instanceID || item.SemanticPayload["kind"] != "attachment" || item.State != "leased" || item.LeaseID != leaseID || !item.LeaseUntil.After(at) {
		return ErrChannelRevision
	}
	payload := map[string]any{}
	if resourceKey != "" {
		payload["provider_resource_type"], payload["provider_resource_key"] = resourceType, resourceKey
	}
	result, err := s.db.ExecContext(ctx, "UPDATE channel_delivery_outbox SET semantic_payload_json=json_patch(semantic_payload_json,?),updated_at=?,lease_until=? WHERE id=? AND instance_id=? AND state='leased' AND lease_id=?", encodeJSON(payload), timeString(at), timeString(at.Add(90*time.Second)), id, instanceID, leaseID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrChannelRevision
	}
	return nil
}
