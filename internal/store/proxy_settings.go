package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) ProxySettings(ctx context.Context) (domain.ProxySettings, error) {
	var item domain.ProxySettings
	var updatedAt string
	var subscriptionAt string
	err := s.db.QueryRowContext(ctx, `SELECT mode,http_proxy,https_proxy,no_proxy,managed_profile_id,managed_node_id,managed_refresh_interval_minutes,managed_subscription_at,updated_at FROM proxy_settings WHERE id=1`).
		Scan(&item.Mode, &item.HTTPProxy, &item.HTTPSProxy, &item.NoProxy, &item.ManagedProfileID, &item.ManagedNodeID, &item.ManagedRefreshIntervalMins, &subscriptionAt, &updatedAt)
	item.ManagedSubscriptionAt = parseTime(subscriptionAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateProxySettings(ctx context.Context, item domain.ProxySettings) (domain.ProxySettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxy_settings(id,mode,http_proxy,https_proxy,no_proxy,managed_profile_id,managed_node_id,managed_refresh_interval_minutes,managed_subscription_at,updated_at) VALUES(1,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,http_proxy=excluded.http_proxy,https_proxy=excluded.https_proxy,
			no_proxy=excluded.no_proxy,managed_profile_id=excluded.managed_profile_id,managed_node_id=excluded.managed_node_id,
			managed_refresh_interval_minutes=excluded.managed_refresh_interval_minutes,
			managed_subscription_at=excluded.managed_subscription_at,updated_at=excluded.updated_at`,
		item.Mode, item.HTTPProxy, item.HTTPSProxy, item.NoProxy, item.ManagedProfileID, item.ManagedNodeID,
		item.ManagedRefreshIntervalMins, timeString(item.ManagedSubscriptionAt), timeString(item.UpdatedAt),
	)
	return item, err
}
