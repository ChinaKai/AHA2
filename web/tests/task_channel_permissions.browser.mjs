// Browser verification for the merged channel page: compact member picker,
// contact removal, and the approval section that now lives at the bottom.
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

const now = new Date().toISOString();
const bootstrap = `
const LANG = new URL(location.href).searchParams.get("lang") || "zh-CN";
const task = {id: "t-ui", code: "task-736", title: "Channel UI", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", channel_instance_id: "", collaboration_mode: "auto", max_agents: 3};
const hostedTask = {id: "t-hosted", code: "task-737", title: "Channel hosted", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", channel_instance_id: "instance-hosted", collaboration_mode: "auto", max_agents: 3};
const readonlyTask = {id: "t-readonly", code: "task-738", title: "Read only channel", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", read_only: true, collaboration_mode: "auto", max_agents: 3};
const members = [
  {identity_link_id: "alice", display_name: "Alice Chen", is_bot: false, source: "provider"},
  {identity_link_id: "bob", display_name: "Bob Bot", is_bot: true, source: "observed"},
  {identity_link_id: "carol", display_name: "Carol Wu", is_bot: false, source: "observed"},
  {identity_link_id: "self", display_name: "AHA Bot", is_bot: true, source: "provider", is_self: true},
];
let contacts = [{identity_link_id: "alice", display_name: "Alice Chen", role: "participant", collaboration_role: "APP", is_bot: false, last_seen_at: ${JSON.stringify(now)}}];
const pending = {id: "perm-pending", task_id: "t-ui", action: "在群聊里公布构建产物下载地址", scope: "release artifacts", reason: "测试同学要看包", status: "pending", applicant_name: "Alice Chen", applicant_is_bot: false, risk: "medium", created_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
const approved = {id: "perm-approved", task_id: "t-ui", action: "重跑 CI", scope: "workflow", reason: "偶发失败", status: "approved", applicant_name: "Bob Bot", applicant_is_bot: true, decider_name: "owner", decision_reason: "同意重跑", created_at: ${JSON.stringify(now)}, decided_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
const hostedApproved = {id: "perm-hosted-approved", task_id: "t-hosted", action: "重跑历史 CI", scope: "workflow", reason: "偶发失败", status: "approved", applicant_name: "Bob Bot", applicant_is_bot: true, decider_name: "owner", decision_reason: "同意重跑", created_at: ${JSON.stringify(now)}, decided_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
const hostedPending = {id: "perm-hosted-pending", task_id: "t-hosted", action: "发布新的构建产物", scope: "release artifacts", reason: "有新的包", status: "pending", applicant_name: "Alice Chen", applicant_is_bot: false, risk: "medium", created_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
let hostedRequests = [hostedApproved];
const detailFor = id => ({
  task: id === "t-hosted" ? hostedTask : id === "t-readonly" ? readonlyTask : task, turns: [],
  agents: [{task_id: id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: [], memory: {},
  permission_requests: id === "t-hosted" ? hostedRequests : id === "t-readonly" ? [] : [pending, approved],
});
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.__channelRouteCalls = 0;
window.__contactPayloads = [];
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path.endsWith("/channel-routes")) window.__channelRouteCalls += 1;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "kaikai"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: LANG}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [task, hostedTask, readonlyTask], has_more: false, counts: {status: {waiting_user: 3}, project: {"project-1": 3}, device: {local: 3}}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false, knowledge_policy: "enabled"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready", read_only: false, capabilities: {}}]});
  if (path === "/api/v1/models" || path === "/api/v1/providers" || path === "/api/v1/env-groups") return json({models: [], providers: [], env_groups: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (["t-ui", "t-hosted", "t-readonly"].includes(path.split("/")[4]) && path.split("/").length === 5 && method === "GET") return json(detailFor(path.split("/")[4]));
  if (path === "/api/v1/tasks/t-ui/channel-routes" && method === "GET") return json({
    destinations: [{conversation_id: "conversation-1", instance_id: "instance-1", instance_name: "Feishu", provider_key: "feishu", endpoint_kind: "group_digital_human", display_name: "联调群", status: "active", updated_at: ${JSON.stringify(now)}}],
    route: {id: "route-1", instance_id: "instance-1", conversation_id: "conversation-1", target_task_id: "t-ui", state: "active", revision: 1},
    contacts, members,
  });
  // The real server rejects routes for hosted and read-only Tasks with 400.
  if ((path === "/api/v1/tasks/t-hosted/channel-routes" || path === "/api/v1/tasks/t-readonly/channel-routes") && method === "GET") {
    return json({ok: false, error: "task_not_eligible", message: "task is not eligible for a primary channel"}, 400);
  }
  if (path.startsWith("/api/v1/tasks/") && path.endsWith("/agents/main/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path === "/api/v1/tasks/t-ui/channel-contacts" && method === "PUT") {
    const payload = JSON.parse(init.body || "{}");
    window.__contactPayloads.push(payload);
    contacts = (payload.contacts || []).map(contact => {
      const member = members.find(item => item.identity_link_id === contact.identity_link_id) || {};
      return {...contact, display_name: member.display_name || contact.identity_link_id, is_bot: Boolean(member.is_bot), source: member.source || "provider", last_seen_at: new Date().toISOString()};
    });
    return json({contacts});
  }
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.__sources = [];
window.__sequence = 0;
window.EventSource = class {
  constructor() { this.listeners = {}; window.__sources.push(this); }
  close() {}
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
};
window.__addHostedPending = () => { hostedRequests = [...hostedRequests, hostedPending]; };
window.__emitTaskUpdate = () => {
  const sequence = ++window.__sequence;
  window.__sources.forEach(source => (source.listeners.update || []).forEach(fn =>
    fn({data: JSON.stringify({sequence, type: "task_permission_requested"})})));
};
if (!sessionStorage.getItem("aha2.navigation")) sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;

// Test-side copy for the deep-link snapshot; the fixture defines the same task
// for the stubbed API.
const readOnlySummary = {id: "t-readonly", code: "task-738", title: "Read only channel", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", read_only: true, collaboration_mode: "auto", max_agents: 3};

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

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => { errors.push(error.message); console.error("pageerror:", error.message); });
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('.task-card[data-task="t-ui"]', {timeout: 15000});
  await page.click('.task-card[data-task="t-ui"]');
  await page.waitForSelector('[data-task-tool="channel"]', {timeout: 15000});
  assert.equal(await page.locator('[data-task-tool="permissions"]').count(), 0, "the permissions tool is still in the toolbar");
  await page.locator('[data-task-tool="channel"]').click();
  await page.waitForSelector("#task-channel-contacts-form", {timeout: 8000});

  const columns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
  assert.equal(columns, 2, `desktop member grid has ${columns} columns`);
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
  assert.equal(await page.locator('#task-channel-contacts-form input[value="self"]').isDisabled(), true);

  // The identity control lives inside the checked member's row and is absent
  // from unchecked rows. The separate "可 @ 联调人" block is gone.
  assert.equal(await page.locator('.task-channel-member[data-member-name="Alice Chen"] .task-channel-member-role').isVisible(), true);
  assert.equal(await page.locator('.task-channel-member[data-member-name="Bob Bot"] .task-channel-member-role').isVisible(), false);
  assert.equal(await page.locator('input[name="role_alice"]').inputValue(), "APP");
  assert.equal(await page.locator('input[name="role_bob"]').isDisabled(), true);
  assert.equal(await page.locator("text=可 @ 联调人").count(), 0);
  assert.equal(await page.locator(".task-channel-contact-list").count(), 0);
  assert.equal(await page.locator('.task-channel-members-section header small').innerText(), "勾选加入联调 · Agent 只会看到这里的人");
  assert.equal((await page.locator("body").innerText()).match(/Agent 只会看到这里的人/g)?.length, 1);

  // Search only hides rows; it never clears the existing selection.
  await page.fill("#task-channel-member-search", "bo");
  const visibleNames = await page.locator(".task-channel-member:not([hidden]) .task-channel-member-text strong").allInnerTexts();
  assert.deepEqual(visibleNames, ["Bob Bot", "AHA Bot"]);
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");

  // Select all / clear act on the filtered rows only. Checking a member shows
  // their identity control immediately; clearing hides it and drops the value.
  await page.click("#task-channel-select-all");
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 2 / 共 4");
  assert.equal(await page.locator('.task-channel-member[data-member-name="Bob Bot"] .task-channel-member-role').isVisible(), true);
  assert.equal(await page.locator('input[name="role_bob"]').isDisabled(), false);
  await page.fill('input[name="role_bob"]', "自定义身份");
  await page.click("#task-channel-clear");
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
  assert.equal(await page.locator('.task-channel-member[data-member-name="Bob Bot"] .task-channel-member-role').isVisible(), false);
  assert.equal(await page.locator('input[name="role_bob"]').inputValue(), "", "unchecking a member must drop their identity");
  assert.equal(await page.locator('input[name="role_bob"]').isDisabled(), true);

  await page.fill("#task-channel-member-search", "");
  await page.waitForFunction(() => document.querySelectorAll(".task-channel-member:not([hidden])").length === 4);

  // Re-checking starts empty, then a custom identity can be saved. Only checked
  // members are submitted.
  await page.locator('input[name="contact_id"][value="bob"]').check();
  assert.equal(await page.locator('input[name="role_bob"]').inputValue(), "");
  await page.fill('input[name="role_bob"]', "field-ops");
  assert.equal(await page.locator('input[name="role_alice"]').inputValue(), "APP");
  const loadCallsBeforeSave = await page.evaluate(() => window.__channelRouteCalls);
  await page.click('#task-channel-contacts-form button[type="submit"]');
  await page.waitForFunction(() => window.__contactPayloads.length === 1, null, {timeout: 8000});
  const payload = await page.evaluate(() => window.__contactPayloads[0]);
  assert.deepEqual(payload.contacts, [
    {identity_link_id: "alice", collaboration_role: "APP"},
    {identity_link_id: "bob", collaboration_role: "field-ops"},
  ]);
  // The fixture echoes the saved contacts, so a post-save reload must re-render
  // the custom identity on the checked member.
  await page.waitForFunction(baseline => window.__channelRouteCalls > baseline, loadCallsBeforeSave, {timeout: 8000});
  assert.equal(await page.locator('input[name="role_bob"]').inputValue(), "field-ops");
  assert.equal(await page.locator('input[name="contact_id"][value="bob"]').isChecked(), true);
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 2 / 共 4");
  assert.equal(await page.locator(".banner.error").count(), 0, "saving contacts surfaced an error");

  // The approval section is part of the channel page and starts with pending
  // work only; its buttons are type=button so they cannot submit the form.
  assert.equal(await page.locator("#task-tool-panel-body #task-channel-permissions").count(), 1);
  assert.equal(await page.locator("#task-tool-panel-body .task-permission-item").count(), 1);
  assert.match(await page.locator("#task-tool-panel-body .task-permission-item").innerText(), /Alice Chen/);
  assert.match(await page.locator("#task-tool-panel-body .task-permission-item").innerText(), /待审批/);
  const decisionTypes = await page.locator("#task-tool-panel-body [data-task-permission-decision]").evaluateAll(nodes => nodes.map(node => node.getAttribute("type")));
  assert.deepEqual(decisionTypes, ["button", "button"]);
  await page.click("#toggle-task-permission-history");
  await page.waitForFunction(() => document.querySelectorAll("#task-tool-panel-body .task-permission-item").length === 1 &&
    document.querySelector("#task-tool-panel-body .task-permission-item")?.innerText.includes("已批准"));
  assert.match(await page.locator("#task-tool-panel-body .task-permission-item").innerText(), /Bob Bot/);
  await page.click("#toggle-task-permission-history");
  await page.waitForFunction(() => document.querySelector("#task-tool-panel-body .task-permission-item")?.innerText.includes("待审批"));

  // Members, approvals, sync: both DOM order and rendered order.
  const order = () => page.evaluate(() => {
    const members = document.querySelector(".task-channel-members-section");
    const approvals = document.querySelector("#task-channel-permissions");
    const sync = document.querySelector(".task-channel-sync");
    const top = node => node?.getBoundingClientRect().top ?? NaN;
    const follows = (first, second) => Boolean(first && second && (first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING));
    return {
      dom: follows(members, approvals) && follows(approvals, sync),
      visual: top(members) < top(approvals) && top(approvals) < top(sync),
      overflow: document.documentElement.scrollWidth > window.innerWidth,
    };
  });
  const desktopOrder = await order();
  assert.equal(desktopOrder.dom, true, "DOM order must be members, approvals, sync");
  assert.equal(desktopOrder.visual, true, "rendered order must be members, approvals, sync");
  await page.screenshot({path: resolve(output, "task-channel-merged.png")});

  // Narrow: one member column; the three blocks keep the same order.
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForTimeout(150);
  const narrowColumns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
  assert.equal(narrowColumns, 1, `narrow member grid has ${narrowColumns} columns`);
  const narrowOrder = await order();
  assert.equal(narrowOrder.dom, true, "narrow DOM order must be members, approvals, sync");
  assert.equal(narrowOrder.visual, true, "narrow rendered order must be members, approvals, sync");
  assert.equal(narrowOrder.overflow, false, "the channel page overflows on a phone");
  await page.screenshot({path: resolve(output, "task-channel-merged-390.png")});

  // Channel-hosted Tasks keep the channel entry at zero pending and can open
  // their approval history; an SSE-created request must update the badge.
  const hosted = await browser.newPage({viewport: {width: 1280, height: 900}});
  const hostedErrors = [];
  hosted.on("pageerror", error => hostedErrors.push(error.message));
  await hosted.goto(url, {waitUntil: "networkidle"});
  await hosted.waitForSelector('.task-card[data-task="t-hosted"]', {timeout: 15000});
  await hosted.click('.task-card[data-task="t-hosted"]');
  await hosted.waitForSelector('[data-task-tool="channel"]', {timeout: 15000});
  assert.equal(await hosted.locator('[data-task-tool="channel"]').count(), 1, "channel-hosted task lost its channel entry");
  assert.equal(await hosted.locator('[data-task-tool="channel"] .task-tool-badge').count(), 0, "zero pending should show no badge");
  await hosted.locator('[data-task-tool="channel"]').click();
  await hosted.waitForSelector("#task-tool-panel-body #task-channel-permissions", {timeout: 8000});
  assert.equal(await hosted.locator("#task-channel-route-form").count(), 0, "channel-hosted task should not render the route form");
  assert.match(await hosted.locator("#task-channel-permissions").innerText(), /当前没有待审批的权限申请/);
  await hosted.click("#toggle-task-permission-history");
  await hosted.waitForFunction(() => document.querySelector("#task-channel-permissions")?.innerText.includes("重跑历史 CI"), null, {timeout: 5000});
  assert.match(await hosted.locator("#task-channel-permissions").innerText(), /已批准/);
  assert.match(await hosted.locator("#task-channel-permissions").innerText(), /Bob Bot/);
  await hosted.click("#toggle-task-permission-history");
  await hosted.waitForFunction(() => document.querySelector("#task-channel-permissions")?.innerText.includes("当前没有待审批的权限申请"));
  await hosted.evaluate(() => { window.__addHostedPending(); window.__emitTaskUpdate(); });
  await hosted.waitForFunction(() => document.querySelector('[data-task-tool="channel"] .task-tool-badge')?.textContent === "1", null, {timeout: 8000});
  assert.equal(await hosted.locator('[data-task-tool="channel"]').evaluate(node => node.classList.contains("has-pending")), true);
  await hosted.waitForFunction(() => document.querySelector("#task-channel-permissions")?.innerText.includes("发布新的构建产物"), null, {timeout: 8000});
  await hosted.screenshot({path: resolve(output, "task-channel-hosted-sse.png")});
  assert.deepEqual(hostedErrors, [], "the channel-hosted task threw");

  // A pinned channel must not call channel-routes for Tasks that cannot bind
  // one. The mock returns the same 400 as the real server, so any request
  // would surface as an error banner.
  const openPinnedUnbindable = async (taskID, directTask = null) => {
    const pinned = await browser.newPage({viewport: {width: 1280, height: 900}});
    await pinned.addInitScript(({direct}) => {
      localStorage.setItem("aha2.task-tool-layout", JSON.stringify({pinnedTool: "channel"}));
      // Read-only Tasks are outside the default local-device list, so deep-link
      // one through the navigation snapshot instead of changing the filters.
      if (direct) sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: direct.id, task: direct}));
    }, {direct: directTask});
    await pinned.goto(url, {waitUntil: "networkidle"});
    if (directTask) {
      await pinned.waitForFunction(id => document.querySelector(".task-head .task-code")?.textContent?.includes(id), directTask.code, {timeout: 15000});
    } else {
      await pinned.waitForSelector(`.task-card[data-task="${taskID}"]`, {timeout: 15000});
      await pinned.click(`.task-card[data-task="${taskID}"]`);
    }
    await pinned.waitForSelector("#task-tool-panel-body #task-channel-permissions", {timeout: 15000});
    await pinned.waitForTimeout(300);
    return pinned;
  };
  const pinnedHosted = await openPinnedUnbindable("t-hosted");
  assert.equal(await pinnedHosted.evaluate(() => window.__channelRouteCalls), 0, "pinned hosted Task requested channel-routes");
  assert.equal(await pinnedHosted.locator(".banner.error").count(), 0, "pinned hosted Task surfaced a route error");
  assert.equal(await pinnedHosted.locator("#task-channel-route-form").count(), 0);
  assert.match(await pinnedHosted.locator("#task-channel-permissions").innerText(), /当前没有待审批的权限申请/);
  assert.match(await pinnedHosted.locator("#task-channel-permissions").innerText(), /查看历史/);
  await pinnedHosted.screenshot({path: resolve(output, "task-channel-hosted-pinned.png")});

  const pinnedReadOnly = await openPinnedUnbindable("t-readonly", readOnlySummary);
  assert.equal(await pinnedReadOnly.evaluate(() => window.__channelRouteCalls), 0, "pinned read-only Task requested channel-routes");
  assert.equal(await pinnedReadOnly.locator(".banner.error").count(), 0, "pinned read-only Task surfaced a route error");
  assert.equal(await pinnedReadOnly.locator("#task-channel-route-form").count(), 0);
  assert.match(await pinnedReadOnly.locator("#task-channel-permissions").innerText(), /当前没有待审批的权限申请/);
  assert.match(await pinnedReadOnly.locator("#task-channel-permissions").innerText(), /查看历史/);
  await pinnedReadOnly.screenshot({path: resolve(output, "task-channel-readonly-pinned.png")});

  // English UI must not fall back to Chinese for the new copy.
  const english = await browser.newPage({viewport: {width: 1280, height: 900}});
  const englishErrors = [];
  english.on("pageerror", error => englishErrors.push(error.message));
  await english.goto(`${url}/?lang=en-US`, {waitUntil: "networkidle"});
  await english.waitForSelector('.task-card[data-task="t-ui"]', {timeout: 15000});
  await english.click('.task-card[data-task="t-ui"]');
  await english.waitForSelector('[data-task-tool="channel"]', {timeout: 15000});
  assert.match(await english.locator('[data-task-tool="channel"]').getAttribute("title"), /\(1 pending\)/);
  await english.locator('[data-task-tool="channel"]').click();
  await english.waitForSelector("#task-channel-contacts-form", {timeout: 8000});
  assert.equal(await english.locator("#task-channel-member-search").getAttribute("placeholder"), "Search by name");
  assert.equal(await english.locator("#task-channel-member-count").innerText(), "Selected 1 / 4 total");
  assert.equal((await english.locator(".task-channel-members-section header small").innerText()).trim(), "Select to join integration · The Agent only sees the people here");
  assert.equal(await english.locator('input[name="role_alice"]').getAttribute("placeholder"), "APP / Cloud / Firmware");
  assert.equal((await english.locator(".task-channel-sync button").innerText()).trim(), "Sync group members");
  assert.equal((await english.locator("#toggle-task-permission-history").innerText()).trim(), "View history");
  assert.match(await english.locator("#task-channel-permissions").innerText(), /Pending review/);
  assert.deepEqual(englishErrors, [], "the English channel page threw");
  assert.deepEqual(errors, [], "the channel page threw while being driven");
  console.log("merged channel page verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}
