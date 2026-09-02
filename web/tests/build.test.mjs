import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";

test("built web contains responsive application", async () => {
  const root = resolve(import.meta.dirname, "..");
  const script = await readFile(resolve(root, "dist", "app.js"), "utf8");
  const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
  assert.match(script, /api\.authStatus/);
  assert.match(script, /EventSource/);
  assert.match(css, /@media \(max-width: 760px\)/);
});
