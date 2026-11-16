import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const modulesDir = resolve(import.meta.dirname, "..", "build");
const moduleSource = await readFile(resolve(modulesDir, "data_retention.js"), "utf8");
// Load i18n through the exact URL the panel imports so both share one locale.
const i18nSpecifier = moduleSource.match(/from "(\.\/i18n\.js[^"]*)"/)?.[1];
const i18n = await import(new URL(i18nSpecifier, pathToFileURL(resolve(modulesDir, "data_retention.js"))).href);
const panel = await import(pathToFileURL(resolve(modulesDir, "data_retention.js")).href);

function stats(overrides = {}) {
  return {
    file_bytes: 5_160_000_000, wal_bytes: 4_000_000, shm_bytes: 32_768,
    page_size: 4096, page_count: 1_260_000, freelist_count: 100,
    used_bytes: 5_159_000_000, free_bytes: 409_600,
    table_count: 4, cached: false,
    categories: [
      {id: "A", key: "tasks", safety: "keep", rows: 159, rows_exact: true, estimated_bytes: 1000, tables: [
        {name: "tasks", rows: 159, rows_exact: true, sample_rows: 159, avg_row_bytes: 6, estimated_bytes: 1000, deletable: false},
      ]},
      {id: "B", key: "conversation", safety: "preview_purge", cleanup: "conversation", rows: 400, rows_exact: true, estimated_bytes: 8000, tables: [
        {name: "conversation_items", rows: 400, rows_exact: true, sample_rows: 400, avg_row_bytes: 20, estimated_bytes: 8000, deletable: true},
      ]},
      {id: "C", key: "hardware", safety: "preview_purge", cleanup: "hardware_io", rows: 12_380_996, rows_exact: false, estimated_bytes: 2_228_579_280, tables: [
        {name: "hardware_io", rows: 12_380_996, rows_exact: false, sample_rows: 500, avg_row_bytes: 180, estimated_bytes: 2_228_579_280, deletable: true},
      ]},
      {id: "D", key: "sync", safety: "retire", rows: 12, rows_exact: true, estimated_bytes: 1080, tables: [
        {name: "sync_applied", rows: 12, rows_exact: true, sample_rows: 12, avg_row_bytes: 90, estimated_bytes: 1080, deletable: false},
      ]},
      {id: "J", key: "attachments", safety: "read_only", rows: 0, rows_exact: true, estimated_bytes: 0, tables: []},
      {id: "other", key: "other", safety: "bogus", rows: 1, rows_exact: true, estimated_bytes: 0, tables: [
        {name: "zz_extra", rows: 1, rows_exact: true, sample_rows: 0, avg_row_bytes: 0, estimated_bytes: 0, deletable: false},
      ]},
    ],
    hardware_io: {min_sequence: 1, max_sequence: 12_380_996, estimated_rows: 12_380_996, oldest_at: "2026-09-05T00:00:00Z", newest_at: "2026-11-06T00:00:00Z"},
    retention: {retention_days: 30, max_delete_rows: 200000},
    conversation_retention: {retention_days: 30, purge_tool: true, purge_update: false},
    collected_at: "2026-11-07T00:00:00Z", duration_ms: 3,
    ...overrides,
  };
}

function viewState(overrides = {}) {
  return {
    database: stats(), loading: false, error: "", plan: null, result: null, busy: "",
    conversationPlan: null, conversationResult: null, conversationBusy: "",
    compactionPlan: null, compactionResult: null, compactionBusy: false, openDialog: "",
    ...overrides,
  };
}

function hardwarePlan(overrides = {}) {
  return {
    retention_days: 30, max_delete_rows: 200000, cutoff: "2026-10-08T00:00:00Z",
    min_sequence: 1, max_sequence: 12_380_996, cutoff_sequence: 6_000_001, eligible_rows_estimate: 6_000_000,
    run_start_sequence: 1, run_end_sequence: 200_001, run_rows: 200000, avg_row_bytes: 180,
    run_bytes_estimate: 36_000_000, eligible_bytes_estimate: 1_080_000_000, remaining_eligible_estimate: 5_800_000,
    ...overrides,
  };
}

function conversationPlan(overrides = {}) {
  return {
    retention_days: 30, purge_tool: true, purge_update: false, categories: ["tool"],
    cutoff: "2026-10-08T00:00:00Z", eligible_rows: 1200, run_rows: 1200, tasks_affected: 7,
    avg_row_bytes: 180, run_bytes_estimate: 216_000,
    ...overrides,
  };
}

function compactionPlan(overrides = {}) {
  return {
    mode: "vacuum_in_place", database_path: "/data/aha2.db",
    file_bytes: 5_160_000_000, wal_bytes: 4_000_000, shm_bytes: 32_768,
    page_size: 4096, page_count: 1_260_000, freelist_count: 1000,
    free_bytes: 4_096_000, estimated_reclaimed_bytes: 4_096_000,
    required_temp_bytes: 11_352_064_000, disk_free_bytes: 80_000_000_000,
    active_tasks: 0, active_turns: 0, pending_approvals: 0, pet_approvals: 0,
    can_compact: true, blocking_reasons: [], checked_at: "2026-11-16T00:00:00Z",
    ...overrides,
  };
}

function button(html, attribute) {
  return html.match(new RegExp(`<button[^>]*${attribute}[^>]*>`))?.[0] ?? "";
}

function dialog(html, id) {
  return html.match(new RegExp(`<dialog[^>]*data-db-dialog="${id}"[\\s\\S]*?</dialog>`))?.[0] ?? "";
}

test("database panel renders file stats and the VACUUM note without a second compaction entry", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState());
  assert.match(html, /data-db-table="hardware_io"/);
  assert.match(html, /≈ 12,380,996/, "hardware_io rows are labelled as an estimate");
  assert.match(html, /<td class="data-size">12<\/td>/, "exact small-table counts have no ≈");
  assert.match(html, /data-db-range|C\. 硬件 I\/O/);
  assert.match(html, /文件不一定变小/);
  assert.match(html, /VACUUM/);
  // The single compaction entry lives on the inventory database row, next to
  // the other cleanup actions, not on this details card.
  assert.doesNotMatch(html, /data-db-compact/);
});

test("only categories with an executable cleanup show a cleanup button, but every category keeps its dialog", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState());
  assert.match(html, /data-db-cleanup="B"/);
  assert.match(html, /data-db-cleanup="C"/);
  for (const id of ["A", "D", "J", "other"]) {
    assert.doesNotMatch(html, new RegExp(`data-db-cleanup="${id}"`), `${id} is read-only and must not offer cleanup`);
    assert.ok(dialog(html, id), `${id} dialog`);
  }
  assert.equal((html.match(/data-db-cleanup="/g) || []).length, 2);

  const readOnlyA = dialog(html, "A");
  assert.match(readOnlyA, /A\. 任务与执行/);
  assert.match(readOnlyA, /只读/);
  assert.match(readOnlyA, /不提供批量删除/);
  assert.doesNotMatch(readOnlyA, /data-hardware-io-purge|data-conversation-purge/);

  const readOnlyD = dialog(html, "D");
  assert.match(readOnlyD, /D\. 同步与远端镜像/);
  assert.match(readOnlyD, /不提供批量删除/);
  assert.match(html, /data-db-badge-retire">可 retire/);
});

test("B dialog defaults to tool events, makes update optional and guards the purge", () => {
  i18n.applyLocale("zh-CN");
  const closed = panel.renderDatabasePanel(viewState());
  const b = dialog(closed, "B");
  assert.match(b, /id="data-conversation-tool"[^>]*checked/);
  assert.doesNotMatch(b, /id="data-conversation-update"[^>]*checked/);
  assert.match(b, /id="data-conversation-days"[^>]*min="1"[^>]*max="3650"[^>]*value="30"/);
  assert.match(button(b, "data-conversation-purge"), /disabled/);
  assert.match(b, /最终答复/);
  assert.match(b, /待审批/);

  const withPlan = panel.renderDatabasePanel(viewState({conversationPlan: conversationPlan()}));
  assert.doesNotMatch(button(dialog(withPlan, "B"), "data-conversation-purge"), /disabled/);
  assert.match(dialog(withPlan, "B"), /本次将删除 1,200 行/);
  assert.match(dialog(withPlan, "B"), /7 个终态任务/);

  const updatePlan = panel.renderDatabasePanel(viewState({conversationPlan: conversationPlan({purge_update: true, categories: ["tool", "update"]})}));
  assert.match(dialog(updatePlan, "B"), /tool 事件 \+ agent update/);
});

test("C dialog keeps the hardware_io preview/confirm flow", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState());
  const c = dialog(html, "C");
  assert.match(c, /id="data-hardware-io-days"[^>]*value="30"/);
  assert.match(c, /id="data-hardware-io-rows"[^>]*value="200000"/);
  assert.match(button(c, "data-hardware-io-purge"), /disabled/);

  const preview = panel.renderDatabasePanel(viewState({plan: hardwarePlan()}));
  assert.doesNotMatch(button(dialog(preview, "C"), "data-hardware-io-purge"), /disabled/);
  assert.equal(panel.purgeEnabled(viewState({plan: hardwarePlan()}), 30, 200000), true);
  assert.equal(panel.purgeEnabled(viewState({plan: hardwarePlan()}), 31, 200000), false);
  assert.equal(panel.purgeEnabled(viewState({plan: hardwarePlan({run_rows: 0, skip_reason: "nothing_eligible"})}), 30, 200000), false);
});

test("conversation cleanup is enabled only for a matching, non-empty preview", () => {
  i18n.applyLocale("zh-CN");
  assert.equal(panel.conversationPurgeEnabled(viewState({conversationPlan: conversationPlan()}), 30, true, false), true);
  assert.equal(panel.conversationPurgeEnabled(viewState({conversationPlan: conversationPlan()}), 31, true, false), false, "edited days need a new preview");
  assert.equal(panel.conversationPurgeEnabled(viewState({conversationPlan: conversationPlan()}), 30, true, true), false, "edited checkboxes need a new preview");
  assert.equal(panel.conversationPurgeEnabled(viewState({conversationPlan: conversationPlan()}), 30, false, false), false);
  assert.equal(panel.conversationPurgeEnabled(viewState({conversationPlan: conversationPlan({run_rows: 0, skip_reason: "nothing_eligible"})}), 30, true, false), false);
  const skipped = panel.renderDatabasePanel(viewState({conversationPlan: conversationPlan({run_rows: 0, skip_reason: "nothing_eligible"})}));
  assert.match(dialog(skipped, "B"), /没有早于保留天数的数据/);
});

test("form validation rejects out-of-range values and an empty selection", () => {
  i18n.applyLocale("zh-CN");
  assert.equal(panel.retentionFormError("30", "200000"), "");
  assert.match(panel.retentionFormError("0", "200000"), /保留天数/);
  assert.match(panel.retentionFormError("1.5", "200000"), /保留天数/);
  assert.match(panel.retentionFormError("30", "999"), /单次最多删除行数/);
  assert.equal(panel.conversationFormError("30", true, false), "");
  assert.equal(panel.conversationFormError("30", false, true), "");
  assert.match(panel.conversationFormError("30", false, false), /tool 事件或 agent update/);
  assert.match(panel.conversationFormError("0", true, false), /保留天数/);
});

test("compaction dialog shows preflight numbers and blocks when preflight fails", () => {
  i18n.applyLocale("zh-CN");
  const ready = panel.renderDatabasePanel(viewState({compactionPlan: compactionPlan()}));
  const readyDialog = ready.match(/<dialog[^>]*data-compact-dialog[\s\S]*?<\/dialog>/)[0];
  assert.match(readyDialog, /当前文件/);
  assert.match(readyDialog, /空闲页/);
  assert.match(readyDialog, /所需临时空间/);
  assert.match(readyDialog, /磁盘可用/);
  assert.doesNotMatch(button(readyDialog, "data-compact-run"), /disabled/, "ready plan enables compaction");

  const blockedPlan = compactionPlan({can_compact: false, blocking_reasons: ["active_tasks", "disk_space"]});
  const blocked = panel.renderDatabasePanel(viewState({compactionPlan: blockedPlan}));
  const blockedDialog = blocked.match(/<dialog[^>]*data-compact-dialog[\s\S]*?<\/dialog>/)[0];
  assert.match(blockedDialog, /当前不能压缩：有活动、等待或阻塞任务或 Turn；磁盘可用空间不足/);
  assert.match(button(blockedDialog, "data-compact-run"), /disabled/);

  const done = panel.renderDatabasePanel(viewState({
    compactionPlan: compactionPlan(),
    compactionResult: {
      plan: compactionPlan(), backup_path: "/data/backups/database-compact-1/aha2.db",
      before_bytes: 5_160_000_000, after_bytes: 4_000_000_000, freed_bytes: 1_160_000_000,
      wal_before_bytes: 4_000_000, wal_after_bytes: 0, integrity_check: "ok",
      restored: false, restart_required: false, duration_ms: 1200,
    },
  }));
  assert.match(done, /压缩完成：4.8 GB → 3.7 GB，释放 1.1 GB；完整性 ok/);
});

test("database panel strings are translated in English", () => {
  i18n.applyLocale("en-US");
  try {
    const html = panel.renderDatabasePanel(viewState({
      plan: hardwarePlan(),
      conversationPlan: conversationPlan(),
      compactionPlan: compactionPlan({can_compact: false, blocking_reasons: ["active_tasks"]}),
    }));
    assert.match(html, /Database details/);
    assert.match(html, /C\. Hardware I\/O/);
    assert.match(html, /A\. Tasks and execution/);
    assert.match(html, /Preview cleanup/);
    assert.match(html, /Conversation event cleanup/);
    assert.match(html, /Clean up/);
    assert.match(html, /Compaction is blocked: active, waiting, or blocked tasks or Turns/);
    assert.match(html, /does not necessarily shrink/);
    assert.doesNotMatch(html, /[一-鿿]/, "no Chinese text leaks into the English panel");
    assert.match(panel.conversationFormError("30", false, false), /Select at least/);
    assert.equal(panel.skipReasonText("nothing_eligible"), "No rows are older than the retention period.");
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("every database panel key exists in both catalogs", async () => {
  const source = await readFile(resolve(import.meta.dirname, "..", "src", "i18n.ts"), "utf8");
  const zh = source.slice(source.indexOf("const zhCN"), source.indexOf("const enUS"));
  const en = source.slice(source.indexOf("const enUS"), source.indexOf("const legacyEnglish"));
  const keys = [...new Set([...zh.matchAll(/"(data\.db\.[\w.]+)":/g)].map(match => match[1]))];
  assert.ok(keys.length > 60);
  for (const key of keys) assert.ok(en.includes(`"${key}":`), `missing English ${key}`);
  const enKeys = [...en.matchAll(/"(data\.db\.[\w.]+)":/g)].map(match => match[1]);
  assert.equal(enKeys.length, keys.length);
  // The backup preview keys are also translated.
  for (const key of ["data.backupsReading", "data.backupsPreviewIntro", "data.backupsConfirm"]) {
    assert.ok(zh.includes(`"${key}":`), `missing Chinese ${key}`);
    assert.ok(en.includes(`"${key}":`), `missing English ${key}`);
  }
});

test("data page wires backup preview, database cleanup and compaction without a keep input", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const build = await readFile(resolve(root, "..", "scripts", "build-web.mjs"), "utf8");
  const app = await readFile(resolve(modulesDir, "app.js"), "utf8");
  assert.match(main, /renderDatabasePanel\(state\.dataRetention\)/);
  assert.match(main, /api\.previewBackupCleanup\(\)/);
  assert.doesNotMatch(main, /data-backup-keep/);
  assert.doesNotMatch(main, /keepInput/);
  assert.match(main, /backup_count/, "cleanup is disabled when only one backup exists");
  assert.match(main, /api\.cleanupData\(\{category: "backups", confirm: true\}\)/);
  assert.match(main, /api\.previewConversationPurge/);
  assert.match(main, /api\.purgeConversation\(\{\.\.\.confirmed, confirm: true\}\)/);
  assert.match(main, /api\.databaseCompactionPlan\(\)/);
  assert.match(main, /api\.compactDatabase\(\{confirm: true\}\)/);
  assert.match(api, /\/api\/v1\/system\/data\/backups/);
  assert.match(api, /\/api\/v1\/system\/data\/conversation\/preview/);
  assert.match(api, /\/api\/v1\/system\/data\/conversation\/purge/);
  assert.match(api, /\/api\/v1\/system\/data\/database\/compaction/);
  assert.match(build, /"data_retention"/);
  // app.js and the panel must load the same versioned i18n.js.
  const appI18n = app.match(/from "(\.\/i18n\.js[^"]*)"/)?.[1];
  assert.equal(i18nSpecifier, appI18n);
  assert.match(app, /"\.\/data_retention\.js\?v=/);
});
