package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) GeneralSettings(ctx context.Context) (domain.GeneralSettings, error) {
	var item domain.GeneralSettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT language,updated_at FROM general_settings WHERE id=1`).
		Scan(&item.Language, &updatedAt)
	item.Language = domain.NormalizeLanguage(item.Language)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateGeneralSettings(ctx context.Context, item domain.GeneralSettings) (domain.GeneralSettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	item.Language = domain.NormalizeLanguage(item.Language)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO general_settings(id,language,updated_at) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET language=excluded.language,updated_at=excluded.updated_at`,
		item.Language, timeString(item.UpdatedAt),
	)
	return item, err
}
