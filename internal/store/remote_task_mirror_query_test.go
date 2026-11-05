package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func seedMirrorTask(t testing.TB, database *Store, owner, sourceID, projectID, title, status string, updatedAt time.Time) {
	t.Helper()
	task := domain.Task{
		ID: sourceID, ProjectID: projectID, Title: title, Status: domain.TaskStatus(status),
		CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt,
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRemoteTaskObject(context.Background(), RemoteTaskObject{
		OwnerDeviceID: owner, ObjectType: "task", ObjectID: sourceID, TaskID: sourceID,
		ProjectID: projectID, Payload: payload, CreatedAt: task.CreatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func explainQueryPlan(t *testing.T, database *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := database.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return details
}

func assertIndexOnlyPlan(t *testing.T, label string, details []string, wantIndex string) {
	t.Helper()
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "USING INDEX "+wantIndex) {
		t.Fatalf("%s did not use %s:\n%s", label, wantIndex, plan)
	}
	if strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("%s still sorts in a temporary B-tree:\n%s", label, plan)
	}
	for _, detail := range details {
		if detail == "SCAN sync_remote_task_objects" {
			t.Fatalf("%s still scans the whole mirror table:\n%s", label, plan)
		}
	}
}

// The list refresh used to scan all 133k mirror rows (mostly conversation
// items) and sort them to return ~50 Tasks. Schema v119 adds partial indexes
// that both filters and the ORDER BY can use; this test fails if a rewrite
// reintroduces the full scan or the temporary sort.
func TestSyncRemoteTaskObjectIndexesAndQueryPlans(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=119)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v119 not recorded: migrated=%t err=%v", migrated, err)
	}
	for _, index := range []string{
		"idx_sync_remote_task_objects_task_updated",
		"idx_sync_remote_task_objects_task_project_updated",
	} {
		var exists bool
		if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='index' AND name=?)`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s missing: exists=%t err=%v", index, exists, err)
		}
	}

	// Seed conversation rows so the plan has to choose between the old index
	// and the task-only partial indexes, like the production table does.
	now := time.Now().UTC()
	for index := 0; index < 200; index++ {
		if _, err := database.db.ExecContext(ctx, `INSERT INTO sync_remote_task_objects(owner_device_id,object_type,object_id,task_id,project_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			"dev-shared", "conversation", fmt.Sprintf("conv-%03d", index), "source-task", "project-shared", "{}", timeString(now), timeString(now),
		); err != nil {
			t.Fatal(err)
		}
	}
	seedMirrorTask(t, database, "dev-a", "source-task-a", "project-shared", "A", string(domain.TaskActive), now)
	seedMirrorTask(t, database, "dev-b", "source-task-b", "project-other", "B", string(domain.TaskWaitingUser), now.Add(time.Minute))

	assertIndexOnlyPlan(t, "RemoteTaskMirrors", explainQueryPlan(t, database, remoteTaskMirrorsQuery),
		"idx_sync_remote_task_objects_task_updated")
	projectPlan := explainQueryPlan(t, database, remoteTaskMirrorsQueryByProject, "project-shared")
	projectExec := strings.Join(projectPlan, "\n")
	if !strings.Contains(projectExec, "SEARCH sync_remote_task_objects USING INDEX idx_sync_remote_task_objects_task_project_updated") {
		t.Fatalf("project-filtered mirrors did not use the project index:\n%s", projectExec)
	}
	if strings.Contains(projectExec, "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("project-filtered mirrors still sort in a temporary B-tree:\n%s", projectExec)
	}

	facetPlan := strings.Join(explainQueryPlan(t, database, taskFacetMirrorCountsQuery), "\n")
	if !strings.Contains(facetPlan, "USING INDEX idx_sync_remote_task_objects_task_updated") {
		t.Fatalf("facet counts did not use the task partial index:\n%s", facetPlan)
	}
	if strings.Contains(facetPlan, "USING INDEX idx_sync_remote_task_objects_task\n") ||
		strings.HasSuffix(facetPlan, "USING INDEX idx_sync_remote_task_objects_task") {
		t.Fatalf("facet counts fell back to the unfiltered task index:\n%s", facetPlan)
	}

	// Re-applying the migration over a database that already holds rows must be
	// idempotent: an upgrade keeps the mirror data and recreates nothing.
	var before, after int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_remote_task_objects`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=119`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("re-apply schema v119: %v", err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_remote_task_objects`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("re-applying v119 changed mirror rows: before=%d after=%d", before, after)
	}
}

func TestRemoteTaskMirrorsOrderingAndProjectFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	seedMirrorTask(t, database, "dev-a", "source-1", "project-1", "first", string(domain.TaskWaitingUser), base)
	seedMirrorTask(t, database, "dev-a", "source-2", "project-2", "second", string(domain.TaskActive), base.Add(2*time.Minute))
	seedMirrorTask(t, database, "dev-b", "source-3", "project-1", "third", string(domain.TaskCompleted), base.Add(2*time.Minute))
	seedMirrorTask(t, database, "dev-b", "source-4", "project-2", "fourth", string(domain.TaskDraft), base.Add(time.Minute))
	// A conversation object with the newest updated_at must never leak into
	// the Task list.
	if _, err := database.db.ExecContext(ctx, `INSERT INTO sync_remote_task_objects(owner_device_id,object_type,object_id,task_id,project_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"dev-a", "conversation", "newest-conversation", "source-1", "project-1", "{}", timeString(base), timeString(base.Add(time.Hour)),
	); err != nil {
		t.Fatal(err)
	}

	mirrors, err := database.RemoteTaskMirrors(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(mirrors))
	for _, mirror := range mirrors {
		got = append(got, mirror.SourceTaskID)
		if !mirror.Task.ReadOnly || mirror.Task.OwnerDeviceID == "" {
			t.Fatalf("mirror lost its read-only owner decoration: %#v", mirror.Task)
		}
		if !strings.HasPrefix(mirror.Task.ID, "remote_") {
			t.Fatalf("mirror public ID = %q", mirror.Task.ID)
		}
	}
	// updated_at DESC, then owner_device_id DESC, then task_id DESC.
	want := []string{"source-3", "source-2", "source-4", "source-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mirror order = %v, want %v", got, want)
	}

	filtered, err := database.RemoteTaskMirrors(ctx, "project-1")
	if err != nil {
		t.Fatal(err)
	}
	gotFiltered := make([]string, 0, len(filtered))
	for _, mirror := range filtered {
		gotFiltered = append(gotFiltered, mirror.SourceTaskID)
		if mirror.Task.ProjectID != "project-1" {
			t.Fatalf("project filter leaked %q", mirror.Task.ProjectID)
		}
	}
	if strings.Join(gotFiltered, ",") != "source-3,source-1" {
		t.Fatalf("project-filtered order = %v", gotFiltered)
	}
	if _, err := database.RemoteTaskMirrors(ctx, "project-missing"); err != nil {
		t.Fatal(err)
	}
}

func TestTaskFacetCountsStillCountsMirrorsAndLocalTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	local := createStoreTestTask(t, database)
	now := time.Now().UTC()
	seedMirrorTask(t, database, "dev-a", "source-facet-1", "project-mirror", "mirror 1", string(domain.TaskWaitingUser), now)
	seedMirrorTask(t, database, "dev-b", "source-facet-2", "project-mirror", "mirror 2", string(domain.TaskWaitingUser), now)
	seedMirrorTask(t, database, "dev-b", "source-facet-3", "project-mirror-2", "mirror 3", string(domain.TaskCompleted), now)
	seedMirrorTask(t, database, "", "source-facet-4", "project-mirror", "mirror 4", string(domain.TaskDraft), now)

	counts, err := database.TaskFacetCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Project[local.ProjectID] != 1 {
		t.Fatalf("local project count = %d", counts.Project[local.ProjectID])
	}
	if counts.Project["project-mirror"] != 3 || counts.Project["project-mirror-2"] != 1 {
		t.Fatalf("mirror project counts = %#v", counts.Project)
	}
	if counts.Status[string(domain.TaskWaitingUser)] != 2 || counts.Status[string(domain.TaskCompleted)] != 1 {
		t.Fatalf("mirror status counts = %#v", counts.Status)
	}
	if counts.Device[TaskDeviceLocalKey] != 1 ||
		counts.Device[TaskDeviceRemotePrefix+"dev-a"] != 1 ||
		counts.Device[TaskDeviceRemotePrefix+"dev-b"] != 2 ||
		counts.Device[TaskDeviceRemoteUnknown] != 1 {
		t.Fatalf("device counts = %#v", counts.Device)
	}
}

// BenchmarkRemoteTaskMirrors exercises the production shape: a large
// conversation mirror set and a small Task subset. Before schema v119 the
// "all" case scanned every row and sorted it; the report compares cold and
// warm timings.
func BenchmarkRemoteTaskMirrors(b *testing.B) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(b.TempDir(), "aha2.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO sync_remote_task_objects(owner_device_id,object_type,object_id,task_id,project_id,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 20000; index++ {
		if _, err := insert.ExecContext(ctx, "dev-shared", "conversation", fmt.Sprintf("conversation-%05d", index),
			"source-bench", "project-bench", `{"summary":"mirror conversation"}`, timeString(now), timeString(now)); err != nil {
			b.Fatal(err)
		}
	}
	insert.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 50; index++ {
		seedMirrorTask(b, database, fmt.Sprintf("dev-%02d", index%3), fmt.Sprintf("source-bench-%02d", index),
			"project-bench", fmt.Sprintf("Task %d", index), string(domain.TaskActive), now.Add(time.Duration(index)*time.Second))
	}

	b.Run("all", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, err := database.RemoteTaskMirrors(ctx, ""); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("project", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, err := database.RemoteTaskMirrors(ctx, "project-bench"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("facet_counts", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, err := database.TaskFacetCounts(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
