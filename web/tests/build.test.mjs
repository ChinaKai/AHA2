import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

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
  assert.match(conversation, /messageCollapseChars = 900/);
  assert.match(helpers, /sessionStorage/);
  assert.match(helpers, /navigator\.clipboard/);
  assert.match(taskComposer, /composer-target-wrap/);
  assert.match(taskComposer, /conversation-filter-popover/);
  assert.match(taskComposer, /data-conversation-category/);
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
  assert.match(css, /overflow-wrap: anywhere/);
  assert.doesNotMatch(script, /data-task-action="delete"/);
  assert.match(script, /requestAnimationFrame\(runTaskClock\)/);
  assert.match(script, /composer-focused/);
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

test("knowledge workspace separates global areas from project settings", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderKnowledgeWorkspace} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const html = renderKnowledgeWorkspace({
    projects: [{id: "project-1", name: "AHA2", description: "", project_type: "git", knowledge_policy: "enabled", knowledge_revision: 3, updated_at: ""}],
    workspaces: [{id: "workspace-1", project_id: "project-1", name: "Native", locality: "local", transport: "native", root_path: "C:/repo", ssh_password_configured: false, health: "ready"}],
    knowledge: [{id: "knowledge-1", scope: "project", project_id: "project-1", type: "navigation", title: "Entry", body: "Read internal/app.", status: "verified", confidence: .9, revision: 2, helped_count: 1, stale_count: 0, created_at: "", updated_at: ""}],
    refreshData: async () => {}, render: () => {}, setMessage: () => {},
  });
  assert.match(html, /PROJECT KNOWLEDGE/);
  assert.match(html, /Workspace Bindings/);
  assert.match(html, /Agent Pull/);
  assert.match(html, /data-knowledge-area="global"/);
  assert.match(html, /data-knowledge-area="skills"/);
  assert.match(html, /data-knowledge-area="updates"/);
  assert.doesNotMatch(html, /data-knowledge-section="skills"/);
  assert.match(html, /data-knowledge-section="settings"/);
  assert.doesNotMatch(html, /graph-canvas|knowledge-graph/);
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
