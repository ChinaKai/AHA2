package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func makeLinePatch(id string) domain.TaskLinePatch {
	return domain.TaskLinePatch{ID: id, Title: stringPtr("Line " + id), Owner: stringPtr("self"), State: stringPtr("open")}
}

func keepTaskMemory(*domain.TaskMemory) (*domain.TaskGoalChange, error) { return nil, nil }

func TestTaskLinesMergeAndHistory(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createStoreTestTask(t, db)
	turn := domain.Turn{ID: "turn-1", RoundID: "round-1"}
	now := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)
	write := func(patches ...domain.TaskLinePatch) {
		t.Helper()
		if err := db.UpdateTaskMemory(ctx, task.ID, turn, now, patches, keepTaskMemory); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	read := func() domain.TaskMemory {
		t.Helper()
		memory, err := db.TaskMemory(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		return memory
	}
	write(makeLinePatch("device"), makeLinePatch("docs"))
	opened := read().Lines
	update := domain.TaskLinePatch{ID: "device", Title: stringPtr("Validate device"), Owner: stringPtr("other"),
		Who: stringPtr("Lab"), State: stringPtr("waiting"), Step: stringPtr("Build sent"), Blocker: stringPtr("Waiting on device")}
	write(update)
	write(update)
	transfer := domain.TaskLinePatch{ID: "device", Who: stringPtr("QA"), Step: stringPtr("New build sent"), Blocker: stringPtr("")}
	write(transfer)
	closeDocs := domain.TaskLinePatch{ID: "docs", State: stringPtr("closed")}
	write(closeDocs)
	write(closeDocs)
	write() // A caller without lines leaves the entire line snapshot untouched.

	memory := read()
	if len(memory.Lines) != 2 {
		t.Fatalf("expected two lines: %+v", memory.Lines)
	}
	device, docs := memory.Lines[0], memory.Lines[1]
	if device.ID != "device" || device.Owner != "other" || device.Who != "QA" || device.Step != "New build sent" ||
		device.Blocker != "" || device.State != "waiting" || !device.OpenedAt.Equal(opened[0].OpenedAt) ||
		!device.UpdatedAt.Equal(opened[0].OpenedAt.Add(3*time.Second)) || device.ClosedAt != nil {
		t.Fatalf("merged device line: %+v", device)
	}
	if docs.Title != opened[1].Title || docs.State != "closed" || docs.ClosedAt == nil || !docs.ClosedAt.Equal(docs.UpdatedAt) ||
		!docs.OpenedAt.Equal(opened[1].OpenedAt) || !docs.UpdatedAt.Equal(opened[1].OpenedAt.Add(4*time.Second)) {
		t.Fatalf("closed docs line: %+v", docs)
	}
	cards, err := db.TaskStateCards(ctx, []string{task.ID})
	if err != nil || !reflect.DeepEqual(cards[task.ID].Lines, memory.Lines) {
		t.Fatalf("batch card = %+v, err=%v", cards[task.ID], err)
	}

	history, err := db.TaskLineEvents(ctx, task.ID)
	wantKinds := []string{"opened", "opened", "updated", "updated", "closed"}
	if err != nil || len(history) != len(wantKinds) {
		t.Fatalf("history = %+v, err=%v", history, err)
	}
	for i, event := range history {
		if event.Kind != wantKinds[i] || event.TaskID != task.ID || event.LineID != event.After.ID ||
			event.RoundID != turn.RoundID || event.TurnID != turn.ID || !event.CreatedAt.Equal(event.After.UpdatedAt) ||
			(i > 0 && event.Sequence <= history[i-1].Sequence) {
			t.Fatalf("event %d: %+v", i, event)
		}
	}
	if history[0].Before != nil || !reflect.DeepEqual(history[0].After, opened[0]) ||
		!reflect.DeepEqual(history[2].Before, &opened[0]) || history[2].After.Title != "Validate device" ||
		history[2].After.Owner != "other" || history[2].After.Who != "Lab" || history[2].After.Step != "Build sent" ||
		history[2].After.Blocker != "Waiting on device" || history[2].After.State != "waiting" ||
		!reflect.DeepEqual(history[3].Before, &history[2].After) || !reflect.DeepEqual(history[3].After, device) ||
		!reflect.DeepEqual(history[4].Before, &opened[1]) || !reflect.DeepEqual(history[4].After, docs) {
		t.Fatalf("before/after snapshots don't describe the changes: %+v", history)
	}
	if err := db.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if events, err := db.TaskLineEvents(ctx, task.ID); err != nil || len(events) != 0 {
		t.Fatalf("deleted Task left events: %+v, %v", events, err)
	}
}

func TestTaskLinesValidationAndWriteRollback(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createStoreTestTask(t, db)
	turn := domain.Turn{ID: "turn-1", RoundID: "round-1"}
	now := time.Now().UTC()
	write := func(patches ...domain.TaskLinePatch) error {
		return db.UpdateTaskMemory(ctx, task.ID, turn, now, patches, keepTaskMemory)
	}
	closed := makeLinePatch("closed")
	closed.Title = stringPtr(strings.Repeat("长", 200))
	if err := write(makeLinePatch("open"), closed); err != nil {
		t.Fatal(err)
	}
	if err := write(domain.TaskLinePatch{ID: "closed", State: stringPtr("closed")}); err != nil {
		t.Fatal(err)
	}

	before, err := db.TaskMemory(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents, err := db.TaskLineEvents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkUnchanged := func(t *testing.T) {
		t.Helper()
		after, err := db.TaskMemory(ctx, task.ID)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatalf("rejection changed the state card: %+v, %v", after, err)
		}
		events, err := db.TaskLineEvents(ctx, task.ID)
		if err != nil || !reflect.DeepEqual(events, beforeEvents) {
			t.Fatalf("rejection changed event history: %+v, %v", events, err)
		}
		storedTask, err := db.Task(ctx, task.ID)
		if err != nil || storedTask.CurrentGoal != task.CurrentGoal {
			t.Fatalf("rejection changed the task goal: %+v, %v", storedTask, err)
		}
	}
	tooLong := stringPtr(strings.Repeat("长", 201))
	for _, test := range []struct {
		name   string
		patch  domain.TaskLinePatch
		reject string
	}{
		{"empty ID", domain.TaskLinePatch{}, "line id must"},
		{"unsafe ID", makeLinePatch("bad/id"), "line id must"},
		{"leading punctuation", makeLinePatch("_id"), "line id must"},
		{"long ID", makeLinePatch(strings.Repeat("a", 33)), "line id must"},
		{"duplicate ID", domain.TaskLinePatch{ID: "valid"}, "more than once"},
		{"missing title", domain.TaskLinePatch{ID: "open", Title: stringPtr(" ")}, "needs a title"},
		{"long title", domain.TaskLinePatch{ID: "open", Title: tooLong}, "title holds at most 200"},
		{"long who", domain.TaskLinePatch{ID: "open", Who: tooLong}, "who holds at most 200"},
		{"long step", domain.TaskLinePatch{ID: "open", Step: tooLong}, "step holds at most 200"},
		{"long blocker", domain.TaskLinePatch{ID: "open", Blocker: tooLong}, "blocker holds at most 200"},
		{"invalid owner", domain.TaskLinePatch{ID: "open", Owner: stringPtr("stranger")}, "owner must be"},
		{"invalid state", domain.TaskLinePatch{ID: "open", State: stringPtr("finished")}, "state must be"},
		{"other without who", domain.TaskLinePatch{ID: "open", Owner: stringPtr("other"), Who: stringPtr(" ")}, "needs who"},
		{"change closed", domain.TaskLinePatch{ID: "closed", Step: stringPtr("more work")}, "is closed"},
		{"close unopened", domain.TaskLinePatch{ID: "never", Title: stringPtr("Never"), Owner: stringPtr("self"), State: stringPtr("closed")}, "must be opened"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := write(makeLinePatch("valid"), test.patch)
			if err == nil || !strings.Contains(err.Error(), test.reject) {
				t.Fatalf("wanted %q, got %v", test.reject, err)
			}
			checkUnchanged(t)
		})
	}

	// Abort the final card write, after the line and goal events have run.
	if _, err := db.db.ExecContext(ctx, `
		CREATE TRIGGER fail_task_card BEFORE UPDATE OF status ON task_memory
		WHEN NEW.status = 'injected-failure' BEGIN SELECT RAISE(ABORT, 'fail task card'); END
	`); err != nil {
		t.Fatal(err)
	}
	err = db.UpdateTaskMemory(ctx, task.ID, turn, now.Add(time.Second), []domain.TaskLinePatch{makeLinePatch("new")},
		func(memory *domain.TaskMemory) (*domain.TaskGoalChange, error) {
			memory.Status = "injected-failure"
			return &domain.TaskGoalChange{TaskID: task.ID, GoalAfter: "Changed goal", ChangeType: "revise", Reason: "Test"}, nil
		})
	if err == nil || !strings.Contains(err.Error(), "fail task card") {
		t.Fatalf("did not hit injected failure: %v", err)
	}
	checkUnchanged(t)
}

func TestTaskLinesLimitAndArchivedReopen(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createStoreTestTask(t, db)
	now := time.Now().UTC()
	turn := domain.Turn{ID: "turn-1"}
	write := func(patches ...domain.TaskLinePatch) error {
		now = now.Add(time.Second)
		return db.UpdateTaskMemory(ctx, task.ID, turn, now, patches, keepTaskMemory)
	}
	mustWrite := func(patches ...domain.TaskLinePatch) {
		t.Helper()
		if err := write(patches...); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < taskLineMaxRecentClosed+1; i++ {
		id := fmt.Sprintf("old-%d", i)
		mustWrite(makeLinePatch(id))
		mustWrite(domain.TaskLinePatch{ID: id, State: stringPtr("closed")})
	}
	if err := write(domain.TaskLinePatch{ID: "old-0", Step: stringPtr("can't update this closed line")}); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("archived closure was not enforced: %v", err)
	}
	var active []domain.TaskLinePatch
	for i := 0; i < domain.TaskLineMaxActive; i++ {
		active = append(active, makeLinePatch(fmt.Sprintf("active-%d", i)))
	}
	mustWrite(active...)
	before, err := db.TaskMemory(ctx, task.ID)
	if err != nil || len(before.Lines) != taskLineMaxRecentClosed+domain.TaskLineMaxActive || before.Lines[0].ID != "old-1" {
		t.Fatalf("retained lines: %+v, %v", before.Lines, err)
	}
	if err := write(makeLinePatch("over")); err == nil || !strings.Contains(err.Error(), "at most 8") {
		t.Fatalf("ninth active line should be rejected: %v", err)
	}
	after, err := db.TaskMemory(ctx, task.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("over-limit update changed the card: %+v, %v", after.Lines, err)
	}
	// The count applies to the final card, so the reopen may precede the close.
	turn = domain.Turn{ID: "turn-2", RoundID: "round-2"}
	mustWrite(domain.TaskLinePatch{ID: "old-0", State: stringPtr("open")}, domain.TaskLinePatch{ID: "active-0", State: stringPtr("closed")})
	history, err := db.TaskLineEvents(ctx, task.ID)
	if err != nil || len(history) != 2*(taskLineMaxRecentClosed+1)+domain.TaskLineMaxActive+2 {
		t.Fatalf("event counts: %d, %v", len(history), err)
	}
	reopened := history[len(history)-2]
	if reopened.Kind != "reopened" || reopened.TurnID != turn.ID || reopened.RoundID != turn.RoundID ||
		!reflect.DeepEqual(reopened.Before, &history[1].After) || reopened.After.Title != "Line old-0" ||
		reopened.After.State != "open" || reopened.After.ClosedAt != nil || !reopened.After.OpenedAt.Equal(history[0].CreatedAt) {
		t.Fatalf("reopen event = %+v", reopened)
	}
}

func TestTaskLinesConcurrentWritesMerge(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := createStoreTestTask(t, db)
	var writers sync.WaitGroup
	for _, id := range []string{"device", "docs", "device"} {
		writers.Add(1)
		go func(id string) {
			defer writers.Done()
			err := db.UpdateTaskMemory(ctx, task.ID, domain.Turn{ID: "turn-1"}, time.Now().UTC(),
				[]domain.TaskLinePatch{makeLinePatch(id)}, keepTaskMemory)
			if err != nil {
				t.Error(err)
			}
		}(id)
	}
	writers.Wait()
	memory, err := db.TaskMemory(ctx, task.ID)
	if err != nil || len(memory.Lines) != 2 {
		t.Fatalf("concurrent writes lost a line: %+v, %v", memory.Lines, err)
	}
	history, err := db.TaskLineEvents(ctx, task.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("concurrent retries duplicated events: %+v, %v", history, err)
	}
}

func TestTaskLinesMigrationRollbackRetryAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	task := createStoreTestTask(t, db)
	if err := db.UpsertTaskMemory(ctx, domain.TaskMemory{
		TaskID: task.ID, CurrentGoal: task.CurrentGoal, Status: "Existing state card", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `
		DROP TABLE task_line_events;
		ALTER TABLE task_memory DROP COLUMN lines_json;
		DELETE FROM schema_migrations WHERE version=167;
		CREATE TRIGGER fail_task_line_migration BEFORE INSERT ON schema_migrations
		WHEN NEW.version=167 BEGIN SELECT RAISE(ABORT, 'fail v167 marker'); END;
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateTaskLines(ctx); err == nil || !strings.Contains(err.Error(), "fail v167 marker") {
		t.Fatalf("migration must fail after DDL: %v", err)
	}
	var table, marker bool
	if err := db.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sqlite_master WHERE name='task_line_events'),
		EXISTS(SELECT 1 FROM schema_migrations WHERE version=167)`).Scan(&table, &marker); err != nil {
		t.Fatal(err)
	}
	if table || marker || db.tableHasColumn(ctx, "task_memory", "lines_json") {
		t.Fatal("failed migration left part of v167 installed")
	}
	if _, err := db.db.ExecContext(ctx, `DROP TRIGGER fail_task_line_migration`); err != nil {
		t.Fatal(err)
	}

	db = reopenAfterMigration(t, db, path)
	card, err := db.TaskMemory(ctx, task.ID)
	if err != nil || card.Status != "Existing state card" || len(card.Lines) != 0 {
		t.Fatalf("migration did not preserve the old card: %+v, %v", card, err)
	}
	if err := db.UpdateTaskMemory(ctx, task.ID, domain.Turn{}, time.Now().UTC(),
		[]domain.TaskLinePatch{makeLinePatch("persist")}, keepTaskMemory); err != nil {
		t.Fatal(err)
	}
	db = reopenAfterMigration(t, db, path)
	card, err = db.TaskMemory(ctx, task.ID)
	if err != nil || len(card.Lines) != 1 || card.Lines[0].ID != "persist" || card.Status != "Existing state card" {
		t.Fatalf("reopening erased line or card data: %+v, %v", card, err)
	}
	var markers int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=167`).Scan(&markers); err != nil || markers != 1 {
		t.Fatalf("migration markers = %d, %v", markers, err)
	}
}
