import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const module = await import(pathToFileURL(resolve(root, "build", "model_source_picker.js")));

const providerModels = [
  {id: "model-provider", display_name: "Provider Claude", provider_name: "Local", source: "provider", backend: "claude", wire_model: "claude-provider"},
];
const accounts = [
  {
    id: "account-ready", label: "Ready Account", status: "ready", credential_configured: true,
    available_models: [{wire_model: "gpt-ready", display_name: "GPT Ready"}],
  },
  {
    id: "account-expired", label: "Expired Account", status: "expired", credential_configured: true,
    available_models: [{wire_model: "gpt-expired", display_name: "GPT Expired"}],
  },
  {
    id: "account-no-credentials", label: "No Credentials", status: "ready", credential_configured: false,
    available_models: [{wire_model: "gpt-anon", display_name: "GPT Anon"}],
  },
];
const claudeReady = {
  availability: "ready",
  models: [{wire_model: "claude-sonnet", display_name: "Sonnet"}],
  message: "ready",
};

test("source picker lists Provider, Codex official account and Claude native", () => {
  const options = module.buildModelSourceOptions(providerModels, accounts, claudeReady, {source: "provider", modelID: "model-provider"});
  assert.deepEqual(options.map(item => item.source), ["provider", "official", "claude_native"]);
  assert.equal(options[0].entries[0].value, "model-provider");
  assert.ok(options[1].entries.some(entry => entry.value === "gpt-ready" && !entry.disabled));
  assert.ok(options[1].entries.some(entry => entry.value === "gpt-expired" && entry.disabled && entry.reason.includes("expired")));
  assert.ok(options[1].entries.some(entry => entry.value === "gpt-anon" && entry.disabled && entry.reason.length > 0));
  assert.ok(options[2].entries.some(entry => entry.value === "claude-sonnet" && !entry.disabled));
});

test("source picker keeps an unavailable saved selection instead of falling back", () => {
  const options = module.buildModelSourceOptions(providerModels, accounts, {availability: "not_logged_in", models: [], message: "not signed in"}, {
    source: "claude_native", wireModel: "claude-opus",
  });
  const native = options.find(item => item.source === "claude_native");
  assert.equal(native.entries.length, 1);
  assert.equal(native.entries[0].value, "claude-opus");
  assert.equal(native.entries[0].disabled, true);
  assert.match(native.entries[0].reason, /not signed in/);

  const html = module.modelSourceFieldsHTML("probe", options, {source: "claude_native", wireModel: "claude-opus"});
  assert.match(html, /value="claude_native" selected/);
  assert.match(html, /value="claude-opus" selected disabled/);
  assert.doesNotMatch(html, /value="model-provider" selected/);
});

test("official saved account and model stay selected when the account is unavailable", () => {
  const html = module.modelSourceFieldsHTML("probe", module.buildModelSourceOptions(providerModels, accounts, claudeReady, {
    source: "official", wireModel: "gpt-expired", accountID: "account-expired",
  }), {source: "official", wireModel: "gpt-expired", accountID: "account-expired"});
  assert.match(html, /value="official" selected/);
  assert.match(html, /value="gpt-expired" data-account-id="account-expired" selected disabled/);
  assert.equal((html.match(/ selected/g) || []).length, 2);
});

test("unknown source values normalize to provider", () => {
  assert.equal(module.normalizeModelSource("env"), "provider");
  assert.equal(module.normalizeModelSource("codex_official"), "official");
  assert.equal(module.normalizeModelSource("claude_native"), "claude_native");
  assert.equal(module.normalizeModelSource("mystery"), "provider");
});
