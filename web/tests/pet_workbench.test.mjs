import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {createPetApprovalCounter, petApprovalFilterCount, petPendingApprovalBadge, renderPetApprovalFilters, renderPetApprovalList, sortPetApprovals} from "../build/pet_approvals.js";
import {bindPetDocEditorButtons, countPetWatchAnomalies, createPetWorkbenchLoader, createPetWorkbenchPageKeeper, mapPetWorkbenchTab, PET_APPROVAL_MAX_LIMIT, petApprovalMatches, petApprovalPage, renderPetApprovalLoadMore, renderPetOverview, renderPetRecords, renderPetWorkbenchLoadError, sortPetWorklog, swapPetWorkbenchPage} from "../build/pet_workbench.js";

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
  for (const name of ["overview", "approvals", "records", "settings"]) {
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
  // and approvals, and each click follows the normal path.
  tabs.get("overview").click();
  assert.equal(active, "overview");
  assert.equal(tabs.get("overview").isActive(), true);
  assert.equal(binding.tab, "overview");
  tabs.get("approvals").click();
  assert.equal(active, "approvals");
  assert.equal(tabs.get("approvals").isActive(), true);
  assert.equal(binding.tab, "approvals");
  assert.equal(requests.length, 1, "tab switches must not issue another request");

  // Already on the wanted tab: no extra click (otherwise renders could loop).
  const approvalClicks = clickCounts.get("approvals");
  assert.equal(keeper.beforeRender({view: "pet-workbench", authenticated: true, tab: "approvals"}), page);
  assert.equal(clickCounts.get("approvals"), approvalClicks, "a matching tab is not clicked again");
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
  // Only the approvals tab reloads live; entering it re-reads because events
  // that arrived on another tab cannot patch the mounted rows.
  assert.match(main, /binding\.refresh = \(\) => \{ if \(active === "approvals"\) refreshApprovals\(\); \};/);
  assert.match(main, /if \(active === "approvals"\) void load\(\);/);
  // An event during an in-flight read queues exactly one follow-up.
  assert.match(main, /if \(approvalRefreshQueued\) return;/);
  assert.match(main, /approvalRefreshQueued = true;/);
  assert.match(main, /void load\(\)\.then\(\(\) => \{\s*approvalRefreshQueued = false;/);
  // A failed refresh keeps the last good list instead of clearing it.
  assert.match(main, /if \(view && active === "approvals"\) renderTab\(\);/);
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
  assert.match(main, /renderPetRecords\(view\.core, view\.worklog,/);
  assert.match(loader, /core\.map\(\(entry, index\) => renderCoreEntry\(/);
  assert.match(loader, /<details class="pet-doc-entry" id="pet-record-core-\$\{index\}">/);
  assert.doesNotMatch(loader, /<details class="pet-doc-entry" open>/);
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

test("the four workbench tabs default to overview and absorb legacy names", async () => {
  const {PET_WORKBENCH_TABS} = await import("../build/pet_workbench.js");
  assert.deepEqual([...PET_WORKBENCH_TABS], ["overview", "approvals", "records", "settings"]);
  assert.equal(mapPetWorkbenchTab(undefined), "overview");
  assert.equal(mapPetWorkbenchTab(""), "overview");
  assert.equal(mapPetWorkbenchTab("overview"), "overview");
  assert.equal(mapPetWorkbenchTab("approvals"), "approvals");
  assert.equal(mapPetWorkbenchTab("ideas"), "approvals", "legacy idea links open approvals");
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
  assert.match(html, /<details class="pet-overview-follows" id="pet-overview-follows"[^>]*><summary>关注任务（2）<\/summary>/);
  assert.doesNotMatch(html, /class="pet-overview-follows"[^>]*\sopen/);
  const ignoredList = html.match(/<details [^>]*id="pet-overview-ignored"[\s\S]*?<\/details>/)?.[0] ?? "";
  const unfollowedList = html.match(/<details [^>]*id="pet-overview-unfollowed"[\s\S]*?<\/details>/)?.[0] ?? "";
  assert.match(ignoredList, /<summary>忽略的任务（1）<\/summary>/);
  assert.match(ignoredList, /data-pet-watch-ignore="false" data-pet-watch-task="t-ignored"/);
  assert.doesNotMatch(ignoredList, /t-unwatched/);
  assert.match(unfollowedList, /<summary>未关注的任务（1）<\/summary>/);
  assert.match(unfollowedList, /data-pet-watch-ignore="true" data-pet-watch-task="t-unwatched"/);
  assert.doesNotMatch(unfollowedList, /t-ignored/);
  assert.doesNotMatch(html, /class="pet-overview-other"[^>]*\sopen/);
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

test("empty watch lists render as collapsed sections with zero counts", () => {
  const html = renderPetOverview({
    watches: [],
    channels: [],
    translate: overviewText,
    formatTime: identityTime,
  });
  assert.match(html, /class="pet-overview-follows"[^>]*><summary>关注任务（0）<\/summary>/);
  assert.match(html, /class="pet-overview-other"[^>]*><summary>忽略的任务（0）<\/summary>/);
  assert.match(html, /没有忽略的任务。/);
  assert.doesNotMatch(html, /class="pet-overview-follows"[^>]*\sopen/);
  assert.doesNotMatch(html, /class="pet-overview-other"[^>]*\sopen/);
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
  assert.match(html, /data-pet-record-goto="pet-record-core"/);
  assert.match(html, /data-pet-record-goto="pet-record-core-0"/);
  assert.match(html, /data-pet-record-goto="pet-record-worklog"/);
  // The index lists each dated worklog entry, not only the section heading.
  assert.match(html, /data-pet-record-goto="pet-record-worklog-0">2026-01-03</);
  assert.match(html, /data-pet-record-goto="pet-record-worklog-1">2026-01-02</);
  assert.match(html, /data-pet-record-goto="pet-record-worklog-2">2026-01-01</);
  assert.match(html, /id="pet-record-worklog-0"/);
  assert.ok(html.indexOf('id="pet-record-core"') < html.indexOf('id="pet-record-worklog"'), "core stays pinned on top");
  assert.ok(html.indexOf("newest") < html.indexOf("middle") && html.indexOf("middle") < html.indexOf("oldest"));
  assert.match(html, /data-pet-doc-section="core" data-pet-doc-name="identity\.md"/);
  assert.match(html, /data-pet-doc-section="worklog" data-pet-doc-name="2026-01-03\.md"/);

  const collapsed = renderPetRecords([], [], {
    translate: overviewText, emptyCore: "no core", emptyWorklog: "no worklog", tocOpen: false,
  });
  assert.match(collapsed, /<div class="pet-records toc-collapsed">/);
  assert.match(collapsed, /data-pet-record-toc-toggle aria-expanded="false"[^>]*>展开目录</);
});

test("phones fit four tabs and turn approval dialogs into full-screen sheets", async () => {
  const css = await readFile(resolve(root, "web/src/styles.css"), "utf8");
  const mobile = css.slice(css.lastIndexOf("@media (max-width: 640px)"));
  assert.notEqual(mobile, css, "a phone breakpoint must exist");
  assert.match(mobile, /\.pet-workbench-tabs \{ display: grid; grid-template-columns: repeat\(4, minmax\(0, 1fr\)\); gap: 4px; \}/);
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
  const loader = await readFile(resolve(root, "web/src/pet_workbench.ts"), "utf8");
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
