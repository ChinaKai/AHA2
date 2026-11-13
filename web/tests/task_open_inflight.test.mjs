import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");
const main = await readFile(resolve(root, "src", "main.ts"), "utf8");

// main.ts boots the whole application on import, so the open/stream wiring is
// pinned at the source level. The behavioural half lives in
// api_inflight.test.mjs (one in-flight request per identical read).
test("opening a Task does not start the SSE fallback with an immediate refresh", () => {
  assert.match(main, /function startTaskFallback\(taskID: string, announce = true, immediate = true\)/);
  // The open just fetched detail and conversation; a fallback tick at the same
  // moment repeated both requests. It now waits for the 2s interval.
  assert.match(main, /startTaskFallback\(taskID, false, false\)/);
  assert.doesNotMatch(main, /startTaskFallback\(taskID, false\)\s*;/);
});

test("a connected event stream stops fallback polling", () => {
  assert.match(
    main,
    /events\.addEventListener\("open", \(\) => \{[\s\S]{0,400}?noteTaskStreamSignal\(taskID\);/,
    "an open stream must mark itself live and stop the fallback timer",
  );
  // The fallback timer is still started before the stream opens, so a stream
  // that never connects is covered by the 2s refresh.
  assert.match(main, /if \(taskFallbackTimer !== null\) return;/);
});
