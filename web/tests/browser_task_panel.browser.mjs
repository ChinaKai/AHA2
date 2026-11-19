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
  {title: "Work 门户", url: "https://work.example/home?token=abcdefghijklmnop", folder: "收藏夹栏/Work"},
  {title: "Tools", url: "https://tools.example/", folder: "收藏夹栏/Tools"},
  {title: "文档中心", url: "https://docs.example/very/long/path?token=abcdefghijklmnop&redirect=https%3A%2F%2Fexample.com", folder: "收藏夹栏/Docs"},
  {title: "HUALAI 文档", url: "https://hualai.example/docs?token=abcdefghijklmnop", folder: "收藏夹栏/Docs/HUALAI"},
  {title: "蓝牙调试", url: "https://bluetooth.example/debug?token=abcdefghijklmnop", folder: "收藏夹栏/Docs/蓝牙"},
  {title: "官网", url: "https://website.example/", folder: "收藏夹栏/Website"},
  {title: "WYZE", url: "https://wyze.example/", folder: "收藏夹栏/WYZE-VEGA"},
  {title: "其他", url: "https://others.example/", folder: "收藏夹栏/Others"},
  {title: "监控", url: "https://monitor.example/dashboard?range=last-24h&filters=service%3Dapi", folder: "收藏夹栏/bulbcam"},
  {title: "发布面板", url: "https://release.example/deploy?environment=production&region=ap-east-1", folder: "收藏夹栏/Work"},
  {title: "工单", url: "https://tickets.example/queue?assignee=owner&status=open", folder: ""},
  {title: "Mail", url: "https://mail.example/", folder: ""},
  {title: "根目录直达", url: "https://root.example/home?token=abcdefghijklmnop", folder: "收藏夹栏"},
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
// Foreground window of a multi-window Profile; independent of active.
window.__ownerFocused = "";
window.__syncRequests = 0;
window.__bookmarks = null;
// Explicit gate for delayed-response probes: arm {promise, release} before the
// request that must be held; the fixture awaits it and the test releases it by
// signal instead of guessing a timeout.
window.__syncHold = null;
window.__syncHoldTaken = false;
window.__syncHoldDone = false;
window.__frameErrorTab = "";
window.__frameErrorStatus = 404;
window.__frameRequests = 0;
// Frame provenance: which tab each served frame came from, so a test can prove
// the picture matches the selected tab rather than some other window's page.
window.__frameTabs = [];
window.__lastFrameTab = "";
window.__frameDelayTab = "";
window.__frameDelayMs = 0;
window.__frameDelayStarted = 0;
window.__frameDelayDone = 0;
// A stale frame may be visible for only one paint. Hold every frame for the
// new tab after the first one, so a late old-tab paint cannot be masked by a
// newer frame before the test looks at the picture.
window.__frameHoldTab = "";
window.__frameHoldAfter = 1;
window.__frameOK = {};
window.__frameHeld = 0;
window.__frameHoldWaiters = [];
window.__frameHoldReleased = 0;
window.__releaseFrameHolds = () => {
  window.__frameHoldTab = "";
  const waiters = window.__frameHoldWaiters.splice(0);
  window.__frameHoldReleased += waiters.length;
  waiters.forEach(resolve => resolve());
};
// Every decoded preview frame is recorded with the pixel it painted, so even a
// one-frame flash of the old tab is visible after the fact.
window.__paints = [];
const rememberPaint = image => {
  if (!image.complete || !image.naturalWidth) return;
  const canvas = document.createElement("canvas");
  canvas.width = 1;
  canvas.height = 1;
  const context = canvas.getContext("2d");
  try {
    context.drawImage(image, 0, 0, 1, 1);
    const [red, green] = context.getImageData(0, 0, 1, 1).data;
    window.__paints.push({color: red > 200 ? "red" : green > 200 ? "green" : "other", at: performance.now()});
  } catch {
    window.__paints.push({color: "error", at: performance.now()});
  }
};
const watchPreviewPaint = image => {
  if (!image || image.__paintWatched) return;
  image.__paintWatched = true;
  image.addEventListener("load", () => rememberPaint(image));
};
new MutationObserver(() => {
  document.querySelectorAll("#desktop-browser-frame").forEach(watchPreviewPaint);
}).observe(document.documentElement, {childList: true, subtree: true});
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
// Distinguishable 1x1 PNGs: tab 1 is red, every other tab is green. The test
// reads the rendered pixel, so forcing all frame requests to tab 1 changes the
// picture and fails the assertion instead of only the request bookkeeping.
const FRAME_PNG = {
  red: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC",
  green: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNg+M8AAAICAQB7CYF4AAAAAElFTkSuQmCC",
};
// Pages the Owner opened, closed, navigated or activated in the real browser
// window appear here without going through the panel's own APIs.
const ownerView = tabs => {
  let list = tabs.concat(window.__ownerTabs).filter(tab => !window.__ownerClosed.includes(tab.id)
    && !window.__frameClosed.includes(tab.id));
  if (window.__ownerActive) list = list.map(tab => ({...tab, active: tab.id === window.__ownerActive}));
  if (window.__ownerFocused) {
    const focusedIDs = Array.isArray(window.__ownerFocused) ? window.__ownerFocused : [window.__ownerFocused];
    list = list.map(tab => ({...tab, focused: focusedIDs.includes(tab.id)}));
  }
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
      return json({error: {code: "browser_profile_in_use", message: "该浏览器正由其他 Task 使用；请先在对应 Task 关闭共享"}}, 409);
    }
    return json({ok: true, grant: GRANT, session: GRANT.session});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1" && method === "DELETE") {
    window.__released = true;
    window.__grantDeletes++;
    return json({ok: true, grant: {...GRANT, status: "revoked"}});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1/bookmarks") {
    return json({ok: true, bookmarks: window.__bookmarks || BOOKMARKS});
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
    const tabID = path.split("/").slice(-2)[0];
    window.__lastFrameTab = tabID;
    window.__frameTabs.push(tabID);
    window.__frameOK[tabID] = (window.__frameOK[tabID] || 0) + 1;
    if (window.__frameHoldTab === tabID && window.__frameOK[tabID] > window.__frameHoldAfter) {
      window.__frameHeld++;
      await new Promise(resolve => window.__frameHoldWaiters.push(resolve));
    }
    // Opt-in slow frame for the old tab: a switch must discard it on arrival.
    if (window.__frameDelayTab && tabID === window.__frameDelayTab && window.__frameDelayMs > 0) {
      window.__frameDelayStarted++;
      await new Promise(resolve => setTimeout(resolve, window.__frameDelayMs));
      window.__frameDelayDone++;
    }
    window.__frames++;
    const png = tabID === "browser_tab_2" ? FRAME_PNG.green : FRAME_PNG.red;
    const body = Uint8Array.from(atob(png), char => char.charCodeAt(0));
    return new Response(body, {status: 200, headers: {"Content-Type": "image/png"}});
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
// Reads the preview's rendered pixel. Frame fixtures are 1x1 PNGs (tab 1 red,
// other tabs green), so the color proves which tab produced the visible picture
// instead of trusting the request bookkeeping alone.
const frameColor = async page => page.evaluate(async () => {
  const image = document.querySelector("#desktop-browser-frame");
  if (!image) return "none";
  if (!image.complete) await new Promise(resolve => image.addEventListener("load", resolve, {once: true}));
  if (!image.naturalWidth) return "broken";
  const canvas = document.createElement("canvas");
  canvas.width = 1;
  canvas.height = 1;
  const context = canvas.getContext("2d");
  context.drawImage(image, 0, 0, 1, 1);
  const [red, green] = context.getImageData(0, 0, 1, 1).data;
  return red > 200 ? "red" : green > 200 ? "green" : "other";
});
const waitForFrameColor = async (page, color, timeout = 6000) => {
  await waitFor(page, expected => {
    const image = document.querySelector("#desktop-browser-frame");
    if (!image || !image.complete || !image.naturalWidth) return false;
    const canvas = document.createElement("canvas");
    canvas.width = 1;
    canvas.height = 1;
    const context = canvas.getContext("2d");
    context.drawImage(image, 0, 0, 1, 1);
    const [red, green] = context.getImageData(0, 0, 1, 1).data;
    return expected === "red" ? red > 200 : green > 200;
  }, {arg: color, timeout});
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
  assert.equal(await selector.locator("#desktop-browser-release").innerText(), "关闭共享浏览器");
  for (const forbidden of ["#desktop-browser-frame", "[data-browser-tab]", "#desktop-browser-url", "#desktop-browser-bookmarks", "#desktop-browser-open-form"]) {
    assert.equal(await selector.locator(forbidden).count(), 0, `target popover must not render ${forbidden}`);
  }
  assert.match(await selector.locator("#desktop-browser-status").innerText(), /浏览器共享已开启/);

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
  assert.match(barText, /浏览器共享中/);
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

  // 2b. Two windows each stay active; the foreground one decides the preview.
  // The picture itself is checked (tab 1 red, tab 2 green), not just the frame
  // counter: forcing every frame request to tab 1 must fail these assertions.
  const pressed = tab => page.evaluate(id =>
    document.querySelector(`[data-browser-tab='${id}']`)?.getAttribute("aria-pressed") === "true", tab);
  const assertSingleCurrent = async id => {
    assert.equal(await pressed(id), true, `${id} should be current`);
    assert.equal(await page.locator(".desktop-browser-tab.active").count(), 1);
    assert.equal(await page.locator('[aria-current="page"]').count(), 1);
    assert.equal(await page.locator("[data-browser-tab]").evaluateAll(items => items.find(item => item.getAttribute("aria-current") === "page")?.getAttribute("data-browser-tab")), id);
  };
  await waitForFrameColor(page, "red");
  assert.equal(await page.evaluate(() => window.__lastFrameTab), "browser_tab_1");

  // Start a slow tab-1 frame, then switch focus to tab 2 and force an immediate
  // sync. Every tab-2 frame after the first is held, so a late tab-1 paint
  // cannot be covered by a newer green frame before the test inspects it. Each
  // decoded frame is also recorded with its pixel: a one-frame red flash fails
  // even if a later green frame would have restored the picture.
  await page.evaluate(() => {
    window.__frameDelayTab = "browser_tab_1";
    window.__frameDelayMs = 1500;
    window.__frameHoldTab = "browser_tab_2";
    window.__frameHoldAfter = 1;
  });
  await waitFor(page, () => window.__frameDelayStarted > 0, {timeout: 5000});
  const paintsBeforeSwitch = await page.evaluate(() => window.__paints.length);
  await page.evaluate(() => {
    window.__ownerFocused = "browser_tab_2";
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_2']")?.getAttribute("aria-pressed") === "true", {timeout: 8000});
  await assertSingleCurrent("browser_tab_2");
  await waitForFrameColor(page, "green");
  assert.equal(await page.evaluate(() => window.__lastFrameTab), "browser_tab_2", "frames must come from the selected tab");
  await waitFor(page, () => window.__frameHeld > 0, {timeout: 8000});
  await waitFor(page, () => window.__frameDelayDone > 0, {timeout: 8000});
  // Give the browser a chance to run any paint that a stale response queued.
  await page.waitForTimeout(250);
  const paintsAfterSwitch = await page.evaluate(mark => window.__paints.slice(mark), paintsBeforeSwitch);
  assert.deepEqual(paintsAfterSwitch.filter(paint => paint.color === "red"), [],
    `the late tab-1 frame must never paint over tab 2; paints=${JSON.stringify(paintsAfterSwitch)}`);
  assert.equal(await page.evaluate(() => window.__lastFrameTab), "browser_tab_2", "the late tab-1 frame must not be requested again");
  assert.equal(await frameColor(page), "green", "the late tab-1 frame must not overwrite tab 2's picture");
  await page.evaluate(() => {
    window.__frameDelayTab = "";
    window.__frameDelayMs = 0;
    window.__releaseFrameHolds();
  });

  // Switching back follows the new foreground window in the same way.
  await page.evaluate(() => { window.__ownerFocused = "browser_tab_1"; document.dispatchEvent(new Event("visibilitychange")); });
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_1']")?.getAttribute("aria-pressed") === "true", {timeout: 8000});
  await assertSingleCurrent("browser_tab_1");
  await waitForFrameColor(page, "red");
  assert.equal(await page.evaluate(() => window.__lastFrameTab), "browser_tab_1");

  // Several focused pages must not outrank a single active page: both are
  // focused, only tab 2 is active, so tab 2 wins.
  await page.evaluate(() => {
    window.__ownerFocused = ["browser_tab_1", "browser_tab_2"];
    window.__ownerActive = "browser_tab_2";
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_2']")?.getAttribute("aria-pressed") === "true", {timeout: 8000});
  await assertSingleCurrent("browser_tab_2");
  await waitForFrameColor(page, "green");

  // Several focused and several active pages stay ambiguous: keep the current
  // selection instead of jumping.
  const syncsBeforeAmbiguous = await page.evaluate(() => window.__syncRequests);
  await page.evaluate(() => {
    window.__ownerActive = "";
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await waitFor(page, count => window.__syncRequests > count, {arg: syncsBeforeAmbiguous, timeout: 8000});
  await page.waitForTimeout(150);
  await assertSingleCurrent("browser_tab_2");
  await waitForFrameColor(page, "green");

  // Restore the initial single-window state for the remaining cases.
  await page.evaluate(() => { window.__ownerFocused = "browser_tab_1"; document.dispatchEvent(new Event("visibilitychange")); });
  await waitFor(page, () => document.querySelector("[data-browser-tab='browser_tab_1']")?.getAttribute("aria-pressed") === "true", {timeout: 8000});
  await page.evaluate(() => { window.__ownerFocused = ""; window.__ownerActive = ""; });

  // The tab strip is horizontal, compact, and never renders the full URL.
  const longTab = page.locator("[data-browser-tab='browser_tab_1']");
  assert.doesNotMatch(await longTab.innerText(), /redirect_uri|\?/);
  assert.match(await longTab.locator(".desktop-browser-tab-origin").innerText(), /Owner/);
  assert.match(await longTab.getAttribute("title"), /redirect_uri=/);
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector("#desktop-browser-tabs")).overflowX), "auto");
  const tabHeights = await page.locator(".desktop-browser-tab-item").evaluateAll(items => items.map(item => item.getBoundingClientRect().height));
  assert.ok(tabHeights.every(height => height < 44), `tab rows should stay compact: ${tabHeights}`);

  // 3. The bookmark bar is horizontal: one chip per first-level folder (nested
  // Edge folders fold into their parent), plus direct entries, no long URL.
  await page.waitForSelector("[data-browser-bookmark-folder]");
  const bookmarkBar = page.locator("#desktop-browser-bookmarks");
  assert.deepEqual(
    await page.locator("[data-browser-bookmark-folder]").evaluateAll(chips => chips.map(chip => chip.dataset.browserBookmarkFolder)),
    ["Work", "Tools", "Docs", "Website", "WYZE-VEGA", "Others", "bulbcam"],
  );
  assert.equal(await page.locator("[data-browser-bookmark-folder='Docs/HUALAI']").count(), 0);
  assert.equal(await page.locator("[data-browser-bookmark-folder='Docs/蓝牙']").count(), 0);
  // A bookmark whose folder is the bar root itself stays a direct URL chip,
  // never a "收藏夹栏" folder chip.
  assert.equal(await page.locator("[data-browser-bookmark-folder='收藏夹栏']").count(), 0);
  assert.equal(await page.locator("[data-browser-bookmark-folder='Docs'] .desktop-browser-bookmark-title").innerText(), "Docs");
  assert.equal(await page.locator("[data-browser-bookmark-folder='Docs'] .icon").count(), 1);
  const directChipTitles = await page.locator("#desktop-browser-bookmarks [data-browser-bookmark-index]")
    .evaluateAll(chips => chips.map(chip => chip.querySelector(".desktop-browser-bookmark-title")?.textContent || ""));
  assert.ok(directChipTitles.includes("根目录直达"), `root-level bookmarks stay direct chips: ${JSON.stringify(directChipTitles)}`);
  assert.ok(directChipTitles.length >= 1);
  assert.match(await bookmarkBar.innerText(), /Mail|文档中心/);
  assert.doesNotMatch(await bookmarkBar.innerText(), /token=|redirect=|environment=|收藏夹栏\//);
  // Tooltips have no URL, query or token, and click payloads are indexes.
  const barMarkup = await page.evaluate(() => document.querySelector("#desktop-browser-bookmarks").outerHTML);
  assert.doesNotMatch(barMarkup, /[?]|https?:\/\/|token=|redirect=|environment=|assignee=/i);
  const barAttributeLeaks = await page.locator("#desktop-browser-bookmarks [data-browser-bookmark-index], #desktop-browser-bookmarks [data-browser-bookmark-folder]")
    .evaluateAll(nodes => nodes.flatMap(node => ["title", "aria-label"].map(name => node.getAttribute(name) || ""))
      .filter(value => /[?]|:\/\//.test(value)));
  assert.deepEqual(barAttributeLeaks, [], "bookmark bar title/aria-label must not contain a URL or query");
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

  // The chevron lists the first-level folders that no longer fit plus
  // bookmark-bar URLs, and both kinds of entry are actionable. Shrinking the
  // panel while the menu is open must refresh that folder list.
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "400px"; });
  await page.waitForTimeout(180);
  assert.equal(await page.locator("#desktop-browser-bookmarks-more").isVisible(), true, "the narrow bookmark bar needs an overflow menu");
  await page.click("#desktop-browser-bookmarks-more");
  await page.waitForSelector("#desktop-browser-bookmarks-menu");
  await waitFor(page, () => document.querySelector("#desktop-browser-bookmarks-more").getBoundingClientRect().width > 0);
  const hiddenAt400 = await page.evaluate(() => [...document.querySelectorAll("[data-browser-bookmark-folder]")]
    .filter(chip => chip.hidden).map(chip => chip.dataset.browserBookmarkFolder));
  assert.ok(hiddenAt400.length > 0, "the 400px bar must already hide some folder chips");
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "260px"; });
  await waitFor(page, count => {
    const hidden = [...document.querySelectorAll("[data-browser-bookmark-folder]")]
      .filter(chip => chip.hidden).map(chip => chip.dataset.browserBookmarkFolder).sort();
    const menu = [...document.querySelectorAll("#desktop-browser-bookmarks-menu [data-browser-bookmark-menu-folder]")]
      .map(button => button.dataset.browserBookmarkMenuFolder).sort();
    return hidden.length > count && hidden.join("\n") === menu.join("\n");
  }, {arg: hiddenAt400.length});
  const hiddenFolders = await page.evaluate(() => [...document.querySelectorAll("[data-browser-bookmark-folder]")]
    .filter(chip => chip.hidden).map(chip => chip.dataset.browserBookmarkFolder));
  assert.ok(hiddenFolders.length > hiddenAt400.length,
    `shrinking must hide more folders: ${JSON.stringify({hiddenAt400, hiddenFolders})}`);
  const hiddenFolder = hiddenFolders[0];
  const moreMenu = page.locator("#desktop-browser-bookmarks-menu");
  const moreMenuText = await moreMenu.innerText();
  assert.match(moreMenuText, /更多收藏/);
  assert.match(moreMenuText, /更多文件夹/);
  assert.match(moreMenuText, /收藏栏网址/);
  assert.ok(moreMenuText.includes(hiddenFolder), `overflow menu must list the hidden folder ${hiddenFolder}: ${moreMenuText}`);
  assert.equal(await moreMenu.locator(`[data-browser-bookmark-menu-folder='${hiddenFolder}']`).count(), 1);
  // Only folders whose chips are hidden may appear; a visible folder must never
  // be duplicated into the chevron menu.
  assert.deepEqual(
    (await moreMenu.locator("[data-browser-bookmark-menu-folder]").evaluateAll(buttons => buttons.map(button => button.dataset.browserBookmarkMenuFolder))).sort(),
    hiddenFolders.slice().sort(),
    `the chevron must list exactly the hidden first-level folders: ${JSON.stringify({hiddenFolders, moreMenuText})}`,
  );
  // 「收藏栏网址」 holds only bookmarks placed directly on the bar root - never a
  // bookmark that lives inside a first-level folder or a deeper subfolder.
  assert.deepEqual(
    await moreMenu.locator("[data-browser-bookmark-index] span").evaluateAll(spans => spans.map(span => span.textContent)),
    ["工单", "Mail", "根目录直达"],
    `the chevron's URL section must list only bar-level bookmarks: ${moreMenuText}`,
  );
  // Folder-internal titles must not leak in under any real menu entry either
  // (the `<span>` of every URL row is already pinned to the bar-level set).
  for (const folderBookmark of ["Work 门户", "发布面板", "文档中心", "HUALAI 文档", "蓝牙调试", "官网", "其他", "监控"]) {
    assert.equal(await moreMenu.locator(`[data-browser-bookmark-index] span:text-is("${folderBookmark}")`).count(), 0,
      `folder bookmark "${folderBookmark}" must not leak into the chevron menu`);
  }
  assert.doesNotMatch(moreMenuText, /token=|environment=|redirect=|收藏夹栏\//);
  const moreMenuMarkup = await moreMenu.evaluate(node => node.outerHTML);
  assert.doesNotMatch(moreMenuMarkup, /[?]|https?:\/\/|token=|environment=|redirect=|assignee=/i);
  const moreAttributeLeaks = await page.locator("#desktop-browser-bookmarks-menu [data-browser-bookmark-index], #desktop-browser-bookmarks-menu [data-browser-bookmark-menu-folder]")
    .evaluateAll(nodes => nodes.flatMap(node => ["title", "aria-label"].map(name => node.getAttribute(name) || ""))
      .filter(value => /[?]|:\/\//.test(value)));
  assert.deepEqual(moreAttributeLeaks, [], "overflow menu title/aria-label must not contain a URL or query");
  const moreGeometry = await page.evaluate(() => {
    const panel = document.querySelector("#desktop-browser-control").getBoundingClientRect();
    const bar = document.querySelector("#desktop-browser-bookmarkbar").getBoundingClientRect();
    const menu = document.querySelector("#desktop-browser-bookmarks-menu").getBoundingClientRect();
    const more = document.querySelector("#desktop-browser-bookmarks-more").getBoundingClientRect();
    return {
      panelLeft: panel.left, panelRight: panel.right,
      menuLeft: menu.left, menuRight: menu.right, menuTop: menu.top,
      moreRight: more.right, barBottom: bar.bottom,
    };
  });
  assert.ok(moreGeometry.menuLeft >= moreGeometry.panelLeft - 1 && moreGeometry.menuRight <= moreGeometry.panelRight + 1,
    `the overflow menu must stay inside the narrow panel: ${JSON.stringify(moreGeometry)}`);
  assert.ok(Math.abs(moreGeometry.menuRight - moreGeometry.moreRight) <= 8,
    `the overflow menu must anchor next to the chevron: ${JSON.stringify(moreGeometry)}`);
  assert.ok(moreGeometry.menuTop >= moreGeometry.barBottom - 0.5 && moreGeometry.menuTop <= moreGeometry.barBottom + 1.5,
    `the overflow menu top must hug the bookmark bar bottom: ${JSON.stringify(moreGeometry)}`);
  // A folder row in the chevron menu drills into that folder.
  await page.click(`#desktop-browser-bookmarks-menu [data-browser-bookmark-menu-folder='${hiddenFolder}']`);
  await page.waitForSelector("#desktop-browser-bookmarks-menu .desktop-browser-bookmark-menu-head");
  assert.ok((await page.locator("#desktop-browser-bookmarks-menu .desktop-browser-bookmark-menu-head").innerText()).includes(hiddenFolder));
  await page.click("#desktop-browser-bookmarks-menu-close");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 0);
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = ""; });

  // Folder chip opens just that folder's bookmarks, with its subfolders
  // grouped inside, and the popover is anchored to the chip's left edge.
  await page.click("[data-browser-bookmark-folder='Docs']");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]");
  const docsMenu = page.locator("#desktop-browser-bookmarks-menu");
  const docsMenuText = await docsMenu.innerText();
  assert.match(docsMenuText, /文档中心/);
  assert.match(docsMenuText, /HUALAI/);
  assert.match(docsMenuText, /蓝牙/);
  assert.doesNotMatch(docsMenuText, /token=|redirect=|environment=|收藏夹栏\//);
  assert.doesNotMatch(await docsMenu.evaluate(node => node.outerHTML), /[?]|https?:\/\/|token=|redirect=|environment=|assignee=/i);
  assert.deepEqual(await page.locator("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]")
    .evaluateAll(nodes => nodes.flatMap(node => ["title", "aria-label"].map(name => node.getAttribute(name) || ""))
      .filter(value => /[?]|:\/\//.test(value))), [], "folder menu title/aria-label must not contain a URL or query");
  const docsGeometry = await page.evaluate(() => {
    const chip = document.querySelector("[data-browser-bookmark-folder='Docs']").getBoundingClientRect();
    const bar = document.querySelector("#desktop-browser-bookmarkbar").getBoundingClientRect();
    const menu = document.querySelector("#desktop-browser-bookmarks-menu").getBoundingClientRect();
    const panel = document.querySelector("#desktop-browser-control").getBoundingClientRect();
    return {
      chipLeft: chip.left, menuLeft: menu.left, menuTop: menu.top,
      barBottom: bar.bottom, menuRight: menu.right, panelRight: panel.right,
    };
  });
  assert.ok(Math.abs(docsGeometry.menuLeft - docsGeometry.chipLeft) <= 2,
    `the menu left edge must align with the clicked Docs chip: ${JSON.stringify(docsGeometry)}`);
  assert.ok(docsGeometry.menuTop >= docsGeometry.barBottom - 0.5 && docsGeometry.menuTop <= docsGeometry.barBottom + 1.5,
    `the menu top must hug the bookmark bar bottom: ${JSON.stringify(docsGeometry)}`);
  assert.ok(docsGeometry.menuRight <= docsGeometry.panelRight + 1,
    `the menu must not overflow the panel: ${JSON.stringify(docsGeometry)}`);
  // An outside click closes the menu without a repaint; reopening the same
  // folder must render again instead of trusting the cached HTML.
  await page.click("#desktop-browser-url");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 0);
  await page.click("[data-browser-bookmark-folder='Docs']");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 1, "external close must not break reopening");
  await page.keyboard.press("Escape");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 0);
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute("data-browser-bookmark-folder")), "Docs",
    "Escape must restore focus to the folder chip that opened the menu");

  // Owner-requested hover behaviour: a click opens the folder menu and it stays
  // open while the pointer moves between the chip and the popover. Leaving the
  // union of the two areas closes it after a short grace period, so the close
  // button is no longer the only way out.
  const docsChip = page.locator("[data-browser-bookmark-folder='Docs']");
  await docsChip.click();
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]");
  const chipBox = await docsChip.boundingBox();
  const hoverMenu = page.locator("#desktop-browser-bookmarks-menu");
  const menuBox = await hoverMenu.boundingBox();
  const onChip = {x: chipBox.x + chipBox.width / 2, y: chipBox.y + chipBox.height / 2};
  const onMenu = {x: menuBox.x + menuBox.width / 2, y: menuBox.y + menuBox.height / 2};
  await page.mouse.move(onChip.x, onChip.y, {steps: 2});
  await page.waitForTimeout(320);
  assert.equal(await hoverMenu.count(), 1, "the menu must stay open while the pointer rests on the chip");
  // Chip → menu: crossing the few pixels of bar padding must not close it.
  await page.mouse.move(onMenu.x, onMenu.y, {steps: 8});
  await page.waitForTimeout(320);
  assert.equal(await hoverMenu.count(), 1, "moving from the chip into its menu must not close it");
  // Menu → chip → menu: back-and-forth selection stays open.
  await page.mouse.move(onChip.x, onChip.y, {steps: 8});
  await page.waitForTimeout(320);
  assert.equal(await hoverMenu.count(), 1, "moving back to the chip must not close the menu");
  await page.mouse.move(onMenu.x, onMenu.y, {steps: 8});
  await page.waitForTimeout(320);
  assert.equal(await hoverMenu.count(), 1, "moving between the chip and the menu repeatedly must not close it");
  // Leaving both areas closes it without touching the close button.
  await page.mouse.move(onMenu.x, menuBox.y + menuBox.height + 140, {steps: 4});
  await page.waitForTimeout(520);
  assert.equal(await hoverMenu.count(), 0,
    "leaving the chip and the menu must close the folder menu after the grace period");

  // Low viewport: the menu scrolls inside the space it actually has instead of
  // poking out of the panel/viewport bottom.
  await page.setViewportSize({width: 1100, height: 240});
  await page.waitForTimeout(150);
  await page.click("[data-browser-bookmark-folder='Docs']");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]");
  const lowViewport = await page.evaluate(() => {
    const panel = document.querySelector("#desktop-browser-control").getBoundingClientRect();
    const menu = document.querySelector("#desktop-browser-bookmarks-menu");
    const menuRect = menu.getBoundingClientRect();
    return {
      panelBottom: panel.bottom, viewportBottom: window.innerHeight,
      menuBottom: menuRect.bottom, menuTop: menuRect.top,
      menuScrollHeight: menu.scrollHeight, menuClientHeight: menu.clientHeight,
    };
  });
  const bottomBoundary = Math.min(lowViewport.panelBottom, lowViewport.viewportBottom);
  assert.ok(lowViewport.menuBottom <= bottomBoundary + 0.5,
    `the menu must not cross the panel/viewport bottom: ${JSON.stringify(lowViewport)}`);
  assert.ok(lowViewport.menuScrollHeight > lowViewport.menuClientHeight,
    `a short viewport must keep the menu internally scrollable: ${JSON.stringify(lowViewport)}`);
  await page.keyboard.press("Escape");
  await page.setViewportSize({width: 1100, height: 900});
  await page.waitForTimeout(150);

  // Keyboard-only overflow menu when every entry is a folder: the first
  // focusable entry receives focus, Escape closes and returns focus to the
  // chevron. This used to fall through to BODY and leave Escape inert.
  const pureFolders = ["Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel"];
  const pureBookmarks = pureFolders.map((name, index) => ({
    title: `${name} 文档`, url: `https://folder-${index}.example/docs?token=abcdefghijklmnop`,
    folder: `收藏夹栏/${name}/Nested`,
  }));
  await page.evaluate(() => window.__panel.dispose());
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "260px"; });
  await reset(page, {__bookmarks: pureBookmarks});
  await page.waitForSelector("[data-browser-bookmark-folder='Alpha']");
  await waitFor(page, () => document.querySelector("#desktop-browser-bookmarks-more").getBoundingClientRect().width > 0);
  const hiddenPureFolders = await page.locator("[data-browser-bookmark-folder]")
    .evaluateAll(chips => chips.filter(chip => chip.hidden).map(chip => chip.dataset.browserBookmarkFolder));
  assert.ok(hiddenPureFolders.length > 0, "the pure-folder bar must hide at least one folder chip");
  await page.focus("#desktop-browser-bookmarks-more");
  await page.keyboard.press("Enter");
  await page.waitForSelector("#desktop-browser-bookmarks-menu [data-browser-bookmark-menu-folder]");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]").count(), 0,
    "a folder-only fixture renders no bookmark URL entries");
  assert.deepEqual(
    await page.locator("#desktop-browser-bookmarks-menu [data-browser-bookmark-menu-folder]")
      .evaluateAll(buttons => buttons.map(button => button.dataset.browserBookmarkMenuFolder)),
    hiddenPureFolders,
  );
  const focusedFolder = await page.evaluate(() => document.activeElement?.getAttribute("data-browser-bookmark-menu-folder") || "");
  assert.ok(pureFolders.includes(focusedFolder), `focus must land on a folder entry, got ${JSON.stringify(focusedFolder)}`);
  await page.keyboard.press("Escape");
  assert.equal(await page.locator("#desktop-browser-bookmarks-menu").count(), 0);
  assert.equal(await page.evaluate(() => document.activeElement?.id), "desktop-browser-bookmarks-more",
    "Escape must restore focus to the chevron trigger");
  await page.evaluate(() => window.__panel.dispose());
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = ""; });
  await reset(page, {__bookmarks: null});
  await page.waitForSelector("[data-browser-bookmark-folder='Docs']");

  // Regression: when only direct URL chips overflow and every folder chip fits,
  // the chevron must not fall back to listing every folder. It is then a pure
  // 「收藏栏网址」 menu, and a bookmark inside a folder never shows up there.
  const barOnlyBookmarks = [
    {title: "Solo 文档", url: "https://solo.example/docs?token=abcdefghijklmnop", folder: "收藏夹栏/Solo/Nested"},
    {title: "栏内一", url: "https://bar-one.example/", folder: "收藏夹栏"},
    {title: "栏内二", url: "https://bar-two.example/long/path", folder: ""},
    {title: "栏内三", url: "https://bar-three.example/long/path", folder: "收藏夹"},
  ];
  await page.evaluate(() => window.__panel.dispose());
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "260px"; });
  await reset(page, {__bookmarks: barOnlyBookmarks});
  await page.waitForSelector("[data-browser-bookmark-folder='Solo']");
  await waitFor(page, () => document.querySelector("#desktop-browser-bookmarks-more").getBoundingClientRect().width > 0);
  const barOnlyOverflow = await page.evaluate(() => ({
    soloHidden: document.querySelector("[data-browser-bookmark-folder='Solo']").hidden,
    hiddenFolders: [...document.querySelectorAll("[data-browser-bookmark-folder]")].filter(chip => chip.hidden).length,
    hiddenDirect: [...document.querySelectorAll("#desktop-browser-bookmarks [data-browser-bookmark-index]")].filter(chip => chip.hidden).length,
  }));
  assert.deepEqual([barOnlyOverflow.soloHidden, barOnlyOverflow.hiddenFolders], [false, 0],
    `the fixture must keep its only folder chip visible: ${JSON.stringify(barOnlyOverflow)}`);
  assert.ok(barOnlyOverflow.hiddenDirect > 0,
    `the fixture must overflow on direct URL chips only: ${JSON.stringify(barOnlyOverflow)}`);
  await page.click("#desktop-browser-bookmarks-more");
  await page.waitForSelector("#desktop-browser-bookmarks-menu");
  const barOnlyMenu = page.locator("#desktop-browser-bookmarks-menu");
  const barOnlyText = await barOnlyMenu.innerText();
  assert.doesNotMatch(barOnlyText, /更多文件夹/, "no hidden folder means no folder section");
  assert.equal(await barOnlyMenu.locator("[data-browser-bookmark-menu-folder]").count(), 0,
    "a visible folder must never be duplicated into the chevron menu");
  assert.match(barOnlyText, /收藏栏网址/);
  assert.deepEqual(
    await barOnlyMenu.locator("[data-browser-bookmark-index] span").evaluateAll(spans => spans.map(span => span.textContent)),
    ["栏内一", "栏内二", "栏内三"],
    `the chevron must list the bar-level URLs only: ${barOnlyText}`,
  );
  assert.equal(barOnlyText.includes("Solo 文档"), false,
    "a bookmark inside a first-level folder must not appear in the bar URL list");
  assert.doesNotMatch(barOnlyText, /token=|收藏夹栏\//);
  await page.keyboard.press("Escape");
  await page.evaluate(() => window.__panel.dispose());
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = ""; });
  await reset(page, {__bookmarks: null});
  await page.waitForSelector("[data-browser-bookmark-folder='Docs']");

  // 4. Switching a tab activates it in the visible browser and keeps frames.
  await page.click("[data-browser-tab='browser_tab_2']");
  await waitFor(page, () => window.__activates.length === 1);
  await waitFor(page, count => window.__frames > count, {arg: firstFrames});

  // Closing an Owner-opened page closes it under exclusive control.
  await page.click("[data-browser-tab-close='browser_tab_2']");
  await waitFor(page, () => window.__deletes.length === 1);

  // A bookmark-bar URL from the chevron menu opens through the same tab API.
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = "260px"; });
  await page.waitForTimeout(180);
  await page.click("#desktop-browser-bookmarks-more");
  await page.waitForSelector("#desktop-browser-bookmarks-menu");
  await page.locator("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]").filter({hasText: "工单"}).click();
  await waitFor(page, () => window.__opens.includes("https://tickets.example/queue?assignee=owner&status=open"));
  await waitFor(page, () => document.querySelector("#desktop-browser-url").value === "https://tickets.example/queue?assignee=owner&status=open");
  await page.locator("#desktop-tool").evaluate(node => { node.style.width = ""; });

  // Folder entries open through the same tab API.
  await page.click("[data-browser-bookmark-folder='Docs']");
  const hualaiEntry = page.locator("#desktop-browser-bookmarks-menu [data-browser-bookmark-index]").filter({hasText: "HUALAI 文档"});
  await hualaiEntry.waitFor();
  await hualaiEntry.click();
  await waitFor(page, () => window.__opens.includes("https://hualai.example/docs?token=abcdefghijklmnop"));
  await waitFor(page, () => document.querySelector("#desktop-browser-url").value === "https://hualai.example/docs?token=abcdefghijklmnop");

  // The + button opens AHA's local start page, never about:blank.
  await page.click("#desktop-browser-newtab");
  await waitFor(page, () => window.__opens.some(url => new URL(url).pathname === "/browser-newtab"));
  assert.equal(await page.evaluate(() => window.__opens.some(url => url.startsWith("about:"))), false);
  await waitFor(page, () => document.querySelector("#desktop-browser-url").value.endsWith("/browser-newtab"));

  // The header stop calls this release path; the popover marker stays open.
  const grantedBefore = await page.evaluate(() => window.__granted);
  await page.evaluate(() => window.__panel.release());
  await waitFor(page, () => document.querySelector("#desktop-browser-attach") !== null);
  assert.match(await page.locator("#desktop-browser-status").innerText(), /共享开关/);
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
  assert.match(await page.locator("#desktop-browser-status").innerText(), /浏览器共享已开启/);
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

  // 7. A profile held by another Task has no transfer control: the single
  // switch is disabled and the status says who must close it.
  await reset(page, {__mode: "tabs", __released: false, __inUse: true, __transferred: false, __opens: [], __opened: 0, __openedTabs: [], __granted: 0});
  await page.waitForSelector("#desktop-browser-attach", {timeout: 5000});
  assert.equal(await page.locator("#desktop-browser-attach").isDisabled(), true);
  assert.equal(await page.locator("#desktop-browser-transfer").count(), 0, "transfer control must not be shown");
  assert.match(await page.locator("#desktop-browser-status").innerText(), /其他 Task 使用/);
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
  await page.waitForSelector("#desktop-browser-release", {timeout: 5000});
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
  await page.waitForSelector("#desktop-browser-release", {timeout: 5000});
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

  // 14d. A held refresh that hiding invalidates must still release the loading
  // state it owned. The later tab sync sees unchanged data and repaints
  // nothing, so without that cleanup the refresh/switch buttons stay disabled
  // even after the preview comes back.
  await reset(page, {__mode: "tabs", __hostAvailable: true, __hostOnlyDown: false, __staleError: false, __released: false,
    __inUse: false, __transferred: false, __attached: true, __ownerTabs: [], __ownerClosed: [],
    __ownerNav: {}, __ownerActive: "", __frameErrorTab: "", __frameClosed: [], __syncRequests: 0,
    __syncHold: null, __syncHoldTaken: false, __syncHoldDone: false, __frames: 0, __granted: 0});
  await page.waitForSelector("#desktop-browser-release", {timeout: 5000});
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 5000});
  await page.evaluate(() => {
    window.__vis = "visible";
    Object.defineProperty(document, "visibilityState", {configurable: true, get: () => window.__vis});
  });
  await page.evaluate(() => {
    let release;
    const promise = new Promise(resolve => { release = resolve; });
    window.__syncHold = {promise, release};
    window.__syncHoldTaken = false;
    window.__syncHoldDone = false;
    document.querySelector("#desktop-browser-refresh").click();
  });
  await waitFor(page, () => window.__syncHoldTaken, {timeout: 3000});
  await page.evaluate(() => { window.__vis = "hidden"; document.dispatchEvent(new Event("visibilitychange")); });
  await waitFor(page, () => document.querySelector("#desktop-browser-frame").hidden);
  // The held refresh resolves only after hiding invalidated it.
  await page.evaluate(() => { window.__syncHold.release(); window.__syncHold = null; });
  await waitFor(page, () => window.__syncHoldDone, {timeout: 3000});
  await page.waitForTimeout(150);
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-refresh").disabled), false,
    "a dropped refresh must clear the loading state it owned");
  await page.evaluate(() => { window.__vis = "visible"; document.dispatchEvent(new Event("visibilitychange")); });
  await page.waitForSelector("#desktop-browser-frame:not([hidden])", {timeout: 8000});
  // One further regular sync applies unchanged data; the controls must already
  // be usable instead of waiting for a repaint that sync will not perform.
  const syncsAfterRestore = await page.evaluate(() => window.__syncRequests);
  await waitFor(page, count => window.__syncRequests > count, {arg: syncsAfterRestore, timeout: 6000});
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-refresh").disabled), false,
    "the refresh button must stay usable after the stale refresh lands");
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-release").disabled), false,
    "the release switch must stay usable after the stale refresh lands");
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-browser-frame").hidden), false,
    "the preview must recover after hidden→visible");
  await page.evaluate(() => { delete document.visibilityState; window.__vis = "visible"; });
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
