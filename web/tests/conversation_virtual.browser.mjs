// Browser verification for the virtual conversation list.
//
// Drives the real app against a 1000-message fixture: only a bounded set of
// rows may be mounted, scrolling must keep the same content under the reader,
// live appends must not pull a reader who scrolled up, and expand/details/
// image/copy/focus state must survive recycling.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
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
const output = resolve(root, ".aha2-context", "task_1b8aebee53ad2b363881", "scratch", "main", "virtual");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8"))
  .match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const TASK = {
  id: "t-virtual", code: "task-950", title: "Virtual conversation", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "chat", current_goal: "chat", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
};

function messages(count) {
  return Array.from({length: count}, (_, index) => {
    const last = index === count - 1;
    const tool = index === count - 2;
    const image = index === count - 3;
    const long = last || index % 20 === 0;
    const summary = long
      ? `Long message ${index}: ` + "lorem ipsum dolor sit amet consectetur adipiscing elit ".repeat(40)
      : tool
        ? `Run command ${index}`
        : image
          ? `Image message ${index}`
          : `Line ${String(index + 1).padStart(4, "0")}: the quick brown fox jumps over the lazy dog.`;
    const payload = tool
      ? {output_tail: `tool output ${index}\n` + "stderr line\n".repeat(12), command: `cmd-${index}`}
      : image
        ? {attachments: [{id: `img-${index}`, name: "pixel.png", media_type: "image/png", size: 68}]}
        : {};
    return {
      sequence: index + 1,
      id: `ci-${index + 1}`,
      task_id: TASK.id,
      agent_id: "main",
      category: tool ? "tool" : "chat",
      kind: tool ? "agent_command_finished" : "agent_message",
      summary,
      payload,
      created_at: "2026-09-27T00:00:00Z",
    };
  });
}

const MESSAGES = messages(1000);
const OLDER = Array.from({length: 50}, (_, index) => ({
  sequence: 6000 + index,
  id: `old-${index + 1}`,
  task_id: TASK.id,
  agent_id: "main",
  category: "chat",
  kind: "agent_message",
  summary: `Older history ${index + 1}`,
  created_at: "2026-09-26T00:00:00Z",
}));

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const MESSAGES = ${JSON.stringify(MESSAGES)};
const OLDER = ${JSON.stringify(OLDER)};
let live = 0;
let older = 0;
window.__quiet = true;
window.__live = () => live;
window.__older = () => older;
Object.defineProperty(navigator, "clipboard", {
  configurable: true,
  value: {writeText: async () => { window.__copied = (window.__copied || 0) + 1; }},
});
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: 0}],
  latest_round: null, hardware: [], memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: new URLSearchParams(location.search).get("lang") || "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks/t-virtual" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-virtual/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-virtual/agents") return json({agents: detail().agents});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.startsWith("/api/v1/tasks/t-virtual/attachments/")) return json({});
  if (path.includes("/conversation")) {
    const before = Number(url.searchParams.get("before") || 0);
    if (before > 0) {
      older += 1;
      return json({conversation: {items: OLDER, has_more: false, next_before: 0, latest_sequence: 1000 + live}});
    }
    const isAfter = url.searchParams.has("after");
    // Quiet mode answers the live poll with no new rows, so the fixture's
    // 1000-message history is not trimmed by the app's 300-row runtime cap
    // before the scroll scenarios run.
    if (isAfter && window.__quiet) {
      return json({conversation: {items: [], has_more: true, next_before: 1, latest_sequence: 1000}});
    }
    if (isAfter) live += 1;
    const liveItems = Array.from({length: live}, (_, index) => ({
      sequence: 1001 + index, id: "live-" + (index + 1), task_id: TASK.id, agent_id: "main",
      category: "chat", kind: "agent_message", summary: "A new live message " + (index + 1),
      payload: {}, created_at: "2026-09-27T00:00:02Z",
    }));
    return json({conversation: {
      items: MESSAGES.concat(liveItems), has_more: true, next_before: 1, latest_sequence: 1000 + live,
    }});
  }
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 0});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
// No open/heartbeat: the app falls back to its 2s conversation poll, which lets
// this test inject a live append without waiting on an SSE heartbeat.
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: TASK.id, task: TASK, agentID: "main"}));
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

const pixel = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z0ZkAAAAASUVORK5CYII=", "base64");
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
    } else if (name === "/pixel.png" || name.startsWith("/api/v1/tasks/t-virtual/attachments/")) {
      response.setHeader("Content-Type", "image/png");
      response.end(pixel);
    } else if (name.startsWith("/assets/")) {
      const type = name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png"
        : name.endsWith(".woff2") ? "font/woff2" : "application/octet-stream";
      response.setHeader("Content-Type", type);
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

const settle = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));

async function listState(page) {
  return page.evaluate(() => {
    const list = document.querySelector("#conversation-list");
    const nodes = [...list.querySelectorAll("[data-message-id]")];
    return {
      count: nodes.length,
      ids: nodes.map(node => node.dataset.messageId),
      scrollTop: Math.round(list.scrollTop),
      scrollHeight: Math.round(list.scrollHeight),
      clientHeight: list.clientHeight,
      atBottom: list.scrollHeight - list.scrollTop - list.clientHeight < 80,
      hint: !document.querySelector("#conversation-new-messages")?.hidden,
    };
  });
}

async function anchor(page) {
  return page.evaluate(() => {
    const list = document.querySelector("#conversation-list");
    const listTop = list.getBoundingClientRect().top;
    for (const node of list.querySelectorAll("[data-message-id]")) {
      const rect = node.getBoundingClientRect();
      if (rect.bottom > listTop + 1) {
        return {id: node.dataset.messageId, offset: Math.round((rect.top - listTop) * 100) / 100};
      }
    }
    return {id: "", offset: 0};
  });
}

async function scrollTo(page, top) {
  await page.evaluate(top => {
    const list = document.querySelector("#conversation-list");
    const delta = top < list.scrollTop ? -800 : 800;
    list.dispatchEvent(new WheelEvent("wheel", {deltaY: delta, bubbles: true, cancelable: true}));
    list.scrollTop = top;
  }, top);
  await settle(page);
  await settle(page);
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
  await page.waitForSelector("#conversation-list .message", {timeout: 20000});
  await page.waitForFunction(() => {
    const list = document.querySelector("#conversation-list");
    return list && list.scrollHeight - list.scrollTop - list.clientHeight < 80;
  }, null, {timeout: 20000});

  const initial = await listState(page);
  assert.ok(initial.scrollHeight > 50000, `the fixture did not produce a long history: ${initial.scrollHeight}`);
  assert.ok(initial.count <= 60, `the virtual window mounted too many rows: ${initial.count}`);
  assert.ok(initial.atBottom, "the conversation did not open at the newest message");
  const sequences = initial.ids.map(id => Number(id.replace("ci-", ""))).filter(Number.isFinite);
  assert.deepEqual(sequences, [...sequences].sort((left, right) => left - right), "rendered rows are out of order");
  const initialScrollTop = initial.scrollTop;

  // Expand the newest long message, open a tool card and an image dialog.
  await page.click('[data-message-id="ci-1000"] [data-toggle-message]');
  await page.click('[data-message-id="ci-999"] .message-output > summary');
  await page.click('[data-message-id="ci-998"] [data-image-preview]');
  await page.waitForSelector("#image-preview-dialog[open]", {timeout: 5000});
  await page.click("[data-image-preview-close]");
  await page.click('[data-message-id="ci-1000"] [data-copy-message]');
  await page.waitForFunction(() => window.__copied >= 1, null, {timeout: 5000});
  const prepared = await page.evaluate(() => ({
    expanded: document.querySelector('[data-message-id="ci-1000"]')?.classList.contains("expanded"),
    fullHidden: document.querySelector('[data-message-id="ci-1000"] .message-full-text')?.hidden,
    detailsOpen: document.querySelector('[data-message-id="ci-999"] .message-output')?.open,
    image: Boolean(document.querySelector('[data-message-id="ci-998"] [data-image-preview]')),
  }));
  assert.deepEqual(prepared, {expanded: true, fullHidden: false, detailsOpen: true, image: true});

  // Recycle every one of those rows by scrolling up beyond the overscan.
  await page.evaluate(() => document.activeElement?.blur());
  await scrollTo(page, initialScrollTop - 1800);
  await page.waitForFunction(() => !document.querySelector('[data-message-id="ci-1000"]'), null, {timeout: 5000});
  const recycled = await listState(page);
  assert.ok(recycled.count <= 60, `recycled window mounted too many rows: ${recycled.count}`);
  assert.equal(recycled.atBottom, false);

  // Returning to the bottom restores expanded/details state on the new nodes.
  await scrollTo(page, recycled.scrollHeight);
  await scrollTo(page, (await listState(page)).scrollHeight);
  await page.waitForSelector('[data-message-id="ci-1000"]', {timeout: 5000});
  const restored = await page.evaluate(() => ({
    expanded: document.querySelector('[data-message-id="ci-1000"]')?.classList.contains("expanded"),
    fullHidden: document.querySelector('[data-message-id="ci-1000"] .message-full-text')?.hidden,
    detailsOpen: document.querySelector('[data-message-id="ci-999"] .message-output')?.open,
    image: Boolean(document.querySelector('[data-message-id="ci-998"] [data-image-preview]')),
  }));
  assert.deepEqual(restored, {expanded: true, fullHidden: false, detailsOpen: true, image: true});

  await page.click('[data-message-id="ci-998"] [data-image-preview]');
  await page.waitForSelector("#image-preview-dialog[open]", {timeout: 5000});
  await page.click("[data-image-preview-close]");
  await page.click('[data-message-id="ci-1000"] [data-copy-message]');
  await page.waitForFunction(() => window.__copied >= 2, null, {timeout: 5000});

  // Up-scroll: a live append must not pull the viewport or steal the anchor.
  await page.evaluate(() => document.activeElement?.blur());
  await scrollTo(page, (await listState(page)).scrollTop - 1600);
  const beforeLive = await listState(page);
  const anchoredBefore = await anchor(page);
  assert.equal(beforeLive.atBottom, false);
  const liveBefore = await page.evaluate(() => window.__live());
  await page.evaluate(() => { window.__quiet = false; });
  await page.waitForFunction(before => window.__live() > before, liveBefore, {timeout: 10000});
  await page.waitForFunction(() => !document.querySelector("#conversation-new-messages")?.hidden, null, {timeout: 10000});
  const afterLive = await listState(page);
  const anchoredAfter = await anchor(page);
  assert.equal(afterLive.atBottom, false, "a live append pulled a reader who had scrolled up");
  assert.equal(anchoredAfter.id, anchoredBefore.id, "the anchored row changed while new messages arrived");
  assert.ok(Math.abs(anchoredAfter.offset - anchoredBefore.offset) <= 2,
    `the anchored row moved ${anchoredBefore.offset} -> ${anchoredAfter.offset}px`);
  assert.ok(afterLive.hint, "the new-message hint did not appear");
  const zhHint = await page.evaluate(() => document.querySelector("#conversation-new-messages")?.textContent?.trim() || "");
  assert.match(zhHint, /^有新消息（\d+）$/, `zh-CN unread hint: ${zhHint}`);

  // The hint returns to the newest message and following resumes.
  await page.click("#conversation-new-messages");
  await page.waitForFunction(() => {
    const list = document.querySelector("#conversation-list");
    return list && list.scrollHeight - list.scrollTop - list.clientHeight < 80
      && document.querySelector("#conversation-new-messages")?.hidden;
  }, null, {timeout: 5000});
  const followFrom = await page.evaluate(() => window.__live());
  await page.waitForFunction(before => window.__live() > before, followFrom, {timeout: 10000});
  await settle(page);
  const followed = await listState(page);
  assert.ok(followed.atBottom, "following did not resume after returning to the bottom");

  // Focus inside a row must pin it across a recycling update.
  await page.evaluate(() => { window.__quiet = true; });
  await settle(page);
  const focusTarget = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll("#conversation-list [data-message-id]")];
    const node = nodes[nodes.length - 1];
    node.querySelector("[data-copy-message]")?.focus();
    return node.dataset.messageId;
  });
  await scrollTo(page, (await listState(page)).scrollTop - 1200);
  const focused = await page.evaluate(() => ({
    id: document.activeElement?.closest?.('[data-message-id]')?.dataset.messageId || "",
    connected: document.activeElement?.isConnected === true,
  }));
  assert.deepEqual(focused, {id: focusTarget, connected: true}, "focus was handed to a recycled row");
  assert.ok((await listState(page)).count <= 80, "focus pinning exploded the window");

  // Loading older history above the anchor must not move the reader.
  await page.evaluate(() => document.activeElement?.blur());
  await scrollTo(page, 300);
  const olderAnchor = await anchor(page);
  const olderBefore = await listState(page);
  await page.evaluate(() => document.querySelector("#load-older-conversation")?.click());
  await page.waitForFunction(() => window.__older() >= 1, null, {timeout: 5000});
  await page.waitForFunction(() => !document.querySelector("#load-older-conversation"), null, {timeout: 5000});
  await settle(page);
  const olderAnchorAfter = await anchor(page);
  const olderAfter = await listState(page);
  assert.equal(olderAnchorAfter.id, olderAnchor.id, "loading older history moved the anchored row");
  assert.ok(Math.abs(olderAnchorAfter.offset - olderAnchor.offset) <= 2,
    `loading older history moved the row ${olderAnchor.offset} -> ${olderAnchorAfter.offset}px`);
  assert.ok(olderAfter.count <= 80, `older-history window mounted too many rows: ${olderAfter.count}`);
  assert.ok(olderAfter.scrollTop > olderBefore.scrollTop, "the scroll offset did not absorb the prepended history");

  assert.deepEqual(errors, [], "the virtual conversation threw");

  // The unread hint is rendered through t(), so en-US must use the enUS table
  // rather than the legacy English phrase table (which only backs tr() and the
  // DOM translation safety net).
  const english = await browser.newPage({viewport: {width: 1280, height: 800}});
  const englishErrors = [];
  english.on("pageerror", error => englishErrors.push(error.message));
  english.on("console", message => { if (message.type() === "error") englishErrors.push(message.text()); });
  await english.goto(`${url}?lang=en-US`, {waitUntil: "domcontentloaded"});
  await english.waitForSelector("#conversation-list .message", {timeout: 20000});
  await english.waitForFunction(() => {
    const list = document.querySelector("#conversation-list");
    return list && list.scrollHeight - list.scrollTop - list.clientHeight < 80;
  }, null, {timeout: 20000});
  await scrollTo(english, (await listState(english)).scrollTop - 1600);
  await english.evaluate(() => { window.__quiet = false; });
  await english.waitForFunction(() => !document.querySelector("#conversation-new-messages")?.hidden, null, {timeout: 10000});
  const englishHint = await english.evaluate(() => document.querySelector("#conversation-new-messages")?.textContent?.trim() || "");
  assert.match(englishHint, /^New messages \(\d+\)$/, `en-US unread hint: ${englishHint}`);
  assert.ok(!/[\u3400-\u9fff]/.test(englishHint), `en-US unread hint contains Han characters: ${englishHint}`);
  assert.deepEqual(englishErrors, [], "the en-US conversation threw");
  await english.close();

  await page.screenshot({path: resolve(output, "conversation-virtual.png")});
  console.log("conversation virtual:", JSON.stringify({
    initialRows: initial.count, initialHeight: initial.scrollHeight,
    recycledRows: recycled.count, olderRows: olderAfter.count,
    liveAnchor: [anchoredBefore.id, anchoredBefore.offset, anchoredAfter.offset],
    olderAnchor: [olderAnchor.id, olderAnchor.offset, olderAnchorAfter.offset],
  }));
} finally {
  if (browser) await browser.close();
  server.close();
}