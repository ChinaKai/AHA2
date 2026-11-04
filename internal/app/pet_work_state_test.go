package app

import (
	"fmt"
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
