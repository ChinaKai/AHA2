package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) SecuritySettings(ctx context.Context) (domain.SecuritySettings, error) {
	var item domain.SecuritySettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT validate_origin,updated_at FROM security_settings WHERE id=1`).
		Scan(&item.ValidateOrigin, &updatedAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateSecuritySettings(ctx context.Context, item domain.SecuritySettings) (domain.SecuritySettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO security_settings(id,validate_origin,updated_at) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET validate_origin=excluded.validate_origin,updated_at=excluded.updated_at`,
		boolInt(item.ValidateOrigin), timeString(item.UpdatedAt),
	)
	return item, err
}
