package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareio"
)

// HardwareIODirName is the data directory child that holds terminal IO files.
const HardwareIODirName = "hardware-io"

// HardwareIOMaxBytesEnv overrides domain.HardwareIOFileMaxBytesDefault.
const HardwareIOMaxBytesEnv = "AHA2_HARDWARE_IO_MAX_BYTES"

func (s *Store) openHardwareIO() error {
	root := filepath.Join(s.dataDir, HardwareIODirName)
	if isMemoryDatabase(s.path) {
		temp, err := os.MkdirTemp("", "aha2-hardware-io-")
		if err != nil {
			return fmt.Errorf("create hardware io directory: %w", err)
		}
		root, s.hardwareIOTemp = temp, temp
	}
	s.hardwareIO = hardwareio.Open(hardwareio.Options{Root: root, SeedSequence: s.legacyHardwareIOSequence, MaxBytes: HardwareIOMaxBytes()})
	s.ownsHardwareIO = true
	return nil
}

func (s *Store) closeHardwareIO() error {
	if !s.ownsHardwareIO || s.hardwareIO == nil {
		return nil
	}
	err := s.hardwareIO.Close()
	if s.hardwareIOTemp != "" {
		_ = os.RemoveAll(s.hardwareIOTemp)
	}
	return err
}

// HardwareIOCapacitySignal fires when an append pushes the terminal IO files
// above the disk cap, so maintenance can run before the hourly pass.
func (s *Store) HardwareIOCapacitySignal() <-chan struct{} {
	if s.hardwareIO == nil {
		return nil
	}
	return s.hardwareIO.CapacitySignal()
}

// HardwareIOFiles exposes the file store for maintenance and tests.
func (s *Store) HardwareIOFiles() *hardwareio.Store { return s.hardwareIO }

// legacyHardwareIOSequence is the largest sequence the SQLite table ever
// handed out. sqlite_sequence keeps it even after the rows are deleted, so
// new file sequences continue above every cursor issued before the switch.
func (s *Store) legacyHardwareIOSequence() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var recorded, maximum sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='hardware_io'`).Scan(&recorded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no such table") {
		return 0, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT MAX(sequence) FROM hardware_io`).Scan(&maximum)
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		return 0, err
	}
	return max(recorded.Int64, maximum.Int64), nil
}

// AppendHardwareIO records one terminal event in the file store. Nothing is
// written to SQLite.
func (s *Store) AppendHardwareIO(_ context.Context, item domain.HardwareIOEvent) (int64, error) {
	if s.hardwareIO == nil {
		return 0, errors.New("hardware io store is not open")
	}
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return s.hardwareIO.Append(hardwareio.Record{
		ID: item.ID, TaskID: item.TaskID, HardwareID: item.HardwareID, Transport: item.Transport,
		Direction: item.Direction, Data: item.Data, Encoding: item.Encoding, Source: item.Source,
		CreatedAt: timeString(createdAt),
	})
}

// HardwareIOPage keeps the SQLite-era contract: with after>0 it returns the
// next limit events in sequence order; otherwise the newest limit events.
// LatestSequence is the last returned sequence (or the stream maximum when
// nothing is returned) and HasMore reports newer events beyond the page.
func (s *Store) HardwareIOPage(
	_ context.Context,
	taskID, hardwareID, transport string,
	after int64,
	limit int,
) (domain.HardwareIOPage, error) {
	if limit < 1 {
		limit = 500
	}
	if limit > 2000 {
		limit = 2000
	}
	page := domain.HardwareIOPage{Items: make([]domain.HardwareIOEvent, 0)}
	if s.hardwareIO == nil {
		return page, errors.New("hardware io store is not open")
	}
	key := hardwareio.StreamKey{TaskID: taskID, HardwareID: hardwareID, Transport: transport}
	var records []hardwareio.Record
	var maximum int64
	var err error
	if after > 0 {
		records, maximum, err = s.hardwareIO.After(key, after, limit)
	} else {
		records, maximum, err = s.hardwareIO.Latest(key, limit)
	}
	if err != nil {
		return domain.HardwareIOPage{}, err
	}
	for _, record := range records {
		page.Items = append(page.Items, domain.HardwareIOEvent{
			Sequence: record.Sequence, ID: record.ID, TaskID: record.TaskID, HardwareID: record.HardwareID,
			Transport: record.Transport, Direction: record.Direction, Data: record.Data,
			Encoding: record.Encoding, Source: record.Source, CreatedAt: parseTime(record.CreatedAt),
		})
		page.LatestSequence = record.Sequence
	}
	if maximum > 0 {
		if page.LatestSequence == 0 {
			page.LatestSequence = maximum
		}
		page.HasMore = maximum > page.LatestSequence
	}
	return page, nil
}

// RemoveTaskHardwareIO deletes the terminal IO files of a Task. Task deletion
// used to cascade to hardware_io rows; the files need an explicit call (the
// periodic maintenance also removes streams of Tasks that no longer exist).
func (s *Store) RemoveTaskHardwareIO(taskID string) error {
	if s.hardwareIO == nil {
		return nil
	}
	_, err := s.hardwareIO.RemoveTask(taskID)
	return err
}

// LegacyHardwareIOClearResult reports the one-time removal of SQLite rows.
type LegacyHardwareIOClearResult struct {
	Rows         int64
	FreedPages   int64
	FloorSeq     int64
	DurationMS   int64
	AlreadyEmpty bool
}

// ClearLegacyHardwareIO deletes every row of the legacy hardware_io table.
// The Owner decided (task-711, 2026-11-20) that SQLite-era terminal output is
// not kept. It runs at startup before the server accepts requests.
//
// The sequence floor is recorded in the file store first, so cursors stay
// monotonic even if sqlite_sequence is later lost. The DELETE runs on its own
// single connection with foreign_keys off: hardware_io is only a child table
// (nothing references it), so no constraint can be violated, and without
// foreign key processing SQLite uses its truncate optimization, which frees
// the pages in seconds instead of deleting 12M rows one by one through the
// WAL (measured 3.5 s vs 4 m 10 s and a 4 GB WAL on a 6.8 GB copy). The table
// and its schema stay so an older binary can still open the database.
// The file does not shrink until `aha2 db compact` runs; the freed pages are
// reused by later writes.
func (s *Store) ClearLegacyHardwareIO(ctx context.Context) (LegacyHardwareIOClearResult, error) {
	started := time.Now()
	var result LegacyHardwareIOClearResult
	if s.hardwareIO != nil {
		floor, err := s.hardwareIO.LastSequence()
		if err != nil {
			return result, fmt.Errorf("record hardware io sequence floor: %w", err)
		}
		result.FloorSeq = floor
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hardware_io)`).Scan(&exists); err != nil {
		return result, err
	}
	if !exists {
		result.AlreadyEmpty = true
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}
	var before, after int64
	_ = s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&before)
	if isMemoryDatabase(s.path) {
		deleted, err := s.db.ExecContext(ctx, `DELETE FROM hardware_io`)
		if err != nil {
			return result, err
		}
		result.Rows, _ = deleted.RowsAffected()
	} else {
		pool, err := openSQLitePool(s.path, 1)
		if err != nil {
			return result, err
		}
		defer pool.Close()
		conn, err := pool.Conn(ctx)
		if err != nil {
			return result, err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			return result, err
		}
		deleted, err := conn.ExecContext(ctx, `DELETE FROM hardware_io`)
		if err != nil {
			return result, fmt.Errorf("clear legacy hardware_io: %w", err)
		}
		result.Rows, _ = deleted.RowsAffected()
		_, _ = conn.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
	}
	_ = s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&after)
	result.FreedPages = max(0, after-before)
	s.invalidateDatabaseStats()
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// HardwareIOMaxBytes returns the configured disk cap for terminal IO files.
func HardwareIOMaxBytes() int64 {
	if value := strings.TrimSpace(os.Getenv(HardwareIOMaxBytesEnv)); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= domain.HardwareIOFileMaxBytesMin {
			return parsed
		}
	}
	return domain.HardwareIOFileMaxBytesDefault
}

// RunHardwareIOMaintenance applies retention (the data management page's
// hardware_io retention days), the disk cap and Task deletion to the files.
func (s *Store) RunHardwareIOMaintenance(ctx context.Context) (hardwareio.PruneResult, error) {
	if s.hardwareIO == nil {
		return hardwareio.PruneResult{}, nil
	}
	settings, err := s.HardwareIORetentionSettings(ctx)
	if err != nil {
		return hardwareio.PruneResult{}, err
	}
	return s.hardwareIO.Prune(hardwareio.Policy{
		RetentionDays: settings.RetentionDays,
		MaxBytes:      HardwareIOMaxBytes(),
		KeepTask: func(taskID string) (bool, error) {
			var exists bool
			err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=?)`, taskID).Scan(&exists)
			return exists, err
		},
	})
}

// HardwareIOFileStats summarizes the files for the data management page.
func (s *Store) HardwareIOFileStats() (domain.HardwareIOFileStats, error) {
	result := domain.HardwareIOFileStats{MaxBytes: HardwareIOMaxBytes()}
	if s.hardwareIO == nil {
		return result, nil
	}
	stats, err := s.hardwareIO.Stats()
	if err != nil {
		return result, err
	}
	result.Directory = stats.Root
	result.Streams = stats.Streams
	result.Segments = stats.Segments
	result.Records = stats.Records
	result.Bytes = stats.Bytes
	result.LastSequence = stats.LastSequence
	result.CorruptLines = stats.CorruptLines
	result.Failures = stats.Failures.Total
	result.LastError = stats.Failures.LastError
	result.LastErrorAt = stats.Failures.LastAt
	result.PlainSegments = stats.PlainSegments
	result.StaleFiles = stats.StaleFiles
	result.OldestAt = stats.OldestAt
	result.NewestAt = stats.NewestAt
	return result, nil
}
