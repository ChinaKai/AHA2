package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func scanChannelDestination(scanner interface{ Scan(...any) error }) (domain.ChannelDestination, error) {
	var item domain.ChannelDestination
	var routeID, targetTaskID, targetTaskCode, targetTaskName sql.NullString
	var updated string
	err := scanner.Scan(
		&item.ConversationID, &item.InstanceID, &item.InstanceName, &item.ProviderKey,
		&item.EndpointKind, &item.DisplayName, &item.Status,
		&routeID, &item.RouteRevision, &targetTaskID, &targetTaskCode, &targetTaskName, &updated,
	)
	if routeID.Valid {
		item.RouteID = routeID.String
	}
	if targetTaskID.Valid {
		item.TargetTaskID = targetTaskID.String
	}
	if targetTaskCode.Valid {
		item.TargetTaskCode = targetTaskCode.String
	}
	if targetTaskName.Valid {
		item.TargetTaskName = targetTaskName.String
	}
	if strings.TrimSpace(item.DisplayName) == "" {
		if item.EndpointKind == domain.ChannelEndpointAssistantDM {
			item.DisplayName = "Owner 私聊"
		} else {
			item.DisplayName = "未命名群聊"
		}
	}
	item.UpdatedAt = parseTime(updated)
	return item, err
}

const channelDestinationQuery = `
	SELECT c.id,i.id,i.name,p.provider_key,e.kind,c.display_name,c.status,
	       r.id,COALESCE(r.revision,0),r.target_task_id,t.code,t.title,c.updated_at
	FROM channel_conversations c
	JOIN channel_instances i ON i.id=c.instance_id
	JOIN channel_plugins p ON p.id=i.plugin_id
	JOIN channel_endpoints e ON e.id=c.endpoint_id
	LEFT JOIN channel_task_routes r ON r.conversation_id=c.id AND r.state='active'
	LEFT JOIN tasks t ON t.id=r.target_task_id
	WHERE i.owner_id=? AND i.retired_at='' AND i.status IN ('ready','degraded') AND c.status='active' AND e.enabled=1
	  AND (
	    e.kind<>'group_digital_human'
	    OR c.id=(
	      SELECT candidate.id
	      FROM channel_conversations candidate
	      WHERE candidate.endpoint_id=c.endpoint_id
	        AND candidate.external_chat_id=c.external_chat_id
	        AND candidate.status='active'
	      ORDER BY CASE WHEN EXISTS(
	                 SELECT 1 FROM channel_task_routes candidate_route
	                 WHERE candidate_route.conversation_id=candidate.id AND candidate_route.state='active'
	               ) THEN 0 ELSE 1 END,
	               candidate.scope_key_version DESC,candidate.updated_at DESC,candidate.id DESC
	      LIMIT 1
	    )
	  )`

func (s *Store) ChannelDestinations(ctx context.Context, ownerID string) ([]domain.ChannelDestination, error) {
	rows, err := s.db.QueryContext(ctx, channelDestinationQuery+` ORDER BY i.name,e.kind,c.display_name,c.updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChannelDestination{}
	for rows.Next() {
		item, err := scanChannelDestination(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ChannelDestination(ctx context.Context, ownerID, conversationID string) (domain.ChannelDestination, error) {
	return scanChannelDestination(s.db.QueryRowContext(ctx, channelDestinationQuery+` AND c.id=?`, ownerID, conversationID))
}

func (s *Store) ChannelContactsForConversation(ctx context.Context, instanceID, conversationID string, limit int) ([]domain.ChannelContact, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH observed(identity_link_id,seen_at) AS (
			SELECT json_extract(ci.payload_json,'$.channel_context.actor.identity_link_id'),ci.created_at
			FROM conversation_items ci
			WHERE json_extract(ci.payload_json,'$.channel_context.conversation_id')=?
			UNION ALL
			SELECT json_extract(mention.value,'$.identity_link_id'),ci.created_at
			FROM conversation_items ci, json_each(ci.payload_json,'$.channel_context.mentions') mention
			WHERE json_extract(ci.payload_json,'$.channel_context.conversation_id')=?
		)
		SELECT identity.id,identity.display_name,identity.role,MAX(observed.seen_at)
		FROM observed
		JOIN channel_identity_links identity ON identity.id=observed.identity_link_id
		WHERE observed.identity_link_id<>'' AND identity.instance_id=? AND identity.status='active' AND identity.role='participant'
		GROUP BY identity.id,identity.display_name,identity.role
		ORDER BY MAX(observed.seen_at) DESC,identity.display_name,identity.id
		LIMIT ?`, conversationID, conversationID, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChannelContact{}
	for rows.Next() {
		var item domain.ChannelContact
		var seenAt string
		if err := rows.Scan(&item.IdentityLinkID, &item.DisplayName, &item.Role, &seenAt); err != nil {
			return nil, err
		}
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		if item.DisplayName == "" {
			item.DisplayName = "群成员"
		}
		item.LastSeenAt = parseTime(seenAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanChannelTaskRoute(scanner interface{ Scan(...any) error }) (domain.ChannelTaskRoute, error) {
	var item domain.ChannelTaskRoute
	var pendingAction sql.NullString
	var activated, exited string
	err := scanner.Scan(
		&item.ID, &item.InstanceID, &item.ConversationID, &item.TargetTaskID, &item.State,
		&item.Revision, &pendingAction, &activated, &exited, &item.ExitReason,
	)
	if pendingAction.Valid {
		item.PendingActionID = pendingAction.String
	}
	item.ActivatedAt, item.ExitedAt = parseTime(activated), parseTime(exited)
	return item, err
}

func (s *Store) ActiveChannelTaskRouteForTask(ctx context.Context, taskID string) (domain.ChannelTaskRoute, error) {
	return scanChannelTaskRoute(s.db.QueryRowContext(ctx, `
		SELECT id,instance_id,conversation_id,target_task_id,state,revision,pending_action_id,activated_at,exited_at,exit_reason
		FROM channel_task_routes WHERE target_task_id=? AND state='active'`, taskID))
}

// TransferOwnerChannelTaskRoute moves the source Task's active primary channel
// to a new Task and copies its participant configuration in one transaction.
// A conversation has only one active Task, so this is deliberately a handoff,
// not a second subscription.
func (s *Store) TransferOwnerChannelTaskRoute(ctx context.Context, ownerID, sourceTaskID, targetTaskID string, at time.Time) (domain.ChannelTaskRoute, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelTaskRoute{}, false, err
	}
	defer tx.Rollback()
	route, err := scanChannelTaskRoute(tx.QueryRowContext(ctx, `
		SELECT route.id,route.instance_id,route.conversation_id,route.target_task_id,route.state,
		       route.revision,route.pending_action_id,route.activated_at,route.exited_at,route.exit_reason
		FROM channel_task_routes route
		JOIN channel_instances instance ON instance.id=route.instance_id
		WHERE route.target_task_id=? AND route.state='active' AND instance.owner_id=?`,
		sourceTaskID, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelTaskRoute{}, false, nil
	}
	if err != nil {
		return domain.ChannelTaskRoute{}, false, err
	}
	if _, err := activateChannelTaskRouteTx(ctx, tx, route.InstanceID, route.ConversationID, targetTaskID, "", at); err != nil {
		return domain.ChannelTaskRoute{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO channel_task_contacts(
			task_id,conversation_id,identity_link_id,collaboration_role,created_at,updated_at
		)
		SELECT ?,conversation_id,identity_link_id,collaboration_role,?,?
		FROM channel_task_contacts
		WHERE task_id=? AND conversation_id=?`,
		targetTaskID, timeString(at), timeString(at), sourceTaskID, route.ConversationID); err != nil {
		return domain.ChannelTaskRoute{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelTaskRoute{}, false, err
	}
	transferred, err := s.ActiveChannelTaskRouteForTask(ctx, targetTaskID)
	return transferred, true, err
}

func (s *Store) ActivateOwnerChannelTaskRoute(ctx context.Context, instanceID, conversationID, taskID string, at time.Time) (domain.ChannelTaskRoute, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	defer tx.Rollback()
	if _, err := activateChannelTaskRouteTx(ctx, tx, instanceID, conversationID, taskID, "", at); err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	return s.ActiveChannelTaskRouteForTask(ctx, taskID)
}

func (s *Store) ExitOwnerChannelTaskRoute(ctx context.Context, routeID, taskID string, at time.Time) error {
	return s.exitChannelTaskRoute(ctx, routeID, taskID, "owner disconnected task channel", at)
}

func (s *Store) ExitTerminalChannelTaskRoute(ctx context.Context, routeID, taskID string, at time.Time) error {
	return s.exitChannelTaskRoute(ctx, routeID, taskID, "task reached terminal state", at)
}

func (s *Store) exitChannelTaskRoute(ctx context.Context, routeID, taskID, reason string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	route, err := scanChannelTaskRoute(tx.QueryRowContext(ctx, `
		SELECT id,instance_id,conversation_id,target_task_id,state,revision,pending_action_id,activated_at,exited_at,exit_reason
		FROM channel_task_routes WHERE id=? AND target_task_id=? AND state='active'`, routeID, taskID))
	if err != nil {
		return err
	}
	if err := exitChannelTaskRouteTx(ctx, tx, route, reason, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) TerminalChannelTaskRoutes(ctx context.Context) ([]domain.ChannelTaskRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT route.id,route.instance_id,route.conversation_id,route.target_task_id,route.state,
		       route.revision,route.pending_action_id,route.activated_at,route.exited_at,route.exit_reason
		FROM channel_task_routes route
		JOIN tasks task ON task.id=route.target_task_id
		WHERE route.state='active' AND task.status IN (?,?)
		ORDER BY route.id`, domain.TaskCompleted, domain.TaskCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := []domain.ChannelTaskRoute{}
	for rows.Next() {
		route, err := scanChannelTaskRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

// LatestReusableChannelTask returns the most recently bound, non-terminal
// channel-owned Task for a conversation. Channel-owned means the Task lives in
// the instance's host Project/Workspace; ordinary Tasks the Owner bound to the
// conversation live in their own Project and are deliberately excluded. The
// caller passes the Task it just unbound so an unbind never immediately
// re-selects the same Task. completed/cancelled/blocked Tasks are not reusable
// because SubmitMessage rejects them.
func (s *Store) LatestReusableChannelTask(ctx context.Context, conversationID, excludeTaskID string) (string, error) {
	var taskID string
	err := s.db.QueryRowContext(ctx, `
		WITH candidates(task_id, sort_at) AS (
			SELECT conversation.host_task_id, host_task.created_at
			FROM channel_conversations conversation
			JOIN tasks host_task ON host_task.id=conversation.host_task_id
			WHERE conversation.id=?
			UNION ALL
			SELECT route.target_task_id, route.activated_at
			FROM channel_task_routes route
			WHERE route.conversation_id=?
		)
		SELECT task.id
		FROM candidates
		JOIN tasks task ON task.id=candidates.task_id
		JOIN channel_conversations conversation ON conversation.id=?
		JOIN channel_instances instance ON instance.id=conversation.instance_id
		WHERE instance.retired_at=''
		  AND task.project_id=instance.host_project_id
		  AND task.workspace_id=instance.host_workspace_id
		  AND task.status IN (?,?,?,?)
		  AND task.id<>?
		GROUP BY task.id
		ORDER BY MAX(candidates.sort_at) DESC, task.updated_at DESC, task.id DESC
		LIMIT 1`,
		conversationID, conversationID, conversationID,
		domain.TaskPreparing, domain.TaskActive, domain.TaskWaitingUser, domain.TaskFailed,
		excludeTaskID,
	).Scan(&taskID)
	return taskID, err
}

// AdoptChannelConversationHost promotes an existing channel-owned Task to the
// conversation's default host Task. The next inbound message then routes to it
// in assistant/group_qa mode and keeps using the conversation_host mirror,
// instead of creating another continuation or requiring an explicit task_route.
func (s *Store) AdoptChannelConversationHost(ctx context.Context, conversationID, taskID string, at time.Time) error {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(taskID) == "" {
		return errors.New("conversation and task are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var instanceID, currentHost, endpointKind string
	if err := tx.QueryRowContext(ctx, `
		SELECT conversation.instance_id,conversation.host_task_id,endpoint.kind
		FROM channel_conversations conversation
		JOIN channel_endpoints endpoint ON endpoint.id=conversation.endpoint_id
		WHERE conversation.id=?`, conversationID,
	).Scan(&instanceID, &currentHost, &endpointKind); err != nil {
		return err
	}
	if currentHost == taskID {
		return tx.Commit()
	}
	var reusable bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM tasks task
			JOIN channel_instances instance ON instance.id=?
			WHERE task.id=? AND task.project_id=instance.host_project_id
			  AND task.workspace_id=instance.host_workspace_id
			  AND task.status IN (?,?,?,?)
		)`, instanceID, taskID,
		domain.TaskPreparing, domain.TaskActive, domain.TaskWaitingUser, domain.TaskFailed,
	).Scan(&reusable); err != nil {
		return err
	}
	if !reusable {
		return errors.New("channel fallback task is not reusable")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_conversations SET host_task_id=?,updated_at=? WHERE id=?`,
		taskID, timeString(at), conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET state='closed',updated_at=? WHERE conversation_id=? AND kind='conversation_host' AND state='active'`,
		timeString(at), conversationID); err != nil {
		return err
	}
	var cursor int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM channel_source_events`).Scan(&cursor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_subscriptions(id,instance_id,conversation_id,route_id,source_task_id,kind,filter_version,filter_json,source_cursor,state,created_at,updated_at) VALUES(?,?,?,NULL,?,'conversation_host',1,'{}',?,'active',?,?)`,
		domain.NewID("channel_subscription"), instanceID, conversationID, taskID, cursor, timeString(at), timeString(at)); err != nil {
		return err
	}
	mode := "assistant"
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		mode = "group_qa"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_sessions SET mode=? WHERE conversation_id=? AND status='active'`,
		mode, conversationID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ChannelConversationSummaries(ctx context.Context, ownerID, instanceID string) ([]domain.ChannelConversationSummary, error) {
	destinations, err := s.ChannelDestinations(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	result := []domain.ChannelConversationSummary{}
	for _, destination := range destinations {
		if destination.InstanceID != instanceID {
			continue
		}
		summary := domain.ChannelConversationSummary{
			ID: destination.ConversationID, EndpointKind: destination.EndpointKind,
			DisplayName: destination.DisplayName, Status: destination.Status,
			BoundTaskID: destination.TargetTaskID, BoundTaskCode: destination.TargetTaskCode,
			BoundTaskTitle: destination.TargetTaskName,
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_inbox_dedup WHERE conversation_id=?`, destination.ConversationID).Scan(&summary.InboundCount); err != nil {
			return nil, err
		}
		var receivedAt string
		if err := s.db.QueryRowContext(ctx, `SELECT state,outcome,received_at FROM channel_inbox_dedup WHERE conversation_id=? ORDER BY received_at DESC,id DESC LIMIT 1`, destination.ConversationID).Scan(&summary.LastInboundState, &summary.LastInboundOutcome, &receivedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		} else if err == nil {
			summary.LastInboundAt = receivedAt
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_delivery_outbox WHERE conversation_id=?`, destination.ConversationID).Scan(&summary.OutboundCount); err != nil {
			return nil, err
		}
		var updatedAt string
		if err := s.db.QueryRowContext(ctx, `SELECT state,last_error_code,updated_at FROM channel_delivery_outbox WHERE conversation_id=? ORDER BY updated_at DESC,id DESC LIMIT 1`, destination.ConversationID).Scan(&summary.LastOutboundState, &summary.LastOutboundError, &updatedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		} else if err == nil {
			summary.LastOutboundAt = updatedAt
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *Store) ChannelRouteOwnedBy(ctx context.Context, routeID, ownerID string) (domain.ChannelTaskRoute, error) {
	route, err := scanChannelTaskRoute(s.db.QueryRowContext(ctx, `
		SELECT r.id,r.instance_id,r.conversation_id,r.target_task_id,r.state,r.revision,r.pending_action_id,r.activated_at,r.exited_at,r.exit_reason
		FROM channel_task_routes r
		JOIN channel_instances i ON i.id=r.instance_id
		WHERE r.id=? AND i.owner_id=?`, routeID, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelTaskRoute{}, sql.ErrNoRows
	}
	return route, err
}
