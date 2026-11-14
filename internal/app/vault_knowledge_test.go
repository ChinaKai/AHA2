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
	for _, projectID := range []string{petProjectID, aha2ProjectID, "project_other"} {
		if !knowledgeVaultReadAllowed(projectID) {
			t.Fatalf("unset allowlist did not admit %s", projectID)
		}
	}
	if knowledgeVaultReadAllowed("") {
		t.Fatal("unset allowlist admitted an empty project ID")
	}
	if scope := KnowledgeVaultReadScope(); scope != "all" {
		t.Fatalf("unset read scope = %q, want all", scope)
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, "project_other")
	if knowledgeVaultReadAllowed(petProjectID) || knowledgeVaultReadAllowed(aha2ProjectID) || !knowledgeVaultReadAllowed("project_other") {
		t.Fatal("explicit allowlist was not applied")
	}
	if scope := KnowledgeVaultReadScope(); scope != "project_other" {
		t.Fatalf("explicit read scope = %q", scope)
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID+", "+aha2ProjectID)
	for _, projectID := range []string{petProjectID, aha2ProjectID} {
		if !knowledgeVaultReadAllowed(projectID) {
			t.Fatalf("comma-separated allowlist missed %s", projectID)
		}
	}
	if knowledgeVaultReadAllowed("project_other") {
		t.Fatal("comma-separated allowlist admitted a project outside the list")
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, "")
	for _, projectID := range []string{petProjectID, aha2ProjectID, "project_other"} {
		if knowledgeVaultReadAllowed(projectID) {
			t.Fatalf("empty allowlist did not roll %s back to legacy knowledge", projectID)
		}
	}
	if scope := KnowledgeVaultReadScope(); scope != "none" {
		t.Fatalf("empty read scope = %q, want none", scope)
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

func TestVaultKnowledgeDefaultsToAllProjectsAndHonoursRollbackAllowlist(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	manager := buildVaultReadManager(t, true, vaultCanaryFiles())
	service := &Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true}
	ctx := context.Background()

	// With the env unset every bound project reads the vault, not just the old
	// pet/AHA2 canary pair.
	for _, projectID := range []string{petProjectID, aha2ProjectID, "project_other"} {
		snapshot, ok := service.vaultKnowledgeForProject(ctx, projectID)
		if !ok || len(snapshot.Project) != 2 || snapshot.Project[0].ProjectID != projectID {
			t.Fatalf("project %s snapshot = %#v ok=%v", projectID, snapshot, ok)
		}
	}

	pet, ok := service.vaultKnowledgeForProject(ctx, petProjectID)
	if !ok {
		t.Fatal("pet project did not read the vault")
	}
	if len(pet.Stale) != 1 || pet.Stale[0].ID != "knowledge_pet_stale" {
		t.Fatalf("pet stale entries = %#v", pet.Stale)
	}
	for _, entry := range append(append([]domain.KnowledgeEntry{}, pet.Global...), pet.Project...) {
		if entry.ID == "knowledge_aha2_note" || entry.ID == "knowledge_other_note" {
			t.Fatalf("project isolation leaked %s", entry.ID)
		}
	}
	// The rebuild backfills global/general/index.md for the fixture's
	// global/general/shared.md, so the global feed also carries that generated
	// navigation page. Assert the real entries are present instead of pinning
	// the total count.
	globalIDs := map[string]bool{}
	for _, entry := range pet.Global {
		globalIDs[entry.ID] = true
	}
	for _, want := range []string{"knowledge_root_global", "knowledge_shared"} {
		if !globalIDs[want] {
			t.Fatalf("global entries are missing %s: %#v", want, pet.Global)
		}
	}

	// An explicit list is the rollback lever: only listed projects read vault.
	t.Setenv(knowledgeVaultReadProjectsEnv, petProjectID+","+aha2ProjectID)
	if _, ok := service.vaultKnowledgeForProject(ctx, petProjectID); !ok {
		t.Fatal("listed project stopped reading the vault")
	}
	if _, ok := service.vaultKnowledgeForProject(ctx, "project_other"); ok {
		t.Fatal("non-allowlisted project read the vault")
	}

	t.Setenv(knowledgeVaultReadProjectsEnv, "")
	for _, projectID := range []string{petProjectID, aha2ProjectID, "project_other"} {
		if _, ok := service.vaultKnowledgeForProject(ctx, projectID); ok {
			t.Fatalf("empty allowlist did not roll %s back", projectID)
		}
	}
}

func TestVaultKnowledgeRuntimeReadGateOffFallsBack(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	files := vaultCanaryFiles()
	files["global/skills/demo/SKILL.md"] = "---\nname: Demo\ndescription: Vault demo\n---\n\n# Vault Demo\n"
	manager := buildVaultReadManager(t, true, files)
	ctx := context.Background()

	// The CLI/env default is on, but an explicit false must keep both knowledge
	// and skills on the legacy sources even when the allowlist is unset.
	service := &Service{knowledgeVault: manager}
	for _, projectID := range []string{petProjectID, aha2ProjectID} {
		if service.vaultReadEnabled(projectID) {
			t.Fatalf("runtime gate off still routed %s to the vault", projectID)
		}
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

	enabled := &Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true}
	snapshot, ok := enabled.vaultKnowledgeForProject(ctx, petProjectID)
	if !ok || len(snapshot.Project) == 0 {
		t.Fatalf("runtime gate on did not read the vault: %#v ok=%v", snapshot, ok)
	}
}

func TestVaultKnowledgeFallsBackWhenDisabledOrUnbound(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	disabled := buildVaultReadManager(t, false, vaultCanaryFiles())
	if (&Service{knowledgeVault: disabled, knowledgeVaultRuntimeRead: true}).vaultReadEnabled(petProjectID) {
		t.Fatal("Web vault switch off still routed reads to the vault")
	}
	if _, ok := (&Service{knowledgeVault: disabled, knowledgeVaultRuntimeRead: true}).vaultKnowledgeForProject(context.Background(), petProjectID); ok {
		t.Fatal("disabled vault was treated as a read source")
	}

	manager := buildVaultReadManager(t, true, map[string]string{})
	manager.SetRuntimeWrite(true)
	service := &Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true, knowledgeVaultRuntimeWrite: true}
	if service.vaultReadEnabled(petProjectID) {
		t.Fatal("unbound vault project was routed to the vault")
	}
	if service.vaultKnowledgeWriteEnabled(petProjectID) {
		t.Fatal("unbound vault project was allowed to write the vault")
	}
	if _, ok := service.vaultKnowledgeForProject(context.Background(), petProjectID); ok {
		t.Fatal("unbound vault project was treated as a read source")
	}
	legacy := []domain.SkillFile{{Path: "SKILL.md", Content: "legacy"}}
	skills := []domain.Skill{{ID: "skill_project", Scope: "project", ProjectID: petProjectID, PackageSlug: "demo", PackageFiles: legacy}}
	result := service.applyVaultSkillPackages(context.Background(), petProjectID, skills)
	if len(result[0].PackageFiles) != 1 || result[0].PackageFiles[0].Content != "legacy" {
		t.Fatalf("unbound project did not keep the legacy skill package: %#v", result[0].PackageFiles)
	}
}

func TestVaultKnowledgeWriteRequiresReadAndRuntimeGates(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	manager := buildVaultReadManager(t, true, vaultCanaryFiles())

	// Read gate off (even with runtime write on) must make writes unavailable;
	// the write gate never routes a project that reads legacy back to the vault.
	manager.SetRuntimeWrite(true)
	readOff := &Service{knowledgeVault: manager, knowledgeVaultRuntimeWrite: true}
	if readOff.vaultKnowledgeWriteEnabled(petProjectID) {
		t.Fatal("write gate enabled while the read gate is off")
	}

	// Read on but runtime write off.
	writeOff := &Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true}
	if writeOff.vaultKnowledgeWriteEnabled(petProjectID) {
		t.Fatal("write gate enabled while runtime write is off")
	}

	// Read and runtime write on, but the engine gate still off.
	manager.SetRuntimeWrite(false)
	if (&Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true, knowledgeVaultRuntimeWrite: true}).vaultKnowledgeWriteEnabled(petProjectID) {
		t.Fatal("write gate enabled while the engine gate is off")
	}

	// All three agree.
	manager.SetRuntimeWrite(true)
	if !(&Service{knowledgeVault: manager, knowledgeVaultRuntimeRead: true, knowledgeVaultRuntimeWrite: true}).vaultKnowledgeWriteEnabled(petProjectID) {
		t.Fatal("write gate stayed off with read, runtime write and engine gates on")
	}

	// The Web vault switch is part of the read gate, so a disabled manager
	// keeps writes on the legacy path too.
	disabled := buildVaultReadManager(t, false, vaultCanaryFiles())
	disabled.SetRuntimeWrite(true)
	if (&Service{knowledgeVault: disabled, knowledgeVaultRuntimeRead: true, knowledgeVaultRuntimeWrite: true}).vaultKnowledgeWriteEnabled(petProjectID) {
		t.Fatal("disabled Web vault still routed writes to the vault")
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
