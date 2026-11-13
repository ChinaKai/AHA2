// Rejected unsaved-change switches must also roll the control back. The page
// render restores control values by region key, so a select that still shows
// the rejected project survives every later render and disagrees with the
// module state; this guards the change handlers' reject branch.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const build = resolve(import.meta.dirname, "..", "build", "knowledge_workspace.js");

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach(name => this.values.add(name)); }
  remove(...names) { names.forEach(name => this.values.delete(name)); }
  contains(name) { return this.values.has(name); }
  toggle(name) { if (this.values.has(name)) this.values.delete(name); else this.values.add(name); }
}

class FakeElement {
  constructor({tag = "div", value = "", name = "", dataset = {}} = {}) {
    this.tagName = tag.toUpperCase();
    this.value = value;
    this.name = name;
    this.dataset = dataset;
    this.classList = new FakeClassList();
    this.listeners = {};
    this.disabled = false;
    this.form = null;
    this.innerHTML = "";
  }
  addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler); }
  dispatch(type, init = {}) {
    const event = {currentTarget: this, target: init.target || this, preventDefault() {}, ...init};
    for (const handler of this.listeners[type] || []) handler(event);
  }
  closest() { return null; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
  setAttribute() {}
  getAttribute() { return null; }
  focus() {}
}

test("rejecting an unsaved switch restores the project and source selects", async () => {
  const module = await import(pathToFileURL(build).href);
  const projectSelect = new FakeElement({tag: "select", value: "project-1"});
  const sourceSelect = new FakeElement({tag: "select", value: "all"});
  const editButton = new FakeElement({dataset: {knowledgeEdit: "doc"}});
  const editor = new FakeElement({tag: "form"});

  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const confirmCalls = [];
  let renderCount = 0;

  globalThis.document = {
    activeElement: null,
    querySelector(selector) {
      if (selector === "#knowledge-project-select") return projectSelect;
      if (selector === "[data-knowledge-source-filter]") return sourceSelect;
      if (selector === "#knowledge-inline-editor") return editor;
      return null;
    },
    querySelectorAll(selector) {
      return selector === "[data-knowledge-edit]" ? [editButton] : [];
    },
    createElement() { return new FakeElement(); },
  };
  globalThis.window = {
    confirm(message) { confirmCalls.push(message); return false; },
    location: {hash: ""},
    history: {replaceState() {}},
  };
  // bindKnowledgeWorkspace warms the skill/product-line catalog through fetch;
  // this test only exercises the change handlers, so fail that call fast.
  globalThis.fetch = () => Promise.reject(new Error("offline"));

  const context = {
    projects: [
      {id: "project-1", name: "Project One", project_type: "folder", knowledge_policy: "enabled"},
      {id: "project-2", name: "Project Two", project_type: "folder", knowledge_policy: "enabled"},
    ],
    knowledge: [{id: "doc", scope: "project", project_id: "project-1", title: "Doc", body: "Body", type: "practice"}],
    proposals: [],
    libraries: [],
    workspaces: [],
    render() { renderCount += 1; },
    setMessage() {},
  };

  try {
    module.bindKnowledgeWorkspace(context);
    // Entering edit mode is asynchronous because the document detail may load.
    editButton.dispatch("click");
    await new Promise(done => setImmediate(done));
    assert.deepEqual(confirmCalls, [], "entering edit mode must not prompt");

    // Dirtying the editor is what arms the discard guard.
    editor.dispatch("input", {target: {name: "title", value: "Changed", form: null}});

    projectSelect.value = "project-2";
    projectSelect.dispatch("change");
    assert.equal(confirmCalls.length, 1, "the dirty guard must ask before switching projects");
    assert.equal(projectSelect.value, "project-1", "a rejected project switch must restore the selected project");
    assert.ok(renderCount > 0, "a rejected switch must still re-render the view");

    sourceSelect.value = "project-1";
    sourceSelect.dispatch("change");
    assert.equal(confirmCalls.length, 2, "the dirty guard must ask before switching sources");
    assert.equal(sourceSelect.value, "all", "a rejected source switch must restore the selected source");

    // Accepting still switches for real and must not be undone by the rollback.
    globalThis.window.confirm = () => true;
    projectSelect.value = "project-2";
    projectSelect.dispatch("change");
    assert.equal(projectSelect.value, "project-2");
  } finally {
    if (previousDocument === undefined) delete globalThis.document; else globalThis.document = previousDocument;
    if (previousWindow === undefined) delete globalThis.window; else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch; else globalThis.fetch = previousFetch;
  }
});