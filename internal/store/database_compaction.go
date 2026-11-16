package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// databaseCompactMu keeps manual compactions from overlapping. A second
// request is reported as skipped instead of queueing.
var databaseCompactMu sync.Mutex

// databaseDiskFree is injectable for tests that exercise the disk-space
// precheck without filling a filesystem.
var databaseDiskFree = diskSpaceFree

// databaseCompactAfterVacuum is a test hook that runs after VACUUM and before
// verification. It lets a test force the rollback path (for example by writing
// a row and returning an error).
var databaseCompactAfterVacuum func(context.Context, *Store) error

// databaseCompactRowCountTables are the tables whose counts must survive the
// rebuild. Missing tables are ignored so older databases still compact.
var databaseCompactRowCountTables = []string{
	"projects", "tasks", "task_rounds", "turns", "messages", "conversation_items",
	"events", "audit_events", "attachments", "knowledge_entries", "channel_instances",
	"data_retention_settings",
}

// DatabaseCompactionPlan reports whether a manual compaction can run now. It is
// read-only: it reports free pages, the estimated reclaimable bytes, the
// temporary space needed (~2.2x the database, including the backup copy),
// current disk space and the writers that would block the rebuild.
func (s *Store) DatabaseCompactionPlan(ctx context.Context) (domain.DatabaseCompactionPlan, error) {
	plan := domain.DatabaseCompactionPlan{
		Mode:            domain.DatabaseCompactionModeVacuumInPlace,
		DatabasePath:    s.path,
		BlockingReasons: []string{},
		CheckedAt:       time.Now().UTC(),
	}
	if s.path == "" || isMemoryDatabase(s.path) {
		plan.BlockingReasons = append(plan.BlockingReasons, domain.DatabaseCompactionSkipNotFile)
		return plan, nil
	}
	plan.FileBytes = fileSize(s.path)
	plan.WALBytes = fileSize(s.path + "-wal")
	plan.SHMBytes = fileSize(s.path + "-shm")
	for _, pragma := range []struct {
		name   string
		target *int64
	}{
		{"page_size", &plan.PageSize},
		{"page_count", &plan.PageCount},
		{"freelist_count", &plan.FreelistCount},
	} {
		if err := s.db.QueryRowContext(ctx, `PRAGMA `+pragma.name).Scan(pragma.target); err != nil {
			return plan, fmt.Errorf("read %s: %w", pragma.name, err)
		}
	}
	plan.FreeBytes = plan.FreelistCount * plan.PageSize
	plan.EstimatedReclaimedBytes = plan.FreeBytes
	// VACUUM needs roughly the database size as scratch space; the backup copy
	// adds another full copy. Keep a 10% margin and 64 MiB for the journal.
	plan.RequiredTempBytes = plan.FileBytes + plan.FileBytes*12/10 + 64<<20
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tasks
		WHERE status IN ('preparing','active','waiting_user','blocked')`).Scan(&plan.ActiveTasks); err != nil {
		return plan, fmt.Errorf("count active tasks: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM turns
		WHERE status IN ('queued','preparing','starting','running','waiting')`).Scan(&plan.ActiveTurns); err != nil {
		return plan, fmt.Errorf("count active turns: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM task_permission_requests WHERE status='pending'`).Scan(&plan.PendingApprovals); err != nil {
		return plan, fmt.Errorf("count pending approvals: %w", err)
	}
	if plan.ActiveTasks > 0 || plan.ActiveTurns > 0 {
		plan.BlockingReasons = append(plan.BlockingReasons, domain.DatabaseCompactionSkipActive)
	}
	if plan.PendingApprovals > 0 {
		plan.BlockingReasons = append(plan.BlockingReasons, "pending_approvals")
	}
	free, err := databaseDiskFree(filepath.Dir(s.path))
	if err != nil {
		return plan, fmt.Errorf("read free disk space: %w", err)
	}
	plan.DiskFreeBytes = free
	if free < plan.RequiredTempBytes {
		plan.BlockingReasons = append(plan.BlockingReasons, domain.DatabaseCompactionSkipDisk)
	}
	plan.CanCompact = len(plan.BlockingReasons) == 0
	return plan, nil
}

// CompactDatabase performs one manual, confirmed compaction:
//
//	backup (VACUUM INTO) -> wal_checkpoint(TRUNCATE) -> VACUUM in place ->
//	integrity_check + key table row counts.
//
// It refuses when a task, turn or permission request is active, when another
// compaction is running, or when free disk space is below the plan's
// requirement. A failed rebuild or verification restores every table from the
// backup. It never runs automatically and never touches the database before the
// preflight passes.
func (s *Store) CompactDatabase(ctx context.Context) (domain.DatabaseCompactionResult, error) {
	started := time.Now()
	result := domain.DatabaseCompactionResult{}
	if !databaseCompactMu.TryLock() {
		result.SkipReason = domain.DatabaseCompactionSkipAlreadyRunning
		return result, nil
	}
	defer databaseCompactMu.Unlock()
	plan, err := s.DatabaseCompactionPlan(ctx)
	result.Plan = plan
	if err != nil {
		return result, err
	}
	if !plan.CanCompact {
		result.SkipReason = strings.Join(plan.BlockingReasons, ",")
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}
	backupPath, err := s.backupDatabaseForCompaction(ctx)
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result, err
	}
	result.BackupPath = backupPath
	result.BeforeBytes = plan.FileBytes
	result.WALBeforeBytes = plan.WALBytes
	beforeCounts, err := s.countTablesForCompaction(ctx, "main")
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result, err
	}
	result.BeforeRowCounts = beforeCounts

	busy, _, _, err := s.walCheckpointTruncate(ctx)
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result, err
	}
	if busy != 0 {
		result.SkipReason = domain.DatabaseCompactionSkipActiveWrites
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		result.SkipReason = domain.DatabaseCompactionSkipVacuumFailed
		if ctx.Err() != nil {
			result.SkipReason = domain.DatabaseCompactionSkipCanceled
		}
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result, err
	}
	if databaseCompactAfterVacuum != nil {
		if err := databaseCompactAfterVacuum(ctx, s); err != nil {
			return s.rollbackCompaction(ctx, result, err, started)
		}
	}
	integrity, err := s.integrityCheck(ctx)
	if err != nil {
		return s.rollbackCompaction(ctx, result, err, started)
	}
	result.IntegrityCheck = integrity
	if !strings.EqualFold(integrity, "ok") {
		return s.rollbackCompaction(ctx, result,
			fmt.Errorf("integrity_check returned %q", integrity), started)
	}
	afterCounts, err := s.countTablesForCompaction(ctx, "main")
	if err != nil {
		return s.rollbackCompaction(ctx, result, err, started)
	}
	result.AfterRowCounts = afterCounts
	if mismatch := compareRowCounts(beforeCounts, afterCounts); mismatch != "" {
		return s.rollbackCompaction(ctx, result,
			fmt.Errorf("row counts changed across compaction: %s", mismatch), started)
	}
	result.AfterBytes = fileSize(s.path)
	result.WALAfterBytes = fileSize(s.path + "-wal")
	result.FreedBytes = max(0, result.BeforeBytes-result.AfterBytes)
	result.RestartRequired = false
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// rollbackCompaction restores the pre-compaction tables from the backup after a
// failed verification. The backup is always kept, so a failed restore still
// leaves the owner a recoverable snapshot.
func (s *Store) rollbackCompaction(ctx context.Context, result domain.DatabaseCompactionResult, cause error, started time.Time) (domain.DatabaseCompactionResult, error) {
	result.SkipReason = domain.DatabaseCompactionSkipIntegrity
	if strings.Contains(cause.Error(), "row counts") {
		result.SkipReason = domain.DatabaseCompactionSkipRowCount
	}
	result.Error = cause.Error()
	if result.BackupPath != "" {
		restoreCtx := context.WithoutCancel(ctx)
		if restoreErr := s.restoreTablesFromBackup(restoreCtx, result.BackupPath); restoreErr != nil {
			result.RestoreError = restoreErr.Error()
		} else {
			result.Restored = true
		}
	}
	result.AfterBytes = fileSize(s.path)
	result.FreedBytes = max(0, result.BeforeBytes-result.AfterBytes)
	result.DurationMS = time.Since(started).Milliseconds()
	return result, cause
}

// backupDatabaseForCompaction writes a consistent snapshot to a fresh
// backups/database-compact-* directory and returns the database file path.
func (s *Store) backupDatabaseForCompaction(ctx context.Context) (string, error) {
	if s.path == "" || isMemoryDatabase(s.path) {
		return "", errors.New("compaction requires a file-backed database")
	}
	root := filepath.Join(filepath.Dir(s.path), "backups")
	base := filepath.Join(root, "database-compact-"+time.Now().UTC().Format("20060102-150405"))
	target := base
	for attempt := 2; ; attempt++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		target = fmt.Sprintf("%s-%d", base, attempt)
		if attempt > 100 {
			return "", fmt.Errorf("could not find a free compaction backup directory under %s", root)
		}
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("create compaction backup directory: %w", err)
	}
	backupPath := filepath.Join(target, "aha2.db")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, backupPath); err != nil {
		return "", fmt.Errorf("backup database before compaction: %w", err)
	}
	if err := os.Chmod(backupPath, 0o600); err != nil {
		return "", err
	}
	return backupPath, nil
}

// walCheckpointTruncate flushes and truncates the WAL. busy is non-zero when
// another connection holds a read or write transaction; the caller then refuses
// to rebuild.
func (s *Store) walCheckpointTruncate(ctx context.Context) (busy, logFrames, checkpointed int64, err error) {
	err = s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed)
	return busy, logFrames, checkpointed, err
}

func (s *Store) integrityCheck(ctx context.Context) (string, error) {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return "", fmt.Errorf("integrity_check: %w", err)
	}
	return result, nil
}

// countTablesForCompaction counts the key tables in the given schema ("main"
// or an attached backup) on one dedicated connection.
func (s *Store) countTablesForCompaction(ctx context.Context, schema string) (map[string]int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	counts := map[string]int64{}
	for _, table := range databaseCompactRowCountTables {
		var exists int
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info(?)`, table).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			continue
		}
		var count int64
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+quoteIdentifier(schema)+`.`+quoteIdentifier(table)).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	return counts, nil
}

func compareRowCounts(before, after map[string]int64) string {
	for table, count := range before {
		if after[table] != count {
			return fmt.Sprintf("%s before=%d after=%d", table, count, after[table])
		}
	}
	for table, count := range after {
		if _, ok := before[table]; !ok && count != 0 {
			return fmt.Sprintf("%s appeared with %d rows", table, count)
		}
	}
	return ""
}

// restoreTablesFromBackup copies every table from the attached backup back into
// main, deferring foreign-key checks to COMMIT. It never deletes the backup.
func (s *Store) restoreTablesFromBackup(ctx context.Context, backupPath string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS compact_backup`, backupPath); err != nil {
		return fmt.Errorf("attach compaction backup: %w", err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE compact_backup`)
	rows, err := conn.QueryContext(ctx,
		`SELECT name FROM main.sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Turn enforcement off only on this dedicated connection and only for the
	// restore transaction. The matching ON runs even when the restore fails or
	// the context is canceled so the pooled connection is never reused with
	// foreign keys disabled.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys=ON`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range tables {
		quoted := quoteIdentifier(table)
		if _, err := tx.ExecContext(ctx, `DELETE FROM main.`+quoted); err != nil {
			return fmt.Errorf("restore %s: %w", table, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO main.`+quoted+` SELECT * FROM compact_backup.`+quoted); err != nil {
			return fmt.Errorf("restore %s rows: %w", table, err)
		}
	}
	return tx.Commit()
}
