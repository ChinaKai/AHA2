// Optional browser verification for the language preference: the real bundle
// must load the saved preference, switch the shell without a reload, and send
// the new value back through the settings API.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
let language = "zh-CN";
window.__languagePuts = [];
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general" && method === "GET") return json({ok: true, general: {language}});
  if (path === "/api/v1/settings/general" && method === "PUT") {
    const body = JSON.parse(init.body || "{}");
    language = body.language;
    window.__languagePuts.push(body);
    return json({ok: true, general: {language}});
  }
  if (path === "/api/v1/settings/security") return json({security: {validate_origin: true, access_scope: "lan", startup_override: false}});
  if (path === "/api/v1/settings/network") return json({network: {listen_address: "", startup_listen_address: "0.0.0.0:8766", restart_required: false}});
  if (path === "/api/v1/settings/backend") return json({backend: {idle_timeout_seconds: 300, turn_timeout_seconds: 3600}});
  if (path === "/api/v1/settings/agent-api") return json({agent_api: {url: "", startup_url: "http://127.0.0.1:8766", effective_url: "http://127.0.0.1:8766"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
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
    const file = resolve(root, "web", "build", name.slice(1));
    const data = await readFile(file);
    response.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    response.end(data);
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
  assert.equal(await page.locator("[data-view=projects] span").first().textContent(), "项目");
  await page.evaluate(() => document.querySelector("[data-view=advanced]")?.click());
  await page.waitForSelector("#language-setting", {timeout: 15000});
  await page.selectOption("#language-setting", "en-US");
  await page.waitForFunction(() => document.querySelector("[data-view=projects] span")?.textContent === "Projects", null, {timeout: 15000});
  assert.equal(await page.locator("[data-view=advanced] span").first().textContent(), "Advanced");
  assert.match(await page.locator("h1").first().textContent(), /Advanced settings/);
  assert.deepEqual(await page.evaluate(() => window.__languagePuts), [{language: "en-US"}]);
  assert.equal(await page.evaluate(() => document.documentElement.lang), "en-US");
  await page.evaluate(async () => {
    const mod = await import("/i18n.js");
    const probe = document.createElement("div");
    probe.id = "i18n-probe";
    probe.innerHTML = '<button>保存</button><span title="刷新">项目</span><input placeholder="输入命令或数据"><input placeholder="例如：本地开发流程"><input placeholder="支持 Markdown 格式"><span class="feishu">飞书</span><span>正在连接服务…</span><div class="markdown-body">保存</div>';
    document.body.appendChild(probe);
    const bootProbe = document.createElement("div");
    bootProbe.id = "boot-i18n-probe";
    bootProbe.innerHTML = "<small>正在连接服务…</small>";
    document.body.appendChild(bootProbe);
    mod.applyLocale("en-US");
    mod.translateUI(probe);
  });
  assert.equal(await page.locator("#i18n-probe button").textContent(), "Save");
  assert.equal(await page.locator("#i18n-probe span[title]").getAttribute("title"), "Refresh");
  assert.equal(await page.locator("#i18n-probe span[title]").textContent(), "Project");
  assert.equal(await page.locator("#i18n-probe .markdown-body").textContent(), "保存");
  assert.equal(await page.locator("#i18n-probe input").nth(0).getAttribute("placeholder"), "Enter a command or data");
  assert.equal(await page.locator("#i18n-probe input").nth(1).getAttribute("placeholder"), "e.g. Local development workflow");
  assert.equal(await page.locator("#i18n-probe input").nth(2).getAttribute("placeholder"), "Markdown supported");
  assert.equal(await page.locator("#i18n-probe .feishu").textContent(), "feishu");
  assert.equal(await page.locator("#i18n-probe span").nth(2).textContent(), "Connecting to service…");
  assert.equal(await page.locator("#boot-i18n-probe small").textContent(), "Connecting to service…");
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
console.log("i18n browser check passed");
