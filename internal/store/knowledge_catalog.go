package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateProductLine(ctx context.Context, item domain.ProductLine) error {
	item.UpdatedAt = s.sharedTimeVersion(ctx, "product_line", item.ID, item.UpdatedAt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if item.Default {
		if _, err := tx.ExecContext(ctx, `UPDATE product_lines SET is_default=0 WHERE project_id=?`, item.ProjectID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO product_lines(id,project_id,name,branch_pattern,is_default,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`, item.ID, item.ProjectID, item.Name, item.BranchPattern, boolInt(item.Default),
		timeString(item.CreatedAt), timeString(item.UpdatedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

const productLineColumns = `id,project_id,name,branch_pattern,is_default,created_at,updated_at`

func scanProductLine(scanner interface{ Scan(...any) error }) (domain.ProductLine, error) {
	var item domain.ProductLine
	var createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.ProjectID, &item.Name, &item.BranchPattern, &item.Default, &createdAt, &updatedAt)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) ListProductLines(ctx context.Context, projectID string) ([]domain.ProductLine, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+productLineColumns+` FROM product_lines WHERE project_id=? ORDER BY is_default DESC,name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ProductLine{}
	for rows.Next() {
		item, err := scanProductLine(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ProductLine(ctx context.Context, id string) (domain.ProductLine, error) {
	return scanProductLine(s.db.QueryRowContext(ctx, `SELECT `+productLineColumns+` FROM product_lines WHERE id=?`, id))
}

func (s *Store) UpdateProductLine(ctx context.Context, item domain.ProductLine) error {
	item.UpdatedAt = s.sharedTimeVersion(ctx, "product_line", item.ID, item.UpdatedAt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if item.Default {
		if _, err := tx.ExecContext(ctx, `UPDATE product_lines SET is_default=0 WHERE project_id=? AND id<>?`, item.ProjectID, item.ID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE product_lines SET project_id=?,name=?,branch_pattern=?,is_default=?,updated_at=? WHERE id=?`,
		item.ProjectID, item.Name, item.BranchPattern, boolInt(item.Default), timeString(item.UpdatedAt), item.ID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) DeleteProductLine(ctx context.Context, id string) error {
	item, err := s.ProductLine(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.deleteSharedObject(ctx, "product_line", id, timeString(item.UpdatedAt), time.Now().UTC())
	return err
}

func (s *Store) CreateSkill(ctx context.Context, item domain.Skill) error {
	item.Version = s.sharedNumericVersion(ctx, "skill", item.ID, item.Version)
	if item.PackageSlug == "" {
		item.PackageSlug = s.uniqueSkillSlug(item.Name, item.ID)
	}
	if err := s.writeSkillPackage(&item); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = s.removeSkillPackage(item)
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skills(id,package_slug,scope,project_id,name,description,instructions,version,status,enabled,auto_attach,source_path,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.PackageSlug, item.Scope, item.ProjectID, item.Name, item.Description, item.Instructions,
		item.Version, item.Status, boolInt(item.Enabled), boolInt(item.AutoAttach), item.SourcePath, timeString(item.CreatedAt), timeString(item.UpdatedAt)); err != nil {
		_ = s.removeSkillPackage(item)
		return err
	}
	resurrectToken, err := supersedeSkillTombstoneTx(ctx, tx, item.ID, item.Version)
	if err != nil {
		_ = s.removeSkillPackage(item)
		return err
	}
	// A delete enqueued by an earlier run but not pushed yet would recreate the
	// tombstone on peers right before the rebuild reaches them, making it a
	// no-op there. The object identity is scope independent, so the withdrawal
	// applies to every queued scope.
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_outbox WHERE object_type='skill' AND object_id=? AND operation='delete' AND status='pending'`, item.ID); err != nil {
		_ = s.removeSkillPackage(item)
		return err
	}
	if resurrectToken != "" {
		if err := enqueueSkillResurrectTx(ctx, tx, item, resurrectToken); err != nil {
			_ = s.removeSkillPackage(item)
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		// The row may already be committed; keep the package on disk rather
		// than leaving a row whose files were removed under it.
		return err
	}
	return nil
}

// supersedeSkillTombstoneTx lets a deliberate local create reuse a skill ID
// that was deleted earlier: sharedNumericVersion has already raised the new
// version above the deleted one, so the row and the tombstone cannot both be
// exported. It returns the generation token of the resurrection this create
// represents, which the caller queues as an explicit resurrect event so peers
// holding their own tombstone can accept the rebuild.
//
// A tombstone whose version cannot be parsed, or which the new row does not
// exceed, is never removed: silently dropping it would let a stale rebuild
// resurrect an object whose deletion order is unknown.
func supersedeSkillTombstoneTx(ctx context.Context, tx *sql.Tx, id string, version int) (string, error) {
	var deleted string
	err := tx.QueryRowContext(ctx, `SELECT version FROM sync_tombstones WHERE object_type='skill' AND object_id=?`, id).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	deletedVersion, convErr := strconv.Atoi(deleted)
	if convErr != nil {
		return "", fmt.Errorf("skill %s: tombstone version %q is not numeric; refusing to rebuild over it", id, deleted)
	}
	if version <= deletedVersion {
		return "", fmt.Errorf("skill %s: version %d does not exceed tombstone version %d", id, version, deletedVersion)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_tombstones WHERE object_type='skill' AND object_id=?`, id); err != nil {
		return "", err
	}
	return domain.NewID("resurrect"), nil
}

// skillWirePayload mirrors the skill payload written by internal/sync. The
// resurrect token is the only additive field: peers that predate it ignore the
// unknown key and keep blocking the object, which is the safe outcome.
type skillWirePayload struct {
	Skill     domain.Skill       `json:"skill"`
	Files     []domain.SkillFile `json:"files"`
	Resurrect string             `json:"resurrect,omitempty"`
}

// enqueueSkillResurrectTx queues one explicit rebuild event. Shipping the event
// with its own identity (instead of only a snapshot upsert) is what lets peers
// tell a deliberate resurrection from an ordinary, possibly stale, upsert.
func enqueueSkillResurrectTx(ctx context.Context, tx *sql.Tx, item domain.Skill, token string) error {
	var scope string
	err := tx.QueryRowContext(ctx, `SELECT scope FROM sync_state ORDER BY scope LIMIT 1`).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		// Sync is not configured on this device; nothing to propagate.
		return nil
	}
	if err != nil {
		return err
	}
	payloadItem := item
	payloadItem.SourcePath = ""
	payloadItem.Files = nil
	payloadItem.PackageFiles = nil
	payload, err := json.Marshal(skillWirePayload{
		Skill:     payloadItem,
		Files:     []domain.SkillFile{{Path: "SKILL.md", Content: skillMarkdown(item)}},
		Resurrect: token,
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO sync_outbox(id,scope,object_type,object_id,operation,payload_json,base_version,source_version,idempotency_key,status,attempts,next_attempt_at,last_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		domain.NewID("sync"), scope, "skill", item.ID, "upsert", string(payload), "",
		strconv.Itoa(item.Version), "skill:"+item.ID+":resurrect:"+token, "pending", 0, "", "",
		timeString(now), timeString(now))
	return err
}

// AdvanceSkillVersion raises the sync version of an existing skill without
// rewriting its package. Sync uses it when an inbound object carries a higher
// version but identical content: the content is unchanged, yet the version is
// the watermark stale events are compared against, so a later lower-versioned
// event must not overwrite what this device has already seen.
func (s *Store) AdvanceSkillVersion(ctx context.Context, id string, baseVersion, version int) error {
	if version <= baseVersion {
		return nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skills SET version=? WHERE id=? AND version=?`, version, id, baseVersion)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return fmt.Errorf("skill version conflict")
	}
	return nil
}

func (s *Store) ListSkills(ctx context.Context, scope, projectID string, enabledOnly bool) ([]domain.Skill, error) {
	query := `SELECT id,package_slug,scope,project_id,name,description,instructions,version,status,enabled,auto_attach,source_path,created_at,updated_at FROM skills WHERE 1=1`
	args := []any{}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	if enabledOnly {
		query += ` AND enabled=1 AND status='active'`
	}
	query += ` ORDER BY scope,name`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Skill{}
	for rows.Next() {
		var item domain.Skill
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.PackageSlug, &item.Scope, &item.ProjectID, &item.Name, &item.Description, &item.Instructions,
			&item.Version, &item.Status, &item.Enabled, &item.AutoAttach, &item.SourcePath, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		if err := s.hydrateSkillPackage(&item); err != nil {
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
		if binding, ok := bindings[result[index].ProjectID]; ok {
			result[index].BoundProjectID = binding.ProjectID
			result[index].BindingMode = binding.BindingMode
			result[index].CanUpdate = binding.BindingMode == "project"
		}
	}
	return result, nil
}

func (s *Store) UpdateSkill(ctx context.Context, item domain.Skill) error {
	if err := s.writeSkillPackage(&item); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE skills SET package_slug=?,scope=?,project_id=?,name=?,description=?,instructions=?,version=?,status=?,enabled=?,auto_attach=?,source_path=?,updated_at=? WHERE id=?`,
		item.PackageSlug, item.Scope, item.ProjectID, item.Name, item.Description, item.Instructions, item.Version, item.Status,
		boolInt(item.Enabled), boolInt(item.AutoAttach), item.SourcePath, timeString(item.UpdatedAt), item.ID)
	return err
}

func (s *Store) Skill(ctx context.Context, id string) (domain.Skill, error) {
	var item domain.Skill
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,package_slug,scope,project_id,name,description,instructions,version,status,enabled,auto_attach,source_path,created_at,updated_at FROM skills WHERE id=?`, id).
		Scan(&item.ID, &item.PackageSlug, &item.Scope, &item.ProjectID, &item.Name, &item.Description, &item.Instructions,
			&item.Version, &item.Status, &item.Enabled, &item.AutoAttach, &item.SourcePath, &createdAt, &updatedAt)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	if err == nil {
		if bindings, bindingErr := s.knowledgeLibraryBindings(ctx); bindingErr == nil {
			if binding, ok := bindings[item.ProjectID]; ok {
				item.BoundProjectID = binding.ProjectID
				item.BindingMode = binding.BindingMode
				item.CanUpdate = binding.BindingMode == "project"
			}
		}
		err = s.hydrateSkillPackage(&item)
	}
	return item, err
}

func (s *Store) SkillsByIDs(ctx context.Context, ids []string) ([]domain.Skill, error) {
	result := make([]domain.Skill, 0, len(ids))
	for _, id := range ids {
		item, err := s.Skill(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) DeleteSkill(ctx context.Context, id string) error {
	item, err := s.Skill(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.deleteSharedObject(ctx, "skill", id, strconv.Itoa(item.Version), time.Now().UTC()); err != nil {
		return err
	}
	return s.removeSkillPackage(item)
}
