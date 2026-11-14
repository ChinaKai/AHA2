package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// KnowledgeVaultSettings reads the single-row Phase 3 feature switch. The
// legacy knowledge tables are not involved: this row only records whether the
// owner enabled the file-backed read-only vault in the Web UI.
func (s *Store) KnowledgeVaultSettings(ctx context.Context) (domain.KnowledgeVaultSettings, error) {
	var item domain.KnowledgeVaultSettings
	var enabled, configured int
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT enabled,configured,updated_at FROM knowledge_vault_settings WHERE id=1`).
		Scan(&enabled, &configured, &updatedAt)
	item.Enabled = enabled != 0
	item.Configured = configured != 0
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateKnowledgeVaultSettings(ctx context.Context, item domain.KnowledgeVaultSettings) (domain.KnowledgeVaultSettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_vault_settings(id,enabled,configured,updated_at) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,configured=excluded.configured,updated_at=excluded.updated_at`,
		boolInt(item.Enabled), boolInt(item.Configured), timeString(item.UpdatedAt),
	)
	return item, err
}
