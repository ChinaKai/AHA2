// Browser verification for live approval-list updates.
//
// Checks against the real built Web bundle with a stubbed API:
//   - a new pending approval pushed over the global SSE appears in the mounted
//     approval history (records tab, task-813) without any reload or manual
//     action;
//   - an approval resolved elsewhere disappears the same way;
//   - a failed background refresh keeps the last good rows and shows an inline
//     error instead of clearing the list;
//   - an ordinary list refresh writes nothing to the #app-status live region,
//     while a genuinely new approval still announces once (task-495);
//   - a burst of pet_approval events coalesces into at most one follow-up
//     request, and entering records re-reads events missed on another tab.
//
// Dependencies stay in .tools/web-validation; a machine without that directory
// can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE at a local install.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const approval = (id, title, status = "pending") => ({
  id, task_id: "pet-task", kind: "delete_task", title, summary: title, reason: "测试",
  risk: "high", status, created_at: "2026-11-15T00:00:00Z",
});
window.__approvals = [approval("a-1", "删除任务一")];
window.__workbenchReads = 0;
window.__failWorkbench = false;
window.__sources = [];
const pendingApprovals = () => window.__approvals.filter(item => item.status === "pending");
const workbench = () => ({
  ok: true, core: [], worklog: [], approvals: window.__approvals,
  approval_total: window.__approvals.length, updated_at: new Date().toISOString(),
  inspection: {
    health: "ok", version: "test", web_version: "test", uptime_minutes: 1,
    checked_at: new Date().toISOString(), pending_approvals: pendingApprovals().length,
  },
  channels: [], channel_total: 0,
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "test", version: "test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: pendingApprovals().length});
  if (path === "/api/v1/pet/workbench") {
    window.__workbenchReads++;
    if (window.__failWorkbench) return json({ok: false, error: "workbench_refresh_failed"}, 500);
    return json(workbench());
  }
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/providers") return json({providers: []});
  if (path === "/api/v1/env-groups") return json({env_groups: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/mcp-servers") return json({servers: []});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class {
  constructor(url) { this.url = String(url); this.listeners = {}; this.closed = false; window.__sources.push(this); }
  close() { this.closed = true; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
};
window.__emit = (type, payload) => {
  for (const source of window.__sources) {
    if (source.closed || !source.url.startsWith("/api/v1/events")) continue;
    for (const fn of source.listeners[type] || []) fn({data: JSON.stringify(payload)});
  }
};
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "pet-workbench"}));
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><div id="app-status" class="visually-hidden" role="status" aria-live="polite" aria-atomic="true"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name === "/favicon.ico") {
      response.writeHead(204).end();
    } else if (name.startsWith("/assets/")) {
      response.setHeader("Content-Type", name.endsWith(".svg") ? "image/svg+xml" : "image/png");
      response.end(await readFile(resolve(root, "web/build", name.replace(/^\//, ""))));
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

const emitApproval = page => page.evaluate(() => window.__emit("update", {type: "pet_approval", data: {}}));
const row = (page, id) => page.locator(`[data-pet-approval-open="${id}"]`);
const rowIDs = page => page.evaluate(() => [...document.querySelectorAll("[data-pet-approval-open]")].map(node => node.dataset.petApprovalOpen));
const workbenchReads = page => page.evaluate(() => window.__workbenchReads);
const announcements = page => page.evaluate(() => window.__announcements || []);

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
  await page.goto(url, {waitUntil: "domcontentloaded"});
  await page.waitForSelector('[data-pet-workbench-tab="records"]', {timeout: 20000});
  assert.equal(await page.locator('[data-pet-workbench-tab="approvals"]').count(), 0, "the approvals tab is gone");
  // The initial overview load also proves the page is bound; clicking before
  // bindPetWorkbenchPage attaches its tab listeners would be a no-op.
  await page.waitForSelector(".pet-overview", {timeout: 20000});
  await page.click('[data-pet-workbench-tab="records"]');
  await page.waitForSelector('#pet-record-approvals [data-pet-approval-filters-open]', {timeout: 15000});
  await page.waitForSelector('[data-pet-approval-open="a-1"]', {timeout: 15000});
  // The history shows every status by default; this test follows the pending
  // queue, so narrow it to pending through the real filter dialog.
  await page.click("#pet-record-approvals [data-pet-approval-filters-open]");
  await page.click('dialog.pet-approval-filter-dialog [data-pet-approval-status-filter="pending"]');
  await page.click("dialog.pet-approval-filter-dialog [data-pet-approval-filters-close]");
  await page.waitForSelector("dialog.pet-approval-filter-dialog", {state: "detached"});

  // Record every non-empty write to the persistent live region.
  await page.evaluate(() => {
    window.__announcements = [];
    const node = document.querySelector("#app-status");
    new MutationObserver(() => {
      const text = node.textContent.trim();
      if (text) window.__announcements.push(text);
    }).observe(node, {childList: true, characterData: true, subtree: true});
  });

  // A new approval appears over SSE with no reload or manual action, and the
  // task-495 count increase announces exactly once.
  await page.evaluate(() => { window.__approvals.push({id: "a-2", task_id: "pet-task", kind: "delete_task", title: "删除任务二", summary: "删除任务二", reason: "测试", risk: "high", status: "pending", created_at: "2026-11-15T00:01:00Z"}); });
  await emitApproval(page);
  await row(page, "a-2").waitFor({timeout: 15000});
  await page.waitForFunction(() => document.querySelector("#app-status").textContent.includes("新的审批"), null, {timeout: 15000});
  assert.deepEqual((await rowIDs(page)).sort(), ["a-1", "a-2"]);
  assert.equal((await announcements(page)).filter(text => text.includes("新的审批")).length, 1, "a new approval must announce once");

  // An approval resolved elsewhere disappears from the pending list silently.
  const announcementsBeforeResolve = (await announcements(page)).length;
  await page.evaluate(() => { window.__approvals.find(item => item.id === "a-1").status = "approved"; });
  await emitApproval(page);
  await page.waitForFunction(() => !document.querySelector('[data-pet-approval-open="a-1"]'), null, {timeout: 15000});
  assert.deepEqual(await rowIDs(page), ["a-2"]);
  await page.waitForTimeout(300);
  assert.equal((await announcements(page)).length, announcementsBeforeResolve, "a settled approval must not announce");

  // A failed background refresh keeps the last good rows and shows an error.
  await page.evaluate(() => {
    window.__failWorkbench = true;
    window.__approvals.push({id: "a-3", task_id: "pet-task", kind: "delete_task", title: "删除任务三", summary: "删除任务三", reason: "测试", risk: "high", status: "pending", created_at: "2026-11-15T00:02:00Z"});
  });
  await emitApproval(page);
  await page.waitForFunction(() => document.querySelector("#pet-workbench-body")?.textContent.includes("workbench_refresh_failed"), null, {timeout: 15000});
  assert.deepEqual(await rowIDs(page), ["a-2"], "a failed refresh must keep the old list");
  assert.notEqual((await page.locator("#pet-workbench-body").innerText()).trim(), "", "a failed refresh must not blank the list");

  // Retry recovers through the inline button.
  await page.evaluate(() => { window.__failWorkbench = false; });
  await page.click("[data-pet-workbench-retry]");
  await row(page, "a-3").waitFor({timeout: 15000});
  assert.deepEqual((await rowIDs(page)).sort(), ["a-2", "a-3"]);
  assert.doesNotMatch(await page.locator("#pet-workbench-body").innerText(), /workbench_refresh_failed/);

  // A list-only refresh (same pending count) updates rows without announcing.
  const announcementsBeforePatch = (await announcements(page)).length;
  await page.evaluate(() => { window.__approvals.find(item => item.id === "a-2").title = "改名后的审批"; });
  await emitApproval(page);
  await page.waitForFunction(() => document.querySelector('[data-pet-approval-open="a-2"] strong')?.textContent === "改名后的审批", null, {timeout: 15000});
  await page.waitForTimeout(300);
  assert.equal((await announcements(page)).length, announcementsBeforePatch, "an ordinary list patch must not announce");

  // A burst coalesces: one read plus at most one queued follow-up.
  await page.evaluate(() => { window.__approvals.push({id: "a-4", task_id: "pet-task", kind: "delete_task", title: "删除任务四", summary: "删除任务四", reason: "测试", risk: "high", status: "pending", created_at: "2026-11-15T00:03:00Z"}); });
  const readsBeforeBurst = await workbenchReads(page);
  await page.evaluate(() => { for (let index = 0; index < 5; index++) window.__emit("update", {type: "pet_approval", data: {}}); });
  await row(page, "a-4").waitFor({timeout: 15000});
  await page.waitForTimeout(700);
  const burstReads = (await workbenchReads(page)) - readsBeforeBurst;
  assert.ok(burstReads <= 2, `five approval events caused ${burstReads} workbench reads`);

  // Events that arrive on another tab do not patch hidden rows, but entering
  // records re-reads, so the missed approval is there immediately.
  await page.click('[data-pet-workbench-tab="overview"]');
  await page.waitForSelector(".pet-overview", {timeout: 15000});
  await page.evaluate(() => { window.__approvals.push({id: "a-5", task_id: "pet-task", kind: "delete_task", title: "删除任务五", summary: "删除任务五", reason: "测试", risk: "high", status: "pending", created_at: "2026-11-15T00:04:00Z"}); });
  await emitApproval(page);
  await page.waitForTimeout(300);
  assert.equal(await row(page, "a-5").count(), 0, "the overview must not render approval rows");
  await page.click('[data-pet-workbench-tab="records"]');
  await row(page, "a-5").waitFor({timeout: 15000});
  assert.ok((await rowIDs(page)).includes("a-5"));

  assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);
  console.log("pet approval refresh browser checks passed");
} finally {
  await browser?.close();
  server.close();
}