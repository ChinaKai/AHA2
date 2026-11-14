// Browser verification for the Round summary vertical baseline (task-399).
//
// `Round n`, the event word (including a raw tool name), both separators and
// both clocks must share one text baseline. The event word also breathes on
// its own: there is no status dot before it. Before the fix .round-activity
// was an inline-flex whose baseline came from its first child box, so the
// whole event word sat lower than the surrounding text.
//
// Baselines are measured with zero-height inline-block probes (their bottom
// edge is the baseline) instead of comparing glyph boxes, so fallback fonts
// with different ascent/descent cannot skew the result. The fixture renders
// both locales at 1280, 390 and 360 for thinking, tool, empty tool name,
// completed and failed rounds.
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
  id: "t-round", code: "task-399", title: "Round summary baseline", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "work", current_goal: "work", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-11-13T00:00:00Z", updated_at: "2026-11-13T00:00:00Z",
};

const bootstrapFor = locale => `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const iso = value => new Date(value).toISOString();
window.__scenario = "running";
window.__phase = "thinking";
window.__toolName = "Read";
window.__sequence = 0;
const scenario = () => {
  const now = Date.now();
  if (window.__scenario === "running") {
    return {
      round: {id: "round-7", task_id: TASK.id, sequence: 7, status: "running", created_at_ms: now - 120000, started_at_ms: now - 120000},
      turnStatus: "running",
      activity: {phase: window.__phase, tool_name: window.__toolName, since_ms: now - 5000, since: iso(now - 5000), elapsed_ms: 5000,
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
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: ${JSON.stringify(locale)}}});
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

let fixtureSource = bootstrapFor("zh-CN");
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(fixtureSource);
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

const viewports = [
  {width: 1280, height: 800},
  {width: 390, height: 844},
  {width: 360, height: 800},
];
const copy = {
  "zh-CN": {thinking: "正在思考", toolFallback: "工具", completed: "完成", failed: "失败"},
  "en-US": {thinking: "Thinking", toolFallback: "Tool", completed: "Completed", failed: "Failed"},
};
const TOOL_NAME = "Bash";
// `tool` renders the normalised tool name, `tool-empty` the locale fallback.
const states = [
  {kind: "running", phase: "thinking", expected: locale => copy[locale].thinking},
  {kind: "running", phase: "tool", toolName: TOOL_NAME, expected: () => TOOL_NAME},
  {kind: "running", phase: "tool", toolName: "", expected: locale => copy[locale].toolFallback},
  {kind: "terminal", status: "completed", expected: locale => copy[locale].completed},
  {kind: "terminal", status: "failed", expected: locale => copy[locale].failed},
];

const measure = page => page.evaluate(() => {
  const card = document.querySelector("#agent-turn-slot .agent-turn-card");
  const summary = card.querySelector("details > summary");
  const label = summary.querySelector(":scope > span");
  const phase = summary.querySelector(".round-summary-phase");
  const activity = phase.querySelector(".round-activity");
  const activityText = phase.querySelector(".round-activity-text");
  const step = phase.querySelector('time[data-round-time="step"]');
  const total = phase.querySelector('time[data-round-time="total"]');
  const seps = [...phase.querySelectorAll(".round-summary-sep")];
  // A zero-height inline-block is baseline-aligned by default, so its bottom
  // edge sits exactly on the text baseline of its line.
  const baseline = node => {
    const probe = document.createElement("span");
    probe.style.cssText = "display:inline-block;width:0;height:0;vertical-align:baseline";
    node.appendChild(probe);
    const y = probe.getBoundingClientRect().bottom;
    probe.remove();
    return +y.toFixed(3);
  };
  const terminalText = activityText ? null : [...phase.childNodes].find(node => node.nodeType === 3 && node.textContent.trim());
  const summaryRect = summary.getBoundingClientRect();
  const cardRect = card.getBoundingClientRect();
  return {
    innerWidth: window.innerWidth,
    summaryHeight: +summaryRect.height.toFixed(3),
    summaryScrollHeight: summary.scrollHeight,
    summaryClientHeight: summary.clientHeight,
    card: {left: +cardRect.left.toFixed(2), right: +cardRect.right.toFixed(2), scrollWidth: card.scrollWidth, clientWidth: card.clientWidth},
    documentScrollWidth: document.documentElement.scrollWidth,
    bodyScrollWidth: document.body.scrollWidth,
    eventText: activityText ? activityText.textContent : terminalText.textContent.trim(),
    phrase: phase.textContent.replace(/\s+/g, " ").trim(),
    baselines: {
      prefix: baseline(label),
      event: baseline(activityText || phase),
      seps: seps.map(node => baseline(node)),
      step: step ? baseline(step) : null,
      total: total ? baseline(total) : null,
    },
    eventFont: activityText ? getComputedStyle(activityText).font : "",
    eventAnimation: activityText ? getComputedStyle(activityText).animationName : "",
    activityChildren: activity ? activity.childElementCount : 0,
    dotBefore: activity ? getComputedStyle(activity, "::before").content : "none",
  };
});

async function showState(page, state, locale) {
  await page.evaluate(next => {
    window.__scenario = next.kind === "terminal" ? next.status : "running";
    if (next.phase) window.__phase = next.phase;
    if (next.toolName !== undefined) window.__toolName = next.toolName;
    window.__sequence += 1;
    const source = (window.__sources || []).find(item => item.url.includes("/tasks/t-round/events"));
    source.emit("update", {data: JSON.stringify({sequence: window.__sequence, type: "turn_status"})});
  }, {kind: state.kind, phase: state.phase, toolName: state.toolName, status: state.status});
  const expected = state.expected(locale);
  await page.waitForFunction(({expected, terminal}) => {
    const card = document.querySelector("#agent-turn-slot .agent-turn-card");
    if (!card) return false;
    if (!terminal) return (card.querySelector(".round-activity-text")?.textContent || "") === expected;
    return (card.querySelector(".round-summary-phase")?.textContent || "").trim().startsWith(expected);
  }, {expected, terminal: state.kind === "terminal"}, {timeout: 10000});
}

const failures = [];
const rows = [];
let browser;
try {
  browser = await chromium.launch({
    executablePath: await binary.executablePath(),
    args: useInstalledBrowser ? ["--no-sandbox"] : binary.args,
    headless: true,
  });
  for (const locale of Object.keys(copy)) {
    fixtureSource = bootstrapFor(locale);
    const page = await browser.newPage({viewport: {width: 1280, height: 800}});
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
    await page.goto(url, {waitUntil: "domcontentloaded"});
    await page.waitForSelector("#agent-turn-slot .agent-turn-card", {timeout: 20000});
    await page.waitForFunction(() => document.querySelector(".round-channel")?.textContent === "SSE", null, {timeout: 5000});

    for (const viewport of viewports) {
      await page.setViewportSize(viewport);
      await page.waitForFunction(width => window.innerWidth === width, viewport.width, {timeout: 5000});
      for (const state of states) {
        await showState(page, state, locale);
        const snapshot = await measure(page);
        const tag = `${locale} ${viewport.width}x${viewport.height} ${state.kind === "terminal" ? state.status : state.phase}${state.toolName === "" ? " (fallback)" : ""}`;
        const row = {
          tag,
          event: snapshot.eventText,
          baselines: snapshot.baselines,
          summaryHeight: snapshot.summaryHeight,
        };
        rows.push(row);
        const others = [snapshot.baselines.prefix, ...snapshot.baselines.seps,
          snapshot.baselines.step, snapshot.baselines.total].filter(value => value !== null);
        const worstBaseline = Math.max(...others.map(value => Math.abs(value - snapshot.baselines.event)));
        if (worstBaseline > 1) failures.push(`${tag}: event baseline off by ${worstBaseline.toFixed(2)}px (${JSON.stringify(row)})`);
        if (snapshot.eventText !== state.expected(locale)) failures.push(`${tag}: event copy ${JSON.stringify(snapshot.eventText)}`);
        if (state.kind === "running") {
          if (snapshot.activityChildren !== 1) failures.push(`${tag}: activity wrapper has ${snapshot.activityChildren} elements, expected only the text span`);
          if (snapshot.dotBefore !== "none") failures.push(`${tag}: status dot still rendered (::before content ${snapshot.dotBefore})`);
          if (!/round-activity-breathe/.test(snapshot.eventAnimation)) failures.push(`${tag}: event text does not breathe (${snapshot.eventAnimation})`);
        }
        if (locale === "zh-CN" && state.phase === "tool" && state.toolName === "" && snapshot.phrase.includes("正在调用工具")) {
          failures.push(`${tag}: empty tool name fell back to the old long label`);
        }
        if (locale === "en-US" && state.phase === "tool" && /Calling tool/.test(snapshot.phrase)) {
          failures.push(`${tag}: tool phase still uses the old Calling tool label`);
        }
        if (snapshot.documentScrollWidth > snapshot.innerWidth || snapshot.bodyScrollWidth > snapshot.innerWidth) {
          failures.push(`${tag}: horizontal overflow ${snapshot.documentScrollWidth}/${snapshot.bodyScrollWidth} > ${snapshot.innerWidth}`);
        }
        if (snapshot.summaryHeight < 37 || snapshot.summaryHeight > 39) failures.push(`${tag}: summary height ${snapshot.summaryHeight}`);
        if (snapshot.summaryScrollHeight > snapshot.summaryClientHeight + 1) failures.push(`${tag}: summary wrapped (${snapshot.summaryScrollHeight} > ${snapshot.summaryClientHeight})`);
      }
    }

    // Only the text animation is left, and reduced motion stops it.
    await showState(page, states[0], locale);
    await page.emulateMedia({reducedMotion: "reduce"});
    const reduced = await measure(page);
    if (reduced.eventAnimation !== "none") failures.push(`${locale}: reduced motion still animates the event text (${reduced.eventAnimation})`);
    await page.emulateMedia({reducedMotion: "no-preference"});

    if (errors.length) failures.push(`${locale}: page errors ${JSON.stringify(errors)}`);
    await page.close();
  }

  console.log(JSON.stringify(rows, null, 1));
  assert.deepEqual(failures, []);
  console.log("task_round_summary_baseline: ok");
} finally {
  await browser?.close();
  server.close();
}