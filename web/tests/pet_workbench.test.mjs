import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {createPetApprovalCounter, petPendingApprovalBadge} from "../dist/pet_approvals.js";
import {createPetWorkbenchLoader, renderPetInspectionSummary, renderPetWorkbenchLoadError} from "../dist/pet_workbench.js";

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
  assert.match(main, /isCurrent: \(\) => body\.isConnected && activePetWorkbenchBinding === binding/);
  assert.match(main, /renderPetInspectionSummary\(view\.inspection, t, formatDateTime\)/);
  assert.match(main, /bindPetWorkbenchPage\(\)\.catch\(showPetWorkbenchBindFailure\)/);
  assert.match(main, /function showPetWorkbenchBindFailure/);
  assert.match(main, /renderPetWorkbenchLoadError\(loadError, t\)/);
  assert.match(main, /workbenchBindFailed/);
  assert.doesNotMatch(main, /petWorkbenchBound/);
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
