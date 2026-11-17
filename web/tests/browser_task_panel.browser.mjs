import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

// Verifies the read-only browser preview lifecycle without a real Edge: the
// panel must start polling authorized tabs, keep rendering frames, and stop all
// timer/object-URL activity when the owner disposes it (panel close or mode
// switch). A second mount proves a missing host produces no frame traffic.
const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : {};
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const now = "2026-11-17T09:00:00Z";
const browserState = host => ({
  task_id: "task-1",
  profiles: [{
    id: "browser_profile_1", name: "工作浏览器", browser_kind: "edge", host_id: "local",
    state: "ready", host_available: host.available, created_at: now, updated_at: now,
    session: {
      id: "browser_session_1", profile_id: "browser_profile_1", host_id: "local",
      state: "ready", started_at: now, created_at: now, updated_at: now,
    },
  }],
  grants: [{
    id: "browser_grant_1", task_id: "task-1", profile_id: "browser_profile_1",
    session_id: "browser_session_1", capabilities: ["open_tab", "navigate", "snapshot"],
    expires_at: "2026-11-18T09:00:00Z", created_at: now, updated_at: now, status: "active",
  }],
  tabs: [
    {id: "browser_tab_1", session_id: "browser_session_1", grant_id: "browser_grant_1", task_id: "task-1", url: "https://example.com/login", title: "登录页", created_at: now, updated_at: now},
    {id: "browser_tab_2", session_id: "browser_session_1", grant_id: "browser_grant_1", task_id: "task-1", url: "https://example.com/home", title: "首页", created_at: now, updated_at: now},
  ],
  host,
});

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.__frames = 0;
window.__hostAvailable = true;
window.__staleError = false;
window.__staleErrors = 0;
window.__staleErrorAt = 0;
window.__switchAt = 0;
const HOST_UP = {available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true};
const HOST_DOWN = {available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"};
const STATE_UP = ${JSON.stringify(browserState({available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true}))};
const STATE_DOWN = ${JSON.stringify(browserState({available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"}))};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/tasks/task-1/browser") return json({ok: true, browser: window.__hostAvailable ? STATE_UP : STATE_DOWN});
  if (/\\/tabs\\/browser_tab_[12]\\/frame$/.test(path)) {
    // A late 404 for the previous tab must not hide or stop the new tab.
    if (window.__staleError && path.includes("browser_tab_1")) {
      window.__staleErrors++;
      await new Promise(resolve => setTimeout(resolve, 1200));
      window.__staleErrorAt = performance.now();
      return json({error: {code: "browser_tab_not_found", message: "标签页已关闭"}}, 404);
    }
    window.__frames++;
    // Two different payloads so the adaptive interval sees a change.
    const body = new Uint8Array([0xff, 0xd8, 0xff, window.__frames % 2, 0xff, 0xd9]);
    return new Response(body, {status: 200, headers: {"Content-Type": "image/jpeg"}});
  }
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body>
  <aside id="desktop-tool"><div id="desktop-browser-target"></div></aside>
  <script src="/fixture.js"></script><script type="module" src="/boot.js"></script>
</body></html>`;
const boot = `import {mountBrowserTaskPanel} from "/browser_task_panel.js"; window.mountBrowserTaskPanel = mountBrowserTaskPanel;`;
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
    } else if (name === "/boot.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(boot);
    } else {
      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      const file = resolve(root, "web", "build", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolvePromise => server.listen(0, "127.0.0.1", resolvePromise));
const base = `http://127.0.0.1:${server.address().port}`;

let browser;
const waitFor = async (page, predicate, options = {}) => {
  const timeout = options.timeout ?? 5000;
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await page.evaluate(predicate, options.arg)) return;
    await page.waitForTimeout(50);
  }
  throw new Error("timed out waiting for the browser panel condition");
};
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1100, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(base, {waitUntil: "networkidle"});

  // Live preview: attach and watch frames arrive for the first tab.
  await page.evaluate(() => {
    window.__panel = window.mountBrowserTaskPanel({
      container: document.querySelector("#desktop-browser-target"),
      taskID: "task-1",
      notify: () => {},
      onChange: () => {},
    });
  });
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await waitFor(page, () => window.__frames >= 2);
  const firstFrames = await page.evaluate(() => window.__frames);
  assert.match(await page.locator("#desktop-browser-frame-status").innerText(), /实时画面/);
  assert.equal(await page.locator("[data-browser-tab]").count(), 2);
  assert.match(await page.locator("[data-browser-tab='browser_tab_1']").innerText(), /登录页/);

  // Switching tabs keeps the stream alive.
  await page.click("[data-browser-tab='browser_tab_2']");
  await waitFor(page, count => window.__frames > count, {arg: firstFrames});

  // Dispose (panel close / mode switch) must stop the timer loop.
  const before = await page.evaluate(() => { window.__panel(); return window.__frames; });
  await page.waitForTimeout(1000);
  const after = await page.evaluate(() => window.__frames);
  assert.ok(after - before <= 1, `frames kept arriving after dispose: ${before} -> ${after}`);

  // Missing host: no frame traffic, explicit status, no console errors.
  await page.evaluate(() => {
    window.__hostAvailable = false;
    document.querySelector("#desktop-browser-target").innerHTML = "";
    window.__panel = window.mountBrowserTaskPanel({
      container: document.querySelector("#desktop-browser-target"),
      taskID: "task-1",
      notify: () => {},
      onChange: () => {},
    });
  });
  await page.waitForSelector("#desktop-browser-status");
  await page.waitForTimeout(400);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /浏览器宿主不可用/);
  assert.equal(await page.evaluate(() => window.__frames), after);
  await page.evaluate(() => window.__panel());

  // A late 404 from the previous tab must not hide or stop the new tab.
  await page.evaluate(() => {
    window.__hostAvailable = true;
    window.__staleError = true;
    window.__frames = 0;
    document.querySelector("#desktop-browser-target").innerHTML = "";
    window.__panel = window.mountBrowserTaskPanel({
      container: document.querySelector("#desktop-browser-target"),
      taskID: "task-1",
      notify: () => {},
      onChange: () => {},
    });
  });
  await page.waitForSelector("[data-browser-tab='browser_tab_2']", {timeout: 5000});
  await page.evaluate(() => {
    document.querySelector("[data-browser-tab='browser_tab_2']")
      .addEventListener("click", () => { window.__switchAt = performance.now(); }, {capture: true});
  });
  await page.click("[data-browser-tab='browser_tab_2']");
  const switchedFrames = await page.evaluate(() => window.__frames);
  await page.waitForTimeout(1800);
  const staleFrames = await page.evaluate(() => window.__frames);
  assert.equal(await page.evaluate(() => window.__staleErrors), 1, "the stale 404 should have fired");
  assert.ok(await page.evaluate(() => window.__staleErrorAt > window.__switchAt),
    "the 404 arrived before the tab switch; the late-error case was not exercised");
  assert.ok(staleFrames > switchedFrames + 2,
    `new tab stream stopped after the stale error: ${switchedFrames} -> ${staleFrames}`);
  const staleStatus = await page.locator("#desktop-browser-frame-status").innerText();
  assert.match(staleStatus, /实时画面/);
  assert.doesNotMatch(staleStatus, /画面暂不可用/);
  await page.evaluate(() => window.__panel());
  assert.deepEqual(errors, []);
  console.log("browser task panel preview verified");
} finally {
  await browser?.close();
  server.close();
}
