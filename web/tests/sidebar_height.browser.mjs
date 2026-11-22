// Real shell geometry against local API fixtures; run after build-web.mjs.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile, writeFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const output = process.env.AHA_BROWSER_SCREENSHOT_DIR;
assert.ok(output, "Set AHA_BROWSER_SCREENSHOT_DIR to a scratch directory");
await mkdir(output, {recursive: true});
const tools = process.env.AHA_WEB_VALIDATION || resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs")));
const version = "v0.5.557+6a5099dbe859974cc723929a467cbb444424f0bf";
const bootstrap = `
const json = value => new Response(JSON.stringify(value), {headers: {"Content-Type": "application/json"}});
const language = new URL(location.href).searchParams.get("lang") || "zh-CN";
window.fetch = async input => {
  const path = new URL(typeof input === "string" ? input : input.url, location.origin).pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "fixture", owner: {id: "owner", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({general: {language}});
  if (path === "/api/v1/system") return json({system: {web_version: ${JSON.stringify(version)}, started_at: "2025-01-01T00:00:00Z"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "p1", name: "Demo", project_type: "folder"}], has_more: false});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  return json({});
};
window.EventSource = class {close() {} addEventListener() {}};
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
const server = createServer(async (request, response) => {
  const path = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    response.setHeader("Content-Type", path.endsWith(".js") ? "text/javascript" : path.endsWith(".css") ? "text/css" : path.endsWith(".svg") ? "image/svg+xml" : path.endsWith(".png") ? "image/png" : "text/html");
    if (path === "/") response.end(html);
    else if (path === "/fixture.js") response.end(bootstrap);
    else if (/^\/(?:[a-z0-9_]+\.(?:js|css)|assets\/[a-zA-Z0-9_/-]+\.(?:svg|png))$/.test(path)) response.end(await readFile(resolve(root, "web/build", path.slice(1))));
    else response.writeHead(404).end();
  } catch { response.writeHead(404).end(); }
});
await new Promise(done => server.listen(0, "127.0.0.1", done));
const url = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({headless: true, executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]});
const results = [], failures = [], errors = [];
const nav = ["projects", "tasks", "channels", "knowledge", "models", "resources", "pet-workbench"];
const selectors = [".command-palette-trigger", ...nav.map(id => `.sidebar nav [data-view="${id}"]`), "#owner-avatar", "#advanced-settings", "#logout"];
async function geometry(page) {
  return page.evaluate(selectors => {
    const sidebar = document.querySelector(".sidebar");
    const read = selector => {
      const node = document.querySelector(selector), r = node.getBoundingClientRect();
      return {selector, x: r.x, y: r.y, right: r.right, bottom: r.bottom, height: r.height};
    };
    const memo = document.querySelector(".sidebar-memo"), list = document.querySelector(".sidebar-memo-list");
    return {height: innerHeight, width: innerWidth, overflow: sidebar.scrollHeight - sidebar.clientHeight,
      sidebarBottom: sidebar.getBoundingClientRect().bottom, boxes: selectors.map(read),
      memoVisible: getComputedStyle(memo).display !== "none", memoHeight: memo.getBoundingClientRect().height,
      listOverflow: list.scrollHeight - list.clientHeight};
  }, selectors);
}
function fits(g, label) {
  assert.ok(g.overflow <= 1, `${label}: whole sidebar overflows by ${g.overflow}px`);
  assert.ok(g.sidebarBottom <= g.height + 1, `${label}: sidebar taller than viewport`);
  for (const box of g.boxes) {
    assert.ok(box.height >= 30, `${label}: ${box.selector} retains its target size`);
    assert.ok(box.y >= 0 && box.bottom <= g.height + 1 && box.x >= 0 && box.right <= g.width, `${label}: ${box.selector} outside viewport: ${JSON.stringify(box)}`);
  }
}
try {
  for (const language of ["zh-CN", "en-US"]) {
    for (const count of [0, 1, 24]) {
      const page = await browser.newPage({viewport: {width: 1024, height: 900}});
      page.on("pageerror", error => errors.push(error.message));
      await page.addInitScript(({count}) => {
        localStorage.setItem("aha2.sidebar-memo", JSON.stringify(Array.from({length: count}, (_, i) => ({id: `memo-${i}`, text: `便签 ${i} / Note ${i}: long text that wraps onto a second line`, done: i === 1}))));
        localStorage.setItem("aha2.sidebar-layout", JSON.stringify({width: 228, memoCollapsed: false, memoColor: "blue"}));
      }, {count});
      await page.goto(`${url}/?lang=${language}#projects`, {waitUntil: "networkidle"});
      await page.waitForSelector("#logout");
      const saved = await page.evaluate(() => [localStorage.getItem("aha2.sidebar-memo"), localStorage.getItem("aha2.sidebar-layout")]);
      // 819x600 is the effective CSS viewport of 1024x750 at 125% browser zoom.
      for (const [width, height] of [[1024,600], [1024,650], [1024,720], [1024,768], [1024,799], [1024,800], [1024,900], [1280,600], [1440,900], [819,600]]) {
        await page.setViewportSize({width, height});
        const label = `${language}-${count}-${width}x${height}`;
        const g = await geometry(page);
        results.push({label, ...g});
        try {
          fits(g, label);
          if (height < 800) assert.equal(g.memoVisible, false, `${label}: short viewport hides notes`);
          if (height >= 800) assert.equal(g.memoVisible, true, `${label}: tall viewport restores notes`);
          if (height === 900 && count === 24) assert.ok(g.listOverflow > 0, `${label}: long notes scroll internally`);
        } catch (error) { failures.push(error.message); }
        if (count === 24 && ((width === 1024 && [600,800,900].includes(height)) || (language === "en-US" && width === 819))) {
          await page.screenshot({path: resolve(output, `${label}.png`)});
        }
      }
      assert.deepEqual(await page.evaluate(() => [localStorage.getItem("aha2.sidebar-memo"), localStorage.getItem("aha2.sidebar-layout")]), saved, "height changes preserve note data, color and fold preference");
      assert.equal(await page.locator(".system-meta > span").first().textContent(), `AHA2 ${version}`, "full version remains readable");
      assert.ok((await page.locator("#system-uptime").innerText()).length > 5);
      await page.setViewportSize({width: 1024, height: 600});
      await page.evaluate(() => { document.body.style.fontSize = "20px"; });
      try { fits(await geometry(page), `${language}-${count}-larger-text`); } catch (error) { failures.push(error.message); }
      await page.evaluate(() => { document.body.style.fontSize = ""; });
      await page.setViewportSize({width: 1024, height: 800});
      await page.evaluate(() => document.querySelector(".app-shell").style.setProperty("--sidebar-width", "180px"));
      try { fits(await geometry(page), `${language}-${count}-minimum-sidebar-width`); } catch (error) { failures.push(error.message); }
      await page.evaluate(() => document.querySelector(".app-shell").style.setProperty("--sidebar-width", "228px"));
      if (count === 24) {
        await page.setViewportSize({width: 1024, height: 900});
        const last = page.locator('[data-sidebar-memo-edit="memo-23"]');
        await last.focus();
        await page.keyboard.press("Enter");
        assert.equal(await page.locator('[data-sidebar-memo-text="memo-23"]').isVisible(), true, "keyboard reaches last note");
        try { fits(await geometry(page), "editing last note"); } catch (error) { failures.push(error.message); }
        await page.locator("#toggle-sidebar-memo").click();
        const folded = await page.evaluate(() => localStorage.getItem("aha2.sidebar-layout"));
        await page.setViewportSize({width: 1024, height: 600});
        await page.setViewportSize({width: 1024, height: 900});
        assert.equal(await page.locator(".sidebar-memo").evaluate(node => node.classList.contains("collapsed")), true);
        assert.equal(await page.evaluate(() => localStorage.getItem("aha2.sidebar-layout")), folded);
      }
      // Below the supported height, scroll must still expose the account controls.
      await page.setViewportSize({width: 1024, height: 360});
      await page.locator("#logout").scrollIntoViewIfNeeded();
      const logout = await page.locator("#logout").boundingBox();
      assert.ok(logout.y >= 0 && logout.y + logout.height <= 361, "very short screen retains scroll access");
      await page.setViewportSize({width: 390, height: 844});
      assert.equal(await page.locator(".sidebar").isVisible(), false);
      const mobile = page.locator('.bottom-nav [data-view="channels"]');
      const pet = page.locator('.bottom-nav [data-view="pet-workbench"]');
      const mobileBox = await mobile.boundingBox();
      assert.ok(mobileBox.y >= 0 && mobileBox.y + mobileBox.height <= 845);
      if (!process.env.AHA_SIDEBAR_BASELINE) {
        assert.notEqual(await mobile.locator("svg").innerHTML(), await pet.locator("svg").innerHTML());
      }
      await mobile.click();
      await page.waitForSelector(".channels-page");
      if (count === 1 && language === "zh-CN") await page.screenshot({path: resolve(output, "mobile-390x844.png")});
      await page.close();
    }
  }
  assert.deepEqual(errors, [], "no runtime / ResizeObserver errors");
} finally {
  await writeFile(resolve(output, "geometry.json"), JSON.stringify({results, failures, errors}, null, 2));
  await browser.close();
  server.close();
}
console.log(`${results.length} geometry cases; ${failures.length} failures`);
assert.deepEqual(failures, []);
