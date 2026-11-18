import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

// Verifies the profile-exclusive browser panel without a real Edge: one Task
// owns every visible tab, can activate/close/navigate/open pages and read
// bookmarks, and the preview keeps the existing frame/generation lifecycle.
const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : {};
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const now = "2026-11-17T09:00:00Z";
const grant = {
  id: "browser_grant_1", task_id: "task-1", profile_id: "browser_profile_1",
  session_id: "browser_session_1", capabilities: ["open_tab", "navigate", "snapshot"],
  expires_at: "2026-11-18T09:00:00Z", created_at: now, updated_at: now, status: "active",
  session: {
    id: "browser_session_1", profile_id: "browser_profile_1", host_id: "local",
    state: "ready", started_at: now, created_at: now, updated_at: now,
  },
};
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
  grants: [grant],
  tabs: [
    {id: "browser_tab_1", session_id: "browser_session_1", grant_id: "browser_grant_1", task_id: "task-1", url: "https://login.dingtalk.com/oauth2/auth?redirect_uri=https%3A%2F%2Fexample.com%2Fcallback&response_type=code&client_id=dingabcdefghijklmnop&scope=openid&state=1234567890abcdef", title: "登录页", origin: "owner", active: true, created_at: now, updated_at: now},
    {id: "browser_tab_2", session_id: "browser_session_1", grant_id: "browser_grant_1", task_id: "task-1", url: "https://example.com/home", title: "首页", origin: "owner", active: true, created_at: now, updated_at: now},
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
window.__mode = "tabs";
window.__released = false;
window.__inUse = false;
window.__transferred = false;
window.__openedTabs = [];
window.__activates = [];
window.__deletes = [];
const HOST_UP = {available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true};
const HOST_DOWN = {available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"};
const STATE_UP = ${JSON.stringify(browserState({available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true}))};
const STATE_DOWN = ${JSON.stringify({...browserState({available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"}), grants: [], tabs: []})};
const GRANT = ${JSON.stringify(grant)};
const currentState = () => {
  if (window.__released) {
    return {...STATE_UP, grants: [], tabs: []};
  }
  if (window.__inUse && !window.__transferred) {
    return {...STATE_UP, grants: [], tabs: [], profiles: STATE_UP.profiles.map(profile => ({
      ...profile, lease_task_id: "task-other", lease_task_title: "另一个 Task",
    }))};
  }
  if (!window.__hostAvailable) return STATE_DOWN;
  const base = window.__mode === "empty" ? {...STATE_UP, tabs: []} : {...STATE_UP};
  return {...base, tabs: base.tabs.concat(window.__openedTabs)};
};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = String(init.method || "GET").toUpperCase();
  if (path === "/api/v1/tasks/task-1/browser") {
    return json({ok: true, browser: currentState()});
  }
  if (path === "/api/v1/tasks/task-1/browser-sessions" && method === "POST") {
    if (window.__inUse && !window.__transferred) {
      return json({error: {code: "browser_profile_in_use", message: "该 Profile 正由其他 Task 独占控制；请先让原 Task 释放，或由 Owner 显式转移控制权"}}, 409);
    }
    return json({ok: true, grant: GRANT, session: GRANT.session});
  }
  if (path === "/api/v1/browser/profiles/browser_profile_1/transfer" && method === "POST") {
    window.__transferred = true;
    window.__inUse = false;
    return json({ok: true, grant: GRANT, session: GRANT.session});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1" && method === "DELETE") {
    window.__released = true;
    return json({ok: true, grant: {...GRANT, status: "revoked"}});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1/bookmarks") {
    return json({ok: true, bookmarks: [{title: "Mail", url: "https://mail.example/", folder: "Bar"}]});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1/tabs" && method === "POST") {
    const payload = JSON.parse(String(init.body || "{}"));
    const tab = {...GRANT, id: "browser_tab_opened", url: payload.url || "https://example.com/opened", title: "打开的页面", origin: "owner", active: true};
    window.__openedTabs.push(tab);
    return json({ok: true, tab});
  }
  if (/\\/api\\/v1\\/browser-grants\\/browser_grant_1\\/tabs\\/browser_tab_[12]\\/activate$/.test(path) && method === "POST") {
    window.__activates.push(path);
    return json({ok: true, tab: {id: path.split("/")[6]}});
  }
  if (method === "DELETE" && /\\/api\\/v1\\/browser-grants\\/browser_grant_1\\/tabs\\/browser_tab_[12]$/.test(path)) {
    window.__deletes.push(path);
    return json({ok: true});
  }
  if (/\\/tabs\\/(?:browser_tab_[12]|browser_tab_opened)\\/frame$/.test(path)) {
    if (window.__staleError && path.includes("browser_tab_1")) {
      window.__staleErrors++;
      await new Promise(resolve => setTimeout(resolve, 1200));
      window.__staleErrorAt = performance.now();
      return json({error: {code: "browser_tab_not_found", message: "标签页已关闭"}}, 404);
    }
    window.__frames++;
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
const mount = async page => {
  await page.evaluate(() => {
    document.querySelector("#desktop-browser-target").innerHTML = "";
    window.__panel = window.mountBrowserTaskPanel({
      container: document.querySelector("#desktop-browser-target"),
      taskID: "task-1",
      notify: () => {},
      onChange: () => {},
    });
  });
};
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1100, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(base, {waitUntil: "networkidle"});

  // Exclusive control: every visible page is listed under the lease.
  await mount(page);
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await waitFor(page, () => window.__frames >= 2);
  const firstFrames = await page.evaluate(() => window.__frames);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /独占控制中/);
  assert.match(await page.locator("#desktop-browser-frame-status").innerText(), /实时画面/);
  assert.equal(await page.locator("[data-browser-tab]").count(), 2);
  assert.match(await page.locator("[data-browser-tab='browser_tab_1']").innerText(), /当前/);
  // Both host tabs are active=true (one visible page per browser window), but
  // the panel previews exactly one page and must therefore label exactly one.
  assert.equal(await page.locator(".desktop-browser-tab-current").count(), 1);
  assert.deepEqual(await page.locator("[data-browser-tab]").evaluateAll(items => items.map(item => item.getAttribute("aria-pressed"))), ["true", "false"]);
  // The compact strip shows title + host; the long OAuth URL stays in `title`.
  const longTab = page.locator("[data-browser-tab='browser_tab_1']");
  assert.doesNotMatch(await longTab.innerText(), /redirect_uri|\?/);
  assert.match(await longTab.locator(".desktop-browser-tab-host").innerText(), /^login\.dingtalk\.com$/);
  assert.match(await longTab.getAttribute("title"), /redirect_uri=/);
  const tabHeights = await page.locator(".desktop-browser-tab-item").evaluateAll(items => items.map(item => item.getBoundingClientRect().height));
  assert.ok(tabHeights.every(height => height < 60), `tab rows should stay compact: ${tabHeights}`);
  assert.equal(await page.evaluate(() => {
    const preview = document.querySelector(".desktop-browser-preview");
    const tabs = document.querySelector("#desktop-browser-tabs");
    return Boolean(preview.compareDocumentPosition(tabs) & Node.DOCUMENT_POSITION_FOLLOWING);
  }), true, "the preview must come before the tab list");

  // Switching tab activates it in the visible browser and keeps the stream.
  await page.click("[data-browser-tab='browser_tab_2']");
  await waitFor(page, () => window.__activates.length === 1);
  await waitFor(page, count => window.__frames > count, {arg: firstFrames});

  // Closing an Owner-opened page closes it under exclusive control.
  await page.click("[data-browser-tab-close='browser_tab_2']");
  await waitFor(page, () => window.__deletes.length === 1);

  // Bookmarks are read-only and open their URL through the normal tab API.
  await page.waitForSelector("#desktop-browser-frame:not([hidden])");
  await page.waitForTimeout(100);
  const previewBeforeDrawer = await page.locator(".desktop-browser-preview").boundingBox();
  await page.click("#desktop-browser-bookmarks-toggle");
  await page.waitForSelector("[data-browser-bookmark]");
  const previewAfterDrawer = await page.locator(".desktop-browser-preview").boundingBox();
  const drawerBox = await page.locator("#desktop-browser-bookmarks").boundingBox();
  assert.equal(previewAfterDrawer.y, previewBeforeDrawer.y, "opening bookmarks must not move the preview");
  assert.equal(previewAfterDrawer.height, previewBeforeDrawer.height, "opening bookmarks must not resize the preview");
  assert.ok(drawerBox.y >= previewAfterDrawer.y + previewAfterDrawer.height, "bookmarks must open below the preview");
  assert.equal(await page.locator("#desktop-browser-bookmarks-toggle").getAttribute("aria-expanded"), "true");
  await waitFor(page, () => document.querySelector("#desktop-browser-bookmarks").contains(document.activeElement));
  await page.keyboard.press("Escape");
  assert.equal(await page.locator("#desktop-browser-bookmarks").isHidden(), true);
  assert.equal(await page.evaluate(() => document.activeElement?.id), "desktop-browser-bookmarks-toggle");
  await page.click("#desktop-browser-bookmarks-toggle");
  await page.waitForSelector("[data-browser-bookmark]");
  assert.match(await page.locator("[data-browser-bookmark]").innerText(), /Mail/);
  await page.click("[data-browser-bookmark]");
  await page.waitForSelector("[data-browser-tab='browser_tab_opened']", {timeout: 5000});

  // Release is explicit and leaves the visible browser open.
  await page.click("#desktop-browser-release");
  await waitFor(page, () => document.querySelector("#desktop-browser-release") === null);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /获取独占控制/);
  await page.evaluate(() => window.__panel());

  // Second Task: attach returns 409, then the Owner can transfer explicitly.
  await page.evaluate(() => {
    window.__released = false;
    window.__inUse = true;
    window.__transferred = false;
    window.__frames = 0;
  });
  await mount(page);
  await page.waitForSelector("#desktop-browser-transfer", {timeout: 5000});
  assert.match(await page.locator("#desktop-browser-status").innerText(), /其他 Task 独占控制/);
  await page.click("#desktop-browser-transfer");
  await waitFor(page, () => document.querySelector("#desktop-browser-release") !== null);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /独占控制中/);
  await page.evaluate(() => window.__panel());

  // Empty tabs: explicit empty state, then open a page under the same lease.
  await page.evaluate(() => {
    window.__mode = "empty";
    window.__inUse = false;
    window.__transferred = false;
    window.__openedTabs = [];
    window.__frames = 0;
  });
  await mount(page);
  await page.waitForSelector("#desktop-browser-open-form");
  assert.match(await page.locator("#desktop-browser-frame-status").innerText(), /还没有网页/);
  const beforeOpen = await page.evaluate(() => window.__frames);
  await page.fill("#desktop-browser-url", "https://example.com/opened");
  await page.click("#desktop-browser-open-form button[type='submit']");
  await page.waitForSelector("[data-browser-tab='browser_tab_opened']", {timeout: 5000});
  await waitFor(page, count => window.__frames > count, {arg: beforeOpen});
  await page.evaluate(() => window.__panel());

  // Missing host: no frame traffic, explicit status, no console errors.
  const framesBeforeHostDown = await page.evaluate(() => window.__frames);
  await page.evaluate(() => {
    window.__mode = "tabs";
    window.__hostAvailable = false;
  });
  await mount(page);
  await page.waitForSelector("#desktop-browser-status");
  await page.waitForTimeout(400);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /浏览器宿主不可用/);
  assert.equal(await page.evaluate(() => window.__frames), framesBeforeHostDown);
  await page.evaluate(() => window.__panel());

  // A late 404 from the previous tab must not hide or stop the new tab.
  await page.evaluate(() => {
    window.__hostAvailable = true;
    window.__staleError = true;
    window.__frames = 0;
  });
  await mount(page);
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
  console.log("browser task panel exclusive preview verified");
} finally {
  await browser?.close();
  server.close();
}
