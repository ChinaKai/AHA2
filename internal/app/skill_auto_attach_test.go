package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
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
	// Skills are vault packages. Auto-attach comes from vault preferences only;
	// a legacy AutoAttach row must not be merged into the task selection.
	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(vaultConfig, knowledgevault.InitOptions{VaultName: "Auto Attach"}); err != nil {
		t.Fatal(err)
	}
	write := func(relative, content string) {
		target := filepath.Join(vaultConfig.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/auto/index.md", "---\nid: knowledge_root_project_auto\ntitle: Auto\nscope: project\nproject_id: "+project.ID+"\ntype: navigation\nstatus: verified\nis_index: true\n---\nAuto.\n")
	write("projects/auto/skills/project-auto/SKILL.md", "---\nname: Project Auto\ndescription: project auto\n---\n\nproject auto\n")
	write("projects/other/index.md", "---\nid: knowledge_root_project_other\ntitle: Other\nscope: project\nproject_id: project-other\ntype: navigation\nstatus: verified\nis_index: true\n---\nOther.\n")
	write("projects/other/skills/other-auto/SKILL.md", "---\nname: Other Auto\ndescription: other auto\n---\n\nother auto\n")
	write("global/skills/global-auto/SKILL.md", "---\nname: Global Auto\ndescription: global auto\n---\n\nglobal auto\n")
	write("global/skills/manual/SKILL.md", "---\nname: Manual\ndescription: manual\n---\n\nmanual\n")
	index, err := knowledgevault.OpenIndex(ctx, vaultConfig.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, vaultConfig.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	service := NewService(database, secretStore, &stubExecutor{})
	service.SetKnowledgeVault(manager, true)
	service.SetKnowledgeVaultRuntimeWrite(true)
	globalKey := "vault-skill:global:global-auto"
	projectKey := "vault-skill:project:" + project.ID + ":project-auto"
	otherKey := "vault-skill:project:project-other:other-auto"
	manualKey := "vault-skill:global:manual"
	if _, err := service.SetVaultSkillAutoAttach(ctx, globalKey, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetVaultSkillAutoAttach(ctx, projectKey, true, false); err != nil {
		t.Fatal(err)
	}
	// Retired legacy row: it must be ignored even though AutoAttach is true.
	if err := database.CreateSkill(ctx, domain.Skill{
		ID: "skill-legacy-auto", Scope: "global", Name: "Legacy Auto", Instructions: "legacy",
		Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "auto", Request: "auto skills", ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSkillID(task.SkillIDs, globalKey) || !hasSkillID(task.SkillIDs, projectKey) {
		t.Fatalf("vault auto skills were not applied: %#v", task.SkillIDs)
	}
	for _, id := range []string{otherKey, manualKey, "skill-legacy-auto"} {
		if hasSkillID(task.SkillIDs, id) {
			t.Fatalf("unexpected auto skill %s in %#v", id, task.SkillIDs)
		}
	}

	explicit := false
	task, err = service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "explicit", Request: "explicit skills",
		ModelID: model.ID, SkillIDs: []string{globalKey}, ApplyAutoSkills: &explicit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.SkillIDs) != 1 || task.SkillIDs[0] != globalKey {
		t.Fatalf("explicit selection was changed: %#v", task.SkillIDs)
	}

	petTask, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: petProjectID, WorkspaceID: petWorkspaceID, Title: "pet", Request: "pet auto",
		ModelID: model.ID, AllowSystemProject: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSkillID(petTask.SkillIDs, globalKey) {
		t.Fatalf("system task did not receive the global auto skill: %#v", petTask.SkillIDs)
	}
	if hasSkillID(petTask.SkillIDs, projectKey) {
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
