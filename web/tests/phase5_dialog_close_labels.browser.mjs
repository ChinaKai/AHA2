// Phase 5 High batch: every icon-only dialog close button has an accessible
// name, and dialogs put initial focus on a meaningful control instead of the
// close button.
//
// Covers the 23 close buttons in five variants (data-close,
// data-setup-close, data-onboarding-close, data-kb-close, data-mcp-close)
// against the real built Web bundle. Each dialog is opened, checked with
// axe-core's button-name rule, checked for entry focus, and closed. Dialogs
// reachable from the UI are opened through their real trigger so focus return
// is measured as well.
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

const now = new Date().toISOString();
const TASKS = [
  {id: "t-done", code: "task-201", title: "已经完成的任务", status: "completed", project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1, original_request: "finish it", current_goal: "finish it", read_only: true, read_only_reason: "remote_device", created_at: now, updated_at: now},
  {id: "t-active", code: "task-202", title: "进行中的任务", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", created_at: now, updated_at: now},
];
const PROJECTS = [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false}];
const WORKSPACES = [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", locality: "local", health: "ready", read_only: false}];
const PROVIDERS = [{id: "provider-1", name: "Provider One", base_url: "https://example.test/v1", credential_configured: true, revision: 1}];
const KNOWLEDGE = [
  {id: "knowledge-index", scope: "project", project_id: "project-1", parent_id: "", is_index: true, type: "navigation", title: "Project index", slug: "index", body: "home", status: "verified", confidence: 0.9, revision: 1, sort_order: 0, helped_count: 0, stale_count: 0, created_at: now, updated_at: now},
];
const CHANNEL_INSTANCE = {id: "instance-1", plugin_id: "plugin-1", provider_key: "feishu", owner_id: "owner-1", runtime_device_id: "device-1", name: "团队飞书", status: "ready", effective_availability: "available", revision: 1, app_id: "cli_test", credential_configured: true, owner_bound: true, host_project_id: "project-1", host_workspace_id: "workspace-1", retired: false, created_at: now};

const data = {tasks: TASKS, projects: PROJECTS, workspaces: WORKSPACES, providers: PROVIDERS, knowledge: KNOWLEDGE};
const bootstrap = `
const data = ${JSON.stringify(data)};
const instance = ${JSON.stringify(CHANNEL_INSTANCE)};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const params = new URLSearchParams(location.search);
const lang = params.get("lang") || "zh-CN";
const nav = {view: params.get("view") || "tasks"};
const taskID = params.get("task") || "";
if (taskID) { nav.taskID = taskID; nav.task = data.tasks.find(item => item.id === taskID); nav.agentID = "main"; }
const projectID = params.get("project") || "";
if (projectID) { nav.projectID = projectID; nav.project = data.projects.find(item => item.id === projectID); }
sessionStorage.setItem("aha2.navigation", JSON.stringify(nav));
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: lang}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: data.tasks, has_more: false, counts: {status: {}, project: {}, device: {}}});
  const task = data.tasks.find(item => path === "/api/v1/tasks/" + item.id);
  if (task && method === "GET") return json({task: task, turns: [], agents: [{task_id: task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: [], memory: {}});
  if (path === "/api/v1/tasks/t-done/agents") return json({agents: [{task_id: "t-done", agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}]});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path === "/api/v1/projects") return json({projects: data.projects, has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: data.workspaces});
  if (path === "/api/v1/providers") return json({providers: data.providers});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/env-groups") return json({env_groups: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/mcp-servers") return json({servers: []});
  if (path === "/api/v1/knowledge") return json({knowledge: data.knowledge, review_settings: {auto_approve: false}, has_more: false});
  if (path === "/api/v1/knowledge/libraries") return json({libraries: []});
  if (path === "/api/v1/knowledge/indexes") return json({indexes: data.knowledge});
  if (path === "/api/v1/knowledge/proposals") return json({proposals: [], total: 0, page: 1, pages: 0, has_more: false});
  if (path === "/api/v1/projects/project-1/product-lines") return json({product_lines: []});
  if (path === "/api/v1/settings/sync") return json({sync: {scope: "default", enabled: false, endpoint: "", device_id: "", device_name: "", interval_seconds: 300, token_configured: false, passphrase_configured: false}});
  if (path === "/api/v1/settings/sync/status") return json({state: {scope: "default", cursor: "", last_error: ""}, pending: 0, run: {running: false}});
  if (path === "/api/v1/settings/sync/conflicts") return json({conflicts: []});
  if (path === "/api/v1/channel-providers") return json({providers: [{id: "plugin-1", provider_key: "feishu", display_name: "飞书", package_version: "1.0.0", install_state: "installed", enabled: true, available: true, revision: 1}]});
  if (path === "/api/v1/channel-instances") return json({instances: [instance]});
  if (path === "/api/v1/channel-instances/instance-1/deliveries") return json({deliveries: []});
  if (path === "/api/v1/channel-instances/instance-1/knowledge-policy") return json({policies: []});
  if (path === "/api/v1/channel-instances/instance-1/conversations") return json({conversations: []});
  if (path === "/api/v1/channel-instances/instance-1/onboarding-options") return json({options: {mode: "register_app", permission_catalog_version: 1, permissions: [{id: "im:message", name: "消息", description: "收发消息", required: false, enabled: false}]}});
  if (path === "/api/v1/channel-instances/instance-1/onboarding-sessions" && method === "POST") return json({onboarding: {id: "onb-1", instance_id: "instance-1", mode: "register_app", registration_command_id: "cmd-1", status: "pending", step: "waiting_scan", expires_at: new Date(Date.now() + 600000).toISOString(), created_at: new Date().toISOString(), updated_at: new Date().toISOString()}});
  if (path === "/api/v1/channel-onboarding-sessions/onb-1") return json({onboarding: {id: "onb-1", instance_id: "instance-1", mode: "register_app", registration_command_id: "cmd-1", status: "pending", step: "waiting_scan", expires_at: new Date(Date.now() + 600000).toISOString(), created_at: new Date().toISOString(), updated_at: new Date().toISOString()}});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
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
const dismiss = "[data-close],[data-setup-close],[data-onboarding-close],[data-kb-close],[data-mcp-close]";
let browser;
const errors = [];
const checked = [];

async function openView(path, ready) {
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  page.on("pageerror", error => { errors.push(`${path}: ${String(error)}`); console.error(`${path} pageerror:`, error?.stack || String(error)); });
  page.on("dialog", dialog => void dialog.dismiss());
  await page.goto(`${url}/${path}`);
  await page.waitForSelector(ready, {state: "attached", timeout: 15000});
  return page;
}

async function openStatic(page, id) {
  await page.evaluate(dialogID => {
    const dialog = document.getElementById(dialogID);
    if (!(dialog instanceof HTMLDialogElement)) throw new Error(`missing dialog ${dialogID}`);
    if (dialog.open) dialog.close();
    dialog.showModal();
  }, id);
  await page.waitForTimeout(40);
}

async function dialogState(page, id) {
  return page.evaluate(({dialogID, dismiss}) => {
    const dialog = document.getElementById(dialogID);
    if (!(dialog instanceof HTMLDialogElement)) return {exists: false};
    const close = dialog.querySelector(`button.icon-button${dismiss.replaceAll(",", ",button.icon-button")}`);
    const active = document.activeElement;
    return {
      exists: true,
      open: dialog.open,
      closeCount: dialog.querySelectorAll(`button.icon-button${dismiss.replaceAll(",", ",button.icon-button")}`).length,
      label: close?.getAttribute("aria-label") || "",
      closeText: (close?.textContent || "").trim(),
      entryInside: Boolean(active && dialog.contains(active)),
      entryDismiss: Boolean(active && active.matches(dismiss)),
      entryTag: active?.tagName || "",
      entryTabindex: active?.getAttribute("tabindex"),
      entryText: (active?.textContent || "").trim().slice(0, 80),
      entryDisabled: Boolean(active && "disabled" in active && active.disabled),
    };
  }, {dialogID: id, dismiss});
}

async function assertEntryFocus(page, id) {
  const state = await dialogState(page, id);
  assert.equal(state.open, true, `${id}: dialog did not open`);
  assert.ok(state.entryInside, `${id}: initial focus stayed outside the dialog`);
  assert.equal(state.entryDismiss, false, `${id}: initial focus landed on the close control`);
  assert.ok(["INPUT", "SELECT", "TEXTAREA", "H2", "H3", "BUTTON", "A"].includes(state.entryTag), `${id}: unexpected initial focus <${state.entryTag}>`);
  assert.equal(state.entryDisabled, false, `${id}: initial focus landed on a disabled control`);
  if (state.entryTag === "H2" || state.entryTag === "H3") {
    assert.equal(state.entryTabindex, "-1", `${id}: the heading receiving entry focus is not programmatically focusable`);
    assert.ok(state.entryText, `${id}: the heading receiving entry focus has no text`);
  }
  return state;
}

async function assertCloseButton(page, id, expectedLabel, variant, track = true) {
  const state = await dialogState(page, id);
  assert.equal(state.exists, true, `${id}: dialog is missing`);
  assert.equal(state.closeCount, 1, `${id}: expected exactly one icon-only close button`);
  assert.equal(state.label, expectedLabel, `${id}: close button accessible name is ${JSON.stringify(state.label)}`);
  assert.equal(state.closeText, "", `${id}: the icon-only close button also contains text`);
  const violations = await page.evaluate(async dialogID => {
    const dialog = document.getElementById(dialogID);
    const close = dialog.querySelector('button.icon-button[data-close],button.icon-button[data-setup-close],button.icon-button[data-onboarding-close],button.icon-button[data-kb-close],button.icon-button[data-mcp-close]');
    const result = await window.axe.run(close, {runOnly: {type: "rule", values: ["button-name"]}});
    return result.violations.map(item => ({id: item.id, html: item.nodes.map(node => node.html)}));
  }, id);
  assert.deepEqual(violations, [], `${id}: axe button-name reported ${JSON.stringify(violations)}`);
  assert.ok(await page.evaluate(({dialogID, attr}) => document.querySelector(`#${dialogID} button.icon-button[${attr}]`) !== null, {dialogID: id, attr: variant}), `${id}: close button is not a ${variant} variant`);
  if (track) checked.push({id, variant});
}

async function closeByButton(page, id, variant = "data-close") {
  await page.click(`#${id} button.icon-button[${variant}]`);
  await page.waitForTimeout(60);
  const open = await page.evaluate(dialogID => document.getElementById(dialogID)?.open === true, id);
  assert.equal(open, false, `${id}: the close button did not close the dialog`);
}

async function pressEscape(page, id) {
  await page.keyboard.press("Escape");
  await page.waitForTimeout(80);
  return page.evaluate(dialogID => document.getElementById(dialogID)?.open === true, id);
}

try {
  browser = await chromium.launch(launchOptions);

  // 1. Task create dialog: real trigger, focus return, axe, close button.
  const tasks = await openView("?view=tasks", "#task-dialog");
  await tasks.locator('[data-dialog="task"]').click();
  await tasks.waitForSelector("#task-dialog[open]");
  await assertEntryFocus(tasks, "task-dialog");
  await tasks.screenshot({path: resolve(output, "phase5-task-dialog-close-label.png")});
  await assertCloseButton(tasks, "task-dialog", "关闭对话框", "data-close");
  assert.equal(await pressEscape(tasks, "task-dialog"), false, "task-dialog: Escape did not close the dialog");
  const taskReturn = await tasks.evaluate(() => ({tag: document.activeElement?.tagName, dialog: document.activeElement?.dataset?.dialog || ""}));
  assert.equal(taskReturn.dialog, "task", "task-dialog: focus did not return to the create-task trigger");
  await openStatic(tasks, "task-dialog");
  await closeByButton(tasks, "task-dialog");
  await tasks.close();

  // 2. Task detail (completed): agent config, takeover and reopen templates.
  const taskDetail = await openView("?view=tasks&task=t-done", "#reopen-task-dialog");
  await taskDetail.waitForSelector(".task-screen");
  for (const id of ["agent-config-dialog", "task-takeover-dialog", "reopen-task-dialog"]) {
    await openStatic(taskDetail, id);
    await assertEntryFocus(taskDetail, id);
    await assertCloseButton(taskDetail, id, "关闭对话框", "data-close");
    assert.equal(await pressEscape(taskDetail, id), false, `${id}: Escape did not close the dialog`);
    await openStatic(taskDetail, id);
    await closeByButton(taskDetail, id);
  }
  await taskDetail.close();

  // 3. Projects list + project detail: project, workspace, workspace-takeover.
  const projects = await openView("?view=projects", "#project-dialog");
  await projects.locator('[data-dialog="project"]').click();
  await projects.waitForSelector("#project-dialog[open]");
  await assertEntryFocus(projects, "project-dialog");
  await assertCloseButton(projects, "project-dialog", "关闭对话框", "data-close");
  assert.equal(await pressEscape(projects, "project-dialog"), false, "project-dialog: Escape did not close the dialog");
  const projectReturn = await projects.evaluate(() => ({dialog: document.activeElement?.dataset?.dialog || ""}));
  assert.equal(projectReturn.dialog, "project", "project-dialog: focus did not return to the trigger");
  await openStatic(projects, "project-dialog");
  await closeByButton(projects, "project-dialog");
  await projects.click('[data-project="project-1"]');
  await projects.waitForSelector("#workspace-dialog", {state: "attached"});
  for (const id of ["workspace-dialog", "workspace-takeover-dialog"]) {
    await openStatic(projects, id);
    await assertEntryFocus(projects, id);
    await assertCloseButton(projects, id, "关闭对话框", "data-close");
    assert.equal(await pressEscape(projects, id), false, `${id}: Escape did not close the dialog`);
    await openStatic(projects, id);
    await closeByButton(projects, id);
  }
  await projects.close();

  // 4. Models: provider, model picker, model add, model edit, Codex account.
  const models = await openView("?view=models", "#provider-dialog");
  for (const id of ["provider-dialog", "model-dialog", "model-add-dialog", "model-edit-dialog", "codex-account-dialog"]) {
    await openStatic(models, id);
    await assertEntryFocus(models, id);
    await assertCloseButton(models, id, "关闭对话框", "data-close");
    assert.equal(await pressEscape(models, id), false, `${id}: Escape did not close the dialog`);
    await openStatic(models, id);
    await closeByButton(models, id);
  }
  await models.close();

  // 5. Sync conflict dialog.
  const sync = await openView("?view=sync", "#sync-conflict-dialog");
  await openStatic(sync, "sync-conflict-dialog");
  await assertEntryFocus(sync, "sync-conflict-dialog");
  await assertCloseButton(sync, "sync-conflict-dialog", "关闭对话框", "data-close");
  assert.equal(await pressEscape(sync, "sync-conflict-dialog"), false, "sync-conflict-dialog: Escape did not close the dialog");
  await openStatic(sync, "sync-conflict-dialog");
  await closeByButton(sync, "sync-conflict-dialog");
  await sync.close();

  // 6. Knowledge: skill editor, skill detail, product line.
  const knowledge = await openView("?view=knowledge", "#skill-editor-dialog");
  for (const id of ["skill-editor-dialog", "skill-detail-dialog", "product-line-dialog"]) {
    await openStatic(knowledge, id);
    await assertEntryFocus(knowledge, id);
    await assertCloseButton(knowledge, id, "关闭对话框", "data-kb-close");
    assert.equal(await pressEscape(knowledge, id), false, `${id}: Escape did not close the dialog`);
    await openStatic(knowledge, id);
    await closeByButton(knowledge, id, "data-kb-close");
  }
  await knowledge.close();

  // 7. MCP: edit, detail, delete.
  const mcp = await openView("?view=mcp", "#mcp-edit-dialog");
  for (const id of ["mcp-edit-dialog", "mcp-detail-dialog", "mcp-delete-dialog"]) {
    await openStatic(mcp, id);
    await assertEntryFocus(mcp, id);
    await assertCloseButton(mcp, id, "关闭对话框", "data-mcp-close");
    assert.equal(await pressEscape(mcp, id), false, `${id}: Escape did not close the dialog`);
    await openStatic(mcp, id);
    await closeByButton(mcp, id, "data-mcp-close");
  }
  await mcp.close();

  // 8. Channels: settings, conversations, onboarding setup, onboarding session.
  const channels = await openView("?view=channels", "[data-channel-settings-open]");
  await channels.click("[data-channel-settings-open]");
  await channels.waitForSelector("#channel-settings-dialog[open]");
  await assertEntryFocus(channels, "channel-settings-dialog");
  await assertCloseButton(channels, "channel-settings-dialog", "关闭对话框", "data-close");
  assert.equal(await pressEscape(channels, "channel-settings-dialog"), false, "channel-settings-dialog: Escape did not close the dialog");
  await channels.waitForSelector("#channel-settings-dialog", {state: "detached"});
  await channels.click("[data-channel-conversations-open]");
  await channels.waitForSelector("#channel-conversations-dialog[open]");
  await assertEntryFocus(channels, "channel-conversations-dialog");
  await assertCloseButton(channels, "channel-conversations-dialog", "关闭对话框", "data-close");
  assert.equal(await pressEscape(channels, "channel-conversations-dialog"), false, "channel-conversations-dialog: Escape did not close the dialog");
  await channels.waitForSelector("#channel-conversations-dialog", {state: "detached"});
  await channels.click("[data-channel-onboard]");
  await channels.waitForSelector("#channel-onboarding-setup[open]");
  await assertEntryFocus(channels, "channel-onboarding-setup");
  await assertCloseButton(channels, "channel-onboarding-setup", "关闭对话框", "data-setup-close");
  assert.equal(await pressEscape(channels, "channel-onboarding-setup"), false, "channel-onboarding-setup: Escape did not close the dialog");
  await channels.waitForSelector("#channel-onboarding-setup", {state: "detached"});
  await channels.click("[data-channel-onboard]");
  await channels.waitForSelector("#channel-onboarding-setup[open]");
  await channels.click("[data-onboarding-confirm]");
  await channels.waitForSelector("#channel-onboarding-dialog[open]");
  await assertEntryFocus(channels, "channel-onboarding-dialog");
  await assertCloseButton(channels, "channel-onboarding-dialog", "关闭对话框", "data-onboarding-close");
  const onboardingEsc = await pressEscape(channels, "channel-onboarding-dialog");
  assert.equal(onboardingEsc, true, "channel-onboarding-dialog: Escape should keep an in-flight binding open");
  await closeByButton(channels, "channel-onboarding-dialog", "data-onboarding-close");
  await channels.waitForSelector("#channel-onboarding-dialog", {state: "detached"});
  await channels.close();

  // 9. The English catalog renders the same shared key.
  const english = await openView("?view=mcp&lang=en-US", "#mcp-edit-dialog");
  for (const id of ["mcp-edit-dialog", "mcp-detail-dialog", "mcp-delete-dialog"]) {
    await openStatic(english, id);
    await assertCloseButton(english, id, "Close dialog", "data-mcp-close", false);
    await closeByButton(english, id, "data-mcp-close");
  }
  await english.close();

  assert.equal(checked.length, 23, `expected 23 close buttons, checked ${checked.length}`);
  const variants = new Map();
  for (const item of checked) variants.set(item.variant, (variants.get(item.variant) || 0) + 1);
  assert.deepEqual(Object.fromEntries([...variants].sort()), {
    "data-close": 15,
    "data-kb-close": 3,
    "data-mcp-close": 3,
    "data-onboarding-close": 1,
    "data-setup-close": 1,
  }, "close button variant coverage is incomplete");
  assert.deepEqual(errors, [], "the dialog pages reported page errors");
  console.log("dialog close labels: 23/23 axe button-name clean, 5 variants, entry focus and Escape/focus return verified");
} finally {
  await browser?.close();
  server.close();
}