import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

// Integration hook for the shared-control panel: the "共享目标" popover is a
// picker only. Attaching a browser profile closes it, the main panel shows the
// tab strip/address/bookmarks/preview, and the header stop releases the lease.
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
  session: {id: "browser_session_1", profile_id: "browser_profile_1", host_id: "local", state: "ready", started_at: now, created_at: now, updated_at: now},
};
const tab = {
  id: "browser_tab_1", session_id: "browser_session_1", grant_id: "browser_grant_1", task_id: "task-1",
  url: "https://example.com/home", title: "首页", origin: "owner", active: true, created_at: now, updated_at: now,
};
const host = {available: true, host_id: "local", version: "0.1.0", platform: "win32", edge_available: true};
const bootstrap = `
import {renderDesktopPanel, bindDesktopPanel, stopDesktopPanel} from "/desktop_panel.js";
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.__granted = null;
window.__frames = 0;
window.__opens = [];
const GRANT = ${JSON.stringify(grant)};
const TAB = ${JSON.stringify(tab)};
const HOST = ${JSON.stringify(host)};
const profiles = [{id: "browser_profile_1", name: "工作浏览器", browser_kind: "edge", host_id: "local",
  state: "ready", host_available: true, created_at: ${JSON.stringify(now)}, updated_at: ${JSON.stringify(now)},
  session: GRANT.session}];
const browserState = () => ({task_id: "task-1", profiles, grants: window.__granted ? [GRANT] : [],
  tabs: window.__granted ? [TAB] : [], host: HOST});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = String(init.method || "GET").toUpperCase();
  if (path === "/api/v1/tasks/task-1/desktop") {
    return json({status: {supported: true, foreground_supported: true, targets_supported: true,
      shared_control_supported: true, stream_supported: false, session: null}});
  }
  if (path === "/api/v1/tasks/task-1/desktop/targets") {
    return json({targets: {windows: [], desktops: [], monitors: [], desktop_switch_supported: false}});
  }
  if (path === "/api/v1/tasks/task-1/browser") return json({ok: true, browser: browserState()});
  if (path === "/api/v1/tasks/task-1/browser-sessions" && method === "POST") {
    window.__granted = GRANT;
    return json({ok: true, grant: GRANT, session: GRANT.session});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1" && method === "DELETE") {
    window.__granted = null;
    return json({ok: true, grant: {...GRANT, status: "revoked"}});
  }
  if (path === "/api/v1/browser-grants/browser_grant_1/bookmarks") return json({ok: true, bookmarks: []});
  if (path === "/api/v1/browser-grants/browser_grant_1/tabs" && method === "POST") {
    const payload = JSON.parse(String(init.body || "{}"));
    window.__opens.push(payload.url);
    return json({ok: true, tab: {...TAB, id: "browser_tab_opened", url: payload.url}});
  }
  if (/\\/tabs\\/[a-z0-9_]+\\/frame$/.test(path)) {
    window.__frames++;
    return new Response(new Uint8Array([0xff, 0xd8, 0xff, 1, 0xff, 0xd9]), {status: 200, headers: {"Content-Type": "image/jpeg"}});
  }
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.mountPanel = () => {
  stopDesktopPanel();
  const fixture = document.querySelector("#fixture");
  fixture.innerHTML = "";
  const detail = {task: {id: "task-1", read_only: false}};
  fixture.innerHTML = renderDesktopPanel(detail);
  bindDesktopPanel(detail, () => {});
};
window.unmountPanel = () => stopDesktopPanel();
window.mountPanel();
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body>
  <main id="fixture"></main><script type="module" src="/fixture.js"></script>
</body></html>`;
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
    } else if (name === "/browser-newtab") {
      response.setHeader("Content-Type", "text/html");
      response.end(await readFile(resolve(root, "web/build/browser-newtab.html")));
    } else {
      if (!/^\/(?:vendor\/)?[a-z0-9_-]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
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
    if (await page.evaluate(predicate)) return;
    await page.waitForTimeout(50);
  }
  throw new Error("timed out waiting for the split browser panel");
};
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1200, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(base, {waitUntil: "networkidle"});

  // The mode picker lives inside the targets popover, so open it first.
  await page.click('[data-desktop-popover="targets"]');
  assert.equal(await page.locator("#desktop-targets-popover").isVisible(), true);
  await page.selectOption("#desktop-control-mode", "browser");
  await page.waitForSelector("#desktop-browser-attach", {timeout: 5000});

  // The popover is a picker, not a browser surface.
  const popover = page.locator("#desktop-targets-popover");
  assert.equal(await popover.locator("#desktop-browser-profile").count(), 1);
  assert.equal(await popover.locator("#desktop-browser-attach").count(), 1);
  for (const forbidden of ["#desktop-browser-frame", "[data-browser-tab]", "#desktop-browser-url", "#desktop-browser-bookmarks", ".desktop-browser-preview"]) {
    assert.equal(await popover.locator(forbidden).count(), 0, `popover must not contain ${forbidden}`);
  }

  // Attach closes the popover and hands the main panel to the browser.
  await page.click("#desktop-browser-attach");
  await waitFor(page, () => document.querySelector("#desktop-targets-popover").hidden);
  await page.waitForSelector("#desktop-browser-tabs");
  assert.equal(await page.locator("#desktop-targets-popover").isVisible(), false);
  assert.equal(await page.locator("#desktop-browser-control").isVisible(), true);
  assert.equal(await page.locator("#desktop-browser-tabs [data-browser-tab]").count(), 1);
  assert.equal(await page.locator("#desktop-browser-bookmarkbar").isVisible(), true);
  assert.equal(await page.locator(".desktop-browser-preview").isVisible(), true);
  assert.equal(await page.locator("#desktop-stop").isEnabled(), true);

  // The target button reopens the picker; with a lease its primary action says
  // "切换到所选目标" and contains no preview/tab/url/bookmarks.
  await page.click('[data-desktop-popover="targets"]');
  assert.equal(await page.locator("#desktop-targets-popover").isVisible(), true);
  assert.equal(await page.locator("#desktop-browser-switch").innerText(), "切换到所选目标");
  assert.equal(await popover.locator(".desktop-browser-preview").count(), 0);
  assert.equal(await popover.locator("#desktop-browser-tabs").count(), 0);
  await page.click("#desktop-browser-switch");
  assert.equal(await page.locator("#desktop-targets-popover").isVisible(), false);

  // Header stop releases the browser lease instead of stopping a desktop share.
  const writesBefore = await page.evaluate(() => window.__opens.length);
  await page.click("#desktop-stop");
  await waitFor(page, () => window.__granted === null);
  await waitFor(page, () => document.querySelector("#desktop-browser-attach") !== null);
  assert.equal(await page.locator("#desktop-browser-control .desktop-browser-empty-state").count(), 1);
  assert.equal(await page.evaluate(() => window.__opens.length), writesBefore, "stop must not open pages");

  // Reopening the picker still shows only the compact form.
  await page.click('[data-desktop-popover="targets"]');
  assert.equal(await page.locator("#desktop-browser-attach").count(), 1);
  assert.equal(await popover.locator("#desktop-browser-frame").count(), 0);
  assert.equal(await popover.locator("#desktop-browser-bookmarks").count(), 0);
  await page.evaluate(() => window.unmountPanel());
  assert.deepEqual(errors, []);
  console.log("shared-targets popover split and header release verified");
} finally {
  await browser?.close();
  server.close();
}
