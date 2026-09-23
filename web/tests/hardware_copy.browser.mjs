// Optional browser verification; dependencies and browser files stay in .tools.
// Drives the real built Web bundle, with the real vendored xterm, to confirm the
// hardware console copy button works against an actual terminal buffer rather
// than a stub that would agree with whatever the code does.
//
// The terminal instance is not reachable from the page, and reaching into xterm
// internals would test those instead of our code, so the data arrives the way it
// does in production: through the hardware stream the panel replays on mount.
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

const TASK = {id: "t-hw", code: "task-900", title: "Hardware console", status: "active", project_id: "project-1", workspace_id: "workspace-1"};
const HARDWARE = [{
  id: "hw-1", task_id: "t-hw", position: 1, description: "Debug board", mode: "serial",
  serial: {device: "/dev/ttyUSB0", baudrate: 115200},
  network: {host: "", port: 0, protocol: "tcp", ssh_auth: "auto"},
  username: "", password_configured: false, access: "read_write",
  created_at: "2026-09-23T00:00:00Z", updated_at: "2026-09-23T00:00:00Z",
}];

// Console output replayed into the terminal before the copy runs. The middle line
// ends in real spaces, which must survive; xterm's own padding out to the column
// width must not.
const CONSOLE_TEXT = "AT+GMR\r\n  busy   \r\nOK\r\n";
const EXPECTED = "AT+GMR\n  busy   \nOK";
const STATUS = {connected: true, read_only: false, status: "connected", endpoint: "/dev/ttyUSB0", error: ""};

const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/styles.css"></head>
<body><div id="app"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const HARDWARE = ${JSON.stringify(HARDWARE)};
const STATUS = ${JSON.stringify(STATUS)};
const CONSOLE_TEXT = ${JSON.stringify(CONSOLE_TEXT)};
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: HARDWARE, memory: {}, skills: [], knowledge: {},
});
window.__noticeText = "";
window.fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input.url;
  const path = new URL(url, location.href).pathname;
  const method = (init?.method || "GET").toUpperCase();
  if (path === "/api/v1/tasks/t-hw" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-hw/hardware/serial-ports") return json({ports: []});
  if (path.endsWith("/terminal")) {
    // The panel polls for status and replayed output; this is how real console
    // bytes reach the xterm buffer. Shape matches api.hardwareTerminal:
    // {status, stream: {items, latest_sequence}}. Honour the after parameter the
    // way the real endpoint does, so a replayed event is delivered exactly once
    // rather than on every poll.
    const after = Number(new URL(url, location.href).searchParams.get("after") || 0);
    const first = after < 1;
    return json({
      status: STATUS,
      stream: {
        items: first ? [{sequence: 1, direction: "rx", data: CONSOLE_TEXT}] : [],
        latest_sequence: 1,
      },
    });
  }
  if (path === "/api/v1/system") return json({ok: true, web_version: "v0.0.0.test"});
  return json({ok: true});
};
window.WebSocket = class {
  constructor() { this.readyState = 0; }
  addEventListener() {} removeEventListener() {} send() {} close() {}
};
`;

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
      // The panel pulls xterm from /vendor, so that path has to be servable too.
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
  const context = await browser.newContext({viewport: {width: 1280, height: 900}, permissions: ["clipboard-read", "clipboard-write"]});
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});

  const bound = await page.evaluate(async () => {
    // The console pulls xterm on demand, so wait for the same asset the panel
    // waits for rather than assuming the app preloaded it.
    if (!window.Terminal) {
      await new Promise((resolve, reject) => {
        const script = document.createElement("script");
        script.src = "/vendor/xterm.js";
        script.onload = resolve;
        script.onerror = () => reject(new Error("xterm failed to load"));
        document.head.appendChild(script);
      });
    }
    const mod = await import("/hardware_panel.js");
    const detail = await (await fetch("/api/v1/tasks/t-hw")).json();
    // Render the panel the way the app does, then bind it: bindHardwarePanel
    // only attaches to an existing root, it does not create one.
    document.querySelector("#app").innerHTML = mod.renderHardwarePanel(detail);
    mod.bindHardwarePanel(detail, (kind, message) => { window.__noticeText = kind + ":" + message; });
    return document.querySelector("#hardware-copy-output") !== null;
  });
  assert.equal(bound, true, "hardware panel did not render its copy button");

  // The panel polls quickly; wait for the replayed console text to land.
  await page.waitForFunction(() => {
    const rows = document.querySelector(".xterm-rows");
    return Boolean(rows && rows.textContent && rows.textContent.includes("AT+GMR"));
  }, null, {timeout: 15000});
  assert.deepEqual(errors, [], "the hardware panel threw while rendering");

  const copied = await page.evaluate(async () => {
    document.querySelector("#hardware-copy-output").click();
    await new Promise(resolve => setTimeout(resolve, 200));
    return navigator.clipboard.readText();
  });

  assert.equal(copied, EXPECTED, `clipboard did not match the console: ${JSON.stringify(copied)}`);
  assert.ok(!copied.includes("AT+GMR "), "xterm width padding leaked into the clipboard");
  assert.ok(copied.includes("  busy   "), "real spacing was stripped from the clipboard");
  assert.match(await page.evaluate(() => window.__noticeText), /^notice:已复制终端内容$/, "copy did not report success");

  await page.screenshot({path: resolve(output, "hardware-copy.png")});
  console.log("hardware copy clipboard:", JSON.stringify(copied));
} finally {
  if (browser) await browser.close();
  server.close();
}
