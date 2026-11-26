package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func getLifecycle(t *testing.T, f taskDetailFixture, id string, client *http.Client, wantStatus int) app.TaskLifecycle {
	t.Helper()
	response, err := client.Get(f.server.URL + "/api/v1/tasks/" + id + "/lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("lifecycle status=%d want=%d body=%s", response.StatusCode, wantStatus, readBody(t, response))
	}
	if wantStatus != http.StatusOK {
		return app.TaskLifecycle{}
	}
	var result struct {
		OK        bool              `json:"ok"`
		Lifecycle app.TaskLifecycle `json:"lifecycle"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || !result.OK {
		t.Fatalf("lifecycle response=%+v err=%v", result, err)
	}
	return result.Lifecycle
}

func lifecycleTestTurn(t *testing.T, f taskDetailFixture, task domain.Task, agent, suffix, title, result, turnError string, at time.Time, tokens int) domain.Turn {
	t.Helper()
	id := task.ID + "-lifecycle-" + suffix
	message := domain.Message{ID: id, TaskID: task.ID, Role: "user", Sender: "owner", Content: "turn", CreatedAt: at}
	turn := domain.Turn{
		ID: id, TaskID: task.ID, AgentID: agent, InputMessageID: id, Status: domain.TurnSucceeded,
		Title: title, Result: result, Error: turnError, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		QueuedAt: at, StartedAt: at, FinishedAt: at.Add(2 * time.Second), Usage: map[string]any{"input_tokens": tokens},
	}
	ctx := context.Background()
	if agent != "main" {
		// A child is attached to a collaboration batch below, not an Owner round.
		turn.Sequence = 3
		if err := f.database.AddMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
		if err := f.database.CreateTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
		return turn
	}
	round := domain.TaskRound{ID: id + "-round", TaskID: task.ID, InputMessageID: id, Status: domain.RoundCompleted,
		CreatedAt: at, StartedAt: at, FinishedAt: turn.FinishedAt}
	_, saved, err := f.database.CreateMessageRoundAndTurn(ctx, message, round, turn)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestTaskLifecycleEndpointSourcesAndAccess(t *testing.T) {
	f := newTaskDetailFixture(t, false)
	db, ctx := f.database, context.Background()
	task := f.task
	at := task.CreatedAt.Add(time.Second)
	getLifecycle(t, f, task.ID, f.server.Client(), http.StatusUnauthorized)
	getLifecycle(t, f, "missing-task", f.client, http.StatusNotFound)

	first := lifecycleTestTurn(t, f, task, "main", "1", "", "Initial result\nIgnore this line", "", at, 7)
	last := lifecycleTestTurn(t, f, task, "main", "2", "", "Final result\nIgnore this line", strings.Repeat("错", 220), at.Add(time.Minute), 11)
	child := lifecycleTestTurn(t, f, task, "sub-001", "3", "Inspect build", "Sub result", "", at.Add(2*time.Second), 5)
	goal := domain.TaskGoalChange{TaskID: task.ID, TurnID: first.ID, GoalAfter: "Revised goal", ChangeType: "revise", Reason: "Scope changed", CreatedAt: at}
	if err := db.UpdateTaskGoalWithEvent(ctx, goal); err != nil {
		t.Fatal(err)
	}
	title, owner, state := "Inspections", "self", "open"
	opened := domain.TaskLinePatch{ID: "inspect", Title: &title, Owner: &owner, State: &state}
	followupTitle, followupOwner, followupWho, followupState := "Follow-up", "other", "Lab", "waiting"
	followup := domain.TaskLinePatch{ID: "followup", Title: &followupTitle, Owner: &followupOwner, Who: &followupWho, State: &followupState}
	if err := db.UpdateTaskMemory(ctx, task.ID, first, at, []domain.TaskLinePatch{opened}, func(memory *domain.TaskMemory) (*domain.TaskGoalChange, error) {
		memory.Status, memory.StatusUpdatedAt = "Current status", at
		memory.NextSteps = []string{"Next action"}
		memory.OpenQuestions = []string{"Scope?"}
		memory.Constraints = []domain.TaskMemoryNote{{Text: "No network", TurnID: first.ID, CreatedAt: at}}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	state = "blocked"
	reason := "Device offline"
	if err := db.UpdateTaskMemory(ctx, task.ID, first, at.Add(time.Second),
		[]domain.TaskLinePatch{{ID: "inspect", State: &state, Blocker: &reason}}, keepLifecycleCard); err != nil {
		t.Fatal(err)
	}
	state = "closed"
	if err := db.UpdateTaskMemory(ctx, task.ID, last, at.Add(2*time.Second),
		[]domain.TaskLinePatch{{ID: "inspect", State: &state}, followup}, keepLifecycleCard); err != nil {
		t.Fatal(err)
	}
	approval := domain.PetApproval{ID: "review-deployment", TaskID: "task_pet", Kind: "deploy", Title: "Deploy build", Summary: "Target staging",
		Status: "pending", Payload: fmt.Sprintf(`{"task_id": %q}`, task.ID), CreatedAt: at}
	if err := db.CreatePetApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolvePetApproval(ctx, approval.ID, "rejected", "denied", at.Add(time.Second), "Use tomorrow's window"); err != nil {
		t.Fatal(err)
	}
	approval.ID, approval.Payload = "foreign-approval", `{"task_id":"somebody-else"}`
	if err := db.CreatePetApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	for _, batch := range []struct {
		id, agent, child string
		when             time.Time
	}{{"old-batch", "sub-old", "", at}, {"latest-batch", "sub-001", child.ID, at.Add(time.Second)}} {
		row := domain.CollaborationBatch{ID: batch.id, TaskID: task.ID, RoundID: first.RoundID,
			ParentTurnID: first.ID, CreatedAt: batch.when, UpdatedAt: batch.when}
		member := domain.CollaborationMember{AgentID: batch.agent, ChildTurnID: batch.child, Required: true, CreatedAt: batch.when, UpdatedAt: batch.when}
		if err := db.CreateCollaborationBatch(ctx, row, []domain.CollaborationMember{member}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := db.RecordCollaborationResult(ctx, child, "succeeded", "Sub result"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCollaborationConsumed(ctx, "latest-batch", at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	review := domain.Task{ID: "review-task", ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID,
		Title: "Review", CurrentGoal: "Review changes", Status: domain.TaskCompleted,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, CreatedAt: at.Add(4 * time.Second), UpdatedAt: at.Add(8 * time.Second)}
	if err := db.CreateTask(ctx, review); err != nil {
		t.Fatal(err)
	}
	reviewTurn := lifecycleTestTurn(t, f, review, "main", "1", "", "Verdict: APPROVE\nVerified evidence", "", at.Add(4*time.Second), 2)
	if err := db.CreatePetCodeReviewLink(ctx, review.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	done := at.Add(2 * time.Minute)
	decision := domain.TaskReviewDecision{ReviewDecision: "required", ReviewReason: "Independent review was required", ReviewTaskID: review.ID, ReviewDecidedAt: done}
	current, err := db.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteTaskWithReviewDecision(ctx, task.ID, current.Status, done.Format(time.RFC3339Nano), &decision); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskGoalWithEvent(ctx, domain.TaskGoalChange{TaskID: task.ID, GoalAfter: goal.GoalAfter, ChangeType: "complete", Reason: "Completion recorded", CreatedAt: done}); err != nil {
		t.Fatal(err)
	}
	view := getLifecycle(t, f, task.ID, f.client, http.StatusOK)
	if view.Task.ID != task.ID || view.Task.Project != "Detail" || view.Task.CurrentGoal != goal.GoalAfter || view.Task.Status != domain.TaskCompleted ||
		view.Now.Status != "Current status" || view.Now.StuckOn != "" || !view.Now.LinesRecorded || len(view.Now.NextSteps) != 1 ||
		len(view.Now.OpenQuestions) != 1 || !view.Now.UpdatedAt.Equal(at) || !view.Now.IdleSince.IsZero() {
		t.Fatalf("task/now = %+v %+v", view.Task, view.Now)
	}
	if len(view.Lines) != 2 || view.Lines[0].ID != "followup" || !view.Lines[0].ClosedAt.IsZero() ||
		view.Lines[1].ID != "inspect" || !view.Lines[1].ClosedAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("active first and closure dates: %+v", view.Lines)
	}
	if len(view.Parallel.Subs) != 1 || view.Parallel.Subs[0].AgentID != child.AgentID || view.Parallel.Subs[0].Title != child.Title ||
		view.Parallel.Subs[0].Status != "succeeded" || !view.Parallel.Subs[0].FinishedAt.Equal(child.FinishedAt) ||
		len(view.Parallel.Reviews) != 1 || view.Parallel.Reviews[0].Decision != "APPROVE" ||
		view.Parallel.Reviews[0].Reason != "Verified evidence" || !view.Parallel.Reviews[0].DecidedAt.Equal(reviewTurn.FinishedAt) {
		t.Fatalf("parallel = %+v", view.Parallel)
	}
	wantKinds := []string{"created", "goal", "goal", "constraint", "approval_requested", "line_opened",
		"approval_resolved", "line_updated", "line_closed", "line_opened", "review", "goal", "review", "completed"}
	var gotKinds []string
	for i, event := range view.Timeline {
		gotKinds = append(gotKinds, event.Kind)
		if i > 0 && event.At.Before(view.Timeline[i-1].At) {
			t.Fatalf("out of order at %d: %+v", i, view.Timeline)
		}
	}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("timeline got %v want %v %+v", gotKinds, wantKinds, view.Timeline)
	}
	if view.Timeline[2].Title != "revise" || view.Timeline[2].Detail != "Revised goal\nScope changed" ||
		view.Timeline[3].RefID != first.ID || view.Timeline[3].Detail != "No network" ||
		view.Timeline[4].RefID != "review-deployment" || view.Timeline[4].Title != "Deploy build" ||
		view.Timeline[6].Title != "rejected" || view.Timeline[6].Detail != "Use tomorrow's window" ||
		view.Timeline[7].Title != "Inspections" || view.Timeline[7].Detail != "self · blocked · Device offline" ||
		view.Timeline[8].RefID != "inspect" || view.Timeline[10].RefID != review.ID ||
		view.Timeline[10].Detail != "Verified evidence" || view.Timeline[11].Title != "complete" ||
		view.Timeline[13].RefID != view.Task.ID || view.Timeline[13].At.IsZero() {
		t.Fatalf("timeline source mismatch: %+v", view.Timeline)
	}
	if len(view.Turns) != 2 || view.Turns[0].ID != last.ID || view.Turns[0].Summary != "Final result" ||
		utf8.RuneCountInString(view.Turns[0].Error) != 200 || view.Turns[0].DurationSeconds != 2 ||
		view.Stats.Turns != 2 || view.Stats.DurationSeconds != 4 || view.Stats.Tokens != 23 ||
		view.TurnsTruncated || view.TimelineTruncated {
		t.Fatalf("turns/stats=%+v %+v", view.Turns, view.Stats)
	}
}

func keepLifecycleCard(*domain.TaskMemory) (*domain.TaskGoalChange, error) { return nil, nil }

func TestTaskLifecycleLimitsAndLatency(t *testing.T) {
	f := newTaskDetailFixture(t, false)
	task := f.task
	ctx := context.Background()
	at := task.CreatedAt.Add(time.Second)
	for i := range 200 {
		lifecycleTestTurn(t, f, task, "main", fmt.Sprint(i), "", strings.Repeat("重", 220)+"\nSecond line", "", at.Add(time.Duration(i)*time.Second), 3)
	}
	for i := range 20 {
		if err := f.database.UpdateTaskGoalWithEvent(ctx, domain.TaskGoalChange{
			TaskID: task.ID, GoalAfter: fmt.Sprintf("Goal %d", i), ChangeType: "revise", Reason: "Fixture",
			CreatedAt: at.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	title, owner, state := "Line", "self", "open"
	lineAt := at.Add(time.Hour)
	writeLine := func(i int) {
		t.Helper()
		step := fmt.Sprintf("Step %d", i)
		if err := f.database.UpdateTaskMemory(ctx, task.ID, domain.Turn{}, lineAt.Add(time.Duration(i)*time.Second),
			[]domain.TaskLinePatch{{ID: "line", Title: &title, Owner: &owner, State: &state, Step: &step}}, keepLifecycleCard); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 100 {
		writeLine(i)
	}
	durations := make([]time.Duration, 20)
	var view app.TaskLifecycle
	for i := range durations {
		start := time.Now()
		view = getLifecycle(t, f, task.ID, f.client, http.StatusOK)
		durations[i] = time.Since(start)
	}
	slices.Sort(durations)
	t.Logf("owner HTTP incl. JSON decode, 200 rounds/Turns, 100 line events, 20 goal changes: n=%d min=%v p50=%v p95=%v max=%v",
		len(durations), durations[0], durations[9], durations[18], durations[19])
	if view.Stats.Turns != 200 || view.Stats.Tokens != 600 || view.Stats.DurationSeconds != 400 ||
		len(view.Turns) != 100 || !view.TurnsTruncated || view.Turns[0].Sequence != 200 || view.Turns[99].Sequence != 101 ||
		utf8.RuneCountInString(view.Turns[0].Summary) != 200 || len(view.Timeline) != 122 || view.TimelineTruncated {
		t.Fatalf("limits and stats: stats=%+v turns=%d timeline=%d trunc=%v/%v", view.Stats, len(view.Turns), len(view.Timeline), view.TurnsTruncated, view.TimelineTruncated)
	}
	for i := 100; i < 201; i++ {
		writeLine(i)
	}
	view = getLifecycle(t, f, task.ID, f.client, http.StatusOK)
	if len(view.Timeline) != 200 || !view.TimelineTruncated || view.Timeline[0].Kind != "line_updated" ||
		!strings.HasSuffix(view.Timeline[0].Detail, "Step 1") || !strings.HasSuffix(view.Timeline[199].Detail, "Step 200") {
		t.Fatalf("timeline limit got len=%d truncated=%v, first=%+v last=%+v",
			len(view.Timeline), view.TimelineTruncated, view.Timeline[0], view.Timeline[len(view.Timeline)-1])
	}
}
