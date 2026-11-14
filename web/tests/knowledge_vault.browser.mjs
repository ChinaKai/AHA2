// Optional browser verification for the Phase 2 read-only knowledge vault.
// Drives the real bundle against a fixture API and covers: four entries,
// library/project switching, tree links, editor URIs, search/deep links,
// recent/inbox/skills, mobile two-level navigation, overflow, axe, and the
// switch-off legacy page.
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile, mkdir} from "node:fs/promises";
import {resolve, extname} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = process.env.AHA_WEB_VALIDATION || resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const axeSource = await readFile(process.env.AHA_AXE_SOURCE || resolve(tools, "node_modules/axe-core/axe.min.js"), "utf8");
const {chromium} = await import(pathToFileURL(playwrightModule));
const executablePath = process.env.AHA_BROWSER_EXECUTABLE;
const launchOptions = executablePath
  ? {executablePath, args: ["--no-sandbox"]}
  : await (async () => {
      const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
      return {executablePath: await binary.executablePath(), args: binary.args};
    })();
const screenshots = process.env.AHA_BROWSER_SCREENSHOT_DIR || resolve(tools, "screenshots");
await mkdir(screenshots, {recursive: true});

const now = "2026-11-14T10:00:00Z";
const DOC = {
  id: "doc-1", title: "构建指南", path: "projects/p1/guides/build.md",
  relative_path: "projects/p1/guides/build.md",
  absolute_path: "/srv/vault/项目 知识/projects/p1/guides/build.md",
  scope: "project", project_id: "p1", library_id: "project:p1", library_name: "Demo",
  type: "practice", status: "verified",
  body: "# 构建\n\n## 步骤\n\n运行 `make build`。\n\n参见 [[部署]]。\n",
  tags: ["build", "ops"], modified_at: now, is_index: false, system: false,
  outline: [{level: 1, text: "构建", slug: "build"}, {level: 2, text: "步骤", slug: "steps"}],
  backlinks: [{id: "doc-2", title: "发布检查", path: "projects/p1/guides/release.md"}],
  obsidian_ready: true, obsidian_file: "projects/p1/guides/build",
  obsidian_vault: "AHA2 Knowledge", remote_host: "devbox",
};
const INBOX_DOC = {...DOC, id: "inbox-1", title: "新想法", path: "projects/p1/inbox/idea.md", relative_path: "projects/p1/inbox/idea.md", absolute_path: "/srv/vault/项目 知识/projects/p1/inbox/idea.md", type: "practice", status: "unverified", tags: []};
const globalTree = [{
  id: "g-index", title: "全局索引", path: "global/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true, children: [],
}, {
  id: "g-folder", title: "guides", path: "global/guides", type: "reference", status: "verified", scope: "global", is_index: false, system: false,
  children: [{id: "g-doc", title: "全局指南", path: "global/guides/guide.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []}],
}];
const projectTree = [{
  id: "p-index", title: "Demo 索引", path: "projects/p1/index.md", type: "navigation", status: "verified", scope: "project", project_id: "p1", is_index: true, system: true, children: [],
}, ...Array.from({length: 28}, (_, index) => ({
  id: index === 0 ? "doc-1" : `doc-${index + 1}`,
  title: index === 0 ? "构建指南" : `项目文档 ${index + 1}`,
  path: index === 0 ? "projects/p1/guides/build.md" : `projects/p1/guides/doc-${index + 1}.md`,
  type: "practice", status: "verified", scope: "project", project_id: "p1", is_index: false, system: false, children: [],
}))];
const RECENT = [
  {id: "doc-1", title: "构建指南", path: "projects/p1/guides/build.md", type: "practice", status: "verified", scope: "project", project_id: "p1", library_id: "project:p1", library: "Demo", modified_at: now},
  {id: "inbox-1", title: "新想法", path: "projects/p1/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "p1", library_id: "project:p1", library: "Demo", modified_at: "2026-11-13T08:00:00Z"},
];
const SEARCH = [
  {id: "doc-1", title: "构建指南", path: "projects/p1/guides/build.md", type: "practice", scope: "project", project_id: "p1", status: "verified", tags: ["build"], snippet: "运行 make build 构建项目", updated: now},
];
const LIBS = {ok: true, enabled: true, vault_ready: true, index_ready: true, vault_name: "AHA2 Knowledge", obsidian_vault: "AHA2 Knowledge", obsidian_ready: true, remote_host: "devbox", vault_path: "/srv/vault", rebuilt_at: now,
  libraries: [{id: "global", kind: "global", name: "全局知识", count: 1}, {id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 28}],
  projects: [{id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 28}], archives: [], orphans: 0};

function bootstrap(off) {
  return `
const OFF = ${off};
const LIBS = ${JSON.stringify(LIBS)};
const TREES = ${JSON.stringify({global: globalTree, "project:p1": projectTree})};
const DOC = ${JSON.stringify(DOC)};
const INBOX_DOC = ${JSON.stringify(INBOX_DOC)};
const RECENT = ${JSON.stringify(RECENT)};
const SEARCH = ${JSON.stringify(SEARCH)};
const OLD_KNOWLEDGE = [{id: "global-root", scope: "global", parent_id: "", is_index: true, type: "navigation", title: "Global index", slug: "index", body: "Global home.", status: "verified", confidence: .9, revision: 1, sort_order: 0, helped_count: 0, stale_count: 0, created_at: "${now}", updated_at: "${now}"}];
window.__vaultRequests = [];
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  window.__vaultRequests.push(path + url.search);
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: "${now}"}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: false}, messages: [], models: [], tools: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/projects") return json({projects: [{id: "p1", name: "Demo", project_type: "folder"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path === "/api/v1/knowledge-vault/libraries") return OFF ? json({ok: false, error: "knowledge_vault_disabled"}, 404) : json(LIBS);
  if (path === "/api/v1/knowledge-vault/tree") {
    const library = url.searchParams.get("library") || "global";
    return json({ok: true, enabled: true, index_ready: true, library, count: (TREES[library] || []).length, nodes: TREES[library] || []});
  }
  if (path.startsWith("/api/v1/knowledge-vault/doc/")) {
    const id = decodeURIComponent(path.slice("/api/v1/knowledge-vault/doc/".length));
    if (id === "doc-1" || id === "doc-2" || id.startsWith("doc-") || id === "g-doc" || id === "g-index") return json({ok: true, doc: DOC});
    if (id === "inbox-1") return json({ok: true, doc: INBOX_DOC});
    return json({ok: false, error: "knowledge_vault_document_not_found"}, 404);
  }
  if (path === "/api/v1/knowledge-vault/search") return json({ok: true, enabled: true, index_ready: true, results: SEARCH});
  if (path === "/api/v1/knowledge-vault/recent") return json({ok: true, enabled: true, index_ready: true, items: RECENT});
  if (path === "/api/v1/knowledge-vault/inbox") return json({ok: true, enabled: true, index_ready: true, count: 1, counts: [{library_id: "project:p1", library: "Demo", project_id: "p1", count: 1}], items: [RECENT[1]]});
  if (path === "/api/v1/knowledge-vault/skills") return json({ok: true, enabled: true, skills: [{id: "s1", slug: "demo", name: "Demo Skill", scope: "global", library: "全局知识", description: "构建技能", file_count: 3, relative_path: "skills/demo", absolute_path: "/srv/vault/skills/demo", open_path: "/srv/vault/skills/demo/SKILL.md"}]});
  if (path === "/api/v1/knowledge") return json({knowledge: OLD_KNOWLEDGE, proposals: [], review_settings: {auto_approve: false}, has_more: false});
  if (path === "/api/v1/knowledge/libraries") return json({libraries: []});
  if (path === "/api/v1/knowledge/indexes") return json({indexes: []});
  if (path === "/api/v1/knowledge/proposals") return json({proposals: [], total: 0, page: 1, pages: 0, has_more: false});
  if (path.startsWith("/api/v1/")) return json({});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
window.confirm = () => true;
`;
}

const pageHTML = fixture => `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><main id="app"></main><script src="${fixture}"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon"};
const server = createServer(async (request, response) => {
  const pathname = new URL(request.url, "http://localhost").pathname;
  try {
    if (pathname === "/" || pathname === "/index.html") return response.end(pageHTML("/fixture.js"));
    if (pathname === "/off" || pathname === "/off/") return response.end(pageHTML("/off/fixture.js"));
    if (pathname === "/fixture.js" || pathname === "/off/fixture.js") {
      response.setHeader("Content-Type", "text/javascript");
      return response.end(bootstrap(pathname.startsWith("/off")));
    }
    const data = await readFile(resolve(root, "web", "build", pathname.slice(1)));
    response.setHeader("Content-Type", mime[extname(pathname)] || "application/octet-stream");
    response.end(data);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}`;

async function axe(page, label) {
  await page.addScriptTag({content: axeSource});
  const result = await page.evaluate(async () => {
    const root = document.querySelector(".knowledge-vault") || document.querySelector("#app");
    return await window.axe.run(root, {resultTypes: ["violations"]});
  });
  const bad = result.violations.filter(item => item.impact === "critical" || item.impact === "serious");
  assert.deepEqual(bad.map(item => ({id: item.id, impact: item.impact, nodes: item.nodes.length})), [], `${label}: axe critical/serious violations`);
  assert.equal(result.violations.find(item => item.id === "button-name")?.nodes.length || 0, 0, `${label}: button-name`);
  assert.equal(result.violations.find(item => item.id === "aria-required-children")?.nodes.length || 0, 0, `${label}: aria-required-children`);
  console.log(`axe ${label}: ${result.violations.length} violation types, 0 critical/serious, 0 button-name, 0 aria-required-children`);
}

let browser;
try {
  browser = await chromium.launch({executablePath, args: ["--no-sandbox"], headless: true});
  const page = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "zh-CN"});
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector(".knowledge-vault .kv-tab", {timeout: 15000});

  // Four top-level entries, no old tabs/proposals.
  const tabs = await page.locator(".kv-tabs .kv-tab").allTextContents();
  assert.deepEqual(tabs.map(text => text.trim()), ["知识", "最近", "待整理", "技能"]);
  assert.equal(await page.locator(".kv-tabs .kv-tab").count(), 4);
  assert.equal(await page.locator("[data-knowledge-scope]").count(), 0, "old knowledge tabs are gone");
  assert.equal(await page.locator("text=待审提案").count(), 0);

  // Tree is a nav with real anchors, grouped by folder and not by type.
  assert.equal(await page.locator("nav.kv-tree-panel").count(), 1);
  assert.equal(await page.locator('nav.kv-tree-panel [role="tree"]').count(), 0);
  assert.ok(await page.locator('a.kv-tree-link[href="#knowledge?doc=g-doc&scope=global"]').count() >= 1);
  assert.match(await page.locator("nav.kv-tree-panel").innerText(), /系统索引[\s\S]*文档/);

  // Document page and the three open methods (plus copy path).
  await page.locator('a[data-kv-doc="g-doc"]').click();
  await page.waitForSelector(".kv-doc-body");
  assert.equal((await page.locator(".kv-doc-head h1").textContent()).trim(), "构建指南");
  const local = page.locator('a.kv-open[href^="vscode://file/"]');
  const remote = page.locator('a.kv-open[href^="vscode://vscode-remote/ssh-remote+devbox/"]');
  const obsidian = page.locator('a.kv-open[href^="obsidian://open"]');
  assert.equal(await local.count(), 1);
  assert.equal(await remote.count(), 1);
  assert.equal(await obsidian.count(), 1);
  assert.match(await local.getAttribute("href"), /%E9%A1%B9%E7%9B%AE%20%E7%9F%A5%E8%AF%86/);
  assert.equal(await local.getAttribute("target"), "_blank");
  assert.equal(await obsidian.getAttribute("href"), "obsidian://open?vault=AHA2%20Knowledge&file=projects%2Fp1%2Fguides%2Fbuild");
  assert.equal(await page.locator("[data-kv-copy]").count() >= 1, true);
  assert.equal(await page.locator(".kv-doc-body").innerHTML().then(html => /<script/i.test(html)), false);
  assert.match(await page.locator(".kv-context-panel").innerText(), /大纲[\s\S]*构建[\s\S]*标签[\s\S]*build[\s\S]*反链[\s\S]*发布检查/);

  // Read-only: no edit/delete/review/move controls anywhere.
  const controls = await page.locator(".knowledge-vault").innerText();
  assert.doesNotMatch(controls, /新建|编辑|删除|移动|待审提案/);

  // Ctrl/Cmd+K search with filters and highlight; clicking opens the document.
  await page.keyboard.press("Control+k");
  await page.waitForSelector("#kv-search", {state: "visible"});
  await page.fill("#kv-search", "构建");
  await page.waitForSelector(".kv-search-result");
  assert.equal(await page.locator(".kv-search-result mark").count() >= 1, true);
  await axe(page, "search-open");
  await page.keyboard.press("Escape");
  await page.waitForSelector("#kv-search", {state: "hidden"});
  await page.keyboard.press("Control+k");
  await page.waitForSelector("#kv-search", {state: "visible"});
  await page.waitForSelector(".kv-search-result");
  await page.selectOption("#kv-search-type", "practice");
  await page.waitForTimeout(250);
  const searchRequest = await page.evaluate(() => window.__vaultRequests.filter(item => item.startsWith("/api/v1/knowledge-vault/search")).at(-1));
  assert.match(searchRequest, /q=%E6%9E%84%E5%BB%BA/);
  assert.match(searchRequest, /type=practice/);
  await page.locator('.kv-search-result a[data-kv-doc="doc-1"]').click();
  await page.waitForSelector(".kv-doc-body");
  assert.equal(await page.locator(".kv-search-panel").isVisible(), false);

  // Library/project switch loads the project tree.
  await page.selectOption("#kv-library", "project:p1");
  await page.waitForFunction(() => document.querySelectorAll("[data-kv-toggle], .kv-tree-link").length > 0);
  assert.ok(await page.locator('a[data-kv-doc="doc-1"]').count() >= 1);
  // Recent, inbox (read-only), skills.
  await page.locator('[data-kv-tab-button="recent"]').click();
  await page.waitForSelector(".kv-feed-day");
  assert.match(await page.locator(".kv-feed").innerText(), /构建指南[\s\S]*Demo/);
  await page.locator('[data-kv-tab-button="inbox"]').click();
  await page.waitForSelector(".kv-inbox");
  assert.match(await page.locator(".kv-inbox").innerText(), /1 条待整理[\s\S]*projects\/p1\/inbox\/idea\.md/);
  assert.equal(await page.locator(".kv-inbox [data-kv-move], .kv-inbox [data-kv-edit]").count(), 0);
  await page.locator('.kv-tabs [data-kv-tab-button="skills"]').click();
  await page.waitForSelector(".kv-skill-card");
  assert.match(await page.locator(".kv-skill-card").first().innerText(), /Demo Skill[\s\S]*构建技能[\s\S]*3 个文件/);
  assert.equal(await page.locator('a.kv-open[href^="vscode://file/"]').count() >= 1, true);

  await axe(page, "desktop");
  assert.deepEqual(errors, []);

  // Deep link bypasses navigation state and fetches the document.
  await page.goto(`${url}/?deep=1#knowledge?doc=doc-1&scope=project&project=p1`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-doc-body");
  assert.equal((await page.locator(".kv-doc-head h1").textContent()).trim(), "构建指南");

  // An unknown deep link is an explicit error state, never a blank page.
  await page.goto(`${url}/?deep=2#knowledge?doc=missing&scope=global`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-state-error");
  assert.match(await page.locator(".kv-state-error").innerText(), /knowledge_vault_document_not_found/);

  // Mobile: one column, list -> doc -> back keeps scroll, copy path only.
  await page.setViewportSize({width: 390, height: 780});
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector(".kv-tree-link");
  assert.equal(await page.locator(".kv-tree-panel").isVisible(), true);
  await page.screenshot({path: resolve(screenshots, "knowledge-vault-mobile-list.png"), fullPage: false});
  assert.equal(await page.locator(".kv-doc-panel").isVisible(), false);
  await page.locator("[data-kv-more]").click();
  assert.equal(await page.locator(".kv-more-menu").isVisible(), true);
  assert.equal(await page.locator('.kv-more-menu [data-kv-tab-button="skills"]').count(), 1);
  assert.equal(await page.locator(".kv-more-menu [data-kv-settings]").count(), 1);
  await page.locator("[data-kv-more]").click();
  await page.evaluate(() => window.scrollTo(0, 360));
  await page.waitForTimeout(80);
  const before = await page.evaluate(() => window.scrollY);
  await page.locator('a[data-kv-doc="g-doc"]').click();
  await page.waitForSelector(".kv-doc-body");
  assert.equal(await page.locator(".kv-tree-panel").isVisible(), false);
  assert.equal(await page.locator(".kv-doc-panel").isVisible(), true);
  assert.equal(await page.locator(".kv-copy").isVisible(), true);
  assert.equal(await page.locator(".kv-open-desktop").first().isVisible(), false);
  assert.ok((await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)), "390px document view has no horizontal overflow");
  await page.screenshot({path: resolve(screenshots, "knowledge-vault-mobile-doc.png"), fullPage: false});
  await page.locator("[data-kv-context-toggle]").first().click();
  assert.equal(await page.locator(".knowledge-vault").evaluate(element => element.classList.contains("context-open")), true);
  assert.equal(await page.locator(".kv-context-panel").isVisible(), true);
  await page.keyboard.press("Escape");
  assert.equal(await page.locator(".knowledge-vault").evaluate(element => element.classList.contains("context-open")), false);
  await page.locator("[data-kv-back]").click();
  await page.waitForTimeout(120);
  assert.equal(await page.locator(".kv-tree-panel").isVisible(), true);
  assert.ok(Math.abs((await page.evaluate(() => window.scrollY)) - before) < 80, "list scroll is restored");
  assert.ok((await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)), "390px has no horizontal overflow");
  await axe(page, "mobile-390");

  await page.setViewportSize({width: 360, height: 780});
  await page.waitForTimeout(80);
  assert.ok((await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)), "360px has no horizontal overflow");
  await page.setViewportSize({width: 1280, height: 900});
  await page.waitForTimeout(80);
  assert.ok((await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)), "1280px has no horizontal overflow");
  await page.screenshot({path: resolve(screenshots, "knowledge-vault-desktop.png"), fullPage: false});

  // Switch off: the legacy knowledge page is exactly what renders; the new
  // endpoints 404 and no vault chunk is mounted.
  const off = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "zh-CN"});
  await off.goto(`${url}/off`, {waitUntil: "networkidle"});
  await off.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await off.waitForSelector("[data-knowledge-scope=global]", {timeout: 15000});
  assert.equal(await off.locator(".knowledge-vault").count(), 0);
  assert.ok(await off.evaluate(() => window.__vaultRequests.some(item => item === "/api/v1/knowledge-vault/libraries")));
  const offStatus = await off.evaluate(async () => (await fetch("/api/v1/knowledge-vault/libraries")).status);
  assert.equal(offStatus, 404);
  await off.screenshot({path: resolve(screenshots, "knowledge-vault-switch-off.png"), fullPage: false});
  assert.deepEqual(errors, []);
  console.log("knowledge vault browser checks passed");
} finally {
  await browser?.close();
  server.close();
}
