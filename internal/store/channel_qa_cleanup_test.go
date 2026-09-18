package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSchemaV67RemovesOnlyUncuratedChannelAnswers pins the v67 cleanup rule.
//
// The rule is a whole-subtree decision, not a per-row one, and both directions
// are load-bearing: a verified answer filed under a noise parent must survive
// with that parent, and a noise answer with a verified child must survive with
// its child. A per-row guard gets one of the two wrong and leaves the tree
// damaged in a way nothing else would notice.
func TestSchemaV67RemovesOnlyUncuratedChannelAnswers(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const projectID = "project_v67"
	now := "2026-01-01T00:00:00Z"
	seed := []struct{ id, parent, status string }{
		{"qa_curated", "", "verified"},                  // curated by an operator: keep
		{"qa_noise", "", "observed"},                    // auto-captured noise: remove
		{"qa_noise_child", "qa_noise", "observed"},      // its noise child: remove too
		{"qa_kept_parent", "", "observed"},              // ancestor of a kept entry: keep
		{"qa_kept_child", "qa_kept_parent", "verified"}, // curated below it: keep
	}
	for _, item := range seed {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO knowledge_entries(id,scope,project_id,parent_id,slug,type,title,body,status,confidence,revision,content_hash,source_task_id,source_turn_id,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			item.id, "project", projectID, item.parent, item.id, "channel_qa", item.id, "body",
			item.status, 1, 1, "hash", "", "", now, now); err != nil {
			t.Fatalf("seed %s: %v", item.id, err)
		}
	}
	// v67 has already run on this fresh database, so rewind its marker to replay
	// it over the seeded rows.
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=67`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("replay v67: %v", err)
	}

	remaining := map[string]bool{}
	rows, err := database.db.QueryContext(ctx, `SELECT id FROM knowledge_entries WHERE type='channel_qa'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		remaining[id] = true
	}
	for _, want := range []string{"qa_curated", "qa_kept_parent", "qa_kept_child"} {
		if !remaining[want] {
			t.Fatalf("%s must survive v67; remaining=%v", want, remaining)
		}
	}
	for _, gone := range []string{"qa_noise", "qa_noise_child"} {
		if remaining[gone] {
			t.Fatalf("%s must be removed by v67; remaining=%v", gone, remaining)
		}
	}
	// The kept parent must still be reachable from its kept child, or the child
	// would be stranded under a missing parent.
	var parentOfKeptChild string
	if err := database.db.QueryRowContext(ctx, `SELECT parent_id FROM knowledge_entries WHERE id='qa_kept_child'`).Scan(&parentOfKeptChild); err != nil {
		t.Fatal(err)
	}
	if parentOfKeptChild != "qa_kept_parent" {
		t.Fatalf("kept child parent = %q, want qa_kept_parent", parentOfKeptChild)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version < 67 {
		t.Fatalf("schema version = %d err=%v, want >= 67", version, err)
	}
}
