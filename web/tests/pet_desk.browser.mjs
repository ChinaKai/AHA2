// Browser verification for the pet desk overview (task-813, design doc
// section 10 items 1, 4, 5, 10, 12, 13 and 15; section 11.2 items 2, 4, 5, 6
// and 7).
//
// Checks against the real built Web bundle with a stubbed API:
//   - with `desk` the overview renders the desk blocks in order, the waiting
//     and running columns sit side by side on a wide screen and stack on a
//     phone, and the English UI shows no Chinese interface copy;
//   - a task row opens the Task, an approval row and an idea open the existing
//     approval detail (also when the approval is older than the loaded page),
//     a channel row jumps to the channel cards and "see all" opens Records;
//   - the desk buttons still work after the body is repainted;
//   - without `desk` the overview falls back to the pre-desk layout.
//
// Dependencies stay in .tools/web-validation; a machine without that directory
// can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE at a local install.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const minutesAgo = minutes => new Date(Date.now() - minutes * 60_000).toISOString();
// The newest approval page holds only the idea; the desk approval a-old is
// older than that page, so opening it must read the wider approval list.
const pageApprovals = [
  {id: "idea-1", task_id: "pet-task", kind: "idea", title: "整理渠道联系人", summary: "想法说明", risk: "low", status: "pending", created_at: minutesAgo(10)},
];
const allApprovals = [
  ...pageApprovals,
  {id: "a-old", task_id: "pet-task", kind: "owner_decision", title: "是否合并 task-801 的候选", summary: "宠物的理由摘要", risk: "high", status: "pending", created_at: minutesAgo(60)},
];
// ?history=1 serves an approval history of 14 rows in mixed states, paged 10
// at a time like the server: the records tab's history group must show every
// status newest first, filter by kind and status, and load the next page.
const historyApprovals = Array.from({length: 14}, (_, i) => ({
  id: `h-${String(i).padStart(2, "0")}`, task_id: "pet-task", kind: i % 4 === 0 ? "idea" : "owner_decision",
  title: `历史审批 ${i}`, summary: "历史", risk: "low",
  status: ["approved", "rejected", "cancelled", "pending"][i % 4],
  created_at: minutesAgo(1000 + i * 60), resolved_at: i % 4 === 3 ? "" : minutesAgo(900 + i * 60),
}));
const desk = {
  needs_owner: [
    {kind: "blocked", task_id: "t-790", task_code: "task-790", title: "需要一个权限才能继续", detail: "缺少权限", since: minutesAgo(90)},
    {kind: "approval", approval_id: "a-old", approval_kind: "owner_decision", risk: "high", task_id: "t-801", task_code: "task-801", title: "是否合并 task-801 的候选", detail: "宠物的理由摘要", since: minutesAgo(60)},
    {kind: "question", task_id: "t-813", task_code: "task-813", title: "宠物工作台改造计划", questions: ["先只做第一步？", "只出计划？"], since: minutesAgo(45)},
  ],
  needs_owner_total: 3,
  later: [
    {kind: "idea", approval_id: "idea-1", title: "整理渠道联系人", since: minutesAgo(10)},
    {kind: "next_step", task_id: "t-3", task_code: "task-3", title: "做完一轮的任务", detail: "等评审意见", since: minutesAgo(200)},
  ],
  later_total: 2,
  waiting_others: [
    {kind: "review", task_id: "t-805", task_code: "task-805", title: "评审中的任务", on: "task-806", since: minutesAgo(50)},
    {kind: "channel", title: "飞书群 A", on: "c-1", count: 3, since: minutesAgo(4)},
  ],
  waiting_others_total: 2,
  running: [
    {task_id: "t-811", task_code: "task-811", title: "跑过夜稳定性对比", status: "active", turn_status: "running", detail: "正在对比", hardware: ["调试板 1"], since: minutesAgo(12)},
  ],
  running_total: 1,
  corrections: [{at: minutesAgo(300), approval_id: "a-9", kind: "delete_workspace", title: "删除工作区 X", owner_note: "这个还要留着对比"}],
  open_loops: ["跟进合作方答复"], open_loops_total: 5,
  agenda: ["整理渠道"], agenda_total: 3,
  last_reply: {text: "推进了 task-790，等 owner 批权限", finished_at: minutesAgo(20), noop: false, from_wake: true},
};
desk.waiting_others.push({kind: "loop", title: "等合作方答复接口格式", detail: "周五再问一次", on: "core/loops.md", since: "0001-01-01T00:00:00Z"});
desk.waiting_others_total = desk.waiting_others.length;
// Layout fixtures (owner's 2026-11-26 screenshot): "owner" has the same
// amount of content as the live desk, "large" is the server's upper bound.
const longReply = `这轮把 0.5.577 的来源查清了，不用你再解释：它的 commit \`061d1cf\` 就是 **task-813「宠物工作台改造计划」三步的合并提交**——那个 Task 13:53 说三步都合进 main、可以部署体验，14:18 就有人用同一个部署 Skill 把它装上了。已把这层关系记进 loops 和 worklog，0.5.577 运行正常（health ok，渠道 ready）。\n\n另外做了一件小事：**watch 了 task-813**。它改的是我的工作台（近 7 天、宠物上一轮、等待项、记录页），和我直接相关，但现在是你在推动、Opus 在做台账修复，我不插手，只在它收尾或部署后按真实数据体验并反馈。task-276 仍等王小二修两处统计问题；__其余项目__没有新的异常，渠道都在线，下一轮继续看部署后的真实数据和台账修复的评审结论，有变化再提给你。`.padEnd(400, "。");
const ownerDesk = {
  needs_owner: [{kind: "question", task_id: "t-276", task_code: "task-276", title: "Bulbcam本地录制改标志MP4+录制音频开关", questions: ["App 原因码与文案未定：只新增设备内部详情，不改现有异常 3/格式化 4。", "退避例外及 SD/云警检出帧口径、容差与降级，默认开关和直播影响阈值待确认。", "Wi-Fi 产测 FEATURE_ID 与低内存 RX 策略待正式整合前确定。"], since: minutesAgo(11)}],
  needs_owner_total: 1, later: [], later_total: 0, waiting_others: [], waiting_others_total: 0,
  running: [{task_id: "t-813", task_code: "task-813", title: "宠物工作台改造计划", status: "active", turn_status: "running", since: minutesAgo(11),
    detail: "owner 说“删除并修复”：原任务分支已删除（已全部在 main 里）；宠物接口覆盖台账生成器的修复已派给 Opus，批次 collaboration_batch_996a7a35db9d7209c7a6，在新分支 aha/task_e40adf343e3e375b5f7c-coverage（从 main 061d1cf 建）上做，进行中。要修的是：扫描器不认裸函数名的 handler（server.go 三处 workspaceViewerMethodNotAllowed），之后还卡在 registerWorkspaceTerminalRoutes 未分类；修好后用 -update 重新生成三份台账。"}],
  running_total: 1, corrections: [], open_loops: [], open_loops_total: 0, agenda: [], agenda_total: 0,
  last_reply: {text: longReply, finished_at: minutesAgo(5), noop: false, from_wake: true},
};
const largeDesk = {
  ...ownerDesk,
  needs_owner: Array.from({length: 50}, (_, i) => ({kind: i % 2 ? "blocked" : "question", task_id: `t-n${i}`, task_code: `task-n${i}`, title: `要你定的第 ${i + 1} 件事`, detail: "说明文字".repeat(30), questions: ["问题一？", "问题二？"], since: minutesAgo(100 - i)})),
  needs_owner_total: 50,
  waiting_others: Array.from({length: 30}, (_, i) => ({kind: "review", task_id: `t-w${i}`, task_code: `task-w${i}`, title: `等别人的第 ${i + 1} 件`, on: "task-999", since: minutesAgo(40 + i)})),
  waiting_others_total: 30,
  running: Array.from({length: 30}, (_, i) => ({...ownerDesk.running[0], task_id: `t-r${i}`, task_code: `task-r${i}`})),
  running_total: 30,
};
const ownerInsights = {
  from: minutesAgo(7 * 24 * 60), to: minutesAgo(0), actions: 311,
  approvals: {total: 67, approved: 40, rejected: 14, cancelled: 3, pending: 10, ideas: 1},
  corrections: 14, wakes: {turns: 1497, noop: 194, tokens: 98765432, token_scope: "window"},
};
const insights = {
  from: minutesAgo(8 * 24 * 60), to: minutesAgo(0), actions: 12,
  approvals: {total: 5, approved: 2, rejected: 1, cancelled: 1, pending: 1, ideas: 3},
  corrections: 2, wakes: {turns: 40, noop: 31, tokens: 123456, token_scope: "window"},
};
const watches = [
  {task_id: "t-w", code: "task-9", title: "关注的任务", status: "active", watched: true, ignored: false, reason: "manual", created_at: minutesAgo(600), updated_at: minutesAgo(30)},
];
// The layout desks also carry ignored and unfollowed tasks, so all three
// follow lists render.
const layoutWatches = [
  ...watches,
  ...Array.from({length: 20}, (_, i) => ({task_id: `t-ig${i}`, code: `task-ig${i}`, title: `忽略的任务 ${i}`, status: "active", watched: true, ignored: true, reason: "auto", created_at: minutesAgo(900), updated_at: minutesAgo(60 + i)})),
  ...Array.from({length: 20}, (_, i) => ({task_id: `t-un${i}`, code: `task-un${i}`, title: `未关注的任务 ${i}`, status: "active", watched: false, ignored: false, reason: "auto", created_at: minutesAgo(900), updated_at: minutesAgo(80 + i)})),
];

// Follow-list sizes for the always-open lists: ?watches=small is watched 1 /
// ignored 2 / unfollowed 1, ?watches=huge is 200 / 300 / 50.
const watchSet = (watched, ignored, unfollowed) => [
  ...Array.from({length: watched}, (_, i) => ({task_id: `t-fw${i}`, code: `task-fw${i}`, title: `关注 ${i}`, status: "active", watched: true, ignored: false, reason: "manual", created_at: minutesAgo(900), updated_at: minutesAgo(30 + i)})),
  ...Array.from({length: ignored}, (_, i) => ({task_id: `t-fi${i}`, code: `task-fi${i}`, title: `忽略 ${i}`, status: "active", watched: true, ignored: true, reason: "auto", created_at: minutesAgo(900), updated_at: minutesAgo(60 + i)})),
  ...Array.from({length: unfollowed}, (_, i) => ({task_id: `t-fu${i}`, code: `task-fu${i}`, title: `未关注 ${i}`, status: "active", watched: false, ignored: false, reason: "auto", created_at: minutesAgo(900), updated_at: minutesAgo(80 + i)})),
];
const watchSets = {small: watchSet(1, 2, 1), huge: watchSet(200, 300, 50)};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const params = new URLSearchParams(location.search);
const layoutDesks = {owner: ${JSON.stringify(ownerDesk)}, large: ${JSON.stringify(largeDesk)}};
const watchSets = ${JSON.stringify(watchSets)};
window.__desk = params.get("desk") === "0" ? undefined : layoutDesks[params.get("desk")] || ${JSON.stringify(desk)};
const weekInsights = layoutDesks[params.get("desk")] ? ${JSON.stringify(ownerInsights)} : ${JSON.stringify(insights)};
window.__workbenchQueries = [];
window.__taskReads = [];
window.__approvalReads = [];
window.__insightReads = 0;
// insights=fail answers 500, insights=old answers 404 like a server without
// the route, insights=slow holds the response until __releaseInsights().
window.__insightsMode = params.get("insights") || "ok";
window.__recordsMode = params.get("records") || "ok";
window.__recordsData = {
  correction_history: params.get("corrections") === "none" ? [] : [
    {at: new Date(Date.now() - 3600e3).toISOString(), approval_id: "a-9", kind: "delete_workspace", title: "删除工作区 X", owner_note: "这个还要留着对比", state: "open"},
    {at: new Date(Date.now() - 3 * 86400e3).toISOString(), approval_id: "a-8", kind: "owner_decision", title: "合并 task-700", owner_note: "先评审再合", state: "absorbed"},
    {at: new Date(Date.now() - 9 * 86400e3).toISOString(), approval_id: "a-7", kind: "owner_decision", title: "换模型", owner_note: "等下个月", state: "expired"},
  ],
  ledgers: [{name: "loops.md", counts: {open: 2, waiting: 1}, total: 3}, {name: "agenda.md", counts: {next: 1}, total: 1}],
  knowledge: {
    entries: 42, pending: 2, helped: 7, helped_partial: true, truncated: false, failed: 0, projects: 3, elapsed_ms: 4, spaces: [],
    pending_items: [{id: "k-1", title: "过时的部署说明", scope: "project", project_id: "p-1", space: "AHA2", status: "stale"}],
  },
};
window.__insightWaiters = [];
window.__releaseInsights = () => { for (const resolve of window.__insightWaiters.splice(0)) resolve(); };
const pageApprovals = ${JSON.stringify(pageApprovals)};
const allApprovals = ${JSON.stringify(allApprovals)};
const historyApprovals = ${JSON.stringify(historyApprovals)};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: params.get("language") || "en-US"}});
  if (path === "/api/v1/system") return json({system: {web_version: "test", version: "test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/pet/approvals/pending-count") return json({ok: true, pending: 2});
  if (path.startsWith("/api/v1/pet/workbench/core/") && (init.method || "GET").toUpperCase() === "PUT") {
    window.__savedDocs = [...(window.__savedDocs || []), decodeURIComponent(path.split("/").pop())];
    return json({ok: true});
  }
  if (path.startsWith("/api/v1/pet/watches/")) (window.__watchWrites ||= []).push([(init.method || "GET").toUpperCase(), decodeURIComponent(path.split("/").pop())]);
  if (path.startsWith("/api/v1/pet/approvals/") && (init.method || "GET").toUpperCase() === "GET") {
    const id = decodeURIComponent(path.split("/")[5]);
    window.__approvalReads.push(id);
    const item = allApprovals.find(approval => approval.id === id);
    return item ? json({ok: true, approval: item}) : json({ok: false, error: "pet_approval_not_found"}, 404);
  }
  if (path === "/api/v1/pet/workbench/insights" && url.searchParams.get("records") === "1") {
    // The records tab's read (task-813 step three). Counted apart so the
    // overview's insights checks keep their meaning.
    window.__recordsReads = (window.__recordsReads || 0) + 1;
    if (window.__recordsMode === "fail") return json({ok: false, error: "pet_insights_failed"}, 500);
    if (window.__recordsMode === "slow") {
      (window.__recordsSignals ||= []).push(init.signal);
      await new Promise(resolve => (window.__recordsWaiters ||= []).push(resolve));
    }
    return json({ok: true, insights: {...${JSON.stringify(insights)}, ...window.__recordsData}});
  }
  if (path === "/api/v1/pet/workbench/insights") {
    window.__insightReads++;
    if (window.__insightsMode === "fail") return json({ok: false, error: "pet_insights_failed"}, 500);
    if (window.__insightsMode === "old") return new Response("404 page not found", {status: 404});
    if (window.__insightsMode === "slow") {
      (window.__insightSignals ||= []).push(init.signal);
      await new Promise(resolve => window.__insightWaiters.push(resolve));
    }
    return json({ok: true, insights: weekInsights});
  }
  if (path === "/api/v1/pet/workbench") {
    const limit = Number(url.searchParams.get("approval_limit") || 50);
    window.__workbenchQueries.push(limit);
    if (window.__workbenchFailAfter && window.__workbenchQueries.length > 1) return json({ok: false, error: "workbench_refresh_failed"}, 500);
    const history = params.get("history") === "1";
    const approvals = history ? historyApprovals.slice(0, limit) : limit >= 200 ? allApprovals : pageApprovals;
    return json({
      ok: true, core: [{name: "loops.md", content: "loops"}, {name: "identity.md", content: "identity"}, {name: "principles.md", content: "principles"}, {name: "agenda.md", content: "agenda"}], worklog: [{date: "2026-11-25", content: "做了一件事"}], worklog_total: 1, approvals, approval_total: history ? historyApprovals.length : allApprovals.length,
      approval_counts: history ? Object.values(historyApprovals.reduce((groups, item) => { const key = (item.kind === "idea" ? "idea" : "owner_decision") + item.status; groups[key] = groups[key] || {kind: item.kind, status: item.status, count: 0}; groups[key].count++; return groups; }, {})) : [{kind: "idea", status: "pending", count: 1}, {kind: "owner_decision", status: "pending", count: 1}],
      watches: watchSets[params.get("watches")] || (layoutDesks[params.get("desk")] ? ${JSON.stringify(layoutWatches)} : ${JSON.stringify(watches)}), updated_at: new Date().toISOString(),
      inspection: {health: "ok", version: "9.9.9", web_version: "web-1", uptime_minutes: 125, checked_at: new Date().toISOString(), pending_approvals: 2},
      channels: [{id: "c-1", name: "Mail", status: "degraded", pending: 3, degraded: true}], channel_total: 1,
      ...(window.__desk ? {desk: window.__desk} : {}),
    });
  }
  if (path.startsWith("/api/v1/tasks/")) {
    window.__taskReads.push(path.split("/")[4]);
    return json({ok: false, error: "task_stub"}, 404);
  }
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false});
  if (path === "/api/v1/projects") return json({projects: [], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/models") return json({models: []});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
// The global stream can be driven from the test: __emit(type) sends an
// "update" event, __reopen() fires "open" as after a reconnect.
window.__sources = [];
window.EventSource = class {
  constructor(url) { this.url = String(url); this.listeners = {}; window.__sources.push(this); }
  close() { this.closed = true; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
};
window.__emit = type => { for (const source of window.__sources) if (!source.closed && source.url.startsWith("/api/v1/events")) for (const fn of source.listeners.update || []) fn({data: JSON.stringify({type, data: {}})}); };
window.__reopen = () => { for (const source of window.__sources) if (!source.closed && source.url.startsWith("/api/v1/events")) for (const fn of source.listeners.open || []) fn({}); };
// ?workbench=fail-after makes every workbench read after the first fail.
window.__workbenchFailAfter = params.get("workbench") === "fail-after";
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "pet-workbench"}));
`;

const html = `<!doctype html><html lang="en-US"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><div id="app-status" class="visually-hidden" role="status" aria-live="polite" aria-atomic="true"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;

// While deskChunkFailures > 0 the server refuses the lazy desk module, so the
// test can prove a failed chunk shows a retry card instead of a blank tab.
let deskChunkFailures = 0;
let deskChunkRequests = 0;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    if (name === "/pet_desk.js") {
      deskChunkRequests++;
      if (deskChunkFailures > 0) {
        deskChunkFailures--;
        response.writeHead(503).end();
        return;
      }
    }
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name === "/favicon.ico") {
      response.writeHead(204).end();
    } else if (name.startsWith("/assets/")) {
      response.setHeader("Content-Type", name.endsWith(".svg") ? "image/svg+xml" : "image/png");
      response.end(await readFile(resolve(root, "web/build", name.replace(/^\//, ""))));
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

// Same scan as pet_i18n.browser.mjs: interface copy must be English; server
// content is marked data-i18n-ignore by the desk renderer.
const untranslated = page => page.evaluate(() => {
  const body = document.querySelector("#pet-workbench-body");
  // .pet-approval-row-main strong is the approval title (server text) in the
  // shared approval row, skipped the same way in pet_i18n.browser.mjs.
  const skip = "[data-i18n-ignore],.pet-watch-row strong,.pet-approval-row-main strong,pre,code,textarea,input";
  const out = [];
  const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) {
    const el = walker.currentNode.parentElement;
    if (!el || el.closest(skip)) continue;
    const text = (walker.currentNode.nodeValue || "").trim();
    if (text && /[一-鿿]/.test(text)) out.push(text);
  }
  for (const el of body.querySelectorAll("[title],[aria-label]")) {
    if (el.closest(skip)) continue;
    for (const name of ["title", "aria-label"]) {
      const value = el.getAttribute(name);
      if (value && /[一-鿿]/.test(value)) out.push(`${name}=${value}`);
    }
  }
  return out;
});

const openOverview = async (page, query = "") => {
  await page.goto(`${url}/${query}`, {waitUntil: "domcontentloaded"});
  await page.waitForSelector("#pet-workbench-body .pet-overview", {timeout: 20000});
};
const backToOverview = async page => {
  await page.click('[data-pet-workbench-tab="overview"]');
  await page.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 15000});
};
const closeDialog = async page => {
  await page.click("dialog.pet-approval-detail-dialog [data-pet-approval-detail-close]");
  await page.waitForSelector("dialog.pet-approval-detail-dialog", {state: "detached"});
};

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  const page = await browser.newPage({viewport: {width: 1400, height: 1000}, locale: "en-US"});
  const errors = [];
  // The Task stub answers 404 on purpose: this test only proves the click asks
  // for the right Task. The task page's own handling of that 404 is not under
  // test here, so its stub error is not a desk failure.
  page.on("pageerror", error => { if (error.message !== "task_stub") errors.push(error.message); });

  await openOverview(page);
  const body = page.locator("#pet-workbench-body");
  await body.locator(".pet-desk").waitFor();

  // Item 1: block order; cards and recent activity are gone.
  const blocks = await page.evaluate(() => [...document.querySelector("#pet-workbench-body .pet-desk").children].map(el => el.className));
  assert.deepEqual(blocks, ["pet-desk-status", "pet-desk-reply", "pet-desk-board", "pet-desk-follows"]);
  const columnBlocks = await page.evaluate(() => [...document.querySelectorAll("#pet-workbench-body .pet-desk-col")].map(col => [col.classList[1], [...col.children].map(el => el.id)]));
  assert.deepEqual(columnBlocks, [["pet-desk-col-main", ["pet-desk-needs"]], ["pet-desk-col-side", ["pet-desk-waiting", "pet-desk-pet"]], ["pet-desk-col-run", ["pet-desk-running", "pet-desk-insights"]]]);
  // Step two, item 4: the last reply sits under the status row.
  assert.match(await body.locator(".pet-desk-reply").innerText(), /Pet's last check[\s\S]*推进了 task-790/);
  // Item 2: the last 7 days fill in after the desk.
  await body.locator("#pet-desk-insights .pet-desk-insights-list").waitFor({timeout: 15000});
  const week = await body.locator("#pet-desk-insights").innerText();
  assert.match(week, /Handled by the pet: 12/);
  assert.match(week, /\d{4}-\d\d-\d\d to today/, "the range shows dates only");
  assert.doesNotMatch(week, /\d\d:\d\d:\d\d/);
  assert.match(week, /Put to you: 5\s+approved 2 · rejected 1 · cancelled 1 · still waiting 1/);
  assert.match(week, /Wake-ups: 40, 31 with nothing to move\s+Tokens in these 7 days: 123,456/);
  // Item 5: the pet's own waiting note shows with its next step.
  const loopRow = body.locator("#pet-desk-waiting .pet-desk-item.kind-loop");
  assert.match(await loopRow.innerText(), /等合作方答复接口格式[\s\S]*Waiting \(pet's notes\)[\s\S]*core\/loops\.md[\s\S]*Next: 周五再问一次/);
  assert.equal(await body.locator(".pet-overview-cards, .pet-overview-recent").count(), 0);
  assert.equal(await body.locator("#pet-overview-follows, #pet-overview-ignored").count(), 2);
  // Wide screen: waiting and running side by side; phone: stacked.
  const columns = () => page.evaluate(() => {
    const box = id => document.getElementById(id).getBoundingClientRect();
    const waiting = box("pet-desk-waiting");
    const running = box("pet-desk-running");
    return {sameRow: Math.abs(waiting.top - running.top) < 2 && running.left > waiting.right - 1, stacked: running.top >= waiting.bottom - 1};
  });
  assert.equal((await columns()).sameRow, true, "wide screen puts the two columns side by side");
  await page.setViewportSize({width: 390, height: 844});
  assert.equal((await columns()).stacked, true, "a phone stacks the two columns");
  const overflow = await page.evaluate(() => document.querySelector("#pet-workbench-body").scrollWidth - document.querySelector("#pet-workbench-body").clientWidth);
  assert.ok(overflow <= 1, `the desk must not scroll sideways on a phone (${overflow}px)`);
  await page.setViewportSize({width: 1400, height: 1000});

  // Item 13: the English desk has no Chinese interface copy.
  await body.locator("#pet-desk-later summary").click();
  assert.deepEqual(await untranslated(page), [], "English desk left untranslated Chinese");
  const status = await body.locator(".pet-desk-status").innerText();
  assert.match(status, /3 for you/);
  assert.match(status, /3 waiting on others/);
  assert.match(status, /1 running/);
  assert.match(await body.locator(".pet-desk-status").getAttribute("title"), /Version: 9\.9\.9/);
  // Item 3: numbering from ① in response order.
  assert.deepEqual(await body.locator("#pet-desk-needs .pet-desk-index").allInnerTexts(), ["①", "②", "③"]);
  // Item 5: the later list starts folded on every fresh render.
  await page.click('[data-pet-workbench-tab="records"]');
  await backToOverview(page);
  assert.equal(await body.locator("#pet-desk-later").getAttribute("open"), null, "later starts collapsed");

  // Item 4 (step two, item 7): an approval older than the loaded approval
  // page opens its detail through GET /api/v1/pet/approvals/{id}, without
  // re-reading the whole workbench.
  const queriesBefore = await page.evaluate(() => window.__workbenchQueries.length);
  await body.locator('#pet-desk-needs [data-pet-approval-open="a-old"].pet-desk-item-main').click();
  await page.waitForSelector("dialog.pet-approval-detail-dialog[open]", {timeout: 15000});
  assert.match(await page.locator("dialog.pet-approval-detail-dialog").innerText(), /是否合并 task-801 的候选/);
  assert.deepEqual(await page.evaluate(() => window.__approvalReads), ["a-old"]);
  assert.equal(await page.evaluate(() => window.__workbenchQueries.length), queriesBefore, "no whole-page re-read");
  await closeDialog(page);
  assert.equal(await page.locator('[data-pet-workbench-tab="overview"]').getAttribute("class"), "active", "still on the overview");
  // An idea from the folded later list opens from the loaded page directly.
  const queriesBeforeIdea = await page.evaluate(() => window.__workbenchQueries.length);
  await body.locator("#pet-desk-later summary").click();
  await body.locator('#pet-desk-later [data-pet-approval-open="idea-1"].pet-desk-item-main').click();
  await page.waitForSelector("dialog.pet-approval-detail-dialog[open]");
  assert.match(await page.locator("dialog.pet-approval-detail-dialog").innerText(), /整理渠道联系人/);
  assert.equal(await page.evaluate(() => window.__workbenchQueries.length), queriesBeforeIdea, "a loaded approval needs no extra read");
  assert.deepEqual(await page.evaluate(() => window.__approvalReads), ["a-old"], "nor a by-id read");
  await closeDialog(page);
  // No approve/reject buttons on the desk itself.
  assert.equal(await body.locator("[data-pet-approval-decision]").count(), 0);

  // Item 12: after a repaint (tab round trip) the desk buttons still work.
  await page.click('[data-pet-workbench-tab="records"]');
  await page.waitForSelector("#pet-record-approvals [data-pet-approval-filters-open]");
  await backToOverview(page);
  await body.locator("#pet-desk-pet [data-pet-overview-jump=\"records\"]").click();
  await page.waitForSelector('[data-pet-workbench-tab="records"].active');
  await body.locator(".pet-records").waitFor();
  await backToOverview(page);

  // A channel row jumps to the channel cards.
  await body.locator('#pet-desk-waiting [data-pet-overview-jump="channels"]').click();
  await page.waitForSelector(".pet-workbench-page", {state: "detached", timeout: 15000});
  await page.waitForSelector('.sidebar [data-view="channels"].active', {timeout: 15000});
  await page.locator('.sidebar [data-view="pet-workbench"]').click();
  await page.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});

  // A task row opens the Task.
  await page.locator('#pet-desk-needs .pet-desk-item-main[data-pet-overview-task="t-790"]').click();
  await page.waitForFunction(() => window.__taskReads.includes("t-790"), null, {timeout: 15000});
  await page.waitForSelector(".pet-workbench-page", {state: "detached"});

  // Item 10: without desk the pre-desk overview comes back.
  await openOverview(page, "?desk=0");
  assert.equal(await body.locator(".pet-desk").count(), 0);
  assert.equal(await body.locator(".pet-overview-cards").count(), 1);
  assert.equal(await body.locator(".pet-overview-recent").count(), 1);
  assert.deepEqual(await untranslated(page).then(items => items.filter(text => !/整理渠道联系人|关注的任务/.test(text))), []);

  // The Chinese desk renders the same structure.
  await openOverview(page, "?language=zh-CN");
  await body.locator(".pet-desk").waitFor();
  assert.match(await body.locator("#pet-desk-needs h3").innerText(), /要你定（3）/);
  assert.match(await body.locator("#pet-desk-later summary").innerText(), /不急的还有 2 件/);

  // Step two, item 6: a same-view full render (a hash change re-runs render()
  // for the same view) keeps the lazily loaded page working, and a slow
  // insights read that lands after the render fills the live block.
  await openOverview(page, "?insights=slow");
  await body.locator(".pet-desk").waitFor();
  await page.waitForFunction(() => window.__insightReads === 1);
  const bodyHandle = await page.evaluateHandle(() => document.querySelector("#pet-workbench-body"));
  for (let round = 1; round <= 3; round++) {
    await page.evaluate(round => { location.hash = `#pet-workbench?render=${round}`; }, round);
    await page.waitForFunction(round => location.hash.endsWith(String(round)), round);
  }
  await page.waitForTimeout(200);
  assert.equal(await page.evaluate(body => body === document.querySelector("#pet-workbench-body") && body.isConnected, bodyHandle), true, "the page survives same-view renders");
  assert.equal(await page.evaluate(() => window.__insightReads), 1, "renders do not restart the insights read");
  await page.evaluate(() => window.__releaseInsights());
  await body.locator("#pet-desk-insights .pet-desk-insights-list").waitFor({timeout: 15000});
  // Buttons rendered by the lazy chunk still work after those renders.
  await body.locator("#pet-desk-pet [data-pet-overview-jump=\"records\"]").click();
  await body.locator(".pet-records").waitFor();
  await backToOverview(page);
  // The finished result is reused: a repaint paints it at once and does not
  // read again.
  assert.equal(await body.locator("#pet-desk-insights .pet-desk-insights-list").count(), 1, "repaint shows the cached block immediately");
  await page.waitForTimeout(300);
  assert.equal(await page.evaluate(() => window.__insightReads), 1, "repaint does not read again");

  // A tab switch while insights are still loading aborts that read; coming
  // back starts one new read and only it fills the block.
  await openOverview(page, "?insights=slow");
  await page.waitForFunction(() => window.__insightReads === 1);
  await page.click('[data-pet-workbench-tab="settings"]');
  await page.waitForSelector("#pet-settings-host");
  assert.equal(await page.evaluate(() => window.__insightSignals.at(-1)?.aborted), true, "leaving the overview aborts the insights read");
  await backToOverview(page);
  await page.waitForFunction(() => window.__insightReads === 2);
  assert.equal(await page.evaluate(() => window.__insightSignals.at(-1)?.aborted), false);
  await page.evaluate(() => window.__releaseInsights());
  await body.locator("#pet-desk-insights .pet-desk-insights-list").waitFor({timeout: 15000});
  assert.equal(await body.locator("#pet-desk-insights").count(), 1);

  // Item 2: a failing insights endpoint or an older server without it shows
  // "not available" in the block; the rest of the desk is untouched.
  for (const mode of ["fail", "old"]) {
    await openOverview(page, `?insights=${mode}`);
    await body.locator(".pet-desk").waitFor();
    await page.waitForFunction(() => document.querySelector("#pet-desk-insights")?.textContent.includes("not available"), null, {timeout: 15000});
    assert.equal(await body.locator("#pet-desk-needs .pet-desk-index").count(), 3, `${mode}: the desk still renders`);
    assert.deepEqual(await untranslated(page), [], `${mode}: English only`);
  }

  // Item 6: a desk chunk that fails to load shows a retry card, and retry
  // brings the overview back without a page reload.
  deskChunkFailures = 1;
  const requestsBefore = deskChunkRequests;
  await page.goto(`${url}/`, {waitUntil: "domcontentloaded"});
  await page.waitForSelector("#pet-workbench-body .chunk-state-error", {timeout: 20000});
  assert.equal(deskChunkRequests, requestsBefore + 1);
  assert.match(await body.innerText(), /The workbench page failed to load/);
  assert.deepEqual(await untranslated(page), []);
  await body.locator("[data-chunk-retry]").click();
  await body.locator(".pet-desk").waitFor({timeout: 15000});
  // The settings tab does not need the chunk at all.
  deskChunkFailures = 1;
  await page.goto(`${url}/`, {waitUntil: "domcontentloaded"});
  await page.waitForSelector("#pet-workbench-body .chunk-state-error", {timeout: 20000});
  await page.click('[data-pet-workbench-tab="settings"]');
  await page.waitForSelector("#pet-settings-host", {timeout: 15000});

  // Round 4, item 2: the approvals tab is gone. Its history is a group of the
  // records tab, after the corrections, with the same filters and paging;
  // it shows every status, newest first.
  const historyPage = await browser.newPage({viewport: {width: 1400, height: 1000}, locale: "en-US"});
  historyPage.on("pageerror", error => errors.push(error.message));
  await historyPage.goto(`${url}/?history=1`, {waitUntil: "domcontentloaded"});
  await historyPage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});
  assert.deepEqual(await historyPage.locator("[data-pet-workbench-tab]").evaluateAll(tabs => tabs.map(tab => tab.dataset.petWorkbenchTab)), ["overview", "records", "settings"]);
  await historyPage.click('[data-pet-workbench-tab="records"]');
  const history = historyPage.locator("#pet-record-approvals");
  await history.locator("[data-pet-approval-open]").first().waitFor({timeout: 15000});
  assert.ok(await historyPage.evaluate(() => {
    const corrections = document.getElementById("pet-record-corrections").getBoundingClientRect().top;
    return corrections < document.getElementById("pet-record-approvals").getBoundingClientRect().top;
  }), "approval history follows the corrections");
  assert.match(await history.locator(".pet-record-intro").innerText(), /Pending ones are on the overview under “For you”/);
  const historyIDs = () => history.locator("[data-pet-approval-open]").evaluateAll(rows => rows.map(row => row.dataset.petApprovalOpen));
  // Ten per page, every status, newest first: a resolved row by when it was
  // resolved, a pending one by when it was created (h-03 and h-07 are pending
  // and older than their resolved neighbours).
  assert.deepEqual(await historyIDs(), ["h-00", "h-01", "h-02", "h-04", "h-03", "h-05", "h-06", "h-08", "h-07", "h-09"]);
  assert.match(await history.locator(".pet-approval-filter-count").innerText(), /^0$/, "no filter by default");
  await history.locator("[data-pet-load-more=\"approval\"]").click();
  await historyPage.waitForFunction(() => document.querySelectorAll("#pet-record-approvals [data-pet-approval-open]").length === 14);
  // Filter: ideas only, then also pending only.
  await history.locator("[data-pet-approval-filters-open]").click();
  await historyPage.click('dialog.pet-approval-filter-dialog [data-pet-approval-filter="idea"]');
  assert.deepEqual(await historyIDs(), ["h-00", "h-04", "h-08", "h-12"]);
  await historyPage.click('dialog.pet-approval-filter-dialog [data-pet-approval-status-filter="rejected"]');
  assert.deepEqual(await historyIDs(), [], "no rejected ideas");
  assert.match(await history.innerText(), /No matching records\./);
  await historyPage.click('dialog.pet-approval-filter-dialog [data-pet-approval-filter="all"]');
  assert.deepEqual(await historyIDs(), ["h-01", "h-05", "h-09", "h-13"]);
  await historyPage.click("dialog.pet-approval-filter-dialog [data-pet-approval-filters-close]");
  await historyPage.waitForSelector("dialog.pet-approval-filter-dialog", {state: "detached"});
  assert.equal(await history.locator(".pet-approval-filter-count").innerText(), "1");
  // A row opens the existing detail dialog.
  await history.locator('[data-pet-approval-open="h-05"]').click();
  await historyPage.waitForSelector("dialog.pet-approval-detail-dialog[open]");
  assert.match(await historyPage.locator("dialog.pet-approval-detail-dialog").innerText(), /历史审批 5/);
  await historyPage.click("dialog.pet-approval-detail-dialog [data-pet-approval-detail-close]");
  assert.deepEqual(await untranslated(historyPage), [], "English approval history");
  // Old entry points. The command palette's "Open approvals" and the
  // workbench page entry mean handle what is pending: the overview, with
  // focus in "For you". "Approval history" opens the records group.
  const palette = async query => {
    await historyPage.keyboard.press("Control+k");
    await historyPage.waitForSelector("#command-palette-input:focus");
    await historyPage.keyboard.type(query);
    await historyPage.waitForTimeout(200);
    await historyPage.keyboard.press("Enter");
    await historyPage.waitForFunction(() => !document.querySelector("dialog[open]"), null, {timeout: 5000});
  };
  await palette("Open approvals");
  await historyPage.waitForSelector('[data-pet-workbench-tab="overview"].active');
  await historyPage.waitForFunction(() => document.activeElement?.closest("#pet-desk-needs") !== null);
  await palette("Approval history");
  await historyPage.waitForSelector('[data-pet-workbench-tab="records"].active');
  await historyPage.waitForFunction(() => document.activeElement?.id === "pet-record-approvals-title");
  // From another page the workbench entry still lands on the overview.
  await historyPage.locator('.sidebar [data-view="projects"]').click();
  await historyPage.waitForSelector(".pet-workbench-page", {state: "detached"});
  await palette("Approvals (Pet workbench)");
  await historyPage.waitForSelector('#pet-workbench-body .pet-desk', {timeout: 15000});
  assert.equal(await historyPage.locator('[data-pet-workbench-tab="overview"]').getAttribute("class"), "active");
  await historyPage.close();

  // Round 4, item 4: Alt+P opens the workbench overview. It works on another
  // page and inside a text field, does nothing while a dialog (the command
  // palette) is open or while an IME composes, and prevents the default.
  const keyPage = await browser.newPage({viewport: {width: 1400, height: 1000}, locale: "en-US"});
  keyPage.on("pageerror", error => errors.push(error.message));
  await keyPage.goto(`${url}/`, {waitUntil: "domcontentloaded"});
  await keyPage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});
  // The shortcut is discoverable: the nav button's name and the palette item.
  const petNav = keyPage.locator('.sidebar [data-view="pet-workbench"]');
  assert.match(await petNav.getAttribute("title"), /\(Alt\+P\)$/);
  assert.equal(await petNav.getAttribute("aria-keyshortcuts"), "Alt+P");
  await keyPage.click('[data-pet-workbench-tab="records"]');
  await keyPage.keyboard.press("Alt+KeyP");
  await keyPage.waitForSelector('[data-pet-workbench-tab="overview"].active', {timeout: 5000});
  await keyPage.locator('.sidebar [data-view="projects"]').click();
  await keyPage.waitForSelector(".pet-workbench-page", {state: "detached"});
  await keyPage.keyboard.press("Alt+KeyP");
  await keyPage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 15000});
  // Inside a text field.
  await keyPage.locator('.sidebar [data-view="projects"]').click();
  await keyPage.waitForSelector(".pet-workbench-page", {state: "detached"});
  await keyPage.evaluate(() => { const input = document.createElement("input"); input.id = "probe-input"; document.body.append(input); input.focus(); });
  const prevented = await keyPage.evaluate(() => {
    const event = new KeyboardEvent("keydown", {key: "p", code: "KeyP", altKey: true, bubbles: true, cancelable: true});
    document.getElementById("probe-input").dispatchEvent(event);
    return event.defaultPrevented;
  });
  assert.equal(prevented, true, "the shortcut prevents the default in a text field");
  await keyPage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 15000});
  // A dialog is open: nothing happens.
  await keyPage.locator('.sidebar [data-view="projects"]').click();
  await keyPage.waitForSelector(".pet-workbench-page", {state: "detached"});
  await keyPage.keyboard.press("Control+k");
  await keyPage.waitForSelector("#command-palette-input:focus");
  await keyPage.keyboard.press("Alt+KeyP");
  await keyPage.waitForTimeout(300);
  assert.equal(await keyPage.locator(".pet-workbench-page").count(), 0, "no navigation behind an open dialog");
  assert.equal(await keyPage.evaluate(() => document.querySelector("dialog#command-palette")?.open), true);
  await keyPage.keyboard.press("Escape");
  // IME composition: nothing happens.
  const composing = await keyPage.evaluate(() => {
    const event = new KeyboardEvent("keydown", {key: "Process", code: "KeyP", altKey: true, isComposing: true, bubbles: true, cancelable: true});
    document.body.dispatchEvent(event);
    return event.defaultPrevented;
  });
  await keyPage.waitForTimeout(300);
  assert.equal(composing, false);
  assert.equal(await keyPage.locator(".pet-workbench-page").count(), 0, "no navigation while composing");
  // The palette item names the shortcut.
  await keyPage.keyboard.press("Control+k");
  await keyPage.keyboard.type("Open pet workbench");
  await keyPage.waitForTimeout(200);
  assert.match(await keyPage.locator(".command-palette-option").first().innerText(), /Pet workbench · Alt\+P/);
  await keyPage.keyboard.press("Escape");
  await keyPage.close();

  // Round 4, item 5: the overview follows the system. The page's clock is
  // installed before the app loads so timers can be driven without waiting.
  const livePage = await browser.newPage({viewport: {width: 1920, height: 912}, locale: "en-US"});
  livePage.on("pageerror", error => errors.push(error.message));
  await livePage.clock.install();
  await livePage.goto(`${url}/?desk=large&watches=huge`, {waitUntil: "domcontentloaded"});
  await livePage.clock.runFor(2000);
  await livePage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});
  const reads = () => livePage.evaluate(() => window.__workbenchQueries.length);
  assert.match(await livePage.locator("[data-pet-desk-updated]").innerText(), /^Updated \d\d:\d\d:\d\d$/, "the status row shows when the data was fetched");
  // State the owner made: a scrolled block, an open "later" list, an open
  // reply, a scrolled follow list.
  await livePage.locator("#pet-desk-needs .pet-desk-scroll").evaluate(el => { el.scrollTop = 200; });
  await livePage.locator("#pet-overview-ignored .pet-follow-body").evaluate(el => { el.scrollTop = 300; });
  await livePage.locator("#pet-desk-later summary").click();
  await livePage.locator("[data-pet-desk-reply-toggle]").click();
  const baseReads = await reads();
  // A burst of ten events (and events the desk ignores) is one read after
  // the debounce.
  await livePage.evaluate(() => { for (let i = 0; i < 10; i++) window.__emit(i % 2 ? "turn_running" : "task_status_changed"); window.__emit("pet_state"); window.__emit("pet_action"); });
  await livePage.clock.runFor(500);
  assert.equal(await reads(), baseReads, "inside the debounce");
  await livePage.clock.runFor(1000);
  await livePage.waitForFunction(base => window.__workbenchQueries.length === base + 1, baseReads);
  await livePage.clock.runFor(4000);
  assert.equal(await reads(), baseReads + 1, "one read for the burst");
  // The repaint kept the owner's state.
  assert.equal(await livePage.locator("#pet-desk-needs .pet-desk-scroll").evaluate(el => el.scrollTop), 200, "block scroll kept");
  assert.equal(await livePage.locator("#pet-overview-ignored .pet-follow-body").evaluate(el => el.scrollTop), 300, "follow list scroll kept");
  assert.equal(await livePage.locator("#pet-desk-later").evaluate(el => el.open), true, "later list stays open");
  assert.equal(await livePage.locator("[data-pet-desk-reply-toggle]").getAttribute("aria-expanded"), "true", "reply stays open");
  // A dialog stays open through a refresh: the default desk's idea.
  await livePage.goto(`${url}/`, {waitUntil: "domcontentloaded"});
  await livePage.clock.runFor(2000);
  await livePage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});
  await livePage.locator("#pet-desk-later summary").click();
  await livePage.locator('#pet-desk-later .pet-desk-item-main[data-pet-approval-open="idea-1"]').click();
  await livePage.waitForSelector("dialog.pet-approval-detail-dialog[open]");
  const dialogReads = await reads();
  await livePage.evaluate(() => window.__emit("pet_approval"));
  await livePage.clock.runFor(5000);
  await livePage.waitForFunction(base => window.__workbenchQueries.length > base, dialogReads);
  assert.equal(await livePage.locator("dialog.pet-approval-detail-dialog[open]").count(), 1, "the detail dialog survives a refresh");
  await livePage.click("dialog.pet-approval-detail-dialog [data-pet-approval-detail-close]");
  // The one-minute fallback poll, and reconnect, focus and visibility wakes.
  let before = await reads();
  await livePage.clock.runFor(61_000);
  await livePage.waitForFunction(base => window.__workbenchQueries.length > base, before);
  before = await reads();
  await livePage.evaluate(() => window.__reopen());
  await livePage.clock.runFor(4000);
  await livePage.waitForFunction(base => window.__workbenchQueries.length > base, before);
  before = await reads();
  await livePage.evaluate(() => window.dispatchEvent(new Event("focus")));
  await livePage.clock.runFor(4000);
  await livePage.waitForFunction(base => window.__workbenchQueries.length > base, before);
  // Elapsed labels move on their own every 30 seconds without a read. The
  // fake clock moves Date.now, so after ten minutes "waited 1 h 30 min" is
  // "waited 1 h 40 min" before any request.
  // Mark the node: a repaint would replace it, so a still-marked node with a
  // new text was updated in place by the tick, not by a read.
  const firstWait = livePage.locator("#pet-desk-needs .pet-desk-elapsed").first();
  const waitedBefore = await firstWait.innerText();
  await firstWait.evaluate(el => { el.dataset.probe = "kept"; });
  await livePage.clock.runFor(10 * 60_000);
  const after = await livePage.evaluate(() => {
    const node = document.querySelector('#pet-desk-needs .pet-desk-elapsed[data-probe="kept"]');
    return node ? node.textContent : null;
  });
  // Ten minutes include polls that repaint; check the tick on a fresh mark.
  await firstWait.evaluate(el => { el.dataset.probe = "tick"; });
  const textAtMark = await firstWait.innerText();
  await livePage.clock.runFor(31_000);
  const ticked = await livePage.evaluate(() => document.querySelector('#pet-desk-needs .pet-desk-elapsed[data-probe="tick"]')?.textContent ?? null);
  assert.ok(after !== null || ticked !== null, "an elapsed label was updated in place at least once");
  assert.notEqual(textAtMark, waitedBefore, `ten minutes later the label moved (${waitedBefore} -> ${textAtMark})`);
  // The records and settings tabs do not refresh on a timer.
  await livePage.click('[data-pet-workbench-tab="settings"]');
  await livePage.waitForSelector("#pet-settings-host");
  before = await reads();
  await livePage.clock.runFor(180_000);
  await livePage.evaluate(() => window.__emit("task_status_changed"));
  await livePage.clock.runFor(5000);
  assert.equal(await reads(), before, "no overview refresh from the settings tab");
  await livePage.close();
  // A failing refresh keeps the desk and says so; the next try runs again.
  const failPage = await browser.newPage({viewport: {width: 1920, height: 912}, locale: "en-US"});
  failPage.on("pageerror", error => errors.push(error.message));
  await failPage.clock.install();
  await failPage.goto(`${url}/?workbench=fail-after`, {waitUntil: "domcontentloaded"});
  await failPage.clock.runFor(2000);
  await failPage.waitForSelector("#pet-workbench-body .pet-desk", {timeout: 20000});
  const stamp = await failPage.locator("[data-pet-desk-updated]").innerText();
  await failPage.evaluate(() => window.__emit("task_status_changed"));
  await failPage.clock.runFor(3000);
  await failPage.waitForFunction(() => document.querySelector("[data-pet-desk-updated]")?.classList.contains("failed"), null, {timeout: 10000});
  assert.match(await failPage.locator("[data-pet-desk-updated]").innerText(), new RegExp(`^Refresh failed, will retry; showing data from ${stamp.replace("Updated ", "")}$`));
  assert.equal(await failPage.locator("#pet-workbench-body .pet-desk").count(), 1, "the desk stays on screen");
  await failPage.close();

  // Step three, items 9-11: the records tab.
  await openOverview(page);
  await page.click('[data-pet-workbench-tab="records"]');
  await body.locator(".pet-records").waitFor({timeout: 15000});
  const groups = await page.evaluate(() => [...document.querySelectorAll("#pet-workbench-body .pet-record-section")].map(section => [section.id, section.querySelector("h3")?.textContent, Boolean(section.querySelector(".pet-record-intro")?.textContent.trim())]));
  assert.deepEqual(groups, [
    ["pet-record-judgment", "Who it is and how it judges", true],
    ["pet-record-corrections", "Your corrections", true],
    ["pet-record-approvals", "Approval history", true],
    ["pet-record-promises", "What it promised", true],
    ["pet-record-worklog", "What it did", true],
  ]);
  await body.locator(".pet-record-correction").first().waitFor({timeout: 15000});
  assert.deepEqual(await body.locator(".pet-record-correction-state").allInnerTexts(), ["Not absorbed yet", "In its principles", "Expired"]);
  assert.match(await body.locator(".pet-record-correction").first().innerText(), /删除工作区 X[\s\S]*这个还要留着对比/);
  assert.match(await body.locator('[data-pet-ledger-counts="loops.md"]').innerText(), /open 2 · waiting 1/);
  assert.match(await body.locator('[data-pet-ledger-counts="agenda.md"]').innerText(), /next 1/);
  assert.match(await body.locator("[data-pet-record-knowledge]").innerText(), /Knowledge: 42 entries · 2 to revise · 7 helped \(the new knowledge base does not keep this count\)/);
  assert.equal(await body.locator("[data-pet-knowledge-link]").getAttribute("href"), "#knowledge?doc=k-1&scope=project&project=p-1");
  // English only (titles and notes are content, marked data-i18n-ignore).
  assert.deepEqual(await untranslated(page).then(items => items.filter(text => !/^(identity|principles|loops|agenda)$/.test(text) && text !== "做了一件事")), []);
  // The table of contents follows the groups and still jumps.
  const tocTargets = await page.evaluate(() => [...document.querySelectorAll("#pet-record-toc-nav [data-pet-record-goto]")].map(button => button.dataset.petRecordGoto));
  assert.deepEqual(tocTargets.filter(target => !target.startsWith("pet-record-core-") && !target.startsWith("pet-record-worklog-")),
    ["pet-record-judgment", "pet-record-corrections", "pet-record-approvals", "pet-record-promises", "pet-record-worklog"]);
  await body.locator('#pet-record-toc-nav [data-pet-record-goto="pet-record-promises"]').click();
  // The edit button on a document in a regrouped section still edits and saves it.
  // agenda.md is the fourth core document in the response (index 3); its id
  // stays put whichever group it renders in. The edit button that carries the
  // name is removed once the editor opens, so select by id.
  const agenda = body.locator("#pet-record-promises #pet-record-core-3");
  assert.equal(await agenda.locator('[data-pet-doc-name="agenda.md"]').count(), 1);
  await agenda.locator("[data-pet-doc-edit]").click();
  await agenda.locator("textarea").fill("新的议程");
  await agenda.locator("button.primary").click();
  await page.waitForFunction(() => (window.__savedDocs || []).includes("agenda.md"), null, {timeout: 15000});
  // The knowledge link opens the knowledge page.
  await body.locator("[data-pet-knowledge-link]").click();
  await page.waitForSelector(".pet-workbench-page", {state: "detached", timeout: 15000});
  assert.match(await page.evaluate(() => location.hash), /^#knowledge/);

  // A failed records read: the line and the corrections say so, the documents
  // still render and their edit buttons still exist.
  await openOverview(page, "?records=fail");
  await page.click('[data-pet-workbench-tab="records"]');
  await page.waitForFunction(() => document.querySelector("[data-pet-record-knowledge]")?.textContent.includes("not available"), null, {timeout: 15000});
  assert.match(await body.locator("[data-pet-record-corrections]").innerText(), /Corrections are not available right now\./);
  assert.equal(await body.locator("#pet-record-judgment [data-pet-doc-edit]").count(), 2);
  assert.equal(await body.locator(".pet-record-ledger-counts").count(), 0, "no counts without data");
  assert.deepEqual(await untranslated(page).then(items => items.filter(text => !/^(identity|principles|loops|agenda)$/.test(text) && text !== "做了一件事")), []);

  // No corrections yet: the empty state.
  await openOverview(page, "?corrections=none");
  await page.click('[data-pet-workbench-tab="records"]');
  await page.waitForFunction(() => document.querySelector("[data-pet-record-corrections]")?.textContent.includes("You have not corrected"), null, {timeout: 15000});

  // The records read happens only on the records tab, once per page, and a
  // tab switch while it is in flight aborts it.
  await openOverview(page);
  await body.locator("#pet-desk-insights .pet-desk-insights-list").waitFor({timeout: 15000});
  assert.equal(await page.evaluate(() => window.__recordsReads || 0), 0, "the overview never asks for records data");
  await page.click('[data-pet-workbench-tab="records"]');
  await body.locator(".pet-record-correction").first().waitFor({timeout: 15000});
  // Overview and back repaints the tab without a reload: the result is reused.
  await page.click('[data-pet-workbench-tab="overview"]');
  await page.click('[data-pet-workbench-tab="records"]');
  assert.equal(await body.locator(".pet-record-correction").count(), 3, "repaint fills from the cached result at once");
  await page.waitForTimeout(300);
  assert.equal(await page.evaluate(() => window.__recordsReads), 1, "a finished records read is reused");

  // Leaving the records tab while its read is in flight aborts it, and the
  // late answer never writes into the page.
  await openOverview(page, "?records=slow");
  await page.click('[data-pet-workbench-tab="records"]');
  await page.waitForFunction(() => (window.__recordsSignals || []).length === 1);
  await page.click('[data-pet-workbench-tab="overview"]');
  assert.equal(await page.evaluate(() => window.__recordsSignals[0].aborted), true, "leaving records aborts its read");
  await page.evaluate(() => { for (const resolve of window.__recordsWaiters.splice(0)) resolve(); });
  await page.waitForTimeout(200);
  assert.equal(await body.locator(".pet-desk").count(), 1, "the overview is untouched by the late answer");
  assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);

  // Layout (owner, 2026-11-26): on a wide screen the overview is one board
  // that fills the viewport. Neither the document nor the workspace scrolls;
  // the blocks scroll inside. Narrow or short screens keep the page scroll.
  const layout = page => page.evaluate(() => {
    const doc = document.documentElement;
    const workspace = document.querySelector(".workspace");
    const box = selector => document.querySelector(selector)?.getBoundingClientRect();
    const inView = rect => Boolean(rect) && rect.top >= 0 && rect.bottom <= innerHeight + 0.5 && rect.height > 0;
    const scroller = id => { const el = document.querySelector(`#${id} .pet-desk-scroll`); return el ? {scroll: el.scrollHeight, client: el.clientHeight} : null; };
    return {
      docScroll: doc.scrollHeight - doc.clientHeight,
      workspaceScroll: workspace.scrollHeight - workspace.clientHeight,
      sideways: doc.scrollWidth - doc.clientWidth,
      board: getComputedStyle(document.querySelector(".pet-desk-col")).display !== "contents",
      headers: ["pet-desk-needs", "pet-desk-waiting", "pet-desk-pet", "pet-desk-running", "pet-desk-insights"].map(id => inView(box(`#${id} > header`))),
      follows: [...document.querySelectorAll(".pet-desk-follows > .pet-follow-list > .pet-follow-title")].map(el => inView(el.getBoundingClientRect())),
      columns: ["pet-desk-col-side", "pet-desk-col-main", "pet-desk-col-run"].map(name => Math.round(document.querySelector(`.${name} > section`).getBoundingClientRect().left)),
      stackOrder: [box("#pet-desk-waiting").bottom <= box("#pet-desk-pet").top, box("#pet-desk-running").bottom <= box("#pet-desk-insights").top],
      needs: scroller("pet-desk-needs"), waiting: scroller("pet-desk-waiting"), running: scroller("pet-desk-running"),
    };
  });
  const fitsBoard = (m, label) => {
    assert.equal(m.board, true, `${label}: board layout`);
    assert.ok(m.docScroll <= 0, `${label}: the document must not scroll (${m.docScroll}px)`);
    assert.ok(m.workspaceScroll <= 0, `${label}: the workspace must not scroll (${m.workspaceScroll}px)`);
    assert.ok(m.sideways <= 0, `${label}: no sideways scroll (${m.sideways}px)`);
    assert.deepEqual(m.headers, [true, true, true, true, true], `${label}: every block header is on screen`);
    assert.deepEqual(m.follows, [true, true, true], `${label}: the three follow titles are on screen`);
    assert.ok(m.columns[0] < m.columns[1] && m.columns[1] < m.columns[2], `${label}: waiting | for you | running, left to right`);
    assert.deepEqual(m.stackOrder, [true, true], `${label}: waiting above the pet's side, running above the last 7 days`);
  };
  const layoutPage = await browser.newPage({viewport: {width: 1920, height: 912}, locale: "en-US"});
  layoutPage.on("pageerror", error => errors.push(error.message));
  const shots = process.env.AHA_PET_DESK_SCREENSHOT_DIR;
  if (shots) await mkdir(shots, {recursive: true});
  const openLayout = async (width, height, query) => {
    await layoutPage.setViewportSize({width, height});
    await layoutPage.goto(`${url}/?desk=${query}`, {waitUntil: "domcontentloaded"});
    await layoutPage.waitForSelector("#pet-workbench-body #pet-desk-insights .pet-desk-insights-list", {timeout: 20000});
  };
  for (const [width, height] of [[1920, 912], [1440, 900]]) {
    const label = `${width}x${height}`;
    await openLayout(width, height, "owner");
    fitsBoard(await layout(layoutPage), `${label} owner`);
    if (shots) await layoutPage.screenshot({path: resolve(shots, `pet-desk-${label}.png`)});
    // The last reply is one line; expanding shows the original text and the
    // page still does not scroll. Opening a follow list keeps it that way.
    const reply = layoutPage.locator(".pet-desk-reply");
    assert.ok((await reply.boundingBox()).height < 48, `${label}: the last reply is one line`);
    assert.doesNotMatch(await reply.locator(".pet-desk-reply-text").innerText(), /\*\*|__|`/, "the preview drops Markdown markers");
    const toggle = reply.locator("[data-pet-desk-reply-toggle]");
    assert.equal(await toggle.getAttribute("aria-expanded"), "false");
    await toggle.click();
    assert.equal(await toggle.getAttribute("aria-expanded"), "true");
    assert.equal(await toggle.innerText(), "Collapse");
    assert.match(await reply.locator(".pet-desk-reply-full").innerText(), /\*\*task-813[\s\S]*__其余项目__/, "the full text is the original");
    let m = await layout(layoutPage);
    fitsBoard(m, `${label} expanded`);
    assert.ok(await layoutPage.locator("#pet-overview-ignored .pet-overview-other-body").evaluate(el => el.scrollHeight > el.clientHeight && el.getBoundingClientRect().bottom <= innerHeight), `${label}: the follow list scrolls inside`);
    await toggle.click();
    assert.equal(await toggle.getAttribute("aria-expanded"), "false");
    assert.equal(await reply.locator(".pet-desk-reply-full").isHidden(), true);
    // Long text: a running row's note is two lines until it gets focus.
    const note = layoutPage.locator("#pet-desk-running .pet-desk-detail").first();
    const clamped = (await note.boundingBox()).height;
    assert.ok(clamped < 2.6 * 13 * 1.6, `${label}: the note is at most two lines (${clamped}px)`);
    assert.match(await note.locator("[title]").getAttribute("title"), /registerWorkspaceTerminalRoutes/, "the hint holds the full note");
    await note.focus();
    assert.ok((await note.boundingBox()).height > clamped, `${label}: focus shows the whole note`);

    await openLayout(width, height, "large");
    m = await layout(layoutPage);
    fitsBoard(m, `${label} large`);
    for (const name of ["needs", "waiting", "running"]) assert.ok(m[name].scroll > m[name].client, `${label}: ${name} scrolls inside its block`);
    // The keyboard reaches a block's scroll area and scrolls it; the header stays.
    const needs = layoutPage.locator("#pet-desk-needs .pet-desk-scroll");
    assert.equal(await needs.getAttribute("tabindex"), "0");
    await needs.focus();
    await layoutPage.keyboard.press("PageDown");
    await layoutPage.waitForFunction(() => document.querySelector("#pet-desk-needs .pet-desk-scroll").scrollTop > 0);
    fitsBoard(await layout(layoutPage), `${label} large scrolled`);
    await layoutPage.locator("[data-pet-desk-reply-toggle]").click();
    fitsBoard(await layout(layoutPage), `${label} large expanded`);
  }
  // The three follow lists are always open (owner, 2026-11-26). Two data
  // sizes: watched 1 / ignored 2 / unfollowed 1, and 200 / 300 / 50. The
  // page never scrolls, each list scrolls under its own title, the longest
  // list sets the strip height (capped), the shorter lists keep their own
  // height instead of a mostly empty scroll area, and the three boards above
  // still show each block's title and at least two rows.
  const followStrip = page => page.evaluate(() => {
    const lists = [...document.querySelectorAll(".pet-desk-follows > .pet-follow-list")].map(list => {
      const body = list.querySelector(".pet-follow-body");
      const rows = body.querySelectorAll(".pet-watch-row").length;
      return {height: Math.round(list.getBoundingClientRect().height), rows, scrolls: body.scrollHeight > body.clientHeight + 1, empty: body.scrollHeight - body.clientHeight, title: list.querySelector(".pet-follow-title").getBoundingClientRect().top};
    });
    const rowsShown = id => {
      const scroll = document.querySelector(`#${id} .pet-desk-scroll`);
      if (!scroll) return 99;
      const bottom = scroll.getBoundingClientRect().bottom;
      return [...scroll.querySelectorAll(":scope > ol > li, :scope > ul > li")].filter(li => li.getBoundingClientRect().bottom <= bottom + 1).length;
    };
    return {strip: Math.round(document.querySelector(".pet-desk-follows").getBoundingClientRect().height), details: document.querySelectorAll(".pet-desk-follows details, .pet-desk-follows summary").length, lists,
      shown: ["pet-desk-needs", "pet-desk-waiting", "pet-desk-running", "pet-desk-insights"].map(rowsShown)};
  });
  for (const [width, height] of [[1920, 912], [1440, 900], [1366, 768], [1100, 640]]) {
    for (const set of ["small", "huge"]) {
      const label = `${width}x${height} follows ${set}`;
      await layoutPage.setViewportSize({width, height});
      await layoutPage.goto(`${url}/?desk=large&watches=${set}`, {waitUntil: "domcontentloaded"});
      await layoutPage.waitForSelector("#pet-desk-insights .pet-desk-insights-list", {timeout: 20000});
      fitsBoard(await layout(layoutPage), label);
      const f = await followStrip(layoutPage);
      assert.equal(f.details, 0, `${label}: no collapse control`);
      assert.ok(f.strip <= Math.max(150, (height - 210) / 4) + 2, `${label}: the strip stays within its cap (${f.strip}px)`);
      assert.ok(f.shown.every(rows => rows >= 2), `${label}: every block above shows its title and two rows (${f.shown})`);
      const tallest = Math.max(...f.lists.map(list => list.height));
      assert.equal(f.strip, tallest, `${label}: the longest list sets the strip height`);
      for (const list of f.lists) {
        // A list either fits (no scroll area) or fills the strip and scrolls.
        assert.ok(!list.scrolls || list.height === tallest, `${label}: a scrolling list is the full strip height`);
      }
      if (set === "small") {
        assert.deepEqual(f.lists.map(list => list.rows), [1, 2, 1]);
        assert.ok(f.lists[0].height < tallest && f.lists[2].height < tallest || width < 1366, `${label}: the one-row lists are shorter than the two-row list`);
      } else {
        assert.deepEqual(f.lists.map(list => list.rows), [200, 300, 50]);
        assert.ok(f.lists.every(list => list.scrolls), `${label}: every long list scrolls inside`);
        // Scrolling a list keeps its title where it is and the page still.
        const ignoredBody = layoutPage.locator("#pet-overview-ignored .pet-follow-body");
        await ignoredBody.focus();
        await layoutPage.keyboard.press("End");
        await layoutPage.waitForFunction(() => document.querySelector("#pet-overview-ignored .pet-follow-body").scrollTop > 0);
        const after = await followStrip(layoutPage);
        assert.equal(after.lists[1].title, f.lists[1].title, `${label}: the title does not scroll away`);
        fitsBoard(await layout(layoutPage), `${label} scrolled`);
      }
    }
  }
  // A follow action still works from the board's follow lists.
  await openLayout(1920, 912, "owner");
  await layoutPage.locator('#pet-overview-unfollowed [data-pet-watch-follow][data-pet-watch-task="t-un0"]').click();
  await layoutPage.waitForFunction(() => (window.__watchWrites || []).some(([method, id]) => method === "POST" && id === "t-un0"));
  await openLayout(1366, 768, "owner");
  fitsBoard(await layout(layoutPage), "1366x768");
  await openLayout(1366, 768, "large");
  fitsBoard(await layout(layoutPage), "1366x768 large");
  // Narrow or phone: the pre-board flow and a page that scrolls.
  for (const [width, height] of [[900, 700], [390, 844]]) {
    await openLayout(width, height, "owner");
    const m = await layout(layoutPage);
    assert.equal(m.board, false, `${width}x${height}: no board`);
    assert.ok(m.docScroll > 0 || m.workspaceScroll > 0, `${width}x${height}: the page scrolls`);
    assert.ok(m.sideways <= 1, `${width}x${height}: no sideways scroll (${m.sideways}px)`);
    const order = await layoutPage.evaluate(() => ["pet-desk-needs", "pet-desk-waiting", "pet-desk-pet", "pet-desk-insights"].map(id => document.getElementById(id).getBoundingClientRect().top));
    assert.deepEqual([...order].sort((a, b) => a - b), order, `${width}x${height}: for you, waiting, the pet's side, last 7 days top to bottom`);
    // Stacked: the follow lists are open too, each capped and scrolling
    // inside, so 550 rows do not stretch the page.
    await layoutPage.goto(`${url}/?desk=owner&watches=huge`, {waitUntil: "domcontentloaded"});
    await layoutPage.waitForSelector("#pet-desk-insights .pet-desk-insights-list", {timeout: 20000});
    const stacked = await layoutPage.evaluate(() => [...document.querySelectorAll(".pet-desk-follows .pet-follow-body")].map(body => [body.clientHeight <= innerHeight * 0.4 + 1, body.scrollHeight > body.clientHeight]));
    assert.deepEqual(stacked, [[true, true], [true, true], [true, true]], `${width}x${height}: stacked lists are capped and scroll inside`);
  }
  assert.deepEqual(await untranslated(layoutPage), [], "English board left untranslated Chinese");
  await layoutPage.close();

  // Chinese records page.
  await openOverview(page, "?language=zh-CN");
  await page.click('[data-pet-workbench-tab="records"]');
  await body.locator(".pet-record-correction").first().waitFor({timeout: 15000});
  assert.deepEqual(await body.locator(".pet-record-section h3").allInnerTexts(), ["它是谁、怎么判断", "你的纠正", "审批记录", "它答应的事", "它做过的事"]);
  assert.deepEqual(await body.locator(".pet-record-correction-state").allInnerTexts(), ["还没消化", "已写进原则", "已过期"]);

  assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);
  console.log("pet desk browser checks passed");
} finally {
  await browser?.close();
  server.close();
}
