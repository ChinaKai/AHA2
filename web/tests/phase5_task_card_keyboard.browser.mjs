// Phase 5 High batch: the Task list card is a keyboard entry point.
//
// Checks against the real built Web bundle:
//   - the card body is a focusable button-like control with a task code + title
//     + status accessible name, and appears in the Tab order before its
//     edit/delete buttons;
//   - Enter and Space open the same Task; edit/delete keep their own behavior
//     and never navigate to the detail;
//   - the accessible name follows the page locale: en-US ends in the same
//     visible status badge text ("Waiting for input"), zh-CN is unchanged, and
//     a user-written Chinese title is not mistaken for an untranslated status;
//   - while the inline title editor is open the row body drops its button
//     semantics, so axe nested-interactive stays clean; cancelling restores
//     role/tabindex/aria-label and the card still opens with Enter;
//   - returning to the list restores focus to the card, through both the back
//     control and Escape; a dialog or a text control keeps its own Escape;
//   - when the row is removed by the list refresh that was parked while the
//     detail was open, focus moves to the task list container instead of BODY;
//   - 360/390/1280 keep the card height and horizontal overflow unchanged
//     across focus/open/back (focus must not reflow the row).
//
// Dependencies stay in .tools/web-validation; a machine without that directory
// can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE / AHA_AXE_SOURCE at
// a local install.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const axeSource = await readFile(process.env.AHA_AXE_SOURCE || resolve(tools, "node_modules/axe-core/axe.min.js"), "utf8");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const output = process.env.AHA_BROWSER_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const TASKS = [
  {id: "t-alpha", code: "task-101", title: "键盘打开的任务", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-beta", code: "task-102", title: "第二个任务", status: "active", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-done", code: "task-103", title: "已经完成的任务", status: "completed", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-gamma", code: "task-104", title: "Keyboard task", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1"},
];
const COUNTS = {status: {waiting_user: 2, active: 1, completed: 1}, project: {"project-1": 4}, device: {local: 4}};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASKS = ${JSON.stringify(TASKS)};
const COUNTS = ${JSON.stringify(COUNTS)};
// The list response is mutable so a test can make a row disappear while its
// detail is open, then let the parked list refresh run after closing.
window.__taskList = TASKS;
window.__eventSources = [];
const LANGUAGE = new URL(location.href).searchParams.get("lang") === "en-US" ? "en-US" : "zh-CN";
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: LANGUAGE}});
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
  const task = TASKS.find(item => path === \`/api/v1/tasks/\${item.id}\`);
  if (task && method === "GET") return json({task: {...task, collaboration_mode: "auto", max_agents: 3}, turns: [], agents: [{task_id: task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: [], memory: {}});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
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
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><script src="/axe.js"></script></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
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
    } else if (name === "/axe.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(axeSource);
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
const errors = [];
const card = (page, id) => page.locator(`.task-card[data-task="${id}"]`);
const cardOpen = (page, id) => page.locator(`.task-card[data-task="${id}"] [data-task-open]`);
const active = page => page.evaluate(() => {
  const element = document.activeElement;
  if (!(element instanceof HTMLElement)) return null;
  return {
    tag: element.tagName,
    cls: element.className,
    task: element.closest("[data-task]")?.dataset.task || "",
    open: element.matches("[data-task-open]"),
    edit: element.matches("[data-edit-task-title]"),
    del: element.matches("[data-delete-task]"),
    label: element.getAttribute("aria-label") || "",
  };
});
const focusCardByTab = async (page, id) => {
  await page.evaluate(() => (document.activeElement instanceof HTMLElement ? document.activeElement.blur() : null));
  for (let index = 0; index < 60; index++) {
    await page.keyboard.press("Tab");
    const step = await active(page);
    if (step?.task === id && step.open) return true;
  }
  return false;
};
const tabOrder = async page => {
  await page.evaluate(() => (document.activeElement instanceof HTMLElement ? document.activeElement.blur() : null));
  const steps = [];
  for (let index = 0; index < 60; index++) {
    await page.keyboard.press("Tab");
    const step = await active(page);
    steps.push(step);
    if (step?.task === "t-alpha" && step.del) break;
  }
  return steps;
};

try {
  browser = await chromium.launch(launchOptions);
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  page.on("pageerror", error => { errors.push(String(error)); console.error("pageerror:", error.stack || String(error)); });
  const dialogs = [];
  page.on("dialog", dialog => {
    dialogs.push({type: dialog.type(), message: dialog.message()});
    void dialog.dismiss();
  });
  await page.goto(url);
  await page.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});

  // Semantics and accessible name: code + title + status, one control per card.
  const semantics = await cardOpen(page, "t-alpha").evaluate(element => ({
    tag: element.tagName,
    role: element.getAttribute("role"),
    tabindex: element.getAttribute("tabindex"),
    label: element.getAttribute("aria-label") || "",
  }));
  assert.equal(semantics.role, "button", "the card body is not exposed as a button");
  assert.equal(semantics.tabindex, "0", "the card body is not in the Tab order");
  for (const part of ["task-101", "键盘打开的任务", "等待输入"]) {
    assert.ok(semantics.label.includes(part), `card accessible name is missing ${part}: ${semantics.label}`);
  }
  assert.equal(await card(page, "t-alpha").evaluate(element => element.getAttribute("tabindex")), null, "the article itself should not be a duplicate tab stop");

  // zh-CN keeps the visible badge text in the accessible name, word for word.
  const zhBadge = await card(page, "t-gamma").locator(".task-card-title-status").innerText();
  const zhLabel = await cardOpen(page, "t-gamma").getAttribute("aria-label");
  assert.equal(zhBadge.trim(), "等待输入", "the zh-CN status badge changed");
  assert.equal(zhLabel, "task-104 Keyboard task 等待输入", "the zh-CN accessible name does not end in the visible badge text");

  // Tab reaches the card body first, then its edit/delete controls independently.
  const steps = await tabOrder(page);
  const cardIndex = steps.findIndex(step => step?.task === "t-alpha" && step.open);
  const editIndex = steps.findIndex(step => step?.task === "t-alpha" && step.edit);
  const deleteIndex = steps.findIndex(step => step?.task === "t-alpha" && step.del);
  assert.ok(cardIndex >= 0, "Tab never reached the Task card body");
  assert.ok(editIndex > cardIndex, "the edit button came before the card body in Tab order");
  assert.ok(deleteIndex > editIndex, "the delete button did not follow the edit button");

  // Enter opens the same Task.
  await cardOpen(page, "t-alpha").focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector(".task-screen", {timeout: 15000});
  assert.match(await page.locator(".task-title-text").innerText(), /键盘打开的任务/, "Enter opened the wrong Task");

  // Returning restores focus to the same card.
  await page.click("#back-tasks");
  await page.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});
  const returned = await active(page);
  assert.deepEqual({task: returned?.task, open: returned?.open}, {task: "t-alpha", open: true}, "focus did not return to the Task card");

  // Escape is the layer's other dismiss path and restores the same focus.
  await cardOpen(page, "t-alpha").focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector(".task-screen", {timeout: 15000});
  await page.keyboard.press("Escape");
  await page.waitForSelector(".task-screen", {state: "detached", timeout: 15000});
  const escaped = await active(page);
  assert.deepEqual({task: escaped?.task, open: escaped?.open}, {task: "t-alpha", open: true}, "Escape did not return focus to the Task card");

  // A text control keeps Escape for itself: dismissing things in the composer
  // must not knock the keyboard user out of the Task.
  await cardOpen(page, "t-alpha").focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector(".task-screen", {timeout: 15000});
  await page.locator("#message-form textarea").focus();
  await page.keyboard.press("Escape");
  assert.equal(await page.locator(".task-screen").count(), 1, "Escape in the composer closed the Task detail");

  // Dialogs above the detail keep their own Escape: closing Agent config must
  // leave the Task open underneath.
  await page.locator(".composer-agent-config").click();
  await page.waitForSelector("#agent-config-dialog[open]", {timeout: 5000});
  await page.keyboard.press("Escape");
  await page.waitForSelector("#agent-config-dialog", {state: "hidden", timeout: 5000});
  assert.equal(await page.locator(".task-screen").count(), 1, "Escape in the Agent config dialog also closed the Task detail");
  await page.click("#back-tasks");
  await page.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});

  // Space opens the same Task and does not scroll the page.
  const scrollBefore = await page.evaluate(() => window.scrollY);
  await cardOpen(page, "t-alpha").focus();
  await page.keyboard.press(" ");
  await page.waitForSelector(".task-screen", {timeout: 15000});
  assert.match(await page.locator(".task-title-text").innerText(), /键盘打开的任务/, "Space opened the wrong Task");
  assert.equal(await page.evaluate(() => window.scrollY), scrollBefore, "Space scrolled the list instead of activating the card");
  await page.click("#back-tasks");
  await page.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});

  // Edit remains its own control: Enter starts the title editor, no navigation.
  const cardBody = (target, id) => target.locator(`.task-card[data-task="${id}"] .task-card-main`);
  const bodySemantics = element => ({
    role: element.getAttribute("role"),
    tabindex: element.getAttribute("tabindex"),
    label: element.getAttribute("aria-label"),
  });
  await page.locator('.task-card[data-task="t-alpha"] [data-edit-task-title]').focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector('.task-card[data-task="t-alpha"] input.task-title-input', {timeout: 5000});
  assert.equal(await page.locator(".task-screen").count(), 0, "Enter on the edit button opened the Task");
  // While the editor is open the row body must not claim button semantics:
  // an input inside role="button" is axe nested-interactive.
  assert.deepEqual(
    await cardBody(page, "t-alpha").evaluate(bodySemantics),
    {role: null, tabindex: null, label: null},
    "the editing card body still exposes button semantics",
  );
  const editingAxe = await page.evaluate(async () => {
    const result = await window.axe.run(document.querySelector('.task-card[data-task="t-alpha"]'), {
      runOnly: {type: "rule", values: ["nested-interactive"]},
    });
    return result.violations.map(item => ({id: item.id, nodes: item.nodes.map(node => node.html)}));
  });
  assert.deepEqual(editingAxe, [], "axe nested-interactive fired while the inline title editor was open");
  await page.keyboard.press("Escape");
  await page.waitForSelector('.task-card[data-task="t-alpha"] input.task-title-input', {state: "detached", timeout: 5000});
  const restored = await cardBody(page, "t-alpha").evaluate(bodySemantics);
  assert.equal(restored.role, "button", "cancelling the edit did not restore the card role");
  assert.equal(restored.tabindex, "0", "cancelling the edit did not restore the card Tab stop");
  assert.ok(restored.label?.includes("task-101"), `cancelling the edit did not restore the card accessible name: ${restored.label}`);
  // Saving takes the same row patch path. The fixture echoes the old title, so
  // the row node survives the refresh and the restore must hold there too.
  await page.locator('.task-card[data-task="t-alpha"] [data-edit-task-title]').focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector('.task-card[data-task="t-alpha"] input.task-title-input', {timeout: 5000});
  await page.fill('.task-card[data-task="t-alpha"] input.task-title-input', "重命名的任务");
  await page.keyboard.press("Enter");
  await page.waitForSelector('.task-card[data-task="t-alpha"] input.task-title-input', {state: "detached", timeout: 5000});
  const saved = await cardBody(page, "t-alpha").evaluate(bodySemantics);
  assert.equal(saved.role, "button", "saving the edit did not restore the card role");
  assert.equal(saved.tabindex, "0", "saving the edit did not restore the card Tab stop");
  // The restored card still opens from the keyboard.
  await cardOpen(page, "t-alpha").focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector(".task-screen", {timeout: 15000});
  assert.match(await page.locator(".task-title-text").innerText(), /键盘打开的任务/, "the restored card opened the wrong Task");
  await page.click("#back-tasks");
  await page.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});

  // Delete stays its own control: Enter asks for confirmation, no navigation.
  await page.locator('.task-card[data-task="t-alpha"] [data-delete-task]').focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(150);
  const confirmDialog = dialogs.find(entry => entry.type === "confirm");
  assert.ok(confirmDialog, "Enter on the delete button did not ask for confirmation");
  assert.match(confirmDialog.message, /删除该任务/, "the delete confirmation does not describe the action");
  assert.equal(await page.locator(".task-screen").count(), 0, "Enter on the delete button opened the Task");

  // If the row that opened the detail is gone once the parked list refresh
  // catches up, focus must land on the list container — not on BODY.
  await cardOpen(page, "t-alpha").focus();
  await page.keyboard.press("Enter");
  await page.waitForSelector(".task-screen", {timeout: 15000});
  await page.evaluate(() => {
    window.__taskList = window.__taskList.filter(task => task.id !== "t-alpha");
    window.__emitTaskEvent("task_updated", {task_id: "t-alpha"});
  });
  await page.click("#back-tasks");
  await page.waitForFunction(() => !document.querySelector('.task-card[data-task="t-alpha"]'), null, {timeout: 15000});
  const fallback = await active(page);
  assert.deepEqual({tag: fallback?.tag, cls: fallback?.cls}, {tag: "DIV", cls: "task-list"}, "a removed row dropped focus instead of moving it to the task list");

  // en-US: the accessible name uses the same translated status text as the
  // visible badge; a user-written Chinese title stays untouched.
  const enPage = await browser.newPage({viewport: {width: 1280, height: 900}});
  enPage.on("pageerror", error => { errors.push(String(error)); console.error("pageerror:", String(error)); });
  await enPage.goto(`${url}/?lang=en-US`);
  await enPage.waitForSelector('.task-card[data-task="t-gamma"]', {timeout: 15000});
  assert.equal(await enPage.evaluate(() => document.documentElement.lang), "en-US", "the fixture did not switch to en-US");
  const enBadge = (await card(enPage, "t-gamma").locator(".task-card-title-status").innerText()).trim();
  const enLabel = await cardOpen(enPage, "t-gamma").getAttribute("aria-label");
  assert.equal(enBadge, "Waiting for input", "the en-US status badge was not translated");
  assert.equal(enLabel, "task-104 Keyboard task Waiting for input", `en-US accessible name is not fully translated: ${enLabel}`);
  assert.doesNotMatch(enLabel, /\u3400-\u9fff/, `en-US accessible name still contains Han characters: ${enLabel}`);
  const enChineseTitleLabel = await cardOpen(enPage, "t-alpha").getAttribute("aria-label");
  assert.equal(enChineseTitleLabel, "task-101 键盘打开的任务 Waiting for input", `en-US name mangled a user-written title: ${enChineseTitleLabel}`);
  assert.ok(enChineseTitleLabel.includes("键盘打开的任务"), "the user-written Chinese title was dropped from the en-US name");
  // The translated card remains the keyboard entry point.
  await cardOpen(enPage, "t-gamma").focus();
  await enPage.keyboard.press("Enter");
  await enPage.waitForSelector(".task-screen", {timeout: 15000});
  assert.match(await enPage.locator(".task-title-text").innerText(), /Keyboard task/, "en-US Enter opened the wrong Task");
  await enPage.close();
  console.log(`en-US card label: ${enLabel}`);
  console.log(`editing axe nested-interactive violations: ${editingAxe.length}`);

  // Focus must not reflow the card at any of the three required widths.
  for (const width of [360, 390, 1280]) {
    const shapePage = await browser.newPage({viewport: {width, height: 900}});
    shapePage.on("pageerror", error => errors.push(String(error)));
    await shapePage.goto(url);
    await shapePage.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});
    const before = await card(shapePage, "t-alpha").evaluate(element => ({
      height: element.getBoundingClientRect().height,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    }));
    assert.equal(await focusCardByTab(shapePage, "t-alpha"), true, `${width}px: Tab did not reach the card body`);
    const focused = await card(shapePage, "t-alpha").evaluate(element => ({
      height: element.getBoundingClientRect().height,
      outline: getComputedStyle(element.matches("[data-task-open]") ? element : element.querySelector("[data-task-open]")).outlineStyle,
    }));
    await card(shapePage, "t-alpha").click();
    await shapePage.waitForSelector(".task-screen", {timeout: 15000});
    await shapePage.click("#back-tasks");
    await shapePage.waitForSelector('.task-card[data-task="t-alpha"]', {timeout: 15000});
    const after = await card(shapePage, "t-alpha").evaluate(element => ({
      height: element.getBoundingClientRect().height,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      focused: document.activeElement?.matches("[data-task-open]") === true,
    }));
    assert.ok(Math.abs(focused.height - before.height) < 0.5, `${width}px: focusing the card changed its height (${before.height} -> ${focused.height})`);
    assert.ok(Math.abs(after.height - before.height) < 0.5, `${width}px: opening and returning changed the card height (${before.height} -> ${after.height})`);
    assert.equal(focused.outline, "solid", `${width}px: the focused card has no visible outline`);
    assert.ok(before.overflow <= 0 && after.overflow <= 0, `${width}px: the task list overflows horizontally`);
    assert.equal(after.focused, true, `${width}px: returning from the detail lost card focus`);
    await shapePage.screenshot({path: resolve(output, `phase5-task-card-${width}.png`)});
    await shapePage.close();
  }

  assert.deepEqual(errors, [], "the task list reported page errors");
  console.log("task card keyboard: Tab/Enter/Space, zh-CN/en-US names, edit-mode axe, edit/delete isolation and layout verified at 360/390/1280");
} finally {
  await browser?.close();
  server.close();
}