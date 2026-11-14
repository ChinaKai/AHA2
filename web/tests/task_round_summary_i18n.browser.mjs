// Served-build regression for the English Round summary (task-397 review).
//
// The page is loaded from web/build with the en-US locale. This must go through
// the real versioned module URLs: when scripts/build-web.mjs misses the i18n
// importer for task_agents.js, the built page instantiates i18n twice and the
// summary keeps Chinese copy even though the rest of the UI is English.
// Importing web/src modules directly would hide that bug, so the fixture always
// serves the build output.
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
  id: "t-round", code: "task-397", title: "Round summary en-US", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "work", current_goal: "work", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-11-13T00:00:00Z", updated_at: "2026-11-13T00:00:00Z",
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const iso = value => new Date(value).toISOString();
window.__scenario = "running";
window.__phase = "tool";
window.__sequence = 0;
const scenario = () => {
  const now = Date.now();
  if (window.__scenario === "running") {
    return {
      round: {id: "round-7", task_id: TASK.id, sequence: 7, status: "running", created_at_ms: now - 120000, started_at_ms: now - 120000},
      turnStatus: "running",
      activity: {phase: window.__phase, tool_name: "Read", since_ms: now - 5000, since: iso(now - 5000), elapsed_ms: 5000,
        attempt: 1, round_id: "round-7", turn_id: "turn-7", agent_id: "main"},
    };
  }
  const failed = window.__scenario === "failed";
  return {
    round: {id: "round-7", task_id: TASK.id, sequence: 7, status: failed ? "failed" : "completed",
      created_at_ms: now - 120000, started_at_ms: now - 120000, finished_at_ms: now - 2000},
    turnStatus: failed ? "failed" : "succeeded",
    activity: {phase: failed ? "failed" : "done", since_ms: now - 2000, since: iso(now - 2000), elapsed_ms: 0,
      attempt: 1, round_id: "round-7", turn_id: "turn-7", agent_id: "main"},
  };
};
const detail = () => {
  const now = Date.now();
  const state = scenario();
  const finished = state.turnStatus !== "running";
  return {
    task: TASK, latest_round: state.round, activity: state.activity,
    turns: [{
      id: "turn-7", task_id: TASK.id, round_id: "round-7", agent_id: "main", sequence: 7,
      generation: 1, attempt: 1, status: state.turnStatus,
      queued_at: iso(now - 120000), queued_at_ms: now - 120000,
      started_at: iso(now - 119000), started_at_ms: now - 119000,
      finished_at_ms: finished ? now - 2000 : undefined,
      first_event_at_ms: now - 118000, elapsed_ms: 118000,
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
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "en-US"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks/t-round" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-round/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-round/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
// A controllable stream: an "update" event schedules the same refresh a real
// SSE frame would, so every phase change renders without waiting for fallback.
window.EventSource = class {
  constructor(url) {
    this.url = url;
    (window.__sources ||= []).push(this);
    this.listeners = new Map();
    setTimeout(() => this.emit("open", {}), 30);
  }
  addEventListener(type, listener) {
    const list = this.listeners.get(type) || [];
    list.push(listener);
    this.listeners.set(type, list);
  }
  emit(type, event) {
    for (const listener of this.listeners.get(type) || []) listener(event);
  }
  close() {}
};
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

const PHASES = new Map([
  ["queued", "Queued"],
  ["preparing", "Preparing"],
  ["session_wake", "Waking session"],
  ["thinking", "Thinking"],
  ["tool", "Calling tool"],
  ["waiting_backend", "Waiting for backend"],
  ["retrying", "Retrying"],
  ["finalizing", "Finalizing"],
]);
const HAN = /\p{Script=Han}/u;

const summary = page => page.evaluate(() =>
  document.querySelector("#agent-turn-slot .agent-turn-card details > summary > span:first-child")?.textContent?.replace(/\s+/g, " ").trim() || "");
const eventText = page => page.evaluate(() =>
  document.querySelector("#agent-turn-slot .round-activity-text")?.textContent || "");

async function emitUpdate(page, scenario, phase) {
  await page.evaluate(({scenario, phase}) => {
    window.__scenario = scenario;
    window.__phase = phase;
    window.__sequence += 1;
    const source = (window.__sources || []).find(item => item.url.includes("/tasks/t-round/events"));
    source.emit("update", {data: JSON.stringify({sequence: window.__sequence, type: "turn_status"})});
  }, {scenario, phase});
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
  await page.waitForFunction(() => document.querySelector(".round-channel")?.textContent === "SSE", null, {timeout: 5000});

  const seen = new Map();
  for (const [phase, label] of PHASES) {
    await emitUpdate(page, "running", phase);
    // Wait briefly for the refresh, then assert on the actual copy so a locale
    // regression reports the Chinese text instead of an opaque timeout.
    try {
      await page.waitForFunction(expected =>
        (document.querySelector("#agent-turn-slot .round-activity-text")?.textContent || "") === expected,
      label, {timeout: 3000});
    } catch {}
    const text = await summary(page);
    seen.set(phase, text);
    const event = await eventText(page);
    assert.equal(event, label, `${phase}: English event copy`);
    assert.match(text, /Round 7/, `${phase}: round summary: ${text}`);
    assert.doesNotMatch(text, HAN, `${phase}: Chinese leaked into the en-US summary: ${text}`);
    assert.match(text, new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), `${phase}: label missing: ${text}`);
    assert.doesNotMatch(text, /Read/, `${phase}: tool name leaked`);
    const clocks = await page.evaluate(() =>
      [...document.querySelectorAll(".round-summary-phase time")].map(node => node.textContent));
    assert.equal(clocks.length, 2, `${phase}: both clocks`);
  }

  // The reviewer's exact runtime line: Round 7 · Calling tool · 5s · 02:00.
  const toolLine = seen.get("tool");
  assert.match(toolLine, /Round 7 · Calling tool/, `en-US tool line: ${toolLine}`);
  assert.match(toolLine, /\d+s/, `en-US tool step clock: ${toolLine}`);
  assert.match(toolLine, /\d{2}:\d{2}/, `en-US tool total clock: ${toolLine}`);
  // Nothing Chinese anywhere in any of the eight running summaries.
  for (const [phase, text] of seen) assert.doesNotMatch(text, HAN, `${phase}: Han in summary`);
  console.log(`en-US runtime summary: ${toolLine}`);

  await emitUpdate(page, "completed", "done");
  await page.waitForFunction(() =>
    (document.querySelector("#agent-turn-slot .agent-turn-card details > summary")?.textContent || "").includes("Completed"),
  null, {timeout: 10000});
  const completed = await summary(page);
  assert.match(completed, /Round 7 · Completed/, `en-US completed summary: ${completed}`);
  assert.doesNotMatch(completed, HAN, `Chinese leaked into the completed summary: ${completed}`);
  assert.equal(await page.locator("#agent-turn-slot .round-activity").count(), 0, "completed round still breathes");

  await emitUpdate(page, "failed", "failed");
  await page.waitForFunction(() =>
    (document.querySelector("#agent-turn-slot .agent-turn-card details > summary")?.textContent || "").includes("Failed"),
  null, {timeout: 10000});
  const failed = await summary(page);
  assert.match(failed, /Round 7 · Failed/, `en-US failed summary: ${failed}`);
  assert.doesNotMatch(failed, /Completed/, `failed summary read as completed: ${failed}`);
  assert.doesNotMatch(failed, HAN, `Chinese leaked into the failed summary: ${failed}`);

  assert.deepEqual(errors, []);
  console.log("task_round_summary_i18n: ok");
} finally {
  await browser?.close();
  server.close();
}