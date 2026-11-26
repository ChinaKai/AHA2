package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskStuckOnPrecedence(t *testing.T) {
	self := domain.TaskLine{Owner: "self", State: "open"}
	waiting := domain.TaskLine{Owner: "self", State: "waiting"}
	blocked := domain.TaskLine{Owner: "self", State: "blocked"}
	owner := domain.TaskLine{Owner: "owner", State: "blocked"}
	qa := domain.TaskLine{Owner: "other", State: "waiting", Who: "QA"}
	ops := domain.TaskLine{Owner: "other", State: "open", Who: "Operations"}
	closed := domain.TaskLine{Owner: "owner", State: "closed"}
	for _, test := range []struct {
		name  string
		lines []domain.TaskLine
		on    string
		who   []string
	}{
		{"self wins after other and owner", []domain.TaskLine{qa, owner, self}, "self", nil},
		{"owner wins over other and blocked self", []domain.TaskLine{qa, waiting, owner, ops}, "owner", nil},
		{"other collects distinct names", []domain.TaskLine{waiting, closed, qa, qa, ops}, "other", []string{"QA", "Operations"}},
		{"all closed", []domain.TaskLine{closed}, "none", nil},
		{"only waiting self", []domain.TaskLine{waiting}, "none", nil},
		{"only blocked self", []domain.TaskLine{blocked}, "none", nil},
		{"no recorded lines", nil, "unknown", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := taskStuckOn(test.lines)
			if got.on != test.on || !slices.Equal(got.who, test.who) || (test.on == "self" && got.self != self) {
				t.Fatalf("got %+v, want %s/%v", got, test.on, test.who)
			}
		})
	}
}

func (f *petDeskFixture) lifecycleTurn(task domain.Task, agent string, status domain.TurnStatus, finished time.Time) {
	f.t.Helper()
	f.sequence++
	id := fmt.Sprintf("lifecycle-%d", f.sequence)
	message := domain.Message{ID: id, TaskID: task.ID, Role: "user", Sender: "owner", Content: "go", CreatedAt: finished}
	if err := f.database.AddMessage(f.ctx, message); err != nil {
		f.t.Fatal(err)
	}
	turn := domain.Turn{ID: id, TaskID: task.ID, AgentID: agent, Sequence: f.sequence,
		InputMessageID: id, Status: status, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		StartedAt: finished.Add(-time.Minute), FinishedAt: finished}
	if !status.Terminal() {
		turn.FinishedAt = time.Time{}
	}
	if err := f.database.CreateTurn(f.ctx, turn); err != nil {
		f.t.Fatal(err)
	}
}

func stallPatch(id, owner, state, who string) domain.TaskLinePatch {
	title, step, blocker := "Check "+id, "Step "+id, "Blocker "+id
	return domain.TaskLinePatch{ID: id, Title: &title, Owner: &owner, State: &state, Who: &who, Step: &step, Blocker: &blocker}
}

func (f *petDeskFixture) lines(task domain.Task, patches ...domain.TaskLinePatch) {
	f.t.Helper()
	if err := f.database.UpdateTaskMemory(f.ctx, task.ID, domain.Turn{}, f.now, patches,
		func(*domain.TaskMemory) (*domain.TaskGoalChange, error) { return nil, nil }); err != nil {
		f.t.Fatal(err)
	}
}

func TestPetDeskStalledAndLineClassification(t *testing.T) {
	f := newPetDeskFixture(t)
	old := f.now.Add(-20 * time.Minute)
	self := stallPatch("self", "self", "open", "")
	owner := stallPatch("owner", "owner", "waiting", "")
	other := stallPatch("other", "other", "blocked", "QA")
	newTask := func(id string, status domain.TaskStatus, turn domain.TurnStatus, at time.Time, patches ...domain.TaskLinePatch) domain.Task {
		task := f.task(id, status, time.Hour)
		if turn != "" {
			f.lifecycleTurn(task, "main", turn, at)
		}
		if len(patches) != 0 {
			f.lines(task, patches...)
		}
		return task
	}
	stalled := newTask("stalled", domain.TaskWaitingUser, domain.TurnSucceeded, old, owner, self)
	edge := newTask("edge", domain.TaskWaitingUser, domain.TurnSucceeded, f.now.Add(-15*time.Minute), self)
	pastEdge := newTask("past-edge", domain.TaskWaitingUser, domain.TurnSucceeded, f.now.Add(-15*time.Minute-time.Nanosecond), self)
	needsOwner := newTask("owner", domain.TaskWaitingUser, domain.TurnSucceeded, old, owner, other)
	f.card(needsOwner.ID, "status", nil, []string{"old question"}, old)
	waitOther := newTask("other", domain.TaskWaitingUser, domain.TurnSucceeded, old, other, stallPatch("ops", "other", "waiting", "Operations"))
	none := newTask("none", domain.TaskWaitingUser, domain.TurnSucceeded, old, stallPatch("paused", "self", "blocked", ""))
	legacyStall := newTask("legacy-stall", domain.TaskWaitingUser, domain.TurnSucceeded, old.Add(-time.Hour))
	f.card(legacyStall.ID, "", []string{"next action"}, nil, old)
	legacyLater := newTask("legacy-later", domain.TaskWaitingUser, domain.TurnSucceeded, old)
	legacyAsk := newTask("legacy-ask", domain.TaskWaitingUser, domain.TurnSucceeded, old)
	f.card(legacyAsk.ID, "", []string{"plan"}, []string{"decision"}, old)
	failedTurn := newTask("legacy-failed-turn", domain.TaskWaitingUser, domain.TurnInterrupted, old)
	f.card(failedTurn.ID, "", []string{"plan"}, nil, old)
	noTurn := newTask("never-ran", domain.TaskWaitingUser, "", old, self)
	failedTask := newTask("failed-task", domain.TaskFailed, domain.TurnSucceeded, old, self)
	approved := newTask("approval", domain.TaskWaitingUser, domain.TurnSucceeded, old, self)
	executing := newTask("executing", domain.TaskWaitingUser, domain.TurnSucceeded, old, self)
	underReview := newTask("reviewed", domain.TaskWaitingUser, domain.TurnSucceeded, old, self)
	review := f.task("review", domain.TaskActive, time.Minute)
	if err := f.database.CreatePetCodeReviewLink(f.ctx, review.ID, underReview.ID); err != nil {
		t.Fatal(err)
	}
	ignored := newTask("ignored", domain.TaskWaitingUser, domain.TurnSucceeded, old, self)
	if err := f.database.IgnorePetTaskWatch(f.ctx, ignored.ID, "ignore-only", old); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ taskID, status string }{{approved.ID, "pending"}, {executing.ID, "executing"}} {
		if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
			ID: "approval-" + test.taskID, Kind: "external_channel_send", Title: test.taskID, Status: test.status,
			Payload: fmt.Sprintf(`{"task_id":%q}`, test.taskID), CreatedAt: old,
		}); err != nil {
			t.Fatal(err)
		}
	}
	desk := f.desk()
	if len(desk.Stalled) != 3 || desk.Stalled[0].TaskID != legacyStall.ID || desk.Stalled[0].Detail != "next action" ||
		desk.Stalled[0].LineID != "" || desk.Stalled[1].TaskID != stalled.ID || desk.Stalled[1].LineID != "self" ||
		desk.Stalled[1].Detail != "Step self" || desk.Stalled[2].TaskID != pastEdge.ID || desk.StalledTotal != 3 {
		t.Fatalf("stalled = %+v", desk.Stalled)
	}
	later := map[string]PetDeskNeed{}
	needs := map[string]PetDeskNeed{}
	waiting := map[string]PetDeskWait{}
	for _, row := range desk.Later {
		later[row.TaskID] = row
	}
	for _, row := range desk.NeedsOwner {
		needs[row.TaskID] = row
	}
	for _, row := range desk.WaitingOthers {
		waiting[row.TaskID] = row
	}
	for id, expected := range map[string]string{edge.ID: "self", none.ID: "none", legacyLater.ID: "unknown", noTurn.ID: "self", executing.ID: "self"} {
		if got := later[id]; got.Kind != PetDeskLaterNextStep || got.StuckOn != expected {
			t.Fatalf("later %s = %+v", id, got)
		}
	}
	if got := needs[needsOwner.ID]; got.Kind != PetDeskNeedQuestion || got.StuckOn != "owner" ||
		len(got.Lines) != 1 || got.Lines[0].ID != "owner" || !slices.Equal(got.Questions, []string{"old question"}) {
		t.Fatalf("owner's lines = %+v", got)
	}
	if needs[legacyAsk.ID].StuckOn != "unknown" || needs[legacyAsk.ID].Kind != PetDeskNeedQuestion ||
		needs[failedTurn.ID].Kind != PetDeskNeedStopped || needs[failedTask.ID].Kind != PetDeskNeedFailed ||
		needs[approved.ID].Kind != PetDeskNeedApproval || waiting[underReview.ID].Kind != PetDeskWaitReview {
		t.Fatalf("existing priorities changed: needs=%+v waiting=%+v", needs, waiting)
	}
	if got := waiting[waitOther.ID]; got.Kind != PetDeskWaitLine || got.On != "QA、Operations" ||
		len(got.Lines) != 2 || got.Lines[1].Who != "Operations" || !got.Since.Equal(waitOther.UpdatedAt) {
		t.Fatalf("waiting lines = %+v", got)
	}
	for _, id := range []string{ignored.ID, stalled.ID, legacyStall.ID, pastEdge.ID} {
		if later[id].TaskID != "" || waiting[id].TaskID != "" || needs[id].TaskID != "" {
			t.Fatalf("task %s also appeared in another desk list", id)
		}
	}
}

func TestPetDeskRecentActivityScopeAndLimits(t *testing.T) {
	f := newPetDeskFixture(t)
	edge := f.task("edge", domain.TaskCompleted, 7*24*time.Hour)
	tooOld := f.task("too-old", domain.TaskCancelled, 7*24*time.Hour+time.Nanosecond)
	draft := f.task("draft", domain.TaskDraft, time.Minute)
	ignored := f.task("ignored", domain.TaskWaitingUser, time.Minute)
	if err := f.database.IgnorePetTaskWatch(f.ctx, ignored.ID, "ignore-only", f.now); err != nil {
		t.Fatal(err)
	}
	// Latest activity uses any finished Turn, even behind a newer running Turn.
	closedRecently := f.task("closed-recently", domain.TaskCancelled, 8*24*time.Hour)
	stamp := f.now.Truncate(time.Second).Add(-time.Second)
	f.lifecycleTurn(closedRecently, "main", domain.TurnSucceeded, stamp)
	f.lifecycleTurn(closedRecently, "sub-001", domain.TurnSucceeded, stamp.Add(time.Nanosecond))
	f.lifecycleTurn(closedRecently, "main", domain.TurnRunning, f.now)
	watched := f.task("watched", domain.TaskActive, time.Minute)
	if err := f.database.UpsertPetTaskWatch(f.ctx, watched.ID, "manual", f.now); err != nil {
		t.Fatal(err)
	}

	ordinaryProject, ordinaryWorkspace := f.project, f.workspace
	f.project.ID, f.project.ProjectType = "system-project", "system"
	f.workspace.ID, f.workspace.ProjectID = "system-workspace", f.project.ID
	if err := f.database.CreateProject(f.ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if err := f.database.CreateWorkspace(f.ctx, f.workspace); err != nil {
		t.Fatal(err)
	}
	systemHidden := f.task("system-hidden", domain.TaskWaitingUser, time.Second)
	systemWatched := f.task("system-watched", domain.TaskWaitingUser, 10*time.Minute)
	if err := f.database.UpsertPetTaskWatch(f.ctx, systemWatched.ID, "manual", f.now); err != nil {
		t.Fatal(err)
	}
	f.project, f.workspace = ordinaryProject, ordinaryWorkspace

	desk := f.desk()
	want := []string{closedRecently.ID, watched.ID, systemWatched.ID, edge.ID}
	var got []string
	for _, item := range desk.Recent {
		got = append(got, item.TaskID)
	}
	if !slices.Equal(got, want) || desk.RecentTotal != len(want) || !desk.Recent[0].LastActive.Equal(stamp.Add(time.Nanosecond)) ||
		desk.Recent[0].Watched || !desk.Recent[1].Watched || !desk.Recent[2].Watched {
		t.Fatalf("recent got %v want %v; excluded %v; rows %+v", got, want, []string{tooOld.ID, draft.ID, ignored.ID, systemHidden.ID}, desk.Recent)
	}
	for i := range 34 {
		task := f.task(fmt.Sprintf("old-%02d", i), domain.TaskWaitingUser, time.Duration(90+i)*time.Minute)
		f.lifecycleTurn(task, "main", domain.TurnSucceeded, f.now.Add(-time.Duration(90+i)*time.Minute))
		f.card(task.ID, "", []string{"next"}, nil, f.now)
	}
	desk = f.desk()
	if desk.RecentTotal != 38 || len(desk.Recent) != 30 || desk.StalledTotal != 34 || len(desk.Stalled) != 30 ||
		desk.Stalled[0].TaskID != "old-33" || desk.Stalled[29].TaskID != "old-04" {
		t.Fatalf("recent %d/%d, stalled %d/%d, first/last=%+v/%+v", len(desk.Recent), desk.RecentTotal,
			len(desk.Stalled), desk.StalledTotal, desk.Stalled[0], desk.Stalled[29])
	}
}

func TestPetWorkStateStalledPermissionAndBudget(t *testing.T) {
	f := newPetDeskFixture(t)
	old := f.now.Add(-35 * time.Minute)
	const private = "private-qa"
	for _, id := range []string{private, "pet-created", "untracked", "ignored", "has-review"} {
		task := f.task(id, domain.TaskWaitingUser, time.Hour)
		f.lifecycleTurn(task, "main", domain.TurnSucceeded, old)
		f.lines(task, stallPatch(id, "self", "open", ""))
		switch id {
		case private, "has-review":
			if err := f.database.UpsertPetTaskWatch(f.ctx, task.ID, "manual", f.now); err != nil {
				t.Fatal(err)
			}
		case "ignored":
			if err := f.database.IgnorePetTaskWatch(f.ctx, task.ID, "ignore-only", f.now); err != nil {
				t.Fatal(err)
			}
		case "pet-created":
			if err := f.database.AppendPetEvent(f.ctx, domain.PetEvent{ID: "created", At: old, Actor: "pet", Kind: "task_created", Detail: task.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	review := f.task("active-review", domain.TaskActive, time.Minute)
	if err := f.database.CreatePetCodeReviewLink(f.ctx, review.ID, "has-review"); err != nil {
		t.Fatal(err)
	}
	// Force the default budget to discard real history. The large approvals
	// target other objects and do not affect which Tasks qualify as stalled.
	for i := range 8 {
		if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
			ID: fmt.Sprintf("history-%d", i), Kind: "external_channel_send", Status: "approved",
			Summary: strings.Repeat("内容", 2000), Payload: "{}", CreatedAt: old.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, canView := range []bool{false, true} {
		if _, err := f.service.UpdateOwnerPetSettings(f.ctx, PetSettingsInput{
			Enabled: true, ChatModelID: f.model.ID, Actions: []string{"walk"}, IdleMinutes: 5,
			QuietStart: 22, QuietEnd: 8, HourlyLimit: 2, AllowViewTasks: &canView,
		}); err != nil {
			t.Fatal(err)
		}
		state, err := f.service.PetWorkState(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.StalledTasks) != 2 || state.StalledTasksNote != petStalledTaskNote || !state.HistoryTruncated {
			t.Fatalf("stalled=%+v note=%q history_truncated=%v", state.StalledTasks, state.StalledTasksNote, state.HistoryTruncated)
		}
		payload, err := json.Marshal(state)
		if err != nil || len(payload) > petWorkStateDefaultBudgetBytes {
			t.Fatalf("default briefing is %d bytes, err=%v", len(payload), err)
		}
		for _, row := range state.StalledTasks {
			if row.Code == "" || row.IdleMinutes != 35 {
				t.Fatalf("identity and age = %+v", row)
			}
			if row.ID == private && !canView {
				rowJSON, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(rowJSON, &fields); err != nil || len(fields) != 3 || fields["idle_minutes"] == nil {
					t.Fatalf("private stalled row leaked: %s (%v)", rowJSON, err)
				}
				continue
			}
			if row.LineID != row.ID || row.LineTitle != "Check "+row.ID || row.Detail != "Step "+row.ID || row.Title == "" {
				t.Fatalf("visible stalled row lacks details: %+v", row)
			}
		}
	}
}

func TestTaskLifecycleWaitingSummary(t *testing.T) {
	f := newPetDeskFixture(t)
	old := f.now.Add(-30 * time.Minute)
	task := f.task("waiting-summary", domain.TaskWaitingUser, time.Hour)
	f.lifecycleTurn(task, "main", domain.TurnSucceeded, old)
	f.lines(task, stallPatch("lab", "other", "waiting", "Lab team"))
	read := func() TaskLifecycle {
		t.Helper()
		view, err := f.service.TaskLifecycle(f.ctx, task)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	if now := read().Now; now.StuckOn != "other" || !slices.Equal(now.StuckWho, []string{"Lab team"}) || !now.IdleSince.IsZero() {
		t.Fatalf("waiting for others = %+v", now)
	}
	f.lines(task, stallPatch("next", "self", "open", ""))
	if now := read().Now; now.StuckOn != "self" || len(now.StuckWho) != 0 || !now.IdleSince.Equal(old) {
		t.Fatalf("idle waiting = %+v", now)
	}
	if err := f.database.IgnorePetTaskWatch(f.ctx, task.ID, "ignore-only", f.now); err != nil {
		t.Fatal(err)
	}
	if now := read().Now; now.StuckOn != "self" || !now.IdleSince.IsZero() {
		t.Fatalf("ignored idle = %+v", now)
	}
}

func TestPetDeskStallChecksApprovalsBeyondVisibleLimit(t *testing.T) {
	f := newPetDeskFixture(t)
	task := f.task("older-approval-target", domain.TaskWaitingUser, time.Hour)
	f.lifecycleTurn(task, "main", domain.TurnSucceeded, f.now.Add(-30*time.Minute))
	f.lines(task, stallPatch("next", "self", "open", ""))
	if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
		ID: "target-approval", Status: "pending", CreatedAt: f.now.Add(-time.Hour),
		Payload: fmt.Sprintf(`{"task_id": %q}`, task.ID),
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
			ID: fmt.Sprintf("newer-%03d", i), Status: "pending", Payload: "{}", CreatedAt: f.now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if desk := f.desk(); desk.StalledTotal != 0 || desk.NeedsOwnerTotal != 200 {
		t.Fatalf("pending approval outside desk page incorrectly allowed a stall: count=%d approvals=%d",
			desk.StalledTotal, desk.NeedsOwnerTotal)
	}
}
