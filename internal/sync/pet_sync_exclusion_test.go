package sync

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestExportExcludesSystemManagedPetProject(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	now := time.Now().UTC()
	petProject := domain.Project{
		ID: "project_pet", Name: "宠物", ProjectType: "system", KnowledgePolicy: "none",
		CreatedAt: now, UpdatedAt: now,
	}
	petWorkspace := domain.Workspace{
		ID: "workspace_pet", ProjectID: petProject.ID, Name: "pet-workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		OwnerDeviceID: "device-a", CreatedAt: now, UpdatedAt: now,
	}
	normalProject := domain.Project{
		ID: "project-normal", Name: "Normal", ProjectType: "folder", KnowledgePolicy: "none",
		CreatedAt: now, UpdatedAt: now,
	}
	normalWorkspace := domain.Workspace{
		ID: "workspace-normal", ProjectID: normalProject.ID, Name: "normal",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		OwnerDeviceID: "device-a", CreatedAt: now, UpdatedAt: now,
	}
	for _, item := range []domain.Project{petProject, normalProject} {
		if err := db.CreateProject(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []domain.Workspace{petWorkspace, normalWorkspace} {
		if err := db.CreateWorkspace(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	objects, err := ExportBusinessObjectsForDevice(ctx, db, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(objects)
	text := string(raw)
	for _, forbidden := range []string{"project_pet", "workspace_pet", "task_pet"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("pet object %q was exported: %s", forbidden, text)
		}
	}
	foundProject, foundWorkspace := false, false
	for _, obj := range objects {
		if obj.Type == TypeProject && obj.ID == normalProject.ID {
			foundProject = true
		}
		if obj.Type == TypeWorkspace && strings.Contains(obj.ID, normalWorkspace.ID) {
			foundWorkspace = true
		}
	}
	if !foundProject || !foundWorkspace {
		t.Fatalf("normal project/workspace missing: project=%t workspace=%t", foundProject, foundWorkspace)
	}
}

func TestPetSyncObjectsAreSkipped(t *testing.T) {
	ctx, db := context.Background(), businessStore(t)
	now := time.Now().UTC()
	petProject := domain.Project{
		ID: "project_pet", Name: "宠物", ProjectType: "system", KnowledgePolicy: "none",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateProject(ctx, petProject); err != nil {
		t.Fatal(err)
	}
	petTask := domain.Task{
		ID: "task_pet", ProjectID: petProject.ID, WorkspaceID: "workspace_pet",
		Title: "宠物", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now,
	}
	if !skipSystemManagedSyncObject(ctx, db, TypeTask, graphObject(t, TypeTask, "work-windows:task_pet", "work-windows", petProject.ID, "task_pet", petTask)) {
		t.Fatal("pet task graph object was not skipped")
	}
	if !skipSystemManagedSyncObject(ctx, db, TypeTask, domain.SyncObject{Type: TypeTask, ID: "work-windows:task_pet", Operation: "delete"}) {
		t.Fatal("pet task delete was not skipped")
	}
	if !skipSystemManagedSyncObject(ctx, db, TypeProject, graphObject(t, TypeProject, "project_pet", "work-windows", petProject.ID, "", petProject)) {
		t.Fatal("pet project object was not skipped")
	}
	normal := domain.Project{ID: "project-normal", Name: "Normal", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateProject(ctx, normal); err != nil {
		t.Fatal(err)
	}
	if skipSystemManagedSyncObject(ctx, db, TypeProject, graphObject(t, TypeProject, normal.ID, "device-b", normal.ID, "", normal)) {
		t.Fatal("normal project was skipped")
	}
}
