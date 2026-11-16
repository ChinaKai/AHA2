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

test("a failure is retryable and a late failure cannot clear the newer success", async () => {
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

  // A delayed failure from the first attempt must not reopen the error state.
  loads[0].reject(new Error("old failure"));
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(errors, ["network down"]);
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
