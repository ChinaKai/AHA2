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
  {title: "Wiki", url: "https://wiki.example/home", folder: "收藏夹/Wiki"},
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
window.__hostOnlyDown = false;
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
// Owner actions performed in the real browser window, outside the panel.
window.__ownerTabs = [];
window.__ownerClosed = [];
window.__ownerNav = {};
window.__ownerActive = "";
window.__syncRequests = 0;
// Explicit gate for delayed-response probes: arm {promise, release} before the
// request that must be held; the fixture awaits it and the test releases it by
// signal instead of guessing a timeout.
window.__syncHold = null;
window.__syncHoldTaken = false;
window.__syncHoldDone = false;
window.__frameErrorTab = "";
window.__frameErrorStatus = 404;
window.__frameRequests = 0;
window.__frameUnavailable = false;
window.__frame503 = 0;
window.__frameClosed = [];
window.__vis = "visible";
const HOST_UP = {available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true};
const HOST_DOWN = {available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"};
const STATE_UP = ${JSON.stringify(browserState({available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true}))};
const STATE_DOWN = ${JSON.stringify({...browserState({available: false, host_id: "local", edge_available: false, code: "browser_host_start_failed", error: "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"}), grants: [], tabs: []})};
const GRANT = ${JSON.stringify(grant)};
const BOOKMARKS = ${JSON.stringify(bookmarks)};
// Pages the Owner opened, closed, navigated or activated in the real browser
// window appear here without going through the panel's own APIs.
const ownerView = tabs => {
  let list = tabs.concat(window.__ownerTabs).filter(tab => !window.__ownerClosed.includes(tab.id)
    && !window.__frameClosed.includes(tab.id));
  if (window.__ownerActive) list = list.map(tab => ({...tab, active: tab.id === window.__ownerActive}));
  list = list.map(tab => window.__ownerNav[tab.id] ? {...tab, ...window.__ownerNav[tab.id]} : tab);
  return list;
};
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
  if (window.__hostOnlyDown) {
    const base = {...STATE_UP, host: HOST_DOWN};
    return {...base, tabs: ownerView(base.tabs.concat(window.__openedTabs))};
  }
  const base = window.__mode === "empty" ? {...STATE_UP, tabs: []} : {...STATE_UP};
  return {...base, tabs: ownerView(base.tabs.concat(window.__openedTabs))};
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
  if (path === "/api/v1/tasks/task-1/browser") {
    window.__syncRequests++;
    // Capture the answer before any hold: a request started before a release
    // must still carry the lease it was issued against.
    const snapshot = currentState();
    if (window.__syncHold && !window.__syncHoldTaken) {
      window.__syncHoldTaken = true;
      await window.__syncHold.promise;
      window.__syncHoldDone = true;
    }
    return json({ok: true, browser: snapshot});
  }
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
    window.__frameRequests++;
    if (window.__frameUnavailable) {
      window.__frame503++;
      return json({error: {code: "browser_host_unavailable", message: "浏览器宿主不可用"}}, 503);
    }
    if (window.__frameErrorTab && path.includes(window.__frameErrorTab)) {
      const failing = path.split("/").slice(-2)[0];
      if (window.__frameErrorStatus === 404 || window.__frameErrorStatus === 410) window.__frameClosed.push(failing);
      return json({error: {code: "browser_tab_not_found", message: "标签页已关闭"}}, window.__frameErrorStatus);
    }
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

const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css">
  <style>#desktop-tool{display:flex;flex-direction:column;height:780px;width:100%;}</style></head><body>
  <aside id="desktop-tool">
    <div id="desktop-browser-target"></div>
    <div id="desktop-browser-control" class="desktop-browser-main"></div>
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
  // No left "内置浏览器" column: the tab strip spans the panel and the preview
  // takes the height left below the fixed bars instead of a 360px cap.
  assert.equal(await page.locator("#desktop-browser-surface").count(), 0);
  const fill = await page.evaluate(() => {
    const panel = document.querySelector("#desktop-browser-control").getBoundingClientRect();
    const tabs = document.querySelector("#desktop-browser-tabs").getBoundingClientRect();
    const preview = document.querySelector(".desktop-browser-preview").getBoundingClientRect();
    return {panelWidth: panel.width, tabsWidth: tabs.width, panelHeight: panel.height, previewHeight: preview.height};
  });
  assert.ok(fill.tabsWidth > fill.panelWidth * 0.8, `tabs must span the panel: ${JSON.stringify(fill)}`);
  assert.ok(fill.previewHeight > fill.panelHeight * 0.5, `preview must fill the remaining height: ${JSON.stringify(fill)}`);
  // The two status lines are one bar at the bottom of the preview.
  const barText = await page.locator(".desktop-browser-statusbar").innerText();
  assert.match(barText, /独占控制中/);
  assert.match(barText, /实时画面/);

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
  // A nested Edge folder arrives as "收藏夹/Wiki"; the repeated root is
  // stripped from the visible label while the raw path stays the filter key.
  assert.equal(await page.locator("[data-browser-bookmark-folder='收藏夹/Wiki'] .desktop-browser-bookmark-title").innerText(), "Wiki");
  assert.equal(await page.locator("[data-browser-bookmark-folder='收藏夹/Wiki'] .icon").count(), 1);
  assert.match(await bookmarkBar.innerText(), /Mail|文档中心/);
  assert.doesNotMatch(await bookmarkBar.innerText(), /token=|redirect=|environment=|收藏夹\//);
  // One overflow entry only: the bar itself never scrolls horizontally.
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector("#desktop-browser-bookmarks")).overflowX), "hidden");
  assert.equal(await page.evaluate(() => {
    const items = document.querySelector("#desktop-browser-bookmarks");
    return items.scrollWidth <= items.clientWidth + 1;
  }), true);
  const barHeights = await page.evaluate(() => {
    const bar = document.querySelector("#desktop-browser-bookmarkbar").getBoundingClientRect();
    const preview = document.querySelector(".desktop-browser-preview").getBoundingClientRect();
    return {bar: bar.height, preview: preview.height};
  });
  assert.ok(barHeights.bar < 60, `bookmark bar must stay one horizontal row: ${JSON.stringify(barHeights)}`);
  assert.ok(barHeights.preview > barHeights.bar, `the preview must remain the dominant surface: ${JSON.stringify(barHeights)}`);

  // 380px shared panel: the Edge surfaces stay inside the column, the preview
  // keeps its height, and nothing overflows horizontally.
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "380px"; });
  await page.waitForTimeout(180);
  const narrowPanel = await page.evaluate(() => {
    const panel = document.querySelector("#desktop-browser-control");
    const preview = document.querySelector(".desktop-browser-preview").getBoundingClientRect();
    const bookmarks = document.querySelector("#desktop-browser-bookmarks");
    return {
      scrollWidth: panel.scrollWidth, clientWidth: panel.clientWidth,
      previewWidth: preview.width, previewHeight: preview.height,
      bookmarkOverflow: getComputedStyle(bookmarks).overflowX,
    };
  });
  assert.ok(narrowPanel.scrollWidth <= narrowPanel.clientWidth + 1,
    `the 380px panel must not overflow horizontally: ${JSON.stringify(narrowPanel)}`);
  assert.ok(narrowPanel.previewWidth > 100 && narrowPanel.previewHeight > 0,
    `the 380px preview must stay usable: ${JSON.stringify(narrowPanel)}`);
  assert.equal(narrowPanel.bookmarkOverflow, "hidden");
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = ""; });

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

  // 11. While the panel is open, changes made in the real browser window are
  // pulled in by the tab-sync timer: new page, switched page and navigation.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __syncRequests: 0, __frames: 0, __granted: 0});
  await page.waitForSelector("[data-browser-tab='browser_tab_1']", {timeout: 5000});
  const syncBefore = await page.evaluate(() => window.__syncRequests);
  await page.evaluate(() => window.__ownerTabs.push({id: "browser_tab_owner_3", session_id: "browser_session_1",
    grant_id: "browser_grant_1", task_id: "task-1", url: "https://owner.example/new", title: "Owner 新页",
    origin: "owner", active: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString()}));
  await page.waitForSelector("[data-browser-tab='browser_tab_owner_3']", {timeout: 8000});
  assert.ok(await page.evaluate(() => window.__syncRequests) > syncBefore,
    "the open panel must poll the tab list");

  // The Owner switches to another page in the real window; the panel follows
  // the single active page and restarts the preview on it.
  await page.evaluate(() => { window.__ownerActive = "browser_tab_2"; });
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_2']")?.getAttribute("aria-pressed") === "true", {timeout: 8000});
  const framesAfterOwnerSwitch = await page.evaluate(() => window.__frames);
  await waitFor(page, count => window.__frames > count, {arg: framesAfterOwnerSwitch, timeout: 5000});

  // Navigating inside that page updates its title and the address bar; the
  // frame stream keeps going without a panel reload.
  await page.evaluate(() => { window.__ownerNav["browser_tab_2"] = {url: "https://example.com/next", title: "下一页"}; });
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_2'] .desktop-browser-tab-title")?.textContent === "下一页"
    && document.querySelector("#desktop-browser-url")?.value === "https://example.com/next", {timeout: 8000});

  // Closing the page in the real window removes its tab and moves the preview
  // to a remaining page.
  await page.evaluate(() => { window.__ownerClosed.push("browser_tab_2"); });
  await waitFor(page, () => !document.querySelector("[data-browser-tab='browser_tab_2']"), {timeout: 8000});
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_1']")?.getAttribute("aria-pressed") === "true", {timeout: 8000});

  // A host outage clears the bitmap; once the host is reachable again the sync
  // timer restarts the frame loop instead of leaving a frozen picture.
  await page.evaluate(() => { window.__hostOnlyDown = true; });
  await waitFor(page, () => document.querySelector("#desktop-browser-frame").hidden
    && /浏览器宿主不可用/.test(document.querySelector("#desktop-browser-frame-status").textContent), {timeout: 8000});
  await page.evaluate(() => { window.__hostOnlyDown = false; });
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 8000});
  await page.evaluate(() => window.__panel.dispose());

  // 12. A terminal frame answer (404) clears the bitmap, drops the closed tab
  // and never leaves the last screenshot on screen. Closing every page leaves
  // an explicit empty state instead of a frozen preview.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: ["browser_tab_1", "browser_tab_2"],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "browser_tab_", __frameErrorStatus: 404, __frameClosed: [], __syncRequests: 0, __frames: 0, __granted: 0});
  await waitFor(page, () => !document.querySelector("[data-browser-tab]"), {timeout: 8000});
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").hidden), true);
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").getAttribute("src")), null);
  assert.match(await page.locator("#desktop-browser-frame-status").innerText(), /没有打开的页面|画面暂不可用/);
  await page.evaluate(() => window.__panel.dispose());

  // 13. Hiding AHA Web pauses the stream and drops the bitmap; becoming
  // visible again immediately re-syncs tabs and restarts the preview.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __syncRequests: 0, __frames: 0, __granted: 0});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await page.evaluate(() => {
    window.__vis = "visible";
    Object.defineProperty(document, "visibilityState", {configurable: true, get: () => window.__vis});
  });
  await page.evaluate(() => { window.__vis = "hidden"; document.dispatchEvent(new Event("visibilitychange")); });
  await waitFor(page, () => document.querySelector("#desktop-browser-frame").hidden);
  await page.waitForTimeout(200);
  const hiddenFrames = await page.evaluate(() => window.__frames);
  const hiddenSyncs = await page.evaluate(() => window.__syncRequests);
  await page.evaluate(() => window.__ownerTabs.push({id: "browser_tab_owner_hidden", session_id: "browser_session_1",
    grant_id: "browser_grant_1", task_id: "task-1", url: "https://owner.example/hidden", title: "隐藏时新开",
    origin: "owner", active: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString()}));
  // Long enough for at least one 3s sync tick: neither frames nor tab reads
  // may run while the page is hidden.
  await page.waitForTimeout(3300);
  assert.equal(await page.locator("[data-browser-tab='browser_tab_owner_hidden']").count(), 0,
    "hidden pages must not keep syncing in the background");
  assert.equal(await page.evaluate(() => window.__syncRequests), hiddenSyncs,
    "the tab-sync timer must pause while hidden");
  assert.equal(await page.evaluate(() => window.__frames), hiddenFrames,
    "the frame loop must pause while hidden");
  await page.evaluate(() => { window.__vis = "visible"; document.dispatchEvent(new Event("visibilitychange")); });
  await page.waitForSelector("[data-browser-tab='browser_tab_owner_hidden']", {timeout: 8000});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await waitFor(page, count => window.__frames > count, {arg: hiddenFrames, timeout: 5000});
  await page.evaluate(() => { delete document.visibilityState; window.__vis = "visible"; });
  await page.evaluate(() => window.__panel.dispose());

  // 14. A tab-sync read that started before release must not resurrect the
  // lease it was issued against. The fixture holds the response on an explicit
  // gate; the test releases it only after the release has completed.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __hostOnlyDown: false, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __syncRequests: 0,
    __syncHold: null, __syncHoldTaken: false, __syncHoldDone: false, __frames: 0, __granted: 0});
  await page.waitForSelector("#desktop-browser-switch", {timeout: 5000});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await page.evaluate(() => {
    let release;
    const promise = new Promise(resolve => { release = resolve; });
    window.__syncHold = {promise, release};
    window.__syncHoldTaken = false;
    window.__syncHoldDone = false;
  });
  const syncsBeforeHold = await page.evaluate(() => window.__syncRequests);
  await waitFor(page, () => window.__syncHoldTaken, {timeout: 6000});
  assert.ok(await page.evaluate(() => window.__syncRequests) > syncsBeforeHold, "a tab sync should have been requested");
  // The held sync carries the active grant; release while it is in flight.
  await page.evaluate(() => window.__panel.release());
  await page.waitForSelector("#desktop-browser-attach", {timeout: 5000});
  assert.equal(await page.evaluate(() => window.__panel.granted()), false, "release must clear the lease");
  // Let the stale response land only after the release finished.
  await page.evaluate(() => { window.__syncHold.release(); window.__syncHold = null; });
  await waitFor(page, () => window.__syncHoldDone, {timeout: 3000});
  await page.waitForTimeout(150);
  assert.equal(await page.evaluate(() => window.__panel.granted()), false,
    "an in-flight state read from before the release must not restore the lease");
  assert.equal(await page.locator("#desktop-browser-attach").count(), 1,
    "the released panel must stay on the attach action");
  assert.equal(await page.locator("#desktop-browser-control .desktop-browser-empty-state").count(), 1);
  await page.evaluate(() => window.__panel.dispose());

  // 14b. The explicit refresh path (load) must obey the same epoch. Hold the
  // refresh's own read, release the lease, then let the old answer land.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __hostOnlyDown: false, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __syncRequests: 0,
    __syncHold: null, __syncHoldTaken: false, __syncHoldDone: false, __frames: 0, __granted: 0});
  await page.waitForSelector("#desktop-browser-switch", {timeout: 5000});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  // Wait until a tab sync has just run, so the next read is the refresh click;
  // arm the gate and click in the same step to make that request the held one.
  const syncsBeforeRefresh = await page.evaluate(() => window.__syncRequests);
  await waitFor(page, count => window.__syncRequests > count, {arg: syncsBeforeRefresh, timeout: 6000});
  await page.evaluate(() => {
    let release;
    const promise = new Promise(resolve => { release = resolve; });
    window.__syncHold = {promise, release};
    window.__syncHoldTaken = false;
    window.__syncHoldDone = false;
    document.querySelector("#desktop-browser-refresh").click();
  });
  await waitFor(page, () => window.__syncHoldTaken, {timeout: 3000});
  await page.evaluate(() => window.__panel.release());
  await page.waitForSelector("#desktop-browser-attach", {timeout: 5000});
  assert.equal(await page.evaluate(() => window.__panel.granted()), false, "release must clear the lease");
  await page.evaluate(() => { window.__syncHold.release(); window.__syncHold = null; });
  await waitFor(page, () => window.__syncHoldDone, {timeout: 3000});
  await page.waitForTimeout(150);
  assert.equal(await page.evaluate(() => window.__panel.granted()), false,
    "a held refresh answer from before the release must not restore the lease");
  assert.equal(await page.locator("#desktop-browser-attach").count(), 1,
    "the released panel must keep the attach action after the stale refresh lands");
  assert.equal(await page.locator("#desktop-browser-control .desktop-browser-empty-state").count(), 1);
  await page.evaluate(() => window.__panel.dispose());

  // 14c. A normal explicit refresh still applies: the lease and preview stay.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __hostOnlyDown: false, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __syncRequests: 0,
    __syncHold: null, __syncHoldTaken: false, __syncHoldDone: false, __frames: 0, __granted: 0});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  const syncsBeforeNormalRefresh = await page.evaluate(() => window.__syncRequests);
  await page.click("#desktop-browser-refresh");
  await waitFor(page, count => window.__syncRequests > count, {arg: syncsBeforeNormalRefresh, timeout: 3000});
  await page.waitForSelector("[data-browser-tab='browser_tab_1']", {timeout: 5000});
  assert.equal(await page.evaluate(() => window.__panel.granted()), true, "a normal refresh must keep the lease");
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").hidden), false,
    "a normal refresh must keep the preview");
  await page.evaluate(() => window.__panel.dispose());

  // 15. A terminal frame answer (409/403) is never retried by the 2s error
  // backoff or by restoring visibility: the suppression outlives hidden→visible.
  for (const terminalStatus of [409, 403]) {
    await reset(page, {__mode: "tabs", __hostAvailable: true, __hostOnlyDown: false, __staleError: false, __released: false,
      __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
      __ownerNav: {}, __ownerActive: "", __frameErrorTab: "browser_tab_1", __frameErrorStatus: terminalStatus,
      __frameClosed: [], __frameRequests: 0, __frameUnavailable: false, __frame503: 0,
      __syncRequests: 0, __syncHold: null, __syncHoldTaken: false, __syncHoldDone: false, __frames: 0, __granted: 0});
    await waitFor(page, () => /画面暂不可用/.test(document.querySelector("#desktop-browser-frame-status")?.textContent || ""),
      {timeout: 8000});
    const requestsAfterTerminal = await page.evaluate(() => window.__frameRequests);
    assert.ok(requestsAfterTerminal >= 1, `the ${terminalStatus} frame should have been requested`);
    assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").hidden), true);
    assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").getAttribute("src")), null);
    // Beyond the retry backoff, a terminal answer must not be re-requested.
    await page.waitForTimeout(2600);
    assert.equal(await page.evaluate(() => window.__frameRequests), requestsAfterTerminal,
      `a terminal ${terminalStatus} frame must not be retried on the timer`);
    // Pause and resume visibility; the terminal suppression must survive.
    await page.evaluate(() => {
      window.__vis = "visible";
      Object.defineProperty(document, "visibilityState", {configurable: true, get: () => window.__vis});
      window.__vis = "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(page, () => document.querySelector("#desktop-browser-frame").hidden);
    await page.evaluate(() => { window.__vis = "visible"; document.dispatchEvent(new Event("visibilitychange")); });
    await page.waitForTimeout(1200);
    assert.equal(await page.evaluate(() => window.__frameRequests), requestsAfterTerminal,
      `restoring visibility must not retry a terminal ${terminalStatus} frame`);
    await page.evaluate(() => { delete document.visibilityState; window.__vis = "visible"; });
    await page.evaluate(() => window.__panel.dispose());
  }

  // 16. A 503 / browser_host_unavailable is not a per-tab terminal answer but
  // it must not keep the frame loop alive: the bitmap is cleared immediately,
  // the loop pauses, and only the health sync restarts it after recovery.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __hostOnlyDown: false, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __frameRequests: 0,
    __frameUnavailable: false, __frame503: 0, __syncRequests: 0, __syncHold: null, __syncHoldTaken: false, __syncHoldDone: false, __frames: 0, __granted: 0});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await waitFor(page, () => window.__frames >= 2);
  await page.evaluate(() => { window.__frameUnavailable = true; });
  // The first 503 clears the preview; the health sync restarts one probe, which
  // 503s again. Two failures must cost two requests, not a hot retry loop.
  await waitFor(page, () => window.__frame503 >= 2, {timeout: 10000});
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").hidden), true,
    "a 503 must hide the last bitmap immediately");
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").getAttribute("src")), null,
    "a 503 must drop the last bitmap instead of keeping a stale screenshot");
  assert.ok(await page.evaluate(() => window.__frameRequests) <= 6,
    "a host-unavailable answer must not be retried every frame");
  // The health read now reports the host down, so nothing polls until recovery.
  await page.evaluate(() => { window.__hostOnlyDown = true; });
  const requestsAfter503 = await page.evaluate(() => window.__frameRequests);
  await page.waitForTimeout(6500);
  assert.equal(await page.evaluate(() => window.__frameRequests), requestsAfter503,
    "the frame loop must stay paused while the host is unavailable");
  assert.match(await page.locator("#desktop-browser-frame-status").innerText(), /浏览器宿主不可用|画面暂不可用/);
  // The host comes back; the tab-sync timer restarts the preview.
  const framesBeforeRecovery = await page.evaluate(() => window.__frames);
  await page.evaluate(() => { window.__frameUnavailable = false; window.__hostOnlyDown = false; });
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 8000});
  await waitFor(page, count => window.__frames > count, {arg: framesBeforeRecovery, timeout: 5000});
  await page.evaluate(() => window.__panel.dispose());

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
