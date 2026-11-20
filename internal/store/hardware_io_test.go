package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareio"
)

func countLegacyHardwareIO(t *testing.T, database *Store) int64 {
	t.Helper()
	var rows int64
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM hardware_io`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestHardwareIOIsWrittenToFilesNotSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	created := time.Date(2026, 11, 20, 8, 30, 1, 123456789, time.UTC)
	var sequences []int64
	for index, data := range []string{"boot\r\n", "login: ", "root\n"} {
		direction := "rx"
		if index == 2 {
			direction = "tx"
		}
		sequence, err := database.AppendHardwareIO(ctx, domain.HardwareIOEvent{
			ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: "board",
			Transport: domain.HardwareTransportSerial, Direction: direction, Data: data,
			Encoding: "text", Source: "runtime", CreatedAt: created.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, sequence)
	}
	if rows := countLegacyHardwareIO(t, database); rows != 0 {
		t.Fatalf("hardware_io has %d rows, want terminal IO kept out of SQLite", rows)
	}
	if _, err := os.Stat(filepath.Join(dataDir, HardwareIODirName, "v1", task.ID, "board", "serial")); err != nil {
		t.Fatalf("stream directory missing: %v", err)
	}
	page, err := database.HardwareIOPage(ctx, task.ID, "board", domain.HardwareTransportSerial, sequences[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequences[1] || !page.HasMore || page.LatestSequence != sequences[1] {
		t.Fatalf("incremental page = %#v", page)
	}
	item := page.Items[0]
	if item.TaskID != task.ID || item.HardwareID != "board" || item.Transport != "serial" || item.Direction != "rx" ||
		item.Data != "login: " || item.Encoding != "text" || item.Source != "runtime" || !item.CreatedAt.Equal(created.Add(time.Second)) {
		t.Fatalf("event fields not preserved: %#v", item)
	}
	page, err = database.HardwareIOPage(ctx, task.ID, "board", domain.HardwareTransportSerial, sequences[2], 10)
	if err != nil || len(page.Items) != 0 || page.LatestSequence != sequences[2] || page.HasMore {
		t.Fatalf("caught-up page = %#v %v", page, err)
	}
	page, err = database.HardwareIOPage(ctx, task.ID, "board", domain.HardwareTransportNetwork, 0, 10)
	if err != nil || len(page.Items) != 0 || page.LatestSequence != 0 {
		t.Fatalf("other transport page = %#v %v", page, err)
	}
	stats, err := database.DatabaseStats(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if stats.HardwareIOFiles == nil || stats.HardwareIOFiles.Records != 3 || stats.HardwareIOFiles.Streams != 1 ||
		stats.HardwareIOFiles.MaxBytes != domain.HardwareIOFileMaxBytesDefault || stats.HardwareIOFiles.LastSequence != sequences[2] {
		t.Fatalf("file stats = %#v", stats.HardwareIOFiles)
	}
}

func TestClearLegacyHardwareIOKeepsSequencesMonotonic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	// Rows written by the SQLite-era binary, the newest with sequence 4200.
	for _, sequence := range []int64{4100, 4150, 4200} {
		if _, err := database.db.ExecContext(ctx, `INSERT INTO hardware_io(sequence,id,task_id,hardware_id,transport,direction,data,encoding,source,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, sequence, domain.NewID("hardware_io"), task.ID, "board", "serial", "rx", "old\n", "text", "runtime",
			timeString(time.Now().Add(-time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	result, err := database.ClearLegacyHardwareIO(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 || result.FloorSeq != 4200 || result.AlreadyEmpty {
		t.Fatalf("clear result = %#v", result)
	}
	if rows := countLegacyHardwareIO(t, database); rows != 0 {
		t.Fatalf("legacy rows left: %d", rows)
	}
	var foreignKeys int
	if err := database.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("main pool foreign_keys = %d %v, the clear must not change it", foreignKeys, err)
	}
	var recorded int64
	if err := database.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='hardware_io'`).Scan(&recorded); err != nil || recorded != 4200 {
		t.Fatalf("sqlite_sequence = %d %v", recorded, err)
	}
	again, err := database.ClearLegacyHardwareIO(ctx)
	if err != nil || !again.AlreadyEmpty || again.Rows != 0 {
		t.Fatalf("second clear = %#v %v", again, err)
	}
	sequence, err := database.AppendHardwareIO(ctx, domain.HardwareIOEvent{
		ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: "board", Transport: "serial",
		Direction: "rx", Data: "new\n", Encoding: "text", Source: "runtime", CreatedAt: time.Now(),
	})
	if err != nil || sequence != 4201 {
		t.Fatalf("first file sequence = %d %v, want 4201", sequence, err)
	}
	// The table must still exist with its schema so an older binary can open
	// the database after a downgrade.
	if _, err := database.db.ExecContext(ctx, `SELECT sequence,id,task_id,hardware_id,transport,direction,data,encoding,source,created_at FROM hardware_io LIMIT 1`); err != nil {
		t.Fatalf("legacy table schema changed: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// Even when sqlite_sequence is lost, the reservation file keeps every new
	// sequence above the cursors clients already hold.
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.db.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name='hardware_io'`); err != nil {
		t.Fatal(err)
	}
	next, err := reopened.AppendHardwareIO(ctx, domain.HardwareIOEvent{
		ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: "board", Transport: "serial",
		Direction: "rx", Data: "after restart\n", Encoding: "text", Source: "runtime", CreatedAt: time.Now(),
	})
	if err != nil || next <= 4201 {
		t.Fatalf("sequence after restart = %d %v, want > 4201", next, err)
	}
	page, err := reopened.HardwareIOPage(ctx, task.ID, "board", "serial", 4200, 10)
	if err != nil || len(page.Items) != 2 || page.Items[0].Data != "new\n" {
		t.Fatalf("page after restart = %#v %v", page, err)
	}
}

func TestDeleteTaskRemovesHardwareIOFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	if _, err := database.AppendHardwareIO(ctx, domain.HardwareIOEvent{
		ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: "board", Transport: "serial",
		Direction: "rx", Data: "x", Encoding: "text", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(dataDir, HardwareIODirName, "v1", task.ID)
	if _, err := os.Stat(taskDir); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Fatalf("task IO directory kept after delete: %v", err)
	}
	page, err := database.HardwareIOPage(ctx, task.ID, "board", "serial", 0, 10)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("page after delete = %#v %v", page, err)
	}
}

func TestRunHardwareIOMaintenanceAppliesRetentionAndRemovesOrphans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	if _, err := database.UpdateHardwareIORetentionSettings(ctx, domain.HardwareIORetentionSettings{RetentionDays: 7, MaxDeleteRows: domain.HardwareIOPurgeDefaultRows}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, age := range []time.Duration{20 * 24 * time.Hour, 10 * 24 * time.Hour, time.Hour} {
		if _, err := database.AppendHardwareIO(ctx, domain.HardwareIOEvent{
			ID: domain.NewID("hardware_io"), TaskID: task.ID, HardwareID: "board", Transport: "serial",
			Direction: "rx", Data: "age " + age.String(), Encoding: "text", CreatedAt: now.Add(-age),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.HardwareIOFiles().Append(hardwareio.Record{
		ID: "orphan", TaskID: "task_deleted_elsewhere", HardwareID: "board", Transport: "serial", Direction: "rx", Data: "x",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := database.RunHardwareIOMaintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.OrphanTasks != 1 || result.RetentionSegments != 2 {
		t.Fatalf("maintenance = %#v", result)
	}
	page, err := database.HardwareIOPage(ctx, task.ID, "board", "serial", 0, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Data != "age 1h0m0s" {
		t.Fatalf("page after retention = %#v %v", page, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, HardwareIODirName, "v1", "task_deleted_elsewhere")); !os.IsNotExist(err) {
		t.Fatalf("orphan stream kept: %v", err)
	}
}

func TestHardwareIOMaxBytesEnvOverride(t *testing.T) {
	t.Setenv(HardwareIOMaxBytesEnv, "")
	if HardwareIOMaxBytes() != domain.HardwareIOFileMaxBytesDefault {
		t.Fatal("default cap not used")
	}
	t.Setenv(HardwareIOMaxBytesEnv, "1073741824")
	if HardwareIOMaxBytes() != 1<<30 {
		t.Fatal("override not applied")
	}
	t.Setenv(HardwareIOMaxBytesEnv, "1024")
	if HardwareIOMaxBytes() != domain.HardwareIOFileMaxBytesDefault {
		t.Fatal("a cap below the minimum must fall back to the default")
	}
}
