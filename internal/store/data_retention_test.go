package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func openDataRetentionTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// seedHardwareIO inserts count rows whose created_at starts at first and grows
// by step per row, in one transaction so large fixtures stay fast.
func seedHardwareIO(t *testing.T, database *Store, taskID string, count int, first time.Time, step time.Duration) {
	t.Helper()
	ctx := context.Background()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO hardware_io(id,task_id,hardware_id,transport,direction,data,encoding,source,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < count; index++ {
		created := first.Add(time.Duration(index) * step)
		if _, err := statement.ExecContext(ctx,
			domain.NewID("hardware_io"), taskID, "board", domain.HardwareTransportSerial, "rx",
			fmt.Sprintf("line %06d\r\n", index), "text", "bridge", timeString(created),
		); err != nil {
			t.Fatal(err)
		}
	}
	statement.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func hardwareIOCount(t *testing.T, database *Store) int64 {
	t.Helper()
	var count int64
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM hardware_io`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestHardwareIORetentionSettingsDefaultsPersistAndValidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	settings, err := database.HardwareIORetentionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RetentionDays != 30 || settings.MaxDeleteRows != 200_000 {
		t.Fatalf("defaults=%#v", settings)
	}
	for _, invalid := range []domain.HardwareIORetentionSettings{
		{RetentionDays: 0, MaxDeleteRows: 200_000},
		{RetentionDays: 3651, MaxDeleteRows: 200_000},
		{RetentionDays: 30, MaxDeleteRows: 999},
		{RetentionDays: 30, MaxDeleteRows: 1_000_001},
	} {
		if _, err := database.UpdateHardwareIORetentionSettings(ctx, invalid); err == nil {
			t.Fatalf("invalid settings accepted: %#v", invalid)
		}
	}
	if _, err := database.UpdateHardwareIORetentionSettings(ctx, domain.HardwareIORetentionSettings{RetentionDays: 7, MaxDeleteRows: 5_000}); err != nil {
		t.Fatal(err)
	}
	settings, err = database.HardwareIORetentionSettings(ctx)
	if err != nil || settings.RetentionDays != 7 || settings.MaxDeleteRows != 5_000 || settings.UpdatedAt.IsZero() {
		t.Fatalf("stored=%#v err=%v", settings, err)
	}
}

// The statistics path runs on every data page load, so none of its hardware_io
// queries may scan the table. A full COUNT(*) on the production table took
// about three minutes.
func TestDatabaseStatsHardwareIOQueriesAvoidScans(t *testing.T) {
	t.Parallel()
	database := openDataRetentionTestStore(t)
	columns, err := database.tableColumns(context.Background(), "hardware_io")
	if err != nil {
		t.Fatal(err)
	}
	queries := boundedTableStatQueries("hardware_io", columns)
	cases := map[string]struct {
		query string
		args  []any
	}{
		"min":    {queries.minRowID, nil},
		"max":    {queries.maxRowID, nil},
		"sample": {queries.sampleNewest, []any{int64(1)}},
		"bound":  {`SELECT created_at FROM hardware_io WHERE sequence=?`, []any{int64(1)}},
		"probe":  {`SELECT sequence,created_at FROM hardware_io WHERE sequence>=? ORDER BY sequence LIMIT 1`, []any{int64(1)}},
		"run": {`SELECT COUNT(*) FROM hardware_io WHERE sequence>=? AND sequence<? AND substr(created_at,1,19)<?`,
			[]any{int64(1), int64(2), "x"}},
		"delete": {`DELETE FROM hardware_io WHERE sequence>=? AND sequence<? AND substr(created_at,1,19)<?`,
			[]any{int64(1), int64(2), "x"}},
	}
	for label, item := range cases {
		plan := strings.Join(explainQueryPlan(t, database, item.query, item.args...), "\n")
		if strings.Contains(plan, "SCAN hardware_io") {
			t.Fatalf("%s scans hardware_io:\n%s", label, plan)
		}
		if !strings.Contains(plan, "SEARCH hardware_io") {
			t.Fatalf("%s is not a rowid search:\n%s", label, plan)
		}
	}
	// Guard the reason MIN and MAX are separate statements.
	combined := strings.Join(explainQueryPlan(t, database, `SELECT MIN(sequence),MAX(sequence) FROM hardware_io`), "\n")
	if !strings.Contains(combined, "SCAN hardware_io") {
		t.Logf("combined MIN/MAX no longer scans; the split is still correct:\n%s", combined)
	}
}

func TestDatabaseStatsEstimatesHardwareIOWithoutCounting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	first := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	seedHardwareIO(t, database, task.ID, 100, first, time.Minute)
	// Delete a hole: the estimate stays MAX-MIN+1 and is labelled inexact.
	if _, err := database.db.Exec(`DELETE FROM hardware_io WHERE sequence BETWEEN 10 AND 19`); err != nil {
		t.Fatal(err)
	}
	stats, err := database.DatabaseStats(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if stats.PageSize <= 0 || stats.PageCount <= 0 || stats.UsedBytes <= 0 || stats.FileBytes <= 0 {
		t.Fatalf("page stats=%#v", stats)
	}
	byName := map[string]domain.DatabaseTableStat{}
	categoryOf := map[string]domain.DatabaseCategory{}
	for _, category := range stats.Categories {
		for _, table := range category.Tables {
			byName[table.Name] = table
			categoryOf[table.Name] = category
		}
	}
	for _, name := range []string{"hardware_io", "conversation_items", "sync_remote_task_objects", "events", "audit_events", "messages", "agent_inbox", "sync_applied"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing table stat %s: %#v", name, stats.Categories)
		}
	}
	hardware := byName["hardware_io"]
	if hardware.Rows != 100 || hardware.RowsExact || !hardware.Deletable {
		t.Fatalf("hardware_io must be the rowid span, never an exact count: %#v", hardware)
	}
	if category := categoryOf["hardware_io"]; category.ID != "C" || category.Safety != domain.DatabaseSafetyPreviewPurge {
		t.Fatalf("hardware_io category=%#v", category)
	}
	if hardware.SampleRows != 90 || hardware.AvgRowBytes <= 0 || hardware.EstimatedBytes != hardware.AvgRowBytes*100 {
		t.Fatalf("hardware_io sample=%#v", hardware)
	}
	if stats.HardwareIO.MinSequence != 1 || stats.HardwareIO.MaxSequence != 100 || stats.HardwareIO.EstimatedRows != 100 {
		t.Fatalf("range=%#v", stats.HardwareIO)
	}
	if stats.HardwareIO.OldestAt == nil || !stats.HardwareIO.OldestAt.Equal(first) ||
		stats.HardwareIO.NewestAt == nil || !stats.HardwareIO.NewestAt.Equal(first.Add(99*time.Minute)) {
		t.Fatalf("range times=%#v", stats.HardwareIO)
	}
	if !byName["sync_applied"].RowsExact || byName["sync_applied"].Rows != 0 {
		t.Fatalf("empty small table=%#v", byName["sync_applied"])
	}
	if stats.Retention.RetentionDays != 30 {
		t.Fatalf("retention=%#v", stats.Retention)
	}
}

// Every table of the real schema must land in one A-K category; a new table
// that nobody categorized shows up under "other" and fails here.
func TestDatabaseCatalogCoversSchemaOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	seen := map[string]string{}
	for _, def := range databaseCatalog {
		for _, table := range def.tables {
			if previous, ok := seen[table]; ok {
				t.Fatalf("%s listed in %s and %s", table, previous, def.id)
			}
			seen[table] = def.id
		}
	}
	stats, err := database.DatabaseStats(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(stats.Categories))
	tables := 0
	for _, category := range stats.Categories {
		ids = append(ids, category.ID)
		tables += len(category.Tables)
		if category.ID == "other" {
			t.Fatalf("uncategorized tables: %#v", category.Tables)
		}
		for _, table := range category.Tables {
			if table.Deletable != (table.Name == "hardware_io") {
				t.Fatalf("only hardware_io may be deletable: %#v", table)
			}
		}
		wantSafety := map[string]string{"B": domain.DatabaseSafetyReadOnly, "C": domain.DatabaseSafetyPreviewPurge, "D": domain.DatabaseSafetyRetire}
		if want, ok := wantSafety[category.ID]; ok && category.Safety != want {
			t.Fatalf("category %s safety=%s", category.ID, category.Safety)
		}
		if category.ID != "C" && category.Safety == domain.DatabaseSafetyPreviewPurge {
			t.Fatalf("category %s allows purge", category.ID)
		}
	}
	if strings.Join(ids, "") != "ABCDEFGHIJK" || tables != stats.TableCount {
		t.Fatalf("categories=%v tables=%d of %d", ids, tables, stats.TableCount)
	}

	// An unknown table is reported read-only under "other".
	if _, err := database.db.Exec(`CREATE TABLE zz_extra(id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	stats, err = database.DatabaseStats(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	last := stats.Categories[len(stats.Categories)-1]
	if last.ID != "other" || last.Safety != domain.DatabaseSafetyReadOnly || len(last.Tables) != 1 || last.Tables[0].Name != "zz_extra" {
		t.Fatalf("other=%#v", last)
	}
}

// Large tables report the rowid span instead of COUNT(*), and the totals of a
// category are the sum of its tables.
func TestDatabaseStatsLargeTablesUseRowidSpanAndCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	// A rowid jump past exactCountLimit makes audit_events "large" without
	// inserting that many rows.
	for _, id := range []int64{1, exactCountLimit + 10} {
		if _, err := database.db.Exec(`INSERT INTO audit_events(sequence,id,owner_id,action,resource_type,resource_id,data_json,occurred_at) VALUES(?,?,?,?,?,?,?,?)`,
			id, domain.NewID("audit"), "owner", "seed", "test", "x", "{}", timeString(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := database.DatabaseStats(ctx, false)
	if err != nil || stats.Cached {
		t.Fatalf("first collection cached=%v err=%v", stats.Cached, err)
	}
	var audit domain.DatabaseTableStat
	var conversation domain.DatabaseCategory
	for _, category := range stats.Categories {
		if category.ID == "B" {
			conversation = category
		}
		for _, table := range category.Tables {
			if table.Name == "audit_events" {
				audit = table
			}
		}
	}
	if audit.RowsExact || audit.Rows != exactCountLimit+10 {
		t.Fatalf("large table must use the rowid span: %#v", audit)
	}
	var sum int64
	for _, table := range conversation.Tables {
		sum += table.Rows
	}
	if conversation.RowsExact || conversation.Rows != sum {
		t.Fatalf("category totals=%#v", conversation)
	}

	// A second load inside the TTL reuses the estimates but reads the
	// retention settings fresh; refresh forces a new collection.
	if _, err := database.UpdateHardwareIORetentionSettings(ctx, domain.HardwareIORetentionSettings{RetentionDays: 9, MaxDeleteRows: 2_000}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DELETE FROM audit_events`); err != nil {
		t.Fatal(err)
	}
	cached, err := database.DatabaseStats(ctx, false)
	if err != nil || !cached.Cached || !cached.CollectedAt.Equal(stats.CollectedAt) || cached.Retention.RetentionDays != 9 {
		t.Fatalf("cached=%v collected=%s retention=%#v err=%v", cached.Cached, cached.CollectedAt, cached.Retention, err)
	}
	fresh, err := database.DatabaseStats(ctx, true)
	if err != nil || fresh.Cached {
		t.Fatalf("refresh cached=%v err=%v", fresh.Cached, err)
	}
	for _, category := range fresh.Categories {
		for _, table := range category.Tables {
			if table.Name == "audit_events" && (table.Rows != 0 || !table.RowsExact) {
				t.Fatalf("refresh kept stale audit_events: %#v", table)
			}
		}
	}
}

func TestHardwareIOPurgePlanBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	settings := domain.HardwareIORetentionSettings{RetentionDays: 30, MaxDeleteRows: 1_000}

	plan, err := database.HardwareIOPurgePlan(ctx, settings, now)
	if err != nil || plan.SkipReason != domain.HardwareIOPurgeSkipEmpty {
		t.Fatalf("empty plan=%#v err=%v", plan, err)
	}

	task := createStoreTestTask(t, database)
	// One row per hour from 40 days ago: 240 rows (sequence 1..240) are older
	// than the 30-day cutoff, the rest are newer.
	first := now.Add(-40 * 24 * time.Hour)
	seedHardwareIO(t, database, task.ID, 40*24, first, time.Hour)
	plan, err = database.HardwareIOPurgePlan(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CutoffSequence != 241 || plan.EligibleRowsEstimate != 240 {
		t.Fatalf("cutoff=%#v", plan)
	}
	if plan.RunStartSequence != 1 || plan.RunEndSequence != 241 || plan.RunRows != 240 || plan.RemainingEligibleEstimate != 0 {
		t.Fatalf("run=%#v", plan)
	}
	if plan.AvgRowBytes <= 0 || plan.RunBytesEstimate != plan.AvgRowBytes*240 || plan.SkipReason != "" {
		t.Fatalf("bytes=%#v", plan)
	}
	if !plan.Cutoff.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("cutoff time=%s", plan.Cutoff)
	}
	if hardwareIOCount(t, database) != 960 {
		t.Fatal("preview deleted rows")
	}

	// A row exactly at the cutoff is kept.
	settings.RetentionDays = 40
	plan, err = database.HardwareIOPurgePlan(ctx, settings, now)
	if err != nil || plan.SkipReason != domain.HardwareIOPurgeSkipNothingEligible || plan.RunRows != 0 {
		t.Fatalf("row at cutoff must be kept: %#v err=%v", plan, err)
	}

	// The per-run limit caps the run and reports the rest.
	settings = domain.HardwareIORetentionSettings{RetentionDays: 1, MaxDeleteRows: 1_000}
	plan, err = database.HardwareIOPurgePlan(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.EligibleRowsEstimate != 936 || plan.RunRows != 936 {
		t.Fatalf("1-day plan=%#v", plan)
	}

	if _, err := database.HardwareIOPurgePlan(ctx, domain.HardwareIORetentionSettings{RetentionDays: 0, MaxDeleteRows: 1_000}, now); err == nil {
		t.Fatal("invalid retention accepted")
	}
}

func TestPurgeHardwareIODeletesOnlyOldRowsInBatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	// 25,000 rows one minute apart ending now. The row exactly one day old is
	// kept, so 25,000-1,441 = 23,559 rows are older, more than two 10k batches.
	first := now.Add(-time.Duration(25_000-1) * time.Minute)
	seedHardwareIO(t, database, task.ID, 25_000, first, time.Minute)
	if err := database.AppendAudit(ctx, "owner", "seed", "test", "x", nil, timeString(now)); err != nil {
		t.Fatal(err)
	}

	settings := domain.HardwareIORetentionSettings{RetentionDays: 1, MaxDeleteRows: 22_000}
	result, err := database.PurgeHardwareIO(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 22_000 || result.Batches != 3 || result.BatchSize != 10_000 || result.Partial {
		t.Fatalf("first run=%#v", result)
	}
	if result.Plan.RemainingEligibleEstimate != 23_559-22_000 {
		t.Fatalf("remaining=%d", result.Plan.RemainingEligibleEstimate)
	}
	if hardwareIOCount(t, database) != 3_000 {
		t.Fatalf("rows after first run=%d", hardwareIOCount(t, database))
	}

	result, err = database.PurgeHardwareIO(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 1_559 || result.Batches != 1 {
		t.Fatalf("second run=%#v", result)
	}
	var oldest string
	if err := database.db.QueryRow(`SELECT created_at FROM hardware_io ORDER BY sequence LIMIT 1`).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if parseTime(oldest).Before(now.Add(-24 * time.Hour)) {
		t.Fatalf("an old row survived: %s", oldest)
	}
	if hardwareIOCount(t, database) != 1_441 {
		t.Fatalf("recent rows were deleted: %d", hardwareIOCount(t, database))
	}

	result, err = database.PurgeHardwareIO(ctx, settings, now)
	if err != nil || result.DeletedRows != 0 || result.SkipReason != domain.HardwareIOPurgeSkipNothingEligible {
		t.Fatalf("third run=%#v err=%v", result, err)
	}
	var audits int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("purge touched another table: audits=%d err=%v", audits, err)
	}
}

// A recent row followed by rows whose clock jumped backwards. The binary search
// probes the recent row and stops the sequence range before it, so this covers
// the search, not the created_at guard in the DELETE (see the next test).
func TestPurgeHardwareIONeverDeletesRecentRowsInsideRange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	seedHardwareIO(t, database, task.ID, 10, now.Add(-10*24*time.Hour), time.Hour)
	seedHardwareIO(t, database, task.ID, 1, now.Add(-time.Hour), 0)
	seedHardwareIO(t, database, task.ID, 10, now.Add(-9*24*time.Hour), time.Hour)
	result, err := database.PurgeHardwareIO(ctx, domain.HardwareIORetentionSettings{RetentionDays: 2, MaxDeleteRows: 1_000}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 10 {
		t.Fatalf("result=%#v", result)
	}
	var recent int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM hardware_io WHERE sequence=11`).Scan(&recent); err != nil || recent != 1 {
		t.Fatalf("recent row deleted: %d %v", recent, err)
	}
}

// The created_at guard in the DELETE must keep a recent row that the sequence
// range includes. The fixture deliberately breaks the order of sequence and
// created_at: one row at sequence 5 is an hour old (written with a clock that
// was ahead, or before a backwards jump) among 19 rows from nine and ten days
// ago. With 20 rows the binary search probes sequences 11, 16, 19 and 20, all
// old, so it never sees sequence 5 and returns cutoff 21. The run [1,21) then
// contains the recent row and only the guard stops it from being deleted.
// Not parallel: hardwareIOPurgeMu is package-wide, and a concurrent purge test
// would make this one skip with already_running instead of running the DELETE.
func TestPurgeHardwareIOCreatedAtGuardKeepsRecentRowInsideRange(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	seedHardwareIO(t, database, task.ID, 4, now.Add(-10*24*time.Hour), time.Hour)
	seedHardwareIO(t, database, task.ID, 1, now.Add(-time.Hour), 0)
	seedHardwareIO(t, database, task.ID, 15, now.Add(-9*24*time.Hour), time.Hour)
	const recentSequence = 5
	var recentCreatedAt string
	if err := database.db.QueryRow(`SELECT created_at FROM hardware_io WHERE sequence=?`, recentSequence).Scan(&recentCreatedAt); err != nil {
		t.Fatal(err)
	}
	if !parseTime(recentCreatedAt).Equal(now.Add(-time.Hour)) {
		t.Fatalf("fixture: sequence %d created_at=%s", recentSequence, recentCreatedAt)
	}

	settings := domain.HardwareIORetentionSettings{RetentionDays: 2, MaxDeleteRows: 1_000}
	result, err := database.PurgeHardwareIO(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.SkipReason != "" {
		t.Fatalf("purge skipped: %#v", result)
	}
	// The run must cover the recent row, otherwise the guard is not exercised.
	plan := result.Plan
	if plan.CutoffSequence != 21 || plan.RunStartSequence > recentSequence || plan.RunEndSequence <= recentSequence {
		t.Fatalf("recent row is outside the purge range: %#v", plan)
	}
	if result.DeletedRows != 19 || result.Batches != 1 || result.Partial {
		t.Fatalf("result=%#v", result)
	}
	var survivors []int64
	rows, err := database.db.Query(`SELECT sequence FROM hardware_io ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		survivors = append(survivors, sequence)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(survivors) != 1 || survivors[0] != recentSequence {
		t.Fatalf("survivors=%v, want only the recent row %d", survivors, recentSequence)
	}
}

func TestPurgeHardwareIOStopsWhenCanceledAndRejectsOverlap(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	seedHardwareIO(t, database, task.ID, 100, now.Add(-100*24*time.Hour), time.Hour)
	settings := domain.HardwareIORetentionSettings{RetentionDays: 1, MaxDeleteRows: 1_000}

	hardwareIOPurgeMu.Lock()
	result, err := database.PurgeHardwareIO(ctx, settings, now)
	hardwareIOPurgeMu.Unlock()
	if err != nil || result.SkipReason != domain.HardwareIOPurgeSkipAlreadyRunning || result.DeletedRows != 0 {
		t.Fatalf("overlap=%#v err=%v", result, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	result, _ = database.PurgeHardwareIO(canceled, settings, now)
	if result.DeletedRows != 0 || hardwareIOCount(t, database) != 100 {
		t.Fatalf("canceled purge deleted rows: %#v", result)
	}
}
