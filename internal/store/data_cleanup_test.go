package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func setStoreTestTaskStatus(t *testing.T, database *Store, taskID string, status domain.TaskStatus) {
	t.Helper()
	if _, err := database.db.Exec(`UPDATE tasks SET status=? WHERE id=?`, status, taskID); err != nil {
		t.Fatal(err)
	}
}

func seedConversationItem(t *testing.T, database *Store, taskID, category, kind string, createdAt time.Time) string {
	t.Helper()
	id := domain.NewID("conversation")
	if _, err := database.db.Exec(`
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, taskID, "", "", "main", "main", "main", "main", "",
		category, kind, kind+" summary", "{}", timeString(createdAt),
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedConversationAttachment(t *testing.T, database *Store, taskID, messageID string, createdAt time.Time) {
	t.Helper()
	if _, err := database.db.Exec(`
		INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		domain.NewID("attachment"), taskID, messageID, "note.txt", "text/plain", 3, "abc", timeString(createdAt),
	); err != nil {
		t.Fatal(err)
	}
}

func conversationItemExists(t *testing.T, database *Store, id string) bool {
	t.Helper()
	var exists bool
	if err := database.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversation_items WHERE id=?)`, id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestConversationRetentionSettingsDefaultsPersistAndValidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	settings, err := database.ConversationRetentionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RetentionDays != 30 || !settings.PurgeTool || settings.PurgeUpdate {
		t.Fatalf("defaults=%#v", settings)
	}
	for _, invalid := range []domain.ConversationRetentionSettings{
		{RetentionDays: 0, PurgeTool: true},
		{RetentionDays: 3651, PurgeTool: true},
		{RetentionDays: 30},
	} {
		if _, err := database.UpdateConversationRetentionSettings(ctx, invalid); err == nil {
			t.Fatalf("invalid settings accepted: %#v", invalid)
		}
	}
	if _, err := database.UpdateConversationRetentionSettings(ctx, domain.ConversationRetentionSettings{
		RetentionDays: 7, PurgeTool: true, PurgeUpdate: true,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err = database.ConversationRetentionSettings(ctx)
	if err != nil || settings.RetentionDays != 7 || !settings.PurgeTool || !settings.PurgeUpdate || settings.UpdatedAt.IsZero() {
		t.Fatalf("stored=%#v err=%v", settings, err)
	}
	// Saving the conversation settings must not reset the hardware_io ones.
	if _, err := database.UpdateHardwareIORetentionSettings(ctx, domain.HardwareIORetentionSettings{
		RetentionDays: 12, MaxDeleteRows: 4_000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateConversationRetentionSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	hardware, err := database.HardwareIORetentionSettings(ctx)
	if err != nil || hardware.RetentionDays != 12 || hardware.MaxDeleteRows != 4_000 {
		t.Fatalf("hardware settings lost: %#v err=%v", hardware, err)
	}
}

func TestConversationPurgePlanProtectsActiveApprovalsFinalAnswersAndAttachments(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-time.Hour)

	terminal := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, terminal.ID, domain.TaskCompleted)
	eligibleTool := seedConversationItem(t, database, terminal.ID, "tool", "agent_command_finished", old)
	recentTool := seedConversationItem(t, database, terminal.ID, "tool", "agent_command_started", recent)
	eligibleUpdate := seedConversationItem(t, database, terminal.ID, "update", "agent_progress", old)
	channelCard := seedConversationItem(t, database, terminal.ID, "update", "agent_channel_route", old)
	finalAnswer := seedConversationItem(t, database, terminal.ID, "chat", "agent_message", old)
	errorItem := seedConversationItem(t, database, terminal.ID, "error", "turn_failed", old)
	attachedTool := seedConversationItem(t, database, terminal.ID, "tool", "agent_command_finished", old)
	seedConversationAttachment(t, database, terminal.ID, attachedTool, old)

	active := createStoreTestTask(t, database)
	activeTool := seedConversationItem(t, database, active.ID, "tool", "agent_command_finished", old)

	pending := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, pending.ID, domain.TaskCompleted)
	pendingTool := seedConversationItem(t, database, pending.ID, "tool", "agent_command_finished", old)
	if err := database.CreateTaskPermissionRequest(ctx, domain.TaskPermissionRequest{
		ID: domain.NewID("permission"), TaskID: pending.ID, Action: "shell", Status: "pending",
		CreatedAt: old, UpdatedAt: old, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	petApprovalTask := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, petApprovalTask.ID, domain.TaskCompleted)
	petApprovalItem := seedConversationItem(t, database, petApprovalTask.ID, "tool", "agent_command_finished", old)
	// Production shape: the approval row belongs to the pet Task and names the
	// real target only inside payload_json.
	if _, err := database.db.Exec(`
		INSERT INTO pet_approvals(id,task_id,kind,title,summary,payload_json,status,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		domain.NewID("pet_approval"), "task_pet", "delete_task", "Delete task", "waiting",
		fmt.Sprintf(`{"task_id":%q}`, petApprovalTask.ID), "executing", timeString(old)); err != nil {
		t.Fatal(err)
	}

	// Tool only: one row per terminal task, and only the unattached old tool
	// item is eligible.
	settings := domain.ConversationRetentionSettings{RetentionDays: 30, PurgeTool: true}
	plan, err := database.ConversationPurgePlan(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.EligibleRows != 1 || plan.TasksAffected != 1 || plan.RunRows != 1 || plan.SkipReason != "" {
		t.Fatalf("tool plan=%#v", plan)
	}
	if len(plan.Categories) != 1 || plan.Categories[0] != "tool" || !plan.Cutoff.Equal(now.Add(-30*24*time.Hour)) {
		t.Fatalf("tool plan scope=%#v", plan)
	}

	// Tool + update: the old agent_progress update joins, but the channel card
	// stays because update cleanup only accepts agent-progress kinds.
	settings.PurgeUpdate = true
	plan, err = database.ConversationPurgePlan(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.EligibleRows != 2 || plan.TasksAffected != 1 {
		t.Fatalf("tool+update plan=%#v", plan)
	}

	// Seed rows in the neighbouring B tables and in protected tables, then
	// snapshot their counts. Conversation cleanup must only write
	// conversation_items.
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at) VALUES(?,?,?,?,?,?,?)`,
			[]any{"message-1", terminal.ID, "", "assistant", "main", "final reply", timeString(old)}},
		{`INSERT INTO events(id,aggregate_type,aggregate_id,type,data_json,occurred_at) VALUES(?,?,?,?,?,?)`,
			[]any{"event-1", "task", terminal.ID, "test", "{}", timeString(old)}},
		{`INSERT INTO audit_events(id,owner_id,action,resource_type,resource_id,data_json,occurred_at) VALUES(?,?,?,?,?,?,?)`,
			[]any{"audit-1", "owner", "seed", "test", "x", "{}", timeString(old)}},
	} {
		if _, err := database.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	protectedTables := []string{
		"messages", "events", "audit_events", "attachments", "tasks", "projects",
		"task_permission_requests", "knowledge_entries", "channel_instances", "prompt_template_overrides",
	}
	protectedBefore := map[string]int64{}
	for _, table := range protectedTables {
		var count int64
		if err := database.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		protectedBefore[table] = count
	}

	result, err := database.PurgeConversationItems(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 2 || result.Batches != 1 {
		t.Fatalf("purge result=%#v", result)
	}
	for table, before := range protectedBefore {
		var after int64
		if err := database.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("conversation cleanup changed %s: %d -> %d", table, before, after)
		}
	}
	for label, id := range map[string]string{
		"eligible tool": eligibleTool, "eligible update": eligibleUpdate,
	} {
		if conversationItemExists(t, database, id) {
			t.Fatalf("%s was not deleted", label)
		}
	}
	for label, id := range map[string]string{
		"recent tool":      recentTool,
		"channel card":     channelCard,
		"final answer":     finalAnswer,
		"error":            errorItem,
		"attached tool":    attachedTool,
		"active task tool": activeTool,
		"pending approval": pendingTool,
		"pet approval":     petApprovalItem,
	} {
		if !conversationItemExists(t, database, id) {
			t.Fatalf("%s was deleted", label)
		}
	}

	// A cleanup deletes every eligible row, so a fresh preview reports none
	// left rather than a stale "remaining" count.
	after, err := database.ConversationPurgePlan(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if after.EligibleRows != 0 || after.SkipReason != domain.ConversationPurgeSkipNothingEligible {
		t.Fatalf("post-purge plan=%#v", after)
	}
}

func TestPurgeConversationItemsBatchesUntilDone(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	const total = domain.ConversationPurgeBatchRows*2 + 1
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < total; index++ {
		if _, err := statement.ExecContext(ctx, domain.NewID("conversation"), task.ID, "", "", "main", "main", "main", "main", "",
			"tool", "agent_command_finished", "line", "{}", timeString(now.Add(-40*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	statement.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := database.PurgeConversationItems(ctx,
		domain.ConversationRetentionSettings{RetentionDays: 30, PurgeTool: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != total || result.Batches != 3 {
		t.Fatalf("result=%#v", result)
	}
	var remaining int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM conversation_items`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}

func TestDatabaseCompactionPlanBlocksActiveTasksAndLowDisk(t *testing.T) {
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	plan, err := database.DatabaseCompactionPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanCompact || plan.ActiveTasks != 1 || plan.Mode != domain.DatabaseCompactionModeVacuumInPlace {
		t.Fatalf("active plan=%#v", plan)
	}
	foundActive := false
	for _, reason := range plan.BlockingReasons {
		if reason == domain.DatabaseCompactionSkipActive {
			foundActive = true
		}
	}
	if !foundActive {
		t.Fatalf("active reason missing: %#v", plan.BlockingReasons)
	}

	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	if _, err := database.db.Exec(`
		INSERT INTO pet_approvals(id,task_id,kind,title,summary,payload_json,status,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		domain.NewID("pet_approval"), "task_pet", "delete_task", "Delete task", "waiting",
		fmt.Sprintf(`{"task_id":%q}`, task.ID), "pending", timeString(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	plan, err = database.DatabaseCompactionPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanCompact || plan.PetApprovals != 1 {
		t.Fatalf("pet approval was not counted: %#v", plan)
	}
	if _, err := database.db.Exec(`UPDATE pet_approvals SET status='approved',resolution='ok'`); err != nil {
		t.Fatal(err)
	}
	plan, err = database.DatabaseCompactionPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.CanCompact || len(plan.BlockingReasons) != 0 || plan.RequiredTempBytes <= plan.FileBytes {
		t.Fatalf("idle plan=%#v", plan)
	}

	originalDiskFree := databaseDiskFree
	databaseDiskFree = func(string) (int64, error) { return 1, nil }
	t.Cleanup(func() { databaseDiskFree = originalDiskFree })
	plan, err = database.DatabaseCompactionPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanCompact {
		t.Fatalf("low disk still allowed: %#v", plan)
	}
	foundDisk := false
	for _, reason := range plan.BlockingReasons {
		if reason == domain.DatabaseCompactionSkipDisk {
			foundDisk = true
		}
	}
	if !foundDisk {
		t.Fatalf("disk reason missing: %#v", plan.BlockingReasons)
	}
}

func TestCompactDatabaseBacksUpVerifiesAndKeepsRowCounts(t *testing.T) {
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	now := time.Now().UTC()
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(context.Background(), `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 200; index++ {
		if _, err := statement.ExecContext(context.Background(), domain.NewID("conversation"), task.ID, "", "",
			"main", "main", "main", "main", "", "tool", "agent_command_finished", "line", "{}", timeString(now)); err != nil {
			t.Fatal(err)
		}
	}
	statement.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := database.CompactDatabase(context.Background())
	if err != nil {
		t.Fatalf("compact: %v result=%#v", err, result)
	}
	if result.SkipReason != "" || result.BackupPath == "" || result.IntegrityCheck != "ok" {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if result.BeforeRowCounts["conversation_items"] != 200 || result.AfterRowCounts["conversation_items"] != 200 {
		t.Fatalf("row counts=%#v -> %#v", result.BeforeRowCounts, result.AfterRowCounts)
	}
	if result.BeforeRowCounts["tasks"] != 1 || result.BeforeRowCounts["projects"] != 1 {
		t.Fatalf("key tables missing from counts: %#v", result.BeforeRowCounts)
	}
	if filepath.Dir(result.BackupPath) == filepath.Dir(database.path) {
		t.Fatalf("backup was written next to the live database: %s", result.BackupPath)
	}
}

func TestCompactDatabaseRestoresFromBackupWhenVerificationFails(t *testing.T) {
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	original := seedConversationItem(t, database, task.ID, "tool", "agent_command_finished", time.Now().UTC())
	originalHook := databaseCompactAfterVacuum
	databaseCompactAfterVacuum = func(context.Context, *Store) error {
		return errors.New("injected verification failure")
	}
	t.Cleanup(func() { databaseCompactAfterVacuum = originalHook })

	result, err := database.CompactDatabase(context.Background())
	if err == nil {
		t.Fatal("expected the injected verification failure")
	}
	if !result.Restored || result.RestoreError != "" {
		t.Fatalf("restore failed: %#v", result)
	}
	if result.SkipReason != domain.DatabaseCompactionSkipIntegrity {
		t.Fatalf("skip reason=%q result=%#v", result.SkipReason, result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("backup must be kept after rollback: %v", err)
	}
	if !conversationItemExists(t, database, original) {
		t.Fatal("the pre-compaction row was lost")
	}
	var tasks int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("tasks after restore=%d err=%v", tasks, err)
	}
	if len(result.BeforeRowCounts) == 0 {
		t.Fatal("before row counts should be reported")
	}
}

// The conversation preview and the batched DELETE must use the v145 expression
// index, not scan conversation_items on the largest database.
func TestConversationPurgeQueriesUseCleanupIndex(t *testing.T) {
	t.Parallel()
	database := openDataRetentionTestStore(t)
	settings := domain.ConversationRetentionSettings{RetentionDays: 30, PurgeTool: true, PurgeUpdate: true}
	scope, args := conversationPurgeScope(settings, "2026-10-08T00:00:00")
	base := ` FROM conversation_items c
		JOIN tasks t ON t.id=c.task_id
		LEFT JOIN turns tn ON tn.id=c.turn_id AND c.turn_id<>''
		WHERE ` + scope
	cases := map[string]struct {
		query string
		args  []any
	}{
		"count": {
			query: `SELECT COUNT(*),COUNT(DISTINCT c.task_id)` + base,
			args:  args,
		},
		"delete": {
			query: `DELETE FROM conversation_items WHERE sequence IN (
				SELECT c.sequence` + base + ` ORDER BY c.sequence LIMIT ?)`,
			args: append(append([]any{}, args...), domain.ConversationPurgeBatchRows),
		},
	}
	for label, item := range cases {
		plan := strings.Join(explainQueryPlan(t, database, item.query, item.args...), "\n")
		if strings.Contains(plan, "SCAN c") {
			t.Fatalf("%s scans conversation_items:\n%s", label, plan)
		}
		if !strings.Contains(plan, "idx_conversation_items_cleanup") {
			t.Fatalf("%s does not use the cleanup index:\n%s", label, plan)
		}
	}
}

// Database cleanups must snapshot the database before deleting. A backup
// failure aborts the run with every row intact.
func TestDatabaseCleanupBacksUpBeforeDeletingAndAbortsOnBackupFailure(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	terminal := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, terminal.ID, domain.TaskCompleted)
	seedConversationItem(t, database, terminal.ID, "tool", "agent_command_finished", old)

	settings := domain.ConversationRetentionSettings{RetentionDays: 30, PurgeTool: true}
	result, err := database.PurgeConversationItems(ctx, settings, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == "" {
		t.Fatal("conversation cleanup did not report a backup")
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("conversation cleanup backup missing: %v", err)
	}
	if _, err := database.db.Exec(`ATTACH DATABASE ? AS backup_check`, result.BackupPath); err != nil {
		t.Fatal(err)
	}
	var backedUp int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM backup_check.conversation_items`).Scan(&backedUp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DETACH DATABASE backup_check`); err != nil {
		t.Fatal(err)
	}
	if backedUp != 1 {
		t.Fatalf("backup was taken after the delete: %d rows", backedUp)
	}

	// A failed backup must abort before any DELETE.
	seedConversationItem(t, database, terminal.ID, "tool", "agent_command_finished", old)
	originalBackup := databasePurgeBackup
	databasePurgeBackup = func(context.Context, *Store, string) (string, error) {
		return "", errors.New("injected backup failure")
	}
	t.Cleanup(func() { databasePurgeBackup = originalBackup })
	result, err = database.PurgeConversationItems(ctx, settings, now)
	if err == nil || result.DeletedRows != 0 {
		t.Fatalf("backup failure did not abort: result=%#v err=%v", result, err)
	}
	var remaining int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM conversation_items`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("rows after failed backup=%d err=%v", remaining, err)
	}

	// hardware_io uses the same pre-delete backup.
	seedHardwareIO(t, database, terminal.ID, 100, old, time.Hour)
	resultHardware, err := database.PurgeHardwareIO(ctx,
		domain.HardwareIORetentionSettings{RetentionDays: 30, MaxDeleteRows: 1000}, now)
	if err == nil || resultHardware.DeletedRows != 0 {
		t.Fatalf("hardware backup failure did not abort: result=%#v err=%v", resultHardware, err)
	}
	if hardwareIOCount(t, database) != 100 {
		t.Fatalf("hardware rows deleted despite backup failure: %d", hardwareIOCount(t, database))
	}
}

// Compaction holds a SQLite exclusive lock from before the backup through
// verification. An external writer attempted after VACUUM must be refused, and
// a verification failure must restore the pre-compaction rows without a
// phantom "new write" to lose.
func TestCompactDatabaseBlocksWritesAndRestoresWithoutLosingData(t *testing.T) {
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	original := seedConversationItem(t, database, task.ID, "tool", "agent_command_finished", time.Now().UTC())
	external, err := sql.Open("sqlite", database.path+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { external.Close() })
	var writeErr error
	originalHook := databaseCompactAfterVacuum
	databaseCompactAfterVacuum = func(ctx context.Context, _ *Store) error {
		_, writeErr = external.ExecContext(ctx, `
			INSERT INTO conversation_items(
				id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
				category,kind,summary,payload_json,created_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			"conversation_during_compaction", task.ID, "", "", "main", "main", "main", "main", "",
			"tool", "agent_command_finished", "must not commit", "{}", timeString(time.Now().UTC()))
		return errors.New("injected verification failure")
	}
	t.Cleanup(func() { databaseCompactAfterVacuum = originalHook })
	result, err := database.CompactDatabase(context.Background())
	if err == nil {
		t.Fatal("expected the injected verification failure")
	}
	if writeErr == nil || !IsSQLiteBusy(writeErr) {
		t.Fatalf("external write was not blocked by the maintenance lock: %v", writeErr)
	}
	if !result.Restored {
		t.Fatalf("restore did not run: %#v", result)
	}
	if conversationItemExists(t, database, "conversation_during_compaction") {
		t.Fatal("a write committed despite the exclusive lock")
	}
	if !conversationItemExists(t, database, original) {
		t.Fatal("the pre-compaction row was lost")
	}
}

// A running compaction holds the maintenance gate exclusively; a cleanup
// started at the same time reports already_running instead of blocking or
// deleting rows.
func TestMaintenanceGateSkipsCleanupWhileCompactionRuns(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	seedConversationItem(t, database, task.ID, "tool", "agent_command_finished", now.Add(-40*24*time.Hour))
	seedHardwareIO(t, database, task.ID, 10, now.Add(-40*24*time.Hour), time.Hour)

	databaseMaintenanceGate.Lock()
	defer databaseMaintenanceGate.Unlock()
	conversation, err := database.PurgeConversationItems(ctx,
		domain.ConversationRetentionSettings{RetentionDays: 30, PurgeTool: true}, now)
	if err != nil || conversation.SkipReason != domain.ConversationPurgeSkipAlreadyRunning || conversation.DeletedRows != 0 {
		t.Fatalf("conversation=%#v err=%v", conversation, err)
	}
	hardware, err := database.PurgeHardwareIO(ctx,
		domain.HardwareIORetentionSettings{RetentionDays: 30, MaxDeleteRows: 1000}, now)
	if err != nil || hardware.SkipReason != domain.HardwareIOPurgeSkipAlreadyRunning || hardware.DeletedRows != 0 {
		t.Fatalf("hardware=%#v err=%v", hardware, err)
	}
	compaction, err := database.CompactDatabase(ctx)
	if err != nil || compaction.SkipReason != domain.DatabaseCompactionSkipAlreadyRunning {
		t.Fatalf("compaction=%#v err=%v", compaction, err)
	}
	if !conversationItemPresentInStore(t, database, task.ID) || hardwareIOCount(t, database) != 10 {
		t.Fatal("a skipped cleanup deleted rows")
	}
}

func conversationItemPresentInStore(t *testing.T, database *Store, taskID string) bool {
	t.Helper()
	var count int64
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM conversation_items WHERE task_id=?`, taskID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

// A writer that already holds a SQLite write transaction blocks the exclusive
// maintenance lock. Compaction must refuse with active_writes, before creating
// any backup or VACUUM.
func TestCompactDatabaseRefusesWhenAnActiveWriteTransactionExists(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	external, err := sql.Open("sqlite", database.path+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	conn, err := external.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE tasks SET updated_at=updated_at WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := database.CompactDatabase(ctx)
	if err != nil {
		t.Fatalf("busy lock surfaced as an error instead of a skip: %v result=%#v", err, result)
	}
	if result.SkipReason != domain.DatabaseCompactionSkipActiveWrites {
		t.Fatalf("skip reason=%q result=%#v", result.SkipReason, result)
	}
	if result.BackupPath != "" {
		t.Fatalf("backup was created before the lock was acquired: %s", result.BackupPath)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("busy lock took %s to refuse", elapsed)
	}
	if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(database.path), "backups"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "database-compact-") {
			t.Fatalf("compaction left a backup after refusing: %s", entry.Name())
		}
	}
}

// Repeated compactions keep only the newest full-database snapshot.
func TestCompactDatabaseKeepsOnlyNewestBackup(t *testing.T) {
	ctx := context.Background()
	database := openDataRetentionTestStore(t)
	task := createStoreTestTask(t, database)
	setStoreTestTaskStatus(t, database, task.ID, domain.TaskCompleted)
	first, err := database.CompactDatabase(ctx)
	if err != nil || first.BackupPath == "" {
		t.Fatalf("first compaction=%#v err=%v", first, err)
	}
	firstDir := filepath.Dir(first.BackupPath)
	second, err := database.CompactDatabase(ctx)
	if err != nil || second.BackupPath == "" {
		t.Fatalf("second compaction=%#v err=%v", second, err)
	}
	if _, err := os.Stat(firstDir); !os.IsNotExist(err) {
		t.Fatalf("old compaction backup still exists: %v", err)
	}
	if _, err := os.Stat(second.BackupPath); err != nil {
		t.Fatalf("new compaction backup missing: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(database.path), "backups"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "database-compact-") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("database-compact backups=%d, want 1", count)
	}
}
