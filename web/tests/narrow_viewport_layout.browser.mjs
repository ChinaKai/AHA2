import assert from "node:assert/strict";
import {createServer} from "node:http";
import {mkdir, readFile} from "node:fs/promises";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

// Regression for the 0.5.533 phone layout:
//   * the Resources page rendered outside its .page container, so the copy,
//     refresh button and "新建 Profile" button sat on the screen edge; the
//     browser empty state had no layout and its CTA flowed inside the help
//     paragraph (and the docked pet covered it);
//   * the knowledge settings dialog inherited width:100%/38px for its
//     auto-sync checkbox, which pushed the copy past the dialog edge and left
//     the footer far below a short landscape viewport.
// Both surfaces are measured at 390x844 and 844x390. Every assertion is a
// geometry check that fails on the un-fixed build.
const root = resolve(import.meta.dirname, "../..");
const tools = process.env.AHA_WEB_VALIDATION || resolve(root, ".tools/web-validation");
const playwrightModule = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
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

const now = "2026-11-17T09:00:00Z";
const SETTINGS = {
  ok: true,
  settings: {
    enabled: true, source: "database", vault_ready: true, vault_initialized: true, index_ready: true,
    vault_name: "AHA2 Knowledge",
    vault_path: "/srv/build/users/owner/aha2/项目 知识/一个很长的目录名称用来验证设置弹窗在窄视口下不会横向溢出",
    index_path: "/srv/build/users/owner/aha2/knowledge-vault-index.db",
    entry_count: 5, rebuilt_at: now,
    // The real settings payload carries auto_sync; the older fixture omitted
    // it, which is why the giant-checkbox regression was never exercised.
    auto_sync: {
      enabled: true, paused: false, pause_reason: "", conflict: false, conflict_detail: "",
      last_error: "上次自动同步失败：无法写入索引目录，请检查权限与磁盘空间", warning: "",
      blocked: 1, blocked_files: [{path: "projects/p1/notes/一个很长的未跟踪文件名称.md", reason: "untracked"}],
      last_sync_at: now, pending: 3,
    },
  },
};
const GIT_STATUS = {
  available: true, initialized: true, branch: "main", current_branch: "main",
  remote: "git@example.com:…", remote_full: "git@example.com:org/a-very-long-repository-name.git",
  remote_set: true, clean: false, changed: 2, tracking: true, ahead: 1, behind: 0, has_commits: true,
  last_commit: "1a2b3c4", last_commit_message: "更新知识库里的一个很长很长的提交说明", last_commit_at: now,
  last_sync_at: now,
};

const bootstrap = `
const json = (value, status = 200) => new Response(JSON.stringify(value), {status, headers: {"Content-Type": "application/json"}});
const NOW = ${JSON.stringify(now)};
const SETTINGS = ${JSON.stringify(SETTINGS)};
const GIT = ${JSON.stringify(GIT_STATUS)};
window.fetch = async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, location.origin);
  const path = url.pathname;
  if (path === "/api/v1/auth/status") return json({authenticated: true, csrf_token: "csrf", owner: {id: "owner-1", username: "owner"}});
  if (path === "/api/v1/settings/general") return json({ok: true, general: {language: "zh-CN"}});
  if (path === "/api/v1/system") return json({system: {web_version: "v0.0.0.test", version: "v0.0.0.test", started_at: NOW}});
  if (path === "/api/v1/assistant") return json({assistant: {character: "gugugaga", name: "AHA", nickname: "AHA", enabled: true}, messages: [], models: [], tools: []});
  if (path === "/api/v1/projects") return json({projects: [{id: "p1", name: "Demo", project_type: "folder"}], has_more: false});
  if (path === "/api/v1/workspaces") return json({workspaces: []});
  if (path === "/api/v1/tasks") return json({tasks: [], has_more: false, counts: {status: {}, project: {}, device: {}}});
  if (path === "/api/v1/events") return json({events: [], cursor: 0});
  if (path === "/api/v1/hardware-resources") return json({resources: []});
  if (path === "/api/v1/browser/host") return json({ok: true, host: {
    available: false, host_id: "local", edge_available: false,
    code: "browser_host_unavailable", error: "浏览器宿主未配置（AHA2_BROWSER_HOST_URL）",
  }});
  if (path === "/api/v1/browser/profiles") return json({ok: true, profiles: []});
  if (path === "/api/v1/serial-ports") return json({ports: []});
  if (path === "/api/v1/knowledge-vault/libraries") return json({ok: true, enabled: true, vault_ready: true, index_ready: true, vault_path: SETTINGS.settings.vault_path, projects: [{id: "p1", name: "Demo"}], libraries: [{id: "global", name: "全局知识", knowledge_count: 2, skill_count: 1}], obsidian_ready: true});
  if (path === "/api/v1/knowledge-vault/open-info") return json({ok: true, open_available: true, vault_path: SETTINGS.settings.vault_path});
  if (path === "/api/v1/knowledge-vault/tree") return json({ok: true, enabled: true, index_ready: true, library: "all", count: 1, nodes: [{id: "g-index", title: "全局知识首页", path: "global/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true, children: [{id: "g-doc", title: "全局指南", path: "global/guides/guide.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []}]}]});
  if (path === "/api/v1/knowledge-vault/recent") return json({ok: true, enabled: true, index_ready: true, items: []});
  if (path === "/api/v1/knowledge-vault/inbox") return json({ok: true, enabled: true, index_ready: true, count: 0, counts: [], items: []});
  if (path === "/api/v1/knowledge-vault/skills") return json({ok: true, enabled: true, index_ready: true, runtime_read: true, bound_project_ids: ["p1"], skills: []});
  if (path === "/api/v1/settings/knowledge-vault") return json(SETTINGS);
  if (path === "/api/v1/knowledge-vault/git/status") return json({ok: true, git: GIT});
  if (path.startsWith("/api/v1/")) return json({ok: true});
  return json({});
};
window.EventSource = class { constructor() {} close() {} addEventListener() {} };
`;

const html = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AHA2</title><link rel="stylesheet" href="/styles.css"></head><body><div id="app"></div><div id="app-status" class="visually-hidden" role="status" aria-live="polite"></div><script src="/fixture.js"></script><script type="module" src="/app.js"></script></body></html>`;
const mime = {".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon"};
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
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
    } else {
      if (!/^\/(?:vendor\/)?[a-z0-9_]+\.(?:js|css)$/.test(name)) { response.writeHead(404).end(); return; }
      const file = resolve(root, "web", "build", name.slice(1));
      response.setHeader("Content-Type", mime[name.slice(name.lastIndexOf("."))] || "application/octet-stream");
      response.end(await readFile(file));
    }
  } catch { response.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${server.address().port}`;

const intersects = (left, right, tolerance = 1) =>
  left.x < right.right - tolerance && right.x < left.right - tolerance &&
  left.y < right.bottom - tolerance && right.y < left.bottom - tolerance;

async function checkResources(page, width, height) {
  await page.goto(`${base}/#resources?tab=browser`, {waitUntil: "networkidle"});
  await page.waitForSelector(".browser-resources-view", {timeout: 15000});
  await page.waitForSelector(".hardware-resource-empty", {timeout: 15000});
  // Let the pet settle after the lazy route paints.
  await page.waitForTimeout(500);

  const layout = await page.evaluate(() => {
    const read = selector => {
      const element = document.querySelector(selector);
      if (!element) return null;
      const rect = element.getBoundingClientRect();
      return {x: rect.x, y: rect.y, width: rect.width, height: rect.height, right: rect.right, bottom: rect.bottom};
    };
    return {
      pageOverflow: document.documentElement.scrollWidth - innerWidth,
      toolbar: read(".resource-panel-toolbar"),
      title: read(".resource-panel-toolbar strong"),
      desc: read(".resource-panel-toolbar p"),
      actions: read(".browser-resource-toolbar-actions"),
      refresh: read("#refresh-browser-profiles"),
      add: read("#add-browser-profile"),
      emptyTitle: read(".hardware-resource-empty strong"),
      emptyText: read(".hardware-resource-empty > span"),
      emptyButton: read("[data-browser-profile-add-empty]"),
      pet: read("#assistant-pet"),
      nav: read(".bottom-nav"),
      navVisible: Boolean(document.querySelector(".bottom-nav") && getComputedStyle(document.querySelector(".bottom-nav")).display !== "none"),
    };
  });
  const label = `${width}x${height} resources`;
  assert.ok(layout.toolbar, `${label}: toolbar exists`);
  assert.ok(layout.pageOverflow <= 1, `${label}: no horizontal page overflow (got ${layout.pageOverflow})`);
  assert.ok(layout.toolbar.x >= 8, `${label}: page keeps horizontal padding (toolbar starts at ${layout.toolbar.x})`);

  const parts = {
    title: layout.title,
    desc: layout.desc,
    refresh: layout.refresh,
    add: layout.add,
    emptyTitle: layout.emptyTitle,
    emptyText: layout.emptyText,
    emptyButton: layout.emptyButton,
  };
  for (const [name, rect] of Object.entries(parts)) assert.ok(rect, `${label}: ${name} exists`);
  const names = Object.keys(parts);
  for (let left = 0; left < names.length; left++) {
    for (let right = left + 1; right < names.length; right++) {
      assert.equal(intersects(parts[names[left]], parts[names[right]]), false,
        `${label}: ${names[left]} must not overlap ${names[right]}: ${JSON.stringify(parts[names[left]])} vs ${JSON.stringify(parts[names[right]])}`);
    }
  }
  // The action row is a parent of refresh/add; compare it with the copy and
  // the empty state, not with its own children.
  for (const name of ["title", "desc", "emptyTitle", "emptyText", "emptyButton"]) {
    assert.equal(intersects(layout.actions, parts[name]), false, `${label}: action row must not overlap ${name}`);
  }
  assert.ok(layout.emptyButton.y >= layout.emptyText.bottom - 1,
    `${label}: empty-state CTA is its own row below the copy (button ${layout.emptyButton.y}, text bottom ${layout.emptyText.bottom})`);

  for (const name of ["title", "desc", "refresh", "add", "emptyTitle", "emptyText", "emptyButton"]) {
    assert.equal(intersects(layout.pet, parts[name]), false,
      `${label}: pet must not cover ${name}`);
  }
  if (layout.navVisible) {
    assert.ok(layout.pet.bottom <= layout.nav.y + 1,
      `${label}: pet docks above the bottom navigation (pet ${layout.pet.bottom}, nav ${layout.nav.y})`);
  }
  await page.screenshot({path: resolve(screenshots, `narrow-resources-${width}x${height}.png`)});
}

async function checkKnowledgeSettings(page, width, height) {
  await page.goto(`${base}/#knowledge`, {waitUntil: "networkidle"});
  await page.waitForSelector(".kv-tree-link", {timeout: 15000});
  await page.locator(".kv-settings-trigger").click();
  await page.waitForSelector("#kv-settings-dialog", {state: "visible"});
  await page.waitForSelector(".kv-autosync-toggle", {timeout: 10000});
  await page.waitForTimeout(200);

  const layout = await page.evaluate(() => {
    const read = selector => {
      const element = document.querySelector(selector);
      if (!element) return null;
      const rect = element.getBoundingClientRect();
      const style = getComputedStyle(element);
      return {x: rect.x, y: rect.y, width: rect.width, height: rect.height, right: rect.right, bottom: rect.bottom, display: style.display};
    };
    const dialog = document.querySelector("#kv-settings-dialog");
    return {
      pageOverflow: document.documentElement.scrollWidth - innerWidth,
      dialog: read("#kv-settings-dialog"),
      dialogScroll: {scrollWidth: dialog.scrollWidth, clientWidth: dialog.clientWidth},
      checkbox: read("#kv-auto-sync"),
      autoText: read(".kv-autosync-toggle > span"),
      close: read("#kv-settings-dialog header [data-kv-settings-close]"),
      footer: read("#kv-settings-dialog > footer"),
    };
  });
  const label = `${width}x${height} knowledge settings`;
  assert.ok(layout.dialog, `${label}: dialog exists`);
  assert.ok(layout.dialogScroll.scrollWidth - layout.dialogScroll.clientWidth <= 1,
    `${label}: dialog content does not overflow horizontally (${layout.dialogScroll.scrollWidth} vs ${layout.dialogScroll.clientWidth})`);
  assert.ok(layout.pageOverflow <= 1, `${label}: no horizontal page overflow`);
  assert.ok(layout.checkbox.width <= 20 && layout.checkbox.height <= 20,
    `${label}: auto-sync checkbox keeps its intrinsic size (${layout.checkbox.width}x${layout.checkbox.height})`);
  assert.ok(layout.autoText.right <= layout.dialog.right - 1,
    `${label}: auto-sync copy stays inside the dialog (text ${layout.autoText.right}, dialog ${layout.dialog.right})`);
  for (const name of ["close", "footer"]) {
    assert.ok(layout[name].y >= layout.dialog.y - 1 && layout[name].bottom <= layout.dialog.bottom + 1,
      `${label}: ${name} stays visible inside the dialog (${JSON.stringify(layout[name])}, dialog ${JSON.stringify(layout.dialog)})`);
  }
  await page.screenshot({path: resolve(screenshots, `narrow-knowledge-settings-${width}x${height}.png`)});
}

let browser;
try {
  browser = await chromium.launch({...launchOptions, headless: true});
  // The 768px band sits just above the 760px breakpoint: the toolbar is still
  // a flex row while the sidebar has not been dropped yet. Sample it too.
  for (const [width, height] of [[390, 844], [844, 390], [768, 1024], [1024, 768]]) {
    const page = await browser.newPage({viewport: {width, height}, locale: "zh-CN"});
    // Keep the pet parked and the dock deterministic for geometry checks.
    await page.emulateMedia({reducedMotion: "reduce"});
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await checkResources(page, width, height);
    await checkKnowledgeSettings(page, width, height);
    assert.deepEqual(errors, [], `${width}x${height}: no page errors`);
    await page.close();
  }
  console.log("narrow viewport layout verified");
} finally {
  await browser?.close();
  server.close();
}
