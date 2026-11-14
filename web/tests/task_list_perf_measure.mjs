// Opt-in measurement harness for Web Phase 4 milestone 2B (task-401).
// Serves the real web/build bundle with fetch/SSE fixtures generated in memory
// (no fixture files are committed) and records first render, scroll, click,
// memory and SSE refresh behaviour at 200/500 Tasks and 190/500 approvals.
//
// Build first, then run with Playwright and a Chromium binary:
//   npm --prefix web run build
//   AHA_PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs \
//   AHA_BROWSER_EXECUTABLE=/path/to/chrome-headless-shell \
//   AHA_MEASURE_OUTPUT=/tmp/list-result.json \
//     node web/tests/task_list_perf_measure.mjs
// AHA_SCENARIO=tasks_200|tasks_500|approvals_190|approvals_500 limits the run.
// See docs/web-phase4-milestone2b-decision.md for the recorded results.
import {createServer} from "node:http";
import {readFile, writeFile} from "node:fs/promises";
import {existsSync} from "node:fs";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(process.env.AHA_REPO_ROOT || resolve(import.meta.dirname, "../.."));
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const executablePath = process.env.AHA_BROWSER_EXECUTABLE;
if (!executablePath) throw new Error("set AHA_BROWSER_EXECUTABLE");
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
const buildIndex = await readFile(resolve(root, "web/build/index.html"), "utf8");

const TASK_STATUSES = ["active", "active", "active", "active", "waiting_user", "active", "failed", "active", "active", "blocked"];

function taskFixture(count) {
  const now = Date.parse("2026-11-13T12:00:00Z");
  return Array.from({length: count}, (_, index) => ({
    id: `task-${String(index).padStart(3, "0")}`,
    code: `task-${String(index + 1).padStart(3, "0")}`,
    title: `阶段 4 高保真任务 ${index + 1}：测量列表渲染与实时更新`,
    status: TASK_STATUSES[index % TASK_STATUSES.length],
    project_id: index % 3 === 0 ? "project-2" : "project-1",
    workspace_id: index % 2 === 0 ? "workspace-1" : "workspace-2",
    current_goal: index % 4 === 0 ? `当前目标：验证第 ${index + 1} 条任务在列表中的渲染与刷新行为` : "",
    task_branch: index % 3 === 0 ? `aha/task-${String(index).padStart(4, "0")}` : "",
    total_tokens: 1200 + index * 731,
    created_at: new Date(now - index * 61_000).toISOString(),
    collaboration_mode: "auto",
    max_agents: 3,
  }));
}

function approvalFixture(count) {
  const now = Date.parse("2026-11-13T12:00:00Z");
  const kinds = ["idea", "owner_decision", "delete_task", "delete_model", "permission_change"];
  return Array.from({length: count}, (_, index) => ({
    id: `approval-${String(index).padStart(3, "0")}`,
    task_id: `task-${String(index % 200).padStart(3, "0")}`,
    kind: kinds[index % kinds.length],
    title: `待审批事项 ${index + 1}：删除或调整一个运行时资源`,
    summary: `宠物请求处理第 ${index + 1} 项变更，需要主人确认。`,
    reason: "measurement fixture",
    risk: index % 3 === 0 ? "high" : index % 2 === 0 ? "medium" : "low",
    payload: index % 5 === 1 ? JSON.stringify({options: ["replay", "skip"]}) : "{}",
    status: "pending",
    created_at: new Date(now - index * 90_000).toISOString(),
  }));
}

function fixtureScript(tasks, approvals, view) {
  return `
const TASKS = ${JSON.stringify(taskFixture(tasks))};
const APPROVALS = ${JSON.stringify(approvalFixture(approvals))};
const projects = [
  {id: "project-1", name: "AHA2", project_type: "folder", channel_retired: false},
  {id: "project-2", name: "Aha2 Measurement", project_type: "folder", channel_retired: false},
];
const workspaces = [
  {id: "workspace-1", project_id: "project-1", name: "builder", root_path: "/srv/build/AHA2", transport: "native", health: "ready"},
  {id: "workspace-2", project_id: "project-2", name: "wsl", root_path: "E:/kk-workspace/AHA2", transport: "wsl", health: "ready"},
];
window.__fixture = {
  tasks: TASKS,
  approvals: APPROVALS,
  taskRequests: [],
  taskDetailRequests: [],
  workbenchRequests: [],
  pendingCountRequests: 0,
  bumpTask(index) {
    const task = TASKS[index % TASKS.length];
    task.status = task.status === "active" ? "waiting_user" : "active";
    task.total_tokens += 17;
  },
  addApproval() {
    const next = ${JSON.stringify(approvalFixture(1)[0])};
    next.id = "approval-new-" + Date.now();
    next.created_at = new Date().toISOString();
    APPROVALS.unshift(next);
    return next.id;
  },
};
window.__longTasks = [];
new PerformanceObserver(list => {
  for (const entry of list.getEntries()) window.__longTasks.push({start: entry.startTime, duration: entry.duration});
}).observe({entryTypes: ["longtask"]});

const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const taskCounts = () => {
  const status = {};
  const project = {};
  const device = {local: 0};
  for (const task of TASKS) {
    status[task.status] = (status[task.status] || 0) + 1;
    project[task.project_id] = (project[task.project_id] || 0) + 1;
    device.local++;
  }
  return {status, project, device};
};
const taskDetail = id => ({
  task: TASKS.find(task => task.id === id) || TASKS[0],
  turns: [],
  activity: null,
  agents: [{agent_id: "main", backend: "claude", model: "claude-sonnet-5", status: "idle"}],
  hardware: [],
  hardware_resources: [],
  permission_requests: [],
  event_cursor: 0,
  server_time_ms: Date.now(),
});

window.__sse = [];
class MockEventSource {
  constructor(url) {
    this.url = String(url);
    this.listeners = {};
    this.closed = false;
    window.__sse.push(this);
  }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  close() { this.closed = true; }
  _emit(type, payload) {
    const event = {data: JSON.stringify(payload)};
    for (const fn of this.listeners[type] || []) fn(event);
  }
}
window.EventSource = MockEventSource;
window.__emitGlobal = (type, data) => {
  for (const source of window.__sse) {
    if (!source.closed && source.url === "/api/v1/events") source._emit("update", {type, data});
  }
};
window.__emitTask = (type, data, eventType = "update") => {
  for (const source of window.__sse) {
    if (!source.closed && source.url.startsWith("/api/v1/tasks/") && source.url.includes("/events")) source._emit(eventType, data);
  }
};

window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  const method = (init.method || "GET").toUpperCase();
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks" && method === "GET") {
    const limit = Number(url.searchParams.get("limit") || 50);
    const cursor = Number(url.searchParams.get("cursor") || 0);
    const slice = TASKS.slice(cursor, cursor + limit);
    const next = cursor + limit < TASKS.length ? String(cursor + limit) : "";
    window.__fixture.taskRequests.push({at: performance.now(), cursor, limit, returned: slice.length});
    return json({tasks: slice, has_more: Boolean(next), next_cursor: next, counts: taskCounts()});
  }
  if (path === "/api/v1/projects") return json({projects, has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/codex/accounts") return json({accounts: []});
  if (path === "/api/v1/pet/approvals/pending-count") {
    window.__fixture.pendingCountRequests++;
    return json({ok: true, pending: APPROVALS.filter(item => item.status === "pending").length});
  }
  if (path === "/api/v1/pet/workbench") {
    const approvalLimit = Number(url.searchParams.get("approval_limit") || 10);
    const worklogLimit = Number(url.searchParams.get("worklog_limit") || 30);
    const approvals = APPROVALS.slice(0, approvalLimit);
    window.__fixture.workbenchRequests.push({at: performance.now(), approvalLimit, returned: approvals.length});
    return json({
      ok: true,
      core: [{name: "identity.md", content: "# pet"}],
      worklog: [],
      worklog_total: 0,
      approvals,
      approval_total: APPROVALS.length,
      approval_counts: [{kind: "all", status: "pending", count: APPROVALS.length}, {kind: "all", status: "all", count: APPROVALS.length}],
      watches: [],
      channels: [],
      channel_total: 0,
      inspection: {pending_approvals: APPROVALS.filter(item => item.status === "pending").length},
      updated_at: new Date().toISOString(),
    });
  }
  if (path.startsWith("/api/v1/tasks/")) {
    const rest = path.slice("/api/v1/tasks/".length);
    const [id, ...tail] = rest.split("/");
    if (tail[0] === "agents" && tail[2] === "conversation") {
      return json({conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
    }
    if (tail[0] === "channel-routes") return json({destinations: [], contacts: [], members: []});
    if (tail[0] === "mail" || tail[0] === "mail-cards") return json({messages: [], total: 0});
    if (!tail.length) {
      window.__fixture.taskDetailRequests.push({at: performance.now(), id});
      return json(taskDetail(id));
    }
  }
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: ${JSON.stringify(view)}}));
`;
}

const MIME = {".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".json": "application/json"};

async function startServer(tasks, approvals, view) {
  const fixture = fixtureScript(tasks, approvals, view);
  const html = buildIndex.replace('<script type="module" src="/app.js', '<script type="module" src="/fixture.js"></script>\n  <script type="module" src="/app.js');
  const server = createServer(async (request, response) => {
    const name = new URL(request.url, "http://localhost").pathname;
    try {
      response.setHeader("Content-Security-Policy", csp);
      if (name === "/" || name === "/index.html") {
        response.setHeader("Content-Type", "text/html");
        response.end(html);
        return;
      }
      if (name === "/fixture.js") {
        response.setHeader("Content-Type", "text/javascript");
        response.end(fixture);
        return;
      }
      const file = resolve(root, "web/build", name.slice(1));
      if (!file.startsWith(resolve(root, "web/build")) || !existsSync(file)) {
        response.writeHead(404).end();
        return;
      }
      response.setHeader("Content-Type", MIME[extname(file)] || "application/octet-stream");
      response.end(await readFile(file));
    } catch (error) {
      response.writeHead(500).end(String(error));
    }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  return {server, url: `http://127.0.0.1:${server.address().port}`};
}

const browser = await chromium.launch({
  executablePath,
  args: ["--no-sandbox", "--disable-dev-shm-usage", "--js-flags=--expose-gc"],
});
const results = {environment: {}, scenarios: {}};
const version = await readFile(resolve(root, "web/package.json"), "utf8");
results.environment = {
  measured_at: new Date().toISOString(),
  git: (await import("node:child_process")).execSync("git rev-parse --short HEAD", {cwd: root}).toString().trim(),
  viewport: "1280x800",
  browser: executablePath,
  node: process.version,
  build: "web/build from `npm --prefix web run build` (unminified ES modules)",
  esbuild: JSON.parse(version).devDependencies.esbuild,
  debounce: "list refresh debounce 350ms / maxWait 1000ms; detail per-task SSE debounce 100ms; fallback poll 2000ms",
};

async function newPage(tasks, approvals, view = "tasks") {
  const {server, url} = await startServer(tasks, approvals, view);
  const context = await browser.newContext({viewport: {width: 1280, height: 800}});
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => { errors.push(String(error)); process.stderr.write(`[pageerror] ${String(error)}\n`); });
  page.on("console", message => { if (message.type() === "error") process.stderr.write(`[console] ${message.text()}\n`); });
  await page.goto(`${url}/?perf=1`);
  await page.waitForFunction(() => window.__ahaPerf && window.__ahaPerf.firstScreenMs > 0, null, {timeout: 30000});
  return {server, context, page, errors};
}

async function closePage(handle) {
  await handle.context.close();
  await new Promise(done => handle.server.close(done));
}

async function heapBytes(page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Performance.enable").catch(() => {});
  await cdp.send("HeapProfiler.collectGarbage").catch(() => {});
  const usage = await cdp.send("Runtime.getHeapUsage");
  await cdp.detach();
  return usage.usedSize;
}

const perf = page => page.evaluate(() => ({...window.__ahaPerf, apiSamples: (window.__ahaPerf.apiSamples || []).map(sample => ({path: sample.path, status: sample.status, durationMs: sample.durationMs}))}));

async function measureScroll(page, durationMs = 1500, step = 40) {
  return page.evaluate(async ({durationMs, step}) => {
    const frames = [];
    let last = performance.now();
    const start = last;
    const startScroll = window.scrollY;
    await new Promise(done => {
      const tick = () => {
        const now = performance.now();
        frames.push(now - last);
        last = now;
        window.scrollBy(0, step);
        if (now - start >= durationMs) done();
        else requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
    });
    const sorted = [...frames].sort((a, b) => a - b);
    const pct = p => sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))] : 0;
    return {
      frames: frames.length,
      fps: Math.round(frames.length / (durationMs / 1000)),
      maxFrameMs: Math.round(Math.max(...frames) * 10) / 10,
      p95FrameMs: Math.round(pct(0.95) * 10) / 10,
      framesOver33ms: frames.filter(value => value > 33).length,
      framesOver50ms: frames.filter(value => value > 50).length,
      scrolledPx: Math.round(window.scrollY - startScroll),
      from: Math.round(startScroll),
      to: Math.round(window.scrollY),
    };
  }, {durationMs, step});
}

async function scenarioTasks(size) {
  const handle = await newPage(size, 0, "tasks");
  const {page, errors} = handle;
  const out = {};
  out.fixture_tasks = size;
  out.first_page_rows = await page.locator(".task-card").count();
  out.first_screen_ms = (await perf(page)).firstScreenMs;
  out.dom_rows_after_first_page = out.first_page_rows;

  // Full-page render cost of "load more": the click handler calls render()
  // synchronously before it awaits the request.
  const loadMoreSync = [];
  let guard = 0;
  while ((await page.locator(".task-card").count()) < size && guard++ < 20) {
    const rows = await page.locator(".task-card").count();
    const button = page.locator('[data-load-more="tasks"]');
    if (!(await button.count())) break;
    const timing = await page.evaluate(async previous => {
      const start = performance.now();
      const updated = new Promise(done => {
        const observer = new MutationObserver(() => {
          if (document.querySelectorAll(".task-card").length > previous) {
            observer.disconnect();
            done(performance.now() - start);
          }
        });
        observer.observe(document.body, {childList: true, subtree: true});
        setTimeout(() => { observer.disconnect(); done(-1); }, 8000);
      });
      document.querySelector('[data-load-more="tasks"]').click();
      const firstRender = performance.now() - start;
      return {first_render_ms: firstRender, update_ms: await updated};
    }, rows);
    loadMoreSync.push({
      rows_before: rows,
      rows_after: rows + 50,
      first_render_ms: Math.round(timing.first_render_ms * 10) / 10,
      full_render_to_new_rows_ms: Math.round(timing.update_ms * 10) / 10,
    });
  }
  out.load_more_full_render_ms = loadMoreSync;
  out.rows = await page.locator(".task-card").count();
  out.dom_nodes = await page.evaluate(() => document.querySelectorAll("*").length);
  out.scroll_height = await page.evaluate(() => document.documentElement.scrollHeight);
  out.heap_mb = Math.round((await heapBytes(page)) / 1048576 * 10) / 10;
  out.long_tasks = await page.evaluate(() => window.__longTasks.length);

  // Scroll.
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(150);
  out.scroll = await measureScroll(page);
  out.list_still_scrollable = await page.evaluate(() => window.scrollY > 0);

  // Click response (list -> detail).
  await page.evaluate(() => window.scrollTo(0, 0));
  const clickStart = await page.evaluate(() => {
    window.__clickStart = performance.now();
    document.querySelector(".task-card").click();
    return window.__clickStart;
  });
  await page.waitForFunction(() => document.querySelector(".task-screen") || document.querySelector(".message.error"), null, {timeout: 15000});
  const clickState = await page.evaluate(() => ({
    screen: Boolean(document.querySelector(".task-screen")),
    error: document.querySelector(".message.error")?.textContent?.trim() || "",
    body: document.body.textContent.slice(0, 400),
  }));
  if (!clickState.screen) throw new Error(`task detail did not open: ${JSON.stringify(clickState)}`);
  out.click_to_detail_ms = Math.round(await page.evaluate(start => performance.now() - start, clickStart) * 10) / 10;
  out.task_open_reported_ms = (await perf(page)).taskOpenLastMs;
  out.detail_dom_nodes = await page.evaluate(() => document.querySelectorAll("*").length);
  // Let the per-task stream report open so the fallback poll stays quiet.
  await page.evaluate(() => window.__emitTask("open", {}, "open"));
  await page.click("#back-tasks");
  await page.waitForSelector(".task-card", {timeout: 15000});
  await page.waitForTimeout(400);

  // SSE burst without a detail open: 20 events over 2s.
  await page.waitForTimeout(800);
  await page.evaluate(() => window.scrollTo(0, 1200));
  await page.waitForTimeout(100);
  const burstScrollBefore = await page.evaluate(() => Math.round(window.scrollY));
  const burstBefore = await perf(page);
  const burstLongBefore = await page.evaluate(() => window.__longTasks.length);
  const burstFetchBefore = await page.evaluate(() => window.__fixture.taskRequests.length);
  await page.evaluate(async () => {
    await new Promise(done => {
      let index = 0;
      const timer = setInterval(() => {
        window.__fixture.bumpTask(index + 1);
        window.__emitGlobal("task_status", {task_id: `task-${String(index % 10).padStart(3, "0")}`, status: "active"});
        if (++index >= 20) { clearInterval(timer); done(); }
      }, 100);
    });
  });
  await page.waitForTimeout(1500);
  const burstAfter = await perf(page);
  out.sse_burst_20_2s = {
    list_refresh_requests: burstAfter.listRefreshRequests - burstBefore.listRefreshRequests,
    task_list_fetches: (await page.evaluate(() => window.__fixture.taskRequests.length)) - burstFetchBefore,
    list_refresh_last_ms: burstAfter.listRefreshLastMs,
    avg_refresh_ms: burstAfter.listRefreshRequests ? Math.round(burstAfter.listRefreshTotalMs / burstAfter.listRefreshRequests) : 0,
    long_tasks: (await page.evaluate(() => window.__longTasks.length)) - burstLongBefore,
    rows_after: await page.locator(".task-card").count(),
    scroll_before: burstScrollBefore,
    scroll_after: await page.evaluate(() => Math.round(window.scrollY)),
    requests: await page.evaluate(() => window.__fixture.taskRequests.slice(-4)),
    loaded_count_text: await page.evaluate(() => document.querySelector(".task-count")?.textContent?.trim() || ""),
    last_row: await page.evaluate(() => [...document.querySelectorAll(".task-list article[data-task]")].slice(-1).map(row => row.dataset.task)),
  };

  // One event every 2s (the polling cadence) for 10s.
  await page.waitForTimeout(500);
  const pollBefore = await perf(page);
  const pollFetchBefore = await page.evaluate(() => window.__fixture.taskRequests.length);
  await page.evaluate(async () => {
    await new Promise(done => {
      let index = 0;
      const timer = setInterval(() => {
        window.__fixture.bumpTask(index + 2);
        window.__emitGlobal("task_status", {task_id: "task-002", status: "active"});
        if (++index >= 5) { clearInterval(timer); done(); }
      }, 2000);
    });
  });
  await page.waitForTimeout(800);
  const pollAfter = await perf(page);
  out.sse_one_event_per_2s = {
    seconds: 10,
    list_refresh_requests: pollAfter.listRefreshRequests - pollBefore.listRefreshRequests,
    task_list_fetches: (await page.evaluate(() => window.__fixture.taskRequests.length)) - pollFetchBefore,
  };

  // Detail open while the global list stream keeps firing.
  const rowsBeforeDetail = await page.locator(".task-card").count();
  await page.evaluate(() => window.scrollTo(0, 800));
  await page.waitForTimeout(100);
  const scrollBeforeDetail = await page.evaluate(() => Math.round(window.scrollY));
  const detailLogBefore = await page.evaluate(() => ({
    details: window.__fixture.taskDetailRequests.length,
    opens: window.__ahaPerf.taskOpenCount,
    card: document.querySelector(".task-card")?.dataset.task || "",
    editor: Boolean(document.querySelector(".task-card input")),
  }));
  await page.evaluate(() => document.querySelector(".task-card").click());
  await page.waitForTimeout(1500);
  const secondOpen = await page.evaluate(() => ({
    screen: Boolean(document.querySelector(".task-screen")),
    error: document.querySelector(".message.error")?.textContent?.trim() || "",
    cards: document.querySelectorAll(".task-card").length,
    active: document.activeElement?.tagName || "",
    details: window.__fixture.taskDetailRequests.length,
    opens: window.__ahaPerf.taskOpenCount,
  }));
  if (!secondOpen.screen) throw new Error(`second task open failed: ${JSON.stringify({before: detailLogBefore, after: secondOpen})}`);
  await page.evaluate(() => window.__emitTask("open", {}, "open"));
  await page.waitForTimeout(200);
  const detailBefore = await perf(page);
  const detailFetchBefore = await page.evaluate(() => window.__fixture.taskRequests.length);
  const textarea = page.locator("#message-form textarea");
  await textarea.fill("详情输入不应被抢走");
  await textarea.focus();
  const focusDetailBefore = await page.evaluate(() => ({
    tag: document.activeElement?.tagName || "",
    value: document.activeElement?.value || "",
  }));
  await page.evaluate(async () => {
    await new Promise(done => {
      let index = 0;
      const timer = setInterval(() => {
        window.__fixture.bumpTask(index + 3);
        window.__emitGlobal("task_status", {task_id: "task-003", status: "active"});
        if (++index >= 20) { clearInterval(timer); done(); }
      }, 100);
    });
  });
  await page.waitForTimeout(1200);
  const detailAfter = await perf(page);
  out.detail_open_global_sse = {
    list_refresh_requests: detailAfter.listRefreshRequests - detailBefore.listRefreshRequests,
    task_list_fetches: (await page.evaluate(() => window.__fixture.taskRequests.length)) - detailFetchBefore,
    focus_before: focusDetailBefore,
    focus_after: await page.evaluate(() => ({
      tag: document.activeElement?.tagName || "",
      value: document.activeElement?.value || "",
    })),
  };
  const backBefore = await perf(page);
  await page.click("#back-tasks");
  await page.waitForSelector(".task-card", {timeout: 15000});
  await page.waitForFunction(count => window.__ahaPerf.listRefreshRequests > count, backBefore.listRefreshRequests, {timeout: 10000});
  await page.waitForTimeout(400);
  const backAfter = await perf(page);
  out.detail_close_catchup = {
    list_refresh_requests: backAfter.listRefreshRequests - backBefore.listRefreshRequests,
    list_refresh_last_ms: backAfter.listRefreshLastMs,
    rows_before_detail: rowsBeforeDetail,
    rows_after_close: await page.locator(".task-card").count(),
    scroll_before_detail: scrollBeforeDetail,
    scroll_after_close: await page.evaluate(() => Math.round(window.scrollY)),
  };

  // Focus preservation across an SSE list refresh, last because closing the
  // title editor triggers a full loadAll() that would invalidate the rows above.
  const rowsBeforeFocus = await page.locator(".task-card").count();
  await page.evaluate(() => {
    const row = document.querySelector(".task-card");
    row.querySelector("[data-edit-task-title]")?.click();
  });
  await page.waitForTimeout(100);
  const editor = page.locator(".task-card input.task-title-input").first();
  if (await editor.count()) {
    await editor.fill("测量中的标题");
    await editor.focus();
    await page.evaluate(() => window.scrollTo(0, 600));
    const before = await perf(page);
    const focusBefore = await page.evaluate(() => ({
      tag: document.activeElement?.tagName || "",
      value: document.activeElement?.value || "",
      scrollY: Math.round(window.scrollY),
    }));
    await page.evaluate(async () => {
      await new Promise(done => {
        let index = 0;
        const timer = setInterval(() => {
          window.__fixture.bumpTask(index);
          window.__emitGlobal("task_status", {task_id: "task-000", status: "active"});
          if (++index >= 10) { clearInterval(timer); done(); }
        }, 100);
      });
    });
    await page.waitForFunction(count => window.__ahaPerf.listRefreshRequests > count, before.listRefreshRequests, {timeout: 10000});
    await page.waitForTimeout(300);
    const after = await perf(page);
    out.focus_refresh = {
      rows_before: rowsBeforeFocus,
      list_refresh_requests: after.listRefreshRequests - before.listRefreshRequests,
      list_refresh_last_ms: after.listRefreshLastMs,
      focus_before: focusBefore,
      focus_after: await page.evaluate(() => ({
        tag: document.activeElement?.tagName || "",
        value: document.activeElement?.value || "",
        scrollY: Math.round(window.scrollY),
      })),
      rows_after: await page.locator(".task-card").count(),
    };
  } else {
    out.focus_refresh = {skipped: "no title editor"};
  }

  out.page_errors = errors;
  await closePage(handle);
  return out;
}

async function scenarioApprovals(size) {
  const handle = await newPage(0, size, "pet-workbench");
  const {page, errors} = handle;
  const out = {fixture_approvals: size};
  await page.click('[data-pet-workbench-tab="approvals"]');
  await page.waitForSelector("#pet-approval-rows-host .pet-approval-row", {timeout: 15000});
  out.first_page_rows = await page.locator(".pet-approval-row").count();
  out.first_screen_ms = (await perf(page)).firstScreenMs;

  const redraw = [];
  let guard = 0;
  const target = Math.min(size, 200);
  while ((await page.locator(".pet-approval-row").count()) < target && guard++ < 25) {
    const rows = await page.locator(".pet-approval-row").count();
    const button = page.locator('[data-pet-load-more="approval"]');
    if (!(await button.count())) break;
    const timing = await page.evaluate(async previous => {
      const start = performance.now();
      const updated = new Promise(done => {
        const observer = new MutationObserver(() => {
          if (document.querySelectorAll(".pet-approval-row").length > previous) {
            observer.disconnect();
            done(performance.now() - start);
          }
        });
        observer.observe(document.body, {childList: true, subtree: true});
        setTimeout(() => { observer.disconnect(); done(-1); }, 8000);
      });
      document.querySelector('[data-pet-load-more="approval"]').click();
      const firstRender = performance.now() - start;
      return {first_render_ms: firstRender, update_ms: await updated};
    }, rows);
    const after = await page.locator(".pet-approval-row").count();
    redraw.push({
      rows_before: rows,
      rows_after: after,
      first_render_ms: Math.round(timing.first_render_ms * 10) / 10,
      full_render_to_new_rows_ms: Math.round(timing.update_ms * 10) / 10,
      new_rows: after - rows,
    });
  }
  out.load_sequence = redraw;
  out.rows = await page.locator(".pet-approval-row").count();
  out.dom_nodes = await page.evaluate(() => document.querySelectorAll("*").length);
  out.scroll_height = await page.evaluate(() => document.documentElement.scrollHeight);
  out.heap_mb = Math.round((await heapBytes(page)) / 1048576 * 10) / 10;
  out.cap_hint = await page.evaluate(() => document.querySelector('[data-pet-load-more="approval"]')?.textContent.trim() || document.querySelector(".pet-approval-limit-hint")?.textContent.trim() || "");

  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(150);
  out.scroll = await measureScroll(page);

  // Full redraw of the loaded list when switching tabs away and back (cached view).
  const beforeSwitch = await perf(page);
  const switchMs = await page.evaluate(() => {
    const started = performance.now();
    document.querySelector('[data-pet-workbench-tab="overview"]').click();
    document.querySelector('[data-pet-workbench-tab="approvals"]').click();
    return performance.now() - started;
  });
  out.tab_switch_full_redraw_ms = Math.round(switchMs * 10) / 10;
  out.tab_switch_rows = await page.locator(".pet-approval-row").count();
  void beforeSwitch;

  // Approval click -> detail dialog latency.
  const clickMs = await page.evaluate(() => {
    const started = performance.now();
    document.querySelector(".pet-approval-row").click();
    return performance.now() - started;
  });
  out.click_to_detail_ms = Math.round(clickMs * 10) / 10;
  out.detail_dialog_open = await page.evaluate(() => Boolean(document.querySelector("dialog[open]")));
  await page.keyboard.press("Escape");
  await page.waitForTimeout(100);

  // pet_approval SSE burst: what updates today?
  const countBefore = await page.evaluate(() => window.__fixture.pendingCountRequests);
  const workbenchBefore = await page.evaluate(() => window.__fixture.workbenchRequests.length);
  const rowsBefore = await page.locator(".pet-approval-row").count();
  await page.evaluate(async () => {
    await new Promise(done => {
      let index = 0;
      const timer = setInterval(() => {
        window.__emitGlobal("pet_approval", {approval_id: "approval-000"});
        if (++index >= 10) { clearInterval(timer); done(); }
      }, 100);
    });
  });
  await page.waitForTimeout(800);
  out.sse_pet_approval_10_1s = {
    pending_count_requests: (await page.evaluate(() => window.__fixture.pendingCountRequests)) - countBefore,
    workbench_requests: (await page.evaluate(() => window.__fixture.workbenchRequests.length)) - workbenchBefore,
    rows_before: rowsBefore,
    rows_after: await page.locator(".pet-approval-row").count(),
    long_tasks: await page.evaluate(() => window.__longTasks.length),
  };
  const added = await page.evaluate(() => {
    const id = window.__fixture.addApproval();
    window.__emitGlobal("pet_approval", {approval_id: id});
    return id;
  });
  await page.waitForTimeout(800);
  out.new_approval_visible_after_event = await page.locator(`[data-pet-approval-open="${added}"]`).count();
  out.pending_badge_after_add = await page.evaluate(() => document.querySelector("#pet-approval-count")?.textContent.trim() || "");
  out.count_requests_after_add = await page.evaluate(() => window.__fixture.pendingCountRequests);

  out.page_errors = errors;
  await closePage(handle);
  return out;
}

console.log = () => {};
const only = process.env.AHA_SCENARIO || "";
if (!only || only === "tasks_200") results.scenarios.tasks_200 = await scenarioTasks(200);
if (!only || only === "tasks_500") results.scenarios.tasks_500 = await scenarioTasks(500);
if (!only || only === "approvals_190") results.scenarios.approvals_190 = await scenarioApprovals(190);
if (!only || only === "approvals_500") results.scenarios.approvals_500 = await scenarioApprovals(500);
await browser.close();

const text = JSON.stringify(results, null, 2);
await writeFile(process.env.AHA_MEASURE_OUTPUT || resolve(process.cwd(), "list-refresh-measure-result.json"), text);
process.stdout.write(text + "\n");