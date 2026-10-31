package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPetCapabilitiesPetSayAndIdea(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(false), AllowMessageTasks: boolPointer(false),
		AllowManageModels: boolPointer(false), AllowManageKnowledge: boolPointer(false),
		AllowManageChannels: boolPointer(false), AllowManageSystem: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
	capabilities, err := fixture.service.PetCapabilities(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Permissions.ViewTasks || !capabilities.Permissions.CreateTasks ||
		capabilities.Permissions.ManageModels || capabilities.Permissions.ManageChannels {
		t.Fatalf("permissions=%#v", capabilities.Permissions)
	}
	names := map[string]bool{}
	for _, tool := range capabilities.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"aha_overview", "aha_pet_work_state", "aha_capabilities", "aha_create_task", "aha_task_detail", "pet_say", "pet_submit_idea"} {
		if !names[want] {
			t.Fatalf("capabilities missing %q: %#v", want, capabilities.Tools)
		}
	}
	for _, unwanted := range []string{"aha_create_provider", "aha_send_channel_message", "aha_update_security_settings"} {
		if names[unwanted] {
			t.Fatalf("capabilities leaked disallowed tool %q", unwanted)
		}
	}

	if _, err := fixture.service.PetMCPTool(fixture.ctx, "pet_say", json.RawMessage(`{"text":"我在。"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "pet_say", json.RawMessage(`{"text":"   "}`)); err == nil {
		t.Fatal("empty pet_say was accepted")
	}

	approval, err := fixture.service.PetSubmitIdea(fixture.ctx, PetIdeaInput{
		Title:           "给自己加一个网页搜索能力",
		Summary:         "巡检时经常需要查外部资料，希望能在授权后调用搜索工具。",
		Reason:          "外部资料能减少重复排查并提高诊断准确度。",
		Plan:            "先增加搜索 MCP，再在需要时查询并记录来源。",
		ExpectedOutcome: "减少排查等待时间。", Risk: "medium",
		PermissionsNeeded: []string{"manage_system"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval.Kind != "idea" || approval.Status != petApprovalPending {
		t.Fatalf("idea approval=%#v", approval)
	}
	if _, err := fixture.service.PetSubmitIdea(fixture.ctx, PetIdeaInput{
		Title: "长想法", Summary: strings.Repeat("想法", 1000), Reason: "测试想法长载荷需要保留", Plan: strings.Repeat("检查", 2000),
	}); err != nil {
		t.Fatalf("long idea payload was rejected: %v", err)
	}
	resolved, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "可以先做个原型")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != petApprovalApproved {
		t.Fatalf("resolved idea=%#v", resolved)
	}
	workbench, err := fixture.service.PetWorkbench(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range workbench.Approvals {
		if item.ID == approval.ID && item.Kind == "idea" {
			found = true
		}
	}
	if !found {
		t.Fatalf("idea missing from workbench: %#v", workbench.Approvals)
	}
}
