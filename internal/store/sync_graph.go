package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type RemoteTaskMirror struct {
	Task         domain.Task
	Agents       []domain.TaskAgent
	Rounds       []domain.TaskRound
	Turns        []domain.Turn
	Conversation []domain.ConversationItem
	Memory       domain.TaskMemory
	Hardware     []domain.HardwareGroup
	SourceTaskID string
}

func remoteTaskPublicID(ownerDeviceID, sourceTaskID string) string {
	sum := sha256.Sum256([]byte(ownerDeviceID + "\x00" + sourceTaskID))
	return "remote_" + hex.EncodeToString(sum[:12])
}

func (s *Store) RemoteTaskMirrors(ctx context.Context, projectID string) ([]RemoteTaskMirror, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT owner_device_id,task_id,payload_json FROM sync_remote_task_objects WHERE object_type='task' AND (?='' OR project_id=?) ORDER BY updated_at DESC`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RemoteTaskMirror
	for rows.Next() {
		var owner, sourceID, payload string
		if err := rows.Scan(&owner, &sourceID, &payload); err != nil {
			return nil, err
		}
		var task domain.Task
		if err := json.Unmarshal([]byte(payload), &task); err != nil {
			return nil, err
		}
		task.ID, task.OwnerDeviceID, task.ReadOnly = remoteTaskPublicID(owner, sourceID), owner, true
		result = append(result, RemoteTaskMirror{Task: task, SourceTaskID: sourceID})
	}
	return result, rows.Err()
}

func (s *Store) RemoteTaskMirror(ctx context.Context, publicID string) (RemoteTaskMirror, error) {
	mirrors, err := s.RemoteTaskMirrors(ctx, "")
	if err != nil {
		return RemoteTaskMirror{}, err
	}
	for _, mirror := range mirrors {
		if mirror.Task.ID != publicID {
			continue
		}
		objects, err := s.RemoteTaskObjects(ctx, mirror.Task.OwnerDeviceID, mirror.SourceTaskID)
		if err != nil {
			return RemoteTaskMirror{}, err
		}
		for _, object := range objects {
			switch object.ObjectType {
			case "task_agent":
				var value domain.TaskAgent
				if json.Unmarshal(object.Payload, &value) == nil {
					value.TaskID = publicID
					mirror.Agents = append(mirror.Agents, value)
				}
			case "round":
				var value domain.TaskRound
				if json.Unmarshal(object.Payload, &value) == nil {
					value.TaskID = publicID
					mirror.Rounds = append(mirror.Rounds, value)
				}
			case "turn":
				var value domain.Turn
				if json.Unmarshal(object.Payload, &value) == nil {
					value.TaskID = publicID
					mirror.Turns = append(mirror.Turns, value)
				}
			case "conversation":
				var value domain.ConversationItem
				if json.Unmarshal(object.Payload, &value) == nil {
					value.TaskID = publicID
					value.Sequence = int64(len(mirror.Conversation) + 1)
					mirror.Conversation = append(mirror.Conversation, value)
				}
			case "task_memory":
				_ = json.Unmarshal(object.Payload, &mirror.Memory)
				mirror.Memory.TaskID = publicID
			case "hardware":
				var value domain.HardwareGroup
				if json.Unmarshal(object.Payload, &value) == nil {
					value.TaskID = publicID
					value.CredentialRef = ""
					value.Access = domain.HardwareAccessReadOnly
					mirror.Hardware = append(mirror.Hardware, value)
				}
			}
		}
		return mirror, nil
	}
	return RemoteTaskMirror{}, fmt.Errorf("remote task not found")
}

type RemoteTaskObject struct {
	OwnerDeviceID string
	ObjectType    string
	ObjectID      string
	TaskID        string
	ProjectID     string
	Payload       json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (s *Store) ClaimLocalWorkspaces(ctx context.Context, deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE workspaces SET owner_device_id=? WHERE read_only=0 AND owner_device_id=''`, deviceID)
	return err
}

func (s *Store) PurgeOwnRemoteMirrors(ctx context.Context, deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_remote_task_objects WHERE owner_device_id=?`, deviceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE read_only=1 AND owner_device_id=?`, deviceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertSyncedWorkspace(ctx context.Context, item domain.Workspace) error {
	return s.UpsertSyncedWorkspaceFromSource(ctx, item, "")
}

func (s *Store) UpsertSyncedWorkspaceFromSource(ctx context.Context, item domain.Workspace, sourceID string) error {
	wireID := ownedGraphObjectID(item.OwnerDeviceID, sourceID)
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspaces(id,project_id,name,locality,transport,root_path,ssh_host,ssh_user,ssh_port,ssh_auth,ssh_credential_ref,ssh_password_configured,distro,platform,health,capabilities_json,repository_json,last_detected_at,created_at,updated_at,owner_device_id,read_only)
	SELECT ?,?,?,?,?,'',?,?,?,?,'',?,?,'','remote','{}','{}','',?,?,?,1
	WHERE NOT EXISTS(SELECT 1 FROM sync_tombstones WHERE object_type='workspace' AND object_id=?)
	ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id,name=excluded.name,locality='remote',transport=excluded.transport,
		root_path='',ssh_host=excluded.ssh_host,ssh_user=excluded.ssh_user,ssh_port=excluded.ssh_port,ssh_auth=excluded.ssh_auth,
		ssh_credential_ref='',ssh_password_configured=excluded.ssh_password_configured,distro=excluded.distro,
		platform='',health='remote',capabilities_json='{}',repository_json='{}',last_detected_at='',updated_at=excluded.updated_at,
		owner_device_id=excluded.owner_device_id,read_only=1`,
		item.ID, item.ProjectID, item.Name, "remote", item.Transport, item.SSHHost, item.SSHUser, item.SSHPort,
		item.SSHAuth, boolInt(item.SSHPasswordConfigured), item.Distro,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), item.OwnerDeviceID, wireID)
	return err
}

func (s *Store) UpsertRemoteTaskObject(ctx context.Context, item RemoteTaskObject) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	wireTaskID := ownedGraphObjectID(item.OwnerDeviceID, item.TaskID)
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_remote_task_objects(owner_device_id,object_type,object_id,task_id,project_id,payload_json,created_at,updated_at)
		SELECT ?,?,?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM sync_tombstones WHERE object_type='task' AND object_id=?)
		ON CONFLICT(owner_device_id,object_type,object_id) DO UPDATE SET task_id=excluded.task_id,project_id=excluded.project_id,payload_json=excluded.payload_json,updated_at=excluded.updated_at`, item.OwnerDeviceID, item.ObjectType, item.ObjectID, item.TaskID, item.ProjectID, string(item.Payload), timeString(item.CreatedAt), timeString(item.UpdatedAt), wireTaskID)
	return err
}

func (s *Store) RemoteTaskObjectExists(ctx context.Context, ownerDeviceID, objectType, objectID string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sync_remote_task_objects WHERE owner_device_id=? AND object_type=? AND object_id=?)`, ownerDeviceID, objectType, objectID).Scan(&found)
	return found, err
}

func (s *Store) RemoteTaskObjects(ctx context.Context, ownerDeviceID, taskID string) ([]RemoteTaskObject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT owner_device_id,object_type,object_id,task_id,project_id,payload_json,created_at,updated_at FROM sync_remote_task_objects WHERE owner_device_id=? AND (?='' OR task_id=?) ORDER BY CASE object_type WHEN 'task' THEN 1 WHEN 'task_agent' THEN 2 WHEN 'hardware' THEN 3 WHEN 'round' THEN 4 WHEN 'turn' THEN 5 WHEN 'conversation' THEN 6 WHEN 'task_memory' THEN 7 ELSE 9 END,created_at,object_id`, ownerDeviceID, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RemoteTaskObject
	for rows.Next() {
		var item RemoteTaskObject
		var payload, created, updated string
		if err := rows.Scan(&item.OwnerDeviceID, &item.ObjectType, &item.ObjectID, &item.TaskID, &item.ProjectID, &payload, &created, &updated); err != nil {
			return nil, err
		}
		item.Payload = json.RawMessage(payload)
		item.CreatedAt = parseTime(created)
		item.UpdatedAt = parseTime(updated)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) SyncRounds(ctx context.Context, taskID string) ([]domain.TaskRound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+roundColumns+` FROM task_rounds WHERE task_id=? ORDER BY sequence`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.TaskRound
	for rows.Next() {
		item, err := scanRound(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (s *Store) SyncTurns(ctx context.Context, taskID string) ([]domain.Turn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE task_id=? ORDER BY sequence`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Turn
	for rows.Next() {
		item, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (s *Store) SyncConversation(ctx context.Context, taskID string) ([]domain.ConversationItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,category,kind,summary,payload_json,created_at FROM conversation_items WHERE task_id=? ORDER BY sequence`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ConversationItem
	for rows.Next() {
		var item domain.ConversationItem
		var payload, created string
		if err := rows.Scan(&item.Sequence, &item.ID, &item.TaskID, &item.RoundID, &item.TurnID, &item.AgentID, &item.StreamAgentID, &item.FromAgentID, &item.ToAgentID, &item.RouteKind, &item.Category, &item.Kind, &item.Summary, &payload, &created); err != nil {
			return nil, err
		}
		item.Payload = decodeJSON(payload, map[string]any{})
		item.CreatedAt = parseTime(created)
		result = append(result, item)
	}
	return result, rows.Err()
}
