package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// databaseDiskFree is injectable for tests that exercise the offline
// compaction disk-space precheck without filling a filesystem.
var databaseDiskFree = diskSpaceFree

// databaseCompactRowCountTables are the tables whose counts must survive an
// offline VACUUM. Missing tables are ignored so older databases still compact.
var databaseCompactRowCountTables = []string{
	"projects", "tasks", "task_rounds", "turns", "messages", "conversation_items",
	"events", "audit_events", "attachments", "knowledge_entries", "channel_instances",
	"data_retention_settings",
}

// sqlExecer is *sql.DB or *sql.Conn. Cleanup backups run through the ordinary
// pool; offline compaction uses its exclusive-lock connection.
type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// backupDatabaseTo writes a consistent VACUUM INTO snapshot under
// <dataDir>/backups/<prefix>-<timestamp>/aha2.db and returns its path.
func (s *Store) backupDatabaseTo(ctx context.Context, exec sqlExecer, prefix string) (string, error) {
	if s.path == "" || isMemoryDatabase(s.path) {
		return "", errors.New("cleanup backup requires a file-backed database")
	}
	root := filepath.Join(filepath.Dir(s.path), "backups")
	base := filepath.Join(root, prefix+"-"+time.Now().UTC().Format("20060102-150405"))
	target := base
	for attempt := 2; ; attempt++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		target = fmt.Sprintf("%s-%d", base, attempt)
		if attempt > 100 {
			return "", fmt.Errorf("could not find a free %s backup directory under %s", prefix, root)
		}
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("create %s backup directory: %w", prefix, err)
	}
	backupPath := filepath.Join(target, "aha2.db")
	if _, err := exec.ExecContext(ctx, `VACUUM INTO ?`, backupPath); err != nil {
		return "", fmt.Errorf("backup database before %s: %w", prefix, err)
	}
	if err := os.Chmod(backupPath, 0o600); err != nil {
		return "", err
	}
	return backupPath, nil
}

// pruneOldCleanupBackups keeps only the newest safety snapshot for prefix. It
// is best effort: a locked old directory must not fail the cleanup that just
// completed its backup.
func (s *Store) pruneOldCleanupBackups(prefix, keepPath string) {
	if s.path == "" || isMemoryDatabase(s.path) {
		return
	}
	root := filepath.Join(filepath.Dir(s.path), "backups")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	keepDir := filepath.Dir(keepPath)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix+"-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if path == keepDir {
			continue
		}
		_ = os.RemoveAll(path)
	}
}
