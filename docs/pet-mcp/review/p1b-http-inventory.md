# P1b：注册 HTTP 操作账本

`1b8246266cbaeba6a905801bf3e28ebc85223f2b..c8644634c374d89ec0bcc96ea16fb0a46df4e797`。本阶段是完整需求的可追溯起点，不是全覆盖验收结论。

425 行来自四个实际注册文件，含桌面两张有限路由表及常量路由。source 列相对 `internal/httpapi/`，handler 是可定位函数；同一路由若承载多个业务子操作，后续仍需拆分操作核验。284 处 Web 字面调用点和扫描脚本见 P1c。411 行仍未满足最终门禁，包括已实现但生命周期测试未齐的硬件操作。认证/审批/凭据相关行保持 pending，不以 owner_only 或 N/A 结束盘点。

验证：`python3 scripts/pet-mcp-coverage.py` 退出 0；`--complete` 当前预期退出 1。扫描器不访问运行服务、数据库或任何凭据。新注册语法超出已支持形式会直接报错，避免静默漏项。以下是生成调用链与该提交完整 diff。

```python
def routes():
    rows = {}
    constants = {}
    for source_path in (ROOT / "internal/httpapi").glob("*.go"):
        if not source_path.name.endswith("_test.go"):
            constants.update(re.findall(r'\b(\w+)\s*=\s*"([^"\n]+)"', source_path.read_text()))
    for path in sorted((ROOT / "internal/httpapi").glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        source = path.read_text()
        for match in re.finditer(r'mux\.Handle(?:Func)?\(([^\n]+)', source):
            line = match.group(1)
            symbol = re.match(r'(\w+)\s*,',line)
            if symbol and symbol.group(1) in constants:
                line = '"' + constants[symbol.group(1)] + '",' + line[symbol.end():]
            literal = re.match(r'"([^"\n]+)"\s*,', line)
            if literal:
                pattern = literal.group(1)
                names = re.findall(r'\bs\.([A-Za-z0-9_]+)', line)
                handler = names[-1] if names else "inline"
                boundary = "agent_turn" if "withAgentCapability" in line else "owner_session" if "withAuth" in line else "transport_auth"
                add_route(rows, pattern, path, handler, boundary)
            elif path.name == "desktop_handlers.go" and line.startswith("route.pattern,"):
                # Both finite desktop tables are expanded below. Any third
                # dynamic registration must be taught to this scanner.
                continue
            else:
                raise ValueError(f"unsupported registration: {path.name}: {line}")
        if path.name == "desktop_handlers.go":
            if len(re.findall(r'mux\.Handle\(route.pattern,', source)) != 2:
                raise ValueError("desktop route tables changed; review scanner")
            for expression, handler in re.findall(r'\{([^\n]+?),\s*s\.(\w+)\},', source):
                if not re.match(r'"(?:GET|POST|DELETE|PUT|PATCH) ', expression):
                    continue
                expression = expression.replace('prefix', '"/api/v1/tasks/{id}/desktop"')
                parts = re.findall(r'"([^"\n]*)"', expression)
                remainder = re.sub(r'"[^"\n]*"|[+\s]', '', expression)
                if remainder:
                    raise ValueError(f"unsupported desktop pattern: {expression}")
                pattern = ''.join(parts)
                add_route(rows, pattern, path, handler, "agent_turn" if "/agent/" in pattern else "owner_session")
    return rows


def add_route(rows, pattern, path, handler, boundary):
    # Static resources and methodless fallback are still accounted for.
    method, sep, route = pattern.partition(' ')
    if not sep:
        method, route = "ANY", pattern
    key = f"{method} {route}"
    if key in rows:
        raise ValueError(f"duplicate route: {key}")
    rows[key] = {"operation": key, "source": path.name, "handler": handler, "boundary": boundary}

```

```diff
diff --git a/docs/pet-mcp/coverage.tsv b/docs/pet-mcp/coverage.tsv
new file mode 100644
index 0000000..f5ebcb2
--- /dev/null
+++ b/docs/pet-mcp/coverage.tsv
@@ -0,0 +1,426 @@
+operation	source	handler	boundary	mcp	permission	protected_flow	tests	status
+ANY /	server.go	serveWeb	transport_auth	not a business operation	existing transport boundary	health probe or static Web delivery; no MCP business action	existing HTTP server tests	transport_na
+ANY /api/v1/prompts/entries	server.go	rejectPromptReadOnly	owner_session	pending	pending	pending	pending	pending
+ANY /api/v1/prompts/entries/{entry}/anchors/{anchor}	server.go	rejectPromptReadOnly	owner_session	pending	pending	pending	pending	pending
+ANY /api/v1/prompts/migration-preview	server.go	rejectPromptReadOnly	owner_session	pending	pending	pending	pending	pending
+ANY /api/v1/prompts/scenario-preview	server.go	rejectPromptReadOnly	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/agent/browser/grants/{grant}	server.go	agentBrowserRelease	agent_turn	pending	pending	pending	pending	pending
+DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}	server.go	agentBrowserCloseTab	agent_turn	pending	pending	pending	pending	pending
+DELETE /api/v1/browser-grants/{id}	browser_handlers.go	revokeBrowserGrant	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/browser-grants/{id}/tabs/{tab}	browser_handlers.go	closeBrowserTab	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/browser/profiles/{id}	browser_handlers.go	deleteBrowserProfile	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/browser/profiles/{id}/session	browser_handlers.go	closeBrowserProfileSession	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/codex-accounts/login/{id}	server.go	cancelCodexAccountLogin	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/codex-accounts/{id}	server.go	deleteCodexAccount	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/hardware-resources/{id}	server.go	deleteHardwareResource	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/knowledge/libraries/{id}	server.go	deleteKnowledgeLibrary	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/knowledge/{id}	server.go	deleteKnowledge	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/mail-accounts/{id}	server.go	deleteMailAccount	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/mail-bindings/{id}	server.go	deleteMailBinding	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/mail-templates/{id}	server.go	deleteMailTemplate	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/mcp-servers/{id}	server.go	deleteMCPServer	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/models/{id}	server.go	deleteModel	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/pet/watches/{task}	server.go	deletePetWatch	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/projects/{id}	server.go	deleteProject	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/projects/{id}/knowledge	server.go	deleteProjectKnowledge	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/projects/{id}/product-lines/{line}	server.go	deleteProductLine	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/providers/{id}	server.go	deleteProvider	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/settings/proxy/profiles/{id}	server.go	deleteProxyProfile	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/skills/{id}	server.go	rejectRemovedSkillWrite	owner_session	Web also rejects this removed legacy mutation	owner session still cannot execute	use the vault Skill management operations instead	existing Skill removal tests; exact reference pending	web_prohibited_na
+DELETE /api/v1/tasks/{id}	server.go	deleteTask	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/tasks/{id}/attachments/{attachment}	server.go	deleteTaskAttachment	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/tasks/{id}/channel-routes/{route}	server.go	unbindTaskChannelRoute	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/tasks/{id}/remote-mirror	server.go	retireRemoteTaskMirror	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/workspaces/{id}	server.go	deleteWorkspace	owner_session	pending	pending	pending	pending	pending
+DELETE /api/v1/workspaces/{id}/remote-mirror	server.go	retireRemoteWorkspaceMirror	owner_session	pending	pending	pending	pending	pending
+GET /api/channel-runtime/v1/deliveries/{id}/attachment	server.go	channelRuntimeMediaContent	transport_auth	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser	server.go	agentBrowserState	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/bookmarks	server.go	agentBrowserBookmarks	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/cookies	server.go	agentBrowserCookies	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/downloads	server.go	agentBrowserDownloads	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/downloads/{download}	server.go	agentBrowserDownload	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/tabs	server.go	agentBrowserTabs	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame	server.go	agentBrowserFrame	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot	server.go	agentBrowserSnapshot	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/capabilities	server.go	agentCapabilitiesInfo	agent_turn	aha2_core.capabilities	Turn capability; live Task/Agent/Turn	none	internal/httpapi/agent_pet_domains_test.go:TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary	implemented
+GET /api/v1/agent/channel/catalog	server.go	agentChannelCatalog	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/channel/contacts	server.go	agentTaskChannelContacts	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/channel/context	server.go	agentChannelContext	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/desktop	desktop_handlers.go	desktopStatus	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/desktop/observation	desktop_handlers.go	desktopObserve	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/hardware	server.go	agentHardware	agent_turn	aha2_hardware.hardware_list	bound Task resources; channel gate; pet live view permission	no credential values or internal device endpoints returned	internal/httpapi/agent_hardware_mcp_test.go:TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields	implemented
+GET /api/v1/agent/hardware/{hardware}/terminal	server.go	agentHardwareTerminal	agent_turn	aha2_hardware.hardware_terminal_read	bound Task resource; channel gate; pet live view permission	bounded output; no automatic connection	internal/httpapi/agent_hardware_mcp_test.go:TestAgentHardwareMCPTerminalReadPagingAndCaps	implemented
+GET /api/v1/agent/knowledge	server.go	agentKnowledge	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/knowledge/guide	server.go	agentKnowledgeGuide	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/knowledge/{knowledge}	server.go	agentKnowledgeEntry	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/mail/accounts	server.go	agentMailAccounts	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/mail/messages/{id}	server.go	agentMailMessageStatus	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/mail/templates	server.go	agentMailTemplates	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/mail/templates/{id}	server.go	agentMailTemplateDetail	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/processes	server.go	agentProcesses	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/processes/{name}	server.go	agentProcessStatus	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/project/runtimes	server.go	agentProjectRuntimes	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/project/workspaces	server.go	agentProjectWorkspaces	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/skills	server.go	agentSkills	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/skills/{skill}	server.go	agentSkill	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/agent/tasks/{task}	server.go	agentTaskStatus	agent_turn	pending	pending	pending	pending	pending
+GET /api/v1/assistant	server.go	ownerAssistant	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/assistant/hints	server.go	ownerAssistantHints	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/auth/status	server.go	authStatus	transport_auth	pending	pending	pending	pending	pending
+GET /api/v1/browser-grants/{id}	browser_handlers.go	browserGrantDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser-grants/{id}/bookmarks	browser_handlers.go	browserGrantBookmarks	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser-grants/{id}/tabs	browser_handlers.go	listBrowserGrantTabs	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser-grants/{id}/tabs/{tab}/frame	browser_handlers.go	browserTabFrame	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser-grants/{id}/tabs/{tab}/snapshot	browser_handlers.go	snapshotBrowserTab	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser/host	browser_handlers.go	browserHostHealth	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser/profiles	browser_handlers.go	listBrowserProfiles	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser/profiles/{id}	browser_handlers.go	browserProfileDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/browser/profiles/{id}/session	browser_handlers.go	browserProfileSession	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-deliveries/{id}/attempts	server.go	channelDeliveryAttempts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances	server.go	channelInstances	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}	server.go	channelInstance	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/conversations	server.go	channelConversationSummaries	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/deliveries	server.go	channelDeliveries	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/handoffs	server.go	channelHandoffs	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/knowledge-bindings	server.go	channelKnowledgeBindings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/knowledge-policy	server.go	channelKnowledgePolicy	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/onboarding-options	server.go	channelOnboardingOptions	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-instances/{id}/purge-preview	server.go	channelPurgePreview	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-onboarding-sessions/{id}	server.go	channelOnboarding	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-onboarding-sessions/{id}/qr	server.go	channelOnboardingQR	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/channel-providers	server.go	channelProviders	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/codex-accounts	server.go	listCodexAccounts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/codex-accounts/login/{id}	server.go	codexAccountLoginStatus	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/env-groups	server.go	listEnvGroups	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/events	server.go	allEvents	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/hardware-resources	server.go	listHardwareResources	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/hardware/serial-ports	server.go	hardwareSerialPorts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge	server.go	listKnowledge	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/doc/{id}	server.go	knowledgeVaultDocument	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/doc/{id}/diff	server.go	knowledgeVaultDocumentDiff	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/file	server.go	knowledgeVaultFile	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/file/download	server.go	knowledgeVaultFileDownload	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/files	server.go	knowledgeVaultFiles	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/git/status	server.go	knowledgeVaultGitStatus	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/inbox	server.go	knowledgeVaultInbox	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/libraries	server.go	knowledgeVaultLibraries	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/open-info	server.go	knowledgeVaultOpenInfo	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/recent	server.go	knowledgeVaultRecent	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/search	server.go	knowledgeVaultSearch	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/skills	server.go	knowledgeVaultSkills	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge-vault/tree	server.go	knowledgeVaultTree	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge/indexes	server.go	knowledgeIndexes	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge/libraries	server.go	listKnowledgeLibraries	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge/proposals	server.go	listKnowledgeProposals	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/knowledge/{id}	server.go	knowledgeDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-accounts	server.go	listMailAccounts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-accounts/{id}	server.go	mailAccountDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-bindings	server.go	listMailBindings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-messages	server.go	listMailMessages	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-messages/{id}	server.go	mailMessageDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-messages/{id}/attempts	server.go	mailMessageAttempts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-messages/{id}/download-links	server.go	mailMessageDownloadLinks	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-templates	server.go	listMailTemplates	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-templates/{id}	server.go	mailTemplateDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mail-templates/{id}/versions	server.go	mailTemplateVersions	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mcp-servers	server.go	listMCPServers	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/mcp-servers/{id}	server.go	mcpServerDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/models	server.go	listModels	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/pet	server.go	ownerPetState	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/pet/activity	server.go	ownerPetActivity	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/pet/approvals/pending-count	server.go	petPendingApprovalCount	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/pet/events	server.go	ownerPetEvents	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/pet/prompts	server.go	ownerPetPrompts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/pet/workbench	server.go	petWorkbench	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/projects	server.go	listProjects	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/projects/{id}/product-lines	server.go	listProductLines	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/prompts/entries	server.go	promptEntryCatalog	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/prompts/entries/{entry}/anchors/{anchor}	server.go	promptEntryAnchorPreview	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/prompts/migration-preview	server.go	promptMigrationPreview	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/prompts/scenario-preview	server.go	promptScenarioPreview	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/prompts/templates	server.go	promptTemplates	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/providers	server.go	listProviders	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/providers/{id}/model-detection-jobs/{job}/events	server.go	modelDetectionJobEvents	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/agent-api	server.go	agentAPISettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/backend	server.go	backendSettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/general	server.go	generalSettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/knowledge-review	server.go	knowledgeReviewSettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/knowledge-vault	server.go	knowledgeVaultSettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/network	server.go	networkSettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/proxy	server.go	proxySettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/security	server.go	securitySettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/sync	server.go	syncSettings	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/sync/conflicts	server.go	syncConflicts	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/sync/preview	server.go	syncPreview	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/settings/sync/status	server.go	syncStatus	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/skills	server.go	listSkills	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system	server.go	systemInfo	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system/data	server.go	dataInventory	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system/data/backups	server.go	previewBackupCleanup	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system/data/conversation/purge/progress	server.go	conversationPurgeProgress	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system/data/database	server.go	databaseStats	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system/data/database/offline-compaction	server.go	offlineCompactionInfo	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/system/data/hardware-io/purge/progress	server.go	hardwareIOPurgeProgress	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/task-clone-sources	server.go	listTaskCloneSources	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks	server.go	listTasks	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}	server.go	taskDetail	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/agents	server.go	taskAgents	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/agents/{agent}/context	server.go	agentContext	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/agents/{agent}/conversation	server.go	agentConversation	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/attachments/{attachment}	server.go	taskAttachmentContent	transport_auth	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/browser	browser_handlers.go	taskBrowser	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/browser-grants	browser_handlers.go	listTaskBrowserGrants	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/browser-sessions	browser_handlers.go	listTaskBrowserGrants	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/channel-routes	server.go	taskChannelRoutes	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/context	server.go	taskContext	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/conversation	server.go	taskConversation	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/desktop	desktop_handlers.go	desktopStatus	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/desktop/observation	desktop_handlers.go	desktopObserve	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/desktop/stream	desktop_handlers.go	desktopStream	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/desktop/targets	desktop_handlers.go	desktopTargets	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/desktop/windows	desktop_handlers.go	desktopWindows	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/events	server.go	taskEvents	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/hardware	server.go	taskHardware	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/hardware/{hardware}/host-key	server.go	hardwareSSHHostKey	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/hardware/{hardware}/terminal	server.go	hardwareTerminal	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws	server.go	hardwareTerminalWebSocket	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/tasks/{id}/mail	server.go	taskMailOptions	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/workspaces	server.go	listWorkspaces	owner_session	pending	pending	pending	pending	pending
+GET /api/v1/workspaces/{id}/host-key	server.go	workspaceSSHHostKey	owner_session	pending	pending	pending	pending	pending
+GET /d/{token}	mail_download_handlers.go	mailDownloadConfirmPage	transport_auth	pending	pending	pending	pending	pending
+GET /healthz	server.go	health	transport_auth	not a business operation	existing transport boundary	health probe or static Web delivery; no MCP business action	existing HTTP server tests	transport_na
+PATCH /api/v1/agent/turn/memory	server.go	updateAgentMemory	agent_turn	aha2_core.memory_update	Main; writable snapshot; current Task/Turn	none	internal/httpapi/agent_pet_domains_test.go:TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary	implemented
+PATCH /api/v1/browser/profiles/{id}	browser_handlers.go	renameBrowserProfile	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/channel-instances/{id}	server.go	updateChannelInstance	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/channel-plugins/{id}	server.go	updateChannelPlugin	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/pet/watches/{task}	server.go	updatePetWatch	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/settings/proxy/profiles/{id}	server.go	patchProxyProfile	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/tasks/{id}	server.go	updateTaskTitle	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/tasks/{id}/agents/{agent}	server.go	updateAgentConfig	owner_session	pending	pending	pending	pending	pending
+PATCH /api/v1/tasks/{id}/collaboration	server.go	updateTaskCollaboration	owner_session	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/commands/{id}/attachment	server.go	channelRuntimeUploadMedia	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/commands/{id}/complete	server.go	channelRuntimeCommandComplete	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/commands/{id}/progress	server.go	channelRuntimeCommandProgress	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/deliveries/{id}/ack	server.go	channelRuntimeDeliveryAck	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/deliveries/{id}/media	server.go	channelRuntimeMediaUploaded	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/deliveries/{id}/nack	server.go	channelRuntimeDeliveryNack	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/handshake	server.go	channelRuntimeHandshake	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/instances/{instance_id}/commands:claim	server.go	channelRuntimeClaimCommands	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/instances/{instance_id}/deliveries:claim	server.go	channelRuntimeClaimDeliveries	transport_auth	pending	pending	pending	pending	pending
+POST /api/channel-runtime/v1/instances/{instance_id}/inbound-events	server.go	channelRuntimeInbound	transport_auth	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/cookies	server.go	agentBrowserImportCookies	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/tabs	server.go	agentBrowserOpenTab	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/tabs/current	server.go	agentBrowserBindCurrent	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions	server.go	agentBrowserAction	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate	server.go	agentBrowserActivate	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/files	server.go	agentBrowserUpload	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate	server.go	agentBrowserNavigate	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/browser/mcp	server.go	agentBrowserMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/channel/actions/preview	server.go	previewAgentChannelAction	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/channel/handoffs	server.go	createAgentChannelHandoff	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/channel/mcp	server.go	agentChannelMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/channel/messages	server.go	sendAgentTaskChannelMessage	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/channel/replies	server.go	sendAgentChannelReply	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/channel/reply-decision	server.go	setAgentChannelReplyDecision	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/collab/mcp	server.go	agentCollabMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/collaboration/batches	server.go	submitAgentCollaboration	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/collaboration/batches/{batch}/wait	server.go	waitAgentCollaboration	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/core/mcp	server.go	agentCoreMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/desktop/actions	desktop_handlers.go	desktopAct	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/desktop/control	desktop_handlers.go	desktopControl	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/hardware/mcp	server.go	agentHardwareMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/hardware/{hardware}/adb/exec	server.go	agentADBExec	agent_turn	aha2_hardware.hardware_adb_exec	Main+writable snapshot; bound writable resource; pet live create_tasks permission	no host ADB commands; owner pairing remains protected	internal/httpapi/agent_pet_domains_test.go:TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions	implemented
+POST /api/v1/agent/hardware/{hardware}/connect	server.go	agentConnectHardware	agent_turn	aha2_hardware.hardware_connect	Main+writable snapshot; bound resource; pet live create_tasks permission	stored credentials only; unknown host key requires owner flow	internal/httpapi/agent_pet_domains_test.go:TestAgentMCPOperationRejectsInvalidBeforeHandlerAndSanitizesResults	implemented_unverified
+POST /api/v1/agent/hardware/{hardware}/disconnect	server.go	agentDisconnectHardware	agent_turn	aha2_hardware.hardware_disconnect	Main+writable snapshot; bound resource; pet live create_tasks permission	none	pending	implemented_unverified
+POST /api/v1/agent/hardware/{hardware}/login	server.go	agentLoginHardware	agent_turn	aha2_hardware.hardware_login	Main+writable snapshot; bound writable resource; pet live create_tasks permission	stored credentials only; password not accepted or returned	pending	implemented_unverified
+POST /api/v1/agent/hardware/{hardware}/send	server.go	agentSendHardware	agent_turn	aha2_hardware.hardware_send	Main+writable snapshot; bound writable resource; pet live create_tasks permission	no credentials schema; terminal output separate	pending	implemented_unverified
+POST /api/v1/agent/knowledge/candidates	server.go	submitAgentKnowledge	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/knowledge/entries	server.go	submitAgentKnowledgeEntry	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/knowledge/feedback	server.go	submitAgentKnowledgeFeedbackEntry	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/knowledge/mcp	server.go	agentKnowledgeMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/knowledge/{knowledge}/feedback	server.go	submitAgentKnowledgeFeedback	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/mail/mcp	server.go	agentMailMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/agent/mail/messages	server.go	agentMailSend	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/mail/preview	server.go	agentMailPreview	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/processes	server.go	startAgentProcess	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/processes/{name}/stop	server.go	stopAgentProcess	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/skills	server.go	createAgentSkill	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/tasks	server.go	createAgentTask	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/turn/attachments	server.go	uploadAgentAttachment	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/turn/inbox/ack	server.go	ackAgentInbox	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/turn/inbox/claim	server.go	claimAgentInbox	agent_turn	pending	pending	pending	pending	pending
+POST /api/v1/agent/turn/inbox/wait	server.go	waitAgentInbox	agent_turn	aha2_core.inbox_wait	Turn capability; current Task/Agent; live Turn	none	internal/httpapi/agent_core_mcp_test.go:TestAgentCoreMCP	implemented
+POST /api/v1/agent/turn/messages	server.go	addAgentProgressMessage	agent_turn	aha2_core.progress; aha2_core.attachment_bind	MCP Main+writable; HTTP existing Agent scope	attachment bytes use existing upload API; IDs remain Task scoped	internal/httpapi/agent_pet_domains_test.go:TestPetCoreMCPCallsUseRealTurnAndKeepManagementBoundary	implemented
+POST /api/v1/agent/workspace/mcp	server.go	agentWorkspaceMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/assistant/hints/dismiss	server.go	dismissOwnerAssistantHint	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/assistant/messages	server.go	askOwnerAssistant	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/auth/login	server.go	authLogin	transport_auth	pending	pending	pending	pending	pending
+POST /api/v1/auth/logout	server.go	authLogout	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/auth/password	server.go	authChangePassword	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/auth/recover	server.go	authRecover	transport_auth	pending	pending	pending	pending	pending
+POST /api/v1/auth/register	server.go	authRegister	transport_auth	pending	pending	pending	pending	pending
+POST /api/v1/browser-grants/{id}/tabs	browser_handlers.go	openBrowserTab	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser-grants/{id}/tabs/current	browser_handlers.go	adoptBrowserCurrentTab	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser-grants/{id}/tabs/{tab}/activate	browser_handlers.go	activateBrowserTab	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser-grants/{id}/tabs/{tab}/navigate	browser_handlers.go	navigateBrowserTab	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser-sessions/{id}/close	browser_handlers.go	closeBrowserSession	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser/profiles	browser_handlers.go	createBrowserProfile	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser/profiles/{id}/login-window	browser_handlers.go	openBrowserLoginWindow	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/browser/profiles/{id}/transfer	browser_handlers.go	transferBrowserProfile	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-deliveries/{id}/replay	server.go	replayChannelDelivery	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-deliveries/{id}/skip	server.go	skipChannelDelivery	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-instances	server.go	createChannelInstance	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-instances/{id}/archive	server.go	archiveChannelInstance	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-instances/{id}/onboarding-sessions	server.go	startChannelOnboarding	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-instances/{id}/purge	server.go	purgeChannelInstance	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-instances/{id}/reset-binding	server.go	resetChannelBinding	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-instances/{id}/runtime-menus/reset	server.go	resetChannelRuntimeMenus	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/channel-onboarding-sessions/{id}/cancel	server.go	cancelChannelOnboarding	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/codex-accounts/import	server.go	importCodexAccount	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/codex-accounts/import-local	server.go	importLocalCodexAccount	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/codex-accounts/login	server.go	startCodexAccountLogin	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/codex-accounts/login/{id}/callback	server.go	submitCodexAccountCallback	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/codex-accounts/{id}/refresh	server.go	refreshCodexAccount	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/env-groups	server.go	createEnvGroup	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/hardware-resources	server.go	createHardwareResource	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/hardware-resources/{id}/adb/pair	server.go	pairHardwareADB	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge	server.go	createKnowledge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge-vault/git/commit	server.go	commitKnowledgeVaultGit	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge-vault/git/init	server.go	initializeKnowledgeVaultGit	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge-vault/git/pull	server.go	pullKnowledgeVaultGit	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge-vault/git/push	server.go	pushKnowledgeVaultGit	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge-vault/git/sync	server.go	syncKnowledgeVaultGit	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge-vault/reveal	server.go	revealKnowledgeVaultPath	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/libraries/{id}/bind	server.go	bindKnowledgeLibrary	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/libraries/{id}/unbind	server.go	unbindKnowledgeLibrary	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/proposals/batch	server.go	batchKnowledgeProposalReviews	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/proposals/{id}/approve	server.go	approveKnowledgeProposal	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/proposals/{id}/reject	server.go	rejectKnowledgeProposal	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/{id}/feedback	server.go	feedbackKnowledge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/knowledge/{id}/verify	server.go	verifyKnowledge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/local/owner-messages	server.go	localOwnerMessageIngest	transport_auth	pending	pending	pending	pending	pending
+POST /api/v1/mail-accounts	server.go	createMailAccount	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-accounts/{id}/test	server.go	testMailAccount	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-bindings	server.go	createMailBinding	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages	server.go	createMailMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages/{id}/approve	server.go	approveMailMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages/{id}/cancel	server.go	cancelMailMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages/{id}/download-links/{attachment_id}/revoke	server.go	revokeMailMessageDownloadLink	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages/{id}/reject	server.go	rejectMailMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages/{id}/retry	server.go	retryMailMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-messages/{id}/submit	server.go	submitMailMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-templates	server.go	createMailTemplate	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-templates/preview	server.go	previewMailTemplateContent	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mail-templates/{id}/preview	server.go	previewMailTemplate	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mcp-servers	server.go	saveMCPServer	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/mcp-servers/test	server.go	testMCPServer	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/models	server.go	createModel	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/models/batch-delete	server.go	requestBatchDeleteModels	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/models/{id}/health-check	server.go	checkModelHealth	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/pet/approvals/{id}/resolve	server.go	resolvePetApproval	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/pet/brain	server.go	ownerPetBrain	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/pet/control	server.go	controlOwnerPet	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/pet/mcp	server.go	petMCP	agent_turn	MCP protocol transport; operation tools tracked separately	agent_turn	existing authentication middleware retained	protocol suites in internal/httpapi/*mcp_test.go	transport_na
+POST /api/v1/pet/navigation/claim	server.go	claimOwnerPetNavigation	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/pet/reset	server.go	resetOwnerPet	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/pet/watches/{task}	server.go	followPetWatch	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/projects	server.go	createProject	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/projects/{id}/knowledge-space	server.go	retryProjectKnowledgeSpace	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/projects/{id}/knowledge/detach	server.go	detachProjectKnowledge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/projects/{id}/product-lines	server.go	createProductLine	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/prompts/templates/{id}/reset	server.go	resetPromptTemplate	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/providers	server.go	createProvider	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/providers/add-models	server.go	addModelsHandler	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/providers/detect-models	server.go	detectModelsHandler	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/providers/{id}/model-detection-jobs	server.go	createModelDetectionJob	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/providers/{id}/model-detection-jobs/{job}/cancel	server.go	cancelModelDetectionJob	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/providers/{id}/model-probes	server.go	probeProviderModel	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/rounds/{id}/interrupt	server.go	interruptRound	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/knowledge-vault/cutover-legacy	server.go	cutoverLegacyKnowledge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/knowledge-vault/import	server.go	importKnowledgeVault	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/knowledge-vault/init	server.go	initializeKnowledgeVault	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/knowledge-vault/migrate	server.go	migrateKnowledgeVault	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/knowledge-vault/rebuild	server.go	rebuildKnowledgeVaultIndex	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/proxy/profiles/{id}/activate	server.go	activateProxyProfile	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/proxy/profiles/{id}/nodes/{node_id}/test	server.go	testProxyNode	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/proxy/profiles/{id}/refresh	server.go	refreshProxyProfile	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/proxy/subscription	server.go	importProxySubscription	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/proxy/subscription/refresh	server.go	refreshProxySubscription	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/proxy/test	server.go	testProxySettings	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/sync/conflicts/{id}/resolve	server.go	resolveSyncConflict	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/settings/sync/run	server.go	runSync	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/skills	server.go	rejectRemovedSkillWrite	owner_session	Web also rejects this removed legacy mutation	owner session still cannot execute	use the vault Skill management operations instead	existing Skill removal tests; exact reference pending	web_prohibited_na
+POST /api/v1/system/data/cleanup	server.go	cleanupData	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/system/data/conversation/preview	server.go	previewConversationPurge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/system/data/conversation/purge	server.go	purgeConversation	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/system/data/hardware-io/preview	server.go	previewHardwareIOPurge	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/system/data/hardware-io/purge	server.go	purgeHardwareIO	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks	server.go	createTask	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/agents/{agent}/messages	server.go	submitAgentMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/agents/{agent}/session/compact	server.go	compactAgentSession	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/agents/{agent}/session/reset	server.go	resetAgentSession	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/attachments	server.go	uploadTaskAttachment	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/browser-grants	browser_handlers.go	createTaskBrowserGrant	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/browser-sessions	browser_handlers.go	createTaskBrowserGrant	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/channel-members:refresh	server.go	refreshTaskChannelMembers	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/channel-routes	server.go	bindTaskChannelRoute	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/complete	server.go	completeTask	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/desktop/actions	desktop_handlers.go	desktopAct	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/desktop/control	desktop_handlers.go	desktopControl	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/desktop/session	desktop_handlers.go	desktopOpen	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/desktop/stop	desktop_handlers.go	desktopStop	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/desktop/switch	desktop_handlers.go	desktopSwitch	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/hardware/{hardware}/connect	server.go	connectHardware	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/hardware/{hardware}/disconnect	server.go	disconnectHardware	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/hardware/{hardware}/host-key/trust	server.go	trustHardwareSSHHostKey	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/hardware/{hardware}/send	server.go	sendHardware	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/messages	server.go	submitMessage	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/permission-requests/{request}/resolve	server.go	resolveTaskPermissionRequest	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/reopen	server.go	reopenTask	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/start	server.go	startTask	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/tasks/{id}/takeover	server.go	takeoverTask	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/turns/{id}/interrupt	server.go	interruptTurn	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/workspaces	server.go	createWorkspace	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/workspaces/{id}/detect	server.go	detectWorkspaceHandler	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/workspaces/{id}/host-key/trust	server.go	trustWorkspaceSSHHostKey	owner_session	pending	pending	pending	pending	pending
+POST /api/v1/workspaces/{id}/takeover	server.go	takeoverWorkspace	owner_session	pending	pending	pending	pending	pending
+POST /d/{token}/confirm	mail_download_handlers.go	mailDownloadConfirm	transport_auth	pending	pending	pending	pending	pending
+PUT /api/channel-runtime/v1/instances/{instance_id}/health	server.go	channelRuntimeHealth	transport_auth	pending	pending	pending	pending	pending
+PUT /api/v1/agent/skills/{skill}	server.go	updateAgentSkill	agent_turn	pending	pending	pending	pending	pending
+PUT /api/v1/assistant	server.go	updateOwnerAssistant	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/channel-instances/{id}/credentials	server.go	updateChannelCredentials	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/channel-instances/{id}/knowledge-bindings	server.go	updateChannelKnowledgeBindings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/channel-instances/{id}/knowledge-bindings/group	server.go	updateChannelGroupKnowledgeBindings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/channel-instances/{id}/knowledge-policy	server.go	updateChannelKnowledgePolicy	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/hardware-resources/{id}	server.go	updateHardwareResource	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/knowledge-vault/git/remote	server.go	updateKnowledgeVaultGitRemote	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/knowledge-vault/skills/auto-attach	server.go	updateKnowledgeVaultSkillAutoAttach	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/knowledge/{id}	server.go	updateKnowledge	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/mail-accounts/{id}	server.go	updateMailAccount	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/mail-bindings/{id}	server.go	updateMailBinding	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/mail-templates/{id}	server.go	updateMailTemplate	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/models/{id}	server.go	updateModel	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/pet	server.go	updateOwnerPetState	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/pet/settings	server.go	updateOwnerPetSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/pet/workbench/core/{name}	server.go	savePetWorkbenchDocument	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/pet/workbench/worklog/{name}	server.go	savePetWorkbenchDocument	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/projects/{id}	server.go	updateProject	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/prompts/templates/{id}	server.go	updatePromptTemplate	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/providers/{id}	server.go	updateProvider	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/agent-api	server.go	updateAgentAPISettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/backend	server.go	updateBackendSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/general	server.go	updateGeneralSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/knowledge-review	server.go	updateKnowledgeReviewSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/knowledge-vault	server.go	updateKnowledgeVaultSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/network	server.go	updateNetworkSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/proxy	server.go	updateProxySettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/security	server.go	updateSecuritySettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/settings/sync	server.go	updateSyncSettings	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/skills/{id}	server.go	rejectRemovedSkillWrite	owner_session	Web also rejects this removed legacy mutation	owner session still cannot execute	use the vault Skill management operations instead	existing Skill removal tests; exact reference pending	web_prohibited_na
+PUT /api/v1/system/data/conversation/retention	server.go	updateConversationRetention	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/system/data/hardware-io/retention	server.go	updateHardwareIORetention	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/tasks/{id}/channel-contacts	server.go	updateTaskChannelContacts	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/tasks/{id}/hardware	server.go	updateTaskHardware	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/tasks/{id}/hardware-bindings	server.go	updateTaskHardwareBindings	owner_session	aha2-pet.aha_update_task_hardware	owner session OR pet create_tasks; manageable Task	binding identities retained; max 16; validate before disconnect	internal/app/agent_pet_domains_test.go:TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation	implemented
+PUT /api/v1/tasks/{id}/mcp-servers	server.go	setTaskMCPServers	owner_session	pending	pending	pending	pending	pending
+PUT /api/v1/workspaces/{id}	server.go	updateWorkspace	owner_session	pending	pending	pending	pending	pending
```
