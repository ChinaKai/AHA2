// Simulated WAN/API only. No requests reach a native desktop provider.
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
const desktopSource = await readFile(resolve(root, "web/dist/desktop_panel.js"), "utf8");
const apiImport = desktopSource.match(/from "(\.\/api\.js[^"]*)"/)[1].replace("./", "/");
const toolsSource = await readFile(resolve(root, "web/dist/task_tools.js"), "utf8");
const desktopImport = toolsSource.match(/from "(\.\/desktop_panel\.js[^"]*)"/)[1].replace("./", "/");
const serverSource = await readFile(resolve(root, "internal/httpapi/server.go"), "utf8");
const csp = serverSource.match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body class="task-view-active"><main id="fixture"></main><script type="module" src="/fixture.js"></script></body></html>`;
const bootstrap = `
import * as desktop from "${desktopImport}";
import {renderTaskToolPanel} from "/task_tools.js";
import {api} from "${apiImport}";
api.setCSRF("mock-stream-csrf");
window.desktop = desktop;
window.mount = (id = "stream-task") => {
  desktop.stopDesktopPanel();
  window.detail = {task: {id}};
  document.querySelector("#fixture").innerHTML = '<div class="task-grid task-tool-open task-tool-fullscreen" style="position:relative;height:100vh;display:block">' + renderTaskToolPanel("browser", detail, "main", "", "fullscreen") + '</div>';
  desktop.bindDesktopPanel(detail, () => {});
};
window.blobCounts = {created: 0, revoked: 0};
const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL);
URL.createObjectURL = blob => { blobCounts.created++; return create(blob); };
URL.revokeObjectURL = url => { blobCounts.revoked++; revoke(url); };
window.mount();
`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/") {
      response.setHeader("Content-Type", "text/html; charset=utf-8");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (/^\/[a-z_]+\.(js|css)$/.test(name)) {
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/dist", name.slice(1))));
    } else response.writeHead(404).end();
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const waitUntil = async (predicate, label, timeout = 15000) => {
  const until = Date.now() + timeout;
  while (!predicate()) {
    assert.ok(Date.now() < until, `timeout: ${label}`);
    await delay(30);
  }
};
let browser;
const timers = new Set();
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}});
  const openPopover = async name => {
    if (!await page.locator(`#desktop-${name}-popover`).isVisible()) await page.click(`[data-desktop-popover="${name}"]`);
  };
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => {
    if (message.type() === "error" && /Content Security Policy|violates.*directive/i.test(message.text())) errors.push(message.text());
  });
  const target = {id: "desktop:mock", title: "Shared desktop WAN fixture", process: "Windows", kind: "desktop"};
  let session = {id: "stream-session", task_id: "stream-task", window: target, controller: "shared", revision: 1,
    expires_at: new Date(Date.now() + 600000).toISOString(), claimed: false, mode: "foreground"};
  const status = () => ({supported: true, foreground_supported: true, shared_control_supported: true, stream_supported: true, session});
  const snapshots = new Map();
  const actions = [];
  const httpQueries = [];
  const streams = [];
  const preferences = [];
  const acknowledged = [];
  let delayMS = 680;
  let bytesPerSecond = 40000;
  let sequence = 0;
  let rejectSockets = false;
  let windowReads = 0;
  let logicalSize = {width: 2560, height: 1440};
  let controlError = "";
  let captureError = "";
  let observationFailure = "";
  const imageCache = new Map();
  const image = async options => {
    const ratio = logicalSize.width / logicalSize.height;
    const width = Math.min(options.max_width, Math.floor(options.max_height * ratio));
    const height = Math.floor(width / ratio);
    const key = `${width}:${height}:${options.quality}`;
    if (!imageCache.has(key)) imageCache.set(key, await page.evaluate(({width, height, quality}) => {
      const canvas = document.createElement("canvas");
      canvas.width = width; canvas.height = height;
      const context = canvas.getContext("2d");
      context.fillStyle = "#f3f6f8"; context.fillRect(0, 0, width, height);
      context.fillStyle = "#263e37"; context.fillRect(0, 0, width, height * .12);
      context.fillStyle = "white"; context.font = `${Math.max(10, width / 35)}px sans-serif`;
      context.fillText("Shared desktop", width * .03, height * .08);
      context.fillStyle = "#dfede6"; context.fillRect(width * .07, height * .22, width * .56, height * .48);
      context.fillStyle = "#275e49"; context.font = `${Math.max(10, width / 30)}px sans-serif`;
      context.fillText("Network preview fixture", width * .10, height * .35);
      context.fillStyle = "#9ebac4"; context.fillRect(width * .69, height * .22, width * .24, height * .48);
      return canvas.toDataURL("image/jpeg", quality / 100).split(",")[1];
    }, {width, height, quality: options.quality}));
    return {width, height, bytes: Buffer.from(imageCache.get(key), "base64")};
  };
  const observation = (viewID, preview) => {
    const value = {id: `frame-${++sequence}`, session_id: session.id, revision: session.revision, window: target,
      width: logicalSize.width, height: logicalSize.height, preview_width: preview.width, preview_height: preview.height, mime: "image/jpeg",
      image: "", elements: [], input_actions: ["click", "double_click", "drag", "scroll", "text", "key", "focus"],
      surface: "logical-2560-1440", ...(controlError ? {control_error: controlError} : {}),
      ...(["desktop_foreground_unavailable", "desktop_foreground_changed"].includes(controlError) ? {input_actions: []} : {}),
      ...(captureError ? {capture_error: captureError, input_actions: ["focus"]} : {})};
    snapshots.set(value.id, {viewID, observation: value});
    return value;
  };
  await page.route("**/api/v1/tasks/*/desktop**", async route => {
    const request = route.request();
    const endpoint = new URL(request.url());
    const payload = request.postDataJSON();
    assert.ok(!endpoint.pathname.endsWith("/control"), "shared preview must not transfer control");
    if (endpoint.pathname.endsWith("/windows")) {
      windowReads++;
      await route.fulfill({json: {windows: [target]}});
    } else if (endpoint.pathname.endsWith("/observation")) {
      const query = Object.fromEntries(endpoint.searchParams);
      httpQueries.push(query);
      if (observationFailure) {
        await route.fulfill({status: 409, json: {error: observationFailure}});
        return;
      }
      const preview = await image({max_width: +query.max_width, max_height: +query.max_height, quality: +query.quality});
      const frame = observation(query.view_id, preview);
      frame.image = preview.bytes.toString("base64");
      await route.fulfill({json: {observation: frame}});
    } else if (endpoint.pathname.endsWith("/actions")) {
      const referenced = snapshots.get(payload.observation_id);
      assert.ok(referenced, "action refers to retained frame");
      assert.equal(payload.view_id, referenced.viewID);
      assert.match(payload.action_id, /^[a-f0-9]{32}$/);
      assert.ok(!actions.some(item => item.action_id === payload.action_id));
      actions.push({...payload, receivedAt: Date.now()});
      await route.fulfill({json: {status: status()}});
    } else if (endpoint.pathname.endsWith("/stop")) {
      session = null;
      await route.fulfill({json: {status: status()}});
    } else await route.fulfill({json: {status: status()}});
  });
  await page.routeWebSocket("**/desktop/stream?**", socket => {
    const stream = {socket, closed: false, options: null, viewID: "", unacked: new Set(), timer: null};
    streams.push(stream);
    const close = () => { stream.closed = true; if (stream.timer) { clearTimeout(stream.timer); timers.delete(stream.timer); } };
    socket.onClose(close);
    socket.onMessage(async raw => {
      if (stream.closed) return;
      const message = JSON.parse(String(raw));
      if (message.type === "start") {
        assert.equal(message.csrf_token, "mock-stream-csrf");
        assert.match(message.view_id, /^[a-f0-9]{32}$/);
        assert.doesNotMatch(socket.url(), /csrf/);
        stream.viewID = message.view_id;
        if (rejectSockets) { close(); socket.close(); return; }
        socket.send(JSON.stringify({type: "ready", protocol: 1, status: status()}));
      } else {
        assert.equal(message.type, "ack");
        assert.ok(stream.unacked.delete(message.frame_id), "ACK references a sent, unacknowledged frame");
        acknowledged.push(message.frame_id);
      }
      const {max_width, max_height, quality, interval_ms} = message;
      stream.options = {max_width, max_height, quality, interval_ms};
      preferences.push(stream.options);
      if (stream.timer || stream.closed) return;
      const timer = setTimeout(async () => {
        timers.delete(timer);
        stream.timer = null;
        if (stream.closed || !session) return;
        const preview = await image(stream.options);
        if (stream.closed || !session) return;
        const frame = observation(stream.viewID, preview);
        const bytes = captureError ? Buffer.alloc(0) : preview.bytes;
        const json = Buffer.from(JSON.stringify({type: "frame", observation: frame, capture_ms: 35, interval_ms: stream.options.interval_ms}));
        const packet = Buffer.alloc(4 + json.length + bytes.length);
        packet.writeUInt32BE(json.length);
        json.copy(packet, 4);
        bytes.copy(packet, 4 + json.length);
        const transfer = setTimeout(() => {
          timers.delete(transfer);
          stream.timer = null;
          if (stream.closed || !session) return;
          stream.unacked.add(frame.id);
          assert.ok(stream.unacked.size <= 2);
          socket.send(packet);
        }, Math.ceil(packet.length * 1000 / bytesPerSecond));
        stream.timer = transfer;
        timers.add(transfer);
      }, Math.max(delayMS, interval_ms));
      stream.timer = timer;
      timers.add(timer);
    });
  });
  await page.goto(url);
  await page.waitForFunction(() => document.querySelector("#desktop-image").src.startsWith("blob:"));
  await waitUntil(() => preferences.some(item => item.quality <= 55), "weak network downgrade");
  assert.ok(preferences.some(item => item.max_width <= 960));
  assert.equal(httpQueries.length, 0, "healthy stream avoids HTTP screenshots");
  const latestStream = () => streams.at(-1);
  const connectionsBeforeClaim = streams.length;
  await page.locator("#desktop-text-input").fill("Owner assistance draft");
  for (const claimed of [true, false, true]) {
    session.claimed = claimed;
    latestStream().socket.send(JSON.stringify({type: "status", status: status()}));
    await delay(50);
    assert.equal(await page.locator("#desktop-focus").isEnabled(), true);
    assert.equal(await page.locator("#desktop-text-input").isEditable(), true);
    assert.equal(await page.locator("#desktop-text-input").inputValue(), "Owner assistance draft");
    assert.equal(streams.length, connectionsBeforeClaim);
    assert.equal(session.revision, 1);
  }
  // A frame already published but not yet delivered must not stop input on the displayed frame.
  const surface = page.locator("#desktop-image");
  await surface.focus();
  delayMS = 1800;
  const beforeAction = Date.now();
  await surface.press("ArrowLeft");
  await waitUntil(() => actions.length > 0, "independent HTTP input", 1000);
  assert.ok(actions[0].receivedAt - beforeAction < 1000, "input not queued behind delayed video");
  await page.waitForFunction(() => !document.querySelector("#desktop-focus").disabled);
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const bounds = await surface.boundingBox();
  await page.mouse.move(bounds.x + bounds.width * .25, bounds.y + bounds.height * .25);
  await page.mouse.down();
  const frameBeforeGesture = await surface.getAttribute("src");
  const ackBefore = acknowledged.length;
  await waitUntil(() => acknowledged.length > ackBefore, "ACK discarded frozen-gesture frames");
  assert.equal(await surface.getAttribute("src"), frameBeforeGesture);
  await page.mouse.move(bounds.x + bounds.width * .5, bounds.y + bounds.height * .5);
  await page.mouse.up();
  await waitUntil(() => actions.some(action => action.kind === "drag"), "logical drag");
  const drag = actions.find(action => action.kind === "drag");
  assert.ok(Math.abs(drag.x - 640) < 2 && Math.abs(drag.y - 360) < 2);
  assert.ok(Math.abs(drag.end_x - 1280) < 2 && Math.abs(drag.end_y - 720) < 2);
  const profileBeforeRecovery = preferences.at(-1).quality;
  delayMS = 70;
  bytesPerSecond = 2000000;
  await waitUntil(() => preferences.at(-1).quality > profileBeforeRecovery, "fast-network hysteresis recovery", 18000);
  const assertFit = async () => {
    const geometry = await page.evaluate(() => {
      const image = document.querySelector("#desktop-image").getBoundingClientRect();
      const view = document.querySelector("#desktop-viewport").getBoundingClientRect();
      const stop = document.querySelector("#desktop-stop").getBoundingClientRect();
      const body = document.querySelector("#task-tool-panel-body");
      return {fits: image.width > 0 && image.height > 0 && image.left >= view.left - 1 && image.top >= view.top - 1
        && image.right <= view.right + 1 && image.bottom <= view.bottom + 1,
        stopVisible: stop.top >= 0 && stop.bottom <= innerHeight,
        bodyScroll: !document.fullscreenElement && body.scrollHeight > body.clientHeight + 1};
    });
    assert.deepEqual(geometry, {fits: true, stopVisible: true, bodyScroll: false});
  };
  for (const viewport of [{width: 1440, height: 900}, {width: 390, height: 844}, {width: 320, height: 740}, {width: 844, height: 390}]) {
    await page.setViewportSize(viewport);
    for (const logical of [{width: 2560, height: 1440}, {width: 900, height: 1600}, {width: 3840, height: 720}]) {
      logicalSize = logical;
      await page.waitForFunction(size => {
        const image = document.querySelector("#desktop-image");
        return +image.getAttribute("width") === size.width && +image.getAttribute("height") === size.height;
      }, logical);
      await delay(100);
      await assertFit();
    }
    await page.screenshot({path: resolve(output, `fit-${viewport.width}.png`)});
  }
  logicalSize = {width: 2560, height: 1440};
  await page.setViewportSize({width: 1440, height: 900});
  await page.waitForFunction(() => +document.querySelector("#desktop-image").getAttribute("width") === 2560);
  assert.equal(await page.locator("#desktop-picture-controls").isVisible(), true);
  await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => document.fullscreenElement?.id === "desktop-tool");
  await assertFit();
  await page.screenshot({path: resolve(output, "fit-fullscreen.png")});
  await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => !document.fullscreenElement);
  await page.locator("#desktop-zoom").evaluate(node => {
    node.value = "200"; node.dispatchEvent(new Event("input", {bubbles: true}));
  });
  await delay(150);
  const clickPoint = await page.evaluate(() => {
    const view = document.querySelector("#desktop-viewport");
    view.scrollLeft = 100; view.scrollTop = 90;
    const image = document.querySelector("#desktop-image").getBoundingClientRect();
    const box = view.getBoundingClientRect();
    const x = box.left + box.width / 2, y = box.top + box.height / 2;
    return {x, y, logicalX: (x - image.left) / image.width * 2560, logicalY: (y - image.top) / image.height * 1440,
      overflow: view.scrollHeight > view.clientHeight || view.scrollWidth > view.clientWidth};
  });
  assert.equal(clickPoint.overflow, true);
  const actionsBeforeZoom = actions.length;
  await page.mouse.click(clickPoint.x, clickPoint.y);
  await waitUntil(() => actions.length > actionsBeforeZoom, "zoomed original-coordinate input");
  assert.ok(Math.abs(actions.at(-1).x - clickPoint.logicalX) < 2 && Math.abs(actions.at(-1).y - clickPoint.logicalY) < 2);
  await page.click("#desktop-fit");
  await delay(100);
  await assertFit();
  await page.keyboard.press("Escape");
  for (const viewport of [{width: 1440, height: 1000}, {width: 390, height: 844}, {width: 320, height: 740}]) {
    await page.setViewportSize(viewport);
    await page.click('[data-desktop-video="bandwidth"]');
    await waitUntil(() => preferences.at(-1).quality === 45, "bandwidth profile");
    await page.click('[data-desktop-video="quality"]');
    await waitUntil(() => preferences.at(-1).quality === 80, "quality profile");
    await page.click('[data-desktop-video="auto"]');
    const editor = page.locator("#desktop-text-input");
    await editor.fill("中文 draft across JPEG frames");
    await editor.focus();
    await delay(500);
    assert.equal(await editor.inputValue(), "中文 draft across JPEG frames");
    assert.equal(await page.evaluate(() => document.activeElement.id), "desktop-text-input");
    const pixels = await page.evaluate(() => {
      const img = document.querySelector("#desktop-image");
      const canvas = document.createElement("canvas"); canvas.width = 16; canvas.height = 16;
      const context = canvas.getContext("2d"); context.drawImage(img, 0, 0, 16, 16);
      return new Set(context.getImageData(0, 0, 16, 16).data).size;
    });
    assert.ok(pixels > 10, "JPEG decoded and nonblank");
    assert.deepEqual(await page.evaluate(() => ({
      document: document.documentElement.scrollWidth > innerWidth,
      panel: document.querySelector("#task-tool-panel-body").scrollWidth > document.querySelector("#task-tool-panel-body").clientWidth,
    })), {document: false, panel: false});
    await page.locator("#task-tool-panel-body").evaluate(element => { element.scrollTop = 0; });
    await page.keyboard.press("Escape");
    await page.screenshot({path: resolve(output, `stream-${viewport.width}.png`)});
  }
  controlError = "desktop_input_partial";
  await page.waitForFunction(() => document.querySelector("#desktop-error").textContent.includes("部分输入"));
  assert.equal(await page.locator("#desktop-stop").isEnabled(), true);
  const connectionsBeforeAccessError = streams.length;
  for (const code of ["desktop_foreground_unavailable", "desktop_foreground_changed"]) {
    controlError = code;
    await page.waitForFunction(() => document.querySelector("#desktop-image").getAttribute("aria-disabled") === "true"
      && document.querySelector("#desktop-error").textContent.includes("前台窗口"));
    assert.equal(await page.locator("#desktop-preview").isVisible(), true);
    assert.equal(await page.locator("#desktop-focus").isDisabled(), true);
    const actionsBeforeError = actions.length;
    const acksBeforeError = acknowledged.length;
    await page.locator("#desktop-image").click({position: {x: 10, y: 10}});
    await page.locator("#desktop-image").press("Enter");
    await page.locator("#desktop-focus").dispatchEvent("click");
    await waitUntil(() => acknowledged.length > acksBeforeError + 1, "image frames continue while input unavailable");
    assert.equal(actions.length, actionsBeforeError);
    assert.equal(streams.length, connectionsBeforeAccessError, "control error must not disconnect a valid image stream");
    assert.match(await page.locator("#desktop-image").getAttribute("src"), /^blob:/);
    await page.screenshot({path: resolve(output, `stream-access-${code}.png`)});
  }
  controlError = "";
  const connectionsBeforeMinimize = streams.length;
  captureError = "window_minimized";
  await page.waitForFunction(() => document.querySelector("#desktop-capture-status")?.textContent.includes("最小化"));
  assert.equal(streams.length, connectionsBeforeMinimize, "minimized window keeps its stream");
  await page.click("#desktop-focus");
  await waitUntil(() => actions.some(action => action.kind === "focus"), "restore from image-free frame");
  captureError = "";
  await page.waitForFunction(() => document.querySelector("#desktop-image").src.startsWith("blob:") && !document.querySelector("#desktop-image").hidden);
  // Active native menus must survive ongoing binary frames and chat live refreshes.
  await openPopover("targets");
  await page.evaluate(() => {
    window.menu = document.querySelector("#desktop-window");
    window.menuChanges = [];
    menu.focus();
    window.observer = new MutationObserver(records => menuChanges.push(...records.map(item => item.type)));
    observer.observe(menu, {attributes: true, childList: true, subtree: true});
    window.chatTimer = setInterval(() => desktop.refreshDesktopPanel(detail, () => {}), 200);
  });
  await page.locator("#desktop-window").click();
  const acksBeforeMenu = acknowledged.length;
  await delay(1800);
  assert.ok(acknowledged.length > acksBeforeMenu);
  assert.equal(await page.evaluate(() => document.activeElement === menu), true);
  assert.deepEqual(await page.evaluate(() => menuChanges), []);
  await page.keyboard.press("Escape");
  await page.evaluate(() => { observer.disconnect(); clearInterval(chatTimer); menu.blur(); });
  const oldView = latestStream().viewID;
  rejectSockets = true;
  latestStream().socket.close();
  await waitUntil(() => httpQueries.length > 0, "socket blocked fallback");
  assert.match(await page.locator("#desktop-video-metrics").textContent(), /HTTP/);
  assert.ok(httpQueries.every(query => query.view_id === oldView && +query.max_width <= 1920 && +query.quality <= 85));
  await delay(5000);
  assert.ok(streams.length <= 3, "bounded reconnect attempts");
  for (const code of ["desktop_busy", "desktop_frame_superseded"]) {
    const displayed = await surface.getAttribute("src");
    const count = httpQueries.length;
    observationFailure = code;
    await waitUntil(() => httpQueries.length > count, "transient fallback capture response");
    await delay(150);
    assert.equal(await surface.getAttribute("src"), displayed, "transient capture retains displayed frame");
    assert.equal(await page.locator("#desktop-focus").isEnabled(), true);
    observationFailure = "";
    await waitUntil(() => httpQueries.length > count + 1, "fallback capture recovery");
  }
  await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => document.fullscreenElement?.id === "desktop-tool");
  await page.click("#desktop-stop");
  await page.waitForFunction(() => document.querySelector("#desktop-status").textContent.includes("已停止"));
  assert.equal(await page.locator("#desktop-fullscreen").isVisible(), true);
  await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => !document.fullscreenElement);
  const connectionsAfterStop = streams.length;
  await delay(800);
  assert.equal(streams.length, connectionsAfterStop);
  await page.evaluate(() => desktop.stopDesktopPanel());
  await delay(100);
  const blobs = await page.evaluate(() => blobCounts);
  assert.ok(blobs.created > 10);
  assert.equal(blobs.revoked, blobs.created, "all displayed and pending Blob URLs released");
  assert.deepEqual(errors, []);
  await page.unrouteAll({behavior: "wait"});
  console.log(`Mock WAN browser passed: 680ms frame delay + 40000 bytes/s throttling, recovery at 70ms + 2000000 bytes/s, binary JPEG+ACK, independent input, logical geometry, frozen gestures, minimized recovery, transient fallback contention, 1440/390/320px and Blob cleanup. Frames ${acknowledged.length}; screenshots ${output}. Not a public-network or native-host measurement.`);
} finally {
  for (const timer of timers) clearTimeout(timer);
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
