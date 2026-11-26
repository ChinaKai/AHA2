import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {createPetApprovalCounter, petApprovalFilterCount, petPendingApprovalBadge, renderPetApprovalFilters, renderPetApprovalList, sortPetApprovals} from "../build/pet_approvals.js";
import {createPetWorkbenchLoader, createPetWorkbenchPageKeeper, mapPetWorkbenchTab, PET_APPROVAL_MAX_LIMIT, petApprovalMatches, petApprovalPage, renderPetApprovalLoadMore, renderPetWorkbenchLoadError, swapPetWorkbenchPage} from "../build/pet_workbench.js";
import {bindPetDocEditorButtons, countPetWatchAnomalies, renderPetOverview, renderPetRecords, sortPetWorklog} from "../build/pet_desk.js";

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
  binding = keeper.adopt(body, page, loader, "overview");
  const pending = loader.load();
  assert.equal(requests.length, 1, "entering the view issues exactly one request");

  // Navigation renders immediately and again after ensureViewData; background
  // resource loads and messages render too. Every one of them must reuse the
  // live page instead of rebinding the loader.
  for (let render = 0; render < 6; render++) {
    assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "overview"}), page, `render ${render} carries the page`);
    assert.equal(keeper.owns(body), true, `render ${render} sees the live binding`);
    assert.equal(requests.length, 1, `render ${render} must not issue another request`);
    assert.equal(requests[0].aborted, false, `render ${render} must not abort the in-flight request`);
  }

  // Only actually leaving the view aborts the request.
  assert.equal(keeper.beforeRender({view: "tasks", authenticated: true}), null);
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
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "core"}), first.page);

  const second = fakeWorkbenchPage();
  keeper.adopt(second.body, second.page, {dispose: () => {}}, "core");
  assert.equal(firstDisposed, 1, "an unreused page must not keep its old request");
  assert.equal(keeper.owns(first.body), false);
  assert.equal(keeper.owns(second.body), true);
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "core"}), second.page);
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
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "core"}), null, "without a page the shell must be rebuilt");
  keeper.release();

  const detached = fakeWorkbenchPage({connected: false});
  keeper.adopt(detached.body, detached.page, {dispose: () => {}}, "core");
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "core"}), null);
});

test("logout releases the keeper and aborts the in-flight workbench request", async () => {
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
  assert.equal(requests.length, 1);

  // Logout renders the login view while state.view is still "pet-workbench".
  // The unauthenticated render must release the page instead of preserving it.
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: false, tab: "core"}), null);
  assert.equal(requests[0].aborted, true, "logout must abort the in-flight request synchronously");
  assert.equal(keeper.binding, null);
  assert.equal(keeper.owns(body), false, "the kept page must be cleared");
  await pending;
  assert.equal(loader.busy(), false, "the released loader still clears its loading state");
  assert.equal(requests.length, 1, "logout must not start a retry or a new request");
});

// Fake tabbed page: the buttons carry the same data attribute and click
// listener shape main.ts uses, so the keeper drives the real follow path.
function fakeTabbedWorkbench() {
  const tabs = new Map();
  const clickCounts = new Map();
  const body = {isConnected: true, innerHTML: "core-body"};
  const page = {
    isConnected: true,
    querySelector(selector) {
      const match = /^\[data-pet-workbench-tab="(.+)"\]$/.exec(selector);
      return match ? tabs.get(match[1]) ?? null : null;
    },
  };
  for (const name of ["overview", "records", "settings"]) {
    const classes = new Set(name === "overview" ? ["active"] : []);
    tabs.set(name, {
      dataset: {petWorkbenchTab: name},
      classList: {toggle(token, force) { if (force) classes.add(token); else classes.delete(token); }},
      listeners: [],
      addEventListener(_event, listener) { this.listeners.push(listener); },
      click() {
        clickCounts.set(name, (clickCounts.get(name) ?? 0) + 1);
        for (const listener of this.listeners) listener();
      },
      isActive: () => classes.has("active"),
    });
  }
  return {page, body, tabs, clickCounts};
}

test("a programmatic tab change is followed on the preserved page without restarting the request", async () => {
  const keeper = createPetWorkbenchPageKeeper();
  const {page, body, tabs, clickCounts} = fakeTabbedWorkbench();
  let binding = null;
  let active = "overview";
  const requests = [];
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
  // Same listener contract as bindPetWorkbenchPage: update the tab state and
  // repaint the kept body.
  for (const tab of tabs.values()) {
    tab.addEventListener("click", () => {
      active = tab.dataset.petWorkbenchTab;
      if (binding) binding.tab = active;
      for (const other of tabs.values()) other.classList.toggle("active", other === tab);
      body.innerHTML = `${active}-body`;
    });
  }
  binding = keeper.adopt(body, page, loader, "core");
  const pending = loader.load();
  assert.equal(requests.length, 1);

  // The pet panel sets petWorkbenchTab = "settings" and triggers a render.
  assert.equal(
    keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "settings"}),
    page,
    "the mounted page is still carried across the render",
  );
  assert.equal(tabs.get("settings").isActive(), true, "the settings tab becomes selected");
  assert.equal(tabs.get("overview").isActive(), false);
  assert.equal(active, "settings");
  assert.equal(binding.tab, "settings");
  assert.equal(body.innerHTML, "settings-body");
  assert.equal(requests.length, 1, "following the tab must not restart the request");
  assert.equal(requests[0].aborted, false, "following the tab must not abort the request");

  // The kept page's listeners still work when the user moves back to overview
  // and records, and each click follows the normal path.
  tabs.get("overview").click();
  assert.equal(active, "overview");
  assert.equal(tabs.get("overview").isActive(), true);
  assert.equal(binding.tab, "overview");
  tabs.get("records").click();
  assert.equal(active, "records");
  assert.equal(tabs.get("records").isActive(), true);
  assert.equal(binding.tab, "records");
  assert.equal(requests.length, 1, "tab switches must not issue another request");

  // Already on the wanted tab: no extra click (otherwise renders could loop).
  const recordClicks = clickCounts.get("records");
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "records"}), page);
  assert.equal(clickCounts.get("records"), recordClicks, "a matching tab is not clicked again");
  assert.equal(requests.length, 1);

  // Leaving the view still aborts exactly once.
  assert.equal(keeper.beforeRender({view: "tasks", authenticated: true}), null);
  assert.equal(requests[0].aborted, true);
  await pending;
  assert.equal(loader.busy(), false);
});

test("navigation's second render swaps the same page and does not rebind or re-request", async () => {
  const keeper = createPetWorkbenchPageKeeper();
  const {page, body} = fakeWorkbenchPage();
  const requests = [];
  let binding = null;
  let bindCalls = 0;
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
  bindCalls++;
  const pending = loader.load();

  // One render() pass: decide, replace the shell, re-insert the preserved page,
  // then bindCommon -> bindPetWorkbenchPage.
  const renderCycle = () => {
    const preserved = keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "core"});
    assert.equal(preserved, page);
    let swapped = null;
    const app = {
      querySelector: selector => selector === ".pet-workbench-page" ? {replaceWith: node => { swapped = node; }} : null,
    };
    assert.equal(swapPetWorkbenchPage(app, preserved), true);
    assert.equal(swapped, page, "the page is re-inserted unchanged");
    if (keeper.owns(body)) return; // bindPetWorkbenchPage's early return
    bindCalls++;
    keeper.adopt(body, page, loader, "core");
    void loader.load();
  };

  renderCycle(); // the navigation click's render()
  renderCycle(); // ensureViewData(...).then(render)
  assert.equal(bindCalls, 1, "the second navigation render must not rebind");
  assert.equal(requests.length, 1, "one entry stays exactly one request");
  assert.equal(requests[0].aborted, false);

  keeper.release();
  await pending;
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

test("workbench wiring uses the guarded loader and a compact overview", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const api = await readFile(resolve(root, "web/src/api.ts"), "utf8");
  const loader = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
  assert.match(main, /createPetWorkbenchLoader\(/);
  assert.match(main, /api\.petWorkbench\(\{approvalLimit, worklogLimit\}, signal\)/);
  assert.match(main, /isCurrent: \(\) => body\.isConnected && petWorkbenchPageKeeper\.binding === binding/);
  assert.match(main, /renderPetOverview\(\{/);
  assert.match(main, /inspection: view\.inspection/);
  assert.match(main, /formatTime: formatDateTime/);
  assert.doesNotMatch(loader, /renderPetInspectionSummary|renderPetChannelHealth/);
  assert.doesNotMatch(loader, /pet-overview-inspection|pet-overview-channels/);
  assert.match(main, /bindPetWorkbenchPage\(\)\.catch\(showPetWorkbenchBindFailure\)/);
  assert.match(main, /function showPetWorkbenchBindFailure/);
  assert.match(main, /renderPetWorkbenchLoadError\(loadError, t\)/);
  assert.match(main, /workbenchBindFailed/);
  assert.doesNotMatch(main, /petWorkbenchBound/);
  // A same-view render carries the mounted page instead of rebinding, and the
  // logout render (authenticated=false) releases it instead of preserving it.
  assert.match(main, /const preservedWorkbenchPage = petWorkbenchPageKeeper\.beforeRender\(\{/);
  assert.match(main, /view: state\.view,[\s\S]{0,80}?authenticated: Boolean\(state\.auth\?\.authenticated\),[\s\S]{0,80}?tab: normalizedPetWorkbenchTab\(\)/);
  assert.match(main, /swapPetWorkbenchPage\(app, preservedWorkbenchPage\)/);
  assert.match(main, /if \(petWorkbenchPageKeeper\.owns\(body\)\) return;/);
  assert.match(main, /petWorkbenchPageKeeper\.release\(\)/);
  // The programmatic tab follow lives in the keeper so it is behavior-tested.
  assert.match(loader, /context\.authenticated \|\| context\.view !== "pet-workbench"/);
  assert.match(loader, /binding\.tab !== context\.tab/);
  assert.match(loader, /querySelector<HTMLButtonElement>\(`\[data-pet-workbench-tab="\$\{context\.tab\}"\]`\)\?\.click\(\)/);
  // Navigation must not restart the load it just started.
  assert.match(main, /state\.view === "pet-workbench" && petWorkbenchPageKeeper\.binding\) return;/);
  assert.doesNotMatch(main, /withTimeout\(api\.petWorkbench/);
  assert.match(api, /petWorkbench\(options: \{[^}]*\}[^)]*signal\?: AbortSignal\)/);
  assert.match(loader, /maxRetries \?\? 1/);
  assert.match(loader, /request\.abort\(\)/);
  assert.match(loader, /loading = false/);
});

test("workbench defaults to overview and removes journal UI/API while retaining old bookmarks", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const api = await readFile(resolve(root, "web/src/api.ts"), "utf8");
  const assistant = await readFile(resolve(root, "web/src/assistant.ts"), "utf8");
  const i18n = await readFile(resolve(root, "web/src/i18n.ts"), "utf8");
  assert.match(main, /let petWorkbenchTab = "overview"/);
  assert.match(main, /class="active" data-pet-workbench-tab="overview"/);
  assert.doesNotMatch(main, /data-pet-workbench-tab="diary"|view\.diary|diaryLimit|petDiary/);
  assert.doesNotMatch(api, /petDiary|diary_limit|diaryLimit/);
  assert.doesNotMatch(assistant + i18n, /日记|diary/i);
  assert.match(main, /value === "pet-diary"\) return "pet-workbench"/);
  assert.match(main, /setInterval\(\(\) => void refreshPetPendingApprovalCount\(\), 60_000\)/);
  assert.match(main, /await api.resolvePetApproval[\s\S]*?await refreshPetPendingApprovalCount\(true\)/);
  assert.doesNotMatch(main, /view\.approvals\.filter\(item => item\.status === "pending"\)\.length/);
});

test("live pet_approval events refresh the mounted approval list", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const loader = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
  // The global stream and the reconnect path share one helper.
  assert.match(main, /if \(type === "pet_approval"\) refreshPetApprovalState\(\);/);
  assert.match(main, /function refreshPetApprovalState\(\): void \{[\s\S]*?void refreshPetPendingApprovalCount\(true\);[\s\S]*?petWorkbenchPageKeeper\.binding\?\.refresh\?\.\(\);/);
  // The approval history now lives on the records tab: it reloads live there,
  // and entering records re-reads because events that arrived on another tab
  // cannot patch the mounted rows.
  assert.match(main, /binding\.refresh = \(\) => \{\s*if \(active === "records"\) refreshApprovals\(\);\s*else approvalEventMissed = true;\s*\};/);
  assert.match(main, /if \(active === "records" && approvalEventMissed\) \{\s*approvalEventMissed = false;\s*void load\(\);/);
  // An event during an in-flight read queues exactly one follow-up.
  assert.match(main, /if \(approvalRefreshQueued\) return;/);
  assert.match(main, /approvalRefreshQueued = true;/);
  assert.match(main, /void load\(\)\.then\(\(\) => \{\s*approvalRefreshQueued = false;/);
  // A failed refresh keeps the last good list instead of clearing it.
  assert.match(main, /if \(view && active === "records"\) renderTab\(\);/);
  assert.match(main, /loadError \? renderPetWorkbenchLoadError\(loadError, t\) : ""/);
  assert.match(loader, /refresh\?: \(\) => void;/);
  // The resolve path uses the same queued refresh instead of a plain load.
  assert.match(main, /await refreshPetPendingApprovalCount\(true\);[\s\S]{0,80}?refreshApprovals\(\);/);
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

test("an executing heavy approval stays visible under the pending filter", () => {
  const running = approvalRow("c", "delete_workspace", "executing");
  assert.equal(petApprovalMatches(running, "all", "pending"), true);
  assert.equal(petApprovalMatches(running, "decision", "all"), true);
  assert.equal(petApprovalMatches(running, "all", "approved"), false);
  const counts = [
    approvalCount("delete_workspace", "executing", 1),
    approvalCount("delete_workspace", "pending", 2),
  ];
  assert.deepEqual(
    petApprovalPage([running], {kind: "all", status: "pending", counts}),
    {shown: 1, total: 3, canLoadMore: true},
  );
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
  // The records renderer moved to the pet_desk route chunk (task-813).
  const desk = await readFile(resolve(root, "web/src/pet_desk.ts"), "utf8");
  assert.match(main, /renderPetRecords\(view\.core, view\.worklog,/);
  // Records render core documents in their groups (task-813 step three).
  assert.match(desk, /entries\.map\(entry => renderCoreEntry\(entry, entry\.index, translate, ledger\)\)/);
  assert.match(desk, /<details class="pet-doc-entry" id="pet-record-core-\$\{index\}">/);
  assert.doesNotMatch(desk, /<details class="pet-doc-entry" open>/);
  // The approval history shows every status by default (the pending queue
  // is the overview's "for you" list).
  assert.match(main, /let petApprovalStatusFilter[^=]*= "all";/);
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
  // Document editing renders and binds from the pet_desk route chunk.
  const loader = await readFile(resolve(root, "web/src/pet_desk.ts"), "utf8");
  const approvals = await readFile(resolve(root, "web/src/pet_approvals.ts"), "utf8");
  const coreEdit = loader.match(/function renderCoreEntry\([\s\S]*?\n}/)?.[0] || "";
  const worklogEdit = loader.match(/function renderWorklogEntry\([\s\S]*?\n}/)?.[0] || "";
  assert.ok(coreEdit.includes('class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="core"'));
  assert.ok(worklogEdit.includes('class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="worklog"'));
  assert.match(loader, /querySelectorAll<HTMLButtonElement>\("\[data-pet-doc-edit\]"\)/);
  assert.match(loader, /const section = button\.dataset\.petDocSection \|\| binding\.section \|\| ""/);
  assert.match(loader, /details\.open = true/);
  assert.match(loader, /textarea\.focus\(\)/);
  assert.match(main, /approvalLimit = Math\.min\(approvalLimit \+ 10, PET_APPROVAL_MAX_LIMIT\)/);
  assert.match(main, /if \(loadingMore !== kind\) return;/);
  assert.match(approvals, /petWorkbench\.pendingFirstHint/);
});

test("the three workbench tabs default to overview and absorb legacy names", async () => {
  const {PET_WORKBENCH_TABS, petWorkbenchTabAnchor, petWorkbenchIntentTab} = await import("../build/pet_workbench.js");
  assert.deepEqual([...PET_WORKBENCH_TABS], ["overview", "records", "settings"]);
  // Old approvals links land on the records tab's approval history.
  assert.equal(petWorkbenchTabAnchor("approvals"), "pet-record-approvals");
  assert.equal(petWorkbenchTabAnchor("ideas"), "pet-record-approvals");
  assert.equal(petWorkbenchTabAnchor("records"), "");
  assert.equal(mapPetWorkbenchTab(petWorkbenchIntentTab("handle-pending")), "overview");
  assert.equal(mapPetWorkbenchTab(petWorkbenchIntentTab("approval-history")), "records");
  assert.equal(petWorkbenchTabAnchor(petWorkbenchIntentTab("approval-history")), "pet-record-approvals");
  assert.equal(mapPetWorkbenchTab(undefined), "overview");
  assert.equal(mapPetWorkbenchTab(""), "overview");
  assert.equal(mapPetWorkbenchTab("overview"), "overview");
  assert.equal(mapPetWorkbenchTab("approvals"), "records", "the approvals tab is the records history now");
  assert.equal(mapPetWorkbenchTab("ideas"), "records", "legacy idea links open the history");
  assert.equal(mapPetWorkbenchTab("records"), "records");
  assert.equal(mapPetWorkbenchTab("core"), "records");
  assert.equal(mapPetWorkbenchTab("worklog"), "records");
  assert.equal(mapPetWorkbenchTab("settings"), "settings");
  assert.equal(mapPetWorkbenchTab("inspection"), "overview");
  assert.equal(mapPetWorkbenchTab("follows"), "overview");
  assert.equal(mapPetWorkbenchTab("unknown-tab"), "overview");
});

const overviewTranslations = {
  "petWorkbench.overviewChannelsHint": "{total} channels",
  "petWorkbench.overviewFollows": "关注任务（{count}）",
  "petWorkbench.otherFollows": "忽略的任务（{count}）",
  "petWorkbench.unfollowedTasks": "未关注的任务（{count}）",
  "petWorkbench.emptyIgnored": "没有忽略的任务。",
  "petWorkbench.recordsToc": "目录",
  "petWorkbench.recordsTocCollapse": "收起目录",
  "petWorkbench.recordsTocExpand": "展开目录",
  "petWorkbench.core": "核心文档",
  "petWorkbench.worklog": "工作记录",
  "petWorkbench.emptyFollows": "暂无关注的任务。",
};
const overviewText = (key, values = {}) => (overviewTranslations[key] ?? key)
  .replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));

const overviewChannel = {
  id: "c-1", name: "Mail", status: "degraded", pending: 2, stuck_pending: 0, queued_pending: 0,
  attempted_pending: 0, expired_leases: 0, oldest_pending_minutes: 0, oldest_unattempted_minutes: 0,
  oldest_queued_minutes: 0, oldest_attempted_minutes: 0, expired_lease_minutes: 0, dead_letter: 0,
  degraded: true,
};

test("overview counts pending approvals, watched anomalies and degraded channels", () => {
  const html = renderPetOverview({
    inspection: {health: "degraded", version: "1.2.3", pending_approvals: 3, checked_at: "2026-01-05T00:00:00Z"},
    approvals: [
      {id: "a-1", title: "Delete task", status: "pending", kind: "delete_task", created_at: "2026-01-05T00:00:00Z"},
      {id: "a-2", title: "Old idea", status: "approved", kind: "idea", created_at: "2026-01-04T00:00:00Z"},
    ],
    watches: [
      {task_id: "t-failed", code: "AHA-1", title: "Failed task", status: "failed", watched: true, reason: "manual", ignored: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-05T00:00:00Z"},
      {task_id: "t-ok", code: "AHA-2", title: "Running task", status: "active", watched: true, reason: "manual", ignored: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-04T00:00:00Z"},
      {task_id: "t-unwatched", code: "AHA-3", title: "Blocked but unwatched", status: "blocked", watched: false, reason: "manual", ignored: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z"},
      {task_id: "t-ignored", code: "AHA-4", title: "Ignored task", status: "waiting_user", watched: false, reason: "ignored", ignored: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z"},
    ],
    channels: [overviewChannel],
    channelTotal: 2,
    pendingApprovals: 3,
    translate: overviewText,
    formatTime: value => `at ${value}`,
  });
  assert.match(html, /data-pet-overview-jump="approvals">\s*<strong>3<\/strong>/);
  assert.match(html, /data-pet-overview-jump="follows"[^>]*>\s*<strong>1<\/strong>/, "only watched anomalies count");
  assert.match(html, /data-pet-overview-jump="channels"[^>]*>\s*<strong>1<\/strong>/);
  assert.match(html, /2 channels/);
  assert.match(html, /id="pet-overview-follows"/);
  // The three follow lists are always open: a plain heading, no <details>.
  assert.match(html, /<section class="pet-overview-follows pet-follow-list" id="pet-overview-follows" aria-labelledby="pet-overview-follows-title"><h3 class="pet-follow-title" id="pet-overview-follows-title">关注任务（2）<\/h3>/);
  assert.doesNotMatch(html, /<details|<summary/);
  const ignoredList = html.match(/<section [^>]*id="pet-overview-ignored"[\s\S]*?<\/section>/)?.[0] ?? "";
  const unfollowedList = html.match(/<section [^>]*id="pet-overview-unfollowed"[\s\S]*?<\/section>/)?.[0] ?? "";
  assert.match(ignoredList, /<h3 class="pet-follow-title" id="pet-overview-ignored-title">忽略的任务（1）<\/h3>/);
  assert.match(ignoredList, /data-pet-watch-ignore="false" data-pet-watch-task="t-ignored"/);
  assert.doesNotMatch(ignoredList, /t-unwatched/);
  assert.match(unfollowedList, /<h3 class="pet-follow-title" id="pet-overview-unfollowed-title">未关注的任务（1）<\/h3>/);
  assert.match(unfollowedList, /data-pet-watch-ignore="true" data-pet-watch-task="t-unwatched"/);
  assert.doesNotMatch(unfollowedList, /t-ignored/);
  // A list with rows is a keyboard-reachable scroll area under its heading.
  assert.match(ignoredList, /<div class="pet-overview-other-body pet-follow-body" tabindex="0" aria-labelledby="pet-overview-ignored-title">/);
  assert.match(html, /data-pet-overview-task="t-failed"/);
  assert.match(html, /data-pet-watch-unfollow data-pet-watch-task="t-failed"/, "unfollow is available from the overview");
  assert.match(html, /pet-watch-row anomaly/);
  assert.match(html, /data-pet-approval-open="a-1"/);
  // The top status row and recent activity stay; the inspection snapshot and
  // channel-health details below it were removed from the overview.
  assert.match(html, /class="pet-overview-status"/);
  assert.match(html, /class="pet-overview-recent"/);
  assert.match(html, /petWorkbench\.inspectionVersion: 1\.2\.3/);
  assert.match(html, /petWorkbench\.inspectionCheckedAt: at 2026-01-05T00:00:00Z/);
  assert.doesNotMatch(html, /class="pet-overview-inspection"/);
  assert.doesNotMatch(html, /class="pet-overview-channels"/);
  assert.doesNotMatch(html, /petWorkbench\.inspectionSummary|petWorkbench\.channelHealth/);
  assert.doesNotMatch(html, /pet-inspection-summary|pet-channel-card|pet-channel-list/);
  // The overview stays compact: only the five newest changes are listed.
  assert.equal((html.match(/class="pet-overview-activity"/g) || []).length, 4);
  assert.deepEqual(
    [countPetWatchAnomalies([
      {status: "failed", watched: true},
      {status: "blocked", watched: true},
      {status: "blocked", watched: false},
      {status: "active", watched: true},
    ])],
    [2],
  );
});

test("empty watch lists stay open with a one-line empty state and zero counts", () => {
  const html = renderPetOverview({
    watches: [],
    channels: [],
    translate: overviewText,
    formatTime: identityTime,
  });
  assert.match(html, /id="pet-overview-follows-title">关注任务（0）<\/h3><div class="pet-overview-follows-body pet-follow-body"><div class="field-help pet-follow-empty">暂无关注的任务。<\/div><\/div>/);
  assert.match(html, /id="pet-overview-ignored-title">忽略的任务（0）<\/h3>/);
  assert.match(html, /没有忽略的任务。/);
  // The unfollowed list is always present now, with its own empty line; an
  // empty list is not a focusable scroll area.
  assert.match(html, /id="pet-overview-unfollowed-title">未关注的任务（0）<\/h3><div class="pet-overview-other-body pet-follow-body"><div class="field-help pet-follow-empty">/);
  assert.doesNotMatch(html, /pet-follow-body" tabindex/);
  assert.doesNotMatch(html, /<details|<summary/);
  assert.doesNotMatch(html, /class="pet-overview-inspection"/);
  assert.doesNotMatch(html, /class="pet-overview-channels"/);
});

test("approvals open on pending, sorted by the longest wait", () => {
  const items = [
    {id: "new-pending", kind: "idea", status: "pending", title: "New", created_at: "2026-01-03T00:00:00Z"},
    {id: "old-pending", kind: "delete_task", status: "pending", title: "Old", created_at: "2026-01-01T00:00:00Z"},
    {id: "running", kind: "delete_workspace", status: "executing", title: "Running", created_at: "2026-01-02T00:00:00Z"},
    {id: "resolved-old", kind: "idea", status: "approved", title: "Older", created_at: "2026-01-01T00:00:00Z", resolved_at: "2026-01-04T00:00:00Z"},
    {id: "resolved-new", kind: "idea", status: "rejected", title: "Newer", created_at: "2026-01-01T00:00:00Z", resolved_at: "2026-01-05T00:00:00Z"},
  ];
  assert.deepEqual(
    sortPetApprovals(items).map(item => item.id),
    ["old-pending", "new-pending", "running", "resolved-new", "resolved-old"],
  );
  assert.deepEqual(items.map(item => item.id), ["new-pending", "old-pending", "running", "resolved-old", "resolved-new"], "sorting must not mutate the response");
  const now = Date.parse("2026-01-03T02:00:00Z");
  const html = renderPetApprovalList(items, {translate: overviewText, now});
  const oldIndex = html.indexOf("data-pet-approval-open=\"old-pending\"");
  const newIndex = html.indexOf("data-pet-approval-open=\"new-pending\"");
  assert.ok(oldIndex >= 0 && oldIndex < newIndex, "longest wait first");
  assert.match(html, /已等待 26 小时 0 分钟|petWorkbench\.approvalWaiting/, "waiting duration is shown");
  assert.doesNotMatch(html, /小时 0 分钟|天 0 小时/, "whole hours and days drop the zero part");
  assert.match(html, /class="pet-approval-row pending"/);
  assert.match(renderPetApprovalList([], {translate: overviewText, empty: "没有待处理审批"}), /没有待处理审批/);
});

test("one filter button reports the active count and keeps pending as the default", () => {
  assert.equal(petApprovalFilterCount("all", "pending"), 1);
  assert.equal(petApprovalFilterCount("idea", "all"), 1);
  assert.equal(petApprovalFilterCount("idea", "approved"), 2);
  assert.equal(petApprovalFilterCount("all", "all"), 0);
  const panel = renderPetApprovalFilters("idea", "approved", overviewText);
  assert.match(panel, /class="pet-approval-filter-summary">petWorkbench\.filterActive</);
  assert.match(panel, /data-pet-approval-filter="idea"[^>]*class="active"|class="active" data-pet-approval-filter="idea"/);
  assert.match(panel, /data-pet-approval-pending-hint/);
  assert.match(renderPetApprovalFilters("all", "pending", overviewText), /data-pet-approval-status-filter="pending"/);
  assert.doesNotMatch(renderPetApprovalFilters("all", "pending", overviewText), /data-pet-approval-pending-hint/);
});

test("records pin core documents above a newest-first worklog with a collapsible TOC", () => {
  const worklog = [
    {date: "2026-01-02", content: "middle"},
    {date: "2026-01-03", content: "newest"},
    {date: "2026-01-01", content: "oldest"},
  ];
  assert.deepEqual(sortPetWorklog(worklog).map(entry => entry.date), ["2026-01-03", "2026-01-02", "2026-01-01"]);
  assert.deepEqual(worklog.map(entry => entry.date), ["2026-01-02", "2026-01-03", "2026-01-01"], "sorting must not mutate the response");
  const html = renderPetRecords(
    [{name: "identity.md", content: "identity"}, {name: "principles.md", content: "principles"}],
    worklog,
    {translate: overviewText, emptyCore: "no core", emptyWorklog: "no worklog", tocOpen: true},
  );
  assert.match(html, /<div class="pet-records">/);
  assert.match(html, /<aside class="pet-record-toc" id="pet-record-toc">/);
  assert.match(html, /<nav id="pet-record-toc-nav">/);
  assert.match(html, /data-pet-record-toc-toggle aria-expanded="true" aria-controls="pet-record-toc-nav">收起目录</);
  // The contents follow the four groups (task-813 step three), with each
  // core document under its group.
  assert.match(html, /data-pet-record-goto="pet-record-judgment"/);
  assert.match(html, /data-pet-record-goto="pet-record-core-0"/);
  assert.match(html, /data-pet-record-goto="pet-record-corrections"/);
  assert.match(html, /data-pet-record-goto="pet-record-promises"/);
  assert.match(html, /data-pet-record-goto="pet-record-worklog"/);
  // The index lists each dated worklog entry, not only the section heading.
  assert.match(html, /data-pet-record-goto="pet-record-worklog-0">2026-01-03</);
  assert.match(html, /data-pet-record-goto="pet-record-worklog-1">2026-01-02</);
  assert.match(html, /data-pet-record-goto="pet-record-worklog-2">2026-01-01</);
  assert.match(html, /id="pet-record-worklog-0"/);
  assert.ok(html.indexOf('id="pet-record-judgment"') < html.indexOf('id="pet-record-worklog"'), "core stays pinned on top");
  assert.ok(html.indexOf("newest") < html.indexOf("middle") && html.indexOf("middle") < html.indexOf("oldest"));
  assert.match(html, /data-pet-doc-section="core" data-pet-doc-name="identity\.md"/);
  assert.match(html, /data-pet-doc-section="worklog" data-pet-doc-name="2026-01-03\.md"/);

  const collapsed = renderPetRecords([], [], {
    translate: overviewText, emptyCore: "no core", emptyWorklog: "no worklog", tocOpen: false,
  });
  assert.match(collapsed, /<div class="pet-records toc-collapsed">/);
  assert.match(collapsed, /data-pet-record-toc-toggle aria-expanded="false"[^>]*>展开目录</);
});

test("phones fit the three tabs and turn approval dialogs into full-screen sheets", async () => {
  const css = await readFile(resolve(root, "web/src/styles.css"), "utf8");
  const mobile = css.slice(css.lastIndexOf("@media (max-width: 640px)"));
  assert.notEqual(mobile, css, "a phone breakpoint must exist");
  assert.match(mobile, /\.pet-workbench-tabs \{ display: grid; grid-template-columns: repeat\(3, minmax\(0, 1fr\)\); gap: 4px; \}/);
  assert.match(mobile, /\.pet-workbench-tabs button \{[^}]*min-width: 0;[^}]*overflow: hidden;/);
  assert.match(mobile, /\.pet-overview-cards \{ grid-template-columns: repeat\(3, minmax\(0, 1fr\)\); gap: 4px; \}/);
  assert.match(mobile, /\.pet-overview-card \{ padding: 6px 8px;/);
  assert.match(mobile, /\.pet-overview-card-label \{[^}]*white-space: nowrap;[^}]*text-overflow: ellipsis;/);
  assert.match(mobile, /\.pet-overview-card small \{ display: none; \}/);
  assert.match(mobile, /\.pet-watch-row-actions \{ width: 100%; justify-content: flex-start; \}/);
  assert.match(mobile, /\.pet-records \{ grid-template-columns: minmax\(0, 1fr\); \}/);
  assert.match(mobile, /\.pet-record-toc-toggle \{ width: 100%; justify-content: flex-start; \}/);
  assert.match(mobile, /dialog\.pet-approval-detail-dialog, dialog\.pet-approval-filter-dialog \{[^}]*width: 100vw;[^}]*height: 100dvh;/);
  assert.match(css, /\.pet-approval-row \{[^}]*grid-template-columns: 32px minmax\(0, 1fr\) auto auto;/);
});

test("watch lists align titles, status and actions to the left", async () => {
  const css = await readFile(resolve(root, "web/src/styles.css"), "utf8");
  assert.match(css, /\.pet-watch-row \{[^}]*text-align: left;/);
  assert.match(css, /\.pet-watch-row-main \{[^}]*justify-content: start;[^}]*justify-items: start;/);
  assert.match(css, /\.pet-watch-row-main strong, \.pet-watch-row-main small \{[^}]*text-align: left;/);
  assert.match(css, /\.pet-watch-row-actions \{[^}]*justify-content: flex-start;/);
  assert.match(css, /\.pet-overview-follows, \.pet-overview-other \{ text-align: left; \}/);
  assert.match(css, /\.pet-record-toc button \{[^}]*text-align: left;[^}]*justify-content: flex-start;/);
  const mobile = css.slice(css.lastIndexOf("@media (max-width: 640px)"));
  assert.match(mobile, /\.pet-watch-row-actions \{ width: 100%; justify-content: flex-start; \}/);
  assert.doesNotMatch(mobile, /\.pet-watch-row-actions \{[^}]*justify-content: flex-end/);
});

test("ignored and unfollowed lists have labels in both locales", async () => {
  const i18n = await readFile(resolve(root, "web/src/i18n.ts"), "utf8");
  assert.match(i18n, /"petWorkbench\.otherFollows": "忽略的任务（\{count\}）"/);
  assert.match(i18n, /"petWorkbench\.otherFollows": "Ignored tasks \(\{count\}\)"/);
  assert.match(i18n, /"petWorkbench\.unfollowedTasks": "未关注的任务（\{count\}）"/);
  assert.match(i18n, /"petWorkbench\.unfollowedTasks": "Unfollowed tasks \(\{count\}\)"/);
  assert.match(i18n, /"petWorkbench\.emptyIgnored": "没有忽略的任务。"/);
  assert.match(i18n, /"petWorkbench\.emptyIgnored": "No ignored tasks\."/);
  assert.doesNotMatch(i18n, /其他关注|Other watches/);
  // Followed tasks expose the count in the same title; legacy ignore/follow
  // data attributes and API calls are unchanged.
  assert.match(i18n, /"petWorkbench\.overviewFollows": "关注任务（\{count\}）"/);
  assert.match(i18n, /"petWorkbench\.overviewFollows": "Watched tasks \(\{count\}\)"/);
});

test("records use a collapsible left contents column that survives tab switches", async () => {
  const css = await readFile(resolve(root, "web/src/styles.css"), "utf8");
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  // The records renderer is in the pet_desk route chunk.
  const loader = await readFile(resolve(root, "web/src/pet_desk.ts"), "utf8");
  assert.match(css, /\.pet-records \{ display: grid; grid-template-columns: minmax\(180px, 220px\) minmax\(0, 1fr\); gap: 12px; align-items: start; \}/);
  assert.match(css, /\.pet-records\.toc-collapsed \{ grid-template-columns: minmax\(0, 1fr\); \}/);
  assert.match(css, /\.pet-records\.toc-collapsed \.pet-record-toc \{ display: none; \}/);
  assert.match(css, /\.pet-record-toc \{[^}]*position: sticky;/);
  assert.match(css, /\.pet-record-content \{[^}]*min-width: 0;/);
  const mobile = css.slice(css.lastIndexOf("@media (max-width: 640px)"));
  assert.match(mobile, /\.pet-records \{ grid-template-columns: minmax\(0, 1fr\); \}/);
  assert.match(mobile, /\.pet-record-toc \{ position: static; max-height: none; \}/);

  // Phones default to folded; the module-level flag is read on every records
  // render, so switching tabs does not reset an owner's choice.
  assert.match(main, /let petRecordsTocOpen: boolean \| null = null;/);
  assert.match(main, /petRecordsTocOpen = !window\.matchMedia\("\(max-width: 640px\)"\)\.matches;/);
  assert.match(main, /tocOpen: petRecordsTocOpen,/);
  assert.match(main, /petRecordsTocOpen = open;/);
  assert.match(main, /classList\.toggle\("toc-collapsed", !open\)/);
  assert.match(main, /bindRecordTocToggle\(\);/);
  assert.match(loader, /tocOpen \? "" : " toc-collapsed"/);
  assert.match(loader, /aria-expanded="\$\{tocOpen\}"/);
});

/* Pet desk overview (task-813, design doc section 10) --------------------- */

// pet_desk.js imports the versioned i18n URL that build-web.mjs writes, so the
// tests must use that same instance: a bare "../build/i18n.js" import is a
// second module whose locale pet_desk never sees.
const deskCatalog = async locale => {
  const source = await readFile(resolve(root, "web/build/pet_desk.js"), "utf8");
  const versioned = source.match(/from "\.\/(i18n\.js\?v=[^"]+)"/)?.[1];
  assert.ok(versioned, "pet_desk.js must import the versioned i18n singleton");
  const i18n = await import(new URL(`../build/${versioned}`, import.meta.url).href);
  i18n.applyLocale(locale);
  return i18n;
};
const deskNow = Date.parse("2026-11-26T02:00:00Z");
const deskFixture = () => ({
  needs_owner: [
    {kind: "blocked", task_id: "t-790", task_code: "task-790", title: "Needs a permission", detail: "missing <permission>", since: "2026-11-26T00:30:00Z"},
    {kind: "approval", approval_id: "a-801", approval_kind: "owner_decision", risk: "high", task_id: "t-801", task_code: "task-801", title: "Merge task-801?", detail: "pet summary", since: "2026-11-26T01:00:00Z"},
    {kind: "question", task_id: "t-813", task_code: "task-813", title: "Desk plan", questions: ["Step one only?", "Plan or build?"], since: "2026-11-26T01:15:00Z"},
    {kind: "failed", task_id: "t-1", task_code: "task-1", title: "Failed task", since: "2026-11-26T01:20:00Z"},
    {kind: "stopped", task_id: "t-2", task_code: "task-2", title: "Stopped task", since: "0001-01-01T00:00:00Z"},
  ],
  needs_owner_total: 7,
  later: [
    {kind: "idea", approval_id: "idea-1", title: "Tidy channel contacts", since: "2026-11-26T01:50:00Z"},
    {kind: "next_step", task_id: "t-3", task_code: "task-3", title: "Done round", detail: "Wait for review notes", since: "2026-11-25T23:00:00Z"},
  ],
  later_total: 12,
  waiting_others: [
    {kind: "review", task_id: "t-805", task_code: "task-805", title: "Reviewed work", on: "task-806", since: "2026-11-26T01:15:00Z"},
    {kind: "executing", title: "Delete workspace X", on: "delete_workspace", since: "2026-11-26T01:58:00Z"},
    {kind: "channel", title: "Feishu group A", on: "c-1", count: 3, since: "2026-11-26T01:56:00Z"},
  ],
  waiting_others_total: 3,
  running: [
    {task_id: "t-811", task_code: "task-811", title: "Overnight comparison", status: "active", turn_status: "running", detail: "comparing builds", hardware: ["Debug board 1"], since: "2026-11-26T01:48:00Z"},
    {task_id: "t-812", task_code: "task-812", title: "Preparing task", status: "preparing", since: "bad-time"},
  ],
  running_total: 4,
  corrections: [{at: "2026-11-25T00:00:00Z", approval_id: "a-9", kind: "delete_workspace", title: "Delete workspace X", owner_note: "Keep it for comparison"}],
  open_loops: ["loop one", "loop two", "loop three"],
  open_loops_total: 5,
  agenda: ["agenda one", "agenda two"],
  agenda_total: 3,
});
// The desk's elapsed-time labels are live spans (data-pet-desk-since) that
// tick in place; the text assertions below read them as plain text.
const deskText = html => html.replace(/<span class="pet-desk-elapsed" data-pet-desk-since="[^"]*" data-pet-desk-elapsed-key="[^"]*">([^<]*)<\/span>/g, "$1");
const renderDesk = options => deskText(renderPetOverview(options));
const deskOptions = (i18n, desk, extra = {}) => ({
  desk,
  inspection: {health: "ok", version: "1.2.3", uptime_minutes: 90, checked_at: "2026-11-26T01:40:00Z"},
  watches: [
    {task_id: "t-w", code: "task-9", title: "Watched", status: "active", watched: true, ignored: false, reason: "manual", created_at: "2026-11-25T00:00:00Z", updated_at: "2026-11-25T00:00:00Z"},
    {task_id: "t-i", code: "task-10", title: "Ignored", status: "waiting_user", watched: false, ignored: true, reason: "ignored", created_at: "2026-11-25T00:00:00Z", updated_at: "2026-11-25T00:00:00Z"},
    {task_id: "t-u", code: "task-11", title: "Unfollowed", status: "waiting_user", watched: false, ignored: false, reason: "manual", created_at: "2026-11-25T00:00:00Z", updated_at: "2026-11-25T00:00:00Z"},
  ],
  channels: [],
  translate: i18n.t,
  formatTime: value => `at ${value}`,
  now: deskNow,
  ...extra,
});
const section = (html, id) => html.match(new RegExp(`<section [^>]*id="${id}"[\\s\\S]*?</section>`))?.[0] ?? "";

test("desk elapsed time is a pure function that hides unknown timestamps", async () => {
  const {petDeskElapsedMinutes, petDeskElapsedText} = await import("../build/pet_desk.js");
  assert.equal(petDeskElapsedMinutes("2026-11-26T00:30:00Z", deskNow), 90);
  assert.equal(petDeskElapsedMinutes("2026-11-26T02:05:00Z", deskNow), 0, "a clock ahead of the browser is zero, not negative");
  for (const bad of ["0001-01-01T00:00:00Z", "1970-01-01T00:00:00Z", "", "  ", "not a time", null, undefined, 42]) {
    assert.equal(petDeskElapsedMinutes(bad, deskNow), null, String(bad));
  }
  const i18n = await deskCatalog("zh-CN");
  assert.equal(petDeskElapsedText("2026-11-26T00:30:00Z", deskNow, "petWorkbench.desk.waited", i18n.t), "等了 1 小时 30 分钟");
  assert.equal(petDeskElapsedText("2026-11-24T00:00:00Z", deskNow, "petWorkbench.desk.ran", i18n.t), "已跑 2 天 2 小时");
  assert.equal(petDeskElapsedText("0001-01-01T00:00:00Z", deskNow, "petWorkbench.desk.waited", i18n.t), "");
});

test("desk circled numerals start at one and fall back past fifty", async () => {
  const {petDeskIndexLabel} = await import("../build/pet_desk.js");
  assert.deepEqual([1, 2, 20, 21, 35, 36, 50].map(petDeskIndexLabel), ["①", "②", "⑳", "㉑", "㉟", "㊱", "㊿"]);
  assert.equal(petDeskIndexLabel(51), "(51)");
});

test("desk status row uses the *_total counts and flags degraded channels", async () => {
  const i18n = await deskCatalog("zh-CN");
  const desk = deskFixture();
  let html = renderDesk(deskOptions(i18n, desk));
  const status = html.match(/<section class="pet-desk-status"[\s\S]*?<\/section>/)?.[0] ?? "";
  assert.match(status, /pet-inspection-health ok">正常/);
  assert.match(status, /7 件要你定/, "needs count comes from needs_owner_total, not the 5 rows");
  assert.match(status, /3 件等别人/);
  assert.match(status, /4 个在跑/, "running count comes from running_total, not the 2 rows");
  assert.match(status, /巡检 at 2026-11-26T01:40:00Z/);
  assert.match(status, /title="版本: 1\.2\.3 · 运行时长: 1 小时 30 分钟"/, "version and uptime move to the hover hint");
  assert.doesNotMatch(status, /data-pet-overview-jump="channels"/, "no channel marker without degraded channels");
  html = renderDesk(deskOptions(i18n, desk, {channels: [overviewChannel, {...overviewChannel, id: "c-2", degraded: false}]}));
  assert.match(html, /<button type="button" class="pet-desk-channel-alert" data-pet-overview-jump="channels">1 个渠道异常<\/button>/);
});

test("desk needs list keeps the server order, numbers from ① and labels every kind", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk(deskOptions(i18n, deskFixture()));
  const needs = section(html, "pet-desk-needs");
  assert.match(needs, /<h3 tabindex="-1">要你定（7）<\/h3>/);
  const order = [...needs.matchAll(/<li class="pet-desk-item kind-(\w+)"(?: data-pet-desk-approval="[^"]*")?>\s*<span class="pet-desk-index" aria-hidden="true">(.)<\/span>/g)].map(match => `${match[2]}${match[1]}`);
  assert.deepEqual(order, ["①blocked", "②approval", "③question", "④failed", "⑤stopped"], "rows keep the response order");
  assert.match(needs, /被阻塞 · <span data-i18n-ignore>task-790<\/span> · 等了 1 小时 30 分钟/);
  assert.match(needs, /审批 · <span data-i18n-ignore>task-801<\/span> · 高风险 · 等了 1 小时<\/small>/, "a whole hour drops the zero minutes");
  assert.match(needs, /有 2 个问题等你 · <span data-i18n-ignore>task-813<\/span> · 等了 45 分钟/);
  assert.match(needs, /<ul class="pet-desk-questions"><li data-i18n-ignore>Step one only\?<\/li><li data-i18n-ignore>Plan or build\?<\/li><\/ul>/);
  assert.match(needs, /失败 · <span data-i18n-ignore>task-1<\/span> · 等了 40 分钟/);
  // A zero `since` shows no duration at all instead of "739,000 days".
  assert.match(needs, /上一轮没跑完 · <span data-i18n-ignore>task-2<\/span><\/small>/);
  assert.match(needs, /<div class="field-help pet-desk-more">还有 2 条未显示<\/div>/, "truncation uses needs_owner_total");
});

test("desk rows open tasks and approval details, and approvals carry inline approve and reject", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk(deskOptions(i18n, deskFixture()));
  const needs = section(html, "pet-desk-needs");
  assert.match(needs, /class="pet-desk-item-main" data-pet-overview-task="t-790"/);
  assert.match(needs, /class="pet-desk-item-main" data-pet-approval-open="a-801"/, "an approval opens its detail");
  // task-813 round 4: approve and reject sit on the row; the title still
  // opens the detail. a-801 is high risk, so approve asks once more.
  assert.match(needs, /<button type="button" class="pet-desk-approve" data-pet-desk-approve="a-801" data-pet-desk-confirm data-i18n-ignore aria-label="批准：Merge task-801\?" data-pet-desk-title="Merge task-801\?">批准<\/button><button type="button" class="pet-desk-reject" data-pet-desk-reject="a-801" data-i18n-ignore aria-label="否决：Merge task-801\?" data-pet-desk-title="Merge task-801\?">否决<\/button><button type="button" data-pet-overview-task="t-801">打开任务<\/button>/);
  assert.match(needs, /<p class="pet-desk-decision-status" data-pet-desk-status="a-801" role="status" aria-live="polite"><\/p>/);
  assert.match(needs, /data-pet-overview-task="t-813"/);
  const later = html.match(/<details class="pet-desk-later"[\s\S]*?<\/details>/)?.[0] ?? "";
  assert.match(later, /data-pet-approval-open="idea-1"/, "an idea opens the approval detail");
  assert.match(later, /data-pet-overview-task="t-3"/);
  assert.match(later, /data-pet-desk-approve="idea-1"(?! data-pet-desk-confirm)/, "an idea is approved with one click");
  assert.doesNotMatch(html, /data-pet-approval-decision|data-pet-approval-resolve/, "the detail dialog's controls are not on the desk");
  assert.match(section(html, "pet-desk-waiting"), /data-pet-overview-jump="channels"/, "a channel row jumps to the channel cards");
  assert.match(section(html, "pet-desk-running"), /data-pet-overview-task="t-811"/);
  assert.match(section(html, "pet-desk-pet"), /data-pet-overview-jump="records"/);
});

test("desk later list folds by default with its total and keeps its order", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk(deskOptions(i18n, deskFixture()));
  const later = html.match(/<details class="pet-desk-later"[^>]*>[\s\S]*?<\/details>/)?.[0] ?? "";
  assert.match(later, /^<details class="pet-desk-later" id="pet-desk-later"><summary>不急的还有 12 件<\/summary>/, "collapsed, titled with later_total");
  assert.ok(later.indexOf("idea-1") < later.indexOf("t-3"), "server order");
  assert.match(later, /宠物的想法 · 等了 10 分钟/);
  assert.match(later, /等你下一句话 · <span data-i18n-ignore>task-3<\/span>/);
  assert.match(later, /<p class="pet-desk-detail" tabindex="0">下一步：<span data-i18n-ignore title="Wait for review notes">Wait for review notes<\/span><\/p>/);
  assert.doesNotMatch(later, /pet-desk-index/, "later rows are not numbered");
  assert.match(later, /还有 10 条未显示/);
});

test("desk waiting and running columns label every kind and show elapsed time and hardware", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk(deskOptions(i18n, deskFixture()));
  const waiting = section(html, "pet-desk-waiting");
  assert.match(waiting, /<h3 tabindex="-1">等别人（3）<\/h3>/);
  assert.match(waiting, /<strong><span data-i18n-ignore>task-805<\/span> · <span data-i18n-ignore>Reviewed work<\/span><\/strong><small>评审中 → <span data-i18n-ignore>task-806<\/span> · 等了 45 分钟/);
  assert.match(waiting, /<small>审批执行中 · 等了 2 分钟<\/small>/);
  assert.match(waiting, /Feishu group A<\/span><\/strong><small>3 条待发 · 最早一条等了 4 分钟<\/small>/);
  assert.doesNotMatch(waiting, /pet-desk-more/, "not truncated when the total equals the rows");
  const running = section(html, "pet-desk-running");
  assert.match(running, /<h3 tabindex="-1">在跑什么（4）<\/h3>/);
  assert.match(running, /task-811<\/span> · <span data-i18n-ignore>Overnight comparison<\/span><\/strong><small>[^<]* · 已跑 12 分钟<\/small>/);
  assert.match(running, /<p class="pet-desk-detail" tabindex="0"><span data-i18n-ignore title="comparing builds">comparing builds<\/span><\/p>/);
  assert.match(running, /<p class="pet-desk-hardware">硬件：<span data-i18n-ignore>Debug board 1<\/span><\/p>/);
  const preparing = running.match(/data-pet-overview-task="t-812">[\s\S]*?<\/small>/)?.[0] ?? "";
  assert.match(preparing, /Preparing task/);
  assert.doesNotMatch(preparing, /已跑/, "an invalid since shows no duration");
  assert.match(running, /还有 2 条未显示/);
});

test("desk pet side lists corrections, loops and agenda with totals", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk(deskOptions(i18n, deskFixture()));
  const pet = section(html, "pet-desk-pet");
  assert.match(pet, /<h4>你最近的纠正（1）<\/h4><ul class="pet-desk-ledger"><li><strong><span data-i18n-ignore>Delete workspace X<\/span><\/strong><span class="pet-desk-quote"><span data-i18n-ignore>Keep it for comparison<\/span><\/span><\/li><\/ul>/);
  assert.match(pet, /<h4>未闭环 5 条<\/h4>[\s\S]*loop three[\s\S]*还有 2 条未显示/);
  assert.match(pet, /<h4>议程 3 条<\/h4>[\s\S]*agenda two[\s\S]*还有 1 条未显示/);
  assert.match(pet, /<button type="button" data-pet-overview-jump="records">到记录页看全部<\/button>/);
});

test("desk layout replaces the cards and recent activity but keeps the follow lists", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk(deskOptions(i18n, deskFixture()));
  const order = ["pet-desk-status", "pet-desk-board", "pet-desk-col-main", "pet-desk-needs", "pet-desk-col-side", "pet-desk-waiting", "pet-desk-pet", "pet-desk-col-run", "pet-desk-running", "pet-desk-insights", "pet-overview-follows", "pet-overview-ignored", "pet-overview-unfollowed"]
    .map(marker => html.indexOf(marker));
  assert.ok(order.every(index => index >= 0), `every block renders: ${order}`);
  assert.deepEqual([...order].sort((a, b) => a - b), order, "top-to-bottom order");
  assert.doesNotMatch(html, /pet-overview-cards|pet-overview-card\b|pet-overview-recent|pet-overview-activity/);
  assert.match(html, /id="pet-overview-follows-title">关注任务（1）<\/h3>/);
  assert.doesNotMatch(html, /<details class="pet-overview/);
  assert.match(html, /data-pet-watch-ignore="false" data-pet-watch-task="t-i"/, "follow actions are unchanged");
});

test("every empty desk block says so, and an empty needs list says nothing waits on the owner", async () => {
  const i18n = await deskCatalog("zh-CN");
  const empty = {needs_owner: [], needs_owner_total: 0, later: [], later_total: 0, waiting_others: [], waiting_others_total: 0, running: [], running_total: 0, corrections: [], open_loops: [], open_loops_total: 0, agenda: [], agenda_total: 0};
  const html = renderDesk(deskOptions(i18n, empty));
  assert.match(section(html, "pet-desk-needs"), /<div class="field-help pet-desk-empty">现在没有事等你定。<\/div>/);
  assert.match(html, /<summary>不急的还有 0 件<\/summary><div class="pet-desk-later-body"><div class="field-help pet-desk-empty">没有不急的事。<\/div><\/div>/);
  assert.match(section(html, "pet-desk-waiting"), /没有在等别人的事。/);
  assert.match(section(html, "pet-desk-running"), /现在没有任务在跑。/);
  assert.match(section(html, "pet-desk-pet"), /宠物这边没有未闭环的事。/);
  // Sparse or partial payloads (older field sets) must not throw or print undefined.
  const sparse = renderDesk(deskOptions(i18n, {needs_owner: [{kind: "question", title: "x"}]}));
  assert.doesNotMatch(sparse, /undefined|NaN/);
  assert.match(sparse, /要你定（1）/);
});

test("unknown desk kinds and missing fields fall back to readable labels", async () => {
  const i18n = await deskCatalog("en-US");
  try {
    const desk = {
      needs_owner: [{kind: "brand_new", title: "", since: ""}, {title: "No kind"}],
      needs_owner_total: 2,
      later: [{kind: "mystery", title: "Later thing", since: ""}],
      later_total: 1,
      waiting_others: [{kind: "elsewhere", title: "", since: ""}],
      waiting_others_total: 1,
      running: [{task_id: "t-r", title: "", status: "", since: ""}],
      running_total: 1,
      corrections: [{title: "", owner_note: "note"}],
      open_loops: [], open_loops_total: 0, agenda: [], agenda_total: 0,
    };
    const html = renderDesk(deskOptions(i18n, desk));
    assert.doesNotMatch(html, /undefined|null|NaN|petWorkbench\.desk\./, "no raw keys or undefined");
    assert.match(html, /<li class="pet-desk-item kind-brand_new">[\s\S]*?<strong>\(untitled\)<\/strong><small>Needs you<\/small>/);
    assert.match(html, /<li class="pet-desk-item kind-other">[\s\S]*?No kind<\/span><\/strong><small>Needs you<\/small>/);
    assert.match(html, /Later thing<\/span><\/strong><small>Can wait<\/small>/);
    assert.match(html, /<strong>\(untitled\)<\/strong><small>In progress<\/small>/);
    assert.match(html, /data-pet-overview-task="t-r"><strong><span data-i18n-ignore>t-r<\/span><\/strong>/, "a running row without title falls back to the task id");
    assert.match(html, /<li><strong>\(untitled\)<\/strong><span class="pet-desk-quote"><span data-i18n-ignore>note<\/span>/);
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("desk escapes every server string", async () => {
  const i18n = await deskCatalog("zh-CN");
  const evil = `<img src=x onerror="alert(1)">&'`;
  const escaped = "&lt;img src=x onerror=&quot;alert(1)&quot;&gt;&amp;&#039;";
  const desk = {
    needs_owner: [
      {kind: "approval", approval_id: `a"><script>`, risk: evil, task_id: `t"><b>`, task_code: evil, title: evil, detail: evil, since: ""},
      {kind: "question", task_id: "t", title: "q", questions: [evil], since: ""},
    ],
    needs_owner_total: 2,
    later: [{kind: evil, title: evil, detail: evil, since: ""}], later_total: 1,
    waiting_others: [{kind: "review", task_id: evil, task_code: evil, title: evil, on: evil, since: ""}, {kind: "channel", title: evil, on: evil, count: 1, since: ""}],
    waiting_others_total: 2,
    running: [{task_id: evil, task_code: evil, title: evil, status: evil, detail: evil, hardware: [evil], since: ""}], running_total: 1,
    corrections: [{title: evil, owner_note: evil}],
    open_loops: [evil], open_loops_total: 1, agenda: [evil], agenda_total: 1,
  };
  const html = renderDesk(deskOptions(i18n, desk));
  assert.doesNotMatch(html, /<img|<script|<b>|onerror="/);
  assert.ok(html.split(escaped).length - 1 >= 12, "server strings are escaped in every block");
  assert.match(html, /data-pet-approval-open="a&quot;&gt;&lt;script&gt;"/);
  assert.match(html, /class="pet-desk-item kind-other"/, "an unexpected kind cannot inject a class");
});

test("without a desk the overview falls back to the pre-desk layout", async () => {
  const i18n = await deskCatalog("zh-CN");
  for (const desk of [undefined, null]) {
    const html = renderPetOverview({...deskOptions(i18n, desk), approvals: [{id: "a-1", title: "Delete", status: "pending", kind: "delete_task", created_at: "2026-11-26T00:00:00Z"}]});
    assert.match(html, /class="pet-overview-cards"/);
    assert.match(html, /class="pet-overview-recent"/);
    assert.match(html, /data-pet-approval-open="a-1"/);
    assert.doesNotMatch(html, /pet-desk/);
  }
});

test("the English desk has no Chinese interface copy", async () => {
  const i18n = await deskCatalog("en-US");
  try {
    const html = renderDesk(deskOptions(i18n, deskFixture(), {channels: [overviewChannel]}));
    // Server content is marked data-i18n-ignore; drop it, then no CJK may remain.
    const chrome = html.replace(/<(\w+)[^>]*data-i18n-ignore[^>]*>[\s\S]*?<\/\1>/g, "");
    assert.doesNotMatch(chrome, /[一-鿿]/, chrome.match(/.{0,40}[一-鿿].{0,40}/)?.[0]);
    assert.match(html, /For you \(7\)/);
    assert.match(html, /<summary>12 more that can wait<\/summary>/);
    assert.match(html, /Questions for you: 2/);
    assert.match(html, /waited 1 h 30 min/);
    assert.match(html, /running for 12 min/);
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("every desk catalog key exists in both locales", async () => {
  const source = await readFile(resolve(root, "web/src/i18n.ts"), "utf8");
  const [zhBlock, enBlock] = [source.slice(0, source.indexOf("const enUS")), source.slice(source.indexOf("const enUS"), source.indexOf("const legacyEnglish"))];
  const keys = block => new Set([...block.matchAll(/"(petWorkbench\.(?:desk|insights)\.[^"]+|petWorkbench\.wait[A-Za-z]+)":/g)].map(match => match[1]));
  const zh = keys(zhBlock);
  const en = keys(enBlock);
  assert.ok(zh.size >= 40, `desk keys: ${zh.size}`);
  assert.deepEqual([...zh].sort(), [...en].sort());
  const renderer = await readFile(resolve(root, "web/src/pet_desk.ts"), "utf8");
  for (const [, key] of renderer.matchAll(/"(petWorkbench\.(?:desk|insights)\.[A-Za-z_.]+)"/g)) assert.ok(zh.has(key), `missing catalog key ${key}`);
  for (const key of ["petWorkbench.waitHoursOnly", "petWorkbench.waitDaysOnly", "petWorkbench.insights.tokensWindow", "petWorkbench.insights.tokensCumulative"]) assert.ok(zh.has(key) && en.has(key), key);
  for (const list of ["need", "later"]) for (const kind of ["other", ...(list === "need" ? ["approval", "question", "blocked", "failed", "stopped"] : ["idea", "next_step"])]) {
    assert.ok(zh.has(`petWorkbench.desk.${list}.${kind}`), `${list}.${kind}`);
  }
});

test("the workbench passes the desk and a fresh clock to the overview", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  assert.match(main, /desk: view\.desk,\n\s*now: Date\.now\(\),/);
  assert.match(main, /jump === "records"/);
  assert.match(main, /openOverviewApproval\(button\.dataset\.petApprovalOpen \|\| ""\)/);
  const types = await readFile(resolve(root, "web/src/types.ts"), "utf8");
  assert.match(types, /desk\?: PetDesk;/);
});

/* Step two (task-813, design doc section 11) ----------------------------- */

test("durations drop the zero part on whole hours and days, in both languages", async () => {
  const {petApprovalDurationText} = await import("../build/pet_approvals.js");
  const i18n = await deskCatalog("zh-CN");
  try {
    const zh = minutes => petApprovalDurationText(minutes, i18n.t);
    assert.deepEqual([0, 45, 60, 61, 120, 1439, 1440, 1500, 2880, 2940].map(zh),
      ["0 分钟", "45 分钟", "1 小时", "1 小时 1 分钟", "2 小时", "23 小时 59 分钟", "1 天", "1 天 1 小时", "2 天", "2 天 1 小时"]);
    i18n.applyLocale("en-US");
    const en = minutes => petApprovalDurationText(minutes, i18n.t);
    assert.deepEqual([0, 60, 90, 1440, 1500].map(en), ["0 min", "1 h", "1 h 30 min", "1 d", "1 d 1 h"]);
    // The approvals tab uses the same helper.
    const {renderPetApprovalList} = await import("../build/pet_approvals.js");
    const html = renderPetApprovalList([{id: "a", kind: "delete_task", status: "pending", title: "x", created_at: "2026-01-01T00:00:00Z"}], {translate: i18n.t, now: Date.parse("2026-01-01T02:00:00Z")});
    assert.match(html, /Waiting 2 h|waited 2 h|2 h(?! \d)/);
    assert.doesNotMatch(html, /2 h 0 min/);
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("a desk that is an array is treated as no desk", async () => {
  const {petDeskPresent} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("zh-CN");
  for (const desk of [[], [deskFixture()], "desk", 1]) {
    assert.equal(petDeskPresent(desk), false, JSON.stringify(desk).slice(0, 20));
    const html = renderDesk(deskOptions(i18n, desk));
    assert.match(html, /class="pet-overview-cards"/, "legacy overview");
    assert.doesNotMatch(html, /pet-desk/);
  }
  assert.equal(petDeskPresent(deskFixture()), true);
  // Rows that are arrays are not records either.
  const html = renderDesk(deskOptions(i18n, {...deskFixture(), needs_owner: [["approval"], {kind: "blocked", task_id: "t", title: "real", since: ""}], needs_owner_total: 2, later: [], later_total: 0}));
  const numbered = section(html, "pet-desk-needs").match(/<span class="pet-desk-index"[^>]*>(.)<\/span>/g) || [];
  assert.deepEqual(numbered.length, 1, "only the object row renders");
  assert.match(section(html, "pet-desk-needs"), /real<\/span>/);
});

test("loop waits from the pet's ledgers show the item, next step and source file", async () => {
  const i18n = await deskCatalog("zh-CN");
  const desk = {...deskFixture(), waiting_others: [
    {kind: "loop", title: "等合作方答复 <格式>", detail: "周五再问", on: "core/loops.md", since: "0001-01-01T00:00:00Z"},
    {kind: "loop", title: "等渠道恢复", on: "core/agenda.md", since: "0001-01-01T00:00:00Z"},
  ], waiting_others_total: 2};
  const waiting = section(renderDesk(deskOptions(i18n, desk)), "pet-desk-waiting");
  assert.match(waiting, /<li class="pet-desk-item kind-loop"><div class="pet-desk-item-body"><button type="button" class="pet-desk-item-main" data-pet-overview-jump="records"><strong><span data-i18n-ignore>等合作方答复 &lt;格式&gt;<\/span><\/strong><small>宠物记下的等待 · <span data-i18n-ignore>core\/loops\.md<\/span><\/small><\/button><p class="pet-desk-detail" tabindex="0">下一步：<span data-i18n-ignore title="周五再问">周五再问<\/span><\/p>/);
  assert.match(waiting, /等渠道恢复<\/span><\/strong><small>宠物记下的等待 · <span data-i18n-ignore>core\/agenda\.md<\/span><\/small><\/button><\/div><\/li>/, "no next step, no detail line");
  assert.doesNotMatch(waiting, /等了/, "loop waits have no time");
});

test("the last reply line shows the wake reply, NOOP copy, or the latest reply", async () => {
  const i18n = await deskCatalog("zh-CN");
  const render = reply => renderDesk(deskOptions(i18n, {...deskFixture(), last_reply: reply}));
  const line = html => html.match(/<section class="pet-desk-reply"[\s\S]*?<\/section>/)?.[0] ?? "";
  let html = render({text: "推进了 <b>task-1</b>", finished_at: "2026-11-26T01:30:00Z", noop: false, from_wake: true});
  assert.match(line(html), /<strong>宠物上次巡检说<\/strong><span class="pet-desk-reply-text" data-i18n-ignore>推进了 &lt;b&gt;task-1&lt;\/b&gt;<\/span><small>at 2026-11-26T01:30:00Z<\/small>/);
  assert.ok(html.indexOf("pet-desk-reply") > html.indexOf("pet-desk-status") && html.indexOf("pet-desk-reply") < html.indexOf("pet-desk-needs"), "between the status row and for-you");
  html = render({text: "NOOP · 没有变化", finished_at: "2026-11-26T01:30:00Z", noop: true, from_wake: true});
  assert.match(line(html), /<span class="pet-desk-reply-noop">上次巡检没有要推进的事<\/span>/);
  assert.doesNotMatch(line(html), /没有变化/);
  html = render({text: "好的，我来看看", finished_at: "2026-11-26T01:30:00Z", noop: false, from_wake: false});
  assert.match(line(html), /<strong>上一轮回复<\/strong>/);
  html = render({text: "x", finished_at: "0001-01-01T00:00:00Z", noop: false, from_wake: true});
  assert.doesNotMatch(line(html), /<small>/, "zero time shows no time");
  for (const reply of [undefined, null, [], {text: "", noop: false}]) {
    assert.doesNotMatch(render(reply), /pet-desk-reply/, `no line for ${JSON.stringify(reply)}`);
  }
});

test("a long last reply shows a marker-free one-line preview and the original text behind an expand button", async () => {
  const {petDeskReplyPreview} = await import("../build/pet_desk.js");
  assert.equal(petDeskReplyPreview("它的 commit `061d1cf` 就是 **task-813**\n\n另外 __watch__ 了"), "它的 commit 061d1cf 就是 task-813 另外 watch 了");
  const i18n = await deskCatalog("zh-CN");
  const text = `把 **0.5.577** 的来源查清了 <b>\`061d1cf\`</b>\n\n${"后面还有很多字。".repeat(40)}`;
  const html = renderDesk(deskOptions(i18n, {...deskFixture(), last_reply: {text, finished_at: "2026-11-26T01:30:00Z", noop: false, from_wake: true}}));
  const line = html.match(/<section class="pet-desk-reply"[\s\S]*?<\/section>/)?.[0] ?? "";
  assert.match(line, /<span class="pet-desk-reply-text" data-i18n-ignore>把 0\.5\.577 的来源查清了 &lt;b&gt;061d1cf&lt;\/b&gt; 后面/, "preview drops the markers and escapes");
  assert.match(line, /<button type="button" class="pet-desk-reply-toggle" data-pet-desk-reply-toggle aria-expanded="false" aria-controls="pet-desk-reply-full">展开<\/button>/);
  assert.match(line, /<div class="pet-desk-reply-full" id="pet-desk-reply-full" tabindex="0" data-i18n-ignore hidden>把 \*\*0\.5\.577\*\* 的来源查清了 &lt;b&gt;`061d1cf`&lt;\/b&gt;\n\n后面/, "full text is the original, escaped");
});

test("the last 7 days block renders counts, outcomes and the token scope", async () => {
  const {renderPetInsightsBody, renderPetInsightsPlaceholder} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("zh-CN");
  const options = {translate: i18n.t, formatTime: value => `at ${value}`};
  const insights = {
    from: "2026-11-18T16:00:00Z", to: "2026-11-26T02:00:00Z", actions: 12,
    approvals: {total: 5, approved: 2, rejected: 1, cancelled: 1, pending: 1, ideas: 3},
    corrections: 2, wakes: {turns: 40, noop: 31, tokens: 1234567, token_scope: "window"},
  };
  let html = renderPetInsightsBody(insights, options);
  assert.match(html, /<h3>近 7 天<\/h3>/);
  // Only the first day, in local time, and "today" for the end.
  const {petInsightsDate} = await import("../build/pet_desk.js");
  const fromLocal = new Date("2026-11-18T16:00:00Z");
  const expected = `${fromLocal.getFullYear()}-${String(fromLocal.getMonth() + 1).padStart(2, "0")}-${String(fromLocal.getDate()).padStart(2, "0")}`;
  assert.equal(petInsightsDate("2026-11-18T16:00:00Z"), expected);
  assert.match(html, new RegExp(`<small class="pet-desk-insights-range">${expected} 至今天</small>`));
  assert.doesNotMatch(html, /\d\d:\d\d/, "no clock time in the range");
  for (const bad of ["", "nope", "0001-01-01T00:00:00Z", null, 3]) assert.equal(petInsightsDate(bad), "", String(bad));
  assert.match(html, /<strong>宠物自己办了 12 件<\/strong>/);
  assert.match(html, /<strong>提给你 5 件<\/strong><small>已批准 2 · 已否决 1 · 已取消 1 · 还在等 1<\/small>/);
  assert.match(html, /想法 3 条/);
  assert.match(html, /你纠正了 2 次/);
  assert.match(html, /宠物被唤醒 40 次，其中 31 次没有要推进的事<\/strong><small>这 7 天用了 1,234,567 Token<\/small>/);
  html = renderPetInsightsBody({...insights, wakes: {...insights.wakes, token_scope: "cumulative"}}, options);
  assert.match(html, /宠物任务累计用了 1,234,567 Token（这 7 天的用量拆不出来）/);
  assert.doesNotMatch(html, /这 7 天用了/);
  for (const failed of [null, undefined, [], "x"]) {
    assert.match(renderPetInsightsBody(failed, options), /暂时读不到近 7 天的数据。/);
  }
  // Partial payloads never print undefined or NaN.
  assert.doesNotMatch(renderPetInsightsBody({}, options), /undefined|NaN/);
  assert.match(renderPetInsightsPlaceholder(i18n.t), /data-pet-insights/);
  // The desk renders the placeholder under the pet's side.
  const desk = renderDesk(deskOptions(i18n, deskFixture()));
  assert.ok(desk.indexOf('id="pet-desk-insights"') > desk.indexOf('id="pet-desk-pet"') && desk.indexOf('id="pet-desk-insights"') < desk.indexOf("pet-desk-follows"));
});

test("the English insights, loop waits and last reply have no Chinese interface copy", async () => {
  const {renderPetInsightsBody} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("en-US");
  try {
    const chrome = html => html.replace(/<(\w+)[^>]*data-i18n-ignore[^>]*>[\s\S]*?<\/\1>/g, "");
    const body = renderPetInsightsBody({from: "a", to: "b", actions: 1, approvals: {total: 1, approved: 1, rejected: 0, cancelled: 0, pending: 0, ideas: 0}, corrections: 0, wakes: {turns: 2, noop: 1, tokens: 5, token_scope: "cumulative"}}, {translate: i18n.t, formatTime: v => v});
    assert.doesNotMatch(body, /[一-鿿]/);
    assert.match(body, /Tokens for the whole pet task: 5/);
    const english = renderPetInsightsBody({from: "2026-11-18T16:00:00Z", to: "2026-11-26T02:00:00Z", actions: 0, approvals: {}, corrections: 0, wakes: {}}, {translate: i18n.t, formatTime: v => v});
    assert.match(english, /\d{4}-\d\d-\d\d to today<\/small>/);
    assert.doesNotMatch(renderPetInsightsBody(null, {translate: i18n.t, formatTime: v => v}), /[一-鿿]/);
    const html = renderDesk(deskOptions(i18n, {...deskFixture(), waiting_others: [{kind: "loop", title: "等", detail: "下", on: "core/loops.md", since: ""}], waiting_others_total: 1, last_reply: {text: "NOOP", noop: true, from_wake: true, finished_at: "2026-11-26T01:00:00Z"}}));
    assert.doesNotMatch(chrome(html), /[一-鿿]/, chrome(html).match(/.{0,40}[一-鿿].{0,40}/)?.[0]);
    assert.match(html, /Nothing to move at the last check/);
    assert.match(html, /Waiting \(pet&#039;s notes\)/);
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("the workbench loads the desk chunk lazily, fills insights per paint and reads one approval by id", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const api = await readFile(resolve(root, "web/src/api.ts"), "utf8");
  const workbench = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
  assert.match(main, /createLazyPage<PetDeskModule>\("pet-desk", \(\) => import\("\.\/pet_desk\.js"\)/);
  assert.doesNotMatch(main, /from "\.\/pet_desk\.js"/, "no static import of the desk chunk");
  assert.match(main, /chunkErrorHTML\(petDeskPage\.key, t\("petWorkbench\.desk\.chunkFailed"\)/, "a failed chunk is retryable");
  assert.match(main, /if \(!host\.isConnected \|\| petWorkbenchPageKeeper\.binding !== binding\) return;/);
  assert.match(main, /insightsController\?\.abort\(\);/);
  assert.match(main, /api\.petApproval\(approvalID, AbortSignal\.timeout\(PET_WORKBENCH_TIMEOUT_MS\)\)/);
  assert.doesNotMatch(main, /approvalLimit: PET_APPROVAL_MAX_LIMIT, worklogLimit: 1/, "the whole-page re-read is gone");
  assert.match(api, /`\/api\/v1\/pet\/workbench\/insights\$\{options\.records \? "\?records=1" : ""\}`/);
  assert.match(api, /`\/api\/v1\/pet\/approvals\/\$\{encodeURIComponent\(id\)\}`/);
  for (const moved of ["renderPetOverview", "renderPetRecords", "renderPetDesk", "bindPetDocEditorButtons"]) {
    assert.doesNotMatch(workbench, new RegExp(`function ${moved}\\b`), `${moved} left the first-screen module`);
  }
});

/* Step three: records page (task-813, design doc section 11) ------------- */

const recordCore = [
  {name: "loops.md", content: "loops"},
  {name: "identity.md", content: "identity"},
  {name: "agenda.md", content: "agenda"},
  {name: "changes.md", content: "changes"},
  {name: "principles.md", content: "principles"},
];

test("the records page shows five groups in order, each with a one-line intro", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderPetRecords(recordCore, [{date: "2026-11-25", content: "did"}], {translate: i18n.t, emptyCore: "无", emptyWorklog: "无", tocOpen: true});
  // The approval history (the former approvals tab) follows the corrections.
  const ids = ["pet-record-judgment", "pet-record-corrections", "pet-record-approvals", "pet-record-promises", "pet-record-worklog"];
  const positions = ids.map(id => html.indexOf(`<section class="pet-record-section" id="${id}"`));
  assert.ok(positions.every(position => position >= 0), `all groups: ${positions}`);
  assert.deepEqual([...positions].sort((a, b) => a - b), positions, "group order");
  for (const [id, title, intro] of [
    ["pet-record-judgment", "它是谁、怎么判断", "宠物的身份和做事准则"],
    ["pet-record-corrections", "你的纠正", "你否决审批时写的原因"],
    ["pet-record-approvals", "审批记录", "宠物提给你的审批和想法，新的在上，可以按种类和状态筛选。待处理的在概览的“要你定”里。"],
    ["pet-record-promises", "它答应的事", "宠物记下的未闭环事项和自己的议程"],
    ["pet-record-worklog", "它做过的事", "宠物按日期写的工作记录"],
  ]) {
    const group = html.match(new RegExp(`<section class="pet-record-section" id="${id}"[^>]*>[\\s\\S]*?</section>`))?.[0] ?? "";
    assert.match(group, new RegExp(`<h3 id="${id}-title" tabindex="-1">${title}</h3>\\s*<p class="pet-record-intro">${intro}`), id);
  }
  // Files land in their group in a fixed order; ids keep the response index.
  const judgment = html.match(/<section class="pet-record-section" id="pet-record-judgment"[^>]*>[\s\S]*?<\/section>/)[0];
  assert.deepEqual([...judgment.matchAll(/data-pet-doc-name="([^"]+)"/g)].map(match => match[1]), ["identity.md", "principles.md", "changes.md"]);
  const promises = html.match(/<section class="pet-record-section" id="pet-record-promises"[^>]*>[\s\S]*?<\/section>/)[0];
  assert.deepEqual([...promises.matchAll(/data-pet-doc-name="([^"]+)"/g)].map(match => match[1]), ["loops.md", "agenda.md"]);
  assert.match(promises, /id="pet-record-core-0"><summary><span>loops\.md<\/span><span class="pet-record-ledger-slot" data-pet-ledger-counts="loops\.md"><\/span><button type="button" class="pet-doc-edit" data-pet-doc-edit data-pet-doc-section="core" data-pet-doc-name="loops\.md">/);
  assert.match(judgment, /id="pet-record-core-1"><summary><span>identity\.md<\/span><button type="button" class="pet-doc-edit" data-pet-doc-edit/, "judgment files have no counts slot");
  // The contents mirror the groups.
  const toc = html.match(/<nav id="pet-record-toc-nav">([\s\S]*?)<\/nav>/)[1];
  assert.deepEqual([...toc.matchAll(/data-pet-record-goto="([^"]+)"/g)].map(match => match[1]),
    ["pet-record-judgment", "pet-record-core-1", "pet-record-core-4", "pet-record-core-3", "pet-record-corrections", "pet-record-approvals", "pet-record-promises", "pet-record-core-0", "pet-record-core-2", "pet-record-worklog", "pet-record-worklog-0"]);
  // The knowledge line and corrections start as placeholders.
  assert.ok(html.indexOf("data-pet-record-knowledge") < html.indexOf('id="pet-record-judgment"'), "knowledge line on top");
  assert.match(html, /data-pet-record-corrections/);
  // An unknown core file is not dropped.
  assert.match(renderPetRecords([{name: "notes.md", content: "x"}], [], {translate: i18n.t, emptyCore: "无", emptyWorklog: "无"}), /<section class="pet-record-section" id="pet-record-judgment"[^>]*>[\s\S]*notes\.md/);
});

test("corrections show the three states, newest first, and an empty or failed read", async () => {
  const {renderPetCorrectionHistory} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("zh-CN");
  const options = {translate: i18n.t, formatTime: value => `at ${value}`};
  const html = renderPetCorrectionHistory([
    {at: "2026-11-26T01:00:00Z", title: "删除 <X>", owner_note: "留着 <对比>", state: "open"},
    {at: "2026-11-20T01:00:00Z", title: "合并", owner_note: "先评审", state: "absorbed"},
    {at: "2026-11-10T01:00:00Z", title: "", owner_note: "太早", state: "expired"},
    {at: "0001-01-01T00:00:00Z", title: "怪状态", owner_note: "x", state: "weird"},
  ], options);
  const rows = [...html.matchAll(/<li class="pet-record-correction state-(\w+)">[\s\S]*?<\/li>/g)];
  assert.deepEqual(rows.map(row => row[1]), ["open", "absorbed", "expired", "open"], "server order; unknown state shows as open");
  assert.match(rows[0][0], /<strong><span data-i18n-ignore>删除 &lt;X&gt;<\/span><\/strong><span class="pet-record-correction-state">还没消化<\/span><small>at 2026-11-26T01:00:00Z<\/small>/);
  assert.match(rows[0][0], /<p><span data-i18n-ignore>留着 &lt;对比&gt;<\/span><\/p>/);
  assert.match(rows[1][0], /已写进原则/);
  assert.match(rows[2][0], /<strong>（无标题）<\/strong><span class="pet-record-correction-state">已过期<\/span>/);
  assert.doesNotMatch(rows[3][0], /<small>/, "zero time shows no time");
  assert.match(renderPetCorrectionHistory([], options), /你还没有纠正过宠物。/);
  for (const failed of [null, undefined, {}, "x"]) assert.match(renderPetCorrectionHistory(failed, options), /暂时读不到纠正记录。/);
});

test("ledger counts come from the server, largest first", async () => {
  const {renderPetLedgerCounts} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("zh-CN");
  assert.equal(renderPetLedgerCounts({name: "loops.md", counts: {waiting: 1, open: 3, "<x>": 2}, total: 6}, i18n.t),
    '<span class="pet-record-ledger-counts"><span data-i18n-ignore>open</span> 3 · <span data-i18n-ignore>&lt;x&gt;</span> 2 · <span data-i18n-ignore>waiting</span> 1</span>');
  assert.match(renderPetLedgerCounts({name: "agenda.md", counts: {}, total: 0}, i18n.t), /没有条目/);
  assert.equal(renderPetLedgerCounts(undefined, i18n.t), "", "no data, no counts");
});

test("the knowledge line shows totals, pending titles with links, and a failed read", async () => {
  const {renderPetKnowledgeHealth, petKnowledgeEntryHash} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("zh-CN");
  const health = {
    entries: 120, pending: 3, helped: 14, helped_partial: false, truncated: false, failed: 0, projects: 4, elapsed_ms: 5,
    spaces: [], pending_items: [
      {id: "k-1", title: "过时 <A>", scope: "global", space: "global", status: "stale"},
      {id: "k-2", title: "错误 B", scope: "project", project_id: "p 1", space: "AHA2", status: "wrong"},
    ],
  };
  const html = renderPetKnowledgeHealth(health, i18n.t);
  assert.match(html, /<strong>知识库：<\/strong> 120 条 · 待修订 3 条 · 有帮助 14 次/);
  assert.match(html, /<a href="#knowledge\?doc=k-1&amp;scope=global" data-pet-knowledge-link><span data-i18n-ignore>过时 &lt;A&gt;<\/span><\/a><small><span data-i18n-ignore>global<\/span> · 过时<\/small>/);
  assert.match(html, /<a href="#knowledge\?doc=k-2&amp;scope=project&amp;project=p\+1" data-pet-knowledge-link>/);
  assert.equal(petKnowledgeEntryHash({id: "k-2", scope: "project", project_id: "p 1"}), "#knowledge?doc=k-2&scope=project&project=p+1");
  // Vault spaces cannot report helped feedback; the line says so.
  assert.match(renderPetKnowledgeHealth({...health, helped_partial: true}, i18n.t), /有帮助 14 次（新知识库不记这个数，未计入）/);
  assert.match(renderPetKnowledgeHealth({...health, truncated: true, projects: 50}, i18n.t), /只读了前 50 个项目/);
  assert.match(renderPetKnowledgeHealth({...health, failed: 2}, i18n.t), /2 个知识空间读取失败/);
  assert.doesNotMatch(renderPetKnowledgeHealth({...health, pending_items: []}, i18n.t), /<ul/);
  for (const failed of [null, undefined, [], "x"]) assert.match(renderPetKnowledgeHealth(failed, i18n.t), /暂时读不到知识库的情况。/);
  assert.doesNotMatch(renderPetKnowledgeHealth({}, i18n.t), /undefined|NaN/);
});

test("the English records page has no Chinese interface copy", async () => {
  const {renderPetCorrectionHistory, renderPetKnowledgeHealth, renderPetLedgerCounts} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("en-US");
  try {
    const chrome = html => html.replace(/<(\w+)[^>]*data-i18n-ignore[^>]*>[\s\S]*?<\/\1>/g, "");
    const options = {translate: i18n.t, formatTime: v => v};
    const pieces = [
      renderPetRecords(recordCore, [{date: "2026-11-25", content: "内容"}], {translate: i18n.t, emptyCore: i18n.t("petWorkbench.emptyCore"), emptyWorklog: i18n.t("petWorkbench.emptyWorklog")}).replace(/<pre>[\s\S]*?<\/pre>/g, ""),
      renderPetCorrectionHistory([{at: "2026-11-26T01:00:00Z", title: "标题", owner_note: "原因", state: "open"}, {at: "", title: "", owner_note: "x", state: "expired"}], options),
      renderPetCorrectionHistory([], options), renderPetCorrectionHistory(null, options),
      renderPetKnowledgeHealth({entries: 1, pending: 1, helped: 0, helped_partial: true, truncated: true, failed: 1, projects: 50, pending_items: [{id: "k", title: "中文标题", scope: "global", space: "全局", status: "wrong"}]}, i18n.t),
      renderPetKnowledgeHealth(null, i18n.t),
      renderPetLedgerCounts({name: "loops.md", counts: {等待: 1}, total: 1}, i18n.t),
      renderPetLedgerCounts({name: "loops.md", counts: {}, total: 0}, i18n.t),
    ];
    for (const piece of pieces) assert.doesNotMatch(chrome(piece), /[一-鿿]/, chrome(piece).match(/.{0,40}[一-鿿].{0,40}/)?.[0]);
    assert.match(pieces[0], /tabindex="-1">What it promised<\/h3>/);
    assert.match(pieces[0], /tabindex="-1">Approval history<\/h3>/);
    assert.match(pieces[1], /Not absorbed yet[\s\S]*Expired/);
  } finally {
    i18n.applyLocale("zh-CN");
  }
});

test("the records tab reads its data only when opened and keeps the copy in the chunk", async () => {
  const main = await readFile(resolve(root, "web/src/main.ts"), "utf8");
  const i18n = await readFile(resolve(root, "web/src/i18n.ts"), "utf8");
  assert.match(main, /api\.petWorkbenchInsights\(controller\.signal, \{records: true\}\)/);
  assert.match(main, /loadRecordsData\(deskModule\);/);
  assert.match(main, /if \(active !== "records"\) \{\s*recordsController\?\.abort\(\);/);
  assert.match(main, /if \(!knowledgeHost\.isConnected \|\| petWorkbenchPageKeeper\.binding !== binding\) return;/);
  assert.doesNotMatch(i18n, /petWorkbench\.records\./, "records copy is not in the first-screen catalog");
});

/* Round 4, item 5: live overview refresh ---------------------------------- */

// A fake clock: timers run in time order when advance() passes them.
function fakeClock() {
  let now = 0;
  let id = 0;
  const timers = new Map();
  return {
    now: () => now,
    setTimer: (callback, ms) => { const handle = ++id; timers.set(handle, {at: now + ms, callback}); return handle; },
    clearTimer: handle => { timers.delete(handle); },
    async advance(ms) {
      const end = now + ms;
      for (;;) {
        const next = [...timers.entries()].sort((a, b) => a[1].at - b[1].at)[0];
        if (!next || next[1].at > end) break;
        timers.delete(next[0]);
        now = next[1].at;
        next[1].callback();
        await new Promise(resolve => setImmediate(resolve));
      }
      now = end;
      await new Promise(resolve => setImmediate(resolve));
    },
    pending: () => timers.size,
  };
}

test("the overview refresh event list covers task, turn, round and pet approval events", async () => {
  const {petDeskRefreshEvent} = await import("../build/pet_desk.js");
  for (const type of ["task_created", "task_completed", "task_status_changed", "turn_running", "turn_succeeded", "turn_failed", "round_started", "round_interrupted", "pet_approval", "pet_event"]) {
    assert.equal(petDeskRefreshEvent(type), true, type);
  }
  for (const type of ["pet_state", "pet_action", "knowledge_proposal_created", "mail_sent", "agent_usage", "heartbeat", ""]) {
    assert.equal(petDeskRefreshEvent(type), false, type);
  }
});

test("a burst of events refreshes once after the debounce, and reads never stack", async () => {
  const {createPetDeskRefresher, PET_DESK_REFRESH} = await import("../build/pet_desk.js");
  const clock = fakeClock();
  let reads = 0;
  let release;
  const refresher = createPetDeskRefresher({
    active: () => true, tick: () => {}, now: clock.now, setTimer: clock.setTimer, clearTimer: clock.clearTimer,
    refresh: () => { reads++; return new Promise(resolve => { release = resolve; }); },
  });
  refresher.start();
  for (let i = 0; i < 10; i++) { refresher.notify(); await clock.advance(100); }
  assert.equal(reads, 0, "still inside the debounce");
  await clock.advance(PET_DESK_REFRESH.debounceMs);
  assert.equal(reads, 1, "one read for the burst");
  assert.equal(refresher.busy(), true);
  // More events while the read is in flight queue exactly one follow-up.
  refresher.notify(); await clock.advance(PET_DESK_REFRESH.debounceMs);
  refresher.notify(); await clock.advance(PET_DESK_REFRESH.debounceMs);
  assert.equal(reads, 1, "no second read while one is in flight");
  release();
  await clock.advance(0);
  // The follow-up waits out the minimum interval after the first read began.
  await clock.advance(PET_DESK_REFRESH.minIntervalMs);
  assert.equal(reads, 2, "one follow-up after the read settles");
  release();
  await clock.advance(PET_DESK_REFRESH.minIntervalMs * 3);
  assert.equal(reads, 2, "nothing more without new events");
  refresher.stop();
});

test("two refreshes keep the minimum interval, wake skips the debounce", async () => {
  const {createPetDeskRefresher, PET_DESK_REFRESH} = await import("../build/pet_desk.js");
  const clock = fakeClock();
  const starts = [];
  const refresher = createPetDeskRefresher({
    active: () => true, tick: () => {}, now: clock.now, setTimer: clock.setTimer, clearTimer: clock.clearTimer,
    refresh: async () => { starts.push(clock.now()); },
  });
  refresher.start();
  refresher.wake();
  await clock.advance(0);
  assert.deepEqual(starts, [0], "wake refreshes immediately");
  refresher.wake();
  await clock.advance(100);
  assert.deepEqual(starts, [0], "a second wake waits for the interval");
  await clock.advance(PET_DESK_REFRESH.minIntervalMs);
  assert.deepEqual(starts, [0, PET_DESK_REFRESH.minIntervalMs]);
  refresher.stop();
});

test("the fallback poll and the elapsed tick run only while active, and stop clears them", async () => {
  const {createPetDeskRefresher, PET_DESK_REFRESH} = await import("../build/pet_desk.js");
  const clock = fakeClock();
  let reads = 0, ticks = 0, live = true;
  const refresher = createPetDeskRefresher({
    active: () => live, now: clock.now, setTimer: clock.setTimer, clearTimer: clock.clearTimer,
    refresh: async () => { reads++; }, tick: () => { ticks++; },
  });
  refresher.start();
  await clock.advance(PET_DESK_REFRESH.pollMs);
  assert.equal(reads, 1, "a quiet minute polls once");
  assert.equal(ticks, 2, "elapsed times tick every 30 seconds without a read");
  await clock.advance(PET_DESK_REFRESH.pollMs);
  assert.equal(reads, 2);
  // Hidden page or another tab: no reads, no ticks.
  live = false;
  await clock.advance(PET_DESK_REFRESH.pollMs * 3);
  assert.equal(reads, 2, "no polling while inactive");
  assert.equal(ticks, 4, "no ticks while inactive");
  live = true;
  refresher.stop();
  assert.equal(clock.pending(), 0, "stop clears every timer");
  refresher.notify();
  await clock.advance(PET_DESK_REFRESH.pollMs);
  assert.equal(reads, 2, "a stopped refresher ignores events");
});

test("elapsed labels tick in place without a new read", async () => {
  const {tickPetDeskElapsed, petDeskClock, paintPetDeskUpdated} = await import("../build/pet_desk.js");
  const i18n = await deskCatalog("zh-CN");
  const html = renderPetOverview(deskOptions(i18n, deskFixture()));
  assert.match(html, /<span class="pet-desk-elapsed" data-pet-desk-since="2026-11-26T00:30:00Z" data-pet-desk-elapsed-key="petWorkbench\.desk\.waited">等了 1 小时 30 分钟<\/span>/);
  // A minimal DOM stand-in: the tick only reads data attributes and writes text.
  const node = {dataset: {petDeskSince: "2026-11-26T00:30:00Z", petDeskElapsedKey: "petWorkbench.desk.waited"}, textContent: "等了 1 小时 30 分钟"};
  const zero = {dataset: {petDeskSince: "0001-01-01T00:00:00Z", petDeskElapsedKey: "petWorkbench.desk.waited"}, textContent: "x"};
  const root = {querySelectorAll: () => [node, zero]};
  assert.equal(tickPetDeskElapsed(root, deskNow + 30 * 60_000, i18n.t), 1);
  assert.equal(node.textContent, "等了 2 小时");
  assert.equal(zero.textContent, "x", "an unknown time is left alone");
  assert.equal(petDeskClock(new Date(2026, 10, 26, 9, 5, 7).getTime()), "09:05:07");
  const stamp = {textContent: "", classList: {on: false, toggle(_name, value) { this.on = value; }}};
  paintPetDeskUpdated({querySelector: () => stamp}, new Date(2026, 10, 26, 9, 5, 7).getTime(), false);
  assert.equal(stamp.textContent, "更新于 09:05:07");
  paintPetDeskUpdated({querySelector: () => stamp}, new Date(2026, 10, 26, 9, 5, 7).getTime(), true);
  assert.match(stamp.textContent, /刷新失败，稍后会再试；显示的是 09:05:07 的数据/);
  assert.equal(stamp.classList.on, true);
});

/* Round 4, item 3: inline approve / reject -------------------------------- */

test("high-risk and delete approvals need a second click; everything else is one click", async () => {
  const {petDeskApprovalNeedsConfirm, PET_DESK_CONFIRM_MS} = await import("../build/pet_desk.js");
  const confirm = (approval_kind, risk) => petDeskApprovalNeedsConfirm({approval_kind, risk});
  // The server's background deletes, other delete_* kinds, and high risk.
  for (const kind of ["delete_task", "delete_project", "delete_workspace", "delete_model", "delete_models", "delete_skill", "delete_knowledge"]) assert.equal(confirm(kind, "low"), true, kind);
  assert.equal(confirm("owner_decision", "high"), true);
  assert.equal(confirm("permission_change", "HIGH"), true, "case-insensitive risk");
  for (const [kind, risk] of [["owner_decision", "low"], ["owner_decision", "medium"], ["idea", "low"], ["permission_change", ""], ["external_channel_send", "medium"], ["", ""]]) {
    assert.equal(confirm(kind, risk), false, `${kind}/${risk}`);
  }
  assert.equal(PET_DESK_CONFIRM_MS, 5000);
});

test("an approval that needs a choice has only Review on the desk", async () => {
  const i18n = await deskCatalog("zh-CN");
  const html = renderDesk({...deskOptions(i18n, deskFixture()), approvalNeedsChoice: id => id === "a-801"});
  const needs = section(html, "pet-desk-needs");
  assert.match(needs, /<button type="button" data-pet-approval-open="a-801" data-i18n-ignore aria-label="去处理：Merge task-801\?">去处理<\/button>/);
  assert.doesNotMatch(needs, /data-pet-desk-approve="a-801"|data-pet-desk-reject="a-801"/);
});

test("the reject form is one labelled line with confirm disabled until there is a reason", async () => {
  const {renderPetDeskRejectForm} = await import("../build/pet_desk.js");
  await deskCatalog("en-US");
  try {
    const html = renderPetDeskRejectForm("a-1", "Delete <x>");
    assert.match(html, /<input type="text" data-pet-desk-reject-reason="a-1" data-i18n-ignore aria-label="Reason for rejecting “Delete &lt;x&gt;”" placeholder="Reason \(the pet keeps it; one line\)" maxlength="500" value="" autocomplete="off">/);
    assert.match(html, /<button type="submit" class="danger" data-pet-desk-reject-confirm="a-1" disabled>Confirm reject<\/button>/);
    assert.match(html, /<button type="button" data-pet-desk-reject-cancel="a-1">Never mind<\/button>/);
    assert.match(renderPetDeskRejectForm("a-1", "x", "kept"), /value="kept"[\s\S]*data-pet-desk-reject-confirm="a-1">Confirm reject/, "a restored reason enables confirm");
  } finally {
    (await deskCatalog("zh-CN"));
  }
});
