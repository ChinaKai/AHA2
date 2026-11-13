// Optional browser verification for the knowledge page: project search/paging,
// skill search/filter/view, and the auto-attach defaults in task creation.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir, writeFile} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const {chromium} = await import(pathToFileURL(resolve(tools, "node_modules/playwright/index.mjs")));
const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
const output = resolve(tools, "screenshots");
await mkdir(output, {recursive: true});

const now = new Date().toISOString();
const projects = Array.from({length: 13}, (_, index) => ({
  id: `project-${index + 1}`,
  name: index === 0 ? "CW500双摄" : `Project ${String(index + 1).padStart(2, "0")}`,
  project_type: index === 0 ? "git" : "folder",
  knowledge_policy: "enabled",
  channel_retired: false,
  created_at: now,
  updated_at: now,
}));
const skill = (id, name, scope, projectID, auto, extra = {}) => ({
  id,
  package_slug: id,
  scope,
  project_id: projectID,
  name,
  description: `${name} description`,
  instructions: `# ${name}\n\nUse ${name} carefully.`,
  version: 1,
  status: "active",
  enabled: true,
  auto_attach: auto,
  source_path: `/tmp/skills/${id}`,
  files: ["SKILL.md", "scripts/run.sh"],
  can_update: true,
  created_at: now,
  updated_at: now,
  ...extra,
});
const skills = [
  skill("skill-global-auto", "Global Auto", "global", "", true),
  skill("skill-global-2", "Global Two", "global", "", false),
  skill("skill-global-3", "Global Three", "global", "", false),
  skill("skill-global-4", "Global Four", "global", "", false),
  skill("skill-project-auto", "Project Auto", "project", "project-1", true),
  skill("skill-project-2", "Project Two", "project", "project-1", false),
  skill("skill-project-3", "Project Three", "project", "project-2", false),
  skill("skill-project-4", "Project Four", "project", "project-3", false),
  skill("skill-bound", "Bound Mirror", "project", "project-container", false, {
    bound_project_id: "project-2", binding_mode: "external", can_update: false,
  }),
  skill("skill-project-5", "Project Five", "project", "project-4", false),
];
const workspace = {
  id: "workspace-1", project_id: "project-1", name: "Local", locality: "local", transport: "native",
  root_path: "/tmp/workspace", health: "ready", read_only: false, capabilities: {},
};
const model = {
  id: "model-1", display_name: "Stub", provider_id: "provider-1", provider_name: "Provider",
  backend: "codex", wire_model: "stub", wire_api: "responses", default_env_group_id: "env-1",
};
const envGroup = {id: "env-1", name: "Stub", provider_id: "provider-1", backend: "codex", revision: 1, environment: {}};
const proposals = [
  ...Array.from({length: 2}, (_, index) => ({
    id: `proposal-pending-${index + 1}`,
    entry_id: `knowledge-pending-${index + 1}`,
    base_revision: 0,
    proposed: {
      id: `knowledge-pending-${index + 1}`, scope: "project", project_id: "project-1",
      title: `Pending Proposal ${index + 1}`, body: "pending body",
    },
    status: "pending",
    created_at: new Date(Date.now() - index * 60000).toISOString(),
    updated_at: new Date(Date.now() - index * 60000).toISOString(),
    review_mode: "manual",
    source_task_id: `task-pending-${index + 1}`,
  })),
  ...Array.from({length: 21}, (_, index) => ({
    id: `proposal-approved-${index + 1}`,
    entry_id: `knowledge-approved-${index + 1}`,
    base_revision: 0,
    proposed: {
      id: `knowledge-approved-${index + 1}`, scope: "global",
      title: `Approved Proposal ${index + 1}`, body: "approved body",
    },
    status: "approved",
    created_at: new Date(Date.now() - (index + 2) * 60000).toISOString(),
    updated_at: new Date(Date.now() - (index + 2) * 60000).toISOString(),
    review_mode: "manual",
    source_task_id: `task-approved-${index + 1}`,
  })),
];

const bootstrap = `
const data = ${JSON.stringify({projects, skills, workspace, model, envGroup, proposals})};
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/settings/security") return json({security: {validate_origin: true, access_scope: "lan", startup_override: false}});
  if (path === "/api/v1/settings/network") return json({network: {listen_address: "", startup_listen_address: "0.0.0.0:8766", restart_required: false}});
  if (path === "/api/v1/settings/backend") return json({backend: {idle_timeout_seconds: 300, turn_timeout_seconds: 3600}});
  if (path === "/api/v1/settings/agent-api") return json({agent_api: {url: "", startup_url: "http://127.0.0.1:8766", effective_url: "http://127.0.0.1:8766"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: new Date().toISOString()}});
  if (path === "/api/v1/projects") return json({projects: data.projects, has_more: false});
  if (path.startsWith("/api/v1/projects/") && path.endsWith("/product-lines")) return json({product_lines: []});
  if (path === "/api/v1/workspaces") return json({workspaces: [data.workspace]});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/skills") return json({skills: data.skills});
  if (path === "/api/v1/models") return json({models: [data.model]});
  if (path === "/api/v1/providers") return json({providers: [{id: "provider-1", name: "Provider", base_url: "https://example.test/v1", credential_configured: true}]});
  if (path === "/api/v1/env-groups") return json({env_groups: [data.envGroup]});
  if (path === "/api/v1/codex-accounts") return json({accounts: []});
  if (path === "/api/v1/mcp-servers") return json({servers: []});
  if (path === "/api/v1/knowledge") return json({knowledge: [], proposals: [], review_settings: {auto_approve: false}, has_more: true, next_cursor: "next"});
  if (path === "/api/v1/knowledge/proposals") {
    const status = url.searchParams.get("status") || "all";
    const pageNo = Math.max(1, Number(url.searchParams.get("page") || 1));
    const limit = Math.max(0, Number(url.searchParams.get("limit") || 10));
    const matching = data.proposals
      .filter(item => status === "all" || item.status === status)
      .sort((left, right) => String(right.created_at).localeCompare(String(left.created_at)));
    const total = matching.length;
    const pages = limit > 0 ? Math.ceil(total / limit) : 0;
    const items = limit > 0 ? matching.slice((pageNo - 1) * limit, pageNo * limit) : matching;
    return json({ok: true, proposals: items, total, page: pageNo, pages, has_more: limit > 0 && pageNo < pages});
  }
  if (path === "/api/v1/knowledge/libraries") return json({libraries: []});
  if (path === "/api/v1/knowledge/indexes") return json({indexes: []});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon"};
const server = createServer(async (request, response) => {
  const name = new URL(request.url, "http://localhost").pathname;
  try {
    if (name === "/" || name === "/index.html") {
      response.setHeader("Content-Type", "text/html");
      response.end(html);
      return;
    }
    if (name === "/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(bootstrap);
      return;
    }
    const file = resolve(root, "web", "build", name.slice(1));
    const bytes = await readFile(file);
    response.setHeader("Content-Type", mime[extname(name)] || "application/octet-stream");
    response.end(bytes);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

let browser;
try {
  browser = await chromium.launch({executablePath: await binary.executablePath(), args: binary.args, headless: true});
  const page = await browser.newPage({viewport: {width: 1440, height: 900}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector("#knowledge-project-search", {timeout: 15000});
  await page.waitForFunction(() => document.querySelectorAll("[data-knowledge-project]").length === 10, null, {timeout: 15000});

  // Knowledge update proposals: pending stays complete, "all" is server-paged.
  await page.waitForSelector("[data-knowledge-update-filter=all]", {timeout: 15000});
  await page.waitForFunction(() => document.querySelectorAll(".knowledge-proposal-card").length === 2, null, {timeout: 15000});
  assert.equal(await page.locator(".knowledge-proposal-card").count(), 2);
  await page.evaluate(() => document.querySelector("[data-knowledge-update-filter=all]")?.click());
  await page.waitForFunction(() => document.querySelectorAll(".knowledge-proposal-card").length === 10, null, {timeout: 15000});
  assert.match(await page.locator("[data-knowledge-update-filter=all]").textContent(), /23/);
  const proposalPager = page.locator(".knowledge-list-footer").filter({has: page.locator("[data-knowledge-proposal-page]")});
  assert.match(await proposalPager.textContent(), /第 1\/3 页/);
  await page.evaluate(() => document.querySelector('[data-knowledge-proposal-page="1"]')?.click());
  await page.waitForFunction(() => {
    const button = document.querySelector("[data-knowledge-proposal-page]");
    return button?.closest(".knowledge-list-footer")?.textContent?.includes("第 2/3 页");
  }, null, {timeout: 15000});
  assert.equal(await page.locator(".knowledge-proposal-card").count(), 10);
  await page.evaluate(() => document.querySelector("[data-knowledge-update-filter=pending]")?.click());
  await page.waitForFunction(() => document.querySelectorAll(".knowledge-proposal-card").length === 2, null, {timeout: 5000});

  // The entry-list load-more belongs to global/project documents only.
  await page.evaluate(() => document.querySelector("[data-knowledge-area=global]")?.click());
  await page.waitForSelector('[data-load-more="knowledge"]', {timeout: 5000});

  // Project search and load-more.
  assert.equal(await page.locator("[data-knowledge-project]").count(), 10);
  await page.locator("#knowledge-project-search").fill("CW500");
  await page.waitForFunction(() => document.querySelectorAll("[data-knowledge-project]").length === 1, null, {timeout: 5000});
  assert.match(await page.locator("[data-knowledge-project]").first().textContent(), /CW500/);
  await page.locator("#knowledge-project-search").fill("");
  await page.waitForFunction(() => document.querySelectorAll("[data-knowledge-project]").length === 10, null, {timeout: 5000});
  assert.equal(await page.locator("[data-knowledge-project-more]").count(), 1);
  await page.evaluate(() => document.querySelector("[data-knowledge-project-more]")?.click());
  await page.waitForFunction(() => document.querySelectorAll("[data-knowledge-project]").length === 13, null, {timeout: 5000});
  assert.equal(await page.locator("[data-knowledge-project-more]").count(), 0);

  // Skills search/filter and detail.
  await page.evaluate(() => document.querySelector("[data-knowledge-area=skills]")?.click());
  await page.waitForSelector("#knowledge-skill-search", {timeout: 15000});
  await page.waitForFunction(() => document.querySelectorAll(".skill-card").length === 6, null, {timeout: 15000});
  assert.equal(await page.locator('[data-load-more="knowledge"]').count(), 0);
  const client = await page.context().newCDPSession(page);
  const skillsShot = await client.send("Page.captureScreenshot", {format: "png", captureBeyondViewport: true});
  await writeFile(resolve(output, "knowledge-skills.png"), Buffer.from(skillsShot.data, "base64"));
  const autoCard = page.locator(".skill-card", {hasText: "Global Auto"});
  assert.match(await autoCard.textContent(), /自动启用/);
  assert.equal(await page.locator("[data-knowledge-skill-more]").count(), 1);
  await page.evaluate(() => document.querySelector("[data-knowledge-skill-more]")?.click());
  await page.waitForFunction(() => document.querySelectorAll(".skill-card").length === 10, null, {timeout: 5000});
  assert.equal(await page.locator("[data-knowledge-skill-more]").count(), 0);
  await page.selectOption("#knowledge-skill-scope", "global");
  await page.waitForFunction(() => document.querySelectorAll(".skill-card").length === 4, null, {timeout: 5000});
  await page.selectOption("#knowledge-skill-scope", "all");
  await page.locator("#knowledge-skill-search").fill("Project Auto");
  await page.waitForFunction(() => document.querySelectorAll(".skill-card").length === 1, null, {timeout: 5000});
  await page.evaluate(() => document.querySelector("[data-skill-view]")?.click());
  await page.waitForSelector("#skill-detail-dialog[open]", {timeout: 5000});
  assert.match(await page.locator("#skill-detail-body").textContent(), /SKILL\.md/);
  assert.match(await page.locator("#skill-detail-body").textContent(), /Project Auto/);
  const detailShot = await client.send("Page.captureScreenshot", {format: "png"});
  await writeFile(resolve(output, "knowledge-skill-detail.png"), Buffer.from(detailShot.data, "base64"));
  await page.evaluate(() => document.querySelector("#skill-detail-dialog")?.close());

  // Global scope does not need a Project selection; Project scope reveals it.
  await page.locator("#knowledge-skill-search").fill("Global Auto");
  await page.waitForFunction(() => {
    const cards = document.querySelectorAll(".skill-card");
    return cards.length === 1 && Boolean(cards[0].textContent?.includes("Global Auto"));
  }, null, {timeout: 5000});
  await page.evaluate(() => document.querySelector("[data-skill-edit]")?.click());
  await page.waitForSelector("#skill-editor-dialog[open]", {timeout: 5000});
  const skillProjectField = page.locator("#skill-editor-dialog .skill-project-field");
  assert.equal(await skillProjectField.isVisible(), false);
  await page.selectOption("#skill-editor-dialog [name=scope]", "project");
  assert.equal(await skillProjectField.isVisible(), true);
  await page.selectOption("#skill-editor-dialog [name=scope]", "global");
  assert.equal(await skillProjectField.isVisible(), false);
  await page.evaluate(() => document.querySelector("#skill-editor-dialog")?.close());

  // Task creation pre-checks auto-attach skills and allows unchecking them.
  await page.evaluate(() => document.querySelector("[data-view=tasks]")?.click());
  await page.waitForSelector("[data-dialog=task]", {timeout: 15000});
  await page.evaluate(() => document.querySelector("[data-dialog=task]")?.click());
  await page.waitForSelector("#task-dialog[open]", {timeout: 5000});
  assert.equal(await page.locator("#task-isolation").inputValue(), "inplace");
  assert.equal(await page.locator("#task-worktree-settings").isVisible(), false);
  await page.evaluate(() => {
    const fold = document.querySelector("#task-skills-fold");
    if (fold instanceof HTMLDetailsElement) fold.open = true;
  });
  await page.waitForSelector('#task-skill-options input[value="skill-global-auto"]', {state: "attached", timeout: 15000});
  const taskShot = await client.send("Page.captureScreenshot", {format: "png"});
  await writeFile(resolve(output, "task-auto-skills.png"), Buffer.from(taskShot.data, "base64"));
  assert.equal(await page.locator('#task-skill-options input[value="skill-global-auto"]').isChecked(), true);
  assert.equal(await page.locator('#task-skill-options input[value="skill-project-auto"]').isChecked(), true);
  assert.equal(await page.locator('#task-skill-options input[value="skill-global-2"]').isChecked(), false);
  await page.evaluate(() => document.querySelector('#task-skill-options input[value="skill-global-auto"]')?.click());
  assert.equal(await page.locator('#task-skill-options input[value="skill-global-auto"]').isChecked(), false);
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
console.log("knowledge page browser check passed");
