# P2 固定 302d491 验证摘要

源码 `302d49144f50effebbb61604fdc834d7fffe5249`，tree `c568e50bd7724ecd18a6acdc43b07ba41c62aa4f`，base `9f54aa62d2c1e7c47fa753d55b55ff4428283a6e`（P1c v3 材料提交）。增量 4 个文件（测试与账本，无生产代码）：

- `docs/pet-mcp/coverage.tsv`
- `docs/pet-mcp/task-mcp-tools.tsv`
- `internal/app/pet_equivalent_tools_test.go`（新文件）
- `internal/httpapi/agent_pet_shared_control_test.go`（新文件）

包边界：A `p2-302d491-impl.md`（实现现状全文，审查对象），B `p2-302d491-tests.md`，C `p2-302d491-ops.md`，D `p2-302d491-tools.md`。

## 固定副本验证

- `go test -json ./internal/petcoverage ./cmd/aha-pet-coverage -count=1` → 退出 0，6.0s
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestPetCoverageScanMatchesLiveMuxes|TestMCPToolCatalogCoversEveryBuiltInServer)$' -count=1` → 退出 0，4.2s，具名用例 3 个全部 run/pass
- `go test -json ./internal/app -run '^(TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation|TestPetKnowledgeEquivalentToolsDispatchByNameUnderLivePermissions|TestPetKnowledgeSkillAndPromptManagement|TestPetSendTaskMessageToolDeliversUnderMessagePermission|TestPetTurnGetsMailAndChannelServersOnlyWhenOwnerBound)$' -count=1 -timeout=30m` → 退出 0，51.5s，具名用例 5 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.5s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestADBDeadlineReportsPossibleExecutionOnHTTPAndMCP|TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRejectsSingleModeAndNonMain|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPAttachmentBinding|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask|TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentHardwareMCPTerminalReadPagingAndCaps|TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentLiveInboxClaimAckHTTP|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestBrowserMCPEveryOperationForOrdinaryAndPetTasks|TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut|TestDesktopMCPOperationsForPetUseOwnerShareAndLivePermission|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestLoginAndSendFailuresHaveFixedDistinctStateCodes|TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks|TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary|TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestPetMailMCPUsesOwnerBindingAndSendStaysPendingApproval|TestPetUploadsAndBindsOwnTurnAttachment|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA|TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket|TestWorkspaceMCPStartsAndStopsOnlyCurrentTaskProcess|TestWorkspaceProcessMCPForPetIsScopedAndPermissionGated)$' -count=1 -timeout=30m` → 退出 0，367.9s，具名用例 36 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi ./internal/app` → 退出 0，1.2s
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.9s；输出：Handle/HandleFunc calls: 423 (route registrations 421, non-route 2) / HTTP operations: 433 (closed 53, open 380); Web API call sites: 282; ordinary-Task MCP tools: 61 (closed 57)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.4s；输出：coverage is incomplete: 380 open operations, 4 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/httpapi ./internal/app` → 退出 0，3.9s

## 仍缺项

- 渠道 4 个工具缺宠物渠道 Turn 的调用测试；操作账本还有 380 条开放（主要是 owner Web 管理域，P3）。
- 全仓 `go test ./...` 基线（cc25be4）另行记录：并行全量时 knowledgevault/store/httpapi 共 5 个知识同步相关用例失败，单独重跑在 base a3ed6cb 与 cc25be4 上都通过；不把它算作全包通过。
- 测试临时目录：本机 Go 1.27 的 `t.TempDir()` 跟随 GOTMPDIR（用户环境固定为 scratch），所以此前设置 TMPDIR=/tmp 并没有让夹具落到 /tmp；P1c v3 包里那句"TMPDIR 放在 /tmp"字面属实但临时目录实际在 scratch，结论不受影响。
