package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func withUnsetEnv(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestKnowledgeVaultReadAllowlistParsing(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	for _, projectID := range []string{petProjectID, aha2ProjectID} {
		if !knowledgeVaultReadAllowed(projectID) {
			t.Fatalf("default allowlist does not include %s", projectID)
		}
	}
	if knowledgeVaultReadAllowed("project_other") || knowledgeVaultReadAllowed("") {
		t.Fatal("default allowlist accepted a non-canary project")
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, "project_other")
	if knowledgeVaultReadAllowed(petProjectID) || knowledgeVaultReadAllowed(aha2ProjectID) || !knowledgeVaultReadAllowed("project_other") {
		t.Fatal("explicit allowlist was not applied")
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID+", "+aha2ProjectID)
	for _, projectID := range []string{petProjectID, aha2ProjectID} {
		if !knowledgeVaultReadAllowed(projectID) {
			t.Fatalf("comma-separated allowlist missed %s", projectID)
		}
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, "")
	if knowledgeVaultReadAllowed(petProjectID) || knowledgeVaultReadAllowed(aha2ProjectID) {
		t.Fatal("empty allowlist did not roll back to legacy knowledge")
	}
}

func buildVaultReadManager(t *testing.T, enabled bool, files map[string]string) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Canary"}); err != nil {
		t.Fatal(err)
	}
	for relative, content := range files {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		if err := manager.SetEnabled(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func vaultCanaryFiles() map[string]string {
	return map[string]string{
		"global/index.md":              "---\nid: knowledge_root_global\ntitle: 全局首页\nscope: global\ntype: navigation\nstatus: verified\nis_index: true\n---\n全局入口。\n",
		"global/general/shared.md":     "---\nid: knowledge_shared\ntitle: 共享经验\nscope: global\ntype: practice\nstatus: verified\n---\n共享正文。\n",
		"projects/pet/index.md":        "---\nid: knowledge_root_project_pet\ntitle: 宠物项目\nscope: project\nproject_id: project_pet\ntype: navigation\nstatus: verified\nis_index: true\n---\n宠物入口。\n",
		"projects/pet/pet-note.md":     "---\nid: knowledge_pet_note\ntitle: 宠物文档\nscope: project\nproject_id: project_pet\ntype: diagnostic\nstatus: verified\n---\n宠物正文。\n",
		"projects/pet/pet-stale.md":    "---\nid: knowledge_pet_stale\ntitle: 宠物旧文\nscope: project\nproject_id: project_pet\ntype: practice\nstatus: stale\n---\n待修订正文。\n",
		"projects/aha2/index.md":       "---\nid: knowledge_root_project_aha2\ntitle: AHA2 项目\nscope: project\nproject_id: project_6ff866f348d8a892d5e8\ntype: navigation\nstatus: verified\nis_index: true\n---\nAHA2 入口。\n",
		"projects/aha2/aha2-note.md":   "---\nid: knowledge_aha2_note\ntitle: AHA2 文档\nscope: project\nproject_id: project_6ff866f348d8a892d5e8\ntype: reference\nstatus: verified\n---\nAHA2 正文。\n",
		"projects/other/index.md":      "---\nid: knowledge_root_project_other\ntitle: 其他项目\nscope: project\nproject_id: project_other\ntype: navigation\nstatus: verified\nis_index: true\n---\n其他入口。\n",
		"projects/other/other-note.md": "---\nid: knowledge_other_note\ntitle: 其他文档\nscope: project\nproject_id: project_other\ntype: practice\nstatus: verified\n---\n其他正文。\n",
	}
}

func TestVaultKnowledgeCanaryProjectsAndIsolation(t *testing.T) {
	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID+","+aha2ProjectID)
	manager := buildVaultReadManager(t, true, vaultCanaryFiles())
	service := &Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true}
	ctx := context.Background()

	pet, ok := service.vaultKnowledgeForProject(ctx, petProjectID)
	if !ok {
		t.Fatal("pet project did not read the vault")
	}
	if len(pet.Project) != 2 || pet.Project[0].ProjectID != petProjectID {
		t.Fatalf("pet project entries = %#v", pet.Project)
	}
	if len(pet.Stale) != 1 || pet.Stale[0].ID != "knowledge_pet_stale" {
		t.Fatalf("pet stale entries = %#v", pet.Stale)
	}
	for _, entry := range append(append([]domain.KnowledgeEntry{}, pet.Global...), pet.Project...) {
		if entry.ID == "knowledge_aha2_note" || entry.ID == "knowledge_other_note" {
			t.Fatalf("project isolation leaked %s", entry.ID)
		}
	}
	if len(pet.Global) != 2 {
		t.Fatalf("global entries = %#v", pet.Global)
	}

	aha2, ok := service.vaultKnowledgeForProject(ctx, aha2ProjectID)
	if !ok || len(aha2.Project) != 2 || aha2.Project[0].ProjectID != aha2ProjectID {
		t.Fatalf("AHA2 project snapshot = %#v ok=%v", aha2, ok)
	}

	if _, ok := service.vaultKnowledgeForProject(ctx, "project_other"); ok {
		t.Fatal("non-allowlisted project read the vault")
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, "")
	if _, ok := service.vaultKnowledgeForProject(ctx, petProjectID); ok {
		t.Fatal("empty allowlist did not roll back")
	}
}

func TestVaultKnowledgeRuntimeReadGateDefaultsOff(t *testing.T) {
	var zero Service
	if zero.knowledgeVaultRuntimeRead {
		t.Fatal("runtime read gate must default to false")
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID+","+aha2ProjectID)
	files := vaultCanaryFiles()
	files["global/skills/demo/SKILL.md"] = "---\nname: Demo\ndescription: Vault demo\n---\n\n# Vault Demo\n"
	manager := buildVaultReadManager(t, true, files)
	service := &Service{knowledgeVault: manager}
	ctx := context.Background()

	for _, projectID := range []string{petProjectID, aha2ProjectID} {
		if _, ok := service.vaultKnowledgeForProject(ctx, projectID); ok {
			t.Fatalf("runtime gate off still read vault knowledge for %s", projectID)
		}
		legacy := []domain.SkillFile{{Path: "SKILL.md", Content: "legacy"}}
		skills := []domain.Skill{{ID: "skill_demo", Scope: "global", PackageSlug: "demo", PackageFiles: legacy}}
		result := service.applyVaultSkillPackages(ctx, projectID, skills)
		if len(result[0].PackageFiles) != 1 || result[0].PackageFiles[0].Content != "legacy" {
			t.Fatalf("runtime gate off still loaded vault skills for %s: %#v", projectID, result[0].PackageFiles)
		}
	}
}

func TestVaultKnowledgeFallsBackWhenDisabledOrUnbound(t *testing.T) {
	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID+","+aha2ProjectID)
	disabled := buildVaultReadManager(t, false, vaultCanaryFiles())
	if _, ok := (&Service{knowledgeVault: disabled, knowledgeVaultRuntimeRead: true}).vaultKnowledgeForProject(context.Background(), petProjectID); ok {
		t.Fatal("disabled vault was treated as a read source")
	}

	manager := buildVaultReadManager(t, true, map[string]string{})
	if _, ok := (&Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true}).vaultKnowledgeForProject(context.Background(), petProjectID); ok {
		t.Fatal("unbound vault project was treated as a read source")
	}
}

func TestApplyVaultSkillPackagesUsesVaultAndFallsBack(t *testing.T) {
	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID)
	manager := buildVaultReadManager(t, true, map[string]string{
		"global/skills/demo/SKILL.md":                 "---\nname: Demo\ndescription: Vault demo\n---\n\n# Vault Demo\n",
		"global/skills/demo/scripts/run.sh":           "#!/bin/sh\necho vault\n",
		"projects/pet/index.md":                       "---\nid: knowledge_root_project_pet\ntitle: 宠物项目\nscope: project\nproject_id: project_pet\ntype: navigation\nstatus: verified\nis_index: true\n---\n宠物入口。\n",
		"projects/pet/skills/project-demo/SKILL.md":   "---\nname: Project Demo\ndescription: Project vault demo\n---\n\n# Project Vault Demo\n",
		"projects/pet/skills/project-demo/scripts/go": "#!/bin/sh\necho project\n",
	})
	service := &Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true}
	legacy := []domain.SkillFile{{Path: "SKILL.md", Content: "legacy"}}
	skills := []domain.Skill{
		{ID: "skill_demo", Scope: "global", PackageSlug: "demo", PackageFiles: legacy},
		{ID: "skill_missing", Scope: "global", PackageSlug: "missing", PackageFiles: legacy},
		{ID: "skill_project", Scope: "project", ProjectID: petProjectID, PackageSlug: "project-demo", PackageFiles: legacy},
	}
	result := service.applyVaultSkillPackages(context.Background(), petProjectID, skills)
	if len(result[0].PackageFiles) != 2 || result[0].PackageFiles[1].Path != "scripts/run.sh" {
		t.Fatalf("vault skill was not loaded: %#v", result[0])
	}
	if len(result[1].PackageFiles) != 1 || result[1].PackageFiles[0].Content != "legacy" {
		t.Fatalf("legacy fallback was not preserved: %#v", result[1])
	}
	if len(result[2].PackageFiles) != 2 || result[2].PackageFiles[1].Path != "scripts/go" {
		t.Fatalf("project vault skill was not loaded: %#v", result[2])
	}
	legacyDir := filepath.Join(manager.Config().DataDir, "knowledge", "skills")
	if _, err := os.Stat(legacyDir); err == nil {
		t.Fatal("test unexpectedly created a legacy skill directory")
	}
}
