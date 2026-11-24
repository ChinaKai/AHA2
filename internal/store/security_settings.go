package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const securitySettingsColumns = `validate_origin,access_scope,workspace_terminal_lan,workspace_terminal_external_origin,updated_at`

func scanSecuritySettings(row *sql.Row) (domain.SecuritySettings, error) {
	var item domain.SecuritySettings
	var updatedAt string
	err := row.Scan(&item.ValidateOrigin, &item.AccessScope, &item.WorkspaceTerminalLAN, &item.WorkspaceTerminalExternalOrigin, &updatedAt)
	item.AccessScope = domain.NormalizeAccessScope(item.AccessScope)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) SecuritySettings(ctx context.Context) (domain.SecuritySettings, error) {
	return scanSecuritySettings(s.db.QueryRowContext(ctx, `SELECT `+securitySettingsColumns+` FROM security_settings WHERE id=1`))
}

func (s *Store) UpdateSecuritySettings(ctx context.Context, item domain.SecuritySettings) (domain.SecuritySettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return scanSecuritySettings(s.db.QueryRowContext(ctx, `
		INSERT INTO security_settings(id,validate_origin,access_scope,updated_at) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET validate_origin=excluded.validate_origin,access_scope=excluded.access_scope,updated_at=excluded.updated_at
		RETURNING `+securitySettingsColumns, boolInt(item.ValidateOrigin), domain.NormalizeAccessScope(item.AccessScope), timeString(item.UpdatedAt)))
}

func (s *Store) UpdateWorkspaceTerminalSettings(ctx context.Context, enabled *bool, externalOrigin *string) (domain.SecuritySettings, error) {
	return scanSecuritySettings(s.db.QueryRowContext(ctx,
		`UPDATE security_settings SET workspace_terminal_lan=COALESCE(?,workspace_terminal_lan),
			workspace_terminal_external_origin=COALESCE(?,workspace_terminal_external_origin),
			updated_at=? WHERE id=1 RETURNING `+securitySettingsColumns,
		enabled, externalOrigin, timeString(time.Now().UTC())))
}
