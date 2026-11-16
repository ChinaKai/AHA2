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
const COMPACTION_PLAN = {
  mode: "vacuum_in_place", database_path: "/srv/aha2/data/aha2.db",
  file_bytes: 5_000_000_000, wal_bytes: 4_000_000, shm_bytes: 32_768,
  page_size: 4096, page_count: 1_220_000, freelist_count: 100,
  free_bytes: 409_600, estimated_reclaimed_bytes: 409_600,
  required_temp_bytes: 11_000_000_000, disk_free_bytes: 80_000_000_000,
  active_tasks: 0, active_turns: 0, pending_approvals: 0, pet_approvals: 0,
  can_compact: true, blocking_reasons: [], checked_at: NOW,
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
const COMPACTION_PLAN = ${JSON.stringify(COMPACTION_PLAN)};
window.__compactionResolvers = [];
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
  if (path === "/api/v1/system/data/database/compaction" && String(init.method || "GET").toUpperCase() === "GET") {
    return new Promise(resolve => {
      window.__compactionResolvers.push(payload => resolve(payload.ok === false ? json(payload, 500) : json(payload)));
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

  // Compaction close/reopen race: A is in flight, the Owner closes and
  // reopens, B fails, then A succeeds late. B's error must win and A's plan
  // must never enable compaction.
  await page.locator("[data-db-compact]").click();
  await page.waitForSelector("dialog[data-compact-dialog][open]", {timeout: 15000});
  await page.waitForFunction(() => window.__compactionResolvers.length === 1);
  await page.locator("dialog[data-compact-dialog] [data-compact-close]").click();
  await page.waitForFunction(() => document.querySelector("dialog[data-compact-dialog]")?.open === false);
  await page.locator("[data-db-compact]").click();
  await page.waitForSelector("dialog[data-compact-dialog][open]", {timeout: 15000});
  await page.waitForFunction(() => window.__compactionResolvers.length === 2);
  await page.evaluate(() => window.__compactionResolvers[1]({ok: false, error: "compaction_preflight_failed", message: "B preflight failed"}));
  await page.waitForSelector("[data-compact-error]", {timeout: 15000});
  assert.match(await page.locator("[data-compact-error]").innerText(), /B preflight failed/);
  await page.evaluate(plan => window.__compactionResolvers[0]({ok: true, plan}), COMPACTION_PLAN);
  await page.waitForTimeout(100);
  const afterLatePlan = await page.evaluate(() => {
    const dialog = document.querySelector("dialog[data-compact-dialog]");
    return {
      text: dialog.innerText,
      runDisabled: dialog.querySelector("[data-compact-run]")?.disabled !== false,
      hasReady: Boolean(dialog.querySelector("[data-compact-ready]")),
    };
  });
  assert.match(afterLatePlan.text, /B preflight failed/, "the newer failure must survive the old success");
  assert.doesNotMatch(afterLatePlan.text, /可以压缩/, "the old plan must not be shown");
  assert.equal(afterLatePlan.hasReady, false, "the old plan must not render the ready state");
  assert.equal(afterLatePlan.runDisabled, true, "compaction must stay disabled after B failed");
  console.log("data management B dialog phone geometry checks passed");
  console.log("compaction close/reopen race checks passed");
} finally {
  await browser?.close();
  server.close();
}
