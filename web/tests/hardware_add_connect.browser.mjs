// Optional browser verification; dependencies and browser files stay in .tools.
//
// Enabling a system hardware resource from a Task should leave the terminal
// ready without asking the user to recreate the physical connection.
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

// One existing binding, so the panel opens on a working console.
const EXISTING = [{
  id: "resource-a", resource_id: "resource-a", task_id: TASK.id, position: 0, description: "Board A", mode: "serial",
  serial: {device: "/dev/ttyUSB0", baudrate: 115200},
  network: {host: "", port: 23, protocol: "telnet", ssh_auth: "auto"},
  username: "", password_configured: false, access: "read_write",
  created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
}];
const RESOURCES = [
  {
    id: "resource-a", description: "Board A", mode: "serial",
    serial: {device: "/dev/ttyUSB0", baudrate: 115200},
    network: {host: "", port: 23, protocol: "telnet", ssh_auth: "auto"},
    password_configured: false, access: "read_write", binding_count: 1,
    created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
  },
  {
    id: "resource-b", description: "Network Board B", mode: "network",
    serial: {device: "", baudrate: 115200},
    network: {host: "192.168.0.50", port: 22, protocol: "ssh", ssh_auth: "auto"},
    username: "kaikai", password_configured: false, access: "read_write", binding_count: 0,
    created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
  },
];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
let GROUPS = ${JSON.stringify(EXISTING)};
const RESOURCES = ${JSON.stringify(RESOURCES)};
window.__saved = null;
window.__connectCalls = [];
window.__terminalStatusCalls = [];
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: GROUPS, hardware_resources: RESOURCES, memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/tasks/t-hw" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-hw/hardware-bindings" && method === "PUT") {
    const body = JSON.parse(init.body || "{}");
    window.__saved = body;
    GROUPS = (body.bindings || []).map((binding, index) => {
      const resource = RESOURCES.find(item => item.id === binding.resource_id);
      return {...resource, id: binding.id, resource_id: resource.id, task_id: TASK.id, position: index};
    });
    return json({ok: true, groups: GROUPS, resources: RESOURCES});
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
    return document.querySelectorAll("[data-hardware-resource-toggle]").length;
  });
  assert.equal(mounted, 2, "the task panel did not list system resources");
  // The config section is a collapsed <details> once a group exists; open it the
  // way a user would before touching anything inside.
  await page.evaluate(() => {
    const details = document.querySelector("#task-tool-panel-body details.hardware-config");
    if (details) details.open = true;
  });
  await page.waitForSelector('[data-hardware-resource-toggle="resource-b"]', {timeout: 15000});
  assert.deepEqual(errors, [], "the panel threw while opening");

  // Enable the second system resource for this Task and save the binding.
  await page.click('[data-hardware-resource-toggle="resource-b"]');
  await page.waitForFunction(() => window.__saved !== null, null, {timeout: 10000});

  const saved = await page.evaluate(() => window.__saved);
  const newID = saved.bindings[saved.bindings.length - 1].id;
  assert.deepEqual(saved.bindings.map(item => item.resource_id), ["resource-a", "resource-b"]);
  assert.equal(Object.hasOwn(saved.bindings[0], "access"), false);
  await page.selectOption("#hardware-group-select", newID);

  // The enabled resource becomes usable immediately, with its own transport and
  // task-level access.
  const view = await page.evaluate(() => {
    const select = document.querySelector("#hardware-group-select");
    const connect = document.querySelector("#hardware-connect");
    return {
      selected: select ? select.value : null,
      options: select ? [...select.options].map(option => option.value) : [],
      connectDisabled: connect ? connect.disabled : null,
      transportLabel: (document.querySelector(".hardware-transport-label") || {}).textContent || "",
      transportSwitches: document.querySelectorAll("[data-hardware-transport]").length,
      status: (document.querySelector("#hardware-status") || {}).textContent || "",
    };
  });
  console.log("after save:", JSON.stringify({newID, ...view}));

  assert.ok(view.options.includes(newID), `the new group is not in the picker: ${JSON.stringify(view)}`);
  assert.equal(view.selected, newID, `the picker did not follow the saved group: ${JSON.stringify(view)}`);
  assert.equal(view.transportLabel, "网络", `the transport did not follow the resource: ${JSON.stringify(view)}`);
  assert.equal(view.transportSwitches, 0, `transport switches remained in the task panel: ${JSON.stringify(view)}`);
  assert.equal(view.connectDisabled, false, `connect is unavailable for the enabled resource: ${JSON.stringify(view)}`);

  // And the console must actually poll that group, which is what makes it usable.
  await page.evaluate(() => { window.__terminalStatusCalls.length = 0; });
  await page.evaluate(() => document.querySelector("#hardware-connect").click());
  await page.waitForFunction(() => window.__connectCalls.length > 0, null, {timeout: 10000});
  const calls = await page.evaluate(() => ({connect: window.__connectCalls, terminal: window.__terminalStatusCalls}));
  assert.ok(calls.connect[0].group.includes(newID), `connect targeted the wrong group: ${JSON.stringify(calls.connect)}`);
  assert.equal(calls.connect[0].transport, "network", `connect used the wrong transport: ${JSON.stringify(calls.connect)}`);
  assert.deepEqual(errors, [], "the panel threw while connecting");

  await page.screenshot({path: resolve(output, "hardware-add-connect.png")});
  await page.setViewportSize({width: 390, height: 844});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
  await page.screenshot({path: resolve(output, "hardware-add-connect-mobile.png")});
  console.log("connect call:", JSON.stringify(calls.connect[0]));
} finally {
  if (browser) await browser.close();
  server.close();
}
