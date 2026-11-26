package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func petInsightsPlan(t *testing.T, database *Store, query string, args ...any) string {
	t.Helper()
	rows, err := database.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
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
	return strings.Join(details, "\n")
}

// TestPetInsightsWindowQueriesUseTheirIndexes pins the access path of each
// window query: pet events walk idx_pet_events_at, the pet Task's Turns walk
// a turns index keyed by task_id, and approvals (no created_at index) scan the
// table. It also times the reads on a populated database; the numbers are
// logged for the record, not asserted.
func TestPetInsightsWindowQueriesUseTheirIndexes(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)
	from, to := now.AddDate(0, 0, -8), now

	plan := petInsightsPlan(t, database, petActionEventsQuery(3), timeString(from), timeString(to), "pet", "a", "b", "c")
	t.Logf("action events plan:\n%s", plan)
	if !strings.Contains(plan, "idx_pet_events_at") {
		t.Fatalf("action events do not use idx_pet_events_at:\n%s", plan)
	}
	plan = petInsightsPlan(t, database, petEventCountQuery, timeString(from), timeString(to), "owner_correction")
	if !strings.Contains(plan, "idx_pet_events_at") {
		t.Fatalf("event count does not use idx_pet_events_at:\n%s", plan)
	}
	for name, query := range map[string]string{"count": petMainTurnWindowCountQuery, "usage": petMainTurnWindowUsageQuery} {
		plan = petInsightsPlan(t, database, query, "task_pet", timeString(from), timeString(to))
		t.Logf("turn %s plan:\n%s", name, plan)
		if !strings.Contains(plan, "SEARCH turns USING") || !strings.Contains(plan, "task_id=?") {
			t.Fatalf("turn %s query is not a task_id index search:\n%s", name, plan)
		}
	}
	plan = petInsightsPlan(t, database, petApprovalWindowCountsQuery, timeString(from), timeString(to))
	t.Logf("approval window plan (no created_at index, table scan expected):\n%s", plan)

	// Populate: 20,000 events over 60 days, 2,000 approvals over 60 days, and
	// 3,000 pet Main Turns over 60 days next to 3,000 Turns of another Task.
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20000; index++ {
		at := now.Add(-petInsightsSpread(index, 20000))
		kind := []string{"task_created", "task_message_sent", "control", "owner_correction"}[index%4]
		if _, err := tx.ExecContext(ctx, `INSERT INTO pet_events(id,at,actor,kind,detail,status) VALUES(?,?,?,?,?,?)`,
			fmt.Sprintf("event-%d", index), timeString(at), "pet", kind, fmt.Sprintf("task-%d", index%500), "open"); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 2000; index++ {
		at := now.Add(-petInsightsSpread(index, 2000))
		if _, err := tx.ExecContext(ctx, `INSERT INTO pet_approvals(id,kind,title,summary,status,created_at) VALUES(?,?,?,?,?,?)`,
			fmt.Sprintf("approval-%d", index), []string{"idea", "owner_decision"}[index%2], "t", "s", []string{"pending", "approved", "rejected"}[index%3], timeString(at)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seedInsightTurns(t, database, "task_pet", 3000, now)
	seedInsightTurns(t, database, "task_other", 3000, now)

	started := time.Now()
	events, err := database.ListPetActionEventsInWindow(ctx, "pet", []string{"task_created", "task_message_sent"}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	eventsElapsed := time.Since(started)
	started = time.Now()
	approvals, err := database.PetApprovalCountsInWindow(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	approvalsElapsed := time.Since(started)
	started = time.Now()
	turns, err := database.CountMainTurnsStartedInWindow(ctx, "task_pet", from, to)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := database.ListMainTurnTokenUsageInWindow(ctx, "task_pet", from, to)
	if err != nil {
		t.Fatal(err)
	}
	turnsElapsed := time.Since(started)
	approvalRows := 0
	for _, group := range approvals {
		approvalRows += group.Count
	}
	// 8 of 60 days are in the window.
	if len(events) < 1300 || len(events) > 1400 || approvalRows < 260 || approvalRows > 275 || turns.Started < 395 || turns.Started > 405 || len(usage) != turns.Started {
		t.Fatalf("window rows: events=%d approvals=%d turns=%d usage=%d", len(events), approvalRows, turns.Started, len(usage))
	}
	if turns.Noop == 0 || turns.Noop >= turns.Started {
		t.Fatalf("noop count = %d of %d", turns.Noop, turns.Started)
	}
	t.Logf("window reads on 20000 events / 2000 approvals / 6000 turns: events %s (%d rows), approvals %s (%d rows), turns %s (%d rows)",
		eventsElapsed, len(events), approvalsElapsed, approvalRows, turnsElapsed, turns.Started)
}

// petInsightsSpread places item index of count evenly over 60 days, in
// seconds so the product cannot overflow time.Duration.
func petInsightsSpread(index, count int) time.Duration {
	return time.Duration(int64(index)*60*24*3600/int64(count)) * time.Second
}

// rawConn returns one pooled connection with foreign keys off, so fixtures
// can write Turns and inbox rows without their parent rows. The pragma is
// per connection, which is why it must not go through the pool.
func rawConn(t *testing.T, database *Store) *sql.Conn {
	t.Helper()
	conn, err := database.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
		_ = conn.Close()
	})
	return conn
}

func seedInsightTurns(t *testing.T, database *Store, taskID string, count int, now time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := rawConn(t, database).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < count; index++ {
		at := now.Add(-petInsightsSpread(index, count))
		result := "did a thing"
		if index%3 == 0 {
			result = "NOOP nothing to do"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO turns(id,task_id,agent_id,sequence,input_message_id,status,runtime_config_snapshot_id,queued_at,started_at,finished_at,result,round_id,usage_json)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("%s-turn-%d", taskID, index), taskID, "main", count-index, "message", string(domain.TurnSucceeded), "snapshot",
			timeString(at), timeString(at), timeString(at), result, fmt.Sprintf("%s-round-%d", taskID, index), `{"input_tokens":10}`); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestLatestFinishedMainTurnReplyReadsTheWakeMarker(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.LatestFinishedMainTurnReply(ctx, "task_pet", false); !IsNoRows(err) {
		t.Fatalf("empty table err = %v", err)
	}
	now := time.Now().UTC()
	conn := rawConn(t, database)
	seed := func(sequence int, agent, batch, result string, finished bool, kinds ...string) {
		t.Helper()
		finishedAt := ""
		if finished {
			finishedAt = timeString(now.Add(time.Duration(sequence) * time.Minute))
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO turns(id,task_id,agent_id,sequence,input_message_id,status,runtime_config_snapshot_id,queued_at,finished_at,result,round_id,inbox_batch_id)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("turn-%d", sequence), "task_pet", agent, sequence, "m", "succeeded", "s", timeString(now), finishedAt, result,
			fmt.Sprintf("round-%d", sequence), batch); err != nil {
			t.Fatal(err)
		}
		for index, kind := range kinds {
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,content,status,batch_id,created_at)
				VALUES(?,?,?,?,?,?,?,?,?,?)`,
				fmt.Sprintf("inbox-%d-%d", sequence, index), "task_pet", fmt.Sprintf("round-%d", sequence), "main", "aha", kind, "x", "processed", batch, timeString(now)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(1, "main", "batch-1", "wake reply", true, domain.PetWakeSourceKind)
	seed(2, "main", "batch-2", "owner chat", true, "owner_message")
	seed(3, "helper", "batch-3", "sub reply", true, domain.PetWakeSourceKind)
	seed(4, "main", "batch-4", "still running", false, domain.PetWakeSourceKind)
	seed(5, "main", "batch-5", "   ", true, domain.PetWakeSourceKind)
	seed(6, "main", "batch-6", "mixed wake", true, domain.PetWakeSourceKind, "owner_message")
	// The same batch id on another Task must not mark this Turn.
	seed(7, "main", "batch-7", "plain", true)
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,content,status,batch_id,created_at)
		VALUES('foreign','task_other','r','main','aha',?,'x','processed','batch-7',?)`, domain.PetWakeSourceKind, timeString(now)); err != nil {
		t.Fatal(err)
	}

	latest, err := database.LatestFinishedMainTurnReply(ctx, "task_pet", false)
	if err != nil || latest.TurnID != "turn-7" || latest.FromWake {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}
	wake, err := database.LatestFinishedMainTurnReply(ctx, "task_pet", true)
	if err != nil || wake.TurnID != "turn-6" || !wake.FromWake || !wake.Mixed || wake.Result != "mixed wake" {
		t.Fatalf("wake = %+v err=%v", wake, err)
	}
}
