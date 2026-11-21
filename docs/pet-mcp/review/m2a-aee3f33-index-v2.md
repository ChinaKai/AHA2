# M2-a 复审索引与验证摘要 v2（固定 aee3f33）

本文件把 M2-a 的评审材料、范围和验证集中到一处，供 task-723 复审。它不改写已有材料：v1 三个包和 manifest 原样保留，sha256 见下表。M2-a 的完整 diff 与调用链源码在 v1 的 routing、chain 两个包里，本索引逐文件核对过它们是否齐全。

## 1. 要修的问题与目标

723 对 9bca378 的复审 r2 指出：Agent token 路由的实时授权按客户端 URL 判断。修复前 `authorizeAgentDomainRoute` 的写法是：

- `strings.HasSuffix(request.URL.Path, "/mcp")` 就直接放行。本意是放行 MCP 端点，但资源 ID 恰好是 `mcp` 的普通路由也会命中，例如 `GET /api/v1/agent/processes/mcp`、`processes/a%2Fmcp`、`DELETE /api/v1/agent/browser/grants/mcp`。Turn 撤权或宠物失去权限后，这些请求仍会进入 handler。
- 域靠一张 URL 前缀表猜（hardware/browser/processes/project/desktop），不在表里的 token 路由不做域检查，新增路由默认放行。

M2-a 的目标：

- 按 ServeMux 实际选中的已注册模式（method + template）授权，不看客户端 URL 的前后缀。
- 每条接受 Agent token 的注册路由都显式登记域、访问类别和权限执行方。
- 只有表中精确的 `POST .../mcp` 入口、且实际转义路径一致，才交给 MCP handler 做逐工具授权。
- 未登记的 token 路由在两个认证包装器里都默认 403，且在 handler 执行前拒绝。
- owner Web 原有的 cookie/CSRF 授权不变；Task 附件这类双鉴权资源保留 owner 入口与当前 Task 协作者读取。

## 2. 固定源码与范围

- 固定源码：`aee3f33e926e1f6345810a5de0f8d1dda77e3b51`，tree `c54bd022f66e9c5900a046d4397284a306e778bf`。
- 增量范围：`7eab7ce..aee3f33`，共 7 个文件。7eab7ce 只是 9bca378 之后的评审材料归档，`git diff 9bca378 7eab7ce -- internal cmd` 为空，所以这也是 `9bca378..aee3f33` 的全部代码改动。
- 材料提交：`9bed44d`（Archive immutable M2-a routing proof and fixed-source validation），在源 worktree 分支 `aha/task_e3d0848747ddc2018605` 上。当前 HEAD 90905af 里这些文件仍在 `docs/pet-mcp/review/`，sha256 与 manifest 一致（本次重新核对）。
- 本索引和 `m2a-aee3f33-manifest-v2.json` 所在的提交号随转交说明一起给出（文件本身无法写入自己的提交号）。

## 3. 文件清单与完整 diff 所在位置

逐文件取 `git diff --unified=3 7eab7ce aee3f33 -- <文件>`，核对它在哪个包里逐字出现（每个文件恰好出现在一个包里）：

| 文件 | 行数 | 完整 diff 所在包 |
|---|---|---|
| `docs/pet-mcp/agent-token-route-policy.tsv` | +83 −0 | `m2a-aee3f33-chain-v1.md` |
| `internal/httpapi/agent_domain_authorization.go` | +50 −13 | `m2a-aee3f33-routing-v1.md` |
| `internal/httpapi/agent_route_opaque_resource_test.go` | +186 −0 | `m2a-aee3f33-routing-v1.md` |
| `internal/httpapi/agent_token_route_policy.go` | +90 −0 | `m2a-aee3f33-routing-v1.md` |
| `internal/httpapi/agent_token_route_policy_test.go` | +242 −0 | `m2a-aee3f33-routing-v1.md` |
| `internal/httpapi/attachment_handlers_test.go` | +5 −3 | `m2a-aee3f33-routing-v1.md` |
| `internal/httpapi/helpers.go` | +5 −1 | `m2a-aee3f33-routing-v1.md` |

材料文件（v1，未改动）：

| 路径 | 字节 | sha256 |
|---|---|---|
| `docs/pet-mcp/review/m2a-aee3f33-routing-v1.md` | 56178 | `30c16537cf043d54e286b8c4f6bcfe54fcdacaaca67230c12ef47e205dffbce2` |
| `docs/pet-mcp/review/m2a-aee3f33-chain-v1.md` | 54630 | `fffd9a043c3445a889e9506896acb4e54715639b501a6224dfd2a4ba6a724694` |
| `docs/pet-mcp/review/m2a-aee3f33-validation-v1.md` | 3508 | `1868e73800d2f54360333c50dce38d83b5261d0dcdacdc06d55f849bc440c8d2` |

## 4. 关键实现与调用方（固定提交全文，已在 v1 包内）

routing 包（`m2a-aee3f33-routing-v1.md`）收录的函数全文：
- `internal/app/agent_domain_authorization.go` · `AgentDomainCallContext`
- `internal/app/agent_control.go` · `AgentCallContext`
- `internal/app/agent_core.go` · `agentSnapshotWritable`
- `internal/app/agent_core.go` · `strictSnapshotWritable`
- `internal/app/agent_hardware_mcp.go` · `AgentHardwareMCPAllowed`
- `internal/app/agent_hardware_mcp.go` · `AgentHardwareMCPWriteAllowed`
- `internal/app/agent_hardware_mcp.go` · `hardwareMCPSnapshotWritable`
- `internal/app/agent_pet_domains.go` · `petDomainReadable`
- `internal/app/agent_knowledge_mcp.go` · `agentMCPChannelBound`
- `internal/app/mcp_runtime.go` · `hardwareMCPInjected`
- `internal/httpapi/helpers.go` · `withAgentCapability`
- `internal/httpapi/helpers.go` · `withOwnerOrAgentAuth`
- `internal/httpapi/agent_state_handlers.go` · `writeAgentControlError`
- `internal/httpapi/agent_domain_authorization.go` · `authorizeAgentDomainRoute`
- `internal/httpapi/agent_handlers.go` · `agentProcessStatus`
- `internal/httpapi/browser_handlers.go` · `agentBrowserRelease`
- `internal/httpapi/agent_core_mcp.go` · `agentCoreMCP`
- `internal/httpapi/attachment_handlers.go` · `taskAttachmentContent`

chain 包（`m2a-aee3f33-chain-v1.md`）收录的函数全文：
- `internal/app/agent_domain_authorization.go` · `AgentDomainCallContext`
- `internal/app/agent_control.go` · `AgentCallContext`
- `internal/app/agent_core.go` · `agentSnapshotWritable`
- `internal/app/agent_core.go` · `strictSnapshotWritable`
- `internal/app/agent_hardware_mcp.go` · `AgentHardwareMCPAllowed`
- `internal/app/agent_hardware_mcp.go` · `AgentHardwareMCPWriteAllowed`
- `internal/app/agent_hardware_mcp.go` · `hardwareMCPSnapshotWritable`
- `internal/app/agent_pet_domains.go` · `petDomainReadable`
- `internal/app/agent_knowledge_mcp.go` · `agentMCPChannelBound`
- `internal/app/mcp_runtime.go` · `hardwareMCPInjected`
- `internal/httpapi/helpers.go` · `withAgentCapability`
- `internal/httpapi/helpers.go` · `withOwnerOrAgentAuth`
- `internal/httpapi/agent_state_handlers.go` · `writeAgentControlError`
- `internal/httpapi/agent_domain_authorization.go` · `authorizeAgentDomainRoute`
- `internal/httpapi/agent_browser_mcp.go` · `agentBrowserMCP`
- `internal/httpapi/agent_workspace_mcp.go` · `agentWorkspaceMCP`
- `internal/httpapi/agent_desktop_mcp.go` · `agentDesktopMCP`
- `internal/httpapi/agent_handlers.go` · `agentADBExec`
- `internal/httpapi/hardware_handlers.go` · `hardwareTarget`
- `internal/httpapi/agent_mcp_operation.go` · `runAgentMCPOperation`
- `internal/httpapi/helpers.go` · `withAuth`

chain 包另外列出了所有经 `withAgentCapability`/`withOwnerOrAgentAuth` 注册的路由声明原文、完整的桌面注册函数，以及注册常量与 `agentOperationContextKey` 类型，用来对账"82 个接受 Agent token 的注册模式与策略表一一对应"。

## 5. 跑过的命令与结果

v1（2026-11-20，固定副本 fixed-aee3f33，git archive）：

- `go test -json ./internal/httpapi -run '^(TestEveryAgentTokenRouteHasExplicitPolicy|TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization|TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|TestTaskAttachmentDownloadByAgentCapabilityIsScopedToTask|TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls|TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects)$' -count=1 -timeout=15m` → 退出 0，67.0s，6 个具名用例全部 run/pass
- `go vet ./internal/httpapi` → 退出 0

v2 复验（2026-11-21，重新 git archive aee3f33，2122 个普通 blob 逐个核对一致，测试进程不带真实 Agent token/URL）：

- 同上具名命令 → 退出 0，130.5s，6 个具名用例全部 run/pass
- `go vet ./internal/httpapi` → 退出 0
- httpapi 全包：在 scratch 里 `go test -c` 编译，`TMPDIR=/tmp/aha2-task722-m2a-aee3f33`（宠物 2026-11-21 16:48 批准的专用目录，跑完已删除）直接运行 `-test.count=1 -test.timeout=60m` → package pass，116.4s，411 个顶层用例全部通过。源码声明 412 个，唯一没跑的 `TestOwnerNativeTemporaryServiceDesktopPreview` 带 `//go:build windows`，Linux 上不编译。

合并 main 之后（当前 HEAD 90905af）：

- M2-a 改过的 4 个源/策略文件（`agent_token_route_policy.go`、`agent_domain_authorization.go`、`helpers.go`、`agent-token-route-policy.tsv`）从 aee3f33 到 90905af 没有任何改动。
- 同一组 6 个具名用例 → package pass，6/6 通过。
- 合并后 httpapi 全包 435 个用例通过（见 merge 90905af 的回报；main 新增的 `POST /api/v1/hardware/adb/usb-devices` 是 owner 路由，不进 Agent token 策略表）。

## 6. 与 v1 摘要相比的更正

v1 摘要里有几句写的是当时还没跑完的状态，现在以本节为准：

- "固定 9bca378 httpapi 仍在跑""aee3f33 未有全包通过结果，N3 待补"：aee3f33 的 httpapi 全包已在 v2 跑完并通过（见上）。9bca378 本身的旧全包结果仍是 40m 超时退出 1，不作任何结论。
- "M5 仍待独立登录/终端安全提交""P1c 全覆盖扫描器尚未修"：M5（7f240dc）、P1c r2（0da575e）都已另行提交评审，不属于 M2-a 范围。

## 7. 仍缺项

- 只验证了 Linux；Windows 只做过交叉编译层面的检查，没有实机。
- 反向依赖 net/http 内部的运行期对照属于 P1c，不在 M2-a 范围。
- M2-a 只改授权入口，不重构各业务 API 自身的权限；那些仍由原 handler 授权，由后续覆盖账本逐行核对。
