package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
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
	// Same slug as the global demo skill: auto-attach must keep only one copy.
	files["projects/vault-skills/skills/demo/SKILL.md"] = "---\nname: Demo\ndescription: Project demo skill\n---\n\n# Project Demo\n"
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
	return newVaultSkillsFixtureWithManager(t, buildVaultReadManager(t, true, vaultSkillFiles()))
}

// buildVaultSkillManagerWithoutIndex opens a real vault with skill files but
// no index, which is the cold-start/rebuild window the catalog must report.
func buildVaultSkillManagerWithoutIndex(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Vault Skills"}); err != nil {
		t.Fatal(err)
	}
	for relative, content := range vaultSkillFiles() {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func newVaultSkillsFixtureWithManager(t *testing.T, manager *knowledgevault.Manager) vaultSkillsFixture {
	t.Helper()
	return newVaultSkillsFixtureForProject(t, manager, domain.Project{ID: vaultSkillsProjectID, Name: "Vault Skills"})
}

func newVaultSkillsFixtureForProject(t *testing.T, manager *knowledgevault.Manager, project domain.Project) vaultSkillsFixture {
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
	if project.Name == "" {
		project.Name = project.ID
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	if project.UpdatedAt.IsZero() {
		project.UpdatedAt = now
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-" + strings.ReplaceAll(project.ID, "_", "-"), ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native",
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
	service := NewService(database, secretStore, &stubExecutor{})
	service.SetKnowledgeVault(manager, true)
	return vaultSkillsFixture{service: service, database: database, ctx: ctx, project: project, workspace: workspace}
}

func (f vaultSkillsFixture) createTask(t *testing.T, skillIDs []string) domain.Task {
	t.Helper()
	task, err := f.service.CreateTask(f.ctx, CreateTaskInput{
		ProjectID: f.project.ID, WorkspaceID: f.workspace.ID, Title: "vault skills",
		Request: "vault skills", SkillIDs: skillIDs, ModelID: "model-vault-skills",
		AllowSystemProject: domain.IsSystemManagedProject(f.project),
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

func TestVaultSkillCatalogUsesDBProjectNameOverManifestCache(t *testing.T) {
	files := vaultSkillFiles()
	// The root index title is the generated knowledge-home page, not the
	// project name. The manifest therefore only knows the slug here; the DB
	// projects table supplies the real display name.
	files["projects/vault-skills/index.md"] = "---\nid: knowledge_root_project_vault_skills\ntitle: 知识首页\nscope: project\nproject_id: " + vaultSkillsProjectID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\nProject entry.\n"
	fixture := newVaultSkillsFixtureWithManager(t, buildVaultReadManager(t, true, files))

	catalog, err := fixture.service.VaultSkillCatalog(fixture.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var projectSkill, globalSkill *VaultSkillView
	for index := range catalog.Skills {
		skill := &catalog.Skills[index]
		switch skill.Key {
		case "vault-skill:project:" + vaultSkillsProjectID + ":vault-review":
			projectSkill = skill
		case "vault-skill:global:demo":
			globalSkill = skill
		}
	}
	if projectSkill == nil {
		t.Fatalf("project skill missing from catalog: %#v", catalog.Skills)
	}
	if projectSkill.ProjectName != fixture.project.Name || projectSkill.Library != fixture.project.Name {
		t.Fatalf("project skill name = %q library = %q, want %q", projectSkill.ProjectName, projectSkill.Library, fixture.project.Name)
	}
	if globalSkill == nil || globalSkill.ProjectName != "" || globalSkill.Library != "全局技能" {
		t.Fatalf("global skill = %#v, want no project name and the global library", globalSkill)
	}

	libraries, err := fixture.service.VaultLibraries(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, library := range libraries.Projects {
		if library.ProjectID != fixture.project.ID {
			continue
		}
		found = true
		if library.Name != fixture.project.Name {
			t.Fatalf("library name = %q, want %q", library.Name, fixture.project.Name)
		}
	}
	if !found {
		t.Fatalf("project library missing from picker: %#v", libraries.Projects)
	}
}

// A project whose vault read is effective takes auto-attach from the vault
// preferences only. Legacy AutoAttach rows, including one with the same slug
// as a vault package, must not be merged in.
func TestVaultAutoAttachUsesVaultSourceAndLoadsPackageFiles(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	for _, item := range []domain.Skill{
		{ID: "skill-legacy-global", PackageSlug: "legacy-global", Scope: "global", Name: "Legacy Global", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-project", PackageSlug: "legacy-project", Scope: "project", ProjectID: fixture.project.ID, Name: "Legacy Project", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-demo", PackageSlug: "demo", Scope: "global", Name: "Demo", Instructions: "legacy demo", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-manual", PackageSlug: "legacy-manual", Scope: "global", Name: "Legacy Manual", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
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
	// Preferences live in their own table: the legacy skills page still sees
	// every row and no vault skill becomes a legacy row.
	legacy, err := fixture.database.ListSkills(ctx, "", "", false)
	if err != nil || len(legacy) != 4 {
		t.Fatalf("legacy skills changed: %#v err=%v", legacy, err)
	}

	task := fixture.createTask(t, nil)
	if len(task.SkillIDs) != 2 {
		t.Fatalf("vault auto-attach merged another source: %#v", task.SkillIDs)
	}
	for _, want := range []string{
		"vault-skill:global:demo", "vault-skill:project:" + fixture.project.ID + ":vault-review",
	} {
		if !hasTaskSkill(task, want) {
			t.Fatalf("auto-attach missed %s: %#v", want, task.SkillIDs)
		}
	}
	for _, unwanted := range []string{"skill-legacy-global", "skill-legacy-project", "skill-legacy-demo", "skill-legacy-manual"} {
		if hasTaskSkill(task, unwanted) {
			t.Fatalf("legacy skill %s was merged into a vault-sourced task: %#v", unwanted, task.SkillIDs)
		}
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
}

// When the runtime read gate is off the vault preference is not effective and
// the legacy AutoAttach rule is no longer consulted: skills are vault-only.
func TestVaultAutoAttachIgnoresLegacyWhenRuntimeReadOff(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	for _, item := range []domain.Skill{
		{ID: "skill-legacy-global", Scope: "global", Name: "Legacy Global", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-project", Scope: "project", ProjectID: fixture.project.ID, Name: "Legacy Project", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := fixture.database.CreateSkill(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}
	fixture.service.knowledgeVaultRuntimeRead = false

	task := fixture.createTask(t, nil)
	if len(task.SkillIDs) != 0 {
		t.Fatalf("a legacy AutoAttach row was applied after the vault read was turned off: %#v", task.SkillIDs)
	}
}

// An unbound project is not vault-effective and the retired legacy AutoAttach
// rows are not a fallback source any more.
func TestVaultAutoAttachIgnoresLegacyForUnboundProject(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	otherProject := domain.Project{ID: "project_vault_unbound", Name: "Unbound", CreatedAt: now, UpdatedAt: now}
	if err := fixture.database.CreateProject(ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	otherWorkspace := domain.Workspace{
		ID: "workspace-vault-unbound", ProjectID: otherProject.ID, Name: "local", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateWorkspace(ctx, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	for _, item := range []domain.Skill{
		{ID: "skill-unbound-global", Scope: "global", Name: "Global Auto", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-unbound-project", Scope: "project", ProjectID: otherProject.ID, Name: "Project Auto", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := fixture.database.CreateSkill(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}

	task, err := fixture.service.CreateTask(ctx, CreateTaskInput{
		ProjectID: otherProject.ID, WorkspaceID: otherWorkspace.ID, Title: "unbound auto",
		Request: "unbound auto", ModelID: "model-vault-skills",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.SkillIDs) != 0 {
		t.Fatalf("an unbound project received a legacy auto-attach skill: %#v", task.SkillIDs)
	}
}

// A legacy AutoAttach row is never merged: the vault preference is the only
// auto-attach source.
func TestVaultAutoAttachIgnoresLegacyRowsWithoutVaultCounterpart(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	for _, item := range []domain.Skill{
		{ID: "skill-legacy-only", PackageSlug: "legacy-only", Scope: "global", Name: "Legacy Only", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-legacy-demo", PackageSlug: "demo", Scope: "global", Name: "Demo", Instructions: "legacy", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := fixture.database.CreateSkill(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}

	selection := fixture.service.vaultAutoAttachSelection(ctx, fixture.project.ID)
	if len(selection.keys) != 1 || selection.keys[0] != "vault-skill:global:demo" {
		t.Fatalf("vault auto-attach selection = %#v", selection.keys)
	}
	task := fixture.createTask(t, nil)
	if len(task.SkillIDs) != 1 || task.SkillIDs[0] != "vault-skill:global:demo" {
		t.Fatalf("legacy auto-attach was still merged: %#v", task.SkillIDs)
	}
}

// Explicit selection is untouched by the source split: a skill the owner
// checked is still applied even when the project reads auto-attach from the
// vault.
func TestVaultAutoAttachKeepsExplicitManualSelection(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}
	manual := "vault-skill:project:" + fixture.project.ID + ":vault-review"
	task := fixture.createTask(t, []string{manual})
	for _, want := range []string{manual, "vault-skill:global:demo"} {
		if !hasTaskSkill(task, want) {
			t.Fatalf("explicit selection or vault auto-attach missing %s: %#v", want, task.SkillIDs)
		}
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

func TestVaultAutoAttachDeduplicatesSameSlugByProject(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true); err != nil {
		t.Fatal(err)
	}
	projectKey := "vault-skill:project:" + fixture.project.ID + ":demo"
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, projectKey, true, false); err != nil {
		t.Fatal(err)
	}
	keys := fixture.service.vaultAutoAttachSkillKeys(ctx, fixture.project.ID)
	if len(keys) != 1 || keys[0] != projectKey {
		t.Fatalf("same-slug auto-attach = %#v, want only %s", keys, projectKey)
	}
	task := fixture.createTask(t, nil)
	if hasTaskSkill(task, "vault-skill:global:demo") {
		t.Fatalf("the global copy of a project skill was auto-attached: %#v", task.SkillIDs)
	}
	if !hasTaskSkill(task, projectKey) {
		t.Fatalf("the project copy was not auto-attached: %#v", task.SkillIDs)
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
	// An unbound project can create a Task while the selection is unavailable.
	logs := captureLog(t)
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
	task, err := fixture.service.CreateTask(ctx, CreateTaskInput{
		ProjectID: otherProject.ID, WorkspaceID: otherWorkspace.ID, Title: "unbound",
		Request: "unbound", SkillIDs: []string{"vault-skill:global:demo"}, ApplyAutoSkills: boolPointer(false),
		ModelID: "model-vault-skills",
	})
	if err != nil || task.Status != domain.TaskActive || !hasTaskSkill(task, "vault-skill:global:demo") {
		t.Fatalf("unbound task = %#v, err = %v", task, err)
	}
	if !strings.Contains(logs.String(), "vault-skill:global:demo") {
		t.Fatal("missing skill degradation warning")
	}
	if _, err := fixture.service.UpdateTaskSkills(ctx, task.ID, []string{"vault-skill:global:demo"}); err == nil {
		t.Fatal("task skill update accepted an unavailable skill")
	}
	turn, err := fixture.service.SubmitMessage(ctx, task.ID, task.OriginalRequest)
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, fixture.database, turn.ID, domain.TurnSucceeded)
	if _, err := fixture.service.RetryProjectKnowledgeSpace(ctx, otherProject.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if skills := fixture.service.activeTaskSkills(ctx, stored); len(skills) != 1 || skills[0].ID != "vault-skill:global:demo" {
		t.Fatalf("recovered Task skills = %+v", skills)
	}
}

func TestVaultSkillAutoAttachRequiresIndexReady(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureWithManager(t, buildVaultSkillManagerWithoutIndex(t))
	ctx := fixture.ctx
	view, err := fixture.service.SetVaultSkillAutoAttach(ctx, "vault-skill:global:demo", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if view.AutoAttachEffective {
		t.Fatalf("write response reported effective without an index: %#v", view)
	}
	if view.UnavailableReason != "index_not_ready" {
		t.Fatalf("write response reason = %q, want index_not_ready", view.UnavailableReason)
	}
	catalog, err := fixture.service.VaultSkillCatalog(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.IndexReady {
		t.Fatal("catalog reported an index that does not exist")
	}
	found := false
	for _, item := range catalog.Skills {
		if item.Key != "vault-skill:global:demo" {
			continue
		}
		found = true
		if !item.AutoAttach || item.AutoAttachEffective {
			t.Fatalf("catalog auto-attach state without an index = %#v", item)
		}
		if item.UnavailableReason != "index_not_ready" {
			t.Fatalf("catalog reason = %q, want index_not_ready", item.UnavailableReason)
		}
	}
	if !found {
		t.Fatal("global vault skill missing from the catalog")
	}
	if keys := fixture.service.vaultAutoAttachSkillKeys(ctx, fixture.project.ID); len(keys) != 0 {
		t.Fatalf("auto-attach ran without an index: %#v", keys)
	}
	task := fixture.createTask(t, nil)
	if hasTaskSkill(task, "vault-skill:global:demo") {
		t.Fatalf("task inherited a vault skill while the index was unavailable: %#v", task.SkillIDs)
	}
}

// captureLog redirects the standard logger for the duration of one test. The
// package's parallel tests are paused while a sequential test runs, so the
// process-wide logger cannot interleave with them.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

const vaultSystemProjectID = "project_system_skills"

// A system project has no project vault space by design (task-535). Its
// selected global vault skills must still load, and repeating the Turn must not
// repeat the old "selected skills unavailable" warning.
func TestVaultTaskSkillsForSystemProjectLoadsGlobalSkillsQuietly(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureForProject(t, buildVaultReadManager(t, true, vaultSkillFiles()), domain.Project{
		ID: vaultSystemProjectID, Name: "System Skills", ProjectType: "system", KnowledgePolicy: "none",
	})
	ctx := fixture.ctx
	task := fixture.createTask(t, []string{"vault-skill:global:demo"})
	if !hasTaskSkill(task, "vault-skill:global:demo") {
		t.Fatalf("system project rejected the global vault skill: %#v", task.SkillIDs)
	}

	logs := captureLog(t)
	for attempt := 0; attempt < 3; attempt++ {
		active := fixture.service.activeTaskSkills(ctx, task)
		if len(active) != 1 {
			t.Fatalf("attempt %d active skills = %#v, want only the global vault skill", attempt, active)
		}
		demo := active[0]
		if demo.ID != "vault-skill:global:demo" || len(demo.PackageFiles) != 2 || demo.PackageFiles[0].Path != "SKILL.md" {
			t.Fatalf("attempt %d global vault package = %#v", attempt, demo)
		}
	}
	if out := logs.String(); strings.Contains(out, "unavailable") || strings.Contains(out, "knowledge vault task skills") {
		t.Fatalf("system project repeated a vault skill warning: %s", out)
	}
	// Re-saving the same selection must keep validating; the pet Task edits its
	// own Skill list through this path.
	if _, err := fixture.service.UpdateTaskSkills(ctx, task.ID, []string{"vault-skill:global:demo"}); err != nil {
		t.Fatalf("update system project task skills: %v", err)
	}
}

// A stale project-scoped vault selection on a system project can never load
// because the space is intentionally absent. That is reported once, not on
// every Turn, and it does not fail the skill load.
func TestVaultTaskSkillsForSystemProjectReportsStaleProjectSkillOnce(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureForProject(t, buildVaultReadManager(t, true, vaultSkillFiles()), domain.Project{
		ID: vaultSystemProjectID, Name: "System Skills", ProjectType: "system", KnowledgePolicy: "none",
	})
	staleID := "vault-skill:project:" + vaultSkillsProjectID + ":vault-review"
	logs := captureLog(t)
	for attempt := 0; attempt < 3; attempt++ {
		if got := fixture.service.vaultTaskSkills(fixture.ctx, fixture.project.ID, []string{staleID}); len(got) != 0 {
			t.Fatalf("attempt %d loaded a project skill without a project space: %#v", attempt, got)
		}
	}
	out := logs.String()
	if count := strings.Count(out, "knowledge vault task skills"); count != 1 {
		t.Fatalf("stale project skill warnings = %d, want 1; logs=%s", count, out)
	}
	if !strings.Contains(out, "has no project vault space") {
		t.Fatalf("warning did not explain the infrastructure reason: %s", out)
	}
}

// A normal project whose index is not ready keeps the diagnostic warning, and
// because the source has not changed it repeats each Turn exactly as before.
func TestVaultTaskSkillsNormalProjectIndexNotReadyKeepsDiagnostic(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureWithManager(t, buildVaultSkillManagerWithoutIndex(t))
	logs := captureLog(t)
	for attempt := 0; attempt < 2; attempt++ {
		if got := fixture.service.vaultTaskSkills(fixture.ctx, fixture.project.ID, []string{"vault-skill:global:demo"}); len(got) != 0 {
			t.Fatalf("attempt %d loaded a skill without an index: %#v", attempt, got)
		}
	}
	out := logs.String()
	if count := strings.Count(out, "selected skill(s) unavailable"); count != 2 {
		t.Fatalf("normal project warnings = %d, want 2 (one per Turn); logs=%s", count, out)
	}
	if !strings.Contains(out, "index is not ready") {
		t.Fatalf("normal project warning lost the index diagnostic: %s", out)
	}
}

// Global vault skills are valid for an infrastructure project; project-scoped
// ones are not. Normal unbound projects keep the old rejection.
func TestVaultSkillExistsInProjectFollowsInfrastructureScope(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureForProject(t, buildVaultReadManager(t, true, vaultSkillFiles()), domain.Project{
		ID: vaultSystemProjectID, Name: "System Skills", ProjectType: "system", KnowledgePolicy: "none",
	})
	ctx := fixture.ctx
	if !fixture.service.vaultSkillExistsInProject(ctx, fixture.project.ID, "vault-skill:global:demo") {
		t.Fatal("system project rejected a global vault skill")
	}
	if fixture.service.vaultSkillExistsInProject(ctx, fixture.project.ID, "vault-skill:project:"+vaultSkillsProjectID+":vault-review") {
		t.Fatal("system project accepted a project vault skill from another project")
	}
	// A normal project that the manifest does not bind keeps the old rule.
	unbound := domain.Project{ID: "project_skill_unbound", Name: "Unbound", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := fixture.database.CreateProject(ctx, unbound); err != nil {
		t.Fatal(err)
	}
	if fixture.service.vaultSkillExistsInProject(ctx, unbound.ID, "vault-skill:global:demo") {
		t.Fatal("unbound normal project accepted a vault skill")
	}
}

// task-535 keeps a non-empty infrastructure space and its manifest mapping.
// That must not make project-scoped skills usable: saving, auto-attach and
// runtime resolution all stay global-only, and the repeated warning stays gone.
func vaultInfrastructureRetainedFiles() map[string]string {
	files := vaultSkillFiles()
	files["projects/pet/skills/pet-skill/SKILL.md"] = "---\nname: Pet Skill\ndescription: Retained pet project skill\n---\n\n# Pet Skill\n"
	return files
}

func TestVaultInfrastructureRetainedSpaceUsesGlobalSkillsOnly(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureForProject(t, buildVaultReadManager(t, true, vaultInfrastructureRetainedFiles()), domain.Project{
		ID: petProjectID, Name: "宠物", ProjectType: "system", KnowledgePolicy: "none",
	})
	ctx := fixture.ctx
	if !fixture.service.vaultReadEnabled(fixture.project.ID) {
		t.Fatal("precondition: a retained non-empty space should keep the project vault read enabled")
	}
	globalKey := "vault-skill:global:demo"
	projectKey := "vault-skill:project:" + petProjectID + ":pet-skill"
	if !fixture.service.vaultSkillExistsInProject(ctx, fixture.project.ID, globalKey) {
		t.Fatal("infrastructure project rejected a global vault skill")
	}
	if fixture.service.vaultSkillExistsInProject(ctx, fixture.project.ID, projectKey) {
		t.Fatal("infrastructure project accepted a project vault skill from its retained space")
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, globalKey, true, true); err != nil {
		t.Fatal(err)
	}
	// A stale project preference remains in the store (the space was kept), but
	// it must not reach a new infrastructure task.
	if _, err := fixture.service.SetVaultSkillAutoAttach(ctx, projectKey, true, false); err != nil {
		t.Fatal(err)
	}
	if keys := fixture.service.vaultAutoAttachSkillKeys(ctx, fixture.project.ID); len(keys) != 1 || keys[0] != globalKey {
		t.Fatalf("infrastructure auto-attach = %#v, want only %s", keys, globalKey)
	}
	task := fixture.createTask(t, nil)
	if !hasTaskSkill(task, globalKey) || hasTaskSkill(task, projectKey) {
		t.Fatalf("infrastructure task skills = %#v, want only the global vault skill", task.SkillIDs)
	}
	if _, err := fixture.service.UpdateTaskSkills(ctx, task.ID, []string{globalKey, projectKey}); err == nil || !strings.Contains(err.Error(), projectKey) {
		t.Fatalf("saving a project vault skill on an infrastructure project err = %v", err)
	}
	logs := captureLog(t)
	for attempt := 0; attempt < 3; attempt++ {
		byID := map[string]domain.Skill{}
		for _, item := range fixture.service.activeTaskSkills(ctx, task) {
			byID[item.ID] = item
		}
		if _, ok := byID[globalKey]; !ok {
			t.Fatalf("attempt %d lost the global vault skill: %#v", attempt, byID)
		}
		if _, ok := byID[projectKey]; ok {
			t.Fatalf("attempt %d loaded a project skill from the retained space: %#v", attempt, byID)
		}
	}
	if out := logs.String(); strings.Contains(out, "knowledge vault task skills") {
		t.Fatalf("infrastructure project repeated a vault skill warning: %s", out)
	}
}

// With the vault read gate off, a historical legacy id keeps loading its
// legacy package instead of being rewritten to a vault key that cannot load.
// A Task created before the cutover can still store a legacy skills-table id.
// That id has no vault package, so it is unavailable: the runtime never falls
// back to the legacy table and the selection cannot be saved again.
func TestHistoricalLegacySkillIsUnavailable(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	legacy := domain.Skill{
		ID: "skill-legacy-demo", PackageSlug: "demo", Scope: "global", Name: "Demo",
		Instructions: "legacy demo", Version: 1, Status: "active", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateSkill(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	task := fixture.createTask(t, nil)
	if err := fixture.database.UpdateTaskSkills(ctx, task.ID, []string{legacy.ID}, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	active := fixture.service.activeTaskSkills(ctx, stored)
	if len(active) != 0 {
		t.Fatalf("legacy id was loaded from the retired skills table: %#v", active)
	}
	if _, err := fixture.service.UpdateTaskSkills(ctx, task.ID, []string{legacy.ID}); err == nil || !strings.Contains(err.Error(), legacy.ID) {
		t.Fatalf("legacy id was accepted by task skill validation: %v", err)
	}
}
