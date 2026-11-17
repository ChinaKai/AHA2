package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// Exit codes are stable for scripts: refused means the command found the
// service running, the database locked, or too little disk space; failed means
// the maintenance attempt itself did not finish its verification.
const (
	dbExitSuccess = 0
	dbExitFailure = 1
	dbExitUsage   = 2
	dbExitRefused = 3
)

func dbCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aha2 db compact [--data-dir <path>] [--backup-dir <path>] [--json] [--keep-backup=true|false]")
		fmt.Fprintln(stderr, "exit codes: 0 success, 1 failed, 2 usage, 3 refused (service running, database locked, or low disk)")
		return dbExitUsage
	}
	switch args[0] {
	case "compact":
		return dbCompact(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown db command %q\n", args[0])
		fmt.Fprintln(stderr, "usage: aha2 db compact [--data-dir <path>] [--backup-dir <path>] [--json] [--keep-backup=true|false]")
		fmt.Fprintln(stderr, "exit codes: 0 success, 1 failed, 2 usage, 3 refused (service running, database locked, or low disk)")
		return dbExitUsage
	}
}

func dbCompact(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("aha2 db compact", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory (must not be in use by a running service)")
	backupDir := flags.String("backup-dir", "", "parent directory for the timestamped offline-compact backup (default <data-dir>/backups)")
	jsonOutput := flags.Bool("json", false, "print the result as JSON on stdout and progress on stderr")
	keepBackup := flags.Bool("keep-backup", true, "keep the verified backup after success; pass --keep-backup=false to delete it only after every verification passes")
	if err := flags.Parse(args); err != nil {
		return dbExitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return dbExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	progressWriter := stdout
	if *jsonOutput {
		progressWriter = stderr
		fmt.Fprintln(progressWriter, "AHA2 离线数据库压缩")
		fmt.Fprintf(progressWriter, "数据目录：%s\n", *dataDir)
	}
	result, compactErr := store.CompactDatabaseOffline(ctx, store.OfflineCompactionOptions{
		DataDir:    *dataDir,
		BackupDir:  *backupDir,
		KeepBackup: *keepBackup,
		Progress: func(format string, args ...any) {
			fmt.Fprintf(progressWriter, format+"\n", args...)
		},
	})

	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return dbExitFailure
		}
	} else {
		writeOfflineCompactionHuman(stdout, result)
		if compactErr != nil {
			fmt.Fprintf(stderr, "离线压缩失败：%v\n", compactErr)
		}
	}

	switch result.Status {
	case domain.OfflineCompactionStatusOK:
		return dbExitSuccess
	case domain.OfflineCompactionStatusRefused:
		return dbExitRefused
	default:
		return dbExitFailure
	}
}

func writeOfflineCompactionHuman(writer io.Writer, result domain.OfflineCompactionResult) {
	fmt.Fprintln(writer, "------------------------------")
	switch result.Status {
	case domain.OfflineCompactionStatusOK:
		fmt.Fprintln(writer, "结果：成功")
	case domain.OfflineCompactionStatusRefused:
		fmt.Fprintln(writer, "结果：已拒绝（数据库未改动）")
	default:
		fmt.Fprintln(writer, "结果：失败（数据库和备份均保留）")
	}
	if result.Reason != "" {
		fmt.Fprintf(writer, "原因：%s\n", result.Reason)
	}
	if result.Error != "" {
		fmt.Fprintf(writer, "说明：%s\n", result.Error)
	}
	fmt.Fprintf(writer, "数据库：%s\n", result.DatabasePath)
	if result.BeforeTotalBytes > 0 {
		fmt.Fprintf(writer, "大小（含 WAL/SHM）：%s → %s（释放 %s）\n",
			formatBytesForCLI(result.BeforeTotalBytes), formatBytesForCLI(result.AfterTotalBytes), formatBytesForCLI(result.FreedBytes))
	}
	if result.IntegrityCheck != "" {
		fmt.Fprintf(writer, "完整性：%s\n", result.IntegrityCheck)
	}
	if result.RowCountsOK {
		fmt.Fprintln(writer, "关键表行数：一致")
	}
	if result.BackupPath != "" {
		if result.BackupKept {
			fmt.Fprintf(writer, "备份：%s\n", result.BackupPath)
		} else {
			fmt.Fprintf(writer, "备份：%s（已按 --keep-backup=false 删除）\n", result.BackupPath)
		}
	}
	if result.BackupKept && result.Status != domain.OfflineCompactionStatusOK {
		fmt.Fprintf(writer, "恢复：停止服务后，把备份目录中的 aha2.db、aha2.db-wal、aha2.db-shm 复制回 %s\n", result.DataDir)
	}
	fmt.Fprintf(writer, "耗时：%d ms\n", result.DurationMS)
}

func formatBytesForCLI(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(bytes)
	index := 0
	for value >= unit && index < len(units)-1 {
		value /= unit
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%.0f %s", value, units[index])
	}
	return fmt.Sprintf("%.1f %s", value, units[index])
}
