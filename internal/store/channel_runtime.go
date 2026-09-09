package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateChannelCapability(ctx context.Context, item domain.ChannelServiceCapability) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_service_capabilities(id,plugin_id,instance_id,token_hash,token_secret_ref,scopes_json,status,issued_at,expires_at,last_used_at,revoked_at,rotated_from_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.PluginID, item.InstanceID, item.TokenHash, item.TokenSecretRef, encodeJSON(item.Scopes), item.Status,
		timeString(item.IssuedAt), timeString(item.ExpiresAt), timeString(item.LastUsedAt), timeString(item.RevokedAt), nullableString(item.RotatedFromID))
	return err
}

func scanChannelCapability(scanner interface{ Scan(...any) error }) (domain.ChannelServiceCapability, error) {
	var item domain.ChannelServiceCapability
	var scopes, issued, expires, lastUsed, revoked string
	var rotated sql.NullString
	err := scanner.Scan(&item.ID, &item.PluginID, &item.InstanceID, &item.TokenHash, &item.TokenSecretRef, &scopes, &item.Status, &issued, &expires, &lastUsed, &revoked, &rotated)
	item.Scopes = decodeJSON(scopes, []string{})
	item.IssuedAt, item.ExpiresAt, item.LastUsedAt, item.RevokedAt = parseTime(issued), parseTime(expires), parseTime(lastUsed), parseTime(revoked)
	if rotated.Valid {
		item.RotatedFromID = rotated.String
	}
	return item, err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) ChannelCapabilityByHash(ctx context.Context, tokenHash string) (domain.ChannelServiceCapability, error) {
	return scanChannelCapability(s.db.QueryRowContext(ctx, `SELECT id,plugin_id,instance_id,token_hash,token_secret_ref,scopes_json,status,issued_at,expires_at,last_used_at,revoked_at,rotated_from_id FROM channel_service_capabilities WHERE token_hash=?`, tokenHash))
}

func (s *Store) TouchChannelCapability(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channel_service_capabilities SET last_used_at=? WHERE id=? AND status='active'`, timeString(at), id)
	return err
}

func (s *Store) RevokeChannelCapabilities(ctx context.Context, instanceID string, at time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT token_secret_ref FROM channel_service_capabilities WHERE instance_id=? AND status='active'`, instanceID)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_service_capabilities SET status='revoked',revoked_at=? WHERE instance_id=? AND status='active'`, timeString(at), instanceID); err != nil {
		return nil, err
	}
	return refs, tx.Commit()
}

const channelCommandColumns = `id,instance_id,kind,idempotency_key,payload_json,progress_json,state,attempts,available_at,lease_id,lease_until,result_json,last_error_code,created_at,completed_at`

func scanChannelCommand(scanner interface{ Scan(...any) error }) (domain.ChannelPluginCommand, error) {
	var item domain.ChannelPluginCommand
	var payload, progress, result, available, leaseUntil, created, completed string
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.Kind, &item.IdempotencyKey, &payload, &progress, &item.State, &item.Attempts,
		&available, &item.LeaseID, &leaseUntil, &result, &item.LastErrorCode, &created, &completed)
	item.Payload = decodeJSON(payload, map[string]any{})
	item.Progress = decodeJSON(progress, map[string]any{})
	item.Result = decodeJSON(result, map[string]any{})
	item.AvailableAt, item.LeaseUntil, item.CreatedAt, item.CompletedAt = parseTime(available), parseTime(leaseUntil), parseTime(created), parseTime(completed)
	return item, err
}

func (s *Store) EnqueueChannelCommand(ctx context.Context, item domain.ChannelPluginCommand) (domain.ChannelPluginCommand, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_plugin_commands(id,instance_id,kind,idempotency_key,payload_json,progress_json,state,attempts,available_at,lease_id,lease_until,result_json,last_error_code,created_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,idempotency_key) DO NOTHING`,
		item.ID, item.InstanceID, item.Kind, item.IdempotencyKey, encodeJSON(item.Payload), encodeJSON(item.Progress), item.State, item.Attempts,
		timeString(item.AvailableAt), item.LeaseID, timeString(item.LeaseUntil), encodeJSON(item.Result), item.LastErrorCode, timeString(item.CreatedAt), timeString(item.CompletedAt))
	if err != nil {
		return domain.ChannelPluginCommand{}, err
	}
	existing, err := scanChannelCommand(s.db.QueryRowContext(ctx, `SELECT `+channelCommandColumns+` FROM channel_plugin_commands WHERE instance_id=? AND idempotency_key=?`, item.InstanceID, item.IdempotencyKey))
	if err != nil {
		return domain.ChannelPluginCommand{}, err
	}
	if existing.Kind != item.Kind || encodeJSON(existing.Payload) != encodeJSON(item.Payload) {
		return domain.ChannelPluginCommand{}, fmt.Errorf("channel command idempotency payload conflict")
	}
	return existing, nil
}

func (s *Store) ClaimChannelCommands(ctx context.Context, instanceID string, limit int, now time.Time, leaseDuration time.Duration) ([]domain.ChannelPluginCommand, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_plugin_commands SET state='pending',lease_id='',lease_until='' WHERE instance_id=? AND state='leased' AND lease_until<>'' AND lease_until<=?`, instanceID, timeString(now)); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM channel_plugin_commands WHERE instance_id=? AND state='pending' AND available_at<=? ORDER BY created_at,id LIMIT ?`, instanceID, timeString(now), limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	claimed := []domain.ChannelPluginCommand{}
	for _, id := range ids {
		leaseID := domain.NewID("channel_command_lease")
		result, err := tx.ExecContext(ctx, `UPDATE channel_plugin_commands SET state='leased',attempts=attempts+1,lease_id=?,lease_until=? WHERE id=? AND state='pending'`, leaseID, timeString(now.Add(leaseDuration)), id)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		item, err := scanChannelCommand(tx.QueryRowContext(ctx, `SELECT `+channelCommandColumns+` FROM channel_plugin_commands WHERE id=?`, id))
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) UpdateChannelCommandProgress(ctx context.Context, instanceID, id, leaseID string, progress map[string]any, leaseUntil time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_plugin_commands SET progress_json=?,lease_until=? WHERE id=? AND instance_id=? AND state='leased' AND lease_id=?`, encodeJSON(progress), timeString(leaseUntil), id, instanceID, leaseID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) CompleteChannelCommand(ctx context.Context, instanceID, id, leaseID string, success bool, result map[string]any, errorCode string, at time.Time) error {
	state := "completed"
	if !success {
		state = "failed"
	}
	updated, err := s.db.ExecContext(ctx, `UPDATE channel_plugin_commands SET state=?,result_json=?,last_error_code=?,lease_id='',lease_until='',completed_at=? WHERE id=? AND instance_id=? AND state='leased' AND lease_id=?`,
		state, encodeJSON(result), errorCode, timeString(at), id, instanceID, leaseID)
	if err != nil {
		return err
	}
	if affected, _ := updated.RowsAffected(); affected != 1 {
		existing, lookupErr := scanChannelCommand(s.db.QueryRowContext(ctx, `SELECT `+channelCommandColumns+` FROM channel_plugin_commands WHERE id=? AND instance_id=?`, id, instanceID))
		if lookupErr == nil && existing.State == state && encodeJSON(existing.Result) == encodeJSON(result) && existing.LastErrorCode == errorCode {
			return nil
		}
		return ErrChannelRevision
	}
	if !success {
		_, _ = s.db.ExecContext(ctx, `UPDATE channel_onboarding_sessions SET status='failed',step=?,updated_at=? WHERE registration_command_id=? AND status IN ('pending','qr_ready')`, errorCode, timeString(at), id)
	}
	return nil
}

const channelDeliveryColumns = `id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at`

func scanChannelDelivery(scanner interface{ Scan(...any) error }) (domain.ChannelDelivery, error) {
	var item domain.ChannelDelivery
	var subscription, replayOf sql.NullString
	var payload, firstAttempt, available, leaseUntil, created, updated, delivered string
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.ConversationID, &subscription, &item.SourceEventSequence, &item.StreamSequence,
		&item.ReplayGeneration, &replayOf, &item.IdempotencyKey, &item.CoalesceKey, &item.PayloadVersion, &payload, &item.State,
		&item.Attempts, &firstAttempt, &available, &item.LeaseID, &leaseUntil, &item.ProviderMessageID, &item.LastErrorCode,
		&item.OutcomeCertainty, &created, &updated, &delivered)
	if subscription.Valid {
		item.SubscriptionID = subscription.String
	}
	if replayOf.Valid {
		item.ReplayOfID = replayOf.String
	}
	item.SemanticPayload = decodeJSON(payload, map[string]any{})
	item.FirstAttemptAt, item.AvailableAt, item.LeaseUntil = parseTime(firstAttempt), parseTime(available), parseTime(leaseUntil)
	item.CreatedAt, item.UpdatedAt, item.DeliveredAt = parseTime(created), parseTime(updated), parseTime(delivered)
	return item, err
}

func (s *Store) EnqueueChannelDelivery(ctx context.Context, item domain.ChannelDelivery) (domain.ChannelDelivery, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_delivery_outbox(id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`,
		item.ID, item.InstanceID, item.ConversationID, nullableString(item.SubscriptionID), item.SourceEventSequence, item.StreamSequence,
		item.ReplayGeneration, nullableString(item.ReplayOfID), item.IdempotencyKey, item.CoalesceKey, item.PayloadVersion, encodeJSON(item.SemanticPayload), item.State,
		item.Attempts, timeString(item.FirstAttemptAt), timeString(item.AvailableAt), item.LeaseID, timeString(item.LeaseUntil), item.ProviderMessageID,
		item.LastErrorCode, item.OutcomeCertainty, timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.DeliveredAt))
	if err != nil {
		return domain.ChannelDelivery{}, err
	}
	return scanChannelDelivery(s.db.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE idempotency_key=?`, item.IdempotencyKey))
}

func (s *Store) ClaimChannelDeliveries(ctx context.Context, instanceID string, limit int, now time.Time, leaseDuration time.Duration) ([]domain.ChannelDelivery, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='pending',lease_id='',lease_until='',updated_at=? WHERE instance_id=? AND state='leased' AND lease_until<>'' AND lease_until<=?`, timeString(now), instanceID, timeString(now)); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT d.id FROM channel_delivery_outbox d
		WHERE d.instance_id=? AND d.state='pending' AND d.available_at<=?
		  AND NOT EXISTS(
		    SELECT 1 FROM channel_delivery_outbox prior
		    WHERE prior.conversation_id=d.conversation_id
		      AND prior.state IN ('pending','leased','dead_letter')
		      AND (prior.stream_sequence<d.stream_sequence OR (prior.stream_sequence=d.stream_sequence AND prior.replay_generation<d.replay_generation))
		  )
		ORDER BY d.available_at,d.conversation_id,d.stream_sequence,d.replay_generation LIMIT ?`, instanceID, timeString(now), limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	claimed := []domain.ChannelDelivery{}
	for _, id := range ids {
		leaseID := domain.NewID("channel_delivery_lease")
		result, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='leased',attempts=attempts+1,first_attempt_at=CASE WHEN first_attempt_at='' THEN ? ELSE first_attempt_at END,lease_id=?,lease_until=?,updated_at=? WHERE id=? AND state='pending'`,
			timeString(now), leaseID, timeString(now.Add(leaseDuration)), timeString(now), id)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		item, err := scanChannelDelivery(tx.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE id=?`, id))
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_delivery_attempts(id,delivery_id,attempt_no,lease_id,outcome,error_code,retry_after_ms,provider_request_id,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			domain.NewID("channel_delivery_attempt"), item.ID, item.Attempts, leaseID, "leased", "", 0, "", timeString(now), ""); err != nil {
			return nil, err
		}
		claimed = append(claimed, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) AckChannelDelivery(ctx context.Context, instanceID, id, leaseID, providerMessageID, providerRequestID string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='delivered',provider_message_id=?,outcome_certainty='confirmed',lease_id='',lease_until='',updated_at=?,delivered_at=? WHERE id=? AND instance_id=? AND state='leased' AND lease_id=?`,
		providerMessageID, timeString(at), timeString(at), id, instanceID, leaseID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		var state, messageID string
		lookupErr := tx.QueryRowContext(ctx, `SELECT state,provider_message_id FROM channel_delivery_outbox WHERE id=? AND instance_id=?`, id, instanceID).Scan(&state, &messageID)
		if lookupErr == nil && state == "delivered" && messageID == providerMessageID {
			return nil
		}
		return ErrChannelRevision
	}
	var semanticJSON, conversationID string
	if err := tx.QueryRowContext(ctx, `SELECT semantic_payload_json,conversation_id FROM channel_delivery_outbox WHERE id=?`, id).Scan(&semanticJSON, &conversationID); err != nil {
		return err
	}
	semantic := decodeJSON(semanticJSON, map[string]any{})
	if semantic["kind"] == "confirmation" {
		actionID := fmt.Sprint(semantic["action_id"])
		binding, err := tx.ExecContext(ctx, `UPDATE channel_pending_actions SET provider_message_id=? WHERE id=? AND instance_id=? AND conversation_id=? AND status='pending' AND (provider_message_id='' OR provider_message_id=?)`, providerMessageID, actionID, instanceID, conversationID, providerMessageID)
		if err != nil {
			return err
		}
		if affected, _ := binding.RowsAffected(); affected != 1 {
			var status, boundMessage string
			lookupErr := tx.QueryRowContext(ctx, `SELECT status,provider_message_id FROM channel_pending_actions WHERE id=?`, actionID).Scan(&status, &boundMessage)
			if lookupErr != nil || (status == "pending" && boundMessage != providerMessageID) {
				return fmt.Errorf("pending action provider message binding conflict")
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_delivery_attempts SET outcome='delivered',provider_request_id=?,finished_at=? WHERE delivery_id=? AND lease_id=?`, providerRequestID, timeString(at), id, leaseID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) NackChannelDelivery(ctx context.Context, instanceID, id, leaseID, errorCode, certainty string, retryAfter time.Duration, permanent bool, at time.Time) error {
	state := "pending"
	available := at.Add(retryAfter)
	if permanent {
		state = "dead_letter"
		available = at
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state=?,available_at=?,lease_id='',lease_until='',last_error_code=?,outcome_certainty=?,updated_at=? WHERE id=? AND instance_id=? AND state='leased' AND lease_id=?`,
		state, timeString(available), errorCode, certainty, timeString(at), id, instanceID, leaseID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		var exists bool
		lookupErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_delivery_attempts WHERE delivery_id=? AND lease_id=? AND finished_at<>'')`, id, leaseID).Scan(&exists)
		if lookupErr == nil && exists {
			return nil
		}
		return ErrChannelRevision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_delivery_attempts SET outcome=?,error_code=?,retry_after_ms=?,finished_at=? WHERE delivery_id=? AND lease_id=?`,
		state, errorCode, retryAfter.Milliseconds(), timeString(at), id, leaseID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplayChannelDelivery(ctx context.Context, id, ownerInstanceID string, at time.Time) (domain.ChannelDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelDelivery{}, err
	}
	defer tx.Rollback()
	original, err := scanChannelDelivery(tx.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE id=? AND instance_id=?`, id, ownerInstanceID))
	if err != nil {
		return domain.ChannelDelivery{}, err
	}
	if original.State != "dead_letter" {
		return domain.ChannelDelivery{}, fmt.Errorf("delivery is not a dead letter")
	}
	var generation int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(replay_generation),0)+1 FROM channel_delivery_outbox WHERE conversation_id=? AND stream_sequence=?`, original.ConversationID, original.StreamSequence).Scan(&generation); err != nil {
		return domain.ChannelDelivery{}, err
	}
	replay := original
	replay.ID = domain.NewID("channel_delivery")
	replay.ReplayGeneration, replay.ReplayOfID = generation, original.ID
	replay.IdempotencyKey = original.IdempotencyKey + ":replay:" + fmt.Sprint(generation)
	replay.State, replay.Attempts, replay.ProviderMessageID, replay.LastErrorCode, replay.OutcomeCertainty = "pending", 0, "", "", ""
	replay.FirstAttemptAt, replay.AvailableAt, replay.LeaseUntil, replay.DeliveredAt = time.Time{}, at, time.Time{}, time.Time{}
	replay.LeaseID, replay.CreatedAt, replay.UpdatedAt = "", at, at
	if _, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='replayed',updated_at=? WHERE id=? AND state='dead_letter'`, timeString(at), original.ID); err != nil {
		return domain.ChannelDelivery{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_delivery_outbox(id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		replay.ID, replay.InstanceID, replay.ConversationID, nullableString(replay.SubscriptionID), replay.SourceEventSequence, replay.StreamSequence,
		replay.ReplayGeneration, replay.ReplayOfID, replay.IdempotencyKey, replay.CoalesceKey, replay.PayloadVersion, encodeJSON(replay.SemanticPayload), replay.State, 0,
		"", timeString(at), "", "", "", "", "", timeString(at), timeString(at), ""); err != nil {
		return domain.ChannelDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelDelivery{}, err
	}
	return replay, nil
}

func (s *Store) SkipChannelDelivery(ctx context.Context, id, instanceID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_delivery_outbox SET state='skipped',updated_at=? WHERE id=? AND instance_id=? AND state='dead_letter'`, timeString(at), id, instanceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) ChannelDeliveries(ctx context.Context, instanceID string, limit int) ([]domain.ChannelDelivery, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE instance_id=? ORDER BY created_at DESC LIMIT ?`, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ChannelDelivery{}
	for rows.Next() {
		item, err := scanChannelDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ChannelDelivery(ctx context.Context, id string) (domain.ChannelDelivery, error) {
	return scanChannelDelivery(s.db.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE id=?`, id))
}

func (s *Store) ChannelDeliveryTarget(ctx context.Context, conversationID string) (map[string]string, error) {
	var chatID, senderID string
	if err := s.db.QueryRowContext(ctx, `SELECT external_chat_id,external_sender_id FROM channel_conversations WHERE id=? AND status='active'`, conversationID).Scan(&chatID, &senderID); err != nil {
		return nil, err
	}
	target := map[string]string{"sender_id": senderID}
	if strings.HasPrefix(chatID, "open_id:") {
		target["receive_id_type"] = "open_id"
		target["receive_id"] = strings.TrimPrefix(chatID, "open_id:")
	} else {
		target["receive_id_type"] = "chat_id"
		target["receive_id"] = chatID
		target["chat_id"] = chatID
	}
	var payloadJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT normalized_payload_json FROM channel_inbox_dedup WHERE conversation_id=? AND state='processed' ORDER BY received_at DESC LIMIT 1`, conversationID).Scan(&payloadJSON); err == nil {
		payload := decodeJSON(payloadJSON, map[string]any{})
		if messageID := strings.TrimSpace(fmt.Sprint(payload["external_message_id"])); messageID != "" && messageID != "<nil>" {
			target["reply_message_id"] = messageID
		}
	}
	return target, nil
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
