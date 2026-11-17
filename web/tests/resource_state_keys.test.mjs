import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");

// Regression for the 0.5.532 Resources page crash: "browser" was added to the
// ResourceKey union and to resourcesForView() but not to the state-map key
// list, so browserResourcesData() read an undefined state during render and
// the page never appeared. Keep the single key list and the view's requested
// keys in sync at the source level, where node --test can catch it.
test("every resource a view loads has an initialized state entry", async () => {
  const source = await readFile(resolve(root, "src/main.ts"), "utf8");

  const list = source.match(/const RESOURCE_KEYS = \[([\s\S]*?)\] as const;/);
  assert.ok(list, "RESOURCE_KEYS list must exist");
  const declared = new Set([...list[1].matchAll(/"([^"]+)"/g)].map(match => match[1]));

  const view = source.match(/function resourcesForView\(view: View\): ResourceKey\[\] \{[\s\S]*?\n\}/);
  assert.ok(view, "resourcesForView must exist");
  const requested = [...view[0].matchAll(/case "[^"]+": return \[([^\]]*)\]/g)]
    .flatMap(match => [...match[1].matchAll(/"([^"]+)"/g)].map(item => item[1]));
  assert.ok(requested.length > 0, "resourcesForView must return resource keys");

  for (const key of requested) {
    assert.ok(declared.has(key), `resourcesForView requests "${key}" without a resourceStates entry`);
  }
  // The Resources view reads this one directly, so the name must stay listed
  // even if resourcesForView is refactored.
  assert.ok(declared.has("browser"), "resourceStates must include browser");
  assert.match(source, /RESOURCE_KEYS\.map\(key => \[key, \{loading: false/);
});
