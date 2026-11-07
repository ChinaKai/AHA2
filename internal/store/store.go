package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sqlite "modernc.org/sqlite"
)

// sqliteConnectionOptions are applied to every connection created by
// database/sql, not just the first one:
//
//   - busy_timeout makes writes wait for SQLite's single writer instead of
//     failing immediately when another connection (for example the dedicated
//     sync worker connection) is committing.
//   - foreign_keys must be set per connection; the PRAGMA in the initial schema
//     only covers the connection that ran the migration.
//   - _txlock=immediate starts write transactions with BEGIN IMMEDIATE. A
//     read-modify-write transaction must take the write lock before reading:
//     with the default deferred BEGIN, a read snapshot upgraded after another
//     connection committed fails with SQLITE_BUSY_SNAPSHOT without consulting
//     busy_timeout. database/sql read-only transactions still use deferred
//     BEGIN (the driver skips _txlock for ReadOnly).
//
// All pools that open the same database file use the same options so the file
// is never reopened with inconsistent connection pragmas. journal_mode=WAL is
// persisted in the database file by the initial schema; it is not a per
// connection setting.
const sqliteConnectionOptions = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"

// webPoolConnections bounds the Web API pool. The pool is bounded on purpose:
// SQLite allows one writer, so more connections do not increase write
// throughput, and a read request must still be able to run while a write waits
// out SQLite's busy timeout. In-memory databases are pinned to one connection
// because every connection would otherwise get a separate database.
const webPoolConnections = 4

// syncPoolConnections keeps scheduled sync serialized on one connection. Its
// isolation from Web traffic comes from using a second pool, not from
// parallelism inside sync itself.
const syncPoolConnections = 1

type Store struct {
	db      *sql.DB
	dataDir string
	path    string

	statusMu       sync.RWMutex
	statusListener TaskStatusListener

	databaseStats databaseStatsCache
}

func (s *Store) DataDir() string {
	if s == nil {
		return ""
	}
	return s.dataDir
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := openSQLiteDB(path)
	if err != nil {
		return nil, err
	}
	result := &Store{db: db, dataDir: filepath.Dir(path), path: path}
	if err := result.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

func openSQLiteDB(path string) (*sql.DB, error) {
	return openSQLitePool(path, webPoolConnections)
}

func openSQLitePool(path string, maxConnections int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if isMemoryDatabase(path) || maxConnections < 1 {
		maxConnections = 1
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	return db, nil
}

func sqliteDSN(path string) string {
	if strings.ContainsRune(path, '?') {
		return path + "&" + sqliteConnectionOptions
	}
	return path + "?" + sqliteConnectionOptions
}

// OpenSecondary opens an independent single-connection pool for the same
// SQLite file. The periodic sync worker uses it so its long export/pull phases
// cannot occupy the Web API pool. The pools share WAL, busy_timeout and
// foreign_keys settings; they do not share transactions.
//
// The caller owns the returned Store and must close it; closing it does not
// close the Store it was opened from. Migrations are not re-run because the
// primary Store has already opened the file.
func (s *Store) OpenSecondary(ctx context.Context) (*Store, error) {
	if s == nil || s.path == "" {
		return nil, errors.New("secondary store requires an open file-backed database")
	}
	if isMemoryDatabase(s.path) {
		return nil, errors.New("secondary store requires a file-backed database")
	}
	db, err := openSQLitePool(s.path, syncPoolConnections)
	if err != nil {
		return nil, err
	}
	secondary := &Store{db: db, dataDir: s.dataDir, path: s.path}
	var journal string
	var foreignKeys, busyTimeout int
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		db.Close()
		return nil, fmt.Errorf("read secondary journal_mode: %w", err)
	}
	if !strings.EqualFold(journal, "wal") {
		db.Close()
		return nil, fmt.Errorf("secondary connection requires WAL, database is in %q mode", journal)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		db.Close()
		return nil, fmt.Errorf("read secondary foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		db.Close()
		return nil, errors.New("secondary connection did not enable foreign_keys")
	}
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		db.Close()
		return nil, fmt.Errorf("read secondary busy_timeout: %w", err)
	}
	if busyTimeout < 5000 {
		db.Close()
		return nil, fmt.Errorf("secondary connection busy_timeout is %dms, want at least 5000ms", busyTimeout)
	}
	return secondary, nil
}

func isMemoryDatabase(path string) bool {
	return path == ":memory:" ||
		strings.HasPrefix(path, "file::memory:") ||
		strings.Contains(path, "mode=memory")
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// PoolStats reports connection pool counters for this Store. It is used by the
// slow-request log to distinguish time spent waiting for a connection from time
// spent executing the query.
func (s *Store) PoolStats() sql.DBStats {
	if s == nil || s.db == nil {
		return sql.DBStats{}
	}
	return s.db.Stats()
}

// IsSQLiteBusy reports whether err is a SQLite busy/locked result, including
// the extended result codes (for example SQLITE_BUSY_SNAPSHOT).
func IsSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5: // SQLITE_BUSY
			return true
		case 6: // SQLITE_LOCKED
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
}

// RetryBusy retries fn while it fails with a SQLite busy/locked error. It is
// intended for background work (the sync worker) whose writes may briefly
// contend with the Web pool's writer. Context cancellation stops the wait and
// returns the last error.
func RetryBusy(ctx context.Context, attempts int, base time.Duration, onRetry func(attempt int, err error), fn func(context.Context) error) error {
	if attempts < 1 {
		attempts = 1
	}
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = fn(ctx)
		if err == nil || !IsSQLiteBusy(err) {
			return err
		}
		if attempt == attempts {
			break
		}
		if onRetry != nil {
			onRetry(attempt, err)
		}
		delay := base * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
	return err
}

func encodeJSON(value any) string {
	if value == nil {
		return "{}"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeJSON[T any](value string, fallback T) T {
	if value == "" {
		return fallback
	}
	var result T
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return fallback
	}
	return result
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	result, _ := time.Parse(time.RFC3339Nano, value)
	return result
}

func unixMilli(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}
