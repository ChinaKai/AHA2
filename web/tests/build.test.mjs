import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

test("read-only task history opens on the latest page and lazily loads older rows", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(script, /detail\.task\.read_only\s*\?\s*\[\s*"chat",\s*"update",\s*"error"\s*\]/);
  assert.match(script, /agentConversation\(taskID,\s*"main",\s*\{\s*limit:\s*50,\s*categories\s*\}/);
  assert.match(script, /scrollConversationToBottom\s*=\s*true/);
  assert.match(script, /detail\.task\.read_only\)\s*closeEvents\(\)/);
  assert.match(script, /currentTop\s*<\s*previousTop\s*&&\s*currentTop\s*<=\s*80/);
  assert.match(script, /loadingOlderConversation/);
});

test("sidebar shows service version and live uptime", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(script, /class="system-meta"/);
  assert.match(script, /id="system-uptime"/);
  assert.match(script, /setInterval\(updateSystemUptime,\s*30_000\)/);
  assert.match(styles, /\.system-meta/);
});

test("built web contains responsive application", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  const runtimePicker = await readFile(resolve(root, "dist", "runtime_picker.js"), "utf8");
  const agents = await readFile(resolve(root, "dist", "task_agents.js"), "utf8");
  const taskComposer = await readFile(resolve(root, "dist", "task_composer.js"), "utf8");
  const taskTools = await readFile(resolve(root, "dist", "task_tools.js"), "utf8");
  const hardwarePanel = await readFile(resolve(root, "dist", "hardware_panel.js"), "utf8");
  const hardwareTerminal = await readFile(resolve(root, "dist", "hardware_terminal.js"), "utf8");
  const conversation = await readFile(resolve(root, "dist", "conversation_ui.js"), "utf8");
  const helpers = await readFile(resolve(root, "dist", "ui_helpers.js"), "utf8");
  const promptAdmin = await readFile(resolve(root, "dist", "prompt_admin.js"), "utf8");
  const proxySettings = await readFile(resolve(root, "dist", "proxy_settings.js"), "utf8");
  const syncSettings = await readFile(resolve(root, "dist", "sync_settings.js"), "utf8");
  const codexAccounts = await readFile(resolve(root, "dist", "codex_accounts.js"), "utf8");
  const knowledgeWorkspace = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  const index = await readFile(resolve(root, "dist", "index.html"), "utf8");
  assert.match(script, /api\.authStatus/);
  assert.match(script, /EventSource/);
  assert.match(script, /api\.agentConversation/);
  assert.match(script, /api\.agentMessage/);
  assert.match(script, /name="ssh_auth"/);
  assert.match(script, /name="ssh_password"/);
  assert.match(script, /name="clear_ssh_password"/);
  assert.match(script, /workspace-clear-secret/);
  assert.match(css, /\.hardware-clear-secret, \.workspace-clear-secret/);
  assert.match(script, /ssh_password_configured/);
  assert.match(script, /clear_ssh_password:\s*payload\.clear_ssh_password === "on"/);
  assert.match(script, /delete body\.clear_ssh_password/);
  assert.match(script, /data-conversation-category/);
  assert.match(agents, /agent-turn-card/);
  assert.match(agents, /AHA 系统路由/);
  assert.doesNotMatch(agents, /virtual-agent-batch/);
  assert.match(css, /\.aha-orchestration-card/);
  assert.match(script, /renderPromptAdmin/);
  assert.match(script, /renderKnowledgeWorkspace/);
  assert.match(script, /name="knowledge_policy"/);
  assert.match(agents, /name="knowledge_policy"/);
  assert.match(knowledgeWorkspace, /Workspace Bindings/);
  assert.match(knowledgeWorkspace, /Product Lines/);
  assert.match(knowledgeWorkspace, /SKILL\.md/);
  assert.match(knowledgeWorkspace, /skill-package-files/);
  assert.match(api, /knowledge\/proposals\/\$\{encodeURIComponent\(id\)\}\/approve/);
  assert.match(api, /knowledge\/proposals\/\$\{encodeURIComponent\(id\)\}\/reject/);
  assert.match(script, /knowledgeProposals/);
  assert.match(knowledgeWorkspace, /data-knowledge-proposal-approve/);
  assert.match(knowledgeWorkspace, /data-knowledge-proposal-reject/);
  assert.match(knowledgeWorkspace, /\\u76f8\\u5173\\u6587\\u6863/);
  assert.match(knowledgeWorkspace, /\\u8fd4\\u56de\\u77e5\\u8bc6\\u5e93\\u5165\\u53e3/);
  assert.match(knowledgeWorkspace, /data-knowledge-back/);
  assert.match(knowledgeWorkspace, /data-knowledge-toggle/);
  assert.match(knowledgeWorkspace, /data-knowledge-entry/);
  assert.match(knowledgeWorkspace, /project-navigation/);
  assert.match(css, /\.knowledge-entry-portals/);
  assert.match(css, /\.knowledge-workspace-layout[^\n]*grid-template-columns: 220px minmax/);
  assert.match(css, /\.knowledge-document-layout\.directory-open \.knowledge-reader \{ display: none; \}/);
  assert.match(css, /\.knowledge-document-layout\.reader-open \.knowledge-tree \{ display: none; \}/);
  assert.match(css, /\.skill-package-files > span \{ display: grid;/);
  assert.match(css, /\.skill-package-files code \{[^}]*overflow-wrap: anywhere;/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*?\.skill-grid \{ grid-template-columns: 1fr; \}/);
  assert.doesNotMatch(knowledgeWorkspace, /Source Path/);
  assert.doesNotMatch(knowledgeWorkspace, /knowledge graph/i);
  assert.match(script, /"prompts",\s*"bot"/);
  assert.match(script, /agent-config-form/);
  assert.match(taskComposer, /id="composer-agent"/);
  assert.match(taskTools, /data-task-tool/);
  assert.match(taskTools, /id="close-task-tool"/);
  assert.match(taskTools, /task-tool-panel/);
  assert.match(taskTools, /renderHardwarePanel/);
  assert.match(hardwarePanel, /hardware-config-form/);
  assert.match(hardwarePanel, /connectHardware/);
  assert.match(hardwarePanel, /sendHardware/);
  assert.match(hardwarePanel, /自定义\.\.\./);
  assert.match(hardwarePanel, /commonBaudrates/);
  assert.match(hardwarePanel, />SSH<\/option>/);
  assert.match(hardwarePanel, /name="ssh_auth"/);
  assert.match(hardwarePanel, /有密码时优先密码/);
  assert.match(hardwarePanel, /data-hardware-config="ssh"/);
  assert.match(hardwarePanel, /data-hardware-config="login"/);
  assert.match(hardwarePanel, /hardware-save-top/);
  assert.match(hardwarePanel, /aha2:hardware-draft:/);
  assert.match(hardwarePanel, /sessionStorage\.setItem/);
  assert.match(hardwarePanel, /password_configured:\s*group\.password_configured/);
  assert.match(hardwarePanel, /lastError/);
  assert.match(hardwarePanel, /stream\.lastError \|\| stream\.status\?\.error/);
  assert.match(hardwarePanel, /item\.direction === "rx"/);
  assert.doesNotMatch(hardwarePanel, /hardware-output|hardware-output-fallback|formatEvent/);
  assert.match(hardwarePanel, /sendEncoding:\s*"text"/);
  assert.match(hardwarePanel, /access:\s*"read_write"/);
  assert.match(hardwareTerminal, /addEventListener\("touchstart", focusTerminal/);
  assert.match(hardwareTerminal, /textarea\.inputMode = "text"/);
  assert.match(hardwareTerminal, /hardware-mobile-terminal-input/);
  assert.match(hardwareTerminal, /hardware-mobile-terminal-view/);
  assert.match(hardwareTerminal, /terminal\.buffer\.active/);
  assert.match(hardwareTerminal, /onFallbackSend/);
  assert.match(hardwareTerminal, /writeFallback/);
  assert.match(hardwarePanel, /bytesToBase64/);
  assert.match(hardwarePanel, /"base64"/);
  assert.match(hardwareTerminal, /replace\(\/\\r\\n\|\\r\|\\n\/g, "\\r"\)/);
  assert.match(css, /\.hardware-xterm \.xterm-rows > div/);
  assert.match(css, /\.hardware-xterm\.mobile-terminal-compat \.xterm/);
  assert.match(api, /hardware\/serial-ports/);
  assert.match(hardwareTerminal, /new WebSocket/);
  assert.match(hardwareTerminal, /window\.Terminal/);
  assert.match(hardwareTerminal, /data instanceof Blob/);
  assert.match(hardwareTerminal, /after:\s*String/);
  assert.match(hardwareTerminal, /TextEncoder/);
  assert.match(hardwareTerminal, /send\s*\(data/);
  assert.match(hardwareTerminal, /type:\s*"resize"/);
  assert.match(index, /vendor\/xterm\.js\?v=[a-f0-9]{12}/);
  assert.match(index, /vendor\/xterm\.css\?v=[a-f0-9]{12}/);
  assert.match(hardwarePanel, /data-hardware-terminal-key="ctrl-c"/);
  assert.match(hardwarePanel, /const sendForm = root\.querySelector/);
  assert.doesNotMatch(script, /data-task-tab=|mobile-agent-strip|conversation-filters/);
  assert.match(taskTools, /renderTaskMemory\(detail\.memory\)/);
  assert.doesNotMatch(script, /data-realtime-state class=/);
  assert.match(script, /task-head-subline/);
  assert.match(taskTools, /iconName: "hardware"/);
  assert.match(taskTools, /iconName: "browser"/);
  assert.match(script, /state\.taskTool === tool/);
  assert.doesNotMatch(script, /<h3>Task Memory<\/h3>.*Context Evidence/s);
  assert.match(script, /<details class="prompt-snapshot" open>/);
  assert.match(script, /refreshTaskRuntime/);
  assert.match(script, /startTaskFallback/);
  assert.match(agents, /data-live-elapsed-ms/);
  assert.match(agents, /ms > 0 && ms < 1000/);
  assert.match(agents, /metrics\.session_id/);
  assert.match(agents, /class="session-id"/);
  assert.match(agents, /function renderTaskMemory/);
  assert.match(conversation, /data-copy-message/);
  assert.match(conversation, /data-toggle-message/);
  assert.match(conversation, /data-image-preview/);
  assert.match(helpers, /showModal\(\)/);
  assert.match(helpers, /data-image-preview-download/);
  assert.match(css, /\.message-image img \{[^}]*width: auto;[^}]*height: auto;/);
  assert.match(css, /\.image-preview-dialog/);
  assert.match(conversation, /messageCollapseChars = 900/);
  assert.match(helpers, /sessionStorage/);
  assert.match(helpers, /navigator\.clipboard/);
  assert.match(taskComposer, /composer-target-wrap/);
  assert.match(taskComposer, /conversation-filter-popover/);
  assert.match(taskComposer, /data-conversation-category/);
  assert.match(script, /taskCategories:\s*\{[\s\S]{0,200}tool:\s*false/);
  assert.match(script, /消息已排队/);
  assert.match(promptAdmin, /prompt-template-layout/);
  assert.match(script, /renderCodexAccounts/);
  assert.match(script, /api\.codexAccounts/);
  assert.match(codexAccounts, /startCodexLogin/);
  assert.match(codexAccounts, /codex-login-proxy/);
  assert.match(codexAccounts, /OAuth Token/);
  assert.match(codexAccounts, /refreshCodexAccount/);
  assert.doesNotMatch(codexAccounts, /data-codex-model|official-model-dialog|添加官方 Codex 模型/);
  assert.doesNotMatch(api, /codex-accounts\/.*\/models/);
  assert.match(runtimePicker, /模型使用方式/);
  assert.match(runtimePicker, />Env</);
  assert.match(runtimePicker, />Official</);
  assert.match(runtimePicker, /Codex 账号/);
  assert.match(runtimePicker, /available_models/);
  assert.match(script, /model_source/);
  assert.match(script, /codex_account_id/);
  assert.match(script, /wire_model/);
  assert.match(api, /codex-accounts\/.*\/refresh/);
  assert.match(codexAccounts, /submitCodexCallback/);
  assert.match(codexAccounts, /Callback URL/);
  assert.match(codexAccounts, /正在校验 Callback 并添加账号/);
  assert.match(codexAccounts, /onError\?\.\(message\)/);
  assert.match(codexAccounts, /limit\.id === "codex"/);
  assert.match(codexAccounts, /return "月额度"/);
  assert.match(codexAccounts, /周额度/);
  assert.match(codexAccounts, /重置券/);
  assert.doesNotMatch(codexAccounts, /codex-quota-grid|flatMap\(limit/);
  assert.match(css, /\.codex-limit-row/);
  assert.match(css, /\.provider-sidebar/);
  assert.doesNotMatch(codexAccounts, /device-auth|setInterval/);
  assert.doesNotMatch(promptAdmin, /prompt-route-list|Effective Prompt|Context Manifest/);
  assert.doesNotMatch(api, /prompts\/routes|prompts\/preview|context\/resources/);
  assert.match(script, /shell\(renderPromptAdmin\(\)\)/);
  assert.match(script, /shell\(renderProxySettings\(state\.proxySettings\)\)/);
  assert.match(script, /name=\\?"proxy_enabled/);
  assert.match(agents, /name="proxy_enabled"/);
  assert.match(proxySettings, /HTTP_PROXY/);
  assert.match(proxySettings, /testProxySettings/);
  assert.match(script, /renderSyncSettings/);
  assert.match(syncSettings, /token_configured/);
  assert.match(syncSettings, /runSync/);
  assert.doesNotMatch(syncSettings, /value="\$\{[^}]*token/);
  assert.match(script, /heartbeat/);
  assert.doesNotMatch(css, /\.conversation-event/);
  assert.match(css, /\.message\.agent-tool-message/);
  assert.match(css, /\.task-tool-actions/);
  assert.match(css, /\.agent-turn-row/);
  assert.match(css, /\.ctx-view/);
  assert.match(css, /\.task-tool-panel\.open/);
  assert.match(css, /\.task-tool-panel\s*\{[^}]*inset:\s*0;[^}]*width:\s*100%;[^}]*height:\s*100%;/s);
  assert.doesNotMatch(css, /\.task-tool-panel\s*\{[^}]*position:\s*fixed;/s);
  assert.doesNotMatch(css, /\.task-tool-panel \.ctx-token-grid/);
  assert.match(css, /\.task-memory-tool\s*\{[^}]*width:\s*min\(960px,100%\);[^}]*margin:\s*0 auto;/s);
  assert.match(css, /@media \(max-width: 760px\)/);
  assert.match(css, /\.mobile-header \{ display: none; \}/);
  assert.match(script, /taskDraft/);
  assert.match(script, /taskRuntimeSignature/);
  assert.match(script, /flushDeferredRender/);
  assert.match(script, /document\.activeElement instanceof HTMLTextAreaElement/);
  assert.match(script, /updateTaskLiveRegions/);
  assert.match(script, /captureRegionUI/);
  assert.match(script, /replaceRegionHTML/);
  assert.match(script, /previousWindowScroll/);
  assert.match(script, /agent-turn-slot/);
  assert.doesNotMatch(script, /task-action-menu/);
  assert.match(script, /slash-command-menu/);
  assert.match(script, /executeTaskSlashCommand/);
  assert.match(script, /\/interrupt/);
  assert.match(script, /\/complete/);
  assert.match(script, /\/reopen/);
  assert.match(script, /taskFailureBannerHtml/);
  assert.match(script, /formatTokenCount\(task\.total_tokens\)/);
  assert.match(script, /Intl\.NumberFormat\("en-US"\)/);
  assert.match(agents, /context_inconsistent/);
  assert.match(script, /id="task-isolation"/);
  assert.match(script, /id="task-worktree-settings"/);
  assert.match(script, /syncTaskGitIsolation/);
  assert.doesNotMatch(script, /id="ws-isolation"|id="ws-worktree-dir"/);
  assert.match(conversation, /agent-update-message/);
  assert.match(conversation, /agent-tool-message/);
  assert.match(conversation, /agent-routed-message/);
  assert.doesNotMatch(conversation, /conversation-event/);
  assert.match(conversation, /visibleAgentText/);
  assert.doesNotMatch(agents, /aha2_checkpoint/);
  assert.match(agents, /Session 唤醒/);
  assert.match(agents, /Backend 启动/);
  assert.doesNotMatch(agents, /Turn Cache/);
  assert.match(agents, /累计 Cache/);
  assert.match(agents, /cap_workspace_read/);
  assert.match(agents, /cap_task_create/);
  assert.match(agents, /cap_clone_hardware/);
  assert.match(css, /\.task-agent-capabilities/);
  assert.match(css, /overflow-wrap: anywhere/);
  assert.doesNotMatch(script, /data-task-action="delete"/);
  assert.match(script, /requestAnimationFrame\(runTaskClock\)/);
  assert.match(script, /composer-focused/);
  assert.match(css, /--mobile-bottom-nav-height:\s*calc\(62px \+ env\(safe-area-inset-bottom\)\)/);
  assert.match(css, /\.task-screen \{[^}]*height:\s*calc\(var\(--visual-viewport-height\) - var\(--mobile-bottom-nav-height\)\)/);
  assert.match(css, /\.bottom-nav \{[^}]*height:\s*var\(--mobile-bottom-nav-height\)/);
  assert.match(script, /class="banner-stack"/);
  assert.match(css, /body\.task-view-active \.banner-stack \{[^}]*position:\s*fixed/);
  assert.match(script, /isSyncSettingsFormEditing/);
  assert.match(script, /state\.view === "sync"[\s\S]{0,250}state\.renderPending = true/);
  assert.match(script, /visualViewport/);
  assert.match(script, /task-view-active/);
  assert.match(api, /cache: "no-store"/);
  assert.match(script, /addEventListener\("heartbeat"/);
  assert.match(css, /body\.composer-focused \.composer-target-wrap/);
  assert.match(css, /body\.task-view-active/);
  assert.match(css, /overscroll-behavior: contain/);
  assert.match(index, /app\.js\?v=[a-f0-9]{12}/);
  assert.match(index, /styles\.css\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_agents\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_composer\.js\?v=[a-f0-9]{12}/);
  assert.match(taskComposer, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_tools\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/task_agents\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/hardware_panel\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/hardware_panel\.js\?v=[a-f0-9]{12}/);
  assert.match(hardwarePanel, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(hardwarePanel, /\.\/hardware_terminal\.js\?v=[a-f0-9]{12}/);
  assert.match(agents, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/conversation_ui\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/ui_helpers\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/prompt_admin\.js\?v=[a-f0-9]{12}/);
  assert.match(promptAdmin, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/codex_accounts\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/runtime_picker\.js\?v=[a-f0-9]{12}/);
  assert.match(codexAccounts, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(codexAccounts, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(agents, /\.\/runtime_picker\.js\?v=[a-f0-9]{12}/);
  assert.match(codexAccounts, /button\.innerHTML = icon\("spinner", true\)/);
  assert.match(codexAccounts, /refreshAccount\(id, true\)[\s\S]*undefined, true/);
});

test("knowledge workspace defaults to actionable updates", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderKnowledgeWorkspace} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const html = renderKnowledgeWorkspace({
    projects: [{id: "project-1", name: "AHA2", description: "", project_type: "git", knowledge_policy: "enabled", knowledge_revision: 3, updated_at: ""}],
    workspaces: [{id: "workspace-1", project_id: "project-1", name: "Native", locality: "local", transport: "native", root_path: "C:/repo", ssh_password_configured: false, health: "ready"}],
    knowledge: [
      {id: "knowledge-1", scope: "project", project_id: "project-1", type: "practice", title: "Current title", body: "Keep this line.\nOld detail.", status: "stale", confidence: .9, revision: 2, helped_count: 1, stale_count: 0, created_at: "", updated_at: ""},
      {id: "knowledge-stale", scope: "project", project_id: "project-1", type: "practice", title: "Published stale", body: "Current content.", status: "stale", confidence: .9, revision: 3, helped_count: 0, stale_count: 1, created_at: "", updated_at: ""},
      {id: "knowledge-legacy", scope: "global", type: "diagnostic", title: "Legacy candidate", body: "Created before proposal migration.", status: "candidate", confidence: .9, revision: 1, helped_count: 0, stale_count: 0, created_at: "", updated_at: ""},
    ],
    proposals: [
      {id: "proposal-new", entry_id: "knowledge-new", base_revision: 0, status: "pending", source_task_id: "task-new", source_turn_id: "turn-new", created_at: "2026-09-06T01:00:00Z", proposed: {id: "knowledge-new", scope: "project", project_id: "project-1", sort_order: 0, is_index: false, type: "practice", title: "Brand new knowledge", body: "**Complete preview**\n\nAll proposed details.", status: "candidate", confidence: .9, revision: 1, helped_count: 0, stale_count: 0, created_at: "", updated_at: ""}},
      {id: "proposal-revision", entry_id: "knowledge-1", base_revision: 2, scope: "project", project_id: "project-1", type: "practice", title: "Updated title", body: "Keep this line.\nNew detail.", base_title: "Current title", base_body: "Keep this line.\nOld detail.", status: "pending", source_task_id: "task-revision", source_turn_id: "turn-revision", created_at: "2026-09-06T02:00:00Z"},
    ],
    refreshData: async () => {}, render: () => {}, setMessage: () => {},
  });
  assert.match(html, /data-knowledge-area="updates" class="active"/);
  assert.match(html, /data-knowledge-update-filter="pending" class="active"/);
  assert.match(html, /data-knowledge-update-filter="all"/);
  assert.match(html, /待处理 <b>3<\/b>/);
  assert.match(html, /data-knowledge-proposal-view="proposal-new"/);
  assert.match(html, /data-knowledge-proposal-approve="proposal-new"/);
  assert.match(html, /data-knowledge-proposal-reject="proposal-new"/);
  assert.match(html, /data-knowledge-proposal-view="proposal-revision"/);
  assert.match(html, /data-knowledge-proposal-approve="proposal-revision"/);
  assert.match(html, /data-knowledge-proposal-reject="proposal-revision"/);
  assert.match(html, /<strong>Complete preview<\/strong>/);
  assert.match(html, /class="remove"[\s\S]*Old detail\./);
  assert.match(html, /class="add"[\s\S]*New detail\./);
  assert.match(html, /<del>Current title<\/del>[\s\S]*<ins>Updated title<\/ins>/);
  assert.doesNotMatch(html, /data-knowledge-edit=/);
  assert.match(html, /data-knowledge-update-open="knowledge-legacy"/);
  assert.match(html, /data-knowledge-verify="knowledge-legacy"/);
  assert.match(html, /data-knowledge-delete="knowledge-legacy"/);
  assert.match(html, /data-knowledge-verify="knowledge-stale"/);
  assert.doesNotMatch(html, /data-knowledge-verify="knowledge-1"/);
  assert.match(html, /确认仍有效/);
  assert.match(html, /等待 Agent 修订/);
  assert.doesNotMatch(html, /data-knowledge-entry="global"/);
  assert.match(html, /data-knowledge-area="global"/);
  assert.match(html, /data-knowledge-area="skills"/);
  assert.match(html, /data-knowledge-area="updates"/);
  assert.doesNotMatch(html, /knowledge-section-rail|data-knowledge-section=/);
  assert.doesNotMatch(html, /data-knowledge-back/);
  assert.doesNotMatch(html, /graph-canvas|knowledge-graph/);
  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  assert.match(source, /portal\("project"/);
  assert.match(source, /portal\("navigation"/);
  assert.match(source, /portal\("settings"/);
});

test("task agent config selects skills explicitly", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderAgentConfigDialog} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
  const html = renderAgentConfigDialog({
    task: {id: "task-1", project_id: "project-1", workspace_id: "workspace-1", title: "Task", original_request: "", current_goal: "", status: "active", isolation: "inplace", collaboration_mode: "auto", max_agents: 3, knowledge_policy: "inherit", skill_ids: ["skill-1"], total_tokens: 0, created_at: "", updated_at: ""},
    agents: [{agent_id: "main", role: "main", title: "Main", backend: "codex", model_source: "env", model_id: "model-1", reasoning_effort: "high", filesystem: "workspace-write", approval: "never", proxy_enabled: false}],
    turns: [], memory: {task_id: "task-1", current_goal: ""},
  }, "main", [], [], [{id: "skill-1", scope: "global", project_id: "", name: "Review", description: "Review changes", instructions: "Review", version: 1, status: "active", enabled: true, source_path: "", created_at: "", updated_at: ""}]);
  assert.match(html, /Task Skills/);
  assert.match(html, /name="skill_ids" value="skill-1" checked/);
});

test("round card reports uncached turn input and hides ambiguous cache usage", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderAgentTurnCard} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
  const html = renderAgentTurnCard({
    task: {id: "task-1", project_id: "project-1", workspace_id: "workspace-1", title: "Task", original_request: "", current_goal: "", status: "active", isolation: "inplace", collaboration_mode: "single", max_agents: 1, knowledge_policy: "inherit", skill_ids: [], total_tokens: 0, created_at: "", updated_at: ""},
    latest_round: {id: "round-1", task_id: "task-1", sequence: 2, status: "running", created_at: ""},
    agents: [],
    turns: [{
      id: "turn-1", task_id: "task-1", round_id: "round-1", agent_id: "main", sequence: 2,
      generation: 1, attempt: 1, status: "running", instruction: "", prompt_chars: 0,
      context_window: 258400, usage: {input_tokens: 1200, cached_input_tokens: 800, context_tokens: 128592},
      created_at: "",
    }],
    event_cursor: 0,
    server_time_ms: 0,
  }, "live", {input_tokens: 2500, output_tokens: 75});
  assert.match(html, /<small>Input<\/small><strong>2\.5K<\/strong>/);
  assert.match(html, /<small>Output<\/small><strong>75<\/strong>/);
  assert.match(html, /<small>Context<\/small><strong>128\.6K \/ 258\.4K<\/strong>/);
  assert.doesNotMatch(html, /<small>Turn Cache<\/small>/);
  assert.doesNotMatch(html, /<small>Input<\/small><strong>128\.6K<\/strong>/);
});

test("codex accounts render compact weekly quota", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderCodexAccounts} = await import(pathToFileURL(resolve(root, "dist", "codex_accounts.js")));
  const html = renderCodexAccounts([{
    id: "account-1",
    label: "工作账号",
    email: "user@example.com",
    plan_type: "pro",
    status: "ready",
    proxy_enabled: true,
    credential_configured: true,
    usage_updated_at: "2026-09-04T09:00:00Z",
    usage: {
      rate_limits: [{
        id: "codex",
        name: "Codex",
        allowed: true,
        limit_reached: false,
        primary_window: {used_percent: 13, limit_window_seconds: 604800, reset_at: 1789099897},
      }, {
        id: "spark",
        name: "GPT-5.3-Codex-Spark",
        allowed: true,
        limit_reached: false,
        primary_window: {used_percent: 0, limit_window_seconds: 18000, reset_at: 1789099897},
      }],
      credits: {has_credits: false, unlimited: false, overage_limit_reached: false},
      reset_credits_available: 1,
    },
    created_at: "2026-09-04T09:00:00Z",
    updated_at: "2026-09-04T09:00:00Z",
  }]);
  assert.match(html, /user@example\.com/);
  assert.match(html, /重置券 1/);
  assert.match(html, /周额度/);
  assert.match(html, /87% 剩余/);
  assert.match(html, /重置/);
  assert.doesNotMatch(html, /GPT-5\.3-Codex-Spark/);
  assert.doesNotMatch(html, /codex-quota-item/);

  const monthly = renderCodexAccounts([{
    id: "account-2", label: "", email: "free@example.com", plan_type: "free", status: "ready",
    proxy_enabled: true, credential_configured: true,
    usage: {
      rate_limits: [{id: "codex", name: "Codex", allowed: true, limit_reached: false,
        primary_window: {used_percent: 0, limit_window_seconds: 2592000, reset_at: 1791106835}}],
      credits: {has_credits: false, unlimited: false, overage_limit_reached: false},
    },
    created_at: "2026-09-04T09:00:00Z", updated_at: "2026-09-04T09:00:00Z",
  }]);
  assert.match(monthly, /月额度/);
  assert.match(monthly, /100% 剩余/);
  assert.doesNotMatch(monthly, /暂不可用/);
});

test("runtime picker separates Env and Official Codex models", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {runtimeFieldsHTML} = await import(pathToFileURL(resolve(root, "dist", "runtime_picker.js")));
  const html = runtimeFieldsHTML("task", [{
    id: "model-env", display_name: "Env Model", provider_id: "gateway", provider_name: "Gateway A", source: "provider",
    backend: "codex", wire_model: "env-model", created_at: "", updated_at: "",
  }], [{
    id: "account-1", label: "Work", status: "ready", proxy_enabled: true,
    credential_configured: true, available_models: [{wire_model: "gpt-5.6-sol", display_name: "GPT-5.6-Sol"}],
    usage: {rate_limits: [{id: "codex", name: "Codex", allowed: true, limit_reached: false,
      primary_window: {used_percent: 13, limit_window_seconds: 604800}}],
      credits: {has_credits: false, unlimited: false, overage_limit_reached: false}},
    created_at: "", updated_at: "",
  }], {
    backend: "codex", model_source: "official", codex_account_id: "account-1", wire_model: "gpt-5.6-sol",
  });
  assert.match(html, /name="model_source"/);
  assert.match(html, /value="env"/);
  assert.match(html, /value="official" selected/);
  assert.match(html, /name="codex_account_id"/);
  assert.match(html, /value="account-1" selected/);
  assert.match(html, /name="wire_model"/);
  assert.match(html, /value="gpt-5\.6-sol" selected/);
  assert.match(html, /Env Model/);
  assert.match(html, /<optgroup label="Gateway A">/);
  assert.match(html, /Work · 周额度已用 13%/);
});

test("conversation renders agent config and turn duration cards", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const items = [{
    sequence: 1, id: "config-1", task_id: "task-1", agent_id: "aha", stream_agent_id: "main",
    category: "update", kind: "agent_config_updated", summary: "main 配置已更新",
    payload: {backend: "codex", model_source: "env", model_name: "GPT-5.6", reasoning_effort: "high",
      filesystem: "workspace-write", approval: "never", proxy_enabled: true},
    created_at: "2026-09-04T09:00:00Z",
  }, {
    sequence: 2, id: "duration-1", task_id: "task-1", agent_id: "aha", stream_agent_id: "main",
    category: "update", kind: "turn_duration", summary: "Turn 1 已结束",
    payload: {turn_sequence: 1, status: "succeeded", elapsed_ms: 5200, queue_duration_ms: 100,
      prepare_duration_ms: 600, run_duration_ms: 4500},
    created_at: "2026-09-04T09:00:05Z",
  }];
  const html = renderConversationList(items, null, false);
  assert.match(html, /Agent 配置更新/);
  assert.match(html, /GPT-5\.6/);
  assert.match(html, /Turn 1 耗时/);
  assert.match(html, /5s/);
});

test("conversation replaces irrecoverable legacy progress mojibake", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const html = renderConversationList([{
    id: "broken-update", task_id: "task-1", agent_id: "main", category: "update", kind: "agent_progress",
    summary: "???????????? v0.4.3 ??????", created_at: "2026-09-06T09:00:00Z",
  }], null, false);
  assert.match(html, /该历史进度消息在旧版中发生编码损坏，原文无法恢复/);
  assert.doesNotMatch(html, /\?{6,}/);
});

test("hardware terminal accepts WebView Blob binary frames", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {terminalFrameBytes} = await import(pathToFileURL(resolve(root, "dist", "hardware_terminal.js")));
  const expected = new Uint8Array([0x1b, 0x5b, 0x33, 0x32, 0x6d]);
  assert.deepEqual(await terminalFrameBytes(expected.buffer), expected);
  assert.deepEqual(await terminalFrameBytes(new Blob([expected])), expected);
});

test("message collapse uses Unicode character count", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const item = summary => ({
    id: "message-1",
    task_id: "task-1",
    agent_id: "main",
    stream_agent_id: "main",
    category: "chat",
    kind: "agent_message",
    summary,
    created_at: "2026-09-03T00:00:00Z",
  });
  const manyLines = Array.from({length: 400}, () => "x").join("\n");
  assert.doesNotMatch(renderConversationList([item(manyLines)], null, false), /data-toggle-message/);
  assert.doesNotMatch(renderConversationList([item("😀".repeat(500))], null, false), /data-toggle-message/);
  assert.match(renderConversationList([item("字".repeat(901))], null, false), /data-toggle-message/);
});

test("tool events render as message bubbles", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const html = renderConversationList([{
    sequence: 1,
    id: "tool-1",
    task_id: "task-1",
    agent_id: "main",
    stream_agent_id: "main",
    category: "tool",
    kind: "agent_command_finished",
    summary: "go test ./...",
    payload: {output_tail: "ok"},
    created_at: "2026-09-03T00:00:00Z",
  }], null, false);
  assert.match(html, /class="message agent\s+agent-tool-message/);
  assert.match(html, />工具</);
  assert.match(html, /message-output/);
  assert.match(html, /\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/);
  assert.doesNotMatch(html, /conversation-event/);
});

test("hardware SSH requires explicit host fingerprint trust", async () => {
  const root = resolve(import.meta.dirname, "..");
  const panel = await readFile(resolve(root, "dist", "hardware_panel.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(panel, /ssh_host_key_unknown/);
  assert.match(panel, /SHA256 指纹/);
  assert.match(panel, /window\.confirm/);
  assert.match(panel, /trustHardwareHostKey/);
  assert.match(api, /host-key\/trust/);
});

test("conversation renders uploaded images and downloadable files", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const html = renderConversationList([{
    id: "message-attachment", task_id: "task-1", agent_id: "owner", category: "chat", kind: "user_message",
    summary: "See attachments", created_at: "2026-09-05T00:00:00Z",
    payload: {attachments: [
      {id: "image-1", name: "screen.png", media_type: "image/png", size: 1024},
      {id: "file-1", name: "notes.txt", media_type: "text/plain", size: 200},
    ]},
  }], null, false);
  assert.match(html, /class="message-image"/);
  assert.doesNotMatch(html, /<a class="message-image"/);
  assert.match(html, /data-image-preview="\/api\/v1\/tasks\/task-1\/attachments\/image-1"/);
  assert.match(html, /tasks\/task-1\/attachments\/image-1/);
  assert.match(html, /id="image-preview-dialog"/);
  assert.match(html, /data-image-preview-download/);
  assert.match(html, /class="message-file"/);
  assert.match(html, /notes\.txt/);
});

test("knowledge workspace renders one document with a clickable hierarchy", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderKnowledgeDocumentWorkspace} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const base = {scope: "global", status: "verified", confidence: .9, revision: 1, helped_count: 0, stale_count: 0, created_at: "", updated_at: "", sort_order: 0};
  const html = renderKnowledgeDocumentWorkspace([
      {...base, id: "root", is_index: true, type: "navigation", title: "Global index", slug: "index", body: "# Entry\nChoose a child."},
      {...base, id: "child", parent_id: "root", is_index: false, type: "practice", title: "Core architecture", slug: "core", body: "Child body"},
      {...base, id: "grandchild", parent_id: "child", is_index: false, type: "practice", title: "Storage", slug: "storage", body: "Grandchild body"},
    ], "No global knowledge.", "global");
  assert.match(html, /knowledge-document-layout/);
  assert.match(html, /directory-open/);
  assert.doesNotMatch(html, /data-knowledge-back/);
  assert.match(html, /data-knowledge-open="root"/);
  assert.doesNotMatch(html, /data-knowledge-delete="root"/);
  assert.match(html, /data-knowledge-open="child"/);
  assert.match(html, /data-knowledge-open="grandchild"/);
  assert.match(html, /data-knowledge-toggle="child"/);
  assert.match(html, /--knowledge-indent:36px/);
  assert.match(html, />全局知识<\/button>/);
  assert.match(html, /<small>分类<\/small>/);
  assert.match(html, /<small>文档<\/small>/);
  assert.match(html, /<h1>Entry<\/h1>/);
  assert.match(html, /子文档/);
  assert.match(html, />新建文档<\/button>/);
  assert.doesNotMatch(html, />index(?:\.md)?</i);
  assert.doesNotMatch(html, />navigation</i);
  assert.doesNotMatch(html, />practice</i);
  assert.doesNotMatch(html, />verified</i);
  assert.doesNotMatch(html, />revision/i);
});

test("project settings is a secondary page with a project entry back action", async () => {
  const root = resolve(import.meta.dirname, "..");
  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  assert.match(source, /function renderSettings[\s\S]*?data-knowledge-back[\s\S]*?knowledge-setting-card/);
  assert.match(source, /knowledgeReaderOpen\s*&&\s*projectMode\s*&&\s*activeSection\s*!==\s*"overview"/);
  assert.match(source, /activeSection\s*=\s*entry\s*===\s*"settings"\s*\?\s*"settings"/);
});

test("stale knowledge action means keeping the current content", async () => {
  const root = resolve(import.meta.dirname, "..");
  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.ok(source.includes("\\u786e\\u8ba4\\u4ecd\\u6709\\u6548"));
  assert.ok(!source.includes("\\u91cd\\u65b0\\u786e\\u8ba4"));
  assert.match(styles, /grid-template-columns:\s*repeat\(3,minmax\(0,1fr\)\)/);
});

test("knowledge proposals keep three approval actions on one mobile row", async () => {
  const root = resolve(import.meta.dirname, "..");
  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  for (const marker of ["data-knowledge-proposal-view", "data-knowledge-proposal-approve", "data-knowledge-proposal-reject"]) assert.match(source, new RegExp(marker));
  for (const label of ["\\u67e5\\u770b\\u5dee\\u5f02", "\\u6279\\u51c6\\u66f4\\u65b0", "\\u62d2\\u7edd"]) assert.ok(source.includes(label));
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.knowledge-proposal-actions \{[^}]*grid-template-columns:\s*repeat\(3,minmax\(0,1fr\)\)/);
  assert.match(styles, /\.knowledge-update-feed article \{[^}]*min-width:\s*0;[^}]*max-width:\s*100%;[^}]*overflow:\s*hidden/);
  assert.match(styles, /\.knowledge-proposal-full \.markdown-body \{[^}]*min-width:\s*0;[^}]*max-width:\s*100%;[^}]*overflow-wrap:\s*anywhere/);
});

test("sync settings use the five domain groups", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {bindSyncSettings, isSyncSettingsFormEditing, renderSyncSettings, syncPreviewMessage, syncSettingsPayload} = await import(pathToFileURL(resolve(root, "dist", "sync_settings.js")));
  const html = renderSyncSettings({scope: "default", enabled: false, endpoint: "", device_id: "", device_name: "device", interval_seconds: 300, token_configured: false, passphrase_configured: false}, {scope: "default", cursor: "", last_error: ""}, 0, [], {upserts: 3, deletes: 2, remote_upserts: 4, remote_deletes: 1, pending: 0, conflicts: 0}, {running: true, phase: "pulling", completed: 5, total: 10});
  assert.match(html, /sync-domain-grid/);
  for (const label of ["项目", "任务", "知识库", "模型", "代理"]) assert.match(html, new RegExp(label));
  assert.ok(html.indexOf("项目") < html.indexOf("任务") && html.indexOf("任务") < html.indexOf("知识库"));

  const values = new Map([["enabled", "on"], ["endpoint", "https://sync.example.com"], ["device_name", "Laptop"], ["interval_seconds", "60"], ["registration_code", "one-time-code"], ["passphrase", "long-passphrase"]]);
  const payload = syncSettingsPayload(
    {scope: "default", enabled: false, endpoint: "", device_id: "dev_existing", device_name: "", interval_seconds: 300, token_configured: true, passphrase_configured: true},
    {get: name => values.get(name) ?? null, getAll: () => []},
  );
  assert.deepEqual(payload, {enabled: true, endpoint: "https://sync.example.com", device_id: "dev_existing", device_name: "Laptop", interval_seconds: 60, registration_code: "one-time-code", passphrase: "long-passphrase", clear_passphrase: false});
  assert.doesNotMatch(html, /provider_ids|env_group_ids|codex_account_ids/);
  assert.match(html, /远端待更新[\s\S]*?<strong>4<\/strong>/);
  assert.match(html, /data-sync-progress[\s\S]*?value="5"/);
  assert.match(syncPreviewMessage({upserts: 3, deletes: 2, remote_upserts: 4, remote_deletes: 1, pending: 1, conflicts: 4}), /远端将更新本机 4 项、删除 1 项/);
  assert.equal(syncPreviewMessage({upserts: 3, deletes: 2, remote_upserts: 4, remote_deletes: 1, pending: 0, conflicts: 1}, true), "同步完成：上传 3，本机删除 2，拉取更新 4，远端删除 1，剩余 0，冲突 1");

  const editingForm = {dataset: {syncDirty: "true"}, contains: node => node === "sync-field"};
  assert.equal(isSyncSettingsFormEditing(editingForm, null), true);
  editingForm.dataset.syncDirty = "false";
  assert.equal(isSyncSettingsFormEditing(editingForm, "sync-field"), true);
  assert.equal(isSyncSettingsFormEditing(editingForm, "outside"), false);

  const handlers = {};
  let saved;
  let refreshed = false;
  const button = {disabled: false};
  const formElement = {dataset: {}, contains: () => false, addEventListener: (type, handler) => { handlers[type] = handler; }, querySelector: () => button};
  const originalDocument = globalThis.document;
  const OriginalFormData = globalThis.FormData;
  globalThis.document = {querySelector: selector => selector === "#sync-settings-form" ? formElement : null};
  globalThis.FormData = class { get(name) { return values.get(name) ?? null; } getAll(name) { return selections.get(name) ?? []; } };
  try {
    bindSyncSettings({
      settings: {scope: "default", enabled: false, endpoint: "", device_id: "dev_bound", device_name: "", interval_seconds: 300, token_configured: true, passphrase_configured: true},
      updateSettings: async value => { saved = value; },
      refresh: async () => { refreshed = true; },
      setMessage: () => {},
    });
    handlers.input();
    assert.equal(formElement.dataset.syncDirty, "true");
    handlers.submit({preventDefault: () => {}, currentTarget: formElement});
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(saved.device_id, "dev_bound");
    assert.equal(refreshed, true);
    assert.equal(button.disabled, false);
    assert.equal(formElement.dataset.syncDirty, "false");
  } finally {
    if (originalDocument === undefined) delete globalThis.document; else globalThis.document = originalDocument;
    globalThis.FormData = OriginalFormData;
  }
});

test("remote workspace and task mirrors expose explicit local takeover", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  for (const marker of ["data-takeover-workspace", "workspace-takeover-form", "task-takeover-form", "reuse_remote_credential"]) {
    assert.match(main, new RegExp(marker));
  }
  assert.match(main, /本机 Root Path/);
  assert.match(main, /本机硬件重绑定/);
  assert.match(api, /workspaces\/.*\/takeover/);
  assert.match(api, /tasks\/.*\/takeover/);
});
