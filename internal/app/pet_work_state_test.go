package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPetOpenTaskCandidatesMergesCreatedAndFollowedTasks(t *testing.T) {
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	tasks := []domain.Task{
		{ID: "a-later", Status: domain.TaskActive, CreatedAt: at.Add(time.Millisecond)},
		{ID: "z-oldest", Status: domain.TaskWaitingUser, CreatedAt: at},
		{ID: "c-both", Status: domain.TaskActive, CreatedAt: at.Add(time.Second)},
		{ID: "b-created", Status: domain.TaskActive, CreatedAt: at.Add(time.Second)},
		{ID: "c-both", Status: domain.TaskActive, CreatedAt: at.Add(time.Second)},
		{ID: "unfollowed", Status: domain.TaskActive, CreatedAt: at},
		{ID: "ignored-follow", Status: domain.TaskActive, CreatedAt: at},
		{ID: "ignore-only", Status: domain.TaskActive, CreatedAt: at},
		{ID: "completed", Status: domain.TaskCompleted, CreatedAt: at},
		{ID: "cancelled", Status: domain.TaskCancelled, CreatedAt: at},
		{ID: petTaskID, Status: domain.TaskActive, CreatedAt: at},
	}
	created := []string{"b-created", "c-both", "c-both", "ignore-only", "completed", petTaskID}
	watches := map[string]domain.PetTaskWatch{
		"a-later":        {Reason: "manual"},
		"z-oldest":       {Reason: "manual"},
		"c-both":         {Reason: "created"},
		"ignored-follow": {Reason: "manual", Ignored: true},
		"ignore-only":    {Reason: petWatchReasonIgnored},
		"cancelled":      {Reason: "manual"},
		petTaskID:        {Reason: "manual"},
	}
	type source struct {
		id           string
		createdByPet bool
	}
	var got []source
	for _, task := range petOpenTaskCandidates(created, tasks, watches) {
		got = append(got, source{task.ID, task.createdByPet})
	}
	want := []source{{"z-oldest", false}, {"a-later", false}, {"b-created", true}, {"c-both", true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered open-task sources = %v, want %v", got, want)
	}
}

func TestPetWorkStateShowsInspectionAndAllOpenTasks(t *testing.T) {
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
	fixture.service.SetBuildInfo("0.5.999", "0.5.999", time.Now().UTC().Add(-5*time.Minute))
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "巡检项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "巡检 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	openTask, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "等待主人的巡检任务",
		Request: "只读核对", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, openTask.ID, domain.TaskWaitingUser)
	completedTask, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "已经关闭的巡检任务",
		Request: "只读核对", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, completedTask.ID, domain.TaskWaitingUser)
	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, completedTask.ID, domain.TaskWaitingUser, domain.TaskCompleted, timeString(now), timeString(now)); err != nil {
		t.Fatal(err)
	}
	ignoredTask, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "已搁置的巡检任务",
		Request: "只读核对", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, ignoredTask.ID, domain.TaskWaitingUser)
	ignoredAt := now.Add(-time.Hour)
	for _, id := range []string{ignoredTask.ID, completedTask.ID} {
		if err := fixture.database.IgnorePetTaskWatch(fixture.ctx, id, petWatchReasonIgnored, ignoredAt); err != nil {
			t.Fatal(err)
		}
	}

	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Inspection.Health != "ok" || state.Inspection.Version != "0.5.999" || state.Inspection.WebVersion != "0.5.999" {
		t.Fatalf("inspection identity=%#v", state.Inspection)
	}
	if state.Inspection.TasksTotal != 3 || state.Inspection.WaitingUser != 2 || state.Inspection.Completed != 1 {
		t.Fatalf("inspection counts=%#v", state.Inspection)
	}
	if state.Inspection.OpenTasks != 1 || state.Inspection.AttentionTasks != 1 {
		t.Fatalf("inspection open/attention=%#v", state.Inspection)
	}
	if len(state.OpenTasks) != 1 || state.OpenTasks[0].ID != openTask.ID || !state.OpenTasks[0].NeedsAttention {
		t.Fatalf("open tasks=%#v", state.OpenTasks)
	}
	wantIgnored := PetIgnoredTask{ID: ignoredTask.ID, Code: ignoredTask.Code, Title: ignoredTask.Title, Status: domain.TaskWaitingUser, IgnoredAt: ignoredAt}
	if len(state.IgnoredTasks) != 1 || state.IgnoredTasks[0] != wantIgnored {
		t.Fatalf("ignored tasks=%+v want=%+v", state.IgnoredTasks, wantIgnored)
	}
	t.Run("overview", func(t *testing.T) {
		waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
		result, err := fixture.service.PetMCPTool(fixture.ctx, "aha_overview", nil)
		if err != nil {
			t.Fatal(err)
		}
		var overview PetAHAOverview
		if err := json.Unmarshal([]byte(result), &overview); err != nil {
			t.Fatal(err)
		}
		if len(overview.Attention) != 1 || overview.Attention[0].ID != openTask.ID {
			t.Fatalf("overview attention=%+v, want only followed Task %s", overview.Attention, openTask.ID)
		}
		if overview.WaitingUser != 3 {
			t.Fatalf("overview waiting_user=%d, want the followed, ignored, and pet Tasks in the global count", overview.WaitingUser)
		}
	})
	if reason := state.OpenTasks[0].AttentionReason; reason != "waiting_user：等待下一步指令" {
		t.Fatalf("succeeded waiting task should await the next message, got %q", reason)
	}
	for _, item := range state.OpenTasks {
		if item.ID == completedTask.ID {
			t.Fatalf("completed task leaked into open tasks: %#v", state.OpenTasks)
		}
	}

	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "owner_decision", Title: "巡检任务需要决定", Summary: "继续还是取消", Risk: "medium",
		Payload: `{"task_id":"` + openTask.ID + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.PendingApprovals != 1 || state.Inspection.PendingApprovals != 1 {
		t.Fatalf("pending approvals=%#v", state.Inspection)
	}
	if len(state.OpenTasks) != 1 || state.OpenTasks[0].PendingApprovalID != approval.ID || state.OpenTasks[0].NeedsAttention {
		t.Fatalf("open task approval link=%#v", state.OpenTasks)
	}
	if reason := state.OpenTasks[0].AttentionReason; reason != "已提交待审批 "+approval.ID {
		t.Fatalf("pending owner decision should keep the approval wording, got %q", reason)
	}
	if state.Inspection.AttentionTasks != 0 {
		t.Fatalf("attention should clear after approval: %#v", state.Inspection)
	}
}

func TestPetWorkStateLabelsWaitingSourceUnderReview(t *testing.T) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "评审项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "评审 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "等待评审的实现任务",
		Request: "实现后等待独立评审", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
	review, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "代码评审：" + source.Code,
		Request: "独立评审源任务改动", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreatePetCodeReviewLink(fixture.ctx, review.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, review.Status, domain.TaskPreparing, timeString(now), ""); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskPreparing, domain.TaskActive, timeString(now), ""); err != nil {
		t.Fatal(err)
	}

	openTask := func(state PetWorkState, id string) (PetOpenTask, bool) {
		for _, item := range state.OpenTasks {
			if item.ID == id {
				return item, true
			}
		}
		return PetOpenTask{}, false
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := openTask(state, source.ID)
	if !ok || item.AttentionReason != "waiting_user：评审中 "+review.Code || !item.NeedsAttention {
		t.Fatalf("in-flight review should label the source: %#v", state.OpenTasks)
	}
	if reviewItem, ok := openTask(state, review.ID); !ok || reviewItem.AttentionReason != "" {
		t.Fatalf("running review should have no attention reason: %#v", state.OpenTasks)
	}
	if state.Inspection.AttentionTasks != 1 {
		t.Fatalf("review wording must not change the attention total: %#v", state.Inspection)
	}

	// A review that already stopped running is no longer "under review"; the
	// task-613 wording applies again.
	now = fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskActive, domain.TaskCompleted, timeString(now), timeString(now)); err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	item, ok = openTask(state, source.ID)
	if !ok || item.AttentionReason != "waiting_user：等待下一步指令" || !item.NeedsAttention {
		t.Fatalf("completed review must fall back to the next-step wording: %#v", state.OpenTasks)
	}

	// Reopen and run the review again, then add a pending approval: the
	// approval wording still wins over the review wording.
	now = fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskCompleted, domain.TaskWaitingUser, timeString(now), ""); err != nil {
		t.Fatal(err)
	}
	now = fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskWaitingUser, domain.TaskActive, timeString(now), ""); err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "owner_decision", Title: "实现任务需要决定", Summary: "继续还是取消", Risk: "medium",
		Payload: `{"task_id":"` + source.ID + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	item, ok = openTask(state, source.ID)
	if !ok || item.AttentionReason != "已提交待审批 "+approval.ID || item.NeedsAttention {
		t.Fatalf("pending approval must win over review wording: %#v", state.OpenTasks)
	}
	if state.Inspection.AttentionTasks != 0 {
		t.Fatalf("approval should clear the source attention: %#v", state.Inspection)
	}
}

func TestPetWorkStateReviewLookupIsBatched(t *testing.T) {
	var queryMu sync.Mutex
	reviewQueries := 0
	var counting atomic.Bool
	store.SetSQLQueryObserverForTest(func(query string) {
		if !counting.Load() || !strings.Contains(query, "pet_code_reviews") {
			return
		}
		queryMu.Lock()
		reviewQueries++
		queryMu.Unlock()
	})
	t.Cleanup(func() { store.SetSQLQueryObserverForTest(nil) })
	fixture := newPetTaskFixture(t)
	// Connections opened by the fixture captured the observer above; don't hand
	// it to any later Store in this test.
	store.SetSQLQueryObserverForTest(nil)
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "批量评审项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "批量评审 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	createWaiting := func(title string) domain.Task {
		task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
			ProjectID: project.ID, WorkspaceID: workspace.ID, Title: title,
			Request: "实现后等待独立评审", Start: boolPointer(true),
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
		return task
	}
	linkReview := func(source domain.Task, status domain.TaskStatus) domain.Task {
		review, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
			ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "代码评审：" + source.Code,
			Request: "独立评审源任务改动", Start: boolPointer(false),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.database.CreatePetCodeReviewLink(fixture.ctx, review.ID, source.ID); err != nil {
			t.Fatal(err)
		}
		now := fixture.service.now().UTC()
		if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskDraft, domain.TaskPreparing, timeString(now), ""); err != nil {
			t.Fatal(err)
		}
		if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskPreparing, domain.TaskActive, timeString(now), ""); err != nil {
			t.Fatal(err)
		}
		if status == domain.TaskCompleted {
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, review.ID, domain.TaskActive, domain.TaskCompleted, timeString(now), timeString(now)); err != nil {
				t.Fatal(err)
			}
		}
		return review
	}
	countReviewQueries := func() int {
		queryMu.Lock()
		reviewQueries = 0
		queryMu.Unlock()
		counting.Store(true)
		_, err := fixture.service.PetWorkState(fixture.ctx)
		counting.Store(false)
		if err != nil {
			t.Fatal(err)
		}
		queryMu.Lock()
		defer queryMu.Unlock()
		return reviewQueries
	}

	// Four waiting sources: no link, in-flight review, completed review, and a
	// pending approval. The old per-source lookup issued one query for each;
	// the batched read must issue exactly one for the whole briefing.
	createWaiting("无关联评审")
	activeSource := createWaiting("在途评审")
	linkReview(activeSource, domain.TaskActive)
	completedSource := createWaiting("评审已结束")
	linkReview(completedSource, domain.TaskCompleted)
	approvalSource := createWaiting("等待审批")
	linkReview(approvalSource, domain.TaskActive)
	if _, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "owner_decision", Title: "实现任务需要决定", Summary: "继续还是取消", Risk: "medium",
		Payload: `{"task_id":"` + approvalSource.ID + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if got := countReviewQueries(); got != 1 {
		t.Fatalf("review link queries=%d want 1 for four waiting tasks", got)
	}

	// Adding more waiting sources, including another linked one, must not add
	// queries: the batch read is independent of the number of waiting Tasks.
	for i := 0; i < 4; i++ {
		createWaiting(fmt.Sprintf("额外等待任务 %d", i))
	}
	extraLinked := createWaiting("额外在途评审")
	linkReview(extraLinked, domain.TaskActive)
	if got := countReviewQueries(); got != 1 {
		t.Fatalf("review link queries=%d want 1 after adding waiting tasks", got)
	}
}

func TestPetOpenTaskAttentionReasonSeparatesWaitingKinds(t *testing.T) {
	const (
		awaitingDecision = "waiting_user：等待 owner 决定或补充信息"
		awaitingNext     = "waiting_user：等待下一步指令"
		underReview      = "waiting_user：评审中 task-777"
	)
	for _, tc := range []struct {
		name     string
		status   domain.TaskStatus
		lastTurn domain.TurnStatus
		review   string
		want     string
	}{
		{"succeeded turn awaits next message", domain.TaskWaitingUser, domain.TurnSucceeded, "", awaitingNext},
		{"failed turn needs owner decision", domain.TaskWaitingUser, domain.TurnFailed, "", awaitingDecision},
		{"interrupted turn needs owner decision", domain.TaskWaitingUser, domain.TurnInterrupted, "", awaitingDecision},
		{"no turn recorded keeps owner decision", domain.TaskWaitingUser, "", "", awaitingDecision},
		{"in-flight review wins over next instruction", domain.TaskWaitingUser, domain.TurnSucceeded, "task-777", underReview},
		{"in-flight review wins over owner decision", domain.TaskWaitingUser, domain.TurnFailed, "task-777", underReview},
		{"blocked unchanged", domain.TaskBlocked, domain.TurnSucceeded, "", "blocked：需要解除阻塞条件"},
		{"failed unchanged", domain.TaskFailed, domain.TurnSucceeded, "", "failed：需要重试或决定是否继续"},
		{"blocked ignores review label", domain.TaskBlocked, domain.TurnSucceeded, "task-777", "blocked：需要解除阻塞条件"},
		{"active has no attention reason", domain.TaskActive, domain.TurnSucceeded, "", ""},
		{"active ignores review label", domain.TaskActive, domain.TurnSucceeded, "task-777", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := petOpenTaskAttentionReason(tc.status, tc.lastTurn, tc.review); got != tc.want {
				t.Fatalf("reason=%q want %q", got, tc.want)
			}
		})
	}
}

func TestPetInspectionChannelHealthClassification(t *testing.T) {
	now := time.Now().UTC()
	channel := func(id, name, status string) domain.ChannelInstance {
		return domain.ChannelInstance{ID: id, Name: name, Status: status, EffectiveAvailability: "available"}
	}
	healthy := channel("channel-healthy", "健康渠道", "ready")
	newInspection := func() PetInspection {
		return PetInspection{Health: "ok", Note: "没有未闭环任务。"}
	}
	apply := func(channel domain.ChannelInstance, row domain.ChannelOutboxHealth) PetInspection {
		inspection := newInspection()
		outbox := map[string]domain.ChannelOutboxHealth{}
		if row.InstanceID != "" {
			outbox[channel.ID] = row
		}
		applyPetChannelHealthValues(&inspection, []domain.ChannelInstance{channel}, outbox, now, false)
		return inspection
	}

	t.Run("healthy", func(t *testing.T) {
		inspection := apply(healthy, domain.ChannelOutboxHealth{})
		if inspection.Health != "ok" || len(inspection.DegradedChannels) != 0 || len(inspection.Channels) != 1 || inspection.ChannelsTotal != 1 {
			t.Fatalf("healthy inspection=%#v", inspection)
		}
		if inspection.Channels[0].Degraded || inspection.Channels[0].Note != "" || inspection.Channels[0].EffectiveAvailability != "available" {
			t.Fatalf("healthy channel=%#v", inspection.Channels[0])
		}
		if inspection.Note != "没有未闭环任务。" {
			t.Fatalf("healthy note changed: %q", inspection.Note)
		}
	})

	t.Run("unattempted_pending_threshold_boundary", func(t *testing.T) {
		for _, scenario := range []struct {
			name     string
			age      time.Duration
			degraded bool
		}{
			{"just_under", petChannelStuckPendingThreshold - time.Second, false},
			{"exactly_at", petChannelStuckPendingThreshold, true},
			{"just_over", petChannelStuckPendingThreshold + time.Second, true},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				candidate := channel("channel-attempted", "尝试渠道", "ready")
				inspection := apply(candidate, domain.ChannelOutboxHealth{
					InstanceID: candidate.ID, Pending: 1, StuckPending: 1,
					OldestPendingAt:            now.Add(-scenario.age),
					OldestUnattemptedPendingAt: now.Add(-scenario.age),
				})
				gotDegraded := inspection.Health == "degraded" && inspection.Channels[0].Degraded
				if gotDegraded != scenario.degraded {
					t.Fatalf("age=%v degraded=%v inspection=%#v", scenario.age, gotDegraded, inspection)
				}
			})
		}
	})

	t.Run("attempted_pending_threshold_boundary", func(t *testing.T) {
		for _, scenario := range []struct {
			name     string
			age      time.Duration
			degraded bool
		}{
			{"just_under", petChannelAttemptedPendingThreshold - time.Second, false},
			{"exactly_at", petChannelAttemptedPendingThreshold, true},
			{"just_over", petChannelAttemptedPendingThreshold + time.Second, true},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				candidate := channel("channel-retrying", "重试渠道", "ready")
				inspection := apply(candidate, domain.ChannelOutboxHealth{
					InstanceID: candidate.ID, Pending: 1, AttemptedPending: 1,
					OldestPendingAt:          now.Add(-scenario.age),
					OldestAttemptedPendingAt: now.Add(-scenario.age),
				})
				gotDegraded := inspection.Health == "degraded" && inspection.Channels[0].Degraded
				if gotDegraded != scenario.degraded {
					t.Fatalf("age=%v degraded=%v inspection=%#v", scenario.age, gotDegraded, inspection)
				}
			})
		}
	})

	t.Run("queued_behind_retry_does_not_use_five_minutes", func(t *testing.T) {
		candidate := channel("channel-queued", "排队渠道", "ready")
		inspection := apply(candidate, domain.ChannelOutboxHealth{
			InstanceID: candidate.ID, Pending: 2, QueuedPending: 1, AttemptedPending: 1,
			OldestPendingAt:          now.Add(-10 * time.Minute),
			OldestQueuedPendingAt:    now.Add(-10 * time.Minute),
			OldestAttemptedPendingAt: now.Add(-10 * time.Minute),
		})
		if inspection.Health != "ok" || inspection.Channels[0].Degraded {
			t.Fatalf("queued row behind retry degraded too early: %#v", inspection)
		}
	})

	t.Run("queued_pending_threshold_boundary", func(t *testing.T) {
		for _, scenario := range []struct {
			name     string
			age      time.Duration
			degraded bool
		}{
			{"just_under", petChannelAttemptedPendingThreshold - time.Second, false},
			{"exactly_at", petChannelAttemptedPendingThreshold, true},
			{"just_over", petChannelAttemptedPendingThreshold + time.Second, true},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				candidate := channel("channel-queued", "排队渠道", "ready")
				inspection := apply(candidate, domain.ChannelOutboxHealth{
					InstanceID: candidate.ID, Pending: 1, QueuedPending: 1,
					OldestPendingAt:       now.Add(-scenario.age),
					OldestQueuedPendingAt: now.Add(-scenario.age),
				})
				gotDegraded := inspection.Health == "degraded" && inspection.Channels[0].Degraded
				if gotDegraded != scenario.degraded {
					t.Fatalf("age=%v degraded=%v inspection=%#v", scenario.age, gotDegraded, inspection)
				}
				if scenario.degraded && !strings.Contains(inspection.Note, "排队等待前序消息 1 条") {
					t.Fatalf("queued note=%q", inspection.Note)
				}
			})
		}
	})

	t.Run("backlog_boundary", func(t *testing.T) {
		for _, scenario := range []struct {
			pending  int
			degraded bool
		}{
			{petChannelPendingBacklog - 1, false},
			{petChannelPendingBacklog, true},
		} {
			candidate := channel("channel-backlog", "堆积渠道", "ready")
			inspection := apply(candidate, domain.ChannelOutboxHealth{
				InstanceID: candidate.ID, Pending: scenario.pending, StuckPending: scenario.pending,
				OldestPendingAt: now, OldestUnattemptedPendingAt: now,
			})
			gotDegraded := inspection.Health == "degraded"
			if gotDegraded != scenario.degraded {
				t.Fatalf("pending=%d degraded=%v inspection=%#v", scenario.pending, gotDegraded, inspection)
			}
			if scenario.degraded && !strings.Contains(inspection.Note, fmt.Sprintf("pending 堆积 %d 条", scenario.pending)) {
				t.Fatalf("backlog note=%q", inspection.Note)
			}
		}
	})

	t.Run("expired_lease", func(t *testing.T) {
		candidate := channel("channel-lease", "租约渠道", "ready")
		inspection := apply(candidate, domain.ChannelOutboxHealth{
			InstanceID: candidate.ID, ExpiredLeases: 1, OldestExpiredLeaseAt: now.Add(-2 * time.Minute),
		})
		if inspection.Health != "degraded" || !inspection.Channels[0].Degraded || inspection.Channels[0].ExpiredLeases != 1 {
			t.Fatalf("expired lease inspection=%#v", inspection)
		}
		for _, want := range []string{"过期租约 1 条", "最久 2 分钟"} {
			if !strings.Contains(inspection.Note, want) {
				t.Fatalf("expired lease note %q missing %q", inspection.Note, want)
			}
		}
	})

	t.Run("dead_letter", func(t *testing.T) {
		candidate := channel("channel-dead", "死信渠道", "ready")
		inspection := apply(candidate, domain.ChannelOutboxHealth{
			InstanceID: candidate.ID, DeadLetter: 1, LastErrorCode: "feishu_target_unavailable",
		})
		if inspection.Health != "degraded" || len(inspection.DegradedChannels) != 1 {
			t.Fatalf("dead letter inspection=%#v", inspection)
		}
		channel := inspection.Channels[0]
		if !channel.Degraded || channel.DeadLetter != 1 || channel.LastErrorCode != "feishu_target_unavailable" {
			t.Fatalf("dead letter channel=%#v", channel)
		}
		for _, want := range []string{"dead_letter 1", "feishu_target_unavailable", "渠道状态异常"} {
			if !strings.Contains(inspection.Note, want) {
				t.Fatalf("dead letter note %q missing %q", inspection.Note, want)
			}
		}
		if strings.Contains(inspection.Note, "消息正文") {
			t.Fatalf("note leaked message text: %q", inspection.Note)
		}
	})

	t.Run("instance_status_variants", func(t *testing.T) {
		for _, status := range []string{"error", "degraded", "draft", "onboarding"} {
			candidate := channel("channel-"+status, status, status)
			inspection := apply(candidate, domain.ChannelOutboxHealth{})
			if inspection.Health != "degraded" || !strings.Contains(inspection.Note, "实例状态 "+status) {
				t.Fatalf("status=%s inspection=%#v", status, inspection)
			}
		}
		disabled := channel("channel-disabled", "已停用", "disabled")
		if inspection := apply(disabled, domain.ChannelOutboxHealth{}); inspection.Health != "ok" {
			t.Fatalf("disabled channel should stay healthy: %#v", inspection)
		}
	})

	t.Run("outbox_read_failure", func(t *testing.T) {
		inspection := newInspection()
		applyPetChannelHealthValues(&inspection, []domain.ChannelInstance{healthy}, nil, now, true)
		if inspection.Health != "degraded" || len(inspection.DegradedChannels) != 1 || !strings.Contains(inspection.Note, "出站队列状态读取失败") {
			t.Fatalf("outbox failure inspection=%#v", inspection)
		}
	})

	t.Run("no_channel", func(t *testing.T) {
		inspection := newInspection()
		applyPetChannelHealthValues(&inspection, nil, nil, now, false)
		if inspection.Health != "ok" || len(inspection.Channels) != 0 || len(inspection.DegradedChannels) != 0 {
			t.Fatalf("no channel inspection=%#v", inspection)
		}
		if inspection.Note != "没有未闭环任务。" {
			t.Fatalf("no channel note changed: %q", inspection.Note)
		}
	})
}

func TestPetChannelHealthSortsDegradedFirstAndCapsRendering(t *testing.T) {
	now := time.Now().UTC()
	channels := make([]domain.ChannelInstance, 0, 25)
	outbox := map[string]domain.ChannelOutboxHealth{}
	for i := 0; i < 22; i++ {
		id := fmt.Sprintf("healthy-%02d", i)
		channels = append(channels, domain.ChannelInstance{ID: id, Name: "a-" + id, Status: "ready"})
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("degraded-%02d", i)
		channels = append(channels, domain.ChannelInstance{ID: id, Name: "z-" + id, Status: "ready"})
		outbox[id] = domain.ChannelOutboxHealth{InstanceID: id, DeadLetter: 1, LastErrorCode: "feishu_target_unavailable"}
	}

	health, degraded, total := buildPetChannelHealth(channels, outbox, now)
	if total != 25 || len(health) != petChannelHealthListLimit || len(degraded) != 3 {
		t.Fatalf("total=%d shown=%d degraded=%d", total, len(health), len(degraded))
	}
	for index := 0; index < 3; index++ {
		if !health[index].Degraded || !strings.HasPrefix(health[index].ID, "degraded-") {
			t.Fatalf("degraded rows not sorted first at %d: %#v", index, health)
		}
	}

	allDegraded := map[string]domain.ChannelOutboxHealth{}
	for _, item := range channels {
		allDegraded[item.ID] = domain.ChannelOutboxHealth{InstanceID: item.ID, DeadLetter: 1}
	}
	health, degraded, total = buildPetChannelHealth(channels, allDegraded, now)
	if total != 25 || len(health) != petChannelHealthListLimit || len(degraded) != 25 {
		t.Fatalf("all degraded total=%d shown=%d degraded=%d", total, len(health), len(degraded))
	}
}

func TestPetWorkStateDefaultBudgetAndExpansion(t *testing.T) {
	fixture := newPetTaskFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.service.OwnerAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()

	// The measured payload had 20 approvals and 20 work rows whose
	// last_turn_result dominated the size. Reproduce both here.
	for i := 0; i < 20; i++ {
		if _, err := fixture.service.CreatePetApproval(ctx, PetApprovalInput{
			Kind:    "external_channel_send",
			Title:   fmt.Sprintf("审批 %02d", i),
			Summary: fmt.Sprintf("%02d %s", i, strings.Repeat("审批说明", 30)),
			Reason:  strings.Repeat("需要主人决定", 10),
			Risk:    "high",
			Payload: fmt.Sprintf(`{"task_id":"task-%02d","note":"%s"}`, i, strings.Repeat("x", 120)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	project := domain.Project{
		ID: "project-work-state-budget", Name: "预算项目", ProjectType: "user",
		KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-work-state-budget", ProjectID: project.ID, Name: "预算 Workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	// Keep a pet-created Task and a followed Task open through all budget and
	// history options.
	openTask, err := fixture.service.CreateTask(ctx, CreateTaskInput{
		ID: "task-work-state-open", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "未闭环任务", Request: "等待主人决定", ModelID: fixture.model.ID,
		Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	openTaskResult := "未闭环任务的最新结果 " + strings.Repeat("结果", 80)
	openMessage := domain.Message{
		ID: domain.NewID("message"), TaskID: openTask.ID, Role: "user",
		Sender: "owner", Content: "开始", CreatedAt: now,
	}
	if err := fixture.database.AddMessage(ctx, openMessage); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateTurn(ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: openTask.ID, AgentID: "main", Sequence: 1,
		InputMessageID: openMessage.ID, Status: domain.TurnSucceeded,
		RuntimeConfigSnapshotID: openTask.RuntimeConfigSnapshotID,
		Result:                  openTaskResult, QueuedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// task_created marks the Task as pet-owned (open_tasks); keep it older than
	// the 20 task_started rows so it stays out of the default 8-row history.
	if err := fixture.database.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now.Add(-30 * time.Second),
		Actor: "pet", Kind: "task_created", Detail: openTask.ID, Status: string(openTask.Status),
	}); err != nil {
		t.Fatal(err)
	}

	const followedTaskID = "task-work-state-19"
	longResult := strings.Repeat("长结果", 900)
	for i := 0; i < 20; i++ {
		task, err := fixture.service.CreateTask(ctx, CreateTaskInput{
			ID:        fmt.Sprintf("task-work-state-%02d", i),
			ProjectID: project.ID, WorkspaceID: workspace.ID,
			Title: fmt.Sprintf("工作动态 %02d", i), Request: "只读核对",
			ModelID: fixture.model.ID, Isolation: "inplace",
			Filesystem: "workspace-write", Approval: "never",
			CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
		})
		if err != nil {
			t.Fatal(err)
		}
		message := domain.Message{
			ID: domain.NewID("message"), TaskID: task.ID, Role: "user",
			Sender: "owner", Content: "开始", CreatedAt: now,
		}
		if err := fixture.database.AddMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
		if err := fixture.database.CreateTurn(ctx, domain.Turn{
			ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "main", Sequence: 1,
			InputMessageID: message.ID, Status: domain.TurnSucceeded,
			RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
			Result:                  longResult, QueuedAt: now, FinishedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		// A task_started event alone is history only. Give one of these Tasks
		// an explicit watch so it also appears in open_tasks.
		if task.ID == followedTaskID {
			if err := fixture.database.UpsertPetTaskWatch(ctx, task.ID, "manual", now); err != nil {
				t.Fatal(err)
			}
		}
		if err := fixture.database.AppendPetEvent(ctx, domain.PetEvent{
			ID: domain.NewID("pet_event"), At: now.Add(time.Duration(i+1) * time.Second),
			Actor: "pet", Kind: "task_started", Detail: task.ID, Status: "started",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := fixture.service.PetUpdateCore(ctx, "loops", "# 开放闭环\n\n- [waiting] 预算检查\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(ctx, "agenda", "# 议程\n\n- 预算检查\n"); err != nil {
		t.Fatal(err)
	}

	// Freeze the clock so the default and history_limit=0 calls produce
	// identical derived inspection/open_task fields.
	clock := fixture.service.now().UTC()
	fixture.service.now = func() time.Time { return clock }

	raw, err := fixture.service.PetMCPTool(ctx, "aha_pet_work_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(raw)) {
		t.Fatalf("default work_state is not valid JSON: %s", raw)
	}
	if len(raw) > petWorkStateDefaultBudgetBytes {
		t.Fatalf("default work_state = %d bytes, want <= %d", len(raw), petWorkStateDefaultBudgetBytes)
	}
	var defaultState PetWorkState
	if err := json.Unmarshal([]byte(raw), &defaultState); err != nil {
		t.Fatal(err)
	}
	if defaultState.PendingApprovals != 20 {
		t.Fatalf("pending approvals = %d", defaultState.PendingApprovals)
	}
	if len(defaultState.Approvals) != petWorkStateDefaultHistoryLimit {
		t.Fatalf("default approvals = %d, want %d", len(defaultState.Approvals), petWorkStateDefaultHistoryLimit)
	}
	if len(defaultState.Work) != petWorkStateDefaultHistoryLimit {
		t.Fatalf("default work rows = %d, want %d", len(defaultState.Work), petWorkStateDefaultHistoryLimit)
	}
	for _, item := range defaultState.Work {
		if item.LastTurnResult != "" {
			t.Fatalf("default work row leaked last_turn_result: %#v", item)
		}
	}
	var defaultPayload map[string]any
	if err := json.Unmarshal([]byte(raw), &defaultPayload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"inspection", "open_tasks", "core", "open_loops", "agenda"} {
		if _, ok := defaultPayload[key]; !ok {
			t.Fatalf("default work_state missing %q: %s", key, raw)
		}
	}
	if work, ok := defaultPayload["work"].([]any); !ok || len(work) == 0 {
		t.Fatalf("default work payload = %#v", defaultPayload["work"])
	} else if _, exists := work[0].(map[string]any)["last_turn_result"]; exists {
		t.Fatalf("default work JSON still has last_turn_result: %#v", work[0])
	}
	if defaultState.HistoryTruncated {
		t.Fatalf("history_truncated set although the default history fits the budget")
	}
	if len(defaultState.OpenTasks) != 2 || defaultState.OpenTasks[0].ID != openTask.ID || !defaultState.OpenTasks[0].CreatedByPet ||
		defaultState.OpenTasks[1].ID != followedTaskID || defaultState.OpenTasks[1].CreatedByPet {
		t.Fatalf("default open_tasks = %#v", defaultState.OpenTasks)
	}
	if defaultState.OpenTasks[0].LastTurnResult != "" {
		t.Fatalf("default open task leaked last_turn_result: %#v", defaultState.OpenTasks[0])
	}

	// Dropping the history lists must not alter any fixed field.
	rawNoHistory, err := fixture.service.PetMCPTool(ctx, "aha_pet_work_state", json.RawMessage(`{"history_limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	var noHistory PetWorkState
	if err := json.Unmarshal([]byte(rawNoHistory), &noHistory); err != nil {
		t.Fatal(err)
	}
	if len(noHistory.Approvals) != 0 || len(noHistory.Work) != 0 {
		t.Fatalf("history_limit=0 returned history: approvals=%d work=%d", len(noHistory.Approvals), len(noHistory.Work))
	}
	if !reflect.DeepEqual(defaultState.Inspection, noHistory.Inspection) {
		t.Fatalf("inspection changed with history: %#v != %#v", defaultState.Inspection, noHistory.Inspection)
	}
	if !reflect.DeepEqual(defaultState.OpenTasks, noHistory.OpenTasks) {
		t.Fatalf("open_tasks changed with history: %#v != %#v", defaultState.OpenTasks, noHistory.OpenTasks)
	}
	if !reflect.DeepEqual(defaultState.Core, noHistory.Core) {
		t.Fatalf("core changed with history: %#v != %#v", defaultState.Core, noHistory.Core)
	}
	if defaultState.OpenLoops != noHistory.OpenLoops || defaultState.OpenLoopsPath != noHistory.OpenLoopsPath || defaultState.OpenLoopsNote != noHistory.OpenLoopsNote {
		t.Fatalf("open_loops changed with history: %#v != %#v", defaultState.OpenLoops, noHistory.OpenLoops)
	}
	if defaultState.Agenda != noHistory.Agenda || defaultState.AgendaPath != noHistory.AgendaPath || defaultState.AgendaNote != noHistory.AgendaNote {
		t.Fatalf("agenda changed with history: %#v != %#v", defaultState.Agenda, noHistory.Agenda)
	}

	raw, err = fixture.service.PetMCPTool(ctx, "aha_pet_work_state",
		json.RawMessage(`{"history_limit":50,"include_turn_results":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var expanded PetWorkState
	if err := json.Unmarshal([]byte(raw), &expanded); err != nil {
		t.Fatal(err)
	}
	if len(expanded.Approvals) != 20 {
		t.Fatalf("expanded approvals = %d, want 20", len(expanded.Approvals))
	}
	// 20 task_started rows plus the older task_created row for the open Task.
	if len(expanded.Work) != 21 {
		t.Fatalf("expanded work rows = %d, want 21", len(expanded.Work))
	}
	fullResults := 0
	openResultReturned := false
	for _, item := range expanded.Work {
		if item.TargetID == openTask.ID {
			openResultReturned = item.LastTurnResult == openTaskResult
			continue
		}
		if item.LastTurnResult == longResult {
			fullResults++
		}
	}
	if fullResults != 20 {
		t.Fatalf("expanded work rows with full result = %d, want 20", fullResults)
	}
	if !openResultReturned {
		t.Fatalf("expanded open-task work row missing full result: %#v", expanded.Work)
	}
	if len(expanded.OpenTasks) != 2 || expanded.OpenTasks[0].ID != openTask.ID || expanded.OpenTasks[0].LastTurnResult != openTaskResult ||
		expanded.OpenTasks[1].ID != followedTaskID || expanded.OpenTasks[1].LastTurnResult != longResult {
		t.Fatalf("expanded open_tasks = %#v", expanded.OpenTasks)
	}

	raw, err = fixture.service.PetMCPTool(ctx, "aha_pet_work_state",
		json.RawMessage(`{"history_limit":50,"include_turn_results":true,"turn_result_chars":120}`))
	if err != nil {
		t.Fatal(err)
	}
	var truncated PetWorkState
	if err := json.Unmarshal([]byte(raw), &truncated); err != nil {
		t.Fatal(err)
	}
	if len(truncated.Work) != 21 {
		t.Fatalf("truncated work rows = %d, want 21", len(truncated.Work))
	}
	wantTruncated := truncateAssistantText(longResult, 120)
	if wantTruncated == "" || len([]rune(wantTruncated)) != 120 {
		t.Fatalf("truncateAssistantText(longResult, 120) = %q (%d runes)", wantTruncated, len([]rune(wantTruncated)))
	}
	startedRows := 0
	for _, item := range truncated.Work {
		if item.TargetID == openTask.ID {
			if want := truncateAssistantText(openTaskResult, 120); item.LastTurnResult != want {
				t.Fatalf("open task result = %q, want %q", item.LastTurnResult, want)
			}
			continue
		}
		if item.LastTurnResult != wantTruncated {
			t.Fatalf("turn result = %q, want %q", item.LastTurnResult, wantTruncated)
		}
		startedRows++
	}
	if startedRows != 20 {
		t.Fatalf("truncated started work rows = %d, want 20", startedRows)
	}
	if len(truncated.OpenTasks) != 2 || truncated.OpenTasks[0].LastTurnResult != truncateAssistantText(openTaskResult, 120) ||
		truncated.OpenTasks[1].LastTurnResult != wantTruncated {
		t.Fatalf("truncated open_tasks = %#v", truncated.OpenTasks)
	}

	// Oversized approvals must still respect the default result budget.
	fixture.service.now = time.Now
	for i := 0; i < 20; i++ {
		if _, err := fixture.service.CreatePetApproval(ctx, PetApprovalInput{
			Kind:    "owner_decision",
			Title:   fmt.Sprintf("大审批 %02d", i),
			Summary: strings.Repeat("巨额审批说明", 100),
			Reason:  strings.Repeat("理由", 50),
			Risk:    "high",
			Payload: fmt.Sprintf(`{"note":"%s"}`, strings.Repeat("y", 200)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	clock = fixture.service.now().UTC()
	fixture.service.now = func() time.Time { return clock }

	raw, err = fixture.service.PetMCPTool(ctx, "aha_pet_work_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > petWorkStateDefaultBudgetBytes {
		t.Fatalf("oversized default work_state = %d bytes, want <= %d", len(raw), petWorkStateDefaultBudgetBytes)
	}
	var budgeted PetWorkState
	if err := json.Unmarshal([]byte(raw), &budgeted); err != nil {
		t.Fatal(err)
	}
	if !budgeted.HistoryTruncated {
		t.Fatalf("history_truncated not set for oversized approvals (approvals=%d work=%d)", len(budgeted.Approvals), len(budgeted.Work))
	}
	if len(budgeted.Approvals) == 0 || len(budgeted.Approvals) >= petWorkStateDefaultHistoryLimit {
		t.Fatalf("budget approvals = %d, want a non-empty trimmed prefix", len(budgeted.Approvals))
	}
	if len(budgeted.Work) == 0 || len(budgeted.Work) >= petWorkStateDefaultHistoryLimit {
		t.Fatalf("budget work rows = %d, want a non-empty trimmed prefix", len(budgeted.Work))
	}
	if len(budgeted.OpenTasks) != 2 || budgeted.OpenTasks[0].ID != openTask.ID || !budgeted.OpenTasks[0].CreatedByPet ||
		budgeted.OpenTasks[1].ID != followedTaskID || budgeted.OpenTasks[1].CreatedByPet {
		t.Fatalf("budget open_tasks = %#v", budgeted.OpenTasks)
	}

	// Fixed fields must survive budget trimming byte-for-byte.
	rawNoHistory, err = fixture.service.PetMCPTool(ctx, "aha_pet_work_state", json.RawMessage(`{"history_limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	var trimmedNoHistory PetWorkState
	if err := json.Unmarshal([]byte(rawNoHistory), &trimmedNoHistory); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(budgeted.Inspection, trimmedNoHistory.Inspection) {
		t.Fatalf("budget inspection = %#v, want %#v", budgeted.Inspection, trimmedNoHistory.Inspection)
	}
	if !reflect.DeepEqual(budgeted.OpenTasks, trimmedNoHistory.OpenTasks) {
		t.Fatalf("budget open_tasks = %#v, want %#v", budgeted.OpenTasks, trimmedNoHistory.OpenTasks)
	}
	if !reflect.DeepEqual(budgeted.Core, trimmedNoHistory.Core) {
		t.Fatalf("budget core = %#v, want %#v", budgeted.Core, trimmedNoHistory.Core)
	}
	if budgeted.OpenLoops != trimmedNoHistory.OpenLoops || budgeted.OpenLoopsPath != trimmedNoHistory.OpenLoopsPath || budgeted.OpenLoopsNote != trimmedNoHistory.OpenLoopsNote {
		t.Fatalf("budget open_loops = %q, want %q", budgeted.OpenLoops, trimmedNoHistory.OpenLoops)
	}
	if budgeted.Agenda != trimmedNoHistory.Agenda || budgeted.AgendaPath != trimmedNoHistory.AgendaPath || budgeted.AgendaNote != trimmedNoHistory.AgendaNote {
		t.Fatalf("budget agenda = %q, want %q", budgeted.Agenda, trimmedNoHistory.Agenda)
	}

	// Trimming keeps the newest prefix; dropping from the front (or reversing
	// the list) would retain a different set of rows.
	fullApprovals, err := fixture.database.ListPetApprovals(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullApprovals) != 40 {
		t.Fatalf("stored approvals = %d, want 40", len(fullApprovals))
	}
	if len(budgeted.Approvals) >= len(fullApprovals) {
		t.Fatalf("budget approvals = %d, want fewer than %d", len(budgeted.Approvals), len(fullApprovals))
	}
	for i := range budgeted.Approvals {
		if budgeted.Approvals[i].ID != fullApprovals[i].ID {
			t.Fatalf("budget approval %d = %s, want newest-prefix %s", i, budgeted.Approvals[i].ID, fullApprovals[i].ID)
		}
	}
	for i := range budgeted.Work {
		if budgeted.Work[i].Kind != expanded.Work[i].Kind || budgeted.Work[i].TargetID != expanded.Work[i].TargetID {
			t.Fatalf("budget work row %d = %s/%s, want newest-prefix %s/%s",
				i, budgeted.Work[i].Kind, budgeted.Work[i].TargetID, expanded.Work[i].Kind, expanded.Work[i].TargetID)
		}
	}
}

func TestPetWorkStateCompacts400FollowedTasksWithoutDroppingRows(t *testing.T) {
	fixture := newPetTaskFixture(t)
	pet := fixture.createPetDraft(t, "宠物")
	now := fixture.createdAt
	const taskCount = 400
	const maxCompactBytes = 96 * 1024
	goal := strings.Repeat("关注目标<", 100)
	lastError := strings.Repeat("最近错误<", 200)
	for i := 0; i < taskCount; i++ {
		id := fmt.Sprintf("task-followed-%03d", i)
		task := domain.Task{
			ID: id, ProjectID: pet.ProjectID, WorkspaceID: pet.WorkspaceID,
			Title: strings.Repeat("标题", 120), CurrentGoal: goal, Status: domain.TaskWaitingUser,
			RuntimeConfigSnapshotID: pet.RuntimeConfigSnapshotID, CreatedAt: now, UpdatedAt: now,
		}
		if err := fixture.database.CreateTask(fixture.ctx, task); err != nil {
			t.Fatal(err)
		}
		msg := domain.Message{ID: domain.NewID("message"), TaskID: id, Role: "user", Sender: "owner", Content: "开始", CreatedAt: now}
		if err := fixture.database.AddMessage(fixture.ctx, msg); err != nil {
			t.Fatal(err)
		}
		if err := fixture.database.CreateTurn(fixture.ctx, domain.Turn{
			ID: domain.NewID("turn"), TaskID: id, AgentID: "main", Sequence: 1,
			InputMessageID: msg.ID, Status: domain.TurnFailed, Error: lastError,
			RuntimeConfigSnapshotID: pet.RuntimeConfigSnapshotID, QueuedAt: now, FinishedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.database.UpsertPetTaskWatch(fixture.ctx, id, "manual", now); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_pet_work_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	var state PetWorkState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	if !state.OpenTaskDetailsTruncated || len(raw) > maxCompactBytes {
		t.Fatalf("compact briefing size=%d bytes, want <=%d; details truncated=%t", len(raw), maxCompactBytes, state.OpenTaskDetailsTruncated)
	}
	expandedRaw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_pet_work_state", json.RawMessage(`{"history_limit":9}`))
	if err != nil {
		t.Fatal(err)
	}
	var expanded PetWorkState
	if err := json.Unmarshal([]byte(expandedRaw), &expanded); err != nil {
		t.Fatal(err)
	}
	if len(expandedRaw) <= maxCompactBytes || expanded.OpenTaskDetailsTruncated || len(expanded.OpenTasks) != taskCount ||
		expanded.OpenTasks[0].Goal == "" || expanded.OpenTasks[0].LastTurnError == "" {
		t.Fatalf("expanded briefing size=%d bytes, rows=%d, details truncated=%t", len(expandedRaw), len(expanded.OpenTasks), expanded.OpenTaskDetailsTruncated)
	}
	t.Logf("briefing for %d tasks: %d bytes compact, %d bytes expanded", taskCount, len(raw), len(expandedRaw))
	if len(state.OpenTasks) != taskCount || state.Inspection.OpenTasks != taskCount {
		t.Fatalf("wake open rows=%d, inspection open count=%d, want %d", len(state.OpenTasks), state.Inspection.OpenTasks, taskCount)
	}
	for i, task := range state.OpenTasks {
		full := expanded.OpenTasks[i]
		if want := fmt.Sprintf("task-followed-%03d", i); task.ID != want || task.Code == "" || task.Code != full.Code ||
			task.Status != string(domain.TaskWaitingUser) || task.CreatedByPet || task.NeedsAttention ||
			task.Goal != "" || task.LastTurnError != "" {
			t.Fatalf("open task %d = %+v, want compact followed task %s", i, task, want)
		}
	}
	view, err := fixture.service.PetWorkbench(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Inspection == nil || view.Inspection.OpenTasks != taskCount ||
		view.Inspection.AttentionTasks != 0 || state.Inspection.AttentionTasks != 0 {
		t.Fatalf("workbench inspection=%+v, want %d open tasks", view.Inspection, taskCount)
	}
}

func TestPetWorkStateShortensFollowedProseBeforeCompactingRows(t *testing.T) {
	followed := PetOpenTask{
		ID: "followed", Code: "task-1", Title: "关注任务", Status: string(domain.TaskActive),
		Goal: strings.Repeat("关注", 120), LastTurnError: strings.Repeat("错误", 120),
	}
	owned := PetOpenTask{
		ID: "owned", Code: "task-2", CreatedByPet: true, Status: string(domain.TaskFailed),
		Goal: strings.Repeat("目标", 120), LastTurnError: strings.Repeat("错误", 120),
	}
	state := PetWorkState{OpenTasks: []PetOpenTask{followed, owned}}
	want := followed
	want.Goal = truncateAssistantText(want.Goal, 40)
	want.LastTurnError = truncateAssistantText(want.LastTurnError, 40)
	target := PetWorkState{OpenTasks: []PetOpenTask{want, owned}, OpenTaskDetailsTruncated: true}
	raw, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	state.fitDefaultBudget(len(raw))
	if !reflect.DeepEqual(state, target) {
		t.Fatalf("expected followed prose to shorten and keep the title, with owned task intact: %+v", state.OpenTasks)
	}
}

// TestPetWorkStateFitDefaultBudgetCountsTruncationFlag reproduces the
// 25-byte boundary where history_truncated is added after the final trim: the
// budget must be measured with the marker already present. A correct budget
// keeps every padded variant within the limit, while the old ordering exceeds
// it for the variants that land in the marker-size gap.
func TestPetWorkStateFitDefaultBudgetCountsTruncationFlag(t *testing.T) {
	limit := petWorkStateDefaultBudgetBytes
	for pad := 0; pad <= 400; pad++ {
		state := PetWorkState{Note: strings.Repeat("n", 15000+pad)}
		for i := 0; i < 8; i++ {
			state.Approvals = append(state.Approvals, domain.PetApproval{
				ID: fmt.Sprintf("approval-%02d", i), Kind: "owner_decision",
				Title: "审批", Summary: strings.Repeat("s", 60), Status: "pending",
			})
			state.Work = append(state.Work, PetWorkItem{
				Kind: "task_started", TargetType: "task", TargetID: fmt.Sprintf("task-%02d", i),
				Title: strings.Repeat("w", 60),
			})
		}
		state.fitDefaultBudget(limit)
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > limit {
			t.Fatalf("pad=%d: budget left %d bytes, want <= %d", pad, len(raw), limit)
		}
		if !state.HistoryTruncated {
			t.Fatalf("pad=%d: history_truncated not set although rows were dropped", pad)
		}
	}
}

func TestPetWorkStateShowsApprovalsAndPriorWork(t *testing.T) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "工作状态项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "工作状态 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "需要继续跟进的任务",
		Request: "先运行", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "external_channel_send", Title: "等待审批", Summary: "测试审批", Risk: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(fixture.ctx, "loops", "# 开放闭环\n\n- [waiting] 跟进任务 A\n"); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.OpenLoops, "跟进任务 A") {
		t.Fatalf("open loops missing from work state: %#v", state)
	}
	if !strings.HasSuffix(state.OpenLoopsPath, "core/loops.md") && !strings.HasSuffix(state.OpenLoopsPath, `core\loops.md`) {
		t.Fatalf("open loops path missing from work state: %#v", state)
	}
	if state.PendingApprovals != 1 || len(state.Approvals) == 0 || state.Approvals[0].ID != approval.ID {
		t.Fatalf("approvals=%#v", state)
	}
	foundTask := false
	for _, item := range state.Work {
		if item.TargetType == "task" && item.TargetID == task.ID {
			foundTask = true
			if item.LastTurnStatus == "" {
				t.Fatalf("task work item has no turn status: %#v", item)
			}
		}
	}
	if !foundTask {
		t.Fatalf("task work item missing: %#v", state.Work)
	}
}

func TestPetWorkStateExposesOpenKnowledgeSyncNotifications(t *testing.T) {
	fixture := newPetTaskFixture(t)
	ctx := fixture.ctx
	now := fixture.service.now().UTC()
	appendEvent := func(id, kind, detail, status string, at time.Time) {
		t.Helper()
		if err := fixture.database.AppendPetEvent(ctx, domain.PetEvent{
			ID: id, At: at, Actor: "aha", Kind: kind, Detail: detail, Status: status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	conflictID := "pet_event_conflict_open"
	pausedID := "pet_event_paused_open"
	appendEvent(conflictID, "knowledge_sync_conflict",
		"本地 abcdef123456，远端 123456abcdef 已分叉；未合并、未 reset。差异文件：global/a.md", "open", now.Add(-time.Minute))
	appendEvent(pausedID, "knowledge_sync_paused",
		"知识库自动同步连续失败 3 次，已暂停：push failed", "open", now)
	// Already handled rows and unrelated/legacy event kinds must not reappear
	// as pending notifications.
	appendEvent("pet_event_conflict_acked", "knowledge_sync_conflict", "旧冲突", "acknowledged", now.Add(-2*time.Minute))
	appendEvent("pet_event_conflict_legacy", "knowledge_sync_conflict", "升级前的旧事件", "", now.Add(-3*time.Minute))
	appendEvent("pet_event_control", "control", `{"say":"hi"}`, "open", now.Add(-4*time.Minute))

	state, err := fixture.service.PetWorkState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Notifications) != 2 {
		t.Fatalf("notifications=%#v, want two open sync events", state.Notifications)
	}
	// Newest first, and every field is the event row's own value: Detail is
	// prose and never interpreted as a target id.
	if state.Notifications[0].ID != pausedID || state.Notifications[0].Kind != "knowledge_sync_paused" ||
		state.Notifications[0].Status != "open" || state.Notifications[0].At != now.Format(time.RFC3339) {
		t.Fatalf("newest notification=%#v", state.Notifications[0])
	}
	if state.Notifications[1].ID != conflictID || !strings.Contains(state.Notifications[1].Detail, "已分叉") {
		t.Fatalf("conflict notification=%#v", state.Notifications[1])
	}
	// history_limit=0 removes approvals/work but must keep unresolved sync
	// notifications visible.
	var noHistory PetWorkState
	raw, err := fixture.service.PetMCPTool(ctx, "aha_pet_work_state", json.RawMessage(`{"history_limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &noHistory); err != nil {
		t.Fatal(err)
	}
	if len(noHistory.Notifications) != 2 || len(noHistory.Work) != 0 {
		t.Fatalf("history_limit=0 state notifications=%#v work=%#v", noHistory.Notifications, noHistory.Work)
	}

	// The ack tool is restricted to the pet Task at execution time: an
	// unidentified call or a capability issued for another Task must be
	// rejected before any row changes.
	if _, err := fixture.service.PetMCPTool(ctx, "aha_ack_knowledge_sync_notification",
		json.RawMessage(`{"ids":["`+conflictID+`"]}`)); err == nil || !strings.Contains(err.Error(), "宠物 Task") {
		t.Fatalf("unidentified ack err=%v", err)
	}
	if _, err := fixture.service.PetMCPToolForTask(ctx, "task_someone_else", "aha_ack_knowledge_sync_notification",
		json.RawMessage(`{"ids":["`+conflictID+`"]}`)); err == nil || !strings.Contains(err.Error(), "宠物 Task") {
		t.Fatalf("foreign-task ack err=%v", err)
	}
	if state, err := fixture.service.PetWorkState(ctx); err != nil || len(state.Notifications) != 2 {
		t.Fatalf("rejected ack changed notifications: %#v err=%v", state.Notifications, err)
	}

	// Acknowledging marks only the listed open rows; duplicates and unknown ids
	// are ignored, and the paused notification stays visible.
	ackRaw, err := fixture.service.PetMCPToolForTask(ctx, petTaskID, "aha_ack_knowledge_sync_notification",
		json.RawMessage(`{"ids":["`+conflictID+`","`+conflictID+`","pet_event_missing"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Acknowledged int               `json:"acknowledged"`
		Remaining    []PetNotification `json:"remaining"`
		Note         string            `json:"note"`
	}
	if err := json.Unmarshal([]byte(ackRaw), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Acknowledged != 1 || len(ack.Remaining) != 1 || ack.Remaining[0].ID != pausedID || ack.Note == "" {
		t.Fatalf("ack response=%s", ackRaw)
	}
	state, err = fixture.service.PetWorkState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Notifications) != 1 || state.Notifications[0].ID != pausedID {
		t.Fatalf("after ack notifications=%#v", state.Notifications)
	}
	// Repeated acknowledgement is a no-op rather than an error or a new row.
	ackRaw, err = fixture.service.PetMCPToolForTask(ctx, petTaskID, "aha_ack_knowledge_sync_notification",
		json.RawMessage(`{"ids":["`+conflictID+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ackRaw, `"acknowledged":0`) {
		t.Fatalf("repeat ack=%s", ackRaw)
	}
	// Acknowledging the last notification empties the work-state field.
	if _, err := fixture.service.PetMCPToolForTask(ctx, petTaskID, "aha_ack_knowledge_sync_notification",
		json.RawMessage(`{"ids":["`+pausedID+`"]}`)); err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.PetWorkState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Notifications) != 0 {
		t.Fatalf("notifications after acking all=%#v", state.Notifications)
	}
	// Only whitelisted sync kinds are ever transitioned; unrelated open
	// events keep their status.
	events, err := fixture.database.ListPetEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, event := range events {
		statuses[event.ID] = event.Status
	}
	if statuses["pet_event_control"] != "open" {
		t.Fatalf("unrelated event status=%q", statuses["pet_event_control"])
	}
	// Resolving closes nothing new once everything is closed, and an empty id
	// list is rejected instead of silently acknowledging everything.
	if resolved, err := fixture.service.ResolveKnowledgeSyncNotifications(ctx); err != nil || resolved != 0 {
		t.Fatalf("resolve closed=%d err=%v", resolved, err)
	}
	if _, err := fixture.service.PetMCPToolForTask(ctx, petTaskID, "aha_ack_knowledge_sync_notification", json.RawMessage(`{"ids":[]}`)); err == nil {
		t.Fatal("empty ids must be rejected")
	}
	// The pet's MCP allowlist must expose the ack tool, otherwise the model can
	// see the notification but has no way to clear it.
	allowlisted := false
	for _, name := range petMCPAllowedTools(domain.Task{ID: petTaskID}) {
		if name == "mcp__aha2-pet__aha_ack_knowledge_sync_notification" {
			allowlisted = true
			break
		}
	}
	if !allowlisted {
		t.Fatal("ack tool missing from the pet MCP allowlist")
	}
}

func TestPetWorkStateBudgetTrimsNotificationBytesButKeepsIdentity(t *testing.T) {
	fixture := newPetTaskFixture(t)
	ctx := fixture.ctx
	now := fixture.service.now().UTC()
	// Ten long Chinese details are far beyond the old 900-rune reasoning:
	// runes are not bytes, so the default briefing would have been far over
	// the 16 KB result budget before notifications participated in trimming.
	longDetail := "本地 abcdef123456，远端 123456abcdef 已分叉；未合并、未 reset。差异文件：" +
		strings.Repeat("知识库目录/中文文件/", 60)
	if runes := []rune(longDetail); len(runes) > 900 {
		longDetail = string(runes[:900])
	}
	ids := make([]string, 0, 10)
	for index := 0; index < 10; index++ {
		id := fmt.Sprintf("pet_event_budget_%02d", index)
		ids = append(ids, id)
		kind := "knowledge_sync_conflict"
		if index%2 == 1 {
			kind = "knowledge_sync_paused"
		}
		if err := fixture.database.AppendPetEvent(ctx, domain.PetEvent{
			ID: id, At: now.Add(-time.Duration(index) * time.Second), Actor: "aha",
			Kind: kind, Detail: longDetail, Status: "open",
		}); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := fixture.service.PetMCPTool(ctx, "aha_pet_work_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > petWorkStateDefaultBudgetBytes {
		t.Fatalf("default work_state = %d bytes, want <= %d", len(raw), petWorkStateDefaultBudgetBytes)
	}
	var state PetWorkState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Notifications) != len(ids) {
		t.Fatalf("notifications=%d, want %d", len(state.Notifications), len(ids))
	}
	if !state.NotificationsTruncated {
		t.Fatal("notifications_truncated not reported")
	}
	seen := map[string]bool{}
	for _, notification := range state.Notifications {
		if notification.ID == "" || notification.Kind == "" || notification.At == "" || notification.Status != "open" {
			t.Fatalf("notification identity/status lost: %#v", notification)
		}
		if len([]rune(notification.Detail)) >= len([]rune(longDetail)) {
			t.Fatalf("notification detail not shortened: %#v", notification)
		}
		seen[notification.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("notification %s dropped from the briefing", id)
		}
	}
}

func TestPetWorkStateNotificationTrimStopsAtDetailFloor(t *testing.T) {
	detail25 := strings.Repeat("知", 25)
	detail24 := strings.Repeat("识", 24)
	detail23 := strings.Repeat("库", 23)
	state := PetWorkState{Notifications: []PetNotification{
		{ID: "n25", Kind: "knowledge_sync_conflict", At: "2026-11-18T00:00:00Z", Detail: detail25, Status: "open"},
		{ID: "n24", Kind: "knowledge_sync_paused", At: "2026-11-18T00:00:01Z", Detail: detail24, Status: "open"},
		{ID: "n23", Kind: "knowledge_sync_paused", At: "2026-11-18T00:00:02Z", Detail: detail23, Status: "open"},
	}}
	// A tiny budget forces the trim loop to run until nothing is eligible.
	state.fitDefaultBudget(64)
	if !state.NotificationsTruncated {
		t.Fatal("notifications_truncated not set")
	}
	expected := []struct {
		id   string
		size int
	}{{"n25", 24}, {"n24", 24}, {"n23", 23}}
	for index, item := range expected {
		got := state.Notifications[index]
		if got.ID != item.id || got.Kind == "" || got.At == "" || got.Status != "open" {
			t.Fatalf("notification %d identity/status changed: %#v", index, got)
		}
		if size := len([]rune(got.Detail)); size != item.size {
			t.Fatalf("%s detail runes=%d want=%d", item.id, size, item.size)
		}
	}
	if !strings.HasSuffix(state.Notifications[0].Detail, "…") {
		t.Fatalf("25-rune detail not marked as shortened: %q", state.Notifications[0].Detail)
	}
	if state.Notifications[1].Detail != detail24 || state.Notifications[2].Detail != detail23 {
		t.Fatalf("detail at or below floor changed: %#v", state.Notifications)
	}
}
