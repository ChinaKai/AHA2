package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrKnowledgeInvalidScope = errors.New("knowledge scope is invalid")
	ErrKnowledgeInvalidSlug  = errors.New("knowledge slug is invalid")
	ErrKnowledgeParent       = errors.New("knowledge parent must be in the same scope and project")
	ErrKnowledgeCycle        = errors.New("knowledge hierarchy contains a cycle")
	ErrKnowledgeRoot         = errors.New("knowledge scope already has a root index")
	ErrKnowledgeRootManaged  = errors.New("knowledge root index is managed and cannot be replaced or deleted")
	ErrKnowledgeSiblingSlug  = errors.New("knowledge sibling slug already exists")
	ErrKnowledgeHasChildren  = errors.New("knowledge entry has children")
)

var knowledgeSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const knowledgeColumns = `id,scope,project_id,parent_id,slug,sort_order,is_index,type,title,body,status,branch_scope,product_line_id,evidence_json,confidence,revision,content_hash,verified_commit,helped_count,stale_count,feedback_state,source_task_id,source_turn_id,created_at,updated_at,last_verified_at`

func ValidKnowledgeSlug(value string) bool {
	return knowledgeSlugPattern.MatchString(value)
}

func KnowledgeSlug(title, fallback string) string {
	if value := knowledgeSlug(title); value != "" {
		return value
	}
	return knowledgeSlug(fallback)
}

func knowledgeRootID(scope, projectID string) string {
	if scope == "global" {
		return "knowledge_root_global"
	}
	return "knowledge_root_project_" + projectID
}

func knowledgeRootDefaults(scope, projectID string, now time.Time) domain.KnowledgeEntry {
	title := "全局知识首页"
	body := "这里汇总跨项目共享的知识文档。"
	if scope == "project" {
		title = "知识首页"
		body = "这里汇总本项目的知识文档与常用入口。"
	}
	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(title)+"\n"+strings.TrimSpace(body))))
	return domain.KnowledgeEntry{
		ID:             knowledgeRootID(scope, projectID),
		Scope:          scope,
		ProjectID:      projectID,
		Slug:           "index",
		IsIndex:        true,
		Type:           "navigation",
		Title:          title,
		Body:           body,
		Status:         domain.KnowledgeVerified,
		Confidence:     1,
		Revision:       1,
		ContentHash:    contentHash,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastVerifiedAt: now,
	}
}

// EnsureKnowledgeRoot creates the durable root index for one knowledge scope and
// attaches legacy top-level documents. Once the scope is normalized, repeated
// calls only read and return the existing root.
func (s *Store) EnsureKnowledgeRoot(ctx context.Context, scope, projectID string) (domain.KnowledgeEntry, error) {
	scope, projectID = strings.TrimSpace(scope), strings.TrimSpace(projectID)
	if scope == "global" {
		projectID = ""
	} else if scope != "project" || projectID == "" {
		return domain.KnowledgeEntry{}, ErrKnowledgeInvalidScope
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	defer tx.Rollback()
	if scope == "project" {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id=?`, projectID).Scan(&exists); err != nil {
			return domain.KnowledgeEntry{}, err
		}
	}
	root, err := scanKnowledge(tx.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE scope=? AND project_id=? AND is_index=1 LIMIT 1`, scope, projectID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeEntry{}, err
	}
	changed := false
	if errors.Is(err, sql.ErrNoRows) {
		root = knowledgeRootDefaults(scope, projectID, time.Now().UTC())
		_, err = tx.ExecContext(ctx, `
			INSERT INTO knowledge_entries(`+knowledgeColumns+`)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			root.ID, root.Scope, root.ProjectID, root.ParentID, root.Slug, root.SortOrder, root.IsIndex,
			root.Type, root.Title, root.Body, root.Status, root.BranchScope,
			root.ProductLineID, root.EvidenceJSON, root.Confidence, root.Revision, root.ContentHash, root.VerifiedCommit,
			root.HelpedCount, root.StaleCount, root.FeedbackState, root.SourceTaskID, root.SourceTurnID,
			timeString(root.CreatedAt), timeString(root.UpdatedAt), timeString(root.LastVerifiedAt),
		)
		if err != nil {
			return domain.KnowledgeEntry{}, err
		}
		changed = true
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_entries SET parent_id=? WHERE scope=? AND project_id=? AND parent_id='' AND is_index=0`, root.ID, scope, projectID)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		changed = true
	}
	result, err = tx.ExecContext(ctx, `UPDATE knowledge_entries
		SET parent_id='',slug='index',is_index=1,status=?,product_line_id='',revision=CASE WHEN revision<1 THEN 1 ELSE revision END,
			last_verified_at=CASE WHEN last_verified_at='' THEN updated_at ELSE last_verified_at END
		WHERE id=? AND (parent_id<>'' OR slug<>'index' OR is_index<>1 OR status<>? OR product_line_id<>'' OR revision<1 OR last_verified_at='')`,
		domain.KnowledgeVerified, root.ID, domain.KnowledgeVerified)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		changed = true
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeEntry{}, err
	}
	if changed {
		return s.Knowledge(ctx, root.ID)
	}
	return root, nil
}

func (s *Store) EnsureKnowledgeRoots(ctx context.Context) error {
	if _, err := s.EnsureKnowledgeRoot(ctx, "global", ""); err != nil {
		return err
	}
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if _, err := s.EnsureKnowledgeRoot(ctx, "project", project.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) defaultKnowledgeParent(ctx context.Context, item *domain.KnowledgeEntry) error {
	if item.IsIndex || item.ParentID != "" {
		return nil
	}
	root, err := scanKnowledge(s.db.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE scope=? AND project_id=? AND is_index=1 LIMIT 1`, item.Scope, item.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		root, err = s.EnsureKnowledgeRoot(ctx, item.Scope, item.ProjectID)
	}
	if err != nil {
		return err
	}
	item.ParentID = root.ID
	return nil
}

func knowledgeSlug(value string) string {
	var result strings.Builder
	separator := false
	for _, value := range strings.ToLower(strings.TrimSpace(value)) {
		if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' {
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(value)
			separator = false
		} else {
			separator = true
		}
	}
	return strings.Trim(result.String(), "-")
}

type knowledgeQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) validateKnowledge(ctx context.Context, item domain.KnowledgeEntry) error {
	return validateKnowledgeWith(ctx, s.db, item)
}

func validateKnowledgeWith(ctx context.Context, queryer knowledgeQueryer, item domain.KnowledgeEntry) error {
	if item.Scope == "global" {
		if item.ProjectID != "" {
			return ErrKnowledgeInvalidScope
		}
	} else if item.Scope != "project" || item.ProjectID == "" {
		return ErrKnowledgeInvalidScope
	}
	if item.Slug != "" && !ValidKnowledgeSlug(item.Slug) {
		return ErrKnowledgeInvalidSlug
	}
	if item.SortOrder < 0 {
		return fmt.Errorf("knowledge sort order must be non-negative")
	}
	if item.IsIndex && item.ParentID != "" {
		return fmt.Errorf("%w: root index cannot have a parent", ErrKnowledgeParent)
	}
	if item.ParentID != "" {
		if item.ParentID == item.ID {
			return ErrKnowledgeCycle
		}
		parent, err := scanKnowledge(queryer.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE id=?`, item.ParentID))
		if err != nil || parent.Scope != item.Scope || parent.ProjectID != item.ProjectID {
			return ErrKnowledgeParent
		}
		seen := map[string]bool{item.ID: true}
		for parent.ID != "" {
			if seen[parent.ID] {
				return ErrKnowledgeCycle
			}
			seen[parent.ID] = true
			if parent.ParentID == "" {
				break
			}
			parent, err = scanKnowledge(queryer.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE id=?`, parent.ParentID))
			if err != nil {
				return ErrKnowledgeParent
			}
		}
	}
	if item.IsIndex {
		var existing string
		err := queryer.QueryRowContext(ctx, `SELECT id FROM knowledge_entries WHERE scope=? AND project_id=? AND is_index=1 AND id<>? LIMIT 1`, item.Scope, item.ProjectID, item.ID).Scan(&existing)
		if err == nil {
			return ErrKnowledgeRoot
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if item.Slug != "" {
		var existing string
		err := queryer.QueryRowContext(ctx, `SELECT id FROM knowledge_entries WHERE scope=? AND project_id=? AND parent_id=? AND slug=? AND id<>? LIMIT 1`, item.Scope, item.ProjectID, item.ParentID, item.Slug, item.ID).Scan(&existing)
		if err == nil {
			return ErrKnowledgeSiblingSlug
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *Store) CreateKnowledge(ctx context.Context, item domain.KnowledgeEntry) error {
	if item.IsIndex {
		return ErrKnowledgeRootManaged
	}
	if item.Revision < 1 {
		item.Revision = 1
	}
	if err := s.defaultKnowledgeParent(ctx, &item); err != nil {
		return err
	}
	if err := s.validateKnowledge(ctx, item); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_entries(`+knowledgeColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Scope, item.ProjectID, item.ParentID, item.Slug, item.SortOrder, item.IsIndex,
		item.Type, item.Title, item.Body, item.Status, item.BranchScope,
		item.ProductLineID, item.EvidenceJSON, item.Confidence, item.Revision, item.ContentHash, item.VerifiedCommit,
		item.HelpedCount, item.StaleCount, item.FeedbackState, item.SourceTaskID, item.SourceTurnID,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.LastVerifiedAt),
	)
	if err == nil && item.ProjectID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.ProjectID)
	}
	return err
}

func scanKnowledge(scanner interface{ Scan(...any) error }) (domain.KnowledgeEntry, error) {
	var item domain.KnowledgeEntry
	var createdAt, updatedAt, verifiedAt string
	err := scanner.Scan(&item.ID, &item.Scope, &item.ProjectID, &item.ParentID, &item.Slug, &item.SortOrder,
		&item.IsIndex, &item.Type, &item.Title, &item.Body, &item.Status,
		&item.BranchScope, &item.ProductLineID, &item.EvidenceJSON, &item.Confidence, &item.Revision, &item.ContentHash,
		&item.VerifiedCommit, &item.HelpedCount, &item.StaleCount, &item.FeedbackState, &item.SourceTaskID,
		&item.SourceTurnID, &createdAt, &updatedAt, &verifiedAt)
	item.CreatedAt, item.UpdatedAt, item.LastVerifiedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(verifiedAt)
	return item, err
}

func (s *Store) Knowledge(ctx context.Context, id string) (domain.KnowledgeEntry, error) {
	return scanKnowledge(s.db.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE id=?`, id))
}

func (s *Store) ListKnowledge(ctx context.Context, scope, projectID string, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	return s.listKnowledge(ctx, scope, projectID, "", false, statuses)
}

func (s *Store) ListApplicableKnowledge(ctx context.Context, projectID, productLineID string, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	return s.listKnowledge(ctx, "project", projectID, productLineID, true, statuses)
}

func (s *Store) listKnowledge(ctx context.Context, scope, projectID, productLineID string, applicableOnly bool, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	query := `SELECT ` + knowledgeColumns + ` FROM knowledge_entries WHERE 1=1`
	args := []any{}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	if applicableOnly {
		query += ` AND (product_line_id='' OR product_line_id=?)`
		args = append(args, productLineID)
	}
	if len(statuses) > 0 {
		query += ` AND status IN (`
		for index, status := range statuses {
			if index > 0 {
				query += `,`
			}
			query += `?`
			args = append(args, status)
		}
		query += `)`
	}
	query += ` ORDER BY is_index DESC,parent_id,sort_order,slug,type,title,updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeEntry{}
	for rows.Next() {
		item, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MatchingKnowledge(ctx context.Context, scope, projectID, productLineID, kind, title string) (domain.KnowledgeEntry, error) {
	return scanKnowledge(s.db.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries
		WHERE scope=? AND project_id=? AND product_line_id=? AND is_index=0 AND type=? AND lower(title)=lower(?) ORDER BY updated_at DESC LIMIT 1`,
		scope, projectID, productLineID, kind, strings.TrimSpace(title)))
}

func (s *Store) UpdateKnowledge(ctx context.Context, item domain.KnowledgeEntry) error {
	existing, err := s.Knowledge(ctx, item.ID)
	if err != nil {
		return err
	}
	if existing.IsIndex {
		if !item.IsIndex || item.Scope != existing.Scope || item.ProjectID != existing.ProjectID || item.ParentID != "" || item.Slug != "index" || item.Status != domain.KnowledgeVerified || item.ProductLineID != "" {
			return ErrKnowledgeRootManaged
		}
	} else if item.IsIndex {
		return ErrKnowledgeRootManaged
	}
	if item.Status == domain.KnowledgeVerified {
		pending, err := s.hasPendingKnowledgeProposal(ctx, item.ID)
		if err != nil {
			return err
		}
		if pending {
			return ErrKnowledgeProposalPending
		}
	}
	if item.Revision < 1 {
		item.Revision = 1
	}
	if err := s.defaultKnowledgeParent(ctx, &item); err != nil {
		return err
	}
	if err := s.validateKnowledge(ctx, item); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE knowledge_entries SET scope=?,project_id=?,parent_id=?,slug=?,sort_order=?,is_index=?,type=?,title=?,body=?,status=?,branch_scope=?,product_line_id=?,evidence_json=?,confidence=?,revision=?,content_hash=?,verified_commit=?,helped_count=?,stale_count=?,feedback_state=?,source_task_id=?,source_turn_id=?,updated_at=?,last_verified_at=? WHERE id=?`,
		item.Scope, item.ProjectID, item.ParentID, item.Slug, item.SortOrder, item.IsIndex,
		item.Type, item.Title, item.Body, item.Status, item.BranchScope, item.ProductLineID,
		item.EvidenceJSON, item.Confidence, item.Revision, item.ContentHash, item.VerifiedCommit, item.HelpedCount,
		item.StaleCount, item.FeedbackState, item.SourceTaskID, item.SourceTurnID, timeString(item.UpdatedAt),
		timeString(item.LastVerifiedAt), item.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	if item.ProjectID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.ProjectID)
	}
	return nil
}

func (s *Store) DeleteKnowledge(ctx context.Context, id string) error {
	item, err := s.Knowledge(ctx, id)
	if err != nil {
		return err
	}
	if item.IsIndex {
		return ErrKnowledgeRootManaged
	}
	pending, err := s.hasPendingKnowledgeProposal(ctx, id)
	if err != nil {
		return err
	}
	if pending {
		return ErrKnowledgeProposalPending
	}
	var children int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries WHERE parent_id=?`, id).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return ErrKnowledgeHasChildren
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE id=?`, id)
	if err == nil && item.ProjectID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.ProjectID)
	}
	return err
}

func (s *Store) VerifyKnowledge(ctx context.Context, id, updatedAt string) error {
	item, err := s.Knowledge(ctx, id)
	if err != nil {
		return err
	}
	item.Status = domain.KnowledgeVerified
	item.Revision++
	item.FeedbackState = ""
	item.LastVerifiedAt = parseTime(updatedAt)
	item.UpdatedAt = item.LastVerifiedAt
	return s.UpdateKnowledge(ctx, item)
}

func (s *Store) FeedbackKnowledge(ctx context.Context, id, kind string, updatedAt string) (domain.KnowledgeEntry, error) {
	item, err := s.Knowledge(ctx, id)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	switch kind {
	case "helped":
		item.HelpedCount++
		item.FeedbackState = "helped"
	case "stale", "wrong":
		item.StaleCount++
		item.FeedbackState = kind
		if !item.IsIndex {
			item.Status = domain.KnowledgeStale
		}
	default:
		return domain.KnowledgeEntry{}, sql.ErrNoRows
	}
	item.UpdatedAt = parseTime(updatedAt)
	if err := s.UpdateKnowledge(ctx, item); err != nil {
		return domain.KnowledgeEntry{}, err
	}
	return item, nil
}
