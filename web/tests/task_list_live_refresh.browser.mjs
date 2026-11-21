// Optional browser verification for live task-list refreshes (SSE -> patch).
// Dependencies and browser files stay in .tools/web-validation by default; a
// machine without that directory can point AHA_PLAYWRIGHT_MODULE /
// AHA_BROWSER_EXECUTABLE at a local Playwright install (see
// docs/web-phase4-milestone2b-decision.md).
//
// Checks, against the real built Web bundle:
//   - A status change that alters the filter-facet counts refreshes the list:
//     changed rows update or leave, order and untouched rows survive, the open
//     filter popover stays open, and the page does not scroll.
//   - The in-row title editor keeps its focus and typed value across a refresh;
//     Escape restores the rendered title.
//   - An open Task detail still refreshes the list data behind the task band,
//     but leaves the mounted list and composer alone; closing shows the
//     already-fresh rows.
//   - No page errors and no horizontal overflow.
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

// 60 open Tasks: the first page shows 50, so one "load more" page is available
// and the list is long enough to scroll. Status groups are what the filter
// popover counts, so a task moving to completed changes the facet HTML and
// exercises the region-replacement path.
const TASKS = Array.from({length: 60}, (_, index) => ({
  id: `task-${String(index).padStart(3, "0")}`,
  code: `task-${String(1000 + index)}`,
  title: `任务 ${index + 1}：列表实时刷新验证`,
  status: index % 2 === 0 ? "active" : "waiting_user",
  project_id: "project-1",
  workspace_id: "workspace-1",
  total_tokens: index * 100,
  created_at: new Date(Date.UTC(2026, 10, 13, 12, 0, 0) - index * 60_000).toISOString(),
}));
const bootstrap = `
const TASKS = ${JSON.stringify(TASKS)};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const counts = () => {
  const status = {};
  const project = {};
  for (const task of TASKS) {
    status[task.status] = (status[task.status] || 0) + 1;
    project[task.project_id] = (project[task.project_id] || 0) + 1;
  }
  return {status, project, device: {local: TASKS.length}};
};
const detail = id => ({
  task: TASKS.find(task => task.id === id) || TASKS[0],
  turns: [],
  activity: null,
  agents: [{agent_id: "main", backend: "claude", model: "claude-sonnet-5", status: "idle"}],
  hardware: [],
  hardware_resources: [],
  permission_requests: [],
  event_cursor: 0,
  server_time_ms: Date.now(),
});
const streams = [];
window.EventSource = class {
  constructor(url) { this.url = String(url); this.listeners = {}; this.closed = false; streams.push(this); }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  close() { this.closed = true; }
  emit(type, payload) { for (const fn of this.listeners[type] || []) fn({data: JSON.stringify(payload)}); }
};
window.__emitGlobal = (type, data) => {
  for (const stream of streams) {
    if (!stream.closed && stream.url === "/api/v1/events") stream.emit("update", {type, data});
  }
};
window.__fixture = {
  tasks: TASKS,
  taskRequests: 0,
  change(id, patch) { Object.assign(TASKS.find(task => task.id === id), patch); },
};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") {
    const limit = Number(url.searchParams.get("limit") || 50);
    const cursor = Number(url.searchParams.get("cursor") || 0);
    const slice = TASKS.slice(cursor, cursor + limit);
    const next = cursor + limit < TASKS.length ? String(cursor + limit) : "";
    window.__fixture.taskRequests++;
    return json({tasks: slice, has_more: Boolean(next), next_cursor: next, counts: counts()});
  }
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false}]});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready"}]});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path.startsWith("/api/v1/tasks/")) {
    const rest = path.slice("/api/v1/tasks/".length);
    const [id, ...tail] = rest.split("/");
    if (!tail.length) return json(detail(id));
    if (tail[0] === "agents" && tail[2] === "conversation") return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
    return json({});
  }
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;
const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
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
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

const rowIDs = page => page.$$eval(".task-card", cards => cards.map(card => card.dataset.task));
const refreshCount = page => page.evaluate(() => window.__ahaPerf.listRefreshRequests);
const scrollY = page => page.evaluate(() => Math.round(window.scrollY));
const statusText = (page, id) => page.locator(`.task-card[data-task="${id}"] .task-card-title-status`).innerText();
const emit = (page, type, data) => page.evaluate(([type, data]) => window.__emitGlobal(type, data), [type, data]);
const waitForRefresh = (page, before) => page.waitForFunction(count => window.__ahaPerf.listRefreshRequests > count, before, {timeout: 15000});

let browser;
const errors = [];
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 800}});
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "domcontentloaded"});
  await page.waitForSelector(".task-card", {timeout: 15000});
  assert.equal(await page.locator(".task-card").count(), 50, "the first page should render 50 rows");
  const initialOrder = await rowIDs(page);

  // A status change inside the open group only changes the group counts in the
  // popover, which is enough to replace the filter region; a move to completed
  // additionally removes the row from the default (open/error) view.
  await page.click('[data-task-filter="status"] summary');
  assert.equal(await page.evaluate(() => document.querySelector('[data-task-filter="status"]').open), true, "status popover did not open");
  await page.evaluate(() => window.scrollTo(0, 900));
  await page.waitForTimeout(100);
  const beforeScroll = await scrollY(page);
  const beforeRefresh = await refreshCount(page);
  await page.evaluate(() => window.__fixture.change("task-045", {status: "completed"}));
  await emit(page, "task_status", {task_id: "task-045", status: "completed"});
  await waitForRefresh(page, beforeRefresh);
  await page.waitForFunction(() => !document.querySelector('.task-card[data-task="task-045"]'), null, {timeout: 10000});

  const afterOrder = await rowIDs(page);
  assert.equal(afterOrder.length, 49, `a completed task should leave the default view (got ${afterOrder.length} rows)`);
  assert.deepEqual(afterOrder, initialOrder.filter(id => id !== "task-045"), "refresh changed row order or dropped other rows");
  assert.equal(await page.evaluate(() => document.querySelector('[data-task-filter="status"]').open), true, "refresh closed the filter popover");
  assert.equal(await page.evaluate(() => document.querySelector('[data-task-filter="status"] input[value="open"] + span small').textContent.trim()), "59", "open facet count did not update");
  assert.equal(await page.evaluate(() => document.querySelector('[data-task-filter="status"] input[value="done"] + span small').textContent.trim()), "1", "done facet count did not update");
  assert.equal(await scrollY(page), beforeScroll, "refresh moved the page scroll position");
  assert.equal(await statusText(page, "task-044"), "执行中", "an untouched row lost its content");

  // Clicking a kept row still opens the detail after the patch rebound controls.
  await page.locator('.task-card[data-task="task-044"]').click();
  await page.waitForSelector(".task-screen", {timeout: 15000});
  await page.click("#back-tasks");
  await page.waitForSelector(".task-card", {timeout: 15000});
  await page.waitForTimeout(400);

  // The in-row title editor must survive a refresh in the row it lives in.
  await page.evaluate(() => document.querySelector('.task-card[data-task="task-000"] [data-edit-task-title]').click());
  const editor = page.locator('.task-card[data-task="task-000"] input.task-title-input');
  await editor.waitFor({timeout: 5000});
  await editor.fill("测量中的标题");
  await editor.focus();
  await page.evaluate(() => window.scrollTo(0, 500));
  await page.waitForTimeout(100);
  const editorScroll = await scrollY(page);
  const editorRefresh = await refreshCount(page);
  await emit(page, "task_status", {task_id: "task-000", status: "active"});
  await page.evaluate(() => window.__fixture.change("task-000", {total_tokens: 42}));
  await waitForRefresh(page, editorRefresh);
  await page.waitForTimeout(300);
  assert.equal(await editor.count(), 1, "refresh removed the row being edited");
  assert.deepEqual(await editor.evaluate(input => ({
    focused: document.activeElement === input,
    value: input.value,
  })), {focused: true, value: "测量中的标题"}, "refresh stole focus or the typed value from the title editor");
  assert.equal(await scrollY(page), editorScroll, "refresh moved the page while the title editor was open");
  await editor.press("Escape");
  await page.waitForFunction(() => !document.querySelector("input.task-title-input"), null, {timeout: 5000});
  assert.equal(await page.locator('.task-card[data-task="task-000"] [data-task-title]').innerText(), "任务 1：列表实时刷新验证", "Escape did not restore the rendered title");

  // An open detail still refreshes the list data for the task band, but does
  // not replace the mounted list or touch the composer. The refreshed rows are
  // visible as soon as the detail closes.
  await page.locator('.task-card[data-task="task-001"]').click();
  await page.waitForSelector(".task-screen", {timeout: 15000});
  await page.waitForSelector("#message-form textarea", {timeout: 10000});
  const textarea = page.locator("#message-form textarea");
  await textarea.fill("详情输入不应被抢走");
  await textarea.focus();
  const detailRefresh = await refreshCount(page);
  await page.evaluate(() => {
    window.__fixture.change("task-002", {status: "completed"});
    window.__fixture.change("task-003", {status: "completed"});
  });
  await emit(page, "task_status", {task_id: "task-002", status: "completed"});
  await emit(page, "task_status", {task_id: "task-003", status: "completed"});
  await page.waitForTimeout(1500);
  assert.equal(await refreshCount(page), detailRefresh + 1, "the task band did not receive one coalesced list refresh while the detail was open");
  assert.equal(await page.locator('.task-band-card[data-task-band-open="task-002"]').count(), 0, "the band kept a completed task while the detail was open");
  assert.equal(await page.locator('.task-band-card[data-task-band-open="task-003"]').count(), 0, "the band kept a completed task while the detail was open");
  assert.deepEqual(await textarea.evaluate(node => ({focused: document.activeElement === node, value: node.value})), {focused: true, value: "详情输入不应被抢走"}, "the global SSE update disturbed the composer");
  assert.equal(await page.locator(".task-screen").count(), 1, "the detail was replaced by a list render");
  await page.click("#back-tasks");
  await page.waitForSelector(".task-card", {timeout: 15000});
  await page.waitForFunction(() => !document.querySelector('.task-card[data-task="task-002"]'), null, {timeout: 10000});
  assert.equal(await page.locator('.task-card[data-task="task-002"]').count(), 0, "the refreshed status change was not visible after closing the detail");
  assert.equal(await page.locator('.task-card[data-task="task-003"]').count(), 0, "the refreshed status change was not visible after closing the detail");

  assert.deepEqual(errors, [], "the page reported errors");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.ok(overflow <= 0, `the page overflows horizontally by ${overflow}px`);
  console.log("task_list_live_refresh.browser: ok");
} finally {
  await browser?.close();
  server.close();
}