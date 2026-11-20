import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const module = await import(pathToFileURL(resolve(root, "build", "pet_character.js")).href);

test("no-character id is recognized without matching real characters", () => {
  assert.equal(module.PET_NO_CHARACTER, "none");
  assert.equal(module.isPetNoCharacter("none"), true);
  assert.equal(module.isPetNoCharacter(" none "), true);
  assert.equal(module.isPetNoCharacter("NONE"), true);
  assert.equal(module.isPetNoCharacter("gugugaga"), false);
  assert.equal(module.isPetNoCharacter("deepseek_fish"), false);
  assert.equal(module.isPetNoCharacter(""), false);
  assert.equal(module.isPetNoCharacter(undefined), false);
});

test("no-character hides the avatar while real characters and switching back render it", () => {
  assert.equal(module.shouldRenderPetAvatar({character: "none", petEnabled: true}), false);
  assert.equal(module.shouldRenderPetAvatar({character: "gugugaga", petEnabled: true}), true);
  assert.equal(module.shouldRenderPetAvatar({character: "deepseek_fish", petEnabled: true}), true);
  // A disabled pet stays hidden regardless of the character choice.
  assert.equal(module.shouldRenderPetAvatar({character: "gugugaga", petEnabled: false}), false);
  // The assistant layer being hidden also wins.
  assert.equal(module.shouldRenderPetAvatar({character: "gugugaga", petEnabled: true, assistantVisible: false}), false);
});
