package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const mailBindingColumns = `id,scope_type,scope_id,account_id,default_template_id,
	approval_mode,allowed_recipients_json,auto_send_enabled,enabled,created_by,
	created_at,updated_at`

func scanMailBinding(scanner interface{ Scan(...any) error }) (domain.MailBinding, error) {
	var item domain.MailBinding
	var defaultTemplate sql.NullString
	var allowedRecipients, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.ScopeType, &item.ScopeID, &item.AccountID, &defaultTemplate,
		&item.ApprovalMode, &allowedRecipients, &item.AutoSendEnabled, &item.Enabled,
		&item.CreatedBy, &createdAt, &updatedAt,
	)
	item.DefaultTemplateID = defaultTemplate.String
	item.AllowedRecipients = decodeJSON(allowedRecipients, []string{})
	item.CreatedAt = parseMailTime(createdAt)
	item.UpdatedAt = parseMailTime(updatedAt)
	return item, err
}

func normalizeMailBinding(item domain.MailBinding, create bool) (domain.MailBinding, error) {
	item.ScopeID = strings.TrimSpace(item.ScopeID)
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.DefaultTemplateID = strings.TrimSpace(item.DefaultTemplateID)
	item.CreatedBy = strings.TrimSpace(item.CreatedBy)
	if !item.ScopeType.Valid() {
		return item, fmt.Errorf("invalid mail binding scope type %q", item.ScopeType)
	}
	if item.ScopeID == "" {
		return item, fmt.Errorf("mail binding scope_id is required")
	}
	if item.AccountID == "" {
		return item, fmt.Errorf("mail binding account_id is required")
	}
	if create && item.ID == "" {
		item.ID = domain.NewID("mail_binding")
	}
	if !create && item.ID == "" {
		return item, fmt.Errorf("mail binding id is required")
	}
	if item.ApprovalMode == "" {
		item.ApprovalMode = domain.MailApprovalAlways
	}
	if item.ApprovalMode != domain.MailApprovalAlways && item.ApprovalMode != domain.MailApprovalAllowlistAuto {
		return item, fmt.Errorf("invalid mail approval mode %q", item.ApprovalMode)
	}
	if item.AllowedRecipients == nil {
		item.AllowedRecipients = []string{}
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item, nil
}

func (s *Store) CreateMailBinding(ctx context.Context, item domain.MailBinding) (domain.MailBinding, error) {
	item, err := normalizeMailBinding(item, true)
	if err != nil {
		return domain.MailBinding{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO mail_bindings(
			id,scope_type,scope_id,account_id,default_template_id,approval_mode,
			allowed_recipients_json,auto_send_enabled,enabled,created_by,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.ScopeType, item.ScopeID, item.AccountID,
		nullIfEmpty(item.DefaultTemplateID), item.ApprovalMode,
		encodeJSON(item.AllowedRecipients), boolInt(item.AutoSendEnabled),
		boolInt(item.Enabled), item.CreatedBy, mailTimeString(item.CreatedAt),
		mailTimeString(item.UpdatedAt)); err != nil {
		return domain.MailBinding{}, err
	}
	return item, nil
}

func (s *Store) UpdateMailBinding(ctx context.Context, item domain.MailBinding) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("mail binding id is required")
	}
	normalized, err := normalizeMailBinding(item, false)
	if err != nil {
		return err
	}
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_bindings SET account_id=?,default_template_id=?,approval_mode=?,
			allowed_recipients_json=?,auto_send_enabled=?,enabled=?,updated_at=?
		WHERE id=?`,
		normalized.AccountID, nullIfEmpty(normalized.DefaultTemplateID),
		normalized.ApprovalMode, encodeJSON(normalized.AllowedRecipients),
		boolInt(normalized.AutoSendEnabled), boolInt(normalized.Enabled),
		mailTimeString(normalized.UpdatedAt), normalized.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) MailBinding(ctx context.Context, id string) (domain.MailBinding, error) {
	return scanMailBinding(s.db.QueryRowContext(ctx,
		`SELECT `+mailBindingColumns+` FROM mail_bindings WHERE id=?`, id))
}

func (s *Store) MailBindings(ctx context.Context, scopeType domain.MailBindingScopeType, scopeID string, enabledOnly bool, limit int) ([]domain.MailBinding, error) {
	if !scopeType.Valid() {
		return nil, fmt.Errorf("invalid mail binding scope type %q", scopeType)
	}
	query := `SELECT ` + mailBindingColumns + ` FROM mail_bindings WHERE scope_type=? AND scope_id=?`
	args := []any{scopeType, strings.TrimSpace(scopeID)}
	if enabledOnly {
		query += ` AND enabled=1`
	}
	query += ` ORDER BY created_at,id LIMIT ?`
	args = append(args, mailListLimit(limit))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailBinding, 0)
	for rows.Next() {
		item, err := scanMailBinding(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// TaskMailBindings returns the enabled bindings visible to a Task: its own,
// its Workspace's and its Project's, in Task -> Workspace -> Project order.
// Empty scope IDs are skipped so an empty Task row cannot match stray data.
func (s *Store) TaskMailBindings(ctx context.Context, taskID, workspaceID, projectID string, limit int) ([]domain.MailBinding, error) {
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 6)
	for _, scoped := range []struct {
		scopeType domain.MailBindingScopeType
		scopeID   string
	}{
		{domain.MailBindingScopeTask, strings.TrimSpace(taskID)},
		{domain.MailBindingScopeWorkspace, strings.TrimSpace(workspaceID)},
		{domain.MailBindingScopeProject, strings.TrimSpace(projectID)},
	} {
		if scoped.scopeID == "" {
			continue
		}
		conditions = append(conditions, `(scope_type=? AND scope_id=?)`)
		args = append(args, scoped.scopeType, scoped.scopeID)
	}
	if len(conditions) == 0 {
		return []domain.MailBinding{}, nil
	}
	query := `SELECT ` + mailBindingColumns + ` FROM mail_bindings
		WHERE enabled=1 AND (` + strings.Join(conditions, ` OR `) + `)
		ORDER BY CASE scope_type WHEN 'task' THEN 0 WHEN 'workspace' THEN 1 ELSE 2 END,
		         created_at,id
		LIMIT ?`
	args = append(args, mailListLimit(limit))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailBinding, 0)
	for rows.Next() {
		item, err := scanMailBinding(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeleteMailBinding(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM mail_bindings WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteMailBindingsForScope is the application-level cascade for a deleted
// Project, Workspace or Task. Mail messages keep their snapshot and have their
// binding_id cleared by the foreign key.
func (s *Store) DeleteMailBindingsForScope(ctx context.Context, scopeType domain.MailBindingScopeType, scopeID string) (int64, error) {
	if !scopeType.Valid() {
		return 0, fmt.Errorf("invalid mail binding scope type %q", scopeType)
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM mail_bindings WHERE scope_type=? AND scope_id=?`,
		scopeType, strings.TrimSpace(scopeID))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
