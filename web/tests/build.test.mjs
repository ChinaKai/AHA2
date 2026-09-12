import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

test("interactive region state restores scroll, focus, controls, and expansion", async () => {
  const previous = {
    HTMLElement: globalThis.HTMLElement,
    document: globalThis.document,
  };
  class FakeClassList {
    constructor(names = []) { this.names = new Set(names); }
    contains(name) { return this.names.has(name); }
    add(name) { this.names.add(name); }
  }
  class FakeElement {
    constructor(tagName, options = {}) {
      this.tagName = tagName.toUpperCase();
      this.id = options.id || "";
      this.dataset = {...(options.dataset || {})};
      this.classList = new FakeClassList(options.classes || []);
      this.parentElement = null;
      this.children = [];
      this.scrollTop = options.scrollTop || 0;
      this.scrollLeft = options.scrollLeft || 0;
      this.scrollHeight = options.scrollHeight || 0;
      this.scrollWidth = options.scrollWidth || 0;
      this.clientHeight = options.clientHeight || 0;
      this.clientWidth = options.clientWidth || 0;
      this.disabled = Boolean(options.disabled);
      this.open = Boolean(options.open);
      this.name = options.name || "";
      this.value = options.value || "";
      this.type = options.type || "";
      this.checked = Boolean(options.checked);
      this.selectionStart = options.selectionStart ?? null;
      this.selectionEnd = options.selectionEnd ?? null;
    }
    append(...children) {
      for (const child of children) { child.parentElement = this; this.children.push(child); }
    }
    querySelectorAll(selector) {
      if (selector !== "*") return [];
      const result = [];
      const visit = element => { for (const child of element.children) { result.push(child); visit(child); } };
      visit(this);
      return result;
    }
    hasAttribute(name) { return name === "disabled" && this.disabled; }
    focus() { globalThis.document.activeElement = this; }
    setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  }
  const tree = fresh => {
    const root = new FakeElement("div");
    const list = new FakeElement("div", {dataset: {uiKey: "model-list"}, classes: ["detected-list"], scrollTop: fresh ? 0 : 140, scrollHeight: 500, clientHeight: 200});
    const search = new FakeElement("input", {id: "detect-search", type: "search", value: fresh ? "" : "model-b", selectionStart: fresh ? 0 : 3, selectionEnd: fresh ? 0 : 5});
    const selected = new FakeElement("input", {dataset: {uiKey: "selected-model"}, type: "checkbox", checked: fresh});
    const pending = new FakeElement("input", {dataset: {uiKey: "pending-model"}, type: "checkbox", checked: fresh, disabled: !fresh});
    const details = new FakeElement("details", {dataset: {uiKey: "details"}, open: !fresh});
    const message = new FakeElement("article", {dataset: {messageId: "message-1"}, classes: fresh ? [] : ["expanded"]});
    list.append(selected, pending);
    root.append(search, list, details, message);
    return {root, list, search, selected, pending, details, message};
  };
  try {
    globalThis.HTMLElement = FakeElement;
    const oldTree = tree(false);
    globalThis.document = {activeElement: oldTree.search};
    const helpers = await import(pathToFileURL(resolve(import.meta.dirname, "..", "dist", "ui_helpers.js")));
    const state = helpers.captureInteractiveRegion(oldTree.root);
    const newTree = tree(true);
    globalThis.document.activeElement = null;
    helpers.restoreInteractiveRegion(newTree.root, state);
    assert.equal(newTree.list.scrollTop, 140);
    assert.equal(newTree.search.value, "model-b");
    assert.equal(globalThis.document.activeElement, newTree.search);
    assert.deepEqual([newTree.search.selectionStart, newTree.search.selectionEnd], [3, 5]);
    assert.equal(newTree.selected.checked, false, "enabled user choice should survive");
    assert.equal(newTree.pending.checked, true, "newly enabled live result should keep its rendered default");
    assert.equal(newTree.details.open, true);
    assert.equal(newTree.message.classList.contains("expanded"), true);
  } finally {
    if (previous.HTMLElement === undefined) delete globalThis.HTMLElement; else globalThis.HTMLElement = previous.HTMLElement;
    if (previous.document === undefined) delete globalThis.document; else globalThis.document = previous.document;
  }
});

test("testing the current proxy never persists or switches settings", async () => {
  const previousDocument = globalThis.document;
  class FakeClassList {
    toggle() {}
  }
  class FakeElement {
    constructor(value = "") {
      this.value = value;
      this.disabled = false;
      this.textContent = "";
      this.classList = new FakeClassList();
      this.listeners = new Map();
    }
    addEventListener(name, listener) { this.listeners.set(name, listener); }
    fire(name) { this.listeners.get(name)?.({currentTarget: this, preventDefault() {}, stopPropagation() {}}); }
  }
  const elements = new Map([
    ["#proxy-test-status", new FakeElement()],
    ["#proxy-mode", new FakeElement("external")],
    ["#proxy-http", new FakeElement("http://127.0.0.1:18080")],
    ["#proxy-https", new FakeElement("http://127.0.0.1:18443")],
    ["#proxy-bypass", new FakeElement("localhost")],
    ["#test-proxy", new FakeElement()],
  ]);
  globalThis.document = {
    querySelector(selector) { return elements.get(selector) || null; },
    querySelectorAll() { return []; },
  };
  const root = resolve(import.meta.dirname, "..");
  const proxySource = await readFile(resolve(root, "dist", "proxy_settings.js"), "utf8");
  const apiVersion = proxySource.match(/\.\/api\.js(\?v=[^"]+)?/i)?.[1] || "";
  const apiURL = pathToFileURL(resolve(root, "dist", "api.js"));
  apiURL.search = apiVersion;
  const apiModule = await import(apiURL);
  const proxyModule = await import(pathToFileURL(resolve(root, "dist", "proxy_settings.js")));
  const originalUpdate = apiModule.api.updateProxySettings;
  const originalTest = apiModule.api.testProxySettings;
  let writes = 0;
  let tested;
  try {
    apiModule.api.updateProxySettings = async () => { writes++; throw new Error("unexpected write"); };
    apiModule.api.testProxySettings = async settings => { tested = settings; return {status_code: 200, elapsed_ms: 12}; };
    proxyModule.bindProxySettings({
      settings: {mode: "external", http_proxy: "", https_proxy: "", no_proxy: "", managed_refresh_interval_minutes: 1440},
      managed: {configured: false, profiles: [], url_configured: false, nodes: [], unsupported_count: 0, unsupported_types: [], status: "idle"},
      onChanged() { throw new Error("proxy test unexpectedly changed state"); },
      setMessage() {},
    });
    assert.equal(elements.get("#test-proxy").listeners.has("click"), true);
    elements.get("#test-proxy").fire("click");
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(writes, 0);
    assert.equal(typeof tested, "object", elements.get("#proxy-test-status").textContent);
    assert.deepEqual(
      {mode: tested.mode, http_proxy: tested.http_proxy, https_proxy: tested.https_proxy, no_proxy: tested.no_proxy},
      {mode: "external", http_proxy: "http://127.0.0.1:18080", https_proxy: "http://127.0.0.1:18443", no_proxy: "localhost"},
    );
    assert.match(elements.get("#proxy-test-status").textContent, /代理可用/);
  } finally {
    apiModule.api.updateProxySettings = originalUpdate;
    apiModule.api.testProxySettings = originalTest;
    if (previousDocument === undefined) delete globalThis.document; else globalThis.document = previousDocument;
  }
});

test("forms and action groups stay aligned without narrow-window button distortion", async () => {
  const styles = await readFile(resolve(import.meta.dirname, "..", "src", "styles.css"), "utf8");
  assert.match(styles, /\.channel-create-form \{[^}]*grid-template-columns:\s*minmax\(160px,220px\) minmax\(220px,1fr\) max-content;[^}]*align-items:\s*end/);
  assert.match(styles, /\.channel-create-form label \{[^}]*margin:\s*0/);
  assert.match(styles, /\.channel-create-form > button \{[^}]*min-height:\s*38px;[^}]*white-space:\s*nowrap/);
  assert.match(styles, /\.managed-profile-meta \{[^}]*grid-template-columns:\s*minmax\(160px,240px\) max-content;[^}]*justify-content:\s*start/);
  assert.match(styles, /\.managed-profile-meta label \{[^}]*margin:\s*0/);
  assert.match(styles, /\.dialog-actions \{[^}]*flex-wrap:\s*wrap/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.dialog-actions \{[^}]*grid-template-columns:\s*repeat\(auto-fit,minmax\(120px,1fr\)\)/);
  assert.match(styles, /@media \(max-width:\s*520px\)[\s\S]*?\.managed-profile-card-actions[^\{]*\{[^}]*grid-template-columns:\s*repeat\(2,minmax\(0,1fr\)\)/);
  assert.match(styles, /\.actions > button, \.row-actions > button, \.task-card-actions > button \{[^}]*white-space:\s*nowrap/);
  assert.match(styles, /dialog > \.dialog-head \{[^}]*padding:\s*18px 18px 0/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.provider-layout \.provider-row \{[^}]*grid-template-columns:\s*minmax\(0,1fr\) auto/);
  assert.match(styles, /@media \(max-width:\s*400px\)[\s\S]*?\.dialog-actions \{[^}]*grid-template-columns:\s*minmax\(0,1fr\)/);
  assert.match(styles, /\.models-table th:nth-child\(2\)/);
  assert.doesNotMatch(styles, /\n\s*th:nth-child\(2\),\s*td:nth-child\(2\)/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.task-draft-banner button \{[^}]*grid-column:\s*1 \/ -1/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.remote-readonly-actions \{[^}]*grid-column:\s*1 \/ -1/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.sync-domain-grid \{[^}]*grid-template-columns:\s*repeat\(2,minmax\(0,1fr\)\);[^}]*overflow:\s*visible/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.hardware-group-row \{[^}]*grid-template-columns:\s*minmax\(0,1fr\) repeat\(3,34px\)/);
  assert.match(styles, /@media \(max-width:\s*360px\)[\s\S]*?\.agent-turn-row code \{[^}]*display:\s*none/);
  assert.match(styles, /@media \(max-width:\s*360px\)[\s\S]*?\.hardware-terminal-keys \{[^}]*grid-template-columns:\s*repeat\(4,minmax\(0,1fr\)\);[^}]*overflow:\s*visible/);
});

test("channel lifecycle renders active and retired actions with guarded purge", async () => {
  const root = resolve(import.meta.dirname, "..");
  const module = await import(pathToFileURL(resolve(root, "dist", "channels.js")));
  const context = {models: [], accounts: [], projects: [], workspaces: [], knowledge: [], libraries: []};
  const instance = {
    id: "channel-1",
    plugin_id: "feishu",
    provider_key: "feishu",
    owner_id: "owner-1",
    runtime_device_id: "device-1",
    name: "团队飞书",
    status: "ready",
    effective_availability: "available",
    revision: 7,
    credential_configured: true,
    owner_bound: true,
    host_project_id: "project-channel-1",
    host_workspace_id: "workspace-channel-1",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
  };
  const active = module.renderChannels([], [instance], context);
  assert.match(active, /data-channel-reset-binding="channel-1"/);
  assert.match(active, /data-channel-archive="channel-1"/);
  assert.match(active, /扫码更新飞书权限/);
  assert.match(active, /Runtime、通知与访问范围/);

  const retired = module.renderChannels([], [{...instance, status: "retired"}], context);
  for (const marker of ["已归档", "data-channel-view", "data-channel-export", "data-channel-purge", "永久删除"]) assert.match(retired, new RegExp(marker));
  for (const forbidden of ["data-channel-instance-toggle", "data-channel-onboard", "data-channel-settings", "data-channel-credentials", "data-channel-reset-binding", "data-channel-archive"]) assert.doesNotMatch(retired, new RegExp(forbidden));
  assert.equal(module.channelPurgePreviewMessage({name: "团队飞书", tasks: 2, conversations: 3, messages: 5, attachments: 7}), "将永久删除“团队飞书”及其本地归档：2 个任务、3 个会话、5 条消息、7 个附件。此操作不可恢复，飞书后台应用不会自动删除。");

  const source = await readFile(resolve(root, "src", "channels.ts"), "utf8");
  assert.ok(source.indexOf("channelInstancePurgePreview(instanceID)") < source.indexOf("window.confirm(channelPurgePreviewMessage(preview))"));
  assert.ok(source.indexOf("window.confirm(channelPurgePreviewMessage(preview))") < source.indexOf("window.prompt(`请输入实例名"));
  assert.ok(source.indexOf("window.prompt(`请输入实例名") < source.indexOf("api.purgeChannelInstance(instanceID"));
  assert.match(source, /waitForChannelReadiness/);
  assert.match(source, /api\.channelInstance\(instanceID\)/);
  assert.match(source, /status === "ready"/);
  assert.match(source, /渠道已就绪，可以开始收发消息/);
  const originalFetch = globalThis.fetch;
  const originalWindow = globalThis.window;
  let refreshed = 0;
  const messages = [];
  globalThis.window = {setTimeout};
  globalThis.fetch = async () => new Response(JSON.stringify({instance: {...instance, status: "ready"}}), {status: 200, headers: {"content-type": "application/json"}});
  try {
    await module.waitForChannelReadiness("channel-1", {
      refresh: async () => { refreshed++; },
      setMessage: (kind, message) => messages.push([kind, message]),
    });
  } finally {
    globalThis.fetch = originalFetch;
    globalThis.window = originalWindow;
  }
  assert.equal(refreshed, 1);
  assert.deepEqual(messages, [["success", "渠道已就绪，可以开始收发消息。"]]);
});

test("channel lifecycle API and archived projects share the purge path", async () => {
  const root = resolve(import.meta.dirname, "..");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const types = await readFile(resolve(root, "src", "types.ts"), "utf8");
  for (const route of ["reset-binding", "/archive", "purge-preview", "/purge"]) assert.match(api, new RegExp(route));
  assert.match(api, /resetChannelBinding[\s\S]*?method: "POST"[\s\S]*?"If-Match"/);
  assert.match(api, /archiveChannelInstance[\s\S]*?method: "POST"[\s\S]*?"If-Match"/);
  assert.match(api, /purgeChannelInstance[\s\S]*?"If-Match"[\s\S]*?confirmation_name/);
  assert.match(types, /channel_instance_id\?: string/);
  assert.match(types, /channel_retired\?: boolean/);
  assert.match(types, /"retired"/);
  assert.match(main, /project\.channel_retired[\s\S]*?purgeArchivedChannelProject/);
  assert.match(main, /purgeArchivedChannelProject[\s\S]*?purgeRetiredChannelInstance/);
  assert.match(main, /state\.projects\.filter\(item => !item\.channel_retired\)/);
});

test("channel reauthorization explains menu preservation and separate creation", async () => {
  const channels = await readFile(resolve(import.meta.dirname, "..", "dist", "channels.js"), "utf8");
  assert.match(channels, /initial.mode === "existing_app"/);
  assert.match(channels, /不修改已有菜单/);
  assert.match(channels, /不自动提交应用草稿发布/);
  assert.match(channels, /新应用会执行一次 AHA 菜单初始化并提交发布/);
  assert.doesNotMatch(channels, /用于新增机器人菜单或显示名权限/);
});

test("workspace detection renders inaccessible, Git, and backend probe states", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderWorkspaceDetection} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));
  const base = {id: "workspace-1", project_id: "project-1", name: "Workspace", locality: "local", transport: "native", root_path: "C:/repo", ssh_password_configured: false, health: "degraded"};
  const unavailable = renderWorkspaceDetection({
    ...base,
    health: "error",
    capabilities: {workspace: {status: "unavailable", error: "Workspace 不可访问: <denied>"}},
    repository: {},
  });
  assert.match(unavailable, /Workspace 不可访问/);
  assert.match(unavailable, /&lt;denied&gt;/);
  assert.doesNotMatch(unavailable, /<denied>/);

  const probes = renderWorkspaceDetection({
    ...base,
    capabilities: {
      workspace: {status: "ready"},
      platform: {status: "execution_failed", error: "平台检测执行失败: timeout"},
      codex: {status: "not_installed"},
      claude: {status: "execution_failed", error: "Claude 检测执行失败: bad config"},
    },
    repository: {status: "not_repository", is_git: false, message: "该目录不是 Git 仓库"},
  });
  assert.match(probes, /该目录不是 Git 仓库/);
  assert.match(probes, /Codex 未安装/);
  assert.match(probes, /Claude 检测执行失败: bad config/);
  assert.match(probes, /平台检测执行失败: timeout/);

  const gitFailure = renderWorkspaceDetection({
    ...base,
    capabilities: {workspace: {status: "ready"}, codex: {status: "ready", version: "1.0"}},
    repository: {status: "execution_failed", is_git: false, error: "Git 检测执行失败: permission denied"},
  });
  assert.match(gitFailure, /Git 检测执行失败: permission denied/);
  assert.match(gitFailure, /proto codex/);
  assert.match(gitFailure, /Codex · 1\.0/);

  const versions = renderWorkspaceDetection({
    ...base,
    capabilities: {
      workspace: {status: "ready"},
      codex: {status: "ready", version: "codex-cli 0.153.4"},
      claude: {status: "ready", version: "2.1.0"},
    },
    repository: {status: "ready", is_git: true},
  });
  assert.match(versions, /Codex · codex-cli 0\.153\.4/);
  assert.match(versions, /Claude · 2\.1\.0/);

  const agentAPI = renderWorkspaceDetection({
    ...base,
    agent_api_status: "ready",
    agent_api_resolved_url: "http://172.28.0.1:8766",
    capabilities: {workspace: {status: "ready"}, agent_api: {status: "ready", url: "http://172.28.0.1:8766"}},
    repository: {status: "ready", is_git: true},
  });
  assert.match(agentAPI, /Agent API · http:\/\/172\.28\.0\.1:8766/);

  const agentAPIFailed = renderWorkspaceDetection({
    ...base,
    agent_api_status: "error",
    agent_api_error: "反向连接超时",
    capabilities: {workspace: {status: "ready"}, agent_api: {status: "unavailable", error: "反向连接超时"}},
    repository: {status: "ready", is_git: true},
  });
  assert.match(agentAPIFailed, /反向连接超时/);
});

test("Agent API settings support global defaults and Workspace reverse probes", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  for (const marker of ["agent-api-settings-form", "ws-agent-api-mode", "ws-agent-api-url", "测试连接", "Agent API 反向连接"]) {
    assert.match(script, new RegExp(marker));
  }
  assert.match(script, /api\.agentAPISettings\(\)/);
  assert.match(script, /api\.updateAgentAPISettings/);
  assert.match(script, /detectWorkspaceWithHostKeyTrust/);
  assert.match(script, /首次连接 SSH Workspace/);
  assert.match(api, /workspaces\/.*\/host-key\/trust/);
  assert.match(script, /agent_api_status === "ready"/);
  assert.match(api, /settings\/agent-api/);
  assert.match(styles, /\.workspace-agent-api-fields/);
});

test("advanced settings expose independent Backend idle and turn timeouts", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  for (const marker of ["backend-settings-form", "idle_timeout_minutes", "turn_timeout_hours", "Codex / Claude 统一生效"]) {
    assert.match(script, new RegExp(marker));
  }
  assert.match(script, /api\.backendSettings\(\)/);
  assert.match(script, /api\.updateBackendSettings/);
  assert.match(api, /settings\/backend/);
});

test("task list refreshes after round terminal events", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {eventRefreshesTaskList} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));
  assert.equal(eventRefreshesTaskList("turn_succeeded"), true);
  assert.equal(eventRefreshesTaskList("round_completed"), true);
  assert.equal(eventRefreshesTaskList("round_failed"), true);
  assert.equal(eventRefreshesTaskList("agent_progress"), false);
});

test("read-only task history opens on the latest page and lazily loads older rows", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(script, /summary\?\.read_only\s*\?\s*\[\s*"chat",\s*"update",\s*"error"\s*\]/);
  assert.match(script, /agentConversation\(taskID,\s*"main",\s*\{\s*limit:\s*initialConversationPageSize,\s*categories\s*\}/);
  assert.match(script, /requestConversationBottom\(\)/);
  assert.match(script, /detail\.task\.read_only\s*\|\|\s*detail\.task\.status\s*===\s*"draft"\)\s*closeEvents\(\)/);
  assert.match(script, /currentTop\s*<\s*previousTop\s*&&\s*currentTop\s*<=\s*80/);
  assert.match(script, /loadingOlderConversation/);
});

test("sidebar shows service version and live uptime", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(script, /class="system-meta"/);
  assert.match(script, /id="system-uptime"/);
  assert.match(script, /state\.system\.web_version \|\| state\.system\.version \|\| "dev"/);
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
  const channels = await readFile(resolve(root, "dist", "channels.js"), "utf8");
  const codexAccounts = await readFile(resolve(root, "dist", "codex_accounts.js"), "utf8");
  const knowledgeWorkspace = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  const index = await readFile(resolve(root, "dist", "index.html"), "utf8");
  assert.match(script, /api\.authStatus/);
  assert.match(api, /auth\/recover/);
  assert.match(api, /auth\/password/);
  assert.match(script, /id="forgot-password"/);
  assert.match(script, /id="password-recovery-form"/);
  assert.match(script, /id="owner-avatar"/);
  assert.match(script, /ownerAvatarClicks\s*>=\s*5/);
  assert.match(script, /id="change-password-form"/);
  assert.match(script, /id="origin-validation-form"/);
  assert.match(script, /api\.updateSecuritySettings/);
  assert.match(script, /--allow-cross-origin/);
  assert.match(script, /state\.view = "advanced"/);
  assert.match(css, /\.account-security-panel/);
  assert.match(css, /\.security-warning\.active/);
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
  assert.match(knowledgeWorkspace, /\\u6587\\u6863\\u94fe\\u63a5/);
  assert.match(knowledgeWorkspace, /knowledgeHomeBody/);
  assert.match(knowledgeWorkspace, /knowledgeIndexSummary/);
  assert.match(knowledgeWorkspace, /knowledge-feedback-active/);
  assert.match(knowledgeWorkspace, /feedback_state === "stale"/);
  assert.match(knowledgeWorkspace, /\\u8fd4\\u56de\\u77e5\\u8bc6\\u5e93\\u5165\\u53e3/);
  assert.match(knowledgeWorkspace, /data-knowledge-back/);
  assert.doesNotMatch(knowledgeWorkspace, /data-knowledge-toggle/);
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
  assert.match(script, /agent-config-form/);
  assert.match(taskComposer, /id="composer-agent"/);
  assert.match(taskTools, /data-task-tool/);
  assert.match(taskTools, /id="close-task-tool"/);
  assert.match(taskTools, /task-tool-panel/);
  assert.match(taskTools, /id="toggle-task-tool-mode"/);
  assert.match(taskTools, /id="task-tool-resizer"/);
  assert.match(script, /localStorage\.setItem\(taskToolLayoutKey/);
  assert.match(script, /state\.taskToolMode === "split" \? "fullscreen" : "split"/);
  assert.match(script, /resizer\.addEventListener\("dblclick"/);
  assert.match(css, /\.task-grid\.task-tool-open\.task-tool-split \{[^}]*grid-template-columns:/);
  assert.match(css, /\.task-grid \{[^}]*grid-template-rows:\s*minmax\(0,1fr\)/);
  assert.match(css, /\.conversation \{[^}]*height:\s*100%;[^}]*overflow:\s*hidden/);
  assert.match(css, /\.task-tool-panel \{[^}]*min-height:\s*0;[^}]*overflow:\s*hidden/);
  assert.match(css, /#task-tool-panel-body \{[^}]*height:\s*100%;[^}]*min-height:\s*0;[^}]*overflow:\s*auto/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*?\.task-grid\.task-tool-open\.task-tool-split \.task-tool-panel \{[^}]*position: absolute;/);
  assert.match(script, /addEventListener\("paste",/);
  assert.match(script, /clipboardImageFiles\(event\)/);
  assert.match(script, /item\.type\.startsWith\("image\/"\)/);
  assert.match(script, /pasted-image-\$\{Date\.now\(\)\}/);
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
  assert.match(hardwarePanel, /hardware-config-collapse/);
  assert.match(hardwarePanel, /configCollapsed/);
  assert.match(taskTools, /renderHardwareGroupSwitcher/);
  assert.match(hardwarePanel, /hardware-title-group-select/);
  assert.match(hardwarePanel, /syncTitleGroupSelect/);
  assert.match(script, /hardware:\s*detail\.hardware/);
  assert.match(script, /refreshHardwarePanel\(detail, setMessage\)/);
  assert.match(css, /\.hardware-title-group-select/);
  assert.match(css, /\.hardware-tool\.config-collapsed \{[^}]*grid-template-columns:\s*42px minmax\(0,1fr\)/);
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
  assert.doesNotMatch(index, /vendor\/xterm\.(?:js|css)/);
  assert.match(hardwarePanel, /loadTerminalAssets/);
  assert.match(hardwarePanel, /\/vendor\/xterm\.js/);
  assert.match(hardwarePanel, /\/vendor\/xterm\.css/);
  assert.match(hardwarePanel, /data-hardware-terminal-key="ctrl-c"/);
  assert.match(hardwarePanel, /const sendForm = root\.querySelector/);
  assert.doesNotMatch(script, /data-task-tab=|mobile-agent-strip|conversation-filters/);
  assert.match(taskTools, /renderTaskMemory\(detail\.memory\)/);
  assert.doesNotMatch(script, /data-realtime-state class=/);
  assert.match(script, /task-head-subline/);
  assert.match(taskTools, /iconName: "hardware"/);
  assert.match(taskTools, /iconName: "browser"/);
  assert.match(script, /state\.taskTool === tool/);
  assert.doesNotMatch(script, /Context Evidence|knowledge-mini|project_knowledge|global_knowledge/);
  assert.match(script, /<details class="prompt-snapshot" open>/);
  assert.match(script, /prompt-snapshot-body markdown-body/);
  assert.match(script, /renderMarkdown\(context\.prompt/);
  assert.match(script, /markdown\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /refreshTaskRuntime/);
  assert.match(script, /api\.agentMessage[\s\S]{0,600}requestConversationBottom\(\)[\s\S]{0,120}updateTaskLiveRegions/);
  assert.match(script, /shouldAutoScrollConversation\(wasAtBottom\)/);
  assert.match(script, /conversationAutoScrollBlockedUntil\s*=\s*Date\.now\(\)\s*\+\s*800/);
  assert.match(script, /function stabilizeConversationBottom[\s\S]*requestAnimationFrame[\s\S]*image\.addEventListener\("load"/);
  assert.match(script, /function stabilizeConversationBottom[\s\S]{0,120}const pinVersion = \+\+conversationBottomPinVersion/);
  assert.match(script, /for \(const delay of \[\s*80,\s*240,\s*600\s*\]\)[\s\S]{0,80}setTimeout\(apply,\s*delay\)/);
  assert.match(script, /\[\s*"wheel",\s*"touchstart",\s*"pointerdown"\s*\][\s\S]*cancelConversationBottomPin/);
  assert.match(css, /\.messages \{[^}]*overflow-anchor:\s*none/);
  assert.match(script, /startTaskFallback/);
  assert.match(agents, /data-live-elapsed-ms/);
  assert.match(agents, /ms > 0 && ms < 1000/);
  assert.match(agents, /metrics\.session_id/);
  assert.match(agents, /class="session-id"/);
  assert.match(agents, /function renderTaskMemory/);
  assert.match(conversation, /data-copy-message/);
  assert.match(conversation, /data-toggle-message/);
  assert.match(conversation, /data-message-id/);
  assert.match(helpers, /classList\.contains\("expanded"\)/);
  assert.match(script, /list\.innerHTML = conversationListHtml\(\);\s*restoreRegionUI\(list, ui, false\);\s*if \(shouldAutoScrollConversation\(wasAtBottom\)\)[\s\S]{0,80}stabilizeConversationBottom\(list\)/);
  assert.match(script, /list\.innerHTML = conversationListHtml\(\);\s*restoreRegionUI\(list, ui, false\);\s*list\.scrollTop = previousTop \+ Math\.max/);
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
  assert.match(api, /codex-accounts\/import-local/);
  assert.match(codexAccounts, /importLocalCodexAccount/);
  assert.match(codexAccounts, /import-local-codex-account/);
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
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*?\.codex-account-head \{[^}]*display:\s*grid;[^}]*grid-template-columns:\s*minmax\(0,1fr\)/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*?\.ws-row \.workspace-detection \{[^}]*grid-column:\s*1 \/ -1/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*?\.ws-row \.proto-badges \{[^}]*flex-wrap:\s*wrap/);
  assert.doesNotMatch(codexAccounts, /device-auth|setInterval/);
  assert.doesNotMatch(promptAdmin, /prompt-route-list|Effective Prompt|Context Manifest/);
  assert.doesNotMatch(api, /prompts\/routes|prompts\/preview|context\/resources/);
  assert.match(script, /advancedSubview\(renderPromptAdmin\(\)\)/);
  assert.match(script, /data-view="prompts"/);
  assert.match(script, /data-view="sync"/);
  assert.match(script, /data-view="advanced">← 返回高级设置/);
  assert.match(script, /const mobileNav = \[[\s\S]{0,100}\.\.\.nav\.slice\(0, 5\)[\s\S]{0,100}"advanced"[\s\S]{0,40}"shield"[\s\S]{0,40}"高级"/);
  assert.match(script, /<nav class="bottom-nav">\$\{mobileNav\.map/);
  assert.match(script, /data-view="proxy">进入代理设置/);
  assert.doesNotMatch(script, /\["prompts",\s*"bot",\s*"提示词"\]/);
  assert.doesNotMatch(script, /\["sync",\s*"sync",/);
  assert.match(css, /\.bottom-nav \{[^}]*grid-template-columns:\s*repeat\(6,1fr\)/);
  assert.match(script, /"channels",\s*"bot",\s*"渠道"/);
	assert.match(script, /renderChannels\(state\.channelProviders, state\.channelInstances,/);
  assert.match(api, /api\/v1\/channel-providers/);
  assert.match(script, /channels\.js\?v=[a-f0-9]{12}/);
  assert.match(channels, /扫码创建并绑定飞书应用/);
  assert.match(channels, /channel-onboarding-sessions\/.*\/qr/);
  assert.match(channels, /name="app_secret" type="password"/);
  assert.match(channels, /data-delivery-replay/);
  assert.match(channels, /Owner 收件箱与投递/);
	for (const marker of ["Runtime、通知与访问范围", "allowed_project_ids", "allowed_workspace_ids", "notify_task_status", "普通 Task 状态变更推送到飞书私聊助手", "knowledge_entry_id"]) assert.match(channels, new RegExp(marker));
	for (const marker of ["data-channel-project-scope", "data-channel-workspace-option", "data-channel-policy-select-all", "data-channel-policy-clear"]) assert.match(channels, new RegExp(marker));
	assert.match(channels, /selectedProjects\.has/);
	for (const marker of ["operation_scope_mode", "knowledge_scope_mode", "data-channel-operation-selected", "data-channel-knowledge-selected", "全部 Project \/ Workspace", "全部项目知识与知识库"]) assert.match(channels, new RegExp(marker));
	assert.match(channels, /channel_revision_conflict/);
	assert.match(channels, /渠道设置已被其他更新修改/);
	assert.match(channels, /扫码更新飞书权限/);
  assert.match(channels, /Knowledge allowlist/);
  assert.match(channels, /人工整理并共享/);
  assert.match(api, /channel-knowledge-records/);
  assert.match(script, /shell\(renderProxySettings\(state\.proxySettings, state\.managedProxy\)\)/);
  assert.match(proxySettings, /AHA 内置代理/);
  assert.match(proxySettings, /VLESS Reality/);
  assert.match(proxySettings, /importProxySubscription/);
  assert.match(proxySettings, /refreshProxyProfile/);
  assert.match(proxySettings, /deleteProxyProfile/);
  assert.match(proxySettings, /data-activate-proxy-profile/);
  assert.match(proxySettings, /data-select-proxy-node/);
  assert.match(proxySettings, /updateProxyProfile/);
  assert.match(proxySettings, /activateProxyProfile/);
  assert.match(proxySettings, /data-delete-proxy-profile/);
  assert.match(proxySettings, /data-test-proxy-node/);
  assert.match(proxySettings, /testProxyNode/);
  assert.match(proxySettings, /节点列表/);
  assert.match(proxySettings, /应用节点/);
  assert.match(proxySettings, /需要重新选择节点/);
  assert.match(proxySettings, /subscription_at/);
  assert.match(proxySettings, /添加后不会切换当前线路/);
  assert.match(proxySettings, /id="proxy-add-dialog"/);
  assert.match(proxySettings, /type="file"/);
  assert.match(proxySettings, /saveProxySettings/);
  assert.match(proxySettings, /pendingSubscription\(\)\.then/);
  assert.match(proxySettings, /return api\.importProxySubscription/);
  assert.match(script, /name=\\?"proxy_enabled/);
  assert.match(agents, /name="proxy_enabled"/);
  assert.match(proxySettings, /HTTP_PROXY/);
  assert.match(proxySettings, /testProxySettings/);
  assert.match(script, /renderSyncSettings/);
  assert.match(syncSettings, /token_configured/);
  assert.match(syncSettings, /runSync/);
  assert.match(api, /tasks\/\$\{encodeURIComponent\(id\)\}\/remote-mirror/);
  assert.match(script, /data-retire-remote-task/);
  assert.match(script, /来源设备已停用或数据已清空/);
  assert.match(script, /retireRemoteTaskMirror\(id\)[\s\S]{0,180}runSync\(\)/);
  assert.match(api, /workspaces\/\$\{encodeURIComponent\(id\)\}\/remote-mirror/);
  assert.match(script, /data-retire-remote-workspace/);
  assert.match(script, /retireRemoteWorkspaceMirror/);
  assert.match(script, /移除孤立 Workspace/);
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
  assert.match(script, /--visual-viewport-offset-top/);
  assert.match(script, /--visual-viewport-bottom-inset/);
  assert.match(script, /setTimeout\(\(\)\s*=>\s*\{[\s\S]{0,120}syncVisualViewportHeight\(\);[\s\S]{0,120}restoreComposerConversationBottom\(\);[\s\S]{0,80}\},\s*180\)/);
  assert.match(script, /composerFocusWasAtBottom/);
  assert.match(script, /restoreComposerConversationBottom/);
  assert.match(script, /conversationIsAtBottom/);
  assert.match(script, /navigationRestoring/);
  assert.match(script, /if \(navigationRestoring\) return/);
  assert.match(script, /navigationRestoring = false;\s*state\.loading = false;\s*if \(state\.selectedTask\) requestConversationBottom\(\);\s*render\(\)/);
  assert.match(script, /project:\s*state\.selectedProject \|\| undefined/);
  assert.match(script, /task:\s*state\.selectedTask\?\.task/);
  assert.match(script, /const savedTask = state\.tasks\.find[\s\S]{0,80}\|\| saved\.task/);
  assert.match(script, /const initialConversationPageSize = 20/);
  assert.match(script, /function showTaskLoading/);
  assert.match(script, /function cancelTaskOpen/);
  assert.match(script, /taskDetailLoading/);
  assert.match(script, /加载最近 \$\{initialConversationPageSize\} 条消息/);
  assert.match(css, /\.task-detail-loading/);
  assert.match(script, /task-view-active/);
  assert.match(api, /cache: "no-store"/);
  assert.match(script, /addEventListener\("heartbeat"/);
  assert.match(css, /body\.composer-focused \.composer-target-wrap/);
  assert.match(css, /body\.composer-focused \.task-screen \{[^}]*position:\s*fixed[^}]*top:\s*var\(--visual-viewport-offset-top\)[^}]*bottom:\s*var\(--visual-viewport-bottom-inset\)/);
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

test("hardware tool title renders the active hardware group switcher", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderTaskToolPanel} = await import(pathToFileURL(resolve(root, "dist", "task_tools.js")));
  const group = (id, description, position) => ({
    task_id: "task-hardware-title",
    id,
    position,
    description,
    mode: "serial",
    serial: {device: `COM${position + 1}`, baudrate: 115200},
    network: {host: "", port: 23, protocol: "telnet", ssh_auth: "auto"},
    username: "",
    password_configured: false,
    access: "read_write",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
  });
  const detail = {
    task: {id: "task-hardware-title", status: "active"},
    hardware: [group("board-a", "主控板", 0), group("board-b", "继电器板", 1)],
    memory: {},
  };
  const html = renderTaskToolPanel("hardware", detail, "main", "", "split");
  assert.match(html, /<h3>硬件调试<\/h3><select id="hardware-title-group-select"/);
  assert.match(html, /<option value="board-a" selected>主控板<\/option>/);
  assert.match(html, /<option value="board-b" >继电器板<\/option>/);
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
      {id: "proposal-new", entry_id: "knowledge-new", base_revision: 0, status: "pending", review_mode: "manual", source_task_id: "task-new", source_turn_id: "turn-new", created_at: "2026-09-06T01:00:00Z", proposed: {id: "knowledge-new", scope: "project", project_id: "project-1", sort_order: 0, is_index: false, type: "practice", title: "Brand new knowledge", body: "**Complete preview**\n\nAll proposed details.", status: "candidate", confidence: .9, revision: 1, helped_count: 0, stale_count: 0, created_at: "", updated_at: ""}},
      {id: "proposal-revision", entry_id: "knowledge-1", base_revision: 2, scope: "project", project_id: "project-1", type: "practice", title: "Updated title", body: "Keep this line.\nNew detail.", base_title: "Current title", base_body: "Keep this line.\nOld detail.", status: "pending", review_mode: "manual", source_task_id: "task-revision", source_turn_id: "turn-revision", created_at: "2026-09-06T02:00:00Z"},
    ],
    reviewSettings: {auto_approve: false},
    refreshData: async () => {}, render: () => {}, setMessage: () => {},
  });
  assert.match(html, /data-knowledge-area="updates" class="active"/);
  assert.match(html, /data-knowledge-update-filter="pending" class="active"/);
  assert.match(html, /data-knowledge-update-filter="all"/);
  assert.match(html, /data-knowledge-auto-review/);
  assert.match(html, /data-knowledge-batch-all/);
  assert.match(html, /data-knowledge-batch-approve/);
  assert.match(html, /data-knowledge-batch-reject/);
  assert.match(html, /data-knowledge-update-select="proposal:proposal-new"/);
  assert.match(html, /data-knowledge-update-select="legacy:knowledge-legacy"/);
  assert.match(html, /手动评审/);
  assert.match(html, /待处理 <b>3<\/b>/);
  assert.match(html, /data-knowledge-proposal-view="proposal-new"/);
  assert.match(html, /data-knowledge-proposal-approve="proposal-new"/);
  assert.match(html, /data-knowledge-proposal-reject="proposal-new"/);
  assert.match(html, /data-knowledge-proposal-view="proposal-revision"/);
  assert.match(html, /data-knowledge-proposal-approve="proposal-revision"/);
  assert.match(html, /data-knowledge-proposal-reject="proposal-revision"/);
  assert.match(html, /knowledge-proposal-summary/);
  assert.match(html, /<strong>Brand new knowledge<\/strong>/);
  assert.match(html, /<strong>Updated title<\/strong>/);
  assert.doesNotMatch(html, /Complete preview|Old detail\.|New detail\./);
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
  const {resolveReasoningEffort, runtimeFieldsHTML} = await import(pathToFileURL(resolve(root, "dist", "runtime_picker.js")));
  assert.equal(resolveReasoningEffort(["low", "medium", "high"], "high", "low", false), "high");
  assert.equal(resolveReasoningEffort(["low", "medium", "high"], "high", "low", true), "low");
  assert.equal(resolveReasoningEffort(["low", "medium", "high"], "", "low", false), "low");
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
  assert.match(html, /name="model_id"[^>]*required/);
  assert.match(html, /name="wire_model"[^>]*required/);
});

test("knowledge update list keeps proposal bodies folded behind details", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderUpdates} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const html = renderUpdates([], [{
    id: "proposal-1", entry_id: "", base_revision: 0, status: "pending",
    title: "运行时实践", body: "PROPOSAL_BODY_MUST_STAY_FOLDED", scope: "global",
    source_task_id: "task-1", created_at: "2026-09-07T10:00:00Z",
  }], []);
  assert.match(html, /运行时实践/);
  assert.match(html, /查看详情/);
  assert.match(html, /knowledge-proposal-summary/);
  assert.doesNotMatch(html, /PROPOSAL_BODY_MUST_STAY_FOLDED/);
  assert.doesNotMatch(html, /knowledge-proposal-full|knowledge-proposal-diff/);
});

test("knowledge proposal history marks automatic and manual review", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderUpdates} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const html = renderUpdates([], [
    {id: "auto", entry_id: "auto-entry", base_revision: 0, status: "pending", review_mode: "auto", title: "Auto", body: "body", scope: "global", created_at: "2026-09-07T10:00:00Z"},
    {id: "manual", entry_id: "manual-entry", base_revision: 0, status: "pending", review_mode: "manual", title: "Manual", body: "body", scope: "global", created_at: "2026-09-07T09:00:00Z"},
  ], [], {auto_approve: true});
  assert.match(html, /自动评审/);
  assert.match(html, /手动评审/);
  assert.match(html, /新提案将自动批准/);
});

test("message send clears draft state before awaiting the request", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const clearAt = script.indexOf('state.taskDrafts[agentID] = ""');
  const sendAt = script.indexOf("await api.agentMessage(taskID, agentID");
  const acceptedAt = script.indexOf("accepted = true", sendAt);
  const refreshAt = script.indexOf("await refreshTaskRuntime(taskID)", sendAt);
  assert.ok(clearAt >= 0 && sendAt > clearAt);
  assert.ok(acceptedAt > sendAt && refreshAt > acceptedAt);
  assert.match(script, /messageSubmitPending = true/);
  assert.match(script, /state\.taskAttachmentDrafts\[agentID\] = \[\]/);
  assert.match(script, /if \(!accepted && state\.selectedTask/);
  assert.match(script, /消息已发送，但刷新失败/);
  assert.match(script, /finally\s*\{\s*messageSubmitPending = false/);
});

test("pending knowledge libraries fit mobile width", async () => {
  const root = resolve(import.meta.dirname, "..");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.knowledge-library-grid \{[^}]*grid-template-columns:\s*minmax\(0,1fr\);[^}]*overflow:\s*hidden/);
  assert.match(styles, /\.knowledge-library-card > footer label \{[^}]*grid-template-columns:\s*1fr;[^}]*white-space:\s*normal/);
  assert.match(styles, /\.knowledge-library-card > footer button \{[^}]*width:\s*100%/);
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

test("tool lifecycle events render as one completed message", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {coalesceToolConversationItems, renderConversationList} = await import(pathToFileURL(resolve(root, "dist", "conversation_ui.js")));
  const base = {
    task_id: "task-1", turn_id: "turn-42", agent_id: "main", stream_agent_id: "main", category: "tool",
    summary: "go test ./...", created_at: "2026-09-03T00:00:00Z",
  };
  const current = coalesceToolConversationItems([
    {...base, sequence: 1, id: "tool-start", kind: "agent_command_started", payload: {tool_call_id: "call-1", status: "in_progress"}},
    {...base, sequence: 2, id: "tool-finish", kind: "agent_command_finished", payload: {tool_call_id: "call-1", status: "completed", output_tail: "ok"}},
  ]);
  assert.equal(current.length, 1);
  assert.equal(current[0].kind, "agent_command_finished");
  assert.equal(current[0].payload.output_tail, "ok");

  const claude = coalesceToolConversationItems([
    {...base, sequence: 1, id: "claude-start", kind: "agent_command_started", summary: "Read", payload: {tool_use_id: "use-1", tool_name: "Read"}},
    {...base, sequence: 2, id: "claude-finish", kind: "agent_command_finished", summary: "agent_command_finished", payload: {tool_use_id: "use-1", output_tail: "file"}},
  ]);
  assert.equal(claude.length, 1);
  assert.equal(claude[0].summary, "Read");

  const historical = renderConversationList([
    {...base, sequence: 3, id: "legacy-start", kind: "agent_command_started", payload: {command: "go test ./..."}},
    {...base, sequence: 4, id: "legacy-finish", kind: "agent_command_finished", payload: {command: "go test ./...", output_tail: "passed"}},
  ], null, false);
  assert.equal((historical.match(/agent-tool-message/g) || []).length, 1);
  assert.match(historical, /passed/);
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

test("global knowledge opens as a virtual index document with described links", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderKnowledgeDocumentWorkspace} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const base = {scope: "global", status: "verified", confidence: .9, revision: 1, helped_count: 0, stale_count: 0, created_at: "", updated_at: "", sort_order: 0};
  const html = renderKnowledgeDocumentWorkspace([
      {...base, id: "root", is_index: true, type: "navigation", title: "Global index", slug: "index", body: "# Entry\nChoose a child."},
      {...base, id: "knowledge_global_general", parent_id: "root", is_index: false, type: "navigation", title: "通用知识", slug: "general", body: "Human-first reference."},
      {...base, id: "knowledge_global_agent_lessons", parent_id: "root", is_index: false, type: "navigation", title: "Agent 经验教训", slug: "agent-lessons", body: "Agent-first lessons."},
      {...base, id: "knowledge_global_agent_lessons_technical", parent_id: "knowledge_global_agent_lessons", is_index: false, type: "navigation", title: "技术诊断", slug: "technical-diagnostics", body: "Technical traps."},
      {...base, id: "knowledge_global_agent_lessons_behavior", parent_id: "knowledge_global_agent_lessons", is_index: false, type: "navigation", title: "行为教训", slug: "behavior-lessons", body: "Behavior corrections."},
      {...base, id: "child", parent_id: "knowledge_global_general", is_index: false, type: "practice", title: "Core architecture", slug: "core", body: "Child body"},
    ], "No global knowledge.", "global");
  assert.match(html, /knowledge-document-layout/);
  assert.match(html, /knowledge-document-layout linked-index/);
  assert.doesNotMatch(html, /directory-open/);
  assert.doesNotMatch(html, /knowledge-section-head/);
  assert.doesNotMatch(html, /class="panel knowledge-tree"/);
  assert.doesNotMatch(html, /data-knowledge-back/);
  assert.match(html, /data-knowledge-open="root"/);
  assert.doesNotMatch(html, /data-knowledge-delete="root"/);
  assert.match(html, /data-knowledge-open="knowledge_global_general"/);
  assert.match(html, /data-knowledge-open="knowledge_global_agent_lessons"/);
  assert.doesNotMatch(html, /data-knowledge-open="child"/);
  assert.doesNotMatch(html, /data-knowledge-toggle/);
  assert.match(html, />全局知识<\/button>/);
  assert.match(html, /<h1>Entry<\/h1>/);
  assert.match(html, /文档链接/);
  assert.match(html, /class="knowledge-index-links"/);
  assert.match(html, /class="knowledge-index-link" data-knowledge-open="knowledge_global_general">通用知识<\/button><span>：Human-first reference\.<\/span>/);
  assert.match(html, /data-knowledge-parent="knowledge_global_general"/);
  assert.doesNotMatch(html, /knowledge-related-links/);
  assert.match(html, />新建文档<\/button>/);
  assert.doesNotMatch(html, />index(?:\.md)?</i);
  assert.doesNotMatch(html, />navigation</i);
  assert.doesNotMatch(html, />practice</i);
  assert.doesNotMatch(html, />verified</i);
  assert.doesNotMatch(html, />revision/i);

  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  assert.match(source, /managedGlobalKnowledgeIDs/);
  assert.match(source, /globalTechnicalLessonsKnowledgeID/);
});

test("project navigation opens as an index document with child links", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderKnowledgeDocumentWorkspace} = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const base = {scope: "project", project_id: "project-1", status: "verified", confidence: .9, revision: 1, helped_count: 0, stale_count: 0, created_at: "", updated_at: "", sort_order: 0, is_index: false};
  const html = renderKnowledgeDocumentWorkspace([
    {...base, id: "intro", parent_id: "root", type: "navigation", title: "Project introduction", slug: "overview", body: "Introduction"},
    {...base, id: "modules", parent_id: "root", type: "practice", title: "modules", slug: "modules", body: "Index"},
    {...base, id: "camera", parent_id: "modules", type: "navigation", title: "Camera", slug: "camera", body: "Camera module"},
  ], "No navigation.", "project-navigation");
  assert.match(html, /data-knowledge-open="intro"/);
  assert.match(html, /data-knowledge-open="modules"/);
  assert.match(html, /knowledge-document-layout linked-index/);
  assert.match(html, /文档链接/);
  assert.match(html, /模块、代码路径、边界与关键流程/);
  assert.match(html, /Introduction/);
  assert.match(html, /<span>：Index<\/span>/);
  assert.doesNotMatch(html, /class="panel knowledge-tree"/);
  assert.doesNotMatch(html, /knowledge-index-toolbar/);
  assert.doesNotMatch(html, /data-knowledge-open="camera"/);
  assert.match(html, />项目介绍</);
  assert.match(html, />模块索引</);
  assert.ok(html.indexOf('data-knowledge-open="intro"') < html.indexOf('data-knowledge-open="modules"'));

  const projectHtml = renderKnowledgeDocumentWorkspace([
    {...base, id: "project-root", parent_id: "", type: "navigation", title: "Knowledge home", slug: "index", body: "Project index body", is_index: true},
    {...base, id: "practice", parent_id: "project-root", type: "practice", title: "Build guide", slug: "build-guide", body: "Build it"},
  ], "No project knowledge.", "project");
  assert.match(projectHtml, /Project index body/);
  assert.match(projectHtml, /data-knowledge-open="practice"/);
  assert.match(projectHtml, /文档链接/);
  assert.match(projectHtml, /Build it/);
  assert.doesNotMatch(projectHtml, /class="panel knowledge-tree"/);

  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  assert.match(source, /projectNavigationEntryIDs/);
  assert.match(source, /navigationIDs\.has\(item\.id\)/);
  assert.match(source, /knowledgeHomeBody/);
  assert.match(source, /knowledgeIndexSummary/);
  assert.doesNotMatch(source, /data-knowledge-search-open|data-knowledge-search-clear/);
  assert.match(source, /renderKnowledgeDocumentWorkspace\(filterEntries\(entries\), [^,]+, "project", false, sourceLabelFor/);
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(styles, /\.knowledge-document-layout\.linked-index \{[^}]*display:\s*block/);
  assert.match(styles, /\.markdown-body \.knowledge-index-link \{[^}]*display:\s*inline/);
});

test("knowledge libraries support pending browse, bind, and unbind", async () => {
  const root = resolve(import.meta.dirname, "..");
  const workspace = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  for (const marker of ["data-knowledge-library-open", "data-knowledge-library-bind", "data-knowledge-library-unbind", "data-knowledge-library-delete", "data-knowledge-library-back"]) {
    assert.match(workspace, new RegExp(marker));
  }
  assert.match(workspace, /bound_project_id/);
  assert.match(workspace, /renderKnowledgeDocumentWorkspace\(entries,[\s\S]*?true\)/);
  assert.match(api, /knowledge\/libraries/);
  assert.match(api, /bindKnowledgeLibrary/);
  assert.match(api, /unbindKnowledgeLibrary/);
  assert.match(api, /deleteKnowledgeLibrary/);
  assert.match(workspace, /data-project-knowledge-detach/);
  assert.match(workspace, /data-project-knowledge-delete/);
  assert.match(api, /detachProjectKnowledge/);
  assert.match(api, /deleteProjectKnowledge/);
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(styles, /\.knowledge-library-card > footer \{[^}]*flex-wrap:\s*wrap/);
  assert.match(styles, /\.knowledge-library-card > footer label \{[^}]*grid-template-columns:\s*max-content minmax\(180px,1fr\)[^}]*white-space:\s*nowrap/);
  assert.match(styles, /\.knowledge-library-card \{[^}]*grid-template-rows:\s*auto auto auto;[^}]*align-content:\s*start;[^}]*height:\s*auto/);
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.knowledge-library-card > header, \.knowledge-library-card > footer \{[^}]*justify-content:\s*flex-start;[^}]*height:\s*auto/);
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.knowledge-primary-rail > nav button \{[^}]*min-width:\s*max-content/);
});

test("project settings is a secondary page with a project entry back action", async () => {
  const root = resolve(import.meta.dirname, "..");
  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  assert.match(source, /function renderSettings[\s\S]*?data-knowledge-back[\s\S]*?knowledge-setting-card/);
  assert.match(source, /knowledgeReaderOpen\s*&&\s*projectMode\s*&&\s*activeSection\s*!==\s*"overview"/);
  assert.match(source, /activeSection\s*=\s*entry\s*===\s*"settings"\s*\?\s*"settings"/);
});

test("project knowledge keeps a merged view while exposing source labels and filters", async () => {
  const root = resolve(import.meta.dirname, "..");
  const module = await import(pathToFileURL(resolve(root, "dist", "knowledge_workspace.js")));
  const sources = module.projectKnowledgeSources(
    {id: "project-1", name: "Project One"},
    [
      {id: "own-1", project_id: "project-1", is_index: false},
      {id: "own-index", project_id: "project-1", is_index: true},
      {id: "external-1", project_id: "library-1", bound_project_id: "project-1", is_index: false},
    ],
    [{id: "binding-1", container_project_id: "library-1", bound_project_id: "project-1", name: "Imported Library"}],
  );
  assert.deepEqual(sources, [
    {id: "project-1", label: "项目自有知识", count: 1, external: false},
    {id: "library-1", label: "Imported Library（外部引用）", count: 1, external: true},
  ]);

  const source = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  for (const marker of ["data-knowledge-source-filter", "knowledge-source-badge", "knowledge-source-settings"]) assert.match(source, new RegExp(marker));
  assert.ok(source.includes("\\u9879\\u76ee\\u81ea\\u6709\\u77e5\\u8bc6"));
  assert.ok(source.includes("\\u7ed1\\u5b9a\\u77e5\\u8bc6\\u5e93"));
  assert.doesNotMatch(source, /boundLibraries\.length\s*\+\s*\(owned/);
  assert.match(styles, /\.knowledge-source-toolbar \{[^}]*display:\s*flex/);
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.knowledge-source-toolbar \{[^}]*flex-direction:\s*column/);
});

test("knowledge library bindings distinguish collaboration from external reference", async () => {
  const root = resolve(import.meta.dirname, "..");
  const workspace = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  for (const marker of ["data-knowledge-library-mode", "data-knowledge-library-mode-save", "bindingModeLabel"]) {
    assert.match(workspace, new RegExp(marker));
  }
  assert.ok(workspace.includes("\\u9879\\u76ee\\u534f\\u4f5c"));
  assert.ok(workspace.includes("\\u5916\\u90e8\\u5f15\\u7528"));
  assert.match(api, /binding_mode/);
  assert.match(workspace, /selected\.binding_mode === "project"/);
});

test("startup renders immediately and CRUD actions refresh local state with progress", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const index = await readFile(resolve(root, "dist", "index.html"), "utf8");
  const knowledge = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  assert.match(index, /boot-loading/);
  assert.doesNotMatch(index, /<script defer src="\/vendor\/xterm\.js/);
  assert.match(main, /state\.hydrating = true;\s*render\(\);\s*await loadCoreData\(\)/);
  assert.match(main, /async function loadCoreData/);
  assert.match(main, /async function ensureViewData/);
  assert.match(main, /loadResource\("knowledge"/);
  assert.doesNotMatch(main, /async function loadDeferredData/);
  assert.match(main, /state\.projects = state\.projects\.filter/);
  assert.match(main, /state\.workspaces = state\.workspaces\.filter/);
  assert.match(main, /state\.tasks = state\.tasks\.filter/);
  assert.match(main, /"保存中", false/);
  assert.match(knowledge, /aria-busy/);
  assert.match(knowledge, /icon\("spinner", true\)/);
  assert.match(knowledge, /context\.setMessage\("notice", message\);\s*context\.render\(\)/);
});

test("route resources load silently and only surface failures", async () => {
  const source = await readFile(resolve(import.meta.dirname, "..", "src", "main.ts"), "utf8");
  const start = source.indexOf("function resourceStatusHTML");
  const end = source.indexOf("\nfunction systemUptimeText", start);
  const implementation = source.slice(start, end);
  assert.match(implementation, /resourceStates\[key\]\.error/);
  assert.match(implementation, /resource-error/);
  assert.doesNotMatch(implementation, /resourceStates\[key\]\.loading/);
  assert.doesNotMatch(implementation, /正在加载/);
});

test("task creation supports manual draft and explicit start", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(main, /name="start_mode" value="manual"/);
  assert.match(main, /name="start_mode" value="immediate"/);
  assert.match(main, /id="start-task"/);
  assert.match(main, /task\.status === "draft"/);
  assert.match(main, /summary\?\.status === "draft"/);
  assert.match(main, /state\.taskConversation = page\?\.conversation\.items \|\| \[\]/);
  assert.match(main, /state\.taskContext = null/);
  assert.match(main, /detail\.task\.read_only \|\| detail\.task\.status === "draft"\) closeEvents/);
  assert.match(main, /state\.selectedTask\?\.task\.status === "draft"/);
  assert.match(main, /尚无 Context/);
  assert.match(main, /if \(state\.selectedTask\.task\.status === "draft"\) \{[\s\S]{0,160}state\.taskContext = null;[\s\S]{0,80}render\(\);[\s\S]{0,80}return;/);
  assert.match(api, /\/tasks\/\$\{encodeURIComponent\(id\)\}\/start/);
});

test("model detection streams catalog results and can be stopped", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(main, /new EventSource\(api\.modelDetectionEventsURL/);
  assert.match(main, /addEventListener\("catalog"/);
  assert.match(main, /addEventListener\("result"/);
  assert.match(main, /id="stop-model-detection"/);
  assert.match(main, /cancelModelDetectionJob/);
  assert.match(main, /replaceRegionHTML\(root,/);
  assert.match(main, /data-ui-key="model-detection-list"/);
  assert.match(main, /applyModelDetectionFilter/);
  assert.match(main, /session\.selected/);
  assert.match(main, /addEventListener\("result"[\s\S]{0,700}updateDetectedModelRow/);
  assert.match(main, /addEventListener\("progress"[\s\S]{0,500}updateModelDetectionProgress/);
  assert.match(main, /finishModelDetection[\s\S]{0,300}#stop-model-detection/);
  const resultHandler = main.slice(main.indexOf('source.addEventListener("result"'), main.indexOf('source.addEventListener("progress"'));
  const progressHandler = main.slice(main.indexOf('source.addEventListener("progress"'), main.indexOf('source.addEventListener("done"'));
  assert.doesNotMatch(resultHandler, /renderModelDetection/);
  assert.doesNotMatch(progressHandler, /renderModelDetection/);
  assert.match(api, /model-detection-jobs/);
});

test("same-page live renders preserve page interaction state", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const helpers = await readFile(resolve(root, "dist", "ui_helpers.js"), "utf8");
  assert.match(main, /const preservePageUI = app\.dataset\.uiScope === nextUIScope/);
  assert.match(main, /const previousAppUI = preservePageUI \? captureRegionUI\(app\) : null/);
  assert.match(main, /restoreRegionUI\(app, previousAppUI, false\)/);
  assert.match(main, /if \(preservePageUI\) window\.scrollTo\(0, previousWindowScroll\)/);
  assert.match(main, /else window\.scrollTo\(0, 0\)/);
  assert.match(main, /if \(appPointerActive\) \{\s*state\.renderPending = true/);
  assert.match(main, /if \(hasActiveTextEditor\(\)\) \{\s*state\.renderPending = true/);
  assert.match(main, /addEventListener\("focusout",\s*\(\)\s*=>\s*window\.setTimeout\(flushDeferredRender, 0\)/);
  assert.match(main, /function updateTaskLiveRegions[\s\S]{0,180}if \(appPointerActive\)/);
  assert.match(main, /document\.activeElement !== agentSelect/);
  assert.match(main, /const listPageVisible = state\.view === "projects" \|\| state\.view === "tasks"/);
  for (const marker of ["captureInteractiveRegion", "restoreInteractiveRegion", "focusKey", "selectionStart", "preserveScroll"]) {
    assert.match(helpers, new RegExp(marker));
  }
});

test("large catalogs use independent route resources and cursor pages", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(main, /const initialListPageSize = 50/);
  assert.match(main, /data-load-more=/);
  assert.match(main, /loadNextPage/);
  assert.match(api, /query\.set\("cursor"/);
  assert.match(api, /query\.set\("limit"/);
});

test("task list protects clicks from live refresh and reports open failures", async () => {
  const source = await readFile(resolve(import.meta.dirname, "..", "src", "main.ts"), "utf8");
  assert.match(source, /taskListPointerActive = true/);
  assert.match(source, /if \(taskListPointerActive\) state\.renderPending = true/);
  assert.match(source, /window\.setTimeout\(flushDeferredRender, 0\)/);
  assert.match(source, /openingTaskID = taskID/);
  assert.match(source, /setMessage\("error", error instanceof Error/);
  assert.match(source, /if \(listRefreshInFlight\) \{\s*listRefreshQueued = true/);
  assert.match(source, /do \{\s*listRefreshQueued = false/);
  assert.match(source, /while \(listRefreshQueued\)/);
});

test("task list filters combine multi-select project, status, and device choices", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  const filters = await import(pathToFileURL(resolve(root, "dist", "task_filters.js")));
  const local = {id: "local", project_id: "p1", status: "active", read_only: false};
  const remoteA = {id: "remote-a", project_id: "p1", status: "completed", read_only: true, owner_device_id: "device-a"};
  const remoteB = {id: "remote-b", project_id: "p2", status: "failed", read_only: true, owner_device_id: "device-b"};

  assert.equal(filters.taskDeviceFilterKey(local), "local");
  assert.equal(filters.taskDeviceFilterKey(remoteA), "remote:device-a");
  assert.deepEqual(filters.taskDeviceFilterOptions([local, remoteA, remoteB]), [
    {value: "local", label: "本机", count: 1},
    {value: "remote:device-a", label: "device-a", count: 1},
    {value: "remote:device-b", label: "device-b", count: 1},
  ]);
  assert.equal(filters.taskMatchesFilters(local, new Set(["p1"]), new Set(["active", "failed"]), new Set(["local"])), true);
  assert.equal(filters.taskMatchesFilters(remoteA, new Set(["p1"]), new Set(["active", "failed"]), new Set()), false);
  assert.equal(filters.taskMatchesFilters(remoteB, new Set(), new Set(["active", "failed"]), new Set(["remote:device-b"])), true);

  for (const marker of ["taskProjectFilters", "taskStatusFilters", "taskDeviceFilters", "data-task-filter-option", "data-task-filter-clear"]) {
    assert.match(main, new RegExp(marker));
  }
  assert.match(main, /taskDeviceFilters:\s*new Set\(\[\s*LOCAL_TASK_DEVICE_FILTER\s*\]\)/);
  assert.match(main, /taskMatchesFilters/);
  assert.match(styles, /\.task-filter-menu/);
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.task-filters \{[^}]*grid-template-columns:\s*repeat\(2,minmax\(0,1fr\)\)/);
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
  for (const label of ["\\u67e5\\u770b\\u8be6\\u60c5", "\\u6279\\u51c6\\u66f4\\u65b0", "\\u62d2\\u7edd"]) assert.ok(source.includes(label));
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.knowledge-proposal-actions \{[^}]*grid-template-columns:\s*repeat\(3,minmax\(0,1fr\)\)/);
  assert.match(styles, /\.knowledge-update-feed article \{[^}]*min-width:\s*0;[^}]*max-width:\s*100%;[^}]*overflow:\s*hidden/);
  assert.match(styles, /\.knowledge-proposal-full \.markdown-body \{[^}]*min-width:\s*0;[^}]*max-width:\s*100%;[^}]*overflow-wrap:\s*anywhere/);
});

test("sync settings put status first and omit the static domain explainer", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {bindSyncSettings, isSyncSettingsFormEditing, renderSyncSettings, syncPreviewMessage, syncSettingsPayload} = await import(pathToFileURL(resolve(root, "dist", "sync_settings.js")));
  const html = renderSyncSettings({scope: "default", enabled: false, endpoint: "", device_id: "", device_name: "device", interval_seconds: 300, token_configured: false, passphrase_configured: false}, {scope: "default", cursor: "", last_error: ""}, 0, [], {upserts: 3, deletes: 2, remote_upserts: 4, remote_deletes: 1, pending: 0, conflicts: 0}, {running: true, phase: "pulling", completed: 5, total: 10});
  assert.doesNotMatch(html, /sync-domain-grid|sync-dependency-note|界面按项目/);
  assert.ok(html.indexOf("同步状态") < html.indexOf("连接设置"));

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

test("sync settings render local connection data before remote preview", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const sync = await readFile(resolve(root, "dist", "sync_settings.js"), "utf8");
  const coreLoad = main.indexOf("api.syncSettings()", main.indexOf('view === "sync"'));
  const coreRendered = main.indexOf("render();", main.indexOf("state.syncConflicts = conflicts.conflicts || [];", coreLoad));
  const previewLoad = main.indexOf("await api.syncPreview()", coreLoad);
  assert.ok(coreLoad >= 0 && coreRendered > coreLoad && previewLoad > coreRendered);
  assert.doesNotMatch(main, /Promise\.all\(\[api\.syncSettings\(\), api\.syncStatus\(\), api\.syncConflicts\(\), api\.syncPreview\(\)\]\)/);
  assert.match(sync, /settings\.device_name\?\.trim\(\) \|\| settings\.device_id/);
  assert.match(main, /syncSettingsReady: false/);
  const settingsReady = main.indexOf("state.syncSettingsReady = true;", coreLoad);
  assert.ok(settingsReady > coreLoad && coreRendered > settingsReady && previewLoad > coreRendered);
  assert.match(sync, /settingsReady \? "" : "disabled"/);
  assert.match(sync, /正在读取本地设置/);
  assert.match(sync, /分析差异…/);
  assert.match(sync, /同步中…/);
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
