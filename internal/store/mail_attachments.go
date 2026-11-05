package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// mailAttachmentColumns omits link_token_ref: only the SMTP worker may resolve
// the download token. Reads return LinkTokenRef empty.
const mailAttachmentColumns = `id,message_id,position,source_type,source_id,
	file_name,media_type,size_bytes,sha256,delivery_mode,disposition,
	link_token_hash,link_expires_at,link_max_downloads,link_download_count,
	link_revoked_at,created_at`

func scanMailAttachment(scanner interface{ Scan(...any) error }) (domain.MailAttachment, error) {
	var item domain.MailAttachment
	var linkExpiresAt, linkRevokedAt, createdAt string
	err := scanner.Scan(
		&item.ID, &item.MessageID, &item.Position, &item.SourceType, &item.SourceID,
		&item.FileName, &item.MediaType, &item.SizeBytes, &item.SHA256,
		&item.DeliveryMode, &item.Disposition, &item.LinkTokenHash,
		&linkExpiresAt, &item.LinkMaxDownloads, &item.LinkDownloadCount,
		&linkRevokedAt, &createdAt,
	)
	item.LinkExpiresAt = parseMailTime(linkExpiresAt)
	item.LinkRevokedAt = parseMailTime(linkRevokedAt)
	item.CreatedAt = parseMailTime(createdAt)
	return item, err
}

func normalizeMailAttachment(item domain.MailAttachment) (domain.MailAttachment, error) {
	item.MessageID = strings.TrimSpace(item.MessageID)
	item.SourceID = strings.TrimSpace(item.SourceID)
	item.FileName = strings.TrimSpace(item.FileName)
	item.MediaType = strings.TrimSpace(item.MediaType)
	item.SHA256 = strings.ToLower(strings.TrimSpace(item.SHA256))
	item.LinkTokenRef = strings.TrimSpace(item.LinkTokenRef)
	item.LinkTokenHash = strings.TrimSpace(item.LinkTokenHash)
	if item.MessageID == "" {
		return item, fmt.Errorf("mail attachment message_id is required")
	}
	if !item.SourceType.Valid() {
		return item, fmt.Errorf("invalid mail attachment source_type %q", item.SourceType)
	}
	if item.SourceID == "" {
		return item, fmt.Errorf("mail attachment source_id is required")
	}
	if item.FileName == "" {
		return item, fmt.Errorf("mail attachment file_name is required")
	}
	if item.SizeBytes < 0 {
		return item, fmt.Errorf("mail attachment size_bytes must not be negative")
	}
	if item.SHA256 == "" {
		return item, fmt.Errorf("mail attachment sha256 is required")
	}
	if !item.DeliveryMode.Valid() {
		return item, fmt.Errorf("invalid mail attachment delivery_mode %q", item.DeliveryMode)
	}
	hasLinkFields := item.LinkTokenRef != "" || item.LinkTokenHash != "" ||
		!item.LinkExpiresAt.IsZero() || item.LinkMaxDownloads != 0 ||
		item.LinkDownloadCount != 0 || !item.LinkRevokedAt.IsZero()
	switch item.DeliveryMode {
	case domain.MailAttachmentDirect:
		if hasLinkFields {
			return item, fmt.Errorf("direct mail attachment must not carry link fields")
		}
	case domain.MailAttachmentLink:
		if item.LinkTokenRef == "" || item.LinkTokenHash == "" || item.LinkExpiresAt.IsZero() {
			return item, fmt.Errorf("link mail attachment requires token_ref, token_hash and expires_at")
		}
	}
	if item.Disposition == "" {
		item.Disposition = domain.MailAttachmentDispositionAttachment
	}
	// Phase one sends attachments only; inline content is reserved.
	if item.Disposition != domain.MailAttachmentDispositionAttachment {
		return item, fmt.Errorf("phase one only allows attachment disposition")
	}
	if item.MediaType == "" {
		item.MediaType = "application/octet-stream"
	}
	if item.Position < 0 {
		item.Position = 0
	}
	if item.ID == "" {
		item.ID = domain.NewID("mail_attachment")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return item, nil
}

func (s *Store) CreateMailAttachment(ctx context.Context, item domain.MailAttachment) (domain.MailAttachment, error) {
	return s.CreateMailAttachmentInTx(ctx, s.db, item)
}

func (s *Store) CreateMailAttachmentInTx(ctx context.Context, exec mailDBTX, item domain.MailAttachment) (domain.MailAttachment, error) {
	item, err := normalizeMailAttachment(item)
	if err != nil {
		return domain.MailAttachment{}, err
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO mail_attachments(
			id,message_id,position,source_type,source_id,file_name,media_type,
			size_bytes,sha256,delivery_mode,disposition,link_token_ref,link_token_hash,
			link_expires_at,link_max_downloads,link_download_count,link_revoked_at,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.MessageID, item.Position, item.SourceType, item.SourceID,
		item.FileName, item.MediaType, item.SizeBytes, item.SHA256,
		item.DeliveryMode, item.Disposition, item.LinkTokenRef, item.LinkTokenHash,
		mailTimeString(item.LinkExpiresAt), item.LinkMaxDownloads,
		item.LinkDownloadCount, mailTimeString(item.LinkRevokedAt),
		mailTimeString(item.CreatedAt)); err != nil {
		return domain.MailAttachment{}, err
	}
	item.LinkTokenRef = ""
	return item, nil
}

func (s *Store) MailAttachment(ctx context.Context, id string) (domain.MailAttachment, error) {
	return scanMailAttachment(s.db.QueryRowContext(ctx,
		`SELECT `+mailAttachmentColumns+` FROM mail_attachments WHERE id=?`, id))
}

func (s *Store) MailAttachments(ctx context.Context, messageID string, limit int) ([]domain.MailAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mailAttachmentColumns+`
		FROM mail_attachments WHERE message_id=?
		ORDER BY position,id LIMIT ?`, messageID, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailAttachment, 0)
	for rows.Next() {
		item, err := scanMailAttachment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// MailAttachmentLinkTokenRef returns the Secret Store key for a controlled
// download token. Only the SMTP worker and download-link management should use
// it; normal attachment reads hide it.
func (s *Store) MailAttachmentLinkTokenRef(ctx context.Context, id string) (string, error) {
	var ref string
	if err := s.db.QueryRowContext(ctx,
		`SELECT link_token_ref FROM mail_attachments WHERE id=?`, id).Scan(&ref); err != nil {
		return "", err
	}
	return ref, nil
}

// RevokeMailAttachmentLink implements the only mutation allowed on an already
// submitted message's link policy. Revocation does not change the approval
// digest.
func (s *Store) RevokeMailAttachmentLink(ctx context.Context, id string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE mail_attachments SET link_revoked_at=? WHERE id=?`,
		mailTimeString(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
