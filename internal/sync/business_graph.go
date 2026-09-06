package sync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type graphEnvelope struct {
	OwnerDeviceID string          `json:"owner_device_id"`
	ProjectID     string          `json:"project_id"`
	TaskID        string          `json:"task_id,omitempty"`
	Value         json.RawMessage `json:"value"`
}
type workspaceMetadata struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	Transport     string    `json:"transport"`
	SSHHost       string    `json:"ssh_host,omitempty"`
	SSHUser       string    `json:"ssh_user,omitempty"`
	SSHPort       int       `json:"ssh_port,omitempty"`
	SSHAuth       string    `json:"ssh_auth,omitempty"`
	SSHConfigured bool      `json:"ssh_password_configured,omitempty"`
	Distro        string    `json:"distro,omitempty"`
	OwnerDeviceID string    `json:"owner_device_id"`
	ReadOnly      bool      `json:"read_only"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func exportTaskGraph(ctx context.Context, database *store.Store, deviceID string) ([]domain.SyncObject, error) {
	var result []domain.SyncObject
	add := func(kind, id, projectID, taskID string, value any, version string) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		envelopeRaw, err := json.Marshal(graphEnvelope{OwnerDeviceID: deviceID, ProjectID: projectID, TaskID: taskID, Value: raw})
		if err != nil {
			return err
		}
		result = append(result, domain.SyncObject{Type: kind, ID: id, Operation: "upsert", Payload: envelopeRaw, RemoteVersion: version, IdempotencyKey: kind + ":" + id + ":" + version})
		return nil
	}
	projects, err := database.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		project.DefaultWorkspaceID = ""
		project.KnowledgeRevision = 0
		if err := add(TypeProject, project.ID, project.ID, "", project, timeVersion(project.UpdatedAt)); err != nil {
			return nil, err
		}
		workspaces, err := database.ListWorkspaces(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		for _, workspace := range workspaces {
			if workspace.ReadOnly || (workspace.OwnerDeviceID != "" && workspace.OwnerDeviceID != deviceID) {
				continue
			}
			meta := workspaceMetadata{
				ID: workspace.ID, ProjectID: workspace.ProjectID, Name: workspace.Name, Transport: workspace.Transport,
				SSHHost: workspace.SSHHost, SSHUser: workspace.SSHUser, SSHPort: workspace.SSHPort,
				SSHAuth: workspace.SSHAuth, SSHConfigured: workspace.SSHPasswordConfigured, Distro: workspace.Distro,
				OwnerDeviceID: deviceID, ReadOnly: true, CreatedAt: workspace.CreatedAt, UpdatedAt: workspace.UpdatedAt,
			}
			if err := add(TypeWorkspace, deviceID+":"+workspace.ID, project.ID, "", meta, timeVersion(workspace.UpdatedAt)); err != nil {
				return nil, err
			}
		}
		tasks, err := database.ListTasks(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if task.ReadOnly {
				continue
			}
			task.Code = ""
			task.RuntimeConfigSnapshotID = ""
			task.WorktreeDir = ""
			task.TaskWorkspacePath = ""
			task.OwnerDeviceID = deviceID
			task.ReadOnly = true
			prefix := deviceID + ":"
			if err := add(TypeTask, prefix+task.ID, project.ID, task.ID, task, timeVersion(task.UpdatedAt)); err != nil {
				return nil, err
			}
			agents, err := database.ListTaskAgents(ctx, task.ID)
			if err != nil {
				return nil, err
			}
			for _, agent := range agents {
				agent.RuntimeConfigSnapshotID = ""
				agent.Backend = ""
				agent.ModelSource = ""
				agent.ModelID = ""
				agent.ModelName = ""
				agent.WireModel = ""
				agent.CodexAccountID = ""
				agent.ReasoningEffort = ""
				agent.Filesystem = ""
				agent.Approval = ""
				agent.RuntimeConfigValid = false
				agent.RuntimeConfigError = ""
				agent.UnreadCount = 0
				if err := add(TypeTaskAgent, prefix+task.ID+":"+agent.AgentID, project.ID, task.ID, agent, timeVersion(agent.UpdatedAt)); err != nil {
					return nil, err
				}
			}
			rounds, err := database.SyncRounds(ctx, task.ID)
			if err != nil {
				return nil, err
			}
			for _, round := range rounds {
				if err := add(TypeRound, prefix+round.ID, project.ID, task.ID, round, timeVersion(round.FinishedAt)); err != nil {
					return nil, err
				}
			}
			turns, err := database.SyncTurns(ctx, task.ID)
			if err != nil {
				return nil, err
			}
			for _, turn := range turns {
				turn.BackendSessionID = ""
				turn.RuntimeConfigSnapshotID = ""
				turn.Instruction = ""
				turn.PromptSnapshot = ""
				turn.InboxBatchID = ""
				if err := add(TypeTurn, prefix+turn.ID, project.ID, task.ID, turn, timeVersion(turn.FinishedAt)); err != nil {
					return nil, err
				}
			}
			conversation, err := database.SyncConversation(ctx, task.ID)
			if err != nil {
				return nil, err
			}
			for _, item := range conversation {
				item.Sequence = 0
				item.Payload = nil
				if err := add(TypeConversation, prefix+item.ID, project.ID, task.ID, item, timeVersion(item.CreatedAt)); err != nil {
					return nil, err
				}
			}
			memory, err := database.TaskMemory(ctx, task.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				memory.Extra = nil
				if err := add(TypeTaskMemory, prefix+task.ID, project.ID, task.ID, memory, timeVersion(memory.UpdatedAt)); err != nil {
					return nil, err
				}
			}
			hardwareGroups, err := database.HardwareGroups(ctx, task.ID)
			if err != nil {
				return nil, err
			}
			for _, group := range hardwareGroups {
				group.CredentialRef = ""
				group.Access = domain.HardwareAccessReadOnly
				if err := add(TypeHardware, prefix+task.ID+":"+group.ID, project.ID, task.ID, group, timeVersion(group.UpdatedAt)); err != nil {
					return nil, err
				}
			}
		}
	}
	return result, nil
}

func applyTaskGraphObject(ctx context.Context, database *store.Store, obj domain.SyncObject) error {
	if obj.Operation == "delete" {
		ownerDeviceID, sourceID, ok := strings.Cut(obj.ID, ":")
		if !ok || strings.TrimSpace(ownerDeviceID) == "" || strings.TrimSpace(sourceID) == "" || (obj.Type != TypeTask && obj.Type != TypeWorkspace) {
			return errors.New("invalid owned task graph tombstone")
		}
		syncKey := obj.IdempotencyKey
		if syncKey == "" {
			syncKey = obj.EventID
		}
		localWorkspaceID := ""
		if obj.Type == TypeWorkspace {
			localWorkspaceID = syncedWorkspaceID(ownerDeviceID, sourceID)
		}
		return database.ApplyRemoteGraphDelete(ctx, obj.Type, ownerDeviceID, sourceID, localWorkspaceID, syncKey, obj.SourceVersion, time.Now().UTC())
	}
	var envelope graphEnvelope
	if err := json.Unmarshal(obj.Payload, &envelope); err != nil {
		return fmt.Errorf("decode %s graph envelope: %w", obj.Type, err)
	}
	if envelope.ProjectID == "" || len(envelope.Value) == 0 {
		return errors.New("invalid task graph dependency metadata")
	}
	if obj.Type == TypeProject {
		var project domain.Project
		if err := json.Unmarshal(envelope.Value, &project); err != nil {
			return err
		}
		project.ID = envelope.ProjectID
		project.DefaultWorkspaceID = ""
		if _, err := database.Project(ctx, project.ID); errors.Is(err, sql.ErrNoRows) {
			return database.CreateProject(ctx, project)
		} else if err != nil {
			return err
		}
		return database.UpdateProject(ctx, project)
	}
	if envelope.OwnerDeviceID == "" {
		return errors.New("remote task graph object has no owner_device_id")
	}
	if _, err := database.Project(ctx, envelope.ProjectID); err != nil {
		return fmt.Errorf("project dependency %s: %w", envelope.ProjectID, err)
	}
	if obj.Type == TypeWorkspace {
		var meta workspaceMetadata
		if err := json.Unmarshal(envelope.Value, &meta); err != nil {
			return err
		}
		if meta.OwnerDeviceID != envelope.OwnerDeviceID {
			return errors.New("workspace owner_device_id mismatch")
		}
		if obj.ID != ownedGraphWireID(envelope.OwnerDeviceID, meta.ID) {
			return errors.New("workspace wire ownership mismatch")
		}
		return database.UpsertSyncedWorkspaceFromSource(ctx, domain.Workspace{
			ID: syncedWorkspaceID(envelope.OwnerDeviceID, meta.ID), ProjectID: envelope.ProjectID, Name: meta.Name,
			Locality: "remote", Transport: meta.Transport, SSHHost: meta.SSHHost, SSHUser: meta.SSHUser,
			SSHPort: meta.SSHPort, SSHAuth: meta.SSHAuth, SSHPasswordConfigured: meta.SSHConfigured,
			Distro: meta.Distro, OwnerDeviceID: envelope.OwnerDeviceID, ReadOnly: true,
			CreatedAt: meta.CreatedAt, UpdatedAt: meta.UpdatedAt,
		}, meta.ID)
	}
	if envelope.TaskID == "" {
		return errors.New("remote task graph object has no task_id")
	}
	if obj.Type != TypeTask {
		exists, err := database.RemoteTaskObjectExists(ctx, envelope.OwnerDeviceID, TypeTask, envelope.TaskID)
		if err != nil {
			return err
		}
		if !exists {
			deleted, tombstoneErr := database.RemoteGraphTombstoned(ctx, TypeTask, ownedGraphWireID(envelope.OwnerDeviceID, envelope.TaskID))
			if tombstoneErr != nil {
				return tombstoneErr
			}
			if deleted {
				return nil
			}
			return fmt.Errorf("task dependency %s is missing", envelope.TaskID)
		}
	} else if obj.ID != ownedGraphWireID(envelope.OwnerDeviceID, envelope.TaskID) {
		return errors.New("task wire ownership mismatch")
	}
	value := append(json.RawMessage(nil), envelope.Value...)
	switch obj.Type {
	case TypeTask:
		var task domain.Task
		if err := json.Unmarshal(value, &task); err != nil {
			return err
		}
		task.OwnerDeviceID = envelope.OwnerDeviceID
		task.ReadOnly = true
		task.WorkspaceID = syncedWorkspaceID(envelope.OwnerDeviceID, task.WorkspaceID)
		task.RuntimeConfigSnapshotID = ""
		task.WorktreeDir = ""
		task.TaskWorkspacePath = ""
		value, _ = json.Marshal(task)
	case TypeTaskAgent:
		var agent domain.TaskAgent
		if err := json.Unmarshal(value, &agent); err != nil {
			return err
		}
		agent.RuntimeConfigSnapshotID = ""
		agent.Backend = ""
		agent.ModelSource = ""
		agent.ModelID = ""
		agent.ModelName = ""
		agent.WireModel = ""
		agent.CodexAccountID = ""
		agent.ReasoningEffort = ""
		agent.Filesystem = ""
		agent.Approval = ""
		agent.RuntimeConfigValid = false
		agent.RuntimeConfigError = ""
		agent.UnreadCount = 0
		value, _ = json.Marshal(agent)
	case TypeTurn:
		var turn domain.Turn
		if err := json.Unmarshal(value, &turn); err != nil {
			return err
		}
		turn.BackendSessionID = ""
		turn.RuntimeConfigSnapshotID = ""
		turn.Instruction = ""
		turn.PromptSnapshot = ""
		turn.InboxBatchID = ""
		value, _ = json.Marshal(turn)
	case TypeConversation:
		var item domain.ConversationItem
		if err := json.Unmarshal(value, &item); err != nil {
			return err
		}
		item.Sequence = 0
		item.Payload = nil
		value, _ = json.Marshal(item)
	case TypeTaskMemory:
		var memory domain.TaskMemory
		if err := json.Unmarshal(value, &memory); err != nil {
			return err
		}
		memory.Extra = nil
		value, _ = json.Marshal(memory)
	case TypeHardware:
		var group domain.HardwareGroup
		if err := json.Unmarshal(value, &group); err != nil {
			return err
		}
		if obj.ID != ownedGraphWireID(envelope.OwnerDeviceID, envelope.TaskID)+":"+group.ID {
			return errors.New("hardware wire ownership mismatch")
		}
		group.TaskID = envelope.TaskID
		group.CredentialRef = ""
		group.Access = domain.HardwareAccessReadOnly
		value, _ = json.Marshal(group)
	}
	return database.UpsertRemoteTaskObject(ctx, store.RemoteTaskObject{OwnerDeviceID: envelope.OwnerDeviceID, ObjectType: obj.Type, ObjectID: sourceGraphObjectID(obj.Type, obj.ID, envelope.TaskID), TaskID: envelope.TaskID, ProjectID: envelope.ProjectID, Payload: value, UpdatedAt: time.Now().UTC()})
}

func syncedWorkspaceID(deviceID, sourceID string) string {
	sum := sha256.Sum256([]byte(deviceID + "\x00" + sourceID))
	return "syncws_" + hex.EncodeToString(sum[:10])
}
func sourceGraphObjectID(kind, wireID, taskID string) string {
	if kind == TypeTask {
		return taskID
	}
	return wireID
}

func ownedGraphWireID(ownerDeviceID, sourceID string) string {
	return ownerDeviceID + ":" + sourceID
}
