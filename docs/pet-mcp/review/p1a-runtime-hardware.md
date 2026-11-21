# P1a：公共门禁与 Turn/硬件首批能力

Base `a3ed6cb566680ff079d660685126a2b77a76bad8` → commit `1b8246266cbaeba6a905801bf3e28ebc85223f2b`。分支 `aha/task_e3d0848747ddc2018605`；worktree `/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_e3d0848747ddc2018605`。

这是完整目标中的首批实现，P1 仍未全部完成。宠物获得 core、绑定硬件、已授权浏览器和匹配 Workspace 的普通域入口；知识只补指南/反馈，复用原有知识与 Skill 管理。工具调用继续校验当前 Turn、Main、只读、渠道来源、资源绑定和实时宠物权限。普通 Task 没有新增跨项目管理权。adapter 只转入逐操作固定的 Agent handler，不接受 URL/method，不携带 Cookie、CSRF 或 Authorization，也不创建 owner 身份。

硬件绑定共用服务层，统一上限 16，保留绑定 ID 与创建时间；先校验再断开旧连接。新增 connect/disconnect/send/login/ADB；本批业务效果验证覆盖 ADB，其他设备生命周期仍待 fixture 回归。host key 信任、ADB 配对、凭据与 owner 安全交接尚待后续批次，当前工具不能执行这些操作。

## 验证

以下命令均使用 `TMPDIR="$AHA2_SCRATCH_DIR" GOTMPDIR="$AHA2_SCRATCH_DIR"`，隔离 DB/fixture，没有生产 I/O。

- `go test ./internal/app ./internal/httpapi -run 'TestPetHardwareBindingReplacement|TestPetCoreMCP|TestPetHardwareMCPExec|TestAgentMCPOperation' -count=1 -timeout=5m`：退出 0；app 7.535s，httpapi 49.404s。含真实持久状态与知识反馈、Main 拒绝、假 ADB 效果、跨资源/额外 secret 字段拒绝、权限撤销、16 资源上限及失败不写库/不掉线。
- `go vet ./internal/app ./internal/httpapi`：退出 0。
- 前一组更宽的定向测试退出 1：测试重复创建 fixture 宠物项目；HTTP 包通过。修复 fixture 后，上述命令通过。同组合修复后重跑退出 0：app 157.862s、httpapi 21.800s；命令为 `go test ./internal/app ./internal/httpapi -run 'TestPetDomainRuntime|TestPetHardwareBindingReplacement|TestPetCoreMCP|TestPetHardwareMCPExec|TestAgentMCPOperation|TestAgentHardwareMCPToolsListSchemas|TestCoreMCPRuntimeInjectionScopesTools|TestBrowserHardwareMCP|TestPetTaskHardwareBindings' -count=1 -timeout=10m`。这些是定向业务回归，不是全量验收。
- `python3 scripts/pet-mcp-coverage.py`：退出 0；425 个注册 HTTP 操作，284 个 Web 字面调用点；411 行仍未满足完整覆盖门禁。账本是路由级起点，尚需业务子操作、现有等价工具与保护交接逐项核验。`--complete` 当前预期退出 1。

关键新函数及完整实现见下方 diff：`runAgentMCPOperation`、`AgentCoreMCPToolNames`、`AgentHardwareMCPWriteAllowed`、`ReplaceTaskHardwareResourceBindings`。调用链为 MCP → 逐操作 schema/白名单 → 原 Agent handler → 原 service/硬件 manager；知识反馈复用 `feedbackKnowledge`，Web writer 与 pet writer 分开记录。以下附完整注入函数及硬件 handler 调用方，之后是该提交全部 diff（`--unified=0` 仅减少上下文，未删任何修改）。

```go
func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agentID, agentAPIURL, inboxBatchID string) ([]backend.MCPServer, []string) {
	servers, allowed := s.mcpRuntimeForTask(ctx, task, agentAPIURL)
	agentAPIURL = strings.TrimSpace(agentAPIURL)
	if agentAPIURL == "" {
		return servers, allowed
	}
	write := false
	if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
		write = s.agentSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agent.AgentID)
	}
	servers = append(servers, backend.MCPServer{
		Name: coreMCPName, Transport: "http",
		URL:            strings.TrimRight(agentAPIURL, "/") + coreMCPPath,
		URLFromEnv:     "AHA2_AGENT_API_URL",
		URLPath:        coreMCPPath,
		BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
	})
	coreNames := coreMCPToolNames(false)
	if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
		coreNames = s.coreMCPNamesForSnapshot(ctx, task, agent.RuntimeConfigSnapshotID, agent.AgentID)
	}
	for _, tool := range coreNames {
		allowed = append(allowed, "mcp__"+coreMCPName+"__"+tool)
	}
	if s.knowledgeMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: knowledgeMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + knowledgeMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        knowledgeMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range s.knowledgeMCPNamesForTask(ctx, task, write) {
			allowed = append(allowed, "mcp__"+knowledgeMCPName+"__"+tool)
		}
	}
	if s.workspaceMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: workspaceMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + workspaceMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        workspaceMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range workspaceMCPToolNames() {
			allowed = append(allowed, "mcp__"+workspaceMCPName+"__"+tool)
		}
	}
	if s.browserMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: browserMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + browserMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        browserMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range browserMCPToolNames {
			allowed = append(allowed, "mcp__"+browserMCPName+"__"+tool)
		}
	}
	if s.hardwareMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: hardwareMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + hardwareMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        hardwareMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		hardwareWrite := false
		if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
			hardwareWrite = s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
		}
		for _, tool := range HardwareMCPToolNamesFor(hardwareWrite) {
			allowed = append(allowed, "mcp__"+hardwareMCPName+"__"+tool)
		}
	}
	if s.collabMCPInjected(ctx, task, agentID, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: collabMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + collabMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        collabMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range collabMCPToolNames {
			allowed = append(allowed, "mcp__"+collabMCPName+"__"+tool)
		}
	}
	return servers, allowed
}
func (s *Server) agentConnectHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.connectHardware(writer, request)
}
```

```diff
diff --git a/internal/app/agent_browser_mcp.go b/internal/app/agent_browser_mcp.go
index f7efcf8..148187f 100644
--- a/internal/app/agent_browser_mcp.go
+++ b/internal/app/agent_browser_mcp.go
@@ -9,2 +9,2 @@ import "context"
-// for the pet Task, for a Task without an active browser grant, and for a
-// channel-hosted Turn.
+// for a Task without an active browser grant, a channel-hosted Turn, or a pet
+// whose live view permission has been revoked.
diff --git a/internal/app/agent_hardware_mcp.go b/internal/app/agent_hardware_mcp.go
index bf64bb2..71fe625 100644
--- a/internal/app/agent_hardware_mcp.go
+++ b/internal/app/agent_hardware_mcp.go
@@ -3 +3,4 @@ package app
-import "context"
+import (
+	"context"
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
@@ -8 +11 @@ import "context"
-// and never for the pet Task or a channel-hosted Turn. Every MCP method
+// and never for a channel-hosted Turn. Pet access rechecks live view permission. Every method
@@ -12,0 +16,17 @@ func (s *Service) AgentHardwareMCPAllowed(ctx context.Context, call AgentCallCon
+
+// Device I/O requires an explicitly writable Main snapshot and a writable
+// resource. The pet additionally needs its live Task-management permission.
+func (s *Service) AgentHardwareMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
+	return s.AgentHardwareMCPAllowed(ctx, call) && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
+}
+
+func (s *Service) hardwareMCPSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
+	if task.Status.Terminal() || !s.agentSnapshotWritable(ctx, task, snapshotID, agentID) {
+		return false
+	}
+	if task.ID == petTaskID {
+		assistant, err := s.petAssistant(ctx)
+		return err == nil && assistant.PetAllowCreateTasks
+	}
+	return true
+}
diff --git a/internal/app/agent_knowledge_mcp.go b/internal/app/agent_knowledge_mcp.go
index 44ffebe..071831b 100644
--- a/internal/app/agent_knowledge_mcp.go
+++ b/internal/app/agent_knowledge_mcp.go
@@ -35,3 +35,3 @@ var ErrKnowledgeMCPGuideUnavailable = errors.New("knowledge guide is unavailable
-// call re-checks it server-side. A Task with knowledge switched off, the pet
-// Task (whose root libraries are outside a Project), and any channel-bound
-// Turn (which resolves other Projects' knowledge) do not receive this domain.
+// call re-checks it server-side. Ordinary Tasks with knowledge switched off
+// and channel-bound Turns stay excluded. The pet receives only guide/feedback,
+// using its existing knowledge-management permissions and library resolution.
@@ -39,4 +39 @@ func (s *Service) AgentKnowledgeMCPAllowed(ctx context.Context, call AgentCallCo
-	if call.Task.ID == petTaskID || call.Task.ProjectID == petProjectID || !knowledgeEnabled(call.Project, call.Task) {
-		return false
-	}
-	return !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
+	return s.knowledgeMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
@@ -483,0 +481,19 @@ func (s *Service) SubmitAgentKnowledgeMCPFeedback(ctx context.Context, claims ag
+	if call, err := s.AgentCallContext(ctx, claims, true); err == nil && call.Task.ID == petTaskID {
+		if !s.AgentKnowledgeMCPAllowed(ctx, call) || !s.AgentWriteAllowed(ctx, call) {
+			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
+		}
+		if _, err := s.petKnowledgeWritable(ctx); err != nil {
+			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
+		}
+		if _, err := s.PetKnowledgeDetail(ctx, entryID); err != nil {
+			return AgentKnowledgeMCPFeedbackResult{}, ErrKnowledgeMCPEntryNotFound
+		}
+		if kind != "helped" && kind != "stale" && kind != "wrong" {
+			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
+		}
+		result, err := s.feedbackKnowledge(ctx, entryID, kind, "pet")
+		if err != nil {
+			return AgentKnowledgeMCPFeedbackResult{}, err
+		}
+		return AgentKnowledgeMCPFeedbackResult{Source: result.Source, Feedback: &AgentKnowledgeMCPFeedback{Kind: kind, ID: result.Entry.ID, Scope: result.Entry.Scope, Status: string(result.Entry.Status), Revision: result.Entry.Revision}}, nil
+	}
diff --git a/internal/app/agent_pet_domains.go b/internal/app/agent_pet_domains.go
new file mode 100644
index 0000000..2921546
--- /dev/null
+++ b/internal/app/agent_pet_domains.go
@@ -0,0 +1,57 @@
+package app
+
+import (
+	"context"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+// AgentCoreMCPToolNames is shared by discovery and dispatch. The pet already
+// has a permission-checked cross-project messaging tool; task_message retains
+// its ordinary project-scoped contract and is never widened for the pet.
+func (s *Service) AgentCoreMCPToolNames(ctx context.Context, call AgentCallContext) []string {
+	return s.coreMCPNamesForSnapshot(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
+}
+
+func (s *Service) coreMCPNamesForSnapshot(ctx context.Context, task domain.Task, snapshotID, agentID string) []string {
+	names := coreMCPToolNames(s.agentSnapshotWritable(ctx, task, snapshotID, agentID))
+	if task.ID != petTaskID {
+		return names
+	}
+	result := make([]string, 0, len(names))
+	for _, name := range names {
+		if name != "task_message" {
+			result = append(result, name)
+		}
+	}
+	return result
+}
+
+// petDomainReadable rechecks the live permission rather than trusting runtime
+// injection. No other Task in the internal pet Project inherits this access.
+func (s *Service) petDomainReadable(ctx context.Context, task domain.Task) bool {
+	if task.ID != petTaskID {
+		return task.ProjectID != petProjectID
+	}
+	if task.ProjectID != petProjectID {
+		return false
+	}
+	assistant, err := s.petAssistant(ctx)
+	return err == nil && assistant.PetAllowViewTasks
+}
+
+// AgentKnowledgeMCPToolNames adds only the missing guide/feedback operations to
+// the pet. Its existing knowledge and Skill management tools remain canonical.
+func (s *Service) AgentKnowledgeMCPToolNames(ctx context.Context, call AgentCallContext) []string {
+	return s.knowledgeMCPNamesForTask(ctx, call.Task, s.AgentWriteAllowed(ctx, call))
+}
+func (s *Service) knowledgeMCPNamesForTask(ctx context.Context, task domain.Task, write bool) []string {
+	if task.ID != petTaskID {
+		return knowledgeMCPToolNames(write)
+	}
+	names := []string{"knowledge_guide"}
+	if _, err := s.petKnowledgeWritable(ctx); err == nil && write {
+		names = append(names, "knowledge_feedback")
+	}
+	return names
+}
diff --git a/internal/app/agent_pet_domains_test.go b/internal/app/agent_pet_domains_test.go
new file mode 100644
index 0000000..703b84e
--- /dev/null
+++ b/internal/app/agent_pet_domains_test.go
@@ -0,0 +1,111 @@
+package app
+
+import (
+	"fmt"
+	"reflect"
+	"testing"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestPetDomainRuntimeUsesCurrentBindingsPermissionsAndMode(t *testing.T) {
+	f := newPetTaskFixture(t)
+	task := f.createPetDraft(t, "pet domain fixture")
+	assistant, err := f.database.EnsureOwnerAssistant(f.ctx, f.createdAt)
+	if err != nil {
+		t.Fatal(err)
+	}
+	set := func(view, manage bool) {
+		t.Helper()
+		if err := f.database.UpdateOwnerPetPermissions(f.ctx, assistant.ID, view, manage, false, false, false, true, false, false, f.createdAt); err != nil {
+			t.Fatal(err)
+		}
+	}
+	set(true, true)
+	seedHardwareMCPGroup(t, f.ctx, f.database, task)
+	seedBrowserMCPGrant(t, f.ctx, f.database, task, "pet-grant", "active")
+	servers, allowed := f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", "")
+	for _, name := range []string{coreMCPName, knowledgeMCPName, workspaceMCPName, browserMCPName, hardwareMCPName} {
+		if !hasMCPServer(servers, name) {
+			t.Fatalf("missing %s", name)
+		}
+	}
+	if hasMCPServer(servers, collabMCPName) {
+		t.Fatal("single pet received collaboration tools")
+	}
+	if len(mcpDomainTools(allowed, hardwareMCPName)) != 7 || len(mcpDomainTools(allowed, coreMCPName)) != 7 || len(mcpDomainTools(allowed, knowledgeMCPName)) != 2 {
+		t.Fatalf("unexpected tools: %v", allowed)
+	}
+	for _, name := range mcpDomainTools(allowed, coreMCPName) {
+		if name == "mcp__aha2_core__task_message" {
+			t.Fatal("peer tool widened pet management")
+		}
+	}
+	set(true, false)
+	_, allowed = f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", "")
+	if len(mcpDomainTools(allowed, hardwareMCPName)) != 2 {
+		t.Fatalf("revoked hardware write still injected: %v", allowed)
+	}
+	set(false, false)
+	servers, _ = f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", "")
+	if hasMCPServer(servers, hardwareMCPName) || hasMCPServer(servers, browserMCPName) || hasMCPServer(servers, workspaceMCPName) {
+		t.Fatal("revoked view permission still injected")
+	}
+	set(true, true)
+	lookalike := task
+	lookalike.ID = "task-pet-project-impostor"
+	if f.service.hardwareMCPInjected(f.ctx, lookalike, "") || f.service.workspaceMCPInjected(f.ctx, lookalike, "") {
+		t.Fatal("another Task inherited pet management")
+	}
+}
+
+func TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation(t *testing.T) {
+	f := newPetTaskFixture(t)
+	enablePetHardwareTestSettings(t, f, true)
+	task := f.petTask(t)
+	resources := make([]string, 17)
+	for index := range resources {
+		id := fmt.Sprintf("resource-%02d", index)
+		resources[index] = id
+		if err := f.database.UpsertHardwareResource(f.ctx, domain.HardwareResource{ID: id, Description: id, Mode: domain.HardwareModeSerial, Serial: domain.HardwareSerialConfig{Device: "FIXTURE", Baudrate: 9600}, Access: domain.HardwareAccessReadWrite, CreatedAt: f.createdAt, UpdatedAt: f.createdAt}); err != nil {
+			t.Fatal(err)
+		}
+	}
+	view, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: resources[:16]})
+	if err != nil || len(view.Bindings) != 16 {
+		t.Fatalf("16 bindings: %d err=%v", len(view.Bindings), err)
+	}
+	before, err := f.database.TaskHardwareBindings(f.ctx, task.ID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	recorder := &recordingHardwareDisconnector{}
+	f.service.SetPetHardwareDisconnector(recorder)
+	for _, ids := range [][]string{resources, {resources[0], resources[0]}, {"missing"}, {""}} {
+		if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: ids}); err == nil {
+			t.Fatalf("accepted invalid bindings %v", ids)
+		}
+		after, _ := f.database.TaskHardwareBindings(f.ctx, task.ID)
+		if !reflect.DeepEqual(before, after) || len(recorder.calls) != 0 {
+			t.Fatal("invalid replacement changed rows or disconnected devices")
+		}
+	}
+	if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: resources[:16]}); err != nil {
+		t.Fatal(err)
+	}
+	after, _ := f.database.TaskHardwareBindings(f.ctx, task.ID)
+	for index := range before {
+		if before[index].ID != after[index].ID || !before[index].CreatedAt.Equal(after[index].CreatedAt) {
+			t.Fatal("unchanged resource lost binding identity")
+		}
+	}
+	if len(recorder.calls) != 16 {
+		t.Fatalf("old connections were not closed: %v", recorder.calls)
+	}
+	if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: []string{resources[16]}}); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := f.database.HardwareGroup(f.ctx, task.ID, resources[0]); err == nil {
+		t.Fatal("removed resource still resolves")
+	}
+}
diff --git a/internal/app/agent_workspace_mcp.go b/internal/app/agent_workspace_mcp.go
index 1b75f76..16818e8 100644
--- a/internal/app/agent_workspace_mcp.go
+++ b/internal/app/agent_workspace_mcp.go
@@ -46,2 +46,2 @@ func (s *Service) SetManagedProcesses(reader ManagedProcessReader) {
-// re-checks it server-side. The pet Task, a Task without a bound Workspace and
-// any channel-hosted Turn are outside this project-scoped domain.
+// re-checks it server-side. A Task without a matching bound Workspace and any
+// channel-hosted Turn remain outside the domain; pet access needs view permission.
@@ -49,12 +49,2 @@ func (s *Service) AgentWorkspaceMCPAllowed(ctx context.Context, call AgentCallCo
-	if call.Task.ID == petTaskID || call.Task.ProjectID == petProjectID {
-		return false
-	}
-	workspaceID := strings.TrimSpace(call.Task.WorkspaceID)
-	if workspaceID == "" {
-		return false
-	}
-	item, err := s.store.Workspace(ctx, workspaceID)
-	if err != nil || item.ProjectID != call.Project.ID {
-		return false
-	}
-	return !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
+
+	return s.workspaceMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
diff --git a/internal/app/knowledge_routes.go b/internal/app/knowledge_routes.go
index da7a3ad..4cb90a3 100644
--- a/internal/app/knowledge_routes.go
+++ b/internal/app/knowledge_routes.go
@@ -185,0 +186,4 @@ func (s *Service) FeedbackWebKnowledge(ctx context.Context, id, kind string) (Kn
+	return s.feedbackKnowledge(ctx, id, kind, "web")
+}
+
+func (s *Service) feedbackKnowledge(ctx context.Context, id, kind, writer string) (KnowledgeFeedbackWriteResult, error) {
@@ -193 +197 @@ func (s *Service) FeedbackWebKnowledge(ctx context.Context, id, kind string) (Kn
-			EntryID: target.VaultEntry.ID, Kind: kind, Writer: "web",
+			EntryID: target.VaultEntry.ID, Kind: kind, Writer: writer,
diff --git a/internal/app/mcp.go b/internal/app/mcp.go
index c90e82f..0ee413f 100644
--- a/internal/app/mcp.go
+++ b/internal/app/mcp.go
@@ -279,2 +279,2 @@ func (s *Service) browserMCPDetail() domain.MCPServer {
-// domain. It is read-only: connect, disconnect, send, login, ADB exec and
-// configuration stay on the existing Agent hardware API.
+// domain. Device I/O reuses the existing Agent hardware API and is only
+// advertised to authorized writable Main Turns. Configuration is owner-managed.
@@ -283,0 +284,5 @@ func (s *Service) hardwareMCPDetail() domain.MCPServer {
+		"hardware_connect":       "连接当前 Task 已绑定的设备；保留资源权限与 host key 检查，不接收或返回凭据。",
+		"hardware_disconnect":    "断开已绑定设备连接。",
+		"hardware_send":          "向已绑定、可读写的连接发送数据。",
+		"hardware_login":         "用服务端已保存的凭据登录设备；不接收密码。",
+		"hardware_adb_exec":      "执行设备范围的 ADB 命令；不允许 host 管理或配对。",
@@ -287,2 +292,2 @@ func (s *Service) hardwareMCPDetail() domain.MCPServer {
-	tools := make([]domain.MCPTool, 0, len(hardwareMCPToolNames))
-	for _, name := range hardwareMCPToolNames {
+	tools := make([]domain.MCPTool, 0, len(HardwareMCPToolNames()))
+	for _, name := range HardwareMCPToolNames() {
@@ -293 +298 @@ func (s *Service) hardwareMCPDetail() domain.MCPServer {
-		Description: "Task 绑定硬件资源后自动注入；只读提供资源元数据与有界终端输出，不含任何连接、发送、登录、ADB 执行或配置能力。",
+		Description: "Task 绑定硬件资源后自动注入；提供元数据与有界终端读取，Main 在当前权限允许时可连接、断开、发送、登录和执行 ADB；凭据与信任仍由 owner 管理。",
diff --git a/internal/app/mcp_runtime.go b/internal/app/mcp_runtime.go
index 84f0890..fcfbfda 100644
--- a/internal/app/mcp_runtime.go
+++ b/internal/app/mcp_runtime.go
@@ -67 +67 @@ const (
-	// browser grant (and is not the pet Task or a channel-hosted Turn). The
+	// browser grant and the Turn is not channel-hosted. The
@@ -75,2 +75,2 @@ const (
-	// needs at least one bound hardware group. The first slice is read-only:
-	// bound resource metadata and bounded terminal output.
+	// needs at least one bound hardware group. Read tools expose metadata and
+	// bounded terminal output; Main writes require a writable snapshot.
@@ -210,2 +210,2 @@ func BrowserMCPToolIsRead(name string) bool {
-// hardwareMCPToolNames is the complete Task hardware first slice. It never
-// includes connect/disconnect/send/login/ADB exec/config or firmware tools.
+// hardwareMCPToolNames is the Task hardware read subset. Device I/O is
+// advertised separately to authorized writable Main Turns.
@@ -219 +219 @@ func HardwareMCPToolNames() []string {
-	return append([]string{}, hardwareMCPToolNames...)
+	return HardwareMCPToolNamesFor(true)
@@ -222,2 +222,20 @@ func HardwareMCPToolNames() []string {
-// HardwareMCPToolIsRead reports whether a hardware tool is in the read-only
-// first slice. There is intentionally no write tool in this domain.
+var hardwareMCPWriteToolNames = []string{"hardware_connect", "hardware_disconnect", "hardware_send", "hardware_login", "hardware_adb_exec"}
+
+func HardwareMCPToolNamesFor(write bool) []string {
+	names := append([]string{}, hardwareMCPToolNames...)
+	if write {
+		names = append(names, hardwareMCPWriteToolNames...)
+	}
+	return names
+}
+
+func HardwareMCPToolIsKnown(name string) bool {
+	for _, item := range HardwareMCPToolNamesFor(true) {
+		if item == name {
+			return true
+		}
+	}
+	return false
+}
+
+// HardwareMCPToolIsRead distinguishes metadata/terminal reads from device I/O.
@@ -259,2 +277,2 @@ func CollabMCPToolIsKnown(name string) bool {
-// belongs to the same Project; the pet Task and channel-hosted Turns are
-// excluded by the shared rules. inboxBatchID is the Turn's actual batch.
+// belongs to the same Project. Pet access requires its live view permission;
+// channel-hosted Turns remain excluded. inboxBatchID is the Turn's actual batch.
@@ -262 +280 @@ func (s *Service) workspaceMCPInjected(ctx context.Context, task domain.Task, in
-	if task.ID == petTaskID || task.ProjectID == petProjectID {
+	if !s.petDomainReadable(ctx, task) {
@@ -279,2 +297,2 @@ func (s *Service) workspaceMCPInjected(ctx context.Context, task domain.Task, in
-// advertised at all. The pet Task and channel-hosted Turns are excluded by the
-// shared rules. A grant whose session is already closed does not count, which
+// advertised at all. Pet access requires its live view permission; channel-hosted
+// Turns remain excluded. A grant whose session is already closed does not count, which
@@ -283 +301 @@ func (s *Service) browserMCPInjected(ctx context.Context, task domain.Task, inbo
-	if task.ID == petTaskID || task.ProjectID == petProjectID {
+	if !s.petDomainReadable(ctx, task) {
@@ -311 +329 @@ func (s *Service) hardwareMCPInjected(ctx context.Context, task domain.Task, inb
-	if task.ID == petTaskID || task.ProjectID == petProjectID {
+	if !s.petDomainReadable(ctx, task) {
@@ -322,4 +340,3 @@ func (s *Service) hardwareMCPInjected(ctx context.Context, task domain.Task, inb
-// which has no AgentCallContext yet. The pet Task is excluded because its root
-// libraries are outside a Project, and channel-bound Turns (task_route or
-// conversation_host) are excluded because their knowledge resolves from
-// another source. inboxBatchID is the Turn's actual batch, not just the Task.
+// which has no AgentCallContext yet. The pet receives only the missing guide
+// and feedback tools; its existing management tools own the library scope.
+// Channel-bound Turns remain excluded because they resolve another source. inboxBatchID is the Turn's actual batch, not just the Task.
@@ -326,0 +344,4 @@ func (s *Service) knowledgeMCPInjected(ctx context.Context, task domain.Task, in
+	if task.ID == petTaskID {
+		_, err := s.petKnowledgeReadable(ctx)
+		return task.ProjectID == petProjectID && err == nil && !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
+	}
@@ -472,3 +493,3 @@ func (s *Service) mcpRuntimeForTask(ctx context.Context, task domain.Task, agent
-// The core server is only injected when an Agent API URL exists and the Task is
-// not the pet Task. A writable Main Turn receives all tools; everyone else —
-// non-Main collaborators and read-only review Tasks — receives read tools only.
+// The core server is injected when an Agent API URL exists. A writable Main
+// receives state tools; pet cross-project messages use its management gate. Other
+// non-Main collaborators and read-only review Tasks receive read tools only.
@@ -479 +500 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
-	if agentAPIURL == "" || task.ID == petTaskID {
+	if agentAPIURL == "" {
@@ -493 +514,5 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
-	for _, tool := range coreMCPToolNames(write) {
+	coreNames := coreMCPToolNames(false)
+	if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
+		coreNames = s.coreMCPNamesForSnapshot(ctx, task, agent.RuntimeConfigSnapshotID, agent.AgentID)
+	}
+	for _, tool := range coreNames {
@@ -504 +529 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
-		for _, tool := range knowledgeMCPToolNames(write) {
+		for _, tool := range s.knowledgeMCPNamesForTask(ctx, task, write) {
@@ -540 +565,5 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
-		for _, tool := range hardwareMCPToolNames {
+		hardwareWrite := false
+		if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
+			hardwareWrite = s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
+		}
+		for _, tool := range HardwareMCPToolNamesFor(hardwareWrite) {
diff --git a/internal/app/mcp_runtime_test.go b/internal/app/mcp_runtime_test.go
index a9ce691..22ec838 100644
--- a/internal/app/mcp_runtime_test.go
+++ b/internal/app/mcp_runtime_test.go
@@ -210 +210 @@ func TestCoreMCPRuntimeInjectionScopesTools(t *testing.T) {
-		{name: "pet task", task: domain.Task{ID: petTaskID}, agentID: "main", apiURL: "http://127.0.0.1:8766/", wantCore: false},
+		{name: "pet without snapshot gets read core", task: domain.Task{ID: petTaskID}, agentID: "main", apiURL: "http://127.0.0.1:8766/", wantTools: CoreMCPToolNames(false), wantCore: true},
diff --git a/internal/app/pet.go b/internal/app/pet.go
index dc64160..977c372 100644
--- a/internal/app/pet.go
+++ b/internal/app/pet.go
@@ -1789 +1789 @@ func ahaPetTools() []gateway.ChatTool {
-				"resource_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
+				"resource_ids": map[string]any{"type": "array", "maxItems": MaxTaskHardwareResources, "uniqueItems": true, "items": map[string]any{"type": "string"}},
diff --git a/internal/app/pet_task_hardware.go b/internal/app/pet_task_hardware.go
index 7a30974..66c7f0e 100644
--- a/internal/app/pet_task_hardware.go
+++ b/internal/app/pet_task_hardware.go
@@ -134,0 +135,8 @@ func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTask
+	existing, err := s.store.TaskHardwareBindings(ctx, task.ID)
+	if err != nil {
+		return PetTaskHardwareView{}, err
+	}
+	byResource := map[string]string{}
+	for _, item := range existing {
+		byResource[item.ResourceID] = item.ID
+	}
@@ -136 +143,0 @@ func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTask
-	seen := map[string]bool{}
@@ -139,10 +146 @@ func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTask
-		if resourceID == "" || seen[resourceID] {
-			continue
-		}
-		seen[resourceID] = true
-		if _, err := s.store.HardwareResource(ctx, resourceID); err != nil {
-			return PetTaskHardwareView{}, fmt.Errorf("硬件资源 %s 不存在", resourceID)
-		}
-		bindings = append(bindings, domain.TaskHardwareBinding{
-			TaskID: task.ID, ID: domain.NewID("hardware_binding"), ResourceID: resourceID,
-		})
+		bindings = append(bindings, domain.TaskHardwareBinding{TaskID: task.ID, ID: byResource[resourceID], ResourceID: resourceID})
@@ -150,4 +148 @@ func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTask
-	if len(bindings) > 8 {
-		return PetTaskHardwareView{}, fmt.Errorf("一个 Task 最多绑定 8 个硬件资源")
-	}
-	if err := s.store.ReplaceTaskHardwareBindings(ctx, task.ID, bindings); err != nil {
+	if err := s.ReplaceTaskHardwareResourceBindings(ctx, task.ID, bindings); err != nil {
@@ -155,0 +151 @@ func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTask
+
diff --git a/internal/app/task_hardware_bindings.go b/internal/app/task_hardware_bindings.go
new file mode 100644
index 0000000..c2c4477
--- /dev/null
+++ b/internal/app/task_hardware_bindings.go
@@ -0,0 +1,68 @@
+package app
+
+import (
+	"context"
+	"fmt"
+	"strings"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
+)
+
+const MaxTaskHardwareResources = 16
+
+// ReplaceTaskHardwareResourceBindings is shared by Web and pet management.
+// Caller-specific owner/Task permissions are checked before entering here.
+// Validation completes before any live connection is closed or row is changed.
+func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
+	if len(bindings) > MaxTaskHardwareResources {
+		return fmt.Errorf("每个任务最多启用 %d 个硬件资源", MaxTaskHardwareResources)
+	}
+	if _, err := s.store.Task(ctx, taskID); err != nil {
+		return err
+	}
+	existing, err := s.store.TaskHardwareBindings(ctx, taskID)
+	if err != nil {
+		return err
+	}
+	byID := map[string]domain.TaskHardwareBinding{}
+	for _, item := range existing {
+		byID[item.ID] = item
+	}
+	ids, resources := map[string]bool{}, map[string]bool{}
+	now := s.now().UTC()
+	for index := range bindings {
+		item := &bindings[index]
+		item.TaskID = taskID
+		item.ResourceID = strings.TrimSpace(item.ResourceID)
+		item.ID = hardwareconfig.NormalizeID(item.ID)
+		if item.ID == "" {
+			item.ID = hardwareconfig.NormalizeID(item.ResourceID)
+		}
+		if item.ID == "" || item.ResourceID == "" || ids[item.ID] || resources[item.ResourceID] {
+			return fmt.Errorf("硬件绑定为空或重复")
+		}
+		if _, err := s.store.HardwareResource(ctx, item.ResourceID); err != nil {
+			return fmt.Errorf("硬件资源不存在")
+		}
+		ids[item.ID], resources[item.ResourceID] = true, true
+		item.CreatedAt, item.UpdatedAt = now, now
+		if previous, ok := byID[item.ID]; ok && previous.ResourceID == item.ResourceID {
+			item.CreatedAt = previous.CreatedAt
+		}
+	}
+	groups, err := s.store.HardwareGroups(ctx, taskID)
+	if err != nil {
+		return err
+	}
+	if s.petHardware != nil {
+		for _, group := range groups {
+			for _, transport := range []string{domain.HardwareTransportSerial, domain.HardwareTransportNetwork} {
+				if group.Supports(transport) {
+					s.petHardware.Disconnect(taskID, group.ID, transport)
+				}
+			}
+		}
+	}
+	return s.store.ReplaceTaskHardwareBindings(ctx, taskID, bindings)
+}
diff --git a/internal/httpapi/agent_core_mcp.go b/internal/httpapi/agent_core_mcp.go
index ab601e9..7a320ce 100644
--- a/internal/httpapi/agent_core_mcp.go
+++ b/internal/httpapi/agent_core_mcp.go
@@ -92 +92 @@ func (s *Server) agentCoreMCP(writer http.ResponseWriter, request *http.Request)
-		respond(map[string]any{"tools": agentCoreMCPTools(s.app.AgentWriteAllowed(request.Context(), call))})
+		respond(map[string]any{"tools": agentCoreMCPToolsNamed(s.app.AgentCoreMCPToolNames(request.Context(), call))})
@@ -194 +194 @@ func (s *Server) callAgentCoreMCPTool(writer http.ResponseWriter, request *http.
-		if !s.app.AgentWriteAllowed(request.Context(), call) {
+		if !coreMCPNameIncluded(s.app.AgentCoreMCPToolNames(request.Context(), call), input.Name) {
@@ -399,0 +400,9 @@ func coreMCPArgumentKeys(raw json.RawMessage) []string {
+func coreMCPNameIncluded(names []string, name string) bool {
+	for _, item := range names {
+		if item == name {
+			return true
+		}
+	}
+	return false
+}
+
@@ -400,0 +410,4 @@ func agentCoreMCPTools(write bool) []mcpTool {
+	return agentCoreMCPToolsNamed(app.CoreMCPToolNames(write))
+}
+
+func agentCoreMCPToolsNamed(names []string) []mcpTool {
@@ -406 +419 @@ func agentCoreMCPTools(write bool) []mcpTool {
-	for _, name := range app.CoreMCPToolNames(write) {
+	for _, name := range names {
diff --git a/internal/httpapi/agent_hardware_mcp.go b/internal/httpapi/agent_hardware_mcp.go
index eea36dd..efb680c 100644
--- a/internal/httpapi/agent_hardware_mcp.go
+++ b/internal/httpapi/agent_hardware_mcp.go
@@ -120 +120 @@ func (s *Server) agentHardwareMCP(writer http.ResponseWriter, request *http.Requ
-		respond(map[string]any{"tools": agentHardwareMCPTools()})
+		respond(map[string]any{"tools": s.hardwareMCPToolsFor(s.app.AgentHardwareMCPWriteAllowed(request.Context(), call))})
@@ -195 +195 @@ func (s *Server) callAgentHardwareMCPTool(writer http.ResponseWriter, request *h
-	if !app.HardwareMCPToolIsRead(input.Name) {
+	if !app.HardwareMCPToolIsKnown(input.Name) {
@@ -215,0 +216,21 @@ func (s *Server) callAgentHardwareMCPTool(writer http.ResponseWriter, request *h
+	if !app.HardwareMCPToolIsRead(input.Name) {
+		if !s.app.AgentHardwareMCPWriteAllowed(request.Context(), call) {
+			auditResult = "denied"
+			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
+			return
+		}
+		for _, operation := range s.hardwareMCPOperations() {
+			if operation.Tool.Name != input.Name {
+				continue
+			}
+			result, err := runAgentMCPOperation(request, operation, input.Arguments)
+			if err != nil {
+				auditResult = "error"
+				toolError(err)
+				return
+			}
+			respondJSON(result)
+			return
+		}
+	}
+
@@ -318 +339 @@ func (s *Server) agentHardwareMCPList(ctx context.Context, call app.AgentCallCon
-		readOnly := readOnlyTask || !group.Writable()
+		readOnly := readOnlyTask || call.Task.ReadOnly || !group.Writable()
diff --git a/internal/httpapi/agent_hardware_mcp_operations.go b/internal/httpapi/agent_hardware_mcp_operations.go
new file mode 100644
index 0000000..49482cf
--- /dev/null
+++ b/internal/httpapi/agent_hardware_mcp_operations.go
@@ -0,0 +1,43 @@
+package httpapi
+
+import "net/http"
+
+func (s *Server) hardwareMCPOperations() []agentMCPOperation {
+	base := func(extra map[string]any) map[string]any {
+		fields := map[string]any{"hardware_id": mcpString(), "transport": mcpEnum("serial", "network")}
+		for key, value := range extra {
+			fields[key] = value
+		}
+		return fields
+	}
+	operation := func(name, description string, handler http.HandlerFunc, fields map[string]any, required ...string) agentMCPOperation {
+		return agentMCPOperation{
+			Tool:    mcpTool{Name: name, Description: description, InputSchema: mcpObject(base(fields), append([]string{"hardware_id"}, required...)...)},
+			Handler: handler, Method: http.MethodPost, Write: true,
+			PathFields: map[string]string{"hardware_id": "hardware"}, QueryFields: []string{"transport"}, ResultFields: []string{"ok"},
+		}
+	}
+	connect := operation("hardware_connect", "Connect a device already bound to this Task using stored credentials. Requires Main and writable resource access. Unknown SSH host keys require owner confirmation in Web. Read connection state with hardware_list.", s.agentConnectHardware, nil)
+	disconnect := operation("hardware_disconnect", "Disconnect one bound device transport. Does not change bindings or credentials.", s.agentDisconnectHardware, nil)
+	send := operation("hardware_send", "Send data to a bound writable connected device. No credentials are accepted; use hardware_login for stored-credential login. Read output with hardware_terminal_read.", s.agentSendHardware, map[string]any{"data": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "encoding": mcpEnum("text", "base64")}, "data")
+	login := operation("hardware_login", "Start stored-credential login on a bound writable device. Credential values are never accepted or returned. Read connection state and terminal output separately.", s.agentLoginHardware, map[string]any{
+		"wakeup": map[string]any{"type": "boolean"}, "line_ending": mcpEnum("lf", "crlf", "cr"),
+		"timeout_seconds": mcpInteger(1, 120), "retries": mcpInteger(0, 5),
+	})
+	adb := operation("hardware_adb_exec", "Run a device-scoped ADB command against a bound writable ADB resource. Host commands, pairing, host-key trust and credentials are excluded. Uses the same command validation, timeout and cancellation as the Agent API.", s.agentADBExec, map[string]any{
+		"args":         map[string]any{"type": "array", "items": mcpString(), "minItems": 1, "maxItems": 64},
+		"stdin_base64": map[string]any{"type": "string", "maxLength": 65536}, "timeout_seconds": mcpInteger(1, 120),
+	}, "args")
+	adb.ResultFields = []string{"ok", "exit_code", "stdout_base64", "stderr_base64", "duration_ms"}
+	return []agentMCPOperation{connect, disconnect, send, login, adb}
+}
+
+func (s *Server) hardwareMCPToolsFor(write bool) []mcpTool {
+	tools := agentHardwareMCPTools()
+	if write {
+		for _, op := range s.hardwareMCPOperations() {
+			tools = append(tools, op.Tool)
+		}
+	}
+	return tools
+}
diff --git a/internal/httpapi/agent_hardware_mcp_test.go b/internal/httpapi/agent_hardware_mcp_test.go
index 15fa8f8..cd713b2 100644
--- a/internal/httpapi/agent_hardware_mcp_test.go
+++ b/internal/httpapi/agent_hardware_mcp_test.go
@@ -196 +196 @@ func TestAgentHardwareMCPToolsListSchemas(t *testing.T) {
-	want := []string{"hardware_list", "hardware_terminal_read"}
+	want := []string{"hardware_adb_exec", "hardware_connect", "hardware_disconnect", "hardware_list", "hardware_login", "hardware_send", "hardware_terminal_read"}
@@ -219 +219 @@ func TestAgentHardwareMCPToolsListSchemas(t *testing.T) {
-	for _, forbidden := range []string{"hardware_connect", "hardware_send", "hardware_login", "hardware_adb_exec", "hardware_port_forward"} {
+	for _, forbidden := range []string{"hardware_port_forward", "hardware_trust_host_key", "hardware_pair", "hardware_configure"} {
diff --git a/internal/httpapi/agent_knowledge_mcp.go b/internal/httpapi/agent_knowledge_mcp.go
index 26b2cc6..c5a2ac2 100644
--- a/internal/httpapi/agent_knowledge_mcp.go
+++ b/internal/httpapi/agent_knowledge_mcp.go
@@ -90,2 +90 @@ func (s *Server) agentKnowledgeMCP(writer http.ResponseWriter, request *http.Req
-		write := s.app.AgentWriteAllowed(request.Context(), call)
-		respond(map[string]any{"tools": agentKnowledgeMCPTools(write)})
+		respond(map[string]any{"tools": agentKnowledgeMCPToolsNamed(s.app.AgentKnowledgeMCPToolNames(request.Context(), call))})
@@ -192,0 +192,6 @@ func (s *Server) callAgentKnowledgeMCPTool(writer http.ResponseWriter, request *
+	if app.IsPetTaskID(call.Task.ID) && !coreMCPNameIncluded(s.app.AgentKnowledgeMCPToolNames(request.Context(), call), input.Name) {
+		auditResult = "denied"
+		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
+		return
+	}
+
@@ -378,0 +384,4 @@ func agentKnowledgeMCPTools(write bool) []mcpTool {
+	return agentKnowledgeMCPToolsNamed(app.KnowledgeMCPToolNames(write))
+}
+
+func agentKnowledgeMCPToolsNamed(names []string) []mcpTool {
@@ -384 +393 @@ func agentKnowledgeMCPTools(write bool) []mcpTool {
-	for _, name := range app.KnowledgeMCPToolNames(write) {
+	for _, name := range names {
diff --git a/internal/httpapi/agent_mcp_operation.go b/internal/httpapi/agent_mcp_operation.go
new file mode 100644
index 0000000..d7556b9
--- /dev/null
+++ b/internal/httpapi/agent_mcp_operation.go
@@ -0,0 +1,239 @@
+package httpapi
+
+import (
+	"bytes"
+	"encoding/base64"
+	"encoding/json"
+	"errors"
+	"fmt"
+	"io"
+	"net/http"
+	"net/url"
+)
+
+// agentMCPOperation binds one named, schema-checked tool to an existing Agent
+// handler. Neither a URL nor an HTTP method is accepted from the model. The
+// original capability context is retained; no owner identity is manufactured.
+type agentMCPOperation struct {
+	Tool         mcpTool
+	Handler      http.HandlerFunc
+	Method       string
+	PathFields   map[string]string
+	QueryFields  []string
+	Write        bool
+	ResultFields []string
+	Binary       bool
+}
+
+func mcpObject(properties map[string]any, required ...string) map[string]any {
+	if required == nil {
+		required = []string{}
+	}
+	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
+}
+
+func mcpString() map[string]any {
+	return map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}
+}
+func mcpEnum(values ...string) map[string]any {
+	return map[string]any{"type": "string", "enum": values}
+}
+func mcpInteger(min, max int) map[string]any {
+	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
+}
+
+// validateMCPOperation enforces the supported schema vocabulary server-side.
+// All operation schemas use this finite subset. Unknown input keys, nulls,
+// wrong types and out-of-range values fail before any business handler runs.
+func validateMCPOperation(value any, schema map[string]any) error {
+	fail := errors.New("invalid tool arguments")
+	switch schema["type"] {
+	case "object":
+		object, ok := value.(map[string]any)
+		if !ok {
+			return fail
+		}
+		properties, ok := schema["properties"].(map[string]any)
+		if !ok {
+			return fail
+		}
+		for key, child := range object {
+			spec, ok := properties[key].(map[string]any)
+			if !ok {
+				return fail
+			}
+			if err := validateMCPOperation(child, spec); err != nil {
+				return fail
+			}
+		}
+		for _, key := range schema["required"].([]string) {
+			if _, ok := object[key]; !ok {
+				return fail
+			}
+		}
+	case "string":
+		text, ok := value.(string)
+		if !ok {
+			return fail
+		}
+		if min, ok := schema["minLength"].(int); ok && len([]rune(text)) < min {
+			return fail
+		}
+		if max, ok := schema["maxLength"].(int); ok && len([]rune(text)) > max {
+			return fail
+		}
+		if values, ok := schema["enum"].([]string); ok {
+			found := false
+			for _, item := range values {
+				if text == item {
+					found = true
+				}
+			}
+			if !found {
+				return fail
+			}
+		}
+	case "integer", "number":
+		number, ok := value.(json.Number)
+		if !ok {
+			return fail
+		}
+		var n float64
+		if schema["type"] == "integer" {
+			i, err := number.Int64()
+			if err != nil {
+				return fail
+			}
+			n = float64(i)
+		} else {
+			var err error
+			n, err = number.Float64()
+			if err != nil {
+				return fail
+			}
+		}
+		if min, ok := schema["minimum"].(int); ok && n < float64(min) {
+			return fail
+		}
+		if max, ok := schema["maximum"].(int); ok && n > float64(max) {
+			return fail
+		}
+	case "boolean":
+		if _, ok := value.(bool); !ok {
+			return fail
+		}
+	case "array":
+		items, ok := value.([]any)
+		if !ok {
+			return fail
+		}
+		if min, ok := schema["minItems"].(int); ok && len(items) < min {
+			return fail
+		}
+		if max, ok := schema["maxItems"].(int); ok && len(items) > max {
+			return fail
+		}
+		child, ok := schema["items"].(map[string]any)
+		if !ok {
+			return fail
+		}
+		for _, item := range items {
+			if validateMCPOperation(item, child) != nil {
+				return fail
+			}
+		}
+	default:
+		return fail
+	}
+	return nil
+}
+
+// mcpOperationResponse bounds buffered results even if a handler writes more
+// than the model-facing budget. Headers (including cookies) are never copied.
+type mcpOperationResponse struct {
+	header   http.Header
+	status   int
+	body     bytes.Buffer
+	overflow bool
+}
+
+func (r *mcpOperationResponse) Header() http.Header { return r.header }
+func (r *mcpOperationResponse) WriteHeader(status int) {
+	if r.status == 0 {
+		r.status = status
+	}
+}
+func (r *mcpOperationResponse) Write(p []byte) (int, error) {
+	if r.status == 0 {
+		r.status = http.StatusOK
+	}
+	if r.body.Len()+len(p) > 2<<20 {
+		r.overflow = true
+		return len(p), nil
+	}
+	return r.body.Write(p)
+}
+
+func runAgentMCPOperation(request *http.Request, op agentMCPOperation, raw json.RawMessage) (map[string]any, error) {
+	decoder := json.NewDecoder(bytes.NewReader(raw))
+	decoder.UseNumber()
+	var arguments map[string]any
+	if decoder.Decode(&arguments) != nil || validateMCPOperation(arguments, op.Tool.InputSchema) != nil {
+		return nil, errors.New("invalid tool arguments")
+	}
+	if decoder.Decode(new(any)) != io.EOF {
+		return nil, errors.New("invalid tool arguments")
+	}
+	child := request.Clone(request.Context())
+	child.Method = op.Method
+	child.Header = make(http.Header)
+	child.URL = &url.URL{Path: request.URL.Path}
+	query := url.Values{}
+	for field, pathName := range op.PathFields {
+		if value, ok := arguments[field]; ok {
+			child.SetPathValue(pathName, fmt.Sprint(value))
+			delete(arguments, field)
+		}
+	}
+	for _, field := range op.QueryFields {
+		if value, ok := arguments[field]; ok {
+			query.Set(field, fmt.Sprint(value))
+			delete(arguments, field)
+		}
+	}
+	child.URL.RawQuery = query.Encode()
+	body, err := json.Marshal(arguments)
+	if err != nil {
+		return nil, errors.New("invalid tool arguments")
+	}
+	child.Header.Set("Content-Type", "application/json")
+	child.Body = io.NopCloser(bytes.NewReader(body))
+	child.ContentLength = int64(len(body))
+	response := &mcpOperationResponse{header: make(http.Header)}
+	op.Handler(response, child)
+	// Raw runtime errors can contain endpoints or credentials. Only a fixed
+	// classification leaves this boundary, and no body is logged here.
+	if response.status >= 400 {
+		return nil, errors.New("operation failed; check resource permissions and state")
+	}
+	if response.overflow {
+		return nil, errors.New("operation response exceeds limit")
+	}
+	if op.Binary {
+		return map[string]any{"media_type": response.header.Get("Content-Type"), "content_base64": base64.StdEncoding.EncodeToString(response.body.Bytes())}, nil
+	}
+	var result map[string]any
+	if json.Unmarshal(response.body.Bytes(), &result) != nil {
+		return nil, errors.New("operation result unavailable")
+	}
+	if op.ResultFields != nil {
+		view := map[string]any{}
+		for _, key := range op.ResultFields {
+			if value, ok := result[key]; ok {
+				view[key] = value
+			}
+		}
+		return view, nil
+	}
+	return result, nil
+}
diff --git a/internal/httpapi/agent_pet_domains_test.go b/internal/httpapi/agent_pet_domains_test.go
new file mode 100644
index 0000000..8f4f043
--- /dev/null
+++ b/internal/httpapi/agent_pet_domains_test.go
@@ -0,0 +1,212 @@
+package httpapi
+
+import (
+	"context"
+	"encoding/json"
+	"net/http"
+	"net/http/httptest"
+	"strings"
+	"sync/atomic"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+)
+
+func createPetDomainHTTPTask(t *testing.T, h *coreMCPHarness, base domain.Task) domain.Task {
+	t.Helper()
+	now := time.Now().UTC()
+	if err := h.database.CreateProject(h.ctx, domain.Project{ID: "project_pet", Name: "Pet", ProjectType: "system", CreatedAt: now, UpdatedAt: now}); err != nil {
+		t.Fatal(err)
+	}
+	if err := h.database.CreateWorkspace(h.ctx, domain.Workspace{ID: "workspace_pet", ProjectID: "project_pet", Name: "Pet", Transport: "native", Locality: "local", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}); err != nil {
+		t.Fatal(err)
+	}
+	snapshot, err := h.database.RuntimeSnapshot(h.ctx, base.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	task, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{ID: "task_pet", ProjectID: "project_pet", WorkspaceID: "workspace_pet", Title: "Pet", Request: "fixture only", ModelID: snapshot.ModelID, Filesystem: "danger-full-access", Approval: "never", CollaborationMode: "single", MaxAgents: 1, AllowSystemProject: true, StartMode: "manual"})
+	if err != nil {
+		t.Fatal(err)
+	}
+	assistant, err := h.database.EnsureOwnerAssistant(h.ctx, now)
+	if err != nil {
+		t.Fatal(err)
+	}
+	assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks = true, true
+	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks, false, false, false, true, false, false, now); err != nil {
+		t.Fatal(err)
+	}
+	return task
+}
+
+func TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	base := createHardwareAPITask(t, h.database)
+	task := createPetDomainHTTPTask(t, h, base)
+	turn := createCoreTurn(t, h.database, task, "main", "")
+	token := h.issue(task.ID, "main", turn.ID)
+	url := h.server.URL + "/api/v1/agent/core/mcp"
+	status, names, _ := coreMCPToolsList(t, url, token)
+	if status != http.StatusOK || !coreMCPNameIncluded(names, "memory_update") || coreMCPNameIncluded(names, "task_message") {
+		t.Fatalf("discovery status=%d names=%v", status, names)
+	}
+	for _, item := range []struct {
+		name string
+		args any
+	}{
+		{"memory_update", map[string]any{"status": "Pet persisted state", "next_steps": []string{"continue"}, "open_questions": []string{}}},
+		{"progress", map[string]any{"message": "Pet structured progress"}},
+		{"capabilities", map[string]any{}},
+	} {
+		result := coreMCPCallTool(t, url, token, item.name, item.args)
+		if result.Status != http.StatusOK || result.IsError || result.Code != 0 {
+			t.Fatalf("%s: %#v", item.name, result)
+		}
+	}
+
+	knowledgeURL := h.server.URL + "/api/v1/agent/knowledge/mcp"
+	_, knowledgeNames, _ := coreMCPToolsList(t, knowledgeURL, token)
+	if len(knowledgeNames) != 2 || !coreMCPNameIncluded(knowledgeNames, "knowledge_guide") || !coreMCPNameIncluded(knowledgeNames, "knowledge_feedback") {
+		t.Fatalf("pet duplicates or misses knowledge operations: %v", knowledgeNames)
+	}
+	entry := domain.KnowledgeEntry{ID: "pet-feedback-fixture", Scope: "global", Type: "practice", Title: "Fixture", Body: "BODY-MUST-NOT-BE-ECHOED", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
+	if err := h.database.CreateKnowledge(h.ctx, entry); err != nil {
+		t.Fatal(err)
+	}
+	feedback := coreMCPCallTool(t, knowledgeURL, token, "knowledge_feedback", map[string]any{"entry_id": entry.ID, "kind": "helped"})
+	if feedback.Status != http.StatusOK || feedback.IsError || strings.Contains(feedback.Text, entry.Body) {
+		t.Fatalf("feedback=%#v", feedback)
+	}
+	persisted, err := h.database.Knowledge(h.ctx, entry.ID)
+	if err != nil || persisted.HelpedCount != 1 {
+		t.Fatalf("feedback was not persisted: count=%d err=%v", persisted.HelpedCount, err)
+	}
+	guide := coreMCPCallTool(t, knowledgeURL, token, "knowledge_guide", map[string]any{})
+	if guide.Status != http.StatusOK || guide.IsError {
+		t.Fatalf("guide=%#v", guide)
+	}
+	memory, err := h.database.TaskMemory(h.ctx, task.ID)
+	if err != nil || memory.Status != "Pet persisted state" {
+		t.Fatalf("memory=%#v err=%v", memory, err)
+	}
+	denied := coreMCPCallTool(t, url, token, "task_message", map[string]any{"target_task_id": base.ID, "content": "must not cross projects"})
+	if denied.Status != http.StatusForbidden {
+		t.Fatalf("pet management bypass: %#v", denied)
+	}
+	turn.Status = domain.TurnSucceeded
+	if err := h.database.UpdateTurn(h.ctx, turn, domain.TurnRunning); err != nil {
+		t.Fatal(err)
+	}
+	sub := turn
+	sub.ID, sub.AgentID, sub.Status = domain.NewID("turn"), "sub", domain.TurnRunning
+	if _, err := h.database.UpsertTaskAgent(h.ctx, domain.TaskAgent{TaskID: task.ID, AgentID: "sub", Role: "sub", Status: "idle", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
+		t.Fatal(err)
+	}
+	if err := h.database.CreateTurn(h.ctx, sub); err != nil {
+		t.Fatal(err)
+	}
+	subToken := h.issue(task.ID, "sub", sub.ID)
+	denied = coreMCPCallTool(t, url, subToken, "memory_update", map[string]any{"status": "forbidden"})
+	if denied.Status != http.StatusForbidden {
+		t.Fatalf("sub changed state: %#v", denied)
+	}
+	after, _ := h.database.TaskMemory(h.ctx, task.ID)
+	if after.Status != memory.Status {
+		t.Fatalf("denied call changed memory: %#v", after)
+	}
+}
+
+func TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	base := createHardwareAPITask(t, h.database)
+	task := createPetDomainHTTPTask(t, h, base)
+	now := time.Now().UTC()
+	if err := h.database.ReplaceHardwareGroups(h.ctx, task.ID, []domain.HardwareGroup{{TaskID: task.ID, ID: "device", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.24", Port: 5555, Protocol: domain.HardwareProtocolADB}, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}}); err != nil {
+		t.Fatal(err)
+	}
+	var executions atomic.Int32
+	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, ExecuteADB: func(_ctx context.Context, endpoint string, args []string, stdin []byte, timeout time.Duration) (hardware.ADBExecResult, error) {
+		executions.Add(1)
+		if endpoint != "192.0.2.24:5555" || strings.Join(args, " ") != "shell id" {
+			t.Errorf("wrong bound target or command")
+		}
+		return hardware.ADBExecResult{Stdout: []byte("fixture-result"), ExitCode: 0}, nil
+	}}).Handler())
+	t.Cleanup(server.Close)
+	turn := createCoreTurn(t, h.database, task, "main", "")
+	token := h.issue(task.ID, "main", turn.ID)
+	url := server.URL + "/api/v1/agent/hardware/mcp"
+	status, names, _ := coreMCPToolsList(t, url, token)
+	if status != http.StatusOK || !coreMCPNameIncluded(names, "hardware_adb_exec") {
+		t.Fatalf("tools=%v status=%d", names, status)
+	}
+	args := map[string]any{"hardware_id": "device", "args": []string{"shell", "id"}}
+	result := coreMCPCallTool(t, url, token, "hardware_adb_exec", args)
+	if result.Status != http.StatusOK || result.IsError || !strings.Contains(result.Text, "Zml4dHVyZS1yZXN1bHQ=") || strings.Contains(result.Text, "192.0.2.24") || executions.Load() != 1 {
+		t.Fatalf("exec result=%#v count=%d", result, executions.Load())
+	}
+	for _, invalid := range []map[string]any{
+		{"hardware_id": "other-task-device", "args": []string{"shell", "id"}},
+		{"hardware_id": "device", "args": []string{"pair"}},
+		{"hardware_id": "device", "args": []string{"shell", "id"}, "password": "MCP-SECRET-MARKER"},
+		{"hardware_id": "device", "args": []string{"shell", "id"}, "task_id": base.ID},
+	} {
+		denied := coreMCPCallTool(t, url, token, "hardware_adb_exec", invalid)
+		if !denied.IsError && denied.Status == http.StatusOK {
+			t.Fatalf("invalid call succeeded: %#v", denied)
+		}
+		if strings.Contains(denied.Text, "MCP-SECRET-MARKER") {
+			t.Fatal("secret echoed")
+		}
+	}
+	if executions.Load() != 1 {
+		t.Fatalf("rejected calls executed: %d", executions.Load())
+	}
+	assistant, _ := h.database.EnsureOwnerAssistant(h.ctx, now)
+	assistant.PetAllowCreateTasks = false
+	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks, false, false, false, false, false, false, now); err != nil {
+		t.Fatal(err)
+	}
+	_, names, _ = coreMCPToolsList(t, url, token)
+	if coreMCPNameIncluded(names, "hardware_adb_exec") {
+		t.Fatal("revoked write tool still visible")
+	}
+	denied := coreMCPCallTool(t, url, token, "hardware_adb_exec", args)
+	if denied.Status != http.StatusForbidden || executions.Load() != 1 {
+		t.Fatalf("revoked permission executed: %#v", denied)
+	}
+	assistant.PetAllowViewTasks = false
+	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, assistant.PetAllowViewTasks, assistant.PetAllowCreateTasks, false, false, false, false, false, false, now); err != nil {
+		t.Fatal(err)
+	}
+	denied = coreMCPCallTool(t, url, token, "hardware_list", map[string]any{})
+	if denied.Status != http.StatusForbidden {
+		t.Fatalf("revoked read still works: %#v", denied)
+	}
+}
+
+func TestAgentMCPOperationRejectsInvalidBeforeHandlerAndSanitizesResults(t *testing.T) {
+	called := 0
+	op := agentMCPOperation{Tool: mcpTool{InputSchema: mcpObject(map[string]any{"n": mcpInteger(1, 2)}, "n")}, Method: http.MethodPost, ResultFields: []string{"ok"}, Handler: func(w http.ResponseWriter, r *http.Request) {
+		called++
+		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "password": "SECRET-IN-HANDLER"})
+	}}
+	request := httptest.NewRequest(http.MethodPost, "/fixed", nil)
+	for _, raw := range []string{`null`, `[]`, `{"n":null}`, `{"n":1.5}`, `{"n":3}`, `{"n":1,"url":"http://localhost"}`, `{"n":1} {}`} {
+		if _, err := runAgentMCPOperation(request, op, json.RawMessage(raw)); err == nil {
+			t.Fatalf("accepted %s", raw)
+		}
+	}
+	if called != 0 {
+		t.Fatal("invalid input reached handler")
+	}
+	value, err := runAgentMCPOperation(request, op, json.RawMessage(`{"n":1}`))
+	encoded, _ := json.Marshal(value)
+	if err != nil || called != 1 || strings.Contains(string(encoded), "SECRET") {
+		t.Fatalf("value=%s err=%v calls=%d", encoded, err, called)
+	}
+}
diff --git a/internal/httpapi/agent_state_handlers.go b/internal/httpapi/agent_state_handlers.go
index e955b35..7863db1 100644
--- a/internal/httpapi/agent_state_handlers.go
+++ b/internal/httpapi/agent_state_handlers.go
@@ -45 +45,7 @@ func (s *Server) agentCapabilityMap(ctx context.Context, claims agentapi.Claims,
-		"progress": true, "live_inbox_wait": true, "knowledge_read": true, "knowledge_feedback": true,
+		"core_mcp": true, "core_mcp_write": s.app.AgentWriteAllowed(ctx, call),
+		"knowledge_mcp":      s.app.AgentKnowledgeMCPAllowed(ctx, call),
+		"workspace_mcp":      s.app.AgentWorkspaceMCPAllowed(ctx, call),
+		"browser_mcp":        s.app.AgentBrowserMCPAllowed(ctx, call),
+		"hardware_mcp":       s.app.AgentHardwareMCPAllowed(ctx, call),
+		"hardware_mcp_write": s.app.AgentHardwareMCPWriteAllowed(ctx, call),
+		"progress":           true, "live_inbox_wait": true, "knowledge_read": true, "knowledge_feedback": true,
diff --git a/internal/httpapi/hardware_handlers.go b/internal/httpapi/hardware_handlers.go
index d688177..12f0f25 100644
--- a/internal/httpapi/hardware_handlers.go
+++ b/internal/httpapi/hardware_handlers.go
@@ -11,0 +12 @@ import (
+	"github.com/ChinaKai/AHA2/internal/app"
@@ -350 +351 @@ func (s *Server) updateTaskHardwareBindings(writer http.ResponseWriter, request
-	if len(payload.Bindings) > 16 {
+	if len(payload.Bindings) > app.MaxTaskHardwareResources {
@@ -393,5 +394,2 @@ func (s *Server) updateTaskHardwareBindings(writer http.ResponseWriter, request
-	if s.hardware != nil {
-		s.hardware.DisconnectTask(taskID)
-	}
-	if err := s.store.ReplaceTaskHardwareBindings(request.Context(), taskID, bindings); err != nil {
-		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_hardware_bindings_failed", "message": err.Error()})
+	if err := s.app.ReplaceTaskHardwareResourceBindings(request.Context(), taskID, bindings); err != nil {
+		writeError(writer, http.StatusBadRequest, "update_hardware_bindings_failed")
@@ -399,0 +398 @@ func (s *Server) updateTaskHardwareBindings(writer http.ResponseWriter, request
+
@@ -573 +572 @@ func (s *Server) connectHardware(writer http.ResponseWriter, request *http.Reque
-			"hardware_id": group.ID, "transport": transport, "error": err.Error(),
+			"hardware_id": group.ID, "transport": transport, "error": "hardware_connect_failed",
@@ -728 +727 @@ func (s *Server) hardwareTarget(
-	readOnly := task.Status.Terminal() || !group.Writable()
+	readOnly := task.Status.Terminal() || task.ReadOnly || !group.Writable()
diff --git a/internal/httpapi/server.go b/internal/httpapi/server.go
index 4446ebd..6387d1a 100644
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -247,0 +248,3 @@ func New(config Config) *Server {
+	if server.app != nil && server.hardware != nil {
+		server.app.SetPetHardwareDisconnector(server.hardware)
+	}
@@ -602 +605 @@ func (s *Server) Handler() http.Handler {
-	// Task hardware domain MCP. Read-only first slice with an independent
+	// Task hardware domain MCP. Read and guarded device I/O with an independent
```
