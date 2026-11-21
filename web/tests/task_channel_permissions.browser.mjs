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
const members = [
  {identity_link_id: "alice", display_name: "Alice Chen", is_bot: false, source: "provider"},
  {identity_link_id: "bob", display_name: "Bob Bot", is_bot: true, source: "observed"},
  {identity_link_id: "carol", display_name: "Carol Wu", is_bot: false, source: "observed"},
  {identity_link_id: "self", display_name: "AHA Bot", is_bot: true, source: "provider", is_self: true},
];
const contacts = [{identity_link_id: "alice", display_name: "Alice Chen", role: "participant", collaboration_role: "APP", is_bot: false, last_seen_at: ${JSON.stringify(now)}}];
const pending = {id: "perm-pending", task_id: "t-ui", action: "在群聊里公布构建产物下载地址", scope: "release artifacts", reason: "测试同学要看包", status: "pending", applicant_name: "Alice Chen", applicant_is_bot: false, risk: "medium", created_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
const approved = {id: "perm-approved", task_id: "t-ui", action: "重跑 CI", scope: "workflow", reason: "偶发失败", status: "approved", applicant_name: "Bob Bot", applicant_is_bot: true, decider_name: "owner", decision_reason: "同意重跑", created_at: ${JSON.stringify(now)}, decided_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 600000).toISOString())}};
const detail = () => ({
  task, turns: [], agents: [{task_id: "t-ui", agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: [], memory: {}, permission_requests: [pending, approved],
});
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "kaikai"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: LANG}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [task], has_more: false, counts: {status: {waiting_user: 1}, project: {"project-1": 1}, device: {local: 1}}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false, knowledge_policy: "enabled"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready", read_only: false, capabilities: {}}]});
  if (path === "/api/v1/models" || path === "/api/v1/providers" || path === "/api/v1/env-groups") return json({models: [], providers: [], env_groups: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path === "/api/v1/tasks/t-ui" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-ui/channel-routes" && method === "GET") return json({
    destinations: [{conversation_id: "conversation-1", instance_id: "instance-1", instance_name: "Feishu", provider_key: "feishu", endpoint_kind: "group_digital_human", display_name: "联调群", status: "active", updated_at: ${JSON.stringify(now)}}],
    route: {id: "route-1", instance_id: "instance-1", conversation_id: "conversation-1", target_task_id: "t-ui", state: "active", revision: 1},
    contacts, members,
  });
  if (path === "/api/v1/tasks/t-ui/agents/main/conversation") return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path === "/api/v1/tasks/t-ui/channel-contacts" && method === "POST") return json({ok: true});
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

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('.task-card[data-task="t-ui"]', {timeout: 15000});
  await page.click('.task-card[data-task="t-ui"]');
  await page.waitForSelector('[data-task-tool="channel"]', {timeout: 15000});
  assert.equal(await page.locator('[data-task-tool="permissions"]').count(), 0, "the permissions tool is still in the toolbar");
  await page.locator('[data-task-tool="channel"]').click();
  await page.waitForSelector("#task-channel-contacts-form", {timeout: 8000});

  const columns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
  assert.equal(columns, 3, `desktop member grid has ${columns} columns`);
  const heights = await page.locator(".task-channel-member").evaluateAll(nodes => nodes.map(node => Math.round(node.getBoundingClientRect().height)));
  assert.deepEqual(heights, [36, 36, 36, 36], `member chip heights: ${heights}`);
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
  assert.equal(await page.locator('#task-channel-contacts-form input[value="self"]').isDisabled(), true);

  // Search only hides rows; it never clears the existing selection.
  await page.fill("#task-channel-member-search", "bo");
  const visibleNames = await page.locator(".task-channel-member:not([hidden]) .task-channel-member-text strong").allInnerTexts();
  assert.deepEqual(visibleNames, ["Bob Bot", "AHA Bot"]);
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");

  // Select all / clear act on the filtered rows only.
  await page.click("#task-channel-select-all");
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 2 / 共 4");
  assert.equal(await page.locator(".task-channel-contact").count(), 2);
  await page.click("#task-channel-clear");
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 1 / 共 4");
  assert.equal(await page.locator(".task-channel-contact").count(), 1);

  await page.fill("#task-channel-member-search", "");
  await page.waitForFunction(() => document.querySelectorAll(".task-channel-member:not([hidden])").length === 4);
  // Removing a contact row is the same as unticking its member chip.
  await page.click('[data-remove-task-contact="alice"]');
  await page.waitForFunction(() => document.querySelectorAll(".task-channel-contact").length === 0);
  assert.equal(await page.locator('#task-channel-contacts-form input[value="alice"]').isChecked(), false);
  assert.equal(await page.locator("#task-channel-member-count").innerText(), "已选 0 / 共 4");
  assert.equal(await page.locator('.task-channel-contact-remove').count(), 0);

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
  await page.screenshot({path: resolve(output, "task-channel-merged.png")});

  // Narrow: two member columns, and a pending approval section moves above the
  // member list.
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForTimeout(150);
  const narrowColumns = await page.locator(".task-channel-member-list").evaluate(node => getComputedStyle(node).gridTemplateColumns.split(" ").length);
  assert.equal(narrowColumns, 2, `narrow member grid has ${narrowColumns} columns`);
  const order = await page.evaluate(() => {
    const approvals = document.querySelector("#task-channel-permissions");
    const members = document.querySelector(".task-channel-members-section");
    return {
      approvalsFirst: Boolean(approvals && members) && approvals.getBoundingClientRect().top < members.getBoundingClientRect().top,
      overflow: document.documentElement.scrollWidth > window.innerWidth,
    };
  });
  assert.equal(order.approvalsFirst, true, "pending approvals should come first on a narrow screen");
  assert.equal(order.overflow, false, "the channel page overflows on a phone");
  await page.screenshot({path: resolve(output, "task-channel-merged-390.png")});

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
  assert.equal((await english.locator("#toggle-task-permission-history").innerText()).trim(), "View history");
  assert.equal(await english.locator(".task-channel-contact-remove").getAttribute("title"), "Remove contact");
  assert.match(await english.locator("#task-channel-permissions").innerText(), /Pending review/);
  assert.deepEqual(englishErrors, [], "the English channel page threw");
  assert.deepEqual(errors, [], "the channel page threw while being driven");
  console.log("merged channel page verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}
