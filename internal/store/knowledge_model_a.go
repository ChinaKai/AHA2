package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func knowledgeStateOrDefault(state string) string {
	switch strings.TrimSpace(state) {
	case domain.KnowledgeStateInitializing, domain.KnowledgeStateReady, domain.KnowledgeStateError:
		return strings.TrimSpace(state)
	default:
		return domain.KnowledgeStateUninitialized
	}
}

// SetProjectKnowledgeState records a space lifecycle transition. The state is
// owned by the project row in model A; there is no separate spaces table.
func (s *Store) SetProjectKnowledgeState(ctx context.Context, projectID, state string) error {
	state = knowledgeStateOrDefault(state)
	result, err := s.db.ExecContext(ctx,
		`UPDATE projects SET knowledge_state=?,updated_at=? WHERE id=?`,
		state, timeString(time.Now().UTC()), strings.TrimSpace(projectID))
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ProjectsWithKnowledgeState lists projects in one lifecycle state. Startup
// reconciliation uses it to retry only the spaces that are not ready.
func (s *Store) ProjectsWithKnowledgeState(ctx context.Context, state string) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,description,project_type,repository_identity,default_workspace_id,default_branch,knowledge_policy,knowledge_revision,knowledge_state,created_at,updated_at
		 FROM projects WHERE knowledge_state=? ORDER BY created_at,id`, knowledgeStateOrDefault(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Project{}
	for rows.Next() {
		var item domain.Project
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.ProjectType, &item.RepositoryIdentity, &item.DefaultWorkspaceID, &item.DefaultBranch, &item.KnowledgePolicy, &item.KnowledgeRevision, &item.KnowledgeState, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

// ReplaceKnowledgeBindings replaces every binding owned by one consumer and
// its explicit global-selection row in one transaction. It is a full
// replacement so an endpoint-level list can become authoritative without
// leaving stale spaces behind, and an endpoint "global off" row can override an
// instance "global on" default.
func (s *Store) ReplaceKnowledgeBindings(ctx context.Context, consumerType, consumerID string, bindings []domain.KnowledgeBinding, includeGlobal bool) ([]domain.KnowledgeBinding, error) {
	consumerType = strings.TrimSpace(consumerType)
	consumerID = strings.TrimSpace(consumerID)
	if consumerType != domain.KnowledgeBindingChannelInstance && consumerType != domain.KnowledgeBindingChannelEndpoint {
		return nil, fmt.Errorf("invalid knowledge binding consumer type %q", consumerType)
	}
	if consumerID == "" {
		return nil, fmt.Errorf("knowledge binding consumer id is required")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM knowledge_bindings WHERE consumer_type=? AND consumer_id=?`,
		consumerType, consumerID); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, binding := range bindings {
		space := strings.TrimSpace(binding.SpaceProject)
		if space == "" || seen[space] {
			continue
		}
		seen[space] = true
		access := strings.TrimSpace(binding.AccessMode)
		if access != "read_write" {
			access = "read"
		}
		scope := strings.TrimSpace(binding.ScopeMode)
		if scope != "selected" {
			scope = "all"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO knowledge_bindings(consumer_type,consumer_id,space_project_id,access_mode,scope_mode,created_at,updated_at)
			 VALUES(?,?,?,?,?,?,?)`,
			consumerType, consumerID, space, access, scope, timeString(now), timeString(now)); err != nil {
			return nil, err
		}
	}
	enabled := 0
	if includeGlobal {
		enabled = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO knowledge_global_bindings(consumer_type,consumer_id,enabled,created_at,updated_at)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(consumer_type,consumer_id) DO UPDATE SET enabled=excluded.enabled,updated_at=excluded.updated_at`,
		consumerType, consumerID, enabled, timeString(now), timeString(now)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListKnowledgeBindings(ctx, consumerType, consumerID)
}

// ListKnowledgeBindings returns the bindings for one consumer ordered by
// project ID so callers get a stable replacement result.
func (s *Store) ListKnowledgeBindings(ctx context.Context, consumerType, consumerID string) ([]domain.KnowledgeBinding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT consumer_type,consumer_id,space_project_id,access_mode,scope_mode,created_at,updated_at
		 FROM knowledge_bindings WHERE consumer_type=? AND consumer_id=? ORDER BY space_project_id`,
		strings.TrimSpace(consumerType), strings.TrimSpace(consumerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeBinding{}
	for rows.Next() {
		var item domain.KnowledgeBinding
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ConsumerType, &item.ConsumerID, &item.SpaceProject, &item.AccessMode, &item.ScopeMode, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

// ChannelEndpointConsumerID is the stable consumer_id used for endpoint-level
// bindings. The instance ID is a prefix so a single instance query can find
// all of its endpoint overrides.
func ChannelEndpointConsumerID(instanceID, endpointKind string) string {
	return strings.TrimSpace(instanceID) + "::" + strings.TrimSpace(endpointKind)
}

// ResolveChannelKnowledgeBindings applies endpoint-over-instance precedence.
// An endpoint binding replaces the instance default; when neither exists the
// result is empty, never an implicit host-project grant.
func (s *Store) ResolveChannelKnowledgeBindings(ctx context.Context, instanceID, endpointKind string) ([]domain.KnowledgeBinding, error) {
	endpoint := strings.TrimSpace(endpointKind)
	if endpoint != "" {
		items, err := s.ListKnowledgeBindings(ctx, domain.KnowledgeBindingChannelEndpoint, ChannelEndpointConsumerID(instanceID, endpoint))
		if err != nil {
			return nil, err
		}
		if len(items) > 0 {
			return items, nil
		}
	}
	return s.ListKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, strings.TrimSpace(instanceID))
}

// KnowledgeGlobalBinding reads one consumer's explicit global selection. The
// second return value reports whether a row exists; an explicit off row is a
// real value, not a missing default.
func (s *Store) KnowledgeGlobalBinding(ctx context.Context, consumerType, consumerID string) (bool, bool, error) {
	var enabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled FROM knowledge_global_bindings WHERE consumer_type=? AND consumer_id=?`,
		strings.TrimSpace(consumerType), strings.TrimSpace(consumerID)).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return enabled != 0, true, nil
}

// ResolveChannelKnowledgeGlobalBinding applies the same endpoint-over-instance
// precedence as ResolveChannelKnowledgeBindings, including an endpoint "off"
// row overriding an instance "on" default. No row anywhere means global is not
// selected.
func (s *Store) ResolveChannelKnowledgeGlobalBinding(ctx context.Context, instanceID, endpointKind string) (bool, error) {
	endpoint := strings.TrimSpace(endpointKind)
	if endpoint != "" {
		enabled, found, err := s.KnowledgeGlobalBinding(ctx, domain.KnowledgeBindingChannelEndpoint, ChannelEndpointConsumerID(instanceID, endpoint))
		if err != nil {
			return false, err
		}
		if found {
			return enabled, nil
		}
	}
	enabled, found, err := s.KnowledgeGlobalBinding(ctx, domain.KnowledgeBindingChannelInstance, strings.TrimSpace(instanceID))
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return enabled, nil
}
