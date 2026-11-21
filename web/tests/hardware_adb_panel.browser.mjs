// Optional browser verification; dependencies and browser files stay in .tools.
// A machine without .tools/web-validation can point AHA_PLAYWRIGHT_MODULE /
// AHA_BROWSER_EXECUTABLE at a local install.
//
// The Task hardware panel must present the three ADB connection forms on
// their own terms: USB direct by serial (never an empty host:port), network
// direct and wireless by host:port, and pairing state only for wireless.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const TASK = {
  id: "t-adb", code: "task-926", title: "ADB", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "adb", current_goal: "adb", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-11-21T00:00:00Z", updated_at: "2026-11-21T00:00:00Z",
};
const stamp = {created_at: "2026-11-21T00:00:00Z", updated_at: "2026-11-21T00:00:00Z"};
const RESOURCES = [
  {id: "usb", description: "USB board", mode: "network", serial: {device: "", baudrate: 115200},
    network: {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: "SERIAL-A"},
    password_configured: false, access: "read_write", binding_count: 1, ...stamp},
  {id: "tv", description: "TV", mode: "network", serial: {device: "", baudrate: 115200},
    network: {host: "192.0.2.70", port: 5601, protocol: "adb", ssh_auth: "auto", adb_connection: "tcp"},
    password_configured: false, access: "read_write", binding_count: 1, ...stamp},
  // Legacy row from an older server: no adb_connection, already paired.
  {id: "phone", description: "Phone", mode: "network", serial: {device: "", baudrate: 115200},
    network: {host: "192.0.2.71", port: 41235, protocol: "adb", ssh_auth: "auto"},
    password_configured: false, adb_paired_at: "2026-11-20T00:00:00Z", access: "read_write", binding_count: 1, ...stamp},
  {id: "usb-empty", description: "USB unset", mode: "network", serial: {device: "", baudrate: 115200},
    network: {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: ""},
    password_configured: false, access: "read_write", binding_count: 1, ...stamp},
];
const GROUPS = RESOURCES.map((resource, index) => ({...resource, resource_id: resource.id, task_id: TASK.id, position: index}));

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const GROUPS = ${JSON.stringify(GROUPS)};
const RESOURCES = ${JSON.stringify(RESOURCES)};
window.__connectCalls = [];
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: GROUPS, hardware_resources: RESOURCES, memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks/t-adb") return json(detail());
  if (path.includes("/hardware/") && path.endsWith("/connect")) {
    window.__connectCalls.push(path.split("/hardware/")[1].split("/")[0]);
    return json({ok: true, status: {connected: false, read_only: false, status: "idle", endpoint: "", error: ""}});
  }
  if (path.includes("/hardware/") && path.endsWith("/terminal")) {
    return json({status: {connected: false, read_only: false, status: "idle", endpoint: "", error: ""}, stream: {items: [], latest_sequence: 0}});
  }
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/channels") return json({channels: []});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.matchMedia = window.matchMedia || (() => ({matches: false, addEventListener() {}, removeEventListener() {}}));
sessionStorage.removeItem("aha2.navigation");
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
      const file = name.startsWith("/vendor/") ? resolve(root, "web", name.slice(1)) : resolve(root, "web/build", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(done => server.listen(0, "127.0.0.1", done));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 1000}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector('[data-view="tasks"]', {state: "attached", timeout: 15000});
  await page.evaluate(async () => {
    const mod = await import("/hardware_panel.js");
    const detail = await (await fetch("/api/v1/tasks/t-adb")).json();
    document.querySelector("#app").innerHTML = '<section id="task-tool-panel-body"></section>';
    document.querySelector("#task-tool-panel-body").innerHTML = mod.renderHardwarePanel(detail);
    mod.bindHardwarePanel(detail, () => {});
    const details = document.querySelector("details.hardware-config");
    if (details) details.open = true;
    window.__panel = mod;
    window.__detail = detail;
  });
  const choiceText = id => page.locator(`[data-hardware-resource-select="${id}"]`).innerText();
  const usbChoice = await choiceText("usb");
  assert.match(usbChoice, /ADB（USB 直连） SERIAL-A/);
  assert.doesNotMatch(usbChoice, /配对|:0\b/);
  const tvChoice = await choiceText("tv");
  assert.match(tvChoice, /ADB（网络直连） 192\.0\.2\.70:5601/);
  assert.doesNotMatch(tvChoice, /配对/);
  const phoneChoice = await choiceText("phone");
  assert.match(phoneChoice, /ADB（无线调试） 192\.0\.2\.71:41235/);
  assert.match(phoneChoice, /已配对/);

  const selectGroup = async id => {
    await page.selectOption("#hardware-group-select", id);
    await page.waitForFunction(value => document.querySelector("#hardware-group-select")?.value === value, id);
    return page.evaluate(() => ({
      endpoint: document.querySelector("#hardware-endpoint")?.textContent || "",
      connectDisabled: document.querySelector("#hardware-connect")?.disabled ?? null,
      label: document.querySelector(".hardware-transport-label")?.textContent || "",
    }));
  };
  const usb = await selectGroup("usb");
  assert.equal(usb.endpoint, "USB SERIAL-A", `USB endpoint: ${JSON.stringify(usb)}`);
  assert.equal(usb.connectDisabled, false, "USB group with a serial must be connectable");
  const tv = await selectGroup("tv");
  assert.equal(tv.endpoint, "192.0.2.70:5601");
  assert.equal(tv.connectDisabled, false);
  const empty = await selectGroup("usb-empty");
  assert.equal(empty.connectDisabled, true, "USB group without a serial must not be connectable");
  assert.doesNotMatch(empty.endpoint, /:0|undefined/);
  assert.deepEqual(errors, []);
  console.log("hardware ADB panel verified");
} finally {
  await browser?.close();
  server.close();
}
