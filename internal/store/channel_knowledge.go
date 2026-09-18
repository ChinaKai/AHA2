package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func stableStoreID(prefix string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + fmt.Sprintf("%x", digest[:12])
}

func (s *Store) ChannelKnowledgePolicies(ctx context.Context, instanceID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,e.kind,p.fixed_index_entry_id,p.default_visibility,p.scope_mode,p.revision,p.created_at,p.updated_at FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? ORDER BY e.kind`, instanceID)
	if err != nil {
		return nil, err
	}
	type policyRow struct {
		id, kind, fixed, visibility, scopeMode, created, updated string
		revision                                                 int
	}
	policies := []policyRow{}
	for rows.Next() {
		var item policyRow
		if err := rows.Scan(&item.id, &item.kind, &item.fixed, &item.visibility, &item.scopeMode, &item.revision, &item.created, &item.updated); err != nil {
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
		result = append(result, map[string]any{"id": item.id, "endpoint": item.kind, "fixed_index_entry_id": item.fixed, "default_visibility": item.visibility, "scope_mode": item.scopeMode, "revision": item.revision, "grants": grants, "created_at": item.created, "updated_at": item.updated})
	}
	return result, nil
}

func (s *Store) ReplaceChannelKnowledgeGrants(ctx context.Context, instanceID, endpointKind, ownerID, scopeMode string, expectedRevision int, grants []domain.ChannelKnowledgeGrant, at time.Time) error {
	if scopeMode != "all" && scopeMode != "selected" {
		return errors.New("invalid channel knowledge scope mode")
	}
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
	result, err := tx.ExecContext(ctx, `UPDATE channel_knowledge_policies SET scope_mode=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, scopeMode, timeString(at), policyID, expectedRevision)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return tx.Commit()
}
