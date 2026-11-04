package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

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
	reviewModel := fixture.model
	reviewModel.ID = "model-review-senior"
	reviewModel.DisplayName = "Senior Review"
	reviewModel.WireModel = "review"
	if err := fixture.database.UpsertModel(fixture.ctx, reviewModel); err != nil {
		t.Fatal(err)
	}
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
	agents, err := fixture.service.TaskAgents(fixture.ctx, review.ID)
	if err != nil || len(agents) == 0 || agents[0].Filesystem != "read-only" || agents[0].ReasoningEffort != "high" || agents[0].ModelID != reviewModel.ID {
		t.Fatalf("review runtime=%#v err=%v", agents, err)
	}
	watch, err := fixture.database.PetTaskWatch(fixture.ctx, review.ID)
	if err != nil || watch.Reason != "review" {
		t.Fatalf("review watch=%#v err=%v", watch, err)
	}
	if !strings.Contains(review.Title, "代码评审") {
		t.Fatalf("review title=%q", review.Title)
	}
}

func TestPetCompleteTaskRequiresApprovedSeniorReview(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
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

	review, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "代码评审",
		Request: "只读评审", ModelID: fixture.model.ID, ReasoningEffort: "high",
		Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: project.KnowledgePolicy, Isolation: "inplace", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, review.ID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("review agents err=%v", err)
	}
	now := fixture.service.now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: review.ID, Role: "user", Sender: "pet",
		Content: "完成评审", CreatedAt: now,
	}
	if err := fixture.database.AddMessage(fixture.ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateTurn(fixture.ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: review.ID, AgentID: "main", Sequence: 1,
		InputMessageID: message.ID, Status: domain.TurnSucceeded,
		RuntimeConfigSnapshotID: agents[0].RuntimeConfigSnapshotID,
		Result:                  "Verdict: APPROVE\n\n检查通过。", QueuedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]domain.TaskStatus{
		{domain.TaskDraft, domain.TaskPreparing},
		{domain.TaskPreparing, domain.TaskActive},
		{domain.TaskActive, domain.TaskWaitingUser},
		{domain.TaskWaitingUser, domain.TaskCompleted},
	} {
		if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, transition[0], transition[1], timeString(now), ""); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := fixture.service.PetCompleteTaskWithReview(fixture.ctx, source.ID, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.TaskCompleted {
		t.Fatalf("completed status=%s", completed.Status)
	}
}
