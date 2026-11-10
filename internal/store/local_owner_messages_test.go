package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestMigrationV129UpgradesV128Database simulates a database created before
// the local ingest columns existed. Reopening it must add the columns and the
// idempotency index without losing existing rows.
func TestMigrationV129UpgradesV128Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	oldMessageID := domain.NewID("message")
	now := time.Now().UTC()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		oldMessageID, task.ID, "", "user", "owner", "旧消息内容", timeString(now),
	); err != nil {
		database.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP INDEX IF EXISTS idx_messages_client_request_id`,
		`ALTER TABLE messages DROP COLUMN source`,
		`ALTER TABLE messages DROP COLUMN client_request_id`,
		`DELETE FROM schema_migrations WHERE version=129`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err != nil {
			database.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var migrated bool
	if err := reopened.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=129)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v129 migrated=%t err=%v", migrated, err)
	}
	columns := map[string]bool{}
	rows, err := reopened.db.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		columns[name] = true
	}
	rows.Close()
	if !columns["source"] || !columns["client_request_id"] {
		t.Fatalf("messages columns=%v", columns)
	}
	var indexSQL string
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT sql FROM sqlite_master
		WHERE type='index' AND name='idx_messages_client_request_id'`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexSQL, "client_request_id") {
		t.Fatalf("index sql=%q", indexSQL)
	}
	messages, err := reopened.ListMessages(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages=%#v", messages)
	}
	if messages[0].Content != "旧消息内容" || messages[0].Source != "" || messages[0].ClientRequestID != "" {
		t.Fatalf("existing row changed during v129 migration: %#v", messages[0])
	}
}

func TestLocalOwnerMessageStoreFingerprintConflict(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "正文一", Source: "voice", ClientRequestID: "store-fingerprint-1", CreatedAt: now,
	}
	_, _, _, inserted, duplicateFlag, messageID, err := database.EnqueueLocalOwnerMessage(ctx, message, "main", nil)
	if err != nil || !inserted || duplicateFlag || messageID != message.ID {
		t.Fatalf("first enqueue inserted=%t duplicate=%t id=%q err=%v", inserted, duplicateFlag, messageID, err)
	}

	differentContent := message
	differentContent.ID = domain.NewID("message")
	differentContent.Content = "正文二"
	if _, _, _, _, _, _, err := database.EnqueueLocalOwnerMessage(ctx, differentContent, "main", nil); !errors.Is(err, ErrLocalIngestRequestConflict) {
		t.Fatalf("different content err=%v", err)
	}

	differentSource := message
	differentSource.ID = domain.NewID("message")
	differentSource.Source = ""
	if _, _, _, _, _, _, err := database.EnqueueLocalOwnerMessage(ctx, differentSource, "main", nil); !errors.Is(err, ErrLocalIngestRequestConflict) {
		t.Fatalf("different source err=%v", err)
	}

	_, _, _, inserted, duplicateFlag, messageID, err = database.EnqueueLocalOwnerMessage(ctx, message, "main", nil)
	if err != nil || inserted || !duplicateFlag || messageID != message.ID {
		t.Fatalf("same fingerprint inserted=%t duplicate=%t id=%q err=%v", inserted, duplicateFlag, messageID, err)
	}
	messages, err := database.ListMessages(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages=%#v", messages)
	}
}

func pendingTurnForTest(t *testing.T, database *Store, task domain.Task) (domain.Turn, []domain.AgentInboxItem) {
	t.Helper()
	ctx := context.Background()
	items, err := database.PendingAgentInbox(ctx, task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected a pending inbox item")
	}
	return domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: items[0].RoundID, AgentID: "main",
		InputMessageID: items[0].MessageID, Status: domain.TurnQueued,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID:            domain.NewID("inbox_batch"), Attempt: 1, Required: true,
		QueuedAt: time.Now().UTC(),
	}, items
}

func enqueuePendingLocalMessage(t *testing.T, database *Store, task domain.Task, content string) {
	t.Helper()
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: content, Source: "voice", ClientRequestID: domain.NewID("request"), CreatedAt: now,
	}
	if _, _, _, _, _, _, err := database.EnqueueLocalOwnerMessage(context.Background(), message, "main", nil); err != nil {
		t.Fatal(err)
	}
}

func TestClaimInboxAndCreateTurnRejectsTerminalTask(t *testing.T) {
	for _, status := range []domain.TaskStatus{domain.TaskCompleted, domain.TaskCancelled, domain.TaskBlocked} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			task := createStoreTestTask(t, database)
			enqueuePendingLocalMessage(t, database, task, "pending")
			now := time.Now().UTC()
			if err := database.UpdateTaskStatus(ctx, task.ID, domain.TaskActive, status, timeString(now), ""); err != nil {
				t.Fatal(err)
			}
			turn, items := pendingTurnForTest(t, database, task)
			if _, _, err := database.ClaimInboxAndCreateTurn(ctx, turn, items, nil); !errors.Is(err, ErrTaskNotRunnable) {
				t.Fatalf("claim err=%v, want ErrTaskNotRunnable", err)
			}
			count, err := database.TaskTurnCount(ctx, task.ID)
			if err != nil || count != 0 {
				t.Fatalf("turn count=%d err=%v", count, err)
			}
			pending, err := database.PendingAgentInbox(ctx, task.ID, "main")
			if err != nil || len(pending) != 1 {
				t.Fatalf("pending=%#v err=%v", pending, err)
			}
		})
	}
}

func TestClaimInboxAndCreateTurnRacesTerminalTransition(t *testing.T) {
	const iterations = 20
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for iteration := 0; iteration < iterations; iteration++ {
		task := createStoreTestTask(t, database)
		enqueuePendingLocalMessage(t, database, task, "race")
		turn, items := pendingTurnForTest(t, database, task)
		completedAt := time.Now().UTC().Add(time.Second)
		start := make(chan struct{})
		var wait sync.WaitGroup
		var claimErr, completeErr error
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, _, claimErr = database.ClaimInboxAndCreateTurn(ctx, turn, items, nil)
		}()
		go func() {
			defer wait.Done()
			<-start
			completeErr = database.UpdateTaskStatus(ctx, task.ID, domain.TaskActive, domain.TaskCompleted, timeString(completedAt), timeString(completedAt))
		}()
		close(start)
		wait.Wait()
		if completeErr != nil {
			t.Fatalf("iteration %d complete err=%v", iteration, completeErr)
		}
		reloaded, err := database.Task(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Status != domain.TaskCompleted {
			t.Fatalf("iteration %d status=%s", iteration, reloaded.Status)
		}
		turns, err := database.ListTurns(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if claimErr != nil {
			if !errors.Is(claimErr, ErrTaskNotRunnable) {
				t.Fatalf("iteration %d claim err=%v", iteration, claimErr)
			}
			if len(turns) != 0 {
				t.Fatalf("iteration %d created %d turns after losing the race", iteration, len(turns))
			}
			continue
		}
		if len(turns) != 1 {
			t.Fatalf("iteration %d turns=%d", iteration, len(turns))
		}
		if turns[0].QueuedAt.After(reloaded.CompletedAt) {
			t.Fatalf("iteration %d turn queued after completion: %s > %s", iteration, turns[0].QueuedAt, reloaded.CompletedAt)
		}
	}
}
