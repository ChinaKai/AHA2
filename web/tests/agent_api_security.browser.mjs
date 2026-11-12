// Optional browser verification; dependencies and browser files stay in .tools.
//
// The Agent API address is no longer configurable from the normal Workspace UI:
// AHA picks the path itself. This drives the real built bundle to verify the two
// things that remain: the read-only result, and the hidden manual override in
// Advanced Settings. A source-text assertion would not catch a form that renders
// but posts the wrong field, which is the failure that matters here.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const output = resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

// The settings the advanced page reads. A stored override is present so the
// panel has a value to render, and there is no insecure switch to flip.
const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.__puts = [];
const agentAPI = () => ({
  url: "https://override.example.test",
  startup_url: "http://127.0.0.1:8766",
  effective_url: "https://override.example.test",
  migration_note: "旧版局域网明文地址已禁用",
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/security" ) return json({security: {validate_origin: true, access_scope: "lan", startup_override: false}});
  if (path === "/api/v1/settings/network") return json({network: {listen_address: "", startup_listen_address: "0.0.0.0:8766", restart_required: false}});
  if (path === "/api/v1/settings/backend") return json({backend: {idle_timeout_seconds: 300, turn_timeout_seconds: 3600}});
  if (path === "/api/v1/settings/agent-api" && method === "GET") return json({agent_api: agentAPI()});
  if (path === "/api/v1/settings/agent-api" && method === "PUT") {
    const body = JSON.parse(init.body || "{}");
    window.__puts.push(body);
    return json({ok: true, agent_api: {...agentAPI(), url: body.url, effective_url: body.url || "http://127.0.0.1:8766", migration_note: ""}});
  }
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/system") return json({ok: true, web_version: "v0.0.0.test"});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

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
      const file = name.startsWith("/vendor/")
        ? resolve(root, "web", name.slice(1))
        : resolve(root, "web/dist", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 1000}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  // Attached, not visible: the nav entry can be inside a collapsed group at this
  // viewport, and the click below is dispatched rather than performed by a user.
  await page.waitForSelector('[data-view="advanced"]', {state: "attached", timeout: 15000});
  await page.evaluate(() => {
    const entry = document.querySelector('[data-view="advanced"]');
    if (!entry) throw new Error("advanced settings entry not found");
    entry.click();
  });
  await page.waitForSelector('[data-view="other"]', {timeout: 15000});
  await page.evaluate(() => {
    const entry = document.querySelector('[data-view="other"]');
    if (!entry) throw new Error("other settings entry not found");
    entry.click();
  });
  await page.waitForSelector("#agent-api-override-form", {timeout: 15000});

  // The insecure switch is gone; a checkbox that still rendered would be dead
  // weight that suggests plaintext is a supported choice.
  assert.equal(await page.locator('#agent-api-override-form input[name="allow_insecure"]').count(), 0,
    "the removed insecure switch is still rendered");
  const panelText = await page.locator(".agent-api-override-panel").innerText();
  assert.match(panelText, /仅用于排障/, "the override no longer says it is a troubleshooting path");
  assert.match(panelText, /旧版局域网明文地址已禁用/, "the migration diagnosis is not shown");

  const input = page.locator('#agent-api-override-form input[name="url"]');
  assert.equal((await input.inputValue()), "https://override.example.test");
  await input.fill("https://troubleshoot.example.test");
  await page.locator("#agent-api-override-form button[type=submit]").click();
  await page.waitForFunction(() => window.__puts.length > 0, null, {timeout: 10000});

  const puts = await page.evaluate(() => window.__puts);
  assert.equal(puts.length, 1, `expected one settings write, got ${puts.length}: ${JSON.stringify(puts)}`);
  assert.equal(puts[0].url, "https://troubleshoot.example.test", `the write did not carry the override: ${JSON.stringify(puts[0])}`);
  assert.deepEqual(Object.keys(puts[0]), ["url"], `the write carried removed fields: ${JSON.stringify(puts[0])}`);
  assert.deepEqual(errors, [], "the settings page threw while saving");

  await page.screenshot({path: resolve(output, "agent-api-override.png")});
  console.log("agent api override PUT:", JSON.stringify(puts[0]));
} finally {
  if (browser) await browser.close();
  server.close();
}
