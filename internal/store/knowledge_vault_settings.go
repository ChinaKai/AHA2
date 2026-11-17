package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// KnowledgeVaultSettings reads the single-row owner switch plus the verified
// migration/import metadata. The legacy knowledge tables are not involved.
func (s *Store) KnowledgeVaultSettings(ctx context.Context) (domain.KnowledgeVaultSettings, error) {
	var item domain.KnowledgeVaultSettings
	var enabled, configured, autoSync int
	var updatedAt, importSource, migrationVerifiedAt, importVerifiedAt, backupPath string
	err := s.db.QueryRowContext(ctx, `SELECT enabled,configured,auto_sync,import_source,migration_verified_at,import_verified_at,last_backup_path,updated_at
		FROM knowledge_vault_settings WHERE id=1`).
		Scan(&enabled, &configured, &autoSync, &importSource, &migrationVerifiedAt, &importVerifiedAt, &backupPath, &updatedAt)
	item.Enabled = enabled != 0
	item.Configured = configured != 0
	item.AutoSync = autoSync != 0
	item.ImportSource = importSource
	item.MigrationVerifiedAt = parseTime(migrationVerifiedAt)
	item.ImportVerifiedAt = parseTime(importVerifiedAt)
	item.LastBackupPath = backupPath
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

// UpdateKnowledgeVaultSettings replaces the full owner decision, including the
// migration/import metadata. Callers that only flip the switch must read the
// current row first so verification timestamps are not accidentally cleared.
func (s *Store) UpdateKnowledgeVaultSettings(ctx context.Context, item domain.KnowledgeVaultSettings) (domain.KnowledgeVaultSettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_vault_settings(id,enabled,configured,auto_sync,import_source,migration_verified_at,import_verified_at,last_backup_path,updated_at)
		VALUES(1,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,configured=excluded.configured,
			auto_sync=excluded.auto_sync,
			import_source=excluded.import_source,migration_verified_at=excluded.migration_verified_at,
			import_verified_at=excluded.import_verified_at,last_backup_path=excluded.last_backup_path,
			updated_at=excluded.updated_at`,
		boolInt(item.Enabled), boolInt(item.Configured), boolInt(item.AutoSync), strings.TrimSpace(item.ImportSource),
		timeString(item.MigrationVerifiedAt), timeString(item.ImportVerifiedAt),
		strings.TrimSpace(item.LastBackupPath), timeString(item.UpdatedAt),
	)
	return item, err
}

// SetKnowledgeVaultSwitch persists only the owner-visible switch so existing
// migration/import metadata survives a toggle.
func (s *Store) SetKnowledgeVaultSwitch(ctx context.Context, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_vault_settings SET enabled=?,configured=1,updated_at=? WHERE id=1`,
		boolInt(enabled), timeString(time.Now().UTC()))
	return err
}

// AutoSyncEnabled implements knowledgevault.AutoSyncSettings. The column
// defaults to 1 so an upgraded instance starts syncing without an extra step.
func (s *Store) AutoSyncEnabled(ctx context.Context) (bool, error) {
	var value int
	if err := s.db.QueryRowContext(ctx, `SELECT auto_sync FROM knowledge_vault_settings WHERE id=1`).Scan(&value); err != nil {
		return false, err
	}
	return value != 0, nil
}

// SetAutoSyncEnabled persists only the auto-sync switch. It never changes the
// knowledge source switch or the migration/import verification metadata.
func (s *Store) SetAutoSyncEnabled(ctx context.Context, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_vault_settings SET auto_sync=?,updated_at=? WHERE id=1`,
		boolInt(enabled), timeString(time.Now().UTC()))
	return err
}

// RecordKnowledgeVaultImport marks a migration or existing-vault import as
// verified and turns the manager on. The old knowledge tables stay untouched.
func (s *Store) RecordKnowledgeVaultImport(ctx context.Context, source string, verifiedAt time.Time, backupPath string, legacyMigration bool) error {
	if verifiedAt.IsZero() {
		verifiedAt = time.Now().UTC()
	}
	column := "import_verified_at"
	if legacyMigration {
		column = "migration_verified_at"
	}
	query := `UPDATE knowledge_vault_settings SET ` + column + `=?,import_source=?,last_backup_path=?,enabled=1,configured=1,updated_at=? WHERE id=1`
	_, err := s.db.ExecContext(ctx, query, timeString(verifiedAt), strings.TrimSpace(source), strings.TrimSpace(backupPath), timeString(time.Now().UTC()))
	return err
}

// LegacyKnowledgeInventory counts pre-vault knowledge that an upgrade may
// still be using. Navigation roots/categories are excluded: every install,
// including a fresh one, creates them, so counting them would make a fresh
// install look like an unverified upgrade. The old flat skill package
// directory is counted separately; the vault and its index are not consulted.
func (s *Store) LegacyKnowledgeInventory(ctx context.Context) (domain.LegacyKnowledgeInventory, error) {
	var inventory domain.LegacyKnowledgeInventory
	if exists, err := s.tableExists(ctx, "knowledge_entries"); err == nil && exists {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries
			WHERE is_index=0 AND type<>'navigation'`).Scan(&inventory.Entries); err != nil {
			return inventory, err
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills`).Scan(&inventory.Skills); err != nil {
		return inventory, err
	}
	inventory.SkillPackages = s.legacySkillPackageCount()
	return inventory, nil
}

func (s *Store) legacySkillPackageCount() int {
	if s == nil || s.dataDir == "" {
		return 0
	}
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "knowledge", "skills"))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if info, statErr := os.Stat(filepath.Join(s.dataDir, "knowledge", "skills", entry.Name(), "SKILL.md")); statErr == nil && !info.IsDir() {
			count++
		}
	}
	return count
}

// DatabasePath exposes the primary SQLite file for backup and read-only
// migration helpers. It is the path this Store was opened with.
func (s *Store) DatabasePath() string {
	if s == nil {
		return ""
	}
	return s.path
}
