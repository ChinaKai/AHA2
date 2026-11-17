package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/instancelock"
)

// OfflineCompactionOptions configures one `aha2 db compact` run. The zero
// backup directory means <data-dir>/backups; KeepBackup defaults to true in the
// CLI and may be false only to delete a backup that already passed every
// verification step.
type OfflineCompactionOptions struct {
	DataDir      string
	DatabasePath string
	BackupDir    string
	KeepBackup   bool
	// Progress receives human-readable step lines. It is optional.
	Progress func(format string, args ...any)
}

// Test hooks. They never run in production because each is nil unless a test
// installs one.
var (
	offlineCompactAfterVacuum    func(context.Context, *sql.Conn) error
	offlineCompactIntegrityCheck = integrityCheck
)

var errOfflineBackupVerify = errors.New("offline compaction backup verification failed")

// CompactDatabaseOffline rebuilds a file-backed database after the AHA2
// service has stopped:
//
//	instance lock -> SQLite exclusive lock -> size/disk checks ->
//	verified DB/WAL/SHM backup -> row counts -> WAL checkpoint -> VACUUM ->
//	integrity_check -> row counts.
//
// It never migrates the schema and never starts online compaction. A refused
// run leaves the database untouched. A failed run keeps the database and the
// pre-compaction backup in place for manual recovery.
func CompactDatabaseOffline(ctx context.Context, options OfflineCompactionOptions) (result domain.OfflineCompactionResult, err error) {
	started := time.Now().UTC()
	result = domain.OfflineCompactionResult{
		Status:     domain.OfflineCompactionStatusFailed,
		KeepBackup: options.KeepBackup,
		StartedAt:  started,
	}
	progress := options.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	dataDir, absErr := filepath.Abs(strings.TrimSpace(options.DataDir))
	if absErr != nil {
		result.Error = fmt.Sprintf("resolve data directory: %v", absErr)
		return finishOfflineCompaction(result, absErr)
	}
	databasePath := strings.TrimSpace(options.DatabasePath)
	if databasePath == "" {
		databasePath = filepath.Join(dataDir, "aha2.db")
	} else if databasePath, absErr = filepath.Abs(databasePath); absErr != nil {
		result.Error = fmt.Sprintf("resolve database path: %v", absErr)
		return finishOfflineCompaction(result, absErr)
	}
	result.DataDir = dataDir
	result.DatabasePath = databasePath

	// Finish every terminal path through this defer so the last result file
	// (and the JSON the CLI prints) always reflects the real outcome.
	defer func() {
		result.FinishedAt = time.Now().UTC()
		result.DurationMS = result.FinishedAt.Sub(started).Milliseconds()
		if writeErr := writeOfflineCompactionResult(dataDir, result); writeErr != nil {
			progress("警告：写入结果文件失败：%v", writeErr)
		}
	}()

	info, statErr := os.Stat(databasePath)
	if errors.Is(statErr, os.ErrNotExist) {
		result.Reason = domain.OfflineCompactionReasonDatabaseMissing
		result.Error = fmt.Sprintf("database does not exist: %s", databasePath)
		return result, fmt.Errorf("%s", result.Error)
	}
	if statErr != nil {
		result.Reason = domain.OfflineCompactionReasonDatabaseMissing
		result.Error = fmt.Sprintf("stat database: %v", statErr)
		return result, statErr
	}
	if info.IsDir() {
		result.Reason = domain.OfflineCompactionReasonNotFileDatabase
		result.Error = fmt.Sprintf("database path is a directory: %s", databasePath)
		return result, fmt.Errorf("%s", result.Error)
	}

	progress("检查 AHA2 服务实例锁…")
	lock, lockErr := instancelock.Acquire(dataDir)
	if errors.Is(lockErr, instancelock.ErrHeld) {
		result.Status = domain.OfflineCompactionStatusRefused
		result.Reason = domain.OfflineCompactionReasonServiceRunning
		result.Error = "AHA2 服务仍在运行；请先停止服务、托盘和所有使用该数据目录的插件"
		return result, nil
	}
	if lockErr != nil {
		result.Error = lockErr.Error()
		return result, lockErr
	}
	defer lock.Close()
	progress("服务实例锁已获取。")

	progress("以独占方式打开数据库…")
	pool, poolErr := openSQLitePool(databasePath, 1)
	if poolErr != nil {
		result.Error = poolErr.Error()
		return result, poolErr
	}
	defer pool.Close()
	conn, connErr := pool.Conn(ctx)
	if connErr != nil {
		result.Error = connErr.Error()
		return result, connErr
	}
	defer conn.Close()
	if _, execErr := conn.ExecContext(ctx, `PRAGMA busy_timeout=1500`); execErr != nil {
		result.Error = execErr.Error()
		return result, execErr
	}
	if _, execErr := conn.ExecContext(ctx, `PRAGMA locking_mode=EXCLUSIVE`); execErr != nil {
		result.Error = fmt.Sprintf("set exclusive locking mode: %v", execErr)
		return result, execErr
	}
	if _, execErr := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); execErr != nil {
		if IsSQLiteBusy(execErr) {
			result.Status = domain.OfflineCompactionStatusRefused
			result.Reason = domain.OfflineCompactionReasonDatabaseLocked
			result.Error = "数据库正被其他进程写入或锁定；请先停止 AHA2 服务/插件后重试"
			return result, nil
		}
		result.Error = fmt.Sprintf("acquire exclusive database lock: %v", execErr)
		return result, execErr
	}
	if _, execErr := conn.ExecContext(ctx, `COMMIT`); execErr != nil {
		result.Error = fmt.Sprintf("commit exclusive database lock: %v", execErr)
		return result, execErr
	}
	progress("数据库独占锁已获取。")

	result.BeforeBytes = fileSize(databasePath)
	result.WALBeforeBytes = fileSize(databasePath + "-wal")
	result.SHMBeforeBytes = fileSize(databasePath + "-shm")
	result.RequiredBytes = offlineCompactionRequiredBytes(result.BeforeBytes, result.WALBeforeBytes, result.SHMBeforeBytes)
	backupRoot := strings.TrimSpace(options.BackupDir)
	if backupRoot == "" {
		backupRoot = filepath.Join(dataDir, domain.OfflineCompactionDefaultBackupDir)
	} else if backupRoot, absErr = filepath.Abs(backupRoot); absErr != nil {
		result.Error = fmt.Sprintf("resolve backup directory: %v", absErr)
		return result, absErr
	}

	progress("检查磁盘可用空间…")
	free, diskErr := offlineCompactionDiskFree(dataDir)
	result.DiskFreeBytes = free
	if diskErr != nil {
		result.Error = fmt.Sprintf("read free disk space: %v", diskErr)
		return result, diskErr
	}
	hasSpace, spaceErr := offlineCompactionHasSpace(dataDir, backupRoot, result.RequiredBytes, result.BeforeBytes, result.WALBeforeBytes, result.SHMBeforeBytes)
	if spaceErr != nil {
		result.Error = fmt.Sprintf("read free disk space: %v", spaceErr)
		return result, spaceErr
	}
	if !hasSpace {
		result.Status = domain.OfflineCompactionStatusRefused
		result.Reason = domain.OfflineCompactionReasonDiskSpace
		result.Error = fmt.Sprintf("磁盘可用空间不足：需要约 %d 字节，当前可用 %d 字节", result.RequiredBytes, free)
		return result, nil
	}
	progress("磁盘空间检查通过（需要约 %s，可用 %s）。", formatByteCount(result.RequiredBytes), formatByteCount(free))

	progress("备份数据库、WAL 和 SHM…")
	backupPath, backupFiles, backupReason, backupErr := createOfflineCompactionBackup(ctx, databasePath, backupRoot, progress)
	result.BackupPath = backupPath
	result.BackupFiles = backupFiles
	result.BackupKept = backupPath != ""
	if backupErr != nil {
		result.Reason = backupReason
		result.Error = backupErr.Error()
		return result, backupErr
	}
	progress("备份校验通过：%s", backupPath)

	beforeCounts, countErr := countTablesForCompaction(ctx, conn, "main")
	if countErr != nil {
		result.Reason = domain.OfflineCompactionReasonBackupFailed
		result.Error = fmt.Sprintf("count tables before compaction: %v", countErr)
		return result, countErr
	}
	result.BeforeRowCounts = beforeCounts

	progress("写入 WAL 检查点…")
	busy, _, _, checkpointErr := walCheckpointTruncate(ctx, conn)
	if checkpointErr != nil {
		result.Reason = domain.OfflineCompactionReasonCheckpointFailed
		result.Error = checkpointErr.Error()
		return result, checkpointErr
	}
	if busy != 0 {
		result.Status = domain.OfflineCompactionStatusRefused
		result.Reason = domain.OfflineCompactionReasonDatabaseLocked
		result.Error = "WAL 检查点时仍有其他连接持有读写事务"
		return result, nil
	}

	progress("执行 VACUUM…")
	if _, vacuumErr := conn.ExecContext(ctx, `VACUUM`); vacuumErr != nil {
		result.Reason = domain.OfflineCompactionReasonVacuumFailed
		if ctx.Err() != nil {
			result.Reason = domain.OfflineCompactionReasonCanceled
		}
		result.Error = vacuumErr.Error()
		return result, vacuumErr
	}
	if offlineCompactAfterVacuum != nil {
		if hookErr := offlineCompactAfterVacuum(ctx, conn); hookErr != nil {
			result.Reason = domain.OfflineCompactionReasonVacuumFailed
			result.Error = hookErr.Error()
			return result, hookErr
		}
	}

	progress("运行 PRAGMA integrity_check…")
	integrity, integrityErr := offlineCompactIntegrityCheck(ctx, conn)
	if integrityErr != nil {
		result.Reason = domain.OfflineCompactionReasonIntegrityFailed
		result.Error = integrityErr.Error()
		return result, integrityErr
	}
	result.IntegrityCheck = integrity
	result.IntegrityOK = strings.EqualFold(integrity, "ok")
	if !result.IntegrityOK {
		result.Reason = domain.OfflineCompactionReasonIntegrityFailed
		result.Error = fmt.Sprintf("integrity_check returned %q", integrity)
		return result, fmt.Errorf("%s", result.Error)
	}

	afterCounts, afterErr := countTablesForCompaction(ctx, conn, "main")
	if afterErr != nil {
		result.Reason = domain.OfflineCompactionReasonRowCountMismatch
		result.Error = fmt.Sprintf("count tables after compaction: %v", afterErr)
		return result, afterErr
	}
	result.AfterRowCounts = afterCounts
	if mismatch := compareRowCounts(beforeCounts, afterCounts); mismatch != "" {
		result.Reason = domain.OfflineCompactionReasonRowCountMismatch
		result.Error = fmt.Sprintf("row counts changed across compaction: %s", mismatch)
		return result, fmt.Errorf("%s", result.Error)
	}
	result.RowCountsOK = true

	// Flush the rebuilt pages out of the WAL so the reported after-size and a
	// later backup describe the compacted main file.
	if busy, _, _, checkpointErr := walCheckpointTruncate(ctx, conn); checkpointErr != nil {
		result.Reason = domain.OfflineCompactionReasonCheckpointFailed
		result.Error = checkpointErr.Error()
		return result, checkpointErr
	} else if busy != 0 {
		result.Reason = domain.OfflineCompactionReasonCheckpointFailed
		result.Error = "WAL 检查点在压缩后仍被其他连接占用"
		return result, fmt.Errorf("%s", result.Error)
	}
	result.AfterBytes = fileSize(databasePath)
	result.WALAfterBytes = fileSize(databasePath + "-wal")
	result.SHMAfterBytes = fileSize(databasePath + "-shm")
	result.BeforeTotalBytes = result.BeforeBytes + result.WALBeforeBytes + result.SHMBeforeBytes
	result.AfterTotalBytes = result.AfterBytes + result.WALAfterBytes + result.SHMAfterBytes
	result.FreedBytes = max(0, result.BeforeTotalBytes-result.AfterTotalBytes)
	result.Status = domain.OfflineCompactionStatusOK

	if !options.KeepBackup && result.BackupPath != "" {
		progress("已按要求删除校验通过的备份 %s…", result.BackupPath)
		if removeErr := os.RemoveAll(result.BackupPath); removeErr != nil {
			progress("警告：删除备份失败，保留在 %s：%v", result.BackupPath, removeErr)
		} else {
			result.BackupKept = false
		}
	}
	progress("离线压缩完成：%s → %s，释放 %s。", formatByteCount(result.BeforeTotalBytes), formatByteCount(result.AfterTotalBytes), formatByteCount(result.FreedBytes))
	return result, nil
}

// finishOfflineCompaction is used by early failures that happen before the
// deferred result writer is installed.
func finishOfflineCompaction(result domain.OfflineCompactionResult, cause error) (domain.OfflineCompactionResult, error) {
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	return result, cause
}

func offlineCompactionRequiredBytes(fileBytes, walBytes, shmBytes int64) int64 {
	// One full backup copy plus VACUUM's temporary rebuild, with a 64 MiB
	// journal/slack margin. This intentionally overestimates instead of letting
	// a backup succeed and VACUUM fail halfway.
	return fileBytes + fileBytes + walBytes + shmBytes + 64<<20
}

func offlineCompactionDiskFree(dataDir string) (int64, error) {
	return databaseDiskFree(existingPath(dataDir))
}

// offlineCompactionHasSpace checks the worst case: when data and backup share a
// filesystem, the backup copy and the VACUUM scratch copy must both fit. A
// separate backup filesystem is checked independently.
func offlineCompactionHasSpace(dataDir, backupRoot string, required, fileBytes, walBytes, shmBytes int64) (bool, error) {
	dataFree, dataErr := databaseDiskFree(existingPath(dataDir))
	if dataErr != nil {
		return false, dataErr
	}
	backupFree, backupErr := databaseDiskFree(existingPath(backupRoot))
	if backupErr != nil {
		return false, backupErr
	}
	backupBytes := fileBytes + walBytes + shmBytes + 64<<20
	vacuumBytes := fileBytes + 64<<20
	if sameFilesystem(dataDir, backupRoot) {
		return dataFree >= required && backupFree >= required, nil
	}
	return dataFree >= vacuumBytes && backupFree >= backupBytes, nil
}

func existingPath(path string) string {
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

func sameFilesystem(first, second string) bool {
	firstInfo, firstErr := os.Stat(existingPath(first))
	secondInfo, secondErr := os.Stat(existingPath(second))
	if firstErr != nil || secondErr != nil {
		// Unknown: assume the conservative same-filesystem requirement.
		return true
	}
	return os.SameFile(firstInfo, secondInfo)
}

func createOfflineCompactionBackup(ctx context.Context, databasePath, backupRoot string, progress func(string, ...any)) (string, []domain.OfflineCompactionFile, string, error) {
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return "", nil, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("create backup root: %w", err)
	}
	base := filepath.Join(backupRoot, "offline-compact-"+time.Now().UTC().Format("20060102-150405"))
	target := base
	for attempt := 2; ; attempt++ {
		err := os.Mkdir(target, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("create backup directory: %w", err)
		}
		target = fmt.Sprintf("%s-%d", base, attempt)
		if attempt > 100 {
			return "", nil, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("could not find a free backup directory under %s", backupRoot)
		}
	}
	files := make([]domain.OfflineCompactionFile, 0, 3)
	for _, item := range []struct {
		name     string
		suffix   string
		required bool
	}{
		{name: "aha2.db", suffix: "", required: true},
		{name: "aha2.db-wal", suffix: "-wal"},
		{name: "aha2.db-shm", suffix: "-shm"},
	} {
		if err := ctx.Err(); err != nil {
			return target, files, domain.OfflineCompactionReasonCanceled, err
		}
		source := databasePath + item.suffix
		if _, err := os.Stat(source); err != nil {
			if errors.Is(err, os.ErrNotExist) && !item.required {
				continue
			}
			return target, files, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("stat backup source %s: %w", source, err)
		}
		destination := filepath.Join(target, item.name)
		partial := destination + ".partial"
		progress("  复制 %s…", item.name)
		size, sum, err := copyFileWithSHA256(source, partial)
		if err != nil {
			return target, files, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("copy %s: %w", item.name, err)
		}
		verifiedSize, verifiedSum, err := hashFile(partial)
		if err != nil {
			return target, files, domain.OfflineCompactionReasonBackupVerifyFailed, fmt.Errorf("verify %s: %w", item.name, err)
		}
		if verifiedSize != size || verifiedSum != sum {
			return target, files, domain.OfflineCompactionReasonBackupVerifyFailed, fmt.Errorf("%w: %s source size=%d sha256=%s, backup size=%d sha256=%s", errOfflineBackupVerify, item.name, size, sum, verifiedSize, verifiedSum)
		}
		if err := os.Rename(partial, destination); err != nil {
			return target, files, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("finalize backup %s: %w", item.name, err)
		}
		files = append(files, domain.OfflineCompactionFile{Name: item.name, Path: destination, Bytes: size, SHA256: sum})
	}
	manifest := map[string]any{
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
		"database":   databasePath,
		"files":      files,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return target, files, domain.OfflineCompactionReasonBackupFailed, err
	}
	if err := os.WriteFile(filepath.Join(target, "manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		return target, files, domain.OfflineCompactionReasonBackupFailed, fmt.Errorf("write backup manifest: %w", err)
	}
	return target, files, "", nil
}

func copyFileWithSHA256(source, destination string) (int64, string, error) {
	input, err := os.Open(source)
	if err != nil {
		return 0, "", err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hasher), input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return written, "", copyErr
	}
	if closeErr != nil {
		return written, "", closeErr
	}
	return written, hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return size, "", err
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

func walCheckpointTruncate(ctx context.Context, conn *sql.Conn) (busy, logFrames, checkpointed int64, err error) {
	err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed)
	return busy, logFrames, checkpointed, err
}

func integrityCheck(ctx context.Context, conn *sql.Conn) (string, error) {
	var result string
	if err := conn.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return "", fmt.Errorf("integrity_check: %w", err)
	}
	return result, nil
}

// countTablesForCompaction counts the key tables in the given schema.
func countTablesForCompaction(ctx context.Context, conn *sql.Conn, schema string) (map[string]int64, error) {
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

// compareRowCounts reports any table whose row count changed across the
// rebuild. Offline compaction holds the exclusive lock the whole time, so no
// legitimate write can explain a difference.
func compareRowCounts(before, after map[string]int64) string {
	for table, count := range before {
		if after[table] != count {
			return fmt.Sprintf("row counts for %s before=%d after=%d", table, count, after[table])
		}
	}
	for table, count := range after {
		if _, ok := before[table]; !ok && count != 0 {
			return fmt.Sprintf("%s appeared with %d rows", table, count)
		}
	}
	return ""
}

// writeOfflineCompactionResult persists the last run atomically so a service
// restarted after compaction can show the result in the data management page.
func writeOfflineCompactionResult(dataDir string, result domain.OfflineCompactionResult) error {
	if dataDir == "" {
		return errors.New("data directory is required")
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dataDir, ".offline-compaction-result-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(dataDir, domain.OfflineCompactionResultFile))
}

// LatestOfflineCompactionResult reads the last CLI report. A missing file is
// not an error; it means the command has never run for this data directory.
func LatestOfflineCompactionResult(dataDir string) (*domain.OfflineCompactionResult, error) {
	path := filepath.Join(dataDir, domain.OfflineCompactionResultFile)
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result domain.OfflineCompactionResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if result.Status == "" {
		return nil, fmt.Errorf("parse %s: missing status", path)
	}
	return &result, nil
}

// OfflineCompactionInfoFor builds the read-only Web guide payload. It only
// stats files; it never opens the database or starts maintenance.
func OfflineCompactionInfoFor(dataDir string) (domain.OfflineCompactionInfo, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(dataDir))
	if err != nil {
		return domain.OfflineCompactionInfo{}, err
	}
	databasePath := filepath.Join(absolute, "aha2.db")
	info := domain.OfflineCompactionInfo{
		DataDir:      absolute,
		DatabasePath: databasePath,
		BackupDir:    filepath.Join(absolute, domain.OfflineCompactionDefaultBackupDir),
		Command:      "aha2 db compact --data-dir " + quoteCommandArgument(absolute),
	}
	if _, err := os.Stat(databasePath); err == nil {
		info.DatabaseExists = true
	}
	info.FileBytes = fileSize(databasePath)
	info.WALBytes = fileSize(databasePath + "-wal")
	info.SHMBytes = fileSize(databasePath + "-shm")
	info.RequiredBytes = offlineCompactionRequiredBytes(info.FileBytes, info.WALBytes, info.SHMBytes)
	if free, err := databaseDiskFree(existingPath(absolute)); err == nil {
		info.DiskFreeBytes = free
	}
	latest, err := LatestOfflineCompactionResult(absolute)
	if err != nil {
		return info, err
	}
	info.LatestResult = latest
	return info, nil
}

func quoteCommandArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func formatByteCount(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	divisor, exponent := int64(unit), 0
	for value := bytes / unit; value >= unit && exponent < 4; value /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(divisor), "KMGTP"[exponent])
}
