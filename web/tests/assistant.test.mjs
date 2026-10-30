import test from "node:test";
import assert from "node:assert/strict";
import {readFile, stat} from "node:fs/promises";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");

test("built assistant module shares the versioned API singleton", async () => {
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(assistant, /\.\/api\.js\?v=[a-f0-9]{12}/);
  assert.match(app, /\.\/assistant\.js\?v=[a-f0-9]{12}/);
});

test("built assistant module exposes the pet and panel mount", async () => {
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  assert.match(assistant, /function mountAssistant/);
  assert.match(assistant, /assistant-pet/);
  assert.match(assistant, /assistant-panel/);
});

test("built assistant ships the gugu and whale character skins", async () => {
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  assert.match(assistant, /gugugaga/);
  assert.match(assistant, /deepseek_fish/);
  assert.doesNotMatch(assistant, /xiaoa/);
  assert.match(assistant, /gugu-3d\.png/);
  assert.match(assistant, /fish-3d\.png/);
  assert.match(assistant, /assistant-character-menu/);
  assert.match(assistant, /scheduleRoam/);
});

test("built assistant ships the original 3D and desk-pet assets", async () => {
  for (const name of ["gugu-3d.png", "gugu-head.png", "fish-3d.png", "fish-head.png"]) {
    const info = await stat(resolve(root, "dist", "assets", "assistant", name));
    assert.ok(info.size > 1000, name + " is missing");
  }
});

test("built assistant ships the procedural motion controller", async () => {
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  for (const token of ["MOTION_LABELS", "data-assistant-motion", "AHA2Assistant", "requestAnimationFrame", "applyPetControl"]) {
    assert.ok(assistant.includes(token), token + " missing");
  }
});

test("pet uses the cached character before the first API response", async () => {
  const assistant = await readFile(resolve(root, "dist", "assistant.js"), "utf8");
  const styles = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(assistant, /aha2\.pet\.character\.v1/);
  assert.match(assistant, /function markCharacterReady/);
  assert.match(assistant, /petState\?\.character \|\| cachedCharacter \|\| DEFAULT_CHARACTER/);
  assert.match(assistant, /character-loading/);
  assert.match(styles, /\.assistant-pet\.character-loading \{ visibility: hidden; \}/);
});
