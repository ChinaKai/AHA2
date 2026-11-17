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
// single API and i18n singleton. Import the exact same URL or the stubs below
// would land on a second instance that the panel never calls.
const panelSource = await readFile(panelPath, "utf8");
const apiVersion = panelSource.match(/from "\.\/api\.js(\?v=[^"]+)?"/)?.[1] ?? "";
const {api} = await import(`${pathToFileURL(resolve(root, "build", "api.js")).href}${apiVersion}`);
const i18nVersion = panelSource.match(/from "\.\/i18n\.js(\?v=[^"]+)?"/)?.[1] ?? "";
// A bare import would silently create a second i18n instance; the panel would
// stay zh-CN after the shell applied en-US, and this file's locale tests would
// import the same bare URL and pass for the wrong reason.
assert.match(i18nVersion, /^\?v=[a-f0-9]{12}$/, "knowledge_vault_settings.js must import the versioned i18n singleton");
const i18n = await import(`${pathToFileURL(resolve(root, "build", "i18n.js")).href}${i18nVersion}`);

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
    rollback_allowed: false,
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
      rollback_allowed: false,
      message: "知识库或索引尚未就绪，索引会自动修复。",
      missing: ["vault_initialized", "index_schema"],
      checks: [
        {id: "vault_initialized", status: "fail", message: "知识库尚未初始化（缺少 .aha-vault.json）"},
        {id: "vault_writable", status: "ok", message: "知识库目录可写"},
        {id: "index_schema", status: "fail", message: "索引尚未建立或不可读"},
        {id: "index_writable", status: "ok", message: "索引目录可写"},
        {id: "index_fresh", status: "fail", message: "索引尚未建立"},
        {id: "manifest_binding", status: "ok", message: "所有项目都已绑定到知识库"},
        {id: "migration", status: "ok", message: "没有检测到旧知识库"},
      ],
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
      rollback_allowed: true,
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
      rollback_allowed: false,
      message: "文件知识库已就绪并在使用；旧知识库仅作只读备份保留，不会被迁移覆盖，也不能回退。",
      checks: [{
        id: "migration",
        status: "ok",
        message: "文件知识库已启用并在使用；旧知识库仅作只读备份保留，不会被迁移覆盖",
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
    assert.match(html, /class="knowledge-vault-state is-on">已启用</);
    // The blocked-page CTA focuses this heading, so it must be focusable
    // without entering the normal tab order.
    assert.match(html, /id="knowledge-vault-settings-title" tabindex="-1"/);
    assert.match(html, /状态来源：数据库设置/);
    assert.match(html, /Vault 路径<\/dt><dd><code>\/srv\/vault<\/code>/);
    assert.match(html, /索引路径<\/dt><dd><code>\/srv\/index\.db<\/code>/);
    assert.match(html, /索引条数<\/dt><dd>5 条/);
    assert.match(html, /class="knowledge-vault-badge is-ok">正常</);
    assert.match(html, /type="checkbox" checked/);
    // The health checks are one compact list instead of 16px block rows.
    assert.match(html, /knowledge-vault-checks-passed/);
    assert.doesNotMatch(html, /class="knowledge-vault-status"/);
    // No legacy data means no legacy section and no legacy counts.
    assert.doesNotMatch(html, /knowledge-vault-legacy/);
    assert.doesNotMatch(html, /旧知识<\/dt>/);
    // An initialized vault must not offer the destructive-ish init action.
    assert.doesNotMatch(html, /knowledge-vault-init/);
    // Rebuilding moved to the knowledge page's gear panel; the advanced panel
    // keeps only the switch, sample init, status and path.
    assert.doesNotMatch(html, /knowledge-vault-rebuild/);
    assert.match(html, /索引会随写入、文件变化和启动对账自动维护/);
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
    assert.match(html, /class="knowledge-vault-state">已关闭</);
    assert.match(html, /状态来源：启动默认/);
    assert.match(html, /class="knowledge-vault-badge is-warn">待初始化</);
    assert.match(html, /目录未初始化/);
    assert.match(html, /知识库或索引尚未就绪，请先重建索引|知识库或索引尚未就绪，索引会自动修复/);
    assert.match(html, /未满足：/);
    assert.match(html, /id="knowledge-vault-init"/);
    assert.doesNotMatch(html, /knowledge-vault-rebuild/);
    assert.doesNotMatch(html, /knowledge-vault-legacy/);
  } finally {
    restore();
  }
});

test("a committed vault locks the switch on and never closes through the client action", async () => {
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
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    // The box is checked and disabled: a read-only status, not a control.
    assert.match(html, /type="checkbox" checked[^>]*disabled/);
    assert.match(html, /class="knowledge-vault-badge">已锁定</);
    assert.match(html, /文件知识库已启用并锁定/);
    assert.match(html, /class="knowledge-vault-state is-on">已启用</);
    // The change action is the single entry point; call it directly to prove a
    // programmatic click cannot reach the API either.
    await panel.__knowledgeVaultSettingsActionsForTest.setEnabled(false);
    assert.deepEqual(calls, [], "setEnabled(false) must not call the API on a locked vault");
    assert.ok(ctx.messages.some(message => message.kind === "error" && message.message.includes("不能关闭回退旧库")));
    assert.match(panel.renderKnowledgeVaultSettingsPanel(ctx), /type="checkbox" checked[^>]*disabled/);
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
    assert.match(html, /class="knowledge-vault-badge is-warn">待初始化</);
    assert.match(html, /id="knowledge-vault-init"/);
    // A fresh install must not show the legacy section or the migration action.
    assert.doesNotMatch(html, /knowledge-vault-legacy/);
    assert.doesNotMatch(html, /id="knowledge-vault-migrate"/);
    // An enabled-but-uninitialized vault is an expected fresh state, not the
    // broken-index alert.
    assert.doesNotMatch(html, /role="alert"/);
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
    assert.match(html, /升级后需要先迁移旧知识库，或导入已有知识库/);
    assert.match(html, /type="checkbox"[^>]*disabled/);
    assert.match(html, /id="knowledge-vault-migrate"/);
    assert.doesNotMatch(html, /id="knowledge-vault-init"/);
    // The missing list stays out of the migration state: the message already
    // says what is missing, and the raw id `migration` must not leak.
    assert.doesNotMatch(html, /未满足：migration/);
    assert.match(html, /knowledge-vault-legacy/);
    assert.match(html, /旧知识<\/dt><dd>3 条/);
    assert.match(html, /旧技能<\/dt><dd>1 个/);
  } finally {
    restore();
  }
});

test("an enabled in-use upgrade locks the switch on and keeps legacy as a read-only backup", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: inUseUpgradeVault()}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /type="checkbox" checked[^>]*disabled/);
    assert.match(html, /已迁移到文件知识库，不能回退旧库/);
    // The migration button would export the legacy database over the live
    // vault, so a running upgrade must never render it.
    assert.doesNotMatch(html, /id="knowledge-vault-migrate"/);
    assert.doesNotMatch(html, /id="knowledge-vault-init"/);
    assert.match(html, /旧知识库仅作只读备份保留，不会被迁移覆盖，也不能通过开关回退/);
    assert.match(html, /class="knowledge-vault-badge">只读保留</);
    assert.match(html, /索引条数<\/dt><dd>992 条/);
    assert.match(html, /class="knowledge-vault-badge is-ok">正常</);
    // The backup sentence appears exactly once, in the legacy section; the
    // switch hint only states the no-rollback rule.
    assert.equal((html.match(/旧知识库仅作只读备份保留/g) ?? []).length, 1);
    assert.equal((html.match(/不能回退旧库/g) ?? []).length, 1);
    // Migration is represented by the legacy facts row, not a duplicate check.
    assert.doesNotMatch(html, /文件知识库已启用并在使用；旧知识库/);
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
      rollback_allowed: false,
      message: "文件知识库已启用，但部分项目仍回退旧知识库或索引需要关注；旧知识库仅作只读备份保留，不会被迁移覆盖。",
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
    assert.match(html, /class="knowledge-vault-badge is-warn">需关注</);
    assert.match(html, /未绑定项目<\/dt><dd>1 个，继续使用旧库/);
    // The degraded explanation is now carried by the badge, the unbound row and
    // the legacy note; it must not also render as a second amber paragraph.
    assert.doesNotMatch(html, /文件知识库已启用，但部分项目仍回退旧知识库/);
    assert.doesNotMatch(html, /knowledge-vault-attention/);
    assert.equal((html.match(/1 个，继续使用旧库/g) ?? []).length, 1);
    assert.equal((html.match(/旧知识库仅作只读备份保留/g) ?? []).length, 1);
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
    assert.match(html, /class="knowledge-vault-badge is-warn">需关注</);
    assert.match(html, /1 个项目未绑定，继续使用旧库/);
    assert.doesNotMatch(html, /可以启用，但/);
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
      message: "知识库或索引尚未就绪，索引会自动修复。",
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
    assert.match(html, /文件知识库已启用，但索引尚未就绪/);
    assert.match(html, /Agent 本次不会回退旧知识库/);
    assert.match(html, /索引未建立或不可读/);
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
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /class="knowledge-vault-state is-on">已启用</);
    assert.match(html, /状态来源：数据库设置/);
  } finally {
    restore();
  }
});

test("the import form labels its fields and only shows the selected source mode", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: emptyVault()}));
  const injected = [];
  const listeners = {};
  const element = (extra = {}) => ({
    ...extra,
    addEventListener(type, handler) { (this.listeners ??= {})[type] = handler; },
  });
  const pathField = element({dataset: {importKind: "local"}, hidden: false});
  const remoteField = element({dataset: {importKind: "git"}, hidden: true});
  const branchField = element({dataset: {importKind: "git"}, hidden: true});
  const localRadio = element({value: "local", checked: true});
  const gitRadio = element({value: "git", checked: false});
  const pathInput = element({value: "", listeners: {}});
  const remoteInput = element({value: "", listeners: {}});
  const branchInput = element({value: "", listeners: {}});
  const details = element({open: false});
  const fakeDocument = {
    getElementById: () => null,
    createElement: () => ({}),
    head: {append: node => injected.push(node)},
    querySelector(selector) {
      if (selector === 'input[name="knowledge-vault-import-kind"]:checked') return localRadio.checked ? localRadio : gitRadio;
      if (selector === "#knowledge-vault-import-path") return pathInput;
      if (selector === "#knowledge-vault-import-remote") return remoteInput;
      if (selector === "#knowledge-vault-import-branch") return branchInput;
      if (selector === ".knowledge-vault-import") return details;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === 'input[name="knowledge-vault-import-kind"]') return [localRadio, gitRadio];
      if (selector === ".knowledge-vault-import-field") return [pathField, remoteField, branchField];
      return [];
    },
  };
  const originalDocument = globalThis.document;
  globalThis.document = fakeDocument;
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const initialHTML = panel.renderKnowledgeVaultSettingsPanel(ctx);
    panel.bindKnowledgeVaultSettingsPanel(ctx);

    // Both modes have an explicit text label, and Git-only rows start hidden.
    assert.match(initialHTML, /class="knowledge-vault-import-modes"/);
    assert.match(initialHTML, /data-import-kind="local">/);
    assert.match(initialHTML, /data-import-kind="git" hidden>/);
    assert.equal(pathField.hidden, false);
    assert.equal(remoteField.hidden, true);
    assert.equal(branchField.hidden, true);
    assert.equal(injected.length, 1);
    assert.equal(injected[0].id, "knowledge-vault-import-styles");
    assert.match(injected[0].textContent, /\.knowledge-vault-import input\[type="radio"\][^{]*\{ width: auto; height: auto; min-height: 0; padding: 0; margin: 0; \}/);
    assert.match(injected[0].textContent, /\.knowledge-vault-import-actions button \{ width: 100%; min-height: 40px; \}/);
    assert.match(injected[0].textContent, /\.knowledge-vault-import-field\[hidden\] \{ display: none; \}/);
    assert.match(injected[0].textContent, /@media \(max-width: 760px\)/);

    // Switching modes only toggles rows; the typed values stay in the inputs.
    pathInput.value = "/srv/existing";
    pathInput.listeners.input();
    localRadio.checked = false;
    gitRadio.checked = true;
    gitRadio.listeners.change();
    assert.equal(pathField.hidden, true);
    assert.equal(remoteField.hidden, false);
    assert.equal(branchField.hidden, false);
    assert.equal(pathInput.value, "/srv/existing");

    // A re-render keeps the selected mode, the draft values and the fold open.
    details.open = true;
    details.listeners.toggle();
    gitRadio.checked = false;
    localRadio.checked = true;
    localRadio.listeners.change();
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /<details class="knowledge-vault-import" open>/);
    assert.match(html, /value="local" checked/);
    assert.match(html, /value="\/srv\/existing"/);
    assert.match(html, /<span>本地目录<\/span><input id="knowledge-vault-import-path"/);
    assert.match(html, /<span>SSH Git 远端<\/span><input id="knowledge-vault-import-remote"/);
    assert.match(html, /<span>分支（可选）<\/span><input id="knowledge-vault-import-branch"/);
    assert.match(html, /class="knowledge-vault-import-actions"><button type="button" id="knowledge-vault-import"/);
    assert.match(html, /class="field-help knowledge-vault-import-help"/);
    assert.doesNotMatch(html, /class="knowledge-vault-import-fields">\s*<label></);
  } finally {
    globalThis.document = originalDocument;
    restore();
  }
});

test("the panel groups source, health and legacy in order without a mixed state word", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: inUseUpgradeVault({
    readiness: {
      status: "degraded", can_enable: true, default_enabled: false, switch_visible: true,
      message: "文件知识库已启用，但部分项目仍回退旧知识库或索引需要关注；旧知识库仅作只读备份保留，不会被迁移覆盖。",
      unbound_projects: ["p1"],
      checks: [
        {id: "vault_initialized", status: "ok", message: "知识库目录已初始化"},
        {id: "index_schema", status: "ok", message: "索引 schema 可读"},
        {id: "manifest_binding", status: "warn", message: "1 个项目未绑定，这些项目继续使用旧知识库"},
        {id: "migration", status: "ok", message: "文件知识库已启用并在使用；旧知识库仅作只读备份保留，不会被迁移覆盖"},
      ],
    },
  })}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    const source = html.indexOf("knowledge-vault-source");
    const health = html.indexOf("knowledge-vault-health");
    const legacy = html.indexOf("knowledge-vault-legacy");
    assert.ok(source >= 0 && health > source && legacy > health, "source → health → legacy order");
    // The panel head only says enabled/disabled; readiness lives in the health
    // badge, so "已启用 · 可启用（部分项目回退）" can no longer appear.
    assert.match(html, /class="knowledge-vault-state is-on">已启用</);
    assert.doesNotMatch(html, /已启用 ·/);
    assert.match(html, /class="knowledge-vault-badge is-warn">需关注</);
  } finally {
    restore();
  }
});

test("long vault and index paths stay escaped inside their value cells", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const longVault = "/srv/build/users/kk/知识库/项目知识/<project>&name/一个非常长的目录名称";
  const longIndex = "/srv/build/users/kk/data/knowledge-vault/index.sqlite3";
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: {...ready, vault_path: longVault, index_path: longIndex}}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.ok(html.includes("&lt;project&gt;&amp;name"));
    assert.ok(html.includes(longIndex));
    assert.doesNotMatch(html, /<project>/);
    assert.match(html, /Vault 路径<\/dt><dd><code>/);
    assert.match(html, /索引路径<\/dt><dd><code>/);
  } finally {
    restore();
  }
});

test("the panel copy follows the active locale", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: ready}));
  const previous = i18n.currentLocale();
  try {
    i18n.applyLocale("en-US");
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /File knowledge vault/);
    assert.match(html, /Knowledge source/);
    assert.match(html, /Vault health/);
    assert.match(html, /Vault path/);
    assert.match(html, /Indexed entries/);
    assert.match(html, /State source: saved setting/);
    assert.doesNotMatch(html, /文件知识库/);
  } finally {
    i18n.applyLocale(previous);
    restore();
  }
});

test("a configured vault without legacy knowledge still locks the switch on", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  // A machine whose legacy tables were cleaned away still counts as committed
  // to the vault: configured=true must not make the switch closable.
  const configured = {
    ...ready,
    configured: true,
    readiness: {...ready.readiness, switch_visible: false, rollback_allowed: false},
    legacy: {entries: 0, skills: 0, skill_packages: 0},
  };
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: configured}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    // switchVisible = readiness.switch_visible || settings.configured: no legacy
    // and switch_visible=false must still render the switch once the owner has
    // configured the vault, but only as a locked-on status.
    assert.match(html, /id="knowledge-vault-enabled"/);
    assert.match(html, /type="checkbox" checked[^>]*disabled/);
    assert.match(html, /class="knowledge-vault-badge">已锁定</);
    assert.match(html, /文件知识库已启用并锁定/);
    assert.doesNotMatch(html, /全新安装默认使用文件知识库/);
    assert.doesNotMatch(html, /knowledge-vault-legacy/);
    assert.match(html, /class="knowledge-vault-state is-on">已启用</);
  } finally {
    restore();
  }
});

test("an unknown readiness check is escaped before it reaches the checklist", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const future = {
    ...ready,
    readiness: {
      ...ready.readiness,
      checks: [
        ...ready.readiness.checks,
        {id: "future_check", status: "warn", message: "<未来 & 检查>"},
      ],
    },
  };
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: future}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.match(html, /&lt;未来 &amp; 检查&gt;/);
    assert.doesNotMatch(html, /<未来/);
  } finally {
    restore();
  }
});

test("an unknown missing id is escaped exactly once", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const blocked = {
    ...emptyVault(),
    readiness: {
      status: "needs_rebuild", can_enable: false, default_enabled: true, switch_visible: true,
      missing: ["<odd&id>"], checks: [],
    },
  };
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings: blocked}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    assert.ok(html.includes("&lt;odd&amp;id&gt;"));
    assert.doesNotMatch(html, /&amp;lt;/);
  } finally {
    restore();
  }
});

test("the advanced settings panel has no second auto-sync entry", async () => {
  panel.__resetKnowledgeVaultSettingsForTest();
  const autoSync = {
    enabled: true, running: false, paused: true, pause_reason: "preflight_failed",
    conflict: false, pending: 1, blocked: 1,
    blocked_files: [{path: "global/general/token-note.md", reason: "secret"}],
    preflight_done: false,
  };
  const settings = {...ready, auto_sync: autoSync};
  const restore = stub("knowledgeVaultSettings", async () => ({ok: true, settings}));
  try {
    const ctx = context();
    await panel.refreshKnowledgeVaultSettingsPanel(ctx);
    const html = panel.renderKnowledgeVaultSettingsPanel(ctx);
    // The gear dialog on the knowledge page is the single auto-sync entry.
    assert.doesNotMatch(html, /knowledge-vault-auto-sync/);
    assert.doesNotMatch(html, /<h3>自动同步<\/h3>/);
  } finally {
    restore();
  }
});
