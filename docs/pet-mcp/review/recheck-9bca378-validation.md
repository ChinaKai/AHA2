# 9bca378 复审验证摘要

记录时点 `2026-11-21T06:36:02.782919+00:00`。**完整 task-722 未完成；本摘要不是全量能力或全包测试通过结论。** 当前 task-723 只静态复审已冻结 r2，其他包、P2、P1b/P1c 均未获通过。

源码固定为 `9bca3785b3ef1d071faed06041aeb2e6de0139a7`，Git tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。从 `git archive` 解到本 Task scratch 的 `fixed-9bca378/`，没有修改该副本源码；定向结束后逐个比对 Git blob，2113 个文件一致、差异 0。工作区后续文档不改变该验证版本。r2 为 45011 字节，SHA-256 `f76eef80228d3481a96501775b19b9988a74bd94cdabf82d0d0b3132519bb814`，已冻结；补证只用新文件。

## 固定版本定向与 vet

完整实际命令如下。在固定副本执行，`TMPDIR`/`GOTMPDIR` 指向本 Task scratch；子进程去除真实 Agent token/URL 环境变量。全部设备/账号为 fixture，未操作生产。

```sh
go test -json ./internal/app ./internal/httpapi -run '^(TestBrowserAndHardwareMCPAreIndependentBuiltins|TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks|TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary|TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions|TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask|TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects|TestOwnerWebHardwarePolicyIsSeparateFromAgentSnapshot|TestReadOnlyResourceCannotConnectOrDisconnectThroughEitherAgentSurface|TestWebBindingReplacementPreservesConnectionsAndCleansOrphans|TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution|TestAgentOperationReturnsOnlyFixedFailureCode|TestPetHardwareNewResourceAvoidsExistingBindingAlias|TestHardwarePolicyReadOnlyTaskMetadataKeepsReadsAndDeniesWrites|TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim|TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls|TestAgentAPIUsesTurnCapabilityAndTaskScope|TestAgentADBExecUsesTaskDeviceAndReturnsBinary|TestAgentBrowserAPIControlsItsOwnGrantAndFailsClosed|TestAgentBrowserOldGrantCannotUseNewEndpointsOverHTTP|TestAgentBrowserActionFailsClosedAfterRelease)$' -count=1 -timeout=20m
go vet ./internal/app ./internal/httpapi ./internal/hardware
```

定向命令退出 **0**，136.834s；vet 退出 **0**，1.176s。从 Go JSON 逐个核对以下 20 个顶层用例均有 run 和 pass，skip=0、遗漏=0；不是仅凭过滤串或编译退出码推断。

| 顶层 Test | run | pass |
|---|---|---|
| `TestBrowserAndHardwareMCPAreIndependentBuiltins` | 有 | 有 |
| `TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks` | 有 | 有 |
| `TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary` | 有 | 有 |
| `TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions` | 有 | 有 |
| `TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask` | 有 | 有 |
| `TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects` | 有 | 有 |
| `TestOwnerWebHardwarePolicyIsSeparateFromAgentSnapshot` | 有 | 有 |
| `TestReadOnlyResourceCannotConnectOrDisconnectThroughEitherAgentSurface` | 有 | 有 |
| `TestWebBindingReplacementPreservesConnectionsAndCleansOrphans` | 有 | 有 |
| `TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution` | 有 | 有 |
| `TestAgentOperationReturnsOnlyFixedFailureCode` | 有 | 有 |
| `TestPetHardwareNewResourceAvoidsExistingBindingAlias` | 有 | 有 |
| `TestHardwarePolicyReadOnlyTaskMetadataKeepsReadsAndDeniesWrites` | 有 | 有 |
| `TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim` | 有 | 有 |
| `TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls` | 有 | 有 |
| `TestAgentAPIUsesTurnCapabilityAndTaskScope` | 有 | 有 |
| `TestAgentADBExecUsesTaskDeviceAndReturnsBinary` | 有 | 有 |
| `TestAgentBrowserAPIControlsItsOwnGrantAndFailsClosed` | 有 | 有 |
| `TestAgentBrowserOldGrantCannotUseNewEndpointsOverHTTP` | 有 | 有 |
| `TestAgentBrowserActionFailsClosedAfterRelease` | 有 | 有 |

## 全包真实状态

- **诊断运行** `go test ./internal/app -count=1 -timeout=40m`：退出 1，2400.529s。
- 旧 worktree 的 app 诊断全包在 40m 累计超时；超时末尾是 `TestInterruptRoundDoesNotScheduleFromPendingInbox`（59s）及 `orphan_preparing`（15s），不能说某个用例卡了 40m。运行期间改过同包源码/fixture，起始完整未提交 patch 未留存，**只作诊断，绝不作为 9bca378 的最终通过/失败证据**；该串行诊断的 httpapi 若尚未产出退出码也不算通过。
- 固定副本当前状态：`{"commit":"9bca3785b3ef1d071faed06041aeb2e6de0139a7","state":"running","package":"app","command":["go","test","-json","./internal/app","-count=1","-timeout=40m"]}`。app/httpapi 按包串行，完整命令为 `go test -json ./internal/app -count=1 -timeout=40m` 和 `go test -json ./internal/httpapi -count=1 -timeout=40m`。
- 固定版本尚无全包退出码；当前 app 仍运行，httpapi 依串行队列等待。**N3 未完成**。完整 JSON 日志持续写 scratch，后续以新补证摘要记录结果；不重写已投审 r2。

原 worktree 的失败与修补也保留为诊断：有活动 Turn 的 fixture 不应重复造 Round；新断言夹具要用真实持久 Turn/App；孤儿共享 attachment 应设为 manager 允许的只读共享。未删除失败路径，未造删减版证明。以上 20 项证据来自修订源码中原测试和新增完整 fixture。

## M/S/N 处理与包分配

| 项 | 当前落实/证据边界 | 包 |
|---|---|---|
| M1 | 保留内置目录/不可绑定保护，明确固定工具名称、读子集；精确 Test 已 run/pass | r1 |
| M2 | HTTP/MCP 同一实时授权层；同 token 撤权后 HTTP 403，假 ADB 不增；普通 Task 不受宠物管理开关约束 | r2、r3a |
| M3 | Agent connect/disconnect 必须资源 read_write；只读新连/断开均拒绝且无副作用 | r3a |
| M4 | 真实 manager + 假 loopback transport 覆盖四生命周期；宠物/普通 Task 的 Main、snapshot、资源、渠道、终态等否定表 | r3a、r3b |
| S1 | owner cookie/CSRF 路径保留原策略；只读 snapshot 的 Agent 被拒、owner 操作仍按原资源策略 | r3a |
| S2 | 封闭固定错误码，不带原 message、凭据或 endpoint | r2、r3b |
| S3 | ADB 仅执行一次，24 KiB 原始输出预算，保留退出码/原字节数/截断标志 | r3b |
| S4 | 无变化不改行/不断合法连接；新 manager 方法清孤儿，Web 实际连接状态回归 | r4 |
| S5 | 新绑定 ID 避开旧别名；空/重复/不存在/超限固定分类 | r4 |
| S6 | trim AgentID 后只查一次 TaskAgent，各域复用 | r1 |
| N1 | ≥3行上下文 diff；完整注册、token/服务层/目标/五handler、绑定写库与manager断开调用链 | r1–r4 |
| N2 | core仅自身；Inbox/附件按claims；Task.ReadOnly为视图元数据而非SQLite列；知识反馈单次解析写源 | r2、r5、r6 |
| N3 | 固定版本全包仍运行，未完成，不宣称通过 | 本摘要/后续新补证 |
| N4 | owner Web真实绑定更新、无变化/孤儿/换绑/非法不写库回归 | r4 |
| N5 | 实际enqueue/claim持久batch；宠物渠道Turn注入和HTTP/MCP调用均有拒绝证明 | r6 |

`Task.ReadOnly=true` 没有可伪造的 tasks 列：使用策略/注入单测覆盖该视图标志，真实入口使用只读 snapshot 及 owner 身份对照；远端镜像原来的 Web 拒绝分支保留。不把单测或fixture写成Windows实机验证。

## 前序范围与未审状态

**不能只取 9d01cb6 为任务基线。** 该 hash 只是这轮修复 diff 的父版本。完整任务基线始终为 `a3ed6cb`，本次固定源码 tip 为 `9bca378`。各阶段如下，均留在本 Task 分支，未合并/推送/部署。

| 阶段 | 完整范围 | 状态 |
|---|---|---|
| P1a | `a3ed6cb566680ff079d660685126a2b77a76bad8..1b8246266cbaeba6a905801bf3e28ebc85223f2b` | REQUEST_CHANGES；原包冻结 |
| P1b | `1b8246266cbaeba6a905801bf3e28ebc85223f2b..c8644634c374d89ec0bcc96ea16fb0a46df4e797` | 未审，HTTP账本 |
| P1c | `c8644634c374d89ec0bcc96ea16fb0a46df4e797..c9aabc1c52e6844933551e0265dab3cbe8b67967` | 未审，扫描器/Web调用点 |
| 材料 | `c9aabc1c52e6844933551e0265dab3cbe8b67967..4aeb68f36ae6e912ff6ba8d05e73c2191b5dd7f1` | 原三包存档，不是功能通过 |
| P2 | `4aeb68f36ae6e912ff6ba8d05e73c2191b5dd7f1..9d01cb63cca51bff23c40dad2c61d55497f2cd3a` | 未审，不能计入M2通过 |
| 修复R1 | `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..bcfd39cd64b706b966fe8673e5e47b1e575034bf` | M1/S6 |
| 修复R2 | `bcfd39cd64b706b966fe8673e5e47b1e575034bf..9bca3785b3ef1d071faed06041aeb2e6de0139a7` | M2–M4及相关S/N；本轮待审 |

原 P1a packet 内容未变。P1b/P1c 的旧自包含包仍在 `p1b-http-inventory.md`、`p1c-scanner-web.md`；P2 的能力实现尚未独立审过，本轮门禁补证不替代 P2 功能评审，后续单独补齐阶段包。

### 完整基线到 tip 文件清单

下列 62 个文件是 `git diff --name-only a3ed6cb..9bca378` 的完整集合。阶段列显示每次被修改的范围；“修复包”只代表本轮修复 diff，不能抹去前序未审改动。七个修复包对 `9d01cb6..9bca378` 的 34 个改动文件恰好覆盖一次，没有漏文件、重复归属或截断 diff。

| 文件 | 所属阶段 | 本轮修复包 |
|---|---|---|
| `docs/pet-mcp/README.md` | P1c | 前序范围，未在本轮修复diff中 |
| `docs/pet-mcp/coverage.tsv` | P1b,P2 | 前序范围，未在本轮修复diff中 |
| `docs/pet-mcp/review/p1a-runtime-hardware.md` | 材料 | 前序范围，未在本轮修复diff中 |
| `docs/pet-mcp/review/p1b-http-inventory.md` | 材料 | 前序范围，未在本轮修复diff中 |
| `docs/pet-mcp/review/p1c-scanner-web.md` | 材料 | 前序范围，未在本轮修复diff中 |
| `docs/pet-mcp/web-call-sites.tsv` | P1c | 前序范围，未在本轮修复diff中 |
| `internal/app/agent_browser_hardware_mcp_test.go` | 修复R1 | r1-catalog-agent-lookup |
| `internal/app/agent_browser_mcp.go` | P1a,P2 | 前序范围，未在本轮修复diff中 |
| `internal/app/agent_control.go` | 修复R2 | r5-own-turn-core-scope |
| `internal/app/agent_core.go` | 修复R2 | r5-own-turn-core-scope |
| `internal/app/agent_desktop_mcp.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/app/agent_domain_authorization.go` | 修复R2 | r2-live-agent-authority |
| `internal/app/agent_hardware_mcp.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/app/agent_hardware_metadata_test.go` | 修复R2 | r2-live-agent-authority |
| `internal/app/agent_knowledge_mcp.go` | P1a,修复R2 | r6-channel-knowledge-source |
| `internal/app/agent_pet_channel_scope_test.go` | 修复R2 | r6-channel-knowledge-source |
| `internal/app/agent_pet_domains.go` | P1a,修复R1 | r1-catalog-agent-lookup |
| `internal/app/agent_pet_domains_test.go` | P1a,P2,修复R2 | r4-binding-lifecycle |
| `internal/app/agent_workspace_mcp.go` | P1a,P2 | 前序范围，未在本轮修复diff中 |
| `internal/app/agent_workspace_mcp_test.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/app/knowledge_routes.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/app/mcp.go` | P1a,P2 | 前序范围，未在本轮修复diff中 |
| `internal/app/mcp_runtime.go` | P1a,P2,修复R1 | r1-catalog-agent-lookup |
| `internal/app/mcp_runtime_test.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/app/pet.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/app/pet_hardware_binding_alias_test.go` | 修复R2 | r4-binding-lifecycle |
| `internal/app/pet_task_hardware.go` | P1a,修复R2 | r4-binding-lifecycle |
| `internal/app/service.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/app/task_hardware_bindings.go` | P1a,修复R2 | r4-binding-lifecycle |
| `internal/hardware/manager.go` | 修复R2 | r4-binding-lifecycle |
| `internal/httpapi/agent_browser_mcp.go` | P2,修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_browser_mcp_operations.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_browser_mcp_test.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_browser_workspace_operations_test.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_core_mcp.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_core_pet_scope_test.go` | 修复R2 | r5-own-turn-core-scope |
| `internal/httpapi/agent_desktop_mcp.go` | P2,修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_desktop_mcp_test.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_domain_authorization.go` | 修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_handlers.go` | 修复R2 | r3b-device-effects-bounded-results |
| `internal/httpapi/agent_handlers_test.go` | 修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_hardware_bindings_test.go` | 修复R2 | r4-binding-lifecycle |
| `internal/httpapi/agent_hardware_effects_test.go` | 修复R2 | r3b-device-effects-bounded-results |
| `internal/httpapi/agent_hardware_fixture_test.go` | 修复R2 | r3b-device-effects-bounded-results |
| `internal/httpapi/agent_hardware_gate_test.go` | 修复R2 | r3a-resource-owner-boundary |
| `internal/httpapi/agent_hardware_mcp.go` | P1a,修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_hardware_mcp_operations.go` | P1a,修复R2 | r3b-device-effects-bounded-results |
| `internal/httpapi/agent_hardware_mcp_test.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_knowledge_mcp.go` | P1a,修复R2 | r6-channel-knowledge-source |
| `internal/httpapi/agent_mcp_operation.go` | P1a,P2,修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_pet_channel_authorization_test.go` | 修复R2 | r6-channel-knowledge-source |
| `internal/httpapi/agent_pet_domains_test.go` | P1a | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_state_handlers.go` | P1a,P2,修复R2 | r5-own-turn-core-scope |
| `internal/httpapi/agent_workspace_mcp.go` | P2,修复R2 | r2-live-agent-authority |
| `internal/httpapi/agent_workspace_mcp_operations.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/agent_workspace_mcp_test.go` | P2 | 前序范围，未在本轮修复diff中 |
| `internal/httpapi/browser_handlers_test.go` | 修复R2 | r2-live-agent-authority |
| `internal/httpapi/hardware_handlers.go` | P1a,修复R2 | r3a-resource-owner-boundary |
| `internal/httpapi/helpers.go` | 修复R2 | r2-live-agent-authority |
| `internal/httpapi/server.go` | P1a,P2 | 前序范围，未在本轮修复diff中 |
| `scripts/pet-mcp-coverage.py` | P1c | 前序范围，未在本轮修复diff中 |
| `scripts/test_pet_mcp_coverage.py` | P1c | 前序范围，未在本轮修复diff中 |

### 包清单

- `recheck-9bca378-r1-catalog-agent-lookup.md`：30668 字节。
- `recheck-9bca378-r2-live-agent-authority.md`：45011 字节。
- `recheck-9bca378-r3a-resource-owner-boundary.md`：57045 字节。
- `recheck-9bca378-r3b-device-effects-bounded-results.md`：62316 字节。
- `recheck-9bca378-r4-binding-lifecycle.md`：26770 字节。
- `recheck-9bca378-r5-own-turn-core-scope.md`：27253 字节。
- `recheck-9bca378-r6-channel-knowledge-source.md`：31649 字节。

固定源码完整覆盖门禁命令 `python3 scripts/pet-mcp-coverage.py --complete` 退出 1，原输出：

```text
HTTP operations: 427; Web literal call sites: 284; pending operations: 411
coverage is incomplete
```

这表示完整目标仍有缺口。工作区中尚未提交的 P2 覆盖标注不计入本摘要的固定源码或完成度。源码提交与材料存档提交分开；无审批/权限自批，也未执行生产操作。
