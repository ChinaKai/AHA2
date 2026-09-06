package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrKnowledgeProposalPending  = errors.New("knowledge entry already has a pending proposal")
	ErrKnowledgeProposalRevision = errors.New("knowledge proposal base revision conflict")
	ErrKnowledgeProposalResolved = errors.New("knowledge proposal is not pending")
)

const knowledgeProposalColumns = `id,entry_id,base_revision,proposed_json,source_task_id,source_turn_id,status,created_at,updated_at,decided_at`

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
	if err := scanner.Scan(&item.ID, &item.EntryID, &item.BaseRevision, &proposedJSON, &item.SourceTaskID, &item.SourceTurnID, &item.Status, &createdAt, &updatedAt, &decidedAt); err != nil {
		return domain.KnowledgeProposal{}, err
	}
	var payload knowledgeProposalPayload
	if err := json.Unmarshal([]byte(proposedJSON), &payload); err != nil {
		return domain.KnowledgeProposal{}, fmt.Errorf("decode knowledge proposal %s: %w", item.ID, err)
	}
	payload.Proposed.EvidenceJSON = payload.EvidenceJSON
	item.Proposed, item.BaseEntry = payload.Proposed, payload.BaseEntry
	item.CreatedAt, item.UpdatedAt, item.DecidedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(decidedAt)
	return item, nil
}

func (s *Store) KnowledgeProposal(ctx context.Context, id string) (domain.KnowledgeProposal, error) {
	return scanKnowledgeProposal(s.db.QueryRowContext(ctx, `SELECT `+knowledgeProposalColumns+` FROM knowledge_proposals WHERE id=?`, id))
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
		if (scope != "" && item.Proposed.Scope != scope) || (projectID != "" && item.Proposed.ProjectID != projectID) {
			continue
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateKnowledgeProposal(ctx context.Context, item domain.KnowledgeProposal) (domain.KnowledgeProposal, error) {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.EntryID) == "" || item.EntryID != item.Proposed.ID || item.Status != domain.KnowledgeProposalPending {
		return domain.KnowledgeProposal{}, fmt.Errorf("knowledge proposal is invalid")
	}
	if item.BaseRevision == 0 && item.Proposed.IsIndex {
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
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_proposals(`+knowledgeProposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.EntryID, item.BaseRevision, proposedJSON, item.SourceTaskID, item.SourceTurnID, item.Status,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.DecidedAt))
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

func (s *Store) ApproveKnowledgeProposal(ctx context.Context, id string, decidedAt time.Time) (domain.KnowledgeProposal, domain.KnowledgeEntry, error) {
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
		entry.HelpedCount, entry.StaleCount = existing.HelpedCount, existing.StaleCount
		if err := validateKnowledgeWith(ctx, tx, entry); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
		if err := updateKnowledgeTx(ctx, tx, entry, proposal.BaseRevision); err != nil {
			return domain.KnowledgeProposal{}, domain.KnowledgeEntry{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_proposals SET status=?,updated_at=?,decided_at=? WHERE id=? AND status=?`,
		domain.KnowledgeProposalApproved, timeString(decidedAt), timeString(decidedAt), proposal.ID, domain.KnowledgeProposalPending)
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

func (s *Store) RejectKnowledgeProposal(ctx context.Context, id string, decidedAt time.Time) (domain.KnowledgeProposal, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE knowledge_proposals SET status=?,updated_at=?,decided_at=? WHERE id=? AND status=?`,
		domain.KnowledgeProposalRejected, timeString(decidedAt), timeString(decidedAt), id, domain.KnowledgeProposalPending)
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
