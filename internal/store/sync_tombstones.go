package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func scanSyncTombstone(scanner interface{ Scan(...any) error }) (domain.SyncTombstone, error) {
	var item domain.SyncTombstone
	var deletedAt string
	err := scanner.Scan(&item.ObjectType, &item.ObjectID, &item.Version, &item.SyncKey, &deletedAt)
	item.DeletedAt = parseTime(deletedAt)
	return item, err
}

func (s *Store) SyncTombstone(ctx context.Context, objectType, objectID string) (domain.SyncTombstone, error) {
	return scanSyncTombstone(s.db.QueryRowContext(ctx, `SELECT object_type,object_id,version,sync_key,deleted_at FROM sync_tombstones WHERE object_type=? AND object_id=?`, objectType, objectID))
}

func (s *Store) ListSyncTombstones(ctx context.Context) ([]domain.SyncTombstone, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT object_type,object_id,version,sync_key,deleted_at FROM sync_tombstones ORDER BY deleted_at,object_type,object_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.SyncTombstone{}
	for rows.Next() {
		item, err := scanSyncTombstone(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeleteSyncTombstone(ctx context.Context, objectType, objectID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sync_tombstones WHERE object_type=? AND object_id=?`, objectType, objectID)
	return err
}

func (s *Store) sharedNumericVersion(ctx context.Context, objectType, objectID string, current int) int {
	tombstone, err := s.SyncTombstone(ctx, objectType, objectID)
	if err != nil {
		return current
	}
	deleted, err := strconv.Atoi(tombstone.Version)
	if err == nil && current <= deleted {
		return deleted + 1
	}
	return current
}

func (s *Store) sharedTimeVersion(ctx context.Context, objectType, objectID string, current time.Time) time.Time {
	tombstone, err := s.SyncTombstone(ctx, objectType, objectID)
	if err != nil {
		return current
	}
	deleted, err := time.Parse(time.RFC3339Nano, tombstone.Version)
	if err == nil && !current.After(deleted) {
		return deleted.Add(time.Nanosecond)
	}
	return current
}

func insertSyncTombstone(ctx context.Context, tx *sql.Tx, item domain.SyncTombstone) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sync_tombstones(object_type,object_id,version,sync_key,deleted_at) VALUES(?,?,?,?,?)
		ON CONFLICT(object_type,object_id) DO UPDATE SET version=excluded.version,sync_key=excluded.sync_key,deleted_at=excluded.deleted_at`, item.ObjectType, item.ObjectID, item.Version, item.SyncKey, timeString(item.DeletedAt))
	return err
}

func deleteSharedRow(ctx context.Context, tx *sql.Tx, objectType, objectID string, deletedAt time.Time) (int64, error) {
	var result sql.Result
	var err error
	switch objectType {
	case "project":
		result, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, objectID)
	case "product_line":
		result, err = tx.ExecContext(ctx, `DELETE FROM product_lines WHERE id=?`, objectID)
	case "knowledge":
		result, err = tx.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE id=? AND is_index=0`, objectID)
	case "knowledge_proposal":
		result, err = tx.ExecContext(ctx, `DELETE FROM knowledge_proposals WHERE id=?`, objectID)
	case "skill":
		result, err = tx.ExecContext(ctx, `DELETE FROM skills WHERE id=?`, objectID)
	case "provider":
		result, err = tx.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, objectID)
	case "model":
		result, err = tx.ExecContext(ctx, `UPDATE models SET deleted_at=?,updated_at=? WHERE id=? AND deleted_at=''`, timeString(deletedAt), timeString(deletedAt), objectID)
	case "env_group":
		result, err = tx.ExecContext(ctx, `DELETE FROM env_groups WHERE id=?`, objectID)
	case "prompt_override":
		result, err = tx.ExecContext(ctx, `DELETE FROM prompt_template_overrides WHERE id=?`, objectID)
	default:
		return 0, fmt.Errorf("unsupported shared tombstone type %q", objectType)
	}
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type cascadedProductLine struct {
	id      string
	version string
}

func projectProductLines(ctx context.Context, tx *sql.Tx, projectID string) ([]cascadedProductLine, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,updated_at FROM product_lines WHERE project_id=? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []cascadedProductLine
	for rows.Next() {
		var item cascadedProductLine
		if err := rows.Scan(&item.id, &item.version); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func insertProjectProductLineTombstones(ctx context.Context, tx *sql.Tx, items []cascadedProductLine, projectSyncKey string, deletedAt time.Time) error {
	for _, child := range items {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sync_tombstones WHERE object_type='product_line' AND object_id=?)`, child.id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		version := child.version
		if version == "" {
			version = timeString(deletedAt)
		}
		if err := insertSyncTombstone(ctx, tx, domain.SyncTombstone{
			ObjectType: "product_line", ObjectID: child.id, Version: version,
			SyncKey: projectSyncKey + ":product_line:" + child.id, DeletedAt: deletedAt.UTC(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteSharedObject(ctx context.Context, objectType, objectID, logicalVersion string, deletedAt time.Time) (domain.SyncTombstone, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SyncTombstone{}, err
	}
	defer tx.Rollback()
	var productLines []cascadedProductLine
	if objectType == "project" {
		productLines, err = projectProductLines(ctx, tx, objectID)
		if err != nil {
			return domain.SyncTombstone{}, err
		}
	}
	if logicalVersion == "" {
		logicalVersion = timeString(deletedAt)
	}
	item := domain.SyncTombstone{
		ObjectType: objectType, ObjectID: objectID, Version: logicalVersion, DeletedAt: deletedAt.UTC(),
		SyncKey: fmt.Sprintf("tombstone:v1:%s:%s:%d", objectType, objectID, deletedAt.UTC().UnixNano()),
	}
	affected, err := deleteSharedRow(ctx, tx, objectType, objectID, deletedAt)
	if err != nil {
		return domain.SyncTombstone{}, err
	}
	if affected != 1 {
		return domain.SyncTombstone{}, sql.ErrNoRows
	}
	if err := insertSyncTombstone(ctx, tx, item); err != nil {
		return domain.SyncTombstone{}, err
	}
	if err := insertProjectProductLineTombstones(ctx, tx, productLines, item.SyncKey, deletedAt); err != nil {
		return domain.SyncTombstone{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.SyncTombstone{}, err
	}
	return item, nil
}

func (s *Store) ApplySyncTombstone(ctx context.Context, objectType, objectID, syncKey, version string, deletedAt time.Time) (domain.SyncTombstone, error) {
	if objectType == "" || objectID == "" || syncKey == "" {
		return domain.SyncTombstone{}, fmt.Errorf("sync tombstone identity is required")
	}
	var skillPackage *domain.Skill
	if objectType == "skill" {
		if item, err := s.Skill(ctx, objectID); err == nil {
			skillPackage = &item
		}
	}
	if objectType == "knowledge" {
		if item, err := s.Knowledge(ctx, objectID); err == nil && item.IsIndex {
			return domain.SyncTombstone{}, ErrKnowledgeRootManaged
		}
	}
	if deletedAt.IsZero() {
		deletedAt = time.Now().UTC()
	}
	if version == "" {
		version = "unknown"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SyncTombstone{}, err
	}
	defer tx.Rollback()
	var productLines []cascadedProductLine
	if objectType == "project" {
		productLines, err = projectProductLines(ctx, tx, objectID)
		if err != nil {
			return domain.SyncTombstone{}, err
		}
	}
	item := domain.SyncTombstone{ObjectType: objectType, ObjectID: objectID, Version: version, SyncKey: syncKey, DeletedAt: deletedAt.UTC()}
	if existing, err := scanSyncTombstone(tx.QueryRowContext(ctx, `SELECT object_type,object_id,version,sync_key,deleted_at FROM sync_tombstones WHERE object_type=? AND object_id=?`, objectType, objectID)); err == nil {
		item = existing
	} else if err != sql.ErrNoRows {
		return domain.SyncTombstone{}, err
	}
	if _, err := deleteSharedRow(ctx, tx, objectType, objectID, deletedAt); err != nil {
		return domain.SyncTombstone{}, err
	}
	if err := insertSyncTombstone(ctx, tx, item); err != nil {
		return domain.SyncTombstone{}, err
	}
	if err := insertProjectProductLineTombstones(ctx, tx, productLines, item.SyncKey, deletedAt); err != nil {
		return domain.SyncTombstone{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.SyncTombstone{}, err
	}
	if skillPackage != nil {
		if err := s.removeSkillPackage(*skillPackage); err != nil {
			return domain.SyncTombstone{}, err
		}
	}
	return item, nil
}
