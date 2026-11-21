# P1c v2：覆盖门禁改为 Go 语法树与可核对证据（固定 6d785b3）

源码 `6d785b35e5455d06ad2e12ac6fd5d2827a9f724a`，tree `8b6209025d6dd874a931facd69fe18d8f9c23c7f`；增量 `322c154..6d785b3` 共 12 个文件（`322c154` 是 M5 材料提交，本包不改 M2-a/M5 及已投审包）。diff 与函数全文都从固定提交取得，diff 保留 3 行上下文。本轮按明确边界分 5 个包：A 扫描器与单元测试，B 账本规则/门禁/文档，L1、L2 新账本全文（按行号切两段），L3 逐行变更对照。合计覆盖全部增量，不截断。

旧扫描器的三类问题（宠物转述的 723 对 P1c 的 REQUEST_CHANGES 三类结论；报告原文不在本轮上下文中，若原文还有其他条目需另行补）：只用正则看 `internal/httpapi` 里的 `mux.Handle` 行，别处的注册会漏；`--complete` 只看状态字段，"existing HTTP server tests"、通配路径、不存在的测试名、任意 N/A 都能关闭一行；`--update` 只按路由键继承旧结论，handler 改了也不作废。

固定副本（git archive，blob 逐个核对一致）验证：

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.1s，具名用例 7 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData)$' -count=1` → 退出 0，6.8s，具名用例 3 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.9s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA)$' -count=1 -timeout=30m` → 退出 0，70.4s，具名用例 15 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，2.2s；输出：
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.8s；输出：HTTP operations: 433 (closed 21, open 412); Web literal call sites: 284; ordinary-Task MCP tools: 61 (closed 0)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.3s；输出：coverage is incomplete: 412 open operations, 61 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage` → 退出 0，5.2s；输出：

已关闭行引用的每个测试都在固定副本里实际跑过（上表中按包列出），不只检查存在。

## L2. 新账本全文（第 218 行至第 434 行，末行）

新账本 `docs/pet-mcp/coverage.tsv` 在固定提交里共 433 行数据（81841 字节，sha256 `da7c9951193d74156b0b60281fab6d3c95c0ebb2640d14fb7f3f0f49a15512c3`）。因为加了 server、fingerprint 列且 source 改为仓库相对路径，git diff 几乎是整表删除加整表新增（139483 字节），超过单包上限；这里改为给出新文件全文（L1、L2）和逐行语义对照（L3）。旧文件全文在 `322c154` 及已归档的 p1b 包中。该文件与源码一致由 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 在固定副本里验证（退出 0）。

```tsv
POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate	httpapi	internal/httpapi/server.go	agentBrowserActivate	agent_turn	4a150454db54	aha2_browser.browser_tab_activate	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	pending	stale
POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/files	httpapi	internal/httpapi/server.go	agentBrowserUpload	agent_turn	b41029b80721	aha2_browser.browser_upload; existing Agent byte transport for large files	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate	httpapi	internal/httpapi/server.go	agentBrowserNavigate	agent_turn	1d051e7a57a1	aha2_browser.browser_navigate	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
POST /api/v1/agent/browser/mcp	httpapi	internal/httpapi/server.go	agentBrowserMCP	agent_turn	1814239c8f90	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_browser_mcp_test.go:TestAgentBrowserMCPRequiresCapabilityAndDomainGate	transport_na
POST /api/v1/agent/channel/actions/preview	httpapi	internal/httpapi/server.go	previewAgentChannelAction	agent_turn	3dcfcf023ec8	pending	pending	pending	pending	pending
POST /api/v1/agent/channel/handoffs	httpapi	internal/httpapi/server.go	createAgentChannelHandoff	agent_turn	116bb3f5d0a4	pending	pending	pending	pending	pending
POST /api/v1/agent/channel/mcp	httpapi	internal/httpapi/server.go	agentChannelMCP	agent_turn	f32ac6643d9a	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/channel_mcp_test.go:TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools	transport_na
POST /api/v1/agent/channel/messages	httpapi	internal/httpapi/server.go	sendAgentTaskChannelMessage	agent_turn	0965550fa660	pending	pending	pending	pending	pending
POST /api/v1/agent/channel/replies	httpapi	internal/httpapi/server.go	sendAgentChannelReply	agent_turn	e7ad2ecf0db8	pending	pending	pending	pending	pending
POST /api/v1/agent/channel/reply-decision	httpapi	internal/httpapi/server.go	setAgentChannelReplyDecision	agent_turn	b35b9a8f6851	pending	pending	pending	pending	pending
POST /api/v1/agent/collab/mcp	httpapi	internal/httpapi/server.go	agentCollabMCP	agent_turn	17f60bcdd138	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_collab_mcp_test.go:TestAgentCollabMCPRequiresCapabilityAndDomainGate	transport_na
POST /api/v1/agent/collaboration/batches	httpapi	internal/httpapi/server.go	submitAgentCollaboration	agent_turn	9240d85d1049	pending	pending	pending	pending	pending
POST /api/v1/agent/collaboration/batches/{batch}/wait	httpapi	internal/httpapi/server.go	waitAgentCollaboration	agent_turn	e5f04eb20e24	pending	pending	pending	pending	pending
POST /api/v1/agent/core/mcp	httpapi	internal/httpapi/server.go	agentCoreMCP	agent_turn	dc162322101a	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_core_mcp_test.go:TestAgentCoreMCPRequiresCapabilityAndRunningTurn	transport_na
POST /api/v1/agent/desktop/actions	httpapi	internal/httpapi/desktop_handlers.go	desktopAct	agent_turn	38bd57ff92cf	aha2_desktop.desktop_action	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_desktop_mcp_test.go:TestDesktopMCPUsesExistingShareAndSingleUseObservation	stale
POST /api/v1/agent/desktop/control	httpapi	internal/httpapi/desktop_handlers.go	desktopControl	agent_turn	f8339577bb60	aha2_desktop.desktop_control	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_desktop_mcp_test.go:TestDesktopMCPUsesExistingShareAndSingleUseObservation	stale
POST /api/v1/agent/desktop/mcp	httpapi	internal/httpapi/server.go	agentDesktopMCP	agent_turn	a4f4faf31814	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_desktop_mcp_test.go:TestDesktopMCPUsesExistingShareAndSingleUseObservation	transport_na
POST /api/v1/agent/desktop/stop	httpapi	internal/httpapi/server.go	agentDesktopStop	agent_turn	4ffd4613931d	aha2_desktop.desktop_stop	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_desktop_mcp_test.go:TestDesktopMCPUsesExistingShareAndSingleUseObservation	stale
POST /api/v1/agent/hardware/mcp	httpapi	internal/httpapi/server.go	agentHardwareMCP	agent_turn	838c600634cd	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_hardware_mcp_test.go:TestAgentHardwareMCPRequiresCapabilityAndDomainGate	transport_na
POST /api/v1/agent/hardware/{hardware}/adb/exec	httpapi	internal/httpapi/server.go	agentADBExec	agent_turn	21fe24c5da74	aha2_hardware.hardware_adb_exec	Main+writable snapshot; bound writable resource; pet live create_tasks permission	no host ADB commands; owner pairing remains protected	internal/httpapi/agent_pet_domains_test.go:TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions	stale
POST /api/v1/agent/hardware/{hardware}/connect	httpapi	internal/httpapi/server.go	agentConnectHardware	agent_turn	816e180e45e6	aha2_hardware.hardware_connect	Main+writable snapshot; bound resource; pet live create_tasks permission	stored credentials only; unknown host key requires owner flow	internal/httpapi/agent_pet_domains_test.go:TestAgentMCPOperationRejectsInvalidBeforeHandlerAndSanitizesResults	stale
POST /api/v1/agent/hardware/{hardware}/disconnect	httpapi	internal/httpapi/server.go	agentDisconnectHardware	agent_turn	a5ce4adfadfa	aha2_hardware.hardware_disconnect	Main+writable snapshot; bound resource; pet live create_tasks permission	none	pending	stale
POST /api/v1/agent/hardware/{hardware}/login	httpapi	internal/httpapi/server.go	agentLoginHardware	agent_turn	67479df095a6	aha2_hardware.hardware_login	Main+writable snapshot; bound writable resource; pet live create_tasks permission	stored credentials only; password not accepted or returned	pending	stale
POST /api/v1/agent/hardware/{hardware}/send	httpapi	internal/httpapi/server.go	agentSendHardware	agent_turn	c1d6cf8ec92c	aha2_hardware.hardware_send	Main+writable snapshot; bound writable resource; pet live create_tasks permission	no credentials schema; terminal output separate	pending	stale
POST /api/v1/agent/knowledge/candidates	httpapi	internal/httpapi/server.go	submitAgentKnowledge	agent_turn	a84be4fd9657	pending	pending	pending	pending	pending
POST /api/v1/agent/knowledge/entries	httpapi	internal/httpapi/server.go	submitAgentKnowledgeEntry	agent_turn	c22adf5e62f4	pending	pending	pending	pending	pending
POST /api/v1/agent/knowledge/feedback	httpapi	internal/httpapi/server.go	submitAgentKnowledgeFeedbackEntry	agent_turn	506f41ec88ff	pending	pending	pending	pending	pending
POST /api/v1/agent/knowledge/mcp	httpapi	internal/httpapi/server.go	agentKnowledgeMCP	agent_turn	f01fad870adc	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_knowledge_mcp_test.go:TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate	transport_na
POST /api/v1/agent/knowledge/{knowledge}/feedback	httpapi	internal/httpapi/server.go	submitAgentKnowledgeFeedback	agent_turn	d80170bc7eb6	pending	pending	pending	pending	pending
POST /api/v1/agent/mail/mcp	httpapi	internal/httpapi/server.go	agentMailMCP	agent_turn	7160868f3790	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/mail_agent_handlers_test.go:TestAgentMailMCPTools	transport_na
POST /api/v1/agent/mail/messages	httpapi	internal/httpapi/server.go	agentMailSend	agent_turn	b2c57a516062	pending	pending	pending	pending	pending
POST /api/v1/agent/mail/preview	httpapi	internal/httpapi/server.go	agentMailPreview	agent_turn	d97d9831fd97	pending	pending	pending	pending	pending
POST /api/v1/agent/processes	httpapi	internal/httpapi/server.go	startAgentProcess	agent_turn	8b1518be0ef7	aha2_workspace.workspace_process_start	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestWorkspaceMCPStartsAndStopsOnlyCurrentTaskProcess	stale
POST /api/v1/agent/processes/{name}/stop	httpapi	internal/httpapi/server.go	stopAgentProcess	agent_turn	378cc595b920	aha2_workspace.workspace_process_stop	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestWorkspaceMCPStartsAndStopsOnlyCurrentTaskProcess	stale
POST /api/v1/agent/skills	httpapi	internal/httpapi/server.go	createAgentSkill	agent_turn	96c03107f208	pending	pending	pending	pending	pending
POST /api/v1/agent/tasks	httpapi	internal/httpapi/server.go	createAgentTask	agent_turn	85e402adcb14	pending	pending	pending	pending	pending
POST /api/v1/agent/turn/attachments	httpapi	internal/httpapi/server.go	uploadAgentAttachment	agent_turn	525ff84121fa	pending	pending	pending	pending	pending
POST /api/v1/agent/turn/inbox/ack	httpapi	internal/httpapi/server.go	ackAgentInbox	agent_turn	4b9c601ff774	pending	pending	pending	pending	pending
POST /api/v1/agent/turn/inbox/claim	httpapi	internal/httpapi/server.go	claimAgentInbox	agent_turn	a125d11ddf8e	pending	pending	pending	pending	pending
POST /api/v1/agent/turn/inbox/wait	httpapi	internal/httpapi/server.go	waitAgentInbox	agent_turn	99728c71f02d	aha2_core.inbox_wait	Turn capability; current Task/Agent; live Turn	none	internal/httpapi/live_inbox_test.go:TestAgentLiveInboxClaimAckHTTP; internal/httpapi/agent_core_pet_scope_test.go:TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks	stale
POST /api/v1/agent/turn/messages	httpapi	internal/httpapi/server.go	addAgentProgressMessage	agent_turn	56c815ea76c0	aha2_core.progress; aha2_core.attachment_bind	MCP Main+writable; HTTP existing Agent scope	attachment bytes use existing upload API; IDs remain Task scoped	internal/httpapi/agent_pet_domains_test.go:TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary	stale
POST /api/v1/agent/workspace/mcp	httpapi	internal/httpapi/server.go	agentWorkspaceMCP	agent_turn	63345b5ddb67	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/agent_workspace_mcp_test.go:TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate	transport_na
POST /api/v1/assistant/hints/dismiss	httpapi	internal/httpapi/server.go	dismissOwnerAssistantHint	owner_session	839f72196e0d	pending	pending	pending	pending	pending
POST /api/v1/assistant/messages	httpapi	internal/httpapi/server.go	askOwnerAssistant	owner_session	ac79df8165f8	pending	pending	pending	pending	pending
POST /api/v1/auth/login	httpapi	internal/httpapi/server.go	authLogin	transport_auth	3e087d36b5d9	pending	pending	pending	pending	pending
POST /api/v1/auth/logout	httpapi	internal/httpapi/server.go	authLogout	owner_session	088258af9855	pending	pending	pending	pending	pending
POST /api/v1/auth/password	httpapi	internal/httpapi/server.go	authChangePassword	owner_session	3b7e5ecc798a	pending	pending	pending	pending	pending
POST /api/v1/auth/recover	httpapi	internal/httpapi/server.go	authRecover	transport_auth	f732eab3881c	pending	pending	pending	pending	pending
POST /api/v1/auth/register	httpapi	internal/httpapi/server.go	authRegister	transport_auth	fa9bbba74fcd	pending	pending	pending	pending	pending
POST /api/v1/browser-grants/{id}/tabs	httpapi	internal/httpapi/browser_handlers.go	openBrowserTab	owner_session	0539288f2859	pending	pending	pending	pending	pending
POST /api/v1/browser-grants/{id}/tabs/current	httpapi	internal/httpapi/browser_handlers.go	adoptBrowserCurrentTab	owner_session	6e0dfa3db343	pending	pending	pending	pending	pending
POST /api/v1/browser-grants/{id}/tabs/{tab}/activate	httpapi	internal/httpapi/browser_handlers.go	activateBrowserTab	owner_session	bc3f3958ba5b	pending	pending	pending	pending	pending
POST /api/v1/browser-grants/{id}/tabs/{tab}/navigate	httpapi	internal/httpapi/browser_handlers.go	navigateBrowserTab	owner_session	61cc15bf36a5	pending	pending	pending	pending	pending
POST /api/v1/browser-sessions/{id}/close	httpapi	internal/httpapi/browser_handlers.go	closeBrowserSession	owner_session	3fd72afb2af6	pending	pending	pending	pending	pending
POST /api/v1/browser/profiles	httpapi	internal/httpapi/browser_handlers.go	createBrowserProfile	owner_session	4381003a6d44	pending	pending	pending	pending	pending
POST /api/v1/browser/profiles/{id}/login-window	httpapi	internal/httpapi/browser_handlers.go	openBrowserLoginWindow	owner_session	da770b23fe25	pending	pending	pending	pending	pending
POST /api/v1/browser/profiles/{id}/transfer	httpapi	internal/httpapi/browser_handlers.go	transferBrowserProfile	owner_session	a1316b606fc8	pending	pending	pending	pending	pending
POST /api/v1/channel-deliveries/{id}/replay	httpapi	internal/httpapi/server.go	replayChannelDelivery	owner_session	69e26e3ab8db	pending	pending	pending	pending	pending
POST /api/v1/channel-deliveries/{id}/skip	httpapi	internal/httpapi/server.go	skipChannelDelivery	owner_session	f9b5eaa4f339	pending	pending	pending	pending	pending
POST /api/v1/channel-instances	httpapi	internal/httpapi/server.go	createChannelInstance	owner_session	8b8cc544d838	pending	pending	pending	pending	pending
POST /api/v1/channel-instances/{id}/archive	httpapi	internal/httpapi/server.go	archiveChannelInstance	owner_session	7a8f9944468a	pending	pending	pending	pending	pending
POST /api/v1/channel-instances/{id}/onboarding-sessions	httpapi	internal/httpapi/server.go	startChannelOnboarding	owner_session	dff2d9c14fef	pending	pending	pending	pending	pending
POST /api/v1/channel-instances/{id}/purge	httpapi	internal/httpapi/server.go	purgeChannelInstance	owner_session	70269baee975	pending	pending	pending	pending	pending
POST /api/v1/channel-instances/{id}/reset-binding	httpapi	internal/httpapi/server.go	resetChannelBinding	owner_session	bf4dd8702cb7	pending	pending	pending	pending	pending
POST /api/v1/channel-instances/{id}/runtime-menus/reset	httpapi	internal/httpapi/server.go	resetChannelRuntimeMenus	owner_session	95dc5b5d9574	pending	pending	pending	pending	pending
POST /api/v1/channel-onboarding-sessions/{id}/cancel	httpapi	internal/httpapi/server.go	cancelChannelOnboarding	owner_session	199b2245813e	pending	pending	pending	pending	pending
POST /api/v1/codex-accounts/import	httpapi	internal/httpapi/server.go	importCodexAccount	owner_session	9881a819c50e	pending	pending	pending	pending	pending
POST /api/v1/codex-accounts/import-local	httpapi	internal/httpapi/server.go	importLocalCodexAccount	owner_session	3d16478adccb	pending	pending	pending	pending	pending
POST /api/v1/codex-accounts/login	httpapi	internal/httpapi/server.go	startCodexAccountLogin	owner_session	599554815827	pending	pending	pending	pending	pending
POST /api/v1/codex-accounts/login/{id}/callback	httpapi	internal/httpapi/server.go	submitCodexAccountCallback	owner_session	d2f455f9feed	pending	pending	pending	pending	pending
POST /api/v1/codex-accounts/{id}/refresh	httpapi	internal/httpapi/server.go	refreshCodexAccount	owner_session	a8ad26d92e26	pending	pending	pending	pending	pending
POST /api/v1/env-groups	httpapi	internal/httpapi/server.go	createEnvGroup	owner_session	92b346e7ddc3	pending	pending	pending	pending	pending
POST /api/v1/hardware-resources	httpapi	internal/httpapi/server.go	createHardwareResource	owner_session	d94240d46486	pending	pending	pending	pending	pending
POST /api/v1/hardware-resources/{id}/adb/pair	httpapi	internal/httpapi/server.go	pairHardwareADB	owner_session	f95f0884da49	pending	pending	pending	pending	pending
POST /api/v1/knowledge	httpapi	internal/httpapi/server.go	createKnowledge	owner_session	9d6216c89d27	pending	pending	pending	pending	pending
POST /api/v1/knowledge-vault/git/commit	httpapi	internal/httpapi/server.go	commitKnowledgeVaultGit	owner_session	112b72762265	pending	pending	pending	pending	pending
POST /api/v1/knowledge-vault/git/init	httpapi	internal/httpapi/server.go	initializeKnowledgeVaultGit	owner_session	b1162f2a700c	pending	pending	pending	pending	pending
POST /api/v1/knowledge-vault/git/pull	httpapi	internal/httpapi/server.go	pullKnowledgeVaultGit	owner_session	9ea2ad4b2523	pending	pending	pending	pending	pending
POST /api/v1/knowledge-vault/git/push	httpapi	internal/httpapi/server.go	pushKnowledgeVaultGit	owner_session	9cd8083c2548	pending	pending	pending	pending	pending
POST /api/v1/knowledge-vault/git/sync	httpapi	internal/httpapi/server.go	syncKnowledgeVaultGit	owner_session	525d406c31b1	pending	pending	pending	pending	pending
POST /api/v1/knowledge-vault/reveal	httpapi	internal/httpapi/server.go	revealKnowledgeVaultPath	owner_session	09e6bddfb70d	pending	pending	pending	pending	pending
POST /api/v1/knowledge/libraries/{id}/bind	httpapi	internal/httpapi/server.go	bindKnowledgeLibrary	owner_session	bb5067fa481a	pending	pending	pending	pending	pending
POST /api/v1/knowledge/libraries/{id}/unbind	httpapi	internal/httpapi/server.go	unbindKnowledgeLibrary	owner_session	5b002ba74e05	pending	pending	pending	pending	pending
POST /api/v1/knowledge/proposals/batch	httpapi	internal/httpapi/server.go	batchKnowledgeProposalReviews	owner_session	9481b251279f	pending	pending	pending	pending	pending
POST /api/v1/knowledge/proposals/{id}/approve	httpapi	internal/httpapi/server.go	approveKnowledgeProposal	owner_session	7cffd3853e5b	pending	pending	pending	pending	pending
POST /api/v1/knowledge/proposals/{id}/reject	httpapi	internal/httpapi/server.go	rejectKnowledgeProposal	owner_session	efc610257798	pending	pending	pending	pending	pending
POST /api/v1/knowledge/{id}/feedback	httpapi	internal/httpapi/server.go	feedbackKnowledge	owner_session	2d8e902aafb4	pending	pending	pending	pending	pending
POST /api/v1/knowledge/{id}/verify	httpapi	internal/httpapi/server.go	verifyKnowledge	owner_session	94a982d46104	pending	pending	pending	pending	pending
POST /api/v1/local/owner-messages	httpapi	internal/httpapi/server.go	localOwnerMessageIngest	transport_auth	4bfae1d314d3	pending	pending	pending	pending	pending
POST /api/v1/mail-accounts	httpapi	internal/httpapi/server.go	createMailAccount	owner_session	344e85848d64	pending	pending	pending	pending	pending
POST /api/v1/mail-accounts/{id}/test	httpapi	internal/httpapi/server.go	testMailAccount	owner_session	031b05ce10dc	pending	pending	pending	pending	pending
POST /api/v1/mail-bindings	httpapi	internal/httpapi/server.go	createMailBinding	owner_session	735ba6fd920b	pending	pending	pending	pending	pending
POST /api/v1/mail-messages	httpapi	internal/httpapi/server.go	createMailMessage	owner_session	cb89af741a19	pending	pending	pending	pending	pending
POST /api/v1/mail-messages/{id}/approve	httpapi	internal/httpapi/server.go	approveMailMessage	owner_session	99962623cca9	pending	pending	pending	pending	pending
POST /api/v1/mail-messages/{id}/cancel	httpapi	internal/httpapi/server.go	cancelMailMessage	owner_session	3d2afc8c7356	pending	pending	pending	pending	pending
POST /api/v1/mail-messages/{id}/download-links/{attachment_id}/revoke	httpapi	internal/httpapi/server.go	revokeMailMessageDownloadLink	owner_session	7b1a4dcf7ba6	pending	pending	pending	pending	pending
POST /api/v1/mail-messages/{id}/reject	httpapi	internal/httpapi/server.go	rejectMailMessage	owner_session	ba892be66520	pending	pending	pending	pending	pending
POST /api/v1/mail-messages/{id}/retry	httpapi	internal/httpapi/server.go	retryMailMessage	owner_session	dc4471937b6d	pending	pending	pending	pending	pending
POST /api/v1/mail-messages/{id}/submit	httpapi	internal/httpapi/server.go	submitMailMessage	owner_session	00c80ad86e52	pending	pending	pending	pending	pending
POST /api/v1/mail-templates	httpapi	internal/httpapi/server.go	createMailTemplate	owner_session	9a2a1a1e6ab9	pending	pending	pending	pending	pending
POST /api/v1/mail-templates/preview	httpapi	internal/httpapi/server.go	previewMailTemplateContent	owner_session	21fc01742467	pending	pending	pending	pending	pending
POST /api/v1/mail-templates/{id}/preview	httpapi	internal/httpapi/server.go	previewMailTemplate	owner_session	6b4822b4d9a1	pending	pending	pending	pending	pending
POST /api/v1/mcp-servers	httpapi	internal/httpapi/server.go	saveMCPServer	owner_session	79bfd40cc0c7	pending	pending	pending	pending	pending
POST /api/v1/mcp-servers/test	httpapi	internal/httpapi/server.go	testMCPServer	owner_session	7ca22ec37ca1	pending	pending	pending	pending	pending
POST /api/v1/models	httpapi	internal/httpapi/server.go	createModel	owner_session	947996a50178	pending	pending	pending	pending	pending
POST /api/v1/models/batch-delete	httpapi	internal/httpapi/server.go	requestBatchDeleteModels	owner_session	a091796667dd	pending	pending	pending	pending	pending
POST /api/v1/models/{id}/health-check	httpapi	internal/httpapi/server.go	checkModelHealth	owner_session	ae5d3d683c41	pending	pending	pending	pending	pending
POST /api/v1/pet/approvals/{id}/resolve	httpapi	internal/httpapi/server.go	resolvePetApproval	owner_session	1e4ac700fbd0	pending	pending	pending	pending	pending
POST /api/v1/pet/brain	httpapi	internal/httpapi/server.go	ownerPetBrain	owner_session	b0d51ee4b382	pending	pending	pending	pending	pending
POST /api/v1/pet/control	httpapi	internal/httpapi/server.go	controlOwnerPet	owner_session	32081fbb0f6c	pending	pending	pending	pending	pending
POST /api/v1/pet/mcp	httpapi	internal/httpapi/server.go	petMCP	agent_turn	d09a354d80ef	none	Agent token; Turn capability and per-domain gate in the handler	MCP JSON-RPC transport only; every tool it serves is a row of task-mcp-tools.tsv or the pet tool catalog	internal/httpapi/pet_mcp_test.go:TestPetMCPPingReturnsResult; internal/httpapi/pet_mcp_test.go:TestPetMCPNotificationsAreAcceptedWithoutJSON	transport_na
POST /api/v1/pet/navigation/claim	httpapi	internal/httpapi/server.go	claimOwnerPetNavigation	owner_session	2f50714bcd1d	pending	pending	pending	pending	pending
POST /api/v1/pet/reset	httpapi	internal/httpapi/server.go	resetOwnerPet	owner_session	bfe15426814e	pending	pending	pending	pending	pending
POST /api/v1/pet/watches/{task}	httpapi	internal/httpapi/server.go	followPetWatch	owner_session	b2cb41e429c9	pending	pending	pending	pending	pending
POST /api/v1/projects	httpapi	internal/httpapi/server.go	createProject	owner_session	1360d2bda019	pending	pending	pending	pending	pending
POST /api/v1/projects/{id}/knowledge-space	httpapi	internal/httpapi/server.go	retryProjectKnowledgeSpace	owner_session	2a302c4d632e	pending	pending	pending	pending	pending
POST /api/v1/projects/{id}/knowledge/detach	httpapi	internal/httpapi/server.go	detachProjectKnowledge	owner_session	ff15d686ebc7	pending	pending	pending	pending	pending
POST /api/v1/projects/{id}/product-lines	httpapi	internal/httpapi/server.go	createProductLine	owner_session	80b65f816214	pending	pending	pending	pending	pending
POST /api/v1/prompts/templates/{id}/reset	httpapi	internal/httpapi/server.go	resetPromptTemplate	owner_session	a31c867eac98	pending	pending	pending	pending	pending
POST /api/v1/providers	httpapi	internal/httpapi/server.go	createProvider	owner_session	e98041244423	pending	pending	pending	pending	pending
POST /api/v1/providers/add-models	httpapi	internal/httpapi/server.go	addModelsHandler	owner_session	0700d2d6c4ca	pending	pending	pending	pending	pending
POST /api/v1/providers/detect-models	httpapi	internal/httpapi/server.go	detectModelsHandler	owner_session	8e1851792ea6	pending	pending	pending	pending	pending
POST /api/v1/providers/{id}/model-detection-jobs	httpapi	internal/httpapi/server.go	createModelDetectionJob	owner_session	290f8e118aa2	pending	pending	pending	pending	pending
POST /api/v1/providers/{id}/model-detection-jobs/{job}/cancel	httpapi	internal/httpapi/server.go	cancelModelDetectionJob	owner_session	8f5ed0647a6a	pending	pending	pending	pending	pending
POST /api/v1/providers/{id}/model-probes	httpapi	internal/httpapi/server.go	probeProviderModel	owner_session	0e399db99d3a	pending	pending	pending	pending	pending
POST /api/v1/rounds/{id}/interrupt	httpapi	internal/httpapi/server.go	interruptRound	owner_session	87c9e4bb66a2	pending	pending	pending	pending	pending
POST /api/v1/settings/knowledge-vault/cutover-legacy	httpapi	internal/httpapi/server.go	cutoverLegacyKnowledge	owner_session	55a66b7fa895	pending	pending	pending	pending	pending
POST /api/v1/settings/knowledge-vault/import	httpapi	internal/httpapi/server.go	importKnowledgeVault	owner_session	8776e4de798c	pending	pending	pending	pending	pending
POST /api/v1/settings/knowledge-vault/init	httpapi	internal/httpapi/server.go	initializeKnowledgeVault	owner_session	0adbb7c20acf	pending	pending	pending	pending	pending
POST /api/v1/settings/knowledge-vault/migrate	httpapi	internal/httpapi/server.go	migrateKnowledgeVault	owner_session	3bb53826575b	pending	pending	pending	pending	pending
POST /api/v1/settings/knowledge-vault/rebuild	httpapi	internal/httpapi/server.go	rebuildKnowledgeVaultIndex	owner_session	fcbd7e72940a	pending	pending	pending	pending	pending
POST /api/v1/settings/proxy/profiles/{id}/activate	httpapi	internal/httpapi/server.go	activateProxyProfile	owner_session	186cf158349f	pending	pending	pending	pending	pending
POST /api/v1/settings/proxy/profiles/{id}/nodes/{node_id}/test	httpapi	internal/httpapi/server.go	testProxyNode	owner_session	ed78ce040177	pending	pending	pending	pending	pending
POST /api/v1/settings/proxy/profiles/{id}/refresh	httpapi	internal/httpapi/server.go	refreshProxyProfile	owner_session	1714c86760fb	pending	pending	pending	pending	pending
POST /api/v1/settings/proxy/subscription	httpapi	internal/httpapi/server.go	importProxySubscription	owner_session	2ea510c924ba	pending	pending	pending	pending	pending
POST /api/v1/settings/proxy/subscription/refresh	httpapi	internal/httpapi/server.go	refreshProxySubscription	owner_session	49b94d727712	pending	pending	pending	pending	pending
POST /api/v1/settings/proxy/test	httpapi	internal/httpapi/server.go	testProxySettings	owner_session	d32d6aa55791	pending	pending	pending	pending	pending
POST /api/v1/settings/sync/conflicts/{id}/resolve	httpapi	internal/httpapi/server.go	resolveSyncConflict	owner_session	85c42e46b586	pending	pending	pending	pending	pending
POST /api/v1/settings/sync/run	httpapi	internal/httpapi/server.go	runSync	owner_session	dacf211e2dc2	pending	pending	pending	pending	pending
POST /api/v1/skills	httpapi	internal/httpapi/server.go	rejectRemovedSkillWrite	owner_session	dcac79ed64a4	none	owner session; still rejected	removed legacy Skill write; Web and API both answer 405, Skill changes use the vault Skill operations	internal/httpapi/skill_write_removed_test.go:TestRemovedSkillWriteRoutesReturnMethodNotAllowed	web_prohibited_na
POST /api/v1/system/data/cleanup	httpapi	internal/httpapi/server.go	cleanupData	owner_session	0d0ed9739223	pending	pending	pending	pending	pending
POST /api/v1/system/data/conversation/preview	httpapi	internal/httpapi/server.go	previewConversationPurge	owner_session	cfdbbf745fb4	pending	pending	pending	pending	pending
POST /api/v1/system/data/conversation/purge	httpapi	internal/httpapi/server.go	purgeConversation	owner_session	dbf3fe605db2	pending	pending	pending	pending	pending
POST /api/v1/system/data/hardware-io/preview	httpapi	internal/httpapi/server.go	previewHardwareIOPurge	owner_session	2a9d2ede7bb6	pending	pending	pending	pending	pending
POST /api/v1/system/data/hardware-io/purge	httpapi	internal/httpapi/server.go	purgeHardwareIO	owner_session	bc5734f4f33c	pending	pending	pending	pending	pending
POST /api/v1/tasks	httpapi	internal/httpapi/server.go	createTask	owner_session	c7220cdae4fb	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/agents/{agent}/messages	httpapi	internal/httpapi/server.go	submitAgentMessage	owner_session	cd6ba66b0450	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/agents/{agent}/session/compact	httpapi	internal/httpapi/server.go	compactAgentSession	owner_session	284b1696ed5f	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/agents/{agent}/session/reset	httpapi	internal/httpapi/server.go	resetAgentSession	owner_session	9840a848c4db	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/attachments	httpapi	internal/httpapi/server.go	uploadTaskAttachment	owner_session	7a29a33628dc	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/browser-grants	httpapi	internal/httpapi/browser_handlers.go	createTaskBrowserGrant	owner_session	5bdea7eecb23	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/browser-sessions	httpapi	internal/httpapi/browser_handlers.go	createTaskBrowserGrant	owner_session	5bdea7eecb23	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/channel-members:refresh	httpapi	internal/httpapi/server.go	refreshTaskChannelMembers	owner_session	b47888309ed7	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/channel-routes	httpapi	internal/httpapi/server.go	bindTaskChannelRoute	owner_session	aca5583b2ec9	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/complete	httpapi	internal/httpapi/server.go	completeTask	owner_session	22e9b85b3154	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/desktop/actions	httpapi	internal/httpapi/desktop_handlers.go	desktopAct	owner_session	bacd21106e56	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/desktop/control	httpapi	internal/httpapi/desktop_handlers.go	desktopControl	owner_session	16cf0a5688e2	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/desktop/session	httpapi	internal/httpapi/desktop_handlers.go	desktopOpen	owner_session	c04910849321	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/desktop/stop	httpapi	internal/httpapi/desktop_handlers.go	desktopStop	owner_session	644f9ddfbc86	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/desktop/switch	httpapi	internal/httpapi/desktop_handlers.go	desktopSwitch	owner_session	16af0832eade	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/hardware/{hardware}/connect	httpapi	internal/httpapi/server.go	connectHardware	owner_session	1937ef47db30	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/hardware/{hardware}/disconnect	httpapi	internal/httpapi/server.go	disconnectHardware	owner_session	5071dece6e69	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/hardware/{hardware}/host-key/trust	httpapi	internal/httpapi/server.go	trustHardwareSSHHostKey	owner_session	12150066124c	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/hardware/{hardware}/send	httpapi	internal/httpapi/server.go	sendHardware	owner_session	c5c9ad250267	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/messages	httpapi	internal/httpapi/server.go	submitMessage	owner_session	e791743bfe3d	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/permission-requests/{request}/resolve	httpapi	internal/httpapi/server.go	resolveTaskPermissionRequest	owner_session	a0dc72ae59fa	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/reopen	httpapi	internal/httpapi/server.go	reopenTask	owner_session	e050d883d2db	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/start	httpapi	internal/httpapi/server.go	startTask	owner_session	b2ed532bef1b	pending	pending	pending	pending	pending
POST /api/v1/tasks/{id}/takeover	httpapi	internal/httpapi/server.go	takeoverTask	owner_session	73b59e9dcf33	pending	pending	pending	pending	pending
POST /api/v1/turns/{id}/interrupt	httpapi	internal/httpapi/server.go	interruptTurn	owner_session	0656fd537f69	pending	pending	pending	pending	pending
POST /api/v1/workspaces	httpapi	internal/httpapi/server.go	createWorkspace	owner_session	a29fb1abf774	pending	pending	pending	pending	pending
POST /api/v1/workspaces/{id}/detect	httpapi	internal/httpapi/server.go	detectWorkspaceHandler	owner_session	21db547839a1	pending	pending	pending	pending	pending
POST /api/v1/workspaces/{id}/host-key/trust	httpapi	internal/httpapi/server.go	trustWorkspaceSSHHostKey	owner_session	1ee1c713751c	pending	pending	pending	pending	pending
POST /api/v1/workspaces/{id}/takeover	httpapi	internal/httpapi/server.go	takeoverWorkspace	owner_session	718e25866c31	pending	pending	pending	pending	pending
POST /d/{token}/confirm	mail-download	internal/httpapi/mail_download_handlers.go	mailDownloadConfirm	transport_auth	af12369097fc	pending	pending	pending	pending	pending
POST /v1/devices/bootstrap-code	centersync	internal/centersync/service.go	inline	transport_auth	a7acb0125559	none	device token (or one-time bootstrap code) of the separate aha-sync center service	device-to-center sync protocol served by cmd/aha-sync, not the owner Web listener; owner sync management is the httpapi settings/sync rows	internal/centersync/service_test.go:TestOneTimeDeviceRegistrationStoresOnlyHashes	transport_na
POST /v1/devices/register	centersync	internal/centersync/service.go	inline	transport_auth	aecd86e5f144	none	device token (or one-time bootstrap code) of the separate aha-sync center service	device-to-center sync protocol served by cmd/aha-sync, not the owner Web listener; owner sync management is the httpapi settings/sync rows	internal/centersync/service_test.go:TestOneTimeDeviceRegistrationStoresOnlyHashes	transport_na
POST /v1/sync/push	centersync	internal/centersync/service.go	inline	device_token	adedb59a7172	none	device token (or one-time bootstrap code) of the separate aha-sync center service	device-to-center sync protocol served by cmd/aha-sync, not the owner Web listener; owner sync management is the httpapi settings/sync rows	internal/centersync/service_test.go:TestPushIsIdempotentAndPullIsIncremental	transport_na
PUT /api/channel-runtime/v1/instances/{instance_id}/health	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
PUT /api/v1/agent/skills/{skill}	httpapi	internal/httpapi/server.go	updateAgentSkill	agent_turn	1f176536a696	pending	pending	pending	pending	pending
PUT /api/v1/assistant	httpapi	internal/httpapi/server.go	updateOwnerAssistant	owner_session	4beaf3de40e2	pending	pending	pending	pending	pending
PUT /api/v1/channel-instances/{id}/credentials	httpapi	internal/httpapi/server.go	updateChannelCredentials	owner_session	9b511787a6e4	pending	pending	pending	pending	pending
PUT /api/v1/channel-instances/{id}/knowledge-bindings	httpapi	internal/httpapi/server.go	updateChannelKnowledgeBindings	owner_session	52620610bbcb	pending	pending	pending	pending	pending
PUT /api/v1/channel-instances/{id}/knowledge-bindings/group	httpapi	internal/httpapi/server.go	updateChannelGroupKnowledgeBindings	owner_session	fe06d7c1e99a	pending	pending	pending	pending	pending
PUT /api/v1/channel-instances/{id}/knowledge-policy	httpapi	internal/httpapi/server.go	updateChannelKnowledgePolicy	owner_session	6045e67930b5	pending	pending	pending	pending	pending
PUT /api/v1/hardware-resources/{id}	httpapi	internal/httpapi/server.go	updateHardwareResource	owner_session	93c1c55602ac	pending	pending	pending	pending	pending
PUT /api/v1/knowledge-vault/git/remote	httpapi	internal/httpapi/server.go	updateKnowledgeVaultGitRemote	owner_session	473a078ede09	pending	pending	pending	pending	pending
PUT /api/v1/knowledge-vault/skills/auto-attach	httpapi	internal/httpapi/server.go	updateKnowledgeVaultSkillAutoAttach	owner_session	3b9ce812d2fd	pending	pending	pending	pending	pending
PUT /api/v1/knowledge/{id}	httpapi	internal/httpapi/server.go	updateKnowledge	owner_session	8865d3a87c55	pending	pending	pending	pending	pending
PUT /api/v1/mail-accounts/{id}	httpapi	internal/httpapi/server.go	updateMailAccount	owner_session	5c93471fb35d	pending	pending	pending	pending	pending
PUT /api/v1/mail-bindings/{id}	httpapi	internal/httpapi/server.go	updateMailBinding	owner_session	74e409c2c193	pending	pending	pending	pending	pending
PUT /api/v1/mail-templates/{id}	httpapi	internal/httpapi/server.go	updateMailTemplate	owner_session	5e85cf8bf373	pending	pending	pending	pending	pending
PUT /api/v1/models/{id}	httpapi	internal/httpapi/server.go	updateModel	owner_session	e4b7982d0104	pending	pending	pending	pending	pending
PUT /api/v1/pet	httpapi	internal/httpapi/server.go	updateOwnerPetState	owner_session	4b060b5c36a0	pending	pending	pending	pending	pending
PUT /api/v1/pet/settings	httpapi	internal/httpapi/server.go	updateOwnerPetSettings	owner_session	343bcdc6fd40	pending	pending	pending	pending	pending
PUT /api/v1/pet/workbench/core/{name}	httpapi	internal/httpapi/server.go	savePetWorkbenchDocument	owner_session	ed3521c76473	pending	pending	pending	pending	pending
PUT /api/v1/pet/workbench/worklog/{name}	httpapi	internal/httpapi/server.go	savePetWorkbenchDocument	owner_session	ed3521c76473	pending	pending	pending	pending	pending
PUT /api/v1/projects/{id}	httpapi	internal/httpapi/server.go	updateProject	owner_session	9aaf7df6a785	pending	pending	pending	pending	pending
PUT /api/v1/prompts/templates/{id}	httpapi	internal/httpapi/server.go	updatePromptTemplate	owner_session	a80176a397ab	pending	pending	pending	pending	pending
PUT /api/v1/providers/{id}	httpapi	internal/httpapi/server.go	updateProvider	owner_session	d7b3dfab1a30	pending	pending	pending	pending	pending
PUT /api/v1/settings/agent-api	httpapi	internal/httpapi/server.go	updateAgentAPISettings	owner_session	a6e8ee69ef81	pending	pending	pending	pending	pending
PUT /api/v1/settings/backend	httpapi	internal/httpapi/server.go	updateBackendSettings	owner_session	0d155c2ba596	pending	pending	pending	pending	pending
PUT /api/v1/settings/general	httpapi	internal/httpapi/server.go	updateGeneralSettings	owner_session	aed2dc888fb6	pending	pending	pending	pending	pending
PUT /api/v1/settings/knowledge-review	httpapi	internal/httpapi/server.go	updateKnowledgeReviewSettings	owner_session	c7d60f8703e8	pending	pending	pending	pending	pending
PUT /api/v1/settings/knowledge-vault	httpapi	internal/httpapi/server.go	updateKnowledgeVaultSettings	owner_session	8783b82db42f	pending	pending	pending	pending	pending
PUT /api/v1/settings/network	httpapi	internal/httpapi/server.go	updateNetworkSettings	owner_session	d575ef84d936	pending	pending	pending	pending	pending
PUT /api/v1/settings/proxy	httpapi	internal/httpapi/server.go	updateProxySettings	owner_session	2c97572ba154	pending	pending	pending	pending	pending
PUT /api/v1/settings/security	httpapi	internal/httpapi/server.go	updateSecuritySettings	owner_session	7bc27c922afc	pending	pending	pending	pending	pending
PUT /api/v1/settings/sync	httpapi	internal/httpapi/server.go	updateSyncSettings	owner_session	5704627732c4	pending	pending	pending	pending	pending
PUT /api/v1/skills/{id}	httpapi	internal/httpapi/server.go	rejectRemovedSkillWrite	owner_session	dcac79ed64a4	none	owner session; still rejected	removed legacy Skill write; Web and API both answer 405, Skill changes use the vault Skill operations	internal/httpapi/skill_write_removed_test.go:TestRemovedSkillWriteRoutesReturnMethodNotAllowed	web_prohibited_na
PUT /api/v1/system/data/conversation/retention	httpapi	internal/httpapi/server.go	updateConversationRetention	owner_session	231342e9b91b	pending	pending	pending	pending	pending
PUT /api/v1/system/data/hardware-io/retention	httpapi	internal/httpapi/server.go	updateHardwareIORetention	owner_session	ae094502b6fb	pending	pending	pending	pending	pending
PUT /api/v1/tasks/{id}/channel-contacts	httpapi	internal/httpapi/server.go	updateTaskChannelContacts	owner_session	df3fe83975eb	pending	pending	pending	pending	pending
PUT /api/v1/tasks/{id}/hardware	httpapi	internal/httpapi/server.go	updateTaskHardware	owner_session	74f0c63a207c	pending	pending	pending	pending	pending
PUT /api/v1/tasks/{id}/hardware-bindings	httpapi	internal/httpapi/server.go	updateTaskHardwareBindings	owner_session	fcfdd2f5f49a	aha2-pet.aha_update_task_hardware	owner session OR pet create_tasks; manageable Task	binding identities retained; max 16; validate before disconnect	internal/app/agent_pet_domains_test.go:TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation	stale
PUT /api/v1/tasks/{id}/mcp-servers	httpapi	internal/httpapi/server.go	setTaskMCPServers	owner_session	95075e92f87b	pending	pending	pending	pending	pending
PUT /api/v1/workspaces/{id}	httpapi	internal/httpapi/server.go	updateWorkspace	owner_session	26ec449c1dc1	pending	pending	pending	pending	pending
```
