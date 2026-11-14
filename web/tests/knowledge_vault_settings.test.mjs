// Phase 3 settings panel: status copy, conditional actions and the runtime
// switch calls. The module is lazy in production, so it is imported directly
// from the build output like the other page chunks.
import test from "node:test";
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {resolve} from "node:path";
import {readFile} from "node:fs/promises";

const root = resolve(import.meta.dirname, "..");
const panelPath = resolve(root, "build", "knowledge_vault_settings.js");
const panel = await import(pathToFileURL(panelPath).href);
// The build appends one shared ?v= to every module URL so the browser keeps a
// single API singleton. Import the exact same URL or the stubs below would land
// on a second instance that the panel never calls.
const panelSource = await readFile(panelPath, "utf8");
const apiVersion = panelSource.match(/from "\.\/api\.js(\?v=[^"]+)?"/)?.[1] ?? "";
const {api} = await import(`${pathToFileURL(resolve(root, "build", "api.js")).href}${apiVersion}`);

const ready = {
  enabled: true,
  source: "database",
  vault_ready: true,
  vault_initialized: true,
  index_ready: true,
  vault_name: "AHA2 Knowledge",
  vault_path: "/srv/vault",
  index_path: "/srv/index.db",
  entry_count: 5,
  rebuilt_at: "2026-11-14T13:38:42Z",
};

function emptyVault(patch = {}) {
  return {
    enabled: false,
    source: "startup",
    vault_ready: true,
    vault_initialized: false,
    index_ready: false,
    vault_path: "/srv/vault",
    index_path: "/srv/index.db",
    entry_count: 0,
    warning: "index_missing",
    ...patch,
  };
}

function context() {
  const applied = [];
  const messages = [];
  return {
    applied,
    messages,
    render() {},
    setMessage(kind, message) { messages.push({kind, message}); },
    onSettingsApplied(settings) { applied.push(settings); },
  };
}

function stub(name, replacement) {
  const original = api[name];
  api[name] = replacement;
  return () => { api[name] = original; };
}

test("settings panel shows vault, index and source for an enabled vault", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: ready}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /已启用 · 来源：数据库设置/);
    assert.match(html, /已就绪（AHA2 Knowledge）/);
    assert.match(html, /已就绪 · 5 条/);
    assert.match(html, /type="checkbox" checked/);
    assert.match(html, /\/srv\/vault/);
    // An initialized vault must not offer the destructive-ish init action.
    assert.doesNotMatch(html, /knowledge-vault-init/);
    // Rebuilding moved to the knowledge page's gear panel; the advanced panel
    // keeps only the switch, sample init, status and path.
    assert.doesNotMatch(html, /knowledge-vault-rebuild/);
    assert.match(html, /知识库页的齿轮设置中重建索引/);
    assert.equal(ctx.applied.length, 1);
    assert.equal(ctx.applied[0].enabled, true);
  } finally {
    restore();
  }
});

test("an empty vault offers sample initialization before enabling the index actions", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: emptyVault()}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /已关闭 · 来源：启动参数/);
    assert.match(html, /未初始化/);
    assert.match(html, /id="knowledge-vault-init"/);
    assert.doesNotMatch(html, /knowledge-vault-rebuild/);
    assert.match(html, /索引尚未建立/);
  } finally {
    restore();
  }
});

test("the toggle persists through the API and updates the shell switch state", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const calls = [];
  const restoreGet = stub("knowledgeVaultSettings", async () => ({ok: true, settings: ready}));
  const restorePut = stub("updateKnowledgeVaultSettings", async enabled => {
    calls.push(enabled);
    return {ok: true, settings: {...ready, enabled}};
  });
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    await panel.__knowledgeVaultSettingsActionsForTest.setEnabled(false);
    assert.deepEqual(calls, [false]);
    assert.equal(ctx.applied.at(-1).enabled, false);
    assert.match(panel.renderKnowledgeVaultSettingsPanel(ctx), /已关闭 · 来源：数据库设置/);
    assert.ok(ctx.messages.some(message => message.kind === "notice" && message.message === "文件知识库已关闭"));
  } finally {
    restoreGet();
    restorePut();
  }
});

test("sample initialization confirms first, then applies the auto-enabled settings", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  let initCalls = 0;
  const restoreGet = stub("knowledgeVaultSettings", async () => ({ok: true, settings: emptyVault()}));
  const restoreInit = stub("initializeKnowledgeVaultSamples", async () => {
    initCalls++;
    return {
      ok: true,
      settings: {...ready, enabled: true},
      created: ["global/general/示例/欢迎使用.md"],
      samples: 4,
      rebuild: {entries: 5, rebuilt_at: "2026-11-14T13:38:42Z"},
    };
  });
  const originalWindow = globalThis.window;
  globalThis.window = {confirm: () => true};
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    await panel.__knowledgeVaultSettingsActionsForTest.initializeSamples();
    assert.equal(initCalls, 1);
    assert.equal(ctx.applied.at(-1).enabled, true);
    assert.ok(ctx.messages.some(message => message.kind === "notice" && message.message.includes("4 个示例文档")));
  } finally {
    globalThis.window = originalWindow;
    restoreGet();
    restoreInit();
  }
});

test("an older server reports the panel as unsupported instead of failing the page", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => {
    throw Object.assign(new Error("not found"), {status: 404});
  });
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /当前版本不支持/);
    assert.match(html, /没有文件知识库设置接口/);
    assert.doesNotMatch(html, /id="knowledge-vault-enabled"/);
  } finally {
    restore();
  }
});
test("a failing status request is shown once and retried explicitly, not in a render loop", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  let calls = 0;
  let fail = true;
  const restore = stub("knowledgeVaultSettings", async () => {
    calls++;
    if (fail) throw new Error("network down");
    return {ok: true, settings: ready};
  });
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    assert.equal(calls, 1);
    // Several plain renders must not start more requests while the error is
    // displayed; the user has an explicit retry path instead.
    panel.renderKnowledgeVaultSettingsPanel(ctx);
    panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.equal(calls, 1);
    assert.match(panel.renderKnowledgeVaultSettingsPanel(ctx), /role="alert"/);
    fail = false;
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    assert.equal(calls, 2);
    assert.match(panel.renderKnowledgeVaultSettingsPanel(ctx), /已启用 · 来源：数据库设置/);
  } finally {
    restore();
  }
});
