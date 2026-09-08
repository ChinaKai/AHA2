package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) RecordChannelAnswer(ctx context.Context, taskID, roundID, turnID, answer string, at time.Time) (domain.ChannelKnowledgeRecord, error) {
	var conversation domain.ChannelConversation
	var endpointKind, projectID string
	var ownerIdentity sql.NullString
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT c.id,c.instance_id,c.endpoint_id,c.scope_key_version,c.scope_key,c.external_chat_id,c.external_sender_id,c.owner_identity_link_id,c.host_task_id,c.status,c.created_at,c.updated_at,e.kind,i.host_project_id FROM channel_conversations c JOIN channel_endpoints e ON e.id=c.endpoint_id JOIN channel_instances i ON i.id=c.instance_id WHERE c.host_task_id=?`, taskID).
		Scan(&conversation.ID, &conversation.InstanceID, &conversation.EndpointID, &conversation.ScopeKeyVersion, &conversation.ScopeKey, &conversation.ExternalChatID, &conversation.ExternalSenderID, &ownerIdentity, &conversation.HostTaskID, &conversation.Status, &created, &updated, &endpointKind, &projectID)
	if err != nil || endpointKind != domain.ChannelEndpointGroupDigitalHuman {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelKnowledgeRecord{}, err
	}
	var question, payloadJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT summary,payload_json FROM conversation_items WHERE task_id=? AND round_id=? AND kind='user_message' ORDER BY sequence DESC LIMIT 1`, taskID, roundID).Scan(&question, &payloadJSON); err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	payload := decodeJSON(payloadJSON, map[string]any{})
	channelContext, _ := payload["channel_context"].(map[string]any)
	actor, _ := channelContext["actor"].(map[string]any)
	identityID := strings.TrimSpace(fmt.Sprint(actor["identity_link_id"]))
	receiptID := strings.TrimSpace(fmt.Sprint(payload["channel_receipt_id"]))
	if identityID == "" || receiptID == "" || strings.TrimSpace(answer) == "" {
		return domain.ChannelKnowledgeRecord{}, errors.New("channel answer provenance is incomplete")
	}
	var fixedIndexID string
	if err := s.db.QueryRowContext(ctx, `SELECT fixed_index_entry_id FROM channel_knowledge_policies WHERE endpoint_id=?`, conversation.EndpointID).Scan(&fixedIndexID); err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	knowledgeID := stableStoreID("knowledge_channel_qa", turnID)
	title := "渠道问答 · " + shortStoreID(turnID)
	body := fmt.Sprintf("# %s\n\n- requester_identity_link_id: %s\n- source_receipt_id: %s\n- occurred_at: %s\n- visibility: conversation_only\n- authority: observed\n\n## Question\n\n%s\n\n## Answer\n\n%s\n", title, identityID, receiptID, timeString(at), strings.TrimSpace(question), strings.TrimSpace(answer))
	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(title)+"\n"+strings.TrimSpace(body))))
	entry := domain.KnowledgeEntry{
		ID: knowledgeID, Scope: "project", ProjectID: projectID, ParentID: fixedIndexID, Slug: "qa-" + shortStoreID(turnID),
		Type: "channel_qa", Title: title, Body: body, Status: domain.KnowledgeObserved, Confidence: 0.5, Revision: 1,
		ContentHash: contentHash, SourceTaskID: taskID, SourceTurnID: turnID, CreatedAt: at, UpdatedAt: at,
	}
	if err := s.CreateKnowledge(ctx, entry); err != nil {
		if _, lookupErr := s.Knowledge(ctx, knowledgeID); lookupErr != nil {
			return domain.ChannelKnowledgeRecord{}, err
		}
	}
	record := domain.ChannelKnowledgeRecord{
		ID: stableStoreID("channel_knowledge_record", turnID), InstanceID: conversation.InstanceID, ConversationID: conversation.ID,
		KnowledgeEntryID: knowledgeID, RequesterIdentityLinkID: identityID, Question: question, Answer: answer,
		Source:     map[string]any{"receipt_id": receiptID, "task_id": taskID, "round_id": roundID, "turn_id": turnID},
		Visibility: "conversation_only", AuthorityStatus: "observed", OccurredAt: at, CreatedAt: at,
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_knowledge_records(id,instance_id,conversation_id,knowledge_entry_id,requester_identity_link_id,question,answer,source_json,visibility,authority_status,occurred_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ID, record.InstanceID, record.ConversationID, record.KnowledgeEntryID, record.RequesterIdentityLinkID,
		record.Question, record.Answer, encodeJSON(record.Source), record.Visibility, record.AuthorityStatus, timeString(record.OccurredAt), timeString(record.CreatedAt))
	return record, err
}

func stableStoreID(prefix string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + fmt.Sprintf("%x", digest[:12])
}

func shortStoreID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:4])
}

func scanChannelKnowledgeRecord(scanner interface{ Scan(...any) error }) (domain.ChannelKnowledgeRecord, error) {
	var item domain.ChannelKnowledgeRecord
	var source, occurred, created string
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.ConversationID, &item.KnowledgeEntryID, &item.RequesterIdentityLinkID,
		&item.Question, &item.Answer, &source, &item.Visibility, &item.AuthorityStatus, &occurred, &created)
	_ = json.Unmarshal([]byte(source), &item.Source)
	item.OccurredAt, item.CreatedAt = parseTime(occurred), parseTime(created)
	return item, err
}

func (s *Store) ChannelKnowledgeRecords(ctx context.Context, instanceID string) ([]domain.ChannelKnowledgeRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,instance_id,conversation_id,knowledge_entry_id,requester_identity_link_id,question,answer,source_json,visibility,authority_status,occurred_at,created_at FROM channel_knowledge_records WHERE instance_id=? ORDER BY created_at DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ChannelKnowledgeRecord{}
	for rows.Next() {
		item, err := scanChannelKnowledgeRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ChannelKnowledgeRecord(ctx context.Context, id string) (domain.ChannelKnowledgeRecord, error) {
	return scanChannelKnowledgeRecord(s.db.QueryRowContext(ctx, `SELECT id,instance_id,conversation_id,knowledge_entry_id,requester_identity_link_id,question,answer,source_json,visibility,authority_status,occurred_at,created_at FROM channel_knowledge_records WHERE id=?`, id))
}

func (s *Store) PromoteChannelKnowledgeRecord(ctx context.Context, id, instanceID, title, body string, at time.Time) (domain.ChannelKnowledgeRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	defer tx.Rollback()
	record, err := scanChannelKnowledgeRecord(tx.QueryRowContext(ctx, `SELECT id,instance_id,conversation_id,knowledge_entry_id,requester_identity_link_id,question,answer,source_json,visibility,authority_status,occurred_at,created_at FROM channel_knowledge_records WHERE id=? AND instance_id=?`, id, instanceID))
	if err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(body) == "" {
		return domain.ChannelKnowledgeRecord{}, errors.New("curated title and body are required")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(title)+"\n"+strings.TrimSpace(body))))
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_entries SET title=?,body=?,status='verified',revision=revision+1,content_hash=?,updated_at=?,last_verified_at=? WHERE id=?`, title, body, hash, timeString(at), timeString(at), record.KnowledgeEntryID); err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_knowledge_records SET visibility='instance_shared',authority_status='verified' WHERE id=?`, id); err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1,updated_at=? WHERE id=(SELECT host_project_id FROM channel_instances WHERE id=?)`, timeString(at), instanceID); err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	return scanChannelKnowledgeRecord(s.db.QueryRowContext(ctx, `SELECT id,instance_id,conversation_id,knowledge_entry_id,requester_identity_link_id,question,answer,source_json,visibility,authority_status,occurred_at,created_at FROM channel_knowledge_records WHERE id=?`, id))
}

func (s *Store) ChannelKnowledgePolicies(ctx context.Context, instanceID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,e.kind,p.fixed_index_entry_id,p.default_visibility,p.revision,p.created_at,p.updated_at FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? ORDER BY e.kind`, instanceID)
	if err != nil {
		return nil, err
	}
	type policyRow struct {
		id, kind, fixed, visibility, created, updated string
		revision                                      int
	}
	policies := []policyRow{}
	for rows.Next() {
		var item policyRow
		if err := rows.Scan(&item.id, &item.kind, &item.fixed, &item.visibility, &item.revision, &item.created, &item.updated); err != nil {
			rows.Close()
			return nil, err
		}
		policies = append(policies, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, item := range policies {
		grantRows, err := s.db.QueryContext(ctx, `SELECT knowledge_entry_id,grant_scope FROM channel_knowledge_grants WHERE policy_id=? AND revoked_at='' ORDER BY knowledge_entry_id,grant_scope`, item.id)
		if err != nil {
			return nil, err
		}
		grants := []map[string]string{}
		for grantRows.Next() {
			var entryID, scope string
			if err := grantRows.Scan(&entryID, &scope); err != nil {
				grantRows.Close()
				return nil, err
			}
			grants = append(grants, map[string]string{"knowledge_entry_id": entryID, "grant_scope": scope})
		}
		grantRows.Close()
		result = append(result, map[string]any{"id": item.id, "endpoint": item.kind, "fixed_index_entry_id": item.fixed, "default_visibility": item.visibility, "revision": item.revision, "grants": grants, "created_at": item.created, "updated_at": item.updated})
	}
	return result, nil
}

func (s *Store) ReplaceChannelKnowledgeGrants(ctx context.Context, instanceID, endpointKind, ownerID string, expectedRevision int, grants []domain.ChannelKnowledgeGrant, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var policyID string
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT p.id,p.revision FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? AND e.kind=?`, instanceID, endpointKind).Scan(&policyID, &revision); err != nil {
		return err
	}
	if revision != expectedRevision {
		return ErrChannelRevision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_knowledge_grants SET revoked_at=? WHERE policy_id=? AND revoked_at=''`, timeString(at), policyID); err != nil {
		return err
	}
	for _, grant := range grants {
		if grant.GrantScope != "node" && grant.GrantScope != "subtree" {
			return errors.New("invalid channel knowledge grant scope")
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM knowledge_entries WHERE id=?`, grant.KnowledgeEntryID).Scan(&status); err != nil {
			return err
		}
		if status != string(domain.KnowledgeVerified) {
			return errors.New("channel knowledge grant requires a verified stable entry id")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_knowledge_grants(id,policy_id,knowledge_entry_id,grant_scope,granted_by_owner_id,created_at,revoked_at) VALUES(?,?,?,?,?,?,'')`, domain.NewID("channel_knowledge_grant"), policyID, grant.KnowledgeEntryID, grant.GrantScope, ownerID, timeString(at)); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE channel_knowledge_policies SET revision=revision+1,updated_at=? WHERE id=? AND revision=?`, timeString(at), policyID, expectedRevision)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return tx.Commit()
}
