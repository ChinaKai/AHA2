// Optional browser verification; dependencies and browser files stay in .tools.
//
// Adding a hardware group and saving it should leave the console ready to
// connect that group. The Owner reports having to reload the page first, so this
// drives the real panel through add -> configure -> save and then asks whether
// the console is usable for the group just saved, without a reload.
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

const TASK = {
  id: "t-hw", code: "task-903", title: "Hardware", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "hw", current_goal: "hw", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
};

// One existing serial group, so the panel opens on a working console.
const EXISTING = [{
  id: "hardware-1", task_id: TASK.id, position: 0, description: "Board A", mode: "serial",
  serial: {device: "/dev/ttyUSB0", baudrate: 115200},
  network: {host: "", port: 23, protocol: "telnet", ssh_auth: "auto"},
  username: "", password_configured: false, access: "read_write",
  created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
}];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
let GROUPS = ${JSON.stringify(EXISTING)};
window.__saved = null;
window.__connectCalls = [];
window.__terminalStatusCalls = [];
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: GROUPS, memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/tasks/t-hw" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-hw/hardware" && method === "PUT") {
    const body = JSON.parse(init.body || "{}");
    window.__saved = body;
    // The server keeps the ids the client sends, and echoes the stored groups.
    GROUPS = (body.groups || []).map((group, index) => Object.assign({}, group, {
      task_id: TASK.id, position: index, password_configured: Boolean(group.password),
    }));
    return json({ok: true, groups: GROUPS});
  }
  if (path.includes("/hardware/") && path.endsWith("/connect")) {
    window.__connectCalls.push({group: path, transport: new URL(url, location.origin).searchParams.get("transport")});
    const id = path.split("/hardware/")[1].split("/")[0];
    return json({ok: true, status: {connected: true, read_only: false, status: "connected", endpoint: id + " endpoint", error: ""}});
  }
  if (path.includes("/hardware/") && path.endsWith("/terminal")) {
    const id = path.split("/hardware/")[1].split("/")[0];
    window.__terminalStatusCalls.push(id);
    return json({status: {connected: false, read_only: false, status: "idle", endpoint: id, error: ""}, stream: {items: [], latest_sequence: 0}});
  }
  if (path === "/api/v1/hardware/serial-ports") return json({ports: [{device: "/dev/ttyUSB0", description: "USB Serial"}]});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path === "/api/v1/tasks/t-hw/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks/t-hw/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/channels") return json({channels: []});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
// jsdom-free environment: the panel only reads this for the mobile layout check.
window.matchMedia = window.matchMedia || (() => ({matches: false, addEventListener() {}, removeEventListener() {}}));
// No navigation preset: the panel is mounted directly below, and letting the app
// open a task detail would drag the whole task view (and its channel data) into
// this test, which is not what is under test.
sessionStorage.removeItem("aha2.navigation");
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

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
      if (!/^\/(?:vendor\/)?[a-z_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
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
  await page.waitForSelector('[data-view="tasks"]', {state: "attached", timeout: 15000});

  // Mount the real panel directly. The task-tool navigation is incidental to
  // this defect -- it lives in the panel's own state handling -- and driving the
  // whole app would make the test depend on layout and routing.
  const mounted = await page.evaluate(async () => {
    const mod = await import("/hardware_panel.js");
    const detail = await (await fetch("/api/v1/tasks/t-hw")).json();
    document.querySelector("#app").innerHTML = '<section id="task-tool-panel-body"></section>';
    document.querySelector("#task-tool-panel-body").innerHTML = mod.renderHardwarePanel(detail);
    window.__notices = [];
    mod.bindHardwarePanel(detail, (kind, message) => { window.__notices.push(kind + ":" + message); });
    return document.querySelector("#hardware-add") !== null;
  });
  assert.equal(mounted, true, "the hardware panel did not mount");
  // The config section is a collapsed <details> once a group exists; open it the
  // way a user would before touching anything inside.
  await page.evaluate(() => {
    const details = document.querySelector("#task-tool-panel-body details.hardware-config");
    if (details) details.open = true;
  });
  await page.waitForSelector("#hardware-add", {timeout: 15000});
  assert.deepEqual(errors, [], "the panel threw while opening");

  // Add a network/SSH group and configure it, exactly as a user would.
  await page.evaluate(() => document.querySelector("#hardware-add").click());
  await page.waitForTimeout(200);
  const form = page.locator("#hardware-config-form");
  await form.locator('select[name="mode"]').selectOption("network");
  await form.locator('input[name="network_host"]').fill("192.168.0.50");
  await form.locator('input[name="network_port"]').fill("22");
  await form.locator('select[name="network_protocol"]').selectOption("ssh");
  // SSH mode makes the login name required, so fill it as a user must.
  await form.locator('input[name="username"]').fill("kaikai");
  await page.waitForTimeout(150);
  await page.evaluate(() => document.querySelector("#hardware-config-form").requestSubmit());
  await page.waitForFunction(() => window.__saved !== null, null, {timeout: 10000});

  const saved = await page.evaluate(() => window.__saved);
  const newID = saved.groups[saved.groups.length - 1].id;

  // The console must now be usable for the group that was just saved: the group
  // is selected, the transport matches it, and connect is available.
  const view = await page.evaluate(() => {
    const select = document.querySelector("#hardware-group-select");
    const connect = document.querySelector("#hardware-connect");
    const networkButton = document.querySelector('[data-hardware-transport="network"]');
    return {
      selected: select ? select.value : null,
      options: select ? [...select.options].map(option => option.value) : [],
      connectDisabled: connect ? connect.disabled : null,
      networkActive: networkButton ? networkButton.classList.contains("active") : null,
      networkDisabled: networkButton ? networkButton.disabled : null,
      status: (document.querySelector("#hardware-status") || {}).textContent || "",
    };
  });
  console.log("after save:", JSON.stringify({newID, ...view}));

  assert.ok(view.options.includes(newID), `the new group is not in the picker: ${JSON.stringify(view)}`);
  assert.equal(view.selected, newID, `the picker did not follow the saved group: ${JSON.stringify(view)}`);
  assert.equal(view.networkActive, true, `the transport did not follow the group's mode: ${JSON.stringify(view)}`);
  assert.equal(view.connectDisabled, false, `connect is unavailable for the group just saved: ${JSON.stringify(view)}`);

  // And the console must actually poll that group, which is what makes it usable.
  await page.evaluate(() => { window.__terminalStatusCalls.length = 0; });
  await page.evaluate(() => document.querySelector("#hardware-connect").click());
  await page.waitForFunction(() => window.__connectCalls.length > 0, null, {timeout: 10000});
  const calls = await page.evaluate(() => ({connect: window.__connectCalls, terminal: window.__terminalStatusCalls}));
  assert.ok(calls.connect[0].group.includes(newID), `connect targeted the wrong group: ${JSON.stringify(calls.connect)}`);
  assert.equal(calls.connect[0].transport, "network", `connect used the wrong transport: ${JSON.stringify(calls.connect)}`);
  assert.deepEqual(errors, [], "the panel threw while connecting");

  await page.screenshot({path: resolve(output, "hardware-add-connect.png")});
  console.log("connect call:", JSON.stringify(calls.connect[0]));
} finally {
  if (browser) await browser.close();
  server.close();
}
