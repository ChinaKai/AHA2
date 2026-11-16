// The channel knowledge picker has two shapes: the legacy per-entry policy
// editor with its all/selected mode select, and the vault editor which has no
// mode select and is exactly the checkbox list. Binding the scope controls must
// not hide the vault list when the select is absent.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const build = resolve(import.meta.dirname, "..", "build", "channels.js");
const channels = await import(pathToFileURL(build).href);

function fakeInput(name, checked) {
  const listeners = {};
  return {name, checked, listeners, addEventListener(type, handler) { listeners[type] = handler; }};
}

function fakeButton() {
  const listeners = {};
  return {listeners, addEventListener(type, handler) { listeners[type] = handler; }};
}

// A minimal DOM stand-in for bindChannelKnowledgeScope. The function only uses
// querySelector/querySelectorAll/addEventListener and the hidden/checked/value
// properties, so a real browser is not needed to cover the regression.
function fakePicker({modeValue} = {}) {
  const selected = {hidden: false};
  const warning = {hidden: false};
  const mode = modeValue === undefined ? null : {value: modeValue, listeners: {}, addEventListener(type, handler) { this.listeners[type] = handler; }};
  const projectInputs = [fakeInput("knowledge_entry_id", true), fakeInput("knowledge_entry_id", false)];
  const globalInput = fakeInput("include_global", false);
  const selectAll = fakeButton();
  const clear = fakeButton();
  const container = {
    querySelector(selector) {
      if (selector === 'select[name="knowledge_scope_mode"]') return mode;
      if (selector === "[data-channel-knowledge-selected]") return selected;
      if (selector === "[data-channel-knowledge-empty-warning]") return warning;
      if (selector === 'input[name="include_global"]') return globalInput;
      if (selector === "[data-channel-policy-select-all]") return selectAll;
      if (selector === "[data-channel-policy-clear]") return clear;
      return null;
    },
    querySelectorAll(selector) {
      // The picker queries either one input name or the combined pair; filtering
      // by the requested name keeps the stand-in honest about selector scope.
      return [...projectInputs, globalInput].filter(input => selector.includes(`name="${input.name}"`));
    },
  };
  return {container, selected, warning, mode, projectInputs, globalInput, selectAll, clear, inputs: [...projectInputs, globalInput]};
}

test("empty vault selection is exactly zero project spaces with global off", () => {
  // The warning predicate is shared by the initial render and the live updates.
  assert.equal(channels.channelGroupKnowledgeIsEmpty(0, false), true);
  assert.equal(channels.channelGroupKnowledgeIsEmpty(1, false), false);
  assert.equal(channels.channelGroupKnowledgeIsEmpty(0, true), false);
  assert.equal(channels.channelGroupKnowledgeIsEmpty(1, true), false);
});

test("vault picker shows the empty warning on load when nothing is selected", () => {
  const picker = fakePicker();
  picker.projectInputs.forEach(input => { input.checked = false; });
  channels.bindChannelKnowledgeScope(picker.container);
  assert.equal(picker.warning.hidden, false, "empty selection with global off must warn");
});

test("vault warning follows project and global checkboxes live", () => {
  const picker = fakePicker();
  picker.projectInputs.forEach(input => { input.checked = false; });
  channels.bindChannelKnowledgeScope(picker.container);
  assert.equal(picker.warning.hidden, false);

  picker.projectInputs[0].checked = true;
  picker.projectInputs[0].listeners.change();
  assert.equal(picker.warning.hidden, true, "one selected project space is enough");

  picker.projectInputs[0].checked = false;
  picker.projectInputs[0].listeners.change();
  assert.equal(picker.warning.hidden, false, "clearing the last project space warns again");

  picker.globalInput.checked = true;
  picker.globalInput.listeners.change();
  assert.equal(picker.warning.hidden, true, "global knowledge alone is a readable selection");

  picker.globalInput.checked = false;
  picker.globalInput.listeners.change();
  assert.equal(picker.warning.hidden, false);
});

test("vault clear shows the warning and select-all hides it", () => {
  const picker = fakePicker();
  channels.bindChannelKnowledgeScope(picker.container);
  picker.clear.listeners.click();
  assert.equal(picker.warning.hidden, false, "clear leaves the group with no knowledge");
  picker.selectAll.listeners.click();
  assert.equal(picker.warning.hidden, true, "select-all restores a readable selection");
});

test("vault picker keeps the project checkbox list visible without a scope select", () => {
  const picker = fakePicker();
  channels.bindChannelKnowledgeScope(picker.container);
  assert.equal(picker.selected.hidden, false, "vault project choices must not be hidden");
});

test("vault select-all and clear cover projects and global together", () => {
  const picker = fakePicker();
  channels.bindChannelKnowledgeScope(picker.container);
  picker.selectAll.listeners.click();
  assert.ok(picker.inputs.every(input => input.checked), "select-all must check global and every project");
  picker.clear.listeners.click();
  assert.ok(picker.inputs.every(input => !input.checked), "clear must uncheck global and every project");
});

test("legacy mode select still hides and shows the checkbox list", () => {
  const picker = fakePicker({modeValue: "all"});
  channels.bindChannelKnowledgeScope(picker.container);
  assert.equal(picker.selected.hidden, true);
  picker.mode.value = "selected";
  picker.mode.listeners.change();
  assert.equal(picker.selected.hidden, false);
});
