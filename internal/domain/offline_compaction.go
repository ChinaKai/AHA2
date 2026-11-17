package domain

import "time"

// Offline database compaction result states. The CLI maps them to exit codes:
// ok and refused are expected outcomes, failed means the command did not finish
// its verification.
const (
	OfflineCompactionStatusOK      = "ok"
	OfflineCompactionStatusRefused = "refused"
	OfflineCompactionStatusFailed  = "failed"
)

// Offline compaction refusal reasons. A refusal never changes the database.
const (
	OfflineCompactionReasonServiceRunning  = "service_running"
	OfflineCompactionReasonDatabaseLocked  = "database_locked"
	OfflineCompactionReasonDiskSpace       = "disk_space"
	OfflineCompactionReasonDatabaseMissing = "database_missing"
	OfflineCompactionReasonNotFileDatabase = "not_file_database"
)

// Offline compaction failure reasons. The backup created before VACUUM is kept
// for every failure so the operator can restore the pre-compaction files.
const (
	OfflineCompactionReasonBackupFailed       = "backup_failed"
	OfflineCompactionReasonBackupVerifyFailed = "backup_verify_failed"
	OfflineCompactionReasonCheckpointFailed   = "checkpoint_failed"
	OfflineCompactionReasonVacuumFailed       = "vacuum_failed"
	OfflineCompactionReasonIntegrityFailed    = "integrity_failed"
	OfflineCompactionReasonRowCountMismatch   = "row_count_mismatch"
	OfflineCompactionReasonCanceled           = "canceled"
)

const (
	// OfflineCompactionResultFile is the last-run report under the data
	// directory. The Web guide reads it; it is never used to drive a VACUUM.
	OfflineCompactionResultFile = "offline-compaction-result.json"
	// OfflineCompactionDefaultBackupDir is the default parent of the
	// timestamped offline-compact-* backup directories, relative to data dir.
	OfflineCompactionDefaultBackupDir = "backups"
)

// OfflineCompactionFile is one verified file in the pre-compaction backup.
type OfflineCompactionFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// OfflineCompactionResult is what `aha2 db compact --json` prints and what is
// stored as offline-compaction-result.json. Before*/After* report the main
// database file and its WAL/SHM companions. RowCountsOK and IntegrityCheck are
// empty/false until the post-VACUUM verification runs.
type OfflineCompactionResult struct {
	Status           string                  `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	DataDir          string                  `json:"data_dir"`
	DatabasePath     string                  `json:"database_path"`
	BackupPath       string                  `json:"backup_path,omitempty"`
	BackupFiles      []OfflineCompactionFile `json:"backup_files,omitempty"`
	BackupKept       bool                    `json:"backup_kept"`
	KeepBackup       bool                    `json:"keep_backup"`
	BeforeBytes      int64                   `json:"before_bytes"`
	WALBeforeBytes   int64                   `json:"wal_before_bytes"`
	SHMBeforeBytes   int64                   `json:"shm_before_bytes"`
	AfterBytes       int64                   `json:"after_bytes"`
	WALAfterBytes    int64                   `json:"wal_after_bytes"`
	SHMAfterBytes    int64                   `json:"shm_after_bytes"`
	BeforeTotalBytes int64                   `json:"before_total_bytes"`
	AfterTotalBytes  int64                   `json:"after_total_bytes"`
	FreedBytes       int64                   `json:"freed_bytes"`
	RequiredBytes    int64                   `json:"required_bytes"`
	DiskFreeBytes    int64                   `json:"disk_free_bytes"`
	IntegrityCheck   string                  `json:"integrity_check,omitempty"`
	IntegrityOK      bool                    `json:"integrity_ok"`
	BeforeRowCounts  map[string]int64        `json:"before_row_counts,omitempty"`
	AfterRowCounts   map[string]int64        `json:"after_row_counts,omitempty"`
	RowCountsOK      bool                    `json:"row_counts_ok"`
	Error            string                  `json:"error,omitempty"`
	StartedAt        time.Time               `json:"started_at"`
	FinishedAt       time.Time               `json:"finished_at"`
	DurationMS       int64                   `json:"duration_ms"`
}

// OfflineCompactionInfo is the read-only payload behind the Web data
// management guide. It contains no preflight decision about active writers:
// the page explains that the service must be stopped instead of pretending it
// can run VACUUM while the control plane is serving requests.
type OfflineCompactionInfo struct {
	DataDir        string                   `json:"data_dir"`
	DatabasePath   string                   `json:"database_path"`
	BackupDir      string                   `json:"backup_dir"`
	Command        string                   `json:"command"`
	FileBytes      int64                    `json:"file_bytes"`
	WALBytes       int64                    `json:"wal_bytes"`
	SHMBytes       int64                    `json:"shm_bytes"`
	RequiredBytes  int64                    `json:"required_bytes"`
	DiskFreeBytes  int64                    `json:"disk_free_bytes"`
	DatabaseExists bool                     `json:"database_exists"`
	LatestResult   *OfflineCompactionResult `json:"latest_result,omitempty"`
}
