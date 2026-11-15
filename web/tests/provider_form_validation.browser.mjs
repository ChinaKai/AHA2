// Regression coverage for the Provider dialog's field-level validation.
// The dialog is button-driven (not a <form>), so native `required` never runs:
// this harness drives the real built bundle and proves empty/whitespace values
// stay in the dialog, surface inline errors, focus the first invalid field,
// clear the error on input, and leave the successful path unchanged at 390px.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = process.env.AHA2_WEB_VALIDATION_DIR || resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const useInstalledBrowser = process.env.AHA2_PLAYWRIGHT_CHROMIUM === "1";

const PROVIDERS = [{id: "provider-1", name: "Provider One", base_url: "https://example.test/v1", credential_configured: true, revision: 1}];
const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const now = new Date().toISOString();
const providers = ${JSON.stringify(PROVIDERS)};
window.__providerWrites = [];
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
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: now}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/env-groups") return json({env_groups: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path === "/api/v1/providers" && method === "GET") return json({providers});
  if (path === "/api/v1/providers" && method === "POST") {
    window.__providerWrites.push({method, path, body: JSON.parse(init.body || "{}")});
    return json({provider: {...providers[0], id: "provider-2", name: "Edge", base_url: "https://edge.example/v1"}}, 201);
  }
  if (path === "/api/v1/providers/provider-1" && method === "PUT") {
    window.__providerWrites.push({method, path, body: JSON.parse(init.body || "{}")});
    return json({provider: {...providers[0], ...JSON.parse(init.body || "{}")}});
  }
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "models"}));
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
  if (useInstalledBrowser) {
    browser = await chromium.launch({headless: true});
  } else {
    const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
    browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  }
  const page = await browser.newPage({viewport: {width: 390, height: 844}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.stack || error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('[data-dialog="provider"]', {timeout: 15000});
  const writes = () => page.evaluate(() => window.__providerWrites);
  const isOpen = () => page.locator("#provider-dialog").evaluate(dialog => dialog.open);
  const activeID = () => page.evaluate(() => document.activeElement?.id || "");
  const fieldState = id => page.evaluate(selector => {
    const input = document.querySelector(selector);
    const error = document.querySelector(selector + "-error");
    return {value: input?.value, invalid: input?.getAttribute("aria-invalid"), describedBy: input?.getAttribute("aria-describedby"), error: error?.textContent || ""};
  }, id);

  // 1. Whitespace-only values stay in the dialog. Both fields mark themselves
  //    invalid and the first one takes focus; nothing is written.
  await page.evaluate(() => document.querySelector('[data-dialog="provider"]')?.click());
  await page.waitForSelector("#provider-dialog[open]", {timeout: 5000});
  await page.fill("#provider-name", "   ");
  await page.fill("#provider-base-url", "   ");
  await page.click("#save-provider");
  await page.waitForTimeout(150);
  assert.equal(await isOpen(), true, "empty submit closed the Provider dialog");
  assert.deepEqual(await writes(), [], "empty submit wrote a Provider");
  assert.equal((await fieldState("#provider-name")).error, "请填写 Provider 名称", "the name field has no inline error");
  assert.equal((await fieldState("#provider-base-url")).error, "请填写 API Base URL", "the base URL field has no inline error");
  assert.equal(await page.locator("#provider-name-error").isVisible(), true, "the name inline error is not visible");
  assert.equal(await page.locator("#provider-base-url-error").isVisible(), true, "the base URL inline error is not visible");
  assert.equal((await fieldState("#provider-name")).invalid, "true");
  assert.equal((await fieldState("#provider-name")).describedBy, "provider-name-error");
  assert.equal(await activeID(), "provider-name", "focus did not move to the first invalid field");
  assert.equal(await page.locator(".banner").count(), 0, "validation added a global toast");

  // 2. Typing clears only that field's error; the other error stays until its
  //    own field is edited.
  await page.fill("#provider-name", "Edge");
  assert.equal((await fieldState("#provider-name")).error, "", "typing did not clear the name error");
  assert.equal((await fieldState("#provider-name")).invalid, null, "the name field stayed aria-invalid after typing");
  assert.equal((await fieldState("#provider-base-url")).error, "请填写 API Base URL", "editing the name cleared the base URL error");
  assert.equal(await page.locator("#provider-base-url-error").isVisible(), true);

  // The dialog and its inline errors still fit a 390px viewport.
  const box = await page.locator("#provider-dialog").boundingBox();
  assert.ok(box && box.x >= 0 && box.x + box.width <= 390.5, `Provider dialog overflows the 390px viewport: ${JSON.stringify(box)}`);
  assert.ok(await page.locator("#provider-dialog").evaluate(dialog => dialog.scrollWidth <= dialog.clientWidth), "inline errors cause horizontal overflow in the dialog");

  // 3. Filling the remaining field clears its error and the successful path is
  //    unchanged: one POST, the dialog closes.
  await page.fill("#provider-base-url", "https://edge.example/v1");
  assert.equal((await fieldState("#provider-base-url")).error, "", "typing did not clear the base URL error");
  await page.click("#save-provider");
  await page.waitForFunction(() => window.__providerWrites.length === 1, null, {timeout: 5000});
  await page.waitForFunction(() => !document.querySelector("#provider-dialog")?.open, null, {timeout: 5000});
  const created = await writes();
  assert.equal(created[0].method, "POST");
  assert.equal(created[0].body.name, "Edge");
  assert.equal(created[0].body.base_url, "https://edge.example/v1");

  // 4. Editing an existing Provider uses the same inline validation: an empty
  //    base URL focuses that field and writes nothing.
  await page.waitForSelector('[data-edit-provider="provider-1"]', {timeout: 10000});
  await page.evaluate(() => document.querySelector('[data-edit-provider="provider-1"]')?.click());
  await page.waitForSelector("#provider-dialog[open]", {timeout: 5000});
  await page.fill("#provider-base-url", "   ");
  await page.click("#save-provider");
  await page.waitForTimeout(150);
  assert.equal(await isOpen(), true, "empty edit submit closed the Provider dialog");
  assert.equal((await writes()).length, 1, "empty edit submit wrote a Provider");
  assert.equal((await fieldState("#provider-name")).error, "", "an already-valid name was marked invalid");
  assert.equal((await fieldState("#provider-base-url")).error, "请填写 API Base URL", "the edit form has no base URL inline error");
  assert.equal(await activeID(), "provider-base-url", "focus did not move to the invalid base URL field");

  // Picking a preset fills both fields, so the stale required error must go too.
  await page.selectOption("#provider-preset", "openai");
  assert.equal((await fieldState("#provider-base-url")).error, "", "a preset left the stale base URL error behind");
  assert.equal((await fieldState("#provider-base-url")).value, "https://api.openai.com/v1");
  assert.equal((await writes()).length, 1, "selecting a preset wrote a Provider");

  assert.deepEqual(errors, [], "the Provider dialog threw while being driven");
  console.log("Provider empty-value inline validation verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}