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

// ChannelKnowledgeSelection is the stored configuration for one endpoint: the
// fixed index plus the owner-selected grants. The IDs may name either a legacy
// knowledge row or a vault document; resolving them against the current read
// source is the app layer's job, not the store's.
type ChannelKnowledgeSelection struct {
	PolicyID          string
	FixedIndexEntryID string
	ScopeMode         string
	Grants            []domain.ChannelKnowledgeGrant
}

func (s *Store) ChannelKnowledgeSelection(ctx context.Context, instanceID, endpointKind string) (ChannelKnowledgeSelection, error) {
	var result ChannelKnowledgeSelection
	err := s.db.QueryRowContext(ctx, `SELECT p.id,p.fixed_index_entry_id,p.scope_mode FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? AND e.kind=? AND e.enabled=1`, instanceID, endpointKind).
		Scan(&result.PolicyID, &result.FixedIndexEntryID, &result.ScopeMode)
	if err != nil {
		return ChannelKnowledgeSelection{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT knowledge_entry_id,grant_scope FROM channel_knowledge_grants WHERE policy_id=? AND revoked_at='' ORDER BY knowledge_entry_id,grant_scope`, result.PolicyID)
	if err != nil {
		return ChannelKnowledgeSelection{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var grant domain.ChannelKnowledgeGrant
		if err := rows.Scan(&grant.KnowledgeEntryID, &grant.GrantScope); err != nil {
			return ChannelKnowledgeSelection{}, err
		}
		grant.PolicyID = result.PolicyID
		result.Grants = append(result.Grants, grant)
	}
	return result, rows.Err()
}

// ReplaceChannelKnowledgeGrants replaces the active grants of one endpoint.
// vaultIDs names the roots whose validation already happened against the vault
// index; every other ID must still exist as a verified legacy knowledge entry.
// This keeps the legacy integrity check without making a vault root depend on a
// legacy row that no longer describes what the project reads.
func (s *Store) ReplaceChannelKnowledgeGrants(ctx context.Context, instanceID, endpointKind, ownerID, scopeMode string, expectedRevision int, grants []domain.ChannelKnowledgeGrant, vaultIDs map[string]bool, at time.Time) error {
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
		if !vaultIDs[grant.KnowledgeEntryID] {
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM knowledge_entries WHERE id=?`, grant.KnowledgeEntryID).Scan(&status); err != nil {
				return err
			}
			if status != string(domain.KnowledgeVerified) {
				return errors.New("channel knowledge grant requires a verified stable entry id")
			}
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
