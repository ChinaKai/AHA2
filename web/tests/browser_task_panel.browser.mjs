import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

// Verifies the split browser panel without a real Edge: the targets popover
// only picks a profile, while the main panel owns the Edge-style tab strip,
// address bar, horizontal bookmark bar and read-only preview. A profile with no
// http(s) page gets AHA's own /browser-newtab start page, never about:blank.
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
const bookmarks = [
  {title: "Mail", url: "https://mail.example/", folder: "Bar"},
  {title: "文档中心", url: "https://docs.example/very/long/path?token=abcdefghijklmnop&redirect=https%3A%2F%2Fexample.com", folder: ""},
  {title: "发布面板", url: "https://release.example/deploy?environment=production&region=ap-east-1", folder: ""},
  {title: "监控", url: "https://monitor.example/dashboard?range=last-24h&filters=service%3Dapi", folder: ""},
  {title: "日志", url: "https://logs.example/search?query=level%3Aerror&from=now-1h", folder: ""},
  {title: "工单", url: "https://tickets.example/queue?assignee=owner&status=open", folder: ""},
];

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
window.__attached = false;
window.__inUse = false;
window.__transferred = false;
window.__opened = 0;
window.__openedTabs = [];
window.__activates = [];
window.__deletes = [];
window.__opens = [];
window.__granted = 0;
window.__openTargets = 0;
window.__openDelay = 0;
window.__grantDeletes = 0;
const HOST_UP = {available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true};
const HOST_DOWN = {available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"};
const STATE_UP = ${JSON.stringify(browserState({available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true}))};
const STATE_DOWN = ${JSON.stringify({...browserState({available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"}), grants: [], tabs: []})};
const GRANT = ${JSON.stringify(grant)};
const BOOKMARKS = ${JSON.stringify(bookmarks)};
const currentState = () => {
  if (window.__released || (window.__mode === "detached" && !window.__attached)) {
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
const newTab = (url, title) => {
  window.__opened += 1;
  return {id: "browser_tab_opened_" + window.__opened, session_id: "browser_session_1", grant_id: "browser_grant_1",
    task_id: "task-1", url, title, origin: "owner", active: true, created_at: ${JSON.stringify(now)}, updated_at: ${JSON.stringify(now)}};
};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = String(init.method || "GET").toUpperCase();
  if (path === "/api/v1/tasks/task-1/browser") return json({ok: true, browser: currentState()});
  if (path === "/api/v1/tasks/task-1/browser-sessions" && method === "POST") {
    window.__attached = true;
    window.__released = false;
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
    window.__grantDeletes++;
    return json({ok: true, grant: {...GRANT, status: "revoked"}});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1/bookmarks") {
    return json({ok: true, bookmarks: BOOKMARKS});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1/tabs" && method === "POST") {
    const payload = JSON.parse(String(init.body || "{}"));
    window.__opens.push(payload.url);
    if (window.__openDelay) await new Promise(resolve => setTimeout(resolve, window.__openDelay));
    const tab = newTab(payload.url || "https://example.com/opened", "打开的页面");
    window.__openedTabs.push(tab);
    return json({ok: true, tab});
  }
  if (/\\/api\\/v1\\/browser-grants\\/browser_grant_1\\/tabs\\/[a-z0-9_]+\\/activate$/.test(path) && method === "POST") {
    window.__activates.push(path);
    return json({ok: true, tab: {id: path.split("/")[6]}});
  }
  if (method === "DELETE" && /\\/api\\/v1\\/browser-grants\\/browser_grant_1\\/tabs\\/[a-z0-9_]+$/.test(path)) {
    window.__deletes.push(path);
    return json({ok: true});
  }
  if (/\\/tabs\\/[a-z0-9_]+\\/frame$/.test(path)) {
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
  <aside id="desktop-tool">
    <div id="desktop-browser-target"></div>
    <div id="desktop-browser-control"></div>
  </aside>
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
    } else if (name === "/browser-newtab") {
      response.setHeader("Content-Type", "text/html");
      response.end(await readFile(resolve(root, "web/build/browser-newtab.html")));
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name === "/boot.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(boot);
    } else {
      if (!/^\/(?:vendor\/)?[a-z0-9_-]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
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
    document.querySelector("#desktop-browser-control").innerHTML = "";
    window.__panel = window.mountBrowserTaskPanel({
      container: document.querySelector("#desktop-browser-control"),
      selector: document.querySelector("#desktop-browser-target"),
      taskID: "task-1",
      notify: () => {},
      onChange: () => {},
      onOpenTargets: () => { window.__openTargets++; },
      onGranted: () => { window.__granted++; },
    });
  });
};
const reset = async (page, values) => {
  await page.evaluate(next => { Object.assign(window, next); }, values);
  await mount(page);
};
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1100, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(base, {waitUntil: "networkidle"});

  // 1. The target popover only contains the profile picker and one button.
  await mount(page);
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await waitFor(page, () => window.__frames >= 2);
  const firstFrames = await page.evaluate(() => window.__frames);
  const selector = page.locator("#desktop-browser-target");
  assert.equal(await selector.locator("#desktop-browser-profile").count(), 1);
  assert.equal(await selector.locator("#desktop-browser-refresh").count(), 1);
  assert.equal(await selector.locator("#desktop-browser-switch").innerText(), "切换到所选目标");
  for (const forbidden of ["#desktop-browser-frame", "[data-browser-tab]", "#desktop-browser-url", "#desktop-browser-bookmarks", "#desktop-browser-open-form"]) {
    assert.equal(await selector.locator(forbidden).count(), 0, `target popover must not render ${forbidden}`);
  }
  assert.match(await selector.locator("#desktop-browser-status").innerText(), /独占控制中/);

  // The main panel owns the Edge-style surfaces.
  const main = page.locator("#desktop-browser-control");
  assert.equal(await main.locator("#desktop-browser-tabs").count(), 1);
  assert.equal(await main.locator("#desktop-browser-open-form").count(), 1);
  assert.equal(await main.locator("#desktop-browser-bookmarkbar").count(), 1);
  assert.equal(await main.locator(".desktop-browser-preview").count(), 1);
  assert.match(await main.locator("#desktop-browser-frame-status").innerText(), /实时画面/);

  // Edge order: tab strip, address bar, bookmark bar, preview.
  const order = await page.evaluate(() => {
    const box = selector => document.querySelector(selector).getBoundingClientRect();
    return {
      tabs: box("#desktop-browser-tabs").top,
      address: box("#desktop-browser-open-form").top,
      bookmarks: box("#desktop-browser-bookmarkbar").top,
      preview: box(".desktop-browser-preview").top,
    };
  });
  assert.ok(order.tabs < order.address && order.address < order.bookmarks && order.bookmarks < order.preview, JSON.stringify(order));

  // 2. Two host tabs may be active=true, but the UI highlights exactly one.
  assert.equal(await page.locator("[data-browser-tab]").count(), 2);
  assert.equal(await page.locator(".desktop-browser-tab.active").count(), 1);
  assert.equal(await page.locator('[aria-current="page"]').count(), 1);
  assert.deepEqual(await page.locator("[data-browser-tab]").evaluateAll(items => items.map(item => item.getAttribute("aria-pressed"))), ["true", "false"]);

  // The tab strip is horizontal, compact, and never renders the full URL.
  const longTab = page.locator("[data-browser-tab='browser_tab_1']");
  assert.doesNotMatch(await longTab.innerText(), /redirect_uri|\?/);
  assert.match(await longTab.locator(".desktop-browser-tab-origin").innerText(), /Owner/);
  assert.match(await longTab.getAttribute("title"), /redirect_uri=/);
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector("#desktop-browser-tabs")).overflowX), "auto");
  const tabHeights = await page.locator(".desktop-browser-tab-item").evaluateAll(items => items.map(item => item.getBoundingClientRect().height));
  assert.ok(tabHeights.every(height => height < 44), `tab rows should stay compact: ${tabHeights}`);

  // 3. The bookmark bar is horizontal: folder chip + entries, no long URL.
  await page.waitForSelector("[data-browser-bookmark-folder]");
  const bookmarkBar = page.locator("#desktop-browser-bookmarks");
  assert.equal(await page.locator("[data-browser-bookmark-folder='Bar']").count(), 1);
  assert.match(await bookmarkBar.innerText(), /Mail|文档中心/);
  assert.doesNotMatch(await bookmarkBar.innerText(), /token=|redirect=|environment=/);
  const barHeights = await page.evaluate(() => {
    const bar = document.querySelector("#desktop-browser-bookmarkbar").getBoundingClientRect();
    const preview = document.querySelector(".desktop-browser-preview").getBoundingClientRect();
    return {bar: bar.height, preview: preview.height};
  });
  assert.ok(barHeights.bar < 60, `bookmark bar must stay one horizontal row: ${JSON.stringify(barHeights)}`);
  assert.ok(barHeights.preview > barHeights.bar, `the preview must remain the dominant surface: ${JSON.stringify(barHeights)}`);

  // Narrow panel: the overflow menu appears instead of a vertical wall of URLs.
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "260px"; });
  await page.waitForTimeout(180);
  assert.equal(await page.locator("#desktop-browser-bookmarks-more").isVisible(), true, "narrow bookmark bar needs an overflow menu");
  await page.click("#desktop-browser-bookmarks-more");
  await page.waitForSelector("#desktop-browser-bookmarks-menu");
  assert.match(await page.locator("#desktop-browser-bookmarks-menu").innerText(), /全部收藏/);
  await page.click("#desktop-browser-bookmarks-menu-close");
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = ""; });

  // Folder chip opens just that folder's bookmarks.
  await page.click("[data-browser-bookmark-folder='Bar']");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark]");
  assert.match(await page.locator("#desktop-browser-bookmarks-menu").innerText(), /Mail/);
  // An outside click closes the menu without a repaint; reopening the same
  // folder must render again instead of trusting the cached HTML.
  await page.click("#desktop-browser-url");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 0);
  await page.click("[data-browser-bookmark-folder='Bar']");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark]");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 1, "external close must not break reopening");
  await page.keyboard.press("Escape");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 0);

  // 4. Switching a tab activates it in the visible browser and keeps frames.
  await page.click("[data-browser-tab='browser_tab_2']");
  await waitFor(page, () => window.__activates.length === 1);
  await waitFor(page, count => window.__frames > count, {arg: firstFrames});

  // Closing an Owner-opened page closes it under exclusive control.
  await page.click("[data-browser-tab-close='browser_tab_2']");
  await waitFor(page, () => window.__deletes.length === 1);

  // Folder entries open through the same tab API.
  await page.click("[data-browser-bookmark-folder='Bar']");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark='https://mail.example/']");
  await page.click("#desktop-browser-bookmarks-menu [data-browser-bookmark='https://mail.example/']");
  await waitFor(page, () => window.__opens.includes("https://mail.example/"));
  await waitFor(page, () => document.querySelector("#desktop-browser-url").value === "https://mail.example/");

  // The + button opens AHA's local start page, never about:blank.
  await page.click("#desktop-browser-newtab");
  await waitFor(page, () => window.__opens.some(url => new URL(url).pathname === "/browser-newtab"));
  assert.equal(await page.evaluate(() => window.__opens.some(url => url.startsWith("about:"))), false);
  await waitFor(page, () => document.querySelector("#desktop-browser-url").value.endsWith("/browser-newtab"));

  // The header stop calls this release path; the popover marker stays open.
  const grantedBefore = await page.evaluate(() => window.__granted);
  await page.evaluate(() => window.__panel.release());
  await waitFor(page, () => document.querySelector("#desktop-browser-attach") !== null);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /获取独占控制/);
  assert.equal(await page.evaluate(() => window.__granted), grantedBefore, "release must not fire the close-popover callback");
  assert.equal(await page.locator("#desktop-browser-control .desktop-browser-empty-state").count(), 1);
  await page.evaluate(() => window.__panel.dispose());

  // 5. Attaching from the popover closes it (onGranted) and shows the controls.
  await reset(page, {__mode: "detached", __attached: false, __released: false, __granted: 0, __openTargets: 0});
  await page.waitForSelector("#desktop-browser-attach", {timeout: 5000});
  assert.doesNotMatch(await page.locator("#desktop-browser-control").innerText(), /浏览器标签页/);
  const deletesBeforeAttach = await page.evaluate(() => window.__deletes.length);
  await page.click("#desktop-browser-attach");
  await waitFor(page, () => window.__granted === 1);
  await page.waitForSelector("#desktop-browser-tabs");
  assert.match(await page.locator("#desktop-browser-status").innerText(), /独占控制中/);
  assert.equal(await page.evaluate(() => window.__deletes.length), deletesBeforeAttach, "acquiring must not close Owner pages");
  // The address bar follows the page opened from it.
  await page.fill("#desktop-browser-url", "https://manual.example/page");
  await page.click("#desktop-browser-open-form button[type='submit']");
  await waitFor(page, () => window.__opens.includes("https://manual.example/page"));
  await waitFor(page, () => document.querySelector("#desktop-browser-url").value === "https://manual.example/page",
    {timeout: 5000});
  await page.evaluate(() => window.__panel.dispose());

  // 6. Empty profile: the start page opens automatically once.
  await reset(page, {__mode: "empty", __released: false, __opened: 0, __openedTabs: [], __opens: [], __granted: 0});
  await waitFor(page, () => window.__opens.length > 0);
  assert.equal(await page.evaluate(() => new URL(window.__opens[0]).pathname), "/browser-newtab");
  assert.equal(await page.evaluate(() => window.__opens[0].startsWith("about:")), false);
  await page.waitForSelector("[data-browser-tab]");
  assert.match(await page.locator("[data-browser-tab]").first().getAttribute("title"), /browser-newtab/);
  assert.equal(await page.evaluate(() => window.__opens.length), 1, "the start page opens once per grant");
  assert.equal(await page.evaluate(() => window.__deletes.length), deletesBeforeAttach, "the start page must not replace Owner pages");
  await page.evaluate(() => window.__panel.dispose());

  // 7. Transfer then release, without inventing a second control path.
  await reset(page, {__mode: "tabs", __released: false, __inUse: true, __transferred: false, __opens: [], __opened: 0, __openedTabs: [], __granted: 0});
  await page.waitForSelector("#desktop-browser-transfer", {timeout: 5000});
  assert.match(await page.locator("#desktop-browser-status").innerText(), /其他 Task 独占控制/);
  await page.click("#desktop-browser-transfer");
  await waitFor(page, () => document.querySelector("#desktop-browser-switch") !== null);
  await waitFor(page, () => window.__granted === 1);
  await page.evaluate(() => window.__panel.dispose());

  // 8. Missing host: explicit status, no frame traffic, no console errors.
  const framesBeforeHostDown = await page.evaluate(() => window.__frames);
  await reset(page, {__mode: "tabs", __hostAvailable: false, __inUse: false, __transferred: false, __granted: 0});
  await page.waitForSelector("#desktop-browser-status");
  await page.waitForTimeout(400);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /浏览器宿主不可用/);
  assert.equal(await page.evaluate(() => window.__frames), framesBeforeHostDown);
  assert.equal(await page.locator("#desktop-browser-retry").count(), 1);
  await page.evaluate(() => window.__panel.dispose());

  // 9. A late 404 from the previous tab must not hide or stop the new tab.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __staleError: true, __frames: 0, __granted: 0});
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
  await page.evaluate(() => window.__panel.dispose());

  // 10. An accepted stop must survive the panel: start a delayed open, ask the
  // header stop to release, then dispose (mode switch) before the open settles.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __opens: [], __openDelay: 1200,
    __grantDeletes: 0, __granted: 0, __frames: 0});
  await page.waitForSelector("#desktop-browser-newtab", {timeout: 5000});
  await page.click("#desktop-browser-newtab");
  await waitFor(page, () => window.__opens.length === 1);
  await page.evaluate(() => {
    window.__panel.release();
    window.__panel.dispose();
  });
  await waitFor(page, () => window.__grantDeletes >= 1, {timeout: 5000});
  assert.equal(await page.evaluate(() => window.__released), true, "dispose must not cancel a requested release");
  await page.evaluate(() => { window.__openDelay = 0; });

  // The start page must load under the production CSP as a plain http(s) page.
  const startPage = await browser.newPage();
  const startErrors = [];
  startPage.on("pageerror", error => startErrors.push(error.message));
  await startPage.goto(`${base}/browser-newtab`, {waitUntil: "networkidle"});
  assert.match(await startPage.title(), /新标签页/);
  assert.equal(await startPage.locator('input[name="q"]').count(), 1);
  assert.equal(await startPage.locator('form[action="https://www.bing.com/search"]').count(), 1);
  assert.deepEqual(startErrors, []);
  await startPage.close();

  assert.deepEqual(errors, []);
  console.log("browser task panel split surfaces, start page and preview verified");
} finally {
  await browser?.close();
  server.close();
}
