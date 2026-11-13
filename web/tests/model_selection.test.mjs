// Behavioral tests for model-page paging, Provider grouping, and multi-select.
// The page keeps 20 models per page while the visible page is grouped by
// Provider; select-all works on both the page and one visible group.
import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {
  MODEL_PAGE_SIZE,
  buildModelPage,
  groupModelsByProvider,
  modelBatchBarHTML,
  modelSelectionIndicator,
  modelSelectionState,
  pruneModelSelection,
  selectModelIDs,
  toggleModelSelection,
} = await import(pathToFileURL(resolve(root, "build", "model_selection.js")).href);

function makeModels(count, providerFor = index => `provider-${index % 2 === 0 ? "a" : "b"}`) {
  return Array.from({length: count}, (_, index) => ({
    id: `model-${index + 1}`,
    display_name: `Model ${index + 1}`,
    provider_id: providerFor(index),
    provider_name: providerFor(index) === "provider-a" ? "Provider A" : "Provider B",
    backend: "codex",
    wire_model: `wire-${index + 1}`,
  }));
}

test("the model page keeps 20 rows per page and clamps the requested page", () => {
  assert.equal(MODEL_PAGE_SIZE, 20);
  const models = makeModels(25);
  const first = buildModelPage(models, 1);
  assert.equal(first.slice.items.length, 20);
  assert.equal(first.slice.pages, 2);
  assert.equal(first.pageIDs.length, 20);
  const second = buildModelPage(models, 2);
  assert.equal(second.slice.items.length, 5);
  assert.equal(second.slice.items[0].id, "model-21");
  const clamped = buildModelPage(models, 99);
  assert.equal(clamped.slice.page, 2);
  assert.equal(clamped.slice.items.length, 5);
});

test("the current page is grouped by Provider and each header carries the provider total", () => {
  // Provider A has 22 models, so page 1 shows 20 of them and page 2 shows 2.
  const models = [
    ...makeModels(22, () => "provider-a"),
    ...makeModels(3, () => "provider-b"),
  ];
  const first = buildModelPage(models, 1);
  assert.deepEqual(first.groups.map(group => group.providerId), ["provider-a"]);
  assert.equal(first.groups[0].providerName, "Provider A");
  assert.equal(first.groups[0].items.length, 20);
  assert.equal(first.groups[0].total, 22);

  const second = buildModelPage(models, 2);
  assert.deepEqual(second.groups.map(group => group.providerId), ["provider-a", "provider-b"]);
  assert.equal(second.groups[0].items.length, 2);
  assert.equal(second.groups[0].total, 22);
  assert.equal(second.groups[1].items.length, 3);
  assert.equal(second.groups[1].total, 3);
});

test("grouping keeps first-seen Provider order and never invents providers", () => {
  const models = [
    {id: "m1", provider_id: "p2", provider_name: "Second"},
    {id: "m2", provider_id: "p1", provider_name: "First"},
    {id: "m3", provider_id: "p2", provider_name: "Second"},
  ];
  const groups = groupModelsByProvider(models, models);
  assert.deepEqual(groups.map(group => group.providerId), ["p2", "p1"]);
  assert.deepEqual(groups.map(group => group.items.map(item => item.id)), [["m1", "m3"], ["m2"]]);
});

test("selection state distinguishes none, some, and all", () => {
  const ids = ["a", "b", "c"];
  assert.equal(modelSelectionState(new Set(), ids), "none");
  assert.equal(modelSelectionState(new Set(["a"]), ids), "some");
  assert.equal(modelSelectionState(new Set(["a", "b", "c"]), ids), "all");
  assert.equal(modelSelectionState(new Set(["a"]), []), "none");
});

test("checkbox indicators distinguish unchecked, indeterminate, and checked", () => {
  const ids = ["a", "b", "c"];
  assert.deepEqual(modelSelectionIndicator(new Set(), ids), {checked: false, indeterminate: false});
  assert.deepEqual(modelSelectionIndicator(new Set(["a"]), ids), {checked: false, indeterminate: true});
  assert.deepEqual(modelSelectionIndicator(new Set(["a", "b", "c"]), ids), {checked: true, indeterminate: false});
  // An empty group cannot be indeterminate.
  assert.deepEqual(modelSelectionIndicator(new Set(["a"]), []), {checked: false, indeterminate: false});
});

test("select all page and select all group add or remove only their own ids", () => {
  const selected = new Set();
  const pageIDs = ["a", "b", "c"];
  selectModelIDs(selected, pageIDs, true);
  assert.deepEqual([...selected], ["a", "b", "c"]);
  selectModelIDs(selected, ["b", "c"], false);
  assert.deepEqual([...selected], ["a"]);
  toggleModelSelection(selected, "d", true);
  toggleModelSelection(selected, "a", false);
  assert.deepEqual([...selected], ["d"]);
});

test("pruning drops selections for models that disappeared", () => {
  const selected = new Set(["a", "b", "gone"]);
  pruneModelSelection(selected, [{id: "a"}, {id: "b"}]);
  assert.deepEqual([...selected], ["a", "b"]);
});

test("the batch bar only appears with a selection", () => {
  assert.equal(modelBatchBarHTML(0), "");
  const html = modelBatchBarHTML(3);
  assert.match(html, /已选 3 个模型/);
  assert.match(html, /data-batch-check-models/);
  assert.match(html, /data-batch-delete-models/);
  assert.match(html, /data-clear-model-selection/);
});
