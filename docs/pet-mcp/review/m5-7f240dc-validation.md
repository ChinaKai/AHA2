# M5 固定 7f240dc 验证摘要

源码 `7f240dcd2a12a479b3954dc842961790a5a5b2de`，tree `309b9d42b5db5ec8734f27edd1c96b722f46b993`，base `9bed44dc1bb53be6ab869416cd9a2acdaa886194`。完整增量 18 个文件：

- `internal/hardware/history_secret.go`
- `internal/hardware/login_security_test.go`
- `internal/hardware/login_state.go`
- `internal/hardware/manager.go`
- `internal/hardware/manager_test.go`
- `internal/hardware/secret_stream.go`
- `internal/httpapi/agent_handlers.go`
- `internal/httpapi/agent_hardware_effects_test.go`
- `internal/httpapi/agent_hardware_fixture_test.go`
- `internal/httpapi/agent_hardware_mcp.go`
- `internal/httpapi/agent_hardware_mcp_operations.go`
- `internal/httpapi/agent_mcp_operation.go`
- `internal/httpapi/hardware_adb_execution_state_test.go`
- `internal/httpapi/hardware_error_codes.go`
- `internal/httpapi/hardware_handlers.go`
- `internal/httpapi/hardware_history.go`
- `internal/httpapi/hardware_login_security_test.go`
- `internal/httpapi/hardware_websocket.go`

包 A（`m5-7f240dc-hardware.md`）覆盖 `internal/hardware/` 的 6 个文件，包 B（`m5-7f240dc-surfaces.md`）覆盖 `internal/httpapi/` 的 12 个文件，合计无遗漏、无截断。

## 固定副本验证

- `go test -json ./internal/hardware -count=1 -race -timeout=15m` → 退出 0，5.4s，具名用例 37 个全部 run/pass，遗漏 0
- `go test -json ./internal/httpapi -run 'Hardware|ADB|Login|Terminal|Credential|HostKey' -count=1 -timeout=40m` → 退出 0，274.3s，具名用例 41 个全部 run/pass，遗漏 0
- `go vet ./internal/hardware ./internal/httpapi` → 退出 0，1.1s

## 具名用例清单

### `go test -json ./internal/hardware`

`TestAppendTrustedSSHHostKeyRejectsChangedKey`, `TestBannerMentioningFailureBeforeAnyCredentialDoesNotAbortLogin`, `TestBase64FallbackInput`, `TestConnectADBAcceptsReadyDevice`, `TestConnectADBReportsOfflineDevice`, `TestConnectADBReportsUnauthorizedDevice`, `TestConnectADBRestartsServerBeforeGivingUp`, `TestConnectADBWaitsForLateAuthorization`, `TestDecodeHexAcceptsCommonByteFormats`, `TestDisconnectDisarmsWriterEvenWhileReadOnlyObserverRemains`, `TestExecuteADBReturnsBinaryResultAndExitCode`, `TestFailureAndFreshPromptInOneReadReportFailureWithoutResend`, `TestHistoryDefenseCannotReassembleCredentialAcrossPages`, `TestHistoryDefenseMasksRetainedSplitEventsWithoutChangingCursors`, `TestKnownCredentialDoesNotEscapeConnectionErrorsStatusOrLogs`, `TestLoginEchoIsMaskedAndAuthenticatedLoginCannotRearm`, `TestLoginFailureTimeoutAndCancelDisarmAndPermitExplicitBudgetedRetry`, `TestLoginReplayIsIdempotentAndExplicitRetriesHaveFiniteBudget`, `TestManagerExplicitSerialLoginRecognizesExistingShell`, `TestManagerExplicitSerialLoginWakesConsoleAndUsesConfiguredLineEnding`, `TestManagerLiveSubscriptionRawInputAndResize`, `TestManagerNetworkADBUsesDeviceShell`, `TestManagerNetworkSSHUsesInteractiveTerminal`, `TestManagerNetworkTelnetAndAutoLogin`, `TestManagerSerialReadWriteAndPermission`, `TestOpenADBTerminalConnectsAndStartsDeviceShell`, `TestOpenSSHTerminalInteractiveShell`, `TestPairADBUsesPairingEndpointAndRedactsCode`, `TestRecordFailureIsCountedLoggedAndShownOnStatus`, `TestResolveADBExecutableFindsWingetPackage`, `TestSSHHostKeyProbeAndExplicitTrust`, `TestSSHKnownHostsCallback`, `TestSSHRequiresKnownHostsAndAuthentication`, `TestSecretStreamProtectsEveryChunkBoundaryAndFinalPrefix`, `TestSharedHardwareAllowsOnlyOneReadWriteTask`, `TestStreamDecoderPreservesSplitUTF8Bytes`, `TestTelnetCodecStripsNegotiation`

### `go test -json ./internal/httpapi`

`TestADBDeadlineReportsPossibleExecutionOnHTTPAndMCP`, `TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution`, `TestADBPairingFailureMessagesAreActionable`, `TestAgentADBExecUsesTaskDeviceAndReturnsBinary`, `TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask`, `TestAgentHardwareMCPBoundsOversizedRequestID`, `TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields`, `TestAgentHardwareMCPOnlyAuditsVerifiedIdentifiers`, `TestAgentHardwareMCPRejectsChannelBoundTurns`, `TestAgentHardwareMCPRequiresCapabilityAndDomainGate`, `TestAgentHardwareMCPTerminalCursorNeverRewinds`, `TestAgentHardwareMCPTerminalReadBoundsCompleteResponse`, `TestAgentHardwareMCPTerminalReadEnforcesResponseBudget`, `TestAgentHardwareMCPTerminalReadPagingAndCaps`, `TestAgentHardwareMCPToolsListSchemas`, `TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects`, `TestCreateTaskClonesLocalSourceHardware`, `TestFitHardwareMCPTerminalResponseRejectsOversizedEnvelope`, `TestHardwareADBPairUsesSeparatePortAndDoesNotPersistCode`, `TestHardwareAPIConfigNetworkTerminalAndPermissions`, `TestHardwareConnectFailureIsAudited`, `TestHardwareIORetentionEndpoints`, `TestHardwareResourcesAreSystemLevelAndBindToTasks`, `TestHardwareSSHHostKeyRequiresExplicitFingerprintConfirmation`, `TestHardwareTerminalWebSocketStreamsRawBytes`, `TestKnowledgeVaultImportEndpointLocalAndCredentialSafety`, `TestLocalOwnerIngestRetryFingerprintAndTerminalTask`, `TestLoginAndSendFailuresHaveFixedDistinctStateCodes`, `TestMailAccountCRUDDoesNotEchoCredential`, `TestMailBindingsRejectTerminalTaskButAllowDelete`, `TestMailOwnerMutationsAreAuditedWithoutCredential`, `TestModelHealthCheckMissingModelAndCredential`, `TestOwnerWebHardwarePolicyIsSeparateFromAgentSnapshot`, `TestPasswordRecoveryRotatesSessionsAndCredentials`, `TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions`, `TestRealADBOutputLimitReportsPossibleExecutionWithoutRetry`, `TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces`, `TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket`, `TestTaskMailOptionsAreOwnerScopedAndCredentialFree`, `TestWorkspaceSSHCredentialsStayInSecretStore`, `TestWorkspaceSSHHostKeyRequiresExplicitFingerprintConfirmation`

## 反向验证（提交前工作区，非固定副本）

在提交前的工作区里，临时去掉对应修复后重跑，确认新测试确实能抓到问题，随后恢复源码（恢复后 `possibleContinuation`/`containsLoginPrompt` 引用计数核对一致）：

- 关闭页首接续遮蔽：`TestHistoryDefenseCannotReassembleCredentialAcrossPages` 与 `TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces`（MCP 分页 50 页时露出凭据片段）失败。
- 关闭"凭据发送后失败提示任意位置生效"：`TestFailureAndFreshPromptInOneReadReportFailureWithoutResend` 失败（5s 内未到 failed）。

提交前工作区另跑：`go test ./internal/hardware -count=1 -race` 退出 0；`go test ./internal/httpapi -run 'Hardware|ADB|Login|Terminal|Credential|HostKey' -count=1` 退出 0（350.1s）；`go vet ./internal/hardware ./internal/httpapi` 退出 0。

## 不在本包范围

- M2-a 已在 `m2a-aee3f33-*` 独立投审，本包不改。r1/r3a/r4/r5/r6 的有限 APPROVE 不代表 P1a 或整体通过。
- P1c（扫描器漏注册、空证据/N/A 绕过 --complete、--update 继承失效状态）尚未修；工作区里 `docs/pet-mcp/coverage.tsv` 的未提交改动属于 P1c/P2 账本，不在本提交内。
- 固定 9bca378 的 app/httpapi 全包两次都在 40m 超时退出 1（0 个单用例失败，但没跑完），不算通过；本包只有上列具名/全包定向结果。
- 凭据轮换后，用旧凭据写下的历史不会被新凭据识别；没有真实设备实测，Windows 只做代码层面检查。
