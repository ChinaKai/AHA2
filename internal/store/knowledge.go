package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrKnowledgeInvalidScope  = errors.New("knowledge scope is invalid")
	ErrKnowledgeInvalidSlug   = errors.New("knowledge slug is invalid")
	ErrKnowledgeParent        = errors.New("knowledge parent must be in the same scope and project")
	ErrKnowledgeCycle         = errors.New("knowledge hierarchy contains a cycle")
	ErrKnowledgeRoot          = errors.New("knowledge scope already has a root index")
	ErrKnowledgeRootManaged   = errors.New("knowledge root index is managed and cannot be replaced or deleted")
	ErrKnowledgeSiblingSlug   = errors.New("knowledge sibling slug already exists")
	ErrKnowledgeHasChildren   = errors.New("knowledge entry has children")
	ErrKnowledgeFeedbackState = errors.New("knowledge feedback conflicts with current revision state")
)

var knowledgeSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	GlobalKnowledgeRootID             = "knowledge_root_global"
	GlobalGeneralKnowledgeID          = "knowledge_global_general"
	GlobalAgentLessonsKnowledgeID     = "knowledge_global_agent_lessons"
	GlobalTechnicalLessonsKnowledgeID = "knowledge_global_agent_lessons_technical"
	GlobalBehaviorLessonsKnowledgeID  = "knowledge_global_agent_lessons_behavior"
)

const globalKnowledgeRootBody = "这是 AHA2 全局知识页面。通用知识以人类阅读为主，Agent 仅在任务主题明确相关时按需读取；Agent 经验教训用于沉淀可跨项目复用的技术诊断与行为教训。请根据分类、标题与摘要选择阅读路径。"

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
		return GlobalKnowledgeRootID
	}
	return "knowledge_root_project_" + projectID
}

func knowledgeRootDefaults(scope, projectID string, now time.Time) domain.KnowledgeEntry {
	title := "全局知识首页"
	body := globalKnowledgeRootBody
	if scope == "project" {
		title = "知识首页"
		body = "这是当前项目的知识页面，保存项目实践、技术决策、诊断结论和可复用经验。请根据下面的标题与摘要按需打开文档。"
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

func IsManagedGlobalKnowledgeCategory(id string) bool {
	switch id {
	case GlobalGeneralKnowledgeID, GlobalAgentLessonsKnowledgeID, GlobalTechnicalLessonsKnowledgeID, GlobalBehaviorLessonsKnowledgeID:
		return true
	default:
		return false
	}
}

func globalKnowledgeCategories(rootID string, now time.Time) []domain.KnowledgeEntry {
	values := []domain.KnowledgeEntry{
		{
			ID: GlobalGeneralKnowledgeID, ParentID: rootID, Slug: "general", SortOrder: 0,
			Title: "通用知识", Body: "面向人类阅读的跨项目参考资料。Agent 仅在当前任务主题明确相关时读取，不作为默认行为约束。",
		},
		{
			ID: GlobalAgentLessonsKnowledgeID, ParentID: rootID, Slug: "agent-lessons", SortOrder: 10,
			Title: "Agent 经验教训", Body: "Agent 应优先检查的跨项目经验索引。只沉淀可复用的触发条件、正确行为、验证方法与必要例外，不保留事故叙事或已否决方案的残留说明。",
		},
		{
			ID: GlobalTechnicalLessonsKnowledgeID, ParentID: GlobalAgentLessonsKnowledgeID, Slug: "technical-diagnostics", SortOrder: 0,
			Title: "技术诊断", Body: "记录稳定可复现的技术陷阱：现象、原因、安全修复与验证方法。",
		},
		{
			ID: GlobalBehaviorLessonsKnowledgeID, ParentID: GlobalAgentLessonsKnowledgeID, Slug: "behavior-lessons", SortOrder: 10,
			Title: "行为教训", Body: "记录 Agent 做事方式的可复用纠正：触发条件、应执行的行为、验收标准与例外。必须始终执行的规则应晋升到 Prompt 或 Skill。",
		},
	}
	for index := range values {
		values[index].Scope = "global"
		values[index].Type = "navigation"
		values[index].Status = domain.KnowledgeVerified
		values[index].Confidence = 1
		values[index].Revision = 1
		values[index].ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(values[index].Title)+"\n"+strings.TrimSpace(values[index].Body))))
		values[index].CreatedAt = now
		values[index].UpdatedAt = now
		values[index].LastVerifiedAt = now
	}
	return values
}

// EnsureGlobalKnowledgeCategories creates the two user-facing global sections
// and moves legacy root-level documents into them. Diagnostics and Agent-authored
// guidance become lessons; remaining human/imported references stay general.
func (s *Store) EnsureGlobalKnowledgeCategories(ctx context.Context) error {
	root, err := s.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_entries
		SET parent_id=CASE
			WHEN type='diagnostic' THEN ?
			WHEN source_task_id<>'' THEN ?
			ELSE ? END
		WHERE scope='global' AND project_id='' AND is_index=0 AND parent_id=?
		  AND id NOT IN (?,?,?,?)`,
		GlobalTechnicalLessonsKnowledgeID, GlobalBehaviorLessonsKnowledgeID, GlobalGeneralKnowledgeID, root.ID,
		GlobalGeneralKnowledgeID, GlobalAgentLessonsKnowledgeID, GlobalTechnicalLessonsKnowledgeID, GlobalBehaviorLessonsKnowledgeID,
	); err != nil {
		return err
	}
	for _, item := range globalKnowledgeCategories(root.ID, now) {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO knowledge_entries(`+knowledgeColumns+`)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			item.ID, item.Scope, item.ProjectID, item.ParentID, item.Slug, item.SortOrder, item.IsIndex,
			item.Type, item.Title, item.Body, item.Status, item.BranchScope, item.ProductLineID, item.EvidenceJSON,
			item.Confidence, item.Revision, item.ContentHash, item.VerifiedCommit, item.HelpedCount, item.StaleCount,
			item.FeedbackState, item.SourceTaskID, item.SourceTurnID, timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.LastVerifiedAt),
		); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,entry_id,status,source_task_id,proposed_json FROM knowledge_proposals`)
	if err != nil {
		return err
	}
	type pendingProposal struct{ id, entryID, status, sourceTaskID, encoded string }
	pending := []pendingProposal{}
	for rows.Next() {
		var item pendingProposal
		if err := rows.Scan(&item.id, &item.entryID, &item.status, &item.sourceTaskID, &item.encoded); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range pending {
		var payload knowledgeProposalPayload
		if json.Unmarshal([]byte(item.encoded), &payload) != nil || payload.Proposed.Scope != "global" || (payload.Proposed.ParentID != "" && payload.Proposed.ParentID != root.ID) {
			continue
		}
		parentID := GlobalGeneralKnowledgeID
		if payload.Proposed.Type == "diagnostic" {
			parentID = GlobalTechnicalLessonsKnowledgeID
		} else if payload.Proposed.SourceTaskID != "" || item.sourceTaskID != "" {
			parentID = GlobalBehaviorLessonsKnowledgeID
		}
		if item.status == string(domain.KnowledgeProposalPending) {
			payload.Proposed.ParentID = parentID
			encoded, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_proposals SET proposed_json=? WHERE id=?`, string(encoded), item.id); err != nil {
				return err
			}
		} else if parentID != GlobalGeneralKnowledgeID {
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_entries SET parent_id=? WHERE id=? AND scope='global' AND parent_id IN (?,?)`, parentID, item.entryID, root.ID, GlobalGeneralKnowledgeID); err != nil {
				return err
			}
		}
	}
	legacyBodies := []string{
		"这里汇总跨项目共享的知识文档。",
		"这是 AHA2 全局知识页面，保存跨项目复用的常用知识，以及 Agent 工作过程中沉淀的经验和教训。请根据下面的标题与摘要按需打开文档。",
	}
	for _, body := range legacyBodies {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_entries SET body=?,content_hash=?,revision=revision+1,updated_at=? WHERE id=? AND body=?`,
			globalKnowledgeRootBody,
			fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(root.Title)+"\n"+globalKnowledgeRootBody))),
			timeString(now), root.ID, body,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	if item.Scope == "global" {
		var categoryID string
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM knowledge_entries WHERE id=? AND scope='global'`, GlobalGeneralKnowledgeID).Scan(&categoryID); err == nil {
			item.ParentID = categoryID
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
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
	if item.IsIndex || IsManagedGlobalKnowledgeCategory(item.ID) {
		return ErrKnowledgeRootManaged
	}
	if item.Revision < 1 {
		item.Revision = 1
	}
	item.Revision = s.sharedNumericVersion(ctx, "knowledge", item.ID, item.Revision)
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
	items, err := s.listKnowledge(ctx, "project", "", "", false, statuses)
	if err != nil {
		return nil, err
	}
	result := make([]domain.KnowledgeEntry, 0, len(items))
	for _, item := range items {
		if item.ProjectID != projectID && item.BoundProjectID != projectID {
			continue
		}
		if item.ProductLineID != "" && item.ProductLineID != productLineID {
			continue
		}
		result = append(result, item)
	}
	return namespaceBoundKnowledge(result, projectID), nil
}

func (s *Store) ListProjectKnowledge(ctx context.Context, projectID string, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	items, err := s.listKnowledge(ctx, "project", "", "", false, statuses)
	if err != nil {
		return nil, err
	}
	result := make([]domain.KnowledgeEntry, 0, len(items))
	for _, item := range items {
		if item.ProjectID == projectID || item.BoundProjectID == projectID && !item.IsIndex {
			result = append(result, item)
		}
	}
	return result, nil
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
	for index := range result {
		result[index].BoundProjectID = bindings[result[index].ProjectID]
	}
	return result, nil
}

func namespaceBoundKnowledge(items []domain.KnowledgeEntry, projectID string) []domain.KnowledgeEntry {
	libraryRoots := map[string]string{}
	for _, item := range items {
		if item.BoundProjectID == projectID && item.IsIndex {
			libraryRoots[item.ProjectID] = item.ID
		}
	}
	result := make([]domain.KnowledgeEntry, 0, len(items))
	for _, item := range items {
		if item.BoundProjectID == projectID && item.IsIndex {
			continue
		}
		if item.BoundProjectID == projectID && item.ParentID == libraryRoots[item.ProjectID] {
			prefix := strings.TrimPrefix(item.ProjectID, "project_")
			if len(prefix) > 8 {
				prefix = prefix[:8]
			}
			item.ParentID = knowledgeRootID("project", projectID)
			if item.Slug != "" {
				item.Slug = "library-" + prefix + "-" + item.Slug
			}
		}
		result = append(result, item)
	}
	return result
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
	if IsManagedGlobalKnowledgeCategory(existing.ID) {
		return ErrKnowledgeRootManaged
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
	if item.IsIndex || IsManagedGlobalKnowledgeCategory(item.ID) {
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
	_, err = s.deleteSharedObject(ctx, "knowledge", id, strconv.Itoa(item.Revision), time.Now().UTC())
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
	item.HelpedCount = 0
	item.StaleCount = 0
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
	if IsManagedGlobalKnowledgeCategory(item.ID) {
		return domain.KnowledgeEntry{}, ErrKnowledgeRootManaged
	}
	switch kind {
	case "helped":
		if item.Status != domain.KnowledgeVerified || item.FeedbackState == "stale" || item.FeedbackState == "wrong" {
			return domain.KnowledgeEntry{}, ErrKnowledgeFeedbackState
		}
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
