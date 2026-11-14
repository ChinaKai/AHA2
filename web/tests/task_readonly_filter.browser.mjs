// Read-only Task message-filter regression.
//
// A read-only mirror used to request chat+update+error while the filter control
// showed 2/4 (chat+error), so update rows appeared without their checkbox and a
// later tool toggle silently dropped update. This fixture records every
// conversation request and asserts that the control's selected categories and
// the requested categories stay identical, including after toggling update and
// tool one at a time.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(
  process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs"),
));
const useInstalledBrowser = Boolean(process.env.AHA_BROWSER_EXECUTABLE);
const binary = useInstalledBrowser
  ? {executablePath: async () => process.env.AHA_BROWSER_EXECUTABLE, args: []}
  : (await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")))).default;
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8"))
  .match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const TASK = {
  id: "task-ro", code: "task-391", title: "Read-only mirror", status: "active", read_only: true,
  owner_device_id: "device-a", project_id: "project-1", workspace_id: "workspace-1",
  collaboration_mode: "single", max_agents: 1, original_request: "work", current_goal: "work",
  task_branch: "", runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-11-13T00:00:00Z", updated_at: "2026-11-13T00:00:00Z",
};

const CONVERSATION = [
  {sequence: 1, id: "ci-chat", task_id: TASK.id, agent_id: "main", category: "chat", kind: "agent_message", summary: "chat row", payload: {}, created_at: "2026-11-13T00:00:00Z"},
  {sequence: 2, id: "ci-update", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_reasoning_update", summary: "update row", payload: {}, created_at: "2026-11-13T00:00:01Z"},
  {sequence: 3, id: "ci-tool", task_id: TASK.id, agent_id: "main", category: "tool", kind: "agent_command_finished", summary: "tool row", payload: {command: "ls"}, created_at: "2026-11-13T00:00:02Z"},
  {sequence: 4, id: "ci-error", task_id: TASK.id, agent_id: "main", category: "error", kind: "agent_error", summary: "error row", payload: {}, created_at: "2026-11-13T00:00:03Z"},
];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const CONVERSATION = ${JSON.stringify(CONVERSATION)};
window.__requests = [];
const detail = () => ({
  task: TASK, turns: [], latest_round: null,
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  memory: {}, hardware: [], event_cursor: 0, server_time_ms: Date.now(),
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks/task-ro" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/task-ro/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/task-ro/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.includes("/conversation")) {
    const categories = (url.searchParams.get("categories") || "").split(",").filter(Boolean);
    window.__requests.push({categories});
    const items = CONVERSATION.filter(item => !categories.length || categories.includes(item.category));
    return json({conversation: {items, has_more: false, next_before: 0, latest_sequence: CONVERSATION.length}});
  }
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: TASK.id, task: TASK, agentID: "main"}));
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name.startsWith("/assets/")) {
      response.setHeader("Content-Type", name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png" : "application/octet-stream");
      response.end(await readFile(resolve(root, "web/build", name.replace(/^\//, ""))));
    } else {
      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css|png)$/.test(name)) { response.writeHead(404).end(); return; }
      const base = name.startsWith("/vendor/") ? resolve(root, "web") : resolve(root, "web/build");
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : name.endsWith(".png") ? "image/png" : "text/javascript");
      response.end(await readFile(resolve(base, name.replace(/^\/(vendor\/)?/, ""))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

async function filterState(page) {
  return page.evaluate(() => {
    const trigger = document.querySelector(".conversation-filter-trigger");
    const selected = [...document.querySelectorAll("[data-conversation-category].active")]
      .map(node => node.dataset.conversationCategory).sort();
    const requested = [...(window.__requests.at(-1)?.categories || [])].sort();
    const visible = [...document.querySelectorAll("#conversation-list [data-message-id]")]
      .map(node => node.dataset.messageId).sort();
    return {
      selected,
      requested,
      count: trigger?.querySelector("small")?.textContent?.trim() || "",
      requests: window.__requests.length,
      visible,
    };
  });
}

function assertConsistent(state, expected) {
  assert.deepEqual(state.requested, expected, `requested categories: ${JSON.stringify(state.requested)}`);
  assert.deepEqual(state.selected, expected, `control categories: ${JSON.stringify(state.selected)}`);
  assert.equal(state.count, `${expected.length}/4`, `control count: ${state.count}`);
}

async function toggle(page, category, expectedRequests, expected) {
  await page.evaluate(() => {
    const popover = document.querySelector(".conversation-filter-popover");
    if (popover) popover.open = true;
  });
  await page.click(`[data-conversation-category="${category}"]`);
  await page.waitForFunction(({count, expected}) => {
    if (window.__requests.length <= count) return false;
    const selected = [...document.querySelectorAll("[data-conversation-category].active")]
      .map(node => node.dataset.conversationCategory).sort();
    return selected.join(",") === expected.join(",");
  }, {count: expectedRequests, expected}, {timeout: 10000});
}

let browser;
try {
  browser = await chromium.launch({
    executablePath: await binary.executablePath(),
    args: useInstalledBrowser ? ["--no-sandbox"] : binary.args,
    headless: true,
  });
  const page = await browser.newPage({viewport: {width: 1280, height: 800}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });

  await page.goto(url, {waitUntil: "domcontentloaded"});
  await page.waitForSelector(".conversation-filter-trigger", {timeout: 20000});
  await page.waitForFunction(() => window.__requests.length > 0 && document.querySelectorAll("#conversation-list [data-message-id]").length === 2, null, {timeout: 20000});

  // Read-only default is the same chat+error filter the control displays. No
  // update row may slip in from a hardcoded read-only request.
  const initial = await filterState(page);
  assertConsistent(initial, ["chat", "error"]);
  assert.deepEqual(initial.visible, ["ci-chat", "ci-error"], `initial rows: ${JSON.stringify(initial.visible)}`);

  await toggle(page, "update", initial.requests, ["chat", "error", "update"]);
  const withUpdate = await filterState(page);
  assertConsistent(withUpdate, ["chat", "error", "update"]);
  assert.deepEqual(withUpdate.visible, ["ci-chat", "ci-error", "ci-update"], `update rows: ${JSON.stringify(withUpdate.visible)}`);
  assert.ok(!withUpdate.requested.includes("tool"), "toggling update must not add tool");

  await toggle(page, "tool", withUpdate.requests, ["chat", "error", "tool", "update"]);
  const withTool = await filterState(page);
  assertConsistent(withTool, ["chat", "error", "tool", "update"]);
  assert.deepEqual(withTool.visible, ["ci-chat", "ci-error", "ci-tool", "ci-update"], `all rows: ${JSON.stringify(withTool.visible)}`);
  assert.ok(withTool.requested.includes("update"), "toggling tool must not drop update");

  // Switching categories off only removes that category too.
  await toggle(page, "update", withTool.requests, ["chat", "error", "tool"]);
  const updateOff = await filterState(page);
  assertConsistent(updateOff, ["chat", "error", "tool"]);
  assert.ok(!updateOff.visible.includes("ci-update"), "update row stayed visible after update was unchecked");
  assert.ok(updateOff.visible.includes("ci-tool"), "tool row disappeared with the update toggle");

  await toggle(page, "tool", updateOff.requests, ["chat", "error"]);
  const toolOff = await filterState(page);
  assertConsistent(toolOff, ["chat", "error"]);
  assert.deepEqual(toolOff.visible, ["ci-chat", "ci-error"], `final rows: ${JSON.stringify(toolOff.visible)}`);
  assert.deepEqual(errors, []);
  console.log("task_readonly_filter: ok");
} finally {
  await browser?.close();
  server.close();
}