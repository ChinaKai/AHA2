package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// PetResetCounts is the number of pet-owned records removed by a factory
// reset. Task, project and workspace counts are added by the app layer after
// it has removed those rows through the normal deletion paths.
type PetResetCounts struct {
	Approvals int `json:"approvals"`
	Events    int `json:"events"`
	Watches   int `json:"watches"`
	Messages  int `json:"messages"`
	Hints     int `json:"hints"`
}

// ResetPetRecords clears the pet's local records and returns the pet to a
// disabled state. It intentionally keeps the owner's configured model,
// character and permission switches; the reset removes pet-produced data, not
// the owner's preferences.
//
// The pet schema has grown over time, so tables introduced by newer builds are
// cleared only when they exist. This keeps the reset safe on older databases
// and also removes residue if a newer build previously wrote those tables.
func (s *Store) ResetPetRecords(ctx context.Context, assistantID string, taskIDs []string, updatedAt time.Time) (PetResetCounts, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PetResetCounts{}, err
	}
	defer tx.Rollback()

	counts := PetResetCounts{}
	for _, item := range []struct {
		table string
		count *int
	}{
		{"pet_approvals", &counts.Approvals},
		{"pet_events", &counts.Events},
		{"pet_task_watches", &counts.Watches},
	} {
		exists, err := tableExistsTx(ctx, tx, item.table)
		if err != nil {
			return PetResetCounts{}, err
		}
		if !exists {
			continue
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM "`+item.table+`"`)
		if err != nil {
			return PetResetCounts{}, err
		}
		affected, _ := result.RowsAffected()
		*item.count = int(affected)
	}

	if counts.Messages, err = deletePetAssistantRows(ctx, tx, assistantID, "assistant_messages"); err != nil {
		return PetResetCounts{}, err
	}
	if counts.Hints, err = deletePetAssistantRows(ctx, tx, assistantID, "assistant_hint_dismissals"); err != nil {
		return PetResetCounts{}, err
	}

	if exists, err := tableExistsTx(ctx, tx, "pet_states"); err != nil {
		return PetResetCounts{}, err
	} else if exists {
		if _, err := tx.ExecContext(ctx, `DELETE FROM pet_states`); err != nil {
			return PetResetCounts{}, err
		}
	}

	if err := resetOwnerAssistantAfterPetReset(ctx, tx, assistantID, updatedAt); err != nil {
		return PetResetCounts{}, err
	}

	// These tables only exist in builds that added the autonomy ledger,
	// capability registry or global Skill update proposals. They are all
	// pet-owned data and should disappear with the rest of the reset.
	for _, table := range []string{"pet_autonomy_actions", "pet_capabilities", "pet_project_autonomy"} {
		exists, err := tableExistsTx(ctx, tx, table)
		if err != nil {
			return PetResetCounts{}, err
		}
		if !exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+table+`"`); err != nil {
			return PetResetCounts{}, err
		}
	}
	if len(taskIDs) > 0 {
		exists, err := tableExistsTx(ctx, tx, "skill_update_proposals")
		if err != nil {
			return PetResetCounts{}, err
		}
		if exists {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(taskIDs)), ",")
			args := make([]any, 0, len(taskIDs))
			for _, id := range taskIDs {
				args = append(args, id)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM skill_update_proposals WHERE source_task_id IN (`+placeholders+`)`, args...); err != nil {
				return PetResetCounts{}, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return PetResetCounts{}, err
	}
	return counts, nil
}

func deletePetAssistantRows(ctx context.Context, tx *sql.Tx, assistantID, table string) (int, error) {
	exists, err := tableExistsTx(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM "`+table+`" WHERE assistant_id=?`, assistantID)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

func resetOwnerAssistantAfterPetReset(ctx context.Context, tx *sql.Tx, assistantID string, updatedAt time.Time) error {
	columns, err := tableColumnsTx(ctx, tx, "owner_assistants")
	if err != nil {
		return err
	}
	sets := []string{"pet_enabled=0"}
	for _, column := range []string{"pet_task_id", "pet_identity", "pet_principles"} {
		if columns[column] {
			sets = append(sets, column+"=''")
		}
	}
	// Newer builds added autonomy state and default quotas. Reset the state so
	// a later upgrade cannot resume an auto-stop or failure streak from before
	// the factory reset.
	for _, item := range []struct {
		column string
		set    string
	}{
		{"pet_autonomy_level", "pet_autonomy_level='proactive'"},
		{"pet_emergency_stop", "pet_emergency_stop=0"},
		{"pet_auto_stopped", "pet_auto_stopped=0"},
		{"pet_auto_stop_reason", "pet_auto_stop_reason=''"},
		{"pet_failure_streak", "pet_failure_streak=0"},
		{"pet_daily_task_limit", "pet_daily_task_limit=3"},
		{"pet_max_concurrent_agents", "pet_max_concurrent_agents=1"},
		{"pet_daily_token_budget", "pet_daily_token_budget=200000"},
	} {
		if columns[item.column] {
			sets = append(sets, item.set)
		}
	}
	sets = append(sets, "updated_at=?")
	args := []any{timeString(updatedAt), assistantID}
	_, err = tx.ExecContext(ctx, `UPDATE owner_assistants SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	return err
}

func tableExistsTx(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count)
	return count > 0, err
}

func tableColumnsTx(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}
