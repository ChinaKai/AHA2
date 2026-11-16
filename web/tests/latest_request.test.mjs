import test from "node:test";
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {resolve} from "node:path";

const {createLatestRequest} = await import(pathToFileURL(resolve(import.meta.dirname, "..", "build", "ui_helpers.js")).href);

function deferred() {
  let resolvePromise;
  let rejectPromise;
  const promise = new Promise((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  return {promise, resolve: resolvePromise, reject: rejectPromise};
}

function controller(onResolve, onError, onSettled) {
  const loads = [];
  const latest = createLatestRequest({
    load: () => {
      const next = deferred();
      loads.push(next);
      return next.promise;
    },
    onResolve,
    onError,
    onSettled,
  });
  return {latest, loads};
}

test("only the newest request writes state; an older slow success is ignored", async () => {
  const resolved = [];
  const settled = [];
  const {latest, loads} = controller(value => resolved.push(value), message => assert.fail(message), () => settled.push("settled"));
  latest.start();
  latest.start();
  loads[0].resolve("old");
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(resolved, [], "the first success landed after a retry and must not overwrite it");

  loads[1].resolve("new");
  await loads[1].promise;
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(resolved, ["new"]);
  assert.deepEqual(settled, ["settled"], "only the newest request renders");
});

test("a failure is retryable", async () => {
  const resolved = [];
  const errors = [];
  const {latest, loads} = controller(value => resolved.push(value), message => errors.push(message));
  latest.start();
  loads[0].reject(new Error("network down"));
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(errors, ["network down"]);

  latest.start();
  loads[1].resolve("preview");
  await loads[1].promise;
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(resolved, ["preview"]);
});

test("a late failure from an older request cannot clear the newer success", async () => {
  const resolved = [];
  const errors = [];
  const {latest, loads} = controller(value => resolved.push(value), message => errors.push(message));
  latest.start();
  latest.start();
  loads[1].resolve("new preview");
  await loads[1].promise;
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(resolved, ["new preview"]);

  // The first request is still pending and fails only now.
  loads[0].reject(new Error("old failure"));
  await loads[0].promise.catch(() => {});
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(errors, [], "the older failure must not replace the newer success");
  assert.deepEqual(resolved, ["new preview"]);
});

test("invalidate (dialog close) makes every in-flight response a no-op", async () => {
  const resolved = [];
  const settled = [];
  const {latest, loads} = controller(value => resolved.push(value), message => assert.fail(message), () => settled.push("settled"));
  latest.start();
  latest.invalidate();
  loads[0].resolve("late");
  await loads[0].promise;
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(resolved, []);
  assert.deepEqual(settled, []);
});

test("compaction close/reopen keeps the newer failure when the old plan succeeds later", async () => {
  // Mirrors requestCompactionPlan: shared view fields plus explicit invalidate
  // on close, then a new request after reopening.
  const view = {compactionPlan: null, compactionError: "", compactionBusy: false};
  const loads = [];
  const latest = createLatestRequest({
    load: () => {
      const next = deferred();
      loads.push(next);
      return next.promise;
    },
    onResolve: plan => { view.compactionPlan = plan; },
    onError: message => { view.compactionError = message; },
    onSettled: () => { view.compactionBusy = false; },
  });
  latest.start(); // A is in flight
  latest.invalidate(); // Owner closes the dialog
  latest.start(); // Owner reopens immediately; B starts
  loads[1].reject(new Error("B preflight failed"));
  await loads[1].promise.catch(() => {});
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(view.compactionPlan, null);
  assert.equal(view.compactionError, "B preflight failed");
  assert.equal(view.compactionBusy, false);

  loads[0].resolve({can_compact: true, blocking_reasons: []});
  await loads[0].promise;
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(view.compactionPlan, null, "the old plan must not replace B's failure");
  assert.equal(view.compactionError, "B preflight failed");
});
