package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The Task list used to total tokens from full Turn rows, which pulls
// prompt_snapshot/result for every Turn on the page (~34MB for 50 Tasks in
// task-341). This pins the two properties that keep that from coming back:
// the projection selects only the token columns, and SQLite serves it from the
// schema v134 covering index instead of walking the wide row pages.
func TestTurnTokenUsageQueryIsNarrowAndCovering(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	hinted, fallback, args := turnTokenUsageQuery([]string{"task-a"})
	rows, err := database.db.QueryContext(ctx, hinted, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "task_id", "sequence", "backend_session_id", "runtime_config_snapshot_id", "usage_json"}
	if strings.Join(columns, ",") != strings.Join(want, ",") {
		t.Fatalf("turn token usage columns = %v, want %v", columns, want)
	}
	for _, column := range columns {
		switch column {
		case "prompt_snapshot", "result", "instruction", "title":
			t.Fatalf("turn token usage projection reads wide column %q", column)
		}
	}

	plan, err := explainQueryPlanRows(ctx, database, hinted, args...)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "USING COVERING INDEX idx_turns_token_usage") {
		t.Fatalf("turn token usage query is not covered by schema v134 index:\n%s", joined)
	}
	for _, detail := range plan {
		if strings.HasPrefix(detail, "SCAN turns") {
			t.Fatalf("turn token usage query scans the wide turn table:\n%s", joined)
		}
	}

	// A database without the v134 index (hand-edited, or opened before the
	// migration) must still return the same columns instead of failing on the
	// INDEXED BY hint.
	if _, err := database.db.ExecContext(ctx, `DROP INDEX idx_turns_token_usage`); err != nil {
		t.Fatal(err)
	}
	fallbackRows, err := database.queryWithIndexFallback(ctx, hinted, fallback, args...)
	if err != nil {
		t.Fatal(err)
	}
	fallbackColumns, err := fallbackRows.Columns()
	fallbackRows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fallbackColumns, ",") != strings.Join(want, ",") {
		t.Fatalf("fallback columns = %v, want %v", fallbackColumns, want)
	}
}

func explainQueryPlanRows(ctx context.Context, database *Store, query string, args ...any) ([]string, error) {
	rows, err := database.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return nil, err
		}
		details = append(details, detail)
	}
	return details, rows.Err()
}
