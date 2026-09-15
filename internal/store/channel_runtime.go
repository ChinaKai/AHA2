package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

func (s *Store) ChannelDeliveryTarget(ctx context.Context, conversationID string, sourceEventSequence int64) (map[string]string, error) {
	var chatID, senderID, instanceID string
	if err := s.db.QueryRowContext(ctx, `SELECT external_chat_id,external_sender_id,instance_id FROM channel_conversations WHERE id=? AND status='active'`, conversationID).Scan(&chatID, &senderID, &instanceID); err != nil {
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
	var taskID, roundID, sourcePayloadJSON string
	if sourceEventSequence > 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT task_id,round_id,semantic_payload_json FROM channel_source_events WHERE sequence=?`, sourceEventSequence).Scan(&taskID, &roundID, &sourcePayloadJSON)
	}
	sourcePayload := decodeJSON(sourcePayloadJSON, map[string]any{})
	if taskID != "" && roundID != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM conversation_items WHERE task_id=? AND round_id=? AND kind='user_message' ORDER BY sequence`, taskID, roundID)
		if err == nil {
			payloads := []string{}
			for rows.Next() {
				var payloadJSON string
				if rows.Scan(&payloadJSON) == nil {
					payloads = append(payloads, payloadJSON)
				}
			}
			_ = rows.Close()
			identityNames := map[string]string{}
			identityOrder := []string{}
			for _, payloadJSON := range payloads {
				payload := decodeJSON(payloadJSON, map[string]any{})
				channelContext, _ := payload["channel_context"].(map[string]any)
				if strings.TrimSpace(fmt.Sprint(channelContext["conversation_id"])) != conversationID {
					continue
				}
				receiptID := strings.TrimSpace(fmt.Sprint(payload["channel_receipt_id"]))
				if receiptID != "" && receiptID != "<nil>" {
					var normalizedPayloadJSON string
					if err := s.db.QueryRowContext(ctx, `SELECT normalized_payload_json FROM channel_inbox_dedup WHERE id=? AND conversation_id=?`, receiptID, conversationID).Scan(&normalizedPayloadJSON); err == nil {
						normalizedPayload := decodeJSON(normalizedPayloadJSON, map[string]any{})
						if messageID := strings.TrimSpace(fmt.Sprint(normalizedPayload["external_message_id"])); messageID != "" && messageID != "<nil>" {
							target["reply_message_id"] = messageID
						}
					}
				}
				actor, _ := channelContext["actor"].(map[string]any)
				identityID := strings.TrimSpace(fmt.Sprint(actor["identity_link_id"]))
				if identityID != "" && identityID != "<nil>" {
					if _, exists := identityNames[identityID]; !exists && len(identityOrder) < 5 {
						identityOrder = append(identityOrder, identityID)
						identityNames[identityID] = strings.TrimSpace(fmt.Sprint(actor["display_name"]))
					}
				}
			}
			userIDs, names := []string{}, []string{}
			for _, identityID := range identityOrder {
				var externalUserID, displayName string
				if err := s.db.QueryRowContext(ctx, `SELECT external_user_id,display_name FROM channel_identity_links WHERE id=? AND status='active'`, identityID).Scan(&externalUserID, &displayName); err != nil || externalUserID == "" {
					continue
				}
				if identityNames[identityID] != "" && identityNames[identityID] != "<nil>" {
					displayName = identityNames[identityID]
				}
				userIDs = append(userIDs, externalUserID)
				names = append(names, displayName)
			}
			if len(userIDs) > 0 {
				rawIDs, _ := json.Marshal(userIDs)
				rawNames, _ := json.Marshal(names)
				target["mention_user_ids"] = string(rawIDs)
				target["mention_names"] = string(rawNames)
			}
		}
	}
	if rawMentionIDs, ok := sourcePayload["mention_identity_link_ids"]; ok {
		raw, _ := json.Marshal(rawMentionIDs)
		var identityIDs []string
		if json.Unmarshal(raw, &identityIDs) == nil {
			userIDs, names := []string{}, []string{}
			seen := map[string]bool{}
			for _, identityID := range identityIDs {
				identityID = strings.TrimSpace(identityID)
				if identityID == "" || seen[identityID] || len(userIDs) >= 5 {
					continue
				}
				var externalUserID, displayName string
				if err := s.db.QueryRowContext(ctx, `
					SELECT identity.external_user_id,identity.display_name
					FROM channel_identity_links identity
					JOIN channel_conversation_members member
					  ON member.identity_link_id=identity.id AND member.conversation_id=?
					JOIN channel_conversations conversation ON conversation.id=member.conversation_id
					WHERE identity.id=? AND identity.instance_id=? AND identity.status='active' AND identity.role='participant'
					  AND (
						member.provider_active=1
						OR (member.is_bot=1 AND member.observed_at<>'')
						OR (conversation.members_synced_at='' AND member.observed_at<>'')
					  )`,
					conversationID, identityID, instanceID).Scan(&externalUserID, &displayName); err != nil || externalUserID == "" {
					continue
				}
				seen[identityID] = true
				userIDs = append(userIDs, externalUserID)
				names = append(names, displayName)
			}
			if len(userIDs) > 0 {
				rawIDs, _ := json.Marshal(userIDs)
				rawNames, _ := json.Marshal(names)
				target["mention_user_ids"] = string(rawIDs)
				target["mention_names"] = string(rawNames)
			}
		}
	}
	if target["reply_message_id"] == "" && roundID == "" && strings.TrimSpace(fmt.Sprint(sourcePayload["kind"])) != "agent_outreach" {
		var payloadJSON string
		if err := s.db.QueryRowContext(ctx, `SELECT normalized_payload_json FROM channel_inbox_dedup WHERE conversation_id=? AND state='processed' ORDER BY received_at DESC LIMIT 1`, conversationID).Scan(&payloadJSON); err == nil {
			payload := decodeJSON(payloadJSON, map[string]any{})
			if messageID := strings.TrimSpace(fmt.Sprint(payload["external_message_id"])); messageID != "" && messageID != "<nil>" {
				target["reply_message_id"] = messageID
			}
		}
	}
	return target, nil
}

func (s *Store) EnqueueTaskChannelOutreach(ctx context.Context, taskID, turnID, requestID, purpose, message string, identityIDs, attachmentIDs []string, at time.Time) (domain.ChannelDelivery, string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	defer tx.Rollback()
	route, err := scanChannelTaskRoute(tx.QueryRowContext(ctx, `
		SELECT id,instance_id,conversation_id,target_task_id,state,revision,pending_action_id,activated_at,exited_at,exit_reason
		FROM channel_task_routes WHERE target_task_id=? AND state='active'`, taskID))
	if err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	var endpointKind, conversationStatus, conversationName, instanceStatus, instanceName string
	if err := tx.QueryRowContext(ctx, `
		SELECT endpoint.kind,conversation.status,conversation.display_name,instance.status,instance.name
		FROM channel_conversations conversation
		JOIN channel_endpoints endpoint ON endpoint.id=conversation.endpoint_id
		JOIN channel_instances instance ON instance.id=conversation.instance_id
		WHERE conversation.id=? AND conversation.instance_id=?`, route.ConversationID, route.InstanceID).
		Scan(&endpointKind, &conversationStatus, &conversationName, &instanceStatus, &instanceName); err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	if endpointKind != domain.ChannelEndpointGroupDigitalHuman || conversationStatus != "active" || (instanceStatus != "ready" && instanceStatus != "degraded") {
		return domain.ChannelDelivery{}, "", false, errors.New("primary channel is not available for outreach")
	}
	recipients := make([]map[string]any, 0, len(identityIDs))
	for _, identityID := range identityIDs {
		var displayName, collaborationRole string
		var isBot bool
		if err := tx.QueryRowContext(ctx, `
			SELECT identity.display_name,contact.collaboration_role,member.is_bot
			FROM channel_task_contacts contact
			JOIN channel_conversation_members member
			  ON member.conversation_id=contact.conversation_id
			 AND member.identity_link_id=contact.identity_link_id
			JOIN channel_conversations conversation ON conversation.id=member.conversation_id
			JOIN channel_identity_links identity ON identity.id=member.identity_link_id
			WHERE contact.task_id=? AND contact.conversation_id=? AND contact.identity_link_id=?
			  AND identity.instance_id=? AND identity.role='participant' AND identity.status='active'
			  AND (
					member.provider_active=1
					OR (member.is_bot=1 AND member.observed_at<>'')
					OR (conversation.members_synced_at='' AND member.observed_at<>'')
			  )`,
			taskID, route.ConversationID, identityID, route.InstanceID).
			Scan(&displayName, &collaborationRole, &isBot); err != nil {
			return domain.ChannelDelivery{}, "", false, errors.New("channel contact is no longer available")
		}
		recipients = append(recipients, map[string]any{
			"identity_link_id": identityID, "display_name": displayName,
			"collaboration_role": collaborationRole, "is_bot": isBot,
		})
	}
	sourceKey := "agent-channel-outreach:" + taskID + ":" + turnID + ":" + requestID
	conversationItemID := stableStoreID("conversation_channel_outreach", taskID, turnID, requestID)
	idempotencyKey := "channel-outreach:" + sourceKey
	if item, lookupErr := scanChannelDelivery(tx.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE idempotency_key=?`, idempotencyKey)); lookupErr == nil {
		return item, conversationItemID, false, tx.Commit()
	} else if lookupErr != sql.ErrNoRows {
		return domain.ChannelDelivery{}, "", false, lookupErr
	}
	payload := map[string]any{
		"kind": "agent_outreach", "purpose": purpose, "task_id": taskID, "text": message,
		"mention_identity_link_ids": identityIDs,
	}
	attachments, err := bindAttachmentsTx(ctx, tx, taskID, conversationItemID, attachmentIDs)
	if err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	if len(attachments) > 0 {
		payload["attachments"] = attachments
	}
	sourceID := domain.NewID("channel_source_event")
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO channel_source_events(id,source_key,source_revision,task_id,round_id,turn_id,conversation_item_id,event_class,event_type,semantic_payload_json,occurred_at) VALUES(?,?,1,?,'',?,?, 'message','agent_outreach',?,?)`,
		sourceID, sourceKey, taskID, turnID, conversationItemID, encodeJSON(payload), timeString(at))
	if err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	if inserted, _ := result.RowsAffected(); inserted == 0 {
		item, lookupErr := scanChannelDelivery(tx.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE idempotency_key=?`, idempotencyKey))
		if lookupErr != nil {
			return domain.ChannelDelivery{}, "", false, lookupErr
		}
		return item, conversationItemID, false, tx.Commit()
	}
	var sourceSequence, streamSequence int64
	if err := tx.QueryRowContext(ctx, `SELECT sequence FROM channel_source_events WHERE id=?`, sourceID).Scan(&sourceSequence); err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(stream_sequence),0)+1 FROM channel_delivery_outbox WHERE conversation_id=?`, route.ConversationID).Scan(&streamSequence); err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	var subscriptionID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM channel_subscriptions WHERE route_id=? AND kind='task_route' AND state='active'`, route.ID).Scan(&subscriptionID); err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	deliveryID := ""
	for partIndex, part := range channelDeliveryParts(payload) {
		partID := domain.NewID("channel_delivery")
		partKey := idempotencyKey
		if partIndex > 0 {
			partKey += ":attachment:" + fmt.Sprint(part["attachment_id"])
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_delivery_outbox(id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			partID, route.InstanceID, route.ConversationID, subscriptionID, sourceSequence, streamSequence+int64(partIndex), 0, nil,
			partKey, "", 1, encodeJSON(part), "pending", 0, "", timeString(at), "", "", "", "", "", timeString(at), timeString(at), ""); err != nil {
			return domain.ChannelDelivery{}, "", false, err
		}
		if partIndex == 0 {
			deliveryID = partID
		}
	}
	var roundID string
	if err := tx.QueryRowContext(ctx, `SELECT round_id FROM turns WHERE id=? AND task_id=?`, turnID, taskID).Scan(&roundID); err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	card := normalizeConversationItem(domain.ConversationItem{
		ID: conversationItemID, TaskID: taskID, RoundID: roundID, TurnID: turnID,
		AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
		RouteKind: "channel_outreach", Category: "update", Kind: "agent_channel_outreach",
		Summary: fmt.Sprintf("AHA 已向 %d 位联调人主动路由阻塞协调消息", len(recipients)),
		Payload: map[string]any{
			"purpose": purpose, "message": message, "delivery_id": deliveryID, "delivery_state": "pending",
			"channel": map[string]any{
				"conversation_id": route.ConversationID, "display_name": conversationName,
				"instance_name": instanceName,
			},
			"recipients": recipients,
		},
		CreatedAt: at,
	})
	if _, err := insertConversationItem(ctx, tx, card); err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	item, err := scanChannelDelivery(tx.QueryRowContext(ctx, `SELECT `+channelDeliveryColumns+` FROM channel_delivery_outbox WHERE id=?`, deliveryID))
	if err != nil {
		return domain.ChannelDelivery{}, "", false, err
	}
	return item, conversationItemID, true, tx.Commit()
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
