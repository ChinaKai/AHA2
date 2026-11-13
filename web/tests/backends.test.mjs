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
    assert.ok(item.installs.length > 0, `${item.id} needs at least one install command`);
  }
});

test("install commands are labelled by platform and follow the official docs", () => {
  const byID = Object.fromEntries(backends.SUPPORTED_BACKENDS.map(item => [item.id, item]));
  assert.deepEqual(byID.claude.installs, [
    {platform: "Windows PowerShell", command: "irm https://claude.ai/install.ps1 | iex"},
    {platform: "macOS / Linux / WSL", command: "curl -fsSL https://claude.ai/install.sh | bash"},
  ]);
  assert.deepEqual(byID.codex.installs, [
    {platform: "macOS / Linux", command: "curl -fsSL https://chatgpt.com/codex/install.sh | sh"},
  ]);
  assert.equal(byID.dsh.requirement, "Node.js >= 24.9.0");
  assert.deepEqual(byID.dsh.installs, [
    {platform: "任意平台", command: "npx @deepseek-ai/dsh web"},
  ]);
  // The retired npm global install must not come back as the documented command.
  assert.doesNotMatch(JSON.stringify(byID.dsh.installs), /npm install -g @deepseek-ai\/dsh/);
});

test("backends panel renders names, identifiers, platforms, protocols and safe documentation links", () => {
  const html = backends.backendsPanelHTML();
  assert.match(html, /AHA 支持的 Backend/);
  assert.match(html, /Agent 运行时，不是 Provider 或模型协议/);

  const expected = [
    {
      id: "claude", name: "Claude Code", tagline: "Anthropic 官方 agent CLI", protocols: ["Anthropic Messages"],
      href: "https://code.claude.com/docs/en/overview",
      installs: [["Windows PowerShell", "irm https://claude.ai/install.ps1 | iex"], ["macOS / Linux / WSL", "curl -fsSL https://claude.ai/install.sh | bash"]],
    },
    {
      id: "codex", name: "Codex CLI", tagline: "OpenAI 官方 agent CLI", protocols: ["Responses", "Chat Completions"],
      href: "https://learn.chatgpt.com/docs/codex/cli",
      installs: [["macOS / Linux", "curl -fsSL https://chatgpt.com/codex/install.sh | sh"]],
    },
    {
      id: "dsh", name: "DeepSeek Harness", tagline: "DeepSeek Harness（DSH）agent 运行时", protocols: ["Responses", "Chat Completions", "Anthropic Messages"],
      href: "https://deepseek-harness.github.io/deepseek-harness/",
      installs: [["任意平台", "npx @deepseek-ai/dsh web"]],
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
    for (const [platform, command] of item.installs) {
      assert.match(html, new RegExp(`data-platform="${platform}"`), `${item.id} platform label ${platform} missing`);
      assert.ok(html.includes(command), `${item.id} install command missing`);
    }
    assert.match(
      html,
      new RegExp(`<a class="backend-doc-link" href="${item.href.replaceAll(".", "\\.")}" target="_blank" rel="noopener noreferrer">官方文档</a>`),
      `${item.id} documentation link must open in a new tab with rel protection`,
    );
  }
  assert.match(html, /运行要求<\/span><code>Node\.js &gt;= 24\.9\.0<\/code>/);

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

test("models page mounts the supported backends panel", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(app, /import \{[^}]*MODEL_WIRE_OPTIONS[^}]*backendsPanelHTML[^}]*\} from "\.\/backends\.js\?v=/);
  assert.match(app, /\$\{backendsPanelHTML\(\)\}/);
});
