package store

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// ListVaultSkillPreferences reads every vault skill auto-attach choice. The
// vault file tree is not consulted here; callers intersect these rows with the
// skills the vault currently exposes so deleted packages degrade explicitly.
func (s *Store) ListVaultSkillPreferences(ctx context.Context) ([]domain.VaultSkillPreference, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT skill_id,scope,project_id,auto_attach,global_attach_confirmed_at,updated_at
		FROM vault_skill_preferences ORDER BY skill_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.VaultSkillPreference{}
	for rows.Next() {
		var item domain.VaultSkillPreference
		var autoAttach int
		var confirmedAt, updatedAt string
		if err := rows.Scan(&item.SkillID, &item.Scope, &item.ProjectID, &autoAttach, &confirmedAt, &updatedAt); err != nil {
			return nil, err
		}
		item.AutoAttach = autoAttach != 0
		item.GlobalAttachConfirmedAt = parseTime(confirmedAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

// SetVaultSkillPreference upserts one auto-attach choice. Only the preference
// row changes; vault files and legacy skill rows are untouched.
func (s *Store) SetVaultSkillPreference(ctx context.Context, item domain.VaultSkillPreference) error {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vault_skill_preferences(skill_id,scope,project_id,auto_attach,global_attach_confirmed_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(skill_id) DO UPDATE SET scope=excluded.scope,project_id=excluded.project_id,
			auto_attach=excluded.auto_attach,global_attach_confirmed_at=excluded.global_attach_confirmed_at,
			updated_at=excluded.updated_at`,
		strings.TrimSpace(item.SkillID), strings.TrimSpace(item.Scope), strings.TrimSpace(item.ProjectID),
		boolInt(item.AutoAttach), timeString(item.GlobalAttachConfirmedAt), timeString(item.UpdatedAt),
	)
	return err
}
