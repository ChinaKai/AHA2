import test from "node:test";
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");
const {api} = await import(pathToFileURL(resolve(root, "build", "api.js")));

// The page open, an SSE fallback tick and an event refresh can all ask for the
// same Task detail or conversation while the first request is still on the
// wire. They must share one request; a call made after it settled must fetch
// again so the view never pins stale data.
function controlledFetch(body) {
  const calls = [];
  const fetch = (path) => new Promise((resolvePromise) => {
    calls.push({path, release: (payload = body) => resolvePromise({
      ok: true,
      status: 200,
      json: async () => payload,
    })});
  });
  return {calls, fetch};
}

test("identical Task detail reads share one in-flight request", async t => {
  const original = globalThis.fetch;
  const {calls, fetch} = controlledFetch({ok: true, task: {id: "task-1"}});
  globalThis.fetch = fetch;
  t.after(() => { globalThis.fetch = original; });

  const first = api.task("task-1");
  const second = api.task("task-1");
  assert.equal(calls.length, 1, "a second read before the first settles must reuse it");
  calls[0].release();
  const [left, right] = await Promise.all([first, second]);
  assert.deepEqual(left, right);
  assert.equal(calls[0].path, "/api/v1/tasks/task-1");

  // Once settled the entry is gone: the next load still asks the server.
  const third = api.task("task-1");
  assert.equal(calls.length, 2);
  calls[1].release();
  await third;
});

test("conversation reads are shared only for the same agent and parameters", async t => {
  const original = globalThis.fetch;
  const {calls, fetch} = controlledFetch({ok: true, conversation: {items: [], has_more: false, next_before: 0, latest_sequence: 0}});
  globalThis.fetch = fetch;
  t.after(() => { globalThis.fetch = original; });

  const initial = api.agentConversation("task-1", "main", {limit: 20, categories: ["chat", "update", "error"]});
  const duplicate = api.agentConversation("task-1", "main", {limit: 20, categories: ["chat", "update", "error"]});
  const otherLimit = api.agentConversation("task-1", "main", {limit: 100, after: 0, categories: ["chat", "update", "error"]});
  assert.equal(calls.length, 2, "different parameters must stay separate requests");
  assert.match(calls[0].path, /agents\/main\/conversation\?limit=20&categories=chat%2Cupdate%2Cerror$/);
  assert.match(calls[1].path, /limit=100/);

  calls[0].release();
  calls[1].release();
  await Promise.all([initial, duplicate, otherLimit]);
});

test("a write between two reads does not let the later read join the pre-write one", async t => {
  const original = globalThis.fetch;
  const {calls, fetch} = controlledFetch({ok: true, task: {id: "task-3"}});
  globalThis.fetch = fetch;
  t.after(() => { globalThis.fetch = original; });

  const before = api.task("task-3");
  const write = api.resolveTaskPermissionRequest("task-3", "request-1", "approve");
  const after = api.task("task-3");
  assert.equal(calls.length, 3, "the post-write refresh must not join the pre-write read");
  assert.equal(calls[1].path, "/api/v1/tasks/task-3/permission-requests/request-1/resolve");

  calls[0].release();
  calls[1].release();
  calls[2].release();
  await Promise.all([before, write, after]);
});

test("a read started while a write is in flight is not reused after the write settles", async t => {
  const original = globalThis.fetch;
  const {calls, fetch} = controlledFetch({ok: true, task: {id: "task-4"}});
  globalThis.fetch = fetch;
  t.after(() => { globalThis.fetch = original; });

  const write = api.resolveTaskPermissionRequest("task-4", "request-2", "approve");
  // This read can reach the server before the write commits. Its promise must
  // not back a refresh that runs after the write settles.
  const duringWrite = api.task("task-4");
  assert.equal(calls.length, 2);

  calls[0].release();
  await write;
  assert.equal(calls.length, 2, "the read requested during the write is still in flight");

  const afterWrite = api.task("task-4");
  assert.equal(calls.length, 3, "post-write refresh must issue a new request, not reuse the in-flight pre-commit read");

  calls[1].release();
  calls[2].release();
  await Promise.all([duringWrite, afterWrite]);
});

test("a failed shared read reaches every waiter and is not cached", async t => {
  const original = globalThis.fetch;
  let attempts = 0;
  globalThis.fetch = async () => {
    attempts++;
    return attempts === 1
      ? {ok: false, status: 500, json: async () => ({error: "boom"})}
      : {ok: true, status: 200, json: async () => ({ok: true, task: {id: "task-2"}})};
  };
  t.after(() => { globalThis.fetch = original; });

  const first = api.task("task-2");
  const second = api.task("task-2");
  const results = await Promise.allSettled([first, second]);
  assert.deepEqual(results.map(result => result.status), ["rejected", "rejected"]);
  assert.equal(attempts, 1, "both waiters observe the same failed request");

  const retry = await api.task("task-2");
  assert.equal(attempts, 2, "a retry after failure must issue a new request");
  assert.equal(retry.task.id, "task-2");
});
