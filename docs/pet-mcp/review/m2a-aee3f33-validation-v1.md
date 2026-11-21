# M2-a aee3f33 验证摘要 v1

固定源码 `aee3f33e926e1f6345810a5de0f8d1dda77e3b51`，tree `c54bd022f66e9c5900a046d4397284a306e778bf`。记录 `2026-11-21T07:26:40.271952+00:00`。完整功能增量是7eab7ce..aee3f33的7文件；9bca378..7eab7ce仅旧材料存档。r1/r3a/r4/r5/r6的有限通过不代表P1a或Task通过。M5、P1c与N3仍未闭环。

## 不可变来源与具名验证

用git archive得到scratch固定副本fixed-aee3f33，未改源码。以下完整命令在该副本运行，临时文件和日志保留Task scratch，真实Agent token/URL不传给测试子进程。

```sh
go test -json ./internal/httpapi -run '^(TestEveryAgentTokenRouteHasExplicitPolicy|TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization|TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|TestTaskAttachmentDownloadByAgentCapabilityIsScopedToTask|TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls|TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects)$' -count=1 -timeout=15m
go vet ./internal/httpapi
```

定向退出0，67.042s；vet退出0，1.129s。逐条读取Go JSON，不凭过滤串推断：

| 顶层用例 | run/pass |
|---|---|
| `TestEveryAgentTokenRouteHasExplicitPolicy` | 均有 |
| `TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization` | 均有 |
| `TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches` | 均有 |
| `TestTaskAttachmentDownloadByAgentCapabilityIsScopedToTask` | 均有 |
| `TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls` | 均有 |
| `TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects` | 均有 |

## 本次状态与范围

M2-a已实现并有断言回归，等待独立复审，不宣称APPROVE。82个精确注册模式显式归类；模式名含mcp/编码斜线不豁免，未知token路由默认403，双鉴权包装也走策略。owner原cookie授权保留，只读查看继续可用；交互写仍需原Main/可写权限，绝无HTTP旁路。策略表与注册AST一一对账；其他业务路由明确委托原handler授权，没有新增默认放行。

本轮7文件完整清单：

- `docs/pet-mcp/agent-token-route-policy.tsv`
- `internal/httpapi/agent_domain_authorization.go`
- `internal/httpapi/agent_route_opaque_resource_test.go`
- `internal/httpapi/agent_token_route_policy.go`
- `internal/httpapi/agent_token_route_policy_test.go`
- `internal/httpapi/attachment_handlers_test.go`
- `internal/httpapi/helpers.go`

其中6个代码/测试文件的完整diff和必要调用链在routing包；TSV完整diff及全部token注册与前次N5补证在chain包。两者的文件边界合计完整覆盖本轮增量，无遗漏、无截断，均为3行上下文。P1b/P1c/P2的前序未审范围仍见已归档recheck-9bca378-validation.md，不把本轮base当作整个Task基线。

## 全包与仍缺项

固定9bca378 app全包退出1（2400.510s，40m累计超时，359个测试pass事件、0个单用例fail事件），不能当成全包通过；固定9bca378 httpapi仍在跑。旧混合源码诊断app退出1、httpapi退出0，全部只作诊断，不能冒充固定版本验收。aee3f33未有全包通过结果，本轮只具名定向+vet通过；N3待补。

M5仍待独立登录/终端安全提交，当前源码不能据此被当成凭据泄漏已修复；P1c全覆盖扫描器尚未修，旧计数和--complete不作验收依据。Windows只fixture/代码验证，没有实机结论。不合并、push、部署或自行complete。后续补证新文件，不改已投审旧包。
