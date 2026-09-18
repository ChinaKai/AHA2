// Optional browser verification; dependencies and browser files stay in .tools.
// Drives the real built Web bundle against a stubbed AHA API to confirm the task
// panel work: grouped status filters with server counts, the create panel's
// project-level grants, the proxy select, and the collapsed optional modules.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const output = resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

// One task per status so every bucket has a countable member, plus a second
// project and a remote mirror so the popovers have more than one option.
const TASKS = [
  {id: "t-active", code: "task-001", title: "Running work", status: "active", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-waiting", code: "task-002", title: "Waiting work", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-draft", code: "task-003", title: "Draft work", status: "draft", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-done", code: "task-004", title: "Finished work", status: "completed", project_id: "project-1", workspace_id: "workspace-1"},
  {id: "t-blocked", code: "task-005", title: "Blocked work", status: "blocked", project_id: "project-2", workspace_id: "workspace-1"},
  {id: "t-failed", code: "task-006", title: "Failed work", status: "failed", project_id: "project-2", workspace_id: "workspace-1"},
  {id: "t-remote", code: "task-007", title: "Remote work", status: "active", project_id: "project-1", workspace_id: "workspace-1", owner_device_id: "device-b", read_only: true},
];
const COUNTS = {
  status: {active: 2, waiting_user: 1, draft: 1, completed: 1, blocked: 1, failed: 1},
  project: {"project-1": 5, "project-2": 2},
  device: {local: 6, "remote:device-b": 1},
};

const bootstrap = `
window.__created = null;
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASKS = ${JSON.stringify(TASKS)};
const COUNTS = ${JSON.stringify(COUNTS)};
const SKILLS = [
  {id: "skill-a", name: "Alpha skill", scope: "global", description: "first", enabled: true, status: "active"},
  {id: "skill-b", name: "Beta skill", scope: "project", project_id: "project-1", description: "second", enabled: true, status: "active"},
  {id: "skill-c", name: "Gamma skill", scope: "project", project_id: "project-2", description: "third", enabled: true, status: "active"},
];
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: TASKS, has_more: false, counts: COUNTS});
  if (path === "/api/v1/tasks" && method === "POST") { window.__created = JSON.parse(init.body || "{}"); return json({task: {...TASKS[0], id: "t-new"}, started: true}); }
  if (path === "/api/v1/projects") return json({projects: [
    {id: "project-1", name: "Project One", project_type: "folder", channel_retired: false, knowledge_policy: "enabled"},
    {id: "project-2", name: "Project Two", project_type: "folder", channel_retired: false, knowledge_policy: "enabled"},
  ], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready", read_only: false, capabilities: {codex: {status: "ready"}}}]});
  if (path === "/api/v1/skills") return json({skills: SKILLS});
  if (path === "/api/v1/models") return json({models: [{id: "model-1", display_name: "Stub model", provider_id: "provider-1", source: "provider", backend: "codex", wire_model: "stub", default_env_group_id: "env-1", created_at: new Date().toISOString(), updated_at: new Date().toISOString()}]});
  if (path === "/api/v1/providers") return json({providers: [{id: "provider-1", name: "Provider", auth_style: "none"}]});
  if (path === "/api/v1/env-groups") return json({env_groups: [{id: "env-1", name: "Env", provider_id: "provider-1", backend: "stub", revision: 1, environment: {}, secret_refs: {}}]});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path === "/api/v1/tasks/t-new" && method === "GET") return json({task: {...TASKS[0], id: "t-new"}, turns: [], agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: [], memory: {}});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
localStorage.setItem("aha2.sidebar-memo", JSON.stringify([
  {id: "memo-1", text: "Fix the flaky installer\\nIt fails on a clean machine.", done: false},
  {id: "memo-2", text: "Already handled", done: true},
  {id: "memo-3", text: "", done: false},
]));
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
      if (!/^\/[a-z_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/dist", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
const visibleCodes = page => page.locator(".task-card .task-code").allInnerTexts();
// Toggling a filter re-renders the whole list, so the checkbox node is replaced
// mid-click. Set the value and dispatch the change the handler listens for.
const toggleFilter = (page, filter, value, checked) => page.evaluate(([filter, value, checked]) => {
  const input = document.querySelector(`[data-task-filter="${filter}"] input[value="${value}"]`);
  input.checked = checked;
  input.dispatchEvent(new Event("change", {bubbles: true}));
}, [filter, value, checked]);
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1440, height: 1400}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector(".task-filters", {timeout: 15000}).catch(async () => {
    console.log("BOOT FAILED, pageerrors:", JSON.stringify(errors));
    console.log("APP TEXT:", (await page.locator("#app").innerText()).slice(0, 300));
    throw new Error("task list never rendered");
  });
  assert.deepEqual(errors, [], "the task list threw while rendering");

  // 1. Status filtering is three buckets, counted from the server totals.
  const statusFilter = page.locator('[data-task-filter="status"]');
  assert.equal(await statusFilter.locator("summary strong").innerText(), "已选 2 项", "status filter does not open on 未完成 + 异常");
  await statusFilter.locator("summary").click();
  const statusRows = await statusFilter.locator(".task-filter-option strong").allInnerTexts();
  assert.deepEqual(statusRows, ["未完成", "已完成", "异常"], "status options are not the three groups");
  const statusCounts = await statusFilter.locator(".task-filter-option small").allInnerTexts();
  // 未完成 = active 2 + waiting_user 1 + draft 1; 已完成 = 1; 异常 = blocked + failed = 2.
  assert.deepEqual(statusCounts, ["4", "1", "2"], "status counts do not match the server totals");
  // The remote mirror counts on its device but is excluded from the open bucket
  // only by its own status, so the default view hides completed work.
  const shown = await visibleCodes(page);
  assert.ok(shown.includes("task-001") && shown.includes("task-005") && shown.includes("task-006"), `open+error filter hid live work: ${shown}`);
  assert.ok(!shown.includes("task-004"), "completed work leaked into the default filter");

  // Ticking 已完成 shows the finished task and hides the open ones.
  await toggleFilter(page, "status", "done", true);
  await toggleFilter(page, "status", "open", false);
  await toggleFilter(page, "status", "error", false);
  await page.waitForFunction(() => document.querySelectorAll(".task-card").length === 1);
  assert.deepEqual(await visibleCodes(page), ["task-004"], "已完成 filter did not isolate completed work");
  await page.screenshot({path: resolve(output, "task-filters-grouped.png")});

  // 2. Project and device popovers are counted from the whole set too.
  await page.locator('[data-task-filter="project"] summary').click();
  const projectCounts = await page.locator('[data-task-filter="project"] .task-filter-option small').allInnerTexts();
  assert.deepEqual(projectCounts, ["5", "2"], "project counts are not the server totals");
  await page.locator('[data-task-filter="device"] summary').click();
  const deviceLabels = await page.locator('[data-task-filter="device"] .task-filter-option strong').allInnerTexts();
  const deviceCounts = await page.locator('[data-task-filter="device"] .task-filter-option small').allInnerTexts();
  assert.deepEqual(deviceLabels, ["本机", "device-b"], `device options lost the whole set: ${deviceLabels}`);
  assert.deepEqual(deviceCounts, ["6", "1"], "device counts are not the server totals");

  // 3. The create panel: proxy is a select, both optional modules start collapsed.
  await page.click('[data-dialog="task"]');
  await page.waitForSelector("#task-dialog[open]", {timeout: 5000});
  // The project select drives which skills are offered, so it must hold a value
  // as soon as the dialog opens.
  assert.notEqual(await page.locator("#task-project").inputValue(), "", "the create panel opened with no project selected");

  const proxy = page.locator('#task-form select[name="proxy_enabled"]');
  assert.equal(await proxy.count(), 1, "proxy is not a select in the create panel");
  assert.equal(await proxy.inputValue(), "disabled", "proxy does not default to 关闭");
  assert.equal(await page.locator('#task-form [name="proxy_enabled"][type=checkbox]').count(), 0, "the proxy checkbox is still present");
  for (const id of ["task-capabilities-fold", "task-skills-fold"]) {
    assert.equal(await page.locator(`#${id}`).evaluate(node => node.open), false, `${id} should start collapsed`);
  }
  assert.match(await page.locator("#task-skills-fold [data-fold-count]").innerText(), /未选择/);
  assert.match(await page.locator("#task-capabilities-fold [data-fold-count]").innerText(), /默认关闭/);
  await page.screenshot({path: resolve(output, "task-panel-collapsed.png")});

  // A collapsed Skills module must not grow the dialog with the skill count.
  const skillsHeight = () => page.locator("#task-skills-fold").evaluate(node => node.getBoundingClientRect().height);
  const collapsedSkillsHeight = await skillsHeight();
  await page.locator("#task-skills-fold summary").click();
  await page.locator('#task-skills-fold input[value="skill-a"]').setChecked(true);
  await page.locator('#task-skills-fold input[value="skill-b"]').setChecked(true);
  assert.match(await page.locator("#task-skills-fold [data-fold-count]").innerText(), /已选 2/);
  const offered = await page.locator('#task-skills-fold input[name="skill_ids"]').evaluateAll(nodes => nodes.map(node => node.value));
  assert.deepEqual(offered.sort(), ["skill-a", "skill-b"], `skills from another project leaked in: ${offered}`);
  await page.screenshot({path: resolve(output, "task-panel-expanded.png")});
  await page.locator("#task-skills-fold summary").click();
  assert.ok(await skillsHeight() <= collapsedSkillsHeight + 1, "collapsed skills module grew with the skill count");

  // 4. Creating passes the grants and the proxy choice through. The dialog is
  // rebuilt by every list re-render, so it is opened here rather than reused
  // from the filter checks above.
  await page.locator("#task-dialog").evaluate(dialog => dialog.close());
  await page.waitForFunction(() => document.querySelector("#task-model")?.options.length > 0, null, {timeout: 10000});
  await page.click('[data-dialog="task"]');
  await page.waitForSelector("#task-dialog[open]", {timeout: 5000});
  await page.locator("#task-capabilities-fold summary").click();
  await page.locator('#task-capabilities-fold input[name="cap_workspace_read"]').setChecked(true);
  assert.match(await page.locator("#task-capabilities-fold [data-fold-count]").innerText(), /已启用 1\/3/);
  await proxy.selectOption("enabled");
  await page.fill('#task-form input[name="title"]', "New task");
  await page.fill('#task-form textarea[name="request"]', "Do something");
  await page.click('#task-form button[value="immediate"]');
  await page.waitForFunction(() => window.__created !== null, null, {timeout: 8000});
  const created = await page.evaluate(() => window.__created);
  assert.equal(created.proxy_enabled, true, "proxy select was not submitted");
  assert.equal(created.agent_capabilities.workspace_read, true, "the workspace grant was not submitted");
  assert.equal(created.agent_capabilities.task_create, false, "an unchecked grant was submitted as granted");
  assert.equal(created.agent_capabilities.clone_hardware, false, "an unchecked grant was submitted as granted");
  assert.deepEqual(created.skill_ids.sort(), ["skill-a", "skill-b"], "selected skills were not submitted");
  assert.deepEqual(errors, [], "the task panel threw while being driven");

  // 5. A memo can seed the create panel. The button only shows on a row that
  // has text and is not done; empty and finished rows keep it hidden.
  await page.locator("#task-dialog").evaluate(dialog => dialog.close());
  const memoRow = id => page.locator(`[data-sidebar-memo-item="${id}"]`);
  // Start from another view: the panel only exists in the tasks view, so this
  // proves the button navigates there rather than assuming it is already open.
  await page.locator('.sidebar [data-view="projects"]').click();
  await page.waitForSelector(".sidebar-memo-item", {timeout: 10000});
  assert.equal(await page.locator("#task-dialog").count(), 0, "another view unexpectedly rendered the create panel");
  assert.equal(await memoRow("memo-1").locator("[data-sidebar-memo-task]").count(), 1, "a creatable memo has no create button");
  const taskButtonVisible = id => memoRow(id).locator("[data-sidebar-memo-task]").evaluate(node => getComputedStyle(node).display !== "none");
  assert.equal(await taskButtonVisible("memo-1"), false, "the create button shows before the row is hovered");
  const previewWidth = () => memoRow("memo-1").locator(".sidebar-memo-preview-text").evaluate(node => node.getBoundingClientRect().width);
  const restingWidth = await previewWidth();
  await memoRow("memo-1").hover();
  assert.ok(Math.abs(await previewWidth() - restingWidth) < 1, "revealing the create button shrank the memo text");
  assert.equal(await taskButtonVisible("memo-1"), true, "hovering the row did not reveal the create button");
  assert.equal(await taskButtonVisible("memo-2"), false, "a finished memo offers to create a task");
  assert.equal(await taskButtonVisible("memo-3"), false, "an empty memo offers to create a task");

  // Clicking it lands on the tasks view with the panel prefilled: first line as
  // the title, the whole memo as the request.
  await memoRow("memo-1").locator("[data-sidebar-memo-task]").click();
  await page.waitForSelector("#task-dialog[open]", {timeout: 10000});
  assert.equal(await page.locator('#task-form input[name="title"]').inputValue(), "Fix the flaky installer");
  assert.equal(await page.locator('#task-form textarea[name="request"]').inputValue(), "Fix the flaky installer\nIt fails on a clean machine.");
  // The panel must be usable straight away, not left with unset selects.
  assert.equal(await page.locator("#task-form").evaluate(f => f.checkValidity()), true, "the prefilled panel is not submittable");
  assert.equal(await page.locator("#task-project").inputValue(), "project-1", "the prefilled panel has no project selected");
  // The memo is left untouched.
  assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem("aha2.sidebar-memo")).find(item => item.id === "memo-1").text), "Fix the flaky installer\nIt fails on a clean machine.");
  assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem("aha2.sidebar-memo")).find(item => item.id === "memo-1").done), false, "creating a task marked the memo done");
  assert.deepEqual(errors, [], "creating a task from a memo threw");
  // Regression guard: the compose page used to drop the memo list, so rows
  // silently reverted to the empty placeholder after leaving and returning.
  await page.locator("#task-dialog").evaluate(dialog => dialog.close());
  await page.locator('.sidebar [data-view="projects"]').click();
  await page.waitForSelector(".sidebar-memo-item", {timeout: 10000});
  assert.match(await memoRow("memo-1").innerText(), /Fix the flaky installer/, "the memo text vanished after navigating");
  // Opening the created Task must not leave an error banner behind.
  await page.waitForFunction(() => !document.querySelector(".banner.error"), null, {timeout: 10000});
  await page.screenshot({path: resolve(output, "memo-create-task.png")});
  console.log("task panels verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}
