package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) ChannelAllowedKnowledge(ctx context.Context, instanceID, endpointKind, conversationID string) ([]domain.KnowledgeEntry, error) {
	var policyID, fixedIndexID string
	err := s.db.QueryRowContext(ctx, `SELECT p.id,p.fixed_index_entry_id FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? AND e.kind=? AND e.enabled=1`, instanceID, endpointKind).Scan(&policyID, &fixedIndexID)
	if err != nil {
		return nil, err
	}
	all, err := s.ListKnowledge(ctx, "", "", nil)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.KnowledgeEntry{}
	children := map[string][]string{}
	for _, entry := range all {
		byID[entry.ID] = entry
		children[entry.ParentID] = append(children[entry.ParentID], entry.ID)
	}
	allowed := map[string]bool{fixedIndexID: true}
	rows, err := s.db.QueryContext(ctx, `SELECT knowledge_entry_id FROM channel_knowledge_records WHERE instance_id=? AND (conversation_id=? OR (visibility='instance_shared' AND authority_status='verified'))`, instanceID, conversationID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		allowed[id] = true
	}
	rows.Close()
	grants, err := s.db.QueryContext(ctx, `SELECT knowledge_entry_id,grant_scope FROM channel_knowledge_grants WHERE policy_id=? AND revoked_at=''`, policyID)
	if err != nil {
		return nil, err
	}
	for grants.Next() {
		var id, scope string
		if err := grants.Scan(&id, &scope); err != nil {
			grants.Close()
			return nil, err
		}
		allowed[id] = true
		if scope == "subtree" {
			queue := append([]string{}, children[id]...)
			for len(queue) > 0 {
				child := queue[0]
				queue = queue[1:]
				if allowed[child] {
					continue
				}
				allowed[child] = true
				queue = append(queue, children[child]...)
			}
		}
	}
	grants.Close()
	result := []domain.KnowledgeEntry{}
	for _, entry := range all {
		if !allowed[entry.ID] {
			continue
		}
		if entry.ID != fixedIndexID && entry.Status != domain.KnowledgeVerified {
			var isConversationRecord bool
			_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_knowledge_records WHERE knowledge_entry_id=? AND conversation_id=?)`, entry.ID, conversationID).Scan(&isConversationRecord)
			if !isConversationRecord {
				continue
			}
		}
		if entry.BranchScope != "" || entry.ProductLineID != "" {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

const channelInboxColumns = `id,instance_id,external_event_id,event_type,payload_digest,normalized_payload_json,state,lease_id,lease_until,conversation_id,aha_message_id,outcome,occurred_at,received_at,processed_at`

func scanChannelInbox(scanner interface{ Scan(...any) error }) (domain.ChannelInboxReceipt, error) {
	var item domain.ChannelInboxReceipt
	var payload, leaseUntil, occurred, received, processed string
	var conversationID sql.NullString
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.ExternalEventID, &item.EventType, &item.PayloadDigest, &payload,
		&item.State, &item.LeaseID, &leaseUntil, &conversationID, &item.AHAMessageID, &item.Outcome, &occurred, &received, &processed)
	item.NormalizedPayload = decodeJSON(payload, map[string]any{})
	if conversationID.Valid {
		item.ConversationID = conversationID.String
	}
	item.LeaseUntil, item.OccurredAt, item.ReceivedAt, item.ProcessedAt = parseTime(leaseUntil), parseTime(occurred), parseTime(received), parseTime(processed)
	return item, err
}

func (s *Store) ReceiveChannelInbox(ctx context.Context, item domain.ChannelInboxReceipt) (domain.ChannelInboxReceipt, bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO channel_inbox_dedup(id,instance_id,external_event_id,event_type,payload_digest,normalized_payload_json,state,lease_id,lease_until,conversation_id,aha_message_id,outcome,occurred_at,received_at,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,external_event_id) DO NOTHING`,
		item.ID, item.InstanceID, item.ExternalEventID, item.EventType, item.PayloadDigest, encodeJSON(item.NormalizedPayload), item.State,
		item.LeaseID, timeString(item.LeaseUntil), nullableString(item.ConversationID), item.AHAMessageID, item.Outcome,
		timeString(item.OccurredAt), timeString(item.ReceivedAt), timeString(item.ProcessedAt))
	if err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	inserted, _ := result.RowsAffected()
	existing, err := scanChannelInbox(s.db.QueryRowContext(ctx, `SELECT `+channelInboxColumns+` FROM channel_inbox_dedup WHERE instance_id=? AND external_event_id=?`, item.InstanceID, item.ExternalEventID))
	if err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	if existing.PayloadDigest != item.PayloadDigest {
		return domain.ChannelInboxReceipt{}, false, ErrChannelInboxDigest
	}
	return existing, inserted == 0, nil
}

func (s *Store) ClaimChannelInbox(ctx context.Context, limit int, now time.Time, leaseDuration time.Duration) ([]domain.ChannelInboxReceipt, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_inbox_dedup SET state='received',lease_id='',lease_until='' WHERE state='processing' AND lease_until<>'' AND lease_until<=?`, timeString(now)); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM channel_inbox_dedup WHERE state='received' ORDER BY received_at,id LIMIT ?`, limit)
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
	items := []domain.ChannelInboxReceipt{}
	for _, id := range ids {
		leaseID := domain.NewID("channel_inbox_lease")
		result, err := tx.ExecContext(ctx, `UPDATE channel_inbox_dedup SET state='processing',lease_id=?,lease_until=? WHERE id=? AND state='received'`, leaseID, timeString(now.Add(leaseDuration)), id)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		item, err := scanChannelInbox(tx.QueryRowContext(ctx, `SELECT `+channelInboxColumns+` FROM channel_inbox_dedup WHERE id=?`, id))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) FinishChannelInbox(ctx context.Context, id, leaseID, state, conversationID, messageID, outcome string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_inbox_dedup SET state=?,conversation_id=?,aha_message_id=?,outcome=?,lease_id='',lease_until='',processed_at=? WHERE id=? AND state='processing' AND lease_id=?`,
		state, nullableString(conversationID), messageID, outcome, timeString(at), id, leaseID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		var existingState, existingOutcome string
		lookupErr := s.db.QueryRowContext(ctx, `SELECT state,outcome FROM channel_inbox_dedup WHERE id=?`, id).Scan(&existingState, &existingOutcome)
		if lookupErr == nil && existingState == state && existingOutcome == outcome {
			return nil
		}
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) ChannelOwnerIdentity(ctx context.Context, instanceID string) (domain.ChannelIdentityLink, error) {
	return scanChannelIdentity(s.db.QueryRowContext(ctx, `SELECT id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at FROM channel_identity_links WHERE instance_id=? AND role='owner' AND status='active'`, instanceID))
}

func (s *Store) RevokeChannelOwnerIdentity(ctx context.Context, instanceID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channel_identity_links SET status='revoked',revoked_at=? WHERE instance_id=? AND role='owner' AND status='active'`, timeString(at), instanceID)
	return err
}

func (s *Store) ResetChannelExternalBinding(ctx context.Context, instanceID string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_identity_links SET status='revoked',revoked_at=? WHERE instance_id=? AND status='active'`, timeString(at), instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_task_routes SET state='revoked',exited_at=?,exit_reason='channel application changed' WHERE instance_id=? AND state='active'`, timeString(at), instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET state='revoked',updated_at=? WHERE instance_id=? AND state='active'`, timeString(at), instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_sessions SET status='closed',closed_at=? WHERE status='active' AND conversation_id IN (SELECT id FROM channel_conversations WHERE instance_id=?)`, timeString(at), instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_conversations SET status='closed',updated_at=? WHERE instance_id=? AND status='active'`, timeString(at), instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_handoffs SET owner_conversation_id=NULL,updated_at=? WHERE instance_id=? AND state IN ('pending_owner','accepted_todo')`, timeString(at), instanceID); err != nil {
		return err
	}
	return tx.Commit()
}

func scanChannelIdentity(scanner interface{ Scan(...any) error }) (domain.ChannelIdentityLink, error) {
	var item domain.ChannelIdentityLink
	var ownerID sql.NullString
	var linked, revoked string
	err := scanner.Scan(&item.ID, &item.InstanceID, &ownerID, &item.ProviderTenantID, &item.ExternalUserID, &item.UnionID, &item.Role, &item.DisplayName, &item.Status, &linked, &revoked)
	if ownerID.Valid {
		item.OwnerID = ownerID.String
	}
	item.LinkedAt, item.RevokedAt = parseTime(linked), parseTime(revoked)
	return item, err
}

func (s *Store) BindChannelOwnerIdentity(ctx context.Context, item domain.ChannelIdentityLink) (domain.ChannelIdentityLink, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelIdentityLink{}, err
	}
	defer tx.Rollback()
	existing, err := scanChannelIdentity(tx.QueryRowContext(ctx, `SELECT id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at FROM channel_identity_links WHERE instance_id=? AND role='owner' AND status='active'`, item.InstanceID))
	if err == nil && existing.ExternalUserID != item.ExternalUserID {
		return domain.ChannelIdentityLink{}, errors.New("channel instance already has an owner")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelIdentityLink{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_identity_links(id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,external_user_id) DO UPDATE SET owner_id=excluded.owner_id,provider_tenant_id=excluded.provider_tenant_id,union_id=excluded.union_id,role='owner',display_name=excluded.display_name,status='active',linked_at=excluded.linked_at,revoked_at=''`,
		item.ID, item.InstanceID, item.OwnerID, item.ProviderTenantID, item.ExternalUserID, item.UnionID, "owner", item.DisplayName, "active", timeString(item.LinkedAt), ""); err != nil {
		return domain.ChannelIdentityLink{}, err
	}
	bound, err := scanChannelIdentity(tx.QueryRowContext(ctx, `SELECT id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at FROM channel_identity_links WHERE instance_id=? AND external_user_id=?`, item.InstanceID, item.ExternalUserID))
	if err != nil {
		return domain.ChannelIdentityLink{}, err
	}
	return bound, tx.Commit()
}

func (s *Store) UpsertChannelParticipant(ctx context.Context, item domain.ChannelIdentityLink) (domain.ChannelIdentityLink, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_identity_links(id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at) VALUES(?,?,NULL,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,external_user_id) DO UPDATE SET provider_tenant_id=excluded.provider_tenant_id,union_id=excluded.union_id,display_name=excluded.display_name,status=CASE WHEN channel_identity_links.role='owner' THEN channel_identity_links.status ELSE 'active' END,linked_at=CASE WHEN channel_identity_links.role='owner' THEN channel_identity_links.linked_at ELSE excluded.linked_at END,revoked_at=CASE WHEN channel_identity_links.role='owner' THEN channel_identity_links.revoked_at ELSE '' END`,
		item.ID, item.InstanceID, item.ProviderTenantID, item.ExternalUserID, item.UnionID, "participant", item.DisplayName, "active", timeString(item.LinkedAt), "")
	if err != nil {
		return domain.ChannelIdentityLink{}, err
	}
	return scanChannelIdentity(s.db.QueryRowContext(ctx, `SELECT id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at FROM channel_identity_links WHERE instance_id=? AND external_user_id=?`, item.InstanceID, item.ExternalUserID))
}

func (s *Store) ChannelEndpoint(ctx context.Context, instanceID, kind string) (domain.ChannelEndpoint, error) {
	var item domain.ChannelEndpoint
	var config, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,kind,enabled,config_json,created_at,updated_at FROM channel_endpoints WHERE instance_id=? AND kind=?`, instanceID, kind).
		Scan(&item.ID, &item.InstanceID, &item.Kind, &item.Enabled, &config, &created, &updated)
	item.Config = decodeJSON(config, map[string]any{})
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

func scanChannelConversation(scanner interface{ Scan(...any) error }) (domain.ChannelConversation, error) {
	var item domain.ChannelConversation
	var ownerIdentity sql.NullString
	var created, updated string
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.EndpointID, &item.ScopeKeyVersion, &item.ScopeKey, &item.ExternalChatID, &item.ExternalSenderID, &ownerIdentity, &item.HostTaskID, &item.Status, &created, &updated)
	if ownerIdentity.Valid {
		item.OwnerIdentityLinkID = ownerIdentity.String
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

func (s *Store) ChannelConversationByScope(ctx context.Context, endpointID string, scopeVersion int, scopeKey string) (domain.ChannelConversation, error) {
	return scanChannelConversation(s.db.QueryRowContext(ctx, `SELECT id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,owner_identity_link_id,host_task_id,status,created_at,updated_at FROM channel_conversations WHERE endpoint_id=? AND scope_key_version=? AND scope_key=?`, endpointID, scopeVersion, scopeKey))
}

func (s *Store) ChannelConversation(ctx context.Context, id string) (domain.ChannelConversation, error) {
	return scanChannelConversation(s.db.QueryRowContext(ctx, `SELECT id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,owner_identity_link_id,host_task_id,status,created_at,updated_at FROM channel_conversations WHERE id=?`, id))
}

func (s *Store) OwnerChannelConversation(ctx context.Context, instanceID string) (domain.ChannelConversation, error) {
	return scanChannelConversation(s.db.QueryRowContext(ctx, `SELECT c.id,c.instance_id,c.endpoint_id,c.scope_key_version,c.scope_key,c.external_chat_id,c.external_sender_id,c.owner_identity_link_id,c.host_task_id,c.status,c.created_at,c.updated_at FROM channel_conversations c JOIN channel_endpoints e ON e.id=c.endpoint_id WHERE c.instance_id=? AND e.kind='assistant_dm' AND c.status='active'`, instanceID))
}

func (s *Store) CreateChannelConversation(ctx context.Context, item domain.ChannelConversation, session domain.ChannelSession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_conversations(id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,owner_identity_link_id,host_task_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.InstanceID, item.EndpointID, item.ScopeKeyVersion, item.ScopeKey, item.ExternalChatID, item.ExternalSenderID, nullableString(item.OwnerIdentityLinkID), item.HostTaskID, item.Status, timeString(item.CreatedAt), timeString(item.UpdatedAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_sessions(id,conversation_id,generation,mode,status,inbound_cursor,outbound_cursor,started_at,closed_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		session.ID, item.ID, session.Generation, session.Mode, session.Status, session.InboundCursor, session.OutboundCursor, timeString(session.StartedAt), timeString(session.ClosedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ActiveChannelTaskRoute(ctx context.Context, conversationID string) (domain.ChannelTaskRoute, error) {
	var item domain.ChannelTaskRoute
	var pendingAction sql.NullString
	var activated, exited string
	err := s.db.QueryRowContext(ctx, `SELECT id,instance_id,conversation_id,target_task_id,state,revision,pending_action_id,activated_at,exited_at,exit_reason FROM channel_task_routes WHERE conversation_id=? AND state='active'`, conversationID).
		Scan(&item.ID, &item.InstanceID, &item.ConversationID, &item.TargetTaskID, &item.State, &item.Revision, &pendingAction, &activated, &exited, &item.ExitReason)
	if pendingAction.Valid {
		item.PendingActionID = pendingAction.String
	}
	item.ActivatedAt, item.ExitedAt = parseTime(activated), parseTime(exited)
	return item, err
}

func (s *Store) EnsureChannelKnowledgePolicies(ctx context.Context, instanceID, fixedIndexID string, at time.Time) error {
	endpoints, err := s.ChannelEndpoints(ctx, instanceID)
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		visibility := "conversation_only"
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_knowledge_policies(id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			domain.NewID("channel_knowledge_policy"), instanceID, endpoint.ID, fixedIndexID, visibility, 1, timeString(at), timeString(at))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EnqueueChannelOwnerMessage(
	ctx context.Context,
	receiptID, leaseID, conversationID string,
	message domain.Message,
	targetAgentID string,
	provenance map[string]any,
) (domain.TaskRound, domain.AgentInboxItem, bool, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	defer tx.Rollback()
	var receiptState, existingMessageID string
	if err := tx.QueryRowContext(ctx, `SELECT state,aha_message_id FROM channel_inbox_dedup WHERE id=? AND lease_id=?`, receiptID, leaseID).Scan(&receiptState, &existingMessageID); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	if existingMessageID != "" {
		inbox, err := scanInbox(tx.QueryRowContext(ctx, `SELECT `+inboxColumns+` FROM agent_inbox WHERE message_id=? ORDER BY sequence LIMIT 1`, existingMessageID))
		if err != nil {
			return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
		}
		round, err := scanRound(tx.QueryRowContext(ctx, `SELECT `+roundColumns+` FROM task_rounds WHERE id=?`, inbox.RoundID))
		if err != nil {
			return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
		}
		return round, inbox, false, false, tx.Commit()
	}
	if receiptState != "processing" {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, ErrChannelRevision
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_agents WHERE task_id=? AND agent_id=?)`, message.TaskID, targetAgentID).Scan(&exists); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	if !exists {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, sql.ErrNoRows
	}
	round, err := scanRound(tx.QueryRowContext(ctx, `SELECT `+roundColumns+` FROM task_rounds WHERE task_id=? AND status IN ('running','waiting') ORDER BY sequence DESC LIMIT 1`, message.TaskID))
	createdRound := false
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at) VALUES(?,?,?,?,?,?,?)`,
		message.ID, message.TaskID, "", message.Role, message.Sender, message.Content, timeString(message.CreatedAt)); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	if errors.Is(err, sql.ErrNoRows) || round.ID == "" {
		round = domain.TaskRound{ID: domain.NewID("round"), TaskID: message.TaskID, InputMessageID: message.ID, Status: domain.RoundRunning, CreatedAt: message.CreatedAt, StartedAt: message.CreatedAt}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM task_rounds WHERE task_id=?`, message.TaskID).Scan(&round.Sequence); err != nil {
			return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_rounds(id,task_id,sequence,input_message_id,status,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?)`,
			round.ID, round.TaskID, round.Sequence, round.InputMessageID, round.Status, timeString(round.CreatedAt), timeString(round.StartedAt), ""); err != nil {
			return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
		}
		createdRound = true
	}
	payload := map[string]any{"channel_receipt_id": receiptID, "channel_context": provenance}
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_items(id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,category,kind,summary,payload_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		domain.NewID("conversation"), message.TaskID, round.ID, "", "owner", targetAgentID, "owner", targetAgentID, "owner_message", "chat", "user_message", message.Content, encodeJSON(payload), timeString(message.CreatedAt)); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	inbox := domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: message.TaskID, RoundID: round.ID, TargetAgentID: targetAgentID,
		SourceAgentID: "owner", SourceKind: "owner_message", MessageID: message.ID, Content: message.Content,
		Payload: payload, Status: "pending", CreatedAt: message.CreatedAt,
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		inbox.ID, inbox.TaskID, inbox.RoundID, inbox.TargetAgentID, inbox.SourceAgentID, inbox.SourceKind, "", inbox.MessageID,
		inbox.Content, encodeJSON(inbox.Payload), inbox.Status, "", timeString(inbox.CreatedAt), "", "")
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	inbox.Sequence, err = result.LastInsertId()
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE channel_inbox_dedup SET state='processed',conversation_id=?,aha_message_id=?,outcome='delivered_to_task',lease_id='',lease_until='',processed_at=? WHERE id=? AND state='processing' AND lease_id=? AND aha_message_id=''`,
		conversationID, message.ID, timeString(message.CreatedAt), receiptID, leaseID)
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, ErrChannelRevision
	}
	if err := tx.Commit(); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, false, err
	}
	return round, inbox, createdRound, true, nil
}

func (s *Store) ChannelContextForInboxBatch(ctx context.Context, batchID string) (map[string]any, error) {
	if batchID == "" {
		return nil, sql.ErrNoRows
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM agent_inbox WHERE batch_id=? ORDER BY sequence`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result map[string]any
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, err
		}
		payload := decodeJSON(payloadJSON, map[string]any{})
		value, ok := payload["channel_context"].(map[string]any)
		if !ok || len(value) == 0 {
			return nil, sql.ErrNoRows
		}
		if result == nil {
			result = value
			continue
		}
		if encodeJSON(result) != encodeJSON(value) {
			return nil, errors.New("mixed channel contexts in one inbox batch")
		}
	}
	if result == nil {
		return nil, sql.ErrNoRows
	}
	return result, rows.Err()
}
