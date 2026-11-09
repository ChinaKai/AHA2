package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskReviewDecisionLifecycleClearsAudit(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := attachmentTestTask(t, database)
	at := "2026-11-09T00:00:00Z"
	audit := domain.TaskReviewDecision{ReviewDecision: "required", ReviewReason: "原完成理由", ReviewTaskID: "old-review", ReviewDecidedBy: "pet", ReviewDecidedAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET status='completed',review_decision_json=? WHERE id=?`, encodeJSON(audit), id); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(ctx, id, domain.TaskWaitingUser, domain.TaskActive, at, ""); err == nil {
		t.Fatal("stale transition accepted")
	}
	stored, err := database.Task(ctx, id)
	if err != nil || stored.TaskReviewDecision != audit {
		t.Fatalf("stale transition changed audit: %#v %v", stored, err)
	}
	if err := database.UpdateTaskStatus(ctx, id, domain.TaskCompleted, domain.TaskWaitingUser, at, ""); err != nil {
		t.Fatal(err)
	}
	assertEmpty := func(status domain.TaskStatus) {
		t.Helper()
		stored, err := database.Task(ctx, id)
		if err != nil || stored.Status != status || stored.TaskReviewDecision != (domain.TaskReviewDecision{}) {
			t.Fatalf("old audit survived: %#v %v", stored, err)
		}
		var raw string
		if err := database.db.QueryRowContext(ctx, `SELECT review_decision_json FROM tasks WHERE id=?`, id).Scan(&raw); err != nil || raw != "{}" {
			t.Fatalf("audit not cleared atomically: %q %v", raw, err)
		}
	}
	assertEmpty(domain.TaskWaitingUser)
	// An old declaration in a non-completed row must also be cleared by Owner
	// completion, independently of the reopen path.
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET review_decision_json=? WHERE id=?`, encodeJSON(audit), id); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteTaskWithReviewDecision(ctx, id, domain.TaskWaitingUser, at, nil); err != nil {
		t.Fatal(err)
	}
	assertEmpty(domain.TaskCompleted)
}
