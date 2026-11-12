// The Agent API settings form is bound through bindForm, whose callback gets a
// FormData — not the form element. Reading the checkbox through the element is
// a TypeError thrown before any request, which is invisible to a source-only
// check and takes the whole save path down with it.
import test from "node:test";
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const {formCheckbox} = await import(pathToFileURL(resolve(root, "dist", "ui_helpers.js")));

test("a checked stdio proxy checkbox is read from FormData", () => {
  const form = new FormData();
  form.append("url", "");
  form.append("stdio_proxy", "on");
  assert.equal(formCheckbox(form, "stdio_proxy"), true);
});

test("an unchecked stdio proxy checkbox is absent from FormData", () => {
  const form = new FormData();
  form.append("url", "https://aha.example.test");
  assert.equal(formCheckbox(form, "stdio_proxy"), false);
});

test("the agent api settings save reads the checkbox from the form data", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  assert.match(app, /formCheckbox\(form, "stdio_proxy"\)/,
    "the settings save no longer reads stdio_proxy from FormData");
  assert.doesNotMatch(app, /form\.querySelector\([^)]*stdio_proxy/,
    "the settings save calls querySelector on FormData again");
  assert.match(app, /updateAgentAPISettings\(\{\s*url,\s*stdio_proxy: stdioProxy\s*\}\)/,
    "the settings save does not send stdio_proxy");
});
