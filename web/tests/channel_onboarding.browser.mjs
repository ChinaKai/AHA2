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

const bootstrap = `
window.__patch = null;
window.__onboard = null;
window.__resetMenus = null;
let channelRevision = 7;
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const instance = () => ({
  id: "channel-1", plugin_id: "feishu", provider_key: "feishu", owner_id: "owner-1", runtime_device_id: "device-1",
  name: "团队飞书", status: "ready", effective_availability: "available", revision: channelRevision,
  credential_configured: true, owner_bound: true, host_project_id: "project-1", host_workspace_id: "workspace-1",
  config: {}, created_at: "2026-10-22T00:00:00Z", updated_at: "2026-10-22T00:00:00Z"
});
const options = {
  mode: "existing_app", permission_catalog_version: 1,
  permissions: [
    {id: "messages_core", name: "基础消息与机器人身份", description: "基础权限", required: true, enabled: true},
    {id: "group_messages", name: "群聊消息", description: "群聊权限", required: false, enabled: true},
    {id: "media", name: "图片与文件", description: "附件权限", required: false, enabled: false},
    {id: "cards", name: "交互卡片与菜单", description: "卡片权限", required: false, enabled: true},
  ],
};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/channel-providers") return json({providers: [{id: "feishu", provider_key: "feishu", display_name: "飞书", package_version: "1.1.3", install_state: "installed", enabled: true, available: true, revision: 1}]});
  if (path === "/api/v1/channel-instances") return json({instances: [instance()]});
  if (path === "/api/v1/channel-instances/channel-1/onboarding-options") return json({options});
  if (path === "/api/v1/channel-instances/channel-1/runtime-menus/reset" && method === "POST") {
    window.__resetMenus = {revision: channelRevision};
    channelRevision++;
    return json({command: {id: "reset-menu-command", kind: "reset_menu", state: "pending"}}, 202);
  }
  if (path === "/api/v1/channel-instances/channel-1" && method === "PATCH") {
    window.__patch = JSON.parse(init.body || "{}");
    channelRevision++;
    return json({instance: {...instance(), config: window.__patch.config || {}}});
  }
  if (path === "/api/v1/channel-instances/channel-1/onboarding-sessions" && method === "POST") {
    window.__onboard = JSON.parse(init.body || "{}");
    return json({onboarding: {id: "onboarding-1", instance_id: "channel-1", mode: "existing_app", status: "pending", step: "starting", expires_at: "2026-10-22T01:00:00Z"}}, 202);
  }
  if (path === "/api/v1/channel-onboarding-sessions/onboarding-1") return json({onboarding: {id: "onboarding-1", instance_id: "channel-1", mode: "existing_app", status: "qr_ready", step: "awaiting_scan", verification_url: "https://accounts.feishu.cn/qr", expires_at: "2026-10-22T01:00:00Z"}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "Local", read_only: false}]});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/providers") return json({providers: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path === "/api/v1/knowledge") return json({knowledge: [], proposals: [], has_more: false});
  if (path === "/api/v1/knowledge/libraries") return json({libraries: []});
  if (path === "/api/v1/knowledge/indexes") return json({indexes: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "channels"}));
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
  page.on("dialog", dialog => dialog.accept());
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('[data-channel-onboard="channel-1"]', {timeout: 15000});
  await page.click('[data-channel-onboard="channel-1"]');
  await page.waitForSelector("#channel-onboarding-setup[open]");
  assert.equal(await page.locator('input[value="messages_core"]').isDisabled(), true);
  assert.equal(await page.locator('input[value="group_messages"]').isDisabled(), true);
  assert.equal(await page.locator('input[value="group_messages"]').isChecked(), true);
  assert.equal(await page.locator('input[value="media"]').isDisabled(), false);
  assert.equal(await page.locator('input[value="media"]').isChecked(), false);
  assert.equal(await page.locator('input[name="channel_runtime_menu"]').count(), 0);
  assert.equal(await page.locator("[data-reset-runtime-menus]").count(), 1);
  assert.match(await page.locator("[data-onboarding-confirm]").innerText(), /更新权限/);
  await page.click("[data-reset-runtime-menus]");
  await page.waitForFunction(() => window.__resetMenus !== null);
  assert.equal((await page.evaluate(() => window.__resetMenus)).revision, 7);

  await page.waitForSelector('[data-channel-onboard="channel-1"]');
  await page.click('[data-channel-onboard="channel-1"]');
  await page.waitForSelector("#channel-onboarding-setup[open]");
  await page.locator('input[value="media"]').check();
  assert.match(await page.locator("[data-onboarding-confirm]").innerText(), /更新权限/);
  await page.screenshot({path: resolve(output, "channel-onboarding-options.png")});
  await page.click("[data-onboarding-confirm]");
  await page.waitForFunction(() => window.__onboard !== null);
  const onboarding = await page.evaluate(() => window.__onboard);
  assert.deepEqual(onboarding.permission_ids, ["messages_core", "group_messages", "media", "cards"]);
  assert.equal(Object.hasOwn(onboarding, "runtime_menu_keys"), false);
  await page.waitForSelector("#channel-onboarding-dialog[open]");
  assert.match(await page.locator("#channel-onboarding-dialog").innerText(), /扫码更新飞书权限/);
  assert.deepEqual(errors, []);

  await page.setViewportSize({width: 390, height: 844});
  await page.locator("#channel-onboarding-dialog [data-onboarding-close]").click();
  await page.click('[data-channel-onboard="channel-1"]');
  await page.waitForSelector("#channel-onboarding-setup[open]");
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
  await page.screenshot({path: resolve(output, "channel-onboarding-options-mobile.png")});
  console.log("channel onboarding options verified");
} finally {
  await browser?.close();
  server.close();
}
