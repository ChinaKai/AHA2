package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func openTestPoolPair(t *testing.T) (*Store, *Store) {
	t.Helper()
	ctx := context.Background()
	web, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = web.Close() })
	syncStore, err := web.OpenSecondary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syncStore.Close() })
	return web, syncStore
}

func assertPoolPragmas(t *testing.T, label string, database *sql.DB) {
	t.Helper()
	var journal string
	if err := database.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("%s journal_mode: %v", label, err)
	}
	if journal != "wal" {
		t.Fatalf("%s journal_mode = %q, want wal", label, journal)
	}
	var foreignKeys int
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("%s foreign_keys: %v", label, err)
	}
	if foreignKeys != 1 {
		t.Fatalf("%s foreign_keys = %d, want 1", label, foreignKeys)
	}
	var busyTimeout int
	if err := database.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("%s busy_timeout: %v", label, err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("%s busy_timeout = %d, want >= 5000", label, busyTimeout)
	}
}

func TestOpenSecondarySharesFileAndPragmas(t *testing.T) {
	ctx := context.Background()
	web, syncStore := openTestPoolPair(t)
	assertPoolPragmas(t, "web", web.db)
	assertPoolPragmas(t, "sync", syncStore.db)

	now := time.Now().UTC()
	if err := syncStore.PutSyncSettings(ctx, domain.SyncSettings{
		Scope: "default", Enabled: true, Endpoint: "https://sync.example", DeviceID: "device-1",
		DeviceName: "device-1", IntervalSeconds: 300, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := web.SyncSettings(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || settings.DeviceID != "device-1" {
		t.Fatalf("settings written through the secondary pool are not visible to the primary pool: %#v", settings)
	}
}

// TestSharedPoolReadBlocksBehindLongTransaction reproduces the pre-fix
// topology: Web and sync shared one pool with one connection. A sync phase
// that keeps that connection (represented here by a long read transaction)
// makes an ordinary Web read wait for the whole phase. The following tests
// assert the dedicated-pool behavior that replaces it.
func TestSharedPoolReadBlocksBehindLongTransaction(t *testing.T) {
	ctx := context.Background()
	shared, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shared.Close() })
	// The legacy server used exactly one connection for Web and sync.
	shared.db.SetMaxOpenConns(1)
	shared.db.SetMaxIdleConns(1)

	tx, err := shared.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&count); err != nil {
		t.Fatal(err)
	}

	const hold = 700 * time.Millisecond
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(hold)
		_ = tx.Rollback()
	}()

	started := time.Now()
	if err := shared.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	<-released
	t.Logf("legacy single-pool Web read waited %v behind a %v sync transaction", elapsed.Round(time.Millisecond), hold)
	if elapsed < hold/2 {
		t.Fatalf("legacy single-pool read returned after %v; expected it to wait behind the %v transaction", elapsed, hold)
	}
}

func TestWebReadNotBlockedBySyncReadTransaction(t *testing.T) {
	ctx := context.Background()
	web, syncStore := openTestPoolPair(t)

	tx, err := syncStore.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// The transaction keeps its read snapshot and the sync pool's only
	// connection checked out, standing in for a long sync export/pull phase.
	time.Sleep(900 * time.Millisecond)

	started := time.Now()
	if err := web.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	t.Logf("dedicated sync pool: Web read took %v while the sync read transaction was held", elapsed.Round(time.Millisecond))
	if elapsed > 400*time.Millisecond {
		t.Fatalf("web read waited for the sync read transaction: %v", elapsed)
	}
}

func TestWebReadNotBlockedBySyncWriteTransaction(t *testing.T) {
	ctx := context.Background()
	web, syncStore := openTestPoolPair(t)
	if _, err := web.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS secondary_hold(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	tx, err := syncStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO secondary_hold(id,value) VALUES(1,'held')`); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(900 * time.Millisecond)
		_ = tx.Commit()
	}()

	started := time.Now()
	var count int
	if err := web.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM secondary_hold`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	<-released
	if count != 0 {
		t.Fatalf("WAL reader must not see an uncommitted write, got count=%d", count)
	}
	t.Logf("dedicated sync pool: Web read took %v while a sync write transaction was open", elapsed.Round(time.Millisecond))
	if elapsed > 400*time.Millisecond {
		t.Fatalf("web read waited for the sync write transaction: %v", elapsed)
	}
}

// TestConcurrentWebReadsDuringSyncWriteTransaction covers the reported
// "whole site queues" symptom: several Web reads run while the sync pool holds
// a write transaction. WAL readers must not wait for that writer and must not
// observe its uncommitted rows.
func TestConcurrentWebReadsDuringSyncWriteTransaction(t *testing.T) {
	ctx := context.Background()
	web, syncStore := openTestPoolPair(t)
	if _, err := web.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS concurrent_hold(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	tx, err := syncStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO concurrent_hold(id,value) VALUES(1,'held')`); err != nil {
		t.Fatal(err)
	}
	const hold = time.Second
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(hold)
		_ = tx.Commit()
	}()

	const readers = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	latencies := make([]time.Duration, readers)
	readErrs := make([]error, readers)
	counts := make([]int, readers)
	started := time.Now()
	for index := 0; index < readers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			readStarted := time.Now()
			readErrs[index] = web.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM concurrent_hold`).Scan(&counts[index])
			latencies[index] = time.Since(readStarted)
		}(index)
	}
	close(start)
	wg.Wait()
	total := time.Since(started)
	<-released

	for index := range readers {
		if readErrs[index] != nil {
			t.Fatalf("reader %d failed while sync held a write transaction: %v", index, readErrs[index])
		}
		if counts[index] != 0 {
			t.Fatalf("reader %d saw uncommitted sync row: count=%d", index, counts[index])
		}
		if latencies[index] > hold/2 {
			t.Fatalf("reader %d waited %v behind the sync write transaction", index, latencies[index])
		}
	}
	var slowest time.Duration
	for _, latency := range latencies {
		if latency > slowest {
			slowest = latency
		}
	}
	t.Logf("%d concurrent Web reads finished in %v (slowest %v) while a sync write transaction was open for %v", readers, total.Round(time.Millisecond), slowest.Round(time.Millisecond), hold)
	if total >= hold {
		t.Fatalf("%d concurrent web reads took %v, matching the sync transaction duration %v", readers, total, hold)
	}
}

// TestWebReadsStayAvailableWhileWebWriteWaitsForSync reproduces the remaining
// single-connection hazard: with sync holding the write lock, a Web write can
// wait up to busy_timeout. If that write owned the only Web connection, every
// read behind it would queue for the same window. The bounded Web pool must
// keep other connections available for reads.
func TestWebReadsStayAvailableWhileWebWriteWaitsForSync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	web, syncStore := openTestPoolPair(t)
	if _, err := web.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS web_wait_hold(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	tx, err := syncStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO web_wait_hold(id,value) VALUES(1,'sync')`); err != nil {
		t.Fatal(err)
	}
	const hold = 1200 * time.Millisecond
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(hold)
		_ = tx.Commit()
	}()

	webWriteDone := make(chan error, 1)
	go func() {
		_, err := web.db.ExecContext(ctx, `INSERT INTO web_wait_hold(id,value) VALUES(2,'web')`)
		webWriteDone <- err
	}()
	// Give the Web write time to reach SQLite and start waiting on the writer.
	time.Sleep(200 * time.Millisecond)

	const readers = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	latencies := make([]time.Duration, readers)
	readErrs := make([]error, readers)
	started := time.Now()
	for index := 0; index < readers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			readStarted := time.Now()
			var count int
			readErrs[index] = web.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_wait_hold`).Scan(&count)
			latencies[index] = time.Since(readStarted)
		}(index)
	}
	close(start)
	wg.Wait()
	total := time.Since(started)

	var slowest time.Duration
	for index := range readers {
		if readErrs[index] != nil {
			t.Fatalf("reader %d failed while a Web write waited for sync: %v", index, readErrs[index])
		}
		if latencies[index] > slowest {
			slowest = latencies[index]
		}
	}
	t.Logf("%d Web reads finished in %v (slowest %v) while a Web write waited for the sync writer", readers, total.Round(time.Millisecond), slowest.Round(time.Millisecond))
	if slowest > hold/2 {
		t.Fatalf("a Web read waited %v behind the blocked Web write (sync hold %v)", slowest, hold)
	}

	if err := <-webWriteDone; err != nil {
		t.Fatalf("Web write did not recover after sync released the writer lock: %v", err)
	}
	<-released
	var count int
	if err := web.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_wait_hold`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("row count after sync and Web writes = %d, want 2", count)
	}
}

func TestConcurrentCrossPoolReadModifyWrite(t *testing.T) {
	ctx := context.Background()
	web, syncStore := openTestPoolPair(t)
	if _, err := web.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS secondary_counter(id INTEGER PRIMARY KEY CHECK(id=1), value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := web.db.ExecContext(ctx, `INSERT INTO secondary_counter(id,value) VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}

	const workers = 16
	const perWorker = 5
	var failures atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		database := web.db
		if index%2 == 1 {
			database = syncStore.db
		}
		wg.Add(1)
		go func(database *sql.DB) {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < perWorker; attempt++ {
				err := RetryBusy(ctx, 6, 20*time.Millisecond, nil, func(runCtx context.Context) error {
					tx, err := database.BeginTx(runCtx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					var value int
					if err := tx.QueryRowContext(runCtx, `SELECT value FROM secondary_counter WHERE id=1`).Scan(&value); err != nil {
						return err
					}
					if _, err := tx.ExecContext(runCtx, `UPDATE secondary_counter SET value=? WHERE id=1`, value+1); err != nil {
						return err
					}
					return tx.Commit()
				})
				if err != nil {
					t.Errorf("cross-pool read-modify-write failed: %v", err)
					failures.Add(1)
					return
				}
			}
		}(database)
	}
	close(start)
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d workers failed", failures.Load())
	}

	var value int
	if err := web.db.QueryRowContext(ctx, `SELECT value FROM secondary_counter WHERE id=1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if want := workers * perWorker; value != want {
		t.Fatalf("counter = %d, want %d (lost update)", value, want)
	}
}

func TestOpenSecondaryRepeatedAndCloseOrder(t *testing.T) {
	ctx := context.Background()
	web, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = web.Close()
		}
	})

	first, err := web.OpenSecondary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := first.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", IntervalSeconds: 300, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := web.OpenSecondary(ctx)
	if err != nil {
		t.Fatalf("reopening a secondary pool after close failed: %v", err)
	}
	settings, err := second.SyncSettings(ctx, "default")
	if err != nil || settings.IntervalSeconds != 300 {
		t.Fatalf("settings after reopen = %#v, err=%v", settings, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := web.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	// t.TempDir cleanup fails on Windows if either pool still holds a file
	// handle, so a successful test run also asserts no leak.
}

func TestOpenSecondaryRejectsMemoryDatabase(t *testing.T) {
	web, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer web.Close()
	if _, err := web.OpenSecondary(context.Background()); err == nil {
		t.Fatal("expected an in-memory database to be rejected as a secondary pool target")
	}
}

func TestRetryBusy(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	err := RetryBusy(ctx, 4, time.Millisecond, nil, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("busy retry: err=%v attempts=%d, want nil/3", err, attempts)
	}

	attempts = 0
	plain := errors.New("constraint failed")
	err = RetryBusy(ctx, 4, time.Millisecond, nil, func(context.Context) error {
		attempts++
		return plain
	})
	if !errors.Is(err, plain) || attempts != 1 {
		t.Fatalf("non-busy error must not be retried: err=%v attempts=%d", err, attempts)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	attempts = 0
	err = RetryBusy(cancelCtx, 4, time.Second, nil, func(context.Context) error {
		attempts++
		cancel()
		return errors.New("SQLITE_BUSY")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("cancellation must stop the backoff: err=%v attempts=%d", err, attempts)
	}
}
