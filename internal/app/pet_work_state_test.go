package app

import (
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
	healthy := domain.ChannelInstance{
		ID: "channel-healthy", Name: "健康渠道", Status: "ready", EffectiveAvailability: "available",
	}
	backlog := domain.ChannelInstance{
		ID: "channel-backlog", Name: "堆积渠道", Status: "ready", EffectiveAvailability: "available",
	}
	deadLetter := domain.ChannelInstance{
		ID: "channel-dead", Name: "死信渠道", Status: "ready", EffectiveAvailability: "available",
	}
	newInspection := func() PetInspection {
		return PetInspection{Health: "ok", Note: "没有未闭环任务。"}
	}

	t.Run("healthy", func(t *testing.T) {
		inspection := newInspection()
		applyPetChannelHealthValues(&inspection, []domain.ChannelInstance{healthy}, nil, now, false)
		if inspection.Health != "ok" || len(inspection.DegradedChannels) != 0 || len(inspection.Channels) != 1 {
			t.Fatalf("healthy inspection=%#v", inspection)
		}
		if inspection.Channels[0].Degraded || inspection.Channels[0].Note != "" || inspection.Channels[0].EffectiveAvailability != "available" {
			t.Fatalf("healthy channel=%#v", inspection.Channels[0])
		}
		if inspection.Note != "没有未闭环任务。" {
			t.Fatalf("healthy note changed: %q", inspection.Note)
		}
	})

	t.Run("pending_backlog", func(t *testing.T) {
		inspection := newInspection()
		outbox := map[string]domain.ChannelOutboxHealth{
			backlog.ID: {InstanceID: backlog.ID, Pending: 3, StuckPending: 3, OldestPendingAt: now.Add(-9 * time.Minute)},
		}
		applyPetChannelHealthValues(&inspection, []domain.ChannelInstance{backlog}, outbox, now, false)
		if inspection.Health != "degraded" || len(inspection.DegradedChannels) != 1 {
			t.Fatalf("backlog inspection=%#v", inspection)
		}
		channel := inspection.Channels[0]
		if !channel.Degraded || channel.Pending != 3 || channel.StuckPending != 3 || channel.OldestPendingMinutes != 9 {
			t.Fatalf("backlog channel=%#v", channel)
		}
		for _, want := range []string{"pending 3", "最老 9 分钟", "attempts=0", "渠道状态异常"} {
			if !strings.Contains(inspection.Note, want) {
				t.Fatalf("backlog note %q missing %q", inspection.Note, want)
			}
		}
	})

	t.Run("dead_letter", func(t *testing.T) {
		inspection := newInspection()
		outbox := map[string]domain.ChannelOutboxHealth{
			deadLetter.ID: {InstanceID: deadLetter.ID, DeadLetter: 1, LastErrorCode: "feishu_target_unavailable"},
		}
		applyPetChannelHealthValues(&inspection, []domain.ChannelInstance{deadLetter}, outbox, now, false)
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
