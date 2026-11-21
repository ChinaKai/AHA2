// Task detail three-column shell: icon rail, task band, detail.
//
// Runs against the real web/build bundle with a stubbed API. It covers the
// acceptance points that can only be observed in a browser: the fixed icon
// sidebar, the band's default scope and card contents, live band updates while
// the detail stays open, draft isolation across Task/agent switches (including
// in-flight uploads and failed sends), collapse persistence across a reload,
// the narrow-window rule that makes the band give width back to the detail,
// Escape closing the overlay, the empty state, escaped titles and the phone
// layout.
//
// Dependencies stay in .tools/web-validation by default; point
// AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE at a local install otherwise.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
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
const output = process.env.AHA_BROWSER_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(output, {recursive: true});

const now = "2026-11-21T09:00:00Z";
const TASKS = [
  {id: "t-alpha", code: "task-101", title: "正在推进的任务", status: "active", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 1200, created_at: now},
  {id: "t-beta", code: "task-102", title: "等待确认的任务", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 800, created_at: now},
  {id: "t-done", code: "task-103", title: "已经完成的任务", status: "completed", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 0, created_at: now},
  {id: "t-cancelled", code: "task-104", title: "已经取消的任务", status: "cancelled", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 0, created_at: now},
  {id: "t-remote", code: "task-105", title: "其他设备的任务", status: "failed", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 0, created_at: now, read_only: true, owner_device_id: "device-b"},
  {id: "t-xss", code: "task-106", title: '<img src=x onerror="window.__xss=1">危险标题', status: "completed", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 0, created_at: now},
];
const COUNTS = {
  status: {active: 1, waiting_user: 1, completed: 2, cancelled: 1, failed: 1},
  project: {"project-1": 6},
  device: {local: 5, "remote:device-b": 1},
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASKS = ${JSON.stringify(TASKS)};
const COUNTS = ${JSON.stringify(COUNTS)};
const MESSAGES = [];
window.__messages = MESSAGES;
window.__eventSources = [];
window.__allTasks = TASKS.map(task => ({...task}));
window.__taskList = window.__allTasks.map(task => ({...task}));
window.__uploadSeq = 0;
window.__uploadDelay = 0;
window.__deleteDelay = 0;
window.__messageDelay = 0;
const sleep = delay => new Promise(resolve => setTimeout(resolve, delay));
const findTask = id => window.__allTasks.find(item => item.id === id);
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: window.__taskList, has_more: false, counts: COUNTS});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false}]});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready"}]});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/providers") return json({providers: []});
  if (path === "/api/v1/env-groups") return json({env_groups: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/mcp-servers") return json({servers: []});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  const task = findTask(path.split("/")[4]);
  if (task && path === \`/api/v1/tasks/\${task.id}\` && method === "GET") return json({task: {...task, collaboration_mode: "auto", max_agents: 3}, turns: [], agents: [{task_id: task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}, {task_id: task.id, agent_id: "helper", role: "sub", status: "idle", title: "Helper", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: [], memory: {}});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (method === "POST" && /\\/attachments$/.test(path)) {
    const taskID = path.split("/")[4];
    await sleep(window.__uploadDelay);
    const sequence = ++window.__uploadSeq;
    return json({attachment: {id: \`att-\${sequence}\`, task_id: taskID, name: \`upload-\${sequence}.png\`, media_type: "image/png", size: 4, sha256: "test", created_at: new Date().toISOString()}});
  }
  if (method === "DELETE" && /\\/attachments\\//.test(path)) {
    await sleep(window.__deleteDelay);
    return json({ok: true});
  }
  if (method === "POST" && /\\/messages$/.test(path)) {
    const body = JSON.parse(init.body || "{}");
    MESSAGES.push({path, body});
    if (String(body.content || "").includes("fail-send")) {
      await sleep(window.__messageDelay);
      return json({message: "发送失败测试"}, 500);
    }
    return json({ok: true, started: true});
  }
  const segments = path.split("/");
  if (segments.length === 6 && segments[3] === "tasks" && segments[5] === "channel-routes") return json({destinations: [], contacts: [], members: []});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class {
  constructor() { this.listeners = {}; window.__eventSources.push(this); }
  close() {}
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
};
window.__emitTaskEvent = (type, data) => window.__eventSources.forEach(source => (source.listeners.update || []).forEach(fn => fn({data: JSON.stringify({type, data})})));
if (!sessionStorage.getItem("aha2.navigation")) sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;
const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><div id="app-status" role="status" aria-live="polite"></div><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else {
      if (!/^\/(?:[a-z0-9_.-]+\/)*[a-z0-9_.-]+\.(?:js|css|svg|png|ico)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
const errors = [];
const openDetail = async (page, id = "t-alpha") => {
  await page.waitForSelector(`.task-card[data-task="${id}"]`, {timeout: 15000});
  await page.locator(`.task-card[data-task="${id}"] [data-task-open]`).click();
  await page.waitForSelector(".app-shell.task-shell", {timeout: 15000});
  await page.waitForSelector(`.task-band-card[data-task-band-open="${id}"]`, {timeout: 15000});
};
const composerValue = page => page.locator("#message-form textarea").inputValue();
const switchAgent = async (page, id) => {
  // A real user focuses the select before changing it; the focused composer
  // otherwise defers render() until blur.
  await page.locator("#composer-agent").focus();
  await page.selectOption("#composer-agent", id);
  await page.waitForFunction(expected => document.querySelector("#composer-agent")?.value === expected, id, {timeout: 15000});
};
const uploadFile = (page, name) => page.locator("#message-attachment-input").setInputFiles({
  name,
  mimeType: "image/png",
  buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47]),
});
const switchBandTask = async (page, id) => {
  await page.locator(`.task-band-card[data-task-band-open="${id}"]`).click();
  await page.waitForFunction(expected => {
    const heading = document.querySelector(".task-head .task-code");
    return heading && heading.textContent === expected;
  }, TASKS.find(task => task.id === id).code, {timeout: 15000});
};
const widths = page => page.evaluate(() => {
  const rect = selector => {
    const element = document.querySelector(selector);
    return element ? element.getBoundingClientRect().width : 0;
  };
  return {
    sidebar: rect(".app-shell.task-shell > .sidebar"),
    band: rect(".task-band"),
    workspace: rect(".app-shell.task-shell > .workspace"),
    viewport: window.innerWidth,
  };
});

try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});

  // The default list and the band share the same scope: completed, cancelled
  // and the other device's Task are all out of the initial result.
  assert.equal(await page.locator(".task-card").count(), 2, "the default task list scope changed");

  // The band has its own entry point but is not rendered on the list page.
  assert.equal(await page.locator(".task-band").count(), 0, "the task band leaked onto the task list");
  const listNavLabels = await page.locator(".sidebar nav button").evaluateAll(buttons => buttons.map(button => button.getAttribute("aria-label")));
  const listNavOrder = await page.locator(".sidebar nav button").evaluateAll(buttons => buttons.map(button => button.dataset.view));
  await openDetail(page);

  // A1: icon-only sidebar, original logo, same nav order and accessible names.
  const layout = await widths(page);
  assert.ok(Math.abs(layout.sidebar - 64) <= 1, `sidebar is ${layout.sidebar}px, expected 64`);
  assert.equal(await page.locator(".sidebar .brand-mark img").getAttribute("src"), "/assets/aha-logo.svg");
  assert.equal(await page.locator(".sidebar .brand-lockup > div").isVisible(), false, "the sidebar still shows its text lockup");
  assert.deepEqual(await page.locator(".sidebar nav button").evaluateAll(buttons => buttons.map(button => button.dataset.view)), listNavOrder);
  assert.deepEqual(await page.locator(".sidebar nav button").evaluateAll(buttons => buttons.map(button => button.getAttribute("aria-label"))), listNavLabels);
  assert.equal(await page.locator("#sidebar-resizer").count(), 0, "the detail still exposes the sidebar resizer");
  assert.equal(await page.locator(".command-palette-trigger > span").isVisible(), false);
  assert.equal(await page.locator("#advanced-settings").isVisible(), true, "settings disappeared from the icon rail");
  assert.equal(await page.locator("#logout").isVisible(), true, "logout disappeared from the icon rail");

  // A2/A3: band sits between the rail and detail, default scope only.
  assert.ok(Math.abs(layout.band - 272) <= 2, `band is ${layout.band}px, expected 272`);
  assert.ok(layout.workspace > 0 && layout.workspace + layout.sidebar + layout.band <= layout.viewport + 2);
  assert.equal(await page.locator(".task-band.collapsed").count(), 0, "the band must default to expanded");
  const bandIDs = await page.locator(".task-band-card").evaluateAll(cards => cards.map(card => card.dataset.taskBandOpen));
  assert.deepEqual(bandIDs, ["t-alpha", "t-beta"], `band cards: ${bandIDs}`);
  assert.equal(await page.locator('.task-band-card[data-task-band-open="t-alpha"]').getAttribute("aria-current"), "true");
  assert.match(await page.locator(".task-band-head").innerText(), /未完成与异常 · 本机 · 全部项目/);
  const firstCard = await page.locator('.task-band-card[data-task-band-open="t-beta"]').innerText();
  assert.match(firstCard, /task-102/);
  assert.match(firstCard, /等待确认的任务/);
  assert.match(firstCard, /Project One/);
  assert.match(firstCard, /WS One/);
  assert.match(firstCard, /等待确认/);

  // A5: drafts survive switching and sending only clears the current Task.
  await page.locator("#message-form textarea").fill("alpha draft");
  await switchBandTask(page, "t-beta");
  assert.equal(await composerValue(page), "", "the second Task inherited the first Task's draft");
  await page.locator("#message-form textarea").fill("beta draft");
  await page.locator("#message-send").click();
  await page.waitForFunction(() => document.querySelector("#message-form textarea")?.value === "");
  await switchBandTask(page, "t-alpha");
  assert.equal(await composerValue(page), "alpha draft", "switching away lost the first Task's draft");
  await switchBandTask(page, "t-beta");
  assert.equal(await composerValue(page), "", "sending did not clear the current Task's draft");
  await switchBandTask(page, "t-alpha");
  assert.equal(await composerValue(page), "alpha draft", "the send cleared a different Task's draft");

  // M2: an upload that is still in flight when the owner switches Task writes
  // back to the Task that started it, and never borrows the visible composer's
  // text.
  await page.evaluate(() => { window.__uploadDelay = 400; });
  await page.locator("#message-form textarea").fill("alpha upload text");
  await uploadFile(page, "alpha.png");
  await page.waitForTimeout(60);
  await switchBandTask(page, "t-beta");
  assert.equal(await composerValue(page), "", "the second Task inherited the uploading Task's draft");
  await page.waitForTimeout(700);
  await switchBandTask(page, "t-alpha");
  assert.equal(await composerValue(page), "alpha upload text", "switching Task during an upload lost the text");
  assert.equal(await page.locator("[data-remove-attachment]").count(), 1, "the upload did not land on its own Task");
  await switchBandTask(page, "t-beta");
  assert.equal(await page.locator("[data-remove-attachment]").count(), 0, "the upload leaked into another Task");

  // Same Task, different agent: the upload belongs to the agent that picked it.
  await switchBandTask(page, "t-alpha");
  await page.waitForFunction(() => document.querySelector('#composer-agent option[value="helper"]'), {timeout: 15000});
  await page.locator("#message-form textarea").fill("main agent text");
  await uploadFile(page, "main-agent.png");
  await page.waitForTimeout(60);
  await switchAgent(page, "helper");
  await page.waitForFunction(() => document.querySelector("#message-form textarea")?.value === "");
  assert.equal(await composerValue(page), "", "the helper agent inherited the main agent's draft");
  await page.waitForTimeout(700);
  await switchAgent(page, "main");
  await page.waitForFunction(() => document.querySelector("#message-form textarea")?.value === "main agent text");
  assert.equal(await composerValue(page), "main agent text", "switching agent during an upload lost the text");
  assert.equal(await page.locator("[data-remove-attachment]").count(), 2, "the upload did not land on the main agent");
  await switchAgent(page, "helper");
  await page.waitForFunction(() => document.querySelector("#message-form textarea")?.value === "");
  assert.equal(await page.locator("[data-remove-attachment]").count(), 0, "the upload leaked into the helper agent");
  await switchAgent(page, "main");
  await page.waitForFunction(() => document.querySelector("#message-form textarea")?.value === "main agent text");
  await page.evaluate(() => { window.__uploadDelay = 0; });

  // Removing an attachment also belongs to the Task/agent that owned the
  // button, even when the request settles after a switch.
  await page.evaluate(() => { window.__deleteDelay = 400; });
  await page.locator("[data-remove-attachment]").first().click();
  await switchBandTask(page, "t-beta");
  await page.waitForTimeout(700);
  await switchBandTask(page, "t-alpha");
  assert.equal(await page.locator("[data-remove-attachment]").count(), 1, "the delete did not apply to its own Task");
  await page.evaluate(() => { window.__deleteDelay = 0; });

  // M3: a failed send restores its Task even when the owner has already moved
  // to another Task; the visible composer is only restored when it is the same.
  await page.evaluate(() => { window.__messageDelay = 400; });
  await page.locator("#message-form textarea").fill("fail-send draft");
  await page.locator("#message-send").click();
  await switchBandTask(page, "t-beta");
  await page.waitForTimeout(700);
  await switchBandTask(page, "t-alpha");
  assert.equal(await composerValue(page), "fail-send draft", "a failed send did not restore the Task it belonged to");
  await page.locator("#message-form textarea").fill("");
  await page.evaluate(() => { window.__messageDelay = 0; });

  // Keep a known draft for the reload check below.
  await page.locator("#message-form textarea").fill("reload draft");
  await page.waitForTimeout(250);

  // A6/A4: narrow windows collapse the band to the rail without touching the
  // saved preference; widening restores the expanded preference.
  await page.setViewportSize({width: 1000, height: 800});
  await page.waitForFunction(() => {
    const band = document.querySelector(".task-band");
    return band && band.classList.contains("collapsed");
  }, {timeout: 10000});
  const narrow = await widths(page);
  assert.ok(narrow.band <= 50, `narrow band is ${narrow.band}px, expected the rail`);
  assert.ok(narrow.workspace >= narrow.viewport - narrow.sidebar - narrow.band - 4, "the detail did not reclaim the band width");
  assert.ok(narrow.workspace >= 850, `detail is only ${narrow.workspace}px at 1000px viewport`);
  // The rail can still open the band, but as an overlay that does not take
  // width from the detail.
  await page.locator(".task-band-rail[data-task-band-toggle]").click();
  await page.waitForSelector(".task-band.narrow-expanded");
  const overlapped = await widths(page);
  assert.ok(overlapped.workspace >= narrow.workspace - 2, "opening the band in a narrow window squeezed the detail");
  // S1: Escape closes the overlay, not the whole Task.
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector(".task-band")?.classList.contains("narrow-expanded"));
  assert.equal(await page.locator(".app-shell.task-shell").count(), 1, "Escape on the band overlay closed the Task detail");
  assert.equal(await page.locator(".task-head .task-code").textContent(), "task-101");
  await page.setViewportSize({width: 1280, height: 900});
  await page.waitForFunction(() => !document.querySelector(".task-band")?.classList.contains("collapsed"), {timeout: 10000});
  assert.equal(await page.evaluate(() => localStorage.getItem("aha2.task-band-collapsed")), null, "auto-collapse overwrote the saved preference");

  // A4: manual collapse persists across a reload and can be reopened.
  await page.locator(".task-band-head [data-task-band-toggle]").click();
  await page.waitForSelector(".task-band.collapsed");
  assert.equal(await page.evaluate(() => localStorage.getItem("aha2.task-band-collapsed")), "collapsed");
  await page.reload({waitUntil: "networkidle"});
  await page.waitForSelector(".app-shell.task-shell", {timeout: 15000});
  assert.equal(await page.locator(".task-band.collapsed").count(), 1, "the collapsed preference was lost after reload");
  assert.equal(await composerValue(page), "reload draft", "the draft did not survive a reload");
  await page.locator(".task-band-rail[data-task-band-toggle]").click();
  await page.waitForSelector(".task-band:not(.collapsed)");
  assert.equal(await page.evaluate(() => localStorage.getItem("aha2.task-band-collapsed")), "expanded");

  // Keyboard reachability: the card is a real button and Enter opens it.
  await page.locator(".task-band-head [data-task-band-toggle]").click();
  await page.waitForSelector(".task-band.collapsed");
  await page.setViewportSize({width: 1280, height: 900});
  await page.locator(".task-band-rail[data-task-band-toggle]").click();
  await page.waitForSelector(".task-band:not(.collapsed)");
  const betaCard = page.locator('.task-band-card[data-task-band-open="t-beta"]');
  assert.equal(await betaCard.evaluate(element => element.tagName), "BUTTON");
  await betaCard.focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector(".task-head .task-code")?.textContent === "task-102");

  // M1: a global task event updates the band without leaving the detail, and
  // switching Tasks performs the same refresh. Completed Tasks leave the band.
  await page.evaluate(() => {
    window.__taskList = window.__taskList.map(task => task.id === "t-alpha" ? {...task, status: "completed"} : task);
    window.__emitTaskEvent("task_updated", {task_id: "t-alpha"});
  });
  await page.waitForFunction(() => !document.querySelector('[data-task-band-open="t-alpha"]'), {timeout: 15000});
  assert.equal(await page.locator(".task-band-card").count(), 1, "the band kept a Task that completed while the detail was open");
  assert.equal(await page.locator(".task-band-head .task-band-count").textContent(), "1");
  assert.equal(await page.locator(".app-shell.task-shell").count(), 1, "the detail was replaced by the band refresh");

  // A new Task and an escaped title: markup in a title must never become DOM.
  await page.evaluate(() => {
    window.__taskList = [
      ...window.__taskList.map(task => task.id === "t-xss" ? {...task, status: "active"} : task),
      {id: "t-new", code: "task-107", title: "刚新建的任务", status: "active", project_id: "project-1", workspace_id: "workspace-1", total_tokens: 0, created_at: new Date().toISOString()},
    ];
    window.__emitTaskEvent("task_created", {task_id: "t-new"});
  });
  await page.waitForFunction(() => document.querySelectorAll(".task-band-card").length === 3, {timeout: 15000});
  const escaped = await page.locator('[data-task-band-open="t-xss"] .task-band-card-title').textContent();
  assert.equal(escaped, '<img src=x onerror="window.__xss=1">危险标题');
  assert.equal(await page.locator(".task-band-card img").count(), 0, "a Task title injected markup into the band");
  assert.equal(await page.evaluate(() => window.__xss), undefined);

  // Empty state, not a blank panel.
  await page.evaluate(() => {
    window.__taskList = window.__taskList.filter(task => task.status === "completed" || task.status === "cancelled" || task.read_only);
    window.__emitTaskEvent("task_deleted", {task_id: "t-new"});
  });
  await page.waitForSelector(".task-band-empty", {timeout: 15000});
  assert.match(await page.locator(".task-band-empty").innerText(), /没有需要处理的任务/);
  assert.equal(await page.locator(".task-band-card").count(), 0);

  // ≤760px: the existing mobile layout hides the rail and band, leaving the
  // detail full width with no horizontal overflow.
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForTimeout(100);
  assert.equal(await page.locator(".task-band").isVisible(), false, "the band is still visible on a phone");
  assert.equal(await page.locator("#message-form").isVisible(), true, "the detail disappeared on a phone");
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "the detail overflows on a phone");

  await page.setViewportSize({width: 1280, height: 900});
  await page.screenshot({path: resolve(output, "task-detail-band.png")});
  assert.deepEqual(errors, [], `page errors: ${errors.join("; ")}`);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
