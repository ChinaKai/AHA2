// Phase 5 High batch: the knowledge directory is a navigation list of real
// buttons, not a fake ARIA tree.
//
// Checks against the real built Web bundle:
//   - no role="tree"/"treeitem"/"group" remains inside the directory;
//   - axe aria-required-children / aria-allowed-role / button-name are clean;
//   - keyboard Tab reaches the group head, node toggles and document buttons,
//     Enter expands/collapses groups and nodes;
//   - filtering, document switching and editing still work.
//
// Dependencies stay in .tools/web-validation; a machine without that directory
// can point AHA_PLAYWRIGHT_MODULE / AHA_BROWSER_EXECUTABLE / AHA_AXE_SOURCE at
// a local install.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const axeSource = await readFile(process.env.AHA_AXE_SOURCE || resolve(tools, "node_modules/axe-core/axe.min.js"), "utf8");
const {chromium} = await import(pathToFileURL(playwrightModule));
const launchOptions = process.env.AHA_BROWSER_EXECUTABLE
  ? {executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const output = process.env.AHA_BROWSER_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(output, {recursive: true});
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];

const now = new Date().toISOString();
const entry = (id, title, parentID, sort, body) => ({
  id, scope: "project", project_id: "project-1", parent_id: parentID,
  is_index: false, type: "practice", title, slug: id, body,
  status: "verified", confidence: 0.9, revision: 1, sort_order: sort,
  helped_count: 0, stale_count: 0, created_at: now, updated_at: now,
});
const KNOWLEDGE = [
  {id: "knowledge-index", scope: "project", project_id: "project-1", parent_id: "", is_index: true, type: "navigation", title: "Project index", slug: "index", body: "Project home.", status: "verified", confidence: 0.9, revision: 1, sort_order: 0, helped_count: 0, stale_count: 0, created_at: now, updated_at: now},
  entry("knowledge-a", "Build guide", "knowledge-index", 1, "Run the build with `make build`."),
  entry("knowledge-b", "Release checklist", "knowledge-a", 2, "Tag the release, then verify the artifact."),
  entry("knowledge-c", "Rollback", "knowledge-b", 3, "Roll back the previous artifact."),
  entry("knowledge-d", "Unrelated note", "", 4, "An unrelated note that stays out of the filtered view."),
];

const bootstrap = `
const KNOWLEDGE = ${JSON.stringify(KNOWLEDGE)};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/projects") return json({projects: [{id: "project-1", name: "Project One", project_type: "folder", knowledge_policy: "enabled", channel_retired: false}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: [{id: "workspace-1", project_id: "project-1", name: "WS One", root_path: "/tmp/ws", transport: "native", locality: "local", health: "ready", read_only: false}]});
  if (path === "/api/v1/skills") return json({skills: []});
  if (path === "/api/v1/knowledge") return json({knowledge: KNOWLEDGE, review_settings: {auto_approve: false}, has_more: false});
  if (path === "/api/v1/knowledge/libraries") return json({libraries: []});
  if (path === "/api/v1/knowledge/indexes") return json({indexes: KNOWLEDGE.filter(item => item.is_index)});
  if (path === "/api/v1/knowledge/proposals") return json({proposals: [], total: 0, page: 1, pages: 0, has_more: false});
  if (path === "/api/v1/projects/project-1/product-lines") return json({product_lines: []});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
sessionStorage.setItem("aha2.navigation", JSON.stringify({view: "knowledge"}));
`;
const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/styles.css"><script src="/axe.js"></script></head><body><main id="app"></main><script type="module" src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    response.setHeader("Content-Security-Policy", csp);
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
    } else if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
    } else if (name === "/axe.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(axeSource);
    } else {
      if (!/^\/[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      response.setHeader("Content-Type", name.endsWith(".css") ? "text/css" : "text/javascript");
      response.end(await readFile(resolve(root, "web/build", name.slice(1))));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;
let browser;
const errors = [];

const active = page => page.evaluate(() => {
  const element = document.activeElement;
  if (!(element instanceof HTMLElement)) return null;
  return {
    tag: element.tagName,
    text: (element.textContent || "").trim(),
    label: element.getAttribute("aria-label") || "",
    open: element.dataset.knowledgeOpen || "",
    group: element.dataset.knowledgeGroupToggle || "",
    toggle: element.dataset.knowledgeTreeToggle || "",
    expanded: element.getAttribute("aria-expanded"),
  };
});

try {
  browser = await chromium.launch(launchOptions);
  const page = await browser.newPage({viewport: {width: 1280, height: 900}});
  page.on("pageerror", error => { errors.push(String(error)); console.error("pageerror:", String(error)); });
  page.on("dialog", dialog => void dialog.dismiss());
  await page.goto(url);
  await page.waitForSelector(".knowledge-tree", {timeout: 15000});
  await page.waitForSelector('.knowledge-tree [data-knowledge-open="knowledge-a"]', {timeout: 15000});

  // Option 1: the directory is a nav of lists/buttons, with no fake tree roles.
  const semantics = await page.evaluate(() => {
    const tree = document.querySelector(".knowledge-tree");
    return {
      tag: tree?.tagName || "",
      nav: tree?.tagName === "NAV",
      label: tree?.getAttribute("aria-label") || "",
      treeRoles: tree?.querySelectorAll('[role="tree"],[role="treeitem"],[role="group"]').length,
      ownRole: tree?.getAttribute("role"),
      buttons: tree?.querySelectorAll("button").length,
    };
  });
  assert.equal(semantics.nav, true, "the directory is not a nav landmark");
  assert.equal(semantics.treeRoles, 0, "fake tree roles are still present inside the directory");
  assert.equal(semantics.ownRole, null, "the directory still carries an explicit role");
  assert.ok(semantics.label, "the directory nav has no accessible name");
  assert.ok(semantics.buttons >= 8, `expected the full button tree, found ${semantics.buttons}`);

  const axeRules = async () => page.evaluate(async () => {
    const result = await window.axe.run(document.querySelector(".knowledge-tree"), {
      runOnly: {type: "rule", values: ["aria-required-children", "aria-allowed-role", "button-name"]},
    });
    return result.violations.map(item => ({id: item.id, html: item.nodes.map(node => node.html)}));
  });
  assert.deepEqual(await axeRules(), [], "axe reported ARIA tree violations before interaction");

  // The practice group can be collapsed and expanded with the keyboard.
  const groupHead = page.locator("[data-knowledge-group-toggle]").nth(1);
  await groupHead.focus();
  const groupExpandedBefore = await groupHead.getAttribute("aria-expanded");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(40);
  assert.equal(await page.locator("[data-knowledge-group-toggle]").nth(1).getAttribute("aria-expanded"), groupExpandedBefore === "true" ? "false" : "true", "Enter on the group head did not toggle aria-expanded");
  await page.locator("[data-knowledge-group-toggle]").nth(1).focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(40);
  assert.equal(await page.locator("[data-knowledge-group-toggle]").nth(1).getAttribute("aria-expanded"), groupExpandedBefore, "the group did not expand again");

  // Tab from the group head reaches the node toggle; Enter collapses its children.
  await page.locator("[data-knowledge-group-toggle]").nth(1).focus();
  let reached = null;
  for (let index = 0; index < 8; index++) {
    await page.keyboard.press("Tab");
    const step = await active(page);
    if (step?.toggle === "knowledge-a") { reached = step; break; }
  }
  assert.ok(reached, "Tab never reached the Build guide node toggle");
  assert.ok(reached.label, "the node toggle has no accessible name");
  assert.equal(reached.expanded, "true", "the node did not start expanded");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(40);
  const collapsed = await page.evaluate(() => {
    const toggle = document.querySelector('[data-knowledge-tree-toggle="knowledge-a"]');
    const children = toggle?.closest(".knowledge-tree-node")?.querySelector(".knowledge-tree-children");
    return {expanded: toggle?.getAttribute("aria-expanded"), className: children?.className || "", childVisible: Boolean(children?.querySelector('[data-knowledge-open="knowledge-b"]'))};
  });
  assert.equal(collapsed.expanded, "false", "Enter on the node toggle did not set aria-expanded=false");
  assert.ok(collapsed.className.includes("is-collapsed"), "the node children were not collapsed");
  await page.locator('[data-knowledge-tree-toggle="knowledge-a"]').focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(40);
  assert.equal(await page.locator('[data-knowledge-tree-toggle="knowledge-a"]').getAttribute("aria-expanded"), "true", "the node did not expand again");

  // Tab order walks the visible hierarchy: A, B, C, then the unrelated root.
  await page.locator('[data-knowledge-tree-toggle="knowledge-a"]').focus();
  const walk = [];
  for (let index = 0; index < 8; index++) {
    await page.keyboard.press("Tab");
    const step = await active(page);
    walk.push(step);
    if (step?.open === "knowledge-d") break;
  }
  const names = walk.map(step => step?.open || (step?.toggle ? `toggle:${step.toggle}` : step?.group ? `group:${step.group}` : step?.text));
  assert.deepEqual(names.filter(Boolean).slice(0, 5), ["knowledge-a", "toggle:knowledge-b", "knowledge-b", "knowledge-c", "knowledge-d"], `unexpected tree Tab order: ${JSON.stringify(names)}`);
  for (const step of walk.slice(0, 5)) {
    assert.ok((step?.label || step?.text || "").trim(), `tree button without an accessible name in Tab order: ${JSON.stringify(step)}`);
  }
  assert.deepEqual(await axeRules(), [], "axe reported ARIA tree violations after keyboard interaction");

  // Filtering keeps the matching document and its ancestors, and drops others.
  await page.fill("#knowledge-search", "Release");
  await page.waitForTimeout(400);
  const filtered = await page.evaluate(() => [...document.querySelectorAll(".knowledge-tree [data-knowledge-open]")].map(element => element.textContent.trim()));
  assert.ok(filtered.includes("Release checklist"), `filter dropped the matching document: ${JSON.stringify(filtered)}`);
  assert.ok(filtered.includes("Build guide"), "filter dropped the matching document's ancestor");
  assert.ok(!filtered.includes("Unrelated note"), "filter kept a non-matching document");
  assert.deepEqual(await axeRules(), [], "axe reported ARIA tree violations after filtering");
  await page.fill("#knowledge-search", "");
  await page.waitForTimeout(400);

  // Detail and edit still work from the directory buttons.
  await page.click('.knowledge-tree [data-knowledge-open="knowledge-b"]');
  await page.waitForTimeout(60);
  assert.equal((await page.locator(".knowledge-reader .knowledge-document > h2").innerText()).trim(), "Release checklist", "clicking a tree document did not open the reader");
  assert.match(await page.locator(".knowledge-document-body").innerText(), /Tag the release/, "the reader did not render the selected document");
  await page.click("[data-knowledge-edit]");
  await page.waitForSelector(".knowledge-editor", {timeout: 5000});
  assert.equal(await page.locator('.knowledge-editor input[name="title"]').inputValue(), "Release checklist", "the editor did not load the selected document");
  await page.click("[data-knowledge-edit-cancel]");
  await page.waitForSelector(".knowledge-editor", {state: "detached", timeout: 5000});

  await page.screenshot({path: resolve(output, "phase5-knowledge-tree.png")});
  assert.deepEqual(errors, [], "the knowledge page reported page errors");
  console.log("knowledge tree: nav/list semantics, axe clean, keyboard traverses/collapses, and filter/detail/edit verified");
} finally {
  await browser?.close();
  server.close();
}