package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestCreateTaskAppliesAutoAttachSkills(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-auto", Name: "Auto", CreatedAt: now, UpdatedAt: now}
	for _, item := range []domain.Project{
		project,
		{ID: petProjectID, Name: "宠物", ProjectType: "system", KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now},
	} {
		if err := database.CreateProject(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	workspace := domain.Workspace{
		ID: "workspace-auto", ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now,
	}
	petWorkspace := domain.Workspace{
		ID: petWorkspaceID, ProjectID: petProjectID, Name: "pet", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now,
	}
	for _, item := range []domain.Workspace{workspace, petWorkspace} {
		if err := database.CreateWorkspace(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	envGroup := domain.EnvGroup{ID: "env-auto", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-auto", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	skills := []domain.Skill{
		{ID: "skill-global-auto", Scope: "global", Name: "Global Auto", Instructions: "global auto", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-project-auto", Scope: "project", ProjectID: project.ID, Name: "Project Auto", Instructions: "project auto", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-other-auto", Scope: "project", ProjectID: "project-other", Name: "Other Auto", Instructions: "other auto", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-manual", Scope: "global", Name: "Manual", Instructions: "manual", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-disabled-auto", Scope: "global", Name: "Disabled Auto", Instructions: "disabled", Version: 1, Status: "active", Enabled: false, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, item := range skills {
		if err := database.CreateSkill(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, secretStore, &stubExecutor{})

	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "auto", Request: "auto skills", ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSkillID(task.SkillIDs, "skill-global-auto") || !hasSkillID(task.SkillIDs, "skill-project-auto") {
		t.Fatalf("auto skills were not applied: %#v", task.SkillIDs)
	}
	for _, id := range []string{"skill-other-auto", "skill-manual", "skill-disabled-auto"} {
		if hasSkillID(task.SkillIDs, id) {
			t.Fatalf("unexpected auto skill %s in %#v", id, task.SkillIDs)
		}
	}

	explicit := false
	task, err = service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "explicit", Request: "explicit skills",
		ModelID: model.ID, SkillIDs: []string{"skill-global-auto"}, ApplyAutoSkills: &explicit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.SkillIDs) != 1 || task.SkillIDs[0] != "skill-global-auto" {
		t.Fatalf("explicit selection was changed: %#v", task.SkillIDs)
	}

	petTask, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: petProjectID, WorkspaceID: petWorkspaceID, Title: "pet", Request: "pet auto",
		ModelID: model.ID, AllowSystemProject: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSkillID(petTask.SkillIDs, "skill-global-auto") {
		t.Fatalf("system task did not receive the global auto skill: %#v", petTask.SkillIDs)
	}
	if hasSkillID(petTask.SkillIDs, "skill-project-auto") {
		t.Fatalf("system task received another project's skill: %#v", petTask.SkillIDs)
	}
}

func TestBoundProjectSkillIsSelectable(t *testing.T) {
	if !skillAppliesToProject(domain.Skill{Scope: "project", ProjectID: "container", BoundProjectID: "bound"}, "bound") {
		t.Fatal("bound project skill was rejected")
	}
	if skillAppliesToProject(domain.Skill{Scope: "project", ProjectID: "other"}, "bound") {
		t.Fatal("unrelated project skill was accepted")
	}
}

func hasSkillID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
