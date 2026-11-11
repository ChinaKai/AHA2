import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");

test("pet open_task waits for a single-use navigation claim", async () => {
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  const api = await readFile(resolve(root, "dist", "api.js"), "utf8");
  assert.match(api, /\/api\/v1\/pet\/navigation\/claim/);
  assert.match(assistant, /function claimPetNavigation/);
  assert.match(assistant, /navigation_id/);
  assert.match(assistant, /function resumePetNavigation/);
  // A live event with a queued action must not navigate before the claim.
  assert.doesNotMatch(assistant, /case "open_task":\s*if \(action\.task\) window\.dispatchEvent/);
});

test("app resumes queued pet navigation when the event stream opens", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(app, /resumePetNavigation/);
  assert.match(app, /addEventListener\("open"/);
});
