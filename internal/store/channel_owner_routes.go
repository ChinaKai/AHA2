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
	if err := exitChannelTaskRouteTx(ctx, tx, route, "owner disconnected task channel", at); err != nil {
		return err
	}
	return tx.Commit()
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
