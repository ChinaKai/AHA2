package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// AgentAPISettings reads the hidden manual override.
//
// The allow_insecure column is deliberately not read: plaintext addresses off
// loopback are no longer a state this program can be in. It is still written as 0
// so databases created by an older build keep satisfying the NOT NULL default.
func (s *Store) AgentAPISettings(ctx context.Context) (domain.AgentAPISettings, error) {
	var item domain.AgentAPISettings
	var updatedAt string
	var stdioProxy int
	err := s.db.QueryRowContext(ctx, `SELECT url,migration_note,stdio_proxy,updated_at FROM agent_api_settings WHERE id=1`).
		Scan(&item.URL, &item.MigrationNote, &stdioProxy, &updatedAt)
	item.StdioProxy = stdioProxy != 0
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateAgentAPISettings(ctx context.Context, item domain.AgentAPISettings) (domain.AgentAPISettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	stdioproxy := 0
	if item.StdioProxy {
		stdioproxy = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_api_settings(id,url,allow_insecure,migration_note,stdio_proxy,updated_at) VALUES(1,?,0,?,?,?)
		ON CONFLICT(id) DO UPDATE SET url=excluded.url,allow_insecure=0,
			migration_note=excluded.migration_note,stdio_proxy=excluded.stdio_proxy,
			updated_at=excluded.updated_at`,
		item.URL, item.MigrationNote, stdioproxy, timeString(item.UpdatedAt),
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

// UpdateWorkspaceAgentAPIDetection records an Agent API detection result.
//
// Turn-time detection runs between loading the workspace and starting the Turn,
// so it writes only the Agent API fields. In capabilities_json it updates just
// $.agent_api: the rest of the map comes from the copy the Turn loaded, and a
// concurrent backend/model probe may have changed it while detection ran.
// Overwriting the whole column would roll those results back.
func (s *Store) UpdateWorkspaceAgentAPIDetection(ctx context.Context, item domain.Workspace) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE workspaces
		SET agent_api_resolved_url=?,agent_api_status=?,agent_api_error=?,agent_api_migration_note=?,
			agent_api_last_checked_at=?,
			capabilities_json=json_set(
				CASE WHEN json_valid(capabilities_json) THEN capabilities_json ELSE '{}' END,
				'$.agent_api', json(?)),
			updated_at=?
		WHERE id=?`,
		item.AgentAPIResolvedURL, workspaceAgentAPIStatus(item.AgentAPIStatus), item.AgentAPIError,
		item.AgentAPIMigrationNote, timeString(item.AgentAPILastCheckedAt),
		encodeJSON(item.Capabilities["agent_api"]), timeString(item.UpdatedAt), item.ID,
	)
	return err
}
