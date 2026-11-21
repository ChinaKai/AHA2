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

## L1. 新账本全文（第 1 行表头至第 217 行）

新账本 `docs/pet-mcp/coverage.tsv` 在固定提交里共 433 行数据（81841 字节，sha256 `da7c9951193d74156b0b60281fab6d3c95c0ebb2640d14fb7f3f0f49a15512c3`）。因为加了 server、fingerprint 列且 source 改为仓库相对路径，git diff 几乎是整表删除加整表新增（139483 字节），超过单包上限；这里改为给出新文件全文（L1、L2）和逐行语义对照（L3）。旧文件全文在 `322c154` 及已归档的 p1b 包中。该文件与源码一致由 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 在固定副本里验证（退出 0）。

```tsv
operation	server	source	handler	boundary	fingerprint	mcp	permission	protected_flow	tests	status
ANY /	httpapi	internal/httpapi/server.go	serveWeb	transport_auth	7d932ea4626c	none	none (public static assets)	static Web assets and SPA fallback; no business action or data	internal/httpapi/web_test.go:TestServeWebUnknownRouteStillFallsBackToSPA; internal/httpapi/web_test.go:TestServeWebServesBrowserNewTabAsHTTPPage	transport_na
ANY /api/v1/prompts/entries	httpapi	internal/httpapi/server.go	rejectPromptReadOnly	owner_session	4f923e677b97	pending	pending	pending	pending	pending
ANY /api/v1/prompts/entries/{entry}/anchors/{anchor}	httpapi	internal/httpapi/server.go	rejectPromptReadOnly	owner_session	4f923e677b97	pending	pending	pending	pending	pending
ANY /api/v1/prompts/migration-preview	httpapi	internal/httpapi/server.go	rejectPromptReadOnly	owner_session	4f923e677b97	pending	pending	pending	pending	pending
ANY /api/v1/prompts/scenario-preview	httpapi	internal/httpapi/server.go	rejectPromptReadOnly	owner_session	4f923e677b97	pending	pending	pending	pending	pending
DELETE /api/v1/agent/browser/grants/{grant}	httpapi	internal/httpapi/server.go	agentBrowserRelease	agent_turn	af8026fc11d0	aha2_browser.browser_release	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}	httpapi	internal/httpapi/server.go	agentBrowserCloseTab	agent_turn	172f07a6d8f8	aha2_browser.browser_tab_close	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
DELETE /api/v1/browser-grants/{id}	httpapi	internal/httpapi/browser_handlers.go	revokeBrowserGrant	owner_session	0696dced67c4	pending	pending	pending	pending	pending
DELETE /api/v1/browser-grants/{id}/tabs/{tab}	httpapi	internal/httpapi/browser_handlers.go	closeBrowserTab	owner_session	09abb88456d8	pending	pending	pending	pending	pending
DELETE /api/v1/browser/profiles/{id}	httpapi	internal/httpapi/browser_handlers.go	deleteBrowserProfile	owner_session	09b33681a770	pending	pending	pending	pending	pending
DELETE /api/v1/browser/profiles/{id}/session	httpapi	internal/httpapi/browser_handlers.go	closeBrowserProfileSession	owner_session	02d5b6629071	pending	pending	pending	pending	pending
DELETE /api/v1/codex-accounts/login/{id}	httpapi	internal/httpapi/server.go	cancelCodexAccountLogin	owner_session	62dea9b1a3b0	pending	pending	pending	pending	pending
DELETE /api/v1/codex-accounts/{id}	httpapi	internal/httpapi/server.go	deleteCodexAccount	owner_session	cc2dc032b51a	pending	pending	pending	pending	pending
DELETE /api/v1/hardware-resources/{id}	httpapi	internal/httpapi/server.go	deleteHardwareResource	owner_session	59194ee3baf1	pending	pending	pending	pending	pending
DELETE /api/v1/knowledge/libraries/{id}	httpapi	internal/httpapi/server.go	deleteKnowledgeLibrary	owner_session	8912939bd196	pending	pending	pending	pending	pending
DELETE /api/v1/knowledge/{id}	httpapi	internal/httpapi/server.go	deleteKnowledge	owner_session	35973089d80c	pending	pending	pending	pending	pending
DELETE /api/v1/mail-accounts/{id}	httpapi	internal/httpapi/server.go	deleteMailAccount	owner_session	163331da39d5	pending	pending	pending	pending	pending
DELETE /api/v1/mail-bindings/{id}	httpapi	internal/httpapi/server.go	deleteMailBinding	owner_session	1bf15057ce89	pending	pending	pending	pending	pending
DELETE /api/v1/mail-templates/{id}	httpapi	internal/httpapi/server.go	deleteMailTemplate	owner_session	66fd3effb470	pending	pending	pending	pending	pending
DELETE /api/v1/mcp-servers/{id}	httpapi	internal/httpapi/server.go	deleteMCPServer	owner_session	b07e16c3bf5a	pending	pending	pending	pending	pending
DELETE /api/v1/models/{id}	httpapi	internal/httpapi/server.go	deleteModel	owner_session	b81c98694980	pending	pending	pending	pending	pending
DELETE /api/v1/pet/watches/{task}	httpapi	internal/httpapi/server.go	deletePetWatch	owner_session	554550ebf732	pending	pending	pending	pending	pending
DELETE /api/v1/projects/{id}	httpapi	internal/httpapi/server.go	deleteProject	owner_session	0304b04b9ecd	pending	pending	pending	pending	pending
DELETE /api/v1/projects/{id}/knowledge	httpapi	internal/httpapi/server.go	deleteProjectKnowledge	owner_session	364ed2fdad08	pending	pending	pending	pending	pending
DELETE /api/v1/projects/{id}/product-lines/{line}	httpapi	internal/httpapi/server.go	deleteProductLine	owner_session	d061eccab702	pending	pending	pending	pending	pending
DELETE /api/v1/providers/{id}	httpapi	internal/httpapi/server.go	deleteProvider	owner_session	d68aca6a2ee9	pending	pending	pending	pending	pending
DELETE /api/v1/settings/proxy/profiles/{id}	httpapi	internal/httpapi/server.go	deleteProxyProfile	owner_session	fca09fb08ebc	pending	pending	pending	pending	pending
DELETE /api/v1/skills/{id}	httpapi	internal/httpapi/server.go	rejectRemovedSkillWrite	owner_session	dcac79ed64a4	none	owner session; still rejected	removed legacy Skill write; Web and API both answer 405, Skill changes use the vault Skill operations	internal/httpapi/skill_write_removed_test.go:TestRemovedSkillWriteRoutesReturnMethodNotAllowed	web_prohibited_na
DELETE /api/v1/tasks/{id}	httpapi	internal/httpapi/server.go	deleteTask	owner_session	456f6d5732b0	pending	pending	pending	pending	pending
DELETE /api/v1/tasks/{id}/attachments/{attachment}	httpapi	internal/httpapi/server.go	deleteTaskAttachment	owner_session	a6d58d42bb5e	pending	pending	pending	pending	pending
DELETE /api/v1/tasks/{id}/channel-routes/{route}	httpapi	internal/httpapi/server.go	unbindTaskChannelRoute	owner_session	20a8c89ccc81	pending	pending	pending	pending	pending
DELETE /api/v1/tasks/{id}/remote-mirror	httpapi	internal/httpapi/server.go	retireRemoteTaskMirror	owner_session	c0b1e2e3260c	pending	pending	pending	pending	pending
DELETE /api/v1/workspaces/{id}	httpapi	internal/httpapi/server.go	deleteWorkspace	owner_session	8d6ed13ab007	pending	pending	pending	pending	pending
DELETE /api/v1/workspaces/{id}/remote-mirror	httpapi	internal/httpapi/server.go	retireRemoteWorkspaceMirror	owner_session	e5020201a205	pending	pending	pending	pending	pending
GET /api/channel-runtime/v1/deliveries/{id}/attachment	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
GET /api/v1/agent/browser	httpapi	internal/httpapi/server.go	agentBrowserState	agent_turn	fa5fbb802383	pending	pending	pending	pending	pending
GET /api/v1/agent/browser/grants/{grant}/bookmarks	httpapi	internal/httpapi/server.go	agentBrowserBookmarks	agent_turn	ecfef9413442	aha2_browser.browser_bookmarks_list	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	pending	stale
GET /api/v1/agent/browser/grants/{grant}/cookies	httpapi	internal/httpapi/server.go	agentBrowserCookies	agent_turn	bf221ea39bfd	pending	pending	pending	pending	pending
GET /api/v1/agent/browser/grants/{grant}/downloads	httpapi	internal/httpapi/server.go	agentBrowserDownloads	agent_turn	8e89d68acbfe	aha2_browser.browser_downloads_list	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	pending	stale
GET /api/v1/agent/browser/grants/{grant}/downloads/{download}	httpapi	internal/httpapi/server.go	agentBrowserDownload	agent_turn	fa265103af3a	aha2_browser.browser_download_get; existing authenticated byte transport for large downloads	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	pending	stale
GET /api/v1/agent/browser/grants/{grant}/tabs	httpapi	internal/httpapi/server.go	agentBrowserTabs	agent_turn	5638f2fd7ee6	pending	pending	pending	pending	pending
GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame	httpapi	internal/httpapi/server.go	agentBrowserFrame	agent_turn	e032ab7f9192	aha2_browser.browser_frame	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot	httpapi	internal/httpapi/server.go	agentBrowserSnapshot	agent_turn	0e007e440538	pending	pending	pending	pending	pending
GET /api/v1/agent/capabilities	httpapi	internal/httpapi/server.go	agentCapabilitiesInfo	agent_turn	2ce1a4cbeeb0	aha2_core.capabilities	Turn capability; live Task/Agent/Turn	none	internal/httpapi/agent_pet_domains_test.go:TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary	stale
GET /api/v1/agent/channel/catalog	httpapi	internal/httpapi/server.go	agentChannelCatalog	agent_turn	4704b71efeee	pending	pending	pending	pending	pending
GET /api/v1/agent/channel/contacts	httpapi	internal/httpapi/server.go	agentTaskChannelContacts	agent_turn	d2de2ae87302	pending	pending	pending	pending	pending
GET /api/v1/agent/channel/context	httpapi	internal/httpapi/server.go	agentChannelContext	agent_turn	77cddcd8fbcc	pending	pending	pending	pending	pending
GET /api/v1/agent/desktop	httpapi	internal/httpapi/desktop_handlers.go	desktopStatus	agent_turn	c1b31ca1e14b	aha2_desktop.desktop_status	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_desktop_mcp_test.go:TestDesktopMCPUsesExistingShareAndSingleUseObservation	stale
GET /api/v1/agent/desktop/observation	httpapi	internal/httpapi/desktop_handlers.go	desktopObserve	agent_turn	32c0e7e62ac2	aha2_desktop.desktop_observe	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_desktop_mcp_test.go:TestDesktopMCPUsesExistingShareAndSingleUseObservation	stale
GET /api/v1/agent/hardware	httpapi	internal/httpapi/server.go	agentHardware	agent_turn	696d555070a0	aha2_hardware.hardware_list	bound Task resources; channel gate; pet live view permission	no credential values or internal device endpoints returned	internal/httpapi/agent_hardware_mcp_test.go:TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields	stale
GET /api/v1/agent/hardware/{hardware}/terminal	httpapi	internal/httpapi/server.go	agentHardwareTerminal	agent_turn	884f11781bef	aha2_hardware.hardware_terminal_read	bound Task resource; channel gate; pet live view permission	bounded output; no automatic connection	internal/httpapi/agent_hardware_mcp_test.go:TestAgentHardwareMCPTerminalReadPagingAndCaps	stale
GET /api/v1/agent/knowledge	httpapi	internal/httpapi/server.go	agentKnowledge	agent_turn	032fc5960306	pending	pending	pending	pending	pending
GET /api/v1/agent/knowledge/guide	httpapi	internal/httpapi/server.go	agentKnowledgeGuide	agent_turn	df4e74710e7d	pending	pending	pending	pending	pending
GET /api/v1/agent/knowledge/{knowledge}	httpapi	internal/httpapi/server.go	agentKnowledgeEntry	agent_turn	3ad977880f56	pending	pending	pending	pending	pending
GET /api/v1/agent/mail/accounts	httpapi	internal/httpapi/server.go	agentMailAccounts	agent_turn	cabc192bba79	pending	pending	pending	pending	pending
GET /api/v1/agent/mail/messages/{id}	httpapi	internal/httpapi/server.go	agentMailMessageStatus	agent_turn	bb2bb0fff2e0	pending	pending	pending	pending	pending
GET /api/v1/agent/mail/templates	httpapi	internal/httpapi/server.go	agentMailTemplates	agent_turn	cdc0974ac9bc	pending	pending	pending	pending	pending
GET /api/v1/agent/mail/templates/{id}	httpapi	internal/httpapi/server.go	agentMailTemplateDetail	agent_turn	19119c5a66b6	pending	pending	pending	pending	pending
GET /api/v1/agent/processes	httpapi	internal/httpapi/server.go	agentProcesses	agent_turn	c2a0fe7fa717	pending	pending	pending	pending	pending
GET /api/v1/agent/processes/{name}	httpapi	internal/httpapi/server.go	agentProcessStatus	agent_turn	4917b281b335	pending	pending	pending	pending	pending
GET /api/v1/agent/project/runtimes	httpapi	internal/httpapi/server.go	agentProjectRuntimes	agent_turn	8e4579b4cde7	pending	pending	pending	pending	pending
GET /api/v1/agent/project/workspaces	httpapi	internal/httpapi/server.go	agentProjectWorkspaces	agent_turn	14da067d859a	pending	pending	pending	pending	pending
GET /api/v1/agent/skills	httpapi	internal/httpapi/server.go	agentSkills	agent_turn	6296994b4ff6	pending	pending	pending	pending	pending
GET /api/v1/agent/skills/{skill}	httpapi	internal/httpapi/server.go	agentSkill	agent_turn	ffa8dc6a44e5	pending	pending	pending	pending	pending
GET /api/v1/agent/tasks/{task}	httpapi	internal/httpapi/server.go	agentTaskStatus	agent_turn	ee234efaebf9	pending	pending	pending	pending	pending
GET /api/v1/assistant	httpapi	internal/httpapi/server.go	ownerAssistant	owner_session	609d2b2d389f	pending	pending	pending	pending	pending
GET /api/v1/assistant/hints	httpapi	internal/httpapi/server.go	ownerAssistantHints	owner_session	a6ab25dcb059	pending	pending	pending	pending	pending
GET /api/v1/auth/status	httpapi	internal/httpapi/server.go	authStatus	transport_auth	d34e4b7b5fa0	pending	pending	pending	pending	pending
GET /api/v1/browser-grants/{id}	httpapi	internal/httpapi/browser_handlers.go	browserGrantDetail	owner_session	14c17363cc84	pending	pending	pending	pending	pending
GET /api/v1/browser-grants/{id}/bookmarks	httpapi	internal/httpapi/browser_handlers.go	browserGrantBookmarks	owner_session	ad0bcfb6b311	pending	pending	pending	pending	pending
GET /api/v1/browser-grants/{id}/tabs	httpapi	internal/httpapi/browser_handlers.go	listBrowserGrantTabs	owner_session	3f3fe6c71b9a	pending	pending	pending	pending	pending
GET /api/v1/browser-grants/{id}/tabs/{tab}/frame	httpapi	internal/httpapi/browser_handlers.go	browserTabFrame	owner_session	0d8e1f255cc1	pending	pending	pending	pending	pending
GET /api/v1/browser-grants/{id}/tabs/{tab}/snapshot	httpapi	internal/httpapi/browser_handlers.go	snapshotBrowserTab	owner_session	83d161e1935c	pending	pending	pending	pending	pending
GET /api/v1/browser/host	httpapi	internal/httpapi/browser_handlers.go	browserHostHealth	owner_session	0d3eae43b463	pending	pending	pending	pending	pending
GET /api/v1/browser/profiles	httpapi	internal/httpapi/browser_handlers.go	listBrowserProfiles	owner_session	97f732a50d5e	pending	pending	pending	pending	pending
GET /api/v1/browser/profiles/{id}	httpapi	internal/httpapi/browser_handlers.go	browserProfileDetail	owner_session	3e07ec5a501a	pending	pending	pending	pending	pending
GET /api/v1/browser/profiles/{id}/session	httpapi	internal/httpapi/browser_handlers.go	browserProfileSession	owner_session	a3180a45cff8	pending	pending	pending	pending	pending
GET /api/v1/channel-deliveries/{id}/attempts	httpapi	internal/httpapi/server.go	channelDeliveryAttempts	owner_session	d8f68db6b683	pending	pending	pending	pending	pending
GET /api/v1/channel-instances	httpapi	internal/httpapi/server.go	channelInstances	owner_session	e9f17bc45e0e	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}	httpapi	internal/httpapi/server.go	channelInstance	owner_session	bea19e276126	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/conversations	httpapi	internal/httpapi/server.go	channelConversationSummaries	owner_session	c94d543fd018	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/deliveries	httpapi	internal/httpapi/server.go	channelDeliveries	owner_session	1138aef9aee7	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/handoffs	httpapi	internal/httpapi/server.go	channelHandoffs	owner_session	07231a3a5e0d	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/knowledge-bindings	httpapi	internal/httpapi/server.go	channelKnowledgeBindings	owner_session	d13282ccb8d7	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/knowledge-policy	httpapi	internal/httpapi/server.go	channelKnowledgePolicy	owner_session	5e3031711393	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/onboarding-options	httpapi	internal/httpapi/server.go	channelOnboardingOptions	owner_session	077afb338f7a	pending	pending	pending	pending	pending
GET /api/v1/channel-instances/{id}/purge-preview	httpapi	internal/httpapi/server.go	channelPurgePreview	owner_session	d22ef0d038be	pending	pending	pending	pending	pending
GET /api/v1/channel-onboarding-sessions/{id}	httpapi	internal/httpapi/server.go	channelOnboarding	owner_session	71c9a92e956f	pending	pending	pending	pending	pending
GET /api/v1/channel-onboarding-sessions/{id}/qr	httpapi	internal/httpapi/server.go	channelOnboardingQR	owner_session	ee7feb75646d	pending	pending	pending	pending	pending
GET /api/v1/channel-providers	httpapi	internal/httpapi/server.go	channelProviders	owner_session	2893a68d5419	pending	pending	pending	pending	pending
GET /api/v1/codex-accounts	httpapi	internal/httpapi/server.go	listCodexAccounts	owner_session	52bbe428f6b0	pending	pending	pending	pending	pending
GET /api/v1/codex-accounts/login/{id}	httpapi	internal/httpapi/server.go	codexAccountLoginStatus	owner_session	276647c94174	pending	pending	pending	pending	pending
GET /api/v1/env-groups	httpapi	internal/httpapi/server.go	listEnvGroups	owner_session	b32d791fd4f1	pending	pending	pending	pending	pending
GET /api/v1/events	httpapi	internal/httpapi/server.go	allEvents	owner_session	a573a7b9116b	pending	pending	pending	pending	pending
GET /api/v1/hardware-resources	httpapi	internal/httpapi/server.go	listHardwareResources	owner_session	d4374eeba374	pending	pending	pending	pending	pending
GET /api/v1/hardware/serial-ports	httpapi	internal/httpapi/server.go	hardwareSerialPorts	owner_session	786f3169d90f	pending	pending	pending	pending	pending
GET /api/v1/knowledge	httpapi	internal/httpapi/server.go	listKnowledge	owner_session	5b602d063536	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/doc/{id}	httpapi	internal/httpapi/server.go	knowledgeVaultDocument	owner_session	fac4f8fba4d8	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/doc/{id}/diff	httpapi	internal/httpapi/server.go	knowledgeVaultDocumentDiff	owner_session	651e4e544c5e	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/file	httpapi	internal/httpapi/server.go	knowledgeVaultFile	owner_session	85e31c12584a	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/file/download	httpapi	internal/httpapi/server.go	knowledgeVaultFileDownload	owner_session	c206477c2836	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/files	httpapi	internal/httpapi/server.go	knowledgeVaultFiles	owner_session	253a988c3944	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/git/status	httpapi	internal/httpapi/server.go	knowledgeVaultGitStatus	owner_session	be7fe63739f2	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/inbox	httpapi	internal/httpapi/server.go	knowledgeVaultInbox	owner_session	cea23db05f59	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/libraries	httpapi	internal/httpapi/server.go	knowledgeVaultLibraries	owner_session	5ba2d9aa3fa2	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/open-info	httpapi	internal/httpapi/server.go	knowledgeVaultOpenInfo	owner_session	0ec916caeb0a	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/recent	httpapi	internal/httpapi/server.go	knowledgeVaultRecent	owner_session	dd191184de31	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/search	httpapi	internal/httpapi/server.go	knowledgeVaultSearch	owner_session	1bca1f44b5c9	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/skills	httpapi	internal/httpapi/server.go	knowledgeVaultSkills	owner_session	c1bd3971e751	pending	pending	pending	pending	pending
GET /api/v1/knowledge-vault/tree	httpapi	internal/httpapi/server.go	knowledgeVaultTree	owner_session	deba842fbef5	pending	pending	pending	pending	pending
GET /api/v1/knowledge/indexes	httpapi	internal/httpapi/server.go	knowledgeIndexes	owner_session	fd1785808b04	pending	pending	pending	pending	pending
GET /api/v1/knowledge/libraries	httpapi	internal/httpapi/server.go	listKnowledgeLibraries	owner_session	f9999c133192	pending	pending	pending	pending	pending
GET /api/v1/knowledge/proposals	httpapi	internal/httpapi/server.go	listKnowledgeProposals	owner_session	088317f80f4a	pending	pending	pending	pending	pending
GET /api/v1/knowledge/{id}	httpapi	internal/httpapi/server.go	knowledgeDetail	owner_session	59dcdd951155	pending	pending	pending	pending	pending
GET /api/v1/mail-accounts	httpapi	internal/httpapi/server.go	listMailAccounts	owner_session	e659d82512c1	pending	pending	pending	pending	pending
GET /api/v1/mail-accounts/{id}	httpapi	internal/httpapi/server.go	mailAccountDetail	owner_session	cb54b265bdb7	pending	pending	pending	pending	pending
GET /api/v1/mail-bindings	httpapi	internal/httpapi/server.go	listMailBindings	owner_session	06f6b22669b8	pending	pending	pending	pending	pending
GET /api/v1/mail-messages	httpapi	internal/httpapi/server.go	listMailMessages	owner_session	4875090ce73e	pending	pending	pending	pending	pending
GET /api/v1/mail-messages/{id}	httpapi	internal/httpapi/server.go	mailMessageDetail	owner_session	c85555d59ef6	pending	pending	pending	pending	pending
GET /api/v1/mail-messages/{id}/attempts	httpapi	internal/httpapi/server.go	mailMessageAttempts	owner_session	ca64b884b107	pending	pending	pending	pending	pending
GET /api/v1/mail-messages/{id}/download-links	httpapi	internal/httpapi/server.go	mailMessageDownloadLinks	owner_session	2e0bf04bd967	pending	pending	pending	pending	pending
GET /api/v1/mail-templates	httpapi	internal/httpapi/server.go	listMailTemplates	owner_session	1fa059f2c8cb	pending	pending	pending	pending	pending
GET /api/v1/mail-templates/{id}	httpapi	internal/httpapi/server.go	mailTemplateDetail	owner_session	c69929dcbd1f	pending	pending	pending	pending	pending
GET /api/v1/mail-templates/{id}/versions	httpapi	internal/httpapi/server.go	mailTemplateVersions	owner_session	cbdbd19f9005	pending	pending	pending	pending	pending
GET /api/v1/mcp-servers	httpapi	internal/httpapi/server.go	listMCPServers	owner_session	c87d4c8f48a7	pending	pending	pending	pending	pending
GET /api/v1/mcp-servers/{id}	httpapi	internal/httpapi/server.go	mcpServerDetail	owner_session	14226fd5f960	pending	pending	pending	pending	pending
GET /api/v1/models	httpapi	internal/httpapi/server.go	listModels	owner_session	118123b3b794	pending	pending	pending	pending	pending
GET /api/v1/pet	httpapi	internal/httpapi/server.go	ownerPetState	owner_session	bc250407762d	pending	pending	pending	pending	pending
GET /api/v1/pet/activity	httpapi	internal/httpapi/server.go	ownerPetActivity	owner_session	42b35bf2b1d9	pending	pending	pending	pending	pending
GET /api/v1/pet/approvals/pending-count	httpapi	internal/httpapi/server.go	petPendingApprovalCount	owner_session	ce65cf2ada42	pending	pending	pending	pending	pending
GET /api/v1/pet/events	httpapi	internal/httpapi/server.go	ownerPetEvents	owner_session	c4cd0221be32	pending	pending	pending	pending	pending
GET /api/v1/pet/prompts	httpapi	internal/httpapi/server.go	ownerPetPrompts	owner_session	032305154e76	pending	pending	pending	pending	pending
GET /api/v1/pet/workbench	httpapi	internal/httpapi/server.go	petWorkbench	owner_session	7f136c3d9038	pending	pending	pending	pending	pending
GET /api/v1/projects	httpapi	internal/httpapi/server.go	listProjects	owner_session	f5aa8ac5b85a	pending	pending	pending	pending	pending
GET /api/v1/projects/{id}/product-lines	httpapi	internal/httpapi/server.go	listProductLines	owner_session	82ec949cd92f	pending	pending	pending	pending	pending
GET /api/v1/prompts/entries	httpapi	internal/httpapi/server.go	promptEntryCatalog	owner_session	36f5ac49df7f	pending	pending	pending	pending	pending
GET /api/v1/prompts/entries/{entry}/anchors/{anchor}	httpapi	internal/httpapi/server.go	promptEntryAnchorPreview	owner_session	53142e1d168b	pending	pending	pending	pending	pending
GET /api/v1/prompts/migration-preview	httpapi	internal/httpapi/server.go	promptMigrationPreview	owner_session	10919e37dfc5	pending	pending	pending	pending	pending
GET /api/v1/prompts/scenario-preview	httpapi	internal/httpapi/server.go	promptScenarioPreview	owner_session	270beb975f40	pending	pending	pending	pending	pending
GET /api/v1/prompts/templates	httpapi	internal/httpapi/server.go	promptTemplates	owner_session	376de212c965	pending	pending	pending	pending	pending
GET /api/v1/providers	httpapi	internal/httpapi/server.go	listProviders	owner_session	89ed6c3e760a	pending	pending	pending	pending	pending
GET /api/v1/providers/{id}/model-detection-jobs/{job}/events	httpapi	internal/httpapi/server.go	modelDetectionJobEvents	owner_session	540019b5147f	pending	pending	pending	pending	pending
GET /api/v1/settings/agent-api	httpapi	internal/httpapi/server.go	agentAPISettings	owner_session	5e7970a91901	pending	pending	pending	pending	pending
GET /api/v1/settings/backend	httpapi	internal/httpapi/server.go	backendSettings	owner_session	1f6cae6ff0c2	pending	pending	pending	pending	pending
GET /api/v1/settings/general	httpapi	internal/httpapi/server.go	generalSettings	owner_session	0ce71def9858	pending	pending	pending	pending	pending
GET /api/v1/settings/knowledge-review	httpapi	internal/httpapi/server.go	knowledgeReviewSettings	owner_session	95b3f1ba9029	pending	pending	pending	pending	pending
GET /api/v1/settings/knowledge-vault	httpapi	internal/httpapi/server.go	knowledgeVaultSettings	owner_session	a570e6a8cbda	pending	pending	pending	pending	pending
GET /api/v1/settings/network	httpapi	internal/httpapi/server.go	networkSettings	owner_session	ba1d495af2ac	pending	pending	pending	pending	pending
GET /api/v1/settings/proxy	httpapi	internal/httpapi/server.go	proxySettings	owner_session	1761832c9ded	pending	pending	pending	pending	pending
GET /api/v1/settings/security	httpapi	internal/httpapi/server.go	securitySettings	owner_session	cf046cde901e	pending	pending	pending	pending	pending
GET /api/v1/settings/sync	httpapi	internal/httpapi/server.go	syncSettings	owner_session	c0656d05778c	pending	pending	pending	pending	pending
GET /api/v1/settings/sync/conflicts	httpapi	internal/httpapi/server.go	syncConflicts	owner_session	f9e9134f50d5	pending	pending	pending	pending	pending
GET /api/v1/settings/sync/preview	httpapi	internal/httpapi/server.go	syncPreview	owner_session	4e4df0a0d5dd	pending	pending	pending	pending	pending
GET /api/v1/settings/sync/status	httpapi	internal/httpapi/server.go	syncStatus	owner_session	623a77b167ac	pending	pending	pending	pending	pending
GET /api/v1/skills	httpapi	internal/httpapi/server.go	listSkills	owner_session	070e840ca8b0	pending	pending	pending	pending	pending
GET /api/v1/system	httpapi	internal/httpapi/server.go	systemInfo	owner_session	4c232fddebc8	pending	pending	pending	pending	pending
GET /api/v1/system/data	httpapi	internal/httpapi/server.go	dataInventory	owner_session	06f6a01494ca	pending	pending	pending	pending	pending
GET /api/v1/system/data/backups	httpapi	internal/httpapi/server.go	previewBackupCleanup	owner_session	8a0323018db5	pending	pending	pending	pending	pending
GET /api/v1/system/data/conversation/purge/progress	httpapi	internal/httpapi/server.go	conversationPurgeProgress	owner_session	51d76a672dca	pending	pending	pending	pending	pending
GET /api/v1/system/data/database	httpapi	internal/httpapi/server.go	databaseStats	owner_session	950dd466fb0a	pending	pending	pending	pending	pending
GET /api/v1/system/data/database/offline-compaction	httpapi	internal/httpapi/server.go	offlineCompactionInfo	owner_session	4fbd74158e7d	pending	pending	pending	pending	pending
GET /api/v1/system/data/hardware-io/purge/progress	httpapi	internal/httpapi/server.go	hardwareIOPurgeProgress	owner_session	d13a099226cd	pending	pending	pending	pending	pending
GET /api/v1/task-clone-sources	httpapi	internal/httpapi/server.go	listTaskCloneSources	owner_session	6247ec76afc0	pending	pending	pending	pending	pending
GET /api/v1/tasks	httpapi	internal/httpapi/server.go	listTasks	owner_session	557a48ab4674	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}	httpapi	internal/httpapi/server.go	taskDetail	owner_session	78c1c28b1374	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/agents	httpapi	internal/httpapi/server.go	taskAgents	owner_session	d1dd24d221e9	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/agents/{agent}/context	httpapi	internal/httpapi/server.go	agentContext	owner_session	afb9ece30d6f	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/agents/{agent}/conversation	httpapi	internal/httpapi/server.go	agentConversation	owner_session	e3de6644605a	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/attachments/{attachment}	httpapi	internal/httpapi/server.go	taskAttachmentContent	owner_or_agent	4fa8e10776f1	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/browser	httpapi	internal/httpapi/browser_handlers.go	taskBrowser	owner_session	5ceb9341933f	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/browser-grants	httpapi	internal/httpapi/browser_handlers.go	listTaskBrowserGrants	owner_session	1e396bdb5abb	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/browser-sessions	httpapi	internal/httpapi/browser_handlers.go	listTaskBrowserGrants	owner_session	1e396bdb5abb	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/channel-routes	httpapi	internal/httpapi/server.go	taskChannelRoutes	owner_session	de0fe2d511cd	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/context	httpapi	internal/httpapi/server.go	taskContext	owner_session	d0b45e1288e5	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/conversation	httpapi	internal/httpapi/server.go	taskConversation	owner_session	db1ca4d3a260	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/desktop	httpapi	internal/httpapi/desktop_handlers.go	desktopStatus	owner_session	b085f71f2c3c	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/desktop/observation	httpapi	internal/httpapi/desktop_handlers.go	desktopObserve	owner_session	8a8ca2327a87	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/desktop/stream	httpapi	internal/httpapi/desktop_handlers.go	desktopStream	owner_session	f41bdc367355	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/desktop/targets	httpapi	internal/httpapi/desktop_handlers.go	desktopTargets	owner_session	8fdbcf19bf00	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/desktop/windows	httpapi	internal/httpapi/desktop_handlers.go	desktopWindows	owner_session	1c7152f14802	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/events	httpapi	internal/httpapi/server.go	taskEvents	owner_session	88909018d331	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/hardware	httpapi	internal/httpapi/server.go	taskHardware	owner_session	62fd549da794	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/hardware/{hardware}/host-key	httpapi	internal/httpapi/server.go	hardwareSSHHostKey	owner_session	cec50c483734	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/hardware/{hardware}/terminal	httpapi	internal/httpapi/server.go	hardwareTerminal	owner_session	70a7c37ca04d	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws	httpapi	internal/httpapi/server.go	hardwareTerminalWebSocket	owner_session	27a829ca6fb5	pending	pending	pending	pending	pending
GET /api/v1/tasks/{id}/mail	httpapi	internal/httpapi/server.go	taskMailOptions	owner_session	c1f83106d259	pending	pending	pending	pending	pending
GET /api/v1/workspaces	httpapi	internal/httpapi/server.go	listWorkspaces	owner_session	8558d36ccf03	pending	pending	pending	pending	pending
GET /api/v1/workspaces/{id}/host-key	httpapi	internal/httpapi/server.go	workspaceSSHHostKey	owner_session	69273cf1fa22	pending	pending	pending	pending	pending
GET /d/{token}	mail-download	internal/httpapi/mail_download_handlers.go	mailDownloadConfirmPage	transport_auth	5beb7fa9f2c9	pending	pending	pending	pending	pending
GET /healthz	centersync	internal/centersync/service.go	inline	transport_auth	ce06a910b2e6	none	device token (or one-time bootstrap code) of the separate aha-sync center service	device-to-center sync protocol served by cmd/aha-sync, not the owner Web listener; owner sync management is the httpapi settings/sync rows	internal/centersync/service_test.go:TestHealthAndAuthentication	transport_na
GET /healthz	httpapi	internal/httpapi/server.go	health	transport_auth	56d4cce0d9f0	none	none (liveness probe)	returns only ok/service; no business data	internal/httpapi/pet_coverage_ledger_test.go:TestHealthProbeCarriesNoBusinessData	transport_na
GET /v1/sync/pull	centersync	internal/centersync/service.go	inline	device_token	b70e1e6f4bff	none	device token (or one-time bootstrap code) of the separate aha-sync center service	device-to-center sync protocol served by cmd/aha-sync, not the owner Web listener; owner sync management is the httpapi settings/sync rows	internal/centersync/service_test.go:TestPushIsIdempotentAndPullIsIncremental	transport_na
PATCH /api/v1/agent/turn/memory	httpapi	internal/httpapi/server.go	updateAgentMemory	agent_turn	bbedd9b72033	aha2_core.memory_update	Main; writable snapshot; current Task/Turn	none	internal/httpapi/agent_pet_domains_test.go:TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary	stale
PATCH /api/v1/browser/profiles/{id}	httpapi	internal/httpapi/browser_handlers.go	renameBrowserProfile	owner_session	81aad22e2da0	pending	pending	pending	pending	pending
PATCH /api/v1/channel-instances/{id}	httpapi	internal/httpapi/server.go	updateChannelInstance	owner_session	aff6e852482c	pending	pending	pending	pending	pending
PATCH /api/v1/channel-plugins/{id}	httpapi	internal/httpapi/server.go	updateChannelPlugin	owner_session	f321a722a04b	pending	pending	pending	pending	pending
PATCH /api/v1/pet/watches/{task}	httpapi	internal/httpapi/server.go	updatePetWatch	owner_session	eed899230af5	pending	pending	pending	pending	pending
PATCH /api/v1/settings/proxy/profiles/{id}	httpapi	internal/httpapi/server.go	patchProxyProfile	owner_session	94edf5483c6f	pending	pending	pending	pending	pending
PATCH /api/v1/tasks/{id}	httpapi	internal/httpapi/server.go	updateTaskTitle	owner_session	6f65ebe59602	pending	pending	pending	pending	pending
PATCH /api/v1/tasks/{id}/agents/{agent}	httpapi	internal/httpapi/server.go	updateAgentConfig	owner_session	becd09e3e680	pending	pending	pending	pending	pending
PATCH /api/v1/tasks/{id}/collaboration	httpapi	internal/httpapi/server.go	updateTaskCollaboration	owner_session	fcb61ead4e18	pending	pending	pending	pending	pending
PATCH /v1/devices/self	centersync	internal/centersync/service.go	inline	device_token	f6a1dc16ce2d	none	device token (or one-time bootstrap code) of the separate aha-sync center service	device-to-center sync protocol served by cmd/aha-sync, not the owner Web listener; owner sync management is the httpapi settings/sync rows	internal/centersync/service_test.go:TestDeviceRenameKeepsCenterGeneratedID	transport_na
POST /api/channel-runtime/v1/commands/{id}/attachment	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/commands/{id}/complete	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/commands/{id}/progress	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/deliveries/{id}/ack	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/deliveries/{id}/media	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/deliveries/{id}/nack	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/handshake	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/instances/{instance_id}/commands:claim	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/instances/{instance_id}/deliveries:claim	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/channel-runtime/v1/instances/{instance_id}/inbound-events	httpapi	internal/httpapi/server.go	withChannelCapability	transport_auth	e1d101d20c1a	pending	pending	pending	pending	pending
POST /api/v1/agent/browser/grants/{grant}/cookies	httpapi	internal/httpapi/server.go	agentBrowserImportCookies	agent_turn	0af73298bb82	pending	pending	pending	pending	pending
POST /api/v1/agent/browser/grants/{grant}/tabs	httpapi	internal/httpapi/server.go	agentBrowserOpenTab	agent_turn	d9c036413870	aha2_browser.browser_tab_open	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
POST /api/v1/agent/browser/grants/{grant}/tabs/current	httpapi	internal/httpapi/server.go	agentBrowserBindCurrent	agent_turn	df70de5f291b	aha2_browser.browser_tab_bind_current	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	pending	stale
POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions	httpapi	internal/httpapi/server.go	agentBrowserAction	agent_turn	7d8326918c1b	aha2_browser.browser_action	live Turn; current Task; channel exclusion; write tools Main+writable snapshot; pet live permission	existing service grant/resource/observation checks retained; no target selection or credentials accepted	internal/httpapi/agent_browser_workspace_operations_test.go:TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut	stale
```
