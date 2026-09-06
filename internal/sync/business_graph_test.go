package sync

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func graphObject(t *testing.T, kind, id, device, projectID, taskID string, value any) domain.SyncObject {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(graphEnvelope{OwnerDeviceID: device, ProjectID: projectID, TaskID: taskID, Value: raw})
	if err != nil {
		t.Fatal(err)
	}
	return domain.SyncObject{Type: kind, ID: id, Operation: "upsert", Payload: payload}
}

func TestTaskGraphTwoDevicesStayReadOnlyAndDistinct(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-shared", Name: "Shared", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := applyBusinessObject(ctx, db, graphObject(t, TypeProject, project.ID, "device-a", project.ID, "", project)); err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{"device-a", "device-b"} {
		workspace := workspaceMetadata{ID: "workspace-local", ProjectID: project.ID, Name: "Remote " + device, OwnerDeviceID: device, ReadOnly: true, CreatedAt: now, UpdatedAt: now}
		if err := applyBusinessObject(ctx, db, graphObject(t, TypeWorkspace, device+":"+workspace.ID, device, project.ID, "", workspace)); err != nil {
			t.Fatal(err)
		}
		task := domain.Task{ID: "task-local", ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Remote", Status: domain.TaskWaitingUser, OwnerDeviceID: device, ReadOnly: true, CreatedAt: now, UpdatedAt: now}
		if err := applyBusinessObject(ctx, db, graphObject(t, TypeTask, device+":"+task.ID, device, project.ID, task.ID, task)); err != nil {
			t.Fatal(err)
		}
		agent := domain.TaskAgent{TaskID: task.ID, AgentID: "main", Role: "main", Status: "idle", CreatedAt: now, UpdatedAt: now}
		if err := applyBusinessObject(ctx, db, graphObject(t, TypeTaskAgent, device+":"+task.ID+":main", device, project.ID, task.ID, agent)); err != nil {
			t.Fatal(err)
		}
	}
	workspaces, err := db.ListWorkspaces(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 2 || workspaces[0].ID == workspaces[1].ID {
		t.Fatalf("remote workspaces=%#v", workspaces)
	}
	for _, workspace := range workspaces {
		if !workspace.ReadOnly || workspace.OwnerDeviceID == "" || workspace.RootPath != "" || workspace.SSHCredentialRef != "" {
			t.Fatalf("unsafe remote workspace: %#v", workspace)
		}
	}
	for _, device := range []string{"device-a", "device-b"} {
		objects, err := db.RemoteTaskObjects(ctx, device, "task-local")
		if err != nil {
			t.Fatal(err)
		}
		if len(objects) != 2 || objects[0].ObjectType != TypeTask || objects[1].ObjectType != TypeTaskAgent {
			t.Fatalf("%s objects=%#v", device, objects)
		}
		var task domain.Task
		if err := json.Unmarshal(objects[0].Payload, &task); err != nil {
			t.Fatal(err)
		}
		if !task.ReadOnly || task.OwnerDeviceID != device || task.RuntimeConfigSnapshotID != "" || task.TaskWorkspacePath != "" {
			t.Fatalf("unsafe remote task: %#v", task)
		}
	}
}

func TestProjectKnowledgeReturnsToSyncedProject(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-one", Name: "Project", ProjectType: "git", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := applyBusinessObject(ctx, db, graphObject(t, TypeProject, project.ID, "device-a", project.ID, "", project)); err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{ID: "knowledge-one", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Scoped", Body: "Body", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now}
	payload, _ := json.Marshal(entry)
	if err := applyBusinessObject(ctx, db, domain.SyncObject{Type: TypeKnowledge, ID: entry.ID, Operation: "upsert", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Knowledge(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Scope != "project" || stored.ProjectID != project.ID {
		t.Fatalf("knowledge misplaced: %#v", stored)
	}
}

func TestGraphExportStripsWorkspaceRuntimeAndConversationPayload(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-one", Name: "Project", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{ID: "workspace-one", ProjectID: project.ID, Name: "Local", Locality: "remote", Transport: "ssh", RootPath: `C:\private\repo`, SSHHost: "private-host", SSHUser: "builder", SSHPort: 2222, SSHAuth: "password", SSHCredentialRef: "workspace/private/password", SSHPasswordConfigured: true, Health: "ready", Capabilities: map[string]any{"runtime": "private"}, Repository: map[string]any{"path": "private"}, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	objects, err := ExportBusinessObjectsForDevice(ctx, db, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(objects)
	text := string(raw)
	for _, forbidden := range []string{`C:\\private\\repo`, "workspace/private/password", `\"runtime\":\"private\"`, `\"path\":\"private\"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("graph export leaked %q: %s", forbidden, text)
		}
	}
	if len(objects) < 2 || objects[0].Type != TypeProject || objects[1].Type != TypeWorkspace {
		t.Fatalf("dependency order=%#v", objects)
	}
	var envelope graphEnvelope
	if err := json.Unmarshal(objects[1].Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	var metadata workspaceMetadata
	if err := json.Unmarshal(envelope.Value, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Transport != "ssh" || metadata.SSHHost != workspace.SSHHost || metadata.SSHUser != workspace.SSHUser || metadata.SSHPort != workspace.SSHPort || !metadata.SSHConfigured {
		t.Fatalf("safe SSH metadata was not materialized: %#v", metadata)
	}
}

func TestBusinessHandlersIgnoreCurrentDeviceTaskMirrors(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-self", Name: "Self", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{}
	RegisterBusinessHandlersForDevice(engine, db, "device-local")
	localTask := domain.Task{ID: "task-local", ProjectID: project.ID, Title: "Local", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
	localObject := graphObject(t, TypeTask, "device-local:task-local", "device-local", project.ID, localTask.ID, localTask)
	if err := engine.handlers[TypeTask](ctx, localObject); err != nil {
		t.Fatal(err)
	}
	found, err := db.RemoteTaskObjectExists(ctx, "device-local", TypeTask, localTask.ID)
	if err != nil || found {
		t.Fatalf("current device task was mirrored: found=%t err=%v", found, err)
	}
	remoteTask := localTask
	remoteTask.ID, remoteTask.Title = "task-remote", "Remote"
	remoteObject := graphObject(t, TypeTask, "device-remote:task-remote", "device-remote", project.ID, remoteTask.ID, remoteTask)
	if err := engine.handlers[TypeTask](ctx, remoteObject); err != nil {
		t.Fatal(err)
	}
	found, err = db.RemoteTaskObjectExists(ctx, "device-remote", TypeTask, remoteTask.ID)
	if err != nil || !found {
		t.Fatalf("other device task was not mirrored: found=%t err=%v", found, err)
	}
	hardware := domain.HardwareGroup{TaskID: remoteTask.ID, ID: "board", Mode: domain.HardwareModeSerial, Serial: domain.HardwareSerialConfig{Device: "/dev/cu.remote", Baudrate: 115200}, Access: domain.HardwareAccessReadOnly}
	wrongHardware := graphObject(t, TypeHardware, "device-other:task-remote:board", "device-remote", project.ID, remoteTask.ID, hardware)
	if err := engine.handlers[TypeHardware](ctx, wrongHardware); err == nil || !strings.Contains(err.Error(), "ownership mismatch") {
		t.Fatalf("mismatched hardware ownership was accepted: %v", err)
	}
	validHardware := graphObject(t, TypeHardware, "device-remote:task-remote:board", "device-remote", project.ID, remoteTask.ID, hardware)
	if err := engine.handlers[TypeHardware](ctx, validHardware); err != nil {
		t.Fatal(err)
	}
	objects, err := db.RemoteTaskObjects(ctx, "device-remote", remoteTask.ID)
	if err != nil || len(objects) != 2 || objects[1].ObjectType != TypeHardware {
		t.Fatalf("remote hardware object=%#v err=%v", objects, err)
	}
}
