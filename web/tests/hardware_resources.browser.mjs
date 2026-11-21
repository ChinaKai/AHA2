import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
// A machine without .tools/web-validation can point AHA_PLAYWRIGHT_MODULE /
// AHA_BROWSER_EXECUTABLE (and AHA_SCREENSHOT_DIR) at a local install.
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const output = process.env.AHA_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
let RESOURCES = [{
  id: "resource-a", description: "Main board", mode: "serial",
  serial: {device: "COM3", baudrate: 115200},
  network: {host: "", port: 23, protocol: "telnet", ssh_auth: "auto"},
  password_configured: false, access: "read_write", binding_count: 2,
  created_at: "2026-10-22T00:00:00Z", updated_at: "2026-10-22T00:00:00Z",
}];
window.__createdResource = null;
window.__updatedResource = null;
window.__pairedADB = null;
window.__usbListCalls = 0;
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/hardware-resources" && method === "GET") return json({resources: RESOURCES});
  if (path === "/api/v1/hardware-resources" && method === "POST") {
    const payload = JSON.parse(init.body || "{}");
    window.__createdResource = payload;
    const resource = {...payload, id: "resource-new-" + (RESOURCES.length + 1), password_configured: Boolean(payload.password), binding_count: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString()};
    RESOURCES.push(resource);
    return json({resource}, 201);
  }
  if (path.startsWith("/api/v1/hardware-resources/") && path.endsWith("/adb/pair") && method === "POST") {
    const body = JSON.parse(init.body || "{}");
    const id = path.split("/")[4];
    const resource = RESOURCES.find(item => item.id === id);
    resource.adb_paired_at = new Date().toISOString();
    window.__pairedADB = {id, pairing_port: body.pairing_port, pairing_code: body.pairing_code};
    return json({message: "Android 设备配对成功", output: "Successfully paired", resource});
  }
  if (path === "/api/v1/hardware/adb/usb-devices" && method === "POST") {
    window.__usbListCalls += 1;
    return json({ok: true, other_count: 1, devices: [
      {serial: "SERIAL-A", state: "device", model: "Pixel_7", selectable: true},
      {serial: "SERIAL-B", state: "unauthorized", selectable: true},
      {serial: "DUP", state: "device", selectable: false},
    ]});
  }
  if (path.startsWith("/api/v1/hardware-resources/") && method === "PUT") {
    const payload = JSON.parse(init.body || "{}");
    const id = path.split("/")[4];
    window.__updatedResource = {id, ...payload};
    const index = RESOURCES.findIndex(item => item.id === id);
    RESOURCES[index] = {...RESOURCES[index], ...payload};
    return json({resource: RESOURCES[index]});
  }
  if (path === "/api/v1/hardware/serial-ports") return json({ports: [{device: "COM3", description: "USB Serial"}]});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "hardware"}));
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
      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      const file = name.startsWith("/vendor/") ? resolve(root, "web", name.slice(1)) : resolve(root, "web/build", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector(".hardware-resources-view", {timeout: 15000}).catch(async () => {
    console.log("page errors:", JSON.stringify(errors));
    console.log("app text:", (await page.locator("#app").innerText()).slice(0, 500));
    throw new Error("hardware resources view did not render");
  });
  assert.match(await page.locator(".hardware-resource-row").first().innerText(), /Main board/);
  assert.match(await page.locator(".hardware-resource-row").first().innerText(), /2 个任务启用/);

  await page.click("#add-hardware-resource");
  await page.waitForSelector(".hardware-resource-dialog[open]");
  const form = page.locator("#hardware-resource-form");
  await form.locator('input[name="description"]').fill("Network lab board");
  await form.locator('select[name="mode"]').selectOption("network");
  const sshAuth = form.locator('[data-resource-config="ssh"]');
  assert.equal(await sshAuth.isHidden(), true, "SSH auth remained visible for Telnet");
  await form.locator('input[name="network_host"]').fill("192.0.2.60");
  await form.locator('input[name="network_port"]').fill("22");
  await form.locator('select[name="network_protocol"]').selectOption("ssh");
  assert.equal(await sshAuth.isVisible(), true, "SSH auth was hidden for SSH");
  await form.locator('input[name="username"]').fill("root");
  await form.locator('input[name="password"]').fill("secret");
  await form.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__createdResource !== null);
  const created = await page.evaluate(() => window.__createdResource);
  assert.equal(created.mode, "network");
  assert.equal(created.network.host, "192.0.2.60");
  assert.equal(created.network.protocol, "ssh");
  assert.equal(created.password, "secret");
  await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 2);

  await page.click("#add-hardware-resource");
  await page.waitForSelector(".hardware-resource-dialog[open]");
  const adbForm = page.locator("#hardware-resource-form");
  await adbForm.locator('input[name="description"]').fill("Android phone");
  await adbForm.locator('select[name="mode"]').selectOption("network");
  await adbForm.locator('select[name="network_protocol"]').selectOption("adb");
  assert.equal(await adbForm.locator('[data-resource-config="ssh"]').isHidden(), true);
  assert.equal(await adbForm.locator('input[name="network_port"]').inputValue(), "5555");
  assert.equal(await adbForm.locator('input[name="adb_pair_port"]').count(), 0);
  assert.equal(await adbForm.locator('[data-resource-config="login"]').isHidden(), true);
  await adbForm.locator('input[name="network_host"]').fill("192.0.2.61");
  await adbForm.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__createdResource?.network?.protocol === "adb");
  const adbCreated = await page.evaluate(() => window.__createdResource);
  assert.equal(adbCreated.network.host, "192.0.2.61");
  assert.equal(adbCreated.network.port, 5555);
  assert.equal(Object.hasOwn(adbCreated.network, "adb_pair_port"), false);
  assert.equal(adbCreated.username, "");
  assert.equal(adbCreated.password, "");
  assert.equal(Object.hasOwn(adbCreated, "pairing_code"), false);
  assert.equal(adbCreated.network.adb_connection, "wireless");
  await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 3);
  assert.match(await page.locator(".hardware-resource-row").last().innerText(), /ADB/);
  assert.match(await page.locator(".hardware-resource-row").last().innerText(), /192\.0\.2\.61:5555/);
  assert.match(await page.locator(".hardware-resource-row").last().innerText(), /未配对/);
  await page.click("[data-hardware-adb-pair]");
  await page.waitForSelector("#adb-pair-form");
  await page.locator('#adb-pair-form input[name="pairing_port"]').fill("37001");
  await page.locator('#adb-pair-form input[name="pairing_code"]').fill("123456");
  await page.locator('#adb-pair-form button[type="submit"]').click();
  await page.waitForFunction(() => window.__pairedADB !== null);
  assert.deepEqual(await page.evaluate(() => window.__pairedADB), {
    id: "resource-new-3",
    pairing_port: 37001,
    pairing_code: "123456",
  });
  await page.waitForFunction(() => document.querySelector(".hardware-resource-row:last-child").textContent.includes("已配对"));

  // ADB USB direct: only the device picker, no host/port/pairing/login.
  const visible = async (locator) => locator.isVisible();
  await page.click("#add-hardware-resource");
  await page.waitForSelector(".hardware-resource-dialog[open]");
  const usbForm = page.locator("#hardware-resource-form");
  await usbForm.locator('input[name="description"]').fill("USB board");
  await usbForm.locator('select[name="mode"]').selectOption("network");
  await usbForm.locator('select[name="network_protocol"]').selectOption("adb_usb");
  await page.waitForFunction(() => window.__usbListCalls === 1);
  await page.waitForFunction(() => document.querySelectorAll('select[name="adb_serial"] option').length === 4);
  assert.equal(await visible(usbForm.locator('[data-resource-config="network-endpoint"]')), false, "USB showed host/port");
  assert.equal(await visible(usbForm.locator('[data-resource-config="adb"]')), false, "USB showed wireless pairing help");
  assert.equal(await visible(usbForm.locator('[data-resource-config="adb-tcp"]')), false, "USB showed network direct help");
  assert.equal(await visible(usbForm.locator('[data-resource-config="login"]')), false, "USB showed login fields");
  assert.equal(await visible(usbForm.locator('[data-resource-config="adb-usb"]')), true, "USB picker hidden");
  // Never preselects a device, even though one is authorized.
  assert.equal(await usbForm.locator('select[name="adb_serial"]').inputValue(), "");
  assert.equal(await usbForm.locator('select[name="adb_serial"] option[value="DUP"]').evaluate(option => option.disabled), true);
  assert.match(await usbForm.locator("[data-adb-usb-status]").innerText(), /3 台 USB 设备.*另有 1 个网络或模拟器设备/s);
  await page.evaluate(() => { window.__createdResource = null; });
  await usbForm.locator('button[type="submit"]').click();
  assert.equal(await page.evaluate(() => window.__createdResource), null, "USB resource saved without a selected device");
  await usbForm.locator('select[name="adb_serial"]').selectOption("SERIAL-B");
  await usbForm.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__createdResource?.network?.adb_connection === "usb");
  const usbCreated = await page.evaluate(() => window.__createdResource);
  assert.deepEqual(usbCreated.network, {host: "", port: 0, protocol: "adb", ssh_auth: "auto", adb_connection: "usb", adb_serial: "SERIAL-B"});
  assert.equal(usbCreated.password, "");
  await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 4);
  const usbRow = page.locator(".hardware-resource-row", {hasText: "USB board"});
  assert.match(await usbRow.innerText(), /ADB（USB 直连）/);
  assert.match(await usbRow.innerText(), /USB SERIAL-B/);
  assert.doesNotMatch(await usbRow.innerText(), /配对/);
  assert.equal(await usbRow.locator("[data-hardware-adb-pair]").count(), 0, "USB row offered pairing");

  // ADB network direct: explicit host:port, default 5555, custom port kept, no pairing.
  await page.click("#add-hardware-resource");
  await page.waitForSelector(".hardware-resource-dialog[open]");
  const tcpForm = page.locator("#hardware-resource-form");
  await tcpForm.locator('input[name="description"]').fill("TV");
  await tcpForm.locator('select[name="mode"]').selectOption("network");
  await tcpForm.locator('select[name="network_protocol"]').selectOption("adb_tcp");
  assert.equal(await tcpForm.locator('input[name="network_port"]').inputValue(), "5555");
  assert.equal(await visible(tcpForm.locator('[data-resource-config="network-endpoint"]')), true);
  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb-tcp"]')), true);
  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb"]')), false, "network direct showed pairing help");
  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb-usb"]')), false, "network direct showed USB picker");
  assert.equal(await page.evaluate(() => window.__usbListCalls), 1, "network direct enumerated USB devices");
  // Switching through USB and back must not leave USB state behind.
  await tcpForm.locator('select[name="network_protocol"]').selectOption("adb_usb");
  await tcpForm.locator('select[name="network_protocol"]').selectOption("ssh");
  assert.equal(await visible(tcpForm.locator('[data-resource-config="adb-usb"]')), false);
  assert.equal(await visible(tcpForm.locator('[data-resource-config="login"]')), true);
  assert.equal(await visible(tcpForm.locator('[data-resource-config="ssh"]')), true);
  await tcpForm.locator('select[name="network_protocol"]').selectOption("adb_tcp");
  assert.equal(await tcpForm.locator('input[name="network_port"]').inputValue(), "5555");
  await tcpForm.locator('input[name="network_host"]').fill("192.0.2.70");
  await tcpForm.locator('input[name="network_port"]').fill("5601");
  await tcpForm.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__createdResource?.network?.adb_connection === "tcp");
  const tcpCreated = await page.evaluate(() => window.__createdResource);
  assert.deepEqual(tcpCreated.network, {host: "192.0.2.70", port: 5601, protocol: "adb", ssh_auth: "auto", adb_connection: "tcp"});
  await page.waitForFunction(() => document.querySelectorAll(".hardware-resource-row").length === 5);
  const tcpRow = page.locator(".hardware-resource-row", {hasText: "TV"});
  assert.match(await tcpRow.innerText(), /ADB（网络直连）/);
  assert.match(await tcpRow.innerText(), /192\.0\.2\.70:5601/);
  assert.equal(await tcpRow.locator("[data-hardware-adb-pair]").count(), 0, "network direct row offered pairing");
  assert.equal(await page.locator("[data-hardware-adb-pair]").count(), 1, "only the wireless row may pair");

  // Editing the USB resource keeps its saved serial selected.
  const listCallsBeforeEdit = await page.evaluate(() => window.__usbListCalls);
  await usbRow.locator("[data-hardware-resource-edit]").click();
  await page.waitForSelector(".hardware-resource-dialog[open]");
  const editForm = page.locator("#hardware-resource-form");
  assert.equal(await editForm.locator('select[name="network_protocol"]').inputValue(), "adb_usb");
  await page.waitForFunction(count => window.__usbListCalls === count + 1, listCallsBeforeEdit);
  assert.equal(await editForm.locator('select[name="adb_serial"]').inputValue(), "SERIAL-B");
  // Switch the USB resource to wireless: the serial is not sent any more.
  await editForm.locator('select[name="network_protocol"]').selectOption("adb");
  assert.equal(await visible(editForm.locator('[data-resource-config="adb-usb"]')), false);
  await editForm.locator('input[name="network_host"]').fill("192.0.2.71");
  await editForm.locator('button[type="submit"]').click();
  await page.waitForFunction(() => window.__updatedResource !== null);
  const updated = await page.evaluate(() => window.__updatedResource);
  assert.equal(updated.network.adb_connection, "wireless");
  assert.equal(Object.hasOwn(updated.network, "adb_serial"), false);
  assert.equal(updated.network.host, "192.0.2.71");
  assert.deepEqual(errors, []);
  await page.screenshot({path: resolve(output, "hardware-resources.png")});

  await page.setViewportSize({width: 390, height: 844});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
  await page.screenshot({path: resolve(output, "hardware-resources-mobile.png")});
  console.log("hardware resources page verified");
} finally {
  await browser?.close();
  server.close();
}
