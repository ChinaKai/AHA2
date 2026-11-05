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
	now := fixture.createdAt
	for _, item := range []domain.Skill{
		{ID: "skill-pet-project", Scope: "project", ProjectID: project.ID, Name: "Project Manual", Instructions: "project manual", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-pet-global", Scope: "global", Name: "Global Manual", Instructions: "global manual", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-pet-auto", Scope: "project", ProjectID: project.ID, Name: "Project Auto", Instructions: "project auto", Version: 1, Status: "active", Enabled: true, AutoAttach: true, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-pet-other", Scope: "project", ProjectID: "project-other", Name: "Other Project", Instructions: "other", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := fixture.database.CreateSkill(fixture.ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	return petSkillFixture{petTaskFixture: fixture, project: project, workspace: workspace}
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
		SkillIDs: []string{"skill-pet-global", " skill-pet-project ", "skill-pet-global"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"skill-pet-global", "skill-pet-project", "skill-pet-auto"}
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
	if want := []string{"skill-pet-auto"}; !reflect.DeepEqual(task.SkillIDs, want) {
		t.Fatalf("skill_ids=%#v want %#v", task.SkillIDs, want)
	}
}

func TestPetCreateTaskRejectsUnavailableSkills(t *testing.T) {
	fixture := newPetSkillFixture(t)
	for _, id := range []string{"skill-pet-other", "skill-missing"} {
		before, err := fixture.database.ListTasks(fixture.ctx, fixture.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
			ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "非法 Skill",
			Request: "应被拒绝", Start: boolPointer(false), SkillIDs: []string{"skill-pet-project", id},
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
	skills := []string{"skill-pet-global", "skill-pet-project"}
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
	invalid := []string{"skill-pet-global", "skill-pet-other"}
	if _, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{
		TaskID: task.ID, Title: &title, SkillIDs: &invalid,
	}); err == nil || !strings.Contains(err.Error(), "skill-pet-other") {
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
		"request": "通过 MCP 创建", "start": false, "skill_ids": []string{"skill-pet-global"},
	})
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_task", args)
	if err != nil {
		t.Fatal(err)
	}
	var created domain.Task
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		t.Fatal(err)
	}
	if want := []string{"skill-pet-global", "skill-pet-auto"}; !reflect.DeepEqual(created.SkillIDs, want) {
		t.Fatalf("mcp create skill_ids=%#v want %#v", created.SkillIDs, want)
	}

	raw, err = fixture.service.PetMCPTool(fixture.ctx, "aha_update_task",
		json.RawMessage(`{"task_id":"`+created.ID+`","skill_ids":["skill-pet-project"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated domain.Task
	if err := json.Unmarshal([]byte(raw), &updated); err != nil {
		t.Fatal(err)
	}
	if want := []string{"skill-pet-project"}; !reflect.DeepEqual(updated.SkillIDs, want) || updated.Title != created.Title {
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
	if updated.Title != title || !reflect.DeepEqual(updated.SkillIDs, []string{"skill-pet-project"}) {
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
	skills := []string{"skill-pet-global"}
	if _, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{TaskID: task.ID, SkillIDs: &skills}); err == nil {
		t.Fatal("skill update was allowed without the create_tasks permission")
	}
	if _, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "无权限",
		Request: "应被拒绝", Start: boolPointer(false), SkillIDs: skills,
	}); err == nil {
		t.Fatal("create with skills was allowed without the create_tasks permission")
	}
	if got := fixture.storedSkillIDs(t, task.ID); !reflect.DeepEqual(got, []string{"skill-pet-auto"}) {
		t.Fatalf("skills changed without permission: %#v", got)
	}
}
