package app

import (
	"encoding/json"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPetKnowledgeSkillAndPromptManagement(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageKnowledge: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{
		ID: "project-knowledge", Name: "知识项目", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}

	submitted, err := fixture.service.PetSubmitKnowledge(fixture.ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "部署前先验证候选", Body: "每次部署前先运行完整测试。", Confidence: 0.9,
		Reason: "多次部署失败都源于未跑完整测试",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Entries) != 1 || submitted.Entries[0].Status != string(domain.KnowledgeCandidate) {
		t.Fatalf("submitted entries=%#v", submitted.Entries)
	}
	if len(submitted.Proposals) != 1 || submitted.Proposals[0].Status != string(domain.KnowledgeProposalPending) {
		t.Fatalf("submitted proposals=%#v", submitted.Proposals)
	}
	view, err := fixture.service.PetListKnowledge(fixture.ctx, "project", project.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Proposals) != 1 || view.Proposals[0].Title != "部署前先验证候选" {
		t.Fatalf("knowledge view=%#v", view)
	}
	proposal, err := fixture.database.KnowledgeProposal(fixture.ctx, submitted.Proposals[0].ID)
	if err != nil || proposal.Reason != "多次部署失败都源于未跑完整测试" {
		t.Fatalf("proposal reason=%#v err=%v", proposal, err)
	}
	approved, entry, err := fixture.database.ApproveKnowledgeProposal(fixture.ctx, submitted.Proposals[0].ID, time.Now().UTC(), "已有失败证据")
	if err != nil {
		t.Fatal(err)
	}
	if approved.DecisionReason != "已有失败证据" {
		t.Fatalf("proposal decision reason=%#v", approved)
	}
	detail, err := fixture.service.PetKnowledgeDetail(fixture.ctx, entry.ID)
	if err != nil || !strings.Contains(detail.Body, "完整测试") {
		t.Fatalf("knowledge detail=%#v err=%v", detail, err)
	}
	knowledgeApproval, err := fixture.service.PetDeleteKnowledge(fixture.ctx, entry.ID, "重复副本需要清理原因")
	if err != nil || knowledgeApproval.Kind != "delete_knowledge" {
		t.Fatalf("delete knowledge approval=%#v err=%v", knowledgeApproval, err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, knowledgeApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Knowledge(fixture.ctx, entry.ID); err == nil {
		t.Fatal("approved knowledge deletion did not delete the entry")
	}

	// Skills are vault packages. Bind this newly-created project right before
	// the skill section so the earlier legacy knowledge proposal flow keeps its
	// legacy fixture.
	manager := buildVaultReadManager(t, true, map[string]string{
		"projects/knowledge/index.md": "---\nid: knowledge_root_project_knowledge\ntitle: 知识项目\nscope: project\nproject_id: " + project.ID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\n知识项目入口。\n",
	})
	fixture.service.SetKnowledgeVault(manager, true)
	fixture.service.SetKnowledgeVaultRuntimeWrite(true)

	skill, err := fixture.service.PetCreateSkill(fixture.ctx, PetSkillInput{
		ProjectID: project.ID, Name: "发布检查", Instructions: "部署前运行测试。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if skill.Version != 1 || !skill.Enabled || skill.Scope != "project" {
		t.Fatalf("skill=%#v", skill)
	}
	updated, err := fixture.service.PetUpdateSkill(fixture.ctx, PetSkillInput{
		SkillID: skill.ID, Name: "发布检查 v2", Instructions: "部署前运行测试并核对哈希。",
	})
	if err != nil || updated.Version != 2 || updated.Name != "发布检查 v2" {
		t.Fatalf("updated skill=%#v err=%v", updated, err)
	}
	// The pet reads a package's files before changing them, and an update that
	// sends files replaces the package.
	withFiles, err := fixture.service.PetUpdateSkill(fixture.ctx, PetSkillInput{
		SkillID: skill.ID, Files: []knowledgevault.SkillPackageFile{
			{Path: "SKILL.md", Content: "---\nname: 发布检查 v2\ndescription: 发布前检查\n---\n部署前运行测试并核对哈希。\n"},
			{Path: "scripts/check.sh", Content: "#!/bin/sh\necho check\n"},
		},
	})
	if err != nil || withFiles.Version != 3 {
		t.Fatalf("package update=%#v err=%v", withFiles, err)
	}
	read, err := fixture.service.PetSkillDetailWithFiles(fixture.ctx, skill.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, file := range read.FileContents {
		if file.Path == "scripts/check.sh" {
			script = file.Content
		}
	}
	if !strings.Contains(script, "echo check") || len(read.FilesOmitted) != 0 {
		t.Fatalf("package files were not readable: %#v", read)
	}
	if plain, _ := fixture.service.PetSkillDetail(fixture.ctx, skill.ID); len(plain.FileContents) != 0 {
		t.Fatal("file contents were returned without being asked for")
	}
	// A field-only update afterwards keeps the script.
	if _, err := fixture.service.PetUpdateSkill(fixture.ctx, PetSkillInput{SkillID: skill.ID, Description: "只改描述"}); err != nil {
		t.Fatal(err)
	}
	if kept, _ := fixture.service.PetSkillDetailWithFiles(fixture.ctx, skill.ID, true); len(kept.FileContents) != 2 || kept.Description != "只改描述" {
		t.Fatalf("a field-only update dropped package files: %#v", kept)
	}

	// The pet manages the whole Skill library, not only project Skills.
	global, err := fixture.service.PetCreateSkill(fixture.ctx, PetSkillInput{
		Scope: "global", Name: "通用评审", Instructions: "先读 diff 再下结论。",
	})
	if err != nil || global.Scope != "global" || global.ProjectID != "" {
		t.Fatalf("global skill=%#v err=%v", global, err)
	}
	globalUpdated, err := fixture.service.PetUpdateSkill(fixture.ctx, PetSkillInput{SkillID: global.ID, Instructions: "先读 diff，再跑测试。"})
	if err != nil || globalUpdated.Version != 2 || !strings.Contains(globalUpdated.Instructions, "再跑测试") {
		t.Fatalf("global skill update=%#v err=%v", globalUpdated, err)
	}
	if _, err := fixture.service.PetCreateSkill(fixture.ctx, PetSkillInput{Name: "缺项目", Instructions: "x"}); err == nil {
		t.Fatal("a project Skill was created without a project")
	}
	if _, err := fixture.service.PetCreateSkill(fixture.ctx, PetSkillInput{Scope: "archive", Name: "归档", Instructions: "x"}); err == nil {
		t.Fatal("an archive Skill was created")
	}
	globalApproval, err := fixture.service.PetDeleteSkill(fixture.ctx, global.ID, "全局技能删除也要走审批")
	if err != nil || globalApproval.Kind != "delete_skill" || !strings.Contains(globalApproval.Summary, "全局") {
		t.Fatalf("global delete approval=%#v err=%v", globalApproval, err)
	}
	if _, err := fixture.service.PetSkillDetail(fixture.ctx, global.ID); err != nil {
		t.Fatal("the global Skill was deleted before the Owner approved it")
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, globalApproval.ID, "approve", "确认"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetSkillDetail(fixture.ctx, global.ID); err == nil {
		t.Fatal("an approved global Skill deletion did not delete it")
	}

	approval, err := fixture.service.PetDeleteSkill(fixture.ctx, skill.ID, "测试删除技能需要原因")
	if err != nil || approval.Kind != "delete_skill" || approval.Status != petApprovalPending {
		t.Fatalf("delete skill approval=%#v err=%v", approval, err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "确认删除"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Skill(fixture.ctx, skill.ID); err == nil {
		t.Fatal("approved skill deletion did not delete the skill")
	}

	templates, err := fixture.service.PetListPromptTemplates(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var targetID string
	for _, item := range templates {
		if item.ID == "channel.web" {
			targetID = item.ID
		}
		if item.ID == "identity.pet-task" && !item.Protected {
			t.Fatal("pet identity prompt is not protected")
		}
	}
	if targetID == "" {
		t.Fatal("channel.web prompt template is missing")
	}
	if _, err := fixture.service.PetPromptTemplateDetail(fixture.ctx, targetID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetRequestPromptUpdate(fixture.ctx, "identity.pet-task", "越权修改", "测试保护提示词需要原因"); err == nil {
		t.Fatal("protected prompt update was accepted")
	}
	promptApproval, err := fixture.service.PetRequestPromptUpdate(fixture.ctx, targetID, "Web 渠道行为（测试覆盖）", "测试批准提示词需要原因")
	if err != nil || promptApproval.Kind != "update_prompt_template" {
		t.Fatalf("prompt approval=%#v err=%v", promptApproval, err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, promptApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	rawTemplates, err := fixture.service.PromptTemplates(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range rawTemplates {
		if item.ID == targetID && strings.Contains(item.Content, "测试覆盖") {
			found = true
		}
	}
	if !found {
		t.Fatal("approved prompt update was not written")
	}
	if err := fixture.service.ResetPromptTemplate(fixture.ctx, targetID); err != nil {
		t.Fatal(err)
	}
}

func TestPetGlobalKnowledgeSubmitReviseAndDelete(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowManageKnowledge: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.UpdateKnowledgeReviewSettings(fixture.ctx, domain.KnowledgeReviewSettings{AutoApprove: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	submitted, err := fixture.service.PetSubmitKnowledge(fixture.ctx, "global", "", []KnowledgeCandidate{{
		Type: "practice", Title: "全局知识测试条目", Body: "初始正文。", Confidence: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Entries) != 1 || submitted.Entries[0].Scope != "global" || submitted.Entries[0].Status != string(domain.KnowledgeVerified) {
		t.Fatalf("global submit=%#v", submitted)
	}
	entry, err := fixture.database.Knowledge(fixture.ctx, submitted.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := fixture.service.PetSubmitKnowledge(fixture.ctx, "global", "", []KnowledgeCandidate{{
		EntryID: entry.ID, BaseRevision: entry.Revision, Type: "practice",
		Title: entry.Title, Body: "修订后的正文。", Confidence: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(revised.Entries) != 1 || revised.Entries[0].Status != string(domain.KnowledgeVerified) || revised.Entries[0].Revision <= entry.Revision {
		t.Fatalf("global revision=%#v", revised)
	}
	if _, err := fixture.service.PetDeleteKnowledge(fixture.ctx, entry.ID, ""); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("missing reason was not rejected with the field name: %v", err)
	}
	approval, err := fixture.service.PetDeleteKnowledge(fixture.ctx, entry.ID, "重复副本需要清理原因")
	if err != nil || approval.Status != petApprovalPending || approval.Kind != "delete_knowledge" {
		t.Fatalf("global delete approval=%#v err=%v", approval, err)
	}
	if _, err := fixture.database.Knowledge(fixture.ctx, entry.ID); err != nil {
		t.Fatal("global entry was deleted before approval")
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Knowledge(fixture.ctx, entry.ID); err == nil {
		t.Fatal("approved global deletion did not delete the entry")
	}
}

func TestPetKnowledgeDeletePrunesEmptyAHA1Directories(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2, AllowManageKnowledge: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.EnsureKnowledgeRoots(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeEntry := func(id, parent, slug, title string, entryType string) domain.KnowledgeEntry {
		return domain.KnowledgeEntry{
			ID: id, Scope: "global", ParentID: parent, Slug: slug, Title: title, Body: title,
			Type: entryType, Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
	}
	outer := "knowledge_aha1_dir_test_outer"
	inner := "knowledge_aha1_dir_test_inner"
	child := "knowledge_aha1_test_child"
	for _, item := range []domain.KnowledgeEntry{
		makeEntry(outer, store.GlobalGeneralKnowledgeID, "aha1-test-outer", "AHA1 test outer", "navigation"),
		makeEntry(inner, outer, "aha1-test-inner", "AHA1 test inner", "navigation"),
		makeEntry(child, inner, "aha1-test-child", "AHA1 test child", "practice"),
	} {
		if err := fixture.database.CreateKnowledge(fixture.ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	approval, err := fixture.service.PetDeleteKnowledge(fixture.ctx, child, "AHA1 空目录清理原因")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{child, inner, outer} {
		if _, err := fixture.database.Knowledge(fixture.ctx, id); err == nil {
			t.Fatalf("empty AHA1 node %s was not pruned", id)
		}
	}
}

func setPetSkillTestPermissions(t *testing.T, fixture petSkillFixture, viewTasks, createTasks, manageKnowledge bool) {
	t.Helper()
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(viewTasks), AllowCreateTasks: boolPointer(createTasks),
		AllowCreateWorkspaces: boolPointer(false), AllowMessageTasks: boolPointer(false),
		AllowManageModels: boolPointer(false), AllowManageKnowledge: boolPointer(manageKnowledge),
		AllowManageChannels: boolPointer(false), AllowManageSystem: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
}

func petSkillsFromMCP(t *testing.T, fixture petSkillFixture) []PetSkillBrief {
	t.Helper()
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_list_skills", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("aha_list_skills: %v", err)
	}
	var skills []PetSkillBrief
	if err := json.Unmarshal([]byte(raw), &skills); err != nil {
		t.Fatal(err)
	}
	return skills
}

func TestPetListSkillsAllowedWithCreateTasksOnly(t *testing.T) {
	fixture := newPetSkillFixture(t)
	setPetSkillTestPermissions(t, fixture, false, true, false)

	skills := petSkillsFromMCP(t, fixture)
	found := false
	for _, skill := range skills {
		if skill.ID == fixture.skillKey("project") {
			found = true
		}
	}
	if !found {
		t.Fatalf("create_tasks-only pet cannot see project Skill IDs: %#v", skills)
	}
	if _, err := fixture.service.PetListSkills(fixture.ctx, "project", fixture.project.ID, 10); err != nil {
		t.Fatalf("service-level Skill listing rejected create_tasks-only pet: %v", err)
	}

	createArgs, _ := json.Marshal(map[string]any{
		"project_id": fixture.project.ID, "name": "越权 Skill", "instructions": "不应创建",
	})
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_skill", createArgs); err == nil ||
		!strings.Contains(err.Error(), "未开启该工具权限") {
		t.Fatalf("aha_create_skill was not permission-gated: %v", err)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_skill",
		json.RawMessage(`{"skill_id":"`+fixture.skillKey("project")+`","name":"越权修改"}`)); err == nil ||
		!strings.Contains(err.Error(), "未开启该工具权限") {
		t.Fatalf("aha_update_skill was not permission-gated: %v", err)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_skill_detail",
		json.RawMessage(`{"skill_id":"`+fixture.skillKey("project")+`"}`)); err == nil ||
		!strings.Contains(err.Error(), "未开启该工具权限") {
		t.Fatalf("aha_skill_detail unexpectedly widened: %v", err)
	}
	items, err := fixture.database.ListSkills(fixture.ctx, "project", fixture.project.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == "越权 Skill" {
			t.Fatal("rejected aha_create_skill still created a Skill")
		}
	}
}

func TestPetListSkillsDeniedWithoutRelatedPermission(t *testing.T) {
	fixture := newPetSkillFixture(t)
	setPetSkillTestPermissions(t, fixture, false, false, false)

	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_list_skills", json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "未开启该工具权限") {
		t.Fatalf("aha_list_skills was not permission-gated: %v", err)
	}
	if _, err := fixture.service.PetListSkills(fixture.ctx, "project", fixture.project.ID, 10); err == nil ||
		!strings.Contains(err.Error(), "Skill") {
		t.Fatalf("service-level Skill listing was not permission-gated: %v", err)
	}
}

func TestPetListSkillsKeepsViewAndManagePermission(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		viewTasks       bool
		manageKnowledge bool
	}{
		{name: "view_tasks", viewTasks: true},
		{name: "manage_knowledge", manageKnowledge: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newPetSkillFixture(t)
			setPetSkillTestPermissions(t, fixture, testCase.viewTasks, false, testCase.manageKnowledge)

			if skills := petSkillsFromMCP(t, fixture); len(skills) == 0 {
				t.Fatal("Skill listing returned no Skills")
			}
			if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_skill_detail",
				json.RawMessage(`{"skill_id":"`+fixture.skillKey("project")+`"}`)); err != nil {
				t.Fatalf("aha_skill_detail regressed: %v", err)
			}
		})
	}
}

// The runtime Skill list is vault-only: a row that still exists in the legacy
// skills table is never listed, even when no vault manager is configured.
func TestPetListSkillsNeverReadsLegacyTable(t *testing.T) {
	fixture := newPetSkillFixture(t)
	if err := fixture.database.CreateSkill(fixture.ctx, domain.Skill{
		ID: "skill-legacy-list", Scope: "global", Name: "Legacy Only",
		Instructions: "legacy", Version: 1, Status: "active", Enabled: true,
		CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := fixture.service.PetListSkills(fixture.ctx, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("vault Skills were not listed")
	}
	for _, item := range items {
		if item.ID == "skill-legacy-list" || item.Name == "Legacy Only" {
			t.Fatalf("legacy skill leaked into the runtime list: %#v", item)
		}
	}
	fixture.service.knowledgeVault = nil
	items, err = fixture.service.PetListSkills(fixture.ctx, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("a nil vault fell back to the legacy skills table: %#v", items)
	}
}
