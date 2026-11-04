package store

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) EnsureOwnerAssistant(ctx context.Context, now time.Time) (domain.OwnerAssistant, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO owner_assistants(id,name,model_id,character,created_at,updated_at)
		VALUES('assistant_default','小A','','gugugaga',?,?)`, timeString(now), timeString(now))
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
			pet_allow_view_tasks,pet_allow_create_tasks,pet_allow_create_workspaces,pet_allow_message_tasks,pet_allow_manage_models,pet_allow_manage_knowledge,pet_allow_manage_channels,pet_allow_manage_system,
			created_at,updated_at
		FROM owner_assistants WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.ModelID, &item.Character, &enabled, &item.BrainModelID, &actions, &item.PetIdentity, &item.PetPrinciples,
			&item.PetIdleMinutes, &quietEnabled, &item.PetQuietStart, &item.PetQuietEnd, &item.PetHourlyLimit, &item.PetTaskID,
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
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET name=?,model_id=?,character=?,updated_at=? WHERE id=?`,
		item.Name, item.ModelID, item.Character, timeString(item.UpdatedAt), item.ID)
	return err
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
	_, err := s.db.ExecContext(ctx, `
		UPDATE owner_assistants SET pet_enabled=?,pet_brain_model_id=?,pet_actions=?,updated_at=? WHERE id=?`,
		petBoolInt(enabled), brainModelID, value, timeString(updatedAt), id)
	return err
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
	if limit <= 0 || limit > 100 {
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

func (s *Store) UpdateOwnerPetTask(ctx context.Context, id, taskID string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE owner_assistants SET pet_task_id=?,updated_at=? WHERE id=?`, taskID, timeString(updatedAt), id)
	return err
}
