package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const buildArtifactColumns = `id,task_id,label,package_name,version,size_bytes,
	sha256,storage_type,attachment_id,external_url,registered_by,created_at`

func scanBuildArtifact(scanner interface{ Scan(...any) error }) (domain.BuildArtifact, error) {
	var item domain.BuildArtifact
	var attachmentID sql.NullString
	var createdAt string
	err := scanner.Scan(&item.ID, &item.TaskID, &item.Label, &item.PackageName,
		&item.Version, &item.SizeBytes, &item.SHA256, &item.StorageType,
		&attachmentID, &item.ExternalURL, &item.RegisteredBy, &createdAt)
	item.AttachmentID = attachmentID.String
	item.CreatedAt = parseMailTime(createdAt)
	return item, err
}

func normalizeBuildArtifact(item domain.BuildArtifact) (domain.BuildArtifact, error) {
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.Label = strings.TrimSpace(item.Label)
	item.PackageName = strings.TrimSpace(item.PackageName)
	item.Version = strings.TrimSpace(item.Version)
	item.SHA256 = strings.ToLower(strings.TrimSpace(item.SHA256))
	item.AttachmentID = strings.TrimSpace(item.AttachmentID)
	item.ExternalURL = strings.TrimSpace(item.ExternalURL)
	item.RegisteredBy = strings.TrimSpace(item.RegisteredBy)
	if item.TaskID == "" {
		return item, fmt.Errorf("build artifact task_id is required")
	}
	if item.Label == "" {
		return item, fmt.Errorf("build artifact label is required")
	}
	if item.SizeBytes < 0 {
		return item, fmt.Errorf("build artifact size_bytes must not be negative")
	}
	if item.SHA256 == "" {
		return item, fmt.Errorf("build artifact sha256 is required")
	}
	if item.StorageType == "" {
		item.StorageType = domain.BuildArtifactStorageAHAAttachment
	}
	switch item.StorageType {
	case domain.BuildArtifactStorageAHAAttachment:
		if item.AttachmentID == "" || item.ExternalURL != "" {
			return item, fmt.Errorf("aha_attachment build artifact requires attachment_id and no external_url")
		}
	case domain.BuildArtifactStorageExternalURL:
		if item.ExternalURL == "" || item.AttachmentID != "" {
			return item, fmt.Errorf("external_url build artifact requires external_url and no attachment_id")
		}
	default:
		return item, fmt.Errorf("invalid build artifact storage_type %q", item.StorageType)
	}
	if item.ID == "" {
		item.ID = domain.NewID("build_artifact")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return item, nil
}

func (s *Store) CreateBuildArtifact(ctx context.Context, item domain.BuildArtifact) (domain.BuildArtifact, error) {
	item, err := normalizeBuildArtifact(item)
	if err != nil {
		return domain.BuildArtifact{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO build_artifacts(
			id,task_id,label,package_name,version,size_bytes,sha256,storage_type,
			attachment_id,external_url,registered_by,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.Label, item.PackageName, item.Version,
		item.SizeBytes, item.SHA256, item.StorageType,
		nullIfEmpty(item.AttachmentID), item.ExternalURL, item.RegisteredBy,
		mailTimeString(item.CreatedAt)); err != nil {
		return domain.BuildArtifact{}, err
	}
	return item, nil
}

func (s *Store) BuildArtifact(ctx context.Context, id string) (domain.BuildArtifact, error) {
	return scanBuildArtifact(s.db.QueryRowContext(ctx,
		`SELECT `+buildArtifactColumns+` FROM build_artifacts WHERE id=?`, id))
}

func (s *Store) BuildArtifactsByTask(ctx context.Context, taskID string, limit int) ([]domain.BuildArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+buildArtifactColumns+`
		FROM build_artifacts WHERE task_id=?
		ORDER BY created_at DESC,id DESC LIMIT ?`, taskID, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.BuildArtifact, 0)
	for rows.Next() {
		item, err := scanBuildArtifact(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
