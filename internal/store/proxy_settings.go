package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) ProxySettings(ctx context.Context) (domain.ProxySettings, error) {
	var item domain.ProxySettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT http_proxy,https_proxy,no_proxy,updated_at FROM proxy_settings WHERE id=1`).
		Scan(&item.HTTPProxy, &item.HTTPSProxy, &item.NoProxy, &updatedAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateProxySettings(ctx context.Context, item domain.ProxySettings) (domain.ProxySettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxy_settings(id,http_proxy,https_proxy,no_proxy,updated_at) VALUES(1,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET http_proxy=excluded.http_proxy,https_proxy=excluded.https_proxy,
			no_proxy=excluded.no_proxy,updated_at=excluded.updated_at`,
		item.HTTPProxy, item.HTTPSProxy, item.NoProxy, timeString(item.UpdatedAt),
	)
	return item, err
}
