# P1c r2 修订：多参数包装与漏网注册写法（固定 0da575e）

源码 `0da575e5d7703b40a207d0cce0b1d629efd78e50`，tree `b4d6c2105e0e22c9dffdc5ba92b5f10693a73a0b`。按 723 要求，本材料只覆盖 `6acfdb6..0da575e`。

范围声明：`6acfdb6..0da575e` 共 4 个提交——
- `9f54aa6 Archive immutable P1c v3 incremental review packets`
- `302d491 Prove pet access to shared browser, desktop, processes, mail and core tools`
- `37132df Archive immutable P2 shared-control evidence review packets`
- `0da575e Resolve multi-argument wrappers and refuse unseen Handle shapes in the coverage scan`

其中 9f54aa6、37132df 只有评审材料（docs/pet-mcp/review/*.md），302d491 是 P2 测试与账本（已获 APPROVE，材料 `p2-302d491-*`）。这三个提交都没有改 P1c 代码路径（`internal/petcoverage`、`cmd/aha-pet-coverage`、`internal/httpapi/pet_coverage_ledger_test.go`、`server.go`、`internal/centersync`、README），已用 `git diff 6acfdb6 37132df` 核对为空。所以下面代码包里 P1c 路径的 `6acfdb6..0da575e` 完整 diff，就是本次修订提交 `0da575e` 的全部代码改动。本次修订提交改的 7 个文件：`docs/pet-mcp/README.md`, `docs/pet-mcp/coverage.tsv`, `internal/httpapi/pet_coverage_ledger_test.go`, `internal/petcoverage/ledger.go`, `internal/petcoverage/petcoverage_test.go`, `internal/petcoverage/routes.go`, `internal/petcoverage/runtime.go`。

固定副本（git archive，blob 逐个核对）验证：

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

## L3. 逐行语义对照（37132df → 0da575e）

新账本 `docs/pet-mcp/coverage.tsv`（433 行，93406 字节，sha256 `8637e7a23136302210e8201c181b40befe2c0493aee805b77e0048d0c5c68f59`）。指纹换成 v2 方案后整列重写，git diff 约 186398 字节，超过单包上限，所以给出新文件全文（L1、L2，按行号切两段）和逐行语义对照（L3）。`6acfdb6..37132df` 之间的账本变化属于已获 APPROVE 的 P2（`p2-302d491-ops.md`）。该文件与源码一致由 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 在固定副本里验证。

- 行数不变：433；没有任何行的 status 变化；关闭行 53 条全部保留结论。它们能保留，是因为旧 v1 指纹等于当前代码按 v1 重算的值（`MergeLedger` 的迁移条件；若不等会退回 stale，`-update` 输出里也没有 stale 行）。
- 只有指纹变化（v1 → `v2:`）：421 行。
- handler/边界变化：12 行，全部是 `/api/channel-runtime/` 下的 pending 行，见下表。

| 操作 | 字段变化（旧 → 新） |
|---|---|
| `GET /api/channel-runtime/v1/deliveries/{id}/attachment` | handler: `withChannelCapability` → `channelRuntimeMediaContent`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/commands/{id}/attachment` | handler: `withChannelCapability` → `channelRuntimeUploadMedia`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/commands/{id}/complete` | handler: `withChannelCapability` → `channelRuntimeCommandComplete`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/commands/{id}/progress` | handler: `withChannelCapability` → `channelRuntimeCommandProgress`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/deliveries/{id}/ack` | handler: `withChannelCapability` → `channelRuntimeDeliveryAck`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/deliveries/{id}/media` | handler: `withChannelCapability` → `channelRuntimeMediaUploaded`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/deliveries/{id}/nack` | handler: `withChannelCapability` → `channelRuntimeDeliveryNack`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/handshake` | handler: `withChannelCapability` → `channelRuntimeHandshake`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/instances/{instance_id}/commands:claim` | handler: `withChannelCapability` → `channelRuntimeClaimCommands`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/instances/{instance_id}/deliveries:claim` | handler: `withChannelCapability` → `channelRuntimeClaimDeliveries`<br>boundary: `transport_auth` → `channel_runtime` |
| `POST /api/channel-runtime/v1/instances/{instance_id}/inbound-events` | handler: `withChannelCapability` → `channelRuntimeInbound`<br>boundary: `transport_auth` → `channel_runtime` |
| `PUT /api/channel-runtime/v1/instances/{instance_id}/health` | handler: `withChannelCapability` → `channelRuntimeHealth`<br>boundary: `transport_auth` → `channel_runtime` |
