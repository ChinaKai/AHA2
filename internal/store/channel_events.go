package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) AppendChannelSourceAndProject(ctx context.Context, source domain.ChannelSourceEvent, coalesceKey string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if source.SourceRevision < 1 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(source_revision),0)+1 FROM channel_source_events WHERE source_key=?`, source.SourceKey).Scan(&source.SourceRevision); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO channel_source_events(id,source_key,source_revision,task_id,round_id,turn_id,conversation_item_id,event_class,event_type,semantic_payload_json,occurred_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		source.ID, source.SourceKey, source.SourceRevision, source.TaskID, source.RoundID, source.TurnID, source.ConversationItemID,
		source.EventClass, source.EventType, encodeJSON(source.SemanticPayload), timeString(source.OccurredAt))
	if err != nil {
		return err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return tx.Commit()
	}
	if err := tx.QueryRowContext(ctx, `SELECT sequence FROM channel_source_events WHERE id=?`, source.ID).Scan(&source.Sequence); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT sub.id,sub.instance_id,sub.conversation_id,sub.kind
		FROM channel_subscriptions sub
		JOIN channel_instances instance ON instance.id=sub.instance_id
		WHERE sub.state='active' AND instance.status IN ('ready','degraded')
		  AND ((sub.kind IN ('task_route','conversation_host') AND sub.source_task_id=?)
		       OR (sub.kind='owner_global' AND ? AND COALESCE(json_extract(instance.config_json,'$.notify_task_status'),0)=1))`, source.TaskID, source.EventClass == "status")
	if err != nil {
		return err
	}
	type target struct{ subscriptionID, instanceID, conversationID, kind string }
	targets := []target{}
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.subscriptionID, &item.instanceID, &item.conversationID, &item.kind); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, item)
	}
	rows.Close()
	for _, target := range targets {
		if target.kind == "owner_global" {
			var managed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_conversations WHERE host_task_id=?)`, source.TaskID).Scan(&managed); err != nil {
				return err
			}
			if managed {
				continue
			}
			var routed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_task_routes WHERE instance_id=? AND target_task_id=? AND state='active')`, target.instanceID, source.TaskID).Scan(&routed); err != nil {
				return err
			}
			if routed {
				continue
			}
		}
		if coalesceKey != "" {
			var existingID string
			err := tx.QueryRowContext(ctx, `SELECT id FROM channel_delivery_outbox WHERE conversation_id=? AND coalesce_key=? AND state='pending' ORDER BY stream_sequence DESC LIMIT 1`, target.conversationID, coalesceKey).Scan(&existingID)
			if err == nil {
				if _, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET source_event_sequence=?,semantic_payload_json=json_patch(semantic_payload_json,?),updated_at=? WHERE id=?`, source.Sequence, encodeJSON(source.SemanticPayload), timeString(source.OccurredAt), existingID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET source_cursor=?,updated_at=? WHERE id=?`, source.Sequence, timeString(source.OccurredAt), target.subscriptionID); err != nil {
					return err
				}
				continue
			}
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		var streamSequence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(stream_sequence),0)+1 FROM channel_delivery_outbox WHERE conversation_id=?`, target.conversationID).Scan(&streamSequence); err != nil {
			return err
		}
		deliveryID := domain.NewID("channel_delivery")
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_delivery_outbox(id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			deliveryID, target.instanceID, target.conversationID, target.subscriptionID, source.Sequence, streamSequence, 0, nil,
			"channel-delivery:"+source.ID+":"+target.subscriptionID, coalesceKey, 1, encodeJSON(source.SemanticPayload), "pending", 0, "", timeString(source.OccurredAt), "", "", "", "", "", timeString(source.OccurredAt), timeString(source.OccurredAt), ""); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET source_cursor=?,updated_at=? WHERE id=?`, source.Sequence, timeString(source.OccurredAt), target.subscriptionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) EnsureOwnerGlobalSubscription(ctx context.Context, instanceID, conversationID string, at time.Time) error {
	var cursor int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM channel_source_events`).Scan(&cursor)
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_subscriptions(id,instance_id,conversation_id,route_id,source_task_id,kind,filter_version,filter_json,source_cursor,state,created_at,updated_at) VALUES(?,?,?,NULL,NULL,'owner_global',1,'{}',?,'active',?,?)`,
		domain.NewID("channel_subscription"), instanceID, conversationID, cursor, timeString(at), timeString(at))
	return err
}

func (s *Store) EnsureConversationHostSubscription(ctx context.Context, instanceID, conversationID, taskID string, at time.Time) error {
	var cursor int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM channel_source_events`).Scan(&cursor)
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_subscriptions(id,instance_id,conversation_id,route_id,source_task_id,kind,filter_version,filter_json,source_cursor,state,created_at,updated_at) VALUES(?,?,?,NULL,?,'conversation_host',1,'{}',?,'active',?,?)`,
		domain.NewID("channel_subscription"), instanceID, conversationID, taskID, cursor, timeString(at), timeString(at))
	return err
}

func ChannelCoalesceKey(taskID, roundID, eventType string) string {
	if roundID == "" {
		return ""
	}
	switch eventType {
	case "agent_reply", "waiting_user", "terminal":
		return fmt.Sprintf("task/%s/round/%s/final", taskID, roundID)
	case "agent_message_update":
		return fmt.Sprintf("task/%s/round/%s/stream", taskID, roundID)
	default:
		return ""
	}
}
