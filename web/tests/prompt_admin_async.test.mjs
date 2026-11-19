// Event-level regression tests for the prompt editor's asynchronous paths.
// These drive the real bindPromptAdmin handlers through a minimal DOM stub and
// the real API client through a controllable fetch, rather than calling the
// pure editor-state helpers.
import assert from "node:assert/strict";
import test from "node:test";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const buildDir = resolve(import.meta.dirname, "..", "build");
const promptPath = resolve(buildDir, "prompt_admin.js");

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach(name => this.values.add(name)); }
  remove(...names) { names.forEach(name => this.values.delete(name)); }
  contains(name) { return this.values.has(name); }
}

class FakeElement {
  constructor({tag = "div", value = "", defaultValue = ""} = {}) {
    this.tagName = tag.toUpperCase();
    this.value = value;
    this.defaultValue = defaultValue;
    this.dataset = {};
    this.listeners = {};
    this.disabled = false;
    this.hidden = false;
    this.open = false;
    this.classList = new FakeClassList();
    this.innerHTML = "";
  }
  addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler); }
  dispatch(type, init = {}) {
    const event = {currentTarget: this, target: init.target || this, preventDefault() {}, ...init};
    for (const handler of [...(this.listeners[type] || [])]) handler(event);
  }
  focus() { globalThis.document.activeElement = this; }
  blur() { if (globalThis.document.activeElement === this) globalThis.document.activeElement = null; }
  matches(selector) { return selector === "[data-prompt-template-content]"; }
  showModal() { this.open = true; }
  close() {
    if (!this.open) return;
    this.open = false;
    this.dispatch("close");
  }
  querySelector() { return null; }
  querySelectorAll() { return []; }
}

function response(payload, status = 200) {
  return {ok: status >= 200 && status < 300, status, json: async () => payload};
}

function deferred() {
  let resolvePromise;
  const promise = new Promise(resolve => { resolvePromise = resolve; });
  return {promise, resolve: resolvePromise};
}

const flush = () => new Promise(resolve => setTimeout(resolve, 0));

async function importPromptModule(caseName) {
  const url = `${pathToFileURL(promptPath).href}?case=${encodeURIComponent(caseName)}`;
  return import(url);
}

async function loadVersionedAPI() {
  const source = await readFile(promptPath, "utf8");
  const match = source.match(/from "(\.\/api\.js\?v=[a-f0-9]{12})"/);
  assert.ok(match, "prompt_admin.js must import the versioned api singleton");
  const module = await import(new URL(match[1], pathToFileURL(promptPath)).href);
  return module.api;
}

const initialTemplates = () => [
  {
    id: "role.main", name: "Main Agent", layer: "role", description: "Main role",
    content: "A saved", source: "override", editable: true, required: true, version: 2,
  },
  {
    id: "role.sub", name: "Sub Agent", layer: "role", description: "Sub role",
    content: "B saved", source: "builtin", editable: true, required: true, version: 1,
  },
];

async function setup(caseName) {
  const module = await importPromptModule(caseName);
  const api = await loadVersionedAPI();
  api.setCSRF("test-csrf");
  let catalogue = initialTemplates();
  const pendingPuts = [];
  const pendingResets = [];
  const notifications = [];

  const templateA = new FakeElement({tag: "button"});
  templateA.dataset.promptTemplate = "role.main";
  const templateB = new FakeElement({tag: "button"});
  templateB.dataset.promptTemplate = "role.sub";
  const textarea = new FakeElement({tag: "textarea", value: "A saved", defaultValue: "A saved"});
  const editButton = new FakeElement({tag: "button"});
  const saveButton = new FakeElement({tag: "button"});
  const cancelButton = new FakeElement({tag: "button"});
  const resetButton = new FakeElement({tag: "button"});
  const switchSave = new FakeElement({tag: "button"});
  const switchDiscard = new FakeElement({tag: "button"});
  const switchCancel = new FakeElement({tag: "button"});
  const switchDialog = new FakeElement({tag: "dialog"});
  const previewButton = new FakeElement({tag: "button"});

  const selectors = new Map([
    ["[data-prompt-template-content]", textarea],
    ["#edit-prompt-template", editButton],
    ["#save-prompt-template", saveButton],
    ["#cancel-prompt-template", cancelButton],
    ["#reset-prompt-template", resetButton],
    ["[data-prompt-switch-save]", switchSave],
    ["[data-prompt-switch-discard]", switchDiscard],
    ["[data-prompt-switch-cancel]", switchCancel],
    ["[data-prompt-switch-dialog]", switchDialog],
    ["[data-prompt-preview-open]", previewButton],
    ["[data-prompt-scenario-dialog]", null],
    ["[data-prompt-scenario-close]", null],
  ]);

  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const previousHTMLElement = globalThis.HTMLElement;
  globalThis.HTMLElement = FakeElement;
  globalThis.document = {
    activeElement: textarea,
    querySelector(selector) { return selectors.get(selector) ?? null; },
    querySelectorAll(selector) {
      return selector === "[data-prompt-template]" ? [templateA, templateB] : [];
    },
    createElement() { return new FakeElement(); },
  };
  globalThis.window = {confirm: () => true};
  globalThis.fetch = (path, options = {}) => {
    const method = (options.method || "GET").toUpperCase();
    if (method === "GET" && path === "/api/v1/prompts/templates") {
      return Promise.resolve(response({templates: structuredClone(catalogue)}));
    }
    if (method === "PUT" && path.startsWith("/api/v1/prompts/templates/")) {
      const request = {...deferred(), path, body: options.body};
      pendingPuts.push(request);
      return request.promise;
    }
    if (method === "POST" && path.endsWith("/reset")) {
      const request = {...deferred(), path};
      pendingResets.push(request);
      return request.promise;
    }
    return Promise.resolve(response({ok: true}));
  };

  module.bindPromptAdmin(() => {}, (kind, message) => notifications.push({kind, message}));
  await module.loadPromptCatalog();
  textarea.value = "A saved";
  textarea.defaultValue = "A saved";

  const resolvePut = (request, status = 200) => {
    if (status === 200) {
      const id = decodeURIComponent(request.path.split("/").pop());
      const content = JSON.parse(request.body).content;
      catalogue = catalogue.map(item => item.id === id
        ? {...item, content, source: "override", version: item.version + 1}
        : item);
      request.resolve(response({ok: true}));
    } else {
      request.resolve(response({ok: false, error: "update_prompt_template_failed", message: "save failed"}, status));
    }
  };
  const resolveReset = request => {
    const id = decodeURIComponent(request.path.replace("/api/v1/prompts/templates/", "").replace("/reset", ""));
    catalogue = catalogue.map(item => item.id === id
      ? {...item, content: id === "role.main" ? "A builtin" : item.content, source: "builtin"}
      : item);
    request.resolve(response({ok: true}));
  };

  const beginEdit = async (content) => {
    editButton.dispatch("click");
    await flush();
    textarea.defaultValue = content;
    textarea.value = content;
  };
  const type = async (value) => {
    textarea.value = value;
    textarea.dispatch("input");
    await flush();
  };

  const restore = () => {
    if (previousDocument === undefined) delete globalThis.document; else globalThis.document = previousDocument;
    if (previousWindow === undefined) delete globalThis.window; else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch; else globalThis.fetch = previousFetch;
    if (previousHTMLElement === undefined) delete globalThis.HTMLElement; else globalThis.HTMLElement = previousHTMLElement;
  };

  return {
    module, templateA, templateB, textarea, editButton, saveButton, cancelButton, resetButton,
    switchSave, switchCancel, switchDialog, previewButton, notifications,
    pendingPuts, pendingResets, resolvePut, resolveReset, beginEdit, type, restore,
  };
}

function assertEditingTemplate(html, templateID, draft) {
  assert.match(html, new RegExp(`data-prompt-template="${templateID}" class="prompt-template-item active"`));
  const textareaTag = html.match(/<textarea[^>]*data-prompt-template-content[^>]*>/)?.[0] || "";
  assert.doesNotMatch(textareaTag, /readonly/);
  assert.ok(html.includes(draft), `rendered editor should contain ${JSON.stringify(draft)}`);
  assert.match(html, /id="preview-prompt-scenario"[^>]*disabled/);
}

test("a delayed reset response cannot replace another template's draft", async () => {
  const h = await setup("reset-race");
  try {
    await h.beginEdit("A saved");
    h.resetButton.dispatch("click");
    await flush();
    assert.equal(h.pendingResets.length, 1, "reset request should be in flight");

    // Cancel A, switch to B, and type a B draft while A's reset is paused.
    h.cancelButton.dispatch("click");
    await flush();
    h.templateB.dispatch("click");
    await flush();
    await h.beginEdit("B saved");
    await h.type("B draft");

    h.resolveReset(h.pendingResets[0]);
    await flush();
    await flush();

    const html = h.module.renderPromptAdmin();
    assertEditingTemplate(html, "role.sub", "B draft");
    assert.doesNotMatch(html, /A builtin/);
    assert.ok(h.notifications.some(item => item.kind === "notice" && item.message.includes("已恢复内置模板")));

    h.previewButton.dispatch("click");
    await flush();
    assert.ok(h.notifications.some(item => item.kind === "error" && item.message.includes("先保存或取消编辑再预览")));
  } finally {
    h.restore();
  }
});

test("canceling a switch invalidates the in-flight save-and-switch", async () => {
  const h = await setup("switch-cancel");
  try {
    await h.beginEdit("A saved");
    await h.type("A draft");
    h.templateB.dispatch("click");
    await flush();
    h.switchSave.dispatch("click");
    await flush();
    assert.equal(h.pendingPuts.length, 1, "save request should be in flight");

    h.switchCancel.dispatch("click");
    await flush();
    h.resolvePut(h.pendingPuts[0]);
    await flush();
    await flush();

    const html = h.module.renderPromptAdmin();
    assertEditingTemplate(html, "role.main", "A draft");
    assert.doesNotMatch(html, /data-prompt-template="role.sub" class="prompt-template-item active"/);
  } finally {
    h.restore();
  }
});

test("Esc closing the switch dialog invalidates the in-flight save-and-switch", async () => {
  const h = await setup("switch-escape");
  try {
    await h.beginEdit("A saved");
    await h.type("A draft");
    h.templateB.dispatch("click");
    await flush();
    h.switchSave.dispatch("click");
    await flush();
    assert.equal(h.pendingPuts.length, 1, "save request should be in flight");

    h.switchDialog.close();
    await flush();
    h.resolvePut(h.pendingPuts[0]);
    await flush();
    await flush();

    const html = h.module.renderPromptAdmin();
    assertEditingTemplate(html, "role.main", "A draft");
    assert.doesNotMatch(html, /data-prompt-template="role.sub" class="prompt-template-item active"/);
  } finally {
    h.restore();
  }
});

test("a failed save keeps the editing draft and preview disabled", async () => {
  const h = await setup("save-failure");
  try {
    await h.beginEdit("A saved");
    await h.type("A failed draft");
    h.saveButton.dispatch("click");
    await flush();
    assert.equal(h.pendingPuts.length, 1);

    h.resolvePut(h.pendingPuts[0], 500);
    await flush();
    await flush();

    const html = h.module.renderPromptAdmin();
    assertEditingTemplate(html, "role.main", "A failed draft");
    assert.ok(h.notifications.some(item => item.kind === "error"));
  } finally {
    h.restore();
  }
});

test("typing during a save keeps the newer draft editable", async () => {
  const h = await setup("typing-during-save");
  try {
    await h.beginEdit("A saved");
    await h.type("A first draft");
    h.saveButton.dispatch("click");
    await flush();
    assert.equal(h.pendingPuts.length, 1);

    await h.type("A newer draft");
    h.resolvePut(h.pendingPuts[0]);
    await flush();
    await flush();

    const html = h.module.renderPromptAdmin();
    assertEditingTemplate(html, "role.main", "A newer draft");
  } finally {
    h.restore();
  }
});
