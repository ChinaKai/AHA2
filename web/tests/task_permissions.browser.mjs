// Optional browser verification for the Task permission request entry.
// Reproduces the failure mode where a tall conversation collapsed the request
// block to zero height, and checks the toolbar badge, panel and decision flow.
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

const now = new Date().toISOString();
const bootstrap = `
let permissionStatus = "pending";
const permission = () => ({
  id: "perm-1", task_id: "t-perm", action: "向 AHA-TEST 群发送实现代码",
  scope: "群聊权限申请 / Owner 认证相关实现", reason: "群成员请求展示源码，需要 Owner 批准",
  status: permissionStatus, applicant_name: "蒋开开", risk: "high",
  created_at: ${JSON.stringify(now)}, expires_at: ${JSON.stringify(new Date(Date.now() + 900000).toISOString())},
});
const task = {id: "t-perm", code: "task-025", title: "Permission task", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "auto", max_agents: 3};
const conversation = Array.from({length: 30}, (_, index) => ({
  id: "message-" + index, sequence: index + 1, agent_id: "main", category: "chat", kind: index % 2 ? "user_message" : "agent_message",
  summary: "Message " + (index + 1), text: "Message " + (index + 1) + " ".repeat(20), created_at: ${JSON.stringify(now)},
}));
const detail = () => ({
  task, turns: [], agents: [{task_id: "t-perm", agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: [], memory: {}, permission_requests: [permission()],
});
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.__resolved = null;
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "kaikai"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [task], has_more: false, counts: {status: {waiting_user: 1}, project: {"project-1": 1}, device: {local: 1}}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false, knowledge_policy: "enabled"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready", read_only: false, capabilities: {}}]});
  if (path === "/api/v1/models" || path === "/api/v1/providers" || path === "/api/v1/env-groups") return json({models: [], providers: [], env_groups: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path === "/api/v1/tasks/t-perm" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-perm/channel-routes" && method === "GET") return json({destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-perm/agents/main/conversation") return json({conversation: {items: conversation, has_more: false, next_before: 0, latest_sequence: conversation.length}});
  if (path === "/api/v1/tasks/t-perm/permission-requests/perm-1/resolve" && method === "POST") {
    const body = JSON.parse(init.body || "{}");
    permissionStatus = body.decision === "approve" ? "approved" : "rejected";
    window.__resolved = body;
    return json({ok: true, request: permission()});
  }
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
      response.end(await readFile(resolve(root, "web/dist", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('.task-card[data-task="t-perm"]', {timeout: 15000});
  await page.click('.task-card[data-task="t-perm"]');
  await page.waitForSelector('[data-task-tool="permissions"]', {timeout: 15000});
  assert.equal(await page.locator("#task-permission-slot").count(), 0, "the inline permission block should be gone");

  const button = page.locator('[data-task-tool="permissions"]');
  assert.equal(await button.count(), 1, "permission entry missing from the Task toolbar");
  assert.equal(await button.locator(".task-tool-badge").innerText(), "1", "pending badge did not render");

  await button.click();
  await page.waitForSelector("#task-tool-panel-body .task-permission-item", {timeout: 5000});
  const item = page.locator("#task-tool-panel-body .task-permission-item").first();
  assert.equal(await item.evaluate(node => node.open), false, "permission list should start collapsed");
  const collapsedHeight = (await item.boundingBox()).height;
  assert.ok(collapsedHeight < 60, `collapsed row is too tall (${collapsedHeight}px)`);
  assert.match(await item.innerText(), /蒋开开/);
  assert.match(await item.innerText(), /待审批/);
  assert.equal(await page.locator('#task-tool-panel-body [data-task-permission-decision="approve"]').first().isVisible(), false, "decision buttons should stay hidden until expanded");

  await item.locator("summary").click();
  await page.waitForFunction(() => document.querySelector("#task-tool-panel-body .task-permission-item")?.open === true, null, {timeout: 5000});
  assert.match(await item.innerText(), /群聊权限申请 \/ Owner 认证相关实现/);
  assert.match(await item.innerText(), /群成员请求展示源码/);
  assert.equal(await page.locator('#task-tool-panel-body [data-task-permission-decision="approve"]').count(), 1);
  assert.equal(await page.locator('#task-tool-panel-body [data-task-permission-decision="reject"]').count(), 1);
  await page.screenshot({path: resolve(output, "task-permissions-panel.png")});

  await page.locator('#task-tool-panel-body [data-task-permission-decision="approve"]').click();
  await page.waitForFunction(() => window.__resolved !== null, null, {timeout: 8000});
  assert.equal((await page.evaluate(() => window.__resolved)).decision, "approve");
  await page.waitForFunction(() => document.querySelector("#task-tool-panel-body")?.innerText.includes("已批准"), null, {timeout: 8000});
  assert.deepEqual(errors, [], "the permission entry threw while being driven");
  console.log("task permission entry verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}
