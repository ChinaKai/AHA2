// Optional browser verification; dependencies and browser files stay in .tools.
// A machine without .tools/web-validation can point AHA_PLAYWRIGHT_MODULE /
// AHA_BROWSER_EXECUTABLE at a local install.
//
// Taking over a remote Task must rebind ADB hardware on its own terms: a USB
// direct group asks for a local device serial (never a host/port), network
// direct and wireless keep host:port and carry their connection form, so the
// server cannot reinterpret one form as another.
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

const now = "2026-11-21T00:00:00Z";
const TASK = {id: "t-remote", code: "task-926", title: "远端 ADB", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-remote", collaboration_mode: "single", max_agents: 1, original_request: "调试设备", current_goal: "调试设备", read_only: true, read_only_reason: "remote_device", owner_device_id: "device-remote", created_at: now, updated_at: now};
const group = (id, description, network) => ({task_id: TASK.id, id, description, mode: "network", serial: {device: "", baudrate: 115200}, network, password_configured: false, access: "read_only", position: 0, created_at: now, updated_at: now});
const HARDWARE = [
  group("usb", "USB 板", {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: "REMOTE-SER"}),
  group("tv", "电视", {host: "10.0.0.9", port: 5601, protocol: "adb", ssh_auth: "auto", adb_connection: "tcp"}),
  // Synced from an older peer: no adb_connection, so it is wireless.
  group("phone", "手机", {host: "10.0.0.10", port: 41235, protocol: "adb", ssh_auth: "auto"}),
];
const data = {
  task: TASK, hardware: HARDWARE,
  projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false}],
  workspaces: [{id: "workspace-local", project_id: "project-1", name: "本机", root_path: "/tmp/ws", transport: "native", locality: "local", health: "ready", read_only: false}],
  models: [{id: "model-1", display_name: "Model One", provider_id: "provider-1", backend: "codex", wire_model: "model-one", source: "provider", default_env_group_id: "env-1", created_at: now, updated_at: now}],
};
const bootstrap = `
const data = ${JSON.stringify(data)};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: data.task.id, task: data.task, agentID: "main"}));
window.__takeover = null;
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [data.task], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/tasks/" + data.task.id + "/takeover" && method === "POST") {
    window.__takeover = JSON.parse(init.body || "{}");
    return json({error: "fixture_stop", message: "fixture stops here"}, 400);
  }
  if (path === "/api/v1/tasks/" + data.task.id && method === "GET") return json({task: data.task, turns: [], agents: [{task_id: data.task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: data.hardware, memory: {}});
  if (path === "/api/v1/tasks/" + data.task.id + "/agents") return json({agents: [{task_id: data.task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}]});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path === "/api/v1/projects") return json({projects: data.projects, has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: data.workspaces});
  if (path === "/api/v1/models") return json({models: data.models});
  if (path === "/api/v1/env-groups") return json({env_groups: [{id: "env-1", name: "Env", provider_id: "provider-1", backend: "codex"}]});
  if (path === "/api/v1/providers") return json({providers: [{id: "provider-1", name: "Provider", base_url: "https://example.test/v1", credential_configured: true, revision: 1}]});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/mcp-servers") return json({servers: []});
  if (path === "/api/v1/channels") return json({channels: []});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
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
  await page.waitForSelector("#takeover-task", {timeout: 15000});
  await page.click("#takeover-task");
  await page.waitForSelector("#task-takeover-dialog[open]");
  const fieldset = id => page.locator(`.takeover-hardware[data-hardware-id="${id}"]`);

  // USB direct: a required local serial, no host/port inputs.
  const usb = fieldset("usb");
  assert.equal(await usb.locator('[data-field="network-adb-serial"]').count(), 1);
  assert.equal(await usb.locator('[data-field="network-adb-serial"]').evaluate(input => input.required), true);
  assert.equal(await usb.locator('[data-field="network-adb-serial"]').getAttribute("placeholder"), "REMOTE-SER");
  assert.equal(await usb.locator('[data-field="network-host"]').count(), 0, "USB group asked for a host");
  assert.equal(await usb.locator('[data-field="network-port"]').count(), 0, "USB group asked for a port");
  // Network direct and wireless keep host:port and carry their form.
  for (const [id, form] of [["tv", "tcp"], ["phone", "wireless"]]) {
    assert.equal(await fieldset(id).locator('[data-field="network-adb-connection"]').inputValue(), form, `${id} connection form`);
    assert.equal(await fieldset(id).locator('[data-field="network-host"]').evaluate(input => input.required), true);
    assert.equal(await fieldset(id).locator('[data-field="network-adb-serial"]').count(), 0, `${id} asked for a USB serial`);
  }
  assert.equal(await fieldset("tv").locator('[data-field="network-port"]').inputValue(), "5601");

  // The runtime catalog (backend/model) is outside this test; give the two
  // required runtime selects a value so only hardware fields decide validity.
  await page.evaluate(() => {
    for (const name of ["backend", "model_id"]) {
      const select = document.querySelector(`#task-takeover-form select[name="${name}"]`);
      if (select && !select.value) {
        select.add(new Option("fixture", "fixture-" + name));
        select.value = "fixture-" + name;
      }
    }
  });
  // Submitting without the local serial is blocked by the form itself.
  await fieldset("tv").locator('[data-field="network-host"]').fill("10.0.0.19");
  await fieldset("phone").locator('[data-field="network-host"]').fill("10.0.0.20");
  await page.locator('#task-takeover-form button[type="submit"]').click();
  assert.equal(await page.evaluate(() => window.__takeover), null, "takeover submitted without a local USB serial");

  await usb.locator('[data-field="network-adb-serial"]').fill("LOCAL-SER");
  await page.locator('#task-takeover-form button[type="submit"]').click();
  await page.waitForFunction(() => window.__takeover !== null, null, {timeout: 10000});
  const sent = await page.evaluate(() => window.__takeover);
  const byID = Object.fromEntries(sent.groups.map(item => [item.id, item.network]));
  assert.equal(byID.usb.protocol, "adb");
  assert.equal(byID.usb.adb_connection, "usb");
  assert.equal(byID.usb.adb_serial, "LOCAL-SER");
  assert.equal(byID.usb.host, "");
  assert.equal(byID.usb.port, 0);
  assert.deepEqual([byID.tv.adb_connection, byID.tv.host, byID.tv.port, byID.tv.adb_serial], ["tcp", "10.0.0.19", 5601, ""]);
  assert.deepEqual([byID.phone.adb_connection, byID.phone.host, byID.phone.port, byID.phone.adb_serial], ["wireless", "10.0.0.20", 41235, ""]);
  assert.deepEqual(errors, []);
  console.log("task takeover ADB rebinding verified");
} finally {
  await browser?.close();
  server.close();
}
