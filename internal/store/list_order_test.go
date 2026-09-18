package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestListsOrderByCreationTimeNotUpdateTime pins the ordering the UI relies on.
//
// Projects, workspaces and tasks are browsed newest-created-first. Ordering by
// update time instead makes an old item jump to the top the moment anything
// touches it, which reads as the list rearranging itself under the user.
//
// The two orders are made to disagree here on purpose: the item created last is
// updated first, so only a creation-time sort puts it on top.
func TestListsOrderByCreationTimeNotUpdateTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	stamp := func(minutes int) string {
		return base.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339Nano)
	}

	if _, err := database.db.ExecContext(ctx,
		`INSERT INTO projects(id,name,project_type,created_at,updated_at) VALUES('p1','P','folder',?,?)`,
		stamp(0), stamp(0)); err != nil {
		t.Fatal(err)
	}
	// oldest created, most recently updated -> must sort LAST, not first
	if _, err := database.db.ExecContext(ctx,
		`INSERT INTO workspaces(id,project_id,name,locality,transport,root_path,created_at,updated_at)
		 VALUES('ws-old','p1','old','remote','ssh','/tmp',?,?)`, stamp(0), stamp(100)); err != nil {
		t.Fatal(err)
	}
	// newest created, never touched since -> must sort FIRST
	if _, err := database.db.ExecContext(ctx,
		`INSERT INTO workspaces(id,project_id,name,locality,transport,root_path,created_at,updated_at)
		 VALUES('ws-new','p1','new','remote','ssh','/tmp',?,?)`, stamp(10), stamp(10)); err != nil {
		t.Fatal(err)
	}

	workspaces, err := database.ListWorkspaces(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("got %d workspaces", len(workspaces))
	}
	if workspaces[0].ID != "ws-new" {
		t.Fatalf("workspaces[0] = %s, want the newest-created ws-new (update time would put ws-old first)", workspaces[0].ID)
	}
}
