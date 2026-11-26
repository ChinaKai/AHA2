package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Keep recent closures on the card, with older closures and their IDs in the
// event history. Closed lines do not count toward the active limit.
const taskLineMaxRecentClosed = 8

var taskLineIDPattern = regexp.MustCompile(domain.TaskLineIDPattern)

func (s *Store) migrateTaskLines(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=167)`).Scan(&applied); err != nil || applied {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE task_memory ADD COLUMN lines_json TEXT NOT NULL DEFAULT '[]';
		CREATE TABLE task_line_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			line_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			round_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			before_json TEXT NOT NULL,
			after_json TEXT NOT NULL
		);
		CREATE INDEX idx_task_line_events_task_sequence ON task_line_events(task_id,sequence);
		CREATE INDEX idx_task_line_events_line_sequence ON task_line_events(task_id,line_id,sequence);
		INSERT INTO schema_migrations(version,applied_at) VALUES(167,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
	`); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateTaskMemory applies the card edit, goal change and line events in one
// transaction. Store connections take the write lock before reading the card.
func (s *Store) UpdateTaskMemory(ctx context.Context, taskID string, turn domain.Turn, now time.Time, patches []domain.TaskLinePatch, edit func(*domain.TaskMemory) (*domain.TaskGoalChange, error)) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	memory, err := readTaskMemory(ctx, tx, taskID)
	if err != nil {
		return err
	}
	goal, err := edit(&memory)
	if err != nil {
		return err
	}
	if len(patches) > 0 {
		if err := updateTaskLines(ctx, tx, memory, turn, now, patches); err != nil {
			return err
		}
	}
	if goal != nil {
		if err := updateTaskGoalWithEvent(ctx, tx, *goal); err != nil {
			return err
		}
		memory.CurrentGoal = goal.GoalAfter
	}
	memory.UpdatedAt = now
	if err := upsertTaskMemory(ctx, tx, memory); err != nil {
		return err
	}
	return tx.Commit()
}

func updateTaskLines(ctx context.Context, tx *sql.Tx, memory domain.TaskMemory, turn domain.Turn, now time.Time, patches []domain.TaskLinePatch) error {
	lines := memory.Lines
	seen := make(map[string]bool, len(patches))
	var events []domain.TaskLineEvent
	for patchIndex, patch := range patches {
		if !taskLineIDPattern.MatchString(patch.ID) {
			return fmt.Errorf("lines[%d]: line id must be 1–32 lowercase ASCII letters, digits, '-' or '_', starting with a letter or digit", patchIndex)
		}
		if seen[patch.ID] {
			return fmt.Errorf("lines[%d]: line id is listed more than once", patchIndex)
		}
		seen[patch.ID] = true

		index := slices.IndexFunc(lines, func(line domain.TaskLine) bool { return line.ID == patch.ID })
		var before *domain.TaskLine
		if index >= 0 {
			line := lines[index]
			before = &line
		} else {
			var err error
			before, err = latestTaskLine(ctx, tx, memory.TaskID, patch.ID)
			if err != nil {
				return err
			}
		}
		after, kind, err := applyTaskLinePatch(before, patch, now)
		if err != nil {
			return fmt.Errorf("lines[%d]: %w", patchIndex, err)
		}
		if kind == "" {
			continue
		}
		switch {
		case index < 0:
			lines = append(lines, after)
		case kind == "closed":
			// Moving the line to the end preserves closure order even when a
			// batch shares one timestamp.
			lines = append(slices.Delete(lines, index, index+1), after)
		default:
			lines[index] = after
		}
		events = append(events, domain.TaskLineEvent{
			TaskID: memory.TaskID, LineID: patch.ID, Kind: kind, RoundID: turn.RoundID,
			TurnID: turn.ID, CreatedAt: now, Before: before, After: after,
		})
	}
	closed := 0
	for _, line := range lines {
		if line.State == domain.TaskLineClosed {
			closed++
		}
	}
	if len(lines)-closed > domain.TaskLineMaxActive {
		return fmt.Errorf("a task can have at most %d lines that are not closed", domain.TaskLineMaxActive)
	}
	if len(events) == 0 {
		return nil
	}
	lines = slices.DeleteFunc(lines, func(line domain.TaskLine) bool {
		if line.State == domain.TaskLineClosed && closed > taskLineMaxRecentClosed {
			closed--
			return true
		}
		return false
	})
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_line_events(task_id,line_id,kind,round_id,turn_id,created_at,before_json,after_json)
			VALUES(?,?,?,?,?,?,?,?)`, event.TaskID, event.LineID, event.Kind, event.RoundID, event.TurnID,
			timeString(event.CreatedAt), encodeJSON(event.Before), encodeJSON(event.After)); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE task_memory SET lines_json=? WHERE task_id=?`, encodeJSON(lines), memory.TaskID)
	return err
}

func applyTaskLinePatch(before *domain.TaskLine, patch domain.TaskLinePatch, now time.Time) (domain.TaskLine, string, error) {
	line := domain.TaskLine{ID: patch.ID, OpenedAt: now}
	if before != nil {
		line = *before
	}
	if patch.Owner != nil {
		line.Owner = *patch.Owner
	}
	if patch.State != nil {
		line.State = *patch.State
	}
	for _, field := range []struct {
		name string
		from *string
		to   *string
	}{
		{"title", patch.Title, &line.Title},
		{"who", patch.Who, &line.Who},
		{"step", patch.Step, &line.Step},
		{"blocker", patch.Blocker, &line.Blocker},
	} {
		if field.from != nil {
			if utf8.RuneCountInString(*field.from) > domain.TaskLineMaxTextRunes {
				return line, "", fmt.Errorf("%s holds at most %d characters", field.name, domain.TaskLineMaxTextRunes)
			}
			*field.to = strings.TrimSpace(*field.from)
		}
	}
	if line.Title == "" {
		return line, "", errors.New("line needs a title")
	}
	switch line.Owner {
	case domain.TaskLineOwnerSelf, domain.TaskLineOwnerOwner:
		line.Who = ""
	case domain.TaskLineOwnerOther:
		if line.Who == "" {
			return line, "", errors.New("line needs who when owner is other")
		}
	default:
		return line, "", errors.New("owner must be self, owner or other")
	}
	switch line.State {
	case domain.TaskLineOpen, domain.TaskLineWaiting, domain.TaskLineBlocked, domain.TaskLineClosed:
	default:
		return line, "", errors.New("state must be open, waiting, blocked or closed")
	}
	if before != nil && line == *before {
		return line, "", nil
	}

	line.UpdatedAt = now
	switch {
	case before == nil:
		if line.State == domain.TaskLineClosed {
			return line, "", errors.New("line must be opened before it can be closed")
		}
		return line, "opened", nil
	case before.State == domain.TaskLineClosed:
		if line.State != domain.TaskLineOpen {
			return line, "", errors.New("line is closed; set state to open to reopen it")
		}
		line.ClosedAt = nil
		return line, "reopened", nil
	case line.State == domain.TaskLineClosed:
		line.ClosedAt = &now
		return line, "closed", nil
	default:
		return line, "updated", nil
	}
}

func latestTaskLine(ctx context.Context, tx *sql.Tx, taskID, lineID string) (*domain.TaskLine, error) {
	var snapshot string
	err := tx.QueryRowContext(ctx, `
		SELECT after_json FROM task_line_events WHERE task_id=? AND line_id=? ORDER BY sequence DESC LIMIT 1
	`, taskID, lineID).Scan(&snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeJSON[*domain.TaskLine](snapshot, nil), nil
}

// TaskLineEvents returns the Task's line history in chronological write order.
func (s *Store) TaskLineEvents(ctx context.Context, taskID string) ([]domain.TaskLineEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence,task_id,line_id,kind,round_id,turn_id,created_at,before_json,after_json
		FROM task_line_events WHERE task_id=? ORDER BY sequence
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.TaskLineEvent{}
	for rows.Next() {
		var event domain.TaskLineEvent
		var createdAt, before, after string
		if err := rows.Scan(&event.Sequence, &event.TaskID, &event.LineID, &event.Kind, &event.RoundID, &event.TurnID, &createdAt, &before, &after); err != nil {
			return nil, err
		}
		event.CreatedAt = parseTime(createdAt)
		event.Before = decodeJSON[*domain.TaskLine](before, nil)
		event.After = decodeJSON(after, domain.TaskLine{})
		result = append(result, event)
	}
	return result, rows.Err()
}
