// 0.5.546 regression: the pet settings host used to share the id generated for
// the picker's source <select>. document.querySelector("#pet-model-source")
// then returned the host div, so the change listener landed on the div and the
// readback fell back to Provider (a div has no value). The channel page does
// not collide, which is why it kept working.
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const picker = await import(pathToFileURL(resolve(root, "build", "model_source_picker.js")).href);
const assistant = await readFile(resolve(root, "build", "assistant.js"), "utf8");

const providerModels = [
  {id: "model-provider", display_name: "Provider Claude", provider_name: "Local", source: "provider", backend: "claude", wire_model: "claude-provider"},
];
const accounts = [
  {
    id: "account-ready", label: "Ready Account", status: "ready", credential_configured: true,
    available_models: [{wire_model: "gpt-ready", display_name: "GPT Ready"}],
  },
];
const claudeReady = {
  availability: "ready",
  models: [{wire_model: "claude-sonnet", display_name: "Sonnet"}],
  message: "ready",
};

function settingsContract() {
  const hostID = assistant.match(/settingsPetModelHost = layer\.querySelector\("#([^"]+)"\)/)?.[1];
  const renderPrefix = assistant.match(/modelSourceFieldsHTML\("([^"]+)", options, selection\)/)?.[1];
  const bindPrefix = assistant.match(/bindModelSourceFields\("([^"]+)", options\)/)?.[1];
  const readPrefix = assistant.match(/readModelSourceSelection\("([^"]+)"\)/)?.[1];
  assert.ok(hostID, "pet settings host id must be queryable from the built assistant");
  assert.ok(renderPrefix, "pet settings must render the shared model source picker");
  return {hostID, renderPrefix, bindPrefix, readPrefix};
}

// The picker reads document.querySelector("#<prefix>-source") etc. This stand-in
// mirrors a browser's id lookup: the first node in document order wins, so a
// colliding host div shadows the real select exactly like the regression did.
function installFakeDocument(nodes) {
  const previous = globalThis.document;
  globalThis.document = {
    querySelector(selector) {
      if (!selector.startsWith("#")) return null;
      const id = selector.slice(1);
      return nodes.find(node => node.id === id) || null;
    },
  };
  return () => {
    if (previous === undefined) delete globalThis.document;
    else globalThis.document = previous;
  };
}

function parseOptions(html) {
  const options = [];
  const pattern = /<option\b([^>]*)>([\s\S]*?)<\/option>/g;
  let match;
  while ((match = pattern.exec(html))) {
    const attrs = match[1];
    options.push({
      value: attrs.match(/\bvalue="([^"]*)"/)?.[1] ?? "",
      label: match[2],
      dataset: {accountId: attrs.match(/\bdata-account-id="([^"]*)"/)?.[1] ?? ""},
      selected: /(^|\s)selected(\s|$)/.test(attrs),
      disabled: /(^|\s)disabled(\s|$)/.test(attrs),
    });
  }
  return options;
}

class FakeSelect {
  constructor(id) {
    this.id = id;
    this.options = [];
    this.selectedOptions = [];
    this.disabled = false;
    this.listeners = new Map();
    this._value = "";
    this._innerHTML = "";
  }

  addEventListener(type, handler) {
    const handlers = this.listeners.get(type) || [];
    handlers.push(handler);
    this.listeners.set(type, handlers);
  }

  dispatch(type) {
    for (const handler of this.listeners.get(type) || []) handler();
  }

  set innerHTML(html) {
    this._innerHTML = html;
    this.options = parseOptions(html);
    const selected = this.options.find(option => option.selected) || this.options[0];
    this.selectedOptions = selected ? [selected] : [];
    this._value = selected?.value ?? "";
  }

  get innerHTML() {
    return this._innerHTML;
  }

  get value() {
    return this._value;
  }

  set value(next) {
    this._value = String(next ?? "");
    const selected = this.options.find(option => option.value === this._value);
    this.selectedOptions = selected ? [selected] : [];
  }
}

class FakeHost {
  constructor(id) {
    this.id = id;
    this.listeners = new Map();
    this._innerHTML = "";
  }

  addEventListener(type, handler) {
    const handlers = this.listeners.get(type) || [];
    handlers.push(handler);
    this.listeners.set(type, handlers);
  }

  set innerHTML(html) {
    this._innerHTML = html;
  }

  get innerHTML() {
    return this._innerHTML;
  }
}

function selectInnerHTML(pickerHTML, id) {
  const escaped = id.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return pickerHTML.match(new RegExp(`<select id="${escaped}"[^>]*>([\\s\\S]*?)</select>`))?.[1] ?? "";
}

test("pet settings host id never collides with the picker field ids", () => {
  const {hostID, renderPrefix, bindPrefix, readPrefix} = settingsContract();
  assert.equal(bindPrefix, renderPrefix, "render and bind must use the same picker prefix");
  assert.equal(readPrefix, renderPrefix, "render and readback must use the same picker prefix");
  for (const suffix of ["source", "model", "help"]) {
    assert.notEqual(hostID, `${renderPrefix}-${suffix}`, `host id collides with #${renderPrefix}-${suffix}`);
  }
  assert.ok(assistant.includes(`id="${hostID}"`), "the settings host div must use the queried id");
  for (const suffix of ["source", "model", "help"]) {
    assert.ok(!assistant.includes(`id="${renderPrefix}-${suffix}"`), `assistant must not render a static #${renderPrefix}-${suffix}`);
  }
});

test("switching the source updates the model list and readback on the settings page", () => {
  const {hostID, renderPrefix} = settingsContract();
  const selection = {source: "provider", modelID: "model-provider"};
  const options = picker.buildModelSourceOptions(providerModels, accounts, claudeReady, selection);
  const pickerHTML = picker.modelSourceFieldsHTML(renderPrefix, options, selection);

  const host = new FakeHost(hostID);
  const sourceSelect = new FakeSelect(`${renderPrefix}-source`);
  const modelSelect = new FakeSelect(`${renderPrefix}-model`);
  const help = {id: `${renderPrefix}-help`, textContent: ""};
  sourceSelect.innerHTML = selectInnerHTML(pickerHTML, `${renderPrefix}-source`);
  modelSelect.innerHTML = selectInnerHTML(pickerHTML, `${renderPrefix}-model`);
  host.innerHTML = pickerHTML;

  const restore = installFakeDocument([host, sourceSelect, modelSelect, help]);
  try {
    picker.bindModelSourceFields(renderPrefix, options);
    assert.ok(sourceSelect.listeners.get("change")?.length, "the change listener must be bound to the real <select>, not the host div");

    // Codex official account: the list must switch to that account's models.
    sourceSelect.value = "official";
    sourceSelect.dispatch("change");
    assert.match(modelSelect.innerHTML, /value="gpt-ready"/);
    assert.doesNotMatch(modelSelect.innerHTML, /value="model-provider"/);
    assert.deepEqual(picker.readModelSourceSelection(renderPrefix), {
      model_source: "official", model_id: "", wire_model: "gpt-ready", codex_account_id: "account-ready",
    });

    // Back to Provider.
    sourceSelect.value = "provider";
    sourceSelect.dispatch("change");
    assert.match(modelSelect.innerHTML, /value="model-provider"/);
    assert.deepEqual(picker.readModelSourceSelection(renderPrefix), {
      model_source: "provider", model_id: "model-provider", wire_model: "", codex_account_id: "",
    });

    // And to Claude native.
    sourceSelect.value = "claude_native";
    sourceSelect.dispatch("change");
    assert.match(modelSelect.innerHTML, /value="claude-sonnet"/);
    assert.deepEqual(picker.readModelSourceSelection(renderPrefix), {
      model_source: "claude_native", model_id: "", wire_model: "claude-sonnet", codex_account_id: "",
    });
  } finally {
    restore();
  }
});
