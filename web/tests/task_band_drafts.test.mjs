// Default Task scope and per-Task draft isolation. These are the two pure
// contracts behind the task band and the composer, so they are exercised
// without a browser; the layout itself has a browser test next to this file.
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
import test from "node:test";

const build = resolve(import.meta.dirname, "..", "build");
const filters = await import(pathToFileURL(resolve(build, "task_filters.js")).href);
const drafts = await import(pathToFileURL(resolve(build, "task_drafts.js")).href);

const task = (id, status, overrides = {}) => ({
  id,
  project_id: "p1",
  workspace_id: "w1",
  status,
  read_only: false,
  ...overrides,
});

test("default scope keeps local unfinished/error work and drops terminal tasks", () => {
  const local = status => task(`local-${status}`, status);
  for (const status of ["draft", "preparing", "active", "waiting_user", "failed", "blocked"]) {
    assert.equal(filters.taskInDefaultTaskScope(local(status)), true, `${status} belongs in the default scope`);
  }
  assert.equal(filters.taskInDefaultTaskScope(local("completed")), false);
  assert.equal(filters.taskInDefaultTaskScope(local("cancelled")), false);
  assert.equal(
    filters.taskInDefaultTaskScope(task("remote", "active", {read_only: true, owner_device_id: "other"})),
    false,
    "another device's Task is not part of the local default scope",
  );
});

test("the default list and the band agree on every terminal and remote Task", () => {
  const projectIDs = new Set();
  const statusGroups = new Set(filters.DEFAULT_TASK_STATUS_FILTERS);
  const deviceIDs = new Set(["local"]);
  assert.equal(filters.isDefaultTaskFilterSelection(projectIDs, statusGroups, deviceIDs), true);
  const candidates = [
    task("a", "active"),
    task("b", "failed"),
    task("c", "completed"),
    task("d", "cancelled"),
    task("e", "active", {read_only: true, owner_device_id: "other"}),
  ];
  for (const item of candidates) {
    assert.equal(
      filters.taskMatchesFilters(item, projectIDs, statusGroups, deviceIDs),
      filters.taskInDefaultTaskScope(item),
      `${item.status} must be filtered the same way in both places`,
    );
  }
});

test("choosing the 异常 bucket by hand still shows cancelled Tasks", () => {
  const cancelled = task("cancelled", "cancelled");
  assert.equal(filters.taskMatchesFilters(cancelled, new Set(), new Set(["error"]), new Set(["local"])), true);
  assert.equal(filters.taskMatchesFilters(cancelled, new Set(), new Set(["open", "error"]), new Set(["local"])), false);
  // A project or device choice also counts as a hand-picked filter.
  assert.equal(filters.taskMatchesFilters(cancelled, new Set(["p1"]), new Set(["open", "error"]), new Set(["local"])), true);
  assert.equal(filters.taskMatchesFilters(cancelled, new Set(), new Set(["open", "error"]), new Set()), true);
});

test("the band label describes the predicate it ships with", () => {
  assert.equal(filters.DEFAULT_TASK_SCOPE_LABEL, "未完成与异常 · 本机 · 全部项目");
  assert.equal(
    filters.hiddenTaskStatusText([{group: "done", label: "已完成", count: 3}], 2),
    "已隐藏 3 个已完成、2 个已取消任务",
  );
});

test("drafts are isolated per Task even when the agent id repeats", () => {
  let store = {};
  store = drafts.writeTaskDraft(store, "task-a", "main", {text: "keep A", attachments: []});
  store = drafts.writeTaskDraft(store, "task-b", "main", {text: "keep B", attachments: []});
  assert.equal(drafts.readTaskDraft(store, "task-a", "main").text, "keep A");
  assert.equal(drafts.readTaskDraft(store, "task-b", "main").text, "keep B");
  assert.equal(drafts.readTaskDraft(store, "task-c", "main").text, "");

  store = drafts.clearTaskDraft(store, "task-b", "main");
  assert.equal(drafts.readTaskDraft(store, "task-a", "main").text, "keep A");
  assert.equal(drafts.readTaskDraft(store, "task-b", "main").text, "");
  assert.equal(drafts.taskDraftKey("task-a", "main") === drafts.taskDraftKey("task-b", "main"), false);
});

test("drafts keep attachments per Task and survive a storage round trip", () => {
  const attachment = {
    id: "att-1", task_id: "task-a", name: "diagram.png",
    media_type: "image/png", size: 12, sha256: "abc", created_at: "2026-11-21T00:00:00Z",
  };
  let store = drafts.writeTaskDraft({}, "task-a", "main", {text: "with file", attachments: [attachment]});
  store = drafts.writeTaskDraft(store, "task-b", "main", {text: "", attachments: [attachment]});
  assert.equal(drafts.readTaskDraft(store, "task-a", "main").attachments[0].id, "att-1");

  const memory = new Map();
  const storage = {
    getItem: key => memory.get(key) ?? null,
    setItem: (key, value) => memory.set(key, value),
  };
  drafts.saveTaskDraftStore(store, storage);
  const restored = drafts.loadTaskDraftStore(storage);
  assert.deepEqual(restored, store);

  // Entries written by a future/broken version without the separator or with
  // garbage attachments are ignored instead of crashing the composer.
  assert.deepEqual(drafts.parseTaskDraftStore('{"main":{"text":"old"}}'), {});
  assert.deepEqual(
    drafts.parseTaskDraftStore('{"task-a\\u0000main":{"text":"ok","attachments":[{"nope":true}]}}'),
    {[drafts.taskDraftKey("task-a", "main")]: {text: "ok", attachments: []}},
  );
  assert.deepEqual(drafts.parseTaskDraftStore("not json"), {});
});

test("empty drafts are removed from the store", () => {
  let store = drafts.writeTaskDraft({}, "task-a", "main", {text: "x", attachments: []});
  store = drafts.writeTaskDraft(store, "task-a", "main", {text: "", attachments: []});
  assert.deepEqual(store, {});
  assert.equal(drafts.draftHasContent({text: "", attachments: []}), false);
});
