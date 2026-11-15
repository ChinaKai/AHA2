// The page-level live region is deliberately narrow: #app no longer carries
// aria-live, and only explicit events write to #app-status. These assertions
// lock the decision (which task states are substantive, and dedup) and the
// wiring (the shell attribute, the banner, and the error path), because a
// browser check alone cannot prove that an ordinary render stays silent.
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {createStatusAnnouncer, taskStatusNeedsAnnouncement} = await import(
  pathToFileURL(resolve(root, "build", "ui_helpers.js")).href
);

test("ordinary renders and running-to-running refreshes do not announce", async () => {
  const index = await readFile(resolve(root, "index.html"), "utf8");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const styles = await readFile(resolve(root, "src", "styles.css"), "utf8");

  // #app is replaced on every render; it must not be a live region any more.
  assert.match(index, /<div id="app">/, "app shell lost its plain container");
  assert.doesNotMatch(index, /<div id="app"[^>]*aria-live/);

  // The replacement region persists beside #app and is visually hidden but
  // still exposed to assistive technology.
  assert.match(index, /<div id="app-status" class="visually-hidden" role="status" aria-live="polite" aria-atomic="true"><\/div>/);
  assert.match(styles, /\.visually-hidden \{[^}]*clip-path: inset\(50%\);[^}]*\}/);

  // The feedback banner is visual only now; the persistent region speaks.
  assert.doesNotMatch(main, /class="banner-stack" role="status"/);

  assert.equal(taskStatusNeedsAnnouncement("active", "active"), false);
  assert.equal(taskStatusNeedsAnnouncement("active", "preparing"), false);
  assert.equal(taskStatusNeedsAnnouncement("waiting_user", "active"), false, "resuming work is not a settled result");
  assert.equal(taskStatusNeedsAnnouncement("completed", "completed"), false);
});

test("a substantive task status announces once", () => {
  assert.equal(taskStatusNeedsAnnouncement("active", "completed"), true);
  assert.equal(taskStatusNeedsAnnouncement("active", "failed"), true);
  assert.equal(taskStatusNeedsAnnouncement("active", "blocked"), true);
  assert.equal(taskStatusNeedsAnnouncement("active", "cancelled"), true);

  let now = 1_000;
  const announcer = createStatusAnnouncer(3_000, () => now);
  assert.equal(announcer.claim(""), false, "empty text must not touch the region");
  assert.equal(announcer.claim("已完成 task-495"), true);
  now += 1_000;
  assert.equal(announcer.claim("已完成 task-495"), false, "a duplicate refresh must not repeat the event");
  assert.equal(announcer.claim("失败 task-495"), true);
  now += 2_000;
  assert.equal(announcer.claim("已完成 task-495"), true, "a genuine repeat after the window is announced again");
});

test("errors and notices still reach the live region", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  const hook = main.match(/function setMessage[\s\S]{0,320}/)?.[0] || "";
  assert.match(hook, /announceStatus\(String\(value\)\)/, "setMessage must announce from the persistent region");
  assert.match(main, /if \(taskStatusNeedsAnnouncement\(previousStatus, detail\.task\.status\)\)[\s\S]{0,180}announceStatus\(/);
  assert.match(main, /seenPendingApprovals[\s\S]{0,220}announceStatus\(t\("a11y\.newApprovals"/);
  // Login and recovery errors are not path of setMessage; keep them assertive.
  assert.equal((main.match(/class="form-error" role="alert"/g) || []).length, 2);
});