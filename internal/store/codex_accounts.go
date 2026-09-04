package store

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) UpsertCodexAccount(ctx context.Context, item domain.CodexAccount) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO codex_accounts(
			id,label,email,account_id,plan_type,status,proxy_enabled,credential_ref,credential_configured,
			usage_json,usage_updated_at,usage_error,models_json,models_updated_at,models_error,
			created_at,updated_at,last_used_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET label=excluded.label,email=excluded.email,account_id=excluded.account_id,
			plan_type=excluded.plan_type,status=excluded.status,proxy_enabled=excluded.proxy_enabled,credential_ref=excluded.credential_ref,
			credential_configured=excluded.credential_configured,usage_json=excluded.usage_json,
			usage_updated_at=excluded.usage_updated_at,usage_error=excluded.usage_error,
			models_json=excluded.models_json,models_updated_at=excluded.models_updated_at,models_error=excluded.models_error,
			updated_at=excluded.updated_at,last_used_at=excluded.last_used_at`,
		item.ID, item.Label, item.Email, item.AccountID, item.PlanType, item.Status, boolInt(item.ProxyEnabled), item.CredentialRef,
		boolInt(item.CredentialConfigured), encodeJSON(item.Usage), timeString(item.UsageUpdatedAt), item.UsageError,
		encodeJSON(item.AvailableModels), timeString(item.ModelsUpdatedAt), item.ModelsError,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.LastUsedAt),
	)
	return err
}

const codexAccountColumns = `id,label,email,account_id,plan_type,status,proxy_enabled,credential_ref,credential_configured,usage_json,usage_updated_at,usage_error,models_json,models_updated_at,models_error,created_at,updated_at,last_used_at`

func scanCodexAccount(scanner interface{ Scan(...any) error }) (domain.CodexAccount, error) {
	var item domain.CodexAccount
	var proxyEnabled, configured int
	var usageJSON, usageUpdatedAt, modelsJSON, modelsUpdatedAt string
	var createdAt, updatedAt, lastUsedAt string
	err := scanner.Scan(
		&item.ID, &item.Label, &item.Email, &item.AccountID, &item.PlanType, &item.Status, &proxyEnabled,
		&item.CredentialRef, &configured, &usageJSON, &usageUpdatedAt, &item.UsageError,
		&modelsJSON, &modelsUpdatedAt, &item.ModelsError, &createdAt, &updatedAt, &lastUsedAt,
	)
	item.ProxyEnabled = proxyEnabled != 0
	item.CredentialConfigured = configured != 0
	if usageJSON != "" && usageJSON != "{}" && usageJSON != "null" {
		usage := decodeJSON(usageJSON, domain.CodexUsage{})
		item.Usage = &usage
	}
	item.AvailableModels = decodeJSON(modelsJSON, []domain.CodexModelOption{})
	item.UsageUpdatedAt, item.ModelsUpdatedAt = parseTime(usageUpdatedAt), parseTime(modelsUpdatedAt)
	item.CreatedAt, item.UpdatedAt, item.LastUsedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(lastUsedAt)
	return item, err
}

func (s *Store) CodexAccount(ctx context.Context, id string) (domain.CodexAccount, error) {
	return scanCodexAccount(s.db.QueryRowContext(ctx, `SELECT `+codexAccountColumns+` FROM codex_accounts WHERE id=?`, id))
}

func (s *Store) ListCodexAccounts(ctx context.Context) ([]domain.CodexAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+codexAccountColumns+` FROM codex_accounts ORDER BY last_used_at DESC,label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.CodexAccount
	for rows.Next() {
		item, err := scanCodexAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeleteCodexAccount(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM codex_accounts WHERE id=?`, id)
	return err
}

func (s *Store) CodexAccountInUse(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM runtime_config_snapshots snapshot
			WHERE snapshot.codex_account_id=? AND (
				EXISTS(SELECT 1 FROM tasks WHERE runtime_config_snapshot_id=snapshot.id) OR
				EXISTS(SELECT 1 FROM task_agents WHERE runtime_config_snapshot_id=snapshot.id) OR
				EXISTS(SELECT 1 FROM turns WHERE runtime_config_snapshot_id=snapshot.id)
			)
		) OR EXISTS(SELECT 1 FROM backend_sessions WHERE codex_account_id=?)`, id, id).Scan(&exists)
	return exists, err
}
