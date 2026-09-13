package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type ChannelMemberInput struct {
	IdentityLinkID string
	IsBot          bool
}

type TaskChannelContactInput struct {
	IdentityLinkID    string
	CollaborationRole string
}

func (s *Store) UpsertChannelConversationMember(ctx context.Context, conversationID, identityLinkID string, isBot bool, observedAt time.Time) error {
	if conversationID == "" || identityLinkID == "" || observedAt.IsZero() {
		return errors.New("channel conversation member is incomplete")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_conversation_members(
			conversation_id,identity_link_id,is_bot,observed_at,provider_seen_at,provider_active,source,updated_at
		) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(conversation_id,identity_link_id) DO UPDATE SET
			is_bot=CASE
				WHEN channel_conversation_members.is_bot=1 OR excluded.is_bot=1 THEN 1 ELSE 0 END,
			observed_at=CASE
				WHEN channel_conversation_members.observed_at='' OR channel_conversation_members.observed_at<excluded.observed_at
				THEN excluded.observed_at ELSE channel_conversation_members.observed_at END,
			source='observed',
			updated_at=excluded.updated_at`,
		conversationID, identityLinkID, isBot, timeString(observedAt), "", false, "observed", timeString(observedAt))
	return err
}

// EnsureCurrentChannelBot materializes only the bot backing this channel
// instance. Other bots must be observed in this conversation by the provider.
func (s *Store) EnsureCurrentChannelBot(ctx context.Context, conversationID string, at time.Time) error {
	var targetInstanceID, externalUserID, displayName, fallbackDisplayName string
	if err := s.db.QueryRowContext(ctx, `
		SELECT conversation.instance_id,
		       COALESCE(json_extract(instance.config_json,'$.runtime_bot_open_id'),''),
		       COALESCE(
		           NULLIF(TRIM(json_extract(instance.config_json,'$.runtime_bot_display_name_override')),''),
		           NULLIF(TRIM(json_extract(instance.config_json,'$.runtime_bot_provider_display_name')),''),
		           NULLIF(TRIM(json_extract(instance.config_json,'$.runtime_bot_display_name')),''),
		           ''
		       ),
		       instance.name
		FROM channel_conversations conversation
		JOIN channel_instances instance ON instance.id=conversation.instance_id
		WHERE conversation.id=? AND conversation.status='active'`,
		conversationID).Scan(&targetInstanceID, &externalUserID, &displayName, &fallbackDisplayName); err != nil {
		return err
	}
	externalUserID = strings.TrimSpace(externalUserID)
	if externalUserID == "" {
		return nil
	}
	displayName = strings.TrimSpace(displayName)
	fallbackDisplayName = strings.TrimSpace(fallbackDisplayName)
	if fallbackDisplayName == "" {
		fallbackDisplayName = "当前渠道机器人"
	}
	identity, err := s.UpsertObservedChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: domain.NewID("channel_identity"), InstanceID: targetInstanceID,
		ExternalUserID: externalUserID, Role: "participant", DisplayName: displayName,
		Status: "active", LinkedAt: at,
	}, fallbackDisplayName)
	if err != nil {
		return err
	}
	return s.UpsertChannelConversationMember(ctx, conversationID, identity.ID, true, at)
}

func (s *Store) ReplaceProviderChannelMembers(ctx context.Context, conversationID string, members []ChannelMemberInput, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE channel_conversation_members
		SET provider_active=0,
		    source='observed',
		    updated_at=?
		WHERE conversation_id=?`, timeString(at), conversationID); err != nil {
		return err
	}
	for _, member := range members {
		if strings.TrimSpace(member.IdentityLinkID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO channel_conversation_members(
				conversation_id,identity_link_id,is_bot,observed_at,provider_seen_at,provider_active,source,updated_at
			) VALUES(?,?,?,'',?,?,?,?)
			ON CONFLICT(conversation_id,identity_link_id) DO UPDATE SET
				is_bot=excluded.is_bot,provider_seen_at=excluded.provider_seen_at,
				provider_active=1,source='provider',updated_at=excluded.updated_at`,
			conversationID, member.IdentityLinkID, member.IsBot, timeString(at), true, "provider", timeString(at)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE channel_conversations SET members_synced_at=?,updated_at=? WHERE id=?`,
		timeString(at), timeString(at), conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM channel_task_contacts
		WHERE conversation_id=?
		  AND identity_link_id IN (
			SELECT identity_link_id
			FROM channel_conversation_members
			WHERE conversation_id=? AND source='observed' AND is_bot=0
		  )`, conversationID, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM channel_conversation_members
		WHERE conversation_id=? AND source='observed' AND observed_at='' AND provider_active=0`, conversationID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ChannelMembersForConversation(ctx context.Context, instanceID, conversationID string, limit int) ([]domain.ChannelGroupMember, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT identity.id,
		       CASE
		           WHEN identity.external_user_id=json_extract(current_instance.config_json,'$.runtime_bot_open_id')
		           THEN COALESCE(
		               NULLIF(TRIM(json_extract(current_instance.config_json,'$.runtime_bot_display_name_override')),''),
		               NULLIF(TRIM(json_extract(current_instance.config_json,'$.runtime_bot_provider_display_name')),''),
		               NULLIF(TRIM(json_extract(current_instance.config_json,'$.runtime_bot_display_name')),''),
		               NULLIF(TRIM(identity.display_name),''),
		               current_instance.name
		           )
		           ELSE identity.display_name
		       END,
		       member.is_bot,
		       member.source,
		       CASE WHEN member.provider_active=1 THEN member.provider_seen_at ELSE member.observed_at END,
		       CASE WHEN identity.external_user_id=json_extract(current_instance.config_json,'$.runtime_bot_open_id') THEN 1 ELSE 0 END
		FROM channel_conversation_members member
		JOIN channel_identity_links identity ON identity.id=member.identity_link_id
		JOIN channel_conversations conversation ON conversation.id=member.conversation_id
		JOIN channel_instances current_instance ON current_instance.id=conversation.instance_id
		WHERE member.conversation_id=? AND identity.instance_id=? AND identity.status='active'
		  AND (
			member.provider_active=1
			OR (member.is_bot=1 AND member.observed_at<>'')
			OR (conversation.members_synced_at='' AND member.observed_at<>'')
		  )
		ORDER BY member.is_bot,identity.display_name,identity.id
		LIMIT ?`, conversationID, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChannelGroupMember{}
	for rows.Next() {
		var item domain.ChannelGroupMember
		var seenAt string
		if err := rows.Scan(&item.IdentityLinkID, &item.DisplayName, &item.IsBot, &item.Source, &seenAt, &item.IsSelf); err != nil {
			return nil, err
		}
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		if item.DisplayName == "" {
			if item.IsBot {
				item.DisplayName = "群机器人"
			} else {
				item.DisplayName = "群成员"
			}
		}
		item.LastSeenAt = parseTime(seenAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) TaskChannelContacts(ctx context.Context, taskID, instanceID, conversationID string, limit int) ([]domain.ChannelContact, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT identity.id,identity.display_name,identity.role,contact.collaboration_role,
		       member.is_bot,
		       CASE WHEN member.observed_at<>'' THEN member.observed_at ELSE member.provider_seen_at END
		FROM channel_task_contacts contact
		JOIN channel_identity_links identity ON identity.id=contact.identity_link_id
		JOIN channel_conversation_members member
		  ON member.conversation_id=contact.conversation_id
		 AND member.identity_link_id=contact.identity_link_id
		JOIN channel_conversations conversation ON conversation.id=contact.conversation_id
		JOIN channel_instances current_instance ON current_instance.id=conversation.instance_id
		WHERE contact.task_id=? AND contact.conversation_id=?
		  AND identity.instance_id=? AND identity.status='active'
		  AND identity.external_user_id<>COALESCE(json_extract(current_instance.config_json,'$.runtime_bot_open_id'),'')
		  AND (
			member.provider_active=1
			OR (member.is_bot=1 AND member.observed_at<>'')
			OR (conversation.members_synced_at='' AND member.observed_at<>'')
		  )
		ORDER BY identity.display_name,identity.id
		LIMIT ?`, taskID, conversationID, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChannelContact{}
	for rows.Next() {
		var item domain.ChannelContact
		var seenAt string
		if err := rows.Scan(
			&item.IdentityLinkID, &item.DisplayName, &item.Role, &item.CollaborationRole,
			&item.IsBot, &seenAt,
		); err != nil {
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

func (s *Store) ReplaceTaskChannelContacts(ctx context.Context, taskID, instanceID, conversationID string, contacts []TaskChannelContactInput, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var routeCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM channel_task_routes
		WHERE target_task_id=? AND instance_id=? AND conversation_id=? AND state='active'`,
		taskID, instanceID, conversationID).Scan(&routeCount); err != nil || routeCount != 1 {
		return errors.New("active task channel route changed")
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM channel_task_contacts WHERE task_id=? AND conversation_id=?`,
		taskID, conversationID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, contact := range contacts {
		identityID := strings.TrimSpace(contact.IdentityLinkID)
		if identityID == "" || seen[identityID] {
			return errors.New("task channel contact is invalid")
		}
		seen[identityID] = true
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM channel_conversation_members member
				JOIN channel_identity_links identity ON identity.id=member.identity_link_id
				JOIN channel_conversations conversation ON conversation.id=member.conversation_id
				JOIN channel_instances current_instance ON current_instance.id=conversation.instance_id
				WHERE member.conversation_id=? AND member.identity_link_id=?
				  AND identity.instance_id=? AND identity.status='active'
				  AND identity.external_user_id<>COALESCE(json_extract(current_instance.config_json,'$.runtime_bot_open_id'),'')
				  AND (
					member.provider_active=1
					OR (member.is_bot=1 AND member.observed_at<>'')
					OR (conversation.members_synced_at='' AND member.observed_at<>'')
				  )
			)`, conversationID, identityID, instanceID).Scan(&exists); err != nil || !exists {
			if err != nil {
				return err
			}
			return errors.New("task channel contact is not a current group member")
		}
		role := strings.TrimSpace(contact.CollaborationRole)
		if len([]rune(role)) > 40 {
			return errors.New("collaboration role exceeds 40 characters")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO channel_task_contacts(
				task_id,conversation_id,identity_link_id,collaboration_role,created_at,updated_at
			) VALUES(?,?,?,?,?,?)`,
			taskID, conversationID, identityID, role, timeString(at), timeString(at)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
