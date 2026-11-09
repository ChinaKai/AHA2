package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetSystemSettingsAndWorkbenchEditing(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, petTaskID)
	if err != nil || len(agents) == 0 || agents[0].Filesystem != "danger-full-access" || agents[0].Approval != "never" || agents[0].ReasoningEffort != "high" {
		t.Fatalf("pet runtime=%#v err=%v", agents, err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageKnowledge: boolPointer(true),
		AllowManageChannels: boolPointer(true), AllowManageSystem: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.UpdateProxySettings(fixture.ctx, domain.ProxySettings{
		Mode: "external", HTTPProxy: "http://owner:secret@proxy.test:8080",
		HTTPSProxy: "https://owner:secret@proxy.test:8443", NoProxy: "127.0.0.1,localhost",
		ManagedRefreshIntervalMins: 60,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := fixture.service.PetSystemSettings(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.OS == "" || settings.DataDir == "" {
		t.Fatalf("system settings=%#v", settings)
	}
	if strings.Contains(settings.Proxy.HTTPProxy, "secret") || strings.Contains(settings.Proxy.HTTPSProxy, "secret") {
		t.Fatalf("proxy credential leaked: %#v", settings.Proxy)
	}
	if !strings.Contains(settings.Proxy.HTTPProxy, "proxy.test:8080") {
		t.Fatalf("proxy endpoint missing: %#v", settings.Proxy)
	}

	applier := &stubPetSecurityApplier{}
	fixture.service.SetPetSecurityApplier(applier)
	if _, err := fixture.service.PetRequestSecuritySettingsUpdate(fixture.ctx, false, domain.AccessScopeLocal); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("security update without reason was accepted: %v", err)
	}
	approval, err := fixture.service.PetRequestSecuritySettingsUpdate(fixture.ctx, false, domain.AccessScopeLocal, "收紧本机访问范围")
	if err != nil {
		t.Fatal(err)
	}
	if approval.Kind != "security_settings_update" || approval.Status != petApprovalPending {
		t.Fatalf("security approval=%#v", approval)
	}
	if applier.calls != 0 {
		t.Fatal("security settings changed before approval")
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 || applier.validateOrigin || applier.accessScope != domain.AccessScopeLocal {
		t.Fatalf("applier calls=%d origin=%v scope=%q", applier.calls, applier.validateOrigin, applier.accessScope)
	}

	if err := fixture.service.PetSaveWorkbenchDocument(fixture.ctx, "core", "identity.md", "# 身份\n\n- 名字：小A\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetSaveWorkbenchDocument(fixture.ctx, "worklog", "2026-10-30.md", "验证并部署。"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetWriteWorklog(fixture.ctx, "2026-10-31", "通过 MCP 写工作记录。"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- 先调度再执行。\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(fixture.ctx, "loops", "# 开放闭环\n\n- [waiting] Task A | 下轮检查 | Task 完成\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetWriteWorklog(fixture.ctx, "2026/10/31", "x"); err == nil {
		t.Fatal("invalid worklog date was accepted")
	}
	if err := fixture.service.PetSaveWorkbenchDocument(fixture.ctx, "core", "../escape.md", "x"); err == nil {
		t.Fatal("invalid core document name was accepted")
	}
	if err := fixture.service.PetSaveWorkbenchDocument(fixture.ctx, "worklog", "../escape.md", "x"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	root := fixture.service.petWorkspaceRoot(fixture.ctx)
	for _, relative := range []string{"core/identity.md", "core/loops.md", "worklog/2026-10-30.md"} {
		raw, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || len(raw) == 0 {
			t.Fatalf("read %s: %v", relative, err)
		}
	}
	core, err := fixture.service.PetCoreDocuments(fixture.ctx)
	if err != nil || len(core) < 3 || !strings.Contains(core[0].Content, "小A") {
		t.Fatalf("core=%#v err=%v", core, err)
	}
	loops := fixture.service.PetOpenLoops(fixture.ctx)
	if !strings.Contains(loops, "Task A") {
		t.Fatalf("loops=%q", loops)
	}
}
