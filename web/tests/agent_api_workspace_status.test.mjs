// SSH stdio is no longer an opt-in switch: the Workspace status must name it,
// and the Advanced Settings page must no longer render a checkbox for it. The
// manual override stays as the only troubleshooting field.
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {renderWorkspaceDetection} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));

function workspace(overrides = {}) {
  return {
    id: "ws-1",
    project_id: "project-1",
    name: "builder",
    locality: "remote",
    transport: "ssh",
    health: "ready",
    agent_api_status: "ready",
    agent_api_resolved_url: "http://127.0.0.1:8766",
    capabilities: {agent_api: {status: "ready", via: "stdio", url: "http://127.0.0.1:8766", runtime: "node"}},
    repository: {},
    ...overrides,
  };
}

test("an SSH stdio workspace is labelled as such", () => {
  const html = renderWorkspaceDetection(workspace());
  assert.match(html, /Agent API · SSH stdio/, "the stdio transport is not named");
  assert.doesNotMatch(html, /Agent API · 直连/, "stdio was reported as a direct connection");
});

test("the tunnel and direct labels are unchanged", () => {
  const tunnel = renderWorkspaceDetection(workspace({capabilities: {agent_api: {status: "ready", via: "tunnel", url: "http://127.0.0.1:8766"}}}));
  assert.match(tunnel, /Agent API · SSH 反向隧道已连通/);
  const direct = renderWorkspaceDetection(workspace({transport: "native", capabilities: {agent_api: {status: "ready", via: "direct", url: "https://aha.example.test"}}}));
  assert.match(direct, /Agent API · 直连 https:\/\/aha\.example\.test/);
});

test("the settings page renders no stdio switch and saves only the override", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.doesNotMatch(app, /name="stdio_proxy"/, "the removed stdio switch is still rendered");
  assert.doesNotMatch(app, /formCheckbox\(form, "stdio_proxy"\)/, "the settings save still reads a removed field");
  assert.doesNotMatch(app, /stdio_proxy: stdioProxy/, "the settings save still sends a removed field");
  assert.match(app, /updateAgentAPISettings\(\{\s*url\s*\}\)/, "the settings save no longer sends just the override");
});

test("a direct load of the other settings view fetches the agent api settings", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  // The Agent API panel lives on "other", but only the "advanced" view used to
  // load its data. A refresh straight into #other therefore rendered defaults
  // and never asked the server.
  assert.match(app, /case "other":\s*return \[\s*"advanced"\s*\];/,
    "the other settings view no longer maps to the advanced resource");
  assert.match(app, /if \(resourcesForView\(view\)\.includes\("advanced"\)\) jobs\.push\(loadResource\("advanced"/,
    "ensureViewData no longer loads advanced settings for every view that renders them");
  assert.match(app, /state\.agentAPISettings = agentAPI\.agent_api/,
    "the agent api response is no longer stored");
});
