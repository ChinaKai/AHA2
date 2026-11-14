// Tree verification for the read-only knowledge vault. Drives the real bundle
// against a fixture API and covers: four entries, one real directory tree rooted
// at global/projects, tree links, editor URIs, search scopes/deep links,
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
// The outline deliberately omits `额外标题` (an older-index simulation), so
// index-based slug assignment would attach the real heading's id to the extra
// heading. `# 注释` is inside a fence and must never appear in the outline.
const FENCE_DOC = {
  ...DOC, id: "doc-fence", title: "围栏文档", path: "global/guides/fence.md",
  relative_path: "global/guides/fence.md", absolute_path: "/srv/vault/项目 知识/global/guides/fence.md",
  scope: "global", project_id: "", library_id: "global", library_name: "全局知识",
  body: "# 文档\n\n### 额外标题\n\n```bash\n# 注释\n```\n\n## 真标题\n\n正文。\n",
  outline: [{level: 1, text: "文档", slug: "kv-doc"}, {level: 2, text: "真标题", slug: "kv-real-title"}],
  backlinks: [], tags: [],
};
const DEEP_DOC = {
  ...DOC, id: "g-deep", title: "深入指南", path: "global/guides/advanced/deep.md",
  relative_path: "global/guides/advanced/deep.md", absolute_path: "/srv/vault/项目 知识/global/guides/advanced/deep.md",
  scope: "global", project_id: "", library_id: "global", library_name: "全局知识",
  obsidian_file: "global/guides/advanced/deep", backlinks: [], tags: [],
};
const allTree = [{
  id: "g-index", title: "全局知识首页", path: "global/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true, children: [{
    id: "g-folder", title: "通用知识", path: "global/guides/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true,
    children: [
      {id: "g-doc", title: "全局指南", path: "global/guides/guide.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      {id: "g-sub", title: "进阶", path: "global/guides/advanced/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true,
        children: [{id: "g-deep", title: "深入指南", path: "global/guides/advanced/deep.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []}]},
    ],
  }, {
    id: "g-other", title: "参考资料", path: "global/reference/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true,
    children: [{id: "g-other-doc", title: "参考手册", path: "global/reference/manual.md", type: "reference", status: "verified", scope: "global", is_index: false, system: false, children: []}],
  }],
}, {
  id: "p-index", title: "Demo 索引", path: "projects/p1/index.md", type: "navigation", status: "verified", scope: "project", project_id: "p1", is_index: true, system: true, children: [{
    id: "p-guides", title: "指南", path: "projects/p1/guides/index.md", type: "navigation", status: "verified", scope: "project", project_id: "p1", is_index: true, system: true,
    children: Array.from({length: 28}, (_, index) => ({
      id: index === 0 ? "doc-1" : `doc-${index + 1}`,
      title: index === 0 ? "构建指南" : `项目文档 ${index + 1}`,
      path: index === 0 ? "projects/p1/guides/build.md" : `projects/p1/guides/doc-${index + 1}.md`,
      type: "practice", status: "verified", scope: "project", project_id: "p1", is_index: false, system: false, children: [],
    })),
  }],
}];
const RECENT = [
  {id: "doc-1", title: "构建指南", path: "projects/p1/guides/build.md", type: "practice", status: "verified", scope: "project", project_id: "p1", library_id: "project:p1", library: "Demo", modified_at: now},
  {id: "inbox-1", title: "新想法", path: "projects/p1/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "p1", library_id: "project:p1", library: "Demo", modified_at: "2026-11-13T08:00:00Z"},
];
const GLOBAL_RECENT = [
  {id: "g-doc", title: "全局指南", path: "global/guides/guide.md", type: "practice", status: "verified", scope: "global", library_id: "global", library: "全局知识", modified_at: now},
];
const GLOBAL_INBOX = {id: "g-inbox", title: "全局想法", path: "global/inbox/idea.md", type: "practice", status: "unverified", scope: "global", library_id: "global", library: "全局知识", modified_at: now};
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
const TREES = ${JSON.stringify({all: allTree})};
const DOC = ${JSON.stringify(DOC)};
const INBOX_DOC = ${JSON.stringify(INBOX_DOC)};
const FENCE_DOC = ${JSON.stringify(FENCE_DOC)};
const DEEP_DOC = ${JSON.stringify(DEEP_DOC)};
const RECENT = ${JSON.stringify(RECENT)};
const GLOBAL_RECENT = ${JSON.stringify(GLOBAL_RECENT)};
const GLOBAL_INBOX = ${JSON.stringify(GLOBAL_INBOX)};
const SEARCH = ${JSON.stringify(SEARCH)};
const OLD_KNOWLEDGE = [{id: "global-root", scope: "global", parent_id: "", is_index: true, type: "navigation", title: "Global index", slug: "index", body: "Global home.", status: "verified", confidence: .9, revision: 1, sort_order: 0, helped_count: 0, stale_count: 0, created_at: "${now}", updated_at: "${now}"}];
window.__vaultRequests = [];
window.__vaultTreeRequests = [];
window.__vaultRecentRequests = [];
window.__vaultInboxRequests = [];
window.__vaultDelays = {};
const delay = async (kind, key) => {
  const milliseconds = Number(window.__vaultDelays?.[kind]?.[key] || 0);
  if (milliseconds > 0) await new Promise(resolve => setTimeout(resolve, milliseconds));
};
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
    const library = url.searchParams.get("library") || "all";
    window.__vaultTreeRequests.push(library);
    await delay("tree", library);
    return json({ok: true, enabled: true, index_ready: true, library, count: (TREES[library] || []).length, nodes: TREES[library] || []});
  }
  if (path.startsWith("/api/v1/knowledge-vault/doc/")) {
    const id = decodeURIComponent(path.slice("/api/v1/knowledge-vault/doc/".length));
    if (id === "doc-fence") return json({ok: true, doc: FENCE_DOC});
    if (id === "g-deep") return json({ok: true, doc: DEEP_DOC});
    if (id === "doc-1" || id === "doc-2" || id.startsWith("doc-") || id === "g-doc" || id === "g-index" || id === "g-folder" || id === "g-sub" || id === "g-other" || id === "p-index" || id === "p-guides") return json({ok: true, doc: DOC});
    if (id === "inbox-1") return json({ok: true, doc: INBOX_DOC});
    return json({ok: false, error: "knowledge_vault_document_not_found"}, 404);
  }
  if (path === "/api/v1/knowledge-vault/search") return json({ok: true, enabled: true, index_ready: true, results: SEARCH});
  if (path === "/api/v1/knowledge-vault/recent") {
    const project = url.searchParams.get("project") || "";
    window.__vaultRecentRequests.push(project || "all");
    await delay("recent", project || "all");
    return json({ok: true, enabled: true, index_ready: true, items: project ? RECENT : [...RECENT, ...GLOBAL_RECENT]});
  }
  if (path === "/api/v1/knowledge-vault/inbox") {
    const project = url.searchParams.get("project") || "";
    window.__vaultInboxRequests.push(project || "all");
    await delay("inbox", project || "all");
    return project
      ? json({ok: true, enabled: true, index_ready: true, count: 1, counts: [{library_id: "project:p1", library: "Demo", project_id: "p1", count: 1}], items: [RECENT[1]]})
      : json({ok: true, enabled: true, index_ready: true, count: 2, counts: [{library_id: "global", library: "全局知识", count: 1}, {library_id: "project:p1", library: "Demo", project_id: "p1", count: 1}], items: [RECENT[1], GLOBAL_INBOX]});
  }
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

async function expandGlobalTree(page) {
  await page.locator('[data-kv-toggle="g-index"]').click();
  await page.waitForSelector('[data-kv-toggle="g-folder"]');
  await page.locator('[data-kv-toggle="g-folder"]').click();
  await page.waitForSelector('a[data-kv-doc="g-doc"]');
}

async function expandProjectTree(page) {
  await page.locator('[data-kv-toggle="vault-dir:projects"]').click();
  await page.waitForSelector('[data-kv-toggle="p-index"]');
  await page.locator('[data-kv-toggle="p-index"]').click();
  await page.waitForSelector('[data-kv-toggle="p-guides"]');
  await page.locator('[data-kv-toggle="p-guides"]').click();
  await page.waitForSelector('a[data-kv-doc="doc-1"]');
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

  // The topbar keeps heading + tabs on one line and centers the controls with
  // them at 1280. Mid widths move the whole controls group below the tab row
  // rather than letting the tabs break into a second line.
  const topbarLayout = page => page.evaluate(() => {
    const box = element => {
      const rect = element.getBoundingClientRect();
      return {top: rect.top, bottom: rect.bottom, right: rect.right, centerY: rect.top + rect.height / 2};
    };
    const tabElements = [...document.querySelectorAll(".kv-tabs .kv-tab")];
    const tabs = tabElements.map(box);
    const controls = box(document.querySelector(".kv-controls"));
    const main = box(document.querySelector(".kv-topbar-main"));
    return {
      overflow: document.documentElement.scrollWidth - window.innerWidth,
      tabCount: tabElements.length,
      tabTop: Math.min(...tabs.map(item => item.top)),
      tabBottom: Math.max(...tabs.map(item => item.bottom)),
      tabTopSpread: Math.max(...tabs.map(item => item.top)) - Math.min(...tabs.map(item => item.top)),
      controls,
      main,
      topbarRight: document.querySelector(".kv-topbar").getBoundingClientRect().right,
      tabsOverflowX: getComputedStyle(document.querySelector(".kv-tabs")).overflowX,
      skillsVisible: Boolean(document.querySelector(".kv-tab-skills")?.offsetParent),
      settingsVisible: Boolean(document.querySelector(".kv-settings-trigger")?.offsetParent),
      moreCount: document.querySelectorAll("[data-kv-more], .kv-more-menu").length,
    };
  });

  let layout = await topbarLayout(page);
  assert.ok(layout.overflow <= 1, "1280px has no horizontal overflow");
  assert.equal(layout.tabCount, 4);
  assert.ok(layout.tabTopSpread < 1, "1280px keeps all four tabs on one row");
  assert.ok(layout.controls.top < layout.tabBottom, "1280px keeps the controls on the tab row");
  assert.ok(Math.abs(layout.main.centerY - layout.controls.centerY) < 8, "1280px centers controls with the tabs");
  assert.ok(Math.abs(layout.controls.right - layout.topbarRight) < 3, "1280px right-aligns the controls");

  for (const width of [1040, 900]) {
    await page.setViewportSize({width, height: 900});
    await page.waitForTimeout(60);
    layout = await topbarLayout(page);
    assert.ok(layout.overflow <= 1, `${width}px has no horizontal overflow`);
    assert.ok(layout.tabTopSpread < 1, `${width}px keeps all four tabs on one row`);
    assert.ok(layout.controls.top >= layout.tabBottom - 1, `${width}px wraps the controls below the tabs as a group`);
    assert.ok(Math.abs(layout.controls.right - layout.topbarRight) < 3, `${width}px keeps the controls right-aligned`);
  }
  await page.setViewportSize({width: 1280, height: 900});
  await page.waitForTimeout(60);

  // The topbar has no library/project scope dropdowns any more: the tree itself
  // navigates the real vault directories, rooted at global/ and projects/.
  assert.equal(await page.locator("#kv-library").count(), 0);
  assert.equal(await page.locator("#kv-project").count(), 0);
  assert.equal(await page.locator("nav.kv-tree-panel").count(), 1);
  assert.equal(await page.locator('nav.kv-tree-panel [role="tree"]').count(), 0);
  let treeText = await page.locator("nav.kv-tree-panel").innerText();
  assert.doesNotMatch(treeText, /系统索引/);
  assert.doesNotMatch(treeText, /全局知识首页/);
  assert.match(treeText, /global/);
  assert.match(treeText, /projects/);
  assert.equal(await page.locator('a[data-kv-doc="g-index"]').count(), 1, "the global root index is the global folder row");
  assert.equal(await page.locator('li.kv-tree-folder').filter({has: page.locator('[data-kv-toggle="g-index"]')}).locator('.kv-badge').count(), 0, "directory rows carry no badges");

  // Every directory starts collapsed like `tree` output: children are not in
  // the DOM until the folder is expanded, and nested folders stay closed.
  assert.equal(await page.locator('[data-kv-toggle="g-index"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('[data-kv-toggle="vault-dir:projects"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('[data-kv-toggle="g-folder"]').count(), 0, "collapsed global children are not rendered");
  assert.equal(await page.locator('[data-kv-toggle="p-index"]').count(), 0, "collapsed project children are not rendered");
  await page.locator('[data-kv-toggle="g-index"]').click();
  assert.equal(await page.locator('[data-kv-toggle="g-index"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="g-folder"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('[data-kv-toggle="g-other"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('a[data-kv-doc="g-doc"]').count(), 0, "collapsed folder children are not rendered");
  assert.equal(await page.locator('a[data-kv-doc="g-deep"]').count(), 0);
  await page.locator('[data-kv-toggle="g-folder"]').click();
  assert.equal(await page.locator('[data-kv-toggle="g-folder"]').getAttribute("aria-expanded"), "true");
  assert.ok(await page.locator('a.kv-tree-link[href="#knowledge?doc=g-doc&scope=global"]').count() >= 1);
  assert.equal(await page.locator('[data-kv-toggle="g-sub"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('a[data-kv-doc="g-deep"]').count(), 0, "nested folders start collapsed too");
  // The toggle keeps keyboard operation: Enter collapses and expands.
  await page.locator('[data-kv-toggle="g-folder"]').focus();
  await page.keyboard.press("Enter");
  assert.equal(await page.locator('[data-kv-toggle="g-folder"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('a[data-kv-doc="g-doc"]').count(), 0);
  await page.keyboard.press("Enter");
  assert.equal(await page.locator('[data-kv-toggle="g-folder"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('a[data-kv-doc="g-doc"]').count(), 1);

  // Clicking the directory row opens the directory's index document; the same
  // index is not rendered a second time as a leaf row.
  await page.locator('a[data-kv-doc="g-folder"]').click();
  await page.waitForSelector(".kv-doc-body");
  assert.equal((await page.locator(".kv-doc-head h1").textContent()).trim(), "构建指南");

  // Enter a project from the tree: projects -> p1 -> guides, each level
  // expanded explicitly. The single tree request already covers all libraries.
  await page.locator('[data-kv-toggle="vault-dir:projects"]').click();
  await page.waitForSelector('[data-kv-toggle="p-index"]');
  assert.equal(await page.locator('[data-kv-toggle="p-index"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('a[data-kv-doc="p-guides"]').count(), 0);
  await page.locator('[data-kv-toggle="p-index"]').click();
  await page.waitForSelector('[data-kv-toggle="p-guides"]');
  assert.equal(await page.locator('[data-kv-toggle="p-guides"]').getAttribute("aria-expanded"), "false");
  await page.locator('[data-kv-toggle="p-guides"]').click();
  await page.waitForSelector('a[data-kv-doc="doc-1"]');
  assert.match(await page.locator(".kv-tree-panel").innerText(), /项目文档 2/);
  assert.equal(await page.locator('a[data-kv-doc="g-doc"]').count(), 1, "the global scope stays browsable in the same tree");
  const treeRequests = await page.evaluate(() => window.__vaultTreeRequests);
  assert.deepEqual([...new Set(treeRequests)], ["all"], "one all-library tree request powers both scopes");

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
  // Search scope lives in the search panel, not in the topbar. It defaults to
  // all and can be narrowed to the current project or directory; results keep
  // scope/project/path context.
  assert.equal(await page.locator("#kv-search-scope").inputValue(), "all");
  assert.match(await page.locator(".kv-search-result").innerText(), /project · p1 · projects\/p1\/guides\/build\.md/);
  await page.selectOption("#kv-search-scope", "project");
  await page.waitForFunction(() => window.__vaultRequests.filter(item => item.startsWith("/api/v1/knowledge-vault/search")).at(-1)?.includes("library=project%3Ap1"));
  const projectSearch = await page.evaluate(() => window.__vaultRequests.filter(item => item.startsWith("/api/v1/knowledge-vault/search")).at(-1));
  assert.match(projectSearch, /project=p1/);
  const beforeDirectorySearch = await page.evaluate(() => window.__vaultRequests.filter(item => item.startsWith("/api/v1/knowledge-vault/search")).length);
  await page.selectOption("#kv-search-scope", "directory");
  await page.waitForFunction(count => window.__vaultRequests.filter(item => item.startsWith("/api/v1/knowledge-vault/search")).length > count, beforeDirectorySearch);
  await page.waitForSelector(".kv-search-result");
  await page.locator('.kv-search-result a[data-kv-doc="doc-1"]').click();
  await page.waitForSelector(".kv-doc-body");
  assert.equal(await page.locator(".kv-search-panel").isVisible(), false);

  // Opening a project document from search expands only its ancestor folders in
  // the single all-library tree. The closed search panel keeps its result links
  // in the DOM, so this wait must be scoped to the tree: an unscoped selector
  // resolves to the hidden search link first and times out waiting for it to
  // become visible.
  assert.equal(await page.locator('.kv-search-panel a[data-kv-doc="doc-1"]').count(), 1, "the hidden search panel keeps its result link in the DOM");
  assert.equal(await page.locator('.kv-search-panel a[data-kv-doc="doc-1"]').isVisible(), false, "the closed search panel link must stay hidden");
  await page.waitForSelector('.kv-tree-panel a[data-kv-doc="doc-1"]');
  assert.equal(await page.locator('[data-kv-toggle="vault-dir:projects"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="p-index"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="p-guides"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="g-other"]').getAttribute("aria-expanded"), "false", "sibling directories stay collapsed");
  // Recent, inbox (read-only) and skills default to all libraries and keep
  // project/path context on every row.
  await page.locator('[data-kv-tab-button="recent"]').click();
  await page.waitForSelector(".kv-feed-day");
  assert.match(await page.locator(".kv-feed").innerText(), /构建指南[\s\S]*Demo[\s\S]*全局指南[\s\S]*全局知识/);
  await page.locator('[data-kv-tab-button="inbox"]').click();
  await page.waitForSelector(".kv-inbox");
  assert.match(await page.locator(".kv-inbox").innerText(), /2 条待整理[\s\S]*projects\/p1\/inbox\/idea\.md[\s\S]*global\/inbox\/idea\.md/);
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

  // A deep-linked document reopens only its ancestor folders, so its row is
  // visible while every sibling directory stays collapsed.
  await page.goto(`${url}/?deep=nested#knowledge?doc=g-deep&scope=global`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-doc-body");
  assert.equal((await page.locator(".kv-doc-head h1").textContent()).trim(), "深入指南");
  await page.waitForSelector('a[data-kv-doc="g-deep"]');
  assert.equal(await page.locator('[data-kv-toggle="g-index"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="g-folder"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="g-sub"]').getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator('[data-kv-toggle="g-other"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('[data-kv-toggle="vault-dir:projects"]').getAttribute("aria-expanded"), "false", "the project branch stays collapsed for a global deep link");
  assert.equal(await page.locator('a[data-kv-doc="g-other-doc"]').count(), 0, "sibling folders stay collapsed");
  assert.equal(await page.locator('a[data-kv-doc="g-deep"]').getAttribute("aria-current"), "page");

  // Fenced `#` lines never become outline entries, and outline links attach by
  // text/level even when the index omits a heading that the file renders.
  await page.goto(`${url}/?fence=1#knowledge?doc=doc-fence&scope=global`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-doc-body");
  const outlineText = await page.locator(".kv-outline").innerText();
  assert.match(outlineText, /真标题/);
  assert.doesNotMatch(outlineText, /注释/);
  assert.equal(await page.locator('a[data-kv-outline="kv-real-title"]').count(), 1);
  assert.equal(await page.locator("h3#kv-real-title").count(), 0, "the extra heading must not steal the outline id");
  await page.locator('a[data-kv-outline="kv-real-title"]').click();
  await page.waitForTimeout(80);
  assert.deepEqual(await page.evaluate(() => ({
    id: document.activeElement?.id,
    tag: document.activeElement?.tagName,
    text: document.activeElement?.textContent,
  })), {id: "kv-real-title", tag: "H2", text: "真标题"});

  // An unknown deep link is an explicit error state, never a blank page.
  await page.goto(`${url}/?deep=2#knowledge?doc=missing&scope=global`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-state-error");
  assert.match(await page.locator(".kv-state-error").innerText(), /knowledge_vault_document_not_found/);

  // Address bar: opening a document updates the hash in place (no reload, no
  // history growth); every top-level view switch drops the knowledge hash so a
  // refresh restores the view the user is actually on.
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector(".kv-tree-link", {timeout: 15000});
  await expandProjectTree(page);
  const deepLinkHistory = await page.evaluate(() => history.length);
  await page.evaluate(() => { window.__deepLinkMarker = "alive"; });
  await page.locator('a[data-kv-doc="doc-1"]').click();
  await page.waitForSelector(".kv-doc-body");
  await page.waitForFunction(() => location.hash.startsWith("#knowledge?doc=doc-1"));
  assert.equal(await page.evaluate(() => window.__deepLinkMarker), "alive", "opening a document is an in-page update, not a reload");
  assert.equal(await page.evaluate(() => history.length), deepLinkHistory, "opening a document replaces the hash instead of stacking history");

  await page.evaluate(() => document.querySelector("[data-view=tasks]")?.click());
  await page.waitForSelector(".task-list", {timeout: 15000});
  assert.equal(await page.evaluate(() => location.hash), "", "leaving knowledge drops its deep-link hash");
  await page.reload({waitUntil: "networkidle"});
  await page.waitForSelector(".task-list", {timeout: 15000});
  assert.equal(await page.locator(".knowledge-vault").count(), 0, "refresh stays on the tasks view");

  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector(".kv-tree-link", {timeout: 15000});
  await expandProjectTree(page);
  await page.locator('a[data-kv-doc="doc-1"]').click();
  await page.waitForSelector(".kv-doc-body");
  await page.waitForFunction(() => location.hash.startsWith("#knowledge?doc=doc-1"));
  await page.evaluate(() => document.querySelector("[data-view=advanced]")?.click());
  await page.waitForSelector("#language-setting", {timeout: 15000});
  assert.equal(await page.evaluate(() => location.hash), "", "settings navigation drops the knowledge hash");
  await page.reload({waitUntil: "networkidle"});
  await page.waitForSelector("#language-setting", {timeout: 15000});
  assert.equal(await page.locator(".knowledge-vault").count(), 0, "refresh stays on the settings view");

  // A direct deep link still opens the referenced document.
  await page.goto(`${url}/#knowledge?doc=doc-1&scope=project&project=p1`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-doc-body");
  assert.match(await page.evaluate(() => location.hash), /^#knowledge\?doc=doc-1/);
  assert.equal((await page.locator(".kv-doc-head h1").textContent()).trim(), "构建指南");

  // Mobile: one column, list -> doc -> back keeps scroll, copy path only.
  await page.setViewportSize({width: 390, height: 780});
  await page.goto(url, {waitUntil: "networkidle"});
  await page.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await page.waitForSelector(".kv-tree-link");
  assert.equal(await page.locator(".kv-tree-panel").isVisible(), true);
  await page.screenshot({path: resolve(screenshots, "knowledge-vault-mobile-list.png"), fullPage: false});
  assert.equal(await page.locator(".kv-doc-panel").isVisible(), false);
  layout = await topbarLayout(page);
  assert.ok(layout.overflow <= 1, "390px has no horizontal overflow");
  assert.ok(layout.tabTopSpread < 1, "390px keeps the four tabs on one row");
  assert.equal(layout.skillsVisible, true, "390px shows the skills tab directly");
  assert.equal(layout.settingsVisible, true, "390px shows the settings gear directly");
  assert.equal(layout.moreCount, 0, "390px has no more trigger or menu");
  assert.equal(layout.tabsOverflowX, "auto", "the mobile tab strip scrolls horizontally when needed");

  // The skills tab is keyboard reachable and activates with Enter.
  await page.locator('.kv-tabs [data-kv-tab-button="skills"]').focus();
  assert.equal(await page.evaluate(() => document.activeElement?.dataset.kvTabButton), "skills");
  await page.keyboard.press("Enter");
  await page.waitForSelector(".kv-skill-card");
  assert.equal(await page.locator('.kv-tabs [data-kv-tab-button="skills"]').getAttribute("aria-current"), "page");
  await page.locator('.kv-tabs [data-kv-tab-button="knowledge"]').click();
  await page.waitForSelector(".kv-tree-link");

  // The gear opens the settings dialog without a menu detour.
  await page.locator(".kv-settings-trigger").click();
  assert.equal(await page.locator("#kv-settings-dialog").evaluate(dialog => dialog.open), true);
  await page.locator("#kv-settings-dialog [data-kv-settings-close]").first().click();
  assert.equal(await page.locator("#kv-settings-dialog").evaluate(dialog => dialog.open), false);

  // The mobile tree also starts collapsed at the two scope roots; expand one
  // directory path to pick a document row.
  assert.equal(await page.locator('[data-kv-toggle="g-index"]').getAttribute("aria-expanded"), "false");
  assert.equal(await page.locator('a[data-kv-doc="g-doc"]').count(), 0);
  await expandGlobalTree(page);

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
  layout = await topbarLayout(page);
  assert.ok(layout.tabTopSpread < 1, "360px keeps the four tabs on one row");
  assert.equal(layout.skillsVisible, true, "360px shows the skills tab directly");
  assert.equal(layout.settingsVisible, true, "360px shows the settings gear directly");
  assert.equal(layout.moreCount, 0, "360px has no more trigger or menu");

  await page.setViewportSize({width: 760, height: 780});
  await page.waitForTimeout(80);
  layout = await topbarLayout(page);
  assert.ok(layout.overflow <= 1, "760px has no horizontal overflow");
  assert.ok(layout.tabTopSpread < 1, "760px keeps the four tabs on one row");
  assert.equal(layout.skillsVisible, true, "760px shows the skills tab directly");
  assert.equal(layout.settingsVisible, true, "760px shows the settings gear directly");
  assert.equal(layout.moreCount, 0, "760px has no more trigger or menu");

  await page.setViewportSize({width: 1280, height: 900});
  await page.waitForTimeout(80);
  assert.ok((await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)), "1280px has no horizontal overflow");
  await page.screenshot({path: resolve(screenshots, "knowledge-vault-desktop.png"), fullPage: false});

  // One all-library tree request is shared by every scope. Switching tabs while
  // it is still in flight must not start a second request or render stale data.
  const single = await browser.newPage({viewport: {width: 1280, height: 900}, locale: "zh-CN"});
  const singleErrors = [];
  single.on("pageerror", error => singleErrors.push(error.message));
  await single.goto(`${url}/?single=1`, {waitUntil: "networkidle"});
  await single.evaluate(() => {
    window.__vaultDelays = {tree: {all: 500}};
  });
  await single.evaluate(() => document.querySelector("[data-view=knowledge]")?.click());
  await single.waitForSelector(".kv-tree-panel", {timeout: 15000});
  await single.locator('[data-kv-tab-button="recent"]').click();
  await single.locator('[data-kv-tab-button="knowledge"]').click();
  await single.waitForSelector('[data-kv-toggle="g-index"]', {timeout: 15000});
  assert.deepEqual((await single.evaluate(() => window.__vaultTreeRequests)), ["all"]);
  assert.match(await single.locator(".kv-tree-panel").innerText(), /global[\s\S]*projects/);
  assert.deepEqual(singleErrors, []);
  await single.close();

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
