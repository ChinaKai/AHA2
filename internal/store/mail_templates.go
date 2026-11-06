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

const mailTemplateColumns = `id,scope,project_id,name,description,mode,subject_template,
	body_text_template,body_html_template,default_to_json,default_cc_json,
	required_variables_json,current_version,status,created_by,created_at,updated_at`

func scanMailTemplate(scanner interface{ Scan(...any) error }) (domain.MailTemplate, error) {
	var item domain.MailTemplate
	var defaultTo, defaultCC, requiredVariables, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.Scope, &item.ProjectID, &item.Name, &item.Description, &item.Mode,
		&item.SubjectTemplate, &item.BodyTextTemplate, &item.BodyHTMLTemplate,
		&defaultTo, &defaultCC, &requiredVariables, &item.CurrentVersion,
		&item.Status, &item.CreatedBy, &createdAt, &updatedAt,
	)
	item.DefaultTo = decodeJSON(defaultTo, []string{})
	item.DefaultCC = decodeJSON(defaultCC, []string{})
	item.RequiredVariables = decodeJSON(requiredVariables, []string{})
	item.CreatedAt = parseMailTime(createdAt)
	item.UpdatedAt = parseMailTime(updatedAt)
	return item, err
}

func normalizeMailTemplate(item domain.MailTemplate, create bool) (domain.MailTemplate, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		return item, fmt.Errorf("mail template name is required")
	}
	if !item.Scope.Valid() {
		return item, fmt.Errorf("invalid mail template scope %q", item.Scope)
	}
	item.ProjectID = strings.TrimSpace(item.ProjectID)
	if item.Scope == domain.MailTemplateScopeGlobal {
		item.ProjectID = ""
	} else if item.ProjectID == "" {
		return item, fmt.Errorf("project mail template requires project_id")
	}
	if create {
		if item.ID == "" {
			item.ID = domain.NewID("mail_template")
		}
		if item.CurrentVersion <= 0 {
			item.CurrentVersion = 1
		}
		if item.Status == "" {
			item.Status = domain.MailTemplateStatusActive
		}
	} else if item.ID == "" {
		return item, fmt.Errorf("mail template id is required")
	}
	// New templates default to guide; existing templates without an explicit
	// mode are legacy render templates. Mode is part of the immutable version
	// snapshot, so an edit can intentionally switch modes without rewriting
	// history.
	if item.Mode == "" {
		if create {
			item.Mode = domain.MailTemplateModeGuide
		} else {
			item.Mode = domain.MailTemplateModeRender
		}
	}
	if !item.Mode.Valid() {
		return item, fmt.Errorf("invalid mail template mode %q", item.Mode)
	}
	if item.DefaultTo == nil {
		item.DefaultTo = []string{}
	}
	if item.DefaultCC == nil {
		item.DefaultCC = []string{}
	}
	if item.RequiredVariables == nil {
		item.RequiredVariables = []string{}
	}
	if item.Mode == domain.MailTemplateModeGuide {
		// Guide templates are reference samples; caller variables are neither
		// required nor substituted.
		item.RequiredVariables = []string{}
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

// CreateMailTemplate writes the template and its immutable version 1 snapshot
// in the same transaction.
func (s *Store) CreateMailTemplate(ctx context.Context, item domain.MailTemplate) (domain.MailTemplate, error) {
	item, err := normalizeMailTemplate(item, true)
	if err != nil {
		return domain.MailTemplate{}, err
	}
	if item.CurrentVersion != 1 {
		return domain.MailTemplate{}, fmt.Errorf("new mail template must start at version 1")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailTemplate{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mail_templates(
			id,scope,project_id,name,description,mode,subject_template,body_text_template,
			body_html_template,default_to_json,default_cc_json,required_variables_json,
			current_version,status,created_by,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Scope, item.ProjectID, item.Name, item.Description, item.Mode,
		item.SubjectTemplate, item.BodyTextTemplate, item.BodyHTMLTemplate,
		encodeJSON(item.DefaultTo), encodeJSON(item.DefaultCC),
		encodeJSON(item.RequiredVariables), item.CurrentVersion, item.Status,
		item.CreatedBy, mailTimeString(item.CreatedAt), mailTimeString(item.UpdatedAt),
	); err != nil {
		return domain.MailTemplate{}, err
	}
	if err := insertMailTemplateVersionTx(ctx, tx, domain.MailTemplateVersion{
		TemplateID:        item.ID,
		Version:           item.CurrentVersion,
		Mode:              item.Mode,
		SubjectTemplate:   item.SubjectTemplate,
		BodyTextTemplate:  item.BodyTextTemplate,
		BodyHTMLTemplate:  item.BodyHTMLTemplate,
		RequiredVariables: item.RequiredVariables,
		CreatedBy:         item.CreatedBy,
		CreatedAt:         item.CreatedAt,
	}); err != nil {
		return domain.MailTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MailTemplate{}, err
	}
	return item, nil
}

func insertMailTemplateVersionTx(ctx context.Context, tx *sql.Tx, version domain.MailTemplateVersion) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO mail_template_versions(
			template_id,version,mode,subject_template,body_text_template,body_html_template,
			required_variables_json,created_by,created_at
		) VALUES(?,?,?,?,?,?,?,?,?)`,
		version.TemplateID, version.Version, version.Mode, version.SubjectTemplate,
		version.BodyTextTemplate, version.BodyHTMLTemplate,
		encodeJSON(version.RequiredVariables), version.CreatedBy,
		mailTimeString(version.CreatedAt))
	return err
}

// UpdateMailTemplate writes a new immutable version before advancing
// current_version. expectedVersion is the version the editor read; a mismatch
// returns ErrMailTemplateVersionConflict instead of overwriting another edit.
func (s *Store) UpdateMailTemplate(ctx context.Context, item domain.MailTemplate, expectedVersion int) (domain.MailTemplate, error) {
	if expectedVersion < 1 {
		return domain.MailTemplate{}, fmt.Errorf("expected mail template version is required")
	}
	item, err := normalizeMailTemplate(item, false)
	if err != nil {
		return domain.MailTemplate{}, err
	}
	// Read the previous timestamp outside the write transaction so the first
	// statement inside it remains the conditional UPDATE. Starting a deferred
	// transaction with a SELECT makes concurrent writers fail with SQLITE_BUSY
	// when they try to upgrade their read lock. The version predicate below is
	// still the sole concurrency decision: a writer that commits between this
	// read and the UPDATE makes the UPDATE affect zero rows.
	var previousUpdatedAt string
	if err := s.db.QueryRowContext(ctx,
		`SELECT updated_at FROM mail_templates WHERE id=?`, item.ID).Scan(&previousUpdatedAt); err != nil {
		return domain.MailTemplate{}, err
	}
	now := nextMailTimestamp(time.Now().UTC(), parseMailTime(previousUpdatedAt))
	item.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailTemplate{}, err
	}
	defer tx.Rollback()
	// A single conditional UPDATE is the sole decision point: it both checks the
	// version the editor read and advances the pointer while holding the write
	// lock. A competing editor sees 0 rows and never reaches the version insert.
	result, err := tx.ExecContext(ctx, `
		UPDATE mail_templates SET
			name=?,description=?,mode=?,subject_template=?,body_text_template=?,body_html_template=?,
			default_to_json=?,default_cc_json=?,required_variables_json=?,
			current_version=current_version+1,updated_at=?
		WHERE id=? AND current_version=? AND status='active'`,
		item.Name, item.Description, item.Mode, item.SubjectTemplate, item.BodyTextTemplate,
		item.BodyHTMLTemplate, encodeJSON(item.DefaultTo), encodeJSON(item.DefaultCC),
		encodeJSON(item.RequiredVariables), mailTimeString(now),
		item.ID, expectedVersion)
	if err != nil {
		return domain.MailTemplate{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		var status domain.MailTemplateStatus
		var currentVersion int
		if err := tx.QueryRowContext(ctx,
			`SELECT status,current_version FROM mail_templates WHERE id=?`, item.ID).
			Scan(&status, &currentVersion); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.MailTemplate{}, err
			}
			return domain.MailTemplate{}, err
		}
		if status == domain.MailTemplateStatusArchived {
			return domain.MailTemplate{}, ErrMailTemplateArchived
		}
		return domain.MailTemplate{}, ErrMailTemplateVersionConflict
	}
	var nextVersion int
	if err := tx.QueryRowContext(ctx,
		`SELECT current_version FROM mail_templates WHERE id=?`, item.ID).Scan(&nextVersion); err != nil {
		return domain.MailTemplate{}, err
	}
	if err := insertMailTemplateVersionTx(ctx, tx, domain.MailTemplateVersion{
		TemplateID:        item.ID,
		Version:           nextVersion,
		Mode:              item.Mode,
		SubjectTemplate:   item.SubjectTemplate,
		BodyTextTemplate:  item.BodyTextTemplate,
		BodyHTMLTemplate:  item.BodyHTMLTemplate,
		RequiredVariables: item.RequiredVariables,
		CreatedBy:         item.CreatedBy,
		CreatedAt:         now,
	}); err != nil {
		if isUniqueConstraintError(err) {
			return domain.MailTemplate{}, ErrMailTemplateVersionConflict
		}
		return domain.MailTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MailTemplate{}, err
	}
	return s.MailTemplate(ctx, item.ID)
}

func (s *Store) MailTemplate(ctx context.Context, id string) (domain.MailTemplate, error) {
	return scanMailTemplate(s.db.QueryRowContext(ctx,
		`SELECT `+mailTemplateColumns+` FROM mail_templates WHERE id=?`, id))
}

func (s *Store) MailTemplates(ctx context.Context, scope domain.MailTemplateScope, projectID string, limit int) ([]domain.MailTemplate, error) {
	if scope != "" && !scope.Valid() {
		return nil, fmt.Errorf("invalid mail template scope %q", scope)
	}
	query := `SELECT ` + mailTemplateColumns + ` FROM mail_templates`
	where := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if scope != "" {
		where = append(where, `scope=?`)
		args = append(args, scope)
	}
	if projectID = strings.TrimSpace(projectID); projectID != "" {
		where = append(where, `project_id=?`)
		args = append(args, projectID)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY updated_at DESC,id DESC LIMIT ?`
	args = append(args, mailListLimit(limit))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailTemplate, 0)
	for rows.Next() {
		item, err := scanMailTemplate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ArchiveMailTemplate keeps template history readable. Archived templates stay
// resolvable for historical mail snapshots.
func (s *Store) ArchiveMailTemplate(ctx context.Context, id string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE mail_templates SET status='archived',updated_at=? WHERE id=? AND status='active'`,
		mailTimeString(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// PurgeMailTemplate physically removes a template and all of its immutable
// versions. Both reference checks and the deletes run in one transaction:
// SQLite's ON DELETE SET NULL would otherwise silently detach a binding or
// message instead of telling the caller why the template is still needed.
// A template referenced by any mail message or by a Task/Workspace/Project
// binding returns ErrMailTemplateInUse without changing any row.
func (s *Store) PurgeMailTemplate(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// This first write both takes the transaction's write lock and removes the
	// version rows; it also means a concurrent binder/editor cannot slip a
	// reference in between the count checks and the template delete. Any
	// in-use result rolls the versions back.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM mail_template_versions WHERE template_id=?`, id); err != nil {
		return err
	}
	var messageCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_messages WHERE template_id=?`, id).Scan(&messageCount); err != nil {
		return err
	}
	if messageCount > 0 {
		return ErrMailTemplateInUse
	}
	var bindingCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_bindings WHERE default_template_id=?`, id).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return ErrMailTemplateInUse
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM mail_templates WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

const mailTemplateVersionColumns = `template_id,version,mode,subject_template,body_text_template,
	body_html_template,required_variables_json,created_by,created_at`

func scanMailTemplateVersion(scanner interface{ Scan(...any) error }) (domain.MailTemplateVersion, error) {
	var item domain.MailTemplateVersion
	var requiredVariables, createdAt string
	err := scanner.Scan(&item.TemplateID, &item.Version, &item.Mode, &item.SubjectTemplate,
		&item.BodyTextTemplate, &item.BodyHTMLTemplate, &requiredVariables,
		&item.CreatedBy, &createdAt)
	item.RequiredVariables = decodeJSON(requiredVariables, []string{})
	item.CreatedAt = parseMailTime(createdAt)
	return item, err
}

func (s *Store) MailTemplateVersions(ctx context.Context, templateID string, limit int) ([]domain.MailTemplateVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mailTemplateVersionColumns+`
		FROM mail_template_versions WHERE template_id=?
		ORDER BY version DESC LIMIT ?`, templateID, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailTemplateVersion, 0)
	for rows.Next() {
		item, err := scanMailTemplateVersion(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MailTemplateVersion(ctx context.Context, templateID string, version int) (domain.MailTemplateVersion, error) {
	return scanMailTemplateVersion(s.db.QueryRowContext(ctx, `SELECT `+mailTemplateVersionColumns+`
		FROM mail_template_versions WHERE template_id=? AND version=?`, templateID, version))
}
