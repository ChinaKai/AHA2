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
    actionError: "", progress: null,
    conversationPlan: null, conversationResult: null, conversationBusy: "",
    conversationActionError: "", conversationProgress: null,
    offlineCompaction: null, offlineCompactionLoading: false, offlineCompactionError: "", openDialog: "",
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

function offlineInfo(overrides = {}) {
  return {
    data_dir: "/srv/aha2/data", database_path: "/srv/aha2/data/aha2.db",
    backup_dir: "/srv/aha2/data/backups", command: 'aha2 db compact --data-dir "/srv/aha2/data"',
    file_bytes: 5_160_000_000, wal_bytes: 4_000_000, shm_bytes: 32_768,
    required_bytes: 11_352_064_000, disk_free_bytes: 80_000_000_000,
    database_exists: true,
    ...overrides,
  };
}

function offlineResult(overrides = {}) {
  return {
    status: "ok", data_dir: "/srv/aha2/data", database_path: "/srv/aha2/data/aha2.db",
    backup_path: "/srv/aha2/data/backups/offline-compact-20261116-120000", backup_kept: true, keep_backup: true,
    before_bytes: 5_160_000_000, wal_before_bytes: 4_000_000, shm_before_bytes: 32_768,
    after_bytes: 4_000_000_000, wal_after_bytes: 0, shm_after_bytes: 32_768,
    before_total_bytes: 5_164_032_768, after_total_bytes: 4_000_032_768,
    freed_bytes: 1_164_000_000, required_bytes: 11_352_064_000, disk_free_bytes: 80_000_000_000,
    integrity_check: "ok", integrity_ok: true, row_counts_ok: true,
    started_at: "2026-11-16T11:59:00Z", finished_at: "2026-11-16T12:00:00Z", duration_ms: 60_000,
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
  assert.match(html, /aha2 db compact --data-dir/);
  assert.doesNotMatch(html, /「压缩数据库」/);
  assert.doesNotMatch(html, /原地 VACUUM/);
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
  // The batch cap is internal now: no rows input and no rows validation copy.
  assert.doesNotMatch(c, /data-hardware-io-rows|单次最多删除行数|data\.db\.maxRows/);
  assert.match(button(c, "data-hardware-io-purge"), /disabled/);
  const daysRow = c.match(/<div class="data-cleanup-actions data-db-form">[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(daysRow, /id="data-hardware-io-days"/);
  assert.doesNotMatch(daysRow, /data-hardware-io-(?:save|preview|purge)/, "inputs and actions live on separate rows");
  const actionRow = c.match(/<div class="data-cleanup-actions">[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(actionRow, /data-hardware-io-save/);
  assert.match(actionRow, /data-hardware-io-preview/);
  assert.match(actionRow, /data-hardware-io-purge/);
  assert.doesNotMatch(actionRow, /<input/, "the action row has no inline input");

  const preview = panel.renderDatabasePanel(viewState({plan: hardwarePlan()}));
  assert.doesNotMatch(button(dialog(preview, "C"), "data-hardware-io-purge"), /disabled/);
  assert.equal(panel.purgeEnabled(viewState({plan: hardwarePlan()}), 30, 200000), true);
  assert.equal(panel.purgeEnabled(viewState({plan: hardwarePlan()}), 31, 200000), false);
  assert.equal(panel.purgeEnabled(viewState({plan: hardwarePlan({run_rows: 0, skip_reason: "nothing_eligible"})}), 30, 200000), false);
});

test("open category dialog body renders purge progress, results and errors in place", () => {
  i18n.applyLocale("zh-CN");
  const category = stats().categories.find(item => item.id === "C");
  const progress = {
    kind: "hardware_io", running: true, phase: "deleting", planned_rows: 200000,
    deleted_rows: 12345, batches: 2, started_at: "2026-11-16T00:00:00Z",
    updated_at: "2026-11-16T00:00:02Z", duration_ms: 2500,
  };
  const body = panel.categoryDialogBody(viewState({busy: "purge", plan: hardwarePlan(), progress}), category);
  assert.match(body, /data-db-progress/);
  assert.match(body, /正在删除旧数据…/);
  assert.match(body, /已删除 12,345 行，共 2 批，已用时 2 秒/);
  assert.match(body, /本次计划删除 200,000 行/);
  assert.match(body, /正在清理…/, "the purge button reports the busy phase instead of staying generic");
  assert.doesNotMatch(body, /data-db-result/, "no result before the run finishes");

  const result = {
    plan: hardwarePlan(), backup_path: "/data/backups/hardware-io-1/aha2.db",
    deleted_rows: 200000, batches: 20, batch_size: 10000, partial: false, duration_ms: 1200,
  };
  const done = panel.categoryDialogBody(viewState({result}), category);
  assert.match(done, /data-db-result/);
  assert.match(done, /已删除 200,000 行，共 20 批/);
  assert.match(done, /hardware-io-1/);
  assert.doesNotMatch(done, /data-db-progress/, "progress is replaced by the result");

  const failed = panel.categoryDialogBody(viewState({actionError: "backup failed", result: {...result, partial: true}}), category);
  assert.match(failed, /data-hardware-io-action-error/);
  assert.match(failed, /backup failed/);
  assert.match(failed, /只完成了一部分/);

  const bCategory = stats().categories.find(item => item.id === "B");
  const bBody = panel.categoryDialogBody(viewState({
    conversationBusy: "purge",
    conversationProgress: {...progress, kind: "conversation", deleted_rows: 400, batches: 1},
  }), bCategory);
  assert.match(bBody, /已删除 400 行，共 1 批/);
  assert.match(bBody, /正在清理…/);
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
  assert.equal(panel.retentionFormError("30"), "");
  assert.match(panel.retentionFormError("0"), /保留天数/);
  assert.match(panel.retentionFormError("1.5"), /保留天数/);
  assert.match(panel.retentionFormError(""), /保留天数/);
  assert.equal(panel.conversationFormError("30", true, false), "");
  assert.equal(panel.conversationFormError("30", false, true), "");
  assert.match(panel.conversationFormError("30", false, false), /tool 事件或 agent update/);
  assert.match(panel.conversationFormError("0", true, false), /保留天数/);
});

test("offline compaction dialog shows sizes, steps and a copyable command", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState({offlineCompaction: offlineInfo()}));
  const compact = html.match(/<dialog[^>]*data-offline-compact-dialog[\s\S]*?<\/dialog>/)[0];
  assert.match(compact, /数据库文件/);
  assert.match(compact, /WAL/);
  assert.match(compact, /预计所需空间/);
  assert.match(compact, /磁盘可用/);
  assert.match(compact, /\/srv\/aha2\/data/);
  assert.match(compact, /操作步骤/);
  assert.match(compact, /停止 AHA2 服务/);
  assert.match(compact, /data-offline-command[^>]*>aha2 db compact --data-dir/);
  assert.match(compact, /data-offline-copy/);
  assert.match(compact, /还没有离线压缩记录/);
  // The page is a guide only: there is no button that could start a VACUUM.
  assert.doesNotMatch(compact, /data-offline-run/);
  assert.doesNotMatch(compact, /确认压缩/);
});

test("offline compaction dialog renders the latest result and keeps failure details", () => {
  i18n.applyLocale("zh-CN");
  const done = panel.renderDatabasePanel(viewState({
    offlineCompaction: offlineInfo({latest_result: offlineResult()}),
  }));
  assert.match(done, /结果：成功/);
  assert.match(done, /4\.8 GB → 3\.7 GB，释放 1\.1 GB/);
  assert.match(done, /完整性：ok/);
  assert.match(done, /关键表行数校验通过/);
  assert.match(done, /备份：\/srv\/aha2\/data\/backups\/offline-compact-20261116-120000/);

  const failed = panel.renderDatabasePanel(viewState({
    offlineCompaction: offlineInfo({latest_result: offlineResult({
      status: "failed", reason: "integrity_failed", integrity_check: "corrupted", integrity_ok: false,
      row_counts_ok: false, error: "integrity_check returned \"corrupted\"",
    })}),
  }));
  assert.match(failed, /结果：失败（数据库和备份保留）/);
  assert.match(failed, /完整性校验失败/);
  assert.match(failed, /说明：integrity_check returned/);
  assert.match(failed, /备份：/);
});

test("offline compaction dialog has explicit loading and error states without the stats card", () => {
  i18n.applyLocale("zh-CN");
  const unavailable = panel.renderDatabasePanel(viewState({database: null, error: "database_unavailable", loading: false}));
  assert.match(unavailable, /data-offline-compact-dialog/, "the guide must not depend on the stats card");
  assert.match(unavailable, /正在读取离线压缩信息/);

  const failed = panel.renderDatabasePanel(viewState({
    database: null, error: "database_unavailable", offlineCompactionError: "database is locked",
  }));
  const compact = failed.match(/<dialog[^>]*data-offline-compact-dialog[\s\S]*?<\/dialog>/)[0];
  assert.match(compact, /data-offline-compaction-error/);
  assert.match(compact, /读取离线压缩信息失败：database is locked/);
  assert.match(compact, /data-offline-compaction-refresh[^>]*>重试<\/button>/);
});

test("B dialog keeps each checkbox attached to its label row", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState());
  const b = dialog(html, "B");
  assert.match(b, /<div class="data-cleanup-note data-db-checks">/);
  assert.match(b, /<label><input id="data-conversation-tool" type="checkbox"[^>]*> 清理 tool 事件<\/label>/);
  assert.match(b, /<label><input id="data-conversation-update" type="checkbox"[^>]*> 同时清理 agent update<\/label>/);
});

test("database panel strings are translated in English", () => {
  i18n.applyLocale("en-US");
  try {
    const html = panel.renderDatabasePanel(viewState({
      plan: hardwarePlan(),
      conversationPlan: conversationPlan(),
      offlineCompaction: offlineInfo({latest_result: offlineResult({status: "refused", reason: "service_running", backup_path: undefined})}),
    }));
    assert.match(html, /Database details/);
    assert.match(html, /C\. Hardware I\/O/);
    assert.match(html, /A\. Tasks and execution/);
    assert.match(html, /Preview cleanup/);
    assert.match(html, /Conversation event cleanup/);
    assert.match(html, /Clean up/);
    assert.match(html, /Offline database compaction/);
    assert.match(html, /the AHA2 service is still running/);
    assert.match(html, /AHA2 never runs VACUUM while it is serving/);
    assert.match(html, /does not necessarily shrink/);
    assert.match(html, /aha2 db compact --data-dir/);
    assert.doesNotMatch(html, /brief write freeze|Compact database to rebuild/);
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

test("data page wires backup preview, database cleanup and the offline guide", async () => {
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
  // The inventory entry opens a read-only guided dialog. There must be no
  // client path that posts a compaction request.
  assert.match(main, /category\.id === "database"[\s\S]{0,200}data-db-offline-compact/);
  assert.match(main, /api\.offlineCompactionInfo\(\)/);
  assert.match(main, /const offlineCompactionInfoRequest = createLatestRequest\(/);
  assert.match(main, /offlineCompactionInfoRequest\.invalidate\(\)/);
  assert.match(main, /data-offline-copy/);
  assert.doesNotMatch(main, /compactDatabase|databaseCompactionPlan/);
  assert.doesNotMatch(api, /database\/compaction/);
  assert.match(api, /\/api\/v1\/system\/data\/database\/offline-compaction/);
  assert.match(api, /\/api\/v1\/system\/data\/backups/);
  assert.match(api, /\/api\/v1\/system\/data\/conversation\/preview/);
  assert.match(api, /\/api\/v1\/system\/data\/conversation\/purge/);
  assert.match(build, /"data_retention"/);
  // app.js and the panel must load the same versioned i18n.js.
  const appI18n = app.match(/from "(\.\/i18n\.js[^"]*)"/)?.[1];
  assert.equal(i18nSpecifier, appI18n);
  assert.match(app, /"\.\/data_retention\.js\?v=/);
});
