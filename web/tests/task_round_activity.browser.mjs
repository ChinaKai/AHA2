// Browser verification for the server-driven Round activity summary.
//
// The fixture serves a running turn whose activity comes from the Task detail
// response, cycles every non-terminal phase, then flips that response to
// completed and failed. The card must name the phase without the tool name,
// show both clocks, breathe only while running, keep diagnostics expandable,
// collapse terminal rounds, and fit a phone without horizontal overflow. The
// same page also checks the chat+error default message filter and a manual
// tool toggle.
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
  {sequence: 2, id: "ci-update", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_progress", summary: "update row", payload: {}, created_at: "2026-11-13T00:00:01Z"},
  {sequence: 3, id: "ci-tool", task_id: TASK.id, agent_id: "main", category: "tool", kind: "agent_command_finished", summary: "tool row", payload: {command: "ls"}, created_at: "2026-11-13T00:00:02Z"},
  {sequence: 4, id: "ci-error", task_id: TASK.id, agent_id: "main", category: "error", kind: "agent_error", summary: "error row", payload: {}, created_at: "2026-11-13T00:00:03Z"},
  {sequence: 5, id: "ci-thinking", task_id: TASK.id, agent_id: "main", category: "update", kind: "agent_reasoning_update", summary: "thinking row", payload: {}, created_at: "2026-11-13T00:00:04Z"},
];

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const CONVERSATION = ${JSON.stringify(CONVERSATION)};
const SINCE = Date.now() - 12000;
const iso = value => new Date(value).toISOString();
const elapsed = value => Math.max(0, Date.now() - value);
window.__scenario = "running";
window.__phase = "tool";
window.__toolName = "Read";
window.__lastCategories = [];
const scenario = () => {
  const now = Date.now();
  const phase = window.__scenario;
  if (phase === "running") {
    return {
      round: {id: "round-1", task_id: TASK.id, sequence: 1, status: "running", created_at: iso(now - 30000), created_at_ms: now - 30000, started_at_ms: now - 30000},
      turnStatus: "running",
      activity: {phase: window.__phase, tool_name: window.__toolName,
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
    const bucket = item => item.category === "update" && item.kind === "agent_reasoning_update" ? "thinking" : item.category;
    const items = CONVERSATION.filter(item => !categories.length || categories.includes(bucket(item)));
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
  const live = slot.querySelector('[role="status"][aria-live="polite"]');
  return {
    text: summary?.textContent?.replace(/\s+/g, " ").trim() || "",
    open: slot.querySelector("details")?.open,
    liveRole: Boolean(live),
    liveText: live?.textContent?.trim() || "",
    rows: slot.querySelectorAll(".agent-turn-row").length,
    rowsVisible: Boolean(slot.querySelector(".agent-turn-rows")?.checkVisibility()),
    failure: slot.querySelector(".turn-failure")?.textContent?.replace(/\s+/g, " ").trim() || "",
    event: slot.querySelector(".round-activity-text")?.textContent || "",
    eventKind: slot.querySelector(".round-activity")?.getAttribute("data-round-event") || "",
    eventAnimation: slot.querySelector(".round-activity-text")
      ? getComputedStyle(slot.querySelector(".round-activity-text")).animationName : "",
    dotContent: slot.querySelector(".round-activity")
      ? getComputedStyle(slot.querySelector(".round-activity"), "::before").content : "none",
    times: [...slot.querySelectorAll(".round-summary-phase time")].map(node => ({
      kind: node.getAttribute("data-round-time"),
      text: node.textContent,
      live: node.getAttribute("data-live-elapsed-ms"),
    })),
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
  await waitForCard(page, "Read");

  const running = await card(page);
  assert.ok(running.text.includes("Round 1"), `round summary: ${running.text}`);
  assert.equal(running.open, false, "the diagnostic details must start collapsed");
  assert.equal(running.rowsVisible, false, "collapsed diagnostics should hide agent rows");

  // Every non-terminal phase has its own event copy. The tool phase renders the
  // normalised tool name, both clocks are present, and the live region holds
  // nothing but the event word.
  const phases = new Map([
    ["queued", "排队等待"],
    ["preparing", "正在准备"],
    ["session_wake", "正在唤醒 Session"],
    ["thinking", "正在思考"],
    ["tool", "Read"],
    ["waiting_backend", "等待 Backend 响应"],
    ["retrying", "重试中"],
    ["finalizing", "正在整理结果"],
  ]);
  for (const [phase, label] of phases) {
    await page.evaluate(next => { window.__scenario = "running"; window.__phase = next; }, phase);
    await waitForCard(page, label);
    const state = await card(page);
    assert.equal(state.event, label, `${phase}: event copy`);
    assert.equal(state.eventKind, phase, `${phase}: event kind`);
    assert.equal(state.liveText, label, `${phase}: live region must contain only the event word`);
    assert.equal(state.times.length, 2, `${phase}: both clocks must be visible`);
    assert.deepEqual(state.times.map(item => item.kind), ["step", "total"], `${phase}: clock kinds`);
    state.times.forEach(item => {
      assert.ok(item.text, `${phase}: ${item.kind} clock is empty`);
      assert.match(item.live, /^\d+$/, `${phase}: ${item.kind} clock is not ticking from an elapsed value`);
    });
    if (phase !== "tool") assert.doesNotMatch(state.text, /Read/, `${phase}: tool name leaked into the summary`);
    assert.match(state.eventAnimation, /round-activity-breathe/, `${phase}: event text should breathe`);
    assert.equal(state.dotContent, "none", `${phase}: the summary still renders a status dot`);
  }

  // A blank tool name falls back to the short generic noun, never the old
  // "正在调用工具" phrase.
  await page.evaluate(() => { window.__scenario = "running"; window.__phase = "tool"; window.__toolName = ""; });
  await waitForCard(page, "工具");
  const fallback = await card(page);
  assert.equal(fallback.event, "工具", "empty tool name fallback copy");
  assert.equal(fallback.liveText, "工具", "empty tool name fallback in the live region");

  // A refresh rebuilds the same activity from Task detail, not from local timers.
  await page.evaluate(() => { window.__toolName = "Read"; });
  await page.reload({waitUntil: "domcontentloaded"});
  await page.waitForSelector("#agent-turn-slot .agent-turn-card", {timeout: 20000});
  await waitForCard(page, "Read");
  assert.equal((await card(page)).open, false);

  // The same line must remain visible while the diagnostic details are open.
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => document.querySelector("#agent-turn-slot .agent-turn-card details")?.open, null, {timeout: 5000});
  assert.equal((await card(page)).rowsVisible, true, "expanded diagnostics lost the agent rows");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => !document.querySelector("#agent-turn-slot .agent-turn-card details")?.open, null, {timeout: 5000});

  await page.evaluate(() => { window.__scenario = "completed"; });
  await waitForCard(page, "完成", false);
  const completed = await card(page);
  assert.ok(completed.text.includes("Round 1"), `completed summary: ${completed.text}`);
  assert.equal(completed.times.length, 1, "a completed round shows the total clock only");
  assert.equal(completed.times[0].kind, "total");
  assert.match(completed.times[0].text, /^\d+s$|^\d+:\d\d$/);
  assert.equal(completed.eventKind, "", "a completed round has no breathing event");
  assert.equal(completed.eventAnimation, "", "a completed round must not animate");
  assert.equal(completed.open, false, "a completed round must default to collapsed");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");
  await page.waitForFunction(() => document.querySelector("#agent-turn-slot .agent-turn-card details")?.open, null, {timeout: 5000});
  assert.equal((await card(page)).rowsVisible, true, "completed diagnostics are no longer expandable");
  await page.click("#agent-turn-slot .agent-turn-card details > summary");

  await page.evaluate(() => { window.__scenario = "failed"; });
  await waitForCard(page, "失败", false);
  const failed = await card(page);
  assert.equal(failed.times.length, 1, "a failed round shows the total clock only");
  assert.equal(failed.times[0].kind, "total");
  assert.equal(failed.eventKind, "", "a failed round has no breathing event");
  assert.equal(failed.eventAnimation, "", "a failed round must not animate");
  assert.equal(failed.open, false, "a failed round must default to collapsed");
  assert.doesNotMatch(failed.text, /完成/, "a failed round must not read as completed");
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

  // Default message filter (task-724): chat + error + Agent Update, count 3/5;
  // manual tool and Thinking toggles add their rows.
  await page.setViewportSize({width: 1280, height: 800});
  const trigger = await page.textContent(".conversation-filter-trigger");
  assert.match(trigger || "", /3\/5/);
  const initialCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
  assert.deepEqual(initialCategories.sort(), ["chat", "error", "update"]);
  const initialIDs = await page.evaluate(() => [...document.querySelectorAll("#conversation-list [data-message-id]")].map(node => node.dataset.messageId));
  assert.deepEqual(initialIDs.sort(), ["ci-chat", "ci-error", "ci-update"]);

  await page.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = true; });
  await page.click('[data-conversation-category="tool"]');
  await page.waitForFunction(() => document.querySelector('[data-message-id="ci-tool"]'), null, {timeout: 10000});
  const toolCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
  assert.deepEqual(toolCategories.sort(), ["chat", "error", "tool", "update"]);
  assert.match(await page.textContent(".conversation-filter-trigger") || "", /4\/5/);

  await page.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = true; });
  await page.click('[data-conversation-category="thinking"]');
  await page.waitForFunction(() => document.querySelector('[data-message-id="ci-thinking"]'), null, {timeout: 10000});
  const thinkingCategories = await page.evaluate(() => window.__lastCategories.at(-1) || []);
  assert.deepEqual(thinkingCategories.sort(), ["chat", "error", "thinking", "tool", "update"]);
  assert.match(await page.textContent(".conversation-filter-trigger") || "", /5\/5/);
  // Filtering messages must not disturb the Round card outside the list.
  assert.match(await page.textContent("#agent-turn-slot .agent-turn-card details > summary") || "", /失败/);

  assert.deepEqual(errors, []);
  console.log("task_round_activity: ok");
} finally {
  await browser?.close();
  server.close();
}