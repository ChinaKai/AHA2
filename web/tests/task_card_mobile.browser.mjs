// Optional browser verification for the mobile Task card layout. Dependencies
// and browser files stay in .tools/web-validation by default; a machine without
// that directory can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE at a
// local Playwright install (see docs/web-conversation-virtual-list.md).
//
// Checks, against the real built Web bundle:
//   390x844 and 360x800: the status badge sits above edit/delete, the title got
//     the released width, one status is announced, nothing overflows, and the
//     row buttons still open the title editor.
//   1280x900: the title still carries the status inline (desktop unchanged).
//   completed / read-only / archived rows: exactly one state badge.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
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
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const TASKS = [
  {id: "t-active", code: "task-039", title: "移动端任务卡片标题很长很长需要验证释放出来的宽度可以显示更多文字", status: "active", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-waiting", code: "task-040", title: "等待输入的任务", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-blocked", code: "task-041", title: "受阻任务", status: "blocked", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-done", code: "task-042", title: "已完成任务", status: "completed", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-readonly", code: "task-043", title: "只读镜像任务", status: "active", project_id: "project-1", workspace_id: "workspace-1", read_only: true, owner_device_id: "device-b"},
  {id: "t-archived", code: "task-044", title: "归档只读任务", status: "active", project_id: "project-1", workspace_id: "workspace-1", channel_retired: true},
];
const COUNTS = {
  status: {active: 3, waiting_user: 1, blocked: 1, completed: 1},
  project: {"project-1": 6},
  device: {local: 5, "remote:device-b": 1},
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASKS = ${JSON.stringify(TASKS)};
const COUNTS = ${JSON.stringify(COUNTS)};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: TASKS, has_more: false, counts: COUNTS});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false}]});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready"}]});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
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

const layout = (page, id) => page.locator(`.task-card[data-task="${id}"]`).evaluate(card => {
  const box = node => {
    const rect = node.getBoundingClientRect();
    return {top: rect.top, right: rect.right, bottom: rect.bottom, width: rect.width, height: rect.height};
  };
  const shown = node => Boolean(node) && getComputedStyle(node).display !== "none";
  const titleStatus = card.querySelector(".task-card-title-status");
  const actionStatus = card.querySelector(".task-card-actions-status");
  const actions = card.querySelector(".task-card-actions");
  const buttons = [...card.querySelectorAll(".task-card-actions button")];
  const heading = card.querySelector(".task-card-title h3");
  return {
    card: box(card),
    headingWidth: heading ? box(heading).width : 0,
    titleStatusShown: shown(titleStatus),
    actionStatusShown: shown(actionStatus),
    actionStatus: shown(actionStatus) ? box(actionStatus) : null,
    actions: box(actions),
    buttons: buttons.map(button => ({title: button.title, ...box(button)})),
    visibleStatuses: [...card.querySelectorAll(".status")].filter(shown).map(node => node.textContent.trim()),
  };
});
// Rebuild the pre-change DOM on a throwaway clone: status back inside the
// title and no mobile action-column layout. Its height and heading width are
// the baseline the mobile layout must stay within.
const baselineLayout = (page, id) => page.locator(`.task-card[data-task="${id}"]`).evaluate(card => {
  const clone = card.cloneNode(true);
  card.parentNode.insertBefore(clone, card.nextSibling);
  const status = clone.querySelector(".task-card-actions-status");
  const actions = clone.querySelector(".task-card-actions");
  const title = clone.querySelector(".task-card-title");
  actions.classList.remove("has-status");
  if (status) {
    status.classList.remove("task-card-actions-status");
    status.classList.add("task-card-title-status");
    status.style.display = "inline-flex";
    title.appendChild(status);
  }
  const heading = clone.querySelector("h3");
  const result = {cardHeight: clone.getBoundingClientRect().height, headingWidth: heading.getBoundingClientRect().width};
  clone.remove();
  return result;
});
const toggle = (page, kind, value, checked) => page.evaluate(([kind, value, checked]) => {
  const input = document.querySelector(`[data-task-filter="${kind}"] input[value="${value}"]`);
  input.checked = checked;
  input.dispatchEvent(new Event("change", {bubbles: true}));
}, [kind, value, checked]);
const expectOpenMobile = (id, card) => {
  assert.equal(card.titleStatusShown, false, `${id}: the title still shows the status on mobile`);
  assert.equal(card.actionStatusShown, true, `${id}: the action column has no status badge on mobile`);
  assert.ok(card.actionStatus.bottom <= card.buttons[0].top + 0.5, `${id}: the status is not above the row buttons`);
  assert.ok(card.actionStatus.right <= card.actions.right + 0.5, `${id}: the status escapes the action column`);
  assert.ok(card.actions.width <= 80, `${id}: the action column grew past two buttons (${card.actions.width})`);
  assert.equal(card.visibleStatuses.length, 1, `${id}: more than one status is visible: ${card.visibleStatuses}`);
};
const expectSingleReadonlyBadge = (id, card, label) => {
  assert.equal(card.titleStatusShown, false, `${id}: the title status is still visible on mobile`);
  assert.equal(card.actionStatusShown, false, `${id}: a duplicate mobile status is rendered`);
  assert.deepEqual(card.visibleStatuses, [label], `${id}: expected one ${label} badge, got ${card.visibleStatuses}`);
};

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('.task-card[data-task="t-active"]', {timeout: 15000});
  // Remote mirrors are filtered out by default; include their device so the
  // read-only card is measurable.
  await toggle(page, "device", "remote:device-b", true);
  await page.waitForFunction(() => document.querySelectorAll(".task-card").length === 5);

  const desktop = await layout(page, "t-active");
  assert.equal(desktop.titleStatusShown, true, "desktop lost its inline title status");
  assert.equal(desktop.actionStatusShown, false, "desktop shows the mobile status copy");
  await page.screenshot({path: resolve(output, "task-card-mobile-desktop.png")});

  for (const viewport of [{width: 390, height: 844, minHeading: 150}, {width: 360, height: 800, minHeading: 120}]) {
    await page.setViewportSize(viewport);
    await page.waitForFunction(() => document.querySelectorAll(".task-card").length === 5);
    const active = await layout(page, "t-active");
    const baseline = await baselineLayout(page, "t-active");
    expectOpenMobile("t-active", active);
    expectOpenMobile("t-waiting", await layout(page, "t-waiting"));
    assert.ok(
      active.headingWidth > baseline.headingWidth + 30,
      `${viewport.width}px: the title did not gain width (${active.headingWidth} vs ${baseline.headingWidth})`,
    );
    assert.ok(active.headingWidth >= viewport.minHeading, `${viewport.width}px: the title is still cramped (${active.headingWidth})`);
    assert.ok(
      active.card.height <= baseline.cardHeight + 0.5,
      `${viewport.width}px: the card grew taller (${active.card.height} vs ${baseline.cardHeight})`,
    );
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth && document.body.scrollWidth <= window.innerWidth);
    assert.equal(overflow, true, `${viewport.width}px: the task list overflows horizontally`);
    await page.screenshot({path: resolve(output, `task-card-mobile-${viewport.width}.png`)});
  }

  // The action buttons keep their size and still open the title editor.
  const buttons = (await layout(page, "t-active")).buttons;
  assert.deepEqual(buttons.map(button => [button.width, button.height]), [[36, 36], [36, 36]], "the mobile row buttons lost their hit area");
  await page.locator('.task-card[data-task="t-active"] [data-edit-task-title]').click();
  assert.equal(await page.locator('.task-card[data-task="t-active"] .task-title-input').count(), 1, "the edit button no longer opens the title editor");

  // Read-only rows keep their single existing badge, with no extra status copy.
  await page.reload({waitUntil: "networkidle"});
  await page.waitForSelector('.task-card[data-task="t-active"]', {timeout: 15000});
  await toggle(page, "device", "remote:device-b", true);
  await page.waitForFunction(() => document.querySelectorAll(".task-card").length === 5);
  expectSingleReadonlyBadge("t-readonly", await layout(page, "t-readonly"), "只读");
  expectSingleReadonlyBadge("t-archived", await layout(page, "t-archived"), "归档只读");

  // Completed rows swap 已完成 for the existing 已完成只读 badge, not both.
  await toggle(page, "status", "done", true);
  await toggle(page, "status", "open", false);
  await toggle(page, "status", "error", false);
  await page.waitForFunction(() => document.querySelectorAll(".task-card").length === 1);
  expectSingleReadonlyBadge("t-done", await layout(page, "t-done"), "已完成只读");
  await page.screenshot({path: resolve(output, "task-card-mobile-done.png")});

  assert.deepEqual(errors, [], "the task list threw while checking the mobile card layout");
  console.log("task card mobile layout verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}