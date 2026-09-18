// Optional browser verification; dependencies and browser files stay in .tools.
// Drives the real built Web bundle against a stubbed AHA API to confirm the two
// behaviours the Owner asked for: a completed Task freezes its input box behind
// a confirmed reopen, and reopening restores normal input.
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

const task = status => ({
  id: "task-1", code: "task-001", title: "Completed example", status,
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "do the thing", current_goal: "do the thing", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
});
const detail = status => ({
  task: task(status),
  turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: [], memory: {current_goal: "do the thing"},
});

const bootstrap = `
window.__reopened = 0;
window.__phase = "active";
window.__calls = [];
window.__sent = [];
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const taskOf = status => ({
  id: "task-1", code: "task-001", title: "Completed example", status,
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "do the thing", current_goal: "do the thing", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
});
const detailOf = status => ({task: taskOf(status), turns: [], agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: [], memory: {current_goal: "do the thing"}});
const real = window.fetch;
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  window.__calls.push(method + " " + path);
  const status = window.__phase === "completed" ? "completed" : "waiting_user";
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/tasks/task-1/reopen" && method === "POST") { window.__reopened++; window.__phase = "reopened"; return json({ok: true}); }
  if (path === "/api/v1/tasks/task-1/agents/main/messages" && method === "POST") { window.__sent.push(JSON.parse(init.body || "{}").content || ""); return json({ok: true, started: false, queued: true}); }
  if (path === "/api/v1/tasks/task-1" && method === "GET") return json(detailOf(status));
  if (path === "/api/v1/tasks/task-1" && method === "PATCH") return json({ok: true, task: taskOf(status)});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "Workspace", root_path: "/tmp/workspace", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks") return json({tasks: [taskOf(status)], has_more: false});
  if (path === "/api/v1/tasks/task-1/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path === "/api/v1/tasks/task-1/agents") return json({agents: detailOf(status).agents});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return real(input, init);
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.__complete = () => { window.__phase = "completed"; };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: "task-1", task: taskOf("active"), agentID: "main"}));
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
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector("#message-form", {timeout: 15000});
  assert.deepEqual(errors, [], "the task screen threw while rendering");

  // 1. While the Task is active the normal composer is present.
  assert.equal(await page.locator("#message-form textarea").count(), 1, "active task lost its input box");
  assert.equal(await page.locator("#reopen-task").count(), 0, "active task offered a reopen control");

  // The composer must stay a <form> in the normal (non-frozen) state: the send
  // button and the Enter key both rely on form submit, so a <div> here breaks
  // sending messages entirely.
  assert.equal(await page.locator("#message-form").evaluate(node => node.tagName), "FORM", "active composer is not a form");
  const sentCount = () => page.evaluate(() => window.__calls.filter(call => call === "POST /api/v1/tasks/task-1/agents/main/messages").length);
  const activeTextarea = page.locator("#message-form textarea");
  // The send button and the Enter key are the two ways a message leaves the Web
  // UI; both go through form submit, so both must work on a normal Task.
  await activeTextarea.fill("sent with the button");
  await page.click("#message-send");
  await page.waitForFunction(() => window.__sent.length === 1, null, {timeout: 5000});
  assert.equal(await sentCount(), 1, "the send button did not post a message");
  assert.equal(await activeTextarea.inputValue(), "", "sending did not clear the input");
  await activeTextarea.fill("sent with Enter");
  await activeTextarea.press("Enter");
  await page.waitForFunction(() => window.__sent.length === 2, null, {timeout: 5000});
  assert.equal(await sentCount(), 2, "the Enter key did not post a message");
  assert.deepEqual(await page.evaluate(() => window.__sent), ["sent with the button", "sent with Enter"]);

  // 2. Completion arriving over SSE freezes the composer without a full reload.
  await page.evaluate(() => window.__complete());
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await page.waitForSelector("#reopen-task", {timeout: 20000});
  assert.equal(await page.locator("#message-form textarea").count(), 0, "live completion kept the input box");
  assert.equal(await page.locator("#message-send").count(), 0, "live completion kept the send button");
  assert.equal(await page.locator("#message-form").evaluate(node => node.tagName), "DIV", "frozen composer is still a form");
  assert.match(await page.locator("#agent-turn-slot").innerText(), /重新打开任务后才能开始下一轮/);
  assert.match(await page.locator("#task-frozen-slot").innerText(), /禁止编辑和继续输入/);
  await page.screenshot({path: resolve(output, "completed-task-frozen.png")});

  // 3. The reopen control must ask for confirmation before calling the API.
  const reloads = () => page.evaluate(() => window.__reopened);
  await page.click("#reopen-task");
  await page.waitForSelector("#reopen-task-dialog[open]", {timeout: 5000});
  assert.match(await page.locator("#reopen-task-dialog").innerText(), /确认重新打开/);
  await page.screenshot({path: resolve(output, "completed-task-reopen-dialog.png")});
  assert.equal(await reloads(), 0, "opening the confirmation dialog already reopened the task");
  await page.click("#reopen-task-dialog button[data-close]");
  await page.waitForSelector("#reopen-task-dialog", {state: "hidden", timeout: 5000});
  assert.equal(await reloads(), 0, "cancelling the dialog reopened the task");

  // 4. Confirming issues exactly one reopen, and the composer comes back.
  await page.click("#reopen-task");
  await page.waitForSelector("#reopen-task-dialog[open]", {timeout: 5000});
  await page.click("#reopen-task-form button[type=submit]");
  await page.waitForSelector("#message-form textarea", {timeout: 10000});
  await page.waitForFunction(() => window.__reopened === 1, null, {timeout: 5000});
  assert.equal(await reloads(), 1, "confirming did not reopen exactly once");
  assert.equal(await page.locator("#reopen-task").count(), 0, "reopen control survived a successful reopen");
  assert.equal(await page.locator("#reopen-task-dialog").count(), 0, "reopen dialog survived a successful reopen");
  assert.equal(await page.locator("#task-frozen-slot").innerText(), "", "frozen banner survived a successful reopen");
  assert.equal(await page.locator("#message-form").evaluate(node => node.tagName), "FORM", "thawed composer is not a form again");
  const textarea = page.locator("#message-form textarea");
  assert.equal(await textarea.isDisabled(), false, "reopened task kept a disabled input");
  await textarea.fill("continue the goal");
  assert.equal(await page.locator("#message-send").isEnabled(), true, "reopened task cannot send a message");
  await page.screenshot({path: resolve(output, "completed-task-reopened.png")});

  // 5. Completing a second time must re-freeze without duplicating the dialog.
  await page.evaluate(() => window.__complete());
  await page.waitForSelector("#reopen-task", {timeout: 20000});
  assert.equal(await page.locator("#reopen-task-dialog").count(), 1, "re-freezing duplicated the reopen dialog");
  assert.deepEqual(errors, [], "the task screen threw during the live transitions");
  console.log("completed task freeze/reopen verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}
