package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LegacyCutoverOptions controls the one-way removal of the pre-vault knowledge
// tables. BackupDir defaults to <data-dir>/backups. VerifyExport runs after the
// backup and before any table is dropped; a non-nil error aborts the cutover
// with every legacy table intact.
type LegacyCutoverOptions struct {
	BackupDir    string
	Now          func() time.Time
	VerifyExport func(ctx context.Context) error
}

// LegacyCutoverReport is the audit record for the cutover.
type LegacyCutoverReport struct {
	BackupPath    string   `json:"backup_path"`
	BackedUpRows  int      `json:"backed_up_rows"`
	BackupTableOK bool     `json:"backup_verified"`
	TablesDropped []string `json:"tables_dropped,omitempty"`
	Applied       bool     `json:"applied"`
	AppliedAt     string   `json:"applied_at,omitempty"`
}

// legacyCutoverTables lists the pre-vault tables in dependency order. Grants
// reference policies and knowledge_entries, so they go first.
var legacyCutoverTables = []string{
	"channel_knowledge_records",
	"channel_knowledge_grants",
	"channel_knowledge_policies",
	"project_knowledge_bindings",
	"knowledge_libraries",
	"knowledge_entries",
}

// LegacyKnowledgeRows counts the legacy knowledge entries. It returns 0 once
// the cutover has run and the table is gone.
func (s *Store) LegacyKnowledgeRows(ctx context.Context) (int, error) {
	exists, err := s.tableExists(ctx, "knowledge_entries")
	if err != nil || !exists {
		return 0, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&exists)
	return exists > 0, err
}

// BackupDatabase makes a consistent copy with SQLite's VACUUM INTO. The
// destination must not already exist.
func (s *Store) BackupDatabase(ctx context.Context, destination string) error {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return fmt.Errorf("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination %s already exists", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	// VACUUM INTO does not accept bound parameters on every SQLite build;
	// escape the literal instead.
	escaped := strings.ReplaceAll(destination, "'", "''")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO '`+escaped+`'`); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	return nil
}

// verifyBackup opens the backup and checks the legacy entry count. A backup
// that cannot be opened is never accepted.
func verifyLegacyBackup(ctx context.Context, path string, expected int) (bool, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return false, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return false, err
	}
	var exists int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='knowledge_entries'`).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return expected == 0, nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_entries`).Scan(&count); err != nil {
		return false, err
	}
	return count == expected, nil
}

// CutoverLegacyKnowledge backs up the database, verifies the caller's export,
// then drops the legacy knowledge tables in one transaction and records schema
// version 143. It is not reversible; a failed verification aborts before any
// drop.
func (s *Store) CutoverLegacyKnowledge(ctx context.Context, options LegacyCutoverOptions) (LegacyCutoverReport, error) {
	report := LegacyCutoverReport{}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	rows, err := s.LegacyKnowledgeRows(ctx)
	if err != nil {
		return report, err
	}
	report.BackedUpRows = rows
	backupDir := strings.TrimSpace(options.BackupDir)
	if backupDir == "" {
		backupDir = filepath.Join(s.dataDir, "backups")
	}
	report.BackupPath = filepath.Join(backupDir, "aha2-before-knowledge-cutover-"+now.Format("20060102T150405Z")+".db")
	if err := s.BackupDatabase(ctx, report.BackupPath); err != nil {
		return report, err
	}
	ok, err := verifyLegacyBackup(ctx, report.BackupPath, rows)
	if err != nil {
		return report, fmt.Errorf("verify legacy knowledge backup: %w", err)
	}
	if !ok {
		return report, fmt.Errorf("legacy knowledge backup row count mismatch")
	}
	report.BackupTableOK = true
	if options.VerifyExport != nil {
		if err := options.VerifyExport(ctx); err != nil {
			return report, fmt.Errorf("legacy knowledge export verification failed: %w", err)
		}
	}
	existing := map[string]bool{}
	for _, table := range legacyCutoverTables {
		exists, existsErr := s.tableExists(ctx, table)
		if existsErr != nil {
			return report, existsErr
		}
		existing[table] = exists
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range legacyCutoverTables {
		if !existing[table] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return report, fmt.Errorf("drop legacy table %s: %w", table, err)
		}
		report.TablesDropped = append(report.TablesDropped, table)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(143, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return report, err
	}
	// Foreign-key checking runs before commit so a remaining reference aborts
	// the whole cutover instead of leaving a half-dropped schema.
	rowsLeft, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return report, err
	}
	violations := 0
	for rowsLeft.Next() {
		violations++
	}
	if err := rowsLeft.Close(); err != nil {
		return report, err
	}
	if violations > 0 {
		return report, fmt.Errorf("legacy cutover would leave %d foreign key violations", violations)
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	report.Applied = true
	report.AppliedAt = now.Format(time.RFC3339)
	return report, nil
}

// LegacyCutoverApplied reports whether schema v143 is recorded.
func (s *Store) LegacyCutoverApplied(ctx context.Context) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=143`).Scan(&exists)
	return exists > 0, err
}
