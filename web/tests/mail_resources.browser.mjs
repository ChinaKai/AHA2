import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const output = resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const now = "2026-11-06T09:00:00Z";
const task = {
  id: "task-mail", code: "task-email", project_id: "project-1", workspace_id: "workspace-1",
  title: "Mail Task", original_request: "", current_goal: "", status: "active",
  target_branch: "", task_branch: "", isolation: "inplace", collaboration_mode: "single",
  max_agents: 1, knowledge_policy: "inherit", skill_ids: [], mcp_server_ids: [],
  agent_capabilities: {}, total_tokens: 0, created_at: now, updated_at: now,
  agent_api_mode: "auto", agent_api_status: "ready",
};
const template = {
  id: "mail_template_1", scope: "global", project_id: "", name: "提测模板",
  description: "", subject_template: "提测通知", body_text_template: "请测试。",
  body_html_template: "", default_to: ["qa@example.com"], default_cc: [],
  required_variables: [], current_version: 1, status: "active",
  created_at: now, updated_at: now,
};
const account = {
  id: "mail_account_1", name: "构建通知", address: "noreply@example.com",
  display_name: "AHA", provider: "netease_qiye", smtp_host: "smtp.qiye.163.com",
  smtp_port: 465, smtp_security: "ssl", smtp_username: "noreply@example.com",
  credential_configured: true, status: "ok", last_test_at: now, last_test_error_code: "",
  aliases: [], created_at: now, updated_at: now,
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const NOW = ${JSON.stringify(now)};
const TASK = ${JSON.stringify(task)};
const TEMPLATE = ${JSON.stringify(template)};
const ACCOUNT = ${JSON.stringify(account)};
let MAIL_ACCOUNTS = [ACCOUNT];
let MESSAGES = [];
window.__createdMailAccount = null;
window.__sentPayload = null;
window.__approved = false;
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(init.body) : {};
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", knowledge_policy: "enabled", knowledge_revision: 1, updated_at: NOW}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "Local", locality: "local", transport: "native", root_path: "E:/repo", health: "ready", ssh_password_configured: false}]});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [TASK], has_more: false, counts: {}});
  if (path === "/api/v1/hardware-resources") return json({resources: []});
  if (path === "/api/v1/mail-accounts" && method === "GET") return json({ok: true, accounts: MAIL_ACCOUNTS});
  if (path === "/api/v1/mail-accounts" && method === "POST") {
    window.__createdMailAccount = body;
    const created = {...ACCOUNT, ...body, id: "mail_account_new", credential_configured: true, status: "unverified", aliases: [], created_at: NOW, updated_at: NOW};
    MAIL_ACCOUNTS = [...MAIL_ACCOUNTS, created];
    return json({ok: true, account: created}, 201);
  }
  if (path === "/api/v1/mail-templates" && method === "GET") return json({ok: true, templates: [TEMPLATE]});
  if (path === "/api/v1/mail-messages" && method === "GET") return json({ok: true, messages: MESSAGES.map(card => card.message)});
  if (path === "/api/v1/tasks/task-mail" && method === "GET") return json({
    task: TASK, latest_round: undefined, turns: [], event_cursor: 0, server_time_ms: Date.now(),
    agents: [{task_id: TASK.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_snapshot_id: "snapshot", inherit_main: false, proxy_enabled: false, fast_mode: false, runtime_config_valid: true, unread_count: 0, created_at: NOW, updated_at: NOW}],
    hardware: [], hardware_resources: [], permission_requests: [],
  });
  if (path === "/api/v1/tasks/task-mail/agents/main/conversation") return json({ok: true, conversation: {items: MESSAGES.map((card, index) => ({sequence: index + 1, id: "conversation-mail-" + card.message.id, task_id: TASK.id, agent_id: "main", category: "mail", kind: "mail_message", summary: "邮件状态", payload: {message_id: card.message.id, status: card.message.status}, created_at: NOW})), has_more: false, next_before: 0, latest_sequence: MESSAGES.length}});
  if (path === "/api/v1/tasks/task-mail/mail" && method === "GET") return json({
    ok: true, task_id: TASK.id,
    accounts: [{account_id: ACCOUNT.id, binding_id: "mail_binding_1", binding_scope: "task", from_address: ACCOUNT.address, display_name: ACCOUNT.display_name, aliases: [], credential_configured: true, status: "ok", default_template_id: TEMPLATE.id}],
    templates: [TEMPLATE], attachments: [], build_artifacts: [], messages: MESSAGES,
  });
  if (path === "/api/v1/mail-messages" && method === "POST") {
    window.__sentPayload = body;
    const message = {id: "mail_message_1", task_id: TASK.id, account_id: ACCOUNT.id, template_id: TEMPLATE.id, template_version: 1, source: "owner", idempotency_key: body.idempotency_key, from_address: ACCOUNT.address, from_name: ACCOUNT.display_name, to: body.to, cc: body.cc, bcc: body.bcc, subject: "提测通知", body_text: "请测试。", content_revision: 1, approval_digest: "sha256:abcdef1234567890", status: "pending_approval", created_at: NOW, updated_at: NOW};
    MESSAGES = [{message, attachments: [], deliveries: []}];
    return json({ok: true, message, attachments: [], deduplicated: false}, 201);
  }
  if (path === "/api/v1/mail-messages/mail_message_1/approve" && method === "POST") {
    window.__approved = true;
    MESSAGES = [{message: {...MESSAGES[0].message, status: "queued"}, attachments: [], deliveries: [{id: "mail_delivery_1", message_id: "mail_message_1", generation: 0, account_id: ACCOUNT.id, state: "queued", attempts: 0, available_at: NOW, created_at: NOW, updated_at: NOW}]}];
    return json({ok: true, message: MESSAGES[0].message, delivery: MESSAGES[0].deliveries[0]});
  }
  if (path === "/api/v1/mail-messages/mail_message_1") return json({ok: true, ...MESSAGES[0]});
  if (path.startsWith("/api/v1/mail-messages/mail_message_1/attempts")) return json({ok: true, deliveries: []});
  if (path.startsWith("/api/v1/mail-messages/mail_message_1/download-links")) return json({ok: true, links: []});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
if (!sessionStorage.getItem("aha2.navigation")) {
  sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "hardware"}));
}
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
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
      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      const file = name.startsWith("/vendor/") ? resolve(root, "web", name.slice(1)) : resolve(root, "web/dist", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("dialog", dialog => void dialog.accept());

  // #hardware must redirect to the resources page and keep the hardware tab.
  await page.goto(`${base}/#hardware`, {waitUntil: "networkidle"});
  await page.waitForSelector(".resources-view", {timeout: 15000});
  assert.match(await page.locator(".resource-tabs").innerText(), /硬件/);
  assert.match(await page.locator(".resource-tabs").innerText(), /邮件/);
  await page.setViewportSize({width: 390, height: 844});
  assert.equal(await page.locator(".bottom-nav button").count(), 8, "mobile navigation must stay at eight items");
  assert.equal(await page.evaluate(() => location.hash), "#resources?tab=hardware");

  // Mail tab: write-only authorization code, no credential ref rendered.
  await page.click('[data-resources-tab="mail"]');
  await page.waitForSelector(".mail-resources-view");
  // Switching tabs must not rewrite the address bar.
  assert.equal(await page.evaluate(() => location.hash), "#resources?tab=hardware");
  assert.match(await page.locator(".mail-resource-row").first().innerText(), /noreply@example.com/);
  await page.click("#add-mail-account");
  await page.waitForSelector(".mail-resource-dialog[open]");
  const form = page.locator(".mail-dialog-form");
  assert.equal(await form.locator('input[name="authorization_code"]').getAttribute("type"), "password");
  await form.locator('input[name="name"]').fill("新账号");
  await form.locator('input[name="address"]').fill("new@example.com");
  await form.locator('input[name="authorization_code"]').fill("write-only-secret");
  await form.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__createdMailAccount !== null);
  assert.equal((await page.evaluate(() => window.__createdMailAccount)).authorization_code, "write-only-secret");
  await page.waitForFunction(() => document.querySelectorAll(".mail-resource-row").length >= 2);
  assert.doesNotMatch(await page.locator("#app").innerText(), /write-only-secret/);
  assert.doesNotMatch(await page.locator("#app").innerText(), /credential_ref/);
  assert.deepEqual(errors, []);

  // Task view: the toolbar mail entry is the only entry point. Submitting
  // produces a pending approval; the timeline stays informational while the
  // mail tool panel owns approval actions.
  await page.goto(base, {waitUntil: "networkidle"});
  await page.evaluate(task => sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: task.id, task})), task);
  await page.reload({waitUntil: "networkidle"});
  await page.waitForSelector(".task-screen", {timeout: 15000});
  assert.equal(await page.locator("#task-send-mail").count(), 0, "header mail icon must be removed");
  await page.setViewportSize({width: 390, height: 844});
  await page.click('[data-task-tool="mail"]');
  await page.waitForSelector(".task-mail-panel");
  await page.waitForSelector("#task-mail-compose:not([disabled])");
  await page.click("#task-mail-compose");
  await page.waitForSelector("#task-mail-compose-dialog[open]");
  const compose = page.locator("#task-mail-compose-dialog");
  const box = await compose.boundingBox();
  assert.ok(box && box.width <= 390, `compose dialog width ${box?.width}`);
  const submitBox = await compose.locator('button[type="submit"]').boundingBox();
  assert.ok(submitBox && submitBox.y + submitBox.height <= 844, "compose footer must stay visible");
  await compose.locator('input[name="to"]').fill("qa@example.com");
  await compose.locator('input[name="bcc"]').fill("audit@example.com");
  await compose.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__sentPayload !== null);
  const sent = await page.evaluate(() => window.__sentPayload);
  assert.deepEqual(sent.to, ["qa@example.com"]);
  assert.deepEqual(sent.bcc, ["audit@example.com"]);
  assert.equal(sent.submit, true);
  await page.waitForFunction(() => document.querySelector('[data-task-tool="mail"] .task-tool-badge')?.textContent === "1");
  await page.waitForSelector(".task-mail-panel [data-mail-card='mail_message_1']");
  assert.match(await page.locator(".task-mail-panel [data-mail-card='mail_message_1']").innerText(), /待审批/);
  // The conversation timeline no longer renders mail cards; the tool panel is
  // the single place to inspect and decide.
  assert.equal(await page.locator("#conversation-list [data-mail-card]").count(), 0);
  const approve = page.locator(".task-mail-panel [data-mail-action='approve']").first();
  assert.equal(await approve.isVisible(), true);
  await approve.click();
  await page.waitForFunction(() => window.__approved === true);
  await page.waitForFunction(() => document.querySelector('[data-task-tool="mail"] .task-tool-badge') === null);
  await page.waitForFunction(() => document.querySelector(".task-mail-panel [data-mail-card='mail_message_1']")?.textContent?.includes("排队中"));
  await page.screenshot({path: resolve(output, "mail-task-mobile.png")});
  assert.deepEqual(errors, []);
  console.log("mail resources page verified");
} finally {
  await browser?.close();
  server.close();
}
