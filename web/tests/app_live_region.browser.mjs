// Browser verification for the narrowed page-level live region.
//
// Checks against the real built Web bundle:
//   - #app has no aria-live; the persistent #app-status region starts empty and
//     an ordinary Task refresh (SSE event with an unchanged status) writes
//     nothing;
//   - a Task transitioning to completed announces once, and a duplicate refresh
//     of the same status does not announce again;
//   - a failed message send still reaches the live region (errors stay known);
//   - a new pending approval announces its count;
//   - closing the Task and handling a background list refresh adds no
//     announcement.
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

const TASK = {
  id: "t-live", code: "task-495", title: "Live region", status: "active",
  project_id: "project-1", workspace_id: "workspace-1",
  created_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z",
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
window.__status = "active";
window.__pending = 0;
window.__failSend = false;
window.__taskRequests = 0;
window.__listRequests = 0;
window.__sources = [];
const detail = () => ({
  task: {...TASK, status: window.__status, updated_at: new Date().toISOString()},
  latest_round: null, turns: [],
  agents: [{task_id: TASK.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  hardware: [], memory: {}, permission_requests: [], event_cursor: 0, server_time_ms: Date.now(),
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "test", version: "test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") {
    window.__listRequests++;
    return json({tasks: [{...TASK, status: window.__status}], has_more: false, counts: {status: {active: 1}, project: {"project-1": 1}, device: {local: 1}}});
  }
  if (path === "/api/v1/tasks/" + TASK.id && method === "GET") {
    window.__taskRequests++;
    return json(detail());
  }
  if (path === "/api/v1/tasks/" + TASK.id + "/agents/main/messages" && method === "POST") {
    if (window.__failSend) return json({message: "发送失败验证"}, 500);
    return json({queued: false, started: true, turn: {id: "turn-1"}});
  }
  if (path === "/api/v1/tasks/" + TASK.id + "/channel-routes") return json({ok: true, destinations: [], contacts: [], members: [], route: null});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: window.__pending});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/models") return json({models: []});
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
window.__emit = (prefix, type, payload) => {
  for (const source of window.__sources) {
    if (source.closed || !source.url.startsWith(prefix)) continue;
    for (const fn of source.listeners[type] || []) fn({data: JSON.stringify(payload)});
  }
};
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: TASK.id, task: TASK, agentID: "main"}));
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

const statusText = page => page.locator("#task-detail-status").innerText();
const announcements = page => page.evaluate(() => window.__announcements);
const appStatusText = page => page.locator("#app-status").textContent();
const emitTaskUpdate = (page, sequence) => page.evaluate(sequence => {
  window.__emit("/api/v1/tasks/t-live/events", "update", {type: "task_status", sequence, data: {}});
}, sequence);
const taskRequests = page => page.evaluate(() => window.__taskRequests);

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 800}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
  await page.goto(url, {waitUntil: "domcontentloaded"});
  await page.waitForSelector("#task-detail-status", {timeout: 20000});
  assert.equal(await statusText(page), "执行中");

  assert.equal(await page.locator("#app").getAttribute("aria-live"), null, "#app must not be a live region");
  assert.equal(await page.locator("#app-status").getAttribute("role"), "status");
  assert.equal(await page.locator("#app-status").getAttribute("aria-live"), "polite");
  assert.equal(await appStatusText(page), "", "opening and rendering a Task must not announce");

  // Observe every non-empty write to the persistent region. The region is
  // emptied before each write, so a repeated identical event still shows up.
  await page.evaluate(() => {
    window.__announcements = [];
    const node = document.querySelector("#app-status");
    new MutationObserver(() => {
      const text = node.textContent.trim();
      if (text) window.__announcements.push(text);
    }).observe(node, {childList: true, characterData: true, subtree: true});
  });

  // An ordinary SSE-triggered refresh with no status change stays silent.
  const beforeIdle = await taskRequests(page);
  await emitTaskUpdate(page, 1);
  await page.waitForFunction(before => window.__taskRequests > before, beforeIdle, {timeout: 15000});
  await page.waitForTimeout(300);
  assert.deepEqual(await announcements(page), [], "an unchanged refresh announced");
  assert.equal(await appStatusText(page), "");

  // Errors remain known: a failed send writes to the same region.
  await page.evaluate(() => { window.__failSend = true; });
  await page.fill("#message-form textarea", "触发错误");
  await page.evaluate(() => document.querySelector("#message-form").requestSubmit());
  await page.waitForFunction(() => document.querySelector("#app-status").textContent.includes("发送失败验证"), null, {timeout: 15000});
  assert.match(await appStatusText(page), /发送失败验证/);

  // A settled Task announces once, with its status label and code.
  const beforeCompleted = await taskRequests(page);
  await page.evaluate(() => { window.__status = "completed"; });
  await emitTaskUpdate(page, 2);
  await page.waitForFunction(before => window.__taskRequests > before, beforeCompleted, {timeout: 15000});
  await page.waitForFunction(() => document.querySelector("#task-detail-status").textContent.includes("已完成"), null, {timeout: 15000});
  await page.waitForFunction(() => document.querySelector("#app-status").textContent.includes("已完成"), null, {timeout: 15000});
  assert.match(await appStatusText(page), /已完成 task-495/);
  const afterCompleted = await announcements(page);
  assert.equal(afterCompleted.filter(text => text.includes("已完成")).length, 1);

  // The same completed detail arriving again (fallback refresh or a second
  // event) must not repeat the announcement.
  const beforeDuplicate = await taskRequests(page);
  await emitTaskUpdate(page, 3);
  await page.waitForFunction(before => window.__taskRequests > before, beforeDuplicate, {timeout: 15000});
  await page.waitForTimeout(300);
  assert.equal((await announcements(page)).filter(text => text.includes("已完成")).length, 1, "the settled status announced twice");

  // A new approval belongs in the live region too, and the initial count is
  // only a baseline.
  await page.evaluate(() => { window.__pending = 1; });
  await page.evaluate(() => window.__emit("/api/v1/events", "update", {type: "pet_approval", data: {}}));
  await page.waitForFunction(() => document.querySelector("#app-status").textContent.includes("新的审批"), null, {timeout: 15000});
  assert.match(await appStatusText(page), /有 1 项新的审批待处理/);

  // Back on the list, a background list refresh is still silent.
  const beforeList = await page.evaluate(() => window.__listRequests);
  await page.click("#back-tasks");
  await page.waitForSelector(".task-card", {timeout: 15000});
  const announcementsBeforeList = (await announcements(page)).length;
  await page.evaluate(() => window.__emit("/api/v1/events", "update", {type: "task_status", data: {}}));
  await page.waitForFunction(before => window.__listRequests > before, beforeList, {timeout: 15000});
  await page.waitForTimeout(300);
  assert.equal((await announcements(page)).length, announcementsBeforeList, "a list refresh announced");

  assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);
  console.log("app live region browser checks passed");
} finally {
  await browser?.close();
  server.close();
}