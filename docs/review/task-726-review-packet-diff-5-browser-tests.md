# task-726 review packet diff-5-browser-tests：浏览器测试

`git diff 43624a4 336d47b -- web/tests/hardware_resources.browser.mjs web/tests/hardware_adb_panel.browser.mjs web/tests/task_takeover_adb.browser.mjs` 原样输出。

~~~~diff
diff --git a/web/tests/hardware_adb_panel.browser.mjs b/web/tests/hardware_adb_panel.browser.mjs
new file mode 100644
index 0000000..074f2e8
--- /dev/null
+++ b/web/tests/hardware_adb_panel.browser.mjs
@@ -0,0 +1,163 @@
+// Optional browser verification; dependencies and browser files stay in .tools.
+// A machine without .tools/web-validation can point AHA_PLAYWRIGHT_MODULE /
+// AHA_BROWSER_EXECUTABLE at a local install.
+//
+// The Task hardware panel must present the three ADB connection forms on
+// their own terms: USB direct by serial (never an empty host:port), network
+// direct and wireless by host:port, and pairing state only for wireless.
+import assert from "node:assert/strict";
+import {createServer} from "node:http";
+import {readFile} from "node:fs/promises";
+import {resolve} from "node:path";
+import {pathToFileURL} from "node:url";
+
+const root = resolve(import.meta.dirname, "../..");
+const tools = resolve(root, ".tools/web-validation");
+const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
+const {chromium} = await import(pathToFileURL(playwrightModule));
+const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
+  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
+  : await (async () => {
+      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
+      return {executablePath: await binary.executablePath(), args: binary.args};
+    })();
+const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
+
+const TASK = {
+  id: "t-adb", code: "task-926", title: "ADB", status: "active",
+  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
+  original_request: "adb", current_goal: "adb", task_branch: "",
+  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
+  created_at: "2026-11-21T00:00:00Z", updated_at: "2026-11-21T00:00:00Z",
+};
+const stamp = {created_at: "2026-11-21T00:00:00Z", updated_at: "2026-11-21T00:00:00Z"};
+const RESOURCES = [
+  {id: "usb", description: "USB board", mode: "network", serial: {device: "", baudrate: 115200},
+    network: {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: "SERIAL-A"},
+    password_configured: false, access: "read_write", binding_count: 1, ...stamp},
+  {id: "tv", description: "TV", mode: "network", serial: {device: "", baudrate: 115200},
+    network: {host: "192.0.2.70", port: 5601, protocol: "adb", ssh_auth: "auto", adb_connection: "tcp"},
+    password_configured: false, access: "read_write", binding_count: 1, ...stamp},
+  // Legacy row from an older server: no adb_connection, already paired.
+  {id: "phone", description: "Phone", mode: "network", serial: {device: "", baudrate: 115200},
+    network: {host: "192.0.2.71", port: 41235, protocol: "adb", ssh_auth: "auto"},
+    password_configured: false, adb_paired_at: "2026-11-20T00:00:00Z", access: "read_write", binding_count: 1, ...stamp},
+  {id: "usb-empty", description: "USB unset", mode: "network", serial: {device: "", baudrate: 115200},
+    network: {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: ""},
+    password_configured: false, access: "read_write", binding_count: 1, ...stamp},
+];
+const GROUPS = RESOURCES.map((resource, index) => ({...resource, resource_id: resource.id, task_id: TASK.id, position: index}));
+
+const bootstrap = `
+const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
+const TASK = ${JSON.stringify(TASK)};
+const GROUPS = ${JSON.stringify(GROUPS)};
+const RESOURCES = ${JSON.stringify(RESOURCES)};
+window.__connectCalls = [];
+const detail = () => ({
+  task: TASK, turns: [],
+  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
+  latest_round: null, hardware: GROUPS, hardware_resources: RESOURCES, memory: {},
+});
+window.fetch = async (input, init = {}) => {
+  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
+  const path = url.pathname;
+  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
+  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
+  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
+  if (path === "/api/v1/tasks/t-adb") return json(detail());
+  if (path.includes("/hardware/") && path.endsWith("/connect")) {
+    window.__connectCalls.push(path.split("/hardware/")[1].split("/")[0]);
+    return json({ok: true, status: {connected: false, read_only: false, status: "idle", endpoint: "", error: ""}});
+  }
+  if (path.includes("/hardware/") && path.endsWith("/terminal")) {
+    return json({status: {connected: false, read_only: false, status: "idle", endpoint: "", error: ""}, stream: {items: [], latest_sequence: 0}});
+  }
+  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
+  if (path === "/api/v1/workspaces") return json({workspaces: []});
+  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
+  if (path === "/api/v1/channels") return json({channels: []});
+  if (path.startsWith("/api/v1/")) return json({});
+  return json({});
+};
+window.EventSource = class { constructor() {} close() {} addEventListener() {} };
+window.matchMedia = window.matchMedia || (() => ({matches: false, addEventListener() {}, removeEventListener() {}}));
+sessionStorage.removeItem("aha2.navigation");
+`;
+
+const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
+const server = createServer(async (request, response) => {
+  const name = new URL(request.url, "http://localhost").pathname;
+  try {
+    response.setHeader("Content-Security-Policy", csp);
+    if (name === "/" || name === "/index.html") {
+      response.setHeader("Content-Type", "text/html");
+      response.end(html);
+    } else if (name === "/fixture.js") {
+      response.setHeader("Content-Type", "text/javascript");
+      response.end(bootstrap);
+    } else {
+      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
+      const file = name.startsWith("/vendor/") ? resolve(root, "web", name.slice(1)) : resolve(root, "web/build", name.slice(1));
+      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
+      response.end(await readFile(file));
+    }
+  } catch { response.writeHead(404).end(); }
+});
+await new Promise(done => server.listen(0, "127.0.0.1", done));
+const url = `http://127.0.0.1:${server.address().port}`;
+
+let browser;
+try {
+  browser = await chromium.launch({...launchOptions, headless: true});
+  const page = await browser.newPage({viewport: {width: 1280, height: 1000}});
+  const errors = [];
+  page.on("pageerror", error => errors.push(error.message));
+  await page.goto(url, {waitUntil: "networkidle"});
+  await page.waitForSelector('[data-view="tasks"]', {state: "attached", timeout: 15000});
+  await page.evaluate(async () => {
+    const mod = await import("/hardware_panel.js");
+    const detail = await (await fetch("/api/v1/tasks/t-adb")).json();
+    document.querySelector("#app").innerHTML = '<section id="task-tool-panel-body"></section>';
+    document.querySelector("#task-tool-panel-body").innerHTML = mod.renderHardwarePanel(detail);
+    mod.bindHardwarePanel(detail, () => {});
+    const details = document.querySelector("details.hardware-config");
+    if (details) details.open = true;
+    window.__panel = mod;
+    window.__detail = detail;
+  });
+  const choiceText = id => page.locator(`[data-hardware-resource-select="${id}"]`).innerText();
+  const usbChoice = await choiceText("usb");
+  assert.match(usbChoice, /ADB（USB 直连） SERIAL-A/);
+  assert.doesNotMatch(usbChoice, /配对|:0\b/);
+  const tvChoice = await choiceText("tv");
+  assert.match(tvChoice, /ADB（网络直连） 192\.0\.2\.70:5601/);
+  assert.doesNotMatch(tvChoice, /配对/);
+  const phoneChoice = await choiceText("phone");
+  assert.match(phoneChoice, /ADB（无线调试） 192\.0\.2\.71:41235/);
+  assert.match(phoneChoice, /已配对/);
+
+  const selectGroup = async id => {
+    await page.selectOption("#hardware-group-select", id);
+    await page.waitForFunction(value => document.querySelector("#hardware-group-select")?.value === value, id);
+    return page.evaluate(() => ({
+      endpoint: document.querySelector("#hardware-endpoint")?.textContent || "",
+      connectDisabled: document.querySelector("#hardware-connect")?.disabled ?? null,
+      label: document.querySelector(".hardware-transport-label")?.textContent || "",
+    }));
+  };
+  const usb = await selectGroup("usb");
+  assert.equal(usb.endpoint, "USB SERIAL-A", `USB endpoint: ${JSON.stringify(usb)}`);
+  assert.equal(usb.connectDisabled, false, "USB group with a serial must be connectable");
+  const tv = await selectGroup("tv");
+  assert.equal(tv.endpoint, "192.0.2.70:5601");
+  assert.equal(tv.connectDisabled, false);
+  const empty = await selectGroup("usb-empty");
+  assert.equal(empty.connectDisabled, true, "USB group without a serial must not be connectable");
+  assert.doesNotMatch(empty.endpoint, /:0|undefined/);
+  assert.deepEqual(errors, []);
+  console.log("hardware ADB panel verified");
+} finally {
+  await browser?.close();
+  server.close();
+}
diff --git a/web/tests/hardware_resources.browser.mjs b/web/tests/hardware_resources.browser.mjs
index 3d3484c..60bb09f 100644
--- a/web/tests/hardware_resources.browser.mjs
+++ b/web/tests/hardware_resources.browser.mjs
@@ -6,9 +6,17 @@ import {pathToFileURL} from "node:url";
 
 const root = resolve(import.meta.dirname, "../..");
 const tools = resolve(root, ".tools/web-validation");
-const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
-const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
-const output = resolve(tools, "screenshots");
+// A machine without .tools/web-validation can point AHA_PLAYWRIGHT_MODULE /
+// AHA_BROWSER_EXECUTABLE (and AHA_SCREENSHOT_DIR) at a local install.
+const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
+const {chromium} = await import(pathToFileURL(playwrightModule));
+const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
+  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
+  : await (async () => {
+      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
+      return {executablePath: await binary.executablePath(), args: binary.args};
+    })();
+const output = process.env.AHA_SCREENSHOT_DIR || resolve(tools, "screenshots");
 await mkdir(output, {recursive: true});
 const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
 
@@ -22,7 +30,9 @@ let RESOURCES = [{
   created_at: "2026-10-22T00:00:00Z", updated_at: "2026-10-22T00:00:00Z",
 }];
 window.__createdResource = null;
+window.__updatedResource = null;
 window.__pairedADB = null;
+window.__usbListCalls = 0;
 window.fetch = async (input, init = {}) => {
   const url = new URL(typeof input === "string" ? input : input.url, location.origin);
   const path = url.pathname;
@@ -46,6 +56,22 @@ window.fetch = async (input, init = {}) => {
     window.__pairedADB = {id, pairing_port: body.pairing_port, pairing_code: body.pairing_code};
     return json({message: "Android 设备配对成功", output: "Successfully paired", resource});
   }
+  if (path === "/api/v1/hardware/adb/usb-devices" && method === "POST") {
+    window.__usbListCalls += 1;
+    return json({ok: true, other_count: 1, devices: [
+      {serial: "SERIAL-A", state: "device", model: "Pixel_7", selectable: true},
+      {serial: "SERIAL-B", state: "unauthorized", selectable: true},
+      {serial: "DUP", state: "device", selectable: false},
+    ]});
+  }
+  if (path.startsWith("/api/v1/hardware-resources/") && method === "PUT") {
+    const payload = JSON.parse(init.body || "{}");
+    const id = path.split("/")[4];
+    window.__updatedResource = {id, ...payload};
+    const index = RESOURCES.findIndex(item => item.id === id);
+    RESOURCES[index] = {...RESOURCES[index], ...payload};
+    return json({resource: RESOURCES[index]});
+  }
   if (path === "/api/v1/hardware/serial-ports") return json({ports: [{device: "COM3", description: "USB Serial"}]});
   if (path === "/api/v1/projects") return json({projects: [], has_more: false});
   if (path === "/api/v1/workspaces") return json({workspaces: []});
@@ -81,7 +107,7 @@ const url = `http://127.0.0.1:${server.address().port}`;
 
 let browser;
 try {
-  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
+  browser = await chromium.launch({...launchOptions, headless: true});
   const page = await browser.newPage({viewport: {width: 1280, height: 900}});
   const errors = [];
   page.on("pageerror", error => errors.push(error.message));
@@ -136,6 +162,7 @@ try {
   assert.equal(adbCreated.username, "");
   assert.equal(adbCreated.password, "");
   assert.equal(Object.hasOwn(adbCreated, "pairing_code"), false);
+  assert.equal(adbCreated.network.adb_connection, "wireless");
   await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 3);
   assert.match(await page.locator(".hardware-resource-row").last().innerText(), /ADB/);
   assert.match(await page.locator(".hardware-resource-row").last().innerText(), /192\.0\.2\.61:5555/);
@@ -152,6 +179,94 @@ try {
     pairing_code: "123456",
   });
   await page.waitForFunction(() => document.querySelector(".hardware-resource-row:last-child").textContent.includes("已配对"));
+
+  // ADB USB direct: only the device picker, no host/port/pairing/login.
+  const visible = async (locator) => locator.isVisible();
+  await page.click("#add-hardware-resource");
+  await page.waitForSelector(".hardware-resource-dialog[open]");
+  const usbForm = page.locator("#hardware-resource-form");
+  await usbForm.locator('input[name="description"]').fill("USB board");
+  await usbForm.locator('select[name="mode"]').selectOption("network");
+  await usbForm.locator('select[name="network_protocol"]').selectOption("adb_usb");
+  await page.waitForFunction(() => window.__usbListCalls === 1);
+  await page.waitForFunction(() => document.querySelectorAll('select[name="adb_serial"] option').length === 4);
+  assert.equal(await visible(usbForm.locator('[data-resource-config="network-endpoint"]')), false, "USB showed host/port");
+  assert.equal(await visible(usbForm.locator('[data-resource-config="adb"]')), false, "USB showed wireless pairing help");
+  assert.equal(await visible(usbForm.locator('[data-resource-config="adb-tcp"]')), false, "USB showed network direct help");
+  assert.equal(await visible(usbForm.locator('[data-resource-config="login"]')), false, "USB showed login fields");
+  assert.equal(await visible(usbForm.locator('[data-resource-config="adb-usb"]')), true, "USB picker hidden");
+  // Never preselects a device, even though one is authorized.
+  assert.equal(await usbForm.locator('select[name="adb_serial"]').inputValue(), "");
+  assert.equal(await usbForm.locator('select[name="adb_serial"] option[value="DUP"]').evaluate(option => option.disabled), true);
+  assert.match(await usbForm.locator("[data-adb-usb-status]").innerText(), /3 台 USB 设备.*另有 1 个网络或模拟器设备/s);
+  await page.evaluate(() => { window.__createdResource = null; });
+  await usbForm.locator('button[type="submit"]').click();
+  assert.equal(await page.evaluate(() => window.__createdResource), null, "USB resource saved without a selected device");
+  await usbForm.locator('select[name="adb_serial"]').selectOption("SERIAL-B");
+  await usbForm.locator('button[type="submit"]').click();
+  await page.waitForFunction(() => window.__createdResource?.network?.adb_connection === "usb");
+  const usbCreated = await page.evaluate(() => window.__createdResource);
+  assert.deepEqual(usbCreated.network, {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: "SERIAL-B"});
+  assert.equal(usbCreated.password, "");
+  await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 4);
+  const usbRow = page.locator(".hardware-resource-row", {hasText: "USB board"});
+  assert.match(await usbRow.innerText(), /ADB（USB 直连）/);
+  assert.match(await usbRow.innerText(), /USB SERIAL-B/);
+  assert.doesNotMatch(await usbRow.innerText(), /配对/);
+  assert.equal(await usbRow.locator("[data-hardware-adb-pair]").count(), 0, "USB row offered pairing");
+
+  // ADB network direct: explicit host:port, default 5555, custom port kept, no pairing.
+  await page.click("#add-hardware-resource");
+  await page.waitForSelector(".hardware-resource-dialog[open]");
+  const tcpForm = page.locator("#hardware-resource-form");
+  await tcpForm.locator('input[name="description"]').fill("TV");
+  await tcpForm.locator('select[name="mode"]').selectOption("network");
+  await tcpForm.locator('select[name="network_protocol"]').selectOption("adb_tcp");
+  assert.equal(await tcpForm.locator('input[name="network_port"]').inputValue(), "5555");
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="network-endpoint"]')), true);
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb-tcp"]')), true);
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb"]')), false, "network direct showed pairing help");
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb-usb"]')), false, "network direct showed USB picker");
+  assert.equal(await page.evaluate(() => window.__usbListCalls), 1, "network direct enumerated USB devices");
+  // Switching through USB and back must not leave USB state behind.
+  await tcpForm.locator('select[name="network_protocol"]').selectOption("adb_usb");
+  await tcpForm.locator('select[name="network_protocol"]').selectOption("ssh");
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb-usb"]')), false);
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="login"]')), true);
+  assert.equal(await visible(tcpForm.locator('[data-resource-config="ssh"]')), true);
+  await tcpForm.locator('select[name="network_protocol"]').selectOption("adb_tcp");
+  assert.equal(await tcpForm.locator('input[name="network_port"]').inputValue(), "5555");
+  await tcpForm.locator('input[name="network_host"]').fill("192.0.2.70");
+  await tcpForm.locator('input[name="network_port"]').fill("5601");
+  await tcpForm.locator('button[type="submit"]').click();
+  await page.waitForFunction(() => window.__createdResource?.network?.adb_connection === "tcp");
+  const tcpCreated = await page.evaluate(() => window.__createdResource);
+  assert.deepEqual(tcpCreated.network, {host: "192.0.2.70", port: 5601, protocol: "adb", ssh_auth: "auto", adb_connection: "tcp"});
+  await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 5);
+  const tcpRow = page.locator(".hardware-resource-row", {hasText: "TV"});
+  assert.match(await tcpRow.innerText(), /ADB（网络直连）/);
+  assert.match(await tcpRow.innerText(), /192\.0\.2\.70:5601/);
+  assert.equal(await tcpRow.locator("[data-hardware-adb-pair]").count(), 0, "network direct row offered pairing");
+  assert.equal(await page.locator("[data-hardware-adb-pair]").count(), 1, "only the wireless row may pair");
+
+  // Editing the USB resource keeps its saved serial selected.
+  const listCallsBeforeEdit = await page.evaluate(() => window.__usbListCalls);
+  await usbRow.locator("[data-hardware-resource-edit]").click();
+  await page.waitForSelector(".hardware-resource-dialog[open]");
+  const editForm = page.locator("#hardware-resource-form");
+  assert.equal(await editForm.locator('select[name="network_protocol"]').inputValue(), "adb_usb");
+  await page.waitForFunction(count => window.__usbListCalls === count + 1, listCallsBeforeEdit);
+  assert.equal(await editForm.locator('select[name="adb_serial"]').inputValue(), "SERIAL-B");
+  // Switch the USB resource to wireless: the serial is not sent any more.
+  await editForm.locator('select[name="network_protocol"]').selectOption("adb");
+  assert.equal(await visible(editForm.locator('[data-resource-config="adb-usb"]')), false);
+  await editForm.locator('input[name="network_host"]').fill("192.0.2.71");
+  await editForm.locator('button[type="submit"]').click();
+  await page.waitForFunction(() => window.__updatedResource !== null);
+  const updated = await page.evaluate(() => window.__updatedResource);
+  assert.equal(updated.network.adb_connection, "wireless");
+  assert.equal(Object.hasOwn(updated.network, "adb_serial"), false);
+  assert.equal(updated.network.host, "192.0.2.71");
   assert.deepEqual(errors, []);
   await page.screenshot({path: resolve(output, "hardware-resources.png")});
 
diff --git a/web/tests/task_takeover_adb.browser.mjs b/web/tests/task_takeover_adb.browser.mjs
new file mode 100644
index 0000000..a401c6e
--- /dev/null
+++ b/web/tests/task_takeover_adb.browser.mjs
@@ -0,0 +1,159 @@
+// Optional browser verification; dependencies and browser files stay in .tools.
+// A machine without .tools/web-validation can point AHA_PLAYWRIGHT_MODULE /
+// AHA_BROWSER_EXECUTABLE at a local install.
+//
+// Taking over a remote Task must rebind ADB hardware on its own terms: a USB
+// direct group asks for a local device serial (never a host/port), network
+// direct and wireless keep host:port and carry their connection form, so the
+// server cannot reinterpret one form as another.
+import assert from "node:assert/strict";
+import {createServer} from "node:http";
+import {readFile} from "node:fs/promises";
+import {resolve} from "node:path";
+import {pathToFileURL} from "node:url";
+
+const root = resolve(import.meta.dirname, "../..");
+const tools = resolve(root, ".tools/web-validation");
+const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
+const {chromium} = await import(pathToFileURL(playwrightModule));
+const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
+  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
+  : await (async () => {
+      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
+      return {executablePath: await binary.executablePath(), args: binary.args};
+    })();
+const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
+
+const now = "2026-11-21T00:00:00Z";
+const TASK = {id: "t-remote", code: "task-926", title: "远端 ADB", status: "waiting_user", project_id: "project-1", workspace_id: "workspace-remote", collaboration_mode: "single", max_agents: 1, original_request: "调试设备", current_goal: "调试设备", read_only: true, read_only_reason: "remote_device", owner_device_id: "device-remote", created_at: now, updated_at: now};
+const group = (id, description, network) => ({task_id: TASK.id, id, description, mode: "network", serial: {device: "", baudrate: 115200}, network, password_configured: false, access: "read_only", position: 0, created_at: now, updated_at: now});
+const HARDWARE = [
+  group("usb", "USB 板", {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: "REMOTE-SER"}),
+  group("tv", "电视", {host: "10.0.0.9", port: 5601, protocol: "adb", ssh_auth: "auto", adb_connection: "tcp"}),
+  // Synced from an older peer: no adb_connection, so it is wireless.
+  group("phone", "手机", {host: "10.0.0.10", port: 41235, protocol: "adb", ssh_auth: "auto"}),
+];
+const data = {
+  task: TASK, hardware: HARDWARE,
+  projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false}],
+  workspaces: [{id: "workspace-local", project_id: "project-1", name: "本机", root_path: "/tmp/ws", transport: "native", locality: "local", health: "ready", read_only: false}],
+  models: [{id: "model-1", display_name: "Model One", provider_id: "provider-1", backend: "codex", wire_model: "model-one", source: "provider", default_env_group_id: "env-1", created_at: now, updated_at: now}],
+};
+const bootstrap = `
+const data = ${JSON.stringify(data)};
+const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
+sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: data.task.id, task: data.task, agentID: "main"}));
+window.__takeover = null;
+window.fetch = async (input, init = {}) => {
+  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
+  const path = url.pathname;
+  const method = (init.method || "GET").toUpperCase();
+  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
+  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
+  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
+  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [data.task], has_more: false, counts: {status: {}, project: {}, device: {}}});
+  if (path === "/api/v1/tasks/" + data.task.id + "/takeover" && method === "POST") {
+    window.__takeover = JSON.parse(init.body || "{}");
+    return json({error: "fixture_stop", message: "fixture stops here"}, 400);
+  }
+  if (path === "/api/v1/tasks/" + data.task.id && method === "GET") return json({task: data.task, turns: [], agents: [{task_id: data.task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}], latest_round: null, hardware: data.hardware, memory: {}});
+  if (path === "/api/v1/tasks/" + data.task.id + "/agents") return json({agents: [{task_id: data.task.id, agent_id: "main", role: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}]});
+  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
+  if (path === "/api/v1/projects") return json({projects: data.projects, has_more: false});
+  if (path === "/api/v1/workspaces") return json({workspaces: data.workspaces});
+  if (path === "/api/v1/models") return json({models: data.models});
+  if (path === "/api/v1/env-groups") return json({env_groups: [{id: "env-1", name: "Env", provider_id: "provider-1", backend: "codex"}]});
+  if (path === "/api/v1/providers") return json({providers: [{id: "provider-1", name: "Provider", base_url: "https://example.test/v1", credential_configured: true, revision: 1}]});
+  if (path === "/api/v1/codex-accounts") return json({accounts: []});
+  if (path === "/api/v1/skills") return json({skills: []});
+  if (path === "/api/v1/mcp-servers") return json({servers: []});
+  if (path === "/api/v1/channels") return json({channels: []});
+  if (path.startsWith("/api/v1/")) return json({});
+  return json({});
+};
+window.EventSource = class { constructor() {} close() {} addEventListener() {} };
+`;
+const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
+const server = createServer(async (request, response) => {
+  const name = new URL(request.url, "http://localhost").pathname;
+  try {
+    response.setHeader("Content-Security-Policy", csp);
+    if (name === "/" || name === "/index.html") {
+      response.setHeader("Content-Type", "text/html");
+      response.end(html);
+    } else if (name === "/fixture.js") {
+      response.setHeader("Content-Type", "text/javascript");
+      response.end(bootstrap);
+    } else {
+      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
+      const file = name.startsWith("/vendor/") ? resolve(root, "web", name.slice(1)) : resolve(root, "web/build", name.slice(1));
+      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
+      response.end(await readFile(file));
+    }
+  } catch { response.writeHead(404).end(); }
+});
+await new Promise(done => server.listen(0, "127.0.0.1", done));
+const url = `http://127.0.0.1:${server.address().port}`;
+
+let browser;
+try {
+  browser = await chromium.launch({...launchOptions, headless: true});
+  const page = await browser.newPage({viewport: {width: 1280, height: 1000}});
+  const errors = [];
+  page.on("pageerror", error => errors.push(error.message));
+  await page.goto(url, {waitUntil: "networkidle"});
+  await page.waitForSelector("#takeover-task", {timeout: 15000});
+  await page.click("#takeover-task");
+  await page.waitForSelector("#task-takeover-dialog[open]");
+  const fieldset = id => page.locator(`.takeover-hardware[data-hardware-id="${id}"]`);
+
+  // USB direct: a required local serial, no host/port inputs.
+  const usb = fieldset("usb");
+  assert.equal(await usb.locator('[data-field="network-adb-serial"]').count(), 1);
+  assert.equal(await usb.locator('[data-field="network-adb-serial"]').evaluate(input => input.required), true);
+  assert.equal(await usb.locator('[data-field="network-adb-serial"]').getAttribute("placeholder"), "REMOTE-SER");
+  assert.equal(await usb.locator('[data-field="network-host"]').count(), 0, "USB group asked for a host");
+  assert.equal(await usb.locator('[data-field="network-port"]').count(), 0, "USB group asked for a port");
+  // Network direct and wireless keep host:port and carry their form.
+  for (const [id, form] of [["tv", "tcp"], ["phone", "wireless"]]) {
+    assert.equal(await fieldset(id).locator('[data-field="network-adb-connection"]').inputValue(), form, `${id} connection form`);
+    assert.equal(await fieldset(id).locator('[data-field="network-host"]').evaluate(input => input.required), true);
+    assert.equal(await fieldset(id).locator('[data-field="network-adb-serial"]').count(), 0, `${id} asked for a USB serial`);
+  }
+  assert.equal(await fieldset("tv").locator('[data-field="network-port"]').inputValue(), "5601");
+
+  // The runtime catalog (backend/model) is outside this test; give the two
+  // required runtime selects a value so only hardware fields decide validity.
+  await page.evaluate(() => {
+    for (const name of ["backend", "model_id"]) {
+      const select = document.querySelector(`#task-takeover-form select[name="${name}"]`);
+      if (select && !select.value) {
+        select.add(new Option("fixture", "fixture-" + name));
+        select.value = "fixture-" + name;
+      }
+    }
+  });
+  // Submitting without the local serial is blocked by the form itself.
+  await fieldset("tv").locator('[data-field="network-host"]').fill("10.0.0.19");
+  await fieldset("phone").locator('[data-field="network-host"]').fill("10.0.0.20");
+  await page.locator('#task-takeover-form button[type="submit"]').click();
+  assert.equal(await page.evaluate(() => window.__takeover), null, "takeover submitted without a local USB serial");
+
+  await usb.locator('[data-field="network-adb-serial"]').fill("LOCAL-SER");
+  await page.locator('#task-takeover-form button[type="submit"]').click();
+  await page.waitForFunction(() => window.__takeover !== null, null, {timeout: 10000});
+  const sent = await page.evaluate(() => window.__takeover);
+  const byID = Object.fromEntries(sent.groups.map(item => [item.id, item.network]));
+  assert.equal(byID.usb.protocol, "adb");
+  assert.equal(byID.usb.adb_connection, "usb");
+  assert.equal(byID.usb.adb_serial, "LOCAL-SER");
+  assert.equal(byID.usb.host, "");
+  assert.equal(byID.usb.port, 0);
+  assert.deepEqual([byID.tv.adb_connection, byID.tv.host, byID.tv.port, byID.tv.adb_serial], ["tcp", "10.0.0.19", 5601, ""]);
+  assert.deepEqual([byID.phone.adb_connection, byID.phone.host, byID.phone.port, byID.phone.adb_serial], ["wireless", "10.0.0.20", 41235, ""]);
+  assert.deepEqual(errors, []);
+  console.log("task takeover ADB rebinding verified");
+} finally {
+  await browser?.close();
+  server.close();
+}
~~~~
