import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const moduleURL = pathToFileURL(resolve(import.meta.dirname, "..", "build", "pet_navigation.js")).href;
const {createPetNavigationClaimer} = await import(moduleURL);

function fakeClock() {
  let current = 0;
  return {
    now: () => current,
    advance(ms) {
      current += ms;
    },
  };
}

function immediateSleep(clock, sleeps) {
  return async ms => {
    sleeps.push(ms);
    clock.advance(ms);
  };
}

function navigation(id = "nav-1", taskID = "task_abc") {
  return {id, task_id: taskID};
}

test("a transient claim failure is retried and navigates exactly once", async () => {
  const clock = fakeClock();
  const sleeps = [];
  const navigated = [];
  let attempts = 0;
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      attempts += 1;
      if (attempts === 1) {
        const error = new Error("bad gateway");
        error.status = 503;
        throw error;
      }
      return {ok: true, actions: [navigation()], expired: 0};
    },
    onNavigate: (taskID, action) => navigated.push(`${taskID}/${action.id}`),
    sessionID: () => "web-test",
    now: clock.now,
    sleep: immediateSleep(clock, sleeps),
  });

  const result = await claimer.claim(["nav-1"]);
  assert.equal(attempts, 2);
  assert.deepEqual(sleeps, [1_000]);
  assert.deepEqual(navigated, ["task_abc/nav-1"]);
  assert.deepEqual(result.map(action => action.id), ["nav-1"]);
});

test("a network error keeps retrying until the bounded attempt cap", async () => {
  const clock = fakeClock();
  const sleeps = [];
  let attempts = 0;
  let navigations = 0;
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      attempts += 1;
      throw new Error("network down");
    },
    onNavigate: () => {
      navigations += 1;
    },
    sessionID: () => "web-test",
    maxAttempts: 3,
    now: clock.now,
    sleep: immediateSleep(clock, sleeps),
  });

  const result = await claimer.claim();
  assert.equal(attempts, 3);
  assert.deepEqual(sleeps, [1_000, 2_000]);
  assert.equal(navigations, 0);
  assert.deepEqual(result, []);
});

test("a non-retryable 4xx stops immediately", async () => {
  let attempts = 0;
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      attempts += 1;
      const error = new Error("forbidden");
      error.status = 403;
      throw error;
    },
    onNavigate: () => {
      throw new Error("must not navigate");
    },
    sessionID: () => "web-test",
  });

  const result = await claimer.claim();
  assert.equal(attempts, 1);
  assert.deepEqual(result, []);
});

test("an expired claim response does not navigate or retry", async () => {
  let attempts = 0;
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      attempts += 1;
      return {ok: true, actions: [], expired: 1};
    },
    onNavigate: () => {
      throw new Error("must not navigate");
    },
    sessionID: () => "web-test",
  });

  const result = await claimer.claim(["nav-1"]);
  assert.equal(attempts, 1);
  assert.deepEqual(result, []);
});

test("retries stop at the navigation TTL instead of starting another attempt", async () => {
  const clock = fakeClock();
  const sleeps = [];
  let attempts = 0;
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      attempts += 1;
      throw new Error("network down");
    },
    onNavigate: () => {
      throw new Error("must not navigate");
    },
    sessionID: () => "web-test",
    ttlMs: 1_000,
    baseDelayMs: 600,
    maxDelayMs: 60_000,
    now: clock.now,
    sleep: immediateSleep(clock, sleeps),
  });

  const result = await claimer.claim();
  assert.equal(attempts, 2);
  assert.deepEqual(sleeps, [600, 400]);
  assert.deepEqual(result, []);
});

test("concurrent claims for the same action share one retry job", async () => {
  let calls = 0;
  const navigated = [];
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      calls += 1;
      return {ok: true, actions: [navigation()], expired: 0};
    },
    onNavigate: (_taskID, action) => navigated.push(action.id),
    sessionID: () => "web-test",
  });

  const [first, second] = await Promise.all([claimer.claim(["nav-1"]), claimer.claim(["nav-1"])]);
  assert.equal(calls, 1);
  assert.deepEqual(navigated, ["nav-1"]);
  assert.equal(first.length, 1);
  assert.equal(second.length, 1);
});

test("a later claim for an already navigated action does not navigate again", async () => {
  let calls = 0;
  const navigated = [];
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      calls += 1;
      return {ok: true, actions: [navigation()], expired: 0};
    },
    onNavigate: (_taskID, action) => navigated.push(action.id),
    sessionID: () => "web-test",
  });

  const first = await claimer.claim(["nav-1"]);
  const second = await claimer.claim(["nav-1"]);
  assert.equal(calls, 2);
  assert.deepEqual(navigated, ["nav-1"]);
  assert.equal(first.length, 1);
  assert.deepEqual(second, []);
});

test("dispose aborts an in-flight retry loop", async () => {
  let attempts = 0;
  const claimer = createPetNavigationClaimer({
    claim: async () => {
      attempts += 1;
      throw new Error("network down");
    },
    onNavigate: () => {
      throw new Error("must not navigate");
    },
    sessionID: () => "web-test",
    sleep: (_ms, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(new Error("aborted")), {once: true});
    }),
  });

  const pending = claimer.claim();
  await new Promise(resolve => setTimeout(resolve, 0));
  claimer.dispose();
  const result = await pending;
  assert.equal(attempts, 1);
  assert.deepEqual(result, []);
});
