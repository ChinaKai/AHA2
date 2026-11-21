# P1c r2 固定 0da575e 验证摘要

源码 `0da575e5d7703b40a207d0cce0b1d629efd78e50`，tree `b4d6c2105e0e22c9dffdc5ba92b5f10693a73a0b`，范围 `6acfdb617caef3eb15b11ca8a474c2266e952a93..0da575e5d7703b40a207d0cce0b1d629efd78e50`（P1c 代码只有本次修订提交改动，见各包范围声明）。

包：A `p1c4-0da575e-code.md`（P1c 路径完整 diff）；L1/L2 `p1c4-0da575e-ledger-1.md`、`-2.md`（新账本全文）；L3 `p1c4-0da575e-ledger-diff.md`（逐行对照）。

## 固定副本验证

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.2s，具名用例 14 个全部 run/pass
- `go test -json ./cmd/aha-pet-coverage -count=1` → 退出 0，1.2s，具名用例 1 个全部 run/pass
- `go test -json ./internal/centersync -count=1` → 退出 0，3.5s，具名用例 12 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestPetCoverageScanMatchesLiveMuxes|TestChannelRuntimeRoutesResolvePastTheirTwoArgumentWrapper|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData|TestEveryAgentTokenRouteHasExplicitPolicy|TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization|TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|TestSecurityHeadersAllowXTermStylesWithoutInlineScripts)$' -count=1` → 退出 0，22.1s，具名用例 9 个全部 run/pass
- `go test -json ./internal/app -run '^(TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation)$' -count=1 -timeout=30m` → 退出 0，10.4s，具名用例 1 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.1s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestADBDeadlineReportsPossibleExecutionOnHTTPAndMCP|TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPAttachmentBinding|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask|TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentHardwareMCPTerminalReadPagingAndCaps|TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentLiveInboxClaimAckHTTP|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestBrowserMCPEveryOperationForOrdinaryAndPetTasks|TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut|TestDesktopMCPOperationsForPetUseOwnerShareAndLivePermission|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestLoginAndSendFailuresHaveFixedDistinctStateCodes|TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks|TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary|TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestPetUploadsAndBindsOwnTurnAttachment|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA|TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket|TestWorkspaceMCPStartsAndStopsOnlyCurrentTaskProcess|TestWorkspaceProcessMCPForPetIsScopedAndPermissionGated)$' -count=1 -timeout=30m` → 退出 0，352.3s，具名用例 34 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi ./internal/centersync` → 退出 0，1.1s
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.9s；输出：Handle/HandleFunc calls: 423 (route registrations 421, non-route 2) / HTTP operations: 433 (closed 53, open 380); Web API call sites: 282; ordinary-Task MCP tools: 61 (closed 57)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.4s；输出：coverage is incomplete: 380 open operations, 4 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，5.2s

## 说明

- 本次修订后账本：433 条操作关闭 53 条，`-complete` 按预期失败；普通 Task 工具账本不变（61 个关闭 57 个）。
- 修订在 main 合入之前提交；随后会在同一 worktree 普通合并 main（12b9be1），不影响本固定提交。
