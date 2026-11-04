package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrKnowledgeProposalPending  = errors.New("knowledge entry already has a pending proposal")
	ErrKnowledgeProposalRevision = errors.New("knowledge proposal base revision conflict")
	ErrKnowledgeProposalResolved = errors.New("knowledge proposal is not pending")
)

const knowledgeProposalColumns = `id,entry_id,base_revision,proposed_json,source_task_id,source_turn_id,status,created_at,updated_at,decided_at,review_mode,reason,decision_reason`

type knowledgeProposalPayload struct {
	Proposed     domain.KnowledgeEntry  `json:"proposed"`
	BaseEntry    *domain.KnowledgeEntry `json:"base_entry,omitempty"`
	EvidenceJSON string                 `json:"evidence_json,omitempty"`
}

func encodeKnowledgeProposal(item domain.KnowledgeProposal) (string, error) {
	value, err := json.Marshal(knowledgeProposalPayload{Proposed: item.Proposed, BaseEntry: item.BaseEntry, EvidenceJSON: item.Proposed.EvidenceJSON})
	return string(value), err
}

func scanKnowledgeProposal(scanner interface{ Scan(...any) error }) (domain.KnowledgeProposal, error) {
	var item domain.KnowledgeProposal
	var proposedJSON, createdAt, updatedAt, decidedAt string
	if err := scanner.Scan(&item.ID, &item.EntryID, &item.BaseRevision, &proposedJSON, &item.SourceTaskID, &item.SourceTurnID, &item.Status, &createdAt, &updatedAt, &decidedAt, &item.ReviewMode, &item.Reason, &item.DecisionReason); err != nil {
		return domain.KnowledgeProposal{}, err
	}
	var payload knowledgeProposalPayload
	if err := json.Unmarshal([]byte(proposedJSON), &payload); err != nil {
		return domain.KnowledgeProposal{}, fmt.Errorf("decode knowledge proposal %s: %w", item.ID, err)
	}
	payload.Proposed.EvidenceJSON = payload.EvidenceJSON
	item.Proposed, item.BaseEntry = payload.Proposed, payload.BaseEntry
	item.ReviewMode = normalizeKnowledgeReviewMode(item.ReviewMode)
	item.CreatedAt, item.UpdatedAt, item.DecidedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(decidedAt)
	return item, nil
}

func (s *Store) KnowledgeProposal(ctx context.Context, id string) (domain.KnowledgeProposal, error) {
	return scanKnowledgeProposal(s.db.QueryRowContext(ctx, `SELECT `+knowledgeProposalColumns+` FROM knowledge_proposals WHERE id=?`, id))
}

// ImportKnowledgeProposal stores a synchronized proposal without replaying its
// approval/rejection side effects. The corresponding KnowledgeEntry is synced
// independently and remains the source of truth for published content.
func (s *Store) ImportKnowledgeProposal(ctx context.Context, item domain.KnowledgeProposal) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.EntryID) == "" || item.EntryID != item.Proposed.ID || item.BaseRevision < 0 {
		return fmt.Errorf("knowledge proposal is invalid")
	}
	switch item.Status {
	case domain.KnowledgeProposalPending, domain.KnowledgeProposalApproved, domain.KnowledgeProposalRejected:
	default:
		return fmt.Errorf("knowledge proposal status is invalid")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	item.ReviewMode = normalizeKnowledgeReviewMode(item.ReviewMode)
	if item.Status == domain.KnowledgeProposalPending {
		if err := s.defaultKnowledgeParent(ctx, &item.Proposed); err != nil {
			return err
		}
		if err := s.validateKnowledge(ctx, item.Proposed); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, existingErr := scanKnowledgeProposal(tx.QueryRowContext(ctx, `SELECT `+knowledgeProposalColumns+` FROM knowledge_proposals WHERE id=?`, item.ID))
	if existingErr == nil {
		existingTerminal := existing.Status != domain.KnowledgeProposalPending
		incomingTerminal := item.Status != domain.KnowledgeProposalPending
		switch {
		case existingTerminal && !incomingTerminal:
			return tx.Commit()
		case existingTerminal && incomingTerminal && !knowledgeProposalNewer(item, existing):
			return tx.Commit()
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	}
	if item.Status == domain.KnowledgeProposalPending {
		other, otherErr := scanKnowledgeProposal(tx.QueryRowContext(ctx, `SELECT `+knowledgeProposalColumns+` FROM knowledge_proposals WHERE entry_id=? AND status='pending' AND id<>? LIMIT 1`, item.EntryID, item.ID))
		if otherErr == nil {
			resolvedAt := laterProposalTime(item, other)
			if knowledgeProposalNewer(item, other) {
				if _, err := tx.ExecContext(ctx, `UPDATE knowledge_proposals SET status=?,updated_at=?,decided_at=? WHERE id=?`,
					domain.KnowledgeProposalRejected, timeString(resolvedAt), timeString(resolvedAt), other.ID); err != nil {
					return err
				}
			} else {
				item.Status = domain.KnowledgeProposalRejected
				item.UpdatedAt, item.DecidedAt = resolvedAt, resolvedAt
			}
		} else if !errors.Is(otherErr, sql.ErrNoRows) {
			return otherErr
		}
	}
	encoded, err := encodeKnowledgeProposal(item)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_proposals(`+knowledgeProposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET entry_id=excluded.entry_id,base_revision=excluded.base_revision,
		proposed_json=excluded.proposed_json,source_task_id=excluded.source_task_id,source_turn_id=excluded.source_turn_id,
		status=excluded.status,created_at=excluded.created_at,updated_at=excluded.updated_at,decided_at=excluded.decided_at,
		review_mode=excluded.review_mode,reason=excluded.reason,decision_reason=excluded.decision_reason`,
		item.ID, item.EntryID, item.BaseRevision, encoded, item.SourceTaskID, item.SourceTurnID, item.Status,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.DecidedAt), item.ReviewMode,
		item.Reason, item.DecisionReason)
	if err != nil && strings.Contains(err.Error(), "knowledge_proposals.entry_id") {
		return ErrKnowledgeProposalPending
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func proposalTimestamp(item domain.KnowledgeProposal) time.Time {
	if !item.UpdatedAt.IsZero() {
		return item.UpdatedAt
	}
	return item.CreatedAt
}

func laterProposalTime(left, right domain.KnowledgeProposal) time.Time {
	leftTime, rightTime := proposalTimestamp(left), proposalTimestamp(right)
	if rightTime.After(leftTime) {
		return rightTime
	}
	return leftTime
}

func knowledgeProposalNewer(candidate, current domain.KnowledgeProposal) bool {
	candidateTime, currentTime := proposalTimestamp(candidate), proposalTimestamp(current)
	if !candidateTime.Equal(currentTime) {
		return candidateTime.After(currentTime)
	}
	if candidate.Status != current.Status {
		if candidate.Status == domain.KnowledgeProposalApproved {
			return true
		}
		if current.Status == domain.KnowledgeProposalApproved {
			return false
		}
	}
	return candidate.ID > current.ID
}

func (s *Store) DeleteKnowledgeProposal(ctx context.Context, id string) error {
	item, err := s.KnowledgeProposal(ctx, id)
	if err != nil {
		return err
	}
	version := timeString(item.UpdatedAt)
	if version == "" {
		version = timeString(item.CreatedAt)
	}
	_, err = s.deleteSharedObject(ctx, "knowledge_proposal", id, version, time.Now().UTC())
	return err
}

func (s *Store) hasPendingKnowledgeProposal(ctx context.Context, entryID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_proposals WHERE entry_id=? AND status='pending')`, entryID).Scan(&exists)
	return exists, err
}

func (s *Store) ListKnowledgeProposals(ctx context.Context, scope, projectID string) ([]domain.KnowledgeProposal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+knowledgeProposalColumns+` FROM knowledge_proposals ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END,created_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeProposal{}
	for rows.Next() {
		item, err := scanKnowledgeProposal(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	bindings, err := s.knowledgeLibraryBindings(ctx)
	if err != nil {
		return nil, err
	}
	filtered := result[:0]
	for _, item := range result {
		if binding, ok := bindings[item.Proposed.ProjectID]; ok {
			item.Proposed.BoundProjectID = binding.ProjectID
			item.Proposed.BindingMode = binding.BindingMode
			item.Proposed.CanProposeRevision = binding.BindingMode == "project"
		}
		if (scope != "" && item.Proposed.Scope != scope) || (projectID != "" && item.Proposed.ProjectID != projectID && item.Proposed.BoundProjectID != projectID) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}

// ListKnowledgeProposalsPage returns one page of proposals for the updates
// history. Pending proposals are usually fetched unpaginated (limit 0) for the
// batch review flow; the "all" tab uses a small page so the response and the
// rendered feed stay bounded.
func (s *Store) ListKnowledgeProposalsPage(ctx context.Context, scope, projectID, status string, page, limit int) ([]domain.KnowledgeProposal, int, error) {
	items, err := s.ListKnowledgeProposals(ctx, scope, projectID)
	if err != nil {
		return nil, 0, err
	}
	if status != "" && status != "all" {
		filtered := make([]domain.KnowledgeProposal, 0, len(items))
		for _, item := range items {
			if string(item.Status) == status {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	total := len(items)
	if limit <= 0 {
		return items, total, nil
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * limit
	if start >= total {
		return []domain.KnowledgeProposal{}, total, nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return append([]domain.KnowledgeProposal(nil), items[start:end]...), total, nil
}

func (s *Store) CreateKnowledgeProposal(ctx context.Context, item domain.KnowledgeProposal) (domain.KnowledgeProposal, error) {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.EntryID) == "" || item.EntryID != item.Proposed.ID || item.Status != domain.KnowledgeProposalPending {
		return domain.KnowledgeProposal{}, fmt.Errorf("knowledge proposal is invalid")
	}
	if item.BaseRevision == 0 && item.Proposed.IsIndex {
		return domain.KnowledgeProposal{}, ErrKnowledgeRootManaged
	}
	if IsManagedGlobalKnowledgeCategory(item.EntryID) {
		return domain.KnowledgeProposal{}, ErrKnowledgeRootManaged
	}
	if item.BaseRevision < 0 {
		return domain.KnowledgeProposal{}, ErrKnowledgeProposalRevision
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	item.ReviewMode = normalizeKnowledgeReviewMode(item.ReviewMode)
	if item.Proposed.CreatedAt.IsZero() {
		item.Proposed.CreatedAt = item.CreatedAt
	}
	item.Proposed.UpdatedAt = item.UpdatedAt
	item.Proposed.Revision = item.BaseRevision + 1
	item.Proposed.Status = domain.KnowledgeCandidate
	item.Proposed.LastVerifiedAt = time.Time{}
	if err := s.defaultKnowledgeParent(ctx, &item.Proposed); err != nil {
		return domain.KnowledgeProposal{}, err
	}
	if err := s.validateKnowledge(ctx, item.Proposed); err != nil {
		return domain.KnowledgeProposal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeProposal{}, err
	}
	defer tx.Rollback()
	staleExisting := false
	if item.BaseRevision == 0 {
		var revision int
		err := tx.QueryRowContext(ctx, `SELECT revision FROM knowledge_entries WHERE id=?`, item.EntryID).Scan(&revision)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return domain.KnowledgeProposal{}, err
			}
			return domain.KnowledgeProposal{}, ErrKnowledgeProposalRevision
		}
	} else {
		existing, err := scanKnowledge(tx.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE id=?`, item.EntryID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.KnowledgeProposal{}, ErrKnowledgeProposalRevision
			}
			return domain.KnowledgeProposal{}, err
		}
		if existing.Revision != item.BaseRevision || existing.Scope != item.Proposed.Scope || existing.ProjectID != item.Proposed.ProjectID {
			return domain.KnowledgeProposal{}, ErrKnowledgeProposalRevision
		}
		if existing.IsIndex {
			if !item.Proposed.IsIndex || item.Proposed.ParentID != "" || item.Proposed.Slug != "index" || item.Proposed.ProductLineID != "" {
				return domain.KnowledgeProposal{}, ErrKnowledgeRootManaged
			}
		} else if item.Proposed.IsIndex {
			return domain.KnowledgeProposal{}, ErrKnowledgeRootManaged
		}
		staleExisting = !existing.IsIndex
		baseEntry := existing
		item.BaseEntry = &baseEntry
	}
	proposedJSON, err := encodeKnowledgeProposal(item)
	if err != nil {
		return domain.KnowledgeProposal{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_proposals(`+knowledgeProposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.EntryID, item.BaseRevision, proposedJSON, item.SourceTaskID, item.SourceTurnID, item.Status,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.DecidedAt), item.ReviewMode,
		item.Reason, item.DecisionReason)
	if err != nil {
		if strings.Contains(err.Error(), "knowledge_proposals.entry_id") {
			return domain.KnowledgeProposal{}, ErrKnowledgeProposalPending
		}
		return domain.KnowledgeProposal{}, err
	}
	if staleExisting {
		result, err := tx.ExecContext(ctx, `UPDATE knowledge_entries SET status=?,updated_at=? WHERE id=? AND revision=?`,
			domain.KnowledgeStale, timeString(item.UpdatedAt), item.EntryID, item.BaseRevision)
		if err != nil {
			return domain.KnowledgeProposal{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.KnowledgeProposal{}, ErrKnowledgeProposalRevision
		}
		if item.Proposed.ProjectID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.Proposed.ProjectID); err != nil {
				return domain.KnowledgeProposal{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeProposal{}, err
	}
	return s.KnowledgeProposal(ctx, item.ID)
}

func normalizeKnowledgeReviewMode(value string) string {
	if strings.TrimSpace(value) == "auto" {
		return "auto"
	}
	return "manual"
}

func insertKnowledgeTx(ctx context.Context, tx *sql.Tx, item domain.KnowledgeEntry) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO knowledge_entries(`+knowledgeColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Scope, item.ProjectID, item.ParentID, item.Slug, item.SortOrder, item.IsIndex,
		item.Type, item.Title, item.Body, item.Status, item.BranchScope, item.ProductLineID, item.EvidenceJSON,
		item.Confidence, item.Revision, item.ContentHash, item.VerifiedCommit, item.HelpedCount, item.StaleCount,
		item.FeedbackState, item.SourceTaskID, item.SourceTurnID, timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.LastVerifiedAt))
	return err
}

func updateKnowledgeTx(ctx context.Context, tx *sql.Tx, item domain.KnowledgeEntry, baseRevision int) error {
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_entries SET scope=?,project_id=?,parent_id=?,slug=?,sort_order=?,is_index=?,type=?,title=?,body=?,status=?,branch_scope=?,product_line_id=?,evidence_json=?,confidence=?,revision=?,content_hash=?,verified_commit=?,helped_count=?,stale_count=?,feedback_state=?,source_task_id=?,source_turn_id=?,updated_at=?,last_verified_at=? WHERE id=? AND revision=?`,
		item.Scope, item.ProjectID, item.ParentID, item.Slug, item.SortOrder, item.IsIndex, item.Type, item.Title,
		item.Body, item.Status, item.BranchScope, item.ProductLineID, item.EvidenceJSON, item.Confidence, item.Revision,
		item.ContentHash, item.VerifiedCommit, item.HelpedCount, item.StaleCount, item.FeedbackState, item.SourceTaskID,
		item.SourceTurnID, timeString(item.UpdatedAt), timeString(item.LastVerifiedAt), item.ID, baseRevision)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrKnowledgeProposalRevision
	}
	return nil
}

func (s *Store) ApproveKnowledgeProposal(ctx context.Context, id string, decidedAt time.Time, decisionReason ...string) (domain.KnowledgeProposal, domain.KnowledgeEntry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	defer tx.Rollback()
	proposal, err := scanKnowledgeProposal(tx.QueryRowContext(ctx, `SELECT `+knowledgeProposalColumns+` FROM knowledge_proposals WHERE id=?`, id))
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	if proposal.Status != domain.KnowledgeProposalPending {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeProposalResolved
	}
	entry := proposal.Proposed
	entry.ID = proposal.EntryID
	entry.Status = domain.KnowledgeVerified
	entry.Revision = proposal.BaseRevision + 1
	entry.HelpedCount, entry.StaleCount, entry.FeedbackState = 0, 0, ""
	entry.UpdatedAt, entry.LastVerifiedAt = decidedAt.UTC(), decidedAt.UTC()
	if proposal.BaseRevision == 0 {
		var existing int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM knowledge_entries WHERE id=?`, entry.ID).Scan(&existing)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
			}
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeProposalRevision
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = proposal.CreatedAt
		}
		if err := validateKnowledgeWith(ctx, tx, entry); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
		if err := insertKnowledgeTx(ctx, tx, entry); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
	} else {
		existing, err := scanKnowledge(tx.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE id=?`, entry.ID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeProposalRevision
			}
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
		if existing.Revision != proposal.BaseRevision {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeProposalRevision
		}
		if existing.IsIndex {
			if !entry.IsIndex || entry.Scope != existing.Scope || entry.ProjectID != existing.ProjectID || entry.ParentID != "" || entry.Slug != "index" || entry.ProductLineID != "" {
				return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeRootManaged
			}
		} else if entry.IsIndex {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeRootManaged
		}
		entry.CreatedAt = existing.CreatedAt
		if err := validateKnowledgeWith(ctx, tx, entry); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
		if err := updateKnowledgeTx(ctx, tx, entry, proposal.BaseRevision); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
	}
	reason := ""
	if len(decisionReason) > 0 {
		reason = strings.TrimSpace(decisionReason[0])
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_proposals SET status=?,updated_at=?,decided_at=?,decision_reason=? WHERE id=? AND status=?`,
		domain.KnowledgeProposalApproved, timeString(decidedAt), timeString(decidedAt), reason, proposal.ID, domain.KnowledgeProposalPending)
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, ErrKnowledgeProposalResolved
	}
	if entry.ProjectID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, entry.ProjectID); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	proposal, err = s.KnowledgeProposal(ctx, proposal.ID)
	if err != nil {
		return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
	}
	entry, err = s.Knowledge(ctx, entry.ID)
	return proposal, entry, err
}

func (s *Store) RejectKnowledgeProposal(ctx context.Context, id string, decidedAt time.Time, decisionReason ...string) (domain.KnowledgeProposal, error) {
	reason := ""
	if len(decisionReason) > 0 {
		reason = strings.TrimSpace(decisionReason[0])
	}
	result, err := s.db.ExecContext(ctx, `UPDATE knowledge_proposals SET status=?,updated_at=?,decided_at=?,decision_reason=? WHERE id=? AND status=?`,
		domain.KnowledgeProposalRejected, timeString(decidedAt), timeString(decidedAt), reason, id, domain.KnowledgeProposalPending)
	if err != nil {
		return domain.KnowledgeProposal{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, err := s.KnowledgeProposal(ctx, id); err != nil {
			return domain.KnowledgeProposal{}, err
		}
		return domain.KnowledgeProposal{}, ErrKnowledgeProposalResolved
	}
	return s.KnowledgeProposal(ctx, id)
}
