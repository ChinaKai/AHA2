package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type petDeskFixture struct {
	*petTaskFixture
	t         *testing.T
	project   domain.Project
	workspace domain.Workspace
	env       domain.EnvGroup
	now       time.Time
	sequence  int
}

func newPetDeskFixture(t *testing.T) *petDeskFixture {
	t.Helper()
	base := newPetTaskFixture(t)
	if _, err := base.service.OwnerAssistant(base.ctx); err != nil {
		t.Fatal(err)
	}
	now := base.createdAt
	base.service.now = func() time.Time { return now }
	fixture := &petDeskFixture{petTaskFixture: base, t: t, now: now}
	fixture.project = domain.Project{ID: "project-desk", Name: "Desk", CreatedAt: now, UpdatedAt: now}
	fixture.workspace = domain.Workspace{
		ID: "workspace-desk", ProjectID: fixture.project.ID, Name: "desk", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	fixture.env = domain.EnvGroup{
		ID: "env-desk", Name: "desk", ProviderID: base.provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return base.database.CreateProject(base.ctx, fixture.project) },
		func() error { return base.database.CreateWorkspace(base.ctx, fixture.workspace) },
		func() error { return base.database.UpsertEnvGroup(base.ctx, fixture.env) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

// task creates a Task whose UpdatedAt is `age` before now, so ordering by how
// long something has waited is deterministic.
func (fixture *petDeskFixture) task(id string, status domain.TaskStatus, age time.Duration) domain.Task {
	fixture.t.Helper()
	at := fixture.now.Add(-age)
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "runtime-" + id, WorkspaceID: fixture.workspace.ID, Backend: "codex",
		ModelID: fixture.model.ID, WireModel: "pet", EnvGroupID: fixture.env.ID, EnvGroupRevision: 1, CreatedAt: at,
	}
	task := domain.Task{
		ID: id, ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "title " + id, CurrentGoal: id,
		Status: status, RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: at, UpdatedAt: at,
	}
	created, err := fixture.database.CreateTaskWithSnapshot(fixture.ctx, snapshot, task)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return created
}

func (fixture *petDeskFixture) card(taskID, status string, nextSteps, openQuestions []string, at time.Time) {
	fixture.t.Helper()
	if err := fixture.database.UpsertTaskMemory(fixture.ctx, domain.TaskMemory{
		TaskID: taskID, CurrentGoal: taskID, Status: status, StatusUpdatedAt: at,
		NextSteps: nextSteps, OpenQuestions: openQuestions, UpdatedAt: at,
	}); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *petDeskFixture) turn(task domain.Task, status domain.TurnStatus, turnError string) {
	fixture.t.Helper()
	fixture.sequence++
	at := fixture.now.Add(-time.Duration(fixture.sequence) * time.Minute)
	message := domain.Message{ID: fmt.Sprintf("message-desk-%d", fixture.sequence), TaskID: task.ID, Role: "user", Sender: "owner", Content: "go", CreatedAt: at}
	if err := fixture.database.AddMessage(fixture.ctx, message); err != nil {
		fixture.t.Fatal(err)
	}
	turn := domain.Turn{
		ID: fmt.Sprintf("turn-desk-%d", fixture.sequence), TaskID: task.ID, AgentID: "main", Sequence: fixture.sequence,
		InputMessageID: message.ID, Status: status, Error: turnError, RoundID: fmt.Sprintf("round-desk-%d", fixture.sequence),
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, QueuedAt: at, StartedAt: at,
	}
	if status.Terminal() {
		turn.FinishedAt = at
	}
	if err := fixture.database.CreateTurn(fixture.ctx, turn); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *petDeskFixture) desk() PetDesk {
	fixture.t.Helper()
	view, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{ApprovalLimit: 50, WorklogLimit: 30})
	if err != nil {
		fixture.t.Fatal(err)
	}
	if view.Desk == nil {
		fixture.t.Fatal("workbench desk missing")
	}
	return *view.Desk
}

func deskNeedKinds(items []PetDeskNeed) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		id := item.TaskID
		if item.ApprovalID != "" {
			id = item.ApprovalID
		}
		parts = append(parts, item.Kind+":"+id)
	}
	return strings.Join(parts, " ")
}

func TestPetDeskSortsWorkByWhoHasToMove(t *testing.T) {
	fixture := newPetDeskFixture(t)
	now := fixture.now

	question := fixture.task("task-question", domain.TaskWaitingUser, 50*time.Minute)
	fixture.turn(question, domain.TurnSucceeded, "")
	fixture.card(question.ID, "plan drafted", []string{"write the plan"}, []string{"  ", "ship step one first?"}, now.Add(-45*time.Minute))

	idle := fixture.task("task-idle", domain.TaskWaitingUser, 20*time.Minute)
	fixture.turn(idle, domain.TurnSucceeded, "")
	fixture.card(idle.ID, "round finished", []string{"wait for the owner"}, nil, now.Add(-20*time.Minute))

	stopped := fixture.task("task-stopped", domain.TaskWaitingUser, 40*time.Minute)
	fixture.turn(stopped, domain.TurnInterrupted, "service restarted")

	blocked := fixture.task("task-blocked", domain.TaskBlocked, 90*time.Minute)
	fixture.turn(blocked, domain.TurnBlocked, "needs a permission")

	failed := fixture.task("task-failed", domain.TaskFailed, 30*time.Minute)
	fixture.turn(failed, domain.TurnFailed, "backend exited")

	approved := fixture.task("task-with-approval", domain.TaskBlocked, 70*time.Minute)
	fixture.turn(approved, domain.TurnBlocked, "waiting for a decision")

	reviewed := fixture.task("task-in-review", domain.TaskWaitingUser, 25*time.Minute)
	fixture.turn(reviewed, domain.TurnSucceeded, "")
	fixture.card(reviewed.ID, "candidate ready", nil, []string{"merge after review?"}, now.Add(-25*time.Minute))
	review := fixture.task("task-review", domain.TaskActive, 10*time.Minute)
	fixture.turn(review, domain.TurnRunning, "")
	if err := fixture.database.CreatePetCodeReviewLink(fixture.ctx, review.ID, reviewed.ID); err != nil {
		t.Fatal(err)
	}

	running := fixture.task("task-running", domain.TaskActive, 15*time.Minute)
	fixture.turn(running, domain.TurnRunning, "")
	fixture.card(running.ID, "running the overnight soak", nil, []string{"stale question from the previous round"}, now.Add(-time.Hour))

	ignored := fixture.task("task-ignored", domain.TaskFailed, 5*time.Minute)
	fixture.turn(ignored, domain.TurnFailed, "ignored failure")
	if err := fixture.database.IgnorePetTaskWatch(fixture.ctx, ignored.ID, petWatchReasonIgnored, now); err != nil {
		t.Fatal(err)
	}
	fixture.task("task-draft", domain.TaskDraft, time.Minute)
	fixture.task("task-done", domain.TaskCompleted, time.Minute)

	for _, approval := range []domain.PetApproval{
		{ID: "approval-decision", TaskID: petTaskID, Kind: "owner_decision", Title: "decide", Summary: "pick one", Risk: "low", Status: petApprovalPending, Payload: `{"task_id":"task-with-approval"}`, CreatedAt: now.Add(-60 * time.Minute)},
		{ID: "approval-idea", TaskID: petTaskID, Kind: "idea", Title: "an idea", Summary: "maybe later", Status: petApprovalPending, CreatedAt: now.Add(-10 * time.Minute)},
		{ID: "approval-executing", TaskID: petTaskID, Kind: "delete_workspace", Title: "delete a workspace", Status: "executing", CreatedAt: now.Add(-8 * time.Minute), ResolvedAt: now.Add(-7 * time.Minute)},
		{ID: "approval-resolved", TaskID: petTaskID, Kind: "owner_decision", Title: "old", Status: "approved", CreatedAt: now.Add(-2 * time.Hour), ResolvedAt: now.Add(-time.Hour)},
	} {
		if err := fixture.database.CreatePetApproval(fixture.ctx, approval); err != nil {
			t.Fatal(err)
		}
	}

	desk := fixture.desk()

	// Longest wait first. The blocked Task that already has a pending approval
	// is represented by the approval, not listed twice.
	if got, want := deskNeedKinds(desk.NeedsOwner), "blocked:task-blocked approval:approval-decision question:task-question stopped:task-stopped failed:task-failed"; got != want {
		t.Fatalf("needs_owner = %q, want %q", got, want)
	}
	if desk.NeedsOwnerTotal != 5 {
		t.Fatalf("needs_owner_total = %d", desk.NeedsOwnerTotal)
	}
	byKind := map[string]PetDeskNeed{}
	for _, item := range desk.NeedsOwner {
		byKind[item.Kind] = item
	}
	if item := byKind[PetDeskNeedQuestion]; len(item.Questions) != 1 || item.Questions[0] != "ship step one first?" || !item.Since.Equal(now.Add(-45*time.Minute)) || item.Detail != "plan drafted" {
		t.Fatalf("question item = %+v", item)
	}
	if item := byKind[PetDeskNeedApproval]; item.TaskID != "task-with-approval" || item.ApprovalKind != "owner_decision" || item.Detail != "pick one" || item.Risk != "low" {
		t.Fatalf("approval item = %+v", item)
	}
	if item := byKind[PetDeskNeedStopped]; item.Detail != "service restarted" {
		t.Fatalf("stopped item = %+v", item)
	}
	if item := byKind[PetDeskNeedBlocked]; item.Detail != "needs a permission" {
		t.Fatalf("blocked item = %+v", item)
	}

	// Newest first: nothing here is blocked on the Owner.
	if got, want := deskNeedKinds(desk.Later), "idea:approval-idea next_step:task-idle"; got != want {
		t.Fatalf("later = %q, want %q", got, want)
	}
	if desk.Later[1].Detail != "wait for the owner" {
		t.Fatalf("next_step detail = %q", desk.Later[1].Detail)
	}

	if len(desk.WaitingOthers) != 2 || desk.WaitingOthersTotal != 2 {
		t.Fatalf("waiting_others = %+v", desk.WaitingOthers)
	}
	if wait := desk.WaitingOthers[0]; wait.Kind != PetDeskWaitReview || wait.TaskID != reviewed.ID || review.Code == "" || wait.On != review.Code {
		t.Fatalf("review wait = %+v", wait)
	}
	if wait := desk.WaitingOthers[1]; wait.Kind != PetDeskWaitExecuting || wait.On != "delete_workspace" || !wait.Since.Equal(now.Add(-7*time.Minute)) {
		t.Fatalf("executing wait = %+v", wait)
	}

	if len(desk.Running) != 2 || desk.RunningTotal != 2 {
		t.Fatalf("running = %+v", desk.Running)
	}
	runningByID := map[string]PetDeskRun{}
	for _, run := range desk.Running {
		runningByID[run.TaskID] = run
	}
	if run := runningByID[running.ID]; run.TurnStatus != string(domain.TurnRunning) || run.Detail != "running the overnight soak" || run.Since.After(now) || run.Since.Equal(running.UpdatedAt) {
		t.Fatalf("running row = %+v", run)
	}
	if _, ok := runningByID[review.ID]; !ok {
		t.Fatalf("review task is not listed as running: %+v", desk.Running)
	}
	for _, id := range []string{ignored.ID, "task-draft", "task-done", petTaskID} {
		for _, item := range append(append([]PetDeskNeed{}, desk.NeedsOwner...), desk.Later...) {
			if item.TaskID == id {
				t.Fatalf("%s must not be on the desk: %+v", id, item)
			}
		}
	}
}

func TestPetDeskFinishedReviewReturnsTheTaskToTheOwner(t *testing.T) {
	fixture := newPetDeskFixture(t)
	source := fixture.task("task-source", domain.TaskWaitingUser, 30*time.Minute)
	fixture.turn(source, domain.TurnSucceeded, "")
	fixture.card(source.ID, "candidate ready", nil, []string{"merge it?"}, fixture.now.Add(-30*time.Minute))
	review := fixture.task("task-review-done", domain.TaskWaitingUser, 10*time.Minute)
	fixture.turn(review, domain.TurnSucceeded, "")
	if err := fixture.database.CreatePetCodeReviewLink(fixture.ctx, review.ID, source.ID); err != nil {
		t.Fatal(err)
	}

	desk := fixture.desk()
	if len(desk.WaitingOthers) != 0 {
		t.Fatalf("a finished review must not count as waiting on others: %+v", desk.WaitingOthers)
	}
	if got, want := deskNeedKinds(desk.NeedsOwner), "question:task-source"; got != want {
		t.Fatalf("needs_owner = %q, want %q", got, want)
	}
}

func TestPetDeskBoundsListsAndKeepsTotals(t *testing.T) {
	fixture := newPetDeskFixture(t)
	total := petDeskNeedLimit + 7
	for index := 0; index < total; index++ {
		fixture.task(fmt.Sprintf("task-failed-%03d", index), domain.TaskFailed, time.Duration(index+1)*time.Minute)
	}
	desk := fixture.desk()
	if len(desk.NeedsOwner) != petDeskNeedLimit || desk.NeedsOwnerTotal != total {
		t.Fatalf("needs_owner len=%d total=%d", len(desk.NeedsOwner), desk.NeedsOwnerTotal)
	}
	if want := fmt.Sprintf("task-failed-%03d", total-1); desk.NeedsOwner[0].TaskID != want {
		t.Fatalf("oldest row = %s, want %s", desk.NeedsOwner[0].TaskID, want)
	}
}

func TestStoreDeskBatchesReadAcrossBatchBoundary(t *testing.T) {
	fixture := newPetDeskFixture(t)
	ids := make([]string, 0, 900)
	for index := 0; index < 900; index++ {
		ids = append(ids, fmt.Sprintf("task-absent-%d", index))
	}
	first := fixture.task("task-card-first", domain.TaskWaitingUser, time.Minute)
	last := fixture.task("task-card-last", domain.TaskWaitingUser, time.Minute)
	fixture.card(first.ID, "first", nil, []string{"q1"}, fixture.now)
	fixture.card(last.ID, "last", []string{"n1"}, nil, fixture.now)
	fixture.turn(first, domain.TurnFailed, "old failure")
	fixture.turn(first, domain.TurnSucceeded, "")
	fixture.turn(last, domain.TurnInterrupted, "cut")
	// A Sub Turn that is newer than the Main Turn must not replace it.
	fixture.sequence++
	if err := fixture.database.CreateTurn(fixture.ctx, domain.Turn{
		ID: "turn-desk-sub", TaskID: last.ID, AgentID: "sub-1", Sequence: fixture.sequence, InputMessageID: "message-desk-3", RoundID: "round-desk-sub",
		Status: domain.TurnSucceeded, RuntimeConfigSnapshotID: last.RuntimeConfigSnapshotID, QueuedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}
	ids = append([]string{first.ID}, append(ids, last.ID)...)

	cards, err := fixture.database.TaskStateCards(fixture.ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 || cards[first.ID].OpenQuestions[0] != "q1" || cards[last.ID].NextSteps[0] != "n1" || cards[last.ID].Status != "last" {
		t.Fatalf("cards = %+v", cards)
	}
	turns, err := fixture.database.LatestMainTurnBriefs(fixture.ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[first.ID].Status != string(domain.TurnSucceeded) || turns[last.ID].Status != string(domain.TurnInterrupted) || turns[last.ID].Error != "cut" {
		t.Fatalf("turns = %+v", turns)
	}
	if empty, err := fixture.database.TaskStateCards(fixture.ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty cards = %+v, %v", empty, err)
	}
}

func TestPetReadsTaskStateCardQuestions(t *testing.T) {
	fixture := newPetDeskFixture(t)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	task := fixture.task("task-followed", domain.TaskWaitingUser, 30*time.Minute)
	fixture.turn(task, domain.TurnSucceeded, "")
	fixture.card(task.ID, "waiting for scope", []string{"write the plan"}, []string{"step one only?", "plan or build?"}, fixture.now.Add(-30*time.Minute))
	bare := fixture.task("task-no-card", domain.TaskWaitingUser, 20*time.Minute)
	for _, id := range []string{task.ID, bare.ID} {
		if err := fixture.database.UpsertPetTaskWatch(fixture.ctx, id, "manual", fixture.now); err != nil {
			t.Fatal(err)
		}
	}

	detail, err := fixture.service.PetTaskDetail(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.StateCard == nil || detail.StateCard.Status != "waiting for scope" || len(detail.StateCard.OpenQuestions) != 2 || detail.StateCard.NextSteps[0] != "write the plan" || detail.StateCard.UpdatedAt == "" {
		t.Fatalf("state card = %+v", detail.StateCard)
	}
	if bareDetail, err := fixture.service.PetTaskDetail(fixture.ctx, bare.ID); err != nil || bareDetail.StateCard != nil {
		t.Fatalf("task without a card: %+v, %v", bareDetail.StateCard, err)
	}

	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	questions := map[string]int{}
	for _, item := range state.OpenTasks {
		questions[item.ID] = item.OwnerQuestions
	}
	if questions[task.ID] != 2 || questions[bare.ID] != 0 {
		t.Fatalf("owner_questions = %+v", questions)
	}

	// With task details private the count is private too.
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range state.OpenTasks {
		if item.ID == task.ID && (item.OwnerQuestions != 0 || item.Title != "") {
			t.Fatalf("private followed task leaked details: %+v", item)
		}
	}
}
