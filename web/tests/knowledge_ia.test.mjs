// Knowledge information architecture: four equal top-level sections, project
// settings reached from the project picker's gear, libraries/bindings owned by
// project settings, no bindings affordance on global knowledge, and a scoped
// review inbox. These render-level checks do not need a browser.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const build = resolve(import.meta.dirname, "..", "build", "knowledge_workspace.js");

const now = "2026-11-01T00:00:00Z";
const knowledge = [
  {id: "project-root", scope: "project", project_id: "project-1", parent_id: "", is_index: true, type: "navigation", title: "Project index", slug: "index", body: "Project home.", status: "verified", confidence: .9, revision: 1, sort_order: 0, helped_count: 0, stale_count: 0, created_at: now, updated_at: now},
  {id: "project-doc", scope: "project", project_id: "project-1", parent_id: "project-root", is_index: false, type: "practice", title: "Build guide", slug: "build-guide", body: "Run the build.", status: "verified", confidence: .9, revision: 1, sort_order: 1, helped_count: 0, stale_count: 0, created_at: now, updated_at: now},
  {id: "global-root", scope: "global", parent_id: "", is_index: true, type: "navigation", title: "Global index", slug: "index", body: "Global home.", status: "verified", confidence: .9, revision: 1, sort_order: 0, helped_count: 0, stale_count: 0, created_at: now, updated_at: now},
];
const proposals = [
  {id: "proposal-project", entry_id: "project-doc", base_revision: 0, status: "pending", review_mode: "manual", scope: "project", project_id: "project-1", title: "Project proposal", body: "project body", created_at: "2026-11-02T02:00:00Z", proposed: {confidence: .8}},
  {id: "proposal-global", entry_id: "global-root", base_revision: 0, status: "pending", review_mode: "manual", scope: "global", title: "Global proposal", body: "global body", created_at: "2026-11-02T01:00:00Z", proposed: {confidence: .7}},
];
const libraries = [
  {id: "library-bound-1", name: "Project One Library", description: "One", container_project_id: "container-1", bound_project_id: "project-1", binding_mode: "project", knowledge_count: 2, skill_count: 0},
  {id: "library-bound-2", name: "Project Two Library", description: "Two", container_project_id: "container-2", bound_project_id: "project-2", binding_mode: "external", knowledge_count: 1, skill_count: 0},
  {id: "library-pending", name: "Imported Library", description: "Pending", container_project_id: "container-3", knowledge_count: 0, skill_count: 0},
];

function makeContext(overrides = {}) {
  return {
    projects: [
      {id: "project-1", name: "Project One", description: "", project_type: "folder", knowledge_policy: "enabled"},
      {id: "project-2", name: "Project Two", description: "", project_type: "folder", knowledge_policy: "enabled"},
    ],
    workspaces: [{id: "workspace-1", project_id: "project-1", name: "Native", locality: "local", transport: "native", root_path: "/repo", health: "ready"}],
    knowledge,
    libraries,
    proposals,
    reviewSettings: {auto_approve: false},
    refreshData: async () => {},
    render: () => {},
    setMessage: () => {},
    ...overrides,
  };
}

test("knowledge exposes exactly four equal top-level sections and only review carries a count", async () => {
  const module = await import(pathToFileURL(build).href);
  const html = module.renderKnowledgeWorkspace(makeContext());
  assert.equal((html.match(/data-knowledge-section=/g) || []).length, 4);
  const order = ["data-knowledge-section=\"project\"", "data-knowledge-section=\"global\"", "data-knowledge-section=\"skills\"", "data-knowledge-section=\"review\""].map(marker => html.indexOf(marker));
  assert.ok(order.every(index => index >= 0), "all four sections must render");
  assert.deepEqual([...order].sort((left, right) => left - right), order, "sections keep project, global, skills, review order");
  assert.match(html, /id="knowledge-project-select"/);
  assert.match(html, /data-knowledge-project-settings[^>]*aria-label="项目设置"/);
  assert.doesNotMatch(html, /库与绑定/);
  assert.doesNotMatch(html, /data-knowledge-mode="manage"/);
  assert.equal((html.match(/knowledge-pending-count/g) || []).length, 1, "only review gets a badge");
  assert.match(html, /knowledge-pending-count has-pending">2</);
  // The review entry is the only review affordance; project knowledge has no shortcut.
  assert.equal((html.match(/data-knowledge-mode="review"/g) || []).length, 1);
});

test("project settings owns libraries, workspace bindings, and bind/unbind for the current project", async () => {
  const module = await import(pathToFileURL(build).href);
  const read = module.renderKnowledgeWorkspace(makeContext());
  assert.doesNotMatch(read, /data-knowledge-library-open|data-knowledge-library-unbind|Workspace Bindings/);

  const settings = module.renderKnowledgeProjectSettings(makeContext());
  assert.match(settings, /class="knowledge-settings"/);
  assert.match(settings, /data-knowledge-settings-close/);
  assert.match(settings, /Workspace Bindings/);
  assert.match(settings, /Project One Library/);
  assert.match(settings, /data-knowledge-library-unbind="library-bound-1"/);
  assert.match(settings, /data-knowledge-library-open="library-pending"/);
  assert.match(settings, /data-knowledge-library-bind="library-pending"/);
  assert.doesNotMatch(settings, /Project Two Library/);
  assert.doesNotMatch(settings, /knowledge-document-body/);
});

test("global knowledge renders no project picker, settings gear, or binding entry", async () => {
  const module = await import(pathToFileURL(build).href);
  const previousWindow = globalThis.window;
  globalThis.window = {location: {hash: "#knowledge?doc=global-root&scope=global"}, history: {replaceState() {}}, confirm: () => true};
  try {
    const html = module.renderKnowledgeWorkspace(makeContext());
    assert.match(html, /knowledge-mode-read/);
    assert.doesNotMatch(html, /id="knowledge-project-select"/);
    assert.doesNotMatch(html, /data-knowledge-project-settings/);
    assert.doesNotMatch(html, /data-knowledge-library-open|data-knowledge-library-bind|data-knowledge-library-unbind|Workspace Bindings/);
  } finally {
    globalThis.window = {location: {hash: "#knowledge"}, history: {replaceState() {}}, confirm: () => true};
    module.renderKnowledgeWorkspace(makeContext());
    if (previousWindow === undefined) delete globalThis.window; else globalThis.window = previousWindow;
  }
});

test("review inbox filters by all, global, or one project without changing the total count", async () => {
  const module = await import(pathToFileURL(build).href);
  const context = makeContext();
  const html = module.renderKnowledgeReviewPage(context);
  assert.match(html, /data-knowledge-review-scope/);
  assert.match(html, /value="all" selected>全部/);
  assert.match(html, /value="global"\s*>全局知识/);
  assert.match(html, /value="project:project-1"\s*>项目 · Project One/);
  assert.match(html, /value="project:project-2"\s*>项目 · Project Two/);

  const queue = module.knowledgeReviewQueue(proposals, knowledge, context.projects);
  assert.equal(queue.length, 2);
  assert.equal(queue[0].scope, "project");
  assert.equal(queue[0].projectID, "project-1");
  assert.deepEqual(module.filterKnowledgeReviewItems(queue, "all").map(item => item.key), ["proposal:proposal-project", "proposal:proposal-global"]);
  assert.deepEqual(module.filterKnowledgeReviewItems(queue, "global").map(item => item.key), ["proposal:proposal-global"]);
  assert.deepEqual(module.filterKnowledgeReviewItems(queue, "project:project-1").map(item => item.key), ["proposal:proposal-project"]);
  assert.deepEqual(module.filterKnowledgeReviewItems(queue, "project:project-2"), []);
});

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach(name => this.values.add(name)); }
  contains(name) { return this.values.has(name); }
}

class FakeElement {
  constructor({tag = "button", value = "", dataset = {}} = {}) {
    this.tagName = tag.toUpperCase();
    this.value = value;
    this.dataset = dataset;
    this.classList = new FakeClassList();
    this.listeners = {};
    this.disabled = false;
    this.form = null;
    this.innerHTML = "";
  }
  addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler); }
  dispatch(type, init = {}) {
    const event = {currentTarget: this, target: this, preventDefault() {}, ...init};
    for (const handler of this.listeners[type] || []) handler(event);
  }
  closest() { return null; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
  setAttribute() {}
  getAttribute() { return null; }
  focus() {}
}

test("the project gear opens settings, follows project switches, and closes back to the same document", async () => {
  const module = await import(pathToFileURL(build).href);
  const gear = new FakeElement({dataset: {}});
  const close = new FakeElement({dataset: {}});
  const doc = new FakeElement({dataset: {knowledgeOpen: "project-doc"}});
  const projectTab = new FakeElement({dataset: {knowledgeScope: "project"}});
  const projectSelect = new FakeElement({tag: "select", value: "project-1"});
  let rendered = "";
  const context = makeContext({render() { rendered = module.renderKnowledgeWorkspace(context); }});

  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  globalThis.document = {
    activeElement: null,
    querySelector(selector) {
      if (selector === "#knowledge-project-select") return projectSelect;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-knowledge-project-settings]") return [gear];
      if (selector === "[data-knowledge-settings-close]") return [close];
      if (selector === "[data-knowledge-open]") return [doc];
      if (selector === "[data-knowledge-scope]") return [projectTab];
      return [];
    },
    createElement() { return new FakeElement(); },
  };
  globalThis.window = {location: {hash: ""}, history: {replaceState() {}}, confirm: () => true};
  globalThis.fetch = () => Promise.reject(new Error("offline"));

  try {
    module.bindKnowledgeWorkspace(context);
    // Earlier tests may have left the view in global scope; return to project knowledge.
    projectTab.dispatch("click");
    // Pick a document so "back to the same position" has something to preserve.
    doc.dispatch("click");
    await new Promise(done => setImmediate(done));
    assert.match(rendered, /knowledge-tree-document active[^>]*>Build guide/);

    gear.dispatch("click");
    assert.match(rendered, /class="knowledge-settings"/);
    assert.match(rendered, /Project One Library/);
    assert.doesNotMatch(rendered, /knowledge-read-layout/);

    // Closing without changing project restores the directory and open document.
    close.dispatch("click");
    assert.doesNotMatch(rendered, /class="knowledge-settings"/);
    assert.match(rendered, /knowledge-read-layout/);
    assert.match(rendered, /knowledge-tree-document active[^>]*>Build guide/);

    // Reopening settings and switching project keeps settings open and swaps content.
    gear.dispatch("click");
    projectSelect.value = "project-2";
    projectSelect.dispatch("change");
    assert.match(rendered, /class="knowledge-settings"/);
    assert.match(rendered, /Project Two Library/);
    assert.doesNotMatch(rendered, /Project One Library/);

    // Switching back before closing returns to project knowledge for project-1.
    projectSelect.value = "project-1";
    projectSelect.dispatch("change");
    assert.match(rendered, /class="knowledge-settings"/);
    close.dispatch("click");
    assert.doesNotMatch(rendered, /class="knowledge-settings"/);
    assert.match(rendered, /knowledge-read-layout/);
    assert.match(rendered, /knowledge-project-select[^>]*>[\s\S]*Project One/);
  } finally {
    if (previousDocument === undefined) delete globalThis.document; else globalThis.document = previousDocument;
    if (previousWindow === undefined) delete globalThis.window; else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch; else globalThis.fetch = previousFetch;
  }
});