package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

const vaultSkillsProjectID = "project_vault_skills"

func vaultSkillFiles() map[string]string {
	files := vaultCanaryFiles()
	files["projects/vault-skills/index.md"] = "---\nid: knowledge_root_project_vault_skills\ntitle: Vault Skills\nscope: project\nproject_id: " + vaultSkillsProjectID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\nProject entry.\n"
	files["global/skills/demo/SKILL.md"] = "---\nname: Demo\ndescription: Vault demo\n---\n\n# Vault Demo\n"
	files["global/skills/demo/scripts/check.sh"] = "#!/bin/sh\necho vault\n"
	files["projects/vault-skills/skills/vault-review/SKILL.md"] = "---\nname: Vault Review\ndescription: Project vault review\n---\n\n# Vault Review\n"
	files["projects/vault-skills/skills/vault-review/scripts/run.sh"] = "#!/bin/sh\necho review\n"
	return files
}

type vaultSkillsFixture struct {
	service   *Service
	database  *store.Store
	ctx       context.Context
	project   domain.Project
	workspace domain.Workspace
}

func newVaultSkillsFixture(t *testing.T) vaultSkillsFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: vaultSkillsProjectID, Name: "Vault Skills", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-vault-skills", ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	envGroup := domain.EnvGroup{ID: "env-vault-skills", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-vault-skills", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	manager := buildVaultReadManager(t, true, vaultSkillFiles())
	service := NewService(database, secretStore, &stubExecutor{})
	service.SetKnowledgeVault(manager, true)
	return vaultSkillsFixture{service: service, database: database, ctx: ctx, project: project, workspace: workspace}
}

func (f vaultSkillsFixture) createTask(t *testing.T, skillIDs []string) domain.Task {
	t.Helper()
	task, err := f.service.CreateTask(f.ctx, CreateTaskInput{
		ProjectID: f.project.ID, WorkspaceID: f.workspace.ID, Title: "vault skills",
		Request: "vault skills", SkillIDs: skillIDs, ModelID: "model-vault-skills",
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func hasTaskSkill(task domain.Task, id string) bool {
	for _, item := range task.SkillIDs {
		if item == id {
			return true
		}
	}
	return false
}

func TestVaultSkillAutoAttachMergesWithLegacyAndLoadsPackageFiles(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	for _, item := range []domain.Skill{
		{ID: "skill-legacy-global", Scope: "global", Name: "Legacy Global", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-project", Scope: "project", ProjectID: fixture.project.ID, Name: "Legacy Project", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-manual", Scope: "global", Name: "Legacy Manual", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := fixture.database.CreateSkill(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:project:"+fixture.project.ID+":vault-review", true, false); err != nil {
		t.Fatal(err)
	}
	// The preference table is the only new storage: vault skills never become
	// legacy rows, so the existing AutoAttach path is unchanged.
	legacy, err := fixture.database.ListSkills(ctx, "", "", false)
	if err != nil || len(legacy) != 3 {
		t.Fatalf("legacy skills changed: %#v err=%v", legacy, err)
	}

	task := fixture.createTask(t, nil)
	for _, want := range []string{
		"skill-legacy-global", "skill-legacy-project",
		"vault-skill:global:demo", "vault-skill:project:" + fixture.project.ID + ":vault-review",
	} {
		if !hasTaskSkill(task, want) {
			t.Fatalf("auto-attach missed %s: %#v", want, task.SkillIDs)
		}
	}
	if hasTaskSkill(task, "skill-legacy-manual") {
		t.Fatalf("non-auto legacy skill was selected: %#v", task.SkillIDs)
	}

	active := fixture.service.activeTaskSkills(ctx, task)
	byID := map[string]domain.Skill{}
	for _, item := range active {
		byID[item.ID] = item
	}
	demo, ok := byID["vault-skill:global:demo"]
	if !ok || len(demo.PackageFiles) != 2 {
		t.Fatalf("vault global skill was not materialized: %#v", byID)
	}
	if demo.PackageFiles[0].Path != "SKILL.md" || demo.Scope != "global" || demo.Status != "active" {
		t.Fatalf("vault global skill shape = %#v", demo)
	}
	review, ok := byID["vault-skill:project:"+fixture.project.ID+":vault-review"]
	if !ok || len(review.PackageFiles) != 2 || review.ProjectID != fixture.project.ID {
		t.Fatalf("vault project skill was not materialized: %#v", byID)
	}
	if _, ok := byID["skill-legacy-global"]; !ok {
		t.Fatal("legacy auto-attach skill disappeared")
	}
}

func TestVaultGlobalAutoAttachRequiresSecondConfirmation(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, false); !errors.Is(err, ErrVaultGlobalConfirmationRequired) {
		t.Fatalf("global enable without confirmation err = %v", err)
	}
	if prefs, err := fixture.database.ListVaultSkillPreferences(ctx); err != nil || len(prefs) != 0 {
		t.Fatalf("rejected global enable wrote a preference: %#v err=%v", prefs, err)
	}
	view, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true)
	if err != nil || !view.AutoAttach || !view.AutoAttachConfirmed {
		t.Fatalf("confirmed global enable = %#v err=%v", view, err)
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", false, false); err != nil {
		t.Fatal(err)
	}
	// Disabling clears the confirmation so turning it on again asks the owner
	// once more.
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, false); !errors.Is(err, ErrVaultGlobalConfirmationRequired) {
		t.Fatalf("re-enable after disable err = %v", err)
	}
}

func TestVaultSkillAutoAttachDegradesForUnboundProject(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}
	if keys := fixture.service.vaultAutoAttachSkillKeys(ctx, "project_unbound"); len(keys) != 0 {
		t.Fatalf("unbound project received vault auto-attach: %#v", keys)
	}
	// A bound project still gets the global preference.
	if keys := fixture.service.vaultAutoAttachSkillKeys(ctx, fixture.project.ID); len(keys) != 1 || keys[0] != "vault-skill:global:demo" {
		t.Fatalf("bound project vault auto-attach = %#v", keys)
	}
	// Explicit selection in an unbound project fails clearly instead of
	// silently dropping the skill.
	otherProject := domain.Project{ID: "project_unbound", Name: "Unbound", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := fixture.database.CreateProject(ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	otherWorkspace := domain.Workspace{
		ID: "workspace-unbound", ProjectID: otherProject.ID, Name: "local", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := fixture.database.CreateWorkspace(ctx, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreateTask(ctx, CreateTaskInput{
		ProjectID: otherProject.ID, WorkspaceID: otherWorkspace.ID, Title: "unbound",
		Request: "unbound", SkillIDs: []string{"vault-skill:global:demo"}, ApplyAutoSkills: boolPointer(false),
		ModelID: "model-vault-skills",
	}); err == nil || !strings.Contains(err.Error(), "vault-skill:global:demo") {
		t.Fatalf("unbound project explicit vault skill err = %v", err)
	}
}

func TestApplyVaultSkillPackagesStillPrefersVaultAndFallsBack(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	legacy := []domain.SkillFile{{Path: "SKILL.md", Content: "legacy"}}
	result := fixture.service.applyVaultSkillPackages(fixture.ctx, fixture.project.ID, []domain.Skill{
		{ID: "skill_demo", Scope: "global", PackageSlug: "demo", PackageFiles: legacy},
		{ID: "skill_missing", Scope: "global", PackageSlug: "missing", PackageFiles: legacy},
	})
	if len(result[0].PackageFiles) != 2 || result[0].PackageFiles[0].Content == "legacy" {
		t.Fatalf("vault package was not substituted: %#v", result[0])
	}
	if len(result[1].PackageFiles) != 1 || result[1].PackageFiles[0].Content != "legacy" {
		t.Fatalf("missing vault package did not fall back: %#v", result[1])
	}
}
