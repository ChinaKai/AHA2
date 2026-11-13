import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const {computeSpeechBubblePlacement, petBubbleRectsOverlap} = await import(
  pathToFileURL(resolve(import.meta.dirname, "..", "build", "pet_bubble.js")).href
);

function assertClearOfPet(pet, placement, width, height, viewportWidth, viewportHeight) {
  const rect = {left: placement.left, top: placement.top, width, height};
  assert.equal(petBubbleRectsOverlap(rect, pet), false, "bubble overlaps the pet");
  assert.ok(placement.left >= 8, "left edge is outside the viewport");
  assert.ok(placement.top >= 8, "top edge is outside the viewport");
  assert.ok(placement.left + width <= viewportWidth - 8, "right edge is outside the viewport");
  assert.ok(placement.top + height <= viewportHeight - 8, "bottom edge is outside the viewport");
  assert.ok(placement.tailX >= 12 && placement.tailX <= width - 12, "tail x is outside the bubble");
  assert.ok(placement.tailY >= 12 && placement.tailY <= height - 12, "tail y is outside the bubble");
}

test("speech bubble prefers the side of the pet", () => {
  const pet = {left: 100, top: 100, width: 64, height: 64};
  const placement = computeSpeechBubblePlacement(pet, 264, 100, 800, 600);
  assert.equal(placement.tail, "tail-left");
  assert.ok(placement.left >= pet.left + pet.width + 12);
  assertClearOfPet(pet, placement, 264, 100, 800, 600);
});

test("speech bubble uses the left side when the right edge would clip", () => {
  const pet = {left: 620, top: 100, width: 64, height: 64};
  const placement = computeSpeechBubblePlacement(pet, 264, 100, 800, 600);
  assert.equal(placement.tail, "tail-right");
  assert.ok(placement.left + 264 <= pet.left - 12);
  assertClearOfPet(pet, placement, 264, 100, 800, 600);
});

test("speech bubble falls above a pet at the bottom of a narrow viewport", () => {
  const pet = {left: 148, top: 380, width: 64, height: 64};
  const placement = computeSpeechBubblePlacement(pet, 264, 100, 360, 500);
  assert.equal(placement.tail, "tail-bottom");
  assert.ok(placement.top + 100 <= pet.top - 12);
  assertClearOfPet(pet, placement, 264, 100, 360, 500);
});

test("speech bubble stays inside viewport margins when the pet is at a corner", () => {
  const pet = {left: 0, top: 0, width: 64, height: 64};
  const placement = computeSpeechBubblePlacement(pet, 264, 100, 400, 300);
  assertClearOfPet(pet, placement, 264, 100, 400, 300);
});
