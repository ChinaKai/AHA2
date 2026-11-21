# 渠道工具 4 项：宠物渠道 Turn 实测（固定 cad301a）

源码 `cad301adea7385958fe73161519a0039c628b280`，tree `2fc3d5f085401af78a1afb53f9ff3890e850e94b`。范围 `ed8a05c..cad301a`，只有 1 个提交：`cad301a Close the four aha2_channel tool rows with a routed pet channel Turn test`。改了 2 个文件：新增测试 `internal/httpapi/agent_pet_channel_turn_test.go`，以及 `docs/pet-mcp/task-mcp-tools.tsv` 的 4 行。**没有改生产代码。** 文末附完整 diff。

## 1. 清单口径

宠物来信列的是 `aha_list_channels`、`aha_channel_detail`、`aha_channel_contacts`、`aha_list_channel_destinations`。它们是宠物自带的只读管理工具（`internal/app/pet.go`），不在我此前 deferred 的缺口里。它们对应 owner Web 的渠道读接口（`GET /api/v1/channel-instances*` 等），这些行在 `coverage.tsv` 仍是 pending，属于 P3 owner Web 渠道域，本轮不扩大。

我审计清单里的 4 项是普通 Task 内置渠道 MCP 服务 `aha2_channel` 的工具，也就是 `task-mcp-tools.tsv` 里此前仅有的 4 行 pending：
`channel_contacts`、`channel_reply`、`channel_reply_decision`、`channel_request_permission`。
此前的证据只到"注入正确"（`internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound`），缺"宠物渠道 Turn 实际调用"。本轮补的就是这一项。

## 2. 测试走的真实链路（进程内，隔离库）

`TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute`：
1. 夹具：隔离 store、真实 `app.Service`、真实 `channel.Service`（fake secret store）、真实 `httpapi.Server`。渠道插件、实例和群端点（`group_digital_human`）都写入隔离库，并绑定 owner 的渠道身份。`runtimeBaseURL` 为空，所以不会拉起任何插件进程。执行器用阻塞替身：Turn 一启动就停住，直到测试释放。
2. owner Web 操作（真实 HTTP handler）：`POST /api/v1/tasks/task_pet/channel-routes` 返回 201，激活群路由；`PUT /api/v1/tasks/task_pet/channel-contacts` 返回 200，指派联系人"李四"。
3. 入站：`channelService.Start` 启动后台 worker，`ReceiveInbound` 收到一条群内 @机器人消息；worker 按 task_route 把它路由给 task_pet，启动一个真实 task_pet Turn。测试等到执行器收到的 `Task.ID == task_pet`，再给这个 Turn 签发 Agent token。
4. 用该 token 经 core MCP 端点调用 4 个工具，逐项断言返回和落库结果。
5. 边界：另一个 Task（base）的 running Turn 拿 task_pet 的 receipt 调 reply/decision，必须被拒，且不得产生投递。
6. 出站：所有投递都只在隔离库里排队，没有 runtime 认领。断言每条投递 `provider_message_id` 为空、`attempts=0`。

## 3. 固定副本实测记录（t.Logf 原文，来自固定副本 `go test -json`）

```
agent_pet_channel_turn_test.go:161: evidence tools/list status=200 names=[channel_reply channel_request_permission channel_reply_decision channel_contacts] (task=task_pet turn=turn_f8362961ae81f567714a receipt=channel_inbox_f55e8963d36e6023ed97)
agent_pet_channel_turn_test.go:186: evidence channel_contacts status=200 isError=false text={"contacts":[{"collaboration_role":"测试","display_name":"李四","identity_link_id":"pet-route-participant","is_bot":false,"role":"participant"}]}
agent_pet_channel_turn_test.go:194: evidence channel_request_permission status=200 isError=false text=Permission request task_permission_74c168f5db9e93fae2d4 created and sent to the Owner.
agent_pet_channel_turn_test.go:199: evidence permission request id=task_permission_74c168f5db9e93fae2d4 status=pending receipt=channel_inbox_f55e8963d36e6023ed97
agent_pet_channel_turn_test.go:207: evidence channel_reply attempt=0 status=200 isError=false text=Reply queued for the channel.
agent_pet_channel_turn_test.go:207: evidence channel_reply attempt=1 status=200 isError=false text=Reply queued for the channel.
agent_pet_channel_turn_test.go:213: evidence reply delivery id=channel_delivery_47bafceb00e372d2a94f state=pending attempts=0 provider_message_id="" (one row after two calls)
agent_pet_channel_turn_test.go:224: evidence channel_reply_decision status=200 isError=false text=No channel reply will be sent for this message. turn.channel_reply_decision=end
agent_pet_channel_turn_test.go:236: evidence borrowed-receipt channel_reply from task_8f016bd47d6a6637239c status=200 isError=true text=sql: no rows in result set
agent_pet_channel_turn_test.go:241: evidence borrowed-receipt channel_reply_decision status=200 isError=true text=agent control operation is forbidden
agent_pet_channel_turn_test.go:246: evidence delivery id=channel_delivery_47bafceb00e372d2a94f state=pending attempts=0 provider_message_id=""
agent_pet_channel_turn_test.go:246: evidence delivery id=channel_delivery_f68e38e72f7502989071 state=pending attempts=0 provider_message_id=""
```

逐项结论：
- `tools/list`：task_pet 渠道 Turn 恰好看到 4 个渠道工具。
- `channel_contacts`：只返回 owner 指派给 task_pet 的联系人（身份链接 ID 和显示名），不含 provider 用户 ID（测试断言 `provider-user-li` 不出现）。
- `channel_request_permission`：返回成功，权限请求落库为 `pending`，关联本 receipt 和申请人；卡片只发往 owner 已绑定的渠道身份。没绑定时工具返回 isError（首次运行即如此，夹具补绑后通过），等待 owner 决定，不会自动批准。
- `channel_reply`：两次调用相同内容，只产生 1 条投递（按 Turn+receipt+内容幂等），状态 pending。
- `channel_reply_decision`：Turn 的 `channel_reply_decision=end`。
- 边界：base Task 借 receipt 调 reply 返回 isError `sql: no rows in result set`（`EnqueueAgentChannelReplyWithMentions` 按 task_id 查 receipt，查不到）；调 decision 返回 isError `agent control operation is forbidden`（`ChannelContextForReceipt` 按 task+turn 查不到）。两次都没有产生投递。这里 reply 的错误文本直接透出了内部 SQL 错误，属于提示文案，不是越权；本轮没有改。

## 4. 反向验证（证明边界断言真的能抓到回归）

在 scratch 的 git archive 副本里改源码，再跑同一个测试（工作区不动）：
- 只放宽 `ChannelContextForReceipt` 的 task/turn 过滤：测试仍通过。原因是 decision 还有第二层 `ChannelReceiptDeliveredToTurn`，reply 走的是另一条路径。这说明实现是多层防护，不是测试漏判。
- **decision 两层都放宽**（`ChannelContextForReceipt` 和 `ChannelReceiptDeliveredToTurn` 的 task/turn 条件改为恒真）后测试失败：
```
agent_pet_channel_turn_test.go:236: evidence borrowed-receipt channel_reply from task_fc6f88638604c5eba912 status=200 isError=true text=sql: no rows in result set
agent_pet_channel_turn_test.go:239: borrowed receipt decision=httpapi.coreMCPToolCall{Status:200, IsError:false, Text:"No channel reply will be sent for this message.", Code:0, Message:""}
--- FAIL: TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute (0.34s)
FAIL
```
- **reply 四层都放宽**（receipt 查询去掉 task_id、`authorized` 恒真、路由查询去掉 target_task_id、群回复资格 `state.matches` 跳过）后测试失败：
```
agent_pet_channel_turn_test.go:234: borrowed receipt reply=httpapi.coreMCPToolCall{Status:200, IsError:false, Text:"Reply queued for the channel.", Code:0, Message:""}
--- FAIL: TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute (0.37s)
FAIL
```
中间一次只放宽了前三层：测试仍通过，借用请求被第四层拒绝，isError 文本为 `group receipt is not eligible for a Task reply`。四层全部放宽后，借来的 receipt 被成功排队，测试判失败。

## 5. 固定副本验证（git archive，逐个 blob 核对）

副本由 `git archive cad301a` 解出，已逐个核对 2197 个被跟踪 blob，不一致 0 个。前 4 步来自上一轮被宠物中断的整套复验（同一副本、同一提交，结果已落盘）。中断发生在第 5 步：那一步当时用 XFS 临时目录，太慢。本轮改为在 scratch 编译测试二进制，用专用 `/tmp` 临时目录重跑（单次超时 9 分钟）；后 4 步也在本轮跑。

- `go test -json ./internal/petcoverage ./cmd/aha-pet-coverage -count=1` → 退出 0，0.6s，具名用例 15 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestPetCoverageScanMatchesLiveMuxes|TestMCPToolCatalogCoversEveryBuiltInServer)$' -count=1` → 退出 0，0.6s，具名用例 3 个全部 run/pass
- `go test -json ./internal/app -run '<app 已关闭行用例>' -count=1` → 退出 0，160.5s，具名用例 5 个全部 run/pass
- `go test -json ./internal/centersync -run '<centersync 已关闭行用例>' -count=1` → 退出 0，3.7s，具名用例 4 个全部 run/pass
- `go test -c ./internal/httpapi '&&' TMPDIR=/tmp/aha2-task722-fixch httpapi.test -test.timeout=9m -test.run '<httpapi 已关闭行用例 38 个>'` → 退出 0，13.7s，具名用例 38 个全部 run/pass（含 `TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute`）
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi ./internal/app` → 退出 0，1.2s
- `go run ./cmd/aha-pet-coverage` → 退出 0，1.0s；输出：Handle/HandleFunc calls: 424 (route registrations 422, non-route 2) / HTTP operations: 434 (closed 53, open 381); Web API call sites: 283; ordinary-Task MCP tools: 61 (closed 61)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：仍有未关闭的 HTTP 操作，门禁必须失败），0.4s；输出：coverage is incomplete: 381 open operations, 0 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/httpapi ./internal/app` → 退出 0，3.8s

`-complete` 退出 1 是预期：HTTP 操作 434 条中仍有 381 条未关闭。普通 Task 工具 61 条全部关闭。

## 6. 仍缺项

- **真实 provider 投递未验证**：回复和权限卡只排进隔离库的投递队列，没有 runtime 认领，没有任何消息离开进程。要验证 provider 侧送达，必须真实发消息，按要求停在这里，没有自行发送。
- `coverage.tsv` 中 agent 渠道 HTTP 行（`/api/v1/agent/channel/replies`、`reply-decision`、`contacts`、`handoffs` 等）仍是 pending，属于 P1b 剩余的 agent_turn 行，本轮不扩大。本测试以后可作为它们的部分证据。
- 宠物自带的 4 个 `aha_*` 渠道只读工具没有专门测试，随 P3 渠道域处理。
- main 已前进到 `f518dfc`（本分支最后一次合并 main 是 90905af）。本轮按要求不合并。

## 7. 完整 diff（`ed8a05c..cad301a`）

```diff
diff --git a/docs/pet-mcp/task-mcp-tools.tsv b/docs/pet-mcp/task-mcp-tools.tsv
index 030505b..bbeac9e 100644
--- a/docs/pet-mcp/task-mcp-tools.tsv
+++ b/docs/pet-mcp/task-mcp-tools.tsv
@@ -15,10 +15,10 @@ aha2_browser.browser_tab_close	same tool via the aha2_browser server injected in
 aha2_browser.browser_tab_open	same tool via the aha2_browser server injected into pet Turns	owner browser grant for task_pet; reads need live pet view permission, writes also Task-management permission, Main and a writable snapshot	internal/httpapi/agent_pet_shared_control_test.go:TestBrowserMCPEveryOperationForOrdinaryAndPetTasks	direct
 aha2_browser.browser_tabs_list	same tool via the aha2_browser server injected into pet Turns	owner browser grant for task_pet; reads need live pet view permission, writes also Task-management permission, Main and a writable snapshot	internal/httpapi/agent_pet_shared_control_test.go:TestBrowserMCPEveryOperationForOrdinaryAndPetTasks	direct
 aha2_browser.browser_upload	same tool via the aha2_browser server injected into pet Turns	owner browser grant for task_pet; reads need live pet view permission, writes also Task-management permission, Main and a writable snapshot	internal/httpapi/agent_pet_shared_control_test.go:TestBrowserMCPEveryOperationForOrdinaryAndPetTasks	direct
-aha2_channel.channel_contacts	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet (injection verified; a pet channel-Turn call test is still missing)	live routed channel Turn of task_pet	internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	pending
-aha2_channel.channel_reply	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet (injection verified; a pet channel-Turn call test is still missing)	live routed channel Turn of task_pet	internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	pending
-aha2_channel.channel_reply_decision	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet (injection verified; a pet channel-Turn call test is still missing)	live routed channel Turn of task_pet	internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	pending
-aha2_channel.channel_request_permission	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet (injection verified; a pet channel-Turn call test is still missing)	live routed channel Turn of task_pet	internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	pending
+aha2_channel.channel_contacts	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet; called from a pet Turn started by a routed inbound message	live routed channel Turn of task_pet; returns only the contacts the owner assigned to task_pet, by identity-link ID and display name, never the provider user ID	internal/httpapi/agent_pet_channel_turn_test.go:TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute; internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	direct
+aha2_channel.channel_reply	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet; called from a pet Turn started by a routed inbound message	live routed channel Turn of task_pet; the inbound receipt must belong to the calling Turn (another Task borrowing it is refused); a reply only queues one delivery per receipt	internal/httpapi/agent_pet_channel_turn_test.go:TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute; internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	direct
+aha2_channel.channel_reply_decision	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet; called from a pet Turn started by a routed inbound message	live routed channel Turn of task_pet; the inbound receipt must belong to the calling Turn (another Task borrowing it is refused); records the decision on the Turn only	internal/httpapi/agent_pet_channel_turn_test.go:TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute; internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	direct
+aha2_channel.channel_request_permission	same tool via the aha2_channel server, injected into pet Turns only while an owner-activated channel route points at task_pet; called from a pet Turn started by a routed inbound message	live routed channel Turn of task_pet; the card goes only to the owner's linked channel identity and the request stays pending until the owner decides	internal/httpapi/agent_pet_channel_turn_test.go:TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute; internal/app/pet_equivalent_tools_test.go:TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound	direct
 aha2_collab.collab_batch_submit	not injected: task_pet is a single-agent Task, and collaboration tools exist only for Main of collaboration-mode Tasks; the same rule applies to every single-mode Task	collaboration mode and Main	internal/httpapi/agent_collab_mcp_test.go:TestAgentCollabMCPRejectsSingleModeAndNonMain	mode_gated
 aha2_collab.collab_batch_wait	not injected: task_pet is a single-agent Task, and collaboration tools exist only for Main of collaboration-mode Tasks; the same rule applies to every single-mode Task	collaboration mode and Main	internal/httpapi/agent_collab_mcp_test.go:TestAgentCollabMCPRejectsSingleModeAndNonMain	mode_gated
 aha2_core.attachment_bind	same tool via the aha2_core server injected into pet Turns	own Turn only; writes need Main and a writable snapshot	internal/httpapi/agent_pet_shared_control_test.go:TestPetUploadsAndBindsOwnTurnAttachment	direct
diff --git a/internal/httpapi/agent_pet_channel_turn_test.go b/internal/httpapi/agent_pet_channel_turn_test.go
new file mode 100644
index 0000000..dcc2ffe
--- /dev/null
+++ b/internal/httpapi/agent_pet_channel_turn_test.go
@@ -0,0 +1,248 @@
+package httpapi
+
+import (
+	"context"
+	"encoding/json"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"path/filepath"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/agentapi"
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/channel"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+// A pet channel Turn reaches the four aha2_channel tools the same way an
+// ordinary routed Task does. The chain is real end to end inside one process:
+// the owner routes a group conversation to task_pet over the Web API, the
+// channel runtime hands in an @-mention, the channel service turns it into a
+// task_pet Turn, and that Turn's token drives the tools. No plugin runtime is
+// running and none is started, so the queued reply and permission card stay
+// in the isolated database and nothing reaches a real channel.
+func TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute(t *testing.T) {
+	ctx := context.Background()
+	dataDir := t.TempDir()
+	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer database.Close()
+	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}), done: make(chan struct{})}
+	appService := app.NewService(database, nil, executor)
+	secrets := &fakeSecretStore{values: map[string]string{}}
+	channelService := channel.New(channel.Config{Store: database, App: appService, Secrets: secrets, DataDir: dataDir})
+	capabilities := agentapi.NewCapabilities()
+	server := httptest.NewServer(New(Config{
+		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
+		App: appService, Channels: channelService, AgentCapabilities: capabilities, Secrets: secrets,
+	}).Handler())
+	defer server.Close()
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, server.URL)
+	owner, err := database.OwnerByUsername(ctx, "owner")
+	if err != nil {
+		t.Fatal(err)
+	}
+	now := time.Now().UTC()
+	activate := func(task domain.Task) {
+		t.Helper()
+		if task.Status != domain.TaskDraft {
+			return
+		}
+		stamp := now.Format(time.RFC3339Nano)
+		if err := database.UpdateTaskStatus(ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, stamp, ""); err != nil {
+			t.Fatal(err)
+		}
+		if err := database.UpdateTaskStatus(ctx, task.ID, domain.TaskPreparing, domain.TaskActive, stamp, ""); err != nil {
+			t.Fatal(err)
+		}
+	}
+	base := createHardwareAPITask(t, database)
+	activate(base)
+	pet := createPetTaskInStore(t, database, base)
+	activate(pet)
+	snapshot, err := database.RuntimeSnapshot(ctx, base.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+
+	plugin := domain.ChannelPlugin{ID: "pet-route-plugin", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
+	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
+		t.Fatal(err)
+	}
+	hostProject := domain.Project{ID: "pet-route-host-project", Name: "Channel", ProjectType: "channel", DefaultWorkspaceID: "pet-route-host-workspace", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
+	hostWorkspace := domain.Workspace{ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel", Locality: "local", Transport: "native", RootPath: dataDir, Health: "ready", AgentAPIMode: "global", AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now}
+	instance := domain.ChannelInstance{ID: "pet-route-instance", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "local", Name: "团队飞书", Status: "ready", Revision: 1, HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{"operation_scope_mode": "all", "runtime_bot_open_id": "pet-route-bot"}, CreatedAt: now, UpdatedAt: now}
+	endpoint := domain.ChannelEndpoint{ID: "pet-route-endpoint", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
+		t.Fatal(err)
+	}
+	// The owner's own channel identity, which receives permission cards.
+	if _, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{ID: "pet-route-owner", InstanceID: instance.ID, OwnerID: owner.ID, ExternalUserID: "pet-route-owner-open", Role: "owner", Status: "active", LinkedAt: now}); err != nil {
+		t.Fatal(err)
+	}
+	hostTask, err := appService.CreateTask(ctx, app.CreateTaskInput{ProjectID: hostProject.ID, WorkspaceID: hostWorkspace.ID, Title: "飞书群聊 · 宠物联调群", Request: "host", Isolation: "inplace", ModelID: snapshot.ModelID, Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "enabled", AllowSystemProject: true})
+	if err != nil {
+		t.Fatal(err)
+	}
+	conversation := domain.ChannelConversation{ID: "pet-route-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 2, ScopeKey: "pet-scope", ExternalChatID: "pet-provider-chat", DisplayName: "宠物联调群", HostTaskID: hostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now}
+	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{ID: "pet-route-session", ConversationID: conversation.ID, Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now}); err != nil {
+		t.Fatal(err)
+	}
+	if err := database.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, hostTask.ID, now); err != nil {
+		t.Fatal(err)
+	}
+	// Owner Web: route the group to task_pet and choose its contact.
+	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+pet.ID+"/channel-routes", map[string]any{"conversation_id": conversation.ID}, csrf)
+	if response.StatusCode != http.StatusCreated {
+		t.Fatalf("owner route to task_pet status=%d body=%s", response.StatusCode, readBody(t, response))
+	}
+	response.Body.Close()
+	participant, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{ID: "pet-route-participant", InstanceID: instance.ID, ExternalUserID: "provider-user-li", Role: "participant", DisplayName: "李四", Status: "active", LinkedAt: now})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, participant.ID, false, now); err != nil {
+		t.Fatal(err)
+	}
+	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+pet.ID+"/channel-contacts", map[string]any{"contacts": []map[string]any{{"identity_link_id": participant.ID, "collaboration_role": "测试"}}}, csrf)
+	if response.StatusCode != http.StatusOK {
+		t.Fatalf("owner contacts for task_pet status=%d body=%s", response.StatusCode, readBody(t, response))
+	}
+	response.Body.Close()
+
+	// Channel runtime: one @-mention; the channel service routes it to task_pet.
+	runCtx, stop := context.WithCancel(ctx)
+	defer stop()
+	channelService.Start(runCtx)
+	receipt, duplicate, err := channelService.ReceiveInbound(ctx, channel.RuntimeClaims{InstanceID: instance.ID, Scopes: map[string]bool{"channel.inbound.write": true}}, domain.ChannelInboundEnvelope{
+		SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: "pet-route-event-1", EventType: "message", OccurredAt: now,
+		ChatType: "group", ExternalChatID: conversation.ExternalChatID, ExternalSenderID: "provider-user-li", ExternalMessageID: "pet-route-message-1",
+		Content: "@AHA 帮忙看下日志", SenderDisplayName: "李四", MentionedBot: true,
+	})
+	if err != nil || duplicate {
+		t.Fatalf("inbound receipt=%#v duplicate=%v err=%v", receipt, duplicate, err)
+	}
+	var turn domain.Turn
+	select {
+	case request := <-executor.started:
+		if request.Task.ID != pet.ID {
+			t.Fatalf("routed message started a Turn for %s, want task_pet", request.Task.ID)
+		}
+		turn = request.Turn
+	case <-time.After(15 * time.Second):
+		t.Fatal("routed channel message did not start a task_pet Turn")
+	}
+	defer func() {
+		close(executor.release)
+		select {
+		case <-executor.done:
+		case <-time.After(10 * time.Second):
+		}
+		stop()
+	}()
+	token, err := capabilities.Issue(pet.ID, "main", turn.ID, time.Hour)
+	if err != nil {
+		t.Fatal(err)
+	}
+	url := server.URL + "/api/v1/agent/channel/mcp"
+	status, names, _ := coreMCPToolsList(t, url, token)
+	if status != http.StatusOK || strings.Join(names, ",") != "channel_reply,channel_request_permission,channel_reply_decision,channel_contacts" {
+		t.Fatalf("pet channel tools status=%d names=%v", status, names)
+	}
+	t.Logf("evidence tools/list status=%d names=%v (task=%s turn=%s receipt=%s)", status, names, pet.ID, turn.ID, receipt.ID)
+	deliveries := func() []domain.ChannelDelivery {
+		t.Helper()
+		items, err := database.ChannelDeliveries(ctx, instance.ID, 100)
+		if err != nil {
+			t.Fatal(err)
+		}
+		return items
+	}
+	carrying := func(marker string) []domain.ChannelDelivery {
+		var found []domain.ChannelDelivery
+		for _, item := range deliveries() {
+			payload, _ := json.Marshal(item.SemanticPayload)
+			if strings.Contains(string(payload), marker) {
+				found = append(found, item)
+			}
+		}
+		return found
+	}
+
+	// channel_contacts: the owner-chosen contact, never the provider identity.
+	contacts := coreMCPCallTool(t, url, token, "channel_contacts", map[string]any{})
+	if contacts.IsError || !strings.Contains(contacts.Text, "李四") || !strings.Contains(contacts.Text, participant.ID) || strings.Contains(contacts.Text, "provider-user-li") {
+		t.Fatalf("contacts=%#v", contacts)
+	}
+	t.Logf("evidence channel_contacts status=%d isError=%v text=%s", contacts.Status, contacts.IsError, contacts.Text)
+
+	// channel_request_permission: a pending request plus an owner card; the
+	// pet cannot approve it, the decision stays with the owner.
+	permission := coreMCPCallTool(t, url, token, "channel_request_permission", map[string]any{"action": "读取固件日志", "scope": "本群当前问题", "reason": "PET-PERMISSION-REASON", "risk": "只读"})
+	if permission.IsError || !strings.Contains(permission.Text, "Permission request") {
+		t.Fatalf("permission=%#v", permission)
+	}
+	t.Logf("evidence channel_request_permission status=%d isError=%v text=%s", permission.Status, permission.IsError, permission.Text)
+	requests, err := database.ListTaskPermissionRequests(ctx, pet.ID, 10)
+	if err != nil || len(requests) != 1 || requests[0].Status != "pending" || requests[0].InboundReceiptID != receipt.ID || requests[0].ApplicantIdentityLinkID == "" {
+		t.Fatalf("permission requests=%#v err=%v", requests, err)
+	}
+	t.Logf("evidence permission request id=%s status=%s receipt=%s", requests[0].ID, requests[0].Status, requests[0].InboundReceiptID)
+
+	// channel_reply: queued once for this receipt; a retry is idempotent.
+	for attempt := 0; attempt < 2; attempt++ {
+		reply := coreMCPCallTool(t, url, token, "channel_reply", map[string]any{"message": "PET-CHANNEL-REPLY"})
+		if reply.IsError || !strings.Contains(reply.Text, "Reply queued") {
+			t.Fatalf("reply attempt %d=%#v", attempt, reply)
+		}
+		t.Logf("evidence channel_reply attempt=%d status=%d isError=%v text=%s", attempt, reply.Status, reply.IsError, reply.Text)
+	}
+	replies := carrying("PET-CHANNEL-REPLY")
+	if len(replies) != 1 || replies[0].ConversationID != conversation.ID || replies[0].ProviderMessageID != "" || replies[0].Attempts != 0 {
+		t.Fatalf("queued replies=%#v", replies)
+	}
+	t.Logf("evidence reply delivery id=%s state=%s attempts=%d provider_message_id=%q (one row after two calls)", replies[0].ID, replies[0].State, replies[0].Attempts, replies[0].ProviderMessageID)
+
+	// channel_reply_decision: records the end decision on this Turn.
+	decision := coreMCPCallTool(t, url, token, "channel_reply_decision", map[string]any{"reason": "已在群里回复"})
+	if decision.IsError {
+		t.Fatalf("decision=%#v", decision)
+	}
+	stored, err := database.Turn(ctx, turn.ID)
+	if err != nil || stored.ChannelReplyDecision != "end" {
+		t.Fatalf("turn decision=%q err=%v", stored.ChannelReplyDecision, err)
+	}
+	t.Logf("evidence channel_reply_decision status=%d isError=%v text=%s turn.channel_reply_decision=%s", decision.Status, decision.IsError, decision.Text, stored.ChannelReplyDecision)
+
+	// Boundary: another Task's Turn cannot borrow task_pet's receipt.
+	otherTurn := createCoreTurn(t, database, base, "main", "batch-pet-route-other")
+	otherToken, err := capabilities.Issue(base.ID, "main", otherTurn.ID, time.Hour)
+	if err != nil {
+		t.Fatal(err)
+	}
+	borrowed := coreMCPCallTool(t, url, otherToken, "channel_reply", map[string]any{"message": "BORROWED-REPLY", "inbound_receipt_id": receipt.ID})
+	if !refused(borrowed) || len(carrying("BORROWED-REPLY")) != 0 {
+		t.Fatalf("borrowed receipt reply=%#v", borrowed)
+	}
+	t.Logf("evidence borrowed-receipt channel_reply from %s status=%d isError=%v text=%s", base.ID, borrowed.Status, borrowed.IsError, borrowed.Text)
+	refusedDecision := coreMCPCallTool(t, url, otherToken, "channel_reply_decision", map[string]any{"reason": "x", "inbound_receipt_id": receipt.ID})
+	if !refused(refusedDecision) {
+		t.Fatalf("borrowed receipt decision=%#v", refusedDecision)
+	}
+	t.Logf("evidence borrowed-receipt channel_reply_decision status=%d isError=%v text=%s", refusedDecision.Status, refusedDecision.IsError, refusedDecision.Text)
+	for _, item := range deliveries() {
+		if item.ProviderMessageID != "" || item.Attempts != 0 {
+			t.Fatalf("a delivery left the process: %#v", item)
+		}
+		t.Logf("evidence delivery id=%s state=%s attempts=%d provider_message_id=%q", item.ID, item.State, item.Attempts, item.ProviderMessageID)
+	}
+}
```
