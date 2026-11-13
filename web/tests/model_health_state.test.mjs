// Behavioral tests for the per-model health state machine: clearing a result
// must also invalidate a request that was already in flight, so an old "可用"
// cannot reappear after the model or Provider was edited.
import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {createModelHealthState, forgetAllModelHealth, forgetModelHealth, runModelHealthCheck, runModelHealthChecks} = await import(
  pathToFileURL(resolve(root, "build", "model_health.js"))
);

function deferred() {
  let resolvePromise;
  let rejectPromise;
  const promise = new Promise((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  return {promise, resolve: resolvePromise, reject: rejectPromise};
}

test("a check shows 检测中 and a second click is ignored while it is running", async () => {
  const state = createModelHealthState();
  const pending = deferred();
  let calls = 0;
  const first = runModelHealthCheck(state, "model-1", () => {
    calls++;
    return pending.promise;
  });
  assert.equal(state.modelHealth["model-1"].status, "checking");
  // The row's button is disabled, but the guard protects a programmatic second
  // click or a re-render that replays the action.
  await runModelHealthCheck(state, "model-1", () => {
    calls++;
    return Promise.resolve({status: "ok"});
  });
  assert.equal(calls, 1);
  pending.resolve({status: "ok"});
  await first;
  assert.equal(state.modelHealth["model-1"].status, "ok");
});

test("forgetting a model discards a response that was already in flight", async () => {
  const state = createModelHealthState();
  const pending = deferred();
  const running = runModelHealthCheck(state, "model-1", () => pending.promise);
  assert.equal(state.modelHealth["model-1"].status, "checking");
  forgetModelHealth(state, "model-1");
  assert.equal(state.modelHealth["model-1"], undefined);
  pending.resolve({status: "ok"});
  await running;
  assert.equal(state.modelHealth["model-1"], undefined, "a stale response resurrected a cleared result");
});

test("an older response cannot overwrite a newer check after a clear", async () => {
  const state = createModelHealthState();
  const older = deferred();
  const newer = deferred();
  const first = runModelHealthCheck(state, "model-1", () => older.promise);
  forgetModelHealth(state, "model-1");
  const second = runModelHealthCheck(state, "model-1", () => newer.promise);
  newer.resolve({status: "ok"});
  await second;
  older.resolve({status: "quota_exhausted", detail: "stale"});
  await first;
  assert.equal(state.modelHealth["model-1"].status, "ok", "the older request won");
  assert.equal(state.modelHealth["model-1"].detail, undefined);
});

test("a forced refresh clears and invalidates every model's in-flight check", async () => {
  const state = createModelHealthState();
  const first = deferred();
  const second = deferred();
  const runningA = runModelHealthCheck(state, "model-a", () => first.promise);
  const runningB = runModelHealthCheck(state, "model-b", () => second.promise);
  forgetAllModelHealth(state);
  assert.deepEqual(state.modelHealth, {});
  first.resolve({status: "ok"});
  second.resolve({status: "unauthorized"});
  await Promise.all([runningA, runningB]);
  assert.deepEqual(state.modelHealth, {}, "a stale response repopulated a cleared table");
});

test("a failed request stays local to the row as 检测失败", async () => {
  const state = createModelHealthState();
  await runModelHealthCheck(state, "model-1", () => Promise.reject(new Error("network down")));
  assert.equal(state.modelHealth["model-1"].status, "error");
  assert.equal(state.modelHealth["model-1"].detail, "network down");
});

test("onChange fires for the progress state and the final state", async () => {
  const state = createModelHealthState();
  const changes = [];
  await runModelHealthCheck(state, "model-1", () => Promise.resolve({status: "rate_limited", retry_after_seconds: 5}), () => {
    changes.push(state.modelHealth["model-1"]?.status);
  });
  assert.deepEqual(changes, ["checking", "rate_limited"]);
});

test("batch checks are bounded and one failure does not block the others", async () => {
  const state = createModelHealthState();
  const started = [];
  let inFlight = 0;
  let maxInFlight = 0;
  const fetchHealth = async modelID => {
    started.push(modelID);
    inFlight += 1;
    maxInFlight = Math.max(maxInFlight, inFlight);
    await new Promise(resolve => setTimeout(resolve, 5));
    inFlight -= 1;
    if (modelID === "model-b") throw new Error("network down");
    return {status: "ok"};
  };
  await runModelHealthChecks(state, ["model-a", "model-b", "model-c", "model-d", "model-a"], fetchHealth, undefined, 2);
  assert.ok(maxInFlight <= 2, `ran ${maxInFlight} checks at once`);
  assert.deepEqual(started.sort(), ["model-a", "model-b", "model-c", "model-d"]);
  assert.equal(state.modelHealth["model-b"].status, "error");
  assert.equal(state.modelHealth["model-b"].detail, "network down");
  for (const id of ["model-a", "model-c", "model-d"]) {
    assert.equal(state.modelHealth[id].status, "ok", `${id} was blocked by another failure`);
  }
});

// The web build only strips types and never runs tsc, so a state property that
// is initialized but missing from the State interface ships silently. This is
// the guard for the health state added in task-330.
test("every top-level state key is declared by State or an inherited interface", async () => {
  const {readFile} = await import("node:fs/promises");
  const source = resolve(import.meta.dirname, "..", "src");
  const main = await readFile(resolve(source, "main.ts"), "utf8");
  const stateModule = await readFile(resolve(source, "model_health.ts"), "utf8");

  const header = main.match(/interface State(?:\s+extends\s+([A-Za-z_$][\w$]*))?\s*\{/);
  assert.ok(header, "State interface must exist");
  const interfaceBody = main.slice(header.index + header[0].length);
  const declared = new Set(
    interfaceBody.slice(0, interfaceBody.indexOf("\n}"))
      .split("\n")
      .map(line => line.replace(/\/\/.*$/, "").trim())
      .map(line => line.match(/^([A-Za-z_$][\w$]*)\s*:/)?.[1])
      .filter(Boolean),
  );
  const inherited = header[1];
  if (inherited) {
    const inheritedBody = stateModule.match(new RegExp(`export interface ${inherited} \\{([\\s\\S]*?)\\n\\}`))?.[1];
    assert.ok(inheritedBody, `inherited interface ${inherited} must be found in model_health.ts`);
    for (const line of inheritedBody.split("\n")) {
      const key = line.replace(/\/\/.*$/, "").trim().match(/^([A-Za-z_$][\w$]*)\s*:/)?.[1];
      if (key) declared.add(key);
    }
    for (const key of ["modelHealth", "modelHealthSeq"]) {
      assert.ok(declared.has(key), `State does not inherit ${key}`);
    }
  }

  const literalStart = main.indexOf("const state: State = {");
  assert.ok(literalStart >= 0, "state literal must be typed as State");
  let depth = 0;
  let end = literalStart;
  for (; end < main.length; end++) {
    if (main[end] === "{") depth++;
    else if (main[end] === "}" && --depth === 0) break;
  }
  const literal = main.slice(literalStart, end + 1);
  let flat = "";
  // The slice starts at the literal's opening brace; begin one level out so
  // top-level properties report at depth 0.
  depth = -1;
  for (const character of literal) {
    if ("{([".includes(character)) { depth++; flat += " "; continue; }
    if ("})]".includes(character)) { depth--; flat += " "; continue; }
    flat += depth === 0 ? character : " ";
  }
  const initialized = [...flat.matchAll(/([A-Za-z_$][\w$]*)\s*:\s*/g)].map(match => match[1]);
  const missing = initialized.filter(key => !declared.has(key) && !["const", "state", "State"].includes(key));
  assert.deepEqual([...new Set(missing)], [], "state keys missing from the State interface");
});
