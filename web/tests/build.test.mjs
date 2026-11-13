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

    // A select whose saved value no longer exists among its options (captured
    // before the options loaded, or pointing at a removed entry) must fall back
    // to the freshly rendered default. Forcing the stale value leaves the select
    // at selectedIndex -1, which silently empties everything derived from it.
    const withSelect = (options, value) => {
      const root = new FakeElement("div");
      const select = new FakeElement("select", {id: "task-project", name: "project_id"});
      // Mirror the browser: options is a live list, value picks a matching
      // option, and a value with no match leaves selectedIndex at -1.
      select.options = options.map(option => ({value: option, selected: false}));
      Object.defineProperty(select, "value", {
        get() { return select.options[select.selectedIndex]?.value ?? ""; },
        set(next) {
          select.selectedIndex = select.options.findIndex(option => option.value === next);
          select.options.forEach((option, index) => { option.selected = index === select.selectedIndex; });
        },
      });
      select.selectedIndex = -1;
      select.value = value;
      root.append(select);
      return {root, select};
    };
    const capturedEmpty = withSelect([], "");
    const capturedState = helpers.captureInteractiveRegion(capturedEmpty.root);
    const repopulated = withSelect(["project-1", "project-2"], "");
    helpers.restoreInteractiveRegion(repopulated.root, capturedState);
    assert.equal(repopulated.select.value, "project-1", "a stale empty select value must adopt the new default");

    // A value that is still offered keeps winning over the default.
    const keptSource = withSelect(["project-1", "project-2"], "project-2");
    const keptState = helpers.captureInteractiveRegion(keptSource.root);
    const keptTarget = withSelect(["project-1", "project-2"], "project-1");
    helpers.restoreInteractiveRegion(keptTarget.root, keptState);
    assert.equal(keptTarget.select.value, "project-2", "a still-valid selection must be preserved");
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
  assert.match(styles, /\.task-channel-switcher label \{[^}]*margin:\s*0/);
  assert.match(styles, /dialog > \.dialog-head \{[^}]*padding:\s*18px 18px 0/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.provider-layout \.provider-row \{[^}]*grid-template-columns:\s*minmax\(0,1fr\) auto/);
  assert.match(styles, /@media \(max-width:\s*400px\)[\s\S]*?\.dialog-actions \{[^}]*grid-template-columns:\s*minmax\(0,1fr\)/);
  assert.match(styles, /\.models-table th:nth-child\(2\)/);
  assert.doesNotMatch(styles, /\n\s*th:nth-child\(2\),\s*td:nth-child\(2\)/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.task-draft-banner button \{[^}]*grid-column:\s*1 \/ -1/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.remote-readonly-actions \{[^}]*grid-column:\s*1 \/ -1/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.sync-domain-grid \{[^}]*grid-template-columns:\s*repeat\(2,minmax\(0,1fr\)\);[^}]*overflow:\s*visible/);
  assert.match(styles, /@media \(max-width:\s*760px\)[\s\S]*?\.hardware-group-row \{[^}]*grid-template-columns:\s*minmax\(0,1fr\) 34px/);
  assert.match(styles, /@media \(max-width:\s*360px\)[\s\S]*?\.agent-turn-row code \{[^}]*display:\s*none/);
  assert.match(styles, /@media \(max-width:\s*360px\)[\s\S]*?\.hardware-terminal-keys \{[^}]*grid-template-columns:\s*repeat\(4,minmax\(0,1fr\)\);[^}]*overflow:\s*visible/);
});

test("task channel selector explains and confirms occupied destination takeover", async () => {
  const main = await readFile(resolve(import.meta.dirname, "..", "src", "main.ts"), "utf8");
  assert.match(main, /const options = result\.destinations\.map\(item =>/);
  assert.match(main, /const occupied = Boolean\(item\.target_task_id && item\.target_task_id !== taskID\)/);
  assert.match(main, /item\.target_task_code \|\| item\.target_task_name \|\| "其他 Task"/);
  assert.match(main, /将从 \$\{occupiedBy\} 接管/);
  assert.match(main, /const takeover = Boolean\(destination\?\.target_task_id/);
  assert.match(main, /连接后会从原 Task 自动解绑并接管/);
  assert.match(main, /Task 主渠道已连接，原 Task 已自动解绑/);
  assert.match(main, /result\.destinations\.length \? "" : "disabled"/);
  assert.doesNotMatch(main, /const available = result\.destinations\.filter/);
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
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const active = module.renderChannels([], [instance], context);
  assert.match(active, /data-channel-reset-binding="channel-1"/);
  assert.match(active, /data-channel-archive="channel-1"/);
  assert.match(active, /data-channel-settings-open="channel-1"/);
  assert.match(active, /data-channel-conversations-open="channel-1"/);
  assert.match(active, />设置</);
  assert.match(active, />会话</);
  assert.match(active, /更新权限/);
  assert.match(active, /权限更新仅做增量授权/);
  assert.match(active, /AHA 运行时菜单可独立勾选/);
  assert.doesNotMatch(active, /data-channel-activity|data-channel-knowledge="|Owner 收件箱与投递|机器人显示名称|runtime_bot_display_name_override/);
  assert.doesNotMatch(active, /data-channel-credentials/);
  assert.match(active, /<dt>App ID<\/dt>/);
  assert.match(active, /channel-instance-actions/);
  assert.match(styles, /\.channel-instance-actions \{[^}]*grid-template-columns:\s*repeat\(auto-fit,minmax\(86px,1fr\)\)/);
  assert.doesNotMatch(active, /人工整理并共享|channel-record-promote/);

  const combinedContext = {
    ...context,
    models: [
      {id: "model-codex", display_name: "GPT-5", provider_name: "Codex", source: "official", backend: "codex", wire_model: "gpt-5", provider_id: "official"},
      {id: "model-provider", display_name: "Claude", provider_name: "Provider", source: "provider", backend: "claude", wire_model: "claude", provider_id: "provider"},
    ],
    accounts: [
      {id: "account-a", label: "账号 A", status: "ready", credential_configured: true, available_models: [{wire_model: "gpt-5", display_name: "GPT-5"}]},
      {id: "account-b", label: "账号 B", status: "ready", credential_configured: true, available_models: [{wire_model: "gpt-5", display_name: "GPT-5"}]},
    ],
  };
  const combinedSelect = module.channelRuntimeOptions({runtime_default: {model_id: "model-codex", codex_account_id: "account-b"}}, combinedContext);
  assert.match(combinedSelect, /自动选择可用模型/);
  assert.match(combinedSelect, /Codex \/ GPT-5/);
  assert.match(combinedSelect, /Provider \/ Claude/);
  assert.match(combinedSelect, /账号 A/);
  assert.match(combinedSelect, /data-codex-account-id="account-b" selected/);
  assert.equal((combinedSelect.match(/ selected/g) || []).length, 1);

  const legacySelect = module.channelRuntimeOptions({
    runtime_default: {model_id: "", codex_account_id: ""},
    runtime_assistant_dm: {inherit: true, model_id: "model_stale", codex_account_id: "acct_stale"},
    runtime_group_digital_human: {model_id: "model_stale"},
  }, context);
  assert.doesNotMatch(legacySelect, /model_stale|assistant_model_id|group_model_id/);
  assert.match(legacySelect, /自动选择可用模型/);

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
  assert.match(source, /channelOnboardingOptions/);
  assert.match(source, /showOnboardingSetup/);
  assert.match(source, /channel_permission_id/);
  assert.doesNotMatch(source, /name="channel_runtime_menu"/);
  assert.match(source, /已开启权限不能在扫码中撤销/);
  assert.match(source, /resetChannelRuntimeMenus/);
  assert.match(source, /重置菜单/);
  assert.match(source, /openChannelSettings/);
  assert.match(source, /openChannelConversations/);
  assert.match(source, /api\.channelConversations/);
  assert.match(source, /channel-conversation-card/);
  assert.match(source, /绑定任务/);
  assert.match(source, /最近入站/);
  assert.match(source, /最近出站/);
  assert.doesNotMatch(source, /data-channel-activity|data-channel-knowledge="|Owner 收件箱与投递/);
  assert.doesNotMatch(source, /runtime_bot_display_name_override|机器人显示名称|data-channel-settings="/);
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
  assert.match(main, /state\.projects\.filter\(item => !item\.channel_retired && !isSystemProject\(item\)\)/);
});

test("channel reauthorization explains menu preservation and separate creation", async () => {
  const channels = await readFile(resolve(import.meta.dirname, "..", "dist", "channels.js"), "utf8");
  assert.match(channels, /initial.mode === "existing_app"/);
  assert.match(channels, /不修改已有菜单/);
  assert.match(channels, /AHA 运行时菜单可独立勾选，不修改飞书后台已有菜单/);
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
      dsh: {status: "not_installed"},
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
      dsh: {status: "ready", version: "0.2.1-rc.2"},
    },
    repository: {status: "ready", is_git: true},
  });
  assert.match(versions, /Codex · codex-cli 0\.153\.4/);
  assert.match(versions, /Claude · 2\.1\.0/);
  assert.match(versions, /DSH · 0\.2\.1-rc\.2/);

  const agentAPI = renderWorkspaceDetection({
    ...base,
    agent_api_status: "ready",
    agent_api_resolved_url: "http://172.28.0.1:8766",
    capabilities: {workspace: {status: "ready"}, agent_api: {status: "ready", url: "http://172.28.0.1:8766"}},
    repository: {status: "ready", is_git: true},
  });
  assert.match(agentAPI, /Agent API · 直连 http:\/\/172\.28\.0\.1:8766/);

  const agentAPITunnel = renderWorkspaceDetection({
    ...base,
    agent_api_status: "ready",
    agent_api_resolved_url: "http://127.0.0.1:8766",
    capabilities: {workspace: {status: "ready"}, agent_api: {status: "ready", url: "http://127.0.0.1:8766", via: "tunnel"}},
    repository: {status: "ready", is_git: true},
  });
  assert.match(agentAPITunnel, /Agent API · SSH 反向隧道已连通/);

  const agentAPIFailed = renderWorkspaceDetection({
    ...base,
    agent_api_status: "error",
    agent_api_error: "反向连接超时",
    capabilities: {workspace: {status: "ready"}, agent_api: {status: "unavailable", error: "反向连接超时"}},
    repository: {status: "ready", is_git: true},
  });
  assert.match(agentAPIFailed, /反向连接超时/);
});

test("Agent API is automatic and read-only in the normal Workspace UI", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");

  // The normal Workspace dialog shows only the selected result. Mode and URL
  // controls are gone because choosing either by hand is what broke remote
  // workspaces once they left the LAN.
  assert.doesNotMatch(script, /ws-agent-api-mode/);
  assert.doesNotMatch(script, /ws-agent-api-url/);
  assert.doesNotMatch(script, /测试连接/);
  assert.doesNotMatch(script, /Agent API 反向连接/);
  assert.match(script, /workspace-agent-api-status/);
  assert.match(styles, /\.workspace-agent-api-status-block/);

  // The hidden manual override lives in advanced settings and is explicitly a
  // troubleshooting path. It accepts only https/loopback (enforced server-side).
  assert.match(script, /agent-api-override-form/);
  assert.match(script, /Agent API 高级覆盖/);
  assert.match(script, /api\.updateAgentAPISettings/);
  assert.doesNotMatch(script, /allow_insecure/);
  assert.doesNotMatch(script, /agent-api-security-form/);

  // Automatic detection still exists; it is what produces the read-only status.
  assert.match(script, /detectWorkspaceWithHostKeyTrust/);
  assert.match(script, /首次连接 SSH Workspace/);
  assert.match(api, /workspaces\/.*\/host-key\/trust/);
  assert.match(script, /agent_api_status === "ready"/);
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

test("list refresh scheduler coalesces bursts, stays single-flight, and defers off-screen", async () => {
  // (The test body below is async so resolved refresh promises can settle
  // between simulated timer advances.)
  const root = resolve(import.meta.dirname, "..");
  const {createListRefreshScheduler} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));

  let now = 0;
  let nextHandle = 1;
  const timers = new Map();
  let allowed = true;
  let inFlight = false;
  let runs = 0;
  let finishRun = () => {};
  const schedule = (callback, delay) => {
    const handle = nextHandle++;
    timers.set(handle, {callback, at: now + delay});
    return handle;
  };
  const advance = ms => {
    now += ms;
    for (let changed = true; changed;) {
      changed = false;
      for (const [handle, timer] of [...timers]) {
        if (timer.at <= now) {
          timers.delete(handle);
          timer.callback();
          changed = true;
          break;
        }
      }
    }
  };
  // Let the refresh promise chain (catch/finally) settle before the next timer.
  const settle = () => new Promise(resolve => setTimeout(resolve, 0));
  const scheduler = createListRefreshScheduler({
    debounceMs: 350,
    maxWaitMs: 1000,
    now: () => now,
    schedule,
    cancel: handle => timers.delete(handle),
    canRefresh: () => allowed,
    isInFlight: () => inFlight,
    refresh: () => {
      runs++;
      inFlight = true;
      return new Promise(resolve => {
        finishRun = () => { inFlight = false; resolve(); };
      });
    },
  });

  // Four events inside the debounce window produce one trailing request.
  scheduler.request();
  advance(80);
  scheduler.request();
  advance(80);
  scheduler.request();
  advance(80);
  scheduler.request();
  assert.equal(runs, 0);
  advance(349);
  assert.equal(runs, 0, "the trailing window must expire before refreshing");
  advance(1);
  assert.equal(runs, 1);

  // Events during the in-flight request stay pending; the next request waits
  // for the first to settle and never overlaps it.
  advance(50);
  scheduler.request();
  scheduler.request();
  advance(400);
  assert.equal(runs, 1, "only one list request may be in flight");
  finishRun();
  await settle();
  advance(350);
  assert.equal(runs, 2);
  finishRun();
  await settle();

  // A hidden page (canRefresh false) keeps the pending event instead of sending.
  allowed = false;
  advance(50);
  scheduler.request();
  advance(1500);
  assert.equal(runs, 2, "a hidden page must not send list requests");
  assert.equal(scheduler.pending(), true);
  allowed = true;
  scheduler.flush();
  assert.equal(runs, 3);
  finishRun();
  assert.equal(scheduler.pending(), false);

  // A continuous burst cannot postpone the first refresh forever: the maximum
  // wait fires it even though each new event would reset the trailing timer.
  let bursts = 0;
  const maxWaitScheduler = createListRefreshScheduler({
    debounceMs: 350,
    maxWaitMs: 1000,
    now: () => now,
    schedule,
    cancel: handle => timers.delete(handle),
    canRefresh: () => true,
    isInFlight: () => false,
    refresh: () => { bursts++; return Promise.resolve(); },
  });
  maxWaitScheduler.request();
  for (let i = 0; i < 9; i++) {
    advance(100);
    maxWaitScheduler.request();
  }
  assert.equal(bursts, 0);
  advance(100);
  maxWaitScheduler.request();
  advance(0);
  assert.equal(bursts, 1, "max wait caps a continuous burst");
});

test("continuous SSE stream is bounded by maxWait instead of the debounce gap", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {createListRefreshScheduler} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));
  let now = 0;
  let nextHandle = 1;
  const timers = new Map();
  let runs = 0;
  const schedule = (callback, delay) => {
    const handle = nextHandle++;
    timers.set(handle, {callback, at: now + delay});
    return handle;
  };
  const advance = ms => {
    now += ms;
    for (let changed = true; changed;) {
      changed = false;
      for (const [handle, timer] of [...timers]) {
        if (timer.at <= now) {
          timers.delete(handle);
          timer.callback();
          changed = true;
          break;
        }
      }
    }
  };
  const scheduler = createListRefreshScheduler({
    debounceMs: 350,
    maxWaitMs: 1000,
    now: () => now,
    schedule,
    cancel: handle => timers.delete(handle),
    canRefresh: () => true,
    isInFlight: () => false,
    refresh: () => { runs++; return Promise.resolve(); },
  });

  // 20 events, one every 100ms: the first refresh fires at the 1000ms cap, the
  // next window likewise, so two seconds of steady events produce two requests
  // rather than one every ~350ms.
  for (let i = 0; i < 20; i++) {
    if (i > 0) advance(100);
    scheduler.request();
  }
  assert.equal(runs, 1, "the first max-wait refresh fires at 1000ms");
  advance(100);
  assert.equal(runs, 2, "the steady stream is capped by maxWait, not the debounce gap");
  advance(1000);
  assert.equal(runs, 2, "no extra refresh after the stream stops");
});

test("resource loader dedupes cached and in-flight requests before counting them", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {createResourceLoader} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));
  const states = {mcp: {loading: false, loaded: false, error: "", updatedAt: 0}};
  let started = 0;
  const load = createResourceLoader({
    states,
    requests: new Map(),
    onStart: () => { started++; },
  });
  let release = () => {};
  const pendingAction = () => new Promise(resolve => { release = resolve; });
  const first = load("mcp", false, pendingAction);
  const second = load("mcp", false, pendingAction);
  assert.equal(first, second, "concurrent loads share the in-flight request");
  assert.equal(started, 1, "the in-flight dedupe must happen before counting");
  release();
  await first;
  assert.equal(states.mcp.loaded, true);
  await load("mcp", false, pendingAction);
  assert.equal(started, 1, "a loaded resource is not requested again");
  await load("mcp", true, async () => {});
  assert.equal(started, 2, "a forced reload issues one real request");

  // A failed request is not cached as loaded, so a later prefetch may retry it
  // and the counter still reflects each real attempt.
  const failingStates = {data: {loading: false, loaded: false, error: "", updatedAt: 0}};
  let dataStarted = 0;
  const loadData = createResourceLoader({
    states: failingStates,
    requests: new Map(),
    onStart: () => { dataStarted++; },
  });
  await loadData("data", false, async () => { throw new Error("boom"); });
  assert.equal(failingStates.data.loaded, false);
  assert.equal(failingStates.data.error, "boom");
  await loadData("data", false, async () => {});
  assert.equal(dataStarted, 2, "the retry is a real request and is counted");
});

test("startup prefetch is idle-only and never chains secondary views", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(script, /function prefetchSecondaryData\(\)/);
  assert.match(script, /function cancelSecondaryPrefetch\(\)/);
  assert.match(script, /requestIdleCallback/);
  assert.match(script, /requestIdle\(run, \{\s*timeout: 2500\s*\}\)/);
  assert.match(script, /secondaryPrefetchTimer = window\.setTimeout\(run, 1200\)/);
  assert.doesNotMatch(script, /ensureViewData\("models"\)\.then\(\(\)=>ensureViewData\("channels"\)\)/);
  assert.doesNotMatch(script, /ensureViewData\("channels"\)\)\.then\(\(\)=>ensureViewData\("knowledge"\)\)/);
  assert.match(script, /ensureViewData\(state\.view\)/);
  assert.match(script, /!state\.auth\?\.authenticated \|\| userInteracted \|\| state\.selectedTask \|\| openingTaskID/);
  assert.match(script, /window\.addEventListener\("pointerdown", noteUserInteraction/);
  assert.match(script, /__ahaPerf/);
  // Every view resource must pass through the deduping loader, otherwise the
  // idle prefetch repeats it even though it is already on screen.
  assert.match(script, /loadResource\("mcp", force/);
  assert.match(script, /loadResource\("data", force/);
  assert.match(script, /"prompts",\s*"mcp",\s*"data"/);
  assert.match(script, /prefetchRequestTracking > 0\) perfMetrics\.prefetchRequests\+\+/);
});

test("opt-in perf panel reports timing metrics with copy and close controls", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  // Only the persistent preference (or `?perf=1`) reaches the panel; a normal
  // visit leaves the sample buffer null and never creates DOM.
  assert.match(script, /if \(perfEnabled\) startPerfPanel\(\)/);
  assert.match(script, /if \(!perfMetrics\.apiSamples \|\| document\.getElementById\("perf-panel"\)\) return/);
  assert.match(script, /panel\.id = "perf-panel"/);
  assert.match(script, /panel\.setAttribute\("role", "status"\)/);
  assert.match(script, /panel\.setAttribute\("aria-live", "off"\)/);
  assert.match(script, /hideButton\.type = "button"/);
  assert.match(script, /hideButton\.textContent = "关闭性能面板"/);
  assert.match(script, /hideButton\.setAttribute\("aria-label", "关闭性能面板"\)/);
  assert.match(script, /copyButton\.type = "button"/);
  assert.match(script, /copyButton\.textContent = "复制"/);
  // Averages come from TotalMs / count, falling back to 0 without samples.
  assert.match(script, /Math\.round\(perfMetrics\.listRefreshTotalMs \/ perfMetrics\.listRefreshRequests\) : 0/);
  assert.match(script, /Math\.round\(perfMetrics\.taskOpenTotalMs \/ perfMetrics\.taskOpenCount\) : 0/);
  // Copy uses the async clipboard and degrades to selecting the values.
  assert.match(script, /navigator\.clipboard/);
  assert.match(script, /console\.warn\(`\[aha-perf\] clipboard unavailable/);
  assert.match(script, /range\.selectNodeContents\(rows\)/);
  assert.match(script, /window\.setInterval\(\(\)=>\{/);
  // 0ms means "not measured yet"; the panel shows a placeholder until a real
  // sample arrives, in both the visible row and the copied text.
  assert.match(script, /perfMetrics\.firstScreenMs > 0 \? `\$\{perfMetrics\.firstScreenMs\}ms` : "—"/);
  assert.match(script, /`首屏: \$\{firstScreen\}`/);
  // Copy reports success/failure briefly, replaces stale timers on repeat clicks,
  // and clears the feedback timer when the panel is hidden.
  assert.match(script, /showCopyFeedback\("已复制"\)/);
  assert.match(script, /showCopyFeedback\("已选中"\)/);
  // A clipboard promise can settle after the user hid the panel; bail out before
  // touching the detached button or arming another timer.
  assert.match(script, /const showCopyFeedback = \(label\)=>\{\s*if \(!panel\.isConnected\) return;/);
  // The degraded path must also bail out before touching the page selection:
  // selecting the detached panel rows would otherwise wipe the user's selection.
  assert.match(script, /const fallbackCopy = \(text\)=>\{\s*if \(!panel\.isConnected\) return;/);
  // When the selection API is unavailable the fallback cannot highlight text,
  // but the button still reports failure instead of doing nothing.
  assert.match(script, /if \(!selection\) \{\s*showCopyFeedback\("复制失败"\);\s*return;\s*\}/);
  assert.match(script, /if \(perfPanelFeedbackTimer !== undefined\) window\.clearTimeout\(perfPanelFeedbackTimer\);\s*copyButton\.textContent = label;/);
  assert.match(script, /perfPanelFeedbackTimer = window\.setTimeout\(\(\)=>\{\s*perfPanelFeedbackTimer = undefined;\s*copyButton\.textContent = "复制";\s*\}, 1200\);/);
  // Closing the panel stops both timers, removes the DOM, and disables the
  // observer/buffer through the shared opt-out function.
  assert.match(script, /hideButton\.addEventListener\("click", \(\)=>disablePerfPanel\(\)\)/);
  assert.match(script, /function disablePerfPanel\(\) \{[\s\S]{0,400}writePerfPanelPreference\(false\);[\s\S]{0,200}setAPIRequestObserver\(null\);[\s\S]{0,200}perfMetrics\.apiSamples = null;[\s\S]{0,400}document\.getElementById\("perf-panel"\)\?\.remove\(\);/);
  // The panel must clear the assistant/banner layer (60) without covering the
  // native top-layer dialogs.
  assert.match(styles, /\.perf-panel \{[^}]*z-index: 70;/);
  assert.doesNotMatch(styles, /\.perf-panel \{[^}]*z-index: 50;/);
  // Desktop keeps the original corner; from the navigation's own breakpoint up
  // the fixed panel must clear the bottom navigation instead of covering it.
  assert.match(styles, /\.perf-panel \{[^}]*bottom: 16px;/);
  // Resolve the media rule that actually wraps a selector, so the panel offset
  // and `.bottom-nav` can be compared without assuming rule order.
  const mediaRuleFor = (selector) => {
    for (const match of styles.matchAll(/@media \(max-width: (\d+)px\) \{/g)) {
      let depth = 1;
      let index = match.index + match[0].length;
      const bodyStart = index;
      while (index < styles.length && depth > 0) {
        if (styles[index] === "{") depth++;
        else if (styles[index] === "}") depth--;
        index++;
      }
      const body = styles.slice(bodyStart, index);
      if (body.includes(selector)) return { width: match[1], body };
    }
    return undefined;
  };
  const perfPanelMedia = mediaRuleFor(".perf-panel {");
  // 641-760px still shows `.bottom-nav`; the panel must follow the same breakpoint.
  assert.equal(perfPanelMedia?.width, "760");
  assert.equal(perfPanelMedia?.width, mediaRuleFor(".bottom-nav {")?.width);
  // Without a bottom navigation (login/loading) the narrow panel keeps the
  // regular 16px corner instead of leaving an empty nav-height gap.
  assert.match(perfPanelMedia.body, /\.perf-panel \{[^}]*bottom: 16px;/);
  const withoutNavScope = perfPanelMedia.body.replace(/body:has\(\.bottom-nav\) \.perf-panel \{[^}]*\}/g, "");
  assert.doesNotMatch(withoutNavScope, /\.perf-panel \{[^}]*bottom: calc/);
  // The shell renders `.bottom-nav`; only then is the panel lifted by its height.
  assert.match(perfPanelMedia.body, /body:has\(\.bottom-nav\) \.perf-panel \{[^}]*bottom: calc\(var\(--mobile-bottom-nav-height\) \+ 8px\);/);
  assert.doesNotMatch(styles, /@media \(max-width: 640px\) \{ \.perf-panel \{[^}]*bottom: 8px;/);
  assert.doesNotMatch(styles, /@media \(max-width: 640px\) \{ \.perf-panel \{[^}]*bottom: calc/);
});

test("API request timings are opt-in, query-free, and bounded in the perf panel", async () => {
  const root = resolve(import.meta.dirname, "..");
  const apiModule = await import(pathToFileURL(resolve(root, "dist", "api.js")));
  const originalFetch = globalThis.fetch;
  const samples = [];
  const requested = [];
  globalThis.fetch = async (path, options = {}) => {
    requested.push({path, options});
    return new Response(JSON.stringify({ok: true}), {status: 200, headers: {"content-type": "application/json"}});
  };
  try {
    // Without an observer nothing is collected; this is the normal-page path.
    apiModule.setAPIRequestObserver(null);
    await apiModule.api.request("/api/v1/tasks?setup_token=secret-token");
    assert.equal(samples.length, 0, "timing stays off until main installs the observer");

    apiModule.setAPIRequestObserver(sample => samples.push(sample));
    await apiModule.api.request("/api/v1/tasks?setup_token=secret-token&filename=private.pdf", {method: "POST", body: JSON.stringify({password: "secret"})});
    await apiModule.api.login({username: "owner", password: "secret"});

    assert.equal(samples.length, 2);
    assert.deepEqual(
      {method: samples[0].method, path: samples[0].path, status: samples[0].status, ok: samples[0].ok},
      {method: "POST", path: "/api/v1/tasks", status: 200, ok: true},
    );
    // Auth endpoints keep only the path; the login body never enters a sample.
    assert.equal(samples[1].method, "POST");
    assert.equal(samples[1].path, "/api/v1/auth/login");
    assert.ok(samples.every(sample => sample.durationMs >= 0));
    const serialized = JSON.stringify(samples);
    for (const secret of ["secret-token", "private.pdf", "password", "owner"]) {
      assert.doesNotMatch(serialized, new RegExp(secret));
    }
  } finally {
    apiModule.setAPIRequestObserver(null);
    globalThis.fetch = originalFetch;
  }

  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  // The startup decision (URL shortcut or stored preference) is the only path
  // that constructs the panel and the section without going through the switch.
  assert.match(script, /if \(perfEnabled\) startPerfPanel\(\)/);
  assert.match(script, /slowTitle\.textContent = "慢请求 Top 5"/);
  assert.match(script, /panel\.append\(head, rows, slowSection, copyButton\)/);
  // Only the opt-in path allocates the sample buffer and installs the observer.
  assert.match(script, /apiSamples: perfEnabled \? \[\] : null/);
  assert.match(script, /if \(perfEnabled\) setAPIRequestObserver\(recordAPIRequest\)/);
  // The ring buffer is capped at 200 while total and slowest stay lifetime-wide.
  assert.match(script, /samples\.length > apiSampleLimit/);
  assert.match(script, /samples\.splice\(0, samples\.length - apiSampleLimit\)/);
  assert.match(script, /apiRequestCount\+\+/);
  assert.match(script, /`API 请求: \$\{perfMetrics\.apiRequestCount\} 次`/);
  assert.match(script, /`最慢请求: \$\{slowestText\}`/);
  assert.match(script, /`慢请求 Top 5:/);
  assert.match(styles, /\.perf-slow-requests/);
  assert.match(styles, /\.perf-slow-top/);
});

test("perf panel preference persists across loads and degrades safely", async () => {
  const root = resolve(import.meta.dirname, "..");
  const perf = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));
  const store = new Map();
  const storage = {
    getItem: key => store.has(key) ? store.get(key) : null,
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: key => store.delete(key),
  };
  // A fresh browser starts off; the ?perf=1 shortcut writes the preference and
  // the next load without the query parameter reads it back as on.
  assert.equal(perf.readPerfPanelPreference(storage), false);
  perf.writePerfPanelPreference(true, storage);
  assert.equal(store.get(perf.PERF_PANEL_STORAGE_KEY), "1");
  assert.equal(perf.readPerfPanelPreference(storage), true);
  // Closing is a real opt-out: the key is removed, not just the DOM.
  perf.writePerfPanelPreference(false, storage);
  assert.equal(store.has(perf.PERF_PANEL_STORAGE_KEY), false);
  assert.equal(perf.readPerfPanelPreference(storage), false);
  // Privacy modes can throw on every storage operation; reads mean "off" and
  // writes must not break boot or the in-page toggle.
  const broken = {
    getItem() { throw new Error("storage denied"); },
    setItem() { throw new Error("storage denied"); },
    removeItem() { throw new Error("storage denied"); },
  };
  assert.equal(perf.readPerfPanelPreference(broken), false);
  assert.doesNotThrow(() => perf.writePerfPanelPreference(true, broken));
  assert.equal(perf.readPerfPanelPreference(null), false);
  assert.doesNotThrow(() => perf.writePerfPanelPreference(true, null));
});

test("perf panel starts, closes, and toggles through advanced settings", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  // `?perf=1` writes the preference before the URL cleanup; startup then treats
  // either the shortcut or the stored preference as enabled.
  assert.match(script, /if \(perfRequestedByURL\) writePerfPanelPreference\(true\)/);
  assert.match(script, /const perfEnabled = perfRequestedByURL \|\| readPerfPanelPreference\(\)/);
  // Closing writes the preference off, drops the observer and sample buffer,
  // stops the timers, and removes the panel DOM.
  assert.match(script, /function disablePerfPanel\(\) \{[\s\S]{0,500}writePerfPanelPreference\(false\);[\s\S]{0,200}setAPIRequestObserver\(null\);[\s\S]{0,200}perfMetrics\.apiSamples = null;[\s\S]{0,300}window\.clearInterval\(perfPanelTimer\);[\s\S]{0,300}document\.getElementById\("perf-panel"\)\?\.remove\(\);/);
  assert.match(script, /hideButton\.addEventListener\("click", \(\)=>disablePerfPanel\(\)\)/);
  // The advanced → other settings switch applies immediately by reusing the
  // same enable path instead of reloading and losing the page under diagnosis.
  assert.match(script, /if \(state\.view === "other"\) bindOtherSettingsPage\(\)/);
  assert.match(script, /id="perf-panel-setting" name="perf_panel" type="checkbox"/);
  assert.match(script, /<strong>显示性能面板<\/strong><small>开启后显示 API\/首屏耗时面板；关闭后立即隐藏。<\/small>/);
  assert.match(script, /function enablePerfPanel\(\) \{[\s\S]{0,400}writePerfPanelPreference\(true\);[\s\S]{0,200}setAPIRequestObserver\(recordAPIRequest\);[\s\S]{0,200}startPerfPanel\(\);/);
  assert.match(script, /if \(toggle\.checked\) enablePerfPanel\(\);\s*else disablePerfPanel\(\);/);
  assert.match(script, /<span id="perf-panel-status">/);
  // When storage is unavailable but `?perf=1` still rendered the panel, the
  // switch reflects the visible panel instead of the missing preference.
  assert.match(script, /function isPerfPanelOn\(\) \{\s*return Boolean\(document\.getElementById\("perf-panel"\)\) \|\| readPerfPanelPreference\(\);/);
  assert.match(script, /const perfPanelOn = isPerfPanelOn\(\)/);
  // A normal visit still allocates no samples, installs no observer, and builds
  // no panel; only an enabled startup or the switch reaches these paths.
  assert.match(script, /apiSamples: perfEnabled \? \[\] : null/);
  assert.match(script, /if \(perfEnabled\) setAPIRequestObserver\(recordAPIRequest\)/);
  assert.match(script, /if \(perfEnabled\) startPerfPanel\(\)/);
});

test("perf panel English copy matches split text nodes after trimming", async () => {
  const root = resolve(import.meta.dirname, "..");
  const i18n = await import(pathToFileURL(resolve(root, "dist", "i18n.js")));
  const previous = {
    document: globalThis.document,
    NodeFilter: globalThis.NodeFilter,
    Element: globalThis.Element,
    DocumentFragment: globalThis.DocumentFragment,
  };
  // The settings help is rendered as two text nodes split by <code>?perf=1</code>.
  // translateTextNode trims each node before lookup, so the legacy keys must not
  // carry the surrounding whitespace. Walk the real translateUI path with a
  // minimal text-only tree and confirm both nodes become English.
  const first = {nodeValue: "偏好只保存在当前浏览器；带 ", parentElement: {closest: () => null}};
  const second = {nodeValue: " 打开页面也会自动开启，不会同步到服务端。", parentElement: {closest: () => null}};
  const nodes = [first, second];
  let cursor = -1;
  globalThis.NodeFilter = {SHOW_TEXT: 4};
  globalThis.Element = class Element {};
  globalThis.DocumentFragment = class DocumentFragment {};
  globalThis.document = {
    documentElement: {lang: "", dataset: {}},
    body: null,
    createTreeWalker: () => ({
      get currentNode() { return nodes[cursor]; },
      nextNode() { cursor++; return cursor < nodes.length; },
    }),
  };
  try {
    i18n.applyLocale("en-US");
    i18n.translateUI({});
    // Whitespace around each trimmed node is preserved, so the sentence still
    // reads correctly around the inline code element.
    assert.equal(first.nodeValue, "The preference stays in this browser only; opening the page with ");
    assert.equal(second.nodeValue, " also turns it on. It is never synced to the server.");
    assert.doesNotMatch(`${first.nodeValue}?perf=1${second.nodeValue}`, /\p{Script=Han}/u);
    // tr() uses the same trimmed lookup, which pins the key format directly.
    assert.equal(i18n.tr("偏好只保存在当前浏览器；带 "), "The preference stays in this browser only; opening the page with");
    assert.equal(i18n.tr(" 打开页面也会自动开启，不会同步到服务端。"), "also turns it on. It is never synced to the server.");
  } finally {
    i18n.applyLocale("zh-CN");
    globalThis.document = previous.document;
    globalThis.NodeFilter = previous.NodeFilter;
    globalThis.Element = previous.Element;
    globalThis.DocumentFragment = previous.DocumentFragment;
  }
});

test("mail phase two keeps navigation stable, one mail entry, and table tools", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const taskTools = await readFile(resolve(root, "dist", "task_tools.js"), "utf8");
  const mailCards = await readFile(resolve(root, "dist", "mail_cards.js"), "utf8");
  const mailResources = await readFile(resolve(root, "dist", "mail_resources.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");

  // perf=1 turns the persistent preference on at boot, then only the perf
  // parameter is removed while the hash and other parameters stay; the stored
  // preference keeps the panel open on later loads.
  assert.match(script, /const initialSearchParams = new URLSearchParams\(window\.location\.search\)/);
  assert.match(script, /if \(perfRequestedByURL\) writePerfPanelPreference\(true\)/);
  assert.match(script, /const perfEnabled = perfRequestedByURL \|\| readPerfPanelPreference\(\)/);
  assert.match(script, /if \(initialSearchParams\.has\("perf"\)\) \{[\s\S]{0,160}url\.searchParams\.delete\("perf"\)[\s\S]{0,160}window\.history\.replaceState/);
  assert.match(script, /`\$\{url\.pathname\}\$\{url\.search\}\$\{url\.hash\}`/);
  assert.match(script, /if \(perfEnabled\) startPerfPanel\(\)/);
  // Resource tabs are display-only and are not part of navigation restore.
  assert.doesNotMatch(script, /history\.replaceState\(null, "", `#resources\?tab=\$\{tab\}`\)/);
  assert.doesNotMatch(script, /resourcesTab: state\.resourcesTab/);
  assert.doesNotMatch(script, /mailSubTab: state\.mailSubTab/);
  assert.doesNotMatch(script, /saved\.resourcesTab/);
  assert.doesNotMatch(script, /saved\.mailSubTab/);
  // Legacy #hardware redirect and the #resources?tab=mail deep-link read remain.
  assert.match(script, /if \(name === "hardware"\) \{[\s\S]{0,120}replaceState\(null, "", "#resources\?tab=hardware"\)/);
  assert.match(script, /params\.get\("tab"\) === "mail" \? "mail" : "hardware"/);
  // The header mail icon and the timeline action binding are gone.
  assert.doesNotMatch(script, /taskSendMailButton/);
  assert.doesNotMatch(script, /id="task-send-mail"/);
  assert.doesNotMatch(script, /bindMailCardActions\(list/);
  // The mail tool is the single entry and owns the pending badge.
  assert.match(taskTools, /tool\.id === "mail" \? Math\.max\(0, Number\(options\.pendingMail\) \|\| 0\)/);
  assert.match(script, /pendingMail: taskMailPendingCount\(\)/);
  assert.match(script, /function syncTaskMailIndicator\(\)/);
  assert.match(script, /syncTaskMailIndicator\(\);/);
  // The timeline no longer renders mail cards at all; mail lives only in the
  // task mail tool panel.
  assert.doesNotMatch(mailCards, /mailConversationCardHtml|mail-message-card/);
  assert.doesNotMatch(script, /mailConversationCardHtml|mail-message-card/);
  // Template editor can insert a fixed table skeleton.
  assert.match(mailResources, /data-mail-insert-table/);
  assert.match(mailResources, /export function mailTemplateTableSkeleton/);
  // Compose shell and compact attachment picker follow the requested layout.
  assert.match(styles, /\.task-mail-compose-dialog > form \{ display: grid; grid-template-rows: auto minmax\(0,1fr\) auto;/);
  assert.match(styles, /\.mail-attachment-option \{ display: grid; grid-template-columns: 18px minmax\(0,1fr\) auto;[^}]*min-height: 30px;/);
  assert.match(styles, /\.mail-attachment-list-scroll \{[^}]*max-height: 180px;[^}]*overflow-y: auto;/);
  assert.match(styles, /\.mail-compose-picker\[hidden\], \.mail-compose-picker \[data-mail-picker-pane\]\[hidden\] \{ display: none; \}/, "author display rules must not override hidden panes");
  assert.match(styles, /\.task-mail-compose-dialog \.dialog-actions \{[^}]*position: sticky;[^}]*bottom: 0;/);
});

test("SSE list refresh is debounced and deferred while a task detail is open", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(script, /debounceMs: 350/);
  assert.match(script, /maxWaitMs: 1000/);
  assert.match(script, /canRefresh: \(\)=>Boolean\(state\.auth\?\.authenticated\) && !document\.hidden && !state\.selectedTask && !openingTaskID/);
  assert.match(script, /isInFlight: \(\)=>listRefreshInFlight/);
  assert.match(script, /function refreshListData\(\)/);
  assert.match(script, /listRefreshScheduler\.request\(\)/);
  assert.match(script, /addEventListener\("visibilitychange"/);
  assert.match(script, /!state\.selectedTask && !openingTaskID && listPageVisible/);
  assert.match(script, /listRefreshRequests\+\+/);
  assert.match(script, /taskOpenLastMs/);
});

test("read-only task history opens on the latest page and lazily loads older rows", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(script, /summary\?\.read_only\s*\?\s*\[\s*"chat",\s*"update",\s*"error"\s*\]/);
  assert.doesNotMatch(script, /summary\?\.read_only\s*\?\s*\[[^\]]*"mail"/);
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
  assert.match(script, /sidebar-resizer/);
  assert.match(script, /sidebar-memo-item/);
  assert.match(script, /data-sidebar-memo-done/);
  assert.match(script, /data-sidebar-memo-text/);
  assert.match(script, /data-sidebar-memo-edit/);
  assert.match(script, /input\.scrollHeight/);
  assert.match(script, /input\.style\.height = "22px";\s*if \(!input\.value\) return;/);
  assert.match(script, /classList\.toggle\("sidebar-memo-empty",\s*!item\.text\)/);
  assert.match(script, /new ResizeObserver/);
  assert.match(script, /fitSidebarMemoTextareas/);
  assert.match(script, /enterSidebarMemoEdit/);
  assert.match(script, /add-sidebar-memo-item/);
  assert.match(script, /localStorage\.setItem\(sidebarMemoKey/);
  assert.match(script, /localStorage\.setItem\(sidebarLayoutKey/);
  assert.match(script, /sidebarMemoColor/);
  assert.match(script, /data-sidebar-memo-color/);
  assert.match(script, /resizeSidebar/);
  assert.match(styles, /\.sidebar-resizer/);
  assert.match(styles, /\.sidebar-memo/);
  assert.match(styles, /\.sidebar-memo-swatch/);
  assert.match(styles, /\.sidebar-memo-item > textarea/);
  assert.match(styles, /\.sidebar-memo-item\.sidebar-memo-empty \.sidebar-memo-preview-text/);
  assert.match(styles, /-webkit-line-clamp: 2/);
  assert.match(styles, /max-height: 240px/);
  assert.match(styles, /\.sidebar-memo-item\.editing \{ border-color: var\(--blue\); \}/);
  assert.doesNotMatch(script, /class="sidebar-memo-item[^"]*\bempty\b/);
  assert.match(styles, /@media \(max-width: 760px\)[\s\S]*?\.sidebar-resizer \{ display: none; \}/);
});

test("task detail can bind one primary private or group channel", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  const conversation = await readFile(resolve(root, "dist", "conversation_ui.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  for (const marker of ["task-channel-route-form", "disconnect-task-channel", "task-channel-contacts-form", "群聊成员", "可 @ 联调人", "保存联调人"]) {
    assert.match(script, new RegExp(marker));
  }
  assert.match(script, /同时 @ 当前渠道机器人和目标机器人/);
  assert.doesNotMatch(script, /task-channel-route-dialog/);
  assert.match(script, /renderTaskToolPanel\([\s\S]*?taskChannelPanelHTML\(\)/);
  assert.match(api, /\/tasks\/\$\{encodeURIComponent\(id\)\}\/channel-routes/);
  assert.match(api, /channel-members:refresh/);
  assert.match(api, /channel-contacts/);
  assert.match(conversation, /actor\.display_name/);
  assert.match(conversation, /chat_display_name/);
  assert.match(styles, /\.task-channel-people/);
  assert.match(styles, /\.task-channel-contact-list/);
});

test("task channel entry keeps a stable tool slot and exposes connected state", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");
  assert.match(script, /taskChannelDestination: ChannelDestination \| null \| undefined/);
  assert.match(script, /loadTaskChannelDestination\(taskID, version\)/);
  assert.match(script, /主渠道已连接/);
  assert.match(script, /renderTaskToolButtons\(state\.taskTool, \{includeChannel: channelVisible, channelConnected: Boolean\(state\.taskChannelDestination\)/);
  assert.match(styles, /\.task-tool-actions \.task-tool-button\.connected/);
});

test("task channel saves preserve drafts and stale loads cannot overwrite current state", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /let taskChannelLoadVersion = 0/);
  assert.match(main, /const loadVersion = \+\+taskChannelLoadVersion/);
  assert.match(main, /loadVersion !== taskChannelLoadVersion/);
  assert.match(main, /form\.querySelector<HTMLElement>\("\[data-form-error\]"\)\?\.remove\(\)/);
  assert.match(main, /feedback\.dataset\.formError = "true"/);
  assert.match(main, /form\.querySelector\("footer"\)\?\.before\(feedback\)/);
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
  const hardwareResources = await readFile(resolve(root, "dist", "hardware_resources.js"), "utf8");
  const resources = await readFile(resolve(root, "dist", "resources.js"), "utf8");
  const mailResources = await readFile(resolve(root, "dist", "mail_resources.js"), "utf8");
  const mailTask = await readFile(resolve(root, "dist", "mail_task.js"), "utf8");
  const mailCards = await readFile(resolve(root, "dist", "mail_cards.js"), "utf8");
  const hardwareTerminal = await readFile(resolve(root, "dist", "hardware_terminal.js"), "utf8");
  const conversation = await readFile(resolve(root, "dist", "conversation_ui.js"), "utf8");
  const helpers = await readFile(resolve(root, "dist", "ui_helpers.js"), "utf8");
  const promptAdmin = await readFile(resolve(root, "dist", "prompt_admin.js"), "utf8");
  const proxySettings = await readFile(resolve(root, "dist", "proxy_settings.js"), "utf8");
  const syncSettings = await readFile(resolve(root, "dist", "sync_settings.js"), "utf8");
  const channels = await readFile(resolve(root, "dist", "channels.js"), "utf8");
  // The scope picker must read the index endpoint, not the entries page: a page
  // is recency-ordered, so filtering it silently drops every project whose
  // knowledge has not changed recently, and those projects then cannot be granted.
  assert.match(api, /knowledgeIndexes\(\)/);
  assert.match(api, /\/api\/v1\/knowledge\/indexes/);
  assert.ok(!/options\.context\.knowledge\b/.test(channels), "scope UI must not read the entries page");
  const codexAccounts = await readFile(resolve(root, "dist", "codex_accounts.js"), "utf8");
  const knowledgeWorkspace = await readFile(resolve(root, "dist", "knowledge_workspace.js"), "utf8");
  const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  const index = await readFile(resolve(root, "dist", "index.html"), "utf8");
  const i18n = await readFile(resolve(root, "dist", "i18n.js"), "utf8");
  assert.match(script, /api\.authStatus/);
  assert.match(promptAdmin, /内置只读模板/);
  assert.doesNotMatch(promptAdmin, /协议由代码管理/);
  assert.match(promptAdmin, /data-ui-key="prompt-template-content:\$\{escapeHTML\(selected\.id\)\}"/);
  assert.match(promptAdmin, /state\.drafts\[state\.selectedTemplateID\]/);
  assert.match(promptAdmin, /delete state\.drafts\[state\.selectedTemplateID\]/);
  assert.match(css, /\.prompt-page \{[^}]*height: calc\(100vh - 52px\);[^}]*overflow: hidden;/);
  assert.match(css, /\.prompt-template-list \{[^}]*overflow-y: auto;/);
  assert.match(css, /\.prompt-editor textarea \{[^}]*height: 100%;[^}]*overflow: auto;/);
  assert.match(api, /auth\/recover/);
  assert.match(api, /auth\/password/);
  assert.match(script, /id="forgot-password"/);
  assert.match(script, /id="password-recovery-form"/);
  assert.match(i18n, /不是程序安装目录/);
  assert.match(i18n, /修改或恢复该文件后需重启 AHA2/);
  assert.match(script, /以计划任务 <code>--data-dir<\/code> 的实际路径为准/);
  assert.match(i18n, /请使用实际数据目录中的 setup-token/);
  assert.match(script, /id="owner-avatar"/);
  assert.match(script, /id="advanced-settings"[^>]*data-view="advanced"[^>]*aria-label="\$\{t\("owner\.advanced"\)\}"/);
  assert.doesNotMatch(script, /ownerAvatarClicks\s*>=\s*5/);
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
  assert.match(knowledgeWorkspace, /\.\/i18n\.js\?v=[a-f0-9]{12}/);
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
  assert.match(script, /stream_idle_timeout_ms/);
  assert.match(script, /stream_max_retries/);
  assert.match(runtimePicker, /SSE 无数据超时（秒）/);
  assert.match(runtimePicker, /流中断重试次数/);
  assert.match(runtimePicker, /codex-stream-settings/);
  assert.match(agents, /SSE 超时/);
  assert.match(agents, /流重试/);
  assert.match(taskComposer, /id="composer-agent"/);
  assert.match(taskComposer, /加载中/);
  assert.match(taskTools, /data-task-tool/);
  assert.match(taskTools, /id="close-task-tool"/);
  assert.match(taskTools, /task-tool-panel/);
  assert.match(taskTools, /id="toggle-task-tool-mode"/);
  assert.match(taskTools, /id="pin-task-tool"/);
  assert.match(taskTools, /aria-pressed="\$\{pinned\}"/);
  assert.match(taskTools, /id="task-tool-resizer"/);
  assert.match(script, /localStorage\.setItem\(taskToolLayoutKey/);
  assert.match(script, /pinnedTool:\s*state\.taskToolPinned/);
  assert.match(script, /state\.taskTool = state\.taskToolPinned/);
  assert.match(script, /state\.taskToolPinned === state\.taskTool/);
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
  assert.doesNotMatch(hardwarePanel, /hardware-config-form/);
  assert.match(hardwarePanel, /connectHardware/);
  assert.match(hardwarePanel, /sendHardware/);
  assert.doesNotMatch(hardwarePanel, /hardware-save-top/);
  assert.match(hardwarePanel, /hardware-config-collapse/);
  assert.match(hardwarePanel, /configCollapsed/);
  assert.match(taskTools, /renderHardwareGroupSwitcher/);
  assert.match(hardwarePanel, /hardware-title-group-trigger/);
  assert.match(hardwarePanel, /hardware-title-group-menu/);
  assert.match(hardwarePanel, /syncTitleGroupSwitcher/);
  assert.match(script, /hardware:\s*detail\.hardware/);
  assert.match(script, /hardwareResources:\s*\[\]/);
  assert.match(script, /refreshHardwarePanel\(detail, setMessage\)/);
  assert.match(css, /\.hardware-title-group-switcher/);
  assert.match(css, /\.hardware-title-group-menu/);
  assert.match(css, /\.hardware-resource-dialog \[hidden\] \{ display: none !important; \}/);
  assert.match(css, /\.task-tool-pin\.active/);
  assert.match(css, /\.hardware-tool\.config-collapsed \{[^}]*grid-template-columns:\s*42px minmax\(0,1fr\)/);
  assert.match(hardwarePanel, /data-hardware-resource-toggle/);
  assert.match(hardwarePanel, /data-hardware-resource-select/);
  assert.match(hardwarePanel, /hardware-toggle/);
  assert.match(hardwarePanel, /updateTaskHardwareBindings/);
  assert.match(hardwarePanel, /data-view="hardware"/);
  assert.doesNotMatch(hardwarePanel, /hardware-config-form/);
  assert.doesNotMatch(hardwarePanel, /aha2:hardware-draft:/);
  assert.doesNotMatch(hardwarePanel, /data-hardware-resource-access/);
  assert.doesNotMatch(hardwarePanel, /hardware-save-bindings/);
  assert.doesNotMatch(hardwarePanel, /data-hardware-transport=/);
  assert.match(hardwareResources, /renderHardwareResources/);
  assert.match(hardwareResources, /createHardwareResource/);
  assert.match(hardwareResources, /updateHardwareResource/);
  assert.match(hardwareResources, /deleteHardwareResource/);
  assert.match(hardwareResources, /data-hardware-adb-pair/);
  assert.match(hardwareResources, /name="pairing_port"/);
  assert.match(hardwareResources, /adb_paired_at/);
  assert.match(hardwareResources, /pairHardwareADB/);
  assert.match(hardwareResources, /串口设备属于当前运行设备/);
  assert.doesNotMatch(hardwareResources, /\["both", "串口 \+ 网络"\]/);
  assert.match(hardwarePanel, /lastError/);
  assert.match(hardwarePanel, /stream\.lastError \|\| stream\.status\?\.error/);
  assert.match(hardwarePanel, /item\.direction === "rx"/);
  assert.doesNotMatch(hardwarePanel, /hardware-output|hardware-output-fallback|formatEvent/);
  assert.match(hardwarePanel, /sendEncoding:\s*"text"/);
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
  assert.match(api, /hardware-resources/);
  assert.match(api, /hardware-bindings/);
  assert.match(api, /pairHardwareADB/);
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
  // Copying the console: a selected region copies alone, otherwise the whole
  // scrollback, and the result is only claimed once the write resolved.
  assert.match(hardwarePanel, /id="hardware-copy-output"/);
  assert.match(hardwarePanel, /if \(!activeTerminal\) return;/);
  assert.match(hardwarePanel, /activeTerminal\.selectedText\(\)/);
  assert.match(hardwarePanel, /selected \|\| activeTerminal\.bufferText\(\)/);
  assert.match(hardwareTerminal, /getSelection\(\)/);
  assert.match(hardwareTerminal, /translateToString\(true\)/);
  assert.match(css, /#hardware-clear-output, #hardware-copy-output/);
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
  // Wheel is tracked by a time window rather than as a gesture: it has no end
  // event, so treating it as one left the flag set forever and new messages
  // stopped following after any wheel scroll.
  assert.match(script, /addEventListener\("wheel",\s*noteConversationWheel/);
  assert.doesNotMatch(script, /\[\s*"wheel",\s*"touchstart"/);
  assert.match(script, /\[\s*"touchstart",\s*"pointerdown"\s*\][\s\S]*beginConversationGesture/);
  assert.match(script, /conversationUserScrollUntil\s*=\s*Date\.now\(\)\s*\+\s*conversationUserScrollWindowMs/);
  assert.match(script, /composingConversationTouch\s*\|\|\s*Date\.now\(\)\s*<\s*conversationUserScrollUntil/);
  // A touch gesture has to be closed explicitly, or the suppression outlives it
  // and new messages stop following for a reader sitting at the bottom.
  assert.match(script, /\[\s*"touchend",\s*"touchcancel",\s*"pointerup",\s*"pointercancel"\s*\][\s\S]*endConversationGesture/);
  // Following new content is a matter of recorded intent, not of position: the
  // bottom test carries an 80px tolerance, so while a reader is inside that band
  // every render snaps them back to the exact bottom and they never get out.
  assert.match(script, /function shouldAutoScrollConversation[\s\S]{0,400}composingConversationTouch\) return false[\s\S]{0,120}conversationFollowBottom\) return false/);
  assert.match(script, /composingConversationTouch = true;[\s\S]{0,160}cancelConversationBottomPin\(\)/);
  assert.match(script, /currentTop < previousTop - 2 && userDriven\) conversationFollowBottom = false/);
  // Restoring the pre-replace position is right for a still reader but rewinds a
  // drag that is still in progress, so both restore sites must skip it mid-gesture.
  const restoreGuards = script.match(/else if \(!composingConversationTouch\)/g) || [];
  assert.equal(restoreGuards.length, 2, `both conversation restore sites must defer to an active gesture, found ${restoreGuards.length}`);
  assert.match(script, /const conversationRenderedHTML = new WeakMap\(\)/);
  assert.match(script, /if \(conversationRenderedHTML\.get\(list\) !== nextHTML\)[\s\S]{0,500}list\.innerHTML = nextHTML[\s\S]{0,180}conversationRenderedHTML\.set\(list,\s*nextHTML\)/);
  assert.match(script, /const preservedConversation = previousConversation && preservePageUI[\s\S]{0,180}conversationRenderedHTML\.get\(previousConversation\) === nextConversationHTML/);
  assert.match(script, /function captureConversationImages[\s\S]{0,600}function restoreConversationImages/);
  assert.match(script, /images\.set\(key,\s*\[[\s\S]{0,100}\.\.\.images\.get\(key\) \|\| \[\],[\s\S]{0,40}image[\s\S]{0,20}\]\)/);
  assert.match(script, /images\.get\(button\.dataset\.imagePreview \|\| ""\)\?\.shift\(\)/);
  assert.match(script, /const images = captureConversationImages\(list\)[\s\S]{0,260}list\.innerHTML = nextHTML[\s\S]{0,180}restoreConversationImages\(list, images\)/);
  assert.match(script, /if \(!preservedConversation && renderedConversation\) restoreConversationImages\(renderedConversation, previousConversationImages\)/);
  assert.match(script, /bindCommon\(\);[\s\S]{0,700}if \(preservedConversation\) document\.querySelector\("#conversation-list"\)\?\.replaceWith\(preservedConversation\)/);
  assert.match(script, /await restoreNavigationState\(savedNavigation\);[\s\S]{0,260}await ensureViewData\(state\.view\);[\s\S]{0,180}if \(state\.selectedTask\) requestConversationBottom\(\);[\s\S]{0,100}prefetchSecondaryData\(\)/);
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
  assert.match(script, /list\.innerHTML = nextHTML;\s*conversationRenderedHTML\.set\(list, nextHTML\);\s*restoreConversationImages\(list, images\);\s*restoreRegionUI\(list, ui, false\);\s*if \(shouldAutoScrollConversation\(wasAtBottom\)\)[\s\S]{0,80}stabilizeConversationBottom\(list\)/);
  assert.match(script, /list\.innerHTML = nextHTML;\s*conversationRenderedHTML\.set\(list, nextHTML\);\s*restoreRegionUI\(list, ui, false\);\s*list\.scrollTop = previousTop \+ Math\.max/);
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
  // The message filter keeps exactly four categories; mail is not one of them.
  assert.match(taskComposer, /chat: "对话",\s*update: "Agent Update",\s*tool: "工具调用",\s*error: "错误",?\s*\}/);
  assert.doesNotMatch(taskComposer, /mail: "邮件"/);
  assert.match(taskComposer, /enabled}\/4/);
  assert.doesNotMatch(taskComposer, /enabled}\/5/);
  assert.doesNotMatch(conversation, /mailConversationCardHtml|mail-message-card|data-message-category="mail"/);
  assert.doesNotMatch(script, /taskCategories:\s*\{[\s\S]{0,200}mail/);
  assert.doesNotMatch(script, /\["chat", "update", "error", "mail"\]/);
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
  assert.match(runtimePicker, /option\("env", "Env"/);
  assert.match(runtimePicker, /option\("official", "Official"/);
  assert.match(runtimePicker, /Official/);
  assert.match(runtimePicker, /claude_native/);
  assert.match(runtimePicker, /官方模型/);
  assert.match(script, /refreshClaudeOfficialCatalog/);
  assert.match(script, /selectedOptionExists/);
  assert.doesNotMatch(script, /Claude Code 原生账号/);
  assert.doesNotMatch(script, /重新检测 Claude Code 登录状态/);
  assert.match(css, /select \{[^}]*appearance: none;[^}]*padding: 0 32px 0 4px;[^}]*background-position: right 10px center;/s);
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
  assert.match(script, /data-view="advanced">\$\{t\("common\.backAdvanced"\)\}/);
  assert.match(script, /const mobileNav = \[[\s\S]{0,160}\.\.\.nav,[\s\S]{0,160}"advanced",[\s\S]{0,80}"nav\.advanced"/);
  assert.match(script, /<nav class="bottom-nav">\$\{mobileNav\.map/);
  assert.match(script, /data-view="proxy">\$\{t\("advanced\.proxy\.action"\)\}/);
  assert.doesNotMatch(script, /\["prompts",\s*"bot",\s*"提示词"\]/);
  assert.doesNotMatch(script, /\["sync",\s*"sync",/);
  assert.match(css, /\.bottom-nav \{[^}]*grid-template-columns:\s*repeat\(8,1fr\)/);
  assert.match(script, /"channels",\s*"bot",\s*"nav\.channels"/);
	assert.match(script, /renderChannels\(state\.channelProviders, state\.channelInstances,/);
  assert.match(api, /api\/v1\/channel-providers/);
  assert.match(script, /channels\.js\?v=[a-f0-9]{12}/);
  assert.match(channels, /扫码创建并绑定/);
  assert.match(channels, /channel-onboarding-sessions\/.*\/qr/);
  assert.match(api, /onboarding-options/);
  assert.match(channels, /channel_permission_id/);
  assert.doesNotMatch(channels, /name="channel_runtime_menu"/);
  assert.match(channels, /data-reset-runtime-menus/);
  assert.match(api, /runtime-menus\/reset/);
  assert.match(api, /channel-instances\/.*\/conversations/);
  assert.match(channels, /权限更新仅做增量授权/);
  assert.doesNotMatch(channels, /name="app_secret" type="password"/);
	for (const marker of ["渠道设置", "channel_runtime", "notify_task_status", "普通 Task 状态变更推送到飞书私聊助手", "knowledge_entry_id"]) assert.match(channels, new RegExp(marker));
	for (const marker of ["data-channel-settings-open", "data-channel-conversations-open", "channelConversations", "channel-conversation-card", "绑定任务", "最近入站", "最近出站"] ) assert.match(channels, new RegExp(marker));
	for (const marker of ["data-channel-policy-select-all", "data-channel-policy-clear", "knowledge_scope_mode", "data-channel-knowledge-selected", "全部项目知识与知识库"]) assert.match(channels, new RegExp(marker));
	assert.match(channels, /私聊默认全部/);
	assert.match(channels, /policy\.endpoint === "group_digital_human"/);
	assert.doesNotMatch(channels, /group_operation_scope_mode|group_allowed_project_ids|group_allowed_workspace_ids/);
	assert.doesNotMatch(channels, /name="operation_scope_mode"|name="allowed_project_ids"|name="allowed_workspace_ids"/);
	assert.doesNotMatch(channels, /data-channel-activity|Owner 收件箱与投递/);
	assert.doesNotMatch(channels, /runtime_bot_display_name_override|机器人显示名称/);
	assert.match(channels, /runtime_assistant_dm:\s*\{[\s\S]{0,40}inherit: true/);
	assert.match(channels, /runtime_group_digital_human:\s*\{[\s\S]{0,40}inherit: true/);
	assert.match(channels, /channel_revision_conflict/);
	assert.match(channels, /渠道设置已被其他更新修改/);
	assert.match(channels, /扫码更新飞书权限/);
  assert.doesNotMatch(channels, /Knowledge allowlist/);
  assert.doesNotMatch(channels, /人工整理并共享/);
  assert.doesNotMatch(channels, /channelKnowledgeRecords/);
  assert.doesNotMatch(api, /channel-knowledge-records/);
  assert.match(script, /renderProxySettings\(/);
  assert.match(script, /!resourceStates\.proxy\.loaded \|\| resourceStates\.proxy\.loading/);
  assert.match(script, /if \(nextView === "proxy"\) resourceStates\.proxy\.loaded = false/);
  assert.match(proxySettings, /AHA 内置代理/);
  assert.match(proxySettings, /正在读取当前代理设置/);
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
  assert.match(script, /<option value="inplace" selected>原地执行（默认）<\/option>/);
  assert.match(script, /isolation\.value = "inplace"/);
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
  assert.match(index, /class="boot-spinner"/);
  assert.doesNotMatch(index, /正在连接服务/);
  assert.match(css, /\.boot-loading \.boot-spinner/);
  assert.match(script, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_agents\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_composer\.js\?v=[a-f0-9]{12}/);
  assert.match(taskComposer, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(taskComposer, /\.\/i18n\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/task_tools\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/task_agents\.js\?v=[a-f0-9]{12}/);
  assert.match(taskTools, /\.\/hardware_panel\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/hardware_panel\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/resources\.js\?v=[a-f0-9]{12}/);
  assert.match(script, /\.\/mail_task\.js\?v=[a-f0-9]{12}/);
  assert.match(resources, /\.\/hardware_resources\.js\?v=[a-f0-9]{12}/);
  assert.match(resources, /\.\/mail_resources\.js\?v=[a-f0-9]{12}/);
  assert.match(mailResources, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(mailTask, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(mailCards, /\.\/icons\.js\?v=[a-f0-9]{12}/);
  assert.match(mailResources, /credential_configured/);
  assert.match(mailTask, /mailCanCancel/);
  assert.match(hardwarePanel, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(hardwareResources, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(hardwareResources, /["']adb["'],\s*["']ADB["']/);
  assert.match(hardwareResources, /protocol === "adb"\) return 5555/);
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

test("composer agent selector stays populated while task detail loads", async () => {
  const composer = await import(pathToFileURL(resolve(import.meta.dirname, "..", "dist", "task_composer.js")));
  const loading = composer.renderComposerAgentOptions({agents: []}, "main");
  assert.match(loading, /value="main"/);
  assert.match(loading, /加载中/);
  assert.doesNotMatch(loading, />\s*<\/option>/);
  const loaded = composer.renderComposerAgentOptions({
    agents: [{agent_id: "main", status: "idle", unread_count: 0}],
  }, "main");
  assert.match(loaded, /value="main" selected/);
  assert.match(loaded, /main · 空闲/);
});

test("hardware tool title renders the active hardware group switcher", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {normalizePinnedTaskTool, renderTaskToolPanel} = await import(pathToFileURL(resolve(root, "dist", "task_tools.js")));
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
  const html = renderTaskToolPanel("hardware", detail, "main", "", "split", "", true);
  assert.match(html, /<h3>硬件调试<\/h3><div class="hardware-title-group-switcher">/);
  assert.match(html, /id="hardware-title-group-trigger"[^>]*><span>主控板<\/span>/);
  assert.match(html, /data-hardware-title-group="board-a" class="active">主控板<\/button>/);
  assert.match(html, /data-hardware-title-group="board-b" class="">继电器板<\/button>/);
  assert.match(html, /id="pin-task-tool"[^>]*class="icon-button task-tool-pin active"[^>]*aria-pressed="true"/);
  assert.match(html, /class="hardware-tool config-collapsed"/);
  assert.doesNotMatch(html, /<details class="hardware-config" open>/);
  assert.equal(normalizePinnedTaskTool("hardware"), "hardware");
  assert.equal(normalizePinnedTaskTool("invalid"), "");

  const emptyHTML = renderTaskToolPanel("hardware", {
    task: {id: "task-hardware-empty", status: "active"},
    hardware: [],
    memory: {},
  }, "main", "", "split");
  assert.doesNotMatch(emptyHTML, /class="hardware-tool config-collapsed"/);
  assert.match(emptyHTML, /<details class="hardware-config" open>/);
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

test("task memory renders a bounded chronological goal timeline", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderTaskMemory} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
  const goal_history = Array.from({length: 21}, (_, index) => ({
    sequence: index + 1,
    id: `goal-${index + 1}`,
    task_id: "task-1",
    goal_before: index ? `goal-${index}` : "",
    goal_after: `goal-${index + 1}`,
    change_type: index ? "revise" : "set",
    reason: `reason-${index + 1}`,
    created_at: `2026-10-23T12:${String(index).padStart(2, "0")}:00Z`,
  }));
  const html = renderTaskMemory({task_id: "task-1", current_goal: "goal-21", goal_history});
  assert.match(html, /最近 20\/20 次 · 按时间顺序/);
  assert.doesNotMatch(html, /reason-1</);
  assert.match(html, /reason-2</);
  assert.match(html, /reason-21</);
  assert.match(html, /goal-20<\/span><b>→<\/b><strong>goal-21<\/strong>/);
  assert.doesNotMatch(html, /memory-block/);
});

// The Agent config dialog is the only place an existing Task's runtime can be
// edited, so the accelerated tier has to be offered there, seeded from the
// stored snapshot, and gated on the Backend that is selected right now.
test("agent config offers the Codex fast tier and follows the backend", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderAgentConfigDialog} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
  const dialog = (backend, fastMode) => renderAgentConfigDialog({
    task: {id: "task-1", project_id: "project-1", workspace_id: "workspace-1", title: "Task", original_request: "", current_goal: "", status: "active", isolation: "inplace", collaboration_mode: "auto", max_agents: 3, knowledge_policy: "inherit", skill_ids: [], total_tokens: 0, created_at: "", updated_at: ""},
    agents: [{agent_id: "main", role: "main", title: "Main", backend, model_source: "env", model_id: "model-1", reasoning_effort: "high", filesystem: "workspace-write", approval: "never", proxy_enabled: false, fast_mode: fastMode}],
    turns: [], memory: {task_id: "task-1", current_goal: ""},
  }, "main", [], [], []);
  const on = dialog("codex", true);
  assert.match(on, /id="agent-config-fast-mode-row"[^>]*>\s*<input name="fast_mode" type="checkbox" checked>/, "a stored tier is not shown as on");
  const off = dialog("codex", false);
  assert.doesNotMatch(off, /name="fast_mode" type="checkbox" checked/, "an unset tier is rendered as on");
  // Hidden, never disabled: a disabled checkbox drops out of the form, which is
  // what used to clear a stored tier whenever another Backend was saved.
  const claude = dialog("claude", true);
  assert.match(claude, /id="agent-config-fast-mode-row"[^>]* hidden>/, "the switch is not gated on the Backend");
  assert.doesNotMatch(claude, /name="fast_mode"[^>]*disabled/, "the switch is disabled instead of hidden");
});

// Live inbox delivery is always on, so the Agent dialog and the configuration
// update card must not expose a per-Agent switch or any "experimental" copy.
// The server may still send the historical stream_input field; the Web client
// must ignore it instead of rendering a hidden or disabled switch.
test("agent config does not expose the live inbox switch", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderAgentConfigDialog, renderConversationWithOrchestration} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
  const dialog = backendName => renderAgentConfigDialog({
    task: {id: "task-1", project_id: "project-1", workspace_id: "workspace-1", title: "Task", original_request: "", current_goal: "", status: "active", isolation: "inplace", collaboration_mode: "auto", max_agents: 3, knowledge_policy: "inherit", skill_ids: [], total_tokens: 0, created_at: "", updated_at: ""},
    agents: [{agent_id: "main", role: "main", title: "Main", backend: backendName, model_source: "env", model_id: "model-1", reasoning_effort: "high", filesystem: "workspace-write", approval: "never", proxy_enabled: false, fast_mode: false, stream_input: true}],
    turns: [], memory: {task_id: "task-1", current_goal: ""},
  }, "main", [], [], []);
  for (const backendName of ["claude", "codex", "dsh"]) {
    const html = dialog(backendName);
    assert.doesNotMatch(html, /stream_input/, `${backendName} still renders the live inbox switch`);
    assert.doesNotMatch(html, /agent-config-stream-input-row/, `${backendName} still renders the switch row`);
    assert.doesNotMatch(html, /实验性/, `${backendName} still renders experimental copy`);
    assert.doesNotMatch(html, /Live inbox/, `${backendName} still reports live inbox configuration`);
  }
  const card = renderConversationWithOrchestration([{
    kind: "agent_config_updated",
    created_at: "2026-01-01T00:00:00Z",
    payload: {
      agent_id: "main", backend: "claude", stream_input: true, model_source: "env",
      model_name: "Model", reasoning_effort: "high", filesystem: "workspace-write",
      approval: "never", proxy_enabled: false, fast_mode: false,
    },
  }], null, () => "");
  assert.doesNotMatch(card, /stream_input|Live inbox|实验性/, "the config card still reports the removed switch");
});

// The removed copy must not linger as dead i18n entries that revive the
// "experimental" wording in English.
test("removed live inbox switch copy has no translation", async () => {
  const root = resolve(import.meta.dirname, "..");
  const i18n = await import(pathToFileURL(resolve(root, "dist", "i18n.js")));
  i18n.applyLocale("en-US");
  try {
    for (const legacy of ["实验性：Turn 内 live inbox 投递", "开启（实验性）"]) {
      assert.equal(i18n.tr(legacy), legacy, `removed copy still has an English translation: ${legacy}`);
    }
  } finally {
    i18n.applyLocale("zh-CN");
  }
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
  assert.match(html, /name="model_id"[^>]*disabled/);
  assert.match(html, /name="wire_model"[^>]*required/);
  assert.match(html, /name="stream_idle_timeout_seconds"[^>]*value="300"/);
  assert.match(html, /name="stream_max_retries"[^>]*value="5"/);
  assert.match(html, /Codex 官方账号使用 CLI 默认的流超时与重试参数/);
  const newTaskHTML = runtimeFieldsHTML("new-task", [{
    id: "model-env", display_name: "Env Model", provider_id: "gateway", source: "provider",
    backend: "codex", wire_model: "env-model", created_at: "", updated_at: "",
  }], []);
  assert.match(newTaskHTML, /name="stream_idle_timeout_seconds"[^>]*value="120"/);
  assert.match(newTaskHTML, /name="stream_max_retries"[^>]*value="2"/);
  const nativeHTML = runtimeFieldsHTML("native", [], [], {
    backend: "claude", model_source: "claude_native", wire_model: "sonnet",
  });
  assert.match(nativeHTML, /value="claude_native" selected/);
  assert.match(nativeHTML, /id="native-claude-native-model"/);
  assert.match(nativeHTML, /value="sonnet" selected/);
  assert.match(nativeHTML, /claude_native/);
  const dshHTML = runtimeFieldsHTML("dsh", [{
    id: "model-dsh", display_name: "DSH Model", provider_id: "gateway", provider_name: "Gateway A",
    source: "provider", backend: "dsh", wire_model: "dsh-model", created_at: "", updated_at: "",
  }], [], {backend: "dsh", model_source: "env", model_id: "model-dsh"});
  assert.match(dshHTML, />DSH<\/option>/);
  assert.match(dshHTML, /value="model-dsh" selected/);
  assert.doesNotMatch(dshHTML, /value="official"/);
  assert.match(dshHTML, /id="dsh-codex-stream-settings"[^>]*hidden/);
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

test("conversation renders agent config, channel outreach, and turn duration cards", async () => {
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
  }, {
    sequence: 3, id: "outreach-1", task_id: "task-1", agent_id: "aha", stream_agent_id: "main",
    category: "update", kind: "agent_channel_outreach", summary: "AHA 已主动路由阻塞协调消息",
    payload: {
      purpose: "blocker", message: "接口字段需要确认 <script>",
      delivery_state: "pending", channel: {display_name: "AHA-TEST"},
      recipients: [{display_name: "张三", collaboration_role: "APP"}, {display_name: "WORK-AHA", is_bot: true}],
    },
    created_at: "2026-09-04T09:00:03Z",
  }, {
    sequence: 4, id: "collaboration-result-1", task_id: "task-1", agent_id: "sub-001",
    stream_agent_id: "main", from_agent_id: "sub-001", to_agent_id: "main",
    route_kind: "collaboration_result", category: "update", kind: "collaboration_result_routed",
    summary: "sub-001 [succeeded, attempt 1]\\nstore verification passed",
    payload: {batch_id: "collaboration_batch_1", child_turn_id: "turn-sub-1", status: "succeeded"},
    created_at: "2026-09-04T09:00:04Z",
  }, {
    sequence: 5, id: "channel-reply-1", task_id: "task-1", agent_id: "aha",
    stream_agent_id: "main", from_agent_id: "aha", to_agent_id: "main",
    route_kind: "channel_reply", category: "update", kind: "agent_channel_reply",
    summary: "AHA 已回复渠道消息：早上好",
    payload: {
      inbound_receipt_id: "channel_inbox_1", delivery_state: "delivered",
      message: "早上好\n这是渠道回复内容",
      channel: {display_name: "团队飞书", instance_name: "飞书", endpoint_kind: "assistant_dm"},
      recipient: {display_name: "Owner", is_bot: false},
    },
    created_at: "2026-09-04T09:00:05Z",
  }, {
    sequence: 6, id: "channel-status-replied-1", task_id: "task-1", agent_id: "aha",
    stream_agent_id: "main", from_agent_id: "aha", to_agent_id: "main",
    route_kind: "channel_route", category: "update", kind: "agent_channel_route",
    summary: "AHA 渠道回复已生成",
    payload: {
      status_card: true, message_status: "replied", status_reason: "",
      inbound_receipt_id: "channel_inbox_1", delivery_state: "delivered",
      inbound_message: "测试消息", reply_message: "早上好\n这是渠道回复内容",
      channel: {display_name: "团队飞书", endpoint_kind: "assistant_dm"},
      actor: {display_name: "Owner", is_bot: false},
    },
    created_at: "2026-09-04T09:00:06Z",
  }, {
    sequence: 7, id: "channel-status-1", task_id: "task-1", agent_id: "aha",
    stream_agent_id: "main", from_agent_id: "aha", to_agent_id: "main",
    route_kind: "channel_route", category: "update", kind: "agent_channel_route",
    summary: "AHA 已处理并选择不回复",
    payload: {
      status_card: true, message_status: "not_replied", status_reason: "reply_decision_end",
      status_reason_text: "这是群聊里的玩笑，没有需要处理的请求 <script>alert(1)</script>",
      inbound_receipt_id: "channel_inbox_2", message: "收到，本轮无需继续",
      channel: {display_name: "团队飞书", endpoint_kind: "group_digital_human"},
      actor: {display_name: "联调机器人", is_bot: true},
    },
    created_at: "2026-09-04T09:00:07Z",
  }, {
    sequence: 8, id: "channel-status-missing-decision", task_id: "task-1", agent_id: "aha",
    stream_agent_id: "main", from_agent_id: "aha", to_agent_id: "main",
    route_kind: "channel_route", category: "update", kind: "agent_channel_route",
    summary: "AHA 未提交渠道回复或无需回复决策",
    payload: {
      status_card: true, message_status: "not_replied", status_reason: "no_channel_reply",
      inbound_receipt_id: "channel_inbox_3", inbound_message: "你好",
      channel: {display_name: "团队飞书", endpoint_kind: "assistant_dm"},
      actor: {display_name: "Owner", is_bot: false},
    },
    created_at: "2026-09-04T09:00:08Z",
  }];
  const html = renderConversationList(items, null, false);
  assert.match(html, /Agent 配置更新/);
  assert.match(html, /GPT-5\.6/);
  assert.match(html, /Turn 1 耗时/);
  assert.match(html, /5s/);
  assert.match(html, /AHA 主动路由/);
  assert.match(html, /阻塞协调/);
  assert.match(html, /WORK-AHA/);
  assert.match(html, /已排队/);
  assert.match(html, /接口字段需要确认 &lt;script&gt;/);
  assert.match(html, /sub-001/);
  assert.match(html, /store verification passed/);
  assert.doesNotMatch(html, /aha-channel-reply-card/);
  assert.equal((html.match(/AHA 渠道消息/g) || []).length, 3);
  assert.match(html, /团队飞书/);
  assert.match(html, /这是渠道回复内容/);
  assert.match(html, /已回复/);
  assert.match(html, /aha-channel-status-card/);
  assert.doesNotMatch(html, /AHA 渠道状态/);
  assert.match(html, /这是群聊里的玩笑，没有需要处理的请求 &lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  assert.doesNotMatch(html, /没有需要处理的请求 <script>/);
  assert.match(html, /无需回复/);
  assert.match(html, /模型结束本轮时未提交渠道回复或无需回复决策/);
  assert.match(html, /未回复/);
  assert.match(html, /agent-routed-message/);
  assert.doesNotMatch(html, /接口字段需要确认 <script>/);
});

test("channel route cards map provider acceptance without claiming read receipts", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {renderConversationWithOrchestration} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
  const render = (kind, payload) => renderConversationWithOrchestration([{
    sequence: 1, id: "card-1", task_id: "task-1", agent_id: "aha", stream_agent_id: "main",
    category: "update", kind, summary: "渠道消息",
    payload: {inbound_receipt_id: "receipt-1", channel: {display_name: "团队飞书"}, ...payload},
    created_at: "2026-11-04T09:00:00Z",
  }], null, () => "");
  const status = payload => render("agent_channel_route", {status_card: true, message_status: "replied", reply_message: "测试回复", ...payload});

  assert.match(status({delivery_state: "pending"}), /回复已排队/);
  assert.match(status({delivery_state: "leased"}), /回复发送中/);
  const accepted = status({delivery_state: "provider_accepted"});
  assert.match(accepted, /已回复（飞书已接受）/);
  assert.doesNotMatch(accepted, /已读|已接收/);
  assert.match(status({delivery_state: "delivered"}), /已回复（飞书已接受）/);
  const failed = status({delivery_state: "failed"});
  assert.match(failed, /回复发送失败/);
  assert.doesNotMatch(failed, /已回复：/);
  assert.match(failed, /回复内容：/);
  assert.match(status({delivery_state: "dead_letter"}), /回复投递失败（待重放）/);
  // Replay only enqueues a new generation; it must not claim it was delivered.
  assert.match(status({delivery_state: "replayed"}), /回复已重新排队/);
  assert.doesNotMatch(status({delivery_state: "replayed"}), /已重新投递/);
  assert.doesNotMatch(status({delivery_state: "failed"}), /已回复/);

  // Non-status route and outreach cards use the same delivery labels.
  assert.match(render("agent_channel_outreach", {delivery_state: "leased", message: "协调"}), /发送中/);
  assert.match(render("agent_channel_route", {delivery_state: "provider_accepted"}), /飞书已接受/);
  assert.match(render("agent_channel_route", {delivery_state: "replayed"}), /已重新排队/);
  assert.doesNotMatch(render("agent_channel_route", {delivery_state: "provider_accepted"}), /已读|已接收/);
});

test("stalled turns explain that backend streaming is silent", async () => {
  const root = resolve(import.meta.dirname, "..");
  const agents = await readFile(resolve(root, "dist", "task_agents.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(agents, /等待 Backend 流事件/);
  assert.match(agents, /模型推理、网络重连或上游排队/);
  assert.match(styles, /\.turn-stalled-note/);
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
  assert.doesNotMatch(main, /id="task-clone-source"/);
  assert.doesNotMatch(main, /旧任务配置/);
  assert.doesNotMatch(main, /clone_warnings/);
  assert.match(api, /\/tasks\/\$\{encodeURIComponent\(id\)\}\/start/);
});

test("system-managed resources are read-only and normal tasks exclude pet MCP", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const catalog = await readFile(resolve(root, "..", "internal", "httpapi", "catalog_handlers.go"), "utf8");
  const tasks = await readFile(resolve(root, "..", "internal", "httpapi", "task_handlers.go"), "utf8");
  const service = await readFile(resolve(root, "..", "internal", "app", "service.go"), "utf8");

  assert.match(main, /function isSystemProject/);
  assert.match(main, /!isSystemProject\(item\)/);
  assert.match(main, /系统托管/);
  assert.match(main, /item\.enabled && !item\.builtin/);
  assert.match(catalog, /system_project_read_only/);
  assert.match(catalog, /domain\.IsSystemManagedProject/);
  assert.match(tasks, /system_project_read_only/);
  assert.match(service, /AllowSystemProject bool/);
  assert.match(service, /!input\.AllowSystemProject/);
});

test("model detection fetches the catalog, supports multi-select and an add dialog", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(main, /api\.detectModels\(\{/);
  assert.match(main, /provider_id: providerID/);
  assert.match(main, /new AbortController\(\)/);
  assert.match(main, /MODEL_CATALOG_TIMEOUT_MS/);
  assert.doesNotMatch(main, /modelDetectionEventsURL/);
  assert.doesNotMatch(main, /new EventSource\(api\./);
  assert.doesNotMatch(main, /stop-model-detection/);
  assert.match(main, /data-probe-model/);
  assert.match(main, /api\.probeModel/);
  assert.match(main, /replaceRegionHTML\(root,/);
  assert.match(main, /data-ui-key="model-detection-list"/);
  assert.match(main, /applyModelDetectionFilter/);
  assert.match(main, /匹配 /);
  assert.match(main, /已选 /);
  assert.match(main, /session\.selected/);
  assert.match(main, /renderProtocolProbeStatus/);
  assert.match(main, /mergeProbedModel/);
  assert.match(main, /max_input_tokens: probed\.max_input_tokens \|\| catalogModel\.max_input_tokens/);
  assert.match(main, /protocolErrors/);
  assert.match(main, /获取模型列表/);
  assert.match(main, /id="open-model-add"/);
  assert.match(main, /id="model-add-dialog"/);
  assert.match(main, /id="model-add-wire"/);
  assert.match(main, /id="confirm-model-add"/);
  assert.match(main, /protocol_errors/);
  assert.doesNotMatch(main, /proto-checks/);
  assert.doesNotMatch(main, /add-selected-models/);
  assert.doesNotMatch(main, /添加到 Backend/);
  assert.match(api, /\/api\/v1\/providers\/detect-models/);
  assert.match(api, /model-probes/);
  assert.doesNotMatch(api, /model-detection-jobs/);
});

test("pet events share the page global event stream", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(main, /handlePetStreamEvent/);
  assert.match(main, /type === "pet_state"/);
  assert.match(assistant, /export function handlePetStreamEvent/);
  assert.doesNotMatch(assistant, /new EventSource\(/);
  assert.doesNotMatch(api, /pet\/events/);
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
  // Live refresh requests are coalesced by the scheduler and never overlap; a
  // pointer press keeps the resulting render deferred as before.
  assert.match(source, /isInFlight: \(\) => listRefreshInFlight/);
  assert.match(source, /listRefreshScheduler\.request\(\)/);
  // The scheduler owns single-flight; performListRefresh must not keep a second
  // guard that silently drops a pending refresh.
  assert.doesNotMatch(source, /if \(listRefreshInFlight\) return;/);
  assert.match(source, /async function performListRefresh\(\): Promise<void> \{\s*listRefreshInFlight = true;/);
  assert.match(source, /!state\.selectedTask && !openingTaskID && listPageVisible/);
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
  // Device options are built from server totals, not from the loaded page.
  assert.deepEqual(filters.taskDeviceFilterOptions({local: 1, "remote:device-a": 1, "remote:device-b": 1}), [
    {value: "local", label: "本机", count: 1},
    {value: "remote:device-a", label: "device-a", count: 1},
    {value: "remote:device-b", label: "device-b", count: 1},
  ]);

  // Status filtering is three owner-facing buckets, and "blocked" counts as an
  // error because it needs the owner to step in.
  assert.equal(filters.taskStatusGroup("active"), "open");
  assert.equal(filters.taskStatusGroup("waiting_user"), "open");
  assert.equal(filters.taskStatusGroup("draft"), "open");
  assert.equal(filters.taskStatusGroup("completed"), "done");
  for (const status of ["failed", "blocked", "cancelled"]) {
    assert.equal(filters.taskStatusGroup(status), "error", `${status} must group as an error`);
  }
  assert.deepEqual(filters.DEFAULT_TASK_STATUS_FILTERS, ["open", "error"]);
  assert.deepEqual(filters.taskStatusGroupCounts({active: 2, blocked: 1, completed: 3, cancelled: 1}), {open: 2, done: 3, error: 2});
  // An unknown status must not vanish from every bucket.
  assert.deepEqual(filters.taskStatusGroupCounts({something_new: 4}), {open: 4, done: 0, error: 0});
  // The default filter must say how many tasks it hides, so one visible task does
  // not read as "only one task exists".
  const grouped = filters.taskStatusGroupCounts({active: 1, completed: 73});
  const hidden = filters.hiddenTaskStatusGroups(grouped, new Set(filters.DEFAULT_TASK_STATUS_FILTERS));
  assert.deepEqual(hidden, [{group: "done", label: "已完成", count: 73}]);
  assert.equal(filters.hiddenTaskStatusText(hidden), "已隐藏 73 个已完成任务");
  assert.deepEqual(filters.hiddenTaskStatusGroups(grouped, new Set()), []);
  assert.deepEqual(filters.hiddenTaskStatusGroups(grouped, new Set(["done", "error"])), [{group: "open", label: "未完成", count: 1}]);
  assert.equal(filters.hiddenTaskStatusText([]), "");

  assert.equal(filters.taskMatchesFilters(local, new Set(["p1"]), new Set(["open"]), new Set(["local"])), true);
  assert.equal(filters.taskMatchesFilters(remoteA, new Set(["p1"]), new Set(["open"]), new Set()), false);
  assert.equal(filters.taskMatchesFilters(remoteB, new Set(), new Set(["error"]), new Set(["remote:device-b"])), true);

  for (const marker of ["taskProjectFilters", "taskStatusFilters", "taskDeviceFilters", "data-task-filter-option", "data-task-filter-clear"]) {
    assert.match(main, new RegExp(marker));
  }
  assert.match(main, /taskDeviceFilters:\s*new Set\(\[\s*LOCAL_TASK_DEVICE_FILTER\s*\]\)/);
  assert.match(main, /taskStatusFilters:\s*new Set\(DEFAULT_TASK_STATUS_FILTERS\)/);
  assert.match(main, /taskMatchesFilters/);
  // Counts must come from the server aggregate, never from the loaded page.
  assert.match(main, /state\.taskFacetCounts = result\.counts/);
  assert.doesNotMatch(main, /statusCounts\.set\(task\.status/);
  // The count names both numbers instead of reading like a page indicator, and
  // the hidden-status line offers a one-click way back to every status.
  assert.match(main, /匹配 \$\{filtered\.length\} \/ 已加载 \$\{state\.tasks\.length\}/);
  assert.match(main, /data-task-show-all/);
  assert.match(main, /state\.taskStatusFilters\.clear\(\)/);
  // "加载更多" merges the next page without touching the filters, and says so when
  // nothing new matches; live refreshes keep the pages already loaded.
  assert.match(main, /data-task-load-more-empty/);
  assert.match(main, /state\.tasks = mergeByID\(state\.tasks, result\.tasks \|\| \[\]\)/);
  assert.match(main, /api\.tasks\("", \{\s*limit: taskRefreshLimit\(\),\s*order: taskListOrder\s*\}\)/);
  // The task page lists unfinished and failed Tasks before completed ones, and a
  // cursor only continues the order that minted it, so every task list request
  // must ask for the same order.
  assert.match(main, /const taskListOrder = "attention"/);
  for (const call of main.match(/api\.tasks\("", \{[^}]*\}\)/g) || []) {
    assert.match(call, /order: taskListOrder/, `task list request without the attention order: ${call}`);
  }
  assert.match(styles, /\.task-filter-hint/);
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
  const html = renderSyncSettings({scope: "default", enabled: false, endpoint: "", device_id: "", device_name: "device", interval_seconds: 300, token_configured: false, passphrase_configured: false}, {scope: "default", cursor: "", last_error: ""}, 0, [], {running: true, phase: "pulling", detail: "第 3 页 · 收到 20 项", completed: 5, total: 10}, true, "2026-10-30T01:00:00Z");
  assert.doesNotMatch(html, /sync-domain-grid|sync-dependency-note|界面按项目/);
  assert.ok(html.indexOf("同步状态") < html.indexOf("连接设置"));
  assert.match(html, /上次成功/);
  assert.match(html, /下次自动同步/);
  assert.match(html, /待处理冲突/);
  assert.match(html, /高级详情/);
  assert.match(html, /sync-status-pill running/);
  assert.match(html, /data-sync-detail>第 3 页 · 收到 20 项/);
  assert.match(html, /id="sync-conflict-dialog"/);
  assert.match(html, /data-sync-resolution="local"/);
  assert.match(html, /data-sync-resolution="remote"/);
  assert.match(html, /data-sync-resolution="ignore"/);
  assert.doesNotMatch(html, /sync-conflicts-table/);

  const values = new Map([["enabled", "on"], ["endpoint", "https://sync.example.com"], ["device_name", "Laptop"], ["interval_seconds", "60"], ["registration_code", "one-time-code"], ["passphrase", "long-passphrase"]]);
  const payload = syncSettingsPayload(
    {scope: "default", enabled: false, endpoint: "", device_id: "dev_existing", device_name: "", interval_seconds: 300, token_configured: true, passphrase_configured: true},
    {get: name => values.get(name) ?? null, getAll: () => []},
  );
  assert.deepEqual(payload, {enabled: true, endpoint: "https://sync.example.com", device_id: "dev_existing", device_name: "Laptop", interval_seconds: 60, registration_code: "one-time-code", passphrase: "long-passphrase", clear_passphrase: false});
  assert.doesNotMatch(html, /provider_ids|env_group_ids|codex_account_ids/);
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
  globalThis.document = {querySelector: selector => selector === "#sync-settings-form" ? formElement : null, querySelectorAll: () => []};
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

test("sync view loads local state without blocking on a remote preview", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const sync = await readFile(resolve(root, "dist", "sync_settings.js"), "utf8");
  const coreLoad = main.indexOf("api.syncSettings()", main.indexOf('view === "sync"'));
  const coreRendered = main.indexOf("render();", main.indexOf("state.syncNextRunAt", coreLoad));
  assert.ok(coreLoad >= 0 && coreRendered > coreLoad);
  assert.doesNotMatch(main, /api\.syncPreview\(\)/);
  assert.match(main, /next_run_at/);
  assert.match(main, /syncSettingsReady: false/);
  const settingsReady = main.indexOf("state.syncSettingsReady = true;", coreLoad);
  assert.ok(settingsReady > coreLoad && coreRendered > settingsReady);
  assert.match(sync, /settings\.device_name\?\.trim\(\) \|\| settings\.device_id/);
  assert.match(sync, /settingsReady \? "" : "disabled"/);
  assert.match(sync, /正在读取本地设置/);
  assert.match(sync, /#preview-sync/);
  assert.match(sync, /#run-sync/);
  assert.match(sync, /initialRun\?\.running/);
  assert.match(sync, /setInterval\(\(\)=>void poll\(\), 800\)/);
  assert.match(sync, /sync_already_running/);
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

// The web build only strips TypeScript types; it never typechecks. A property
// renamed on one side of a plain object literal therefore compiles and ships,
// and fails at runtime as "Cannot read properties of undefined" — which is
// exactly how the channel knowledge-scope picker broke. Until a real typecheck
// runs in CI, this guards the interfaces where it has happened.
test("channel UI context keys match between the interface and every call site", async () => {
  const root = resolve(import.meta.dirname, "..");
  const channels = await readFile(resolve(root, "src", "channels.ts"), "utf8");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");

  const interfaceBody = channels.match(/interface ChannelUIContext \{([\s\S]*?)\n\}/)?.[1];
  assert.ok(interfaceBody, "ChannelUIContext interface must be found");
  const declared = new Set(
    interfaceBody.split("\n")
      .map(line => line.replace(/\/\/.*$/, "").trim())
      .map(line => line.match(/^([A-Za-z_$][\w$]*)\s*:/)?.[1])
      .filter(Boolean),
  );
  assert.ok(declared.size >= 5, `parsed too few interface keys: ${[...declared]}`);

  // Depth-0 keys of an object literal, so nested objects report their own keys.
  const topLevelKeys = body => {
    let depth = 0;
    let flat = "";
    for (const character of body) {
      if ("{([".includes(character)) { depth++; flat += " "; continue; }
      if ("})]".includes(character)) { depth--; flat += " "; continue; }
      flat += depth === 0 ? character : " ";
    }
    return new Set([...flat.matchAll(/([A-Za-z_$][\w$]*)\s*:\s*state\./g)].map(match => match[1]));
  };

  // The channel contexts are exactly the literals whose own keys carry models,
  // accounts and libraries; larger enclosing literals report different keys.
  const contexts = [];
  for (let index = 0; index < main.length; index++) {
    if (main[index] !== "{") continue;
    let depth = 0;
    let end = index;
    for (; end < main.length; end++) {
      if (main[end] === "{") depth++;
      else if (main[end] === "}" && --depth === 0) break;
    }
    const keys = topLevelKeys(main.slice(index + 1, end));
    if (keys.has("models") && keys.has("accounts") && keys.has("libraries")) contexts.push(keys);
    // Do not skip to `end`: the literal we want is often nested inside this one.
  }
  assert.equal(contexts.length, 2, `expected 2 channel contexts, found ${contexts.length}`);

  for (const provided of contexts) {
    const missing = [...declared].filter(key => !provided.has(key));
    const extra = [...provided].filter(key => !declared.has(key));
    assert.deepEqual(missing, [], `channel context is missing keys ${missing.join(", ")}`);
    assert.deepEqual(extra, [], `channel context passes unknown keys ${extra.join(", ")}`);
  }
});

test("advanced settings expose access scope and listen address", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const types = await readFile(resolve(root, "src", "types.ts"), "utf8");

  for (const marker of ["access-scope-form", "listen-address-form", "访问范围", "监听地址"]) {
    assert.match(main, new RegExp(marker));
  }
  // The scope the UI offers must match what the server accepts.
  assert.match(main, /value="lan"/);
  assert.match(main, /value="local"/);
  // Narrowing access is disruptive, so it must be confirmed before saving.
  assert.match(main, /window\.confirm\([^)]*仅本机/);
  // A stored listen address does not take effect until the process restarts, and
  // the UI must say so instead of implying the change is live.
  assert.match(main, /restart_required/);
  assert.match(main, /重启 AHA2 后生效/);
  assert.match(api, /\/api\/v1\/settings\/network/);
  assert.match(types, /access_scope: string/);
  assert.match(types, /NetworkSettings/);
});

test("advanced settings cards use the same details action label", async () => {
  const root = resolve(import.meta.dirname, "..");
  const i18n = await readFile(resolve(root, "src", "i18n.ts"), "utf8");
  const actions = [...i18n.matchAll(/"advanced\.(prompts|sync|proxy|data|mcp|other)\.action": "([^"]+)"/g)].map(match => match[2]);
  assert.equal(actions.length, 12);
  assert.deepEqual([...new Set(actions)].sort(), ["View details", "查看详情"]);
});

test("sidebar pages are not duplicated as advanced settings entries", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const cards = [...main.matchAll(/<article class="panel advanced-tool-card">[^\n]*?data-view="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(cards.sort(), ["data", "mcp", "other", "prompts", "proxy", "sync"]);
  assert.match(main, /resources: \(\) => shell\(renderResourcesPage\(\{/);
  assert.match(main, /"pet-workbench": \(\) => shell\(petWorkbenchView\(\)\)/);
  assert.doesNotMatch(main, /(?:hardware|"pet-workbench"): \(\) => advancedSubview/);
});

test("sidebar and mobile navigation expose pet workbench and resources", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const i18n = await readFile(resolve(root, "src", "i18n.ts"), "utf8");
  assert.match(main, /\["resources", "hardware", "nav\.resources"\]/);
  assert.match(main, /\["pet-workbench", "bot", "petWorkbench\.title"\]/);
  assert.match(main, /const mobileNav = \[\.\.\.nav, \["advanced", "settings", "nav\.advanced"\] as const\]/);
  assert.match(i18n, /"petWorkbench\.title": "宠物工作台"/);
  assert.match(i18n, /"petWorkbench\.title": "Pet workbench"/);
});

test("pet workbench follows tab exposes unfollow and ignore actions", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const i18n = await readFile(resolve(root, "src", "i18n.ts"), "utf8");
  assert.match(main, /data-pet-workbench-tab="follows"/);
  assert.match(main, /renderPetWatches\(view\.watches\)/);
  assert.match(main, /data-pet-watch-follow/);
  assert.match(main, /data-pet-watch-unfollow/);
  assert.match(main, /data-pet-watch-ignore="true"/);
  assert.match(main, /data-pet-watch-ignore="false"/);
  assert.match(main, /petWatchActionKeys\(item\)/);
  assert.match(api, /followPetTask/);
  assert.match(api, /removePetTaskWatch/);
  assert.match(api, /setPetTaskWatchIgnored/);
  assert.match(i18n, /"petWorkbench\.follows": "关注"/);
  assert.match(i18n, /"petWorkbench\.follows": "Following"/);
  assert.match(i18n, /"petWorkbench\.followTask": "关注"/);
  assert.match(i18n, /"petWorkbench\.followTask": "Follow"/);
  assert.match(i18n, /"petWorkbench\.restoreIgnored": "恢复"/);
  assert.match(i18n, /"petWorkbench\.restoreIgnored": "Restore"/);
});

test("pet workbench actions cover unfollowed, followed, ignored, and restored rows", async () => {
  const root = resolve(import.meta.dirname, "..");
  const {petWatchActionKeys} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));
  // An unfollowed row offers follow and ignore without a prior follow.
  assert.deepEqual(petWatchActionKeys({watched: false, ignored: false}), ["follow", "ignore"]);
  // A followed row keeps unfollow plus ignore.
  assert.deepEqual(petWatchActionKeys({watched: true, ignored: false}), ["unfollow", "ignore"]);
  // Restoring a followed row resumes following; restoring an ignore-only row
  // returns it to the unfollowed list instead of silently following it.
  assert.deepEqual(petWatchActionKeys({watched: true, ignored: true}), ["restoreFollow"]);
  assert.deepEqual(petWatchActionKeys({watched: false, ignored: true}), ["restoreIgnored"]);

  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  // The unwatched branch now renders the same ignore action, and its restore
  // button reuses the existing ignored=false handler.
  assert.match(main, /case "ignore": return `<button[^`]*data-pet-watch-ignore="true"/);
  assert.match(main, /case "restoreIgnored": return `<button[^`]*data-pet-watch-ignore="false"/);
  assert.match(main, /\[data-pet-watch-ignore\]"\)\.forEach[\s\S]*?const ignored = button\.dataset\.petWatchIgnore === "true"[\s\S]*?api\.setPetTaskWatchIgnored\(taskID, ignored\)[\s\S]*?await load\(\)/);
});

test("task detail renders owner permission request decisions", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const tools = await readFile(resolve(root, "src", "task_tools.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const types = await readFile(resolve(root, "src", "types.ts"), "utf8");
  assert.match(main, /taskPermissionPanelHtml\(detail\)/);
  assert.match(main, /<details class="task-permission-item/);
  assert.match(main, /task-permission-detail/);
  assert.match(main, /data-task-permission-decision="approve"/);
  assert.match(main, /data-task-permission-decision="reject"/);
  assert.match(main, /bindTaskPermissionActions/);
  assert.match(main, /syncTaskPermissionIndicators/);
  // The inline block above the conversation was replaced by the toolbar panel.
  assert.doesNotMatch(main, /<div id="task-permission-slot">/);
  assert.doesNotMatch(main, /class="conversation-top"/);
  assert.match(tools, /\{id: "permissions", label: "权限申请", iconName: "shield"\}/);
  assert.match(tools, /task-tool-badge/);
  assert.match(api, /resolveTaskPermissionRequest/);
  assert.match(types, /interface TaskPermissionRequest/);
  assert.match(types, /permission_requests\?: TaskPermissionRequest\[\]/);
});

test("completed tasks freeze the composer behind a confirmed reopen", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const handlers = await readFile(resolve(root, "..", "internal", "httpapi", "task_handlers.go"), "utf8");

  // A completed Task renders the reopen control instead of an input box, and
  // reopening only happens after an explicit confirmation dialog.
  assert.match(main, /function completedComposerHtml/);
  assert.match(main, /id="reopen-task"/);
  assert.match(main, /id="reopen-task-dialog"/);
  assert.match(main, /确认重新打开/);
  const dialogBinding = /function bindReopenTaskControls\(\): void \{[\s\S]*?\n\}/.exec(main)?.[0] || "";
  assert.match(dialogBinding, /showModal\(\)/);
  assert.match(dialogBinding, /await api\.reopenTask\(/);
  // The reopen call must live in the dialog submit handler, not the button click.
  const clickBinding = /#reopen-task"\)\?\.addEventListener\("click", ([^\n]*)/.exec(main)?.[1] || "";
  assert.doesNotMatch(clickBinding, /reopenTask/);

  // The send button is type=submit and Enter calls requestSubmit(), so the
  // composer must stay a <form> for every status except "completed". Rendering a
  // <div> in the normal state silently breaks both ways of sending a message.
  const elementHelper = /function messageComposerElement\(detail: TaskDetail\)[\s\S]*?\n\}/.exec(main)?.[0] || "";
  assert.match(elementHelper, /completed \? "div" : "form"/, "the composer element type must depend on the completed status");
  const openTagHelper = /function messageComposerOpenTag\([\s\S]*?\n\}/.exec(main)?.[0] || "";
  assert.match(openTagHelper, /\$\{messageComposerElement\(detail\)\}/, "the rendered composer tag must come from messageComposerElement");
  const nodeHelper = /function messageComposerNode\([\s\S]*?\n\}/.exec(main)?.[0] || "";
  assert.match(nodeHelper, /messageComposerOpenTag\(detail, options\)/, "live composer rebuild must reuse the first-render markup");
  assert.match(nodeHelper, /messageComposerElement\(detail\)/, "live composer rebuild must close the tag it opened");
  // A stray hard-coded <div id="message-form"> in the task view would reintroduce it.
  assert.doesNotMatch(main, /<div id="message-form"/, "the composer must not be hard-coded as a div");

  // Completed status makes the Task read-only for editing, and the server
  // enforces the same freeze rather than trusting the UI.
  assert.match(main, /readOnly: archivedReadOnly \|\| remoteReadOnly \|\| completed/);
  assert.match(handlers, /task_completed_read_only/);
  assert.match(handlers, /func \(s \*Server\) rejectCompletedTaskWrite/);
  for (const guard of ["updateTaskTitle", "updateTaskCollaboration", "updateAgentConfig"]) {
    const body = new RegExp(`func \\(s \\*Server\\) ${guard}\\([\\s\\S]*?\\n\\}`).exec(handlers)?.[0] || "";
    assert.match(body, /rejectCompletedTaskWrite/, `${guard} must reject completed-task writes`);
  }
});

// Background control was removed: control is foreground-only, so the panel must
// offer no mode toggle and the backend must expose no background entry point.
test("background control is gone and control is foreground-only", async () => {
  const root = resolve(import.meta.dirname, "..");
  const source = await readFile(resolve(root, "src", "desktop_panel.ts"), "utf8");
  const native = await readFile(resolve(root, "..", "internal", "desktop", "native.go"), "utf8");
  const manager = await readFile(resolve(root, "..", "internal", "desktop", "manager.go"), "utf8");

  // No mode switch is offered, and no background-only C# source is embedded.
  assert.doesNotMatch(source, /data-desktop-mode/, "the panel still offers a control-mode toggle");
  assert.doesNotMatch(source, /"background"/, "the panel still references the removed background mode");
  assert.doesNotMatch(native, /native_background|native_browser/, "the removed background helpers are still embedded");

  // The only control mode the backend accepts is foreground, with consent.
  assert.match(manager, /if mode != "foreground" \{\s*return Status\{\}, failure\("invalid_mode"\)/);
  assert.match(manager, /case "click", "double_click", "move", "drag", "scroll", "text", "key", "focus":/);
  // Element-shaped actions belonged to the removed adapter.
  assert.doesNotMatch(manager, /case "invoke", "set_value"/, "element actions are still accepted");
  assert.doesNotMatch(native, /SelectBackgroundWindow|background_observe/, "a background entry point remains");
  // The element list may stay as an inspection aid, but it must not dispatch.
  assert.doesNotMatch(source, /data-desktop-action/, "element action buttons still render");
});

test("pet settings live inside the pet workbench", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const assistant = await readFile(resolve(root, "src", "assistant.ts"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");

  assert.match(main, /data-pet-workbench-tab="settings"/);
  assert.match(main, /id="pet-settings-host"/);
  assert.match(main, /void showPetSettings\(\)/);
  assert.match(main, /petWorkbenchTab = "settings"/);
  // The standalone advanced-settings card is gone; the workbench is the entry.
  assert.doesNotMatch(main, /<strong>宠物<\/strong>/);
  // Saving inline keeps the owner on the settings tab instead of bouncing to
  // the advanced page, and the inline footer does not advertise a return path
  // that no longer applies.
  assert.match(assistant, /const inline = settingsPanel\.classList\.contains\("assistant-settings-inline"\)/);
  assert.match(assistant, /if \(inline\) \{\s*renderPetSettings\(\);/);
  assert.match(styles, /assistant-settings-inline #pet-settings-back \{ display: none; \}/);
});

test("pet factory reset requires typed confirmation", async () => {
  const root = resolve(import.meta.dirname, "..");
  const assistant = await readFile(resolve(root, "src", "assistant.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const handler = await readFile(resolve(root, "..", "internal", "httpapi", "pet_handlers.go"), "utf8");

  assert.match(assistant, /const PET_RESET_CONFIRM = "重置宠物"/);
  assert.match(assistant, /id="pet-reset-confirm"/);
  assert.match(assistant, /resetExecuteButton\.disabled = resetRunning \|\| resetConfirmInput\.value\.trim\(\) !== PET_RESET_CONFIRM/);
  assert.match(assistant, /api\.resetPet\(PET_RESET_CONFIRM\)/);
  assert.match(assistant, /applyPetEnabled\(view\.assistant\.pet_enabled !== false\)/);
  assert.match(styles, /\.assistant-layer > dialog \{ pointer-events: auto; \}/);
  assert.match(api, /\/api\/v1\/pet\/reset/);
  assert.match(handler, /Confirm string `json:"confirm"`/);
  assert.match(handler, /s\.app\.ResetOwnerPet/);
});

test("pet speech uses an opaque collision-aware bubble", async () => {
  const root = resolve(import.meta.dirname, "..");
  const assistant = await readFile(resolve(root, "src", "assistant.ts"), "utf8");
  const bubble = await readFile(resolve(root, "src", "pet_bubble.ts"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");

  assert.match(assistant, /computeSpeechBubblePlacement/);
  assert.match(bubble, /export function computeSpeechBubblePlacement/);
  assert.match(bubble, /export function petBubbleRectsOverlap/);
  assert.match(bubble, /tail: "tail-left"/);
  assert.match(assistant, /Array\.from\(text\.replace\(/);
  assert.match(styles, /\.assistant-bubble\.speech \{[^}]*background: rgba\(255,255,255,\.98\)/);
  assert.match(styles, /\.assistant-bubble\.speech \{[^}]*pointer-events: none/);
  assert.match(styles, /\.assistant-bubble\.speech\.tail-left::after/);
});

test("data management shows scoped sizes and safe cleanup actions", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const types = await readFile(resolve(root, "src", "types.ts"), "utf8");
  const backend = await readFile(resolve(root, "..", "internal", "httpapi", "data_management.go"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");

  assert.match(main, /function dataManagementView/);
  assert.match(main, /function bindDataManagementPage/);
  assert.match(main, /data-data-cleanup="backups"/);
  assert.match(main, /data-data-cleanup="logs"/);
  assert.match(main, /data-backup-keep/);
  assert.match(main, /window\.confirm/);
  assert.match(main, /class="data-category"/);
  // The summary card and table notes must not inherit the form helper's
  // negative margin, and narrow screens stack the table into readable rows.
  assert.ok(styles.includes(".data-management-page .field-help { margin: 0; }"));
  assert.ok(styles.includes(".data-summary { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 14px 16px; }"));
  assert.match(styles, /@media \(max-width: 640px\) \{[\s\S]*?\.data-table thead \{ display: none; \}/);
  assert.match(styles, /\.data-table tr \{ display: block; padding: 12px 16px;/);
  assert.match(api, /\/api\/v1\/system\/data/);
  assert.match(api, /\/api\/v1\/system\/data\/cleanup/);
  assert.match(types, /interface DataInventory/);
  assert.match(types, /interface DataCategory/);
  // Cleanup is deliberately limited to install backups and root log files; the
  // database, secrets, attachments and runtime data are never candidates.
  assert.match(backend, /strings\.HasPrefix\(entry\.Name\(\), "user-installed-"\)/);
  assert.match(backend, /case "backups":/);
  assert.match(backend, /case "logs":/);
  assert.doesNotMatch(backend, /case "attachments":/);
  assert.doesNotMatch(backend, /case "database":/);
});

test("the app shell, tab and tray share the AHA2 brand mark", async () => {
  const root = resolve(import.meta.dirname, "..");
  const index = await readFile(resolve(root, "index.html"), "utf8");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const build = await readFile(resolve(root, "..", "scripts", "build-web.mjs"), "utf8");
  const logo = await readFile(resolve(root, "..", "assets", "aha-logo.svg"), "utf8");

  // The tab, shell and tray all use the supplied dark/cyan AHA SVG mark.
  assert.match(index, /rel="icon" type="image\/svg\+xml" href="\/assets\/aha-logo\.svg"/);
  assert.match(index, /rel="icon" href="\/favicon\.ico"/);
  assert.match(main, /class="brand-mark"><img src="\/assets\/aha-logo\.svg"/);
  assert.doesNotMatch(main, /class="brand-mark">A</);
  assert.match(styles, /\.brand-mark img \{[^}]*width: 100%;[^}]*height: 100%;/);
  assert.match(logo, /fill="#0f172a"/);
  assert.match(logo, /stroke="#38bdf8"/);
  assert.match(logo, /M12 48 22 17 32 48 42 17 52 48/);
  assert.match(build, /copyFile\(resolve\(root, "assets", "aha-logo\.svg"\)/);
  assert.match(build, /copyFile\(resolve\(root, "assets", "aha2\.ico"\), resolve\(output, "favicon\.ico"\)/);
});

test("i18n catalogs and language preference are wired into the shell", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const i18n = await import(pathToFileURL(resolve(root, "dist", "i18n.js")).href);

  i18n.applyLocale("en-US");
  assert.equal(i18n.t("nav.projects"), "Projects");
  assert.equal(i18n.t("language.en"), "English (preview)");
  assert.equal(i18n.t("data.cleanedBackups", {count: 2, size: "3 GB"}), "Removed 2 old backups and freed 3 GB.");
  i18n.applyLocale("zh-CN");
  assert.equal(i18n.t("nav.projects"), "项目");
  assert.equal(i18n.resolveLocale("en-US"), "en-US");
  assert.equal(i18n.resolveLocale("zh-CN"), "zh-CN");

  assert.match(api, /\/api\/v1\/settings\/general/);
  assert.match(main, /id="language-setting"/);
  assert.match(main, /api\.updateGeneralSettings\(language\)/);
  assert.match(main, /applyLocale\(result\.general\.language\)/);
  assert.match(main, /generalSettings: \{language: "auto"\}/);
});

test("list pagination clamps pages and keeps counts visible", async () => {
  const {paginateItems, paginationHTML, textMatchesQuery} = await import(pathToFileURL(resolve(import.meta.dirname, "..", "dist", "ui_helpers.js")).href);
  const empty = paginateItems([], 3, 8);
  assert.deepEqual(empty, {items: [], page: 1, pages: 1, total: 0});
  const items = Array.from({length: 17}, (_, index) => index);
  assert.deepEqual(paginateItems(items, 99, 8), {items: [16], page: 3, pages: 3, total: 17});
  assert.equal(paginateItems(items, 0, 8).page, 1);
  assert.equal(paginateItems(items, 2, 8).items.length, 8);
  assert.equal(paginationHTML("models", empty), "");
  const single = paginationHTML("providers", paginateItems([1, 2], 1, 5));
  assert.match(single, /共 2 个/);
  assert.doesNotMatch(single, /data-page-kind/);
  const paged = paginationHTML("models", paginateItems(items, 2, 8));
  assert.match(paged, /data-page-kind="models"/);
  assert.match(paged, /第 2\/3 页/);
  assert.match(paged, /data-page-delta="-1"/);
  assert.match(paged, /data-page-delta="1"/);
  assert.equal(textMatchesQuery(["Claude Sonnet", "huailai"], "sonnet"), true);
  assert.equal(textMatchesQuery(["Claude Sonnet"], "codex"), false);
  assert.equal(textMatchesQuery(["x"], "  "), true);
});

test("models page paginates accounts, providers and models", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const codex = await readFile(resolve(root, "src", "codex_accounts.ts"), "utf8");
  assert.match(main, /const MODEL_PAGE_SIZE = 8/);
  assert.match(main, /const PROVIDER_PAGE_SIZE = 5/);
  assert.match(main, /const CODEX_ACCOUNT_PAGE_SIZE = 3/);
  assert.match(main, /paginateItems\(visibleModels, state\.modelPage, MODEL_PAGE_SIZE\)/);
  assert.match(main, /paginateItems\(state\.providers, state\.providerPage, PROVIDER_PAGE_SIZE\)/);
  assert.match(main, /paginateItems\(state\.codexAccounts, state\.codexAccountPage, CODEX_ACCOUNT_PAGE_SIZE\)/);
  assert.match(main, /id="model-search"/);
  assert.match(main, /data-1p-ignore/);
  assert.match(main, /readonly placeholder="搜索模型/);
  assert.match(main, /modelSearchUserInput/);
  assert.match(main, /nextView === "models"/);
  assert.match(main, /data-page-kind/);
  assert.match(codex, /footer = ""/);
});

test("built-in channel MCP is read-only and auto-injected in the MCP page", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const types = await readFile(resolve(root, "src", "types.ts"), "utf8");
  const agents = await readFile(resolve(root, "src", "task_agents.ts"), "utf8");
  assert.match(types, /auto_inject\?: boolean/);
  assert.match(main, /item\.auto_inject/);
  assert.match(main, /自动注入/);
  assert.match(main, /由当前 Turn 动态注入/);
  assert.match(main, /state\.mcpServers\.filter\(item => item\.enabled && !item\.builtin\)/);
  assert.match(agents, /!item\.auto_inject/);
});

test("knowledge page filters projects and skills, exposes skill details and auto-attach", async () => {
  const root = resolve(import.meta.dirname, "..");
  const knowledge = await readFile(resolve(root, "src", "knowledge_workspace.ts"), "utf8");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const agents = await readFile(resolve(root, "src", "task_agents.ts"), "utf8");
  const types = await readFile(resolve(root, "src", "types.ts"), "utf8");
  assert.match(knowledge, /PROJECT_BATCH_SIZE = 10/);
  assert.match(knowledge, /SKILL_BATCH_SIZE = 6/);
  assert.match(knowledge, /data-knowledge-project-more/);
  assert.match(knowledge, /data-knowledge-skill-more/);
  assert.match(knowledge, /id="knowledge-project-search"/);
  assert.match(knowledge, /id="knowledge-skill-search"/);
  assert.match(knowledge, /id="knowledge-skill-scope"/);
  assert.match(knowledge, /id="knowledge-skill-project"/);
  assert.match(knowledge, /data-skill-view/);
  assert.match(knowledge, /skill-detail-dialog/);
  assert.match(knowledge, /name="auto_attach"/);
  assert.match(knowledge, /payload\.auto_attach/);
  assert.match(types, /auto_attach\?: boolean/);
  assert.match(main, /autoTaskSkillIDs/);
  assert.match(main, /includeAutoDefaults/);
  assert.match(main, /apply_auto_skills: false/);
  assert.match(main, /active\.dataset\.allowRerender/);
  assert.match(agents, /auto_attach/);
  assert.match(knowledge, /PROPOSAL_PAGE_SIZE = 10/);
  assert.match(knowledge, /data-knowledge-proposal-page/);
  assert.match(knowledge, /knowledgeProposals\(\{status: "all"/);
  assert.match(main, /includeProposals: false/);
  assert.match(main, /knowledgeProposals\(\{status: "pending", limit: 0\}\)/);
});
