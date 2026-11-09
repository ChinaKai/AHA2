package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskReviewDecisionMigrationAndPersistence(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "fresh"
		if upgrade {
			name = "upgrade_v126"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "aha2.db")
			database, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { database.Close() }()
			id := attachmentTestTask(t, database)
			at := "2026-11-09T00:00:00Z"
			if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET status='completed',completed_at=? WHERE id=?`, at, id); err != nil {
				t.Fatal(err)
			}
			if upgrade {
				// Remove only v127's column and marker to reproduce the actual v126
				// schema with an existing completed Task, then upgrade via Open.
				if _, err := database.db.ExecContext(ctx, `ALTER TABLE tasks DROP COLUMN review_decision_json; DELETE FROM schema_migrations WHERE version=127`); err != nil {
					t.Fatal(err)
				}
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
				database, err = Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			legacy, err := database.Task(ctx, id)
			if err != nil || legacy.Status != domain.TaskCompleted || legacy.ReviewDecision != "" || legacy.ReviewReason != "" || legacy.ReviewDecidedBy != "" || !legacy.ReviewDecidedAt.IsZero() {
				t.Fatalf("historical task changed: %#v err=%v", legacy, err)
			}
			var applied int
			if err := database.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=127`).Scan(&applied); err != nil || applied != 1 {
				t.Fatalf("migration marker: %d %v", applied, err)
			}
			decision := &domain.TaskReviewDecision{ReviewDecision: "skipped", ReviewReason: "已有充分定向验证证据", ReviewDecidedBy: "pet", ReviewDecidedAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
			if err := database.UpdateTaskStatus(ctx, id, domain.TaskCompleted, domain.TaskWaitingUser, at, ""); err != nil {
				t.Fatal(err)
			}
			// A stale state must persist neither completion nor its declaration.
			if err := database.CompleteTaskWithReviewDecision(ctx, id, domain.TaskActive, at, decision); err == nil {
				t.Fatal("stale completion accepted")
			}
			unchanged, err := database.Task(ctx, id)
			if err != nil || unchanged.Status != domain.TaskWaitingUser || unchanged.ReviewDecision != "" {
				t.Fatalf("failed completion leaked decision: %#v %v", unchanged, err)
			}
			if err := database.CompleteTaskWithReviewDecision(ctx, id, domain.TaskWaitingUser, at, decision); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			database, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			task, err := database.Task(ctx, id)
			if err != nil || task.Status != domain.TaskCompleted || task.TaskReviewDecision != *decision {
				t.Fatalf("decision did not survive reopen/migration: %#v %v", task, err)
			}
			tasks, err := database.ListTasks(ctx, task.ProjectID)
			if err != nil || len(tasks) != 1 || tasks[0].TaskReviewDecision != *decision {
				t.Fatalf("list lost audit: %#v %v", tasks, err)
			}
		})
	}
}
