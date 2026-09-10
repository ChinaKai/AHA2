package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const maxAttachmentNameRunes = 180

func cleanAttachmentName(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, value)
	value = strings.TrimRight(value, ". ")
	if value == "" || value == "." || value == ".." {
		value = "attachment"
	}
	runes := []rune(value)
	if len(runes) > maxAttachmentNameRunes {
		value = string(runes[:maxAttachmentNameRunes])
	}
	return value
}

func (s *Store) CreateAttachment(ctx context.Context, taskID, name, mediaType string, content []byte, now time.Time) (domain.Attachment, error) {
	return s.createAttachment(ctx, domain.NewID("attachment"), taskID, name, mediaType, content, now)
}

func (s *Store) CreateChannelAttachment(ctx context.Context, commandID, taskID, name, mediaType string, content []byte, now time.Time) (domain.Attachment, error) {
	digest := sha256.Sum256([]byte(commandID))
	return s.createAttachment(ctx, "attachment_"+hex.EncodeToString(digest[:]), taskID, name, mediaType, content, now)
}

func (s *Store) createAttachment(ctx context.Context, id, taskID, name, mediaType string, content []byte, now time.Time) (domain.Attachment, error) {
	if len(content) == 0 {
		return domain.Attachment{}, fmt.Errorf("attachment is empty")
	}
	if _, err := s.Task(ctx, taskID); err != nil {
		return domain.Attachment{}, err
	}
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	directory := filepath.Join(s.dataDir, "attachments", "blobs", hash[:2])
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return domain.Attachment{}, fmt.Errorf("create attachment directory: %w", err)
	}
	target := filepath.Join(directory, hash)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		temporary, createErr := os.CreateTemp(directory, ".upload-*")
		if createErr != nil {
			return domain.Attachment{}, fmt.Errorf("create attachment file: %w", createErr)
		}
		temporaryName := temporary.Name()
		defer os.Remove(temporaryName)
		if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
			temporary.Close()
			return domain.Attachment{}, chmodErr
		}
		if _, writeErr := temporary.Write(content); writeErr != nil {
			temporary.Close()
			return domain.Attachment{}, fmt.Errorf("write attachment: %w", writeErr)
		}
		if closeErr := temporary.Close(); closeErr != nil {
			return domain.Attachment{}, closeErr
		}
		if renameErr := os.Rename(temporaryName, target); renameErr != nil {
			if _, statErr := os.Stat(target); statErr != nil {
				return domain.Attachment{}, fmt.Errorf("store attachment: %w", renameErr)
			}
		}
	}
	item := domain.Attachment{
		ID: id, TaskID: taskID, Name: cleanAttachmentName(name),
		MediaType: strings.TrimSpace(mediaType), Size: int64(len(content)), SHA256: hash, CreatedAt: now.UTC(),
	}
	if item.MediaType == "" {
		item.MediaType = "application/octet-stream"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		item.ID, item.TaskID, "", item.Name, item.MediaType, item.Size, item.SHA256, timeString(item.CreatedAt))
	if err != nil {
		return domain.Attachment{}, err
	}
	existing, err := s.Attachment(ctx, taskID, id)
	if err != nil {
		return domain.Attachment{}, err
	}
	if existing.SHA256 != item.SHA256 || existing.Name != item.Name || existing.MediaType != item.MediaType {
		return domain.Attachment{}, fmt.Errorf("attachment content conflict")
	}
	return existing, nil
}

func (s *Store) TurnOutputAttachments(ctx context.Context, taskID, turnID string) ([]domain.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.task_id,a.message_id,a.name,a.media_type,a.size_bytes,a.sha256,a.created_at
		FROM attachments a JOIN conversation_items c ON a.message_id=c.id AND a.task_id=c.task_id
		WHERE a.task_id=? AND c.turn_id=? AND c.agent_id='main' ORDER BY c.sequence,a.created_at,a.id`, taskID, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Attachment
	for rows.Next() {
		item, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanAttachment(scanner interface{ Scan(...any) error }) (domain.Attachment, error) {
	var item domain.Attachment
	var created string
	err := scanner.Scan(&item.ID, &item.TaskID, &item.MessageID, &item.Name, &item.MediaType, &item.Size, &item.SHA256, &created)
	item.CreatedAt = parseTime(created)
	return item, err
}

func (s *Store) Attachment(ctx context.Context, taskID, id string) (domain.Attachment, error) {
	return scanAttachment(s.db.QueryRowContext(ctx, `SELECT id,task_id,message_id,name,media_type,size_bytes,sha256,created_at FROM attachments WHERE id=? AND task_id=?`, id, taskID))
}

func (s *Store) ListTaskAttachments(ctx context.Context, taskID string) ([]domain.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,message_id,name,media_type,size_bytes,sha256,created_at FROM attachments WHERE task_id=? AND message_id<>'' ORDER BY created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Attachment{}
	for rows.Next() {
		item, scanErr := scanAttachment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) AttachmentContent(item domain.Attachment) ([]byte, error) {
	if len(item.SHA256) != 64 || strings.ContainsAny(item.SHA256, `/\\`) {
		return nil, fmt.Errorf("invalid attachment hash")
	}
	return os.ReadFile(filepath.Join(s.dataDir, "attachments", "blobs", item.SHA256[:2], item.SHA256))
}

func (s *Store) ValidateDraftAttachments(ctx context.Context, taskID string, ids []string) error {
	if len(ids) > 8 {
		return fmt.Errorf("at most 8 attachments are allowed")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return fmt.Errorf("invalid attachment selection")
		}
		seen[id] = true
		item, err := s.Attachment(ctx, taskID, id)
		if err != nil {
			return fmt.Errorf("attachment %s: %w", id, err)
		}
		if item.MessageID != "" {
			return fmt.Errorf("attachment is already sent")
		}
	}
	return nil
}

func bindAttachmentsTx(ctx context.Context, tx *sql.Tx, taskID, messageID string, ids []string) ([]domain.Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 8 {
		return nil, fmt.Errorf("at most 8 attachments are allowed")
	}
	seen := map[string]bool{}
	result := make([]domain.Attachment, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, fmt.Errorf("invalid attachment selection")
		}
		seen[id] = true
		item, err := scanAttachment(tx.QueryRowContext(ctx, `SELECT id,task_id,message_id,name,media_type,size_bytes,sha256,created_at FROM attachments WHERE id=? AND task_id=?`, id, taskID))
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %w", id, err)
		}
		if item.MessageID != "" {
			return nil, fmt.Errorf("attachment is already sent")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attachments SET message_id=? WHERE id=? AND task_id=? AND message_id=''`, messageID, id, taskID); err != nil {
			return nil, err
		}
		item.MessageID = messageID
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) DeleteDraftAttachment(ctx context.Context, taskID, id string) error {
	item, err := s.Attachment(ctx, taskID, id)
	if err != nil {
		return err
	}
	if item.MessageID != "" {
		return fmt.Errorf("sent attachment cannot be deleted")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM attachments WHERE id=? AND task_id=? AND message_id=''`, id, taskID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	var references int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256=?`, item.SHA256).Scan(&references)
	if references == 0 && len(item.SHA256) == 64 {
		_ = os.Remove(filepath.Join(s.dataDir, "attachments", "blobs", item.SHA256[:2], item.SHA256))
	}
	return nil
}
