package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const channelPendingActionColumns = `id,instance_id,conversation_id,actor_identity_link_id,operation,target_type,target_id,intent_json,preview_json,precondition_json,precondition_hash,status,provider_message_id,expires_at,consumed_at,created_at,updated_at`

func scanChannelPendingAction(scanner interface{ Scan(...any) error }) (domain.ChannelPendingAction, error) {
	var item domain.ChannelPendingAction
	var intent, preview, precondition, expires, consumed, created, updated string
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.ConversationID, &item.ActorIdentityLinkID, &item.Operation, &item.TargetType,
		&item.TargetID, &intent, &preview, &precondition, &item.PreconditionHash, &item.Status, &item.ProviderMessageID,
		&expires, &consumed, &created, &updated)
	item.Intent = decodeJSON(intent, map[string]any{})
	item.Preview = decodeJSON(preview, map[string]any{})
	item.Precondition = decodeJSON(precondition, map[string]any{})
	item.ExpiresAt, item.ConsumedAt, item.CreatedAt, item.UpdatedAt = parseTime(expires), parseTime(consumed), parseTime(created), parseTime(updated)
	return item, err
}

func (s *Store) ChannelPendingAction(ctx context.Context, id string) (domain.ChannelPendingAction, error) {
	return scanChannelPendingAction(s.db.QueryRowContext(ctx, `SELECT `+channelPendingActionColumns+` FROM channel_pending_actions WHERE id=?`, id))
}

func (s *Store) CreateChannelPendingAction(ctx context.Context, item domain.ChannelPendingAction) (domain.ChannelPendingAction, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelPendingAction{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_pending_actions SET status='superseded',updated_at=? WHERE conversation_id=? AND status='pending'`, timeString(item.CreatedAt), item.ConversationID); err != nil {
		return domain.ChannelPendingAction{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_pending_actions(id,instance_id,conversation_id,actor_identity_link_id,operation,target_type,target_id,intent_json,preview_json,precondition_json,precondition_hash,status,provider_message_id,expires_at,consumed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.InstanceID, item.ConversationID, item.ActorIdentityLinkID, item.Operation, item.TargetType, item.TargetID,
		encodeJSON(item.Intent), encodeJSON(item.Preview), encodeJSON(item.Precondition), item.PreconditionHash, item.Status,
		item.ProviderMessageID, timeString(item.ExpiresAt), timeString(item.ConsumedAt), timeString(item.CreatedAt), timeString(item.UpdatedAt)); err != nil {
		return domain.ChannelPendingAction{}, err
	}
	payload := map[string]any{"kind": "confirmation", "action_id": item.ID, "operation": item.Operation, "preview": item.Preview, "expires_at": timeString(item.ExpiresAt)}
	if err := enqueueChannelActionDeliveryTx(ctx, tx, item.InstanceID, item.ConversationID, item.ID+":preview", "action_preview", payload, item.CreatedAt); err != nil {
		return domain.ChannelPendingAction{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelPendingAction{}, err
	}
	return item, nil
}

func enqueueChannelActionDeliveryTx(ctx context.Context, tx *sql.Tx, instanceID, conversationID, sourceKey, eventType string, payload map[string]any, at time.Time) error {
	var taskID string
	if err := tx.QueryRowContext(ctx, `SELECT host_task_id FROM channel_conversations WHERE id=? AND instance_id=?`, conversationID, instanceID).Scan(&taskID); err != nil {
		return err
	}
	sourceID := domain.NewID("channel_source_event")
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO channel_source_events(id,source_key,source_revision,task_id,round_id,turn_id,conversation_item_id,event_class,event_type,semantic_payload_json,occurred_at) VALUES(?,?,1,?,'','','','channel_control',?,?,?)`,
		sourceID, sourceKey, taskID, eventType, encodeJSON(payload), timeString(at))
	if err != nil {
		return err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return nil
	}
	var sourceSequence, streamSequence int64
	if err := tx.QueryRowContext(ctx, `SELECT sequence FROM channel_source_events WHERE id=?`, sourceID).Scan(&sourceSequence); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(stream_sequence),0)+1 FROM channel_delivery_outbox WHERE conversation_id=?`, conversationID).Scan(&streamSequence); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_delivery_outbox(id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		domain.NewID("channel_delivery"), instanceID, conversationID, nil, sourceSequence, streamSequence, 0, nil,
		"channel-control:"+sourceKey, "", 1, encodeJSON(payload), "pending", 0, "", timeString(at), "", "", "", "", "", timeString(at), timeString(at), "")
	return err
}

func (s *Store) EnqueueChannelControlDelivery(ctx context.Context, instanceID, conversationID, sourceKey, eventType string, payload map[string]any, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := enqueueChannelActionDeliveryTx(ctx, tx, instanceID, conversationID, sourceKey, eventType, payload, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BeginChannelPendingAction(ctx context.Context, id, instanceID, conversationID, actorIdentityID, providerMessageID string, at time.Time) (domain.ChannelPendingAction, bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_pending_actions SET status='executing',consumed_at=?,updated_at=? WHERE id=? AND instance_id=? AND conversation_id=? AND actor_identity_link_id=? AND provider_message_id=? AND status='pending' AND expires_at>?`,
		timeString(at), timeString(at), id, instanceID, conversationID, actorIdentityID, providerMessageID, timeString(at))
	if err != nil {
		return domain.ChannelPendingAction{}, false, err
	}
	action, lookupErr := s.ChannelPendingAction(ctx, id)
	if lookupErr != nil {
		return domain.ChannelPendingAction{}, false, lookupErr
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		return action, true, nil
	}
	if action.Status == "executing" && action.InstanceID == instanceID && action.ConversationID == conversationID && action.ActorIdentityLinkID == actorIdentityID && action.ProviderMessageID == providerMessageID {
		return action, true, nil
	}
	if action.Status == "succeeded" || action.Status == "cancelled" || action.Status == "failed" {
		return action, false, nil
	}
	return action, false, ErrChannelRevision
}

func (s *Store) FinishChannelPendingAction(ctx context.Context, id, targetID, status string, resultPayload map[string]any, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	action, err := scanChannelPendingAction(tx.QueryRowContext(ctx, `SELECT `+channelPendingActionColumns+` FROM channel_pending_actions WHERE id=?`, id))
	if err != nil {
		return err
	}
	if action.Status == status {
		return tx.Commit()
	}
	if action.Status != "executing" {
		return ErrChannelRevision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_pending_actions SET target_id=?,status=?,updated_at=? WHERE id=? AND status='executing'`, targetID, status, timeString(at), id); err != nil {
		return err
	}
	payload := map[string]any{"kind": "action_result", "action_id": id, "operation": action.Operation, "status": status, "result": resultPayload}
	if err := enqueueChannelActionDeliveryTx(ctx, tx, action.InstanceID, action.ConversationID, id+":result:"+status, "action_result", payload, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ActivateChannelTaskRoute(ctx context.Context, action domain.ChannelPendingAction, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_task_routes SET state='superseded',exited_at=?,exit_reason='superseded by confirmed takeover' WHERE conversation_id=? AND state='active'`, timeString(at), action.ConversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET state='closed',updated_at=? WHERE conversation_id=? AND kind='task_route' AND state='active'`, timeString(at), action.ConversationID); err != nil {
		return err
	}
	routeID := domain.NewID("channel_route")
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_task_routes(id,instance_id,conversation_id,target_task_id,state,revision,pending_action_id,activated_at,exited_at,exit_reason) VALUES(?,?,?,?, 'active',1,?,?,'','')`,
		routeID, action.InstanceID, action.ConversationID, action.TargetID, action.ID, timeString(at)); err != nil {
		return err
	}
	var cursor int64
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM channel_source_events`).Scan(&cursor)
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_subscriptions(id,instance_id,conversation_id,route_id,source_task_id,kind,filter_version,filter_json,source_cursor,state,created_at,updated_at) VALUES(?,?,?,?,?,'task_route',1,'{}',?,'active',?,?)`,
		domain.NewID("channel_subscription"), action.InstanceID, action.ConversationID, routeID, action.TargetID, cursor, timeString(at), timeString(at)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_sessions SET mode='task_route' WHERE conversation_id=? AND status='active'`, action.ConversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_pending_actions SET status='succeeded',updated_at=? WHERE id=? AND status='executing'`, timeString(at), action.ID); err != nil {
		return err
	}
	payload := map[string]any{"kind": "action_result", "action_id": action.ID, "operation": "takeover", "status": "succeeded", "target_task_id": action.TargetID}
	if err := enqueueChannelActionDeliveryTx(ctx, tx, action.InstanceID, action.ConversationID, action.ID+":result:succeeded", "route_entered", payload, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ExitChannelTaskRoute(ctx context.Context, action domain.ChannelPendingAction, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE channel_task_routes SET state='exited',exited_at=?,exit_reason='owner confirmed exit' WHERE conversation_id=? AND target_task_id=? AND state='active'`, timeString(at), action.ConversationID, action.TargetID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("active channel route changed")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET state='closed',updated_at=? WHERE conversation_id=? AND kind='task_route' AND state='active'`, timeString(at), action.ConversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_sessions SET mode='assistant' WHERE conversation_id=? AND status='active'`, action.ConversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_pending_actions SET status='succeeded',updated_at=? WHERE id=? AND status='executing'`, timeString(at), action.ID); err != nil {
		return err
	}
	payload := map[string]any{"kind": "action_result", "action_id": action.ID, "operation": "exit", "status": "succeeded"}
	if err := enqueueChannelActionDeliveryTx(ctx, tx, action.InstanceID, action.ConversationID, action.ID+":result:succeeded", "route_exited", payload, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BindChannelActionProviderMessage(ctx context.Context, actionID, instanceID, conversationID, providerMessageID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_pending_actions SET provider_message_id=? WHERE id=? AND instance_id=? AND conversation_id=? AND status='pending' AND (provider_message_id='' OR provider_message_id=?)`, providerMessageID, actionID, instanceID, conversationID, providerMessageID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("pending action provider message binding conflict")
	}
	return nil
}

func (s *Store) ChannelActionProviderMessage(ctx context.Context, actionID, instanceID string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT provider_message_id FROM channel_pending_actions WHERE id=? AND instance_id=?`, actionID, instanceID).Scan(&value)
	return value, err
}
