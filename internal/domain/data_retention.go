package domain

import (
	"fmt"
	"time"
)

// hardware_io retention limits. The table is append-only terminal output; the
// data management page may delete old rows from it and from conversation_items
// (see ConversationRetentionSettings).
const (
	HardwareIORetentionDefaultDays = 30
	HardwareIORetentionMinDays     = 1
	HardwareIORetentionMaxDays     = 3650
	HardwareIOPurgeDefaultRows     = 200_000
	HardwareIOPurgeMinRows         = 1_000
	HardwareIOPurgeMaxRows         = 1_000_000
	HardwareIOPurgeBatchRows       = 10_000
)

type HardwareIORetentionSettings struct {
	RetentionDays int       `json:"retention_days"`
	MaxDeleteRows int       `json:"max_delete_rows"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

func DefaultHardwareIORetentionSettings() HardwareIORetentionSettings {
	return HardwareIORetentionSettings{
		RetentionDays: HardwareIORetentionDefaultDays,
		MaxDeleteRows: HardwareIOPurgeDefaultRows,
	}
}

func (s HardwareIORetentionSettings) Validate() error {
	if s.RetentionDays < HardwareIORetentionMinDays || s.RetentionDays > HardwareIORetentionMaxDays {
		return fmt.Errorf("retention_days must be between %d and %d", HardwareIORetentionMinDays, HardwareIORetentionMaxDays)
	}
	if s.MaxDeleteRows < HardwareIOPurgeMinRows || s.MaxDeleteRows > HardwareIOPurgeMaxRows {
		return fmt.Errorf("max_delete_rows must be between %d and %d", HardwareIOPurgeMinRows, HardwareIOPurgeMaxRows)
	}
	return nil
}

// Conversation retention limits. Only conversation_items of terminal tasks are
// eligible; chat items (the owner's and the agent's final replies), messages,
// events, audit events and that task's attachments are never deleted here.
const (
	ConversationRetentionDefaultDays = 30
	ConversationRetentionMinDays     = 1
	ConversationRetentionMaxDays     = 3650
	ConversationPurgeBatchRows       = 2_000
)

// ConversationRetentionSettings controls the B-category cleanup. PurgeTool
// removes category "tool" items (backend command start/finish events);
// PurgeUpdate additionally removes the agent progress update kinds. At least
// one must be enabled.
type ConversationRetentionSettings struct {
	RetentionDays int       `json:"retention_days"`
	PurgeTool     bool      `json:"purge_tool"`
	PurgeUpdate   bool      `json:"purge_update"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

func DefaultConversationRetentionSettings() ConversationRetentionSettings {
	return ConversationRetentionSettings{
		RetentionDays: ConversationRetentionDefaultDays,
		PurgeTool:     true,
		PurgeUpdate:   false,
	}
}

func (s ConversationRetentionSettings) Validate() error {
	if s.RetentionDays < ConversationRetentionMinDays || s.RetentionDays > ConversationRetentionMaxDays {
		return fmt.Errorf("conversation retention_days must be between %d and %d", ConversationRetentionMinDays, ConversationRetentionMaxDays)
	}
	if !s.PurgeTool && !s.PurgeUpdate {
		return fmt.Errorf("conversation cleanup must include tool or update events")
	}
	return nil
}

// ConversationPurgePlan is what one manual conversation cleanup would delete.
// Categories lists the conversation_item categories in scope ("tool" and
// optionally "update"). Eligible rows belong to terminal tasks, are older than
// Cutoff, are not the task's final answer, have no attachment and are not
// blocked by a pending approval. One confirmed run deletes every eligible row
// (batched internally), so RunRows always equals EligibleRows; there is no
// remaining-for-later field. A later preview reports whatever new rows have
// passed the cutoff by then.
type ConversationPurgePlan struct {
	RetentionDays    int       `json:"retention_days"`
	PurgeTool        bool      `json:"purge_tool"`
	PurgeUpdate      bool      `json:"purge_update"`
	Categories       []string  `json:"categories"`
	Cutoff           time.Time `json:"cutoff"`
	EligibleRows     int64     `json:"eligible_rows"`
	RunRows          int64     `json:"run_rows"`
	TasksAffected    int64     `json:"tasks_affected"`
	AvgRowBytes      int64     `json:"avg_row_bytes"`
	RunBytesEstimate int64     `json:"run_bytes_estimate"`
	SkipReason       string    `json:"skip_reason,omitempty"`
}

type ConversationPurgeResult struct {
	Plan        ConversationPurgePlan `json:"plan"`
	BackupPath  string                `json:"backup_path,omitempty"`
	DeletedRows int64                 `json:"deleted_rows"`
	Batches     int                   `json:"batches"`
	BatchSize   int                   `json:"batch_size"`
	Partial     bool                  `json:"partial"`
	SkipReason  string                `json:"skip_reason,omitempty"`
	DurationMS  int64                 `json:"duration_ms"`
}

const (
	ConversationPurgeSkipEmpty           = "empty"
	ConversationPurgeSkipNothingEligible = "nothing_eligible"
	ConversationPurgeSkipAlreadyRunning  = "already_running"
	ConversationPurgeSkipCanceled        = "canceled"
)

// DatabaseTableStat describes one table without scanning it. Rows is exact only
// when RowsExact is true; otherwise it is MAX(rowid)-MIN(rowid)+1, an upper
// bound that over-counts deleted gaps. EstimatedBytes is the average payload of
// the newest SampleRows rows multiplied by Rows; it excludes B-tree and index
// overhead. Deletable is true only for hardware_io.
type DatabaseTableStat struct {
	Name           string `json:"name"`
	Rows           int64  `json:"rows"`
	RowsExact      bool   `json:"rows_exact"`
	SampleRows     int    `json:"sample_rows"`
	AvgRowBytes    int64  `json:"avg_row_bytes"`
	EstimatedBytes int64  `json:"estimated_bytes"`
	Deletable      bool   `json:"deletable"`
}

// Safety labels of a database category on the data management page. Only
// DatabaseSafetyPreviewPurge allows deletion from the page, and only of
// hardware_io; retire means the data is removed through the sync retire flow,
// never from this page.
const (
	DatabaseSafetyKeep         = "keep"
	DatabaseSafetyReadOnly     = "read_only"
	DatabaseSafetyPreviewPurge = "preview_purge"
	DatabaseSafetyRetire       = "retire"
)

// Cleanup kinds a category exposes on the data management page. Only these
// categories have an executable cleanup; every other category row opens a
// read-only explanation dialog.
const (
	DatabaseCleanupHardwareIO   = "hardware_io"
	DatabaseCleanupConversation = "conversation"
)

// DatabaseCategory groups tables by what they hold. ID is the catalog letter
// (A-K) or "other" for tables the catalog does not list. Rows and
// EstimatedBytes are sums of the table estimates; RowsExact is true only when
// every table count is exact. Cleanup is empty for read-only categories.
type DatabaseCategory struct {
	ID             string              `json:"id"`
	Key            string              `json:"key"`
	Safety         string              `json:"safety"`
	Cleanup        string              `json:"cleanup,omitempty"`
	Rows           int64               `json:"rows"`
	RowsExact      bool                `json:"rows_exact"`
	EstimatedBytes int64               `json:"estimated_bytes"`
	Tables         []DatabaseTableStat `json:"tables"`
}

type HardwareIORange struct {
	MinSequence   int64      `json:"min_sequence"`
	MaxSequence   int64      `json:"max_sequence"`
	EstimatedRows int64      `json:"estimated_rows"`
	OldestAt      *time.Time `json:"oldest_at,omitempty"`
	NewestAt      *time.Time `json:"newest_at,omitempty"`
}

// Terminal IO files (task-711). New hardware terminal output is stored under
// <data-dir>/hardware-io instead of SQLite. Retention reuses
// HardwareIORetentionSettings.RetentionDays and is applied automatically;
// MaxBytes caps the directory by deleting the oldest closed segments first.
const (
	HardwareIOFileMaxBytesDefault int64 = 2 << 30
	HardwareIOFileMaxBytesMin     int64 = 64 << 20
)

// HardwareIOFileStats describes the terminal IO files.
type HardwareIOFileStats struct {
	Directory    string     `json:"directory"`
	Streams      int        `json:"streams"`
	Segments     int        `json:"segments"`
	Records      int64      `json:"records"`
	Bytes        int64      `json:"bytes"`
	MaxBytes     int64      `json:"max_bytes"`
	LastSequence int64      `json:"last_sequence"`
	CorruptLines int64      `json:"corrupt_lines"`
	OldestAt     *time.Time `json:"oldest_at,omitempty"`
	NewestAt     *time.Time `json:"newest_at,omitempty"`
}

type DatabaseStats struct {
	FileBytes     int64              `json:"file_bytes"`
	WALBytes      int64              `json:"wal_bytes"`
	SHMBytes      int64              `json:"shm_bytes"`
	PageSize      int64              `json:"page_size"`
	PageCount     int64              `json:"page_count"`
	FreelistCount int64              `json:"freelist_count"`
	UsedBytes     int64              `json:"used_bytes"`
	FreeBytes     int64              `json:"free_bytes"`
	TableCount    int                `json:"table_count"`
	Categories    []DatabaseCategory `json:"categories"`
	HardwareIO    HardwareIORange    `json:"hardware_io"`
	// HardwareIOFiles is read fresh on every call, also for cached stats.
	HardwareIOFiles *HardwareIOFileStats          `json:"hardware_io_files,omitempty"`
	Retention       HardwareIORetentionSettings   `json:"retention"`
	Conversation    ConversationRetentionSettings `json:"conversation_retention"`
	CollectedAt     time.Time                     `json:"collected_at"`
	DurationMS      int64                         `json:"duration_ms"`
	// Cached is true when the table estimates come from an earlier collection;
	// Retention is always read fresh.
	Cached bool `json:"cached"`
}

// HardwareIOPurgePlan is what one manual cleanup would delete. Rows with
// sequence in [RunStartSequence, RunEndSequence) and created_at before Cutoff
// are deleted; RunRows is their exact count. EligibleRowsEstimate covers every
// row before CutoffSequence and is an upper bound like DatabaseTableStat.Rows.
type HardwareIOPurgePlan struct {
	RetentionDays             int       `json:"retention_days"`
	MaxDeleteRows             int       `json:"max_delete_rows"`
	Cutoff                    time.Time `json:"cutoff"`
	MinSequence               int64     `json:"min_sequence"`
	MaxSequence               int64     `json:"max_sequence"`
	CutoffSequence            int64     `json:"cutoff_sequence"`
	EligibleRowsEstimate      int64     `json:"eligible_rows_estimate"`
	RunStartSequence          int64     `json:"run_start_sequence"`
	RunEndSequence            int64     `json:"run_end_sequence"`
	RunRows                   int64     `json:"run_rows"`
	AvgRowBytes               int64     `json:"avg_row_bytes"`
	RunBytesEstimate          int64     `json:"run_bytes_estimate"`
	EligibleBytesEstimate     int64     `json:"eligible_bytes_estimate"`
	RemainingEligibleEstimate int64     `json:"remaining_eligible_estimate"`
	SkipReason                string    `json:"skip_reason,omitempty"`
}

type HardwareIOPurgeResult struct {
	Plan        HardwareIOPurgePlan `json:"plan"`
	BackupPath  string              `json:"backup_path,omitempty"`
	DeletedRows int64               `json:"deleted_rows"`
	Batches     int                 `json:"batches"`
	BatchSize   int                 `json:"batch_size"`
	Partial     bool                `json:"partial"`
	SkipReason  string              `json:"skip_reason,omitempty"`
	DurationMS  int64               `json:"duration_ms"`
}

const (
	HardwareIOPurgeSkipEmpty           = "empty"
	HardwareIOPurgeSkipNothingEligible = "nothing_eligible"
	HardwareIOPurgeSkipAlreadyRunning  = "already_running"
	HardwareIOPurgeSkipCanceled        = "canceled"
)

// DatabasePurgeProgress is the live state of one manual B/C cleanup. The purge
// request itself stays synchronous; this snapshot lets the Web show the phase,
// deleted rows and batch count while that request is still running. It carries
// only counts and phases, never row contents or file paths.
type DatabasePurgeProgress struct {
	Kind        string    `json:"kind"`
	Running     bool      `json:"running"`
	Phase       string    `json:"phase"`
	PlannedRows int64     `json:"planned_rows"`
	DeletedRows int64     `json:"deleted_rows"`
	Batches     int       `json:"batches"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	DurationMS  int64     `json:"duration_ms"`
	SkipReason  string    `json:"skip_reason,omitempty"`
	Error       string    `json:"error,omitempty"`
}

const (
	DatabasePurgeKindHardwareIO   = "hardware_io"
	DatabasePurgeKindConversation = "conversation"

	DatabasePurgePhaseIdle      = "idle"
	DatabasePurgePhasePreparing = "preparing"
	DatabasePurgePhaseBackup    = "backup"
	DatabasePurgePhaseDeleting  = "deleting"
	DatabasePurgePhaseDone      = "done"
	DatabasePurgePhaseSkipped   = "skipped"
	DatabasePurgePhaseFailed    = "failed"
)

// Offline database compaction types live in offline_compaction.go. Compaction
// only runs from the CLI after the AHA2 service has stopped; the Web page is a
// read-only guide and never starts a VACUUM.
