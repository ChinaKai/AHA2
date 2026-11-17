import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const moduleURL = pathToFileURL(resolve(root, "build/browser_task_panel.js"));
const panel = await import(moduleURL);

test("frame signatures distinguish changed previews and tolerate short frames", () => {
  const first = panel.browserFrameSignature(new Uint8Array([1, 2, 3, 4]));
  const second = panel.browserFrameSignature(new Uint8Array([1, 2, 3, 5]));
  assert.notEqual(first, second);
  assert.equal(panel.browserFrameSignature(new Uint8Array([1, 2, 3, 4])), first);
  assert.equal(panel.browserFrameSignature(new Uint8Array()), "0");
});

test("unchanged pages back off the frame rate without exceeding the cap", () => {
  let delay = panel.BROWSER_FRAME_MIN_DELAY_MS;
  const same = "10:1:2:3:4:5:6";
  for (let index = 0; index < 10; index++) {
    delay = panel.nextBrowserFrameDelay(same, same, delay);
  }
  assert.equal(delay, panel.BROWSER_FRAME_MAX_DELAY_MS);
  assert.ok(delay >= 1000, "an unchanged page should drop below 1 fps");
});

test("a changed page returns to the live frame rate immediately", () => {
  const delay = panel.nextBrowserFrameDelay("10:1:2:3:4:5:6", "10:1:2:9:4:5:6", panel.BROWSER_FRAME_MAX_DELAY_MS);
  assert.equal(delay, panel.BROWSER_FRAME_MIN_DELAY_MS);
  assert.equal(panel.nextBrowserFrameDelay("", "10:1:2:3:4:5:6", panel.BROWSER_FRAME_MAX_DELAY_MS), panel.BROWSER_FRAME_MIN_DELAY_MS);
});
