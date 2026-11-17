// Phone geometry for the data-management cleanup dialogs. The B-category
// checkbox is the one control the global `input { width: 100%; height: 38px }`
// rule used to stretch, so this checks the real 390px layout instead of only
// the CSS selectors.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = process.env.AHA_WEB_VALIDATION || resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const executablePath = process.env.AHA_BROWSER_EXECUTABLE;
const launchOptions = executablePath
  ? {executablePath, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const screenshots = process.env.AHA_BROWSER_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(screenshots, {recursive: true});

const NOW = "2026-11-16T00:00:00Z";
const OFFLINE_INFO = {
  data_dir: "/srv/aha2/data", database_path: "/srv/aha2/data/aha2.db",
  backup_dir: "/srv/aha2/data/backups", command: 'aha2 db compact --data-dir "/srv/aha2/data"',
  file_bytes: 5_000_000_000, wal_bytes: 4_000_000, shm_bytes: 32_768,
  required_bytes: 11_000_000_000, disk_free_bytes: 80_000_000_000,
  database_exists: true,
};
const INVENTORY = {
  data_dir: "/srv/aha2/data", total_bytes: 5_200_000_000, backup_count: 2,
  categories: [
    {id: "backups", label: "安装备份", bytes: 1000, safe_cleanup: true},
    {id: "database", label: "数据库", bytes: 5_000_000_000},
    {id: "logs", label: "日志与临时文件", bytes: 20},
    {id: "attachments", label: "附件", bytes: 30},
  ],
};
const STATS = {
  file_bytes: 5_000_000_000, wal_bytes: 4_000_000, shm_bytes: 32_768,
  page_size: 4096, page_count: 1_220_000, freelist_count: 100,
  used_bytes: 4_999_000_000, free_bytes: 409_600,
  table_count: 4, cached: false,
  categories: [
    {id: "A", key: "tasks", safety: "keep", rows: 159, rows_exact: true, estimated_bytes: 1000, tables: []},
    {id: "B", key: "conversation", safety: "preview_purge", cleanup: "conversation", rows: 400, rows_exact: true, estimated_bytes: 8000, tables: []},
    {id: "C", key: "hardware", safety: "preview_purge", cleanup: "hardware_io", rows: 12_000_000, rows_exact: false, estimated_bytes: 2_000_000_000, tables: []},
    {id: "J", key: "attachments", safety: "read_only", rows: 0, rows_exact: true, estimated_bytes: 0, tables: []},
  ],
  hardware_io: {min_sequence: 1, max_sequence: 12_000_000, estimated_rows: 12_000_000},
  retention: {retention_days: 30, max_delete_rows: 200000},
  conversation_retention: {retention_days: 30, purge_tool: true, purge_update: false},
  collected_at: NOW, duration_ms: 3,
};

function bootstrap() {
  return `
const INVENTORY = ${JSON.stringify(INVENTORY)};
const STATS = ${JSON.stringify(STATS)};
const OFFLINE_INFO = ${JSON.stringify(OFFLINE_INFO)};
const HARDWARE_PLAN = {
  retention_days: 30, max_delete_rows: 200000, cutoff: "2026-10-17T00:00:00Z",
  min_sequence: 1, max_sequence: 12_000_000, cutoff_sequence: 6_000_001,
  eligible_rows_estimate: 6_000_000, run_start_sequence: 1, run_end_sequence: 200_001,
  run_rows: 200000, avg_row_bytes: 180, run_bytes_estimate: 36_000_000,
  eligible_bytes_estimate: 1_080_000_000, remaining_eligible_estimate: 5_800_000,
};
const CONVERSATION_PLAN = {
  retention_days: 30, purge_tool: true, purge_update: false, categories: ["tool"],
  cutoff: "2026-10-17T00:00:00Z", eligible_rows: 1200, run_rows: 1200,
  tasks_affected: 7, avg_row_bytes: 180, run_bytes_estimate: 216_000,
};
window.__offlineResolvers = [];
window.__hardwareCalls = [];
window.__purgeResolvers = {};
window.__purgeProgress = {
  hardware_io: {kind: "hardware_io", running: false, phase: "idle", planned_rows: 0, deleted_rows: 0, batches: 0, duration_ms: 0},
  conversation: {kind: "conversation", running: false, phase: "idle", planned_rows: 0, deleted_rows: 0, batches: 0, duration_ms: 0},
};
window.__resolvePurge = (kind, body, status = 200) => {
  window.__purgeResolvers[kind](new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}}));
};
window.__offlineResolvers = [];
window.__hardwareCalls = [];
window.__backupMode = "ok";
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: "${NOW}"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path === "/api/v1/system/data") return json({ok: true, inventory: INVENTORY});
  if (path === "/api/v1/system/data/database") return json({ok: true, database: STATS});
  if (path === "/api/v1/system/data/hardware-io/retention") {
    const body = JSON.parse(init.body || "{}");
    window.__hardwareCalls.push({kind: "save", body});
    return json({ok: true, retention: {retention_days: body.retention_days, max_delete_rows: body.max_delete_rows}});
  }
  if (path === "/api/v1/system/data/hardware-io/preview") {
    const body = JSON.parse(init.body || "{}");
    window.__hardwareCalls.push({kind: "preview", body});
    return json({ok: true, plan: {...HARDWARE_PLAN, retention_days: body.retention_days, max_delete_rows: body.max_delete_rows}});
  }
  if (path === "/api/v1/system/data/hardware-io/purge/progress") {
    return json({ok: true, progress: window.__purgeProgress.hardware_io});
  }
  if (path === "/api/v1/system/data/hardware-io/purge") {
    return new Promise(resolve => { window.__purgeResolvers.hardware = resolve; });
  }
  if (path === "/api/v1/system/data/conversation/preview") {
    const body = JSON.parse(init.body || "{}");
    return json({ok: true, plan: {...CONVERSATION_PLAN, retention_days: body.retention_days, purge_tool: body.purge_tool, purge_update: body.purge_update}});
  }
  if (path === "/api/v1/system/data/conversation/purge/progress") {
    return json({ok: true, progress: window.__purgeProgress.conversation});
  }
  if (path === "/api/v1/system/data/conversation/purge") {
    return new Promise(resolve => { window.__purgeResolvers.conversation = resolve; });
  }
  if (path === "/api/v1/system/data/backups") {
    if (window.__backupMode === "error") return json({ok: false, error: "data_cleanup_preview_failed", message: "backup scan failed"}, 500);
    return json({ok: true, preview: {
      keep: 1,
      retained: {name: "user-installed-3", bytes: 100, mod_time: "${NOW}"},
      candidates: [
        {name: "user-installed-1", bytes: 100, mod_time: "${NOW}"},
        {name: "user-installed-2", bytes: 100, mod_time: "${NOW}"},
      ],
      freed_bytes: 200,
      can_clean: true,
    }});
  }
  if (path === "/api/v1/system/data/database/offline-compaction" && String(init.method || "GET").toUpperCase() === "GET") {
    return new Promise(resolve => {
      window.__offlineResolvers.push(payload => resolve(payload.ok === false ? json(payload, 500) : json(payload)));
    });
  }
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;
}

const pageHTML = fixture => `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><div id="app-status" class="visually-hidden" role="status" aria-live="polite" aria-atomic="true"></div><script src="${fixture}"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon"};
const server = createServer(async (request, response) => {
  const pathname = new URL(request.url, "http://localhost").pathname;
  try {
    if (pathname === "/" || pathname === "/index.html") return response.end(pageHTML("/fixture.js"));
    if (pathname === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      return response.end(bootstrap());
    }
    const data = await readFile(resolve(root, "web", "build", pathname.slice(1)));
    response.setHeader("Content-Type", mime[extname(pathname)] || "application/octet-stream");
    response.end(data);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolveListening => server.listen(0, "127.0.0.1", resolveListening));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 390, height: 844}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=advanced]")?.click());
  await page.waitForSelector('[data-view="data"]', {timeout: 15000});
  await page.locator('[data-view="data"]').click();
  await page.waitForSelector(".data-management-page", {timeout: 15000});
  await page.waitForSelector('[data-db-cleanup="B"]', {timeout: 15000});
  await page.locator('[data-db-cleanup="B"]').click();
  await page.waitForSelector('dialog[data-db-dialog="B"][open]', {timeout: 15000});

  const geometry = await page.evaluate(() => {
    const dialog = document.querySelector('dialog[data-db-dialog="B"]');
    const checks = dialog.querySelector(".data-db-checks");
    const rows = [...checks.querySelectorAll("label")].map(label => {
      const input = label.querySelector('input[type="checkbox"]');
      const inputBox = input.getBoundingClientRect();
      const labelBox = label.getBoundingClientRect();
      const textNode = [...label.childNodes].find(node => node.nodeType === Node.TEXT_NODE && node.textContent.trim());
      const range = document.createRange();
      range.selectNodeContents(textNode);
      const textBox = range.getBoundingClientRect();
      return {
        input: {width: inputBox.width, height: inputBox.height, top: inputBox.top, right: inputBox.right},
        label: {top: labelBox.top, bottom: labelBox.bottom, right: labelBox.right},
        textLeft: textBox.left,
        textTop: textBox.top,
        textBottom: textBox.bottom,
      };
    });
    return {
      rows,
      dialogOverflow: dialog.scrollWidth - dialog.clientWidth,
      checksOverflow: checks.scrollWidth - checks.clientWidth,
      pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });

  assert.equal(errors.length, 0, `page errors: ${errors.join(" | ")}`);
  assert.equal(geometry.rows.length, 2, "both B checkboxes are present");
  for (const [index, row] of geometry.rows.entries()) {
    assert.ok(Math.abs(row.input.width - 16) <= 0.5, `row ${index + 1} checkbox width ${row.input.width}px, want 16px`);
    assert.ok(Math.abs(row.input.height - 16) <= 0.5, `row ${index + 1} checkbox height ${row.input.height}px, want 16px`);
    assert.ok(row.input.top >= row.label.top - 1 && row.input.top <= row.label.bottom, `row ${index + 1} checkbox stays inside its label`);
    assert.ok(row.textLeft - row.input.right >= 4, `row ${index + 1} checkbox/text gap ${row.textLeft - row.input.right}px is too tight`);
    assert.ok(Math.abs((row.input.top + row.input.height / 2) - (row.textTop + row.textBottom) / 2) <= 2, `row ${index + 1} checkbox and text share a baseline`);
    assert.ok(row.input.right <= row.label.right + 0.5, `row ${index + 1} checkbox does not leave the label`);
  }
  assert.ok(geometry.rows[1].label.top >= geometry.rows[0].label.bottom - 1, "checkbox rows do not overlap");
  assert.ok(geometry.dialogOverflow <= 1, `B dialog overflows by ${geometry.dialogOverflow}px at 390px`);
  assert.ok(geometry.checksOverflow <= 1, `checkbox block overflows by ${geometry.checksOverflow}px at 390px`);
  assert.ok(geometry.pageOverflow <= 1, `page overflows by ${geometry.pageOverflow}px at 390px`);

  await page.screenshot({path: resolve(screenshots, "data-management-b-dialog-390.png"), fullPage: false});
  await page.locator('dialog[data-db-dialog="B"] [data-db-dialog-close]').click();
  await page.waitForFunction(() => document.querySelector('dialog[data-db-dialog="B"]')?.open === false);

  // Hardware IO: only the retention input remains, and the save/preview/purge
  // buttons sit on their own row instead of sharing the input row.
  await page.locator('[data-db-cleanup="C"]').click();
  await page.waitForSelector('dialog[data-db-dialog="C"][open]', {timeout: 15000});
  const hardwareLayout = await page.evaluate(() => {
    const dialog = document.querySelector('dialog[data-db-dialog="C"]');
    const daysRow = dialog.querySelector(".data-db-form");
    const actionRow = dialog.querySelector(".data-cleanup-actions:not(.data-db-form)");
    const box = node => node.getBoundingClientRect();
    return {
      inputs: [...dialog.querySelectorAll("input")].map(node => ({id: node.id, type: node.type})),
      rowsInput: dialog.querySelector("#data-hardware-io-rows") !== null,
      daysRow: box(daysRow),
      actionRow: box(actionRow),
      actions: [...actionRow.querySelectorAll("button")].map(node => ({label: node.textContent.trim(), top: box(node).top, bottom: box(node).bottom})),
      dialogOverflow: dialog.scrollWidth - dialog.clientWidth,
      pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
  assert.deepEqual(hardwareLayout.inputs, [{id: "data-hardware-io-days", type: "number"}], "hardware IO exposes only the retention input");
  assert.equal(hardwareLayout.rowsInput, false, "the max-rows input is gone");
  assert.match(hardwareLayout.actions.map(action => action.label).join(","), /保存设置,预览,确认清理/);
  assert.ok(hardwareLayout.actionRow.top >= hardwareLayout.daysRow.bottom - 1, "the action buttons start on the row below the input");
  assert.ok(hardwareLayout.actions.every(action => action.top >= hardwareLayout.daysRow.bottom - 1), "no action shares the input row");
  assert.ok(hardwareLayout.dialogOverflow <= 1, `C dialog overflows by ${hardwareLayout.dialogOverflow}px at 390px`);
  assert.ok(hardwareLayout.pageOverflow <= 1, `page overflows by ${hardwareLayout.pageOverflow}px at 390px`);

  // On a desktop viewport the three actions collapse onto one row of their
  // own, still below the retention input.
  await page.setViewportSize({width: 1280, height: 900});
  await page.waitForTimeout(80);
  const desktopActions = await page.evaluate(() => {
    const dialog = document.querySelector('dialog[data-db-dialog="C"]');
    const daysRow = dialog.querySelector(".data-db-form").getBoundingClientRect();
    const actionRow = dialog.querySelector(".data-cleanup-actions:not(.data-db-form)").getBoundingClientRect();
    const tops = [...dialog.querySelectorAll("[data-hardware-io-save], [data-hardware-io-preview], [data-hardware-io-purge]")].map(node => node.getBoundingClientRect().top);
    return {daysBottom: daysRow.bottom, actionTop: actionRow.top, spread: Math.max(...tops) - Math.min(...tops)};
  });
  assert.ok(desktopActions.actionTop >= desktopActions.daysBottom - 1, "desktop action row stays below the input");
  assert.ok(desktopActions.spread <= 2, "desktop save/preview/purge share one row");
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForTimeout(80);

  // Save and preview still work and send the internal 200000-row safety cap.
  await page.locator("[data-hardware-io-save]").click();
  await page.waitForFunction(() => window.__hardwareCalls.length === 1);
  await page.locator("[data-hardware-io-preview]").click();
  await page.waitForFunction(() => window.__hardwareCalls.length === 2);
  const hardwareCalls = await page.evaluate(() => window.__hardwareCalls);
  assert.deepEqual(hardwareCalls.map(call => call.kind), ["save", "preview"]);
  for (const call of hardwareCalls) {
    assert.deepEqual(call.body, {retention_days: 30, max_delete_rows: 200000}, `${call.kind} sends the internal batch cap`);
  }
  // The preview writes its plan into the open dialog in place: no close/reopen
  // and no page refresh are allowed to be required.
  await page.waitForSelector('dialog[data-db-dialog="C"] [data-db-plan]', {timeout: 15000});
  const previewText = await page.locator('dialog[data-db-dialog="C"]').innerText();
  assert.match(previewText, /截止时间/, "the preview cutoff appears while the dialog stays open");
  assert.match(previewText, /本次将删除 200,000 行/, "the preview run size appears in place");
  assert.equal(await page.locator('dialog[data-db-dialog="C"] [data-hardware-io-purge]').isDisabled(), false, "a matching preview enables cleanup");
  await page.screenshot({path: resolve(screenshots, "data-management-c-dialog-390.png"), fullPage: false});

  // Confirmed cleanup: the server keeps the request open, so the dialog polls
  // the progress endpoint and shows phase + deleted rows + batches.
  await page.evaluate(() => {
    window.__purgeProgress.hardware_io = {
      kind: "hardware_io", running: true, phase: "deleting", planned_rows: 200000,
      deleted_rows: 30000, batches: 3, started_at: new Date().toISOString(),
      updated_at: new Date().toISOString(), duration_ms: 2500,
    };
  });
  await page.locator('dialog[data-db-dialog="C"] [data-hardware-io-purge]').click();
  await page.waitForSelector('dialog[data-db-dialog="C"] [data-db-progress]', {timeout: 15000});
  await page.waitForFunction(
    () => (document.querySelector('dialog[data-db-dialog="C"] [data-db-progress]')?.textContent || "").includes("30,000"),
    null,
    {timeout: 15000},
  );
  const progressText = await page.locator('dialog[data-db-dialog="C"] [data-db-progress]').innerText();
  assert.match(progressText, /正在删除旧数据/);
  assert.match(progressText, /已删除 30,000 行，共 3 批/);
  assert.match(progressText, /本次计划删除 200,000 行/);

  await page.evaluate(() => {
    window.__purgeProgress.hardware_io = {...window.__purgeProgress.hardware_io, running: false, phase: "done", deleted_rows: 200000, batches: 20};
    window.__resolvePurge("hardware", {
      ok: true,
      result: {plan: {}, backup_path: "/srv/aha2/backups/hardware-io-1/aha2.db", deleted_rows: 200000, batches: 20, batch_size: 10000, partial: false, duration_ms: 5000},
      database: STATS,
    });
  });
  await page.waitForSelector('dialog[data-db-dialog="C"] [data-db-result]', {timeout: 15000});
  const resultText = await page.locator('dialog[data-db-dialog="C"] [data-db-result]').innerText();
  assert.match(resultText, /已删除 200,000 行，共 20 批/);
  assert.match(resultText, /hardware-io-1/);
  assert.equal(await page.locator('dialog[data-db-dialog="C"] [data-db-progress]').count(), 0, "the finished run replaces progress with its result");

  // A failed run keeps the message and any partial result in the dialog and
  // releases every button instead of spinning forever.
  await page.locator('dialog[data-db-dialog="C"] [data-hardware-io-preview]').click();
  await page.waitForSelector('dialog[data-db-dialog="C"] [data-db-plan]', {timeout: 15000});
  await page.evaluate(() => { delete window.__purgeResolvers.hardware; });
  await page.locator('dialog[data-db-dialog="C"] [data-hardware-io-purge]').click();
  await page.waitForFunction(() => Boolean(window.__purgeResolvers.hardware), null, {timeout: 15000});
  await page.evaluate(() => {
    window.__purgeProgress.hardware_io = {...window.__purgeProgress.hardware_io, running: false, phase: "failed"};
    window.__resolvePurge("hardware", {
      ok: false, error: "hardware_io_purge_failed", message: "backup failed",
      result: {plan: {}, deleted_rows: 5000, batches: 1, batch_size: 10000, partial: true, duration_ms: 900},
    }, 500);
  });
  await page.waitForSelector('dialog[data-db-dialog="C"] [data-hardware-io-action-error]', {timeout: 15000});
  const failedText = await page.locator('dialog[data-db-dialog="C"]').innerText();
  assert.match(failedText, /backup failed/);
  assert.match(failedText, /只完成了一部分/);
  assert.equal(await page.locator('dialog[data-db-dialog="C"] [data-db-progress]').count(), 0, "a failed run clears the progress block");
  assert.equal(await page.locator('dialog[data-db-dialog="C"] [data-hardware-io-preview]').isDisabled(), false, "a failed run releases the dialog buttons");
  await page.locator('dialog[data-db-dialog="C"] [data-db-dialog-close]').click();
  await page.waitForFunction(() => document.querySelector('dialog[data-db-dialog="C"]')?.open === false);

  // B conversation cleanup has the same in-place preview, progress and result.
  await page.locator('[data-db-cleanup="B"]').click();
  await page.waitForSelector('dialog[data-db-dialog="B"][open]', {timeout: 15000});
  await page.locator('dialog[data-db-dialog="B"] [data-conversation-preview]').click();
  await page.waitForSelector('dialog[data-db-dialog="B"] [data-conversation-plan]', {timeout: 15000});
  assert.equal(await page.locator('dialog[data-db-dialog="B"] [data-conversation-purge]').isDisabled(), false, "the B preview enables cleanup in place");
  await page.evaluate(() => {
    window.__purgeProgress.conversation = {
      kind: "conversation", running: true, phase: "deleting", planned_rows: 1200,
      deleted_rows: 400, batches: 1, started_at: new Date().toISOString(),
      updated_at: new Date().toISOString(), duration_ms: 1500,
    };
    delete window.__purgeResolvers.conversation;
  });
  await page.locator('dialog[data-db-dialog="B"] [data-conversation-purge]').click();
  await page.waitForSelector('dialog[data-db-dialog="B"] [data-db-progress]', {timeout: 15000});
  await page.waitForFunction(
    () => (document.querySelector('dialog[data-db-dialog="B"] [data-db-progress]')?.textContent || "").includes("400"),
    null,
    {timeout: 15000},
  );
  await page.evaluate(() => {
    window.__purgeProgress.conversation = {...window.__purgeProgress.conversation, running: false, phase: "done", deleted_rows: 1200, batches: 2};
    window.__resolvePurge("conversation", {
      ok: true,
      result: {plan: {}, deleted_rows: 1200, batches: 2, batch_size: 10000, partial: false, duration_ms: 2000},
      database: STATS,
    });
  });
  await page.waitForSelector('dialog[data-db-dialog="B"] [data-conversation-result]', {timeout: 15000});
  assert.match(await page.locator('dialog[data-db-dialog="B"] [data-conversation-result]').innerText(), /已删除 1,200 行，共 2 批/);
  await page.locator('dialog[data-db-dialog="B"] [data-db-dialog-close]').click();
  await page.waitForFunction(() => document.querySelector('dialog[data-db-dialog="B"]')?.open === false);

  // Backup preview must replace "reading" in place while the dialog stays
  // open, then fail into a retry that can recover.
  await page.locator('[data-data-cleanup="backups"]').click();
  await page.waitForSelector("dialog[data-backup-dialog][open]", {timeout: 15000});
  await page.waitForSelector("[data-backup-confirm]", {timeout: 15000});
  const backupText = await page.locator("dialog[data-backup-dialog]").innerText();
  assert.match(backupText, /user-installed-1/);
  assert.doesNotMatch(backupText, /正在读取安装备份列表/, "the preview must not stay on the reading line");
  await page.locator("dialog[data-backup-dialog] [data-backup-close]").click();
  await page.waitForFunction(() => document.querySelector("dialog[data-backup-dialog]")?.open === false);

  await page.evaluate(() => { window.__backupMode = "error"; });
  await page.locator('[data-data-cleanup="backups"]').click();
  await page.waitForSelector("[data-backup-error]", {timeout: 15000});
  assert.match(await page.locator("[data-backup-error]").innerText(), /backup scan failed/);
  await page.evaluate(() => { window.__backupMode = "ok"; });
  await page.locator("[data-backup-retry]").click();
  await page.waitForSelector("[data-backup-confirm]", {timeout: 15000});
  assert.equal(await page.locator("[data-backup-error]").count(), 0);
  await page.locator("dialog[data-backup-dialog] [data-backup-close]").click();
  await page.waitForFunction(() => document.querySelector("dialog[data-backup-dialog]")?.open === false);

  // Offline compaction is a read-only guide. It must show sizes, the data
  // directory, steps and the copyable command, and it must not expose a button
  // that starts a VACUUM while the service is serving.
  await page.locator("[data-db-offline-compact]").click();
  await page.waitForSelector("dialog[data-offline-compact-dialog][open]", {timeout: 15000});
  await page.waitForFunction(() => window.__offlineResolvers.length === 1);
  await page.evaluate(info => window.__offlineResolvers[0]({ok: true, info}), OFFLINE_INFO);
  await page.waitForSelector("[data-offline-command]", {timeout: 15000});
  const guide = await page.locator("dialog[data-offline-compact-dialog]").innerText();
  assert.match(guide, /\/srv\/aha2\/data/);
  assert.match(guide, /操作步骤/);
  assert.match(guide, /aha2 db compact --data-dir/);
  assert.match(guide, /还没有离线压缩记录/);
  assert.doesNotMatch(guide, /确认压缩/, "the online run button must be gone");
  assert.equal(await page.locator("dialog[data-offline-compact-dialog] [data-offline-run]").count(), 0);
  await page.locator("dialog[data-offline-compact-dialog] [data-offline-compact-close]").click();
  await page.waitForFunction(() => document.querySelector("dialog[data-offline-compact-dialog]")?.open === false);

  // Close/reopen race: A is in flight, the Owner closes and reopens, B fails,
  // then A succeeds late. B's error must win and A's info must never render.
  await page.locator("[data-db-offline-compact]").click();
  await page.waitForSelector("dialog[data-offline-compact-dialog][open]", {timeout: 15000});
  await page.waitForFunction(() => window.__offlineResolvers.length === 2);
  await page.locator("dialog[data-offline-compact-dialog] [data-offline-compact-close]").click();
  await page.waitForFunction(() => document.querySelector("dialog[data-offline-compact-dialog]")?.open === false);
  await page.locator("[data-db-offline-compact]").click();
  await page.waitForSelector("dialog[data-offline-compact-dialog][open]", {timeout: 15000});
  await page.waitForFunction(() => window.__offlineResolvers.length === 3);
  await page.evaluate(() => window.__offlineResolvers[2]({ok: false, error: "offline_info_failed", message: "B info failed"}));
  await page.waitForSelector("[data-offline-compaction-error]", {timeout: 15000});
  assert.match(await page.locator("[data-offline-compaction-error]").innerText(), /B info failed/);
  await page.evaluate(info => window.__offlineResolvers[1]({ok: true, info}), OFFLINE_INFO);
  await page.waitForTimeout(100);
  const afterLateInfo = await page.evaluate(() => {
    const dialog = document.querySelector("dialog[data-offline-compact-dialog]");
    return {
      text: dialog.innerText,
      hasCommand: Boolean(dialog.querySelector("[data-offline-command]")),
    };
  });
  assert.match(afterLateInfo.text, /B info failed/, "the newer failure must survive the old success");
  assert.equal(afterLateInfo.hasCommand, false, "the old info must not render the guide");
  console.log("offline compaction guide checks passed");
  console.log("data management B dialog phone geometry checks passed");
} finally {
  await browser?.close();
  server.close();
}
