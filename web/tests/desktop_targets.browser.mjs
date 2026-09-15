// Real renderer/main layout hooks, mocked target provider. No native input.
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
const taskToolsSource = await readFile(resolve(root, "web/dist/task_tools.js"), "utf8");
const desktopImport = taskToolsSource.match(/from "(\.\/desktop_panel\.js[^"]*)"/)[1].replace("./", "/");
const mainSource = await readFile(resolve(root, "web/dist/app.js"), "utf8");
const layoutHooks = mainSource.slice(mainSource.indexOf("function applyTaskToolLayout()"), mainSource.indexOf("function selectedConversationCategories()"));
assert.ok(layoutHooks.includes("toggle.onclick"));
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
const bootstrap = `
import * as desktop from "${desktopImport}";
import {renderTaskToolPanel, normalizeTaskToolWidth, TASK_TOOL_DEFAULT_WIDTH} from "/task_tools.js";
import {icon} from "/icons.js";
window.desktop = desktop;
const state = {taskTool: "browser", taskToolMode: "fullscreen", taskToolWidth: 42};
function persistTaskToolLayout() {}
${layoutHooks}
function render() {
  if (!state.taskTool) {
    desktop.stopDesktopPanel();
    document.querySelector("#fixture").replaceChildren();
    return;
  }
  if (desktop.desktopPanelInteracting()) { desktop.refreshDesktopPanel(window.detail, () => {}); return; }
  const old = document.querySelector("#desktop-tool");
  document.querySelector("#fixture").innerHTML = '<div class="task-grid task-tool-open task-tool-fullscreen" style="position:relative;display:block;height:100vh">' + renderTaskToolPanel("browser", window.detail, "main", "", state.taskToolMode) + '</div>';
  if (old && old.dataset.taskId === window.detail.task.id) document.querySelector("#desktop-tool").replaceWith(old);
  bindTaskToolLayout();
  document.querySelector("#close-task-tool").onclick = () => { state.taskTool = ""; render(); };
  desktop.bindDesktopPanel(window.detail, () => {});
}
window.renderAgain = render;
window.mount = (id = "targets-task") => {
  desktop.stopDesktopPanel();
  document.querySelector("#fixture").replaceChildren();
  window.detail = {task: {id}};
  state.taskTool = "browser"; state.taskToolMode = "fullscreen";
  render();
};
window.mount();
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body class="task-view-active"><main id="fixture"></main><script type="module" src="/fixture.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/") { response.setHeader("Content-Type", "text/html; charset=utf-8"); response.end(html); }
    else if (name === "/fixture.js") { response.setHeader("Content-Type", "text/javascript"); response.end(bootstrap); }
    else if (/^\/[a-z_]+\.(js|css)$/.test(name)) {
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/dist", name.slice(1))));
    } else response.writeHead(404).end();
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1440, height: 900}});
  const errors = [];
  const confirmations = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
  page.on("dialog", dialog => { confirmations.push(dialog.message()); return dialog.accept(); });
  const targets = {
    windows: [{id: "opaque-window-one", title: "Editor One", process: "editor.exe"},
      {id: "opaque-window-two", title: "Editor Two", process: "other.exe"},
      {id: "opaque-other-desktop", title: "Review Editor", process: "review.exe",
        desktop_id: "desktop-b", desktop_name: "Review & notes", other_desktop: true}],
    desktops: [{id: "desktop-a", name: "Work", current: true}, {id: "desktop-b", name: "Review", current: false}],
    monitors: [{id: "left", name: "Left display", x: -1920, y: 0, width: 1920, height: 1080, primary: false},
      {id: "main", name: "Primary display", x: 0, y: 0, width: 2560, height: 1440, primary: true}],
    desktop_switch_supported: true,
  };
  const freshSession = (id, window = targets.windows[0]) => ({id, task_id: "targets-task", revision: 1, mode: "foreground",
    window, controller: "shared", claimed: false, expires_at: new Date(Date.now() + 600000).toISOString()});
  let session = freshSession("initial");
  let sequence = 0;
  let image = "";
  let heldSwitch = null;
  let switchDelay = false;
  let controlError = "";
  let backgroundDesktopSupported = false;
  const requests = [];
  let targetReads = 0;
  await page.route("**/api/v1/tasks/*/desktop**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const payload = request.postDataJSON();
    if (payload) requests.push({path, payload});
    const status = () => ({supported: true, foreground_supported: true, targets_supported: true, shared_control_supported: true,
      background_desktop_supported: backgroundDesktopSupported, stream_supported: false, session});
    if (path.endsWith("/targets")) { targetReads++; await route.fulfill({json: {targets}}); return; }
    if (path.endsWith("/switch") || path.endsWith("/session")) {
      assert.equal(payload.confirm_shared, true);
      const selection = payload.target;
      const nextWindow = selection.kind === "window" ? targets.windows.find(item => item.id === selection.window_id)
        : {id: `opaque-combined-${++sequence}`, title: selection.kind === "new-desktop" ? "New desktop" : "Review desktop",
          process: "Windows", kind: "desktop", desktop_id: selection.desktop_id || "new-owned", monitor_id: selection.monitor_id};
      const next = freshSession(`session-${++sequence}`, nextWindow);
      next.mode = payload.mode;
      if (switchDelay) {
        session = {...session, revision: session.revision + 1, switching: true};
        await new Promise(resolve => { heldSwitch = resolve; });
        // Deliver a deliberately late success after Stop, without reopening backend state.
        await route.fulfill({json: {status: {...status(), session: next}}}).catch(() => {});
        return;
      }
      session = next;
    }
    if (path.endsWith("/stop")) session = null;
    assert.ok(!path.endsWith("/control"), "cooperative UI must not transfer control");
    if (path.endsWith("/observation")) {
      const snapshot = session;
      await new Promise(resolve => setTimeout(resolve, 20));
      await route.fulfill({json: {observation: {
        id: `frame-${++sequence}`, session_id: snapshot.id, revision: snapshot.revision, window: snapshot.window,
        width: 2560, height: 1440, preview_width: 800, preview_height: 450, mime: "image/png", image,
        elements: [], surface: "fixture-surface", input_actions: snapshot.mode === "foreground" ? ["click", "double_click", "drag", "key", "text", "focus"] : [],
        ...(controlError ? {control_error: controlError, input_actions: []} : {}),
      }}});
      return;
    }
    await route.fulfill({json: {status: status()}});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  image = await page.evaluate(() => {
    const canvas = document.createElement("canvas"); canvas.width = 800; canvas.height = 450;
    const context = canvas.getContext("2d");
    context.fillStyle = "#e5ece8"; context.fillRect(0, 0, 800, 450);
    context.fillStyle = "#23584d"; context.fillRect(0, 0, 800, 45);
    context.fillStyle = "#fff"; context.font = "22px sans-serif"; context.fillText("Shared target fixture", 24, 30);
    context.fillStyle = "#fff"; context.fillRect(50, 90, 500, 275);
    context.fillStyle = "#abc2c8"; context.fillRect(580, 90, 170, 275);
    return canvas.toDataURL().split(",")[1];
  });
  await page.waitForFunction(() => document.querySelector("#desktop-image").naturalWidth === 800);
  const open = async name => {
    if (!await page.locator(`#desktop-${name}-popover`).isVisible()) await page.click(`[data-desktop-popover="${name}"]`);
  };
  const choice = (kind, id) => JSON.stringify({kind, id});
  const assertControlRows = async () => {
    const geometry = await page.evaluate(() => {
      const box = selector => document.querySelector(selector).getBoundingClientRect();
      const header = box(".desktop-task-header"), picture = box("#desktop-picture-controls");
      const viewport = box("#desktop-viewport"), input = box("#desktop-input"), root = box("#desktop-tool");
      const inRow = (selector, row) => [...document.querySelectorAll(selector)].filter(node => node.getClientRects().length).every(node => {
        const b = node.getBoundingClientRect();
        return b.left >= row.left && b.right <= row.right && b.top >= row.top && b.bottom <= row.bottom;
      });
      return {
        ordered: picture.top >= header.bottom && viewport.top >= picture.bottom && input.top >= viewport.bottom && input.bottom <= root.bottom,
        compact: picture.height <= 42 && input.height <= 46,
        qualityFits: inRow("#desktop-picture-controls button, #desktop-zoom, #desktop-zoom-value", picture),
        inputFits: inRow("#desktop-input button, #desktop-text-input", input),
        textUsable: box("#desktop-text-input").width >= 60,
      };
    });
    assert.deepEqual(geometry, {ordered: true, compact: true, qualityFits: true, inputFits: true, textUsable: true});
  };
  // Keep the real outer main controls while desktop bindings cycle independently.
  await page.evaluate(() => {
    window.originalClose = document.querySelector("#close-task-tool");
    window.originalLayout = document.querySelector("#toggle-task-tool-mode");
  });
  for (const lifecycle of ["same-dom", "refresh", "read-only", "same-dom-read-only", "same-dom-again", "mount", "mount-again"]) {
    await page.evaluate(kind => {
      if (kind.startsWith("mount")) {
        mount();
        window.originalClose = document.querySelector("#close-task-tool");
        window.originalLayout = document.querySelector("#toggle-task-tool-mode");
      } else if (kind === "read-only") {
        detail.task.read_only = true;
        desktop.refreshDesktopPanel(detail, () => {});
        if ([...document.querySelectorAll(".desktop-header-controls button")].some(button => !button.disabled)) throw new Error("read-only control must be disabled");
        detail.task.read_only = false;
        desktop.refreshDesktopPanel(detail, () => {});
      } else if (kind === "same-dom-read-only") {
        detail.task.read_only = true;
        desktop.bindDesktopPanel(detail, () => {});
        if ([...document.querySelectorAll(".desktop-picture-bar button, .desktop-picture-bar input, .desktop-input-bar button, .desktop-input-bar textarea")].some(node => !node.disabled)) throw new Error("read-only rows must be disabled");
        detail.task.read_only = false;
        desktop.bindDesktopPanel(detail, () => {});
      } else {
        desktop.stopDesktopPanel();
        if (kind === "refresh") desktop.refreshDesktopPanel(detail, () => {});
        else desktop.bindDesktopPanel(detail, () => {});
      }
    }, lifecycle);
    await page.waitForFunction(() => !document.querySelector("#desktop-focus").disabled);
    assert.equal(await page.locator('[data-desktop-video="quality"]').isEnabled(), true);
    assert.equal(await page.locator("#desktop-text-input").isEditable(), true);
    await page.click('[data-desktop-popover="targets"]');
    assert.equal(await page.locator("#desktop-targets-popover").isVisible(), true, `${lifecycle}: one click must open the popup`);
    await page.keyboard.press("Escape");
    const beforeConfirm = confirmations.length;
    const beforeFocus = requests.filter(item => item.path.endsWith("/actions") && item.payload.kind === "focus").length;
    session.claimed = !session.claimed;
    await page.click("#desktop-focus");
    await page.waitForFunction(() => !document.querySelector("#desktop-focus").disabled);
    assert.equal(await page.locator("#desktop-text-input").isEditable(), true);
    assert.equal(await page.locator('[data-desktop-key="enter"]').isEnabled(), true);
    assert.equal(confirmations.length, beforeConfirm, `${lifecycle}: assistance needs no extra confirmation`);
    assert.equal(requests.filter(item => item.path.endsWith("/actions") && item.payload.kind === "focus").length, beforeFocus + 1);
    assert.equal(await page.locator("[data-desktop-controller]").count(), 0);
    await page.keyboard.press("Escape");
    assert.equal(await page.evaluate(() => document.querySelector("#close-task-tool") === originalClose), true);
    assert.equal(await page.evaluate(() => document.querySelector("#toggle-task-tool-mode") === originalLayout), true);
  }
  await page.evaluate(() => desktop.stopDesktopPanel());
  const beforeStoppedConfirm = confirmations.length;
  const beforeStoppedWrites = requests.length;
  await page.locator('[data-desktop-popover="targets"]').dispatchEvent("click");
  await page.locator("#desktop-focus").dispatchEvent("click");
  await page.locator("#desktop-new-desktop").dispatchEvent("click");
  assert.equal(confirmations.length, beforeStoppedConfirm, "unmounted handlers must not open confirms");
  assert.equal(requests.length, beforeStoppedWrites, "unmounted handlers must not write");
  assert.equal(await page.locator("#desktop-targets-popover").isVisible(), false);
  await page.evaluate(() => desktop.bindDesktopPanel(detail, () => {}));
  await page.waitForFunction(() => document.querySelector("#desktop-image").naturalWidth === 800);
  await open("targets");
  const remoteChoice = choice("window", "opaque-other-desktop");
  const remoteOption = page.locator("#desktop-window option").filter({hasText: "Review Editor"});
  assert.match(await remoteOption.textContent(), /Review & notes（其他桌面）/);
  await page.selectOption("#desktop-window", remoteChoice);
  const writesBeforeSelection = requests.length;
  await page.click("#desktop-refresh");
  await page.waitForTimeout(400);
  assert.equal(requests.length, writesBeforeSelection, "selection/refresh must not switch");
  await page.click('[data-desktop-mode="background"]');
  assert.equal(await remoteOption.evaluate(node => node.disabled), true, JSON.stringify(await page.evaluate(() => ({
    mode: document.querySelector('[data-desktop-mode="background"]').getAttribute("aria-pressed"),
    active: document.activeElement?.outerHTML, options: document.querySelector("#desktop-window").innerHTML,
  }))));
  assert.equal(await page.locator("#desktop-share").isDisabled(), true);
  // Background availability must not depend on permission to switch desktops.
  backgroundDesktopSupported = true;
  targets.desktop_switch_supported = false;
  await page.click("#desktop-refresh");
  await page.waitForFunction(() => !document.querySelector("#desktop-share").disabled);
  assert.equal(await remoteOption.evaluate(node => node.disabled), false);
  assert.equal(await page.locator('#desktop-window optgroup[label="已有桌面"]').count(), 0);
  assert.equal(await page.locator("#desktop-new-desktop").isDisabled(), true);
  const beforeBackgroundConfirm = confirmations.length;
  const writesBeforeBackground = requests.length;
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-title").textContent === "Review Editor");
  assert.equal(confirmations.length, beforeBackgroundConfirm + 1);
  assert.match(confirmations.at(-1), /不切换桌面、不激活窗口/);
  assert.match(confirmations.at(-1), /所属桌面「Review & notes」后台操作，不切换到该桌面/);
  assert.doesNotMatch(confirmations.at(-1), /将切换到窗口所属桌面|会移动宿主鼠标/);
  assert.equal(requests.length, writesBeforeBackground + 1);
  assert.deepEqual(requests.at(-1).payload.target, {kind: "window", window_id: "opaque-other-desktop"});
  assert.equal(requests.at(-1).payload.mode, "background");
  assert.equal(requests.at(-1).payload.confirm_foreground, false);
  assert.equal(requests.at(-1).payload.confirm_shared, true);
  assert.equal(await page.locator("#desktop-focus").isDisabled(), true);
  assert.equal(await page.locator("[data-desktop-action]").count(), 0, "available window is not a promise of controls");
  await page.screenshot({path: resolve(output, "background-cross-desktop.png")});
  await open("targets");
  await page.click('[data-desktop-mode="foreground"]');
  targets.desktop_switch_supported = false;
  await page.click("#desktop-refresh");
  await page.waitForFunction(() => document.querySelector("#desktop-share").disabled);
  assert.equal(await remoteOption.evaluate(node => node.disabled), true);
  targets.desktop_switch_supported = true;
  await page.click("#desktop-refresh");
  await page.waitForFunction(() => !document.querySelector("#desktop-share").disabled);
  const beforeRemoteConfirm = confirmations.length;
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-title").textContent === "Review Editor");
  assert.equal(confirmations.length, beforeRemoteConfirm + 1);
  assert.match(confirmations.at(-1), /将切换到窗口所属桌面：Review & notes/);
  assert.deepEqual(requests.at(-1).payload.target, {kind: "window", window_id: "opaque-other-desktop"});
  assert.equal(requests.at(-1).payload.confirm_foreground, true);
  // Return via the public mock selection path, preserving the rest of the suite.
  await open("targets");
  await page.selectOption("#desktop-window", choice("window", "opaque-window-one"));
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-title").textContent === "Editor One");
  const sessionBeforeDesktopSwitch = session.id;
  await page.locator("#desktop-text-input").fill("draft while watching");
  for (const error of ["desktop_foreground_unavailable", "desktop_foreground_changed"]) {
    controlError = error;
    await page.waitForFunction(() => document.querySelector("#desktop-image").getAttribute("aria-disabled") === "true"
      && !document.querySelector("#desktop-preview").hidden
      && document.querySelector("#desktop-error").textContent.includes("前台窗口"));
    assert.equal(await page.locator("#desktop-preview").isVisible(), true);
    assert.equal(await page.locator("#desktop-focus").isDisabled(), true);
    assert.equal(await page.locator("#desktop-stop").isEnabled(), true);
    assert.equal(await page.locator("#desktop-text-form button").isDisabled(), true);
    const writesBeforeInput = requests.length;
    await page.locator("#desktop-image").click();
    await page.locator("#desktop-image").press("Enter");
    await page.locator("#desktop-focus").dispatchEvent("click");
    await page.locator("#desktop-text-form").dispatchEvent("submit");
    await page.waitForTimeout(350);
    assert.equal(requests.length, writesBeforeInput, "read-only observation must not send any action");
    assert.equal(await page.locator("#desktop-text-input").inputValue(), "draft while watching");
    assert.equal(await page.locator("#desktop-preview").isVisible(), true);
    await page.screenshot({path: resolve(output, `access-frame-${error}.png`)});
  }
  controlError = "";
  await page.waitForFunction(() => !document.querySelector("#desktop-focus").disabled);
  for (const viewport of [{width: 1440, height: 900}, {width: 390, height: 844}, {width: 320, height: 740}, {width: 844, height: 390}]) {
    await page.setViewportSize(viewport);
    await page.waitForTimeout(100);
    assert.equal(await page.locator("h3").filter({hasText: "共享控制"}).count(), 1);
    assert.equal(await page.locator(".desktop-task-header").count(), 1);
    const geometry = await page.evaluate(() => {
      const root = document.querySelector("#desktop-tool").getBoundingClientRect();
      const viewport = document.querySelector("#desktop-viewport").getBoundingClientRect();
      const header = document.querySelector(".desktop-task-header").getBoundingClientRect();
      const img = document.querySelector("#desktop-image").getBoundingClientRect();
      const title = document.querySelector(".desktop-task-header h3").getBoundingClientRect();
      const controls = document.querySelector(".desktop-header-controls").getBoundingClientRect();
      const pages = document.querySelector(".desktop-page-actions").getBoundingClientRect();
      const buttons = [...document.querySelectorAll(".desktop-task-header button")].map(button => button.getBoundingClientRect());
      return {headerHeight: header.height, spareHeight: root.height - viewport.height,
        fits: img.left >= viewport.left - 1 && img.right <= viewport.right + 1 && img.top >= viewport.top - 1 && img.bottom <= viewport.bottom + 1,
        overflow: document.documentElement.scrollWidth > innerWidth || document.documentElement.scrollHeight > innerHeight,
        controlsNearTitle: controls.left >= title.right && controls.left - title.right <= 12,
        pageGroupRight: pages.right <= header.right && header.right - pages.right <= 10,
        groupsSeparated: pages.left - controls.right >= 3 && getComputedStyle(document.querySelector(".desktop-page-actions")).borderLeftWidth === "1px",
        noOverlap: buttons.every((box, i) => box.left >= header.left && box.right <= header.right && box.top >= header.top && box.bottom <= header.bottom
          && (i === 0 || box.left >= buttons[i - 1].right))};
    });
    assert.ok(geometry.headerHeight <= 54);
    assert.ok(geometry.spareHeight < 165, "image gets all height except the header, two compact rows and padding");
    assert.equal(geometry.fits, true);
    assert.equal(geometry.overflow, false, JSON.stringify({viewport, geometry, sizes: await page.evaluate(() => [innerWidth, innerHeight, document.documentElement.scrollWidth, document.documentElement.scrollHeight])}));
    assert.equal(geometry.controlsNearTitle, true);
    assert.equal(geometry.pageGroupRight, true);
    assert.equal(geometry.groupsSeparated, true);
    assert.equal(geometry.noOverlap, true);
    await assertControlRows();
    for (const selector of ["#desktop-stop", "#desktop-focus"]) {
      assert.equal(await page.locator(`.desktop-header-controls ${selector}`).isVisible(), true);
      assert.ok(await page.locator(selector).getAttribute("title"));
      assert.ok(await page.locator(selector).getAttribute("aria-label"));
    }
    const focusBefore = requests.filter(item => item.path.endsWith("/actions") && item.payload.kind === "focus").length;
    await page.click("#desktop-focus");
    await page.waitForFunction(() => !document.querySelector("#desktop-focus").disabled);
    assert.equal(requests.filter(item => item.path.endsWith("/actions") && item.payload.kind === "focus").length, focusBefore + 1);
    await page.screenshot({path: resolve(output, `persistent-controls-${viewport.width}.png`)});
    await open("targets");
    assert.equal(await page.evaluate(() => document.querySelector("#desktop-targets-popover").getBoundingClientRect().top >=
      document.querySelector("#desktop-picture-controls").getBoundingClientRect().bottom), true, "target menu must not cover the persistent quality row");
    assert.equal(await page.locator("#desktop-window optgroup").count(), 2);
    assert.equal(await page.locator("#desktop-monitor option").count(), 2);
    await page.locator("#desktop-window").focus();
    await page.keyboard.press("Escape");
    assert.equal(await page.locator("#desktop-targets-popover").isVisible(), false);
    assert.equal(await page.locator('[data-desktop-popover="targets"]').evaluate(node => node === document.activeElement), true);
    assert.equal(await page.locator("#desktop-picture-controls").isVisible(), true);
    const writesBeforeQuality = requests.filter(item => item.path.endsWith("/actions")).length;
    await page.click('[data-desktop-video="quality"]');
    assert.match(await page.locator("#desktop-picture-controls").getAttribute("title"), /清晰/);
    assert.equal(await page.locator('[data-desktop-video="quality"]').getAttribute("aria-pressed"), "true");
    await page.click(".desktop-task-header h3");
    assert.equal(await page.locator("#desktop-picture-popover").count(), 0);
    await page.waitForTimeout(350);
    await page.locator("#desktop-text-input").fill("preserved draft");
    await page.locator("#desktop-focus").hover();
    assert.equal(await page.evaluate(() => document.activeElement.id), "desktop-text-input", "hover does not take local editor focus");
    assert.equal(requests.filter(item => item.path.endsWith("/actions")).length, writesBeforeQuality, "quality, local typing and hover never dispatch native input");
    await page.evaluate(() => renderAgain());
    assert.equal(await page.locator("#desktop-text-input").inputValue(), "preserved draft");
    await page.keyboard.press("Escape");
  }
  await page.setViewportSize({width: 1440, height: 900});
  for (const width of [300, 320, 360, 420]) {
    await page.locator(".desktop-task-panel").evaluate((panel, width) => { panel.style.width = `${width}px`; }, width);
    await page.waitForTimeout(100);
    const fits = await page.evaluate(() => {
      const header = document.querySelector(".desktop-task-header").getBoundingClientRect();
      const title = document.querySelector(".desktop-task-header h3").getBoundingClientRect();
      const controls = document.querySelector(".desktop-header-controls").getBoundingClientRect();
      const pages = document.querySelector(".desktop-page-actions").getBoundingClientRect();
      return controls.left >= title.right && controls.right + 3 <= pages.left && pages.right <= header.right
        && [...document.querySelectorAll(".desktop-task-header button")].every(button => {
          const box = button.getBoundingClientRect();
          return box.left >= header.left && box.right <= header.right;
        });
    });
    assert.equal(fits, true, `controls fit a ${width}px desktop split panel`);
    await assertControlRows();
    await page.click("#desktop-more-keys");
    assert.equal(await page.locator('[data-desktop-key="run"]').isVisible(), true);
    const beforeKey = requests.filter(item => item.path.endsWith("/actions")).length;
    await page.click('[data-desktop-key="run"]');
    await page.waitForFunction(() => !document.querySelector('[data-desktop-key="run"]').disabled);
    assert.equal(requests.filter(item => item.path.endsWith("/actions")).length, beforeKey + 1);
    assert.deepEqual(requests.filter(item => item.path.endsWith("/actions")).at(-1).payload.keys, ["WIN", "R"]);
    await page.keyboard.press("Escape");
    assert.equal(await page.locator('[data-desktop-key="run"]').isVisible(), false);
    assert.equal(await page.evaluate(() => document.activeElement.id), "desktop-more-keys");
    await page.locator(".desktop-task-panel").screenshot({path: resolve(output, `persistent-controls-split-${width}.png`)});
  }
  await page.locator(".desktop-task-panel").evaluate(panel => { panel.style.width = ""; });
  await page.setViewportSize({width: 390, height: 844});
  await open("targets");
  const readsBefore = targetReads;
  await page.locator("#desktop-window").click();
  await page.waitForTimeout(3000);
  assert.equal(targetReads, readsBefore);
  assert.equal(await page.evaluate(() => document.activeElement.id), "desktop-window");
  await page.keyboard.press("Escape");
  await open("targets");
  await page.selectOption("#desktop-window", choice("desktop", "desktop-b"));
  await page.selectOption("#desktop-monitor", "left");
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-title").textContent === "Review desktop");
  const switched = requests.filter(item => item.path.endsWith("/switch")).at(-1);
  assert.deepEqual(switched.payload.target, {kind: "desktop", desktop_id: "desktop-b", monitor_id: "left"});
  assert.equal(switched.payload.session_id, sessionBeforeDesktopSwitch);
  assert.equal(switched.payload.confirm_foreground, true);
  assert.equal(session.controller, "shared");
  assert.equal(await page.locator("#desktop-targets-popover").isVisible(), false);
  assert.equal(await page.locator("#desktop-text-input").inputValue(), "");
  await open("targets");
  await page.selectOption("#desktop-window", choice("window", "opaque-window-two"));
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-title").textContent === "Editor Two");
  assert.ok(requests.filter(item => item.path.endsWith("/switch")).length >= 2);
  assert.ok(!requests.some(item => item.path.endsWith("/stop")), "switch must not be stop+open");
  await open("targets");
  await page.click("#desktop-new-desktop");
  await page.waitForFunction(() => document.querySelector("#desktop-title").textContent === "New desktop");
  assert.equal(requests.filter(item => item.path.endsWith("/switch")).at(-1).payload.target.kind, "new-desktop");
  await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => document.fullscreenElement?.id === "desktop-tool");
  await page.keyboard.press("Escape");
  await open("targets");
  switchDelay = true;
  await page.selectOption("#desktop-window", choice("window", "opaque-window-one"));
  const actionsBeforeSwitch = requests.filter(item => item.path.endsWith("/actions")).length;
  await page.click("#desktop-share");
  await page.waitForFunction(() => document.querySelector("#desktop-preview").hidden);
  assert.equal(await page.locator("#desktop-stop").isEnabled(), true);
  await page.click("#desktop-stop");
  heldSwitch?.();
  await page.waitForFunction(() => document.querySelector("#desktop-status").textContent.includes("已停止"));
  await page.waitForTimeout(150);
  assert.equal(session, null);
  assert.equal(requests.filter(item => item.path.endsWith("/actions")).length, actionsBeforeSwitch);
  if (await page.evaluate(() => Boolean(document.fullscreenElement))) await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => !document.fullscreenElement);
  await page.keyboard.press("Escape");
  await page.locator("#desktop-viewport").click({position: {x: 8, y: 8}});
  await page.evaluate(() => { renderAgain(); renderAgain(); renderAgain(); });
  await page.click("#toggle-task-tool-mode");
  assert.equal(await page.locator(".task-tool-panel").evaluate(node => node.classList.contains("split")), true);
  await page.click("#toggle-task-tool-mode");
  assert.equal(await page.locator(".task-tool-panel").evaluate(node => node.classList.contains("fullscreen")), true);
  await page.click("#desktop-fullscreen");
  await page.waitForFunction(() => Boolean(document.fullscreenElement));
  await page.click("#close-task-tool");
  await page.waitForFunction(() => !document.querySelector("#desktop-tool") && !document.fullscreenElement);
  await page.evaluate(() => mount("next-task"));
  assert.equal(await page.locator("#close-task-tool").count(), 1);
  await page.click("#close-task-tool");
  assert.equal(await page.locator("#desktop-tool").count(), 0);
  assert.deepEqual(errors, []);
  await page.unrouteAll({behavior: "wait"});
  console.log("Targets browser passed: production CSP, real task header/main layout hooks, persistent quality/input rows, left control/right page groups, focus-safe hover/quality, single requests, same-DOM/read-only/remount listeners; 1440/390/320/844 landscape and narrow split panels, target switch/Stop/fullscreen cleanup. Mock target provider only.");
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
