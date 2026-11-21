# P1c v2 固定 6d785b3 验证摘要

源码 `6d785b35e5455d06ad2e12ac6fd5d2827a9f724a`，tree `8b6209025d6dd874a931facd69fe18d8f9c23c7f`，base `322c15458441191210a1967b3d3731bd065dc5d5`。完整增量 12 个文件：

- `cmd/aha-pet-coverage/main.go`（新文件）
- `docs/pet-mcp/README.md`
- `docs/pet-mcp/coverage.tsv`
- `docs/pet-mcp/task-mcp-tools.tsv`（新文件）
- `internal/httpapi/mcp_tool_catalog.go`（新文件）
- `internal/httpapi/pet_coverage_ledger_test.go`（新文件）
- `internal/petcoverage/ledger.go`（新文件）
- `internal/petcoverage/petcoverage_test.go`（新文件）
- `internal/petcoverage/routes.go`（新文件）
- `internal/petcoverage/web.go`（新文件）
- `scripts/pet-mcp-coverage.py`（删除）
- `scripts/test_pet_mcp_coverage.py`（删除）

包边界：A `p1c-6d785b3-scanner.md`（4 个文件）、B `p1c-6d785b3-rules.md`（7 个文件）、L1/L2 `p1c-6d785b3-ledger-1.md`、`p1c-6d785b3-ledger-2.md`（coverage.tsv 新全文）、L3 `p1c-6d785b3-ledger-diff.md`（逐行对照）。

## 固定副本验证

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.1s，具名用例 7 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData)$' -count=1` → 退出 0，6.8s，具名用例 3 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.9s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA)$' -count=1 -timeout=30m` → 退出 0，70.4s，具名用例 15 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，2.2s；输出：
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.8s；输出：HTTP operations: 433 (closed 21, open 412); Web literal call sites: 284; ordinary-Task MCP tools: 61 (closed 0)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.3s；输出：coverage is incomplete: 412 open operations, 61 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage` → 退出 0，5.2s；输出：

## 结论与仍缺项

- 门禁本身：扫描、合并、证据规则和漂移检查均有合成用例与真实仓库测试；反向验证（提交前工作区）：去掉 stale 标记后 `TestMergeMarksChangedReviewedRowsStaleAndKeepsPending` 失败；在 `Handler` 里多注册一条路由后 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 失败；均已恢复。
- 账本现状：433 条操作中关闭 21 条，其余开放；普通 Task 工具账本 61 行全部 pending。`-complete` 按预期失败。旧的 425/411 等计数不再作为任何结论依据。
- 本包只修 P1c 的门禁与证据规则，不宣称任何业务覆盖完成。P1b 账本逐行复核、P2 浏览器/桌面/进程、其余 Web 管理域和普通 Task 工具对齐仍待后续进度。
- 仓库没有常规 CI 测试工作流（只有 release.yml），门禁通过 `go test ./internal/httpapi`、`go test ./internal/petcoverage` 与 `go run ./cmd/aha-pet-coverage -complete` 执行；本轮没有新增 CI 配置。
