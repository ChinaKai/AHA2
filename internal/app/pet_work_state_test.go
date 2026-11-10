package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

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

	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Inspection.Health != "ok" || state.Inspection.Version != "0.5.999" || state.Inspection.WebVersion != "0.5.999" {
		t.Fatalf("inspection identity=%#v", state.Inspection)
	}
	if state.Inspection.TasksTotal != 2 || state.Inspection.WaitingUser != 1 || state.Inspection.Completed != 1 {
		t.Fatalf("inspection counts=%#v", state.Inspection)
	}
	if state.Inspection.OpenTasks != 1 || state.Inspection.AttentionTasks != 1 {
		t.Fatalf("inspection open/attention=%#v", state.Inspection)
	}
	if len(state.OpenTasks) != 1 || state.OpenTasks[0].ID != openTask.ID || !state.OpenTasks[0].NeedsAttention {
		t.Fatalf("open tasks=%#v", state.OpenTasks)
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
	if state.Inspection.AttentionTasks != 0 {
		t.Fatalf("attention should clear after approval: %#v", state.Inspection)
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
	// One pet-created Task stays open, so the budget path must preserve a
	// non-empty open_tasks list and the open-task turn-result behavior.
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
		// Use task_started rather than task_created so these rows exercise the
		// work history without also becoming open_tasks.
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
	if len(defaultState.OpenTasks) != 1 || defaultState.OpenTasks[0].ID != openTask.ID {
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
	if len(expanded.OpenTasks) != 1 || expanded.OpenTasks[0].ID != openTask.ID || expanded.OpenTasks[0].LastTurnResult != openTaskResult {
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
	if len(truncated.OpenTasks) != 1 || truncated.OpenTasks[0].LastTurnResult != truncateAssistantText(openTaskResult, 120) {
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
	if len(budgeted.OpenTasks) != 1 || budgeted.OpenTasks[0].ID != openTask.ID {
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
