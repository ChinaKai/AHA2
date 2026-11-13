// Behavior-level verification for the Task detail Agent config dialog.
// The unit tests pin the shared markup and helpers; this harness drives the
// real built bundle so unsaved-close, cancel, and save are exercised against
// actual event handlers and PATCH requests.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = process.env.AHA2_WEB_VALIDATION_DIR || resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const useInstalledBrowser = process.env.AHA2_PLAYWRIGHT_CHROMIUM === "1";

const TASK = {
  id: "t-active",
  code: "task-001",
  title: "Running work",
  original_request: "",
  current_goal: "",
  status: "active",
  project_id: "project-1",
  workspace_id: "workspace-1",
  collaboration_mode: "single",
  max_agents: 2,
  knowledge_policy: "disabled",
  skill_ids: ["skill-b"],
  mcp_server_ids: [],
  agent_capabilities: {workspace_read: false, task_create: false, clone_hardware: false},
  total_tokens: 0,
  created_at: "",
  updated_at: "",
};
const AGENT = {
  task_id: "t-active",
  agent_id: "main",
  role: "main",
  status: "idle",
  title: "Main",
  backend: "codex",
  model_source: "provider",
  model_id: "model-1",
  model_name: "Stub model",
  wire_model: "stub",
  codex_account_id: "",
  reasoning_effort: "high",
  stream_idle_timeout_ms: 180000,
  stream_max_retries: 3,
  filesystem: "read-only",
  approval: "auto",
  proxy_enabled: true,
  fast_mode: false,
  runtime_config_valid: true,
  inherit_main: false,
  unread_count: 0,
  created_at: "",
  updated_at: "",
};
const SKILLS = [
  {id: "skill-a", name: "Alpha skill", scope: "global", description: "first", enabled: true, status: "active"},
  {id: "skill-b", name: "Beta skill", scope: "project", project_id: "project-1", description: "second", enabled: true, status: "active"},
];
const MCP = {
  id: "mcp-1",
  name: "Tools",
  description: "Tool set",
  transport: "http",
  enabled: true,
  builtin: false,
  auto_inject: false,
  created_at: "",
  updated_at: "",
};

const bootstrap = `
window.__patches = [];
window.__confirms = [];
window.__confirmAnswer = false;
window.confirm = message => {
  window.__confirms.push(String(message));
  return Boolean(window.__confirmAnswer);
};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const AGENT = ${JSON.stringify(AGENT)};
const SKILLS = ${JSON.stringify(SKILLS)};
const MCP = ${JSON.stringify(MCP)};
const detail = () => ({
  task: {...TASK},
  turns: [],
  agents: [{...AGENT}],
  latest_round: null,
  hardware: [],
  memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") return json({tasks: [TASK], has_more: false, counts: {}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", channel_retired: false, knowledge_policy: "enabled"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", health: "ready", read_only: false, capabilities: {codex: {status: "ready"}}}]});
  if (path === "/api/v1/skills") return json({skills: SKILLS});
  if (path === "/api/v1/mcp-servers") return json({servers: [MCP]});
  if (path === "/api/v1/models") return json({models: [{id: "model-1", display_name: "Stub model", provider_id: "provider-1", source: "provider", backend: "codex", wire_model: "stub", default_env_group_id: "env-1", created_at: new Date().toISOString(), updated_at: new Date().toISOString()}]});
  if (path === "/api/v1/providers") return json({providers: [{id: "provider-1", name: "Provider", auth_style: "none"}]});
  if (path === "/api/v1/env-groups") return json({env_groups: [{id: "env-1", name: "Env", provider_id: "provider-1", backend: "stub", revision: 1, environment: {}, secret_refs: {}}]});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path === "/api/v1/tasks/t-active" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-active/channel-routes" && method === "GET") return json({destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-active" && method === "PATCH") {
    const body = JSON.parse(init.body || "{}");
    window.__patches.push({method, path, body});
    return json({task: {...TASK, ...body}});
  }
  if (path === "/api/v1/tasks/t-active/agents/main" && method === "PATCH") {
    const body = JSON.parse(init.body || "{}");
    window.__patches.push({method, path, body});
    return json({agent: {...AGENT, ...body}});
  }
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks"}));
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
  if (useInstalledBrowser) {
    browser = await chromium.launch({headless: true});
  } else {
    const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
    browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  }
  const page = await browser.newPage({viewport: {width: 1280, height: 1000}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.stack || error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector(".task-card", {timeout: 15000});
  await page.locator('.task-card[data-task="t-active"]').click();
  await page.waitForSelector('[data-agent-config="main"]', {timeout: 10000});

  const openDialog = async () => {
    await page.locator('[data-agent-config="main"]').click();
    await page.waitForSelector("#agent-config-dialog[open]", {timeout: 5000});
  };
  const patches = () => page.evaluate(() => window.__patches);
  const confirms = () => page.evaluate(() => window.__confirms);
  const isOpen = () => page.locator("#agent-config-dialog").evaluate(dialog => dialog.open);

  // 1. An edit followed by Esc asks first. Dismissing keeps the dialog and its
  //    edit in place and still writes nothing.
  await openDialog();
  assert.equal(await page.locator("#agent-config-capabilities-fold").count(), 0, "the project-level authorization module is still rendered");
  assert.equal(await page.locator('#agent-config-form input[name="cap_workspace_read"]').count(), 0, "the capability checkboxes are still rendered");
  await page.selectOption("#agent-config-effort", "low");
  await page.evaluate(() => { window.__confirmAnswer = false; });
  await page.keyboard.press("Escape");
  await page.waitForTimeout(100);
  assert.equal(await isOpen(), true, "rejecting the unsaved prompt closed the dialog");
  assert.equal((await confirms()).length, 1, "Esc did not ask before discarding edits");
  assert.match((await confirms())[0], /Agent 配置尚未保存/);
  assert.equal(await page.locator("#agent-config-effort").inputValue(), "low", "the rejected close lost the edit");
  assert.deepEqual(await patches(), [], "rejecting the unsaved prompt wrote config");

  // 2. Accepting the same prompt closes the dialog without writing.
  await page.evaluate(() => { window.__confirmAnswer = true; });
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("#agent-config-dialog")?.open, null, {timeout: 5000});
  assert.equal((await confirms()).length, 2, "accepting the prompt did not use the same guard");
  assert.deepEqual(await patches(), [], "discarding unsaved config wrote PATCH requests");

  // 3. Reopening and cancelling without another edit asks nothing and writes
  //    nothing. The dialog baselines itself on open, not on page load.
  await openDialog();
  await page.locator("#agent-config-dialog .agent-config-actions [data-close]").click();
  await page.waitForFunction(() => !document.querySelector("#agent-config-dialog")?.open, null, {timeout: 5000});
  assert.equal((await confirms()).length, 2, "a clean cancel asked about unsaved changes");
  assert.deepEqual(await patches(), [], "a clean cancel wrote config");

  // 4. Saving sends the task-level and agent-level PATCH payloads in order,
  //    then closes the dialog.
  await openDialog();
  await page.selectOption("#agent-config-effort", "medium");
  await page.selectOption('#agent-config-form select[name="collaboration_mode"]', "auto");
  await page.fill('#agent-config-form input[name="max_agents"]', "3");
  await page.selectOption('#agent-config-form select[name="knowledge_policy"]', "enabled");
  await page.locator("#agent-config-skills-fold summary").click();
  await page.locator('#agent-config-skills-fold input[value="skill-a"]').setChecked(true);
  await page.locator('#agent-config-skills-fold input[value="skill-b"]').setChecked(false);
  await page.locator("#agent-config-mcp-fold summary").click();
  await page.locator('#agent-config-mcp-fold input[value="mcp-1"]').setChecked(true);
  await page.locator("#agent-config-fast-fold summary").click();
  await page.locator('#agent-config-fast-mode-row input[name="fast_mode"]').setChecked(true);
  await page.locator("#agent-config-form button.primary").click();
  await page.waitForFunction(() => window.__patches.length === 2, null, {timeout: 8000});
  await page.waitForFunction(() => !document.querySelector("#agent-config-dialog")?.open, null, {timeout: 5000});
  const saved = await patches();
  assert.deepEqual(saved.map(call => call.path), ["/api/v1/tasks/t-active", "/api/v1/tasks/t-active/agents/main"]);
  assert.deepEqual(saved.map(call => call.method), ["PATCH", "PATCH"]);
  assert.deepEqual(saved[0].body.skill_ids.sort(), ["skill-a"], "task Skills were not saved");
  assert.deepEqual(saved[0].body.mcp_server_ids, ["mcp-1"], "task MCP tools were not saved");
  assert.equal(saved[0].body.collaboration_mode, "auto", "collaboration mode was not saved");
  assert.equal(saved[0].body.max_agents, 3, "max agents was not saved");
  assert.equal(saved[0].body.knowledge_policy, "enabled", "knowledge policy was not saved");
  assert.equal("agent_capabilities" in saved[0].body, false, "the Agent config PATCH still carries capability grants");
  assert.equal(saved[1].body.inherit_main, false);
  assert.equal(saved[1].body.backend, "codex");
  assert.equal(saved[1].body.reasoning_effort, "medium", "reasoning effort was not saved");
  assert.equal(saved[1].body.fast_mode, true, "Fast mode was not saved");
  assert.equal(saved[1].body.filesystem, "read-only");
  assert.equal((await confirms()).length, 2, "saving should not ask about unsaved changes");
  assert.deepEqual(errors, [], "the Agent config dialog threw while being driven");
  console.log("task Agent config save/cancel/unsaved-close verified in the built Web bundle");
} finally {
  await browser?.close();
  server.close();
}