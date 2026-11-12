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

test("a direct load of the other settings view fetches the agent api settings", async () => {
  const app = await readFile(resolve(root, "dist", "app.js"), "utf8");
  // The Agent API panel lives on "other", but only the "advanced" view used to
  // load its data. A refresh straight into #other therefore rendered the
  // default (stdio_proxy off) state and never asked the server.
  assert.match(app, /case "other":\s*return \[\s*"advanced"\s*\];/,
    "the other settings view no longer maps to the advanced resource");
  assert.match(app, /if \(resourcesForView\(view\)\.includes\("advanced"\)\) jobs\.push\(loadResource\("advanced"/,
    "ensureViewData no longer loads advanced settings for every view that renders them");
  assert.match(app, /state\.agentAPISettings = agentAPI\.agent_api/,
    "the agent api response is no longer stored");
  assert.match(app, /agentAPI\.stdio_proxy \? "checked" : ""/,
    "the stdio switch is no longer rendered from the loaded settings");
});
