// Optional browser verification for the Knowledge subpages. These pages render
// most of their labels from TypeScript, so the source-level i18n test cannot
// catch a label that the DOM translator skips. This drives the real bundle and
// checks global knowledge, unbound libraries, Skills, and their editors.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));

const now = "2026-10-31T09:00:00Z";
const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "en-US"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: "${now}"}});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Sample project", project_type: "folder"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/knowledge") return json({knowledge: [], proposals: [], review_settings: {auto_approve: false}, has_more: true, next_cursor: "next"});
  if (path === "/api/v1/knowledge/libraries") return json({libraries: [{id: "library-1", container_project_id: "project-1", name: "Imported Library", description: "", knowledge_count: 2, skill_count: 1, bound_project_id: "", created_at: "${now}", updated_at: "${now}"}]});
  if (path === "/api/v1/knowledge/indexes") return json({indexes: []});
  if (path === "/api/v1/skills") return json({skills: [{id: "skill-1", package_slug: "skill-1", scope: "global", project_id: "", name: "Skill One", description: "", instructions: "Use it.", version: 1, status: "active", enabled: true, files: ["SKILL.md"], created_at: "${now}", updated_at: "${now}"}]});
  if (path.startsWith("/api/v1/product-lines")) return json({product_lines: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;

const html = `<!doctype html><html lang="en-US"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
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
    const file = resolve(root, "web", "dist", name.slice(1));
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
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}, locale: "en-US"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector("[data-knowledge-area=global]", {timeout: 15000});
  assert.equal((await page.locator("[data-knowledge-area=global] span").textContent()).trim(), "Global knowledge");
  assert.equal((await page.locator("[data-knowledge-area=libraries] span").textContent()).trim(), "Unbound");
  assert.equal((await page.locator("[data-knowledge-area=skills] span").textContent()).trim(), "Skills");
  await page.waitForFunction(() => document.querySelector("[data-load-more=knowledge]")?.textContent === "Load more", null, {timeout: 10000});

  const scan = async (label) => {
    const missing = await page.evaluate(() => {
      const root = document.querySelector("#app");
      const skip = [".markdown-body", ".message-text", ".message-bubble", ".task-title-text", "[data-task-title]", ".task-card-title h3", ".item-title strong", ".knowledge-tree-item-title", "pre", "code", "textarea", "input", "[contenteditable='true']", ".skill-card p", ".knowledge-library-card p", "[data-i18n-ignore]"].join(",");
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      const out = [];
      while (walker.nextNode()) {
        const el = walker.currentNode.parentElement;
        if (!el || el.closest(skip)) continue;
        const text = (walker.currentNode.nodeValue || "").trim();
        if (text && /[一-鿿]/.test(text)) out.push(text.slice(0, 100));
      }
      for (const el of root.querySelectorAll("[placeholder],[title],[aria-label]")) {
        if (el.closest(skip)) continue;
        for (const name of ["placeholder", "title", "aria-label"]) {
          const value = el.getAttribute(name);
          if (value && /[一-鿿]/.test(value)) out.push(`${name}=${value.slice(0, 100)}`);
        }
      }
      return [...new Set(out)];
    });
    assert.deepEqual(missing, [], `${label} left untranslated Chinese text`);
  };

  for (const area of ["global", "libraries", "skills"]) {
    await page.evaluate(a => document.querySelector(`[data-knowledge-area="${a}"]`)?.click(), area);
    await page.waitForTimeout(300);
    await scan(`knowledge ${area}`);
    if (area === "global") {
      await page.evaluate(() => document.querySelector("[data-knowledge-new]")?.click());
      await page.waitForTimeout(200);
      await scan("global knowledge editor");
      assert.equal(await page.locator("#knowledge-editor-form input[name=title]").getAttribute("placeholder"), "e.g. Local development workflow");
      await page.evaluate(() => document.querySelector("#knowledge-editor-dialog [data-kb-close]")?.click());
    } else if (area === "skills") {
      await page.evaluate(() => document.querySelector("[data-skill-new]")?.click());
      await page.waitForTimeout(200);
      await scan("Skill editor");
      assert.equal(await page.locator("#skill-editor-form input[name=description]").getAttribute("placeholder"), "Used when deciding whether this Skill applies to a Task");
    }
  }
  await page.evaluate(() => document.querySelector("[data-knowledge-area=updates]")?.click());
  await page.waitForTimeout(300);
  await scan("knowledge updates");
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
console.log("knowledge i18n browser check passed");
