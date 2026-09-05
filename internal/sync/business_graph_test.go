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
	workspace := domain.Workspace{ID: "workspace-one", ProjectID: project.ID, Name: "Local", Locality: "local", Transport: "native", RootPath: `C:\private\repo`, SSHHost: "private-host", SSHCredentialRef: "workspace/private/password", SSHPasswordConfigured: true, Health: "ready", Capabilities: map[string]any{"runtime": "private"}, Repository: map[string]any{"path": "private"}, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	objects, err := ExportBusinessObjectsForDevice(ctx, db, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(objects)
	text := string(raw)
	for _, forbidden := range []string{`C:\\private\\repo`, "private-host", "workspace/private/password", `\"runtime\":\"private\"`, `\"path\":\"private\"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("graph export leaked %q: %s", forbidden, text)
		}
	}
	if len(objects) < 2 || objects[0].Type != TypeProject || objects[1].Type != TypeWorkspace {
		t.Fatalf("dependency order=%#v", objects)
	}
}
