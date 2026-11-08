package app

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func createReviewSkill(t *testing.T, fixture *petTaskFixture, id, name, scope, projectID string, enabled bool) {
	t.Helper()
	now := fixture.createdAt
	if err := fixture.database.CreateSkill(fixture.ctx, domain.Skill{
		ID: id, Scope: scope, ProjectID: projectID, Name: name, Instructions: name,
		Version: 1, Status: "active", Enabled: enabled, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func addReviewModel(t *testing.T, fixture *petTaskFixture) domain.Model {
	t.Helper()
	reviewModel := fixture.model
	reviewModel.ID = "model-review-senior"
	reviewModel.DisplayName = "Senior Review"
	reviewModel.WireModel = "review"
	if err := fixture.database.UpsertModel(fixture.ctx, reviewModel); err != nil {
		t.Fatal(err)
	}
	return reviewModel
}

func assertReviewFirstTurnRuntime(t *testing.T, fixture *petTaskFixture, taskID, filesystem, effort, approval string) {
	t.Helper()
	turn, err := fixture.database.LatestTurnForAgent(fixture.ctx, taskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, turn.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	gotFilesystem, gotApproval := parsePermissionsJSON(snapshot.PermissionsJSON)
	if snapshot.ReasoningEffort != effort || gotFilesystem != filesystem || gotApproval != approval {
		t.Fatalf("first turn runtime reasoning=%q filesystem=%q approval=%q, want %q/%q/%q",
			snapshot.ReasoningEffort, gotFilesystem, gotApproval, effort, filesystem, approval)
	}
}

func petReviewFixture(t *testing.T) (*petTaskFixture, domain.Project, domain.Workspace) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "代码评审项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "代码评审 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, project, workspace
}

func TestPetCreateTaskDefaultsToHighFullAccess(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "默认运行参数",
		Request: "默认高推理和完全访问", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, task.ID)
	if err != nil || len(agents) == 0 || agents[0].ReasoningEffort != "high" || agents[0].Filesystem != "danger-full-access" {
		t.Fatalf("default pet task runtime=%#v err=%v", agents, err)
	}
	override, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "按难度降级",
		Request: "只读低推理", Start: boolPointer(false),
		ReasoningEffort: "low", Filesystem: "read-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	agents, err = fixture.service.TaskAgents(fixture.ctx, override.ID)
	if err != nil || len(agents) == 0 || agents[0].ReasoningEffort != "low" || agents[0].Filesystem != "read-only" {
		t.Fatalf("overridden pet task runtime=%#v err=%v", agents, err)
	}
}

func TestPetRequestCodeReviewCreatesReadOnlyHighTask(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	createReviewSkill(t, fixture, "skill-review-tiered", petCodeReviewTieredSkillName, "project", project.ID, true)
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "待评审代码",
		Request: "修改代码", ModelID: fixture.model.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: reviewModel.ID, Focus: "检查错误处理",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Warnings) != 0 {
		t.Fatalf("unexpected warnings=%#v", review.Warnings)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, review.ID)
	if err != nil || len(agents) == 0 || agents[0].Filesystem != "read-only" || agents[0].ReasoningEffort != "high" || agents[0].ModelID != reviewModel.ID {
		t.Fatalf("review runtime=%#v err=%v", agents, err)
	}
	if want := []string{"skill-review-tiered"}; !reflect.DeepEqual(review.SkillIDs, want) {
		t.Fatalf("review skill_ids=%#v want %#v", review.SkillIDs, want)
	}
	waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
	assertReviewFirstTurnRuntime(t, fixture, review.ID, "read-only", "high", "never")
	watch, err := fixture.database.PetTaskWatch(fixture.ctx, review.ID)
	if err != nil || watch.Reason != "review" {
		t.Fatalf("review watch=%#v err=%v", watch, err)
	}
	if !strings.Contains(review.Title, "代码评审") {
		t.Fatalf("review title=%q", review.Title)
	}
}

// Requested run settings must be part of CreateTask: the first Turn must
// already use them instead of relying on a later UpdateTaskAgent/UpdateTask.
func TestPetRequestCodeReviewAppliesRunConfigAtCreation(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	createReviewSkill(t, fixture, "skill-review-extra", "Extra Review", "project", project.ID, true)
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "需要构建证据的代码",
		Request: "修改代码并跑测试", ModelID: fixture.model.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: reviewModel.ID,
		Filesystem: "danger-full-access", ReasoningEffort: "xhigh", SkillIDs: []string{"skill-review-extra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Warnings) != 0 {
		t.Fatalf("unexpected warnings=%#v", review.Warnings)
	}
	if want := []string{"skill-review-extra"}; !reflect.DeepEqual(review.SkillIDs, want) {
		t.Fatalf("review skill_ids=%#v want %#v", review.SkillIDs, want)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, review.ID)
	if err != nil || len(agents) == 0 || agents[0].Filesystem != "danger-full-access" || agents[0].ReasoningEffort != "xhigh" {
		t.Fatalf("review runtime=%#v err=%v", agents, err)
	}
	waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
	assertReviewFirstTurnRuntime(t, fixture, review.ID, "danger-full-access", "xhigh", "never")
}

// An empty skill_ids uses the project's tiered-code-review Skill. A project
// Skill wins over a global one with the same name.
func TestPetRequestCodeReviewAutoAttachesTieredSkill(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	createReviewSkill(t, fixture, "skill-review-global", petCodeReviewTieredSkillName, "global", "", true)
	createReviewSkill(t, fixture, "skill-review-project", petCodeReviewTieredSkillName, "project", project.ID, true)
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "自动 Skill",
		Request: "修改代码", ModelID: fixture.model.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: reviewModel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"skill-review-project"}; !reflect.DeepEqual(review.SkillIDs, want) {
		t.Fatalf("review skill_ids=%#v want %#v", review.SkillIDs, want)
	}
	if len(review.Warnings) != 0 {
		t.Fatalf("unexpected warnings=%#v", review.Warnings)
	}
}

// A missing auto-attach Skill must not be fabricated: the Review Task is still
// created and the result explains why the Skill was not attached.
func TestPetRequestCodeReviewWarnsWhenTieredSkillMissing(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	if err := fixture.database.CreateSkill(fixture.ctx, domain.Skill{
		ID: "skill-review-bound-mirror", Scope: "project", ProjectID: "other-project", BoundProjectID: project.ID,
		Name: petCodeReviewTieredSkillName, Instructions: "镜像", Version: 1, Status: "active", Enabled: true,
		CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "缺少评审 Skill",
		Request: "修改代码", ModelID: fixture.model.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: reviewModel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.SkillIDs) != 0 {
		t.Fatalf("missing tiered Skill was fabricated: %#v", review.SkillIDs)
	}
	if len(review.Warnings) != 1 || !strings.Contains(review.Warnings[0], petCodeReviewTieredSkillName) {
		t.Fatalf("warnings=%#v", review.Warnings)
	}
}

// The MCP schema and JSON parsing must expose the creation-time run settings.
func TestPetMCPRequestCodeReviewAcceptsRunConfig(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	createReviewSkill(t, fixture, "skill-review-extra", "Extra Review", "project", project.ID, true)

	checked := false
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		if tool.Name != "aha_request_code_review" {
			continue
		}
		checked = true
		properties, _ := tool.Parameters["properties"].(map[string]any)
		for _, name := range []string{"filesystem", "reasoning_effort", "skill_ids"} {
			if _, ok := properties[name].(map[string]any); !ok {
				t.Fatalf("%s does not advertise %s: %#v", tool.Name, name, tool)
			}
		}
		if required, _ := tool.Parameters["required"].([]string); slices.Contains(required, "filesystem") ||
			slices.Contains(required, "reasoning_effort") || slices.Contains(required, "skill_ids") {
			t.Fatalf("%s makes run settings required: %#v", tool.Name, tool.Parameters["required"])
		}
	}
	if !checked {
		t.Fatal("aha_request_code_review not found in the MCP tool list")
	}

	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "MCP 评审",
		Request: "修改代码", ModelID: fixture.model.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"task_id": source.ID, "review_model_id": reviewModel.ID,
		"filesystem": "workspace-write", "reasoning_effort": "high",
		"skill_ids": []string{"skill-review-extra"},
	})
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_request_code_review", args)
	if err != nil {
		t.Fatal(err)
	}
	var review PetCodeReviewResult
	if err := json.Unmarshal([]byte(raw), &review); err != nil {
		t.Fatal(err)
	}
	if review.ID == "" || !reflect.DeepEqual(review.SkillIDs, []string{"skill-review-extra"}) {
		t.Fatalf("mcp review=%#v", review)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, review.ID)
	if err != nil || len(agents) == 0 || agents[0].Filesystem != "workspace-write" || agents[0].ReasoningEffort != "high" {
		t.Fatalf("mcp review runtime=%#v err=%v", agents, err)
	}
}

// Completion accepts a review whose runtime was configured at creation time,
// not only the legacy read-only/high pair, while still requiring approval=never
// and inplace isolation.
func TestPetCompleteTaskRequiresApprovedSeniorReview(t *testing.T) {
	for _, tc := range []struct {
		name       string
		filesystem string
		effort     string
	}{
		{name: "default runtime", filesystem: "read-only", effort: "high"},
		{name: "configured runtime", filesystem: "workspace-write", effort: "xhigh"},
		{name: "full access", filesystem: "danger-full-access", effort: "max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			completeTaskWithReview(t, tc.filesystem, tc.effort)
		})
	}
}

func TestPetCompleteTaskRejectsWeakOrSameModelReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		effort    string
		modelSame bool
		approval  string
		isolation string
		want      string
	}{
		{name: "weak reasoning", effort: "low", want: "high"},
		{name: "same model", effort: "high", modelSame: true, want: "不同的模型"},
		{name: "approval", effort: "high", approval: "auto", want: "never"},
		{name: "isolation", effort: "high", isolation: "worktree", want: "inplace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, project, workspace := petReviewFixture(t)
			reviewModel := addReviewModel(t, fixture)
			if tc.isolation != "" {
				project.ProjectType = "git"
				if err := fixture.database.UpdateProject(fixture.ctx, project); err != nil {
					t.Fatal(err)
				}
			}
			source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "需要强评审的代码任务",
				Request: "改动代码", Isolation: "inplace", Start: boolPointer(false),
			})
			if err != nil {
				t.Fatal(err)
			}
			modelID := reviewModel.ID
			if tc.modelSame {
				modelID = fixture.model.ID
			}
			approval, isolation := tc.approval, tc.isolation
			if approval == "" {
				approval = "never"
			}
			if isolation == "" {
				isolation = "inplace"
			}
			review, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "代码评审",
				Request: "只读评审", ModelID: modelID, ReasoningEffort: tc.effort,
				Filesystem: "read-only", Approval: approval, CollaborationMode: "single", MaxAgents: 1,
				KnowledgePolicy: project.KnowledgePolicy, Isolation: isolation, StartMode: "manual",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.database.CreatePetCodeReviewLink(fixture.ctx, review.ID, source.ID); err != nil {
				t.Fatal(err)
			}
			agents, err := fixture.service.TaskAgents(fixture.ctx, review.ID)
			if err != nil || len(agents) == 0 {
				t.Fatalf("review agents err=%v", err)
			}
			now := fixture.service.now().UTC()
			message := domain.Message{ID: domain.NewID("message"), TaskID: review.ID, Role: "user", Sender: "pet", Content: "完成评审", CreatedAt: now}
			if err := fixture.database.AddMessage(fixture.ctx, message); err != nil {
				t.Fatal(err)
			}
			if err := fixture.database.CreateTurn(fixture.ctx, domain.Turn{
				ID: domain.NewID("turn"), TaskID: review.ID, AgentID: "main", Sequence: 1,
				InputMessageID: message.ID, Status: domain.TurnSucceeded, RuntimeConfigSnapshotID: agents[0].RuntimeConfigSnapshotID,
				Result: "Verdict: APPROVE", QueuedAt: now, FinishedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			for _, transition := range [][2]domain.TaskStatus{
				{domain.TaskDraft, domain.TaskPreparing}, {domain.TaskPreparing, domain.TaskActive},
				{domain.TaskActive, domain.TaskWaitingUser}, {domain.TaskWaitingUser, domain.TaskCompleted},
			} {
				if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, transition[0], transition[1], timeString(now), ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := fixture.service.requirePetCodeReview(fixture.ctx, source, review.ID); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("weak review accepted or wrong error: %v", err)
			}
		})
	}
}

func TestPetRequestCodeReviewRejectsInvalidRunConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input PetRequestCodeReviewInput
		want  string
	}{
		{name: "filesystem", input: PetRequestCodeReviewInput{Filesystem: "invalid"}, want: "filesystem"},
		{name: "reasoning", input: PetRequestCodeReviewInput{ReasoningEffort: "low"}, want: "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, project, workspace := petReviewFixture(t)
			reviewModel := addReviewModel(t, fixture)
			source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "参数校验", Request: "改动代码", Start: boolPointer(false)})
			if err != nil {
				t.Fatal(err)
			}
			tc.input.TaskID, tc.input.ReviewModelID = source.ID, reviewModel.ID
			if _, err := fixture.service.PetRequestCodeReview(fixture.ctx, tc.input); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid config accepted or wrong error: %v", err)
			}
		})
	}
}

func completeTaskWithReview(t *testing.T, filesystem, effort string) {
	t.Helper()
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "需要评审的代码任务",
		Request: "改动代码", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
	if _, err := fixture.service.PetCompleteTask(fixture.ctx, source.ID); err == nil || !strings.Contains(err.Error(), "review_task_id") {
		t.Fatalf("write task completed without review: %v", err)
	}

	fixture.service.executor = &petReviewExecutor{reply: "Verdict: APPROVE\n\n检查通过。"}
	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: reviewModel.ID, ReasoningEffort: effort, Filesystem: filesystem,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
	assertReviewFirstTurnRuntime(t, fixture, review.ID, filesystem, effort, "never")
	if _, err := fixture.service.PetCompleteTaskWithReview(fixture.ctx, source.ID, review.ID); err == nil || !strings.Contains(err.Error(), "尚未完成") {
		t.Fatalf("unfinished review accepted: %v", err)
	}
	if completed, err := fixture.service.PetCompleteTask(fixture.ctx, review.ID); err != nil || completed.Status != domain.TaskCompleted {
		t.Fatalf("review completion recursed: status=%s err=%v", completed.Status, err)
	}
	completed, err := fixture.service.PetCompleteTaskWithReview(fixture.ctx, source.ID, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.TaskCompleted {
		t.Fatalf("completed status=%s", completed.Status)
	}
}
