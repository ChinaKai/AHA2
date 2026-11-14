// Browser verification for the server-driven Round activity summary.
//
// The fixture serves a running turn whose activity comes from the Task detail
// response, then flips that response to waiting, completed and failed. The card
// must announce the phase, keep diagnostics expandable, collapse terminal
// rounds, and fit a phone without horizontal overflow. The same page also
// checks the chat+error default message filter and a manual tool toggle.
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
  id: "t-round", code: "task-386", title: "Round activity", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "work", current_goal: "work", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
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
const SINCE = Date.now() - 12000;
const iso = value => new Date(value).toISOString();
const elapsed = value => Math.max(0, Date.now() - value);
window.__scenario = "running";
window.__lastCategories = [];
const scenario = () => {
  const now = Date.now();
  const phase = window.__scenario;
  if (phase === "running" || phase === "waiting") {
    return {
      round: {id: "round-1", task_id: TASK.id, sequence: 1, status: "running", created_at: iso(now - 30000), created_at_ms: now - 30000, started_at_ms: now - 30000},
      turnStatus: "running",
      activity: {phase: phase === "waiting" ? "waiting_backend" : "tool", tool_name: phase === "waiting" ? undefined : "Read",
        since: iso(SINCE), since_ms: SINCE, elapsed_ms: elapsed(SINCE), attempt: 1, round_id: "round-1", turn_id: "turn-1", agent_id: "main"},
    };
  }
  const failed = phase === "failed";
  return {
    round: {id: "round-1", task_id: TASK.id, sequence: 1, status: failed ? "failed" : "completed", created_at: iso(now - 30000), created_at_ms: now - 30000, started_at_ms: now - 30000, finished_at_ms: now - 2000},
    turnStatus: failed ? "failed" : "succeeded",
    activity: {phase: failed ? "failed" : "done", since: iso(now - 2000), since_ms: now - 2000, elapsed_ms: 0, attempt: 1, round_id: "round-1", turn_id: "turn-1", agent_id: "main"},
  };
};
const detail = () => {
  const state = scenario();
  return {
    task: TASK, latest_round: state.round, activity: state.activity,
    turns: [{
      id: "turn-1", task_id: TASK.id, round_id: "round-1", agent_id: "main", sequence: 1,
      generation: 1, attempt: 1, status: state.turnStatus, queued_at: iso(Date.now() - 30000), queued_at_ms: Date.now() - 30000,
      started_at: iso(Date.now() - 29000), started_at_ms: Date.now() - 29000,
      first_event_at_ms: Date.now() - 28000, elapsed_ms: 28000,
      error: state.turnStatus === "failed" ? "backend exploded" : "",
    }],
    agents: [{agent_id: "main", status: "active", title: "Main", runtime_config_valid: true, unread_count: 0}],
    memory: {}, hardware: [], event_cursor: 0, server_time_ms: Date.now(),
  };
};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks/t-round" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-round/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-round/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.includes("/conversation")) {
    const categories = (url.searchParams.get("categories") || "").split(",").filter(Boolean);
    window.__lastCategories.push(categories);
    const items = CONVERSATION.filter(item => !categories.length || categories.includes(item.category));
    return json({conversation: {items, has_more: false, next_before: 0, latest_sequence: CONVERSATION.length}});
  }
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
// No open/heartbeat events: the 2s fallback is the deterministic refresh path.
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

const card = page => page.evaluate(() => {
  const slot = document.querySelector("#agent-turn-slot .agent-turn-card");
  if (!slot) return null;
  const summary = slot.querySelector("details > summary");
  return {
    text: summary?.textContent?.replace(/\s+/g, " ").trim() || "",
    open: slot.querySelector("details")?.open,
    liveRole: Boolean(slot.querySelector('[role="status"][aria-live="polite"]')),
    rows: slot.querySelectorAll(".agent-turn-row").length,
    rowsVisible: Boolean(slot.querySelector(".agent-turn-rows")?.checkVisibility()),
    failure: slot.querySelector(".turn-failure")?.textContent?.replace(/\s+/g, " ").trim() || "",
    elapsed: slot.querySelector("time[data-live-elapsed-ms]")?.getAttribute("data-live-elapsed-ms") || "",
  };
});

async function waitForCard(page, needle, liveRole = true) {
  await page.waitForFunction(({needle, liveRole}) => {
    const slot = document.querySelector("#agent-turn-slot .agent-turn-card");
    if (!slot) return false;
    const text = slot.querySelector("details > summary")?.textContent || "";
    const hasLiveRole = Boolean(slot.querySelector('[role="status"][aria-live="polite"]'));
    return text.includes(needle) && hasLiveRole === liveRole;
  }, {needle, liveRole}, {timeout: 15000});
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
  await page.waitForSelector("#agent-turn-slot .agent-turn-card", {timeout: 20000});
  await waitForCard(page, "正在调用工具：Read");

  const running = await card(page);
  assert.ok(running.text.includes("Round 1"), `round summary: ${running.text}`);
  assert.match(running.elapsed, /^\d+$/);
  assert.equal(running.open, false, "the diagnostic details must start collapsed");
  assert.equal(running.rowsVisible, false, "collapsed diagnostics should hide agent rows");

  // A refresh rebuilds the same activity from Task detail, not from local timers.
  await page.reload({waitUntil: "domcontentloaded"});
  await page.waitForSelector("#agent-turn-slot .agent-turn-card", {timeout: 20000});
  await waitForCard(page, "正在调用工具：Read");
  assert.equal((await card(page)).open, false);

  // The same line must remain visible while the diagnostic details are open.
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => document.querySelector("#agent-turn-slot .agent-turn-card details")?.open, null, {timeout: 5000});
  assert.equal((await card(page)).rowsVisible, true, "expanded diagnostics lost the agent rows");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => !document.querySelector("#agent-turn-slot .agent-turn-card details")?.open, null, {timeout: 5000});

  await page.evaluate(() => { window.__scenario = "waiting"; });
  await waitForCard(page, "等待 Backend 响应");

  await page.evaluate(() => { window.__scenario = "completed"; });
  await waitForCard(page, "完成", false);
  const completed = await card(page);
  assert.ok(completed.text.includes("Round 1"), `completed summary: ${completed.text}`);
  assert.equal(completed.open, false, "a completed round must default to collapsed");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => document.querySelector("#agent-turn-slot .agent-turn-card details")?.open, null, {timeout: 5000});
  assert.equal((await card(page)).rowsVisible, true, "completed diagnostics are no longer expandable");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");

  await page.evaluate(() => { window.__scenario = "failed"; });
  await waitForCard(page, "失败", false);
  const failed = await card(page);
  assert.equal(failed.open, false, "a failed round must default to collapsed");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => (document.querySelector("#agent-turn-slot .turn-failure")?.textContent || "").includes("backend exploded"), null, {timeout: 5000});
  const failedOpen = await card(page);
  assert.ok(failedOpen.failure.includes("backend exploded"), `failure detail: ${failedOpen.failure}`);

  // Mobile: summary and expanded diagnostics stay inside the viewport.
  await page.setViewportSize({width: 390, height: 844});
  await page.waitForFunction(() => document.documentElement.clientWidth <= 390, null, {timeout: 5000});
  const overflow = await page.evaluate(() => {
    const slot = document.querySelector("#agent-turn-slot .agent-turn-card");
    return {
      page: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      card: slot ? slot.scrollWidth - slot.clientWidth : 0,
    };
  });
  assert.ok(overflow.page <= 1, `the page overflows horizontally by ${overflow.page}px on mobile`);
  assert.ok(overflow.card <= 1, `the round card overflows horizontally by ${overflow.card}px on mobile`);

  // Default message filter: chat + error, count 2/4, manual tool toggle adds rows.
  await page.setViewportSize({width: 1280, height: 800});
  const trigger = await page.textContent(".conversation-filter-trigger");
  assert.match(trigger || "", /2\/4/);
  const initialCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
  assert.deepEqual(initialCategories.sort(), ["chat", "error"]);
  const initialIDs = await page.evaluate(() => [...document.querySelectorAll("#conversation-list [data-message-id]")].map(node => node.dataset.messageId));
  assert.deepEqual(initialIDs.sort(), ["ci-chat", "ci-error"]);

  await page.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = true; });
  await page.click('[data-conversation-category="tool"]');
  await page.waitForFunction(() => document.querySelector('[data-message-id="ci-tool"]'), null, {timeout: 10000});
  const toolCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
  assert.deepEqual(toolCategories.sort(), ["chat", "error", "tool"]);
  assert.match(await page.textContent(".conversation-filter-trigger") || "", /3\/4/);

  await page.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = true; });
  await page.click('[data-conversation-category="update"]');
  await page.waitForFunction(() => document.querySelector('[data-message-id="ci-update"]'), null, {timeout: 10000});
  const updateCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
  assert.deepEqual(updateCategories.sort(), ["chat", "error", "tool", "update"]);
  assert.match(await page.textContent(".conversation-filter-trigger") || "", /4\/4/);
  // Filtering messages must not disturb the Round card outside the list.
  assert.match(await page.textContent("#agent-turn-slot .agent-turn-card details > summary") || "", /失败/);

  assert.deepEqual(errors, []);
  console.log("task_round_activity: ok");
} finally {
  await browser?.close();
  server.close();
}