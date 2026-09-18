package store

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// NetworkSettings returns the persisted listen address, if one was chosen.
//
// An empty ListenAddress means "use the launch default": a fresh install stores
// nothing and keeps whatever address the installer or tray was given, so this
// setting only ever overrides an explicit choice.
func (s *Store) NetworkSettings(ctx context.Context) (domain.NetworkSettings, error) {
	var item domain.NetworkSettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT listen_address,updated_at FROM network_settings WHERE id=1`).
		Scan(&item.ListenAddress, &updatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return domain.NetworkSettings{}, nil
		}
		return domain.NetworkSettings{}, err
	}
	item.UpdatedAt = parseTime(updatedAt)
	return item, nil
}

func (s *Store) UpdateNetworkSettings(ctx context.Context, item domain.NetworkSettings) (domain.NetworkSettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO network_settings(id,listen_address,updated_at) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET listen_address=excluded.listen_address,updated_at=excluded.updated_at`,
		strings.TrimSpace(item.ListenAddress), timeString(item.UpdatedAt),
	)
	return item, err
}

// ValidateListenAddress accepts an "ip:port" pair the server could actually bind.
//
// The address reaches a scheduled task's argument list, so it is restricted to a
// literal IP and port: no hostname, no wildcard, nothing that would need quoting.
func ValidateListenAddress(value string) error {
	value = strings.TrimSpace(value)
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil {
		return ErrInvalidListenAddress
	}
	if net.ParseIP(strings.TrimSpace(host)) == nil {
		return ErrInvalidListenAddress
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return ErrInvalidListenAddress
	}
	return nil
}
