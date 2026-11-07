package domain

import (
	"fmt"
	"time"
)

// hardware_io retention limits. The table is append-only terminal output and is
// the only database table the data management page may delete from.
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

// DatabaseTableStat describes one table without scanning it. Rows is exact only
// when RowsExact is true; otherwise it is MAX(rowid)-MIN(rowid)+1, an upper
// bound that over-counts deleted gaps. EstimatedBytes is the average payload of
// the newest SampleRows rows multiplied by Rows; it excludes B-tree and index
// overhead.
type DatabaseTableStat struct {
	Name           string `json:"name"`
	Exists         bool   `json:"exists"`
	Rows           int64  `json:"rows"`
	RowsExact      bool   `json:"rows_exact"`
	SampleRows     int    `json:"sample_rows"`
	AvgRowBytes    int64  `json:"avg_row_bytes"`
	EstimatedBytes int64  `json:"estimated_bytes"`
}

type HardwareIORange struct {
	MinSequence   int64      `json:"min_sequence"`
	MaxSequence   int64      `json:"max_sequence"`
	EstimatedRows int64      `json:"estimated_rows"`
	OldestAt      *time.Time `json:"oldest_at,omitempty"`
	NewestAt      *time.Time `json:"newest_at,omitempty"`
}

type DatabaseStats struct {
	FileBytes     int64                       `json:"file_bytes"`
	WALBytes      int64                       `json:"wal_bytes"`
	SHMBytes      int64                       `json:"shm_bytes"`
	PageSize      int64                       `json:"page_size"`
	PageCount     int64                       `json:"page_count"`
	FreelistCount int64                       `json:"freelist_count"`
	UsedBytes     int64                       `json:"used_bytes"`
	FreeBytes     int64                       `json:"free_bytes"`
	Tables        []DatabaseTableStat         `json:"tables"`
	HardwareIO    HardwareIORange             `json:"hardware_io"`
	Retention     HardwareIORetentionSettings `json:"retention"`
	CollectedAt   time.Time                   `json:"collected_at"`
	DurationMS    int64                       `json:"duration_ms"`
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
