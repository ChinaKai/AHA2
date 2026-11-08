package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type databaseCategoryDef struct {
	id, key, safety string
	tables          []string
}

// databaseCatalog groups every table into the A-K categories of the data
// management page (pet-lab/database-catalog.md, 2026-11-07). Each table is
// listed once: channel_source_events belongs to channels, pet_events to the
// pet and mail_attachments to mail, so category totals never double count.
// Tables missing here are reported under "other" as read-only.
var databaseCatalog = []databaseCategoryDef{
	{"A", "tasks", domain.DatabaseSafetyKeep, []string{
		"tasks", "task_agents", "task_rounds", "turns", "task_goal_events", "task_memory",
		"task_permission_requests", "collaboration_batches", "collaboration_events",
		"collaboration_members", "agent_session_handoffs", "runtime_config_snapshots", "backend_sessions",
	}},
	{"B", "conversation", domain.DatabaseSafetyReadOnly, []string{
		"conversation_items", "messages", "events", "audit_events", "agent_inbox",
	}},
	{"C", "hardware", domain.DatabaseSafetyPreviewPurge, []string{
		"hardware_io", "hardware_resources", "hardware_groups", "task_hardware_bindings",
	}},
	{"D", "sync", domain.DatabaseSafetyRetire, []string{
		"sync_remote_task_objects", "sync_applied", "sync_tombstones", "sync_conflicts",
		"sync_outbox", "sync_settings", "sync_state", "retired_workspace_mirrors",
	}},
	{"E", "channels", domain.DatabaseSafetyReadOnly, []string{
		"channel_instances", "channel_conversations", "channel_conversation_members", "channel_endpoints",
		"channel_identity_links", "channel_inbox_dedup", "channel_delivery_outbox", "channel_delivery_attempts",
		"channel_source_events", "channel_plugins", "channel_plugin_commands", "channel_service_capabilities",
		"channel_sessions", "channel_subscriptions", "channel_task_contacts", "channel_task_routes",
		"channel_onboarding_sessions", "channel_pending_actions", "channel_knowledge_grants",
		"channel_knowledge_policies", "channel_handoffs",
	}},
	{"F", "mail", domain.DatabaseSafetyKeep, []string{
		"mail_accounts", "mail_account_aliases", "mail_templates", "mail_template_versions", "mail_bindings",
		"mail_messages", "mail_attachments", "mail_deliveries", "mail_delivery_attempts",
	}},
	{"G", "knowledge", domain.DatabaseSafetyKeep, []string{
		"knowledge_entries", "knowledge_libraries", "knowledge_proposals", "knowledge_review_settings",
		"project_knowledge_bindings", "skills", "prompt_template_overrides",
	}},
	{"H", "settings", domain.DatabaseSafetyKeep, []string{
		"models", "providers", "env_groups", "codex_accounts", "backend_settings", "agent_api_settings",
		"general_settings", "security_settings", "network_settings", "proxy_settings", "mcp_servers",
		"data_retention_settings",
	}},
	{"I", "pet", domain.DatabaseSafetyKeep, []string{
		"owner_assistants", "pet_states", "pet_approvals", "pet_events", "pet_task_watches", "pet_code_reviews",
	}},
	{"J", "attachments", domain.DatabaseSafetyReadOnly, []string{
		"attachments", "build_artifacts",
	}},
	{"K", "metadata", domain.DatabaseSafetyKeep, []string{
		"projects", "workspaces", "product_lines", "owners", "sessions", "schema_migrations",
		"sqlite_sequence", "assistant_messages", "assistant_hint_dismissals",
	}},
}

// exactCountLimit is the largest rowid span for which DatabaseStats runs an
// exact COUNT(*); larger tables report the span. COUNT(*) reads every leaf
// page, so the limit keeps tables with large rows (conversation_items, events)
// off the page load path. hardware_io is never counted: on the production
// database a full COUNT(*) over its ~12M rows takes minutes.
const exactCountLimit = 50_000

// databaseStatsTTL is how long collected table estimates are reused. The
// page's refresh button and a cleanup both force a new collection.
const databaseStatsTTL = 5 * time.Minute

type databaseStatsCache struct {
	mu    sync.Mutex
	stats *domain.DatabaseStats
}

// statSampleRows is how many rows are read to estimate the average row size.
const statSampleRows = 500

// hardwareIOPurgeMu keeps manual cleanups from overlapping. A second request
// is reported as skipped instead of queueing behind the first.
var hardwareIOPurgeMu sync.Mutex

func (s *Store) HardwareIORetentionSettings(ctx context.Context) (domain.HardwareIORetentionSettings, error) {
	item := domain.DefaultHardwareIORetentionSettings()
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT hardware_io_retention_days,hardware_io_max_delete_rows,updated_at
		FROM data_retention_settings WHERE id=1`).
		Scan(&item.RetentionDays, &item.MaxDeleteRows, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultHardwareIORetentionSettings(), nil
	}
	if err != nil {
		return domain.HardwareIORetentionSettings{}, err
	}
	item.UpdatedAt = parseTime(updatedAt)
	return item, nil
}

func (s *Store) UpdateHardwareIORetentionSettings(ctx context.Context, item domain.HardwareIORetentionSettings) (domain.HardwareIORetentionSettings, error) {
	if err := item.Validate(); err != nil {
		return domain.HardwareIORetentionSettings{}, err
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO data_retention_settings(id,hardware_io_retention_days,hardware_io_max_delete_rows,updated_at)
		VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			hardware_io_retention_days=excluded.hardware_io_retention_days,
			hardware_io_max_delete_rows=excluded.hardware_io_max_delete_rows,
			updated_at=excluded.updated_at`,
		item.RetentionDays, item.MaxDeleteRows, timeString(item.UpdatedAt),
	)
	return item, err
}

// DatabaseStats returns the cached statistics when they are younger than
// databaseStatsTTL and refresh is false. The retention settings are always
// read fresh so a saved change shows immediately.
func (s *Store) DatabaseStats(ctx context.Context, refresh bool) (domain.DatabaseStats, error) {
	cache := &s.databaseStats
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !refresh && cache.stats != nil && time.Since(cache.stats.CollectedAt) < databaseStatsTTL {
		stats := *cache.stats
		retention, err := s.HardwareIORetentionSettings(ctx)
		if err != nil {
			return domain.DatabaseStats{}, err
		}
		stats.Retention = retention
		stats.Cached = true
		return stats, nil
	}
	stats, err := s.collectDatabaseStats(ctx)
	if err != nil {
		return domain.DatabaseStats{}, err
	}
	cache.stats = &stats
	return stats, nil
}

// collectDatabaseStats reports database file and table usage using only
// bounded queries: PRAGMA page counters, MIN/MAX(rowid) seeks, a sample of the
// newest rows, and exact COUNT(*) only for small tables other than hardware_io.
func (s *Store) collectDatabaseStats(ctx context.Context) (domain.DatabaseStats, error) {
	started := time.Now()
	stats := domain.DatabaseStats{CollectedAt: started.UTC()}
	if s.path != "" && !isMemoryDatabase(s.path) {
		stats.FileBytes = fileSize(s.path)
		stats.WALBytes = fileSize(s.path + "-wal")
		stats.SHMBytes = fileSize(s.path + "-shm")
	}
	for _, pragma := range []struct {
		name   string
		target *int64
	}{
		{"page_size", &stats.PageSize},
		{"page_count", &stats.PageCount},
		{"freelist_count", &stats.FreelistCount},
	} {
		if err := s.db.QueryRowContext(ctx, `PRAGMA `+pragma.name).Scan(pragma.target); err != nil {
			return domain.DatabaseStats{}, fmt.Errorf("read %s: %w", pragma.name, err)
		}
	}
	stats.FreeBytes = stats.FreelistCount * stats.PageSize
	stats.UsedBytes = (stats.PageCount - stats.FreelistCount) * stats.PageSize
	categories, tableCount, err := s.databaseCategories(ctx)
	if err != nil {
		return domain.DatabaseStats{}, err
	}
	stats.Categories, stats.TableCount = categories, tableCount
	hardwareRange, err := s.hardwareIORange(ctx)
	if err != nil {
		return domain.DatabaseStats{}, err
	}
	stats.HardwareIO = hardwareRange
	retention, err := s.HardwareIORetentionSettings(ctx)
	if err != nil {
		return domain.DatabaseStats{}, err
	}
	stats.Retention = retention
	stats.DurationMS = time.Since(started).Milliseconds()
	return stats, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// databaseCategories reads the table list from sqlite_master once and fills
// the catalog categories with the tables that exist. Catalog tables missing
// from this database are left out; tables the catalog does not list go to a
// trailing read-only "other" category.
func (s *Store) databaseCategories(ctx context.Context) ([]domain.DatabaseCategory, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,IFNULL(sql,'') FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil, 0, fmt.Errorf("list tables: %w", err)
	}
	existing := map[string]string{}
	var names []string
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			rows.Close()
			return nil, 0, err
		}
		existing[name] = definition
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	listed := map[string]bool{}
	categories := make([]domain.DatabaseCategory, 0, len(databaseCatalog)+1)
	for _, def := range databaseCatalog {
		category := domain.DatabaseCategory{ID: def.id, Key: def.key, Safety: def.safety, RowsExact: true}
		for _, table := range def.tables {
			listed[table] = true
			definition, ok := existing[table]
			if !ok {
				continue
			}
			if err := s.addTableStat(ctx, &category, table, definition); err != nil {
				return nil, 0, err
			}
		}
		categories = append(categories, category)
	}
	other := domain.DatabaseCategory{ID: "other", Key: "other", Safety: domain.DatabaseSafetyReadOnly, RowsExact: true}
	for _, name := range names {
		if listed[name] {
			continue
		}
		if err := s.addTableStat(ctx, &other, name, existing[name]); err != nil {
			return nil, 0, err
		}
	}
	if len(other.Tables) > 0 {
		categories = append(categories, other)
	}
	return categories, len(names), nil
}

func (s *Store) addTableStat(ctx context.Context, category *domain.DatabaseCategory, table, definition string) error {
	item, err := s.tableStat(ctx, table, definition)
	if err != nil {
		return err
	}
	category.Tables = append(category.Tables, item)
	category.Rows += item.Rows
	category.EstimatedBytes += item.EstimatedBytes
	category.RowsExact = category.RowsExact && item.RowsExact
	return nil
}

func (s *Store) tableStat(ctx context.Context, table, definition string) (domain.DatabaseTableStat, error) {
	item := domain.DatabaseTableStat{Name: table, Deletable: table == "hardware_io"}
	// Virtual and WITHOUT ROWID tables have no rowid to seek, and counting
	// them could scan. None exist in the AHA schema; report them without
	// estimates rather than risk a slow query.
	upper := strings.ToUpper(definition)
	if strings.HasPrefix(upper, "CREATE VIRTUAL") || strings.Contains(upper, "WITHOUT ROWID") {
		return item, nil
	}
	columns, err := s.tableColumns(ctx, table)
	if err != nil {
		return item, err
	}
	queries := boundedTableStatQueries(table, columns)
	var minimum, maximum sql.NullInt64
	if err := s.db.QueryRowContext(ctx, queries.minRowID).Scan(&minimum); err != nil {
		return item, fmt.Errorf("%s min rowid: %w", table, err)
	}
	if err := s.db.QueryRowContext(ctx, queries.maxRowID).Scan(&maximum); err != nil {
		return item, fmt.Errorf("%s max rowid: %w", table, err)
	}
	if !minimum.Valid || !maximum.Valid {
		item.RowsExact = true
		return item, nil
	}
	item.Rows = maximum.Int64 - minimum.Int64 + 1
	if table != "hardware_io" && item.Rows <= exactCountLimit {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdentifier(table)).Scan(&item.Rows); err != nil {
			return item, fmt.Errorf("%s count: %w", table, err)
		}
		item.RowsExact = true
	}
	if queries.sampleNewest != "" {
		// The window is wider than the sample so a few deleted gaps near the end
		// still leave statSampleRows rows to average.
		if err := s.db.QueryRowContext(ctx, queries.sampleNewest, maximum.Int64-statSampleRows*4).
			Scan(&item.AvgRowBytes, &item.SampleRows); err != nil {
			return item, fmt.Errorf("%s sample: %w", table, err)
		}
	}
	item.EstimatedBytes = item.AvgRowBytes * item.Rows
	return item, nil
}

func (s *Store) tableColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("%s columns: %w", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

type tableStatQueries struct {
	minRowID     string
	maxRowID     string
	sampleNewest string
}

// boundedTableStatQueries builds the per-table statistics queries. Every one is
// a rowid seek or a rowid range: MIN and MAX are separate statements because
// SQLite only turns a lone MIN/MAX of the rowid into a single seek (combined it
// scans the table), and the sample is bounded by a rowid lower bound instead of
// relying on LIMIT to stop a scan. TestDatabaseStatsHardwareIOQueriesAvoidScans
// checks the hardware_io plans.
func boundedTableStatQueries(table string, columns []string) tableStatQueries {
	quoted := quoteIdentifier(table)
	queries := tableStatQueries{
		minRowID: `SELECT MIN(rowid) FROM ` + quoted,
		maxRowID: `SELECT MAX(rowid) FROM ` + quoted,
	}
	if len(columns) > 0 {
		queries.sampleNewest = `SELECT IFNULL(CAST(AVG(row_bytes) AS INTEGER),0),COUNT(*) FROM (
			SELECT ` + rowBytesExpression(columns) + ` AS row_bytes FROM ` + quoted + `
			WHERE rowid>=? ORDER BY rowid DESC LIMIT ` + fmt.Sprint(statSampleRows) + `)`
	}
	return queries
}

// rowBytesExpression sums the stored byte length of every column. It measures
// payload only; record headers, B-tree pages and indexes are not included.
func rowBytesExpression(columns []string) string {
	terms := make([]string, 0, len(columns))
	for _, name := range columns {
		terms = append(terms, `IFNULL(length(CAST(`+quoteIdentifier(name)+` AS BLOB)),0)`)
	}
	return strings.Join(terms, "+")
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (s *Store) hardwareIORange(ctx context.Context) (domain.HardwareIORange, error) {
	var result domain.HardwareIORange
	minimum, maximum, err := s.hardwareIOBounds(ctx)
	if err != nil || minimum == 0 {
		return result, err
	}
	result.MinSequence, result.MaxSequence = minimum, maximum
	result.EstimatedRows = maximum - minimum + 1
	for _, bound := range []struct {
		sequence int64
		target   **time.Time
	}{{minimum, &result.OldestAt}, {maximum, &result.NewestAt}} {
		var createdAt string
		if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM hardware_io WHERE sequence=?`, bound.sequence).Scan(&createdAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return result, err
		}
		if parsed := parseTime(createdAt); !parsed.IsZero() {
			*bound.target = &parsed
		}
	}
	return result, nil
}

// hardwareIOBounds returns the first and last sequence, or zeros when empty.
func (s *Store) hardwareIOBounds(ctx context.Context) (int64, int64, error) {
	var minimum, maximum sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(sequence) FROM hardware_io`).Scan(&minimum); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(sequence) FROM hardware_io`).Scan(&maximum); err != nil {
		return 0, 0, err
	}
	if !minimum.Valid || !maximum.Valid {
		return 0, 0, nil
	}
	return minimum.Int64, maximum.Int64, nil
}

// HardwareIOPurgePlan computes what a manual cleanup would delete without
// deleting anything. hardware_io has no created_at index, but sequence is the
// rowid and grows with time, so the first row at or after the cutoff is found
// by a binary search of rowid seeks (about log2(rows) single-row reads). The run
// is the oldest MaxDeleteRows sequence span before that point; its row count is
// an exact COUNT over that rowid range.
func (s *Store) HardwareIOPurgePlan(ctx context.Context, settings domain.HardwareIORetentionSettings, now time.Time) (domain.HardwareIOPurgePlan, error) {
	if err := settings.Validate(); err != nil {
		return domain.HardwareIOPurgePlan{}, err
	}
	plan := domain.HardwareIOPurgePlan{
		RetentionDays: settings.RetentionDays,
		MaxDeleteRows: settings.MaxDeleteRows,
		Cutoff:        now.UTC().Add(-time.Duration(settings.RetentionDays) * 24 * time.Hour).Truncate(time.Second),
	}
	minimum, maximum, err := s.hardwareIOBounds(ctx)
	if err != nil {
		return plan, err
	}
	if minimum == 0 {
		plan.SkipReason = domain.HardwareIOPurgeSkipEmpty
		return plan, nil
	}
	plan.MinSequence, plan.MaxSequence = minimum, maximum
	cutoffSequence, err := s.hardwareIOCutoffSequence(ctx, minimum, maximum, plan.Cutoff)
	if err != nil {
		return plan, err
	}
	plan.CutoffSequence = cutoffSequence
	plan.EligibleRowsEstimate = cutoffSequence - minimum
	if plan.EligibleRowsEstimate <= 0 {
		plan.EligibleRowsEstimate = 0
		plan.SkipReason = domain.HardwareIOPurgeSkipNothingEligible
		return plan, nil
	}
	plan.RunStartSequence = minimum
	plan.RunEndSequence = min(cutoffSequence, minimum+int64(settings.MaxDeleteRows))
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM hardware_io
		WHERE sequence>=? AND sequence<? AND substr(created_at,1,19)<?`,
		plan.RunStartSequence, plan.RunEndSequence, hardwareIOCutoffKey(plan.Cutoff),
	).Scan(&plan.RunRows); err != nil {
		return plan, fmt.Errorf("count hardware_io run: %w", err)
	}
	if plan.RunRows == 0 {
		plan.SkipReason = domain.HardwareIOPurgeSkipNothingEligible
	}
	average, _, err := s.hardwareIORangeAverageBytes(ctx, plan.RunStartSequence, plan.RunEndSequence)
	if err != nil {
		return plan, err
	}
	plan.AvgRowBytes = average
	plan.RunBytesEstimate = average * plan.RunRows
	plan.EligibleBytesEstimate = average * plan.EligibleRowsEstimate
	plan.RemainingEligibleEstimate = max(0, cutoffSequence-plan.RunEndSequence)
	return plan, nil
}

// PurgeHardwareIO deletes the rows of a fresh plan in sequence-range batches of
// domain.HardwareIOPurgeBatchRows. Each batch is its own short statement so the
// write lock is released between batches and terminal recording can continue.
// Only hardware_io is touched. A canceled context stops after the current batch
// and reports a partial result.
func (s *Store) PurgeHardwareIO(ctx context.Context, settings domain.HardwareIORetentionSettings, now time.Time) (domain.HardwareIOPurgeResult, error) {
	started := time.Now()
	result := domain.HardwareIOPurgeResult{BatchSize: domain.HardwareIOPurgeBatchRows}
	if err := settings.Validate(); err != nil {
		return result, err
	}
	if !hardwareIOPurgeMu.TryLock() {
		result.SkipReason = domain.HardwareIOPurgeSkipAlreadyRunning
		return result, nil
	}
	defer hardwareIOPurgeMu.Unlock()
	plan, err := s.HardwareIOPurgePlan(ctx, settings, now)
	result.Plan = plan
	if err != nil {
		return result, err
	}
	if plan.SkipReason != "" {
		result.SkipReason = plan.SkipReason
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}
	cutoffKey := hardwareIOCutoffKey(plan.Cutoff)
	for start := plan.RunStartSequence; start < plan.RunEndSequence; start += domain.HardwareIOPurgeBatchRows {
		if ctx.Err() != nil {
			result.Partial = true
			result.SkipReason = domain.HardwareIOPurgeSkipCanceled
			break
		}
		end := min(start+domain.HardwareIOPurgeBatchRows, plan.RunEndSequence)
		// The created_at check is a guard on top of the sequence range: a row
		// written with a clock that jumped backwards is never deleted early.
		deleted, err := s.db.ExecContext(ctx, `
			DELETE FROM hardware_io
			WHERE sequence>=? AND sequence<? AND substr(created_at,1,19)<?`,
			start, end, cutoffKey,
		)
		if err != nil {
			if ctx.Err() != nil {
				result.Partial = true
				result.SkipReason = domain.HardwareIOPurgeSkipCanceled
				break
			}
			result.Partial = true
			result.DurationMS = time.Since(started).Milliseconds()
			return result, fmt.Errorf("delete hardware_io batch %d-%d: %w", start, end, err)
		}
		count, _ := deleted.RowsAffected()
		result.DeletedRows += count
		result.Batches++
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// hardwareIOCutoffKey is the second-precision UTC prefix compared against
// substr(created_at,1,19). Stored values are RFC3339Nano in UTC with a variable
// fraction, so whole-string comparison would misorder rows within one second.
func hardwareIOCutoffKey(cutoff time.Time) string {
	return cutoff.UTC().Format("2006-01-02T15:04:05")
}

// hardwareIOCutoffSequence returns the smallest sequence such that every row
// below it was created before cutoff, assuming created_at grows with sequence.
// It returns maximum+1 when every row is older and minimum when none is.
func (s *Store) hardwareIOCutoffSequence(ctx context.Context, minimum, maximum int64, cutoff time.Time) (int64, error) {
	low, high := minimum, maximum+1
	for low < high {
		middle := low + (high-low)/2
		var sequence int64
		var createdAt string
		err := s.db.QueryRowContext(ctx, `
			SELECT sequence,created_at FROM hardware_io
			WHERE sequence>=? ORDER BY sequence LIMIT 1`, middle,
		).Scan(&sequence, &createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			high = middle
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("probe hardware_io sequence %d: %w", middle, err)
		}
		created := parseTime(createdAt)
		// An unparseable timestamp counts as new so the search stops before it.
		if !created.IsZero() && created.Before(cutoff) {
			low = sequence + 1
		} else {
			high = middle
		}
	}
	return low, nil
}

// hardwareIORangeAverageBytes samples the oldest rows of [start,end) to
// estimate the payload size per row.
func (s *Store) hardwareIORangeAverageBytes(ctx context.Context, start, end int64) (int64, int, error) {
	columns, err := s.tableColumns(ctx, "hardware_io")
	if err != nil || len(columns) == 0 {
		return 0, 0, err
	}
	var average int64
	var sampled int
	err = s.db.QueryRowContext(ctx, `SELECT IFNULL(CAST(AVG(row_bytes) AS INTEGER),0),COUNT(*) FROM (
		SELECT `+rowBytesExpression(columns)+` AS row_bytes FROM hardware_io
		WHERE sequence>=? AND sequence<? ORDER BY sequence LIMIT `+fmt.Sprint(statSampleRows)+`)`,
		start, min(end, start+statSampleRows*4),
	).Scan(&average, &sampled)
	if err != nil {
		return 0, 0, fmt.Errorf("sample hardware_io: %w", err)
	}
	return average, sampled, nil
}
