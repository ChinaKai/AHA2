import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const moduleURL = pathToFileURL(resolve(root, "dist/desktop_panel.js"));
const desktop = await import(moduleURL);
const source = await readFile(moduleURL, "utf8");
const apiURL = new URL(source.match(/from "(\.\/api\.js[^"]*)"/)[1], moduleURL);
const {api} = await import(apiURL);
const detail = id => ({task: {id}});
const sharedWindow = {id: "window-1", title: "<Editor & notes>", process: "editor.exe"};
const session = (overrides = {}) => ({
  id: "session-1", task_id: "task-1", window: sharedWindow, controller: "shared",
  revision: 1, claimed: false, expires_at: new Date(Date.now() + 600000).toISOString(), ...overrides,
});
const status = value => ({supported: true, foreground_supported: true, shared_control_supported: true, session: value});
const element = {
  id: "edit-1", name: "<untrusted>", role: "Edit", value: "original",
  actions: ["set_value", "invoke", "raw_input"], x: 10, y: 10, width: 40, height: 30,
};
const observation = (overrides = {}) => ({
  id: "observation-1", session_id: "session-1", revision: 1, window: sharedWindow,
  elements: [element], image: "", width: 100, height: 100, ...overrides,
});
const flush = () => new Promise(resolve => setImmediate(resolve));

test("preview fits both axes without cropping and zoom preserves aspect ratio", () => {
  for (const [w, h, vw, vh] of [[2560, 1440, 600, 300], [900, 1600, 300, 400], [1600, 900, 1440, 450], [800, 600, 280, 100]]) {
    const fit = desktop.desktopPreviewSize(w, h, vw, vh);
    assert.ok(fit.width <= vw + .001 && fit.height <= vh + .001);
    assert.ok(Math.abs(fit.width / fit.height - w / h) < .0001);
    const zoom = desktop.desktopPreviewSize(w, h, vw, vh, 2);
    assert.equal(zoom.width, fit.width * 2);
    assert.equal(zoom.height, fit.height * 2);
  }
  assert.deepEqual(desktop.desktopPreviewSize(0, 100, 100, 100), {width: 0, height: 0});
});
function actionPayload(call) {
  const {view_id, action_id, ...payload} = call.payload;
  assert.match(view_id, /^[a-f0-9]{32}$/);
  assert.match(action_id, /^[a-f0-9]{32}$/);
  return payload;
}
const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
};

class Node {
  constructor(dataset = {}) {
    this.dataset = dataset;
    this.listeners = new Map();
    this.attributes = new Map();
    this.style = {};
    this.classList = {toggle() {}};
    this.isConnected = true;
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.textContent = "";
    this.innerHTML = "";
    this.scrollTop = 0;
  }
  addEventListener(name, callback) {
    if (!this.listeners.has(name)) this.listeners.set(name, new Set());
    this.listeners.get(name).add(callback);
  }
  removeEventListener(name, callback) { this.listeners.get(name)?.delete(callback); }
  fire(name, values = {}) {
    for (const listener of [...(this.listeners.get(name) || [])]) listener({target: this, currentTarget: this, preventDefault() {}, ...values});
  }
  setAttribute(name, value) { this.attributes.set(name, value); }
  getAttribute(name) { return this.attributes.get(name) ?? null; }
  removeAttribute(name) { this.attributes.delete(name); }
  closest() { return this; }
  contains() { return false; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
  setPointerCapture() {}
  hasPointerCapture() { return false; }
  releasePointerCapture() {}
  focus() { globalThis.document.activeElement = this; }
  setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  getBoundingClientRect() { return {left: 0, top: 0, width: 200, height: 200}; }
}

function harness(id, responder) {
  const originals = Object.fromEntries(["window", "document", "HTMLElement", "Element"].map(key => [key, globalThis[key]]));
  const originalRequest = api.request;
  const timers = new Map();
  const calls = [];
  let timerID = 0;
  const document = new Node();
  document.hidden = false;
  document.activeElement = null;
  const makeRoot = taskID => {
    const panel = new Node({taskId: taskID});
    const nodes = new Map();
    panel.querySelector = selector => {
      if (!nodes.has(selector)) nodes.set(selector, new Node());
      return nodes.get(selector);
    };
    const owner = new Node({desktopController: "owner"});
    const agent = new Node({desktopController: "agent"});
    panel.querySelectorAll = selector => selector === "[data-desktop-controller]" ? [owner, agent] : [];
    panel.owner = owner;
    panel.agent = agent;
    const keys = ["start", "run", "enter"].map(desktopKey => new Node({desktopKey}));
    panel.querySelectorAll = selector => selector === "[data-desktop-controller]" ? [owner, agent]
      : selector === "[data-desktop-key]" ? keys : [];
    panel.keys = keys;
    return panel;
  };
  let panel = makeRoot(id);
  document.querySelector = selector => selector === "#desktop-tool" ? panel : null;
  globalThis.window = {
    setTimeout(callback) { const id = ++timerID; timers.set(id, callback); return id; },
    clearTimeout(id) { timers.delete(id); },
    confirm() { return true; },
  };
  globalThis.document = document;
  globalThis.HTMLElement = Node;
  globalThis.Element = Node;
  api.request = async (url, options) => {
    const call = {url, options, payload: options.body ? JSON.parse(options.body) : null};
    calls.push(call);
    return responder(call);
  };
  const notices = [];
  const notify = (...args) => notices.push(args);
  return {
    get panel() { return panel; }, document, calls, timers, notices, notify,
    bind(task = detail(id)) { desktop.renderDesktopPanel(task); desktop.bindDesktopPanel(task, notify); },
    changeTask(nextID) { panel.isConnected = false; panel = makeRoot(nextID); this.bind(detail(nextID)); },
    tick() { const callbacks = [...timers.values()]; timers.clear(); callbacks.forEach(callback => callback()); },
    close() {
      desktop.stopDesktopPanel();
      api.request = originalRequest;
      for (const [key, value] of Object.entries(originals)) {
        if (value === undefined) delete globalThis[key]; else globalThis[key] = value;
      }
    },
  };
}

test("transient capture or enumeration contention preserves the displayed frame and retries reads", async () => {
  let captureFailure = "", windowFailure = "";
  let windowCalls = 0;
  const shared = session({mode: "foreground"});
  const frame = observation({image: "AA==", mime: "image/jpeg", elements: [], input_actions: ["click", "focus"]});
  const h = harness("capture-contention", call => {
    if (call.url.endsWith("/windows")) {
      windowCalls++;
      if (windowFailure) throw Object.assign(new Error(windowFailure), {status: 409, code: windowFailure});
      return {windows: [sharedWindow]};
    }
    if (call.url.includes("/observation?")) {
      if (captureFailure) throw Object.assign(new Error(captureFailure), {status: 409, code: captureFailure});
      return {observation: frame};
    }
    return {status: {...status(shared), foreground_supported: true}};
  });
  try {
    h.bind();
    await flush();
    const image = h.panel.querySelector("#desktop-image").src;
    for (const code of ["desktop_busy", "desktop_frame_superseded"]) {
      captureFailure = code;
      h.tick();
      await flush();
      assert.equal(h.panel.querySelector("#desktop-image").src, image);
      assert.equal(h.panel.querySelector("#desktop-focus").disabled, false);
      assert.equal(h.panel.querySelector("#desktop-error").textContent, "");
    }
    captureFailure = "";
    windowFailure = "desktop_busy";
    h.panel.querySelector("#desktop-refresh").fire("click");
    await flush();
    assert.equal(h.panel.querySelector("#desktop-focus").disabled, false);
    const attempts = windowCalls;
    windowFailure = "";
    h.tick();
    await flush();
    assert.ok(windowCalls > attempts, "failed enumeration stays scheduled for retry");
  } finally { h.close(); }
});

test("browser tool keeps its persisted ID and renders shared control, not a placeholder", async () => {
  const tools = await import(pathToFileURL(resolve(root, "dist/task_tools.js")));
  assert.equal(tools.taskToolTitle("browser"), "共享控制");
  assert.match(tools.renderTaskToolButtons("browser"), /data-task-tool="browser"[^>]+title="共享控制"/);
  const html = tools.renderTaskToolContent("browser", detail("render-test"), "");
  assert.match(html, /id="desktop-tool"/);
  assert.doesNotMatch(html, /后续版本|task-tool-placeholder/);
  assert.match(desktop.renderDesktopPanel(detail('task"><script>')), /data-task-id="task&quot;&gt;&lt;script&gt;"/);
});

test("unsupported, remote and unauthorized states do not grant controls", async () => {
  let h = harness("unsupported", () => ({status: {supported: false, reason: "Windows only", session: null}}));
  try {
    h.bind();
    await flush();
    assert.equal(h.calls.length, 1);
    assert.equal(h.panel.querySelector("#desktop-status").textContent, "Windows only");
    assert.equal(h.panel.querySelector("#desktop-share").disabled, true);
  } finally { h.close(); }
  h = harness("remote", () => { throw new Error("remote must not call host API"); });
  try {
    h.bind({task: {id: "remote", read_only: true}});
    await flush();
    assert.equal(h.calls.length, 0);
    assert.match(desktop.renderDesktopPanel({task: {id: "remote", read_only: true}}), /只读/);
  } finally { h.close(); }
  h = harness("denied", () => { throw Object.assign(new Error("not authorized"), {status: 403}); });
  try {
    h.bind();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-status").textContent, "未授权访问共享控制");
    assert.equal(h.timers.size, 0);
    assert.equal(h.panel.querySelector("#desktop-share").disabled, true);
  } finally { h.close(); }
});

test("sharing confirms joint access once and never transfers exclusive control", async () => {
  let currentSession = null;
  const h = harness("lifecycle", ({url, payload}) => {
    if (url.endsWith("/windows")) return {windows: [sharedWindow]};
    if (url.endsWith("/session")) currentSession = session();
    if (url.endsWith("/stop")) currentSession = null;
    if (url.includes("/observation?")) return {observation: observation({revision: currentSession.revision})};
    return {status: status(currentSession)};
  });
  try {
    let confirmation = "";
    window.confirm = message => { confirmation = message; return true; };
    h.bind();
    await flush();
    const chooser = h.panel.querySelector("#desktop-window");
    assert.match(chooser.innerHTML, /&lt;Editor &amp; notes&gt;/);
    chooser.value = JSON.stringify({kind: "window", id: sharedWindow.id});
    chooser.fire("change");
    h.panel.querySelector("#desktop-share").fire("click");
    await flush();
    assert.deepEqual(h.calls.find(call => call.url.endsWith("/session")).payload, {target: {kind: "window", window_id: sharedWindow.id}, mode: "foreground", confirm_foreground: true, confirm_shared: true});
    assert.equal(h.panel.querySelector("#desktop-share").disabled, false);
    assert.match(confirmation, /Main Agent 和 Owner 共同查看并操作/);
    assert.ok(!h.calls.some(call => call.url.endsWith("/control")));
    assert.match(h.panel.querySelector("#desktop-status").textContent, /共同控制/);
    assert.equal(currentSession.controller, "shared");
    h.panel.querySelector("#desktop-stop").fire("click");
    await flush();
    assert.deepEqual(h.calls.find(call => call.url.endsWith("/stop")).payload, {session_id: "session-1"});
    assert.equal(h.panel.querySelector("#desktop-status").textContent, "共享已停止");
  } finally { h.close(); }
});

test("clicking a disabled window chooser cannot keep it locked after stopping sharing", async () => {
  {
    let currentSession = null;
    const second = {...sharedWindow, id: "window-2", title: "Second window"};
    const h = harness("stop-chooser", ({url, payload}) => {
      if (url.endsWith("/windows")) return {windows: [sharedWindow, second]};
      if (url.endsWith("/session")) currentSession = session({window: {id: payload.target.window_id}});
      if (url.endsWith("/stop")) currentSession = null;
      if (url.includes("/observation?")) return {observation: observation()};
      return {status: {...status(currentSession), foreground_supported: true}};
    });
    try {
      h.bind();
      await flush();
      const chooser = h.panel.querySelector("#desktop-window");
      chooser.value = JSON.stringify({kind: "window", id: sharedWindow.id});
      chooser.fire("change");
      h.panel.querySelector("#desktop-share").fire("click");
      await flush();
      chooser.disabled = true;
      // Disabled selects can receive pointer events without focus/blur.
      chooser.fire("pointerdown");
      h.panel.querySelector("#desktop-stop").fire("click");
      await flush();
      assert.equal(h.panel.querySelector("#desktop-status").textContent, "共享已停止");
      assert.equal(chooser.disabled, false, "stopped chooser remains disabled");
      assert.equal(desktop.desktopPanelInteracting(), false);
      chooser.value = JSON.stringify({kind: "window", id: second.id});
      chooser.fire("change");
      h.panel.querySelector("#desktop-share").fire("click");
      await flush();
      assert.equal(h.calls.filter(call => call.url.endsWith("/session")).at(-1).payload.target.window_id, second.id);
    } finally { h.close(); }
  }
});

test("zoom cancels a pending gesture and resumes fallback observations", async () => {
  const h = harness("zoom-gesture", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    const view = h.panel.querySelector("#desktop-viewport");
    view.clientWidth = 300;
    view.clientHeight = 150;
    h.bind();
    await flush();
    const before = h.calls.filter(call => call.url.includes("/observation?")).length;
    const image = h.panel.querySelector("#desktop-image");
    image.fire("pointerdown", {button: 0, pointerId: 1, clientX: 20, clientY: 20});
    const zoom = h.panel.querySelector("#desktop-zoom");
    zoom.value = "200";
    zoom.fire("input");
    image.fire("pointerup", {button: 0, pointerId: 1, clientX: 40, clientY: 40});
    h.tick();
    await flush();
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 0);
    assert.ok(h.calls.filter(call => call.url.includes("/observation?")).length > before);
    assert.equal(h.panel.querySelector("#desktop-zoom-value").textContent, "200%");
    h.panel.querySelector("#desktop-fit").fire("click");
    assert.equal(zoom.value, "100");
  } finally { h.close(); }
});

test("observation polling is single-flight; refresh and chat updates preserve the editor", async () => {
  let held = null;
  const h = harness("single-flight", ({url}) => {
    if (url.includes("/observation?")) return held ? held.promise : {observation: observation()};
    if (url.endsWith("/windows")) return {windows: []};
    return {status: status(session())};
  });
  try {
    h.bind();
    await flush();
    const editor = h.panel.querySelector("#desktop-text-input");
    editor.value = "unfinished draft";
    editor.focus();
    editor.fire("input");
    held = deferred();
    h.tick();
    await flush();
    const count = h.calls.filter(call => call.url.includes("/observation?")).length;
    h.panel.querySelector("#desktop-refresh").fire("click");
    h.document.fire("visibilitychange");
    assert.equal(desktop.refreshDesktopPanel(detail("single-flight"), h.notify), false);
    await flush();
    assert.equal(h.calls.filter(call => call.url.includes("/observation?")).length, count);
    held.resolve({observation: observation({id: "next"})});
    await flush();
    assert.equal(document.activeElement, editor);
    assert.equal(editor.value, "unfinished draft");
    assert.equal(editor.readOnly, false);
    h.document.hidden = true;
    h.tick();
    await flush();
    assert.equal(h.calls.filter(call => call.url.includes("/observation?")).length, count);
    desktop.stopDesktopPanel();
    assert.equal(h.timers.size, 0);
  } finally { h.close(); }
});

test("late task responses are aborted and cannot overwrite another task", async () => {
  const late = deferred();
  const h = harness("old-task", ({url}) => {
    if (url.includes("/old-task/")) return late.promise;
    return {status: {supported: false, reason: "new-task status", session: null}};
  });
  try {
    h.bind();
    await flush();
    h.changeTask("new-task");
    await flush();
    assert.equal(h.calls[0].options.signal.aborted, true);
    late.resolve({status: status(session())});
    await flush();
    assert.equal(h.panel.querySelector("#desktop-status").textContent, "new-task status");
    assert.equal(h.calls.length, 2);
  } finally { h.close(); }
});

// Element-shaped actions were the background adapter's interface. Control now
// goes through the trusted surface, so no element action may be dispatched at
// all, however the picture is clicked.
test("no element action can be dispatched from the panel", async () => {
  const h = harness("no-element-actions", ({url}) => {
    if (url.includes("/observation?")) return {observation: observation({elements: [element]})};
    if (url.endsWith("/windows")) return {windows: []};
    return {status: status(session())};
  });
  try {
    h.bind();
    await flush();
    assert.match(h.panel.querySelector("#desktop-elements").innerHTML, /&lt;untrusted&gt;/);
    for (const selector of ["[data-desktop-action]", "#desktop-value-form", "#desktop-actions"]) {
      assert.equal(h.panel.querySelectorAll(selector).length, 0, `${selector} still exposes element control`);
    }
    h.panel.querySelector("#desktop-image").fire("click", {clientX: 40, clientY: 40});
    await flush();
    const actions = h.calls.filter(call => call.url.endsWith("/actions") && call.payload);
    assert.deepEqual(actions, [], "a picture click dispatched an element action");
  } finally { h.close(); }
});

test("stale observations, agent control and expired grants cannot invoke owner actions", async () => {
  for (const [id, shared, observed] of [
    ["stale", session(), observation({revision: 99})],
    ["agent", session({controller: "agent", claimed: true}), observation()],
    ["expired", session({expires_at: "2000-01-01T00:00:00Z"}), observation()],
  ]) {
    const h = harness(id, ({url}) => {
      if (url.includes("/observation?")) return {observation: observed};
      if (url.endsWith("/windows")) return {windows: []};
      return {status: status(shared)};
    });
    try {
      h.bind();
      await flush();
      h.panel.querySelector("#desktop-elements").fire("click", {target: new Node({desktopElement: element.id})});
      h.panel.querySelector("#desktop-actions").fire("click", {target: new Node({desktopAction: "invoke"})});
      await flush();
      assert.equal(h.calls.filter(call => call.payload).length, 0, id);
      if (id === "expired") assert.match(h.panel.querySelector("#desktop-status").textContent, /过期/);
    } finally { h.close(); }
  }
});

test("stop interrupts an in-flight operation without waiting for its response", async () => {
  const held = deferred();
  let stopped = false;
  const h = harness("urgent-stop", ({url}) => {
    if (url.endsWith("/actions")) return held.promise;
    if (url.endsWith("/stop")) stopped = true;
    if (url.includes("/observation?")) return {observation: observation()};
    if (url.endsWith("/windows")) return {windows: []};
    return {status: status(stopped ? null : session())};
  });
  try {
    h.bind();
    await flush();
    h.panel.querySelector("#desktop-text-input").value = "queued";
    h.panel.querySelector("#desktop-text-input").fire("input");
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
    h.panel.querySelector("#desktop-stop").fire("click");
    await flush();
    // Stop revokes and closes the session; the pending text write cannot land.
    assert.equal(h.panel.querySelector("#desktop-status").textContent, "共享已停止");
    held.resolve({status: status(session())});
    await flush();
    assert.equal(h.panel.querySelector("#desktop-status").textContent, "共享已停止");
  } finally { h.close(); }
});

test("a control-conflict 403 refreshes authority without disabling revocation", async () => {
  let agentControl = false;
  const h = harness("control-conflict", ({url}) => {
    if (url.endsWith("/actions")) {
      agentControl = true;
      throw Object.assign(new Error("desktop_agent_control"), {status: 403, code: "desktop_agent_control"});
    }
    if (url.includes("/observation?")) return {observation: observation({revision: agentControl ? 2 : 1})};
    if (url.endsWith("/windows")) return {windows: []};
    return {status: status(session(agentControl ? {controller: "agent", revision: 2} : {}))};
  });
  try {
    h.bind();
    await flush();
    h.panel.querySelector("#desktop-text-input").value = "queued";
    h.panel.querySelector("#desktop-text-input").fire("input");
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    // A conflict means another participant took over: the panel must refresh
    // the authority it shows while leaving revocation reachable.
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
    await flush();
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
  } finally { h.close(); }
});

test("capture failure preserves accessible elements, while a rejected observation clears actions", async () => {
  let failed = false;
  const h = harness("capture-error", ({url}) => {
    if (url.includes("/observation?")) {
      if (failed) throw new Error("desktop_timeout");
      return {observation: observation({capture_error: "Window capture unavailable"})};
    }
    if (url.endsWith("/windows")) return {windows: []};
    return {status: status(session())};
  });
  try {
    h.bind();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-capture-status").textContent, "Window capture unavailable");
    assert.equal(h.panel.querySelector("#desktop-preview").hidden, true);
    assert.match(h.panel.querySelector("#desktop-elements").innerHTML, /edit-1/);
    failed = true;
    h.tick();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-element-count").textContent, "0");
    assert.equal(h.panel.querySelector("#desktop-error").textContent, "desktop_timeout");
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
  } finally { h.close(); }
});

test("shared control has no global browser handlers or clipboard/storage side effects", async () => {
  assert.doesNotMatch(source, /localStorage|sessionStorage|clipboard|dispatchEvent|console\./);
  const main = await readFile(resolve(root, "src/main.ts"), "utf8");
  assert.match(main, /state\.taskTool === "browser"\) \{\s*refreshDesktopPanel/);
  assert.match(main, /state\.taskTool === "browser"\) stopDesktopPanel/);
  assert.match(source, /credentials|api\.request/);
});

test("quarantined target remains observable but advertises no actions", async () => {
  const h = harness("quarantined", ({url}) => {
    if (url.includes("/observation?")) return {observation: observation({
      control_error: "desktop_focus_side_effect", elements: [{...element, actions: []}],
    })};
    if (url.endsWith("/windows")) return {windows: []};
    return {status: status(session())};
  });
  try {
    h.bind();
    await flush();
    assert.match(h.panel.querySelector("#desktop-error").textContent, /输入焦点变化/);
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
    h.panel.querySelector("#desktop-elements").fire("click", {target: new Node({desktopElement: element.id})});
    assert.doesNotMatch(h.panel.querySelector("#desktop-actions").innerHTML, /data-desktop-action/);
  } finally { h.close(); }
});

const foregroundStatus = value => ({supported: true, foreground_supported: true, shared_control_supported: true, session: value});
const foregroundObservation = (overrides = {}) => observation({
  elements: [], surface: "geometry-1", image: "aGVsbG8=",
  input_actions: ["click", "double_click", "drag", "scroll", "text", "key", "focus"], ...overrides,
});
const foregroundSession = (overrides = {}) => session({mode: "foreground", ...overrides});

test("new desktop is default and foreground creation requires explicit consent", async () => {
  const h = harness("foreground-create", ({url}) => url.endsWith("/windows") ? {windows: []} : {status: foregroundStatus(null)});
  try {
    h.bind();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-window").value, "");
    let prompt = "";
    window.confirm = message => { prompt = message; return false; };
    h.panel.querySelector("#desktop-new-desktop").fire("click");
    assert.match(prompt, /宿主鼠标/);
    assert.match(prompt, /新建并切换 Windows 桌面/);
    assert.equal(h.calls.filter(call => call.payload).length, 0);
    window.confirm = () => true;
    h.panel.querySelector("#desktop-new-desktop").fire("click");
    await flush();
    assert.deepEqual(h.calls.find(call => call.url.endsWith("/session")).payload,
      {target: {kind: "new-desktop"}, mode: "foreground", confirm_foreground: true, confirm_shared: true});
  } finally { h.close(); }
});

test("window menu remains untouched across polls, explicit refresh and chat updates", async () => {
  let nextWindows = [sharedWindow];
  const h = harness("stable-chooser", ({url}) => url.endsWith("/windows")
    ? {windows: nextWindows} : {status: foregroundStatus(null)});
  try {
    h.bind();
    await flush();
    const chooser = h.panel.querySelector("#desktop-window");
    chooser.focus();
    chooser.fire("pointerdown");
    chooser.fire("focus");
    const writes = {innerHTML: 0, value: 0, disabled: 0};
    for (const property of Object.keys(writes)) {
      let value = chooser[property];
      Object.defineProperty(chooser, property, {
        configurable: true, get: () => value, set: next => { value = next; writes[property]++; },
      });
    }
    for (let n = 0; n < 3; n++) {
      h.tick();
      await flush();
      assert.equal(desktop.refreshDesktopPanel(detail("stable-chooser"), h.notify), false);
      assert.equal(desktop.desktopPanelInteracting(), true);
    }
    assert.deepEqual(writes, {innerHTML: 0, value: 0, disabled: 0});
    assert.equal(h.calls.filter(call => call.url.endsWith("/windows")).length, 1);
    nextWindows = [];
    h.panel.querySelector("#desktop-refresh").fire("click");
    await flush();
    assert.deepEqual(writes, {innerHTML: 0, value: 0, disabled: 0});
    chooser.value = JSON.stringify({kind: "window", id: sharedWindow.id});
    chooser.fire("change");
    assert.equal(writes.value, 1, "only the simulated user selection wrote value");
    h.document.activeElement = null;
    chooser.fire("blur");
    assert.equal(writes.innerHTML, 1, "deferred options apply only after interaction ends");
    h.tick();
    await flush();
    h.tick();
    await flush();
    assert.equal(h.calls.filter(call => call.url.endsWith("/windows")).length, 2, "empty lists do not auto-refresh");
  } finally { h.close(); }
});

test("valid foreground actions stay enabled during status refresh and do not depend on elements", async () => {
  let held = null;
  const h = harness("foreground-poll", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    if (!url.endsWith("/actions") && held) return held.promise;
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-focus").disabled, false);
    held = deferred();
    h.tick();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-focus").disabled, false);
    h.panel.querySelector("#desktop-focus").fire("click");
    await flush();
    assert.equal(h.calls.find(call => call.url.endsWith("/actions")).payload.kind, "focus");
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
    held.resolve({status: foregroundStatus(foregroundSession())});
    await flush();
  } finally { h.close(); }
});

test("foreground gestures map image pixels and serialize without replaying busy input", async () => {
  let held = null;
  const h = harness("foreground-input", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    if (url.endsWith("/actions") && held) return held.promise;
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const image = h.panel.querySelector("#desktop-image");
    const pointer = (type, x, y) => image.fire(type, {clientX: x, clientY: y, button: 0, pointerId: 1});
    pointer("pointerdown", 20, 30);
    pointer("pointerup", 20, 30);
    pointer("pointerdown", 20, 30);
    pointer("pointerup", 20, 30);
    await flush();
    const writes = () => h.calls.filter(call => call.url.endsWith("/actions"));
    assert.equal(writes().length, 1);
    assert.deepEqual(actionPayload(writes()[0]), {
      session_id: "session-1", revision: 1, observation_id: "observation-1",
      element_id: "$surface", kind: "double_click", x: 10, y: 15, button: "left",
    });
    pointer("pointerdown", 20, 30);
    pointer("pointerup", 90, 100);
    await flush();
    assert.equal(writes().at(-1).payload.kind, "drag");
    assert.equal(writes().at(-1).payload.end_x, 45);
    image.focus();
    image.fire("wheel", {clientX: 50, clientY: 50, deltaX: -5000, deltaY: 5000, deltaMode: 0});
    await flush();
    assert.equal(writes().at(-1).payload.delta_y, 2400);
    assert.equal(writes().at(-1).payload.delta_x, -2400);
    held = deferred();
    image.fire("keydown", {key: "L", ctrlKey: true});
    await flush();
    const count = writes().length;
    image.fire("keydown", {key: "a"});
    image.fire("keydown", {key: "b"});
    pointer("pointerdown", 20, 30);
    pointer("pointerup", 90, 100);
    assert.equal(writes().length, count);
    held.resolve({status: foregroundStatus(foregroundSession())});
    held = null;
    await flush();
    h.tick();
    await flush();
    assert.equal(writes().length, count);
  } finally { h.close(); }
});

test("keyboard capture is focused-surface-only and explicit Unicode text form preserves drafts", async () => {
  const h = harness("keyboard-scope", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const image = h.panel.querySelector("#desktop-image");
    let prevented = false;
    image.fire("keydown", {key: "a", preventDefault() { prevented = true; }});
    assert.equal(prevented, false);
    image.focus();
    image.fire("keydown", {key: "Delete", ctrlKey: true, altKey: true});
    image.fire("keydown", {key: "F4", ctrlKey: true, metaKey: true});
    image.fire("keydown", {key: "l", metaKey: true});
    image.fire("keydown", {key: "a", repeat: true});
    assert.equal(h.calls.filter(call => call.payload).length, 0);
    image.fire("keydown", {key: "ArrowLeft"});
    await flush();
    assert.deepEqual(h.calls.filter(call => call.payload).at(-1).payload.keys, ["LEFT"]);
    const text = h.panel.querySelector("#desktop-text-input");
    text.focus();
    text.value = "中文 😀";
    text.fire("input");
    h.tick();
    await flush();
    assert.equal(text.value, "中文 😀");
    assert.equal(document.activeElement, text);
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.equal(h.calls.filter(call => call.payload).at(-1).payload.value, "中文 😀");
    assert.equal(text.value, "");
  } finally { h.close(); }
});

test("capture failures still expose advertised focus, unsupported inputs never send", async () => {
  const h = harness("foreground-minimized", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation({
      image: "", width: 0, height: 0, capture_error: "minimized", input_actions: ["focus"],
    })};
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-focus").hidden, false);
    assert.equal(h.panel.querySelector("#desktop-focus").disabled, false);
    assert.equal(h.panel.querySelector("#desktop-text-form").hidden, true);
    h.panel.keys[0].fire("click");
    assert.equal(h.calls.filter(call => call.payload).length, 0);
    h.panel.querySelector("#desktop-focus").fire("click");
    await flush();
    assert.equal(h.calls.filter(call => call.payload).at(-1).payload.kind, "focus");
  } finally { h.close(); }
});

test("a gesture freezes its observed image despite an in-flight refresh", async () => {
  let held = null;
  const h = harness("gesture-refresh", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return held ? held.promise : {observation: foregroundObservation()};
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    held = deferred();
    h.tick();
    await flush();
    const refreshing = h.calls.filter(call => call.url.includes("/observation?")).at(-1);
    const image = h.panel.querySelector("#desktop-image");
    image.fire("pointerdown", {clientX: 40, clientY: 40, button: 0, pointerId: 1});
    assert.equal(refreshing.options.signal.aborted, true);
    held.resolve({observation: foregroundObservation({id: "late-refresh"})});
    held = null;
    await flush();
    image.fire("pointerup", {clientX: 40, clientY: 40, button: 0, pointerId: 1});
    h.tick();
    await flush();
    const action = h.calls.find(call => call.url.endsWith("/actions"));
    assert.equal(action.payload.kind, "click");
    assert.equal(action.payload.observation_id, "observation-1");
  } finally { h.close(); }
});

test("rapid printable typing moves to a draft instead of being lost during a native action", async () => {
  let held = null;
  const h = harness("rapid-typing", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    if (url.endsWith("/actions") && held) return held.promise;
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const image = h.panel.querySelector("#desktop-image");
    image.focus();
    held = deferred();
    image.fire("keydown", {key: "l", ctrlKey: true});
    await flush();
    image.fire("keydown", {key: "g"});
    const editor = h.panel.querySelector("#desktop-text-input");
    assert.equal(document.activeElement, editor);
    assert.equal(editor.value, "g");
    editor.value = "github.com";
    editor.fire("input");
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 1);
    held.resolve({status: foregroundStatus(foregroundSession())});
    held = null;
    await flush();
    h.tick();
    await flush();
    assert.equal(editor.value, "github.com");
    assert.equal(document.activeElement, editor);
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 1);
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).at(-1).payload.value, "github.com");
  } finally { h.close(); }
});

test("text edited while an earlier submission runs is neither dropped nor duplicated", async () => {
  const held = deferred();
  let hold = true;
  const h = harness("text-submit-race", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    if (url.endsWith("/actions") && hold) return held.promise;
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const editor = h.panel.querySelector("#desktop-text-input");
    editor.value = "first";
    editor.fire("input");
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.equal(editor.value, "");
    editor.value = "next";
    editor.fire("input");
    held.resolve({status: foregroundStatus(foregroundSession())});
    hold = false;
    await flush();
    assert.equal(editor.value, "next");
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.deepEqual(h.calls.filter(call => call.url.endsWith("/actions")).map(call => call.payload.value), ["first", "next"]);
  } finally { h.close(); }
});

test("failed text is returned to the editor and oversized text never reaches the host", async () => {
  let revision = 1;
  const h = harness("text-failure", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation({revision})};
    if (url.endsWith("/actions")) {
      revision++;
      throw Object.assign(new Error("failed"), {code: "desktop_input_partial"});
    }
    return {status: foregroundStatus(foregroundSession({revision}))};
  });
  try {
    h.bind();
    await flush();
    const editor = h.panel.querySelector("#desktop-text-input");
    editor.value = "中".repeat(2000);
    editor.fire("input");
    assert.equal(h.panel.querySelector("#desktop-text-form button").disabled, true);
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 0);
    editor.value = "recoverable";
    editor.fire("input");
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    assert.equal(editor.value, "recoverable");
    assert.match(h.panel.querySelector("#desktop-error").textContent, /部分输入可能已经生效/);
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 1);
    h.tick();
    await flush();
    assert.equal(editor.value, "recoverable", "same-owner recovery must retain unsent/recoverable text");
    assert.match(h.panel.querySelector("#desktop-error").textContent, /部分输入可能已经生效/,
      "refresh must not erase the uncertain-action warning");
  } finally { h.close(); }
});

function framePacket(overrides = {}, bytes = new Uint8Array([255, 216, 255, 217])) {
  const metadata = {type: "frame", capture_ms: 40, interval_ms: 100, observation: foregroundObservation({
    image: "", mime: "image/jpeg", width: 2560, height: 1440,
    preview_width: 320, preview_height: 180, ...overrides,
  })};
  const json = new TextEncoder().encode(JSON.stringify(metadata));
  const buffer = new ArrayBuffer(4 + json.length + bytes.length);
  new DataView(buffer).setUint32(0, json.length, false);
  new Uint8Array(buffer, 4, json.length).set(json);
  new Uint8Array(buffer, 4 + json.length).set(bytes);
  return buffer;
}

test("binary frame parser bounds metadata, images, mime and logical/preview sizes", () => {
  const frame = desktop.parseDesktopFrame(framePacket());
  assert.equal(frame.observation.width, 2560);
  assert.equal(frame.observation.preview_width, 320);
  assert.equal(frame.bytes.length, 4);
  assert.throws(() => desktop.parseDesktopFrame(new ArrayBuffer(3)));
  const badLength = framePacket();
  new DataView(badLength).setUint32(0, 65537);
  assert.throws(() => desktop.parseDesktopFrame(badLength));
  assert.throws(() => desktop.parseDesktopFrame(framePacket({mime: "text/html"})));
  assert.throws(() => desktop.parseDesktopFrame(framePacket({mime: "image/svg+xml"})));
  assert.throws(() => desktop.parseDesktopFrame(framePacket({preview_width: 3000})));
  assert.throws(() => desktop.parseDesktopFrame(framePacket({width: -1})));
  assert.throws(() => desktop.parseDesktopFrame(framePacket({image: "base64-not-allowed"})));
  assert.throws(() => desktop.parseDesktopFrame(framePacket({}, new Uint8Array(8 * 1024 * 1024 + 1))));
});

test("adaptive video uses bounded downgrade/recovery hysteresis and explicit modes", () => {
  let adaptive = {level: 1, slow: 0, fast: 0};
  adaptive = desktop.adaptDesktopVideo(adaptive, 900, 400000, 80);
  assert.equal(adaptive.level, 1);
  adaptive = desktop.adaptDesktopVideo(adaptive, 900, 400000, 80);
  assert.equal(adaptive.level, 2);
  for (let i = 0; i < 7; i++) adaptive = desktop.adaptDesktopVideo(adaptive, 100, 20000, 5);
  assert.equal(adaptive.level, 2);
  adaptive = desktop.adaptDesktopVideo(adaptive, 100, 20000, 5);
  assert.equal(adaptive.level, 1);
  for (let i = 0; i < 30; i++) adaptive = desktop.adaptDesktopVideo(adaptive, 2000, 500000, 100);
  assert.equal(adaptive.level, 4);
  assert.deepEqual(desktop.desktopFrameOptions("auto", adaptive.level),
    {max_width: 320, max_height: 180, quality: 35, interval_ms: 500});
  assert.equal(desktop.desktopFrameOptions("quality", 4).max_width, 1920);
  assert.equal(desktop.desktopFrameOptions("bandwidth", 0).max_width, 640);
});

test("HTTP previews carry view and compressed options; independent actions reuse displayed frames with unique IDs", async () => {
  const h = harness("view-ids", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const query = new URL(h.calls.find(call => call.url.includes("/observation?")).url, "http://localhost").searchParams;
    assert.match(query.get("view_id"), /^[a-f0-9]{32}$/);
    assert.equal(query.get("max_width"), "1280");
    assert.equal(query.get("max_height"), "720");
    assert.equal(query.get("quality"), "65");
    h.panel.querySelector("#desktop-focus").fire("click");
    await flush();
    h.panel.querySelector("#desktop-focus").fire("click");
    await flush();
    const writes = h.calls.filter(call => call.url.endsWith("/actions"));
    assert.equal(writes.length, 2);
    assert.equal(writes[0].payload.view_id, query.get("view_id"));
    assert.equal(writes[1].payload.observation_id, writes[0].payload.observation_id);
    assert.notEqual(writes[1].payload.action_id, writes[0].payload.action_id);
    h.changeTask("different-view");
    await flush();
    const next = new URL(h.calls.filter(call => call.url.includes("/observation?")).at(-1).url, "http://localhost").searchParams;
    assert.notEqual(next.get("view_id"), query.get("view_id"));
  } finally { h.close(); }
});

function socketHarness(id) {
  const originals = {WebSocket: globalThis.WebSocket, Image: globalThis.Image, location: globalThis.location};
  const sockets = [];
  let decode = () => Promise.resolve();
  class Socket {
    static OPEN = 1;
    readyState = 0;
    sent = [];
    constructor(url) { this.url = String(url); sockets.push(this); }
    send(data) { this.sent.push(JSON.parse(data)); }
    open() { this.readyState = 1; this.onopen?.(); }
    message(data) { this.onmessage?.({data}); }
    close() { this.readyState = 3; this.onclose?.(); }
  }
  globalThis.WebSocket = Socket;
  globalThis.location = {href: "https://aha.example.test/"};
  globalThis.Image = class extends Node {
    naturalWidth = 320;
    naturalHeight = 180;
    decode() { return decode(); }
  };
  const currentStatus = {...foregroundStatus(foregroundSession()), stream_supported: true};
  const h = harness(id, ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation({id: "http-preview"})};
    return {status: currentStatus};
  });
  return {
    h, sockets, currentStatus,
    setDecode(callback) { decode = callback; },
    ready() {
      const socket = sockets.at(-1);
      socket.open();
      socket.message(JSON.stringify({type: "ready", protocol: 1, status: currentStatus}));
      return socket;
    },
    close() {
      h.close();
      for (const [key, value] of Object.entries(originals)) {
        if (value === undefined) delete globalThis[key]; else globalThis[key] = value;
      }
    },
  };
}

test("socket start carries CSRF only in text; frame ACK follows decode and gestures freeze logical coordinates", async () => {
  const fixture = socketHarness("socket-frame");
  const {h} = fixture;
  api.setCSRF("test-only-csrf");
  try {
    h.bind();
    await flush();
    const socket = fixture.ready();
    assert.match(socket.url, /^wss:/);
    assert.doesNotMatch(socket.url, /test-only-csrf|csrf_token/);
    assert.equal(socket.sent[0].type, "start");
    assert.equal(socket.sent[0].csrf_token, "test-only-csrf");
    assert.match(socket.sent[0].view_id, /^[a-f0-9]{32}$/);
    const decoding = deferred();
    fixture.setDecode(() => decoding.promise);
    socket.message(framePacket());
    await flush();
    assert.equal(socket.sent.filter(message => message.type === "ack").length, 0);
    decoding.resolve();
    await flush();
    assert.equal(socket.sent.at(-1).type, "ack");
    const image = h.panel.querySelector("#desktop-image");
    assert.match(image.src, /^blob:/);
    const firstSrc = image.src;
    image.fire("pointerdown", {clientX: 25, clientY: 50, pointerId: 1, button: 0});
    socket.message(framePacket({id: "newer-but-frozen"}));
    await flush();
    assert.equal(image.src, firstSrc);
    assert.equal(socket.sent.at(-1).frame_id, "newer-but-frozen");
    image.fire("pointerup", {clientX: 75, clientY: 100, pointerId: 1, button: 0});
    await flush();
    const action = h.calls.find(call => call.url.endsWith("/actions")).payload;
    assert.equal(action.observation_id, "observation-1");
    assert.equal(action.x, 320);
    assert.equal(action.y, 360);
    assert.equal(action.end_x, 960);
    assert.equal(action.end_y, 720);
    assert.equal(action.view_id, socket.sent[0].view_id);
  } finally { api.setCSRF(); fixture.close(); }
});

test("malformed stream downgrades to compressed HTTP, hidden/unmount releases URLs and stops reconnect", async () => {
  const fixture = socketHarness("socket-cleanup");
  const {h} = fixture;
  const revoke = URL.revokeObjectURL;
  const revoked = [];
  URL.revokeObjectURL = url => { revoked.push(url); revoke(url); };
  try {
    h.bind();
    await flush();
    const socket = fixture.ready();
    socket.message(framePacket());
    await flush();
    const blob = h.panel.querySelector("#desktop-image").src;
    socket.message(new ArrayBuffer(2));
    await flush();
    assert.equal(socket.readyState, 3);
    assert.ok(revoked.includes(blob));
    assert.ok(h.calls.some(call => call.url.includes("/observation?") && call.url.includes("max_width=")));
    assert.match(h.panel.querySelector("#desktop-video-metrics").textContent, /HTTP/);
    h.document.hidden = true;
    h.document.fire("visibilitychange");
    assert.equal(h.timers.size, 0);
    assert.equal(h.panel.querySelector("#desktop-preview").hidden, true);
    desktop.stopDesktopPanel();
    assert.equal(h.timers.size, 0);
  } finally { fixture.close(); URL.revokeObjectURL = revoke; }
});

test("expired or unauthorized streams cannot reconnect or retain valid input snapshots", async () => {
  const fixture = socketHarness("socket-denied");
  const {h} = fixture;
  try {
    h.bind();
    await flush();
    const socket = fixture.ready();
    socket.message(framePacket());
    await flush();
    socket.message(JSON.stringify({type: "error", error: "desktop_csrf_invalid"}));
    await flush();
    assert.equal(socket.readyState, 3);
    assert.equal(h.panel.querySelector("#desktop-preview").hidden, true);
    h.panel.querySelector("#desktop-focus").fire("click");
    assert.equal(h.calls.filter(call => call.payload).length, 0);
    h.tick();
    await flush();
    assert.equal(fixture.sockets.length, 1);
  } finally { fixture.close(); }
});

test("hidden page cancels pending decode, releases every Blob once and rechecks status before reconnecting", async () => {
  const fixture = socketHarness("socket-hidden");
  const {h} = fixture;
  const create = URL.createObjectURL;
  const revoke = URL.revokeObjectURL;
  const created = [];
  const revoked = [];
  URL.createObjectURL = value => { const url = create(value); created.push(url); return url; };
  URL.revokeObjectURL = url => { revoked.push(url); revoke(url); };
  try {
    h.bind();
    await flush();
    const socket = fixture.ready();
    const decoding = deferred();
    fixture.setDecode(() => decoding.promise);
    socket.message(new Blob([framePacket()]));
    await flush();
    assert.equal(created.length, 1);
    h.document.hidden = true;
    h.document.fire("visibilitychange");
    assert.equal(socket.readyState, 3);
    assert.equal(h.timers.size, 0);
    decoding.resolve();
    await flush();
    assert.deepEqual(revoked, created);
    const before = h.calls.length;
    fixture.currentStatus.session = null;
    h.document.hidden = false;
    h.document.fire("visibilitychange");
    await flush();
    assert.ok(h.calls.length > before);
    assert.equal(fixture.sockets.length, 1, "revoked session must not reconnect");
  } finally {
    fixture.close();
    URL.createObjectURL = create;
    URL.revokeObjectURL = revoke;
  }
});

test("hiding during text input restores uncertain text without replaying it on return", async () => {
  const pending = deferred();
  const h = harness("hidden-text", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    if (url.endsWith("/actions")) return pending.promise;
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const editor = h.panel.querySelector("#desktop-text-input");
    editor.value = "pending";
    editor.fire("input");
    h.panel.querySelector("#desktop-text-form").fire("submit");
    await flush();
    editor.value = " next";
    editor.fire("input");
    h.document.hidden = true;
    h.document.fire("visibilitychange");
    pending.resolve({status: foregroundStatus(foregroundSession())});
    await flush();
    h.document.hidden = false;
    h.document.fire("visibilitychange");
    await flush();
    assert.equal(editor.value, "pending next");
    assert.match(h.panel.querySelector("#desktop-error").textContent, /结果未确认/);
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 1);
  } finally { h.close(); }
});

const sharedTargets = {
  windows: [sharedWindow, {...sharedWindow, id: "other-window", title: "Other"}],
  desktops: [{id: "desktop-one", name: "Desktop 1", current: true}, {id: "desktop-two", name: "Desktop 2", current: false}],
  monitors: [{id: "monitor-left", name: "Left", x: -1920, y: 0, width: 1920, height: 1080, primary: false},
    {id: "monitor-main", name: "Main", x: 0, y: 0, width: 2560, height: 1440, primary: true}],
  desktop_switch_supported: true,
};

test("real task tool renderer contains one header and two persistent control rows", async () => {
  const {renderTaskToolPanel} = await import(pathToFileURL(resolve(root, "dist/task_tools.js")));
  const html = renderTaskToolPanel("browser", detail("real-header"), "main", "", "split");
  assert.equal((html.match(/class="task-tool-panel-head desktop-task-header"/g) || []).length, 1);
  assert.equal((html.match(/<h3>共享控制<\/h3>/g) || []).length, 1);
  assert.equal((html.match(/id="task-tool-panel-body"/g) || []).length, 1);
  assert.equal((html.match(/id="close-task-tool"/g) || []).length, 1);
  assert.doesNotMatch(html, /class="desktop-session"|class="desktop-tools"|class="desktop-video-toolbar"/);
  assert.equal((html.match(/class="desktop-picture-bar"/g) || []).length, 1);
  assert.equal((html.match(/class="desktop-input-bar"/g) || []).length, 1);
  assert.ok(html.indexOf('id="desktop-picture-controls"') < html.indexOf('id="task-tool-panel-body"'));
  assert.ok(html.indexOf('id="desktop-input"') > html.indexOf('id="desktop-viewport"'));
  assert.ok(html.indexOf('id="desktop-text-input"') < html.indexOf('id="desktop-operations-popover"'));
  assert.match(html, /desktop-popover" role="dialog"/);
});

test("target metadata groups and explicit monitor selection produce direct owner-authorized switch", async () => {
  let currentSession = foregroundSession({claimed: true});
  const h = harness("target-switch", ({url, payload}) => {
    if (url.endsWith("/targets")) return {targets: sharedTargets};
    if (url.endsWith("/switch")) currentSession = foregroundSession({id: "new-session", window: {
      id: "opaque-desktop-monitor-target", title: "Desktop 2", process: "Windows", desktop_id: payload.target.desktop_id, monitor_id: payload.target.monitor_id,
    }});
    if (url.includes("/observation?")) return {observation: foregroundObservation({session_id: currentSession.id})};
    return {status: {...foregroundStatus(currentSession), targets_supported: true}};
  });
  try {
    h.bind();
    await flush();
    assert.ok(h.calls.some(call => call.url.endsWith("/targets")));
    assert.ok(!h.calls.some(call => call.url.endsWith("/windows")));
    const chooser = h.panel.querySelector("#desktop-window");
    assert.match(chooser.innerHTML, /optgroup label="已有桌面"/);
    assert.match(chooser.innerHTML, /optgroup label="窗口"/);
    assert.doesNotMatch(chooser.innerHTML, /新建桌面/);
    assert.equal(chooser.disabled, false);
    const monitor = h.panel.querySelector("#desktop-monitor");
    assert.equal(monitor.value, "monitor-main");
    chooser.value = JSON.stringify({kind: "desktop", id: "desktop-two"});
    chooser.fire("change");
    monitor.value = "monitor-left";
    monitor.fire("change");
    window.confirm = () => false;
    h.panel.querySelector("#desktop-share").fire("click");
    assert.equal(h.calls.filter(call => call.payload).length, 0);
    window.confirm = () => true;
    h.panel.querySelector("#desktop-share").fire("click");
    await flush();
    const writes = h.calls.filter(call => call.payload);
    assert.equal(writes.length, 1);
    assert.ok(writes[0].url.endsWith("/switch"));
    assert.deepEqual(writes[0].payload, {session_id: "session-1", revision: 1,
      target: {kind: "desktop", desktop_id: "desktop-two", monitor_id: "monitor-left"},
      mode: "foreground", confirm_foreground: true, confirm_shared: true});
    assert.equal(currentSession.controller, "shared");
    assert.equal(currentSession.claimed, false);
    assert.match(h.panel.querySelector("#desktop-status").textContent, /共同控制/);
  } finally { h.close(); }
});

test("stop cancels pending target switch and a late success cannot re-enable old or new input", async () => {
  const switching = deferred();
  let stopped = false;
  const h = harness("switch-stop", ({url}) => {
    if (url.endsWith("/targets")) return {targets: sharedTargets};
    if (url.endsWith("/switch")) return switching.promise;
    if (url.endsWith("/stop")) stopped = true;
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    return {status: {...foregroundStatus(stopped ? null : foregroundSession()), targets_supported: true}};
  });
  try {
    h.bind();
    await flush();
    const chooser = h.panel.querySelector("#desktop-window");
    chooser.value = JSON.stringify({kind: "window", id: "other-window"});
    chooser.fire("change");
    const image = h.panel.querySelector("#desktop-image");
    image.fire("pointerdown", {clientX: 40, clientY: 40, pointerId: 1, button: 0});
    h.panel.querySelector("#desktop-share").fire("click");
    await flush();
    assert.equal(h.panel.querySelector("#desktop-preview").hidden, true);
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
    image.fire("pointerup", {clientX: 80, clientY: 80, pointerId: 1, button: 0});
    h.panel.querySelector("#desktop-stop").fire("click");
    await flush();
    assert.equal(h.calls.find(call => call.url.endsWith("/switch")).options.signal.aborted, true);
    switching.resolve({status: {...foregroundStatus(foregroundSession({id: "late-new-session"})), targets_supported: true}});
    await flush();
    assert.match(h.panel.querySelector("#desktop-status").textContent, /已停止/);
    assert.ok(!h.calls.some(call => call.url.endsWith("/actions")));
  } finally { h.close(); }
});

test("failed selection refreshes revoked status, and unsupported desktop switching fails closed", async () => {
  let revoked = false;
  const h = harness("switch-failure", ({url}) => {
    if (url.endsWith("/targets")) return {targets: {...sharedTargets, desktop_switch_supported: false, desktop_reason: "Unavailable"}};
    if (url.endsWith("/switch")) { revoked = true; throw new Error("Selection failed"); }
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    return {status: {...foregroundStatus(revoked ? null : foregroundSession()), targets_supported: true}};
  });
  try {
    h.bind();
    await flush();
    h.panel.querySelector("#desktop-new-desktop").fire("click");
    assert.ok(!h.calls.some(call => call.payload));
    const chooser = h.panel.querySelector("#desktop-window");
    chooser.value = JSON.stringify({kind: "desktop", id: "desktop-two"});
    chooser.fire("change");
    h.panel.querySelector("#desktop-share").fire("click");
    assert.ok(!h.calls.some(call => call.payload));
    chooser.value = JSON.stringify({kind: "window", id: "other-window"});
    chooser.fire("change");
    h.panel.querySelector("#desktop-share").fire("click");
    await flush();
    assert.equal(h.panel.querySelector("#desktop-stop").disabled, true);
    assert.equal(h.panel.querySelector("#desktop-error").textContent, "Selection failed");
    assert.ok(h.calls.at(-1).url.endsWith("/desktop") || h.calls.at(-1).url.endsWith("/targets"));
  } finally { h.close(); }
});

test("desktop listeners are removed on stop and rebind without removing unrelated listeners", async () => {
  let currentSession = foregroundSession();
  const h = harness("listener-lifetime", ({url, payload}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation({revision: currentSession.revision})};
    return {status: foregroundStatus(currentSession)};
  });
  try {
    h.bind();
    await flush();
    let externalClicks = 0;
    const focus = h.panel.querySelector("#desktop-focus");
    const external = () => { externalClicks++; };
    focus.addEventListener("click", external);
    const stale = [...focus.listeners.get("click")].filter(callback => callback !== external);
    for (let cycle = 0; cycle < 3; cycle++) {
      desktop.stopDesktopPanel();
      assert.equal(focus.listeners.get("click").size, 1, "only unrelated listener survives");
      for (const callback of stale) callback({target: focus});
      focus.fire("click");
      assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, cycle);
      h.bind();
      await flush();
      assert.equal(focus.listeners.get("click").size, 2, "one desktop and one unrelated listener");
      focus.fire("click");
      await flush();
    }
    assert.equal(externalClicks, 6);
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 3);
  } finally { h.close(); }
});

test("native target errors and desktop_reason use exact codes without adding prefixes", async () => {
  const cases = [
    ["desktop_monitor_changed", /显示器.*变化/],
    ["desktop_monitor_required", /请选择.*显示器/],
    ["desktop_monitor_unavailable", /显示器不可用/],
    ["desktop_foreground_outside_monitor", /不在共享显示器/],
    ["desktop_enumeration_unavailable", /无法读取虚拟桌面列表/],
    ["desktop_state_inconsistent", /桌面状态不一致/],
    ["desktop_switch_unverified", /未能确认桌面切换/],
    ["desktop_switch_limit", /安全步数限制/],
  ];
  for (const [code, message] of cases) {
    const h = harness(`error-${code}`, ({url}) => {
      if (url.endsWith("/targets")) return {targets: {...sharedTargets, desktop_reason: code}};
      if (url.includes("/observation?")) return {observation: foregroundObservation()};
      if (url.endsWith("/actions")) throw Object.assign(new Error(code), {code});
      return {status: {...foregroundStatus(foregroundSession()), targets_supported: true}};
    });
    try {
      h.bind();
      await flush();
      assert.match(h.panel.querySelector("#desktop-target-reason").textContent, message);
      h.panel.querySelector("#desktop-focus").fire("click");
      await flush();
      assert.match(h.panel.querySelector("#desktop-error").textContent, message);
    } finally { h.close(); }
  }
});

test("generic key input rejects cross-desktop chords but allows Run", async () => {
  const h = harness("desktop-key-boundary", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: foregroundObservation()};
    return {status: foregroundStatus(foregroundSession())};
  });
  try {
    h.bind();
    await flush();
    const image = h.panel.querySelector("#desktop-image");
    image.focus();
    for (const key of ["d", "D", "ArrowLeft", "ArrowRight", "F4"]) {
      image.fire("keydown", {key, ctrlKey: true, metaKey: true});
    }
    await flush();
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 0);
    image.fire("keydown", {key: "r", metaKey: true});
    await flush();
    assert.deepEqual(h.calls.find(call => call.url.endsWith("/actions")).payload.keys, ["WIN", "R"]);
  } finally { h.close(); }
});

test("shared header has focus and Stop without handoff icons", async () => {
  const {renderTaskToolPanel} = await import(pathToFileURL(resolve(root, "dist/task_tools.js")));
  const html = renderTaskToolPanel("browser", detail("left-controls"), "main", "", "split");
  const header = html.slice(html.indexOf("<header"), html.indexOf("</header>"));
  for (const marker of ['id="desktop-stop"', 'id="desktop-focus"']) {
    assert.ok(header.includes(marker));
    assert.equal(html.split(marker).length - 1, 1);
  }
  assert.doesNotMatch(html, /data-desktop-controller/);
  assert.doesNotMatch(source, /"\/control"|data-desktop-controller/);
  assert.match(header, /desktop-header-controls" role="group" aria-label="共享控制操作"/);
  assert.match(header, /desktop-page-actions" role="group" aria-label="工具面板布局与关闭"/);
  assert.match(header, /id="desktop-focus" class="icon-button" title="恢复焦点" aria-label="恢复焦点"/);
  assert.doesNotMatch(header, /画质与缩放/);
  assert.doesNotMatch(html, /desktop-picture-popover/);
  assert.match(html, /id="desktop-picture-controls" class="desktop-picture-bar" role="group" aria-label="画质与缩放"/);
});

test("direct focus icon remains visible but unsupported or Agent-owned focus is disabled", async () => {
  for (const [id, shared, inputActions] of [
    ["focus-unshared", null, []],
    ["focus-background", session(), []],
    ["focus-unsupported", foregroundSession(), ["key"]],
    ["focus-agent-owned", foregroundSession({controller: "agent"}), ["focus"]],
    ["focus-owner", foregroundSession(), ["focus"]],
  ]) {
    const h = harness(id, ({url}) => {
      if (url.endsWith("/windows")) return {windows: []};
      if (url.includes("/observation?")) return {observation: foregroundObservation({input_actions: inputActions})};
      return {status: foregroundStatus(shared)};
    });
    try {
      h.bind();
      await flush();
      const focus = h.panel.querySelector("#desktop-focus");
      assert.equal(focus.hidden, false);
      assert.equal(focus.disabled, id !== "focus-owner");
      focus.fire("click");
      await flush();
      assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, id === "focus-owner" ? 1 : 0);
      assert.ok(h.panel.querySelector("#desktop-picture-controls").title.includes("自动"));
    } finally { h.close(); }
  }
});

test("cross-desktop windows show escaped desktop labels and require supported foreground consent", async () => {
  const remoteWindow = {...sharedWindow, id: "other-desktop-window", desktop_id: "desktop-two",
    desktop_name: '<Review & "notes">', other_desktop: true};
  for (const switchSupported of [true, false]) {
    const h = harness(`cross-desktop-${switchSupported}`, ({url}) => {
      if (url.endsWith("/targets")) return {targets: {...sharedTargets, windows: [sharedWindow, remoteWindow], desktop_switch_supported: switchSupported}};
      return {status: {...foregroundStatus(null), targets_supported: true}};
    });
    try {
      let confirmed = "";
      window.confirm = message => { confirmed = message; return false; };
      h.bind();
      await flush();
      const chooser = h.panel.querySelector("#desktop-window");
      assert.match(chooser.innerHTML, /&lt;Review &amp; &quot;notes&quot;&gt;（其他桌面）/);
      const remoteOption = chooser.innerHTML.match(/<option[^>]*other-desktop-window[^>]*>/)[0];
      // Crossing desktops is Owner-only and needs the desktop-switch grant.
      const allowed = switchSupported;
      assert.equal(remoteOption.includes("disabled"), !allowed);
      chooser.value = JSON.stringify({kind: "window", id: remoteWindow.id});
      chooser.fire("change");
      h.tick();
      await flush();
      assert.equal(h.calls.filter(call => call.payload).length, 0, "selection/polling cannot switch desktops");
      assert.equal(h.panel.querySelector("#desktop-share").disabled, !allowed);
      h.panel.querySelector("#desktop-share").fire("click");
      assert.equal(h.calls.filter(call => call.payload).length, 0);
      if (allowed) {
        assert.match(confirmed, /将切换到窗口所属桌面：<Review & "notes">/);
        window.confirm = () => true;
        h.panel.querySelector("#desktop-share").fire("click");
        await flush();
        assert.deepEqual(h.calls.find(call => call.url.endsWith("/session")).payload,
          {target: {kind: "window", window_id: remoteWindow.id}, mode: "foreground", confirm_foreground: true, confirm_shared: true});
      } else assert.equal(confirmed, "", "unavailable targets cannot request consent");
    } finally { h.close(); }
  }
});

test("legacy windows without other_desktop remain selectable and an unswitched desktop stays disabled", async () => {
  const h = harness("cross-desktop-mode-change", ({url}) => {
    if (url.endsWith("/targets")) return {targets: {...sharedTargets, windows: [sharedWindow,
      {...sharedWindow, id: "remote", desktop_id: "desktop-two", other_desktop: true}]}};
    return {status: {...foregroundStatus(null), targets_supported: true}};
  });
  try {
    h.bind();
    await flush();
    const chooser = h.panel.querySelector("#desktop-window");
    assert.match(chooser.innerHTML, /Desktop 2（其他桌面）/);
    chooser.value = JSON.stringify({kind: "window", id: "remote"});
    chooser.fire("change");
    assert.equal(h.panel.querySelector("#desktop-share").disabled, false);
    chooser.value = JSON.stringify({kind: "window", id: sharedWindow.id});
    chooser.fire("change");
    assert.equal(h.panel.querySelector("#desktop-share").disabled, false);
  } finally { h.close(); }
});

test("authorized frames survive foreground control errors while every input path stays disabled", async () => {
  for (const code of ["desktop_foreground_unavailable", "desktop_foreground_changed"]) {
    const h = harness(`readonly-frame-${code}`, ({url}) => {
      if (url.endsWith("/windows")) return {windows: []};
      if (url.includes("/observation?")) return {observation: foregroundObservation({control_error: code, input_actions: []})};
      return {status: foregroundStatus(foregroundSession())};
    });
    try {
      h.bind();
      await flush();
      assert.equal(h.panel.querySelector("#desktop-preview").hidden, false);
      const image = h.panel.querySelector("#desktop-image");
      assert.match(image.src, /^data:image\/png;base64,/);
      assert.equal(image.getAttribute("aria-disabled"), "true");
      assert.equal(h.panel.querySelector("#desktop-focus").disabled, true);
      assert.equal(h.panel.querySelector("#desktop-text-form button").disabled, true);
      assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
      const editor = h.panel.querySelector("#desktop-text-input");
      editor.value = "retain pending draft";
      editor.fire("input");
      image.focus();
      image.fire("pointerdown", {clientX: 20, clientY: 20, pointerId: 1, button: 0});
      image.fire("pointerup", {clientX: 80, clientY: 80, pointerId: 1, button: 0});
      image.fire("keydown", {key: "Enter"});
      image.fire("wheel", {clientX: 20, clientY: 20, deltaY: 80, deltaX: 0});
      h.panel.querySelector("#desktop-focus").fire("click");
      h.panel.querySelector("#desktop-text-form").fire("submit");
      h.tick();
      await flush();
      assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 0);
      assert.equal(h.panel.querySelector("#desktop-preview").hidden, false);
      assert.equal(editor.value, "retain pending draft");
      assert.match(h.panel.querySelector("#desktop-error").textContent, /前台窗口/);
    } finally { h.close(); }
  }
});

test("new capture access rejection codes have safe user-facing messages", async () => {
  for (const code of ["desktop_target_state_unavailable", "desktop_target_process_unavailable", "desktop_target_token_unavailable",
    "desktop_target_foreign_session", "desktop_target_foreign_user", "desktop_foreground_unavailable", "desktop_elevated_target",
    "desktop_background_control_changed", "desktop_background_child_unverifiable",
    "desktop_browser_accessibility_unavailable", "desktop_browser_scope_changed", "desktop_browser_page_unavailable"]) {
    const h = harness(`access-code-${code}`, () => { throw Object.assign(new Error(code), {code}); });
    try {
      h.bind();
      await flush();
      const message = h.panel.querySelector("#desktop-error").textContent;
      assert.ok(message && !message.includes("desktop_"), code);
      if (code.startsWith("desktop_background_")) assert.doesNotMatch(message, /目标窗口已关闭/);
      if (code === "desktop_foreground_unavailable") {
        assert.doesNotMatch(message, /仍可查看/);
        assert.equal(h.panel.querySelector("#desktop-preview").hidden, true);
      }
    } finally { h.close(); }
  }
});

test("unsupported servers and legacy exclusive sessions never silently gain shared access", async () => {
  for (const [id, supported, shared] of [
    ["old-server", undefined, null],
    ["unsupported-server", false, session()],
    ["legacy-owner", true, session({controller: "owner"})],
    ["legacy-agent", true, session({controller: "agent", claimed: true})],
  ]) {
    const h = harness(id, ({url}) => {
      if (url.endsWith("/windows")) return {windows: [sharedWindow]};
      if (url.includes("/observation?")) return {observation: foregroundObservation()};
      return {status: {...foregroundStatus(shared), shared_control_supported: supported}};
    });
    try {
      let confirmations = 0;
      window.confirm = () => { confirmations++; return true; };
      h.bind();
      await flush();
      assert.equal(h.panel.querySelector("#desktop-new-desktop").disabled, true);
      h.panel.querySelector("#desktop-new-desktop").fire("click");
      h.panel.querySelector("#desktop-share").fire("click");
      h.panel.querySelector("#desktop-focus").fire("click");
      await flush();
      assert.equal(confirmations, 0);
      assert.equal(h.calls.filter(call => call.payload).length, 0);
      assert.match(h.panel.querySelector("#desktop-status").textContent, supported ? /停止后重新共享/ : /不支持共同控制/);
      if (shared) assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
    } finally { h.close(); }
  }
});

test("sharing requires one explicit joint-access consent", async () => {
  {
    const h = harness("joint-consent", ({url}) => {
      if (url.endsWith("/windows")) return {windows: [sharedWindow]};
      return {status: foregroundStatus(null)};
    });
    try {
      h.bind();
      await flush();
      const chooser = h.panel.querySelector("#desktop-window");
      chooser.value = JSON.stringify({kind: "window", id: sharedWindow.id});
      chooser.fire("change");
      let confirmations = 0;
      window.confirm = message => { confirmations++; assert.match(message, /Main Agent 和 Owner 共同查看并操作/); return false; };
      h.panel.querySelector("#desktop-share").fire("click");
      assert.equal(h.calls.filter(call => call.payload).length, 0);
      window.confirm = message => { confirmations++; assert.match(message, /无需交接控制权/); return true; };
      h.panel.querySelector("#desktop-share").fire("click");
      await flush();
      assert.equal(confirmations, 2);
      const writes = h.calls.filter(call => call.payload);
      assert.equal(writes.length, 1);
      assert.deepEqual(writes[0].payload, {target: {kind: "window", window_id: sharedWindow.id},
        mode: "foreground", confirm_foreground: true, confirm_shared: true});
    } finally { h.close(); }
  }
});

test("Agent claim preserves Owner input, draft, revision and the existing image stream", async () => {
  const fixture = socketHarness("shared-claim");
  const {h} = fixture;
  try {
    h.bind();
    await flush();
    const socket = fixture.ready();
    socket.message(framePacket());
    await flush();
    const image = h.panel.querySelector("#desktop-image");
    const src = image.src;
    const editor = h.panel.querySelector("#desktop-text-input");
    editor.value = "Owner can assist";
    editor.fire("input");
    for (const claimed of [true, false, true]) {
      fixture.currentStatus.session = {...fixture.currentStatus.session, claimed};
      socket.message(JSON.stringify({type: "status", status: fixture.currentStatus}));
      await flush();
      assert.equal(socket.readyState, 1);
      assert.equal(fixture.sockets.length, 1);
      assert.equal(image.src, src);
      assert.equal(editor.value, "Owner can assist");
      assert.equal(editor.readOnly, false);
      assert.equal(h.panel.querySelector("#desktop-focus").disabled, false);
      assert.equal(fixture.currentStatus.session.revision, 1);
    }
    h.panel.querySelector("#desktop-focus").fire("click");
    await flush();
    assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 1);
    assert.equal(h.calls.filter(call => call.url.endsWith("/control")).length, 0);
    assert.equal(socket.readyState, 1);
  } finally { fixture.close(); }
});

test("cooperative busy and stale-frame errors refresh observations without retrying input", async () => {
  for (const code of ["desktop_busy", "desktop_stale_observation", "desktop_shared_control"]) {
    const h = harness(`shared-error-${code}`, ({url}) => {
      if (url.endsWith("/windows")) return {windows: []};
      if (url.includes("/observation?")) return {observation: foregroundObservation()};
      if (url.endsWith("/actions")) throw Object.assign(new Error(code), {code});
      return {status: foregroundStatus(foregroundSession({claimed: true}))};
    });
    try {
      h.bind();
      await flush();
      h.panel.querySelector("#desktop-focus").fire("click");
      await flush();
      for (let i = 0; i < 3; i++) { h.tick(); await flush(); }
      assert.equal(h.calls.filter(call => call.url.endsWith("/actions")).length, 1);
      assert.equal(h.calls.filter(call => call.url.endsWith("/control")).length, 0);
      assert.equal(h.panel.querySelector("#desktop-stop").disabled, false);
      assert.ok(h.panel.querySelector("#desktop-error").textContent);
    } finally { h.close(); }
  }
});



test("an unverifiable password mask withholds the frame", async () => {
  const h = harness("password-mask", ({url}) => {
    if (url.endsWith("/windows")) return {windows: []};
    if (url.includes("/observation?")) return {observation: observation({image: "", capture_error: "password_bounds_unavailable"})};
    return {status: foregroundStatus(session())};
  });
  try {
    h.bind();
    await flush();
    assert.equal(h.panel.querySelector("#desktop-preview").hidden, true);
    assert.match(h.panel.querySelector("#desktop-capture-status").textContent, /无法验证密码遮罩位置/);
    assert.equal(h.panel.querySelector("#desktop-image").getAttribute("src"), null);
  } finally { h.close(); }
});
