package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetWorkbenchPageLimitsAndTotals(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	if err := os.RemoveAll(filepath.Join(root, "worklog")); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"worklog"} {
		dir := filepath.Join(root, section)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 35; i++ {
			name := time.Date(2099, 1, 1+i, 0, 0, 0, 0, time.UTC).Format("2006-01-02") + ".md"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(section), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := fixture.createdAt
	for i := 0; i < 55; i++ {
		if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{
			ID: fmt.Sprintf("approval-page-%02d", i), Kind: "idea",
			Title: fmt.Sprintf("idea %02d", i), Summary: "summary", Risk: "low",
			Status: petApprovalPending, CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
		ApprovalLimit: 50, WorklogLimit: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.WorklogTotal != 35 || len(first.Worklog) != 30 || first.ApprovalTotal != 55 || len(first.Approvals) != 50 {
		t.Fatalf("first page totals=%d/%d lengths=%d/%d", first.WorklogTotal, first.ApprovalTotal, len(first.Worklog), len(first.Approvals))
	}
	expanded, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
		ApprovalLimit: 80, WorklogLimit: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded.Worklog) != 35 || len(expanded.Approvals) != 55 {
		t.Fatalf("expanded lengths=%d/%d", len(expanded.Worklog), len(expanded.Approvals))
	}
}

// TestPetWorkbenchApprovalPendingPriorityCountsAndPaging pins the two
// properties the approvals page relies on: the pending-first ordering covers
// every pending row before any resolved row in the first page, and
// approval_counts describes the untruncated table so the browser can page and
// label a client-side filter without trusting the page length.
func TestPetWorkbenchApprovalPendingPriorityCountsAndPaging(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		pending  int
		resolved int
	}{
		{"no approvals", 0, 0},
		{"fewer pending than a page", 3, 12},
		{"exactly one pending page", 10, 5},
		{"more pending than a page", 15, 8},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newPetTaskFixture(t)
			if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
				t.Fatal(err)
			}
			base := fixture.createdAt
			for i := 0; i < testCase.pending; i++ {
				kind := "idea"
				if i%2 == 1 {
					kind = "owner_decision"
				}
				if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{
					ID: fmt.Sprintf("pending-%02d", i), Kind: kind, Title: fmt.Sprintf("pending %02d", i),
					Summary: "summary", Risk: "low", Status: petApprovalPending,
					CreatedAt: base.Add(time.Duration(i) * time.Second),
				}); err != nil {
					t.Fatal(err)
				}
			}
			resolvedStatuses := []string{petApprovalApproved, petApprovalRejected, petApprovalCancelled}
			for i := 0; i < testCase.resolved; i++ {
				if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{
					ID: fmt.Sprintf("resolved-%02d", i), Kind: "delete_task", Title: fmt.Sprintf("resolved %02d", i),
					Summary: "summary", Risk: "low", Status: resolvedStatuses[i%len(resolvedStatuses)],
					CreatedAt: base.Add(time.Duration(testCase.pending+i) * time.Second),
				}); err != nil {
					t.Fatal(err)
				}
			}

			total := testCase.pending + testCase.resolved
			view, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
				ApprovalLimit: 10, WorklogLimit: 30,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantPage := total
			if wantPage > 10 {
				wantPage = 10
			}
			if view.ApprovalTotal != total || len(view.Approvals) != wantPage {
				t.Fatalf("total=%d page=%d want %d/%d", view.ApprovalTotal, len(view.Approvals), total, wantPage)
			}
			// The server pages pending-first: no resolved row may precede a
			// pending row, and the newest pending rows come first.
			pendingLoaded := 0
			seenResolved := false
			for index, item := range view.Approvals {
				if item.Status == petApprovalPending {
					if seenResolved {
						t.Fatalf("pending row at %d follows a resolved row: %#v", index, view.Approvals)
					}
					pendingLoaded++
					wantID := fmt.Sprintf("pending-%02d", testCase.pending-pendingLoaded)
					if item.ID != wantID {
						t.Fatalf("pending order at %d=%q want %q", index, item.ID, wantID)
					}
				} else {
					seenResolved = true
				}
			}
			wantPendingLoaded := testCase.pending
			if wantPendingLoaded > wantPage {
				wantPendingLoaded = wantPage
			}
			if pendingLoaded != wantPendingLoaded {
				t.Fatalf("pending loaded=%d want %d (page %#v)", pendingLoaded, wantPendingLoaded, view.Approvals)
			}
			counts := map[string]int{}
			sum := 0
			for _, group := range view.ApprovalCounts {
				counts[group.Kind+"/"+group.Status] = group.Count
				sum += group.Count
			}
			if sum != total || view.ApprovalTotal != total {
				t.Fatalf("approval counts sum=%d total=%d total field=%d", sum, total, view.ApprovalTotal)
			}
			wantIdeaPending := (testCase.pending + 1) / 2
			wantDecisionPending := testCase.pending / 2
			if counts["idea/pending"] != wantIdeaPending || counts["owner_decision/pending"] != wantDecisionPending {
				t.Fatalf("pending kind counts=%#v want idea=%d owner_decision=%d", counts, wantIdeaPending, wantDecisionPending)
			}

			// Loading a second page extends the same pending-first prefix; the
			// counts stay untruncated.
			expanded, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{
				ApprovalLimit: 20, WorklogLimit: 30,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantExpanded := total
			if wantExpanded > 20 {
				wantExpanded = 20
			}
			if len(expanded.Approvals) != wantExpanded {
				t.Fatalf("expanded page=%d want %d", len(expanded.Approvals), wantExpanded)
			}
			for index, item := range expanded.Approvals {
				if index >= wantPage {
					break
				}
				if item.ID != view.Approvals[index].ID {
					t.Fatalf("expanded page reordered row %d: %q != %q", index, item.ID, view.Approvals[index].ID)
				}
			}
			if sum := func() int {
				total := 0
				for _, group := range expanded.ApprovalCounts {
					total += group.Count
				}
				return total
			}(); sum != total {
				t.Fatalf("expanded counts sum=%d want %d", sum, total)
			}
		})
	}
}

func TestPetWorkbenchInspectionSnapshot(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	fixture.service.buildVersion = "test-version"
	fixture.service.webVersion = "web-test"
	fixture.service.startedAt = fixture.createdAt.Add(-65 * time.Minute)
	now := fixture.createdAt
	project := domain.Project{ID: "project-inspection", Name: "Inspection", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-inspection", ProjectID: project.ID, Name: "inspection", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{
		ID: "env-inspection", Name: "inspection", ProviderID: fixture.provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return fixture.database.CreateProject(fixture.ctx, project) },
		func() error { return fixture.database.CreateWorkspace(fixture.ctx, workspace) },
		func() error { return fixture.database.UpsertEnvGroup(fixture.ctx, env) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	create := func(id string, status domain.TaskStatus, petCreated bool) {
		t.Helper()
		snapshot := domain.RuntimeConfigSnapshot{
			ID: "runtime-" + id, WorkspaceID: workspace.ID, Backend: "codex",
			ModelID: fixture.model.ID, WireModel: "pet", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now,
		}
		task := domain.Task{
			ID: id, ProjectID: project.ID, WorkspaceID: workspace.ID, Title: id, CurrentGoal: id,
			Status: status, RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now,
		}
		if _, err := fixture.database.CreateTaskWithSnapshot(fixture.ctx, snapshot, task); err != nil {
			t.Fatal(err)
		}
		if petCreated {
			if err := fixture.database.AppendPetEvent(fixture.ctx, domain.PetEvent{
				ID: "event-" + id, At: now, Actor: "pet", Kind: "task_created", Detail: id,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("task-attention", domain.TaskFailed, true)
	create("task-approved", domain.TaskBlocked, true)
	create("task-active", domain.TaskActive, true)
	create("task-completed", domain.TaskCompleted, true)
	create("task-other", domain.TaskActive, false)
	for _, approval := range []domain.PetApproval{
		{ID: "approval-task", TaskID: petTaskID, Kind: "owner_decision", Title: "decision", Summary: "summary", Status: petApprovalPending, Payload: `{"task_id":"task-approved"}`, CreatedAt: now},
		{ID: "approval-generic", TaskID: petTaskID, Kind: "idea", Title: "idea", Summary: "summary", Status: petApprovalPending, CreatedAt: now},
	} {
		if err := fixture.database.CreatePetApproval(fixture.ctx, approval); err != nil {
			t.Fatal(err)
		}
	}

	view, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{ApprovalLimit: 50, WorklogLimit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if view.Inspection == nil {
		t.Fatal("workbench inspection snapshot missing")
	}
	inspection := view.Inspection
	if inspection.Version != "test-version" || inspection.WebVersion != "web-test" || inspection.Health != "ok" {
		t.Fatalf("inspection identity=%#v", inspection)
	}
	if inspection.CheckedAt == "" || inspection.StartedAt == "" || inspection.UptimeMinutes < 65 {
		t.Fatalf("inspection time fields=%#v", inspection)
	}
	if inspection.TasksTotal != 5 || inspection.Active != 2 || inspection.WaitingUser != 0 ||
		inspection.Failed != 1 || inspection.Blocked != 1 || inspection.Completed != 1 {
		t.Fatalf("task stats=%#v", inspection)
	}
	if inspection.OpenTasks != 3 || inspection.AttentionTasks != 1 {
		t.Fatalf("open/attention=%d/%d", inspection.OpenTasks, inspection.AttentionTasks)
	}
	if inspection.PendingApprovals != 2 || inspection.DegradedChannelCount != 0 || inspection.ChannelsTotal != 0 {
		t.Fatalf("approval/channel fields=%#v", inspection)
	}
	if inspection.Channels != nil {
		t.Fatalf("embedded snapshot should stay compact: %#v", inspection.Channels)
	}
	if !strings.Contains(inspection.Note, "1 个未闭环任务需要处理") {
		t.Fatalf("inspection note=%q", inspection.Note)
	}
	// The existing workbench payload must remain intact next to the additive
	// inspection field.
	if view.UpdatedAt.IsZero() || view.ChannelTotal != 0 || view.ApprovalTotal != 2 {
		t.Fatalf("workbench compatibility fields=%#v", view)
	}
}

func TestPetWorkbenchInspectionSnapshotDegradedChannelAndPayload(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	fixture.service.SetBuildInfo("0.6.0", "web-inspection-test", time.Now().UTC().Add(-65*time.Minute))
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "巡检快照项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "巡检快照 Workspace", RootPath: t.TempDir(),
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
	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "owner_decision", Title: "巡检任务需要决定", Summary: "继续还是取消", Risk: "medium",
		Payload: `{"task_id":"` + openTask.ID + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.Owner{ID: "owner-workbench-inspection", Username: "owner", CreatedAt: now}
	if err := fixture.database.CreateOwner(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "plugin-workbench-inspection", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1.0.0", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1,
		DiscoveredAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertChannelPlugin(fixture.ctx, plugin); err != nil {
		t.Fatal(err)
	}
	degradedChannel := domain.ChannelInstance{
		ID: "channel-workbench-inspection", PluginID: plugin.ID, OwnerID: owner.ID,
		RuntimeDeviceID: "device-workbench-inspection", Name: "降级渠道", Status: "error",
		Revision: 1, HostProjectID: project.ID, HostWorkspaceID: workspace.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateChannelInstance(fixture.ctx, degradedChannel, nil); err != nil {
		t.Fatal(err)
	}

	view, err := fixture.service.PetWorkbenchWithOptions(fixture.ctx, PetWorkbenchOptions{ApprovalLimit: 50, WorklogLimit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if view.Inspection == nil {
		t.Fatal("workbench inspection snapshot is missing")
	}
	inspection := *view.Inspection
	if inspection.Version != "0.6.0" || inspection.WebVersion != "web-inspection-test" {
		t.Fatalf("inspection identity=%#v", inspection)
	}
	if inspection.StartedAt == "" || inspection.CheckedAt == "" || inspection.UptimeMinutes < 65 {
		t.Fatalf("inspection timing=%#v", inspection)
	}
	if inspection.Health != "degraded" {
		t.Fatalf("inspection health=%q degraded=%#v", inspection.Health, inspection.DegradedChannels)
	}
	if inspection.TasksTotal != 2 || inspection.WaitingUser != 1 || inspection.Completed != 1 {
		t.Fatalf("inspection task counts=%#v", inspection)
	}
	if inspection.Active != 0 || inspection.Failed != 0 || inspection.Blocked != 0 {
		t.Fatalf("inspection unexpected status counts=%#v", inspection)
	}
	if inspection.OpenTasks != 1 || inspection.AttentionTasks != 0 {
		t.Fatalf("inspection open/attention=%#v", inspection)
	}
	if inspection.PendingApprovals != 1 {
		t.Fatalf("inspection pending approvals=%#v", inspection)
	}
	if inspection.DegradedChannelCount != 1 || len(inspection.DegradedChannels) != 1 {
		t.Fatalf("inspection degraded channels=%#v count=%d", inspection.DegradedChannels, inspection.DegradedChannelCount)
	}
	if inspection.Channels != nil {
		t.Fatalf("inspection should embed no channel rows in the compact workbench snapshot: %#v", inspection.Channels)
	}
	if !strings.Contains(inspection.Note, "未闭环") || !strings.Contains(inspection.Note, "渠道状态异常") {
		t.Fatalf("inspection note=%q", inspection.Note)
	}
	// The inspection summary is additive; the existing workbench cards stay.
	if len(view.Channels) != 1 || view.ChannelTotal != 1 || !view.Channels[0].Degraded || view.ChannelNote == "" {
		t.Fatalf("workbench channel cards=%#v total=%d note=%q", view.Channels, view.ChannelTotal, view.ChannelNote)
	}
	if len(view.Approvals) != 1 || view.Approvals[0].ID != approval.ID {
		t.Fatalf("workbench approvals=%#v", view.Approvals)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"core", "worklog", "worklog_total", "approvals", "approval_total", "approval_counts",
		"watches", "channels", "channel_total", "channel_note", "updated_at", "inspection",
	} {
		if _, exists := payload[field]; !exists {
			t.Fatalf("workbench response lost field %q", field)
		}
	}
	for _, field := range []string{"diary", "diary_total", "diary_limit"} {
		if _, exists := payload[field]; exists {
			t.Fatalf("retired field %q remains", field)
		}
	}
	snapshot, ok := payload["inspection"].(map[string]any)
	if !ok {
		t.Fatalf("inspection is not a JSON object: %#v", payload["inspection"])
	}
	for _, field := range []string{
		"checked_at", "version", "web_version", "started_at", "uptime_minutes", "health",
		"tasks_total", "active", "waiting_user", "failed", "blocked", "completed",
		"pending_approvals", "degraded_channel_count", "open_tasks", "attention_tasks", "note",
	} {
		if _, exists := snapshot[field]; !exists {
			t.Fatalf("inspection snapshot lost field %q: %#v", field, snapshot)
		}
	}
}

func TestPetWorkbenchRetiredJournalUnavailable(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	if err := fixture.service.PetSaveWorkbenchDocument(fixture.ctx, "diary", "2026-11-09.md", "retired"); err == nil {
		t.Fatal("retired section still writable")
	}
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		if tool.Name == "pet_write_diary" {
			t.Fatal("retired tool still advertised")
		}
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "pet_write_diary", json.RawMessage(`{"date":"2026-11-09","content":"retired"}`)); err == nil {
		t.Fatal("retired tool still callable")
	}
	for _, call := range []struct {
		name string
		args string
	}{
		{"pet_update_core", `{"document":"identity","content":"preserved identity"}`},
		{"pet_write_worklog", `{"date":"2026-11-09","content":"preserved worklog"}`},
	} {
		if _, err := fixture.service.PetMCPTool(fixture.ctx, call.name, json.RawMessage(call.args)); err != nil {
			t.Fatalf("supported tool %s failed: %v", call.name, err)
		}
	}
	for _, file := range []struct{ path, content string }{{"core/identity.md", "preserved identity"}, {"worklog/2026-11-09.md", "preserved worklog"}} {
		raw, err := os.ReadFile(filepath.Join(root, file.path))
		if err != nil || string(raw) != file.content {
			t.Fatalf("supported tool write %s=%q %v", file.path, raw, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		view, err := fixture.service.PetWorkbench(fixture.ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"diary", "diary_total", "diary_limit"} {
			if _, exists := payload[field]; exists {
				t.Fatalf("retired field %q remains", field)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "diary")); !os.IsNotExist(err) {
			t.Fatalf("retired directory recreated: %v", err)
		}
	}
}

func TestPetPendingApprovalCountIgnoresPaginationAndResolved(t *testing.T) {
	fixture := newPetTaskFixture(t)
	assertCount := func(want int) {
		t.Helper()
		got, err := fixture.service.PetPendingApprovalCount(fixture.ctx)
		if err != nil || got != want {
			t.Fatalf("pending=%d err=%v want=%d", got, err, want)
		}
	}
	assertCount(0)
	for i := 0; i < 105; i++ {
		if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{ID: fmt.Sprintf("pending-%d", i), Kind: "owner_decision", Title: "pending", Status: "pending", CreatedAt: fixture.createdAt}); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"approved", "rejected", "cancelled"} {
		if err := fixture.database.CreatePetApproval(fixture.ctx, domain.PetApproval{ID: status, Kind: "owner_decision", Title: status, Status: status, CreatedAt: fixture.createdAt}); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(105)
	page, _, err := fixture.service.PetApprovalsWithTotal(fixture.ctx, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	assertCount(105)
}

func TestPetWorkspaceMigratesOnlyGeneratedLabGuide(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.service.petWorkspaceRoot(fixture.ctx), petLabDir, "README.md")
	legacy := strings.Replace(petLabReadmeSeed, "core/、worklog/", "core/、diary/、worklog/", 1)
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(legacy, "\n", "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWorkbench(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != petLabReadmeSeed {
		t.Fatalf("guide migration failed: %q %v", raw, err)
	}
	custom := "# My custom lab guide\nOwner data remains here.\n"
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWorkbench(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != custom {
		t.Fatalf("custom guide overwritten: %q %v", raw, err)
	}
}
