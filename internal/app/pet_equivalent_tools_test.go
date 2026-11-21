package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Ordinary Tasks reach knowledge through aha2_knowledge; the pet reaches the
// same capability through its own tools, dispatched by name exactly as the
// pet MCP server does. Each one changes or reads the real knowledge store and
// follows the live view/manage-knowledge switches.
func TestPetKnowledgeEquivalentToolsDispatchByNameUnderLivePermissions(t *testing.T) {
	fixture := newPetTaskFixture(t)
	settings := func(view, knowledge bool) {
		t.Helper()
		if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
			Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
			IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
			AllowViewTasks: boolPointer(view), AllowManageKnowledge: boolPointer(knowledge),
		}); err != nil {
			t.Fatal(err)
		}
	}
	settings(true, true)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-equivalent", Name: "等价工具", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) (string, error) {
		data, _ := json.Marshal(args)
		return fixture.service.PetMCPTool(fixture.ctx, name, data)
	}
	submitted, err := call("aha_submit_knowledge", map[string]any{"scope": "project", "project_id": project.ID, "candidates": []map[string]any{{
		"type": "practice", "title": "EQUIVALENT-TITLE", "body": "EQUIVALENT-BODY", "confidence": 0.9, "reason": "fixture",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Entries []struct {
			Status string `json:"status"`
		} `json:"entries"`
		Proposals []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"proposals"`
	}
	if json.Unmarshal([]byte(submitted), &result) != nil || len(result.Entries) != 1 || result.Entries[0].Status != string(domain.KnowledgeCandidate) || len(result.Proposals) != 1 || result.Proposals[0].Status != string(domain.KnowledgeProposalPending) {
		t.Fatalf("submit=%s", submitted)
	}
	listed, err := call("aha_list_knowledge", map[string]any{"scope": "project", "project_id": project.ID})
	if err != nil || !strings.Contains(listed, "EQUIVALENT-TITLE") {
		t.Fatalf("list=%s err=%v", listed, err)
	}
	// The pet only proposes; the owner's decision (store-level here) makes the
	// entry current, after which the pet can read it in full.
	_, entry, err := fixture.database.ApproveKnowledgeProposal(fixture.ctx, result.Proposals[0].ID, time.Now().UTC(), "fixture owner decision")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := call("aha_knowledge_detail", map[string]any{"knowledge_id": entry.ID})
	if err != nil || !strings.Contains(detail, "EQUIVALENT-BODY") {
		t.Fatalf("detail=%s err=%v", detail, err)
	}
	// View alone keeps reads but not proposals; neither switch keeps nothing.
	settings(true, false)
	if _, err := call("aha_submit_knowledge", map[string]any{"scope": "project", "project_id": project.ID, "candidates": []map[string]any{{"type": "practice", "title": "BLOCKED", "body": "x", "confidence": 0.5, "reason": "x"}}}); err == nil {
		t.Fatal("view-only pet submitted knowledge")
	}
	if listed, err := call("aha_list_knowledge", map[string]any{"scope": "project", "project_id": project.ID}); err != nil || strings.Contains(listed, "BLOCKED") {
		t.Fatalf("view-only list=%s err=%v", listed, err)
	}
	settings(false, false)
	for _, name := range []string{"aha_list_knowledge", "aha_knowledge_detail"} {
		if _, err := call(name, map[string]any{"scope": "project", "project_id": project.ID, "knowledge_id": entry.ID}); err == nil {
			t.Fatalf("%s worked without view or knowledge permission", name)
		}
	}
}

// task_message is not offered to the pet; aha_send_task_message is its
// counterpart. Dispatched by name it records the message on the target Task
// and starts a Turn, and the live message permission switches it off.
func TestPetSendTaskMessageToolDeliversUnderMessagePermission(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := setupPetAttachmentTask(t, fixture, "消息目标")
	raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_send_task_message", json.RawMessage(`{"task_id":"`+task.ID+`","content":"PET-EQUIVALENT-MESSAGE"}`))
	if err != nil {
		t.Fatal(err)
	}
	var turn domain.Turn
	if json.Unmarshal([]byte(raw), &turn) != nil || turn.ID == "" || turn.TaskID != task.ID {
		t.Fatalf("turn=%s", raw)
	}
	messages, err := fixture.database.ListMessages(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		found = found || strings.Contains(message.Content, "PET-EQUIVALENT-MESSAGE")
	}
	if !found {
		t.Fatal("pet message was not recorded on the target Task")
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowMessageTasks: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_send_task_message", json.RawMessage(`{"task_id":"`+task.ID+`","content":"BLOCKED"}`)); err == nil {
		t.Fatal("message sent without the message permission")
	}
}

// The pet Turn receives aha2_mail and aha2_channel under exactly the rule an
// ordinary Task does: only after the owner binds a mail account to task_pet,
// and only while an owner-activated channel route points at task_pet.
func TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound(t *testing.T) {
	fixture := newPetTaskFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.service.OwnerAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	pet, err := fixture.database.Task(ctx, petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	inject := func() map[string][]string {
		t.Helper()
		servers, allowed := fixture.service.mcpRuntimeForTurn(ctx, pet, "main", "http://127.0.0.1:8766/", "")
		result := map[string][]string{}
		for _, server := range servers {
			result[server.Name] = nil
		}
		for _, name := range allowed {
			for server := range result {
				if strings.HasPrefix(name, "mcp__"+server+"__") {
					result[server] = append(result[server], strings.TrimPrefix(name, "mcp__"+server+"__"))
				}
			}
		}
		return result
	}
	before := inject()
	if _, ok := before[coreMCPName]; !ok {
		t.Fatalf("pet Turn lost core MCP: %#v", before)
	}
	if _, ok := before[mailMCPName]; ok {
		t.Fatal("unbound pet received mail MCP")
	}
	if _, ok := before[channelMCPName]; ok {
		t.Fatal("unrouted pet received channel MCP")
	}
	now := time.Now().UTC()
	account, err := fixture.database.CreateMailAccount(ctx, domain.MailAccount{ID: "mail_account_pet", Name: "Pet", Address: "pet@example.com", DisplayName: "AHA", CredentialRef: "configured", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.CreateMailBinding(ctx, domain.MailBinding{ID: "mail_binding_pet", ScopeType: domain.MailBindingScopeTask, ScopeID: pet.ID, AccountID: account.ID, ApprovalMode: domain.MailApprovalAlways, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	owner := domain.Owner{ID: "owner-pet-channel", Username: "owner-pet-channel", CreatedAt: now}
	if err := fixture.database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{ID: "pet-plugin", ProviderKey: "feishu", DisplayName: "Feishu", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
	if err := fixture.database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	instance := domain.ChannelInstance{ID: "pet-channel", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "device", Name: "pet", Status: "ready", Revision: 1, HostProjectID: pet.ProjectID, HostWorkspaceID: pet.WorkspaceID, OwnerBound: true, CredentialConfigured: true, CreatedAt: now, UpdatedAt: now}
	endpoint := domain.ChannelEndpoint{ID: "pet-endpoint", InstanceID: instance.ID, Kind: domain.ChannelEndpointAssistantDM, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := fixture.database.CreateChannelInstance(ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{ID: "pet-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 1, ScopeKey: "scope", DisplayName: "pet", Status: "active", HostTaskID: pet.ID, CreatedAt: now, UpdatedAt: now}
	if err := fixture.database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{ID: "pet-session", ConversationID: conversation.ID, Generation: 1, Mode: "task_route", Status: "active", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, pet.ID, now); err != nil {
		t.Fatal(err)
	}
	after := inject()
	want := map[string][]string{
		mailMCPName:    {"mail_list_accounts", "mail_list_templates", "mail_template_detail", "mail_preview", "mail_send", "mail_get_status"},
		channelMCPName: {"channel_reply", "channel_reply_decision", "channel_request_permission", "channel_contacts"},
	}
	for server, tools := range want {
		if strings.Join(after[server], ",") != strings.Join(tools, ",") {
			t.Fatalf("%s for bound pet = %v, want %v", server, after[server], tools)
		}
	}
}
