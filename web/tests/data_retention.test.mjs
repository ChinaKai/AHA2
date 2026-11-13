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
      {id: "C", key: "hardware", safety: "preview_purge", rows: 12_380_996, rows_exact: false, estimated_bytes: 2_228_579_280, tables: [
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
    collected_at: "2026-11-07T00:00:00Z", duration_ms: 3,
    ...overrides,
  };
}

function plan(overrides = {}) {
  return {
    retention_days: 30, max_delete_rows: 200000, cutoff: "2026-10-08T00:00:00Z",
    min_sequence: 1, max_sequence: 12_380_996, cutoff_sequence: 6_000_001, eligible_rows_estimate: 6_000_000,
    run_start_sequence: 1, run_end_sequence: 200_001, run_rows: 200000, avg_row_bytes: 180,
    run_bytes_estimate: 36_000_000, eligible_bytes_estimate: 1_080_000_000, remaining_eligible_estimate: 5_800_000,
    ...overrides,
  };
}

function viewState(overrides = {}) {
  return {database: stats(), loading: false, error: "", plan: null, result: null, busy: "", ...overrides};
}

function purgeButton(html) {
  return html.match(/<button[^>]*data-hardware-io-purge[^>]*>/)[0];
}

test("database panel renders table estimates, range and the VACUUM note", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState());
  assert.match(html, /data-db-table="hardware_io"/);
  assert.match(html, /≈ 12,380,996/, "hardware_io rows are labelled as an estimate");
  assert.match(html, /<td class="data-size">12<\/td>/, "exact small-table counts have no ≈");
  assert.match(html, /data-db-range/);
  assert.match(html, /文件不一定变小/);
  assert.match(html, /VACUUM/);
  assert.match(html, /id="data-hardware-io-days"[^>]*min="1"[^>]*max="3650"[^>]*value="30"/);
  assert.match(html, /id="data-hardware-io-rows"[^>]*min="1000"[^>]*max="1000000"[^>]*value="200000"/);
  assert.match(purgeButton(html), /disabled/, "cleanup stays disabled until a preview exists");
});

test("tables are grouped into categories with safety labels and read-only notes", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState());
  const category = id => html.match(new RegExp(`<details[^>]*data-db-category="${id}"[\\s\\S]*?</details>`))?.[0] ?? "";
  assert.match(html, /共 4 张表，按用途分为 A–K 类/);
  assert.match(category("A"), /A\. 任务与执行/);
  assert.match(category("A"), /data-db-safety="keep"/);
  assert.match(category("A"), />保留</);
  assert.match(category("A"), /不会在此页删除/);
  assert.match(category("C"), /C\. 硬件 I\/O/);
  assert.match(category("C"), /data-db-safety="preview_purge"/);
  assert.match(category("C"), /可预览清理/);
  assert.match(category("C"), /≈ 12,380,996/);
  assert.match(category("C"), /data-db-table="hardware_io"/);
  assert.match(category("D"), /可 retire/);
  assert.match(category("D"), /只读：此类数据不会在此页删除/);
  assert.match(category("J"), /此类别在当前数据库中没有表/);
  // An unknown safety value is shown as read-only, never as cleanable.
  assert.match(category("other"), /未分类/);
  assert.match(category("other"), /data-db-safety="read_only"/);
  // The only cleanup controls on the panel belong to hardware_io.
  assert.equal((html.match(/data-hardware-io-purge/g) || []).length, 1);
  assert.match(html, /统计用时 3 ms/);

  const cached = panel.renderDatabasePanel(viewState({database: stats({cached: true})}));
  assert.match(cached, /统计缓存/);
});

test("cleanup is enabled only for a matching, non-empty preview", () => {
  i18n.applyLocale("zh-CN");
  const html = panel.renderDatabasePanel(viewState({plan: plan()}));
  assert.doesNotMatch(purgeButton(html), /disabled/);
  assert.match(html, /本次将删除 200,000 行/);

  assert.equal(panel.purgeEnabled(viewState({plan: plan()}), 30, 200000), true);
  assert.equal(panel.purgeEnabled(viewState({plan: plan()}), 31, 200000), false, "edited days need a new preview");
  assert.equal(panel.purgeEnabled(viewState({plan: plan()}), 30, 5000), false, "edited limit needs a new preview");
  assert.equal(panel.purgeEnabled(viewState({plan: plan(), busy: "preview"}), 30, 200000), false);
  assert.equal(panel.purgeEnabled(viewState({plan: plan({run_rows: 0, skip_reason: "nothing_eligible"})}), 30, 200000), false);

  const skipped = panel.renderDatabasePanel(viewState({plan: plan({run_rows: 0, skip_reason: "nothing_eligible"})}));
  assert.match(purgeButton(skipped), /disabled/);
  assert.match(skipped, /没有早于保留天数的数据/);

  const busy = panel.renderDatabasePanel(viewState({plan: plan(), busy: "purge"}));
  assert.match(busy, /data-hardware-io-preview disabled/);
  assert.match(purgeButton(busy), /disabled/);
});

test("form validation rejects out-of-range and fractional values", () => {
  i18n.applyLocale("zh-CN");
  assert.equal(panel.retentionFormError("30", "200000"), "");
  assert.equal(panel.retentionFormError("1", "1000"), "");
  assert.match(panel.retentionFormError("0", "200000"), /保留天数/);
  assert.match(panel.retentionFormError("", "200000"), /保留天数/);
  assert.match(panel.retentionFormError("1.5", "200000"), /保留天数/);
  assert.match(panel.retentionFormError("3651", "200000"), /保留天数/);
  assert.match(panel.retentionFormError("30", "999"), /单次最多删除行数/);
  assert.match(panel.retentionFormError("30", "1000001"), /单次最多删除行数/);
});

test("database panel strings are translated in English", () => {
  i18n.applyLocale("en-US");
  try {
    const html = panel.renderDatabasePanel(viewState({
      plan: plan(),
      result: {plan: plan(), deleted_rows: 200000, batches: 20, batch_size: 10000, partial: true, skip_reason: "canceled", duration_ms: 900},
    }));
    assert.match(html, /Database details/);
    assert.match(html, /hardware_io retention and cleanup/);
    assert.match(html, /A\. Tasks and execution/);
    assert.match(html, /Preview cleanup/);
    assert.match(html, /Retire only/);
    assert.match(html, /never deleted on this page/);
    assert.match(html, /does not necessarily shrink/);
    assert.match(html, /Confirm cleanup/);
    assert.match(html, /Deleted 200,000 rows in 20 batches/);
    assert.match(html, /only partly finished/);
    assert.match(html, /The request was canceled/);
    assert.doesNotMatch(html, /[一-鿿]/, "no Chinese text leaks into the English panel");
    assert.match(panel.retentionFormError("0", "200000"), /Keep days must be/);
    assert.equal(panel.skipReasonText("already_running"), "Another cleanup is running; this one was skipped.");
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("every database panel key exists in both catalogs", async () => {
  const source = await readFile(resolve(import.meta.dirname, "..", "src", "i18n.ts"), "utf8");
  const zh = source.slice(source.indexOf("const zhCN"), source.indexOf("const enUS"));
  const en = source.slice(source.indexOf("const enUS"), source.indexOf("const legacyEnglish"));
  const keys = [...new Set([...zh.matchAll(/"(data\.db\.[\w.]+)":/g)].map(match => match[1]))];
  assert.ok(keys.length > 30);
  for (const key of keys) assert.ok(en.includes(`"${key}":`), `missing English ${key}`);
  const enKeys = [...en.matchAll(/"(data\.db\.[\w.]+)":/g)].map(match => match[1]);
  assert.equal(enKeys.length, keys.length);
});

test("data page wires the database panel without touching file cleanup", async () => {
  const root = resolve(import.meta.dirname, "..");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const api = await readFile(resolve(root, "src", "api.ts"), "utf8");
  const build = await readFile(resolve(root, "..", "scripts", "build-web.mjs"), "utf8");
  const app = await readFile(resolve(modulesDir, "app.js"), "utf8");
  assert.match(main, /renderDatabasePanel\(state\.dataRetention\)/);
  assert.match(main, /loadDataInventory\(force\), loadDatabaseStats\(force\)/);
  assert.match(main, /api\.purgeHardwareIO\(\{\.\.\.confirmed, confirm: true\}\)/);
  assert.match(main, /window\.confirm\(t\("data\.db\.confirmPurge"/);
  // The existing backup/log cleanup calls are unchanged.
  assert.match(main, /api\.cleanupData\(\{category, keep, confirm: true\}\)/);
  assert.match(main, /api\.cleanupData\(\{category, confirm: true\}\)/);
  assert.match(api, /\/api\/v1\/system\/data\/database/);
  assert.match(api, /\/api\/v1\/system\/data\/hardware-io\/preview/);
  assert.match(api, /\/api\/v1\/system\/data\/hardware-io\/purge/);
  assert.match(api, /\/api\/v1\/system\/data\/hardware-io\/retention", \{method: "PUT"/);
  assert.match(build, /"data_retention"/);
  // app.js and the panel must load the same versioned i18n.js.
  const appI18n = app.match(/from "(\.\/i18n\.js[^"]*)"/)?.[1];
  assert.equal(i18nSpecifier, appI18n);
  assert.match(app, /"\.\/data_retention\.js\?v=/);
});
