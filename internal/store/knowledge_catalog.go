package store

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateProductLine(ctx context.Context, item domain.ProductLine) error {
	if item.Default {
		if _, err := s.db.ExecContext(ctx, `UPDATE product_lines SET is_default=0 WHERE project_id=?`, item.ProjectID); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO product_lines(id,project_id,name,branch_pattern,is_default,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`, item.ID, item.ProjectID, item.Name, item.BranchPattern, boolInt(item.Default),
		timeString(item.CreatedAt), timeString(item.UpdatedAt))
	return err
}

func (s *Store) ListProductLines(ctx context.Context, projectID string) ([]domain.ProductLine, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_id,name,branch_pattern,is_default,created_at,updated_at FROM product_lines WHERE project_id=? ORDER BY is_default DESC,name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ProductLine{}
	for rows.Next() {
		var item domain.ProductLine
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Name, &item.BranchPattern, &item.Default, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeleteProductLine(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM product_lines WHERE id=?`, id)
	return err
}

func (s *Store) CreateSkill(ctx context.Context, item domain.Skill) error {
	if item.PackageSlug == "" {
		item.PackageSlug = s.uniqueSkillSlug(item.Name, item.ID)
	}
	if err := s.writeSkillPackage(&item); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO skills(id,package_slug,scope,project_id,name,description,instructions,version,status,enabled,source_path,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.PackageSlug, item.Scope, item.ProjectID, item.Name, item.Description, item.Instructions,
		item.Version, item.Status, boolInt(item.Enabled), item.SourcePath, timeString(item.CreatedAt), timeString(item.UpdatedAt))
	if err != nil {
		_ = s.removeSkillPackage(item)
	}
	return err
}

func (s *Store) ListSkills(ctx context.Context, scope, projectID string, enabledOnly bool) ([]domain.Skill, error) {
	query := `SELECT id,package_slug,scope,project_id,name,description,instructions,version,status,enabled,source_path,created_at,updated_at FROM skills WHERE 1=1`
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
			&item.Version, &item.Status, &item.Enabled, &item.SourcePath, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		if err := s.hydrateSkillPackage(&item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateSkill(ctx context.Context, item domain.Skill) error {
	if err := s.writeSkillPackage(&item); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE skills SET package_slug=?,scope=?,project_id=?,name=?,description=?,instructions=?,version=?,status=?,enabled=?,source_path=?,updated_at=? WHERE id=?`,
		item.PackageSlug, item.Scope, item.ProjectID, item.Name, item.Description, item.Instructions, item.Version, item.Status,
		boolInt(item.Enabled), item.SourcePath, timeString(item.UpdatedAt), item.ID)
	return err
}

func (s *Store) Skill(ctx context.Context, id string) (domain.Skill, error) {
	var item domain.Skill
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,package_slug,scope,project_id,name,description,instructions,version,status,enabled,source_path,created_at,updated_at FROM skills WHERE id=?`, id).
		Scan(&item.ID, &item.PackageSlug, &item.Scope, &item.ProjectID, &item.Name, &item.Description, &item.Instructions,
			&item.Version, &item.Status, &item.Enabled, &item.SourcePath, &createdAt, &updatedAt)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	if err == nil {
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
	if _, err := s.db.ExecContext(ctx, `DELETE FROM skills WHERE id=?`, id); err != nil {
		return err
	}
	return s.removeSkillPackage(item)
}
