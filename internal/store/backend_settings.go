package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) BackendSettings(ctx context.Context) (domain.BackendSettings, error) {
	var item domain.BackendSettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT idle_timeout_seconds,turn_timeout_seconds,updated_at FROM backend_settings WHERE id=1`).
		Scan(&item.IdleTimeoutSeconds, &item.TurnTimeoutSeconds, &updatedAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateBackendSettings(ctx context.Context, item domain.BackendSettings) (domain.BackendSettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backend_settings(id,idle_timeout_seconds,turn_timeout_seconds,updated_at) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			idle_timeout_seconds=excluded.idle_timeout_seconds,
			turn_timeout_seconds=excluded.turn_timeout_seconds,
			updated_at=excluded.updated_at`,
		item.IdleTimeoutSeconds, item.TurnTimeoutSeconds, timeString(item.UpdatedAt),
	)
	return item, err
}
