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
  vault_writable: true,
  index_ready: true,
  index_writable: true,
  vault_name: "AHA2 Knowledge",
  vault_path: "/srv/vault",
  index_path: "/srv/index.db",
  entry_count: 5,
  rebuilt_at: "2026-11-14T13:38:42Z",
  configured: true,
  readiness: {
    status: "ready",
    can_enable: true,
    default_enabled: true,
    switch_visible: true,
    checks: [{id: "index_schema", status: "ok", message: "索引 schema 可读"}],
  },
  legacy: {entries: 0, skills: 0, skill_packages: 0},
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
    configured: false,
    readiness: {
      status: "needs_rebuild",
      can_enable: false,
      default_enabled: true,
      switch_visible: false,
      message: "知识库或索引尚未就绪，请先重建索引。",
      missing: ["vault_initialized", "index_schema"],
      checks: [{id: "index_fresh", status: "fail", message: "索引尚未建立"}],
    },
    legacy: {entries: 0, skills: 0, skill_packages: 0},
    ...patch,
  };
}

function upgradeVault(patch = {}) {
  return {
    ...emptyVault(),
    configured: false,
    readiness: {
      status: "needs_migration",
      can_enable: false,
      default_enabled: false,
      switch_visible: true,
      message: "升级后需要先迁移旧知识库，或导入已有知识库，才能启用文件知识库。",
      missing: ["migration"],
      checks: [{id: "migration", status: "fail", message: "检测到旧知识库（3 条知识、1 个技能），迁移或导入尚未验证"}],
    },
    legacy: {entries: 3, skills: 1, skill_packages: 1},
    ...patch,
  };
}

// An upgrade where the owner already runs on a populated vault but the
// migration/import metadata is still empty. Legacy is a rollback copy only.
function inUseUpgradeVault(patch = {}) {
  return {
    ...ready,
    enabled: true,
    configured: true,
    entry_count: 992,
    migration_verified_at: undefined,
    readiness: {
      status: "ready",
      can_enable: true,
      default_enabled: false,
      switch_visible: true,
      message: "文件知识库已就绪并在使用；旧知识库仅作只读回滚保留，不会被迁移覆盖。",
      checks: [{
        id: "migration",
        status: "ok",
        message: "文件知识库已启用并在使用；旧知识库仅作只读回滚保留，不会被迁移覆盖",
      }],
    },
    legacy: {entries: 5, skills: 2, skill_packages: 2},
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
    assert.match(html, /已关闭 · 来源：启动默认/);
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
    assert.ok(ctx.messages.some(message => message.kind === "notice" && message.message.includes("已关闭，已立即回到旧知识库")));
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

test("fresh install hides the switch and keeps the vault as the read-only default", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const fresh = emptyVault({enabled: true, source: "startup"});
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: fresh}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.doesNotMatch(html, /id="knowledge-vault-enabled"/);
    assert.match(html, /新安装默认使用文件知识库/);
    assert.match(html, /就绪度：待初始化或重建索引/);
    assert.match(html, /id="knowledge-vault-init"/);
  } finally {
    restore();
  }
});

test("an upgrade shows the legacy rollback switch, blocks enabling and offers migration", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: upgradeVault()}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /当前使用旧知识库回退/);
    assert.match(html, /type="checkbox"[^>]*disabled/);
    assert.match(html, /id="knowledge-vault-migrate"/);
    assert.doesNotMatch(html, /id="knowledge-vault-init"/);
    assert.match(html, /未满足：migration/);
    assert.match(html, /3 条 · 1 个技能/);
  } finally {
    restore();
  }
});

test("an enabled in-use upgrade hides migration and explains the legacy rollback copy", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: inUseUpgradeVault()}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /type="checkbox" checked/);
    assert.doesNotMatch(html, /type="checkbox"[^>]*disabled/);
    // The migration button would export the legacy database over the live
    // vault, so a running upgrade must never render it.
    assert.doesNotMatch(html, /id="knowledge-vault-migrate"/);
    assert.doesNotMatch(html, /id="knowledge-vault-init"/);
    assert.match(html, /旧知识库仅作只读回滚保留，不会被迁移覆盖/);
    assert.match(html, /仅作只读回滚保留/);
    assert.match(html, /已就绪 · 992 条/);
    assert.match(html, /就绪度：已就绪/);
  } finally {
    restore();
  }
});

test("an enabled degraded upgrade warns about unbound projects without offering migration", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const degraded = inUseUpgradeVault({
    readiness: {
      status: "degraded",
      can_enable: true,
      default_enabled: false,
      switch_visible: true,
      message: "文件知识库已启用，但部分项目仍回退旧知识库或索引需要关注；旧知识库仅作只读回滚保留，不会被迁移覆盖。",
      checks: [{
        id: "manifest_binding",
        status: "warn",
        message: "1 个项目未绑定，这些项目继续使用旧知识库",
      }],
      unbound_projects: ["project-unbound"],
    },
  });
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: degraded}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /文件知识库已启用，但/);
    assert.match(html, /1 个项目未绑定/);
    assert.doesNotMatch(html, /可以启用，但/);
    assert.doesNotMatch(html, /id="knowledge-vault-migrate"/);
  } finally {
    restore();
  }
});

test("migration verifies, persists and enables the vault with a backup notice", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  let calls = 0;
  const restoreGet = stub("knowledgeVaultSettings", async () => ({ok: true, settings: upgradeVault()}));
  const restoreMigrate = stub("request", async (path, options) => {
    assert.equal(path, "/api/v1/settings/knowledge-vault/migrate");
    assert.equal(options.method, "POST");
    calls++;
    return {
      settings: {...ready, configured: true, migration_verified_at: "2026-11-14T14:00:00Z"},
      migration: {
        source: "legacy-migration", vault_path: "/srv/vault", database_path: "/srv/aha2.db",
        backup_path: "/srv/backups/knowledge-migration-1", source_entries: 3,
        exported_entries: 3, indexed_entries: 3, verified_entries: 3,
        titles_verified: 3, hashes_verified: 3, source_skills: 1,
        exported_skills: 1, verified_skills: 1, verified_at: "2026-11-14T14:00:00Z",
      },
    };
  });
  const originalWindow = globalThis.window;
  globalThis.window = {confirm: () => true};
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    await panel.__knowledgeVaultSettingsActionsForTest.migrateLegacy();
    assert.equal(calls, 1);
    assert.equal(ctx.applied.at(-1).enabled, true);
    assert.ok(ctx.messages.some(message => message.message.includes("已校验 3 条知识")));
    assert.match(panel.renderKnowledgeVaultSettingsPanel(ctx), /type="checkbox" checked/);
  } finally {
    globalThis.window = originalWindow;
    restoreGet();
    restoreMigrate();
  }
});

test("an existing local vault imports through the API and reports unbound projects", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const calls = [];
  const restoreGet = stub("knowledgeVaultSettings", async () => ({ok: true, settings: emptyVault()}));
  const restoreImport = stub("request", async (path, options) => {
    assert.equal(path, "/api/v1/settings/knowledge-vault/import");
    calls.push(JSON.parse(options.body));
    return {
      settings: {...ready, import_verified_at: "2026-11-14T14:10:00Z"},
      import: {
        source: "existing-vault", kind: "local", source_path: "/srv/existing",
        vault_path: "/srv/vault", imported_files: 12, project_count: 2,
        unbound_projects: ["project-unbound"], indexed_entries: 10,
        verified_at: "2026-11-14T14:10:00Z",
      },
    };
  });
  const originalWindow = globalThis.window;
  const originalDocument = globalThis.document;
  globalThis.window = {confirm: () => true};
  globalThis.document = {
    querySelector(selector) {
      if (selector === 'input[name="knowledge-vault-import-kind"]:checked') return {value: "local"};
      if (selector === "#knowledge-vault-import-path") return {value: "/srv/existing"};
      if (selector === "#knowledge-vault-import-remote") return {value: ""};
      if (selector === "#knowledge-vault-import-branch") return {value: ""};
      return null;
    },
  };
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    await panel.__knowledgeVaultSettingsActionsForTest.importExistingVault();
    assert.deepEqual(calls, [{kind: "local", path: "/srv/existing", remote: "", branch: ""}]);
    assert.equal(ctx.applied.at(-1).enabled, true);
    assert.ok(ctx.messages.some(message => message.message.includes("1 个项目未绑定")));
  } finally {
    globalThis.window = originalWindow;
    globalThis.document = originalDocument;
    restoreGet();
    restoreImport();
  }
});

test("a degraded vault can be enabled with an explicit fallback warning", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const degraded = {
    ...ready,
    enabled: false,
    readiness: {
      status: "degraded",
      can_enable: true,
      default_enabled: true,
      switch_visible: true,
      message: "文件知识库可以启用，但部分项目仍会回退旧知识库或索引需要关注。",
      checks: [{id: "manifest_binding", status: "warn", message: "1 个项目未绑定，这些项目继续使用旧知识库"}],
      unbound_projects: ["project-unbound"],
    },
    configured: false,
  };
  const restoreGet = stub("knowledgeVaultSettings", async () => ({ok: true, settings: degraded}));
  const restorePut = stub("updateKnowledgeVaultSettings", async enabled => ({ok: true, settings: {...degraded, enabled}}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /可以启用，但/);
    assert.doesNotMatch(html, /type="checkbox"[^>]*disabled/);
    await panel.__knowledgeVaultSettingsActionsForTest.setEnabled(true);
    assert.equal(ctx.applied.at(-1).enabled, true);
    assert.ok(ctx.messages.some(message => message.message.includes("部分项目仍会回退旧知识库")));
  } finally {
    restoreGet();
    restorePut();
  }
});

test("an enabled vault with a broken index shows an Agent fallback error", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const broken = {
    ...ready,
    index_ready: false,
    entry_count: 0,
    readiness: {
      status: "needs_rebuild",
      can_enable: false,
      default_enabled: true,
      switch_visible: true,
      message: "知识库或索引尚未就绪，请先重建索引。",
      missing: ["index_schema"],
      checks: [{id: "index_schema", status: "fail", message: "索引尚未建立或不可读"}],
    },
  };
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: broken}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /role="alert"/);
    assert.match(html, /Agent 本次会回退旧知识库/);
    assert.match(html, /索引尚未建立或不可读/);
  } finally {
    restore();
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
