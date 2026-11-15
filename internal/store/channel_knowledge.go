package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// ErrChannelKnowledgeUnconfigured means the endpoint has no policy row yet.
// It is a normal state for a channel instance created while the file vault is
// the read source: channel knowledge is never granted implicitly, so callers
// must treat this as an explicit empty selection instead of a read failure.
var ErrChannelKnowledgeUnconfigured = errors.New("channel knowledge is not configured")

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
	Configured        bool
	Grants            []domain.ChannelKnowledgeGrant
}

// ChannelKnowledgeSelection reads the endpoint configuration. A missing policy
// row is returned as ErrChannelKnowledgeUnconfigured so callers can keep the
// empty selection explicit; every other error keeps its original meaning.
//
// The private chat is verified as the bound Owner and its read path always
// forces "all", but it still needs a policy row to be configured. A vault-era
// instance only gets a policy when the owner selects group sources, so the DM
// endpoint borrows that policy: the existing product rule is "the private chat
// always sees all". The group's grants are not used by the DM read path; only
// its existence and fixed index are.
func (s *Store) ChannelKnowledgeSelection(ctx context.Context, instanceID, endpointKind string) (ChannelKnowledgeSelection, error) {
	result, err := s.channelKnowledgeSelectionForEndpoint(ctx, instanceID, endpointKind)
	if errors.Is(err, ErrChannelKnowledgeUnconfigured) && endpointKind == domain.ChannelEndpointAssistantDM {
		return s.channelKnowledgeSelectionForEndpoint(ctx, instanceID, domain.ChannelEndpointGroupDigitalHuman)
	}
	return result, err
}

func (s *Store) channelKnowledgeSelectionForEndpoint(ctx context.Context, instanceID, endpointKind string) (ChannelKnowledgeSelection, error) {
	var result ChannelKnowledgeSelection
	err := s.db.QueryRowContext(ctx, `SELECT p.id,p.fixed_index_entry_id,p.scope_mode FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? AND e.kind=? AND e.enabled=1`, instanceID, endpointKind).
		Scan(&result.PolicyID, &result.FixedIndexEntryID, &result.ScopeMode)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelKnowledgeSelection{}, ErrChannelKnowledgeUnconfigured
	}
	if err != nil {
		return ChannelKnowledgeSelection{}, err
	}
	result.Configured = true
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
//
// expectedRevision 0 means "the endpoint has no policy yet": one is created
// with an empty fixed index. A vault-routed channel host project has no legacy
// root to use as its fixed index, and writing a shadow legacy row would defeat
// the vault read source, so the explicit owner selection is the only thing that
// creates the policy.
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
	lookupErr := tx.QueryRowContext(ctx, `SELECT p.id,p.revision FROM channel_knowledge_policies p JOIN channel_endpoints e ON e.id=p.endpoint_id WHERE p.instance_id=? AND e.kind=?`, instanceID, endpointKind).Scan(&policyID, &revision)
	switch {
	case errors.Is(lookupErr, sql.ErrNoRows):
		if expectedRevision != 0 {
			return ErrChannelRevision
		}
		var endpointID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM channel_endpoints WHERE instance_id=? AND kind=?`, instanceID, endpointKind).Scan(&endpointID); err != nil {
			return err
		}
		policyID = domain.NewID("channel_knowledge_policy")
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_knowledge_policies(id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,scope_mode,revision,created_at,updated_at) VALUES(?,?,?,'','conversation_only',?,1,?,?)`,
			policyID, instanceID, endpointID, scopeMode, timeString(at), timeString(at)); err != nil {
			if isUniqueConstraintError(err) {
				// A concurrent first selection created the endpoint policy.
				return ErrChannelRevision
			}
			return err
		}
		revision = 1
	case lookupErr != nil:
		return lookupErr
	case revision != expectedRevision:
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
	result, err := tx.ExecContext(ctx, `UPDATE channel_knowledge_policies SET scope_mode=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, scopeMode, timeString(at), policyID, revision)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return tx.Commit()
}
