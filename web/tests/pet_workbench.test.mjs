import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {createPetApprovalCounter, petPendingApprovalBadge} from "../dist/pet_approvals.js";
import {bindPetDocEditorButtons, createPetWorkbenchLoader, createPetWorkbenchPageKeeper, PET_APPROVAL_MAX_LIMIT, petApprovalMatches, petApprovalPage, renderPetApprovalLoadMore, renderPetInspectionSummary, renderPetWorkbenchLoadError, swapPetWorkbenchPage} from "../dist/pet_workbench.js";

const root = resolve(import.meta.dirname, "../..");

test("pending badges hide zero and display counts capped at 99+", () => {
  assert.equal(petPendingApprovalBadge(0), "");
  assert.match(petPendingApprovalBadge(7), />7<\/span>/);
  assert.match(petPendingApprovalBadge(99), />99<\/span>/);
  assert.match(petPendingApprovalBadge(100), />99\+<\/span>/);
  assert.match(petPendingApprovalBadge(105), /class="task-tool-badge"/);
});

test("approve/reject/cancel refresh from the same authoritative counter", async () => {
  let pending = 3;
  const seen = [];
  const counter = createPetApprovalCounter(async () => ({pending}), () => seen.push(counter.pending));
  await counter.refresh();
  for (const decision of ["approve", "reject", "cancel"]) {
    pending--;
    await counter.refresh(true);
    assert.equal(counter.pending, pending, decision);
  }
  assert.deepEqual(seen, [3, 2, 1, 0]);
});

test("mutation during an in-flight count discards stale results and refreshes once", async () => {
  const reads = [];
  const seen = [];
  const counter = createPetApprovalCounter(() => new Promise(resolve => reads.push(resolve)), () => seen.push(counter.pending));
  const first = counter.refresh();
  assert.equal(counter.refresh(), first);
  assert.equal(counter.refresh(true), first);
  reads.shift()({pending: 9});
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(seen, []);
  assert.equal(reads.length, 1);
  reads.shift()({pending: 2});
  await first;
  assert.deepEqual(seen, [2]);
});

test("failed count refresh retains the last count and allows the next poll", async () => {
  let fail = false;
  const counter = createPetApprovalCounter(async () => {
    if (fail) throw new Error("offline");
    return {pending: 5};
  }, () => {});
  await counter.refresh();
  fail = true;
  await assert.rejects(counter.refresh(), /offline/);
  assert.equal(counter.pending, 5);
  fail = false;
  await counter.refresh();
  assert.equal(counter.pending, 5);
});

const zh = (key, values = {}) => {
  const catalog = {
    "petWorkbench.inspectionSummary": "巡检快照",
    "petWorkbench.inspectionUnavailable": "巡检快照暂不可用，稍后重试。",
    "petWorkbench.inspectionHealthy": "正常",
    "petWorkbench.inspectionDegraded": "降级",
    "petWorkbench.inspectionUnknown": "未知",
    "petWorkbench.inspectionVersion": "版本",
    "petWorkbench.inspectionWebVersion": "Web 版本",
    "petWorkbench.inspectionStartedAt": "启动时间",
    "petWorkbench.inspectionUptime": "运行时长",
    "petWorkbench.inspectionCheckedAt": "检查时间",
    "petWorkbench.inspectionDegradedChannels": "降级渠道",
    "petWorkbench.inspectionTasksTotal": "任务总数",
    "petWorkbench.inspectionActive": "执行中",
    "petWorkbench.inspectionWaitingUser": "等待主人",
    "petWorkbench.inspectionFailed": "失败",
    "petWorkbench.inspectionBlocked": "阻塞",
    "petWorkbench.inspectionCompleted": "已完成",
    "petWorkbench.inspectionOpenTasks": "未闭环",
    "petWorkbench.inspectionAttentionTasks": "需关注",
    "petWorkbench.inspectionPendingApprovals": "待审批",
    "petWorkbench.uptimeMinutes": "{minutes} 分钟",
    "petWorkbench.uptimeHours": "{hours} 小时 {minutes} 分钟",
    "petWorkbench.uptimeDays": "{days} 天 {hours} 小时",
  };
  return (catalog[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));
};
const identityTime = value => value;

test("the retryable error state escapes messages and includes a retry button", () => {
  const translate = key => ({
    "common.retry": "重试",
    "petWorkbench.loadFailed": "加载失败，请重试",
  }[key] ?? key);
  const html = renderPetWorkbenchLoadError("<img src=x onerror=alert(1)>", translate);
  assert.ok(html.includes("&lt;img src=x onerror=alert(1)&gt;"), html);
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /data-pet-workbench-retry/);
  assert.match(html, />重试</);
  assert.match(renderPetWorkbenchLoadError("", translate), /加载失败，请重试/);
});

test("inspection summary renders versions, health, uptime, task stats, loops and note", () => {
  const html = renderPetInspectionSummary({
    checked_at: "2026-11-09T13:01:00Z",
    health: "degraded",
    version: "0.6.0",
    web_version: "abc123",
    started_at: "2026-11-09T11:00:00Z",
    uptime_minutes: 65,
    tasks_total: 7,
    active: 2,
    waiting_user: 1,
    failed: 1,
    blocked: 1,
    completed: 2,
    pending_approvals: 3,
    degraded_channels: ["飞书（degraded）"],
    degraded_channel_count: 1,
    open_tasks: 4,
    attention_tasks: 2,
    note: "有 2 个未闭环任务需要处理。",
  }, zh, identityTime);
  for (const expected of [
    "巡检快照", "降级", "版本: 0.6.0", "Web 版本: abc123", "运行时长: 1 小时 5 分钟",
    "检查时间: 2026-11-09T13:01:00Z", "降级渠道: 1",
    "任务总数", "等待主人", "已完成", "未闭环: 4", "需关注: 2", "待审批: 3",
    "有 2 个未闭环任务需要处理。",
  ]) {
    assert.ok(html.includes(expected), `missing ${expected}: ${html}`);
  }
});

test("inspection summary shows an explicit empty state and never renders dirty values", () => {
  const empty = renderPetInspectionSummary(null, zh, identityTime);
  assert.match(empty, /巡检快照暂不可用/);
  const dirty = renderPetInspectionSummary({
    checked_at: "", health: "", version: undefined, web_version: "",
    uptime_minutes: "not-a-number", tasks_total: undefined, active: -1,
    waiting_user: Number.NaN, failed: null, blocked: "", completed: undefined,
    pending_approvals: -2, degraded_channel_count: "x", open_tasks: undefined,
    attention_tasks: null, note: "",
  }, zh, identityTime);
  assert.match(dirty, /巡检快照暂不可用/);
  assert.doesNotMatch(dirty, /undefined|NaN|null|Infinity/);
});

test("a slow workbench response times out, auto-retries once, then exposes a retryable error", async () => {
  let calls = 0;
  let succeed = false;
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: () => {
      calls++;
      return succeed ? Promise.resolve({ok: true}) : new Promise(() => {});
    },
    isCurrent: () => true,
    onLoading: () => events.push("loading"),
    onData: () => events.push("data"),
    onError: message => events.push(`error:${message}`),
    timeoutMs: 20,
    retryDelayMs: 5,
    timeoutMessage: "加载超时，请重试",
  });
  await loader.load();
  assert.equal(calls, 2, "one automatic retry, never more");
  assert.deepEqual(events, ["loading", "error:加载超时，请重试"]);
  assert.equal(loader.busy(), false, "loading must not stay stuck after the final failure");

  succeed = true;
  await loader.load();
  assert.equal(calls, 3);
  assert.deepEqual(events, ["loading", "error:加载超时，请重试", "loading", "data"]);
  assert.equal(loader.busy(), false);
});

test("an in-flight request superseded by a render cannot write to the detached node", async () => {
  let current = true;
  let resolveRead = () => {};
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: () => new Promise(resolve => { resolveRead = resolve; }),
    isCurrent: () => current,
    onLoading: () => events.push("loading"),
    onData: () => events.push("data"),
    onError: message => events.push(`error:${message}`),
    timeoutMs: 1000,
  });
  const pending = loader.load();
  current = false; // render() replaced #pet-workbench-body while the request was in flight
  resolveRead({ok: true});
  await pending;
  assert.deepEqual(events, ["loading"], "stale result must not be written");
  assert.equal(loader.busy(), false, "superseded loader must still clear its loading flag");

  let freshEvents = [];
  const fresh = createPetWorkbenchLoader({
    read: () => Promise.resolve({ok: true, fresh: true}),
    isCurrent: () => true,
    onLoading: () => freshEvents.push("loading"),
    onData: () => freshEvents.push("data"),
    onError: message => freshEvents.push(`error:${message}`),
  });
  await fresh.load();
  assert.deepEqual(freshEvents, ["loading", "data"], "the re-rendered node loads its own data");
});

test("dispose aborts the in-flight request immediately instead of waiting for the timeout", async () => {
  let signal;
  let resolveRead = () => {};
  const loader = createPetWorkbenchLoader({
    read: requestSignal => {
      signal = requestSignal;
      return new Promise(resolve => { resolveRead = resolve; });
    },
    isCurrent: () => true,
    onLoading: () => {},
    onData: () => {},
    onError: () => {},
    // Far longer than this test window: only dispose() can abort in time.
    timeoutMs: 60_000,
  });
  const pending = loader.load();
  assert.equal(signal.aborted, false);
  loader.dispose();
  assert.equal(signal.aborted, true, "dispose must abort synchronously");
  resolveRead({ok: true});
  await pending;
  assert.equal(loader.busy(), false);
});

// The shell replaces #app wholesale on every render. These fakes model the two
// nodes the keeper cares about: the body it binds and the page it preserves.
function fakeWorkbenchPage({connected = true} = {}) {
  return {page: {isConnected: connected}, body: {isConnected: connected}};
}

test("an entry issues one workbench request and unrelated renders never abort it", async () => {
  const keeper = createPetWorkbenchPageKeeper();
  const {page, body} = fakeWorkbenchPage();
  const requests = [];
  let binding = null;
  const loader = createPetWorkbenchLoader({
    read: signal => new Promise((_resolve, reject) => {
      requests.push(signal);
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    }),
    isCurrent: () => body.isConnected && keeper.binding === binding,
    onLoading: () => {},
    onData: () => {},
    onError: () => {},
    timeoutMs: 60_000,
  });
  binding = keeper.adopt(body, page, loader, "core");
  const pending = loader.load();
  assert.equal(requests.length, 1, "entering the view issues exactly one request");

  // Navigation renders immediately and again after ensureViewData; background
  // resource loads and messages render too. Every one of them must reuse the
  // live page instead of rebinding the loader.
  for (let render = 0; render < 6; render++) {
    assert.equal(keeper.beforeRender("pet-workbench"), page, `render ${render} carries the page`);
    assert.equal(keeper.owns(body), true, `render ${render} sees the live binding`);
    assert.equal(requests.length, 1, `render ${render} must not issue another request`);
    assert.equal(requests[0].aborted, false, `render ${render} must not abort the in-flight request`);
  }

  // Only actually leaving the view aborts the request.
  assert.equal(keeper.beforeRender("tasks"), null);
  assert.equal(requests[0].aborted, true, "leaving the view aborts the in-flight request");
  assert.equal(keeper.binding, null);
  assert.equal(keeper.owns(body), false);
  await pending;
  assert.equal(loader.busy(), false, "the released loader still clears its loading state");

  // Re-entering is a new entry: exactly one new request, never a retry storm.
  const next = fakeWorkbenchPage();
  let nextBinding = null;
  const nextLoader = createPetWorkbenchLoader({
    read: signal => new Promise((_resolve, reject) => {
      requests.push(signal);
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    }),
    isCurrent: () => next.body.isConnected && keeper.binding === nextBinding,
    onLoading: () => {},
    onData: () => {},
    onError: () => {},
    timeoutMs: 60_000,
  });
  nextBinding = keeper.adopt(next.body, next.page, nextLoader, "core");
  const reentered = nextLoader.load();
  assert.equal(requests.length, 2, "a genuine re-entry starts exactly one new request");
  nextLoader.dispose();
  await reentered;
});

test("a new body supersedes and aborts the previous workbench loader", () => {
  const keeper = createPetWorkbenchPageKeeper();
  const first = fakeWorkbenchPage();
  let firstDisposed = 0;
  keeper.adopt(first.body, first.page, {dispose: () => { firstDisposed++; }}, "core");
  assert.equal(keeper.beforeRender("pet-workbench"), first.page);

  const second = fakeWorkbenchPage();
  keeper.adopt(second.body, second.page, {dispose: () => {}}, "core");
  assert.equal(firstDisposed, 1, "an unreused page must not keep its old request");
  assert.equal(keeper.owns(first.body), false);
  assert.equal(keeper.owns(second.body), true);
  assert.equal(keeper.beforeRender("pet-workbench"), second.page);
});

test("the fresh shell swaps in the preserved page even though innerHTML detached it", () => {
  const inserted = [];
  const freshTemplate = {replaceWith: node => inserted.push(node)};
  const app = {querySelector: selector => selector === ".pet-workbench-page" ? freshTemplate : null};
  // The shell replacement detached the old page before this runs, so it is not
  // connected anymore; only beforeRender is allowed to decide reuse.
  const preserved = {isConnected: false};
  assert.equal(swapPetWorkbenchPage(app, preserved), true);
  assert.deepEqual(inserted, [preserved]);
  assert.equal(swapPetWorkbenchPage(app, null), false, "no preserved page leaves the fresh template");
  assert.equal(swapPetWorkbenchPage({querySelector: () => null}, preserved), false, "a missing slot must not throw");
});

test("a disconnected or missing page is not carried across a render", () => {
  const keeper = createPetWorkbenchPageKeeper();
  const {body} = fakeWorkbenchPage();
  keeper.adopt(body, null, {dispose: () => {}}, "core");
  assert.equal(keeper.beforeRender("pet-workbench"), null, "without a page the shell must be rebuilt");
  keeper.release();

  const detached = fakeWorkbenchPage({connected: false});
  keeper.adopt(detached.body, detached.page, {dispose: () => {}}, "core");
  assert.equal(keeper.beforeRender("pet-workbench"), null);
});

test("the timeout aborts the in-flight request before reporting the error", async () => {
  let signal;
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: requestSignal => {
      signal = requestSignal;
      return new Promise(() => {});
    },
    isCurrent: () => true,
    onLoading: () => events.push("loading"),
    onData: () => events.push("data"),
    onError: message => events.push(`error:${message}`),
    timeoutMs: 20,
    retryDelayMs: 1,
    maxRetries: 0,
    timeoutMessage: "超时",
  });
  await loader.load();
  assert.equal(signal.aborted, true, "the timer must abort the request, not only reject the race");
  assert.deepEqual(events, ["loading", "error:超时"]);
  assert.equal(loader.busy(), false);
});

test("a 5xx response is retried once and then renders the data", async () => {
  let calls = 0;
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: () => {
      calls++;
      if (calls === 1) {
        const error = new Error("HTTP 503");
        error.status = 503;
        return Promise.reject(error);
      }
      return Promise.resolve({attempt: calls});
    },
    isCurrent: () => true,
    onLoading: () => events.push("loading"),
    onData: value => events.push(`data:${value.attempt}`),
    onError: message => events.push(`error:${message}`),
    timeoutMs: 1000,
    retryDelayMs: 1,
  });
  await loader.load();
  assert.equal(calls, 2, "5xx must be retried exactly once");
  assert.deepEqual(events, ["loading", "data:2"]);
  assert.equal(loader.busy(), false);
});

test("a network failure is retried once and then renders the data", async () => {
  let calls = 0;
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: () => {
      calls++;
      if (calls === 1) return Promise.reject(new TypeError("Failed to fetch"));
      return Promise.resolve({attempt: calls});
    },
    isCurrent: () => true,
    onLoading: () => events.push("loading"),
    onData: value => events.push(`data:${value.attempt}`),
    onError: message => events.push(`error:${message}`),
    timeoutMs: 1000,
    retryDelayMs: 1,
  });
  await loader.load();
  assert.equal(calls, 2, "a network failure must be retried exactly once");
  assert.deepEqual(events, ["loading", "data:2"]);
  assert.equal(loader.busy(), false);
});

test("a throwing renderer surfaces a retryable error instead of staying on loading", async () => {
  let calls = 0;
  let failRender = true;
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: () => {
      calls++;
      return Promise.resolve({ok: true});
    },
    isCurrent: () => true,
    onLoading: () => events.push("loading"),
    onData: () => {
      if (failRender) throw new Error("render exploded");
      events.push("data");
    },
    onError: message => events.push(`error:${message}`),
    timeoutMs: 1000,
  });
  await loader.load();
  assert.equal(calls, 1, "a rendering failure must not trigger another request");
  assert.deepEqual(events, ["loading", "error:render exploded"]);
  assert.equal(loader.busy(), false, "loading must clear after the render error");

  failRender = false;
  await loader.load();
  assert.deepEqual(events, ["loading", "error:render exploded", "loading", "data"]);
  assert.equal(loader.busy(), false);
});

test("loads requested while one is in flight share it instead of starting a second request", async () => {
  let calls = 0;
  let resolveRead = () => {};
  const loader = createPetWorkbenchLoader({
    read: () => {
      calls++;
      return new Promise(resolve => { resolveRead = resolve; });
    },
    isCurrent: () => true,
    onLoading: () => {},
    onData: () => {},
    onError: () => {},
    timeoutMs: 1000,
  });
  const first = loader.load();
  const second = loader.load();
  assert.equal(first, second);
  assert.equal(calls, 1, "the in-flight request is reused");
  resolveRead({ok: true});
  await first;
  assert.equal(loader.busy(), false);
});

test("non-retryable 4xx failures surface immediately without an automatic retry", async () => {
  let calls = 0;
  const events = [];
  const loader = createPetWorkbenchLoader({
    read: () => {
      calls++;
      const error = new Error("HTTP 404");
      error.status = 404;
      return Promise.reject(error);
    },
    isCurrent: () => true,
    onLoading: () => events.push("loading"),
    onData: () => events.push("data"),
    onError: message => events.push(`error:${message}`),
    timeoutMs: 50,
    retryDelayMs: 1,
  });
  await loader.load();
  assert.equal(calls, 1);
  assert.deepEqual(events, ["loading", "error:HTTP 404"]);
  assert.equal(loader.busy(), false);
});

test("workbench wiring uses the guarded loader and renders the inspection summary", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const api = await readFile(resolve(root, "web/src/api.ts"), "utf8");
  const loader = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
  assert.match(main, /createPetWorkbenchLoader\(/);
  assert.match(main, /api\.petWorkbench\(\{approvalLimit, worklogLimit\}, signal\)/);
  assert.match(main, /isCurrent: \(\) => body\.isConnected && petWorkbenchPageKeeper\.binding === binding/);
  assert.match(main, /renderPetInspectionSummary\(view\.inspection, t, formatDateTime\)/);
  assert.match(main, /bindPetWorkbenchPage\(\)\.catch\(showPetWorkbenchBindFailure\)/);
  assert.match(main, /function showPetWorkbenchBindFailure/);
  assert.match(main, /renderPetWorkbenchLoadError\(loadError, t\)/);
  assert.match(main, /workbenchBindFailed/);
  assert.doesNotMatch(main, /petWorkbenchBound/);
  // A same-view render carries the mounted page instead of rebinding.
  assert.match(main, /const preservedWorkbenchPage = petWorkbenchPageKeeper\.beforeRender\(state\.view\)/);
  assert.match(main, /swapPetWorkbenchPage\(app, preservedWorkbenchPage\)/);
  assert.match(main, /if \(petWorkbenchPageKeeper\.owns\(body\)\) return;/);
  assert.match(main, /petWorkbenchPageKeeper\.release\(\)/);
  // Navigation must not restart the load it just started.
  assert.match(main, /state\.view === "pet-workbench" && petWorkbenchPageKeeper\.binding\) return;/);
  assert.doesNotMatch(main, /withTimeout\(api\.petWorkbench/);
  assert.match(api, /petWorkbench\(options: \{[^}]*\}[^)]*signal\?: AbortSignal\)/);
  assert.match(loader, /maxRetries \?\? 1/);
  assert.match(loader, /request\.abort\(\)/);
  assert.match(loader, /loading = false/);
});

test("workbench defaults to core and removes journal UI/API while retaining old bookmarks", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const api = await readFile(resolve(root, "web/src/api.ts"), "utf8");
  const assistant = await readFile(resolve(root, "web/src/assistant.ts"), "utf8");
  const i18n = await readFile(resolve(root, "web/src/i18n.ts"), "utf8");
  assert.match(main, /let petWorkbenchTab = "core"/);
  assert.match(main, /class="active" data-pet-workbench-tab="core"/);
  assert.doesNotMatch(main, /data-pet-workbench-tab="diary"|view\.diary|diaryLimit|petDiary/);
  assert.doesNotMatch(api, /petDiary|diary_limit|diaryLimit/);
  assert.doesNotMatch(assistant + i18n, /日记|diary/i);
  assert.match(main, /value === "pet-diary"\) return "pet-workbench"/);
  assert.match(main, /setInterval\(\(\) => void refreshPetPendingApprovalCount\(\), 60_000\)/);
  assert.match(main, /await api.resolvePetApproval[\s\S]*?await refreshPetPendingApprovalCount\(true\)/);
  assert.doesNotMatch(main, /view\.approvals\.filter\(item => item\.status === "pending"\)\.length/);
});

const approvalRow = (id, kind, status) => ({id, kind, status, title: id, summary: "", risk: "low"});
const approvalCount = (kind, status, count) => ({kind, status, count});
const approvalTranslations = {
  "petWorkbench.loadMore": "加载更多",
  "petWorkbench.loadingMore": "加载中…",
  "petWorkbench.loadedCount": "已显示 {shown} / 共 {total} 条",
  "petWorkbench.approvalLimitReached": "审批仅加载最近 {limit} 条。",
};
const approvalText = (key, values = {}) => (approvalTranslations[key] ?? key)
  .replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));

const pendingRows = count => Array.from({length: count}, (_, index) => approvalRow(`pending-${index}`, "idea", "pending"));
const resolvedRows = count => Array.from({length: count}, (_, index) => approvalRow(`resolved-${index}`, "delete_task", "approved"));
const countsFor = rows => {
  const grouped = new Map();
  for (const row of rows) {
    const key = `${row.kind}/${row.status}`;
    grouped.set(key, (grouped.get(key) ?? 0) + 1);
  }
  return [...grouped].map(([key, count]) => {
    const [kind, status] = key.split("/");
    return approvalCount(kind, status, count);
  });
};

test("approval filters match the active kind and status buttons", () => {
  const pendingIdea = approvalRow("a", "idea", "pending");
  const approvedDecision = approvalRow("b", "owner_decision", "approved");
  assert.equal(petApprovalMatches(pendingIdea, "all", "all"), true);
  assert.equal(petApprovalMatches(pendingIdea, "all", "pending"), true);
  assert.equal(petApprovalMatches(pendingIdea, "all", "approved"), false);
  assert.equal(petApprovalMatches(pendingIdea, "idea", "pending"), true);
  assert.equal(petApprovalMatches(pendingIdea, "decision", "pending"), false);
  assert.equal(petApprovalMatches(approvedDecision, "decision", "approved"), true);
  assert.equal(petApprovalMatches(approvedDecision, "idea", "all"), false);
});

test("pending pages cover every pending row before offering more", () => {
  // 15 pending + 8 resolved; the server pages pending-first, so the first 10
  // and next 10 rows contain the pending rows before any resolved row.
  const all = [...pendingRows(15), ...resolvedRows(8)];
  const counts = countsFor(all);
  const secondPage = all.slice(0, 20);
  const firstPage = all.slice(0, 10);

  const first = petApprovalPage(firstPage, {kind: "all", status: "pending", counts, total: all.length});
  assert.deepEqual(first, {shown: 10, total: 15, canLoadMore: true});
  const expanded = petApprovalPage(secondPage, {kind: "all", status: "pending", counts, total: all.length});
  assert.deepEqual(expanded, {shown: 15, total: 15, canLoadMore: false}, "button hides once every pending row is loaded");

  // Boundaries: zero, fewer than a page, exactly one page.
  const none = petApprovalPage(resolvedRows(10), {kind: "all", status: "pending", counts: countsFor(resolvedRows(10))});
  assert.deepEqual(none, {shown: 0, total: 0, canLoadMore: false});
  const few = petApprovalPage([...pendingRows(3), ...resolvedRows(7)], {kind: "all", status: "pending", counts: countsFor([...pendingRows(3), ...resolvedRows(12)])});
  assert.deepEqual(few, {shown: 3, total: 3, canLoadMore: false});
  const exact = petApprovalPage(pendingRows(10), {kind: "all", status: "pending", counts: countsFor(pendingRows(10))});
  assert.deepEqual(exact, {shown: 10, total: 10, canLoadMore: false});
});

test("approval counts are exact for every kind and status filter", () => {
  const all = [
    ...pendingRows(2),
    approvalRow("decision-pending", "owner_decision", "pending"),
    approvalRow("idea-approved-1", "idea", "approved"),
    approvalRow("idea-approved-2", "idea", "approved"),
    approvalRow("idea-approved-3", "idea", "approved"),
    ...Array.from({length: 4}, (_, index) => approvalRow(`decision-approved-${index}`, "delete_task", "approved")),
  ];
  const counts = countsFor(all);
  const page = (kind, status, rows = all) => petApprovalPage(rows, {kind, status, counts});

  assert.deepEqual(page("all", "all"), {shown: 10, total: 10, canLoadMore: false});
  assert.deepEqual(page("all", "pending"), {shown: 3, total: 3, canLoadMore: false});
  assert.deepEqual(page("all", "approved"), {shown: 7, total: 7, canLoadMore: false});
  assert.deepEqual(page("all", "rejected"), {shown: 0, total: 0, canLoadMore: false});
  assert.deepEqual(page("idea", "all"), {shown: 5, total: 5, canLoadMore: false});
  assert.deepEqual(page("decision", "all"), {shown: 5, total: 5, canLoadMore: false});
  assert.deepEqual(page("idea", "approved"), {shown: 3, total: 3, canLoadMore: false});
  assert.deepEqual(page("decision", "pending"), {shown: 1, total: 1, canLoadMore: false});
  // A truncated page only knows the rows it loaded; the grouped totals still
  // say how many approved rows remain.
  assert.deepEqual(page("all", "approved", all.slice(0, 4)), {shown: 1, total: 7, canLoadMore: true});
});

test("the inspection pending count is the fallback when grouped counts are absent", () => {
  const full = [...pendingRows(15), ...resolvedRows(5)];
  const first = petApprovalPage(full.slice(0, 10), {kind: "all", status: "pending", total: 53, pendingTotal: 15});
  assert.deepEqual(first, {shown: 10, total: 15, canLoadMore: true});
  const done = petApprovalPage(full, {kind: "all", status: "pending", total: 53, pendingTotal: 15});
  assert.deepEqual(done, {shown: 15, total: 15, canLoadMore: false});
  const none = petApprovalPage(resolvedRows(10), {kind: "all", status: "pending", total: 53, pendingTotal: 0});
  assert.deepEqual(none, {shown: 0, total: 0, canLoadMore: false});
});

test("approval load-more markup matches the task list component and loading state", () => {
  const more = renderPetApprovalLoadMore({shown: 10, total: 15, canLoadMore: true}, false, approvalText);
  assert.match(more, /class="list-load-more pet-approval-load-more"/);
  assert.match(more, /data-pet-load-more="approval"/);
  assert.match(more, />加载更多</);
  assert.match(more, /已显示 10 \/ 共 15 条/);
  assert.doesNotMatch(more, /disabled/);

  const loading = renderPetApprovalLoadMore({shown: 10, total: 15, canLoadMore: true}, true, approvalText, '<svg class="spin"></svg>');
  assert.match(loading, /<button type="button" data-pet-load-more="approval" disabled>/);
  assert.match(loading, /<svg class="spin"><\/svg>加载中…/);

  const complete = renderPetApprovalLoadMore({shown: 3, total: 3, canLoadMore: false}, false, approvalText);
  assert.doesNotMatch(complete, /<button/);
  assert.match(complete, /已显示 3 \/ 共 3 条/);
  assert.equal(renderPetApprovalLoadMore({shown: 0, total: 0, canLoadMore: false}, false, approvalText), "");
});

test("workbench collapses core documents and opens approvals on pending with ten-row pages", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const loader = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
  assert.match(main, /view\.core\.map\(item => `<details class="pet-doc-entry"><summary>/);
  assert.doesNotMatch(main, /<details class="pet-doc-entry" open>/);
  assert.match(main, /let petApprovalStatusFilter[^=]*= "pending";/);
  assert.match(main, /let petApprovalFilter: [^=]+= "all";/);
  assert.match(main, /let approvalLimit = 10;/);
  assert.match(main, /approvalLimit = Math\.min\(approvalLimit \+ 10, PET_APPROVAL_MAX_LIMIT\)/);
  assert.match(main, /let worklogLimit = 30;/);
  assert.match(main, /else worklogLimit \+= 30;/);
  assert.match(main, /renderPetApprovalLoadMore\(page, loadingMore === "approval", t, icon\("spinner", true\), capped\)/);
  assert.match(loader, /class="list-load-more pet-approval-load-more"/);
});

test("approval paging stops at the server page ceiling and explains why", () => {
  const first = pendingRows(PET_APPROVAL_MAX_LIMIT);
  const counts = [approvalCount("idea", "pending", 500)];
  const capped = petApprovalPage(first, {kind: "all", status: "pending", counts, maxItems: PET_APPROVAL_MAX_LIMIT});
  assert.deepEqual(capped, {shown: 200, total: 500, canLoadMore: false});
  const before = petApprovalPage(first.slice(0, 190), {kind: "all", status: "pending", counts, maxItems: PET_APPROVAL_MAX_LIMIT});
  assert.deepEqual(before, {shown: 190, total: 500, canLoadMore: true}, "under the ceiling still offers more");
  const hint = renderPetApprovalLoadMore(capped, false, approvalText, "", true);
  assert.match(hint, /已显示 200 \/ 共 500 条/);
  assert.match(hint, /审批仅加载最近 200 条。/);
  assert.doesNotMatch(hint, /data-pet-load-more/, "the unreachable button must be gone at the ceiling");
  assert.doesNotMatch(renderPetApprovalLoadMore(before, false, approvalText, "", false), /审批仅加载最近/);
});

test("document edit buttons open collapsed entries, focus the editor and save both sections", async () => {
  const previousDocument = globalThis.document;
  const matches = (element, selector) => {
    if (selector === "details") return element.tagName === "DETAILS";
    if (selector === "pre") return element.tagName === "PRE";
    if (selector === "[data-pet-doc-edit]") return Object.prototype.hasOwnProperty.call(element.dataset, "petDocEdit");
    return false;
  };
  class FakeElement {
    constructor(tagName, options = {}) {
      this.tagName = tagName.toUpperCase();
      this.dataset = {...(options.dataset || {})};
      this.children = [];
      this.parentElement = null;
      this.className = options.className || "";
      this.textContent = options.textContent || "";
      this.value = options.value || "";
      this.type = "";
      this.rows = 0;
      this.disabled = false;
      this.open = Boolean(options.open);
      this.listeners = new Map();
      this.insertedHTML = "";
    }
    append(...children) {
      for (const child of children) { child.parentElement = this; this.children.push(child); }
    }
    addEventListener(name, listener) { this.listeners.set(name, listener); }
    fire(name) {
      const event = {
        defaultPrevented: false,
        stopped: false,
        preventDefault() { this.defaultPrevented = true; },
        stopPropagation() { this.stopped = true; },
      };
      this.listeners.get(name)?.(event);
      return event;
    }
    querySelector(selector) {
      for (const child of this.children) {
        if (matches(child, selector)) return child;
        const nested = child.querySelector(selector);
        if (nested) return nested;
      }
      return null;
    }
    querySelectorAll(selector) {
      const result = [];
      const visit = element => {
        for (const child of element.children) {
          if (matches(child, selector)) result.push(child);
          visit(child);
        }
      };
      visit(this);
      return result;
    }
    closest(selector) {
      let node = this;
      while (node) {
        if (matches(node, selector)) return node;
        node = node.parentElement;
      }
      return null;
    }
    remove() {
      if (!this.parentElement) return;
      const index = this.parentElement.children.indexOf(this);
      if (index >= 0) this.parentElement.children.splice(index, 1);
      this.parentElement = null;
    }
    replaceWith(next) {
      if (!this.parentElement) return;
      const parent = this.parentElement;
      const index = parent.children.indexOf(this);
      if (index >= 0) {
        parent.children[index] = next;
        next.parentElement = parent;
        this.parentElement = null;
      }
    }
    insertAdjacentHTML(_position, html) { this.insertedHTML += html; }
    focus() { globalThis.document.activeElement = this; }
  }
  const entry = (section, name) => {
    const details = new FakeElement("details");
    const summary = new FakeElement("summary");
    const button = new FakeElement("button", {dataset: {petDocEdit: "", petDocSection: section, petDocName: name}});
    summary.append(button);
    details.append(summary, new FakeElement("pre", {textContent: `content-${section}`}));
    const root = new FakeElement("div");
    root.append(details);
    return {root, details, button};
  };
  try {
    globalThis.document = {
      activeElement: null,
      createElement(tagName) { return new FakeElement(tagName); },
    };
    for (const section of ["core", "worklog"]) {
      const name = section === "core" ? "identity.md" : "2099-01-01.md";
      const target = entry(section, name);
      const saved = [];
      let reloads = 0;
      let cancels = 0;
      bindPetDocEditorButtons(target.root, {
        section,
        translate: key => ({"common.save": "保存", "common.cancel": "取消"}[key] ?? key),
        save: async (savedSection, savedName, content) => { saved.push({section: savedSection, name: savedName, content}); },
        reload: async () => { reloads++; },
        cancel: () => { cancels++; },
      });
      const click = target.button.fire("click");
      assert.equal(click.defaultPrevented, true, section);
      assert.equal(click.stopped, true, "the summary must not toggle a second time");
      assert.equal(target.details.open, true, `${section} entry must open before rendering the editor`);
      assert.equal(target.button.parentElement, null, "the edit button is replaced by the editor");
      const editor = target.details.children.find(child => child.className === "pet-doc-editor");
      assert.ok(editor, section);
      const textarea = editor.children[0];
      assert.equal(textarea.tagName, "TEXTAREA");
      assert.equal(textarea.value, `content-${section}`);
      assert.equal(globalThis.document.activeElement, textarea, "focus moves into the visible editor");
      const [saveButton, cancelButton] = editor.children[1].children;
      assert.equal(saveButton.textContent, "保存");
      assert.equal(cancelButton.textContent, "取消");
      saveButton.fire("click");
      await new Promise(resolve => setTimeout(resolve, 0));
      assert.deepEqual(saved, [{section, name, content: `content-${section}`}], section);
      assert.equal(reloads, 1);
      cancelButton.fire("click");
      assert.equal(cancels, 1);
    }
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
});

test("document editors share one attribute and the approval footer caps at 200", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const loader = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
  assert.ok(main.includes('class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="core"'));
  assert.ok(main.includes('class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="${escapeHTML(section)}"'));
  assert.match(loader, /querySelectorAll<HTMLButtonElement>\("\[data-pet-doc-edit\]"\)/);
  assert.match(loader, /details\.open = true/);
  assert.match(loader, /textarea\.focus\(\)/);
  assert.match(main, /approvalLimit = Math\.min\(approvalLimit \+ 10, PET_APPROVAL_MAX_LIMIT\)/);
  assert.match(main, /if \(loadingMore !== kind\) return;/);
  assert.match(main, /petWorkbench\.pendingFirstHint/);
});
