// Optional browser verification; dependencies and browser files stay in .tools.
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
const toolsSource = await readFile(resolve(root, "web/dist/task_tools.js"), "utf8");
const desktopImport = toolsSource.match(/from "(\.\/desktop_panel\.js[^"]*)"/)[1].replace("./", "/");
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
const bootstrap = `
import * as desktop from "${desktopImport}";
import {renderTaskToolPanel} from "/task_tools.js";
const detail = {task: {id: "test-task"}};
window.desktop = desktop;
window.mount = (id = "test-task", remote = false) => {
  desktop.stopDesktopPanel();
  window.detail = {task: {id, read_only: remote}};
  document.querySelector("#fixture").innerHTML = '<div class="task-grid task-tool-open task-tool-fullscreen" style="position:relative;height:100vh;display:block">' + renderTaskToolPanel("browser", window.detail, "main", "", "fullscreen") + '</div>';
  desktop.bindDesktopPanel(window.detail, () => {});
};
window.mount();
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body class="task-view-active"><main id="fixture"></main><script type="module" src="/fixture.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else {
      const allowed = /^\/[a-z_]+\.(?:js|css)$/.test(name);
      if (!allowed) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/dist", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
  browser = await chromium.launch({
    executablePath: await binary.executablePath(),
    args: binary.args,
    headless: true,
  });
  const page = await browser.newPage();
  const choice = id => JSON.stringify({kind: "window", id});
  const openPopover = async name => {
    if (!await page.locator(`#desktop-${name}-popover`).isVisible()) await page.click(`[data-desktop-popover="${name}"]`);
  };
  const errors = [];
  const activeObservations = new Set();
  page.on("requestfailed", request => activeObservations.delete(request));
  page.on("requestfinished", request => activeObservations.delete(request));
  page.on("pageerror", error => errors.push(error.message));
  page.on("dialog", dialog => dialog.accept());
  await page.route("**/api/v1/tasks/*/desktop**", async route => {
    const request = route.request();
    const endpoint = new URL(request.url()).pathname;
    const payload = request.postDataJSON();
    if (endpoint.includes("/remote-task/")) throw new Error("remote task must never access desktop API");
    if (endpoint.endsWith("/session")) {
      sessionRequests.push(payload);
      session = makeSession(payload.mode);
      if (payload.target.window_id === secondTarget.id) session.window = secondTarget;
    }
    if (endpoint.endsWith("/stop")) session = null;
    assert.ok(!endpoint.endsWith("/control"), "no exclusive handoff request");
    if (endpoint.endsWith("/actions")) {
      sentActions.push(payload);
      if (actionDelay) await new Promise(resolve => setTimeout(resolve, actionDelay));
    }
    let body = {ok: true, status: {supported: true, foreground_supported: true, shared_control_supported: true, session}};
    if (endpoint.endsWith("/windows")) {
      windowReads++;
      body = {ok: true, windows: [target, secondTarget]};
    }
    if (endpoint.endsWith("/observation")) {
      const snapshotSession = session;
      activeObservations.add(request);
      maxInFlight = Math.max(maxInFlight, activeObservations.size);
      await new Promise(resolve => setTimeout(resolve, 75));
      body = {ok: true, observation: {
        id: `snapshot-${++observations}`, session_id: snapshotSession.id, revision: snapshotSession.revision,
        window: target, image: minimized ? "" : image, width: minimized ? 0 : 800, height: minimized ? 0 : 450,
        capture_error: minimized ? "Window minimized" : "",
        surface: "geometry-v1",
        input_actions: snapshotSession.mode === "foreground" ? (minimized ? ["focus"] : ["click", "double_click", "drag", "scroll", "text", "key", "focus"]) : [],
        elements: snapshotSession.mode === "foreground" ? [] : [
          {id: "container", name: "Editor", role: "Window", x: 0, y: 0, width: 800, height: 450, actions: []},
          {id: "value", name: '<Entry name="untrusted">', role: "Edit", value: "Original", x: 40, y: 80, width: 300, height: 65, actions: ["set_value", "invoke", "unknown"]},
          ...["toggle", "select", "expand", "collapse"].map((kind, i) => ({id: kind, name: kind, role: "Control", x: 400, y: 80 + i * 65, width: 300, height: 50, actions: [kind]})),
        ],
      }};
    }
    await route.fulfill({json: body});
  });
  const target = {id: "win-1", title: "Shared sample editor with a long document title", process: "editor.exe"};
  const secondTarget = {id: "win-2", title: "Second shared window", process: "second-editor.exe"};
  const makeSession = mode => ({id: "session-1", task_id: "test-task", window: target, controller: "shared", revision: 1, expires_at: new Date(Date.now() + 600000).toISOString(), claimed: false, mode});
  let session = null;
  let observations = 0;
  let maxInFlight = 0;
  const sentActions = [];
  const sessionRequests = [];
  let windowReads = 0;
  let minimized = false;
  let actionDelay = 0;
  const stopAndReselect = async mode => {
    await openPopover("targets");
    const chooser = page.locator("#desktop-window");
    assert.equal(await chooser.isDisabled(), false);
    await chooser.scrollIntoViewIfNeeded();
    await page.evaluate(() => {
      window.disabledPointerEvents = 0;
      document.querySelector("#desktop-window").addEventListener("pointerdown", () => disabledPointerEvents++, {once: true});
    });
    const bounds = await chooser.boundingBox();
    // Use a real pointer event: locator.click intentionally refuses disabled controls.
    await page.mouse.click(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
    assert.equal(await page.evaluate(() => disabledPointerEvents), 1);
    await page.click("#desktop-stop");
    await page.waitForFunction(() => document.querySelector("#desktop-status").textContent.includes("已停止"));
    await page.waitForFunction(() => !document.querySelector("#desktop-window").disabled);
    await openPopover("targets");
    await chooser.click();
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    assert.equal(await chooser.inputValue(), choice(secondTarget.id));
    assert.equal(await page.locator("#desktop-share").isEnabled(), true);
    await page.screenshot({path: resolve(output, `reselect-${mode}.png`)});
    await page.click("#desktop-share");
    await page.waitForFunction(() => document.querySelector("#desktop-status").textContent.includes("共同控制"));
    assert.equal(sessionRequests.at(-1).target.window_id, secondTarget.id);
    assert.equal(sessionRequests.at(-1).mode, mode);
    await page.click("#desktop-stop");
    await page.waitForFunction(() => !document.querySelector("#desktop-window").disabled);
  };
  // A deterministic bitmap fixture makes screenshot geometry and image rendering observable.
  await page.goto(url);
  const image = await page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 800; canvas.height = 450;
    const context = canvas.getContext("2d");
    context.fillStyle = "#f7f8fa"; context.fillRect(0, 0, 800, 450);
    context.fillStyle = "#e1e6ec"; context.fillRect(0, 0, 800, 42);
    context.fillStyle = "#172027"; context.font = "20px sans-serif"; context.fillText("Sample editor", 20, 28);
    context.fillStyle = "#ffffff"; context.fillRect(40, 80, 300, 65);
    context.strokeStyle = "#5777ad"; context.strokeRect(40, 80, 300, 65);
    context.fillStyle = "#172027"; context.fillText("Original", 55, 120);
    ["Toggle", "Select", "Expand", "Collapse"].forEach((label, i) => {
      context.fillStyle = "#e9f3eb"; context.fillRect(400, 80 + i * 65, 300, 50);
      context.fillStyle = "#185d30"; context.fillText(label, 420, 112 + i * 65);
    });
    return canvas.toDataURL("image/png").split(",")[1];
  });
  await page.waitForFunction(() => !document.querySelector("#desktop-window").disabled);
  await openPopover("targets");
  await page.click('[data-desktop-mode="background"]');
  await page.selectOption("#desktop-window", choice("win-1"));
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-image").naturalWidth === 800);
  for (const viewport of [{width: 1440, height: 1000}, {width: 390, height: 844}, {width: 320, height: 740}]) {
    await page.setViewportSize(viewport);
    await page.keyboard.press("Escape");
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const bounds = await page.locator("#desktop-image").boundingBox();
    await page.locator("#desktop-image").click({position: {x: bounds.width * 100 / 800, y: bounds.height * 110 / 450}});
    await page.waitForSelector("#desktop-value-form", {state: "visible"});
    assert.equal(await page.locator("#desktop-highlight").isVisible(), true);
    assert.equal(await page.locator("[data-desktop-action=unknown]").count(), 0);
    await page.locator("#desktop-value-input").fill("Draft survives polling");
    await page.locator("#desktop-value-input").focus();
    const before = observations;
    await page.waitForFunction(() => document.querySelector("#desktop-image").naturalWidth === 800);
    await page.waitForTimeout(3000);
    assert.ok(observations > before);
    assert.equal(await page.locator("#desktop-value-input").inputValue(), "Draft survives polling");
    assert.equal(await page.evaluate(() => document.activeElement?.id), "desktop-value-input");
    assert.equal(await page.evaluate(() => desktop.refreshDesktopPanel(detail, () => {})), false);
    const overflow = await page.evaluate(() => ({
      document: document.documentElement.scrollWidth > innerWidth,
      panel: document.querySelector("#task-tool-panel-body").scrollWidth > document.querySelector("#task-tool-panel-body").clientWidth,
    }));
    assert.deepEqual(overflow, {document: false, panel: false});
    await page.locator("#task-tool-panel-body").evaluate(node => { node.scrollTop = 0; });
    await page.screenshot({path: resolve(output, `desktop-${viewport.width}.png`)});
    await page.locator("#desktop-value-form").scrollIntoViewIfNeeded();
    await page.screenshot({path: resolve(output, `inspector-${viewport.width}.png`)});
  }
  await page.click("#desktop-value-form button");
  await page.waitForFunction(() => !document.querySelector("#desktop-value-form button").disabled);
  // The Owner could not act here before: the panel refused every element action
  // unless the session was in foreground mode, which also steals their machine.
  assert.equal(await page.locator("#desktop-mode-status").innerText(), "后台 · 元素操作", "this flow must run in background mode to be meaningful");
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-input").hidden), true, "free-form coordinate input must stay foreground-only");
  assert.equal(sentActions[0].kind, "set_value");
  assert.equal(sentActions[0].value, "Draft survives polling");
  for (const kind of ["toggle", "select", "expand", "collapse"]) {
    await page.click(`[data-desktop-element="${kind}"]`);
    await page.click(`[data-desktop-action="${kind}"]`);
    await page.waitForFunction(() => !document.querySelector("#desktop-refresh").disabled);
    assert.equal(sentActions.at(-1).kind, kind);
  }
  await openPopover("targets");
  session.claimed = true;
  await page.click("#desktop-refresh");
  await page.waitForFunction(() => document.querySelector("#desktop-status").textContent.includes("Agent 已接入"));
  await openPopover("operations");
  await page.click('[data-desktop-element="value"]');
  assert.equal(await page.locator("#desktop-value-form button").isDisabled(), false);
  assert.equal(await page.locator("[data-desktop-controller]").count(), 0);
  await stopAndReselect("background");
  await page.evaluate(() => window.mount("foreground-task"));
  await page.waitForFunction(() => !document.querySelector("#desktop-window").disabled);
  assert.equal(await page.locator("#desktop-window").inputValue(), "");
  await openPopover("targets");
  const readsBeforeMenu = windowReads;
  await page.evaluate(() => {
    window.chooser = document.querySelector("#desktop-window");
    window.menuEvents = [];
    window.menuMutations = [];
    for (const name of ["focus", "blur", "change", "pointerdown"]) chooser.addEventListener(name, () => menuEvents.push(name));
    window.menuObserver = new MutationObserver(records => menuMutations.push(...records.map(record => record.type)));
    menuObserver.observe(chooser, {attributes: true, childList: true, subtree: true});
    window.chatTimer = setInterval(() => desktop.refreshDesktopPanel(window.detail, () => {}), 300);
  });
  // Open Chromium's actual native select popup, not selectOption or synthetic events.
  await page.locator("#desktop-window").click();
  await page.waitForTimeout(5600);
  assert.equal(await page.evaluate(() => document.querySelector("#desktop-window") === chooser), true);
  assert.equal(await page.evaluate(() => document.activeElement === chooser), true);
  assert.deepEqual(await page.evaluate(() => menuMutations), []);
  assert.equal(windowReads, readsBeforeMenu, "polls must not fetch lists");
  assert.equal(await page.evaluate(() => menuEvents.includes("blur")), false);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => JSON.parse(document.querySelector("#desktop-window").value).id === "win-1");
  assert.equal(await page.evaluate(() => menuEvents.includes("change")), true);
  await page.waitForTimeout(2800);
  assert.equal(await page.locator("#desktop-window").inputValue(), choice("win-1"));
  await page.evaluate(() => { menuObserver.disconnect(); clearInterval(chatTimer); });
  await page.click("#desktop-new-desktop");
  await page.waitForFunction(() => document.querySelector("#desktop-image").naturalWidth === 800 && !document.querySelector("#desktop-focus").disabled);
  assert.deepEqual(sessionRequests.at(-1), {target: {kind: "new-desktop"}, mode: "foreground", confirm_foreground: true, confirm_shared: true});
  assert.equal(await page.locator(".desktop-inspector").isVisible(), false);
  const ready = () => page.waitForFunction(() => !document.querySelector("#desktop-focus").disabled);
  await ready();
  const typingStart = sentActions.length;
  actionDelay = 600;
  await page.locator("#desktop-image").focus();
  await page.keyboard.press("Control+l");
  await page.keyboard.type("github.com", {delay: 0});
  await ready();
  actionDelay = 0;
  assert.equal(await page.locator("#desktop-text-input").inputValue(), "github.com",
    "rapid typing must move into a real editor, not disappear behind an in-flight native call");
  assert.equal(sentActions.length, typingStart + 1, "typing must not create a delayed input queue");
  await page.click("#desktop-text-form button");
  await ready();
  assert.equal(sentActions.at(-1).kind, "text");
  assert.equal(sentActions.at(-1).value, "github.com");
  for (const viewport of [{width: 1440, height: 1000}, {width: 390, height: 844}, {width: 320, height: 740}]) {
    await page.setViewportSize(viewport);
    await page.keyboard.press("Escape");
    await ready();
    await page.locator("#desktop-image").scrollIntoViewIfNeeded();
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const imageNode = page.locator("#desktop-image");
    await imageNode.click({position: {x: 25, y: 25}});
    await page.waitForTimeout(500);
    await ready();
    assert.equal(sentActions.at(-1).kind, "click");
    assert.equal(sentActions.at(-1).element_id, "$surface");
    const actionCount = sentActions.length;
    await imageNode.dblclick({position: {x: 25, y: 25}});
    await page.waitForTimeout(500);
    await ready();
    assert.equal(sentActions.at(-1).kind, "double_click");
    assert.equal(sentActions.length, actionCount + 1, "double click must not also send a single click");
    const bounds = await imageNode.boundingBox();
    await page.mouse.move(bounds.x + 20, bounds.y + 20);
    await page.mouse.down();
    await page.mouse.move(bounds.x + 90, bounds.y + 70, {steps: 5});
    await page.mouse.up();
    await page.waitForTimeout(200);
    await ready();
    assert.equal(sentActions.at(-1).kind, "drag");
    await imageNode.focus();
    await page.mouse.move(bounds.x + 35, bounds.y + 40);
    await page.mouse.wheel(0, 480);
    await page.waitForTimeout(200);
    await ready();
    assert.equal(sentActions.at(-1).kind, "scroll");
    await imageNode.press("Control+l");
    await page.waitForTimeout(200);
    await ready();
    assert.deepEqual(sentActions.at(-1).keys, ["CTRL", "L"]);
    assert.equal(await page.locator("#desktop-input").isVisible(), true);
    await page.locator("#desktop-text-input").fill("中文 😀 draft");
    await page.locator("#desktop-text-input").focus();
    const beforeTyping = sentActions.length;
    await page.keyboard.type(" AHA");
    await page.waitForTimeout(2800);
    assert.equal(sentActions.length, beforeTyping, "local textarea typing must not trigger host input");
    assert.equal(await page.locator("#desktop-text-input").inputValue(), "中文 😀 draft AHA");
    assert.equal(await page.evaluate(() => document.activeElement.id), "desktop-text-input");
    assert.deepEqual(await page.evaluate(() => ({
      document: document.documentElement.scrollWidth > innerWidth,
      panel: document.querySelector("#task-tool-panel-body").scrollWidth > document.querySelector("#task-tool-panel-body").clientWidth,
    })), {document: false, panel: false});
    await page.locator("#task-tool-panel-body").evaluate(node => { node.scrollTop = 0; });
    await page.screenshot({path: resolve(output, `foreground-${viewport.width}.png`)});
    await page.locator("#desktop-text-form").scrollIntoViewIfNeeded();
    await page.screenshot({path: resolve(output, `foreground-input-${viewport.width}.png`)});
    await page.click("#desktop-text-form button");
    await ready();
    assert.equal(sentActions.at(-1).kind, "text");
    assert.equal(sentActions.at(-1).value, "中文 😀 draft AHA");
  }
  await openPopover("keys");
  await page.click('[data-desktop-key="start"]');
  await ready();
  assert.deepEqual(sentActions.at(-1).keys, ["WIN"]);
  await page.click('[data-desktop-key="run"]');
  await ready();
  assert.deepEqual(sentActions.at(-1).keys, ["WIN", "R"]);
  minimized = true;
  await openPopover("targets");
  await page.click("#desktop-refresh");
  await page.waitForFunction(() => document.querySelector("#desktop-preview").hidden);
  assert.equal(await page.locator("#desktop-focus").isEnabled(), true);
  await openPopover("operations");
  await page.click("#desktop-focus");
  await ready();
  assert.equal(sentActions.at(-1).kind, "focus");
  minimized = false;
  await openPopover("targets");
  await page.click("#desktop-refresh");
  await page.waitForFunction(() => document.querySelector("#desktop-image").naturalWidth === 800);
  await stopAndReselect("foreground");
  await page.evaluate(() => window.mount("remote-task", true));
  assert.match(await page.locator("#desktop-tool").textContent(), /只读/);
  assert.equal(maxInFlight, 1);
  assert.deepEqual(errors, []);
  await page.unrouteAll({behavior: "wait"});
  console.log(`Browser verification passed (mock API, not native host): background/foreground, native select popup across polls/chat updates, pointer/text/key/focus at 1440/390/320px. Screenshots: ${output}; temporary fixture ${url}`);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
