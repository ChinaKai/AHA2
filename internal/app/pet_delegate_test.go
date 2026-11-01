package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func boolPointer(value bool) *bool { return &value }

func TestPetPermissionsAndWorkbench(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	assistant, err := fixture.database.OwnerAssistant(fixture.ctx, ownerAssistantID)
	if err != nil {
		t.Fatal(err)
	}
	if !assistant.PetAllowViewTasks {
		t.Fatal("view-tasks permission should default on for read-only diagnostics")
	}

	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(false), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageKnowledge: boolPointer(true),
		AllowManageChannels: boolPointer(true), AllowManageSystem: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.PetAllowViewTasks || !view.Assistant.PetAllowCreateTasks ||
		!view.Assistant.PetAllowCreateWorkspaces || !view.Assistant.PetAllowMessageTasks ||
		!view.Assistant.PetAllowManageModels || !view.Assistant.PetAllowManageKnowledge ||
		!view.Assistant.PetAllowManageChannels || !view.Assistant.PetAllowManageSystem {
		t.Fatalf("permissions were not saved: %#v", view.Assistant)
	}

	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	if err := os.MkdirAll(filepath.Join(root, "worklog"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "diary", "2026-10-30.md"), []byte("diary body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worklog", "2026-10-30.md"), []byte("found a failed task"), 0o600); err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "create_task", Title: "创建修复任务", Summary: "任务 A 失败了，需要修复", Risk: "medium",
		Payload: `{"task_id":"task-other"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	workbench, err := fixture.service.PetWorkbench(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workbench.Diary) != 1 || len(workbench.Core) != 3 || len(workbench.Worklog) != 1 || len(workbench.Approvals) != 1 {
		t.Fatalf("workbench=%#v", workbench)
	}
	if workbench.Approvals[0].ID != approval.ID || workbench.Approvals[0].Status != petApprovalPending {
		t.Fatalf("approval=%#v", workbench.Approvals[0])
	}
	resolved, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "可以")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != petApprovalApproved || resolved.Resolution != "可以" {
		t.Fatalf("resolved=%#v", resolved)
	}

	change, err := fixture.service.PetRequestPermissionChange(fixture.ctx, PetPermissionChangeInput{
		Permission: "manage_channels", Enabled: false, Reason: "先收回渠道写权限",
	})
	if err != nil {
		t.Fatal(err)
	}
	if change.Kind != "permission_change" || change.Status != petApprovalPending {
		t.Fatalf("permission change=%#v", change)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, change.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.database.OwnerAssistant(fixture.ctx, ownerAssistantID)
	if err != nil || updated.PetAllowManageChannels {
		t.Fatalf("manage channels permission was not disabled: %#v err=%v", updated, err)
	}
	if _, err := fixture.service.PetRequestPermissionChange(fixture.ctx, PetPermissionChangeInput{
		Permission: "not-a-permission", Enabled: true,
	}); err == nil {
		t.Fatal("unknown permission was accepted")
	}
}

func TestPetMCPApprovalToolAcceptsObjectPayload(t *testing.T) {
	fixture := newPetTaskFixture(t)
	text, err := fixture.service.PetMCPTool(fixture.ctx, "pet_request_approval", json.RawMessage(`{
		"kind":"create_task","title":"创建修复任务","summary":"需要主人批准",
		"reason":"失败任务需要修复","risk":"high","payload":{"task_id":"task-other","reason":"failed"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var approval domain.PetApproval
	if err := json.Unmarshal([]byte(text), &approval); err != nil {
		t.Fatal(err)
	}
	if approval.Status != petApprovalPending || !strings.Contains(approval.Payload, "task-other") {
		t.Fatalf("approval=%#v", approval)
	}
	items, err := fixture.service.PetApprovals(fixture.ctx, 10)
	if err != nil || len(items) != 1 || items[0].ID != approval.ID {
		t.Fatalf("approvals=%#v err=%v", items, err)
	}
}

func TestPetOverviewRequiresViewTasksPermission(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		ID: "project-pet-overview", Name: "Overview", KnowledgePolicy: "none",
		CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}
	workspace := domain.Workspace{
		ID: "workspace-pet-overview", ProjectID: project.ID, Name: "overview",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		AgentAPIMode: "embedded", CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	other, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "需要排查的任务",
		Request: "排查失败原因", ModelID: fixture.model.ID, Isolation: "inplace",
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single",
		MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := fixture.service.StartTask(fixture.ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, started.ID, domain.TaskActive, domain.TaskFailed, timeString(fixture.service.now().UTC()), ""); err != nil {
		t.Fatal(err)
	}
	overview, err := fixture.service.PetAHAOverview(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Failed != 1 || len(overview.Attention) == 0 || overview.Attention[0].ID != other.ID {
		t.Fatalf("overview=%#v", overview)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
	overview, err = fixture.service.PetAHAOverview(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Attention) != 0 || overview.Note == "" {
		t.Fatalf("overview leaked task details without permission: %#v", overview)
	}
	if _, err := fixture.service.PetTaskDetail(fixture.ctx, other.ID); err == nil {
		t.Fatal("task detail was available without permission")
	}
}

func TestPetApprovalDecisionReasonIsRequiredAndReadable(t *testing.T) {
	fixture := newPetTaskFixture(t)
	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "create_task", Title: "创建验证任务", Summary: "用于测试拒绝原因",
		Reason: "需要验证审批原因记录", Risk: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "reject", ""); err == nil || !strings.Contains(err.Error(), "decision_reason") {
		t.Fatalf("reject without decision_reason was accepted: %v", err)
	}
	resolved, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "reject", "当前优先级不足")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DecisionReason != "当前优先级不足" || resolved.Reason != "需要验证审批原因记录" {
		t.Fatalf("resolved approval lost reasons: %#v", resolved)
	}
	items, err := fixture.service.PetApprovals(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ID == approval.ID {
			found = true
			if item.DecisionReason != "当前优先级不足" {
				t.Fatalf("approval readback=%#v", item)
			}
		}
	}
	if !found {
		t.Fatal("resolved approval was not readable")
	}
}
