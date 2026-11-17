package app

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type petSkillFixture struct {
	*petTaskFixture
	project   domain.Project
	workspace domain.Workspace
	keys      map[string]string
}

func (f petSkillFixture) skillKey(name string) string {
	return f.keys[name]
}

func newPetSkillFixture(t *testing.T) petSkillFixture {
	t.Helper()
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "Skill 项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "Skill Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Skills are vault packages now. The fixture binds the pet project and one
	// foreign project, then sets only the project "auto" package as
	// auto-attach: legacy AutoAttach rows no longer feed task selection.
	files := map[string]string{
		"projects/skill-project/index.md":                       "---\nid: knowledge_root_skill_project\ntitle: Skill 项目\nscope: project\nproject_id: " + project.ID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\nSkill 项目入口。\n",
		"projects/skill-project/skills/project-manual/SKILL.md": "---\nname: Project Manual\ndescription: project manual\n---\n\nproject manual\n",
		"projects/skill-project/skills/project-auto/SKILL.md":   "---\nname: Project Auto\ndescription: project auto\n---\n\nproject auto\n",
		"global/skills/global-manual/SKILL.md":                  "---\nname: Global Manual\ndescription: global manual\n---\n\nglobal manual\n",
		"projects/other/index.md":                               "---\nid: knowledge_root_other\ntitle: Other Project\nscope: project\nproject_id: project-other\ntype: navigation\nstatus: verified\nis_index: true\n---\nOther.\n",
		"projects/other/skills/other-project/SKILL.md":          "---\nname: Other Project\ndescription: other\n---\n\nother\n",
	}
	manager := buildVaultReadManager(t, true, files)
	fixture.service.SetKnowledgeVault(manager, true)
	fixture.service.SetKnowledgeVaultRuntimeWrite(true)
	keys := map[string]string{
		"global":  "vault-skill:global:global-manual",
		"project": "vault-skill:project:" + project.ID + ":project-manual",
		"auto":    "vault-skill:project:" + project.ID + ":project-auto",
		"other":   "vault-skill:project:project-other:other-project",
	}
	if _, err := fixture.service.SetVaultSkillAutoAttach(fixture.ctx, keys["auto"], true, false); err != nil {
		t.Fatal(err)
	}
	return petSkillFixture{petTaskFixture: fixture, project: project, workspace: workspace, keys: keys}
}

func (f petSkillFixture) storedSkillIDs(t *testing.T, taskID string) []string {
	t.Helper()
	task, err := f.database.Task(f.ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task.SkillIDs
}

// Requested Skills are kept in order and the project's auto-attach Skills are
// merged after them, the same default CreateTask applies to internal callers.
func TestPetCreateTaskPersistsRequestedSkillsBeforeFirstTurn(t *testing.T) {
	fixture := newPetSkillFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "带 Skill 的任务",
		Request: "按 Skill 执行", Filesystem: "read-only", Start: boolPointer(true),
		SkillIDs: []string{fixture.skillKey("global"), " " + fixture.skillKey("project") + " ", fixture.skillKey("global")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fixture.skillKey("global"), fixture.skillKey("project"), fixture.skillKey("auto")}
	if !reflect.DeepEqual(task.SkillIDs, want) {
		t.Fatalf("returned skill_ids=%#v want %#v", task.SkillIDs, want)
	}
	waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
	if got := fixture.storedSkillIDs(t, task.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored skill_ids=%#v want %#v", got, want)
	}
	// The first Turn's prompt is built from the Task's Skills, so the selection
	// must already be in place when the pet starts the Task.
	prompts, _ := fixture.service.executor.(*stubExecutor).requestHistory()
	var first string
	for _, item := range prompts {
		if strings.Contains(item, "按 Skill 执行") {
			first = item
			break
		}
	}
	if first == "" {
		t.Fatalf("first turn prompt not found in %d prompts", len(prompts))
	}
	for _, name := range []string{"Global Manual", "Project Manual", "Project Auto"} {
		if !strings.Contains(first, name) {
			t.Fatalf("first turn prompt does not reference Skill %q", name)
		}
	}
}

func TestPetCreateTaskWithoutSkillIDsKeepsAutoAttachDefault(t *testing.T) {
	fixture := newPetSkillFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "无 Skill 参数",
		Request: "保持兼容", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{fixture.skillKey("auto")}; !reflect.DeepEqual(task.SkillIDs, want) {
		t.Fatalf("skill_ids=%#v want %#v", task.SkillIDs, want)
	}
}

func TestPetCreateTaskRejectsUnavailableSkills(t *testing.T) {
	fixture := newPetSkillFixture(t)
	for _, id := range []string{fixture.skillKey("other"), "skill-missing"} {
		before, err := fixture.database.ListTasks(fixture.ctx, fixture.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
			ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "非法 Skill",
			Request: "应被拒绝", Start: boolPointer(false), SkillIDs: []string{fixture.skillKey("project"), id},
		})
		if err == nil || !strings.Contains(err.Error(), id) {
			t.Fatalf("skill %s: err=%v", id, err)
		}
		after, err := fixture.database.ListTasks(fixture.ctx, fixture.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Fatalf("skill %s: a task was created despite the rejection", id)
		}
	}
}

func TestPetUpdateTaskSkillsOnly(t *testing.T) {
	fixture := newPetSkillFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "更新 Skill",
		Request: "原始目标", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	skills := []string{fixture.skillKey("global"), fixture.skillKey("project")}
	updated, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{TaskID: task.ID, SkillIDs: &skills})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.SkillIDs, skills) {
		t.Fatalf("skill_ids=%#v want %#v", updated.SkillIDs, skills)
	}
	if updated.Title != task.Title || updated.CurrentGoal != task.CurrentGoal ||
		updated.CollaborationMode != task.CollaborationMode || updated.MaxAgents != task.MaxAgents ||
		updated.Status != task.Status {
		t.Fatalf("skill-only update changed other fields: before=%#v after=%#v", task, updated)
	}

	title := "不应写入的标题"
	invalid := []string{fixture.skillKey("global"), fixture.skillKey("other")}
	if _, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{
		TaskID: task.ID, Title: &title, SkillIDs: &invalid,
	}); err == nil || !strings.Contains(err.Error(), fixture.skillKey("other")) {
		t.Fatalf("cross-project skill update err=%v", err)
	}
	current, err := fixture.database.Task(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current.SkillIDs, skills) || current.Title != task.Title {
		t.Fatalf("rejected update modified the task: %#v", current)
	}

	cleared, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{TaskID: task.ID, SkillIDs: &[]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.SkillIDs) != 0 {
		t.Fatalf("empty skill_ids did not clear the selection: %#v", cleared.SkillIDs)
	}
}

func TestPetMCPTaskToolsAcceptSkillIDs(t *testing.T) {
	fixture := newPetSkillFixture(t)
	checked := 0
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		if tool.Name != "aha_create_task" && tool.Name != "aha_update_task" {
			continue
		}
		checked++
		properties, _ := tool.Parameters["properties"].(map[string]any)
		schema, _ := properties["skill_ids"].(map[string]any)
		if schema["type"] != "array" || !strings.Contains(tool.Description, "Skill") {
			t.Fatalf("%s does not advertise skill_ids: %#v", tool.Name, tool)
		}
		if required, _ := tool.Parameters["required"].([]string); slices.Contains(required, "skill_ids") {
			t.Fatalf("%s makes skill_ids required", tool.Name)
		}
	}
	if checked != 2 {
		t.Fatalf("expected aha_create_task and aha_update_task in the tool list, checked %d", checked)
	}

	args, _ := json.Marshal(map[string]any{
		"project_id": fixture.project.ID, "workspace_id": fixture.workspace.ID, "title": "MCP Skill",
		"request": "通过 MCP 创建", "start": false, "skill_ids": []string{fixture.skillKey("global")},
	})
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_task", args)
	if err != nil {
		t.Fatal(err)
	}
	var created domain.Task
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		t.Fatal(err)
	}
	if want := []string{fixture.skillKey("global"), fixture.skillKey("auto")}; !reflect.DeepEqual(created.SkillIDs, want) {
		t.Fatalf("mcp create skill_ids=%#v want %#v", created.SkillIDs, want)
	}

	raw, err = fixture.service.PetMCPTool(fixture.ctx, "aha_update_task",
		json.RawMessage(`{"task_id":"`+created.ID+`","skill_ids":["`+fixture.skillKey("project")+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated domain.Task
	if err := json.Unmarshal([]byte(raw), &updated); err != nil {
		t.Fatal(err)
	}
	if want := []string{fixture.skillKey("project")}; !reflect.DeepEqual(updated.SkillIDs, want) || updated.Title != created.Title {
		t.Fatalf("mcp update task=%#v", updated)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_task",
		json.RawMessage(`{"task_id":"`+created.ID+`","skill_ids":["skill-missing"]}`)); err == nil {
		t.Fatal("mcp update accepted an unavailable skill")
	}

	// Existing calls without skill_ids keep working; a null value is "not provided".
	title := "仅改标题"
	raw, err = fixture.service.PetMCPTool(fixture.ctx, "aha_update_task",
		json.RawMessage(`{"task_id":"`+created.ID+`","title":"`+title+`","skill_ids":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || !reflect.DeepEqual(updated.SkillIDs, []string{fixture.skillKey("project")}) {
		t.Fatalf("title-only update task=%#v", updated)
	}
}

func TestPetTaskSkillToolsRequireCreateTaskPermission(t *testing.T) {
	fixture := newPetSkillFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "权限",
		Request: "权限边界", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(false),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	skills := []string{fixture.skillKey("global")}
	if _, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{TaskID: task.ID, SkillIDs: &skills}); err == nil {
		t.Fatal("skill update was allowed without the create_tasks permission")
	}
	if _, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "无权限",
		Request: "应被拒绝", Start: boolPointer(false), SkillIDs: skills,
	}); err == nil {
		t.Fatal("create with skills was allowed without the create_tasks permission")
	}
	if got := fixture.storedSkillIDs(t, task.ID); !reflect.DeepEqual(got, []string{fixture.skillKey("auto")}) {
		t.Fatalf("skills changed without permission: %#v", got)
	}
}
