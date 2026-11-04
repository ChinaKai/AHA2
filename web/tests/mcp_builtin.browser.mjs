// Optional browser verification for the built-in channel MCP entry: it must be
// visible in the MCP list, marked as auto-injected, and have no edit/toggle/
// delete actions, while remaining inspectable through the detail dialog.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));

const now = new Date().toISOString();
const pet = {
  id: "mcp_pet", name: "宠物（内置）", description: "AHA2 内置宠物工具", transport: "builtin",
  enabled: true, builtin: true, health: "ok", tools: [{name: "pet_say", description: "让宠物说话"}],
  created_at: now, updated_at: now,
};
const channel = {
  id: "mcp_channel", name: "渠道（内置）",
  description: "群聊路由的 Turn 自动注入；提供渠道回复、结束回复和 Owner 权限申请工具。",
  transport: "builtin", enabled: true, builtin: true, auto_inject: true, health: "ok",
  tools: [
    {name: "channel_reply", description: "回复触发本 Turn 的渠道消息"},
    {name: "channel_reply_decision", description: "本 Turn 无需回复时结束"},
    {name: "channel_request_permission", description: "创建 Owner 权限申请"},
  ],
  created_at: now, updated_at: now,
};
const user = {
  id: "mcp_user", name: "Filesystem", description: "本地文件工具", transport: "http",
  url: "https://mcp.example.test/mcp", enabled: true, builtin: false, health: "ok",
  tools: [{name: "read_file", description: "读取文件"}], created_at: now, updated_at: now,
};
const servers = [pet, channel, user];

const bootstrap = `
const servers = ${JSON.stringify(servers)};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/settings/security") return json({security: {validate_origin: true, access_scope: "lan", startup_override: false}});
  if (path === "/api/v1/settings/network") return json({network: {listen_address: "", startup_listen_address: "0.0.0.0:8766", restart_required: false}});
  if (path === "/api/v1/settings/backend") return json({backend: {idle_timeout_seconds: 300, turn_timeout_seconds: 3600}});
  if (path === "/api/v1/settings/agent-api") return json({agent_api: {url: "", allow_insecure: false, effective_url: "http://127.0.0.1:8766", effective_allow_insecure: false}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/mcp-servers") return json({ok: true, servers});
  if (path === "/api/v1/mcp-servers/mcp_channel") return json({ok: true, server: servers[1]});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon"};
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
      return;
    }
    if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
      return;
    }
    const file = resolve(root, "web", "dist", name.slice(1));
    const bytes = await readFile(file);
    response.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    response.end(bytes);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=advanced]")?.click());
  await page.waitForSelector('[data-view=mcp]', {timeout: 15000});
  await page.evaluate(() => document.querySelector("[data-view=mcp]")?.click());
  await page.waitForSelector("#mcp-form", {timeout: 15000});

  const channelRow = page.locator("tr", {hasText: "渠道（内置）"});
  await channelRow.waitFor({timeout: 15000});
  assert.match(await channelRow.textContent(), /内置/);
  assert.match(await channelRow.textContent(), /自动注入/);
  assert.equal(await channelRow.locator("[data-detail-mcp]").count(), 1);
  assert.equal(await channelRow.locator("[data-toggle-mcp]").count(), 0);
  assert.equal(await channelRow.locator("[data-delete-mcp]").count(), 0);

  const userRow = page.locator("tr", {hasText: "Filesystem"});
  assert.equal(await userRow.locator("[data-toggle-mcp]").count(), 1);
  assert.equal(await userRow.locator("[data-delete-mcp]").count(), 1);

  await channelRow.locator("[data-detail-mcp]").click();
  await page.waitForSelector("#mcp-detail-dialog[open]", {timeout: 5000});
  const detail = await page.locator("#mcp-detail-body").textContent();
  assert.match(detail, /由当前 Turn 动态注入/);
  assert.match(detail, /channel_reply/);
  assert.match(detail, /channel_reply_decision/);
  assert.match(detail, /channel_request_permission/);
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
console.log("built-in channel MCP browser check passed");
