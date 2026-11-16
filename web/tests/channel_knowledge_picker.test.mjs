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
  return {name, checked, addEventListener() {}};
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
  const mode = modeValue === undefined ? null : {value: modeValue, listeners: {}, addEventListener(type, handler) { this.listeners[type] = handler; }};
  const projectInputs = [fakeInput("knowledge_entry_id", true), fakeInput("knowledge_entry_id", false)];
  const globalInput = fakeInput("include_global", false);
  const selectAll = fakeButton();
  const clear = fakeButton();
  const container = {
    querySelector(selector) {
      if (selector === 'select[name="knowledge_scope_mode"]') return mode;
      if (selector === "[data-channel-knowledge-selected]") return selected;
      if (selector === "[data-channel-policy-select-all]") return selectAll;
      if (selector === "[data-channel-policy-clear]") return clear;
      return null;
    },
    querySelectorAll() {
      return [...projectInputs, globalInput];
    },
  };
  return {container, selected, mode, projectInputs, globalInput, selectAll, clear, inputs: [...projectInputs, globalInput]};
}

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
