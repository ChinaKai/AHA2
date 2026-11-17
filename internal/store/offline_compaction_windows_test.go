//go:build windows

package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestOfflineCompactionLargeDatabaseWindows is the Windows-only check for the
// LockFileEx/PENDING_BYTE risk described in docs/offline-database-compaction.md.
// Linux tests cannot exercise it. Run on a Windows host with at least ~4 GiB of
// free temp space:
//
//	set AHA2_WINDOWS_LARGE_DB_TEST=1
//	go test ./internal/store -run TestOfflineCompactionLargeDatabaseWindows -v -timeout 30m
func TestOfflineCompactionLargeDatabaseWindows(t *testing.T) {
	if os.Getenv("AHA2_WINDOWS_LARGE_DB_TEST") != "1" {
		t.Skip("set AHA2_WINDOWS_LARGE_DB_TEST=1 to run the >=1 GiB Windows backup test")
	}
	ctx := context.Background()
	dataDir := t.TempDir()
	databasePath := filepath.Join(dataDir, "aha2.db")
	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.db.ExecContext(ctx, `CREATE TABLE large_payload(id INTEGER PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	statement, err := database.db.PrepareContext(ctx, `INSERT INTO large_payload(data) VALUES(?)`)
	if err != nil {
		t.Fatal(err)
	}
	blob := bytes.Repeat([]byte{0x5a}, 8<<20)
	for index := 0; index < 140; index++ {
		if _, err := statement.ExecContext(ctx, blob); err != nil {
			statement.Close()
			t.Fatal(err)
		}
	}
	statement.Close()
	if _, err := database.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 1<<30 {
		t.Fatalf("fixture database is only %d bytes; want > 1 GiB", info.Size())
	}

	started := time.Now()
	result, err := CompactDatabaseOffline(ctx, OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil {
		t.Fatalf("large-database offline compaction: %v result=%#v", err, result)
	}
	if result.Status != domain.OfflineCompactionStatusOK || !result.IntegrityOK || !result.RowCountsOK {
		t.Fatalf("result=%#v", result)
	}
	if result.BackupPath == "" || len(result.BackupFiles) != 1 || result.BackupFiles[0].Bytes <= 1<<30 {
		t.Fatalf("backup did not contain the >1 GiB snapshot: %#v", result)
	}
	t.Logf("compacted %d bytes in %s; backup=%s", result.BeforeBytes, time.Since(started), result.BackupPath)
}
