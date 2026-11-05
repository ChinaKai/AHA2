package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const mailMessageColumns = `id,task_id,project_id,workspace_id,turn_id,agent_id,
	account_id,binding_id,template_id,template_version,source,idempotency_key,
	from_address,from_name,to_json,cc_json,bcc_json,reply_to,subject,body_text,
	body_html,variables_json,content_fingerprint,content_revision,approval_digest,
	status,approved_by,approved_at,rejected_reason,submitted_at,created_at,updated_at`

func scanMailMessage(scanner interface{ Scan(...any) error }) (domain.MailMessage, error) {
	var item domain.MailMessage
	var bindingID, templateID sql.NullString
	var to, cc, bcc, variables string
	var approvedAt, submittedAt, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.TaskID, &item.ProjectID, &item.WorkspaceID, &item.TurnID,
		&item.AgentID, &item.AccountID, &bindingID, &templateID, &item.TemplateVersion,
		&item.Source, &item.IdempotencyKey, &item.FromAddress, &item.FromName,
		&to, &cc, &bcc, &item.ReplyTo, &item.Subject, &item.BodyText, &item.BodyHTML,
		&variables, &item.ContentFingerprint, &item.ContentRevision, &item.ApprovalDigest,
		&item.Status, &item.ApprovedBy, &approvedAt, &item.RejectedReason, &submittedAt,
		&createdAt, &updatedAt,
	)
	item.BindingID = bindingID.String
	item.TemplateID = templateID.String
	item.To = decodeJSON(to, []string{})
	item.CC = decodeJSON(cc, []string{})
	item.BCC = decodeJSON(bcc, []string{})
	item.Variables = decodeJSON(variables, map[string]string{})
	item.ApprovedAt = parseMailTime(approvedAt)
	item.SubmittedAt = parseMailTime(submittedAt)
	item.CreatedAt = parseMailTime(createdAt)
	item.UpdatedAt = parseMailTime(updatedAt)
	return item, err
}

func normalizeMailMessage(item domain.MailMessage, create bool) (domain.MailMessage, error) {
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.BindingID = strings.TrimSpace(item.BindingID)
	item.TemplateID = strings.TrimSpace(item.TemplateID)
	item.IdempotencyKey = strings.TrimSpace(item.IdempotencyKey)
	item.FromAddress = strings.TrimSpace(item.FromAddress)
	item.FromName = strings.TrimSpace(item.FromName)
	item.ReplyTo = strings.TrimSpace(item.ReplyTo)
	if item.TaskID == "" {
		return item, fmt.Errorf("mail message task_id is required")
	}
	if item.AccountID == "" {
		return item, fmt.Errorf("mail message account_id is required")
	}
	if item.IdempotencyKey == "" {
		return item, fmt.Errorf("mail message idempotency_key is required")
	}
	if item.FromAddress == "" {
		return item, fmt.Errorf("mail message from_address is required")
	}
	if item.Source != domain.MailMessageSourceAgent && item.Source != domain.MailMessageSourceOwner {
		return item, fmt.Errorf("invalid mail message source %q", item.Source)
	}
	if create && item.ID == "" {
		item.ID = domain.NewID("mail_message")
	}
	if !create && item.ID == "" {
		return item, fmt.Errorf("mail message id is required")
	}
	if item.Status == "" {
		item.Status = domain.MailMessageDraft
	}
	if !item.Status.Valid() {
		return item, fmt.Errorf("invalid mail message status %q", item.Status)
	}
	if item.ContentRevision <= 0 {
		item.ContentRevision = 1
	}
	if item.To == nil {
		item.To = []string{}
	}
	if item.CC == nil {
		item.CC = []string{}
	}
	if item.BCC == nil {
		item.BCC = []string{}
	}
	if item.Variables == nil {
		item.Variables = map[string]string{}
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

// CreateMailMessage inserts the immutable approval snapshot. Idempotency and
// duplicate-content decisions belong to the service layer, which can look the
// existing row up with MailMessageByTaskIdempotencyKey.
func (s *Store) CreateMailMessage(ctx context.Context, item domain.MailMessage) (domain.MailMessage, error) {
	item, err := normalizeMailMessage(item, true)
	if err != nil {
		return domain.MailMessage{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO mail_messages(
			id,task_id,project_id,workspace_id,turn_id,agent_id,account_id,binding_id,
			template_id,template_version,source,idempotency_key,from_address,from_name,
			to_json,cc_json,bcc_json,reply_to,subject,body_text,body_html,variables_json,
			content_fingerprint,content_revision,approval_digest,status,approved_by,
			approved_at,rejected_reason,submitted_at,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.ProjectID, item.WorkspaceID, item.TurnID,
		item.AgentID, item.AccountID, nullIfEmpty(item.BindingID),
		nullIfEmpty(item.TemplateID), item.TemplateVersion, item.Source,
		item.IdempotencyKey, item.FromAddress, item.FromName, encodeJSON(item.To),
		encodeJSON(item.CC), encodeJSON(item.BCC), item.ReplyTo, item.Subject,
		item.BodyText, item.BodyHTML, encodeJSON(item.Variables),
		item.ContentFingerprint, item.ContentRevision, item.ApprovalDigest, item.Status,
		item.ApprovedBy, mailTimeString(item.ApprovedAt), item.RejectedReason,
		mailTimeString(item.SubmittedAt), mailTimeString(item.CreatedAt),
		mailTimeString(item.UpdatedAt),
	); err != nil {
		return domain.MailMessage{}, err
	}
	return item, nil
}

func (s *Store) MailMessage(ctx context.Context, id string) (domain.MailMessage, error) {
	return scanMailMessage(s.db.QueryRowContext(ctx,
		`SELECT `+mailMessageColumns+` FROM mail_messages WHERE id=?`, id))
}

func (s *Store) MailMessageByTaskIdempotencyKey(ctx context.Context, taskID, key string) (domain.MailMessage, error) {
	return scanMailMessage(s.db.QueryRowContext(ctx, `SELECT `+mailMessageColumns+`
		FROM mail_messages WHERE task_id=? AND idempotency_key=?`,
		strings.TrimSpace(taskID), strings.TrimSpace(key)))
}

func (s *Store) MailMessagesByTask(ctx context.Context, taskID string, status domain.MailMessageStatus, limit int) ([]domain.MailMessage, error) {
	query := `SELECT ` + mailMessageColumns + ` FROM mail_messages WHERE task_id=?`
	args := []any{strings.TrimSpace(taskID)}
	if status != "" {
		if !status.Valid() {
			return nil, fmt.Errorf("invalid mail message status %q", status)
		}
		query += ` AND status=?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, mailListLimit(limit))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailMessage, 0)
	for rows.Next() {
		item, err := scanMailMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UpdateMailMessageStatus performs a conditional state transition. It reports
// false when the row is not in one of the expected states, which callers must
// treat as a lost race instead of overwriting the newer state.
func (s *Store) UpdateMailMessageStatus(ctx context.Context, id string, from []domain.MailMessageStatus, to domain.MailMessageStatus, now time.Time) (bool, error) {
	return s.UpdateMailMessageStatusInTx(ctx, s.db, id, from, to, now)
}

func (s *Store) UpdateMailMessageStatusInTx(ctx context.Context, exec mailDBTX, id string, from []domain.MailMessageStatus, to domain.MailMessageStatus, now time.Time) (bool, error) {
	if !to.Valid() {
		return false, fmt.Errorf("invalid mail message status %q", to)
	}
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("mail message id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := `UPDATE mail_messages SET status=?,updated_at=? WHERE id=?`
	args := []any{to, mailTimeString(now), id}
	if len(from) > 0 {
		placeholders := make([]string, 0, len(from))
		for _, state := range from {
			if !state.Valid() {
				return false, fmt.Errorf("invalid mail message status %q", state)
			}
			placeholders = append(placeholders, "?")
			args = append(args, state)
		}
		query += ` AND status IN (` + strings.Join(placeholders, ",") + `)`
	}
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}
