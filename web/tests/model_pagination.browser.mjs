// Optional browser verification for the model page pagination: the three
// panels must cap their rows, expose page counts, and reset the model page when
// the search filter changes.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir, writeFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const output = resolve(tools, "screenshots");
await mkdir(output, {recursive: true});

const providers = Array.from({length: 20}, (_, index) => ({
  id: `provider-${index + 1}`,
  name: `Provider ${String(index + 1).padStart(2, "0")}`,
  base_url: `https://provider-${index + 1}.example.test/v1`,
  credential_configured: true,
}));
const models = Array.from({length: 25}, (_, index) => ({
  id: `model-${index + 1}`,
  display_name: index % 5 === 0 ? `Claude Model ${index + 1}` : `Codex Model ${index + 1}`,
  provider_id: `provider-${(index % 20) + 1}`,
  provider_name: `Provider ${String((index % 20) + 1).padStart(2, "0")}`,
  backend: index % 5 === 0 ? "claude" : "codex",
  wire_model: `wire-${index + 1}`,
  wire_api: index % 5 === 0 ? "anthropic_messages" : "responses",
  context_window: 100000,
  max_output_tokens: 8000,
  auto_compact_window: 70000,
  capabilities: {},
}));
const accounts = Array.from({length: 12}, (_, index) => ({
  id: `account-${index + 1}`,
  label: `Account ${index + 1}`,
  email: `user${index + 1}@example.test`,
  plan_type: "Plus",
  status: "ready",
  proxy_enabled: false,
  credential_configured: true,
  owner_bound: true,
  usage: {rate_limits: [], credits: {has_credits: true, unlimited: false, overage_limit_reached: false}},
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
}));
const data = {providers, models, accounts};

const bootstrap = `
const data = ${JSON.stringify(data)};
window.__deletedModels = [];
window.__batchDeleteRequests = [];
window.__batchDeleteIDs = [];
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/settings/security") return json({security: {validate_origin: true, access_scope: "lan", startup_override: false}});
  if (path === "/api/v1/settings/network") return json({network: {listen_address: "", startup_listen_address: "0.0.0.0:8766", restart_required: false}});
  if (path === "/api/v1/settings/backend") return json({backend: {idle_timeout_seconds: 300, turn_timeout_seconds: 3600}});
  if (path === "/api/v1/settings/agent-api") return json({agent_api: {url: "", startup_url: "http://127.0.0.1:8766", effective_url: "http://127.0.0.1:8766"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/providers") return json({providers: data.providers});
  if (path === "/api/v1/models") return json({models: data.models});
  if (path === "/api/v1/models/batch-delete" && method === "POST") {
    window.__batchDeleteRequests.push(path);
    window.__batchDeleteIDs.push(JSON.parse(init.body || "{}").model_ids || []);
    return json({ok: true, approval: {id: "approval-batch", kind: "delete_models", title: "批量删除 20 个模型", status: "pending"}}, 202);
  }
  if (path.startsWith("/api/v1/models/") && method === "DELETE") { window.__deletedModels.push(path); return json({ok: true}); }
  if (path === "/api/v1/codex-accounts") return json({accounts: data.accounts});
  if (path === "/api/v1/env-groups") return json({env_groups: []});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
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
    const file = resolve(root, "web", "build", name.slice(1));
    const bytes = await readFile(file);
    response.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    response.end(bytes);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1440, height: 900}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=models]")?.click());
  await page.waitForSelector("#model-search", {timeout: 15000});
  await page.waitForFunction(() => document.querySelectorAll(".models-table tbody tr.model-row").length === 20, null, {timeout: 15000});

  assert.equal(await page.locator(".models-table tbody tr.model-row").count(), 20);
  assert.equal(await page.locator("#model-search").getAttribute("readonly"), "");
  assert.match(await page.locator(".list-footer-models").textContent(), /共 25 个/);
  assert.match(await page.locator(".list-footer-models").textContent(), /第 1\/2 页/);
  // The visible page is grouped by Provider; every group header names the
  // provider and carries its model count.
  assert.ok(await page.locator(".model-group-row").count() > 0);
  assert.match(await page.locator(".model-group-row").first().textContent(), /Provider 01/);
  assert.match(await page.locator(".model-group-row").first().textContent(), /共 1 个/);
  assert.equal(await page.locator(".provider-row").count(), 5);
  assert.match(await page.locator(".list-footer-providers").textContent(), /第 1\/4 页/);
  assert.equal(await page.locator(".codex-account-card").count(), 3);
  assert.match(await page.locator(".list-footer-codex-accounts").textContent(), /第 1\/4 页/);

  // Click through evaluate: the app re-renders on its polling cadence, which
  // makes Playwright's stability wait unreliable for replaced buttons.
  await page.evaluate(() => document.querySelector('[data-page-kind="models"][data-page-delta="1"]')?.click());
  await page.waitForFunction(() => document.querySelector(".list-footer-models")?.textContent?.includes("第 2/2 页"), null, {timeout: 15000});
  assert.equal(await page.locator(".models-table tbody tr.model-row").count(), 5);
  assert.match(await page.locator(".models-table tbody tr.model-row").first().textContent(), /Model 21/);

  // A browser autofill can write the saved username into the first text input
  // before the user touches it. That must be ignored, not treated as a filter.
  await page.evaluate(() => {
    const input = document.querySelector("#model-search");
    if (!input) return;
    input.value = "kaikai";
    input.dispatchEvent(new InputEvent("input", {bubbles: true, inputType: "insertReplacementText", data: "kaikai"}));
  });
  await page.waitForTimeout(100);
  assert.equal(await page.locator("#model-search").inputValue(), "");
  assert.equal(await page.locator(".models-table tbody tr.model-row").count(), 5);
  assert.match(await page.locator(".list-footer-models").textContent(), /第 2\/2 页/);

  // A real edit (focus + keydown + input) applies the filter and resets paging.
  await page.evaluate(() => {
    const input = document.querySelector("#model-search");
    input.focus();
    input.dispatchEvent(new KeyboardEvent("keydown", {key: "a", bubbles: true}));
    input.value = "claude";
    input.dispatchEvent(new InputEvent("input", {bubbles: true, inputType: "insertText", data: "claude"}));
  });
  await page.waitForFunction(() => document.querySelector(".list-footer-models")?.textContent?.includes("匹配 5 / 共 25 个"), null, {timeout: 15000});
  assert.equal(await page.locator(".models-table tbody tr.model-row").count(), 5);
  assert.doesNotMatch(await page.locator(".list-footer-models").textContent(), /第 \d+\/\d+ 页/);

  // Entering the Models view again starts from a clean search and page 1.
  // Blur first: a real nav click moves focus, while a scripted click would leave
  // the search box focused and let the app defer the view render.
  await page.evaluate(() => {
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    document.querySelector("[data-view=projects]")?.click();
  });
  await page.waitForTimeout(300);
  await page.evaluate(() => document.querySelector("[data-view=models]")?.click());
  await page.waitForFunction(() => document.querySelector("#model-search")?.value === "" && document.querySelectorAll(".models-table tbody tr.model-row").length === 20, null, {timeout: 15000});
  assert.match(await page.locator(".list-footer-models").textContent(), /第 1\/2 页/);

  // Select all on the page, then submit one batch delete approval. The row
  // checkboxes drive a bar that submits a single request with every id.
  await page.evaluate(() => {
    const input = document.querySelector("[data-select-models-page]");
    if (!(input instanceof HTMLInputElement)) return;
    input.checked = true;
    input.dispatchEvent(new Event("change", {bubbles: true}));
  });
  await page.waitForFunction(() => document.querySelector(".model-batch-bar")?.textContent?.includes("已选 20 个模型"), null, {timeout: 15000});
  await page.evaluate(() => document.querySelector("[data-batch-delete-models]")?.click());
  await page.waitForFunction(() => window.__batchDeleteRequests?.length === 1, null, {timeout: 15000});
  assert.equal(await page.evaluate(() => window.__batchDeleteIDs[0].length), 20);
  await page.waitForFunction(() => !document.querySelector(".model-batch-bar"), null, {timeout: 15000});

  // Row actions are delegated through #model-results so they keep working after
  // a search refresh replaces the table body.
  await page.evaluate(() => document.querySelector(".models-table tbody tr.model-row [data-edit-model]")?.click());
  await page.waitForSelector("#model-edit-dialog[open]", {timeout: 5000});
  assert.equal(await page.locator("#model-edit-name").inputValue(), "Claude Model 1");
  await page.evaluate(() => document.querySelector("#model-edit-dialog")?.close());

  await page.evaluate(() => document.querySelector(".models-table tbody tr.model-row [data-delete-model]")?.click());
  await page.waitForFunction(() => window.__deletedModels?.length === 1, null, {timeout: 5000});
  assert.equal(await page.evaluate(() => window.__deletedModels[0]), "/api/v1/models/model-1");

  const client = await page.context().newCDPSession(page);
  const desktop = await client.send("Page.captureScreenshot", {format: "png", captureBeyondViewport: true});
  await writeFile(resolve(output, "model-pagination.png"), Buffer.from(desktop.data, "base64"));

  await page.setViewportSize({width: 420, height: 900});
  await page.waitForTimeout(300);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert.ok(overflow <= 1, `model page overflows horizontally by ${overflow}px`);
  const mobile = await client.send("Page.captureScreenshot", {format: "png", captureBeyondViewport: true});
  await writeFile(resolve(output, "model-pagination-mobile.png"), Buffer.from(mobile.data, "base64"));
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
console.log("model pagination browser check passed");
