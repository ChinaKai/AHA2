import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const backends = await import(pathToFileURL(resolve(root, "dist", "backends.js")));

test("supported backend catalog covers claude, codex and dsh", () => {
  const catalog = backends.SUPPORTED_BACKENDS;
  assert.deepEqual(catalog.map(item => item.id), ["claude", "codex", "dsh"]);
  assert.deepEqual(catalog.map(item => item.name), ["Claude Code", "Codex CLI", "DeepSeek Harness"]);
  // Product-shape gate: adding a backend or wire protocol must be a deliberate
  // change here, not just a new row in the catalog.
  assert.deepEqual(backends.MODEL_WIRE_OPTIONS.map(item => item.value), ["responses", "chat_completions", "anthropic_messages"]);

  const byID = Object.fromEntries(catalog.map(item => [item.id, item]));
  assert.deepEqual(byID.claude.protocols.map(item => item.wire), ["anthropic_messages"]);
  assert.deepEqual(byID.codex.protocols.map(item => item.wire), ["responses", "chat_completions"]);
  assert.deepEqual(byID.dsh.protocols.map(item => item.wire), ["responses", "chat_completions", "anthropic_messages"]);
  assert.equal(byID.dsh.docURL, "https://deepseek-harness.github.io/deepseek-harness/");
  for (const item of catalog) {
    assert.ok(item.tagline.length > 0, `${item.id} needs a one-line description`);
    assert.match(item.docURL, /^https:\/\//);
  }
});

test("backend catalog carries no install commands or platform labels", () => {
  // Install commands belong in the linked docs, not in the UI catalog. Keeping
  // these fields would be dead data the card renderer must never read again.
  const catalog = backends.SUPPORTED_BACKENDS;
  for (const item of catalog) {
    assert.ok(!("installs" in item), `${item.id} must not carry install commands`);
    assert.ok(!("requirement" in item), `${item.id} must not carry a runtime requirement for the card`);
  }
  assert.doesNotMatch(JSON.stringify(catalog), /irm https:|curl -fsSL|npx @deepseek-ai/);
  assert.doesNotMatch(JSON.stringify(catalog), /Windows PowerShell|macOS \/ Linux/);
});

test("backends panel renders names, identifiers, protocols and safe documentation links", () => {
  const html = backends.backendsPanelHTML();
  assert.match(html, /AHA 支持的 Backend/);
  assert.match(html, /Agent 运行时，不是 Provider 或模型协议/);

  const expected = [
    {
      id: "claude", name: "Claude Code", tagline: "Anthropic 官方 agent CLI", protocols: ["Anthropic Messages"],
      href: "https://code.claude.com/docs/en/overview",
    },
    {
      id: "codex", name: "Codex CLI", tagline: "OpenAI 官方 agent CLI", protocols: ["Responses", "Chat Completions"],
      href: "https://learn.chatgpt.com/docs/codex/cli",
    },
    {
      id: "dsh", name: "DeepSeek Harness", tagline: "DeepSeek Harness（DSH）agent 运行时", protocols: ["Responses", "Chat Completions", "Anthropic Messages"],
      href: "https://deepseek-harness.github.io/deepseek-harness/",
    },
  ];
  for (const item of expected) {
    assert.match(html, new RegExp(`data-backend-id="${item.id}"`), `${item.id} card missing`);
    assert.match(html, new RegExp(`<strong>${item.name}</strong>`), `${item.name} display name missing`);
    assert.match(html, new RegExp(`class="backend-identifier">${item.id}<`), `${item.id} identifier missing`);
    assert.match(html, new RegExp(item.tagline), `${item.id} tagline missing`);
    for (const protocol of item.protocols) {
      assert.match(html, new RegExp(`>${protocol}<`), `${item.id} protocol ${protocol} missing`);
    }
    assert.match(
      html,
      new RegExp(`<a class="backend-doc-link" href="${item.href.replaceAll(".", "\\.")}" target="_blank" rel="noopener noreferrer">官方文档</a>`),
      `${item.id} documentation link must open in a new tab with rel protection`,
    );
  }
  assert.equal([...html.matchAll(/class="backend-doc-link"/g)].length, expected.length, "every card keeps exactly one documentation link");

  // Cards show identity, positioning copy, protocol tags and the doc link only:
  // installation commands and platform labels must not come back.
  assert.doesNotMatch(html, /data-platform=|class="backend-install|运行要求/);
  assert.doesNotMatch(html, /Windows PowerShell|macOS \/ Linux/);
  assert.doesNotMatch(html, /irm\s+https:/);
  assert.doesNotMatch(html, /curl -fsSL/);
  assert.doesNotMatch(html, /npx @deepseek-ai/);
  assert.doesNotMatch(html, /npm install -g/);

  // The panel is display-only: no images, scripts, fetches, or credential fields.
  assert.doesNotMatch(html, /<img|<script|<iframe|fetch\(|XMLHttpRequest|EventSource/);
  assert.doesNotMatch(html, /api[_-]?key|password|secret|token=/i);
  assert.doesNotMatch(html, /http:\/\//);
});

test("rendering the panel performs no network access", () => {
  const previous = {
    fetch: globalThis.fetch,
    XMLHttpRequest: globalThis.XMLHttpRequest,
    EventSource: globalThis.EventSource,
  };
  const forbidden = () => { throw new Error("network access during backends render"); };
  globalThis.fetch = forbidden;
  globalThis.XMLHttpRequest = class { constructor() { forbidden(); } };
  globalThis.EventSource = class { constructor() { forbidden(); } };
  try {
    const html = backends.backendsPanelHTML();
    assert.match(html, /Claude Code/);
    assert.match(html, /deepseek-harness\.github\.io/);
  } finally {
    for (const key of Object.keys(previous)) {
      if (previous[key] === undefined) delete globalThis[key];
      else globalThis[key] = previous[key];
    }
  }
});

test("runtime wire matrix and backend dropdowns derive from the catalog", async () => {
  const catalogIDs = backends.SUPPORTED_BACKENDS.map(item => item.id).sort();
  const options = backends.MODEL_WIRE_OPTIONS;

  // Bidirectional set check: an unknown backend added to the runtime matrix
  // (or a catalog entry missing from it) must fail.
  assert.deepEqual([...new Set(options.flatMap(item => item.backends))].sort(), catalogIDs);
  for (const option of options) {
    for (const backend of option.backends) {
      assert.ok(catalogIDs.includes(backend), `${option.value} lists unknown backend ${backend}`);
    }
  }
  for (const backend of backends.SUPPORTED_BACKENDS) {
    const declared = backend.protocols.map(item => item.wire).sort();
    const fromRuntime = options.filter(item => item.backends.includes(backend.id)).map(item => item.value).sort();
    assert.deepEqual(declared, fromRuntime, `${backend.id} protocol tags drifted from MODEL_WIRE_OPTIONS`);
  }

  const selectHTML = backends.backendSelectOptionsHTML();
  assert.deepEqual([...selectHTML.matchAll(/<option value="([a-z]+)">([^<]+)<\/option>/g)].map(match => [match[1], match[2]]),
    backends.SUPPORTED_BACKENDS.map(backend => [backend.id, backend.name]));

  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /import \{[^}]*MODEL_WIRE_OPTIONS[^}]*backendSelectOptionsHTML[^}]*backendsPanelHTML[^}]*\} from "\.\/backends\.js"/);
  assert.doesNotMatch(main, /const MODEL_WIRE_OPTIONS/);
  assert.equal((main.match(/\$\{backendSelectOptionsHTML\(\)\}/g) || []).length, 2);
  assert.doesNotMatch(main, /<option value="dsh">/);
  assert.doesNotMatch(main, /<option value="codex">/);

  // Runtime pickers use the same full display names instead of a second table.
  const picker = await import(pathToFileURL(resolve(root, "dist", "runtime_picker.js")));
  assert.equal(picker.backendLabel("claude"), "Claude Code");
  assert.equal(picker.backendLabel("codex"), "Codex CLI");
  assert.equal(picker.backendLabel("dsh"), "DeepSeek Harness");
  assert.equal(picker.backendLabel("future"), "future");
});

test("workspace backend options derive from catalog membership", async () => {
  const {SUPPORTED_BACKENDS, SUPPORTED_BACKEND_IDS, resolveBackendOptions} = backends;
  assert.deepEqual([...SUPPORTED_BACKEND_IDS].sort(), SUPPORTED_BACKENDS.map(item => item.id).sort());

  const allReady = Object.fromEntries(SUPPORTED_BACKEND_IDS.map(id => [id, {status: "ready"}]));
  assert.deepEqual(resolveBackendOptions({capabilities: allReady, detected: true, presentBackends: [], hasCodexAccount: false}), SUPPORTED_BACKEND_IDS);

  // A workspace that was detected but has nothing ready offers no backend.
  assert.deepEqual(resolveBackendOptions({capabilities: {}, detected: true, presentBackends: ["dsh"], hasCodexAccount: true}), []);

  // Every catalog backend, including one only present in the catalog, is
  // selectable when its detection says ready.
  for (const id of SUPPORTED_BACKEND_IDS) {
    assert.deepEqual(
      resolveBackendOptions({capabilities: {[id]: {status: "ready"}}, detected: true, presentBackends: [], hasCodexAccount: false}),
      [id],
      `${id} must survive workspace backend derivation`,
    );
  }

  // Before detection the fallback is configured models plus the Codex account;
  // unknown backend ids never leak into the picker.
  assert.deepEqual(
    resolveBackendOptions({detected: false, presentBackends: ["dsh", "ghost-backend"], hasCodexAccount: true}),
    SUPPORTED_BACKEND_IDS.filter(id => id === "codex" || id === "dsh"),
  );

  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /resolveBackendOptions\(/);
  assert.doesNotMatch(main, /caps\.codex\?\.status/);
  assert.doesNotMatch(main, /caps\.claude\?\.status/);
  assert.doesNotMatch(main, /caps\.dsh\?\.status/);
  assert.doesNotMatch(main, /\["codex", "claude", "dsh"\]/);
});

test("edit protocol dropdown derives from catalog and maps chat to persisted values", async () => {
  const {MODEL_WIRE_EDIT_OPTIONS, MODEL_WIRE_OPTIONS, modelWireEditOptionsHTML, persistedWireValue, runtimeWireValue} = backends;
  assert.deepEqual(
    MODEL_WIRE_EDIT_OPTIONS.map(option => [option.value, option.label, option.runtime]),
    MODEL_WIRE_OPTIONS.map(option => [persistedWireValue(option.value), option.label, option.value]),
  );
  assert.ok(!MODEL_WIRE_EDIT_OPTIONS.some(option => option.value === "chat_completions"), "edit form must persist chat, not chat_completions");

  assert.equal(persistedWireValue("chat_completions"), "chat");
  assert.equal(persistedWireValue("chat"), "chat");
  assert.equal(runtimeWireValue("chat"), "chat_completions");
  for (const option of MODEL_WIRE_EDIT_OPTIONS) {
    assert.equal(persistedWireValue(runtimeWireValue(option.value)), option.value);
  }

  const html = modelWireEditOptionsHTML();
  assert.deepEqual([...html.matchAll(/<option value="([a-z_]+)">([^<]+)<\/option>/g)].map(match => [match[1], match[2]]),
    MODEL_WIRE_EDIT_OPTIONS.map(option => [option.value, option.label]));

  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /\$\{modelWireEditOptionsHTML\(\)\}/);
  assert.doesNotMatch(main, /<option value="chat">/);
  assert.doesNotMatch(main, /<option value="anthropic_messages">/);
  assert.ok((main.match(/persistedWireValue\(/g) || []).length >= 2, "edit open and save must both normalize the wire value");
});

test("models page mounts the supported backends panel before the Models panel", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(app, /import \{[^}]*MODEL_WIRE_OPTIONS[^}]*backendsPanelHTML[^}]*\} from "\.\/backends\.js\?v=/);

  // Compare positions in the rendered models page markup, not just string
  // presence: the backends panel must come before the Models list and its
  // "add model" button so a long model list cannot push it out of view.
  const viewStart = app.indexOf("function modelsView()");
  const viewEnd = app.indexOf("function advancedSettingsView()");
  assert.ok(viewStart >= 0 && viewEnd > viewStart, "modelsView must exist in the built app");
  const view = app.slice(viewStart, viewEnd);
  assert.equal((view.match(/\$\{backendsPanelHTML\(\)\}/g) || []).length, 1, "models page must mount the backends panel exactly once");
  const page = view.replace("${backendsPanelHTML()}", backends.backendsPanelHTML());

  const panelAt = page.indexOf('id="supported-backends"');
  const modelsAt = page.indexOf("<strong>Models</strong>");
  const addModelAt = page.indexOf("添加模型</button>");
  assert.ok(panelAt >= 0, "backends panel missing from rendered models page");
  assert.ok(modelsAt >= 0 && modelsAt > panelAt, "backends panel must render before the Models panel");
  assert.ok(addModelAt >= 0 && addModelAt > panelAt, "backends panel must render before the add-model button");
  assert.ok(panelAt < page.indexOf('class="provider-layout"'), "backends panel must render above the provider/models layout");
});
