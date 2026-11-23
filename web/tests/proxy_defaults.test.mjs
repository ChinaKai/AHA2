import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {renderProxySettings} = await import(pathToFileURL(resolve(root, "build", "proxy_settings.js")));

const managed = {
  configured: false,
  profiles: [],
  url_configured: false,
  nodes: [],
  unsupported_count: 0,
  unsupported_types: [],
  status: "idle",
};

function settings(overrides = {}) {
  return {
    mode: "off",
    http_proxy: "http://127.0.0.1:7897",
    https_proxy: "http://127.0.0.1:7897",
    no_proxy: "localhost,127.0.0.1,::1",
    managed_refresh_interval_minutes: 1440,
    ...overrides,
  };
}

test("an omitted proxy mode renders as off, matching the server default", () => {
  const html = renderProxySettings(settings({mode: ""}), managed);
  assert.match(html, /<option value="off" selected>关闭代理<\/option>/);
  assert.doesNotMatch(html, /<option value="external" selected>/);
  assert.match(html, /id="proxy-external-fields" class="proxy-mode-fields hidden"/);
});

test("an explicit external mode still renders its fields", () => {
  const html = renderProxySettings(settings({mode: "external"}), managed);
  assert.match(html, /<option value="external" selected>现有 HTTP \/ HTTPS 代理<\/option>/);
  assert.doesNotMatch(html, /<option value="off" selected>/);
  assert.match(html, /id="proxy-external-fields" class="proxy-mode-fields"/);
});

test("the initial web state starts on off before the first settings load", async () => {
  const source = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(source, /proxySettings:\s*\{mode:\s*"off"/);
  assert.doesNotMatch(source, /proxySettings:\s*\{mode:\s*"external"/);
});
