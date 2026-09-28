// Optional browser verification; dependencies and browser files stay in .tools.
//
// On desktop, after scrolling with the wheel, new messages stopped following.
// A wheel sets the "gesture in progress" flag but has no end event to clear it
// the way touchend/pointerup clear a drag, so the flag stayed set and every
// later render declined to follow. This drives the real app: wheel the list,
// come back to the bottom, and check that a live update still scrolls it down.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const output = resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const TASK = {
  id: "t-scroll", code: "task-904", title: "Live conversation", status: "active",
  project_id: "project-1", workspace_id: "workspace-1", collaboration_mode: "single", max_agents: 1,
  original_request: "chat", current_goal: "chat", task_branch: "",
  runtime_config_snapshot_id: "runtime-1", total_tokens: 0,
  created_at: "2026-09-27T00:00:00Z", updated_at: "2026-09-27T00:00:00Z",
};
const BASE = Array.from({length: 40}, (_, index) => ({
  sequence: index + 1,
  id: `ci-${index + 1}`,
  task_id: TASK.id,
  agent_id: "main",
  category: "chat",
  kind: "agent_message",
  summary: `Line ${String(index + 1).padStart(3, "0")}: the quick brown fox jumps over the lazy dog.`,
  created_at: "2026-09-27T00:00:00Z",
}));

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const TASK = ${JSON.stringify(TASK)};
const BASE = ${JSON.stringify(BASE)};
// The conversation grows on every poll so renders really happen; without new
// content the app sees no change and never re-renders, which would make this
// test pass no matter what.
let grown = 0;
window.__grown = () => grown;
const detail = () => ({
  task: TASK, turns: [],
  agents: [{agent_id: "main", status: "idle", title: "Main", runtime_config_valid: true, unread_count: grown}],
  latest_round: null, hardware: [], memory: {},
});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/tasks/t-scroll" && method === "GET") return json(detail());
  if (path === "/api/v1/tasks/t-scroll/channel-routes") return json({ok: true, destinations: [], route: null, contacts: [], members: []});
  if (path === "/api/v1/tasks/t-scroll/agents") return json({agents: detail().agents});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project", project_type: "folder", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "W", root_path: "/tmp", transport: "native", health: "ready"}]});
  if (path === "/api/v1/tasks") return json({tasks: [TASK], has_more: false});
  if (path.includes("/conversation")) {
    grown += 1;
    const extra = Array.from({length: grown}, (_, index) => ({
      sequence: BASE.length + index + 1, id: "live-" + (index + 1), task_id: TASK.id,
      agent_id: "main", category: "chat", kind: "agent_message",
      summary: "Live " + (index + 1), created_at: "2026-09-27T00:00:01Z",
    }));
    return json({conversation: {items: BASE.concat(extra), has_more: false, next_before: 0, latest_sequence: BASE.length + grown}});
  }
  if (path.includes("/context")) return json({context: {}});
  if (path.endsWith("/events")) return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "tasks", taskID: "t-scroll", task: TASK, agentID: "main"}));
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
    } else {
      if (!/^\/(?:vendor\/)?[a-z_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      const file = name.startsWith("/vendor/")
        ? resolve(root, "web", name.slice(1))
        : resolve(root, "web/dist", name.slice(1));
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

const state = page => page.evaluate(() => {
  const list = document.querySelector("#conversation-list");
  if (!list) return null;
  return {top: Math.round(list.scrollTop), max: Math.round(list.scrollHeight - list.clientHeight)};
});

let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  // Desktop: no touch, so the wheel is the only gesture available.
  const page = await browser.newPage({viewport: {width: 1280, height: 800}});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.waitForSelector("#conversation-list", {timeout: 15000});
  await page.waitForFunction(() => {
    const list = document.querySelector("#conversation-list");
    return Boolean(list && list.scrollHeight > list.clientHeight + 300);
  }, null, {timeout: 15000});
  await page.waitForTimeout(1200);

  const start = await state(page);
  assert.ok(start.top > 0 && start.max - start.top < 80, `the conversation did not open at the bottom: ${JSON.stringify(start)}`);

  // Scroll with the wheel, the way a desktop reader does, then come back down.
  await page.evaluate(async () => {
    const list = document.querySelector("#conversation-list");
    const wheel = (deltaY) => list.dispatchEvent(new WheelEvent("wheel", {deltaY, bubbles: true, cancelable: true}));
    wheel(-600);
    list.scrollTop = Math.max(0, list.scrollTop - 600);
    await new Promise(resolve => setTimeout(resolve, 120));
    wheel(600);
    list.scrollTop = list.scrollHeight;
    await new Promise(resolve => setTimeout(resolve, 120));
  });

  // The reader is at the bottom. A live update must now scroll it down for them.
  const before = await state(page);
  const grownBefore = await page.evaluate(() => window.__grown());
  await page.waitForTimeout(6000);
  const after = await state(page);
  const grownAfter = await page.evaluate(() => window.__grown());

  assert.ok(grownAfter > grownBefore + 1, `no live updates arrived (${grownBefore} -> ${grownAfter}), so nothing was exercised`);
  assert.ok(
    after.max - after.top < 80,
    `new messages did not follow while the reader sat at the bottom: top ${after.top} of ${after.max} (was ${before.top})`,
  );
  assert.deepEqual(errors, [], "the conversation threw while scrolling");

  await page.screenshot({path: resolve(output, "conversation-wheel-follow.png")});
  console.log("wheel follow:", JSON.stringify({start: start.top, before: before.top, after: after.top, max: after.max, renders: grownAfter - grownBefore}));
} finally {
  if (browser) await browser.close();
  server.close();
}
