// Browser verification for the mobile Round summary width (task-396).
//
// A running round shows an event word plus a 59s step clock and a 63m total
// clock. The summary must stay on one line inside the card, both clocks and
// the realtime channel badge must remain fully visible, the tool name must
// never render, every running event must breathe, and the card must not grow
// wider than the viewport. Terminal rounds stay static. The same page is
// measured at 1280 to prove the desktop layout is unchanged.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
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
const screenshotDir = process.env.AHA_TEST_SCREENSHOT_DIR || "";

const LONG_TOOL = "mcp__aha2-pet__aha_request_code_review";
const TASK = {
  id: "t-round", code: "task-396", title: "Round summary width", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "work", current_goal: "work", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-11-13T00:00:00Z", updated_at: "2026-11-13T00:00:00Z",
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const LONG_TOOL = ${JSON.stringify(LONG_TOOL)};
const iso = value => new Date(value).toISOString();
window.__phase = "tool";
window.__toolName = LONG_TOOL;
window.__sequence = 100;
// The live stream makes the badge show SSE and keeps the fixture controllable:
// an "update" event schedules the same refresh a real SSE frame would.
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
const detail = () => {
  const now = Date.now();
  const since = now - 59000;
  const activity = {
    phase: window.__phase, tool_name: window.__toolName,
    since: iso(since), since_ms: since, elapsed_ms: 59000, attempt: 1,
    round_id: "round-1", turn_id: "turn-1", agent_id: "main",
  };
  return {
    task: TASK,
    // 63 minutes and 3 seconds: total is over an hour while the step stays 59s.
    latest_round: {id: "round-1", task_id: TASK.id, sequence: 2125, status: "running", created_at: iso(now - 3783000), created_at_ms: now - 3783000, started_at_ms: now - 3783000},
    activity,
    turns: [{
      id: "turn-1", task_id: TASK.id, round_id: "round-1", agent_id: "main", sequence: 1,
      generation: 1, attempt: 1, status: "running", queued_at: iso(now - 3783000), queued_at_ms: now - 3783000,
      started_at: iso(now - 3782000), started_at_ms: now - 3782000,
      first_event_at_ms: now - 60000, elapsed_ms: 59000,
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
  if (path.includes("/conversation")) return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
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

const snapshot = page => page.evaluate(() => {
  const box = node => {
    if (!node) return null;
    const rect = node.getBoundingClientRect();
    return {
      left: +rect.left.toFixed(2), right: +rect.right.toFixed(2), top: +rect.top.toFixed(2),
      width: +rect.width.toFixed(2), height: +rect.height.toFixed(2),
      clientWidth: node.clientWidth, scrollWidth: node.scrollWidth,
    };
  };
  const card = document.querySelector("#agent-turn-slot .agent-turn-card");
  const summary = card?.querySelector("details > summary");
  const label = summary?.querySelector(":scope > span");
  const activity = summary?.querySelector(".round-activity");
  const text = summary?.querySelector(".round-activity-text");
  const step = summary?.querySelector('time[data-round-time="step"]');
  const total = summary?.querySelector('time[data-round-time="total"]');
  const channel = summary?.querySelector(".round-channel");
  const textStyle = text ? getComputedStyle(text) : null;
  return {
    innerWidth: window.innerWidth,
    documentScrollWidth: document.documentElement.scrollWidth,
    bodyScrollWidth: document.body.scrollWidth,
    card: box(card), summary: box(summary), label: box(label), activity: box(activity),
    text: box(text), step: box(step), total: box(total), channel: box(channel),
    summaryHeight: summary ? summary.getBoundingClientRect().height : 0,
    summaryScrollHeight: summary?.scrollHeight || 0,
    summaryClientHeight: summary?.clientHeight || 0,
    summaryText: summary?.textContent?.replace(/\s+/g, " ").trim() || "",
    activityText: text?.textContent || "",
    eventKind: activity?.getAttribute("data-round-event") || "",
    stepText: step?.textContent || "",
    totalText: total?.textContent || "",
    times: [...(summary?.querySelectorAll(".round-summary-phase time") || [])].map(node => node.textContent),
    channelText: channel?.textContent || "",
    textOverflow: textStyle?.textOverflow || "",
    textWhiteSpace: textStyle?.whiteSpace || "",
    textOverflowX: textStyle?.overflow || "",
    textAnimation: textStyle?.animationName || "",
    dotAnimation: activity ? getComputedStyle(activity, "::before").animationName : "",
  };
});

const PHASE_LABELS = {
  queued: "排队等待",
  preparing: "正在准备",
  session_wake: "正在唤醒 Session",
  thinking: "正在思考",
  tool: "正在调用工具",
  waiting_backend: "等待 Backend 响应",
  retrying: "重试中",
  finalizing: "正在整理结果",
};
async function showActivity(page, phase) {
  await page.evaluate(nextPhase => {
    window.__phase = nextPhase;
    window.__sequence += 1;
    // The pet workbench also opens an EventSource; target this Task's stream.
    const source = (window.__sources || []).find(item => item.url.includes("/tasks/t-round/events"));
    source.emit("update", {data: JSON.stringify({sequence: window.__sequence, type: "turn_status"})});
  }, phase);
  await page.waitForFunction(needle =>
    (document.querySelector("#agent-turn-slot .round-activity-text")?.textContent || "").includes(needle),
  PHASE_LABELS[phase], {timeout: 10000});
}

function assertInsideViewport(state, label) {
  assert.ok(state.documentScrollWidth <= state.innerWidth,
    `${label}: document scrollWidth ${state.documentScrollWidth} exceeds innerWidth ${state.innerWidth}`);
  assert.ok(state.bodyScrollWidth <= state.innerWidth,
    `${label}: body scrollWidth ${state.bodyScrollWidth} exceeds innerWidth ${state.innerWidth}`);
  assert.ok(state.card.right <= state.innerWidth + 0.5 && state.card.left >= -0.5,
    `${label}: card [${state.card.left}, ${state.card.right}] is outside [0, ${state.innerWidth}]`);
  assert.ok(state.card.scrollWidth <= state.card.clientWidth + 1,
    `${label}: card content overflows horizontally (${state.card.scrollWidth} > ${state.card.clientWidth})`);
}

function assertBadgeVisible(state, label) {
  assert.ok(state.channel.right <= state.innerWidth + 0.5 && state.channel.left >= -0.5,
    `${label}: channel badge [${state.channel.left}, ${state.channel.right}] is outside the viewport`);
  assert.ok(state.channel.right <= state.card.right + 0.5,
    `${label}: channel badge right ${state.channel.right} is past the card right ${state.card.right}`);
  assert.ok(state.channel.scrollWidth <= state.channel.clientWidth + 1,
    `${label}: channel badge text is truncated`);
  assert.ok(state.channel.width >= 20, `${label}: channel badge collapsed to ${state.channel.width}px`);
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
  await showActivity(page, "tool");
  await page.waitForFunction(() => document.querySelector(".round-channel")?.textContent === "SSE", null, {timeout: 5000});

  // Desktop baseline: the event word, the 59s step and the 63m total all fit on
  // one line; the tool name is never rendered. Card and badge geometry must not
  // depend on which event word is showing.
  const desktopTool = await snapshot(page);
  assert.match(desktopTool.summaryText, /Round 2125/);
  assert.equal(desktopTool.eventKind, "tool");
  assert.equal(desktopTool.activityText, "正在调用工具");
  assert.doesNotMatch(desktopTool.summaryText, new RegExp(LONG_TOOL));
  assert.equal(desktopTool.channelText, "SSE");
  assert.deepEqual(desktopTool.times.length, 2);
  assert.match(desktopTool.stepText, /^(59s|1:\d\d)$/);
  assert.match(desktopTool.totalText, /^\d{2,}:\d\d$/);
  assert.ok(Number(desktopTool.totalText.split(":")[0]) >= 63,
    `desktop total should be over an hour: ${desktopTool.totalText}`);
  assert.ok(desktopTool.step.right <= desktopTool.total.left + 0.5,
    "desktop step clock overlaps the total clock");
  assert.ok(desktopTool.total.right <= desktopTool.summary.right + 0.5,
    "desktop total clock is outside the summary");
  assert.equal(desktopTool.textOverflow, "ellipsis");
  assert.equal(desktopTool.textWhiteSpace, "nowrap");
  assert.match(desktopTool.textAnimation, /round-activity-breathe/);
  assert.match(desktopTool.dotAnimation, /round-activity-pulse/);
  assert.ok(desktopTool.summaryHeight <= 39, `desktop summary grew to ${desktopTool.summaryHeight}px`);
  assertInsideViewport(desktopTool, "desktop tool");
  assertBadgeVisible(desktopTool, "desktop tool");

  // The total clock keeps running between backend refreshes.
  await page.waitForFunction(previous =>
    (document.querySelector('time[data-round-time="total"]')?.textContent || "") !== previous,
  desktopTool.totalText, {timeout: 4000});

  for (const [phase, expected] of Object.entries(PHASE_LABELS)) {
    await showActivity(page, phase);
    const state = await snapshot(page);
    assert.equal(state.activityText, expected, `${phase}: event copy`);
    assert.equal(state.eventKind, phase, `${phase}: event kind`);
    assert.equal(state.times.length, 2, `${phase}: both clocks`);
    assert.doesNotMatch(state.summaryText, new RegExp(LONG_TOOL), `${phase}: tool name leaked`);
    assert.match(state.textAnimation, /round-activity-breathe/, `${phase}: event text should breathe`);
    assert.match(state.dotAnimation, /round-activity-pulse/, `${phase}: status dot should breathe`);
  }

  await showActivity(page, "thinking");
  const desktopThinking = await snapshot(page);
  assert.ok(Math.abs(desktopTool.card.width - desktopThinking.card.width) <= 1,
    `desktop card width changed with the event: ${desktopTool.card.width} vs ${desktopThinking.card.width}`);
  assert.ok(Math.abs(desktopTool.card.height - desktopThinking.card.height) <= 1,
    `desktop card height changed with the event: ${desktopTool.card.height} vs ${desktopThinking.card.height}`);
  assert.ok(Math.abs(desktopTool.channel.left - desktopThinking.channel.left) <= 1 &&
    Math.abs(desktopTool.channel.right - desktopThinking.channel.right) <= 1,
    `desktop badge moved with the event: ${JSON.stringify(desktopThinking.channel)} vs ${JSON.stringify(desktopTool.channel)}`);

  // prefers-reduced-motion turns both breathing animations off.
  await page.emulateMedia({reducedMotion: "reduce"});
  const reduced = await snapshot(page);
  assert.equal(reduced.textAnimation, "none", "reduced motion still animates the event text");
  assert.equal(reduced.dotAnimation, "none", "reduced motion still animates the status dot");
  await page.emulateMedia({reducedMotion: "no-preference"});

  for (const viewport of [{width: 360, height: 800}, {width: 390, height: 844}]) {
    const label = `${viewport.width}x${viewport.height}`;
    await page.setViewportSize(viewport);
    await page.waitForFunction(width => window.innerWidth === width, viewport.width, {timeout: 5000});
    await showActivity(page, "tool");
    const tool = await snapshot(page);
    assert.equal(tool.activityText, "正在调用工具");
    assert.equal(tool.eventKind, "tool");
    assert.deepEqual(tool.times.length, 2);
    assert.match(tool.stepText, /^(59s|1:\d\d)$/);
    assert.match(tool.totalText, /^\d{2,}:\d\d$/);
    assert.doesNotMatch(tool.summaryText, new RegExp(LONG_TOOL));
    assertInsideViewport(tool, `${label} tool`);
    assertBadgeVisible(tool, `${label} tool`);
    assert.equal(tool.channelText, "SSE");

    // One line: the summary keeps its 38px min-height, the event word may
    // ellipsize inside its own box, and both clocks stay visible.
    assert.ok(tool.summaryHeight <= 39, `${label}: summary wrapped to ${tool.summaryHeight}px`);
    assert.ok(tool.summaryScrollHeight <= tool.summaryClientHeight + 1,
      `${label}: summary scroll height ${tool.summaryScrollHeight} exceeds ${tool.summaryClientHeight}`);
    assert.equal(tool.textOverflow, "ellipsis", `${label}: activity label has no ellipsis`);
    assert.equal(tool.textWhiteSpace, "nowrap", `${label}: activity label can wrap`);
    assert.equal(tool.textOverflowX, "hidden", `${label}: activity label is not clipped`);
    assert.ok(tool.step.right <= tool.total.left + 0.5, `${label}: step clock overlaps the total clock`);
    assert.ok(tool.step.left >= tool.summary.left - 0.5 && tool.total.right <= tool.summary.right + 0.5,
      `${label}: a clock is outside the summary`);
    assert.ok(tool.step.scrollWidth <= tool.step.clientWidth + 1,
      `${label}: step clock is truncated (${tool.step.clientWidth}px)`);
    assert.ok(tool.total.scrollWidth <= tool.total.clientWidth + 1,
      `${label}: total clock is truncated (${tool.total.clientWidth}px)`);

    if (screenshotDir) {
      await mkdir(screenshotDir, {recursive: true});
      await page.screenshot({path: resolve(screenshotDir, `task-396-round-${viewport.width}-tool.png`)});
    }

    await showActivity(page, "thinking");
    const thinking = await snapshot(page);
    assert.ok(Math.abs(tool.card.height - thinking.card.height) <= 1,
      `${label}: card height differs from the short event baseline (${tool.card.height} vs ${thinking.card.height})`);
    assert.ok(Math.abs(tool.card.width - thinking.card.width) <= 1,
      `${label}: card width changed with the event (${tool.card.width} vs ${thinking.card.width})`);
    assert.ok(thinking.step.right <= thinking.total.left + 0.5, `${label}: step clock overlaps the total clock`);
    assert.ok(thinking.step.left >= thinking.summary.left - 0.5 && thinking.total.right <= thinking.summary.right + 0.5,
      `${label}: a clock is outside the summary`);
    assertInsideViewport(thinking, `${label} thinking`);
    assertBadgeVisible(thinking, `${label} thinking`);

    if (screenshotDir) {
      await page.screenshot({path: resolve(screenshotDir, `task-396-round-${viewport.width}-thinking.png`)});
    }
  }

  assert.deepEqual(errors, []);
  console.log("task_round_summary_mobile: ok");
} finally {
  await browser?.close();
  server.close();
}