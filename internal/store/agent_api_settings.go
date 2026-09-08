package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) AgentAPISettings(ctx context.Context) (domain.AgentAPISettings, error) {
	var item domain.AgentAPISettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT url,allow_insecure,updated_at FROM agent_api_settings WHERE id=1`).
		Scan(&item.URL, &item.AllowInsecure, &updatedAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateAgentAPISettings(ctx context.Context, item domain.AgentAPISettings) (domain.AgentAPISettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_api_settings(id,url,allow_insecure,updated_at) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET url=excluded.url,allow_insecure=excluded.allow_insecure,updated_at=excluded.updated_at`,
		item.URL, boolInt(item.AllowInsecure), timeString(item.UpdatedAt),
	)
	return item, err
}

func (s *Store) ResetWorkspaceAgentAPIDetection(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE workspaces
		SET agent_api_resolved_url='',agent_api_status='unknown',agent_api_error='',agent_api_last_checked_at='',
			capabilities_json=CASE WHEN json_valid(capabilities_json) THEN json_remove(capabilities_json,'$.agent_api') ELSE '{}' END,
			updated_at=?
		WHERE read_only=0`, timeString(now))
	return err
}
