package store

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) EnsureOwnerAssistant(ctx context.Context, now time.Time) (domain.OwnerAssistant, error) {
	// Preserve stored preferences. A fresh assistant opts in only through
	// Settings, with a fallback for a legacy Task whose assistant row is gone.
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO owner_assistants(id,name,model_id,character,pet_enabled,created_at,updated_at)
		VALUES('assistant_default','小A','','gugugaga',EXISTS(SELECT 1 FROM tasks WHERE id='task_pet'),?,?)`, timeString(now), timeString(now))
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	return s.OwnerAssistant(ctx, "assistant_default")
}

func (s *Store) OwnerAssistant(ctx context.Context, id string) (domain.OwnerAssistant, error) {
	var item domain.OwnerAssistant
	var created, updated, actions string
	var enabled, quietEnabled int
	var allowViewTasks, allowCreateTasks, allowCreateWorkspaces, allowMessageTasks, allowManageModels, allowManageKnowledge, allowManageChannels, allowManageSystem int
	err := s.db.QueryRowContext(ctx, `
		SELECT id,name,model_id,COALESCE(NULLIF(character,''),'gugugaga'),
			pet_enabled,pet_brain_model_id,pet_actions,pet_identity,pet_principles,
			pet_idle_minutes,pet_quiet_enabled,pet_quiet_start,pet_quiet_end,pet_hourly_limit,pet_task_id,
			pet_free_daily_turns,pet_free_day,pet_free_used,
			pet_allow_view_tasks,pet_allow_create_tasks,pet_allow_create_workspaces,pet_allow_message_tasks,pet_allow_manage_models,pet_allow_manage_knowledge,pet_allow_manage_channels,pet_allow_manage_system,
			created_at,updated_at
		FROM owner_assistants WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.ModelID, &item.Character, &enabled, &item.BrainModelID, &actions, &item.PetIdentity, &item.PetPrinciples,
			&item.PetIdleMinutes, &quietEnabled, &item.PetQuietStart, &item.PetQuietEnd, &item.PetHourlyLimit, &item.PetTaskID,
			&item.PetFreeDailyTurns, &item.PetFreeDay, &item.PetFreeUsed,
			&allowViewTasks, &allowCreateTasks, &allowCreateWorkspaces, &allowMessageTasks, &allowManageModels, &allowManageKnowledge, &allowManageChannels, &allowManageSystem,
			&created, &updated)
	item.PetEnabled = enabled != 0
	item.PetQuietEnabled = quietEnabled != 0
	item.PetAllowViewTasks = allowViewTasks != 0
	item.PetAllowCreateTasks = allowCreateTasks != 0
	item.PetAllowCreateWorkspaces = allowCreateWorkspaces != 0
	item.PetAllowMessageTasks = allowMessageTasks != 0
	item.PetAllowManageModels = allowManageModels != 0
	item.PetAllowManageKnowledge = allowManageKnowledge != 0
	item.PetAllowManageChannels = allowManageChannels != 0
	item.PetAllowManageSystem = allowManageSystem != 0
	item.PetActions = splitPetActions(actions)
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

func (s *Store) UpdateOwnerAssistant(ctx context.Context, item domain.OwnerAssistant) error {
	// Profile-only update. The model columns are written by
	// UpdateOwnerAssistantModel or UpdateOwnerPetSettings so a stale profile
	// read can never overwrite a model another request just saved.
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET name=?,character=?,updated_at=? WHERE id=?`,
		item.Name, item.Character, timeString(item.UpdatedAt), item.ID)
	return err
}

// UpdateOwnerAssistantModel writes the unified model in one statement. The pet
// runtime column remains the source of truth and model_id is the derived
// compatibility mirror, so a reader can never observe them disagreeing.
func (s *Store) UpdateOwnerAssistantModel(ctx context.Context, id, modelID string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET model_id=?,pet_brain_model_id=?,updated_at=? WHERE id=?`,
		modelID, modelID, timeString(updatedAt), id)
	return err
}

// NormalizeOwnerAssistantModel applies a read-time repair only when the row is
// still exactly what the caller read. A concurrent save that moved either
// column makes the condition fail, so normalization can never overwrite a
// model the Owner just chose. The boolean reports whether the row was updated.
func (s *Store) NormalizeOwnerAssistantModel(ctx context.Context, id, modelID, expectModelID, expectBrainModelID string, updatedAt time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET model_id=?,pet_brain_model_id=?,updated_at=?
		WHERE id=? AND model_id=? AND pet_brain_model_id=?`,
		modelID, modelID, timeString(updatedAt), id, expectModelID, expectBrainModelID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func splitPetActions(value string) []string {
	result := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return []string{"walk", "jump", "happy", "angry"}
	}
	return result
}

func (s *Store) UpdateOwnerPetSettings(ctx context.Context, id string, enabled bool, brainModelID string, actions []string, updatedAt time.Time) error {
	value := strings.Join(actions, ",")
	if strings.TrimSpace(value) == "" {
		value = ""
	}
	// The save writes both model columns in the same statement. Splitting them
	// into separate updates used to expose a transient (new chat model, old pet
	// model) row that a concurrent read could normalize back over the new choice.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		UPDATE owner_assistants SET model_id=?,pet_brain_model_id=?,pet_enabled=?,pet_actions=?,updated_at=? WHERE id=?`,
		brainModelID, brainModelID, petBoolInt(enabled), value, timeString(updatedAt), id)
	if err != nil {
		return err
	}
	if !enabled {
		// Keep the conversation and running Turn. Pending system wakes must
		// not become follow-up Turns after the owner switches the pet off.
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_inbox SET status='cancelled',processed_at=?
			WHERE task_id='task_pet' AND source_kind=? AND status='pending'`,
			timeString(updatedAt), domain.PetWakeSourceKind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func petBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) UpdateOwnerPetPermissions(ctx context.Context, id string, viewTasks, createTasks, createWorkspaces, messageTasks, manageModels, manageKnowledge, manageChannels, manageSystem bool, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET
			pet_allow_view_tasks=?,pet_allow_create_tasks=?,pet_allow_create_workspaces=?,pet_allow_message_tasks=?,pet_allow_manage_models=?,pet_allow_manage_knowledge=?,pet_allow_manage_channels=?,pet_allow_manage_system=?,updated_at=?
		WHERE id=?`,
		petBoolInt(viewTasks), petBoolInt(createTasks), petBoolInt(createWorkspaces), petBoolInt(messageTasks), petBoolInt(manageModels), petBoolInt(manageKnowledge), petBoolInt(manageChannels), petBoolInt(manageSystem),
		timeString(updatedAt), id)
	return err
}

// AssistantMessages returns the most recent messages in chronological order.
func (s *Store) AssistantMessages(ctx context.Context, assistantID string, limit int) ([]domain.AssistantMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,assistant_id,role,content,created_at FROM (
			SELECT id,assistant_id,role,content,created_at
			FROM assistant_messages WHERE assistant_id=?
			ORDER BY created_at DESC,id DESC LIMIT ?
		) ORDER BY created_at ASC,id ASC`, assistantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AssistantMessage{}
	for rows.Next() {
		var item domain.AssistantMessage
		var created string
		if err := rows.Scan(&item.ID, &item.AssistantID, &item.Role, &item.Content, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) AppendAssistantMessage(ctx context.Context, item domain.AssistantMessage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO assistant_messages(id,assistant_id,role,content,created_at) VALUES(?,?,?,?,?)`,
		item.ID, item.AssistantID, item.Role, item.Content, timeString(item.CreatedAt))
	return err
}

// AssistantHintDismissals returns the currently suppressed hint keys, keyed by
// kind and target id.
func (s *Store) AssistantHintDismissals(ctx context.Context, assistantID string, now time.Time) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind,target_id,dismissed_until FROM assistant_hint_dismissals
		WHERE assistant_id=? AND dismissed_until>?`, assistantID, timeString(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]time.Time{}
	for rows.Next() {
		var kind, targetID, until string
		if err := rows.Scan(&kind, &targetID, &until); err != nil {
			return nil, err
		}
		result[hintKey(kind, targetID)] = parseTime(until)
	}
	return result, rows.Err()
}

func (s *Store) DismissAssistantHint(ctx context.Context, assistantID, kind, targetID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO assistant_hint_dismissals(assistant_id,kind,target_id,dismissed_until)
		VALUES(?,?,?,?)
		ON CONFLICT(assistant_id,kind,target_id) DO UPDATE SET dismissed_until=excluded.dismissed_until`,
		assistantID, kind, targetID, timeString(until))
	return err
}

func hintKey(kind, targetID string) string {
	return kind + "\x00" + targetID
}

func (s *Store) UpdateOwnerPetIdentity(ctx context.Context, id, identity, principles string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET pet_identity=?,pet_principles=?,updated_at=? WHERE id=?`,
		identity, principles, timeString(updatedAt), id)
	return err
}

func (s *Store) AppendPetEvent(ctx context.Context, event domain.PetEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pet_events(id,at,device_id,actor,kind,page,detail,status) VALUES(?,?,?,?,?,?,?,?)`,
		event.ID, timeString(event.At), event.DeviceID, event.Actor, event.Kind, event.Page, event.Detail, event.Status)
	return err
}

func (s *Store) ListPetEvents(ctx context.Context, limit int) ([]domain.PetEvent, error) {
	// The wake-up briefing asks for a wider window when the pet explicitly
	// expands its work history (history_limit up to 50); unrelated control
	// events share the table, so the caller's item count is not a row count.
	if limit <= 0 || limit > 1000 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,at,device_id,actor,kind,page,detail,status FROM pet_events
		ORDER BY at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PetEvent{}
	for rows.Next() {
		var item domain.PetEvent
		var at string
		if err := rows.Scan(&item.ID, &at, &item.DeviceID, &item.Actor, &item.Kind, &item.Page, &item.Detail, &item.Status); err != nil {
			return nil, err
		}
		item.At = parseTime(at)
		result = append(result, item)
	}
	return result, rows.Err()
}

// ListOpenPetEventsByKind returns only rows of the given kinds that are still
// status='open', newest first. Rows written by older AHA2 builds keep whatever
// status they had; anything that is not exactly "open" stays out of the pet's
// pending notifications instead of being revived.
func (s *Store) ListOpenPetEventsByKind(ctx context.Context, kinds []string, limit int) ([]domain.PetEvent, error) {
	if len(kinds) == 0 {
		return []domain.PetEvent{}, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 20
	}
	args := make([]any, 0, len(kinds)+1)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,at,device_id,actor,kind,page,detail,status FROM pet_events
		WHERE status='open' AND kind IN (`+placeholders(len(kinds))+`)
		ORDER BY at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PetEvent{}
	for rows.Next() {
		var item domain.PetEvent
		var at string
		if err := rows.Scan(&item.ID, &at, &item.DeviceID, &item.Actor, &item.Kind, &item.Page, &item.Detail, &item.Status); err != nil {
			return nil, err
		}
		item.At = parseTime(at)
		result = append(result, item)
	}
	return result, rows.Err()
}

// MarkOpenPetEvents transitions open rows of the allowed kinds to status. An
// empty ids slice updates every open row of those kinds (sync recovered); a
// non-empty slice updates only the listed rows (the pet acknowledged them).
// Only kind-whitelisted rows are ever touched, and already closed rows keep
// their original status. Returns the number of rows actually changed.
func (s *Store) MarkOpenPetEvents(ctx context.Context, kinds, ids []string, status string) (int, error) {
	if len(kinds) == 0 || strings.TrimSpace(status) == "" {
		return 0, nil
	}
	query := `UPDATE pet_events SET status=? WHERE status='open' AND kind IN (` + placeholders(len(kinds)) + `)`
	args := make([]any, 0, 1+len(kinds)+len(ids))
	args = append(args, status)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	if len(ids) > 0 {
		query += ` AND id IN (` + placeholders(len(ids)) + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(changed), nil
}

// PetCreatedTaskIDs returns every Task id the pet created, oldest first. The
// work-state briefing uses this instead of the bounded recent-event list so an
// old, still-open Task cannot silently fall out of the pet's view.
func (s *Store) PetCreatedTaskIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT detail FROM pet_events
		WHERE kind='task_created' AND detail<>''
		GROUP BY detail
		ORDER BY MIN(at), detail`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *Store) UpdateOwnerPetBudget(ctx context.Context, id string, idleMinutes int, quietEnabled bool, quietStart, quietEnd, hourlyLimit int, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET pet_idle_minutes=?,pet_quiet_enabled=?,pet_quiet_start=?,pet_quiet_end=?,pet_hourly_limit=?,updated_at=? WHERE id=?`,
		idleMinutes, boolInt(quietEnabled), quietStart, quietEnd, hourlyLimit, timeString(updatedAt), id)
	return err
}

// UpdateOwnerPetFreeDailyTurns sets how many Turns a day the pet may spend on
// its own interests.
func (s *Store) UpdateOwnerPetFreeDailyTurns(ctx context.Context, id string, dailyTurns int, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE owner_assistants SET pet_free_daily_turns=?,updated_at=? WHERE id=?`,
		dailyTurns, timeString(updatedAt), id)
	return err
}

// ClaimPetFreeTurn takes one free-time Turn for the given local day when the
// daily allowance still has room, and reports whether it did. The check and
// the increment are one statement, so the scheduler and a browser-triggered
// wake cannot both take the last one. A new day resets the count.
func (s *Store) ClaimPetFreeTurn(ctx context.Context, id, day string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants
		SET pet_free_used=CASE WHEN pet_free_day=? THEN pet_free_used+1 ELSE 1 END, pet_free_day=?
		WHERE id=? AND pet_free_daily_turns>0
		  AND (pet_free_day<>? OR pet_free_used<pet_free_daily_turns)`,
		day, day, id, day)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// ReleasePetFreeTurn gives back a claimed free-time Turn whose wake was never
// submitted, so a failed submission does not eat the day's allowance.
func (s *Store) ReleasePetFreeTurn(ctx context.Context, id, day string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET pet_free_used=pet_free_used-1
		WHERE id=? AND pet_free_day=? AND pet_free_used>0`, id, day)
	return err
}

func (s *Store) UpdateOwnerPetTask(ctx context.Context, id, taskID string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE owner_assistants SET pet_task_id=?,updated_at=? WHERE id=?`, taskID, timeString(updatedAt), id)
	return err
}
