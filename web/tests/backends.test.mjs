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

  const byID = Object.fromEntries(catalog.map(item => [item.id, item]));
  assert.deepEqual(byID.claude.protocols.map(item => item.wire), ["anthropic_messages"]);
  assert.deepEqual(byID.codex.protocols.map(item => item.wire), ["responses", "chat_completions"]);
  assert.deepEqual(byID.dsh.protocols.map(item => item.wire), ["responses", "chat_completions", "anthropic_messages"]);
  assert.equal(byID.dsh.docURL, "https://deepseek-harness.github.io/deepseek-harness/");
  for (const item of catalog) {
    assert.ok(item.tagline.length > 0, `${item.id} needs a one-line description`);
    assert.match(item.docURL, /^https:\/\//);
    assert.ok(item.installCommand.length > 0, `${item.id} needs an install command`);
  }
});

test("backends panel renders names, identifiers, protocols and safe documentation links", () => {
  const html = backends.backendsPanelHTML();
  assert.match(html, /AHA 支持的 Backend/);
  assert.match(html, /Agent 运行时，不是 Provider 或模型协议/);

  const expected = [
    {id: "claude", name: "Claude Code", tagline: "Anthropic 官方 agent CLI", protocols: ["Anthropic Messages"], href: "https://code.claude.com/docs/en/overview"},
    {id: "codex", name: "Codex CLI", tagline: "OpenAI 官方 agent CLI", protocols: ["Responses", "Chat Completions"], href: "https://learn.chatgpt.com/docs/codex/cli"},
    {id: "dsh", name: "DeepSeek Harness", tagline: "DeepSeek Harness（DSH）agent 运行时", protocols: ["Responses", "Chat Completions", "Anthropic Messages"], href: "https://deepseek-harness.github.io/deepseek-harness/"},
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

  // The panel is display-only: no images, scripts, fetches, or credential fields.
  assert.doesNotMatch(html, /<img|<script|<iframe|fetch\(|XMLHttpRequest|EventSource/);
  assert.doesNotMatch(html, /api[_-]?key|password|secret|token=/i);
  assert.doesNotMatch(html, /http:\/\//);
});

test("catalog protocols match the Models page protocol matrix in main.ts", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const matrix = new Map();
  for (const match of main.matchAll(/\{value: "([a-z_]+)", label: "[^"]+", backends: \[([^\]]*)\]\}/g)) {
    matrix.set(match[1], [...match[2].matchAll(/"([a-z]+)"/g)].map(item => item[1]));
  }
  assert.deepEqual([...matrix.keys()].sort(), ["anthropic_messages", "chat_completions", "responses"]);
  for (const backend of backends.SUPPORTED_BACKENDS) {
    const declared = backend.protocols.map(item => item.wire).sort();
    const fromPage = [...matrix.entries()].filter(([, ids]) => ids.includes(backend.id)).map(([wire]) => wire).sort();
    assert.deepEqual(declared, fromPage, `${backend.id} protocol tags drifted from MODEL_WIRE_OPTIONS`);
  }
});

test("models page mounts the supported backends panel", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(app, /import \{ ?backendsPanelHTML ?\} from "\.\/backends\.js\?v=/);
  assert.match(app, /\$\{backendsPanelHTML\(\)\}/);
});
