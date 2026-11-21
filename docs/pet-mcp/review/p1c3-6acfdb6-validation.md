# P1c v3 固定 6acfdb6 验证摘要

源码 `6acfdb617caef3eb15b11ca8a474c2266e952a93`，tree `598aeccd1fbcab1c7ca798da07a52102c23e00d1`，base `cc25be47a9efb45039ee676ead1261212f074906`（P1c v2 材料提交）。完整增量 12 个文件：

- `cmd/aha-pet-coverage/main.go`
- `cmd/aha-pet-coverage/main_test.go`（新文件）
- `docs/pet-mcp/README.md`
- `docs/pet-mcp/web-call-sites.tsv`
- `internal/centersync/service_test.go`
- `internal/httpapi/pet_coverage_ledger_test.go`
- `internal/httpapi/server.go`
- `internal/petcoverage/ledger.go`
- `internal/petcoverage/petcoverage_test.go`
- `internal/petcoverage/routes.go`
- `internal/petcoverage/runtime.go`（新文件）
- `internal/petcoverage/web.go`

包边界：A `p1c3-6acfdb6-code.md`（11 个文件），B `p1c3-6acfdb6-web.md`（Web 清单）。与 v2 的 `p1c-6d785b3-*` 一起构成 P1c 完整修订。

## 固定副本验证

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.2s，具名用例 9 个全部 run/pass
- `go test -json ./cmd/aha-pet-coverage -count=1` → 退出 0，1.2s，具名用例 1 个全部 run/pass
- `go test -json ./internal/centersync -count=1` → 退出 0，2.9s，具名用例 12 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestPetCoverageScanMatchesLiveMuxes|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData|TestEveryAgentTokenRouteHasExplicitPolicy|TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization|TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|TestSecurityHeadersAllowXTermStylesWithoutInlineScripts)$' -count=1` → 退出 0，18.6s，具名用例 8 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，0.9s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA)$' -count=1 -timeout=30m` → 退出 0，68.2s，具名用例 15 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi ./internal/centersync` → 退出 0，1.2s
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.9s；输出：Handle/HandleFunc calls: 423 (route registrations 421, non-route 2) / HTTP operations: 433 (closed 21, open 412); Web API call sites: 282; ordinary-Task MCP tools: 61 (closed 0)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.4s；输出：coverage is incomplete: 412 open operations, 61 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，5.4s

## 仍缺项

- 账本结论不变：433 条操作关闭 21 条，stale 31、pending 381；工具账本 61 行全部 pending；`-complete` 按预期失败。
- `internal/httpapi/server.go` 的 `Handler()` 拆分会和 task726 对同一文件的改动一起由宠物协调合并；路由行未改。
- 仓库没有常规 CI 测试工作流，门禁依靠 `go test` 与 `go run ./cmd/aha-pet-coverage -complete`。
