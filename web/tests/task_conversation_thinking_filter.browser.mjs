// Live Task Thinking/Agent Update filter regression (task-724).
//
// Reasoning rows are stored as category "update" + kind
// "agent_reasoning_update" and the server splits them out of "update" for the
// "thinking" filter. The fixture's conversation endpoint reproduces the store
// contract (category split, before/after cursors, limit+has_more, latest only
// advancing through returned rows) so this test checks the Web side end to end:
// default 3/5 without Thinking, Thinking alone/combined, "load older" paging
// with no gaps or duplicates, and SSE-driven incremental refresh appending only
// the selected categories.
//
// Mobile (task-724 review M1): the open filter menu reaches the floating
// search button's corner on phones. Every filter button's right edge must hit
// the button itself and a click there must toggle the filter, never open the
// command palette. en-US: the "N loaded" count updated after load-older goes
// through the existing i18n pattern.
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
  id: "task-live", code: "task-724", title: "Thinking filter", status: "waiting_user",
  project_id: "project-1", workspace_id: "workspace-1",
  collaboration_mode: "single", max_agents: 1, original_request: "work", current_goal: "work",
  task_branch: "", runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-11-20T00:00:00Z", updated_at: "2026-11-20T00:00:00Z",
};

// 150 history rows cycling through every visible kind: 25 chat, 50 Agent
// Update (progress + commentary), 25 Thinking, 25 tool, 25 error.
const CYCLE = [
  ["chat", "agent_message", "chat"],
  ["update", "agent_progress", "update"],
  ["update", "agent_reasoning_update", "thinking"],
  ["tool", "agent_command_finished", "tool"],
  ["error", "agent_error", "error"],
  ["update", "agent_message_update", "update"],
];
const row = (sequence, [category, kind, bucket]) => ({
  sequence, id: `r${sequence}-${bucket}`, task_id: TASK.id, agent_id: "main", category, kind,
  summary: `${bucket} row ${sequence}`, payload: category === "tool" ? {command: "ls"} : {},
  created_at: new Date(Date.UTC(2026, 10, 20, 0, 0, sequence)).toISOString(),
});
const HISTORY = Array.from({length: 150}, (_, index) => row(index + 1, CYCLE[index % CYCLE.length]));

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
window.__rows = ${JSON.stringify(HISTORY)};
window.__requests = [];
const bucket = item => item.category === "update" && item.kind === "agent_reasoning_update" ? "thinking" : item.category;
const page = url => {
  const categories = (url.searchParams.get("categories") || "").split(",").filter(Boolean);
  const before = Number(url.searchParams.get("before") || 0);
  const after = Number(url.searchParams.get("after") || 0);
  let limit = Number(url.searchParams.get("limit") || 50);
  if (limit <= 0 || limit > 200) limit = 50;
  window.__requests.push({categories, before, after, limit});
  let rows = window.__rows.filter(item => (!categories.length || categories.includes(bucket(item)))
    && (!before || item.sequence < before) && (!after || item.sequence > after));
  rows = after ? rows.sort((a, b) => a.sequence - b.sequence) : rows.sort((a, b) => b.sequence - a.sequence);
  const hasMore = rows.length > limit;
  rows = rows.slice(0, limit);
  if (!after) rows.reverse();
  const latest = rows.reduce((value, item) => Math.max(value, item.sequence), after);
  return {items: rows, has_more: hasMore, next_before: rows[0]?.sequence || 0, latest_sequence: latest};
};
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
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: window.__ahaLanguage || "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks/task-live" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/task-live/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/task-live/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.includes("/conversation")) return json({conversation: page(url)});
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.__sources = [];
window.EventSource = class {
  constructor(url) { this.url = url; this.listeners = {}; this.closed = false; window.__sources.push(this); }
  close() { this.closed = true; }
  addEventListener(type, listener) { (this.listeners[type] ||= []).push(listener); }
};
window.__sseSequence = 0;
window.__emitUpdate = () => {
  const source = window.__sources.filter(item => !item.closed).at(-1);
  if (!source) return false;
  window.__sseSequence += 1;
  for (const listener of source.listeners.update || []) listener({data: JSON.stringify({sequence: window.__sseSequence, type: "conversation"})});
  return true;
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

async function snapshot(page) {
  return page.evaluate(() => {
    const selected = [...document.querySelectorAll("[data-conversation-category].active")]
      .map(node => node.dataset.conversationCategory).sort();
    const mounted = [...document.querySelectorAll("#conversation-list [data-message-id]")].map(node => node.dataset.messageId);
    return {
      selected,
      count: document.querySelector(".conversation-filter-trigger small")?.textContent?.trim() || "",
      loaded: document.querySelector("#conversation-filter-loaded")?.textContent?.trim() || "",
      last: window.__requests.at(-1) || null,
      requests: window.__requests.length,
      mounted,
      buckets: [...new Set(mounted.map(id => id.replace(/^r\d+-/, "")))].sort(),
      hasOlder: Boolean(document.querySelector("#load-older-conversation")),
    };
  });
}

async function toggle(page, category, expected) {
  const before = await page.evaluate(() => window.__requests.length);
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
  }, {count: before, expected}, {timeout: 10000});
  // Let the post-reload render settle before reading the DOM.
  await page.waitForFunction(expected => {
    const request = window.__requests.at(-1);
    return request && [...request.categories].sort().join(",") === expected.join(",");
  }, expected, {timeout: 10000});
}

const sorted = values => [...values].sort();
const seqs = ids => ids.map(id => Number(id.match(/^r(\d+)-/)[1]));

let browser;
try {
  browser = await chromium.launch({
    executablePath: await binary.executablePath(),
    args: useInstalledBrowser ? ["--no-sandbox"] : binary.args,
    headless: true,
  });
  // Tall viewport so the windowed list mounts every loaded Thinking row.
  const page = await browser.newPage({viewport: {width: 1280, height: 4000}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });

  await page.goto(url, {waitUntil: "domcontentloaded"});
  await page.waitForSelector(".conversation-filter-trigger", {timeout: 20000});
  await page.waitForFunction(() => window.__requests.length > 0 && document.querySelectorAll("#conversation-list [data-message-id]").length > 0, null, {timeout: 20000});

  // Default: chat + error + Agent Update, 3/5, Thinking and tool off.
  const initial = await snapshot(page);
  assert.deepEqual(initial.selected, ["chat", "error", "update"]);
  assert.deepEqual(sorted(initial.last.categories), ["chat", "error", "update"]);
  assert.equal(initial.count, "3/5");
  assert.equal(initial.loaded, "20 条已加载");
  assert.deepEqual(initial.buckets, ["chat", "error", "update"], `default mounted buckets: ${initial.buckets}`);
  const labels = await page.evaluate(() => [...document.querySelectorAll("[data-conversation-category]")]
    .map(node => [node.dataset.conversationCategory, node.textContent.replace("✓", "").trim()]));
  assert.deepEqual(labels, [["chat", "对话"], ["update", "Agent Update"], ["thinking", "Thinking"], ["tool", "工具调用"], ["error", "错误"]]);

  // Thinking alone: switch Agent Update, chat and error off, Thinking on.
  await toggle(page, "thinking", ["chat", "error", "thinking", "update"]);
  await toggle(page, "update", ["chat", "error", "thinking"]);
  await toggle(page, "chat", ["error", "thinking"]);
  await toggle(page, "error", ["thinking"]);
  const thinkingOnly = await snapshot(page);
  assert.equal(thinkingOnly.count, "1/5");
  assert.deepEqual(thinkingOnly.last.categories, ["thinking"]);
  assert.equal(thinkingOnly.loaded, "20 条已加载");
  assert.deepEqual(thinkingOnly.buckets, ["thinking"], `thinking-only buckets: ${thinkingOnly.buckets}`);
  assert.ok(thinkingOnly.hasOlder, "25 Thinking rows must leave a load-older control after the first 20");

  // Load older: the remaining 5 Thinking rows arrive with no gap or duplicate.
  const beforeOlder = thinkingOnly.requests;
  await page.click("#load-older-conversation");
  await page.waitForFunction(count => window.__requests.length > count && document.querySelector("#conversation-filter-loaded")?.textContent?.trim() === "25 条已加载", beforeOlder, {timeout: 10000});
  const older = await snapshot(page);
  assert.deepEqual(older.last.categories, ["thinking"]);
  assert.ok(older.last.before > 0, "load-older must page with a before cursor");
  assert.ok(!older.hasOlder, "load-older control must disappear once every Thinking row is loaded");
  const expectedThinking = Array.from({length: 25}, (_, index) => index * 6 + 3);
  assert.deepEqual(seqs(older.mounted), expectedThinking, `thinking rows after paging: ${older.mounted}`);

  // Live: new rows of every kind arrive; the SSE-driven refresh appends only
  // the new Thinking row, using the current filter and the latest cursor.
  const appendRows = kinds => page.evaluate(kinds => {
    const cycle = {chat: ["chat", "agent_message"], update: ["update", "agent_progress"], thinking: ["update", "agent_reasoning_update"], tool: ["tool", "agent_command_started"], error: ["error", "agent_error"]};
    for (const bucket of kinds) {
      const sequence = window.__rows.length + 1;
      const [category, kind] = cycle[bucket];
      window.__rows.push({sequence, id: `r${sequence}-${bucket}`, task_id: "task-live", agent_id: "main", category, kind, summary: `${bucket} live ${sequence}`, payload: {}, created_at: new Date().toISOString()});
    }
  }, kinds);
  await appendRows(["update", "thinking", "tool", "chat", "error"]);
  const beforeLive = (await snapshot(page)).requests;
  assert.ok(await page.evaluate(() => window.__emitUpdate()), "the task must hold an open event stream");
  await page.waitForFunction(() => document.querySelector('[data-message-id="r152-thinking"]'), null, {timeout: 10000});
  const live = await snapshot(page);
  const liveRequest = await page.evaluate(count => window.__requests.slice(count).find(item => item.after > 0), beforeLive);
  assert.ok(liveRequest, "SSE refresh must issue an incremental after-cursor request");
  assert.deepEqual(liveRequest.categories, ["thinking"]);
  assert.equal(liveRequest.after, 147, "incremental cursor must start at the newest returned Thinking row");
  assert.equal(live.loaded, "26 条已加载");
  assert.deepEqual(live.buckets, ["thinking"], `live thinking-only buckets: ${live.buckets}`);
  assert.deepEqual(seqs(live.mounted), [...expectedThinking, 152]);

  // Agent Update back on together with Thinking: both appear, no duplicates.
  await toggle(page, "update", ["thinking", "update"]);
  const combined = await snapshot(page);
  assert.equal(combined.count, "2/5");
  assert.deepEqual(sorted(combined.last.categories), ["thinking", "update"]);
  assert.deepEqual(combined.buckets, ["thinking", "update"]);
  assert.equal(new Set(combined.mounted).size, combined.mounted.length, "combined filter mounted a duplicate row");
  // Newest 20 of (50+1 Update, 25+1 Thinking) by sequence.
  const combinedExpected = [...Array.from({length: 150}, (_, index) => index + 1).filter(seq => [2, 3, 0].includes(seq % 6)), 151, 152].slice(-20);
  assert.deepEqual(seqs(combined.mounted), combinedExpected, `combined rows: ${combined.mounted}`);

  // A later live batch in the combined filter appends both kinds once each.
  await appendRows(["thinking", "tool", "update", "error"]);
  assert.ok(await page.evaluate(() => window.__emitUpdate()));
  await page.waitForFunction(() => document.querySelector('[data-message-id="r158-update"]') && document.querySelector('[data-message-id="r156-thinking"]'), null, {timeout: 10000});
  const combinedLive = await snapshot(page);
  assert.equal(combinedLive.loaded, "22 条已加载");
  assert.deepEqual(seqs(combinedLive.mounted), [...combinedExpected, 156, 158]);

  // Agent Update alone after Thinking off: no reasoning row remains, chat/error/tool untouched.
  await toggle(page, "thinking", ["update"]);
  const updateOnly = await snapshot(page);
  assert.deepEqual(updateOnly.buckets, ["update"], `update-only buckets: ${updateOnly.buckets}`);
  await appendRows(["thinking", "update"]);
  assert.ok(await page.evaluate(() => window.__emitUpdate()));
  await page.waitForFunction(() => document.querySelector('[data-message-id="r161-update"]'), null, {timeout: 10000});
  const updateLive = await snapshot(page);
  assert.ok(!updateLive.mounted.includes("r160-thinking"), "a live Thinking row leaked into the Agent Update filter");
  assert.deepEqual(updateLive.buckets, ["update"]);

  assert.deepEqual(errors, []);

  // Phones: the FAB steps aside while the menu is open; every button's right
  // edge is hit-testable and a right-edge click only toggles that filter.
  for (const viewport of [{width: 360, height: 740}, {width: 390, height: 844}, {width: 414, height: 896}]) {
    const phone = await browser.newPage({viewport});
    phone.on("pageerror", error => errors.push(`${viewport.width}: ${error.message}`));
    await phone.goto(url, {waitUntil: "domcontentloaded"});
    await phone.waitForFunction(() => window.__requests.length > 0 && document.querySelector(".conversation-filter-trigger"), null, {timeout: 20000});
    const fabVisible = () => phone.evaluate(() => {
      const fab = document.querySelector(".task-search-fab");
      return Boolean(fab) && getComputedStyle(fab).display !== "none";
    });
    assert.ok(await fabVisible(), `${viewport.width}: the search FAB must stay available while the menu is closed`);
    let selected = ["chat", "error", "update"];
    for (const category of ["chat", "update", "thinking", "tool", "error"]) {
      // The menu may stay open across the re-render after a toggle; open it
      // with a real tap only when it is closed.
      if (!(await phone.evaluate(() => Boolean(document.querySelector(".conversation-filter-popover")?.open)))) {
        await phone.click(".conversation-filter-trigger");
      }
      await phone.waitForSelector(`[data-conversation-category="${category}"]`, {state: "visible", timeout: 5000});
      assert.ok(!(await fabVisible()), `${viewport.width}: the search FAB still shows over the open menu`);
      const hits = await phone.evaluate(() => [...document.querySelectorAll("[data-conversation-category]")].map(button => {
        const rect = button.getBoundingClientRect();
        // Sample the whole straight part of the right edge, 1px inside it;
        // the rounded corners are outside the button's hit area by design.
        const radius = parseFloat(getComputedStyle(button).borderTopRightRadius) || 0;
        const ys = [];
        for (let y = rect.top + radius + 1; y <= rect.bottom - radius - 1; y += 2) ys.push(y);
        const covered = ys.filter(y => {
          const hit = document.elementFromPoint(rect.right - 1, y);
          return !(hit && (hit === button || button.contains(hit)));
        }).map(y => {
          const hit = document.elementFromPoint(rect.right - 1, y);
          return `${Math.round(y)}:${hit ? hit.className || hit.tagName : "none"}`;
        });
        return {category: button.dataset.conversationCategory, samples: ys.length, covered, inside: rect.right <= innerWidth && rect.left >= 0};
      }));
      for (const hit of hits) {
        assert.ok(hit.inside, `${viewport.width}: ${hit.category} button leaves the viewport`);
        assert.ok(hit.samples >= 8, `${viewport.width}: ${hit.category} right edge too short to sample (${hit.samples})`);
        assert.deepEqual(hit.covered, [], `${viewport.width}: ${hit.category} right edge is covered`);
      }
      const box = await phone.locator(`[data-conversation-category="${category}"]`).boundingBox();
      const before = await phone.evaluate(() => window.__requests.length);
      await phone.mouse.click(box.x + box.width - 2, box.y + box.height / 2);
      selected = selected.includes(category) ? selected.filter(item => item !== category) : [...selected, category].sort();
      await phone.waitForFunction(({count, expected}) => {
        const request = window.__requests.at(-1);
        return window.__requests.length > count && request && [...request.categories].sort().join(",") === expected.join(",");
      }, {count: before, expected: selected}, {timeout: 10000});
      assert.equal(await phone.evaluate(() => Boolean(document.querySelector("#command-palette[open]"))), false, `${viewport.width}: clicking ${category} opened the search palette`);
      await phone.waitForFunction(expected => [...document.querySelectorAll("[data-conversation-category].active")]
        .map(node => node.dataset.conversationCategory).sort().join(",") === expected.join(","), selected, {timeout: 10000});
    }
    await phone.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = false; });
    assert.ok(await fabVisible(), `${viewport.width}: the search FAB must come back after the menu closes`);
    await phone.close();
  }

  // en-US: the loaded count rewritten after "load older" is translated.
  const english = await browser.newPage({viewport: {width: 1280, height: 800}});
  await english.addInitScript(() => { window.__ahaLanguage = "en-US"; });
  english.on("pageerror", error => errors.push(`en-US: ${error.message}`));
  await english.goto(url, {waitUntil: "domcontentloaded"});
  await english.waitForSelector("#load-older-conversation", {timeout: 20000});
  await english.evaluate(() => { const popover = document.querySelector(".conversation-filter-popover"); if (popover) popover.open = true; });
  assert.equal((await english.textContent("#conversation-filter-loaded"))?.trim(), "20 loaded");
  await english.click("#load-older-conversation");
  await english.waitForFunction(() => document.querySelector("#conversation-filter-loaded")?.textContent?.trim() === "70 loaded", null, {timeout: 10000});
  await english.close();

  assert.deepEqual(errors, []);
  console.log("task_conversation_thinking_filter: ok");
} finally {
  await browser?.close();
  server.close();
}
