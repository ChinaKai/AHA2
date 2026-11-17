import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

// Regression for the 0.5.532 Resources page: the browser tab's resource state
// was missing from the loader state map, so `browserResourcesData()` threw
// during render. The page (and any navigation into it) never appeared. This
// test boots directly on #resources?tab=browser, keeps the profile read
// pending, and verifies the page opens, fails closed without a host, and that
// resource actions produce visible results.
const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
// Dependencies stay in .tools/web-validation; a machine without that directory
// can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE at a local install.
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

const now = "2026-11-17T09:00:00Z";
const hardware = {
  id: "hardware_1", description: "主控板串口", mode: "serial", access: "read_write",
  serial: {device: "COM3", baudrate: 115200},
  network: {host: "", port: 0, protocol: "telnet", ssh_auth: "auto"},
  username: "", password_configured: false, binding_count: 0,
  created_at: now, updated_at: now,
};
// An older/partial row without serial or network blocks must render as
// "not configured" instead of crashing the whole Resources page.
const partialHardware = {
  id: "hardware_2", description: "旧版资源", mode: "serial", access: "read_only",
  username: "", password_configured: false, binding_count: 0,
  created_at: now, updated_at: now,
};
const profile = {
  id: "browser_profile_1", name: "工作浏览器", browser_kind: "edge", host_id: "local",
  state: "idle", host_available: false, created_at: now, updated_at: now,
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const NOW = ${JSON.stringify(now)};
const HARDWARE = ${JSON.stringify(hardware)};
const PARTIAL_HARDWARE = ${JSON.stringify(partialHardware)};
const PROFILE = ${JSON.stringify(profile)};
// The profile read stays pending until the test releases it, so the loading
// state is deterministic instead of a timing guess.
window.__profilesPending = true;
window.__profilesGate = new Promise(resolve => {
  window.__releaseProfiles = () => { window.__profilesPending = false; resolve(json({ok: true, profiles: [PROFILE]})); };
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {}});
  if (path === "/api/v1/hardware-resources" && method === "GET") return json({resources: [HARDWARE, PARTIAL_HARDWARE]});
  // No packaged browser host: the server reports an unavailable host, not a
  // hanging request. The page must still open and explain why without asking
  // the owner to configure a URL.
  if (path === "/api/v1/browser/host") return json({ok: true, host: {
    available: false, host_id: "local", edge_available: false,
    code: "browser_host_start_failed",
    error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志",
  }});
  if (path === "/api/v1/browser/profiles" && method === "GET") return window.__profilesGate;
  if (path === "/api/v1/browser/profiles/browser_profile_1/login-window" && method === "POST") {
    return json({error: {code: "browser_host_unavailable", message: "浏览器宿主不可用，请确认 host 进程已启动且仅监听本机"}}, 503);
  }
  if (path === "/api/v1/serial-ports") return json({ports: []});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
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
      const file = name.startsWith("/vendor/") ? resolve(root, "web", name.slice(1)) : resolve(root, "web/build", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("dialog", dialog => void dialog.accept());

  // A direct deep link used to crash the boot render, leaving the AHA2 boot
  // spinner forever and issuing no Resources requests.
  await page.goto(`${base}/#resources?tab=browser`, {waitUntil: "networkidle"});
  await page.waitForSelector(".resources-view", {timeout: 15000});
  await page.waitForSelector(".browser-resources-view", {timeout: 15000});
  assert.equal(await page.evaluate(() => location.hash), "#resources?tab=browser");
  assert.equal(await page.locator(".chunk-state").count(), 0, "resources chunk must not stay in a loading/error card");

  // Host not configured: while the profile read is pending the banner says it
  // is still reading instead of claiming there is no status at all.
  const banner = page.locator(".browser-host-banner");
  assert.match(await banner.innerText(), /正在读取浏览器宿主状态/);

  // The profile read is still in flight: a deliberate status replaces the
  // misleading "no profiles yet" empty state, and the rest of the page works.
  await page.waitForSelector(".browser-resources-view [aria-busy='true']");
  assert.match(await page.locator(".browser-resources-view").innerText(), /正在读取浏览器资源/);
  await page.click('[data-resources-tab="hardware"]');
  await page.waitForSelector(".hardware-resources-view");
  assert.equal(await page.locator(".hardware-resource-row").count(), 2);
  assert.match(await page.locator(".hardware-resource-row").first().innerText(), /主控板串口/);
  // Missing serial/network: readable fallback, not a render crash.
  assert.match(await page.locator('[data-hardware-resource="hardware_2"]').innerText(), /未配置端点/);
  await page.click('[data-resources-tab="browser"]');
  await page.waitForSelector(".browser-resources-view");

  // Releasing the read replaces the loading card with the real profile and the
  // explicit host-unavailable banner; it must never suggest a fallback.
  await page.evaluate(() => window.__releaseProfiles());
  await page.waitForSelector('[data-browser-profile="browser_profile_1"]');
  assert.match(await page.locator('[data-browser-profile="browser_profile_1"]').innerText(), /工作浏览器/);
  assert.equal(await page.locator(".browser-resources-view [aria-busy='true']").count(), 0);
  assert.match(await banner.innerText(), /浏览器宿主不可用/);
  assert.match(await banner.innerText(), /自动启动失败/);
  assert.doesNotMatch(await banner.innerText(), /AHA2_BROWSER_HOST_URL/);

  // A failed action must surface an error banner immediately, not disappear
  // without feedback.
  await page.click('[data-browser-profile-login="browser_profile_1"]');
  await page.waitForSelector(".banner.error");
  assert.match(await page.locator(".banner.error").innerText(), /浏览器宿主不可用/);

  // Clicking a hardware resource opens its editor instead of doing nothing.
  await page.click('[data-resources-tab="hardware"]');
  await page.waitForSelector(".hardware-resources-view");
  await page.click('[data-hardware-resource-edit="hardware_1"]');
  const dialog = page.locator("dialog.hardware-resource-dialog[open]");
  await dialog.waitFor();
  assert.match(await dialog.innerText(), /编辑硬件资源/);
  await dialog.locator("[data-close]").first().click();
  await page.waitForSelector("dialog.hardware-resource-dialog", {state: "detached"});

  // Rapid repeated navigation into Resources used to re-enter the crashing
  // render on every click and leave the page stuck on the previous view. Each
  // click now re-renders in place, and the last one still lands on Resources.
  await page.click('[data-view="projects"]');
  await page.waitForSelector('button[data-view="projects"].active');
  await page.evaluate(() => {
    for (let index = 0; index < 8; index++) {
      // render() replaces the nav after each click, so re-query every time.
      document.querySelector('button[data-view="resources"]')?.click();
    }
  });
  await page.waitForSelector(".resources-view");
  // The nav renders twice (sidebar and mobile bar); every copy must be active.
  const activeResourcesNav = await page.locator('button[data-view="resources"].active').count();
  const allResourcesNav = await page.locator('button[data-view="resources"]').count();
  assert.ok(activeResourcesNav >= 1 && activeResourcesNav === allResourcesNav);
  assert.equal(await page.locator(".chunk-state").count(), 0, "repeated clicks must not leave a loading/error card");
  assert.match(await page.locator(".resources-view").innerText(), /浏览器/);

  await page.screenshot({path: resolve(output, "resources-page.png")});
  assert.deepEqual(errors, []);
  console.log("resources page verified");
} finally {
  await browser?.close();
  server.close();
}
