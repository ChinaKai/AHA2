package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/instancelock"
)

func openOfflineCompactionFixture(t *testing.T) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	database, err := Open(context.Background(), filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	for index := 0; index < 32; index++ {
		seedConversationItem(t, database, task.ID, "tool", "agent_command_finished", now)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return dataDir, task.ID
}

func TestOfflineCompactionBacksUpCompactsVerifiesAndIsRepeatable(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	first, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil || first.Status != domain.OfflineCompactionStatusOK {
		t.Fatalf("first compaction=%#v err=%v", first, err)
	}
	if first.BackupPath == "" || !first.BackupKept || len(first.BackupFiles) == 0 {
		t.Fatalf("backup missing from result: %#v", first)
	}
	if first.IntegrityCheck != "ok" || !first.IntegrityOK || !first.RowCountsOK {
		t.Fatalf("verification failed: %#v", first)
	}
	if first.BeforeRowCounts["conversation_items"] != 32 || first.AfterRowCounts["conversation_items"] != 32 {
		t.Fatalf("row counts=%#v -> %#v", first.BeforeRowCounts, first.AfterRowCounts)
	}
	for _, file := range first.BackupFiles {
		info, statErr := os.Stat(file.Path)
		if statErr != nil {
			t.Fatalf("backup file missing: %v", statErr)
		}
		if info.Size() != file.Bytes || file.SHA256 == "" {
			t.Fatalf("backup file metadata=%#v stat=%d", file, info.Size())
		}
	}
	if !strings.HasPrefix(first.BackupPath, filepath.Join(dataDir, "backups")+string(os.PathSeparator)) {
		t.Fatalf("backup path %q is not under the data directory's backups", first.BackupPath)
	}

	// Restore the pre-compaction file set into an empty directory and prove the
	// copy is usable, not only hash-identical.
	restoreDir := t.TempDir()
	for _, file := range first.BackupFiles {
		encoded, readErr := os.ReadFile(file.Path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(restoreDir, file.Name), encoded, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	restored, err := sql.Open("sqlite", filepath.Join(restoreDir, "aha2.db")+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	var restoredTasks int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&restoredTasks); err != nil || restoredTasks != 1 {
		t.Fatalf("restored tasks=%d err=%v", restoredTasks, err)
	}
	restored.Close()

	second, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil || second.Status != domain.OfflineCompactionStatusOK || second.BackupPath == "" {
		t.Fatalf("second compaction=%#v err=%v", second, err)
	}
	if first.BackupPath == second.BackupPath {
		t.Fatalf("second run overwrote the first backup: %s", first.BackupPath)
	}
	if _, statErr := os.Stat(first.BackupPath); statErr != nil {
		t.Fatalf("first backup was removed by the second run: %v", statErr)
	}
}

func TestOfflineCompactionRefusesWhenServiceLockIsHeld(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	lock, err := instancelock.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil {
		t.Fatalf("service-running refusal surfaced as an error: %v", err)
	}
	if result.Status != domain.OfflineCompactionStatusRefused || result.Reason != domain.OfflineCompactionReasonServiceRunning {
		t.Fatalf("result=%#v", result)
	}
	if result.BackupPath != "" {
		t.Fatalf("refusal created a backup: %s", result.BackupPath)
	}
}

func TestOfflineCompactionRefusesLowDiskBeforeBackup(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	original := databaseDiskFree
	databaseDiskFree = func(string) (int64, error) { return 1, nil }
	t.Cleanup(func() { databaseDiskFree = original })

	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil {
		t.Fatalf("low-disk refusal surfaced as an error: %v", err)
	}
	if result.Status != domain.OfflineCompactionStatusRefused || result.Reason != domain.OfflineCompactionReasonDiskSpace {
		t.Fatalf("result=%#v", result)
	}
	if result.BackupPath != "" {
		t.Fatalf("low-disk refusal created a backup: %s", result.BackupPath)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "backups")); !os.IsNotExist(statErr) {
		t.Fatalf("backup directory exists after low-disk refusal: %v", statErr)
	}
}

func TestOfflineCompactionHoldsExclusiveDatabaseLock(t *testing.T) {
	dataDir, taskID := openOfflineCompactionFixture(t)
	external, err := sql.Open("sqlite", filepath.Join(dataDir, "aha2.db")+"?_pragma=busy_timeout(150)")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	var writeErr error
	original := offlineCompactAfterVacuum
	offlineCompactAfterVacuum = func(ctx context.Context, _ *sql.Conn) error {
		_, writeErr = external.ExecContext(ctx, `
			INSERT INTO conversation_items(
				id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
				category,kind,summary,payload_json,created_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			domain.NewID("conversation"), taskID, "", "", "main", "main", "main", "main", "",
			"tool", "agent_command_finished", "must not commit", "{}", timeString(time.Now().UTC()))
		return nil
	}
	t.Cleanup(func() { offlineCompactAfterVacuum = original })

	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil || result.Status != domain.OfflineCompactionStatusOK {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if writeErr == nil || !IsSQLiteBusy(writeErr) {
		t.Fatalf("external write was not blocked by the exclusive lock: %v", writeErr)
	}
}

func TestOfflineCompactionRefusesWhenDatabaseIsLocked(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	external, err := sql.Open("sqlite", filepath.Join(dataDir, "aha2.db")+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	conn, err := external.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)

	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err != nil {
		t.Fatalf("locked-database refusal surfaced as an error: %v", err)
	}
	if result.Status != domain.OfflineCompactionStatusRefused || result.Reason != domain.OfflineCompactionReasonDatabaseLocked {
		t.Fatalf("result=%#v", result)
	}
	if result.BackupPath != "" {
		t.Fatalf("locked-database refusal created a backup: %s", result.BackupPath)
	}
}

func TestOfflineCompactionBackupFailureKeepsDatabaseIntact(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	blockedRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{
		DataDir: dataDir, BackupDir: blockedRoot, KeepBackup: true,
	})
	if err == nil {
		t.Fatal("expected the backup failure to be reported")
	}
	if result.Status != domain.OfflineCompactionStatusFailed || result.Reason != domain.OfflineCompactionReasonBackupFailed {
		t.Fatalf("result=%#v", result)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "aha2.db")); statErr != nil {
		t.Fatalf("database missing after backup failure: %v", statErr)
	}
}

func TestOfflineCompactionIntegrityFailureKeepsBackupAndDatabase(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	original := offlineCompactIntegrityCheck
	offlineCompactIntegrityCheck = func(context.Context, *sql.Conn) (string, error) { return "corrupted", nil }
	t.Cleanup(func() { offlineCompactIntegrityCheck = original })

	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err == nil {
		t.Fatal("expected integrity failure")
	}
	if result.Status != domain.OfflineCompactionStatusFailed || result.Reason != domain.OfflineCompactionReasonIntegrityFailed {
		t.Fatalf("result=%#v", result)
	}
	if result.BackupPath == "" {
		t.Fatal("integrity failure did not keep the pre-compaction backup")
	}
	if _, statErr := os.Stat(result.BackupPath); statErr != nil {
		t.Fatalf("backup missing after integrity failure: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "aha2.db")); statErr != nil {
		t.Fatalf("database missing after integrity failure: %v", statErr)
	}
}

func TestOfflineCompactionRowCountMismatchKeepsBackupAndDatabase(t *testing.T) {
	dataDir, taskID := openOfflineCompactionFixture(t)
	original := offlineCompactAfterVacuum
	offlineCompactAfterVacuum = func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `
			INSERT INTO conversation_items(
				id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
				category,kind,summary,payload_json,created_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			domain.NewID("conversation"), taskID, "", "", "main", "main", "main", "main", "",
			"tool", "agent_command_finished", "injected", "{}", timeString(time.Now().UTC()))
		return err
	}
	t.Cleanup(func() { offlineCompactAfterVacuum = original })

	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true})
	if err == nil {
		t.Fatal("expected row count mismatch")
	}
	if result.Status != domain.OfflineCompactionStatusFailed || result.Reason != domain.OfflineCompactionReasonRowCountMismatch {
		t.Fatalf("result=%#v", result)
	}
	if result.BackupPath == "" {
		t.Fatal("row count mismatch did not keep the pre-compaction backup")
	}
	if _, statErr := os.Stat(result.BackupPath); statErr != nil {
		t.Fatalf("backup missing after row count mismatch: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "aha2.db")); statErr != nil {
		t.Fatalf("database missing after row count mismatch: %v", statErr)
	}
}

func TestOfflineCompactionUsesCustomBackupDirectory(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	backupRoot := filepath.Join(t.TempDir(), "custom-backups")
	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{
		DataDir: dataDir, BackupDir: backupRoot, KeepBackup: true,
	})
	if err != nil || result.Status != domain.OfflineCompactionStatusOK {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !strings.HasPrefix(result.BackupPath, backupRoot+string(os.PathSeparator)) {
		t.Fatalf("backup path %q is not under %q", result.BackupPath, backupRoot)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "backups")); !os.IsNotExist(statErr) {
		t.Fatalf("default backup directory was created despite --backup-dir: %v", statErr)
	}
}

func TestOfflineCompactionDeletesBackupOnlyWhenExplicitlyDisabled(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	result, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: false})
	if err != nil || result.Status != domain.OfflineCompactionStatusOK {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if result.BackupPath == "" || result.BackupKept {
		t.Fatalf("backup deletion was not reflected: %#v", result)
	}
	if _, statErr := os.Stat(result.BackupPath); !os.IsNotExist(statErr) {
		t.Fatalf("backup still exists: %v", statErr)
	}
}

func TestOfflineCompactionInfoReadsSizesCommandAndLatestResult(t *testing.T) {
	dataDir, _ := openOfflineCompactionFixture(t)
	if _, err := CompactDatabaseOffline(context.Background(), OfflineCompactionOptions{DataDir: dataDir, KeepBackup: true}); err != nil {
		t.Fatal(err)
	}
	info, err := OfflineCompactionInfoFor(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.DatabaseExists || info.FileBytes == 0 || info.RequiredBytes <= info.FileBytes {
		t.Fatalf("info=%#v", info)
	}
	if !strings.Contains(info.Command, "aha2 db compact") || !strings.Contains(info.Command, dataDir) {
		t.Fatalf("command=%q", info.Command)
	}
	if info.LatestResult == nil || info.LatestResult.Status != domain.OfflineCompactionStatusOK {
		t.Fatalf("latest=%#v", info.LatestResult)
	}
	if info.LatestResult.BackupPath == "" {
		t.Fatalf("latest result lost the backup path: %#v", info.LatestResult)
	}
}
