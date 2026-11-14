// Address-bar ownership across top-level views: a knowledge (or other) deep
// link may stay while its view is open, but leaving that view must drop the
// hash so a refresh uses the session navigation snapshot instead of routing
// back to the page the user just left.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const build = resolve(import.meta.dirname, "..", "build", "ui_helpers.js");
const {staleViewHashTarget} = await import(pathToFileURL(build).href);
const VIEWS = new Set(["projects", "channels", "models", "resources", "tasks", "knowledge", "prompts", "proxy", "sync", "advanced", "data", "other", "mcp", "pet-settings", "pet-workbench"]);
const isViewName = name => VIEWS.has(name);
const target = (hash, view, pathname = "/", search = "") => staleViewHashTarget(hash, pathname, search, view, isViewName);

test("leaving a knowledge deep link drops the hash instead of keeping it for the next refresh", () => {
  const knowledgeHash = "#knowledge?doc=knowledge_249782723be152ab7518&scope=global";
  assert.equal(target(knowledgeHash, "tasks"), "/");
  assert.equal(target(knowledgeHash, "models"), "/");
  assert.equal(target(knowledgeHash, "advanced"), "/");
  // Other query parameters belong to the page and survive the hash cleanup.
  assert.equal(target(knowledgeHash, "tasks", "/", "?perf=1"), "/?perf=1");
});

test("a deep link keeps its hash while its own view stays open", () => {
  assert.equal(target("#knowledge?doc=doc-1&scope=project&project=p1", "knowledge"), null);
  assert.equal(target("#resources?tab=mail", "resources"), null);
});

test("views without a hash and in-page heading anchors are left alone", () => {
  assert.equal(target("", "tasks"), null);
  assert.equal(target("#", "tasks"), null);
  // Outline links use #<heading-slug>; they are not routes and must not be
  // cleared by an unrelated render.
  assert.equal(target("#kv-real-title", "knowledge"), null);
  assert.equal(target("#kv-real-title", "tasks"), null);
  assert.equal(target("#unknown-route", "tasks"), null);
});