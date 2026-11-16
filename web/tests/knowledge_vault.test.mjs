// Phase 2 knowledge vault: editor URI generation, deep links, grouping and
// read-only rendering. The URI helpers are pure so these assertions do not
// depend on VSCode, Obsidian or any desktop environment being installed.
import test from "node:test";
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {resolve} from "node:path";
import {readFile} from "node:fs/promises";

const root = resolve(import.meta.dirname, "..");
const vault = await import(pathToFileURL(resolve(root, "build", "knowledge_vault.js")).href);
// The build rewrites shared imports to "./api.js?v=<build>"; importing the
// unversioned URL would create a second APIClient singleton, so stubs would
// never reach the page module.
const vaultSource = await readFile(resolve(root, "build", "knowledge_vault.js"), "utf8");
const apiVersion = vaultSource.match(/from "\.\/api\.js(\?v=[^"]+)"/)?.[1] || "";
const {api} = await import(pathToFileURL(resolve(root, "build", "api.js")).href + apiVersion);

function reset(patch = {}) {
  vault.__resetKnowledgeVaultStateForTest();
  vault.__setKnowledgeVaultStateForTest({
    initialized: true,
    loading: "ready",
    vaultReady: true,
    indexReady: true,
    vaultPath: "/srv/vault",
    obsidianVault: "AHA2 Knowledge",
    obsidianReady: true,
    // Loopback is the default fixture; remote tests opt out explicitly.
    openAvailable: true,
    libraries: [
      {id: "global", kind: "global", name: "全局知识", count: 2},
      {id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 3},
    ],
    projects: [{id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 3}],
    treeState: "ready",
    docState: "idle",
    recentState: "idle",
    inboxState: "idle",
    skillsState: "idle",
    ...patch,
  });
}

function context() {
  return {render: () => {}, setMessage: () => {}};
}

const doc = {
  id: "doc-1",
  title: "构建指南",
  path: "projects/p1/guides/build.md",
  relative_path: "projects/p1/guides/build.md",
  absolute_path: "/srv/vault/项目 知识/projects/p1/guides/build.md",
  scope: "project",
  project_id: "p1",
  library_id: "project:p1",
  library_name: "Demo",
  type: "practice",
  status: "verified",
  body: "# 构建\n\n运行构建。",
  tags: ["build"],
  modified_at: "2026-11-14T10:00:00Z",
  is_index: false,
  system: false,
  outline: [{level: 1, text: "构建", slug: "构建"}],
  backlinks: [],
  obsidian_ready: true,
  obsidian_file: "projects/p1/guides/build.md",
};

test("VSCode local URIs encode spaces and Chinese, and keep a Windows drive letter", () => {
  assert.equal(
    vault.vscodeLocalURI("/srv/vault/项目 文档/a b.md"),
    "vscode://file/srv/vault/%E9%A1%B9%E7%9B%AE%20%E6%96%87%E6%A1%A3/a%20b.md",
  );
  assert.equal(
    vault.vscodeLocalURI("C:\\知识库\\a b.md"),
    "vscode://file/C:/%E7%9F%A5%E8%AF%86%E5%BA%93/a%20b.md",
  );
  assert.equal(
    vault.vscodeLocalURI("C:\\Users\\Me\\notes.md"),
    "vscode://file/C:/Users/Me/notes.md",
  );
});


test("Obsidian URIs drop the .md suffix and encode the vault and nested file path", () => {
  assert.equal(
    vault.obsidianURI("AHA2 Knowledge", "projects/p1/guides/build.md"),
    "obsidian://open?vault=AHA2%20Knowledge&file=projects%2Fp1%2Fguides%2Fbuild",
  );
  assert.equal(
    vault.obsidianURI("我的 库", "global/index.md"),
    "obsidian://open?vault=%E6%88%91%E7%9A%84%20%E5%BA%93&file=global%2Findex",
  );
});

test("deep links carry id, scope and project and parse back", () => {
  const hash = vault.docHash({id: "doc-1", scope: "project", project_id: "p1"});
  assert.equal(hash, "#knowledge?doc=doc-1&scope=project&project=p1");
  assert.deepEqual(vault.parseDocHash(hash), {id: "doc-1", library: "project:p1", project: "p1"});
  assert.deepEqual(vault.parseDocHash(vault.docHash({id: "g1", scope: "global"})), {id: "g1", library: "global", project: ""});
  assert.deepEqual(vault.parseDocHash(vault.docHash({id: "a1", scope: "archive", project_id: "p1"})), {id: "a1", library: "archive:p1", project: "p1"});
  assert.deepEqual(vault.parseDocHash("#knowledge?doc=x"), {id: "x", library: "", project: ""});
  assert.deepEqual(vault.parseDocHash("#tasks?doc=x"), null);
});

test("query construction omits empty filters and encodes values", () => {
  assert.equal(vault.vaultQuery({q: "构建 指南", library: "", project: "p 1", tag: undefined, limit: 0}), "q=%E6%9E%84%E5%BB%BA+%E6%8C%87%E5%8D%97&project=p+1&limit=0");
});

test("highlight escapes markup and marks the first query occurrence", () => {
  assert.equal(vault.highlightVaultText("<b>Build</b> guide", "build"), "&lt;b&gt;<mark>Build</mark>&lt;/b&gt; guide");
  assert.equal(vault.highlightVaultText("plain", ""), "plain");
  assert.equal(vault.highlightVaultText("plain", "missing"), "plain");
});

test("recent items group by local day with today and yesterday labels", () => {
  const groups = vault.groupRecentByDay([
    {id: "a", title: "A", modified_at: "2026-11-14T09:00:00"},
    {id: "b", title: "B", modified_at: "2026-11-14T08:00:00"},
    {id: "c", title: "C", modified_at: "2026-11-13T23:00:00"},
    {id: "d", title: "D", modified_at: "2026-11-10T12:00:00"},
  ], new Date("2026-11-14T12:00:00"));
  assert.equal(groups.length, 3);
  assert.equal(groups[0].label, "今天");
  assert.deepEqual(groups[0].items.map(item => item.id), ["a", "b"]);
  assert.equal(groups[1].label, "昨天");
  assert.equal(groups[2].items[0].id, "d");
});

function treeFixture(id, title, path, children = [], extra = {}) {
  return {id, title, path, type: "navigation", status: "verified", scope: "global", is_index: true, system: true, children, ...extra};
}

test("the page exposes two top-level tabs, the fixed activity entry, and no edit or review controls", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "doc-1", title: "构建指南", path: "global/general/build.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
    ]),
  ], "global");
  reset({tree});
  const html = vault.renderKnowledgeVaultPage(context());
  for (const tab of ["knowledge", "skills"]) {
    assert.match(html, new RegExp(`data-kv-tab-button="${tab}"`));
  }
  assert.doesNotMatch(html, /data-kv-tab-button="(?:recent|inbox)"/, "recent/inbox tabs are folded into the activity page");
  assert.doesNotMatch(html, />项目知识<|>待审提案<|data-knowledge-section=/);
  assert.doesNotMatch(html, /data-kv-(edit|delete|move|approve)|>删除<|>编辑<|>待审提案</);
  assert.doesNotMatch(html, /系统索引/);
  assert.doesNotMatch(html, /全局知识首页/, "the library root index is not a folder row");
  assert.match(html, /<span class="kv-tree-title">general<\/span>/);
  assert.doesNotMatch(html, /<span class="kv-tree-title">inbox<\/span>/, "the browse tree never shows an inbox folder");
  assert.match(html, /<nav id="kv-tree-panel" class="kv-tree-panel" aria-label="目录">/, "tree is a nav, not a fake tree role");
  assert.doesNotMatch(html, /role="tree"/);
  assert.match(html, /data-kv-toggle="g-general"[^>]*aria-expanded="false"/);
  assert.doesNotMatch(html, /href="#knowledge\?doc=doc-1&amp;scope=global"/, "folder children start hidden");
  assert.equal((html.match(/class="kv-tab kv-tab-/g) || []).length, 2);
  // Heading, tabs and controls form one group; phones put the tabs and the two
  // controls on the same row beneath the title.
  assert.match(html, /<div class="kv-topbar-main">[\s\S]*class="kv-heading"[\s\S]*class="kv-tabs"[\s\S]*<div class="kv-controls">[\s\S]*data-kv-settings[\s\S]*<\/header>/);
  assert.equal((html.match(/data-kv-tab-button="/g) || []).length, 2, "no duplicate tab buttons in a more menu");
  assert.doesNotMatch(html, /data-kv-more|kv-more-trigger|kv-more-menu/);

  // The activity entry is a fixed first row, selected by default, and the
  // right pane is the activity page rather than the old blank "select a doc".
  assert.match(html, /<button type="button" class="kv-tree-link kv-activity-link selected" data-kv-activity aria-current="page">/);
  assert.equal((html.match(/data-kv-activity/g) || []).length, 1);
  assert.equal(html.indexOf("data-kv-activity") < html.indexOf('class="kv-tree-list"'), true, "the activity entry sits above the real directory list");
  assert.match(html, /<article class="kv-doc-panel kv-activity-panel">[\s\S]*id="kv-activity-title">动态与更新/);
  assert.ok(html.indexOf('id="kv-activity-inbox"') < html.indexOf('id="kv-activity-recent"'), "待整理 comes before 最近更新");
  assert.match(html, /id="kv-activity-inbox">待整理/);
  assert.match(html, /id="kv-activity-recent">最近更新/);
  assert.doesNotMatch(html, /从左侧选择文档/);
  assert.doesNotMatch(html, /class="kv-context-panel"/, "the activity page does not leave an empty context column");
});

test("the tree rebuilds real vault directories and uses path basenames, not index titles", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "g-deploy", title: "共享部署经验", path: "global/general/shared-deploy.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
      treeFixture("g-lessons", "Agent 经验教训", "global/agent-lessons/index.md", [
        treeFixture("g-behavior", "行为教训", "global/agent-lessons/behavior-lessons/index.md", []),
        treeFixture("g-technical", "技术诊断", "global/agent-lessons/technical-diagnostics/index.md", [
          {id: "g-serial", title: "串口登录等待提示", path: "global/agent-lessons/technical-diagnostics/serial-login.md", type: "diagnostic", status: "verified", scope: "global", is_index: false, system: false, children: []},
        ]),
      ]),
    ]),
  ], "global");
  assert.deepEqual(tree.map(node => node.title), ["agent-lessons", "general"]);
  const lessons = tree.find(node => node.title === "agent-lessons");
  assert.equal(lessons.id, "g-lessons");
  assert.equal(lessons.path, "global/agent-lessons");
  assert.equal(lessons.has_index, true);
  assert.deepEqual(lessons.children.map(node => node.title), ["behavior-lessons", "technical-diagnostics"]);
  const technical = lessons.children.find(node => node.title === "technical-diagnostics");
  assert.deepEqual(technical.children.map(node => node.title), ["串口登录等待提示"]);
  assert.equal(JSON.stringify(tree).includes("全局知识首页"), false, "the root index title never becomes a folder name");
  assert.equal(JSON.stringify(tree).includes("Agent 经验教训"), false, "index titles never become folder names");
  assert.equal(tree.some(node => node.title === "inbox"), false, "no empty inbox folder is injected");
});

test("project trees show theme directories and hide inbox documents", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("p-root", "Demo 索引", "projects/demo/index.md", [
      treeFixture("p-group", "导航组", "projects/demo/navigation-group/index.md", [
        {id: "p-module", title: "模块 A", path: "projects/demo/navigation-group/module-a.md", type: "reference", status: "verified", scope: "project", project_id: "p1", is_index: false, system: false, children: []},
      ], {scope: "project", project_id: "p1"}),
      treeFixture("p-inbox", "收件箱", "projects/demo/inbox/index.md", [
        {id: "p-idea", title: "收件箱一", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "p1", is_index: false, system: false, children: []},
      ], {scope: "project", project_id: "p1"}),
    ], {scope: "project", project_id: "p1"}),
  ], "project:p1");
  assert.deepEqual(tree.map(node => node.title), ["navigation-group"]);
  assert.equal(tree[0].path, "projects/demo/navigation-group");
  assert.equal(tree[0].id, "p-group");
  assert.equal(tree[0].has_index, true);
  assert.equal(JSON.stringify(tree).includes("inbox"), false);
  assert.equal(JSON.stringify(tree).includes("收件箱一"), false);
});

test("directory titles open their index and never repeat index.md as a leaf", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "g-deploy", title: "共享部署经验", path: "global/general/shared-deploy.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
        {id: "g-extra", title: "索引叶子", path: "global/general/index.md", type: "reference", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
    ]),
  ], "global");
  reset({tree, expanded: new Set(["g-general"])});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.equal((html.match(/data-kv-doc="g-general"/g) || []).length, 0, "the index is opened from the directory title, not a document row");
  assert.match(html, /data-kv-title-open="g-general"/);
  assert.doesNotMatch(html, /data-kv-doc="g-extra"/, "a duplicate index.md entry is not a leaf");
  assert.match(html, /<span class="kv-tree-title">general<\/span>/);
  assert.doesNotMatch(html, /kv-badge-index/);
  const folderRow = html.match(/<div class="kv-tree-row kv-tree-row-toggle" data-kv-row-toggle="g-general">[\s\S]*?<\/div>/)?.[0] || "";
  assert.equal(folderRow.includes("kv-badge"), false, "directory rows do not carry index or navigation badges");
  assert.match(html, /kv-tree-folder-icon/, "directories use a folder icon");
  assert.match(html, /kv-tree-doc-icon/, "documents use a document icon");
  assert.match(html, /kv-badge-type[^>]*>实践</, "leaf documents keep their type badge");
});

test("titles open the index and toggle, chevrons only toggle, and selected titles stay clickable", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-full", "完整目录", "global/full/index.md", [
        treeFixture("g-sub", "子目录", "global/full/sub/index.md", []),
        {id: "g-leaf", title: "同级文档", path: "global/full/leaf.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
      treeFixture("g-empty", "只有索引", "global/empty/index.md", []),
    ]),
  ], "global");
  reset({tree, expanded: new Set(["g-full"]), selectedID: "g-full"});
  const html = vault.renderKnowledgeVaultPage(context());

  // The leftmost chevron toggles only. The folder title is a button that opens
  // the index and toggles the branch, and it carries the selected pill even
  // while the branch is open.
  const fullRow = html.match(/<div class="kv-tree-row kv-tree-row-toggle" data-kv-row-toggle="g-full">[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(fullRow, /<button type="button" class="kv-tree-toggle" data-kv-toggle="g-full" aria-expanded="true"[^>]*><svg[^>]*><path d="m6 9 6 6 6-6"\/>/);
  assert.match(fullRow, /<span class="kv-tree-folder-icon" aria-hidden="true">/);
  assert.match(fullRow, /<button type="button" class="kv-tree-link kv-tree-folder-link kv-tree-folder-title selected" data-kv-folder-title="g-full" data-kv-title-open="g-full" data-kv-title-toggle="g-full" aria-current="page"><span class="kv-tree-title">full<\/span><\/button>/);
  assert.doesNotMatch(fullRow, /kv-tree-folder-title[^>]*data-kv-doc=/);
  assert.doesNotMatch(fullRow, /data-kv-open-index|kv-tree-open-index/);
  assert.equal(fullRow.indexOf("data-kv-toggle=\"g-full\"") < fullRow.indexOf("kv-tree-folder-icon"), true, "the chevron precedes the folder icon");
  assert.equal(fullRow.indexOf("kv-tree-folder-icon") < fullRow.indexOf("data-kv-title-open=\"g-full\""), true, "the title follows the icons");

  // A folder with an index but no children still opens it from the title.
  const emptyRow = html.match(/<div class="kv-tree-row">\s*<span class="kv-tree-toggle-space"[\s\S]*?data-kv-title-open="g-empty"[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(emptyRow, /kv-tree-toggle-space/);
  assert.match(emptyRow, /<button type="button" class="kv-tree-link kv-tree-folder-link kv-tree-folder-title" data-kv-folder-title="g-empty" data-kv-title-open="g-empty"><span class="kv-tree-title">empty<\/span><\/button>/);
  assert.doesNotMatch(emptyRow, /data-kv-toggle="/);
  assert.doesNotMatch(emptyRow, /data-kv-row-toggle/);

  // Document rows share the chevron slot and icon width with folder rows.
  const docRow = html.match(/<li class="kv-tree-node kv-tree-doc" style="--kv-depth:1"><a[\s\S]*?<\/a><\/li>/)?.[0] || "";
  assert.match(docRow, /<span class="kv-tree-toggle-space" aria-hidden="true"><\/span><span class="kv-tree-doc-icon" aria-hidden="true"><svg[^>]*><path d="M6 2h8l4 4v16H6z"\/>/);
  assert.match(docRow, /<span class="kv-tree-title">同级文档<\/span>/);
  assert.match(html, /<li class="kv-tree-node kv-tree-folder" style="--kv-depth:1">/);
});

test("the sidebar defaults to 270px, collapses to a rail and exposes a 6px separator", async () => {
  reset({});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /style="--kv-tree-width:270px;--kv-resizer-width:6px" data-kv-sidebar-collapsed="false"/);
  assert.match(html, /class="kv-tree-resizer" role="separator" aria-orientation="vertical" tabindex="0"[^>]*aria-valuemin="220" aria-valuemax="460" aria-valuenow="270"/);
  assert.match(html, /data-kv-sidebar-toggle aria-expanded="true" aria-controls="kv-tree-panel" aria-label="收起目录栏"/);
  assert.match(html, /data-kv-sidebar-toggle[^>]*>[^<]*<svg[^>]*><path d="m15 18-6-6 6-6"\/>/, "expanded sidebar shows a collapse chevron");

  reset({sidebarCollapsed: true});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /style="--kv-tree-width:48px;--kv-resizer-width:0px" data-kv-sidebar-collapsed="true"/);
  assert.match(html, /class="kv-tree-panel collapsed"/);
  assert.match(html, /data-kv-sidebar-toggle aria-expanded="false" aria-controls="kv-tree-panel" aria-label="展开目录栏"/);
  assert.match(html, /data-kv-sidebar-toggle[^>]*>[^<]*<svg[^>]*><path d="m9 18 6-6-6-6"\/>/, "collapsed rail shows the expand chevron");
  assert.doesNotMatch(html, /kv-tree-resizer/);

  // Route-only styles travel in the lazy knowledge chunk, not styles.css.
  const css = await readFile(resolve(root, "src", "knowledge_vault.ts"), "utf8");
  assert.match(css, /\.kv-tree-list \.kv-tree-list \{ padding-left: 12px; \}/);
  assert.match(css, /\.kv-tree-resizer \{[^}]*width: 6px/);
  // The resize guide line is always visible and only darkens on hover/focus.
  assert.match(css, /\.kv-tree-resizer::after \{[^}]*background: var\(--color-border-strong\); opacity: 1;/);
  assert.match(css, /\.kv-tree-resizer:hover::after, \.kv-tree-resizer:focus-visible::after \{ background: var\(--color-text-muted\); opacity: 1; \}/);
  // Phones hide the collapse rail but keep the file/tree mode toggle.
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*\.kv-sidebar-toggle\[data-kv-sidebar-toggle\], \.kv-tree-resizer \{ display: none; \}/);
  assert.match(css, /\.kv-tree-panel\.collapsed \.kv-panel-tools,\s*\.kv-tree-panel\.collapsed \.kv-files-mode \{ display: none; \}/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*\.kv-tree-panel\.collapsed \.kv-tree-body \{ display: block; \}/);
  // The files-mode switch is a labeled pill, not a second square collapse
  // icon: it must not borrow the collapse button's class.
  const expandedHead = html.slice(html.indexOf('<div class="kv-panel-head">'), html.indexOf('<div class="kv-panel-tools">'));
  assert.match(expandedHead, /class="kv-files-mode" data-kv-files-toggle aria-pressed="false"/);
  assert.doesNotMatch(expandedHead, /class="kv-sidebar-toggle icon-button" data-kv-files-toggle/);
  assert.match(expandedHead, /<span>文件模式<\/span>/);
  assert.equal((expandedHead.match(/data-kv-sidebar-toggle/g) || []).length, 1, "one collapse control only");
  // Collapsed pages omit the resizer element, so the grid drops its track as
  // well; otherwise the reader would be laid into the 0px resizer column.
  assert.match(css, /@media \(min-width: 1181px\) \{\s*\.knowledge-vault\[data-kv-sidebar-collapsed="true"\] \.kv-workspace \{ grid-template-columns: var\(--kv-tree-width\) minmax\(0, 1fr\) var\(--kv-context-width\); \}/);
  assert.match(css, /@media \(min-width: 761px\) and \(max-width: 1180px\) \{\s*\.knowledge-vault\[data-kv-sidebar-collapsed="true"\] \.kv-workspace \{ grid-template-columns: var\(--kv-tree-width\) minmax\(0, 1fr\); \}/);
});

test("the mixed tree roots at the real global and projects directories", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "g-doc", title: "通用文档", path: "global/general/doc.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
    ]),
    treeFixture("p-root", "Demo 索引", "projects/demo/index.md", [
      treeFixture("p-guides", "指南", "projects/demo/guides/index.md", [], {scope: "project", project_id: "demo"}),
      {id: "p-idea", title: "项目想法", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "demo", is_index: false, system: false, children: []},
    ], {scope: "project", project_id: "demo"}),
  ], "all");

  assert.deepEqual(tree.map(node => node.title), ["global", "projects"]);
  const global = tree[0];
  const projects = tree[1];
  assert.equal(global.path, "global");
  assert.equal(global.id, "g-root", "the global root index is the folder row");
  assert.equal(projects.path, "projects");
  assert.equal(projects.has_index, false, "the projects container is a plain directory");
  const demo = projects.children.find(node => node.title === "demo");
  assert.equal(demo.id, "p-root");
  assert.equal(demo.has_index, true);
  assert.deepEqual(demo.children.map(node => node.title), ["guides"]);
  assert.equal(JSON.stringify(tree).includes("inbox"), false, "inbox paths never reach the mixed tree");

  reset({tree});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-toggle="g-root"[^>]*aria-expanded="false"/);
  assert.match(html, /data-kv-toggle="vault-dir:projects"[^>]*aria-expanded="false"/);
  assert.doesNotMatch(html, /data-kv-doc="g-general"/, "the global directory starts collapsed");
  assert.doesNotMatch(html, /data-kv-doc="p-root"/, "the projects directory starts collapsed");

  reset({tree, expanded: new Set(["vault-dir:projects", "p-root"])});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-toggle="p-root"[^>]*aria-expanded="true"/);
  assert.match(html, /<span class="kv-tree-title">guides<\/span>/);
  assert.doesNotMatch(html, /<span class="kv-tree-title">inbox<\/span>/);
});

test("container navigation pages make projects and archive titles clickable", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", []),
    ]),
    treeFixture("nav-projects", "项目知识", "projects/index.md", [
      treeFixture("p-root", "Demo 索引", "projects/demo/index.md", [], {scope: "project", project_id: "demo"}),
    ], {scope: ""}),
    treeFixture("nav-archive", "归档知识", "archive/index.md", [], {scope: ""}),
    treeFixture("nav-archive-projects", "归档项目", "archive/projects/index.md", [], {scope: ""}),
  ], "all");

  assert.deepEqual(tree.map(node => node.title), ["archive", "global", "projects"]);
  const archive = tree.find(node => node.title === "archive");
  const projects = tree.find(node => node.title === "projects");
  assert.equal(projects.path, "projects");
  assert.equal(projects.id, "nav-projects", "the container page is the folder row");
  assert.equal(projects.has_index, true, "the container navigation page makes the folder clickable");
  assert.deepEqual(projects.children.map(node => node.title), ["demo"], "the navigation page is not repeated as a leaf");
  assert.equal(archive.has_index, true);
  const nested = archive.children.find(node => node.title === "projects");
  assert.equal(nested.has_index, true, "archive/projects is its own clickable container");
  assert.equal(nested.children.length, 0);

  reset({tree, expanded: new Set(["nav-archive"])});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-title-open="nav-projects"/);
  assert.match(html, /data-kv-title-toggle="nav-projects"/, "the folder still expands and collapses");
  assert.match(html, /data-kv-title-open="nav-archive"/);
  assert.match(html, /data-kv-title-open="nav-archive-projects"/);
  assert.doesNotMatch(html, /data-kv-doc="nav-projects"/, "the navigation page never becomes a file leaf");
  assert.doesNotMatch(html, /data-kv-doc="nav-archive"/);
  assert.doesNotMatch(html, /<span class="kv-tree-title">index<\/span>/);

  // A folder whose container page is missing stays a plain, non-clickable row.
  reset({tree: vault.buildVaultDirectoryTree([
    treeFixture("p-root", "Demo 索引", "projects/demo/index.md", [], {scope: "project", project_id: "demo"}),
  ], "all")});
  html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /data-kv-title-open="vault-dir:projects"/);
});

test("inbox path segments are filtered from global, project and archive trees", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "g-doc", title: "通用文档", path: "global/general/doc.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
      treeFixture("g-inbox", "收件箱", "global/inbox/index.md", [
        {id: "g-inbox-doc", title: "全局想法", path: "global/inbox/idea.md", type: "practice", status: "unverified", scope: "global", is_index: false, system: false, children: []},
      ]),
    ]),
    treeFixture("p-root", "Demo 索引", "projects/demo/index.md", [
      treeFixture("p-inbox", "收件箱", "projects/demo/inbox/index.md", [
        {id: "p-inbox-doc", title: "项目想法", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "demo", is_index: false, system: false, children: []},
      ], {scope: "project", project_id: "demo"}),
      // Only the exact segment is reserved: a directory that merely starts
      // with "inbox" stays browsable.
      treeFixture("p-notes", "收件箱笔记", "projects/demo/inbox-notes/index.md", [
        {id: "p-note", title: "笔记一", path: "projects/demo/inbox-notes/keep.md", type: "practice", status: "verified", scope: "project", project_id: "demo", is_index: false, system: false, children: []},
      ], {scope: "project", project_id: "demo"}),
    ], {scope: "project", project_id: "demo"}),
    treeFixture("a-root", "归档", "archive/aha1-archive/index.md", [
      treeFixture("a-inbox", "收件箱", "archive/aha1-archive/inbox/index.md", [
        {id: "a-inbox-doc", title: "归档想法", path: "archive/aha1-archive/inbox/legacy.md", type: "practice", status: "unverified", scope: "archive", is_index: false, system: false, children: []},
      ], {scope: "archive"}),
      treeFixture("a-notes", "笔记", "archive/aha1-archive/notes/index.md", [
        {id: "a-note", title: "归档笔记", path: "archive/aha1-archive/notes/keep.md", type: "practice", status: "verified", scope: "archive", is_index: false, system: false, children: []},
      ], {scope: "archive"}),
    ], {scope: "archive"}),
  ], "all");

  const paths = [];
  const walk = nodes => nodes.forEach(node => {
    paths.push(node.path);
    walk(node.children || []);
  });
  walk(tree);
  assert.equal(paths.some(relative => relative.split("/").some(segment => segment.toLowerCase() === "inbox")), false, "no path segment may be inbox");
  assert.equal(paths.includes("global/general/doc.md"), true, "ordinary global documents remain");
  assert.equal(paths.includes("projects/demo/inbox-notes/keep.md"), true, "only the exact inbox segment is filtered");
  assert.equal(paths.includes("archive/aha1-archive/notes/keep.md"), true, "ordinary archive documents remain");
  const serialized = JSON.stringify(tree);
  assert.equal(serialized.includes("全局想法"), false);
  assert.equal(serialized.includes("项目想法"), false);
  assert.equal(serialized.includes("归档想法"), false);

  // Inbox documents stay reachable through the activity page's 待整理 block,
  // which renders its own list independently of the tree.
  reset({inboxState: "ready", inbox: {count: 2, counts: [], items: [
    {id: "p-inbox-doc", title: "项目想法", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "demo", library_id: "project:demo", library: "Demo", modified_at: "2026-11-14T10:00:00Z"},
    {id: "g-inbox-doc", title: "全局想法", path: "global/inbox/idea.md", type: "practice", status: "unverified", scope: "global", library_id: "global", library: "全局知识", modified_at: "2026-11-14T10:00:00Z"},
  ]}});
  const inboxHTML = vault.renderKnowledgeVaultPage(context());
  assert.match(inboxHTML, /2 条待整理/);
  assert.match(inboxHTML, /projects\/demo\/inbox\/idea\.md/);
  assert.match(inboxHTML, /global\/inbox\/idea\.md/);
});

test("the topbar no longer duplicates scope in library and project dropdowns", () => {
  reset({});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /id="kv-library"/);
  assert.doesNotMatch(html, /id="kv-project"/);
  assert.doesNotMatch(html, /kv-scope-select/);
  assert.match(html, /id="kv-search-scope"/);
  assert.match(html, /<option value="all" selected>全部<\/option>/);
  assert.match(html, /<option value="project">当前项目<\/option>/);
  assert.match(html, /<option value="directory">当前目录<\/option>/);
});

test("recent and inbox default to all libraries and keep project/path context", () => {
  reset({
    recentState: "ready",
    recent: [{
      id: "r1", title: "最近想法", path: "projects/demo/inbox/idea.md", type: "practice",
      status: "unverified", scope: "project", project_id: "demo", library_id: "project:demo",
      library: "Demo", modified_at: "2026-11-14T10:00:00Z",
    }],
  });
  const recent = vault.renderKnowledgeVaultPage(context());
  assert.match(recent, /projects\/demo\/inbox\/idea\.md/);
  assert.match(recent, /Demo/);
});

test("the activity page groups the inbox by library and keeps both blocks visible", () => {
  reset({
    recentState: "ready",
    recent: [
      {id: "r1", title: "最近想法", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "demo", library_id: "project:demo", library: "Demo", modified_at: "2026-11-14T10:00:00Z"},
      {id: "r2", title: "全局指南", path: "global/guides/guide.md", type: "reference", status: "verified", scope: "global", library_id: "global", library: "全局知识", modified_at: "2026-11-13T10:00:00Z"},
    ],
    inboxState: "ready",
    inbox: {
      ok: true, enabled: true, index_ready: true, count: 3,
      counts: [{library_id: "global", library: "全局知识", count: 1}, {library_id: "project:demo", library: "Demo", project_id: "demo", count: 2}],
      items: [
        {id: "i1", title: "项目想法", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "demo", library_id: "project:demo", library: "Demo", modified_at: "2026-11-14T10:00:00Z"},
        {id: "i2", title: "全局想法", path: "global/inbox/idea.md", type: "practice", status: "unverified", scope: "global", library_id: "global", library: "全局知识", modified_at: "2026-11-14T11:00:00Z"},
      ],
    },
  });
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /class="kv-activity"/);
  assert.equal((html.match(/class="kv-activity-block"/g) || []).length, 2, "recent and inbox are the two activity blocks");
  assert.ok(html.indexOf('aria-labelledby="kv-activity-inbox"') < html.indexOf('aria-labelledby="kv-activity-recent"'), "the inbox block renders before the recent block");
  assert.match(html, /class="kv-feed"[\s\S]*最近想法[\s\S]*全局指南/);
  assert.match(html, /3 条待整理/);
  assert.equal((html.match(/class="kv-inbox-group"/g) || []).length, 2, "inbox items are grouped by library");
  assert.equal(html.indexOf('data-kv-inbox-library="global"') < html.indexOf('data-kv-inbox-library="project:demo"'), true, "group order follows the API's counts order");
  assert.match(html, /<h3>全局知识<\/h3><span class="kv-chip kv-chip-static">1 条<\/span>/);
  assert.match(html, /<h3>Demo<\/h3><span class="kv-chip kv-chip-static">2 条<\/span>/);
  assert.match(html, /href="#knowledge\?doc=i1&amp;scope=project&amp;project=demo"[^>]*data-kv-doc="i1"/);
  assert.doesNotMatch(html, /data-kv-(move|archive|delete|edit)/);

  reset({recentState: "ready", recent: [], inboxState: "ready", inbox: {count: 0, counts: [], items: []}});
  const empty = vault.renderKnowledgeVaultPage(context());
  assert.match(empty, /最近没有变更/);
  assert.match(empty, /待整理为空/);
  assert.match(empty, /id="kv-activity-title">动态与更新/);
});

test("recent rows open an in-page preview and only 打开原文 links to the reader", async () => {
  const recent = {
    id: "r1", title: "最近想法", path: "projects/demo/inbox/idea.md", type: "practice",
    status: "unverified", scope: "project", project_id: "p1", library_id: "project:p1",
    library: "Demo", modified_at: "2026-11-14T10:00:00Z",
  };
  reset({recentState: "ready", recent: [recent]});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /<a class="kv-feed-item"[^>]*data-kv-preview="r1"[^>]*aria-haspopup="dialog"/);
  assert.doesNotMatch(html, /class="kv-feed-item"[^>]*data-kv-doc="r1"/, "a recent row is not a direct document link");
  assert.doesNotMatch(html, /data-kv-preview-panel/, "the preview only exists while open");

  reset({recentState: "ready", recent: [recent], previewID: "r1", previewState: "ready", previewDoc: doc});
  const preview = vault.renderKnowledgeVaultPage(context());
  assert.match(preview, /<section class="kv-preview-panel" data-kv-preview-panel role="dialog" aria-modal="false" aria-labelledby="kv-preview-title">/);
  assert.match(preview, /id="kv-preview-title">构建指南/);
  assert.match(preview, /projects\/p1\/guides\/build\.md/);
  assert.match(preview, /class="markdown-body kv-preview-body"/, "the preview renders the fetched body");
  assert.match(preview, /data-kv-preview-close/, "the preview has an explicit close control");
  assert.match(preview, /class="kv-preview-open" href="#knowledge\?doc=doc-1&amp;scope=project&amp;project=p1" data-kv-doc="doc-1"/, "only this link keeps the reader route");
  assert.doesNotMatch(preview, /data-kv-doc="r1"/);

  reset({recentState: "ready", recent: [recent], previewID: "r1", previewState: "error", previewError: "读取预览失败"});
  const failed = vault.renderKnowledgeVaultPage(context());
  assert.match(failed, /读取预览失败/);
  assert.match(failed, /data-kv-preview-retry/, "a failed preview retries itself, not another panel");
  assert.match(failed, /data-kv-doc="r1"|data-kv-doc="doc-1"/, "the original document stays reachable after a preview failure");

  const source = await readFile(resolve(root, "src", "knowledge_vault.ts"), "utf8");
  assert.match(source, /\.kv-preview-panel \{[^}]*position: fixed; z-index: 60/);
  assert.match(source, /@media \(max-width: 760px\) \{[\s\S]*?\.kv-preview-panel \{[\s\S]*?height: calc\(100dvh - var\(--mobile-bottom-nav-height\)\)/);
});

test("recent preview defaults to a unified diff and keeps a full-text switch", async () => {
  const recent = {
    id: "r1", title: "最近想法", path: "projects/demo/inbox/idea.md", type: "practice",
    status: "unverified", scope: "project", project_id: "p1", library_id: "project:p1",
    library: "Demo", modified_at: "2026-11-14T10:00:00Z",
  };
  const diff = {
    available: true, baseline: "uncommitted", truncated: false, added: 1, removed: 1,
    lines: [
      {kind: "hunk", text: "@@ -1,3 +1,3 @@", old_no: 0, new_no: 0, old_start: 1, old_lines: 3, new_start: 1, new_lines: 3},
      {kind: "context", text: "# 构建", old_no: 1, new_no: 1},
      {kind: "remove", text: "旧构建命令", old_no: 2, new_no: 0},
      {kind: "add", text: "<新构建命令>", old_no: 0, new_no: 2},
      {kind: "context", text: "", old_no: 3, new_no: 3},
    ],
  };
  reset({
    recentState: "ready", recent: [recent], previewID: "r1", previewState: "ready", previewDoc: doc,
    previewTab: "diff", previewDiffState: "ready", previewDiff: diff,
  });
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-preview-tab="diff" aria-pressed="true"/);
  assert.match(html, /data-kv-preview-tab="full" aria-pressed="false"/);
  assert.match(html, /class="kv-preview-body kv-diff-body"/, "the change view is the default");
  assert.match(html, /class="kv-diff-meta"><span>未提交改动<\/span><span class="kv-diff-count">\+1 −1<\/span>/);
  assert.match(html, /kv-diff-hunk[^>]*><span class="kv-diff-no"><\/span><span class="kv-diff-no"><\/span><span class="kv-diff-sign"><\/span><code>@@ -1,3 \+1,3 @@<\/code>/);
  assert.match(html, /kv-diff-remove[^>]*><span class="kv-diff-no">2<\/span><span class="kv-diff-no"><\/span><span class="kv-diff-sign" aria-hidden="true">-<\/span><code>旧构建命令<\/code>/);
  assert.match(html, /kv-diff-add[^>]*><span class="kv-diff-no"><\/span><span class="kv-diff-no">2<\/span><span class="kv-diff-sign" aria-hidden="true">\+<\/span><code>&lt;新构建命令&gt;<\/code>/);
  assert.match(html, /kv-diff-context[^>]*><span class="kv-diff-no">1<\/span><span class="kv-diff-no">1<\/span>/, "context rows keep both line numbers");
  assert.doesNotMatch(html, /class="markdown-body kv-preview-body"/);
  assert.match(html, /class="kv-preview-open"/, "打开原文 survives the change view");

  reset({
    recentState: "ready", recent: [recent], previewID: "r1", previewState: "ready", previewDoc: doc,
    previewTab: "full", previewDiffState: "ready", previewDiff: diff,
  });
  const full = vault.renderKnowledgeVaultPage(context());
  assert.match(full, /class="markdown-body kv-preview-body"/, "the full-text tab still renders markdown");
  assert.doesNotMatch(full, /class="kv-diff"/);
  assert.match(full, /data-kv-preview-tab="full" aria-pressed="true"/);
});

test("recent preview explains committed, empty and unavailable diffs", async () => {
  const recent = {
    id: "r1", title: "最近想法", path: "projects/demo/inbox/idea.md", type: "practice",
    status: "unverified", scope: "project", project_id: "p1", library_id: "project:p1",
    library: "Demo", modified_at: "2026-11-14T10:00:00Z",
  };
  reset({
    recentState: "ready", recent: [recent], previewID: "r1", previewState: "ready", previewDoc: doc,
    previewTab: "diff", previewDiffState: "ready",
    previewDiff: {
      available: true, baseline: "0123456789abcdef0123456789abcdef01234567",
      commit: "0123456789abcdef0123456789abcdef01234567",
      committed_at: "2026-11-14T10:00:00Z", truncated: true, added: 3, removed: 0,
      lines: [{kind: "hunk", text: "@@ -1 +1,2 @@", old_no: 0, new_no: 0, old_start: 1, old_lines: 1, new_start: 1, new_lines: 2}],
    },
  });
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /<span>提交 01234567<\/span>/);
  assert.match(html, /变更过长，仅显示前 400 行/);
  assert.match(html, /\+3 −0/);

  reset({
    recentState: "ready", recent: [recent], previewID: "r1", previewState: "ready", previewDoc: doc,
    previewTab: "diff", previewDiffState: "ready",
    previewDiff: {available: true, baseline: "uncommitted", truncated: false, added: 0, removed: 0, lines: []},
  });
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /与基线相同，没有可显示的差异。/);
  assert.doesNotMatch(html, /class="kv-diff"/);

  reset({
    recentState: "ready", recent: [recent], previewID: "r1", previewState: "ready", previewDoc: doc,
    previewTab: "full", previewDiffState: "ready",
    previewDiff: {available: false, reason: "知识库不是 git 仓库", truncated: false, added: 0, removed: 0, lines: []},
  });
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /无法显示变更：知识库不是 git 仓库/, "the fallback keeps the reason visible");
  assert.match(html, /class="kv-preview-body"><p class="kv-preview-notice">[\s\S]*class="markdown-body"/, "the unavailable diff falls back to the full body");
  assert.doesNotMatch(html, /class="kv-diff"/);
  assert.match(html, /data-kv-preview-tab="full" aria-pressed="true"/);

  const source = await readFile(resolve(root, "src", "knowledge_vault.ts"), "utf8");
  assert.match(source, /if \(!state\.previewDiff\.available\) state\.previewTab = "full";/, "an unavailable diff switches the tab automatically");
  assert.match(source, /@media \(max-width: 760px\) \{[\s\S]*?\.kv-diff-row \{ grid-template-columns: 34px 34px 16px minmax\(0, 1fr\); \}/);
  assert.match(source, /\.kv-diff-row code \{[^}]*white-space: pre-wrap; overflow-wrap: anywhere;/, "long diff lines wrap on phones instead of widening the sheet");
});

test("folders default to collapsed and only an explicit expansion shows children", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("folder", "guides", "global/guides/index.md", [
        treeFixture("sub", "advanced", "global/guides/advanced/index.md", [
          {id: "deep", title: "深入指南", path: "global/guides/advanced/deep.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
        ]),
      ]),
    ]),
  ], "global");
  reset({tree});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-toggle="folder"[^>]*aria-expanded="false"/);
  assert.doesNotMatch(html, /data-kv-toggle="sub"/);
  assert.doesNotMatch(html, /data-kv-doc="deep"/);
  assert.deepEqual(vault.vaultTreeAncestors(tree, "deep"), ["folder", "sub"]);
  assert.deepEqual(vault.vaultTreeAncestors(tree, "folder"), []);
  assert.equal(vault.vaultTreeAncestors(tree, "missing"), null);

  reset({tree, expanded: new Set(["folder"])});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-toggle="folder"[^>]*aria-expanded="true"/);
  assert.match(html, /data-kv-toggle="sub"[^>]*aria-expanded="false"/);
  assert.doesNotMatch(html, /data-kv-doc="deep"/);

  reset({tree, expanded: new Set(["folder", "sub"])});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-toggle="sub"[^>]*aria-expanded="true"/);
  assert.match(html, /data-kv-doc="deep"/);
  assert.match(html, /href="#knowledge\?doc=deep&amp;scope=global"/, "tree rows are real anchors");
});

test("topbar CSS puts tabs, search and settings on one phone row and shows both on desktop", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  const source = await readFile(resolve(root, "src", "knowledge_vault.ts"), "utf8");
  assert.match(css, /\.kv-topbar-main \{[^}]*flex: 1 1 auto/);
  assert.match(css, /\.kv-tabs \{[^}]*flex-wrap: nowrap/);
  assert.match(css, /\.kv-tabs \{[^}]*overflow-x: auto/);
  assert.match(css, /\.kv-controls \{[^}]*align-items: center/);
  // The phone row lives in the lazy route chunk: title on its own line, then
  // tabs + search + settings with 40px touch targets.
  assert.match(source, /@media \(max-width: 760px\) \{[\s\S]*?\.kv-topbar-main \{ flex-direction: row; flex-wrap: wrap; align-items: center;/);
  assert.match(source, /\.kv-heading \{ flex: 1 1 100%; \}/);
  assert.match(source, /\.kv-tabs \{ flex: 1 1 auto; width: auto; \}/);
  assert.match(source, /\.kv-controls \{ order: 0; flex: 0 0 auto; justify-content: flex-end; margin-left: 0;/);
  assert.match(source, /\.kv-tab \{ min-height: 40px; \}/);
  assert.match(source, /\.kv-search-trigger, \.kv-settings-trigger \{ width: 40px; min-height: 40px; height: 40px;/);
  // The old mobile "more" affordance is gone; skills and settings are never
  // hidden at the phone breakpoint.
  assert.doesNotMatch(css, /\.kv-more-(?:trigger|menu)\b/);
  assert.doesNotMatch(css, /\.kv-tab-skills \{ display: none/);
  assert.doesNotMatch(css, /\.kv-settings-trigger \{ display: none/);
});

test("document and skill actions are compact icon controls on every viewport", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  assert.match(css, /\.kv-doc-toolbar \{[^}]*display: flex[^}]*justify-content: space-between/);
  assert.match(css, /\.kv-open \{[^}]*width: 31px; height: 31px[^}]*padding: 0/);
  assert.match(css, /\.kv-open-actions\.compact \.kv-open \{ width: 29px; height: 29px; \}/);
  assert.match(css, /\.kv-open:disabled \{[^}]*opacity: \.45/);
  assert.match(css, /\.kv-skill-actions \{[^}]*justify-content: flex-end/);
  assert.match(css, /@media \(max-width: 760px\) \{[\s\S]*?\.kv-doc-actions \.kv-open \{ width: 28px; height: 28px; \}/);
  assert.doesNotMatch(css, /\.kv-open-desktop/);
  assert.doesNotMatch(css, /\.kv-doc-tools/);
});

test("document header keeps three icon actions on the breadcrumb row and drops remote VSCode", () => {
  reset({selectedID: "doc-1", docState: "ready", doc});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /href="vscode:\/\/file\/srv\/vault\/%E9%A1%B9%E7%9B%AE%20%E7%9F%A5%E8%AF%86\/projects\/p1\/guides\/build\.md"/);
  assert.match(html, /href="obsidian:\/\/open\?vault=AHA2%20Knowledge&amp;file=projects%2Fp1%2Fguides%2Fbuild"/);
  assert.match(html, /data-kv-copy="\/srv\/vault\/项目 知识\/projects\/p1\/guides\/build\.md"/);
  assert.doesNotMatch(html, /vscode-remote/);
  assert.doesNotMatch(html, /VSCode 远程/);
  assert.doesNotMatch(html, /kv-open-desktop/);
  // The toolbar carries the breadcrumb and the three icon actions; the title
  // sits in the next block on its own line.
  assert.match(html, /<div class="kv-doc-toolbar">\s*<nav class="kv-breadcrumb"[\s\S]*?<div class="kv-doc-actions">[\s\S]*?<div class="kv-open-actions">[\s\S]*?<\/div>\s*<\/div>\s*<div class="kv-doc-head">\s*<h1>/);
  const toolbar = html.slice(html.indexOf('<div class="kv-doc-toolbar">'), html.indexOf('<div class="kv-doc-head">'));
  assert.equal((toolbar.match(/class="kv-open(?: kv-copy)?"/g) || []).length, 3, "exactly three icon actions");
  assert.match(toolbar, /aria-label="在本地 VSCode 打开"/);
  assert.match(toolbar, /aria-label="在 Obsidian 打开"/);
  assert.match(toolbar, /aria-label="复制文档路径"/);
  assert.doesNotMatch(toolbar, /<span>(?:在本地 VSCode 打开|在 Obsidian 打开|复制文档路径)<\/span>/);
  assert.match(html, /有帮助/);
  assert.match(html, /过时/);
  assert.doesNotMatch(html, /window\.open/);
});

test("the settings dialog drops the remote host row and the manual index rebuild", () => {
  reset({
    selectedID: "doc-1", docState: "ready", doc,
    settingsState: "ready",
    settings: {
      enabled: true, source: "database", vault_ready: true, vault_initialized: true,
      index_ready: true, vault_name: "AHA2 Knowledge", vault_path: "/srv/vault",
      index_path: "/srv/index.db", entry_count: 5, rebuilt_at: "2026-11-14T13:38:42Z",
    },
  });
  const html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /远程主机/);
  assert.doesNotMatch(html, /Remote host/);
  assert.doesNotMatch(html, /remoteHost|remote_host/);
  assert.doesNotMatch(html, /id="kv-settings-rebuild"/);
  assert.match(html, /索引操作/);
  assert.match(html, /已就绪/);
});

test("the gear dialog shows read-only index health instead of a rebuild control", () => {
  reset({settingsState: "loading", settings: null});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /id="kv-settings-rebuild"/);
  assert.match(html, /索引操作/);

  reset({
    settingsState: "ready",
    settings: {
      enabled: true, source: "database", vault_ready: true, vault_initialized: false,
      index_ready: false, vault_path: "/srv/vault", index_path: "/srv/index.db", entry_count: 0,
    },
  });
  html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /id="kv-settings-rebuild"/);
  assert.match(html, /索引待自动修复|未就绪/);
});

test("the gear dialog reports automatic index health without a rebuild action", () => {
  const settings = {
    enabled: true, source: "database", vault_ready: true, vault_initialized: true,
    index_ready: true, vault_name: "AHA2 Knowledge", vault_path: "/srv/vault",
    index_path: "/srv/index.db", entry_count: 5, rebuilt_at: "2026-11-14T13:38:42Z",
  };
  reset({settingsState: "ready", settings});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /id="kv-settings-rebuild"/);
  assert.match(html, /索引操作/);
  assert.match(html, /已就绪/);

  reset({settingsState: "ready", settings: {...settings, index_stale: true}});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /索引待自动修复/);
  assert.doesNotMatch(html, /id="kv-settings-rebuild"/);
});

test("a settings load failure is explicit and retryable without breaking the dialog", () => {
  reset({settingsState: "error", settingsError: "network down"});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /role="alert">network down/);
  assert.match(html, /data-kv-settings-retry/);
});

test("unregistered Obsidian is greyed out and explains the reason", () => {
  reset({selectedID: "doc-1", docState: "ready", obsidianReady: false, doc});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /obsidian:\/\/open/);
  assert.match(html, /<button type="button" class="kv-open" disabled title="在 Obsidian 打开：先在 Obsidian 打开该文件夹" aria-label="在 Obsidian 打开：先在 Obsidian 打开该文件夹">/);
  assert.match(html, /aria-label="在本地 VSCode 打开"/);
  assert.match(html, /aria-label="复制文档路径"/);
});

test("tree loading, empty and error states are explicit", () => {
  reset({treeState: "loading"});
  assert.match(vault.renderKnowledgeVaultPage(context()), /kv-state-loading/);
  reset({treeState: "ready", tree: []});
  assert.match(vault.renderKnowledgeVaultPage(context()), /在 VSCode 新建 \.md，或让 Agent 通过 API 写入/);
  reset({treeState: "error", treeError: "vault unreadable"});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /vault unreadable/);
  assert.match(html, /data-kv-retry/);
  reset({indexReady: false, tree: []});
  assert.match(vault.renderKnowledgeVaultPage(context()), /索引尚未构建/);
});

test("inbox and skills are read-only lists with no move or edit affordances", () => {
  reset({
    inboxState: "ready",
    inbox: {
      ok: true, enabled: true, index_ready: true, count: 2,
      counts: [{library_id: "project:p1", library: "Demo", project_id: "p1", count: 2}],
      items: [
        {id: "i1", title: "想法一", path: "projects/p1/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "p1", library_id: "project:p1", library: "Demo", modified_at: "2026-11-14T10:00:00Z"},
      ],
    },
  });
  const inbox = vault.renderKnowledgeVaultPage(context());
  assert.match(inbox, /2 条待整理/);
  assert.match(inbox, /projects\/p1\/inbox\/idea\.md/);
  assert.match(inbox, /href="#knowledge\?doc=i1&amp;scope=project&amp;project=p1"/);
  assert.doesNotMatch(inbox, /data-kv-(move|archive|delete|edit)/);

  reset({tab: "skills", skillsState: "ready", skillRuntimeRead: true, skills: [{
    key: "vault-skill:global:build", id: "s1", slug: "build", name: "Build", scope: "global", library: "全局知识",
    description: "构建技能", file_count: 3, relative_path: "skills/build",
    absolute_path: "/srv/vault/skills/build", open_path: "/srv/vault/skills/build",
    source: "vault", auto_attach: false, auto_attach_effective: false,
    auto_attach_confirmed: false, can_update: true, project_bound: true,
  }]});
  const skills = vault.renderKnowledgeVaultPage(context());
  assert.match(skills, /Build/);
  assert.match(skills, /3 个文件/);
  assert.match(skills, /vscode:\/\/file\/srv\/vault\/skills\/build/);
  // Skill cards keep the three icon actions and add the task auto-attach
  // switch beside them; the source and scope are always spelled out.
  assert.match(skills, /<footer class="kv-skill-footer">/);
  assert.match(skills, /data-kv-skill-auto="vault-skill:global:build"/);
  assert.match(skills, /任务自动启用/);
  assert.match(skills, /vault 技能/);
  const card = skills.slice(skills.indexOf('<article class="kv-skill-card"'), skills.indexOf("</article>"));
  assert.equal((card.match(/class="kv-open(?: kv-copy)?"/g) || []).length, 3);
  assert.match(card, /aria-label="在本地 VSCode 打开「Build」"/);
  assert.match(card, /aria-label="在 Obsidian 打开「Build」"/);
  assert.match(card, /aria-label="复制技能路径「Build」"/);
  assert.doesNotMatch(card, /VSCode 远程/);
  assert.doesNotMatch(card, /<span>(?:在本地 VSCode 打开|在 Obsidian 打开|复制技能路径)/);
  assert.doesNotMatch(skills, /data-kv-(edit|delete)/);
});

test("project skill cards prefer the authoritative project name", () => {
  reset({tab: "skills", skillsState: "ready", skillRuntimeRead: true, skills: [
    {
      key: "vault-skill:project:p1:review", id: "s2", slug: "review", name: "Review", scope: "project",
      project_id: "p1", library: "demo-slug", project_name: "AHA2", description: "审查技能",
      file_count: 1, relative_path: "projects/aha2/skills/review",
      absolute_path: "/srv/vault/projects/aha2/skills/review", open_path: "/srv/vault/projects/aha2/skills/review/SKILL.md",
      source: "vault", auto_attach: false, auto_attach_effective: false,
      auto_attach_confirmed: false, can_update: true, project_bound: true,
    },
    {
      key: "vault-skill:global:build", id: "s1", slug: "build", name: "Build", scope: "global", library: "全局技能",
      description: "构建技能", file_count: 3, relative_path: "skills/build",
      absolute_path: "/srv/vault/skills/build", open_path: "/srv/vault/skills/build/SKILL.md",
      source: "vault", auto_attach: false, auto_attach_effective: false,
      auto_attach_confirmed: false, can_update: true, project_bound: true,
    },
  ]});
  const html = vault.renderKnowledgeVaultPage(context());
  // The API's project_name wins over the stale library slug and the picker
  // map (state.projects still calls p1 "Demo" here).
  assert.match(html, /项目 · AHA2/);
  assert.doesNotMatch(html, /项目 · Demo/);
  assert.doesNotMatch(html, /项目 · demo-slug/);
  assert.match(html, /vault 技能[\s\S]*?全局<\/small>/);
});

test("vault API requests carry library, project, type, status and tag filters", async t => {
  const original = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async path => {
    calls.push(String(path));
    return {ok: true, status: 200, json: async () => ({ok: true})};
  };
  t.after(() => { globalThis.fetch = original; });
  await api.knowledgeVaultTree("project:p 1", "p 1");
  await api.knowledgeVaultSearch({q: "构建 指南", library: "project:p1", project: "p1", type: "practice", status: "verified", tag: "build", limit: 20});
  await api.knowledgeVaultDoc("doc/1");
  assert.equal(calls[0], "/api/v1/knowledge-vault/tree?library=project%3Ap+1&project=p+1");
  assert.equal(calls[1], "/api/v1/knowledge-vault/search?q=%E6%9E%84%E5%BB%BA+%E6%8C%87%E5%8D%97&library=project%3Ap1&project=p1&type=practice&status=verified&tag=build&limit=20&include_archives=true");
  assert.equal(calls[2], "/api/v1/knowledge-vault/doc/doc%2F1");
});
const gitStatus = {
  available: true,
  initialized: true,
  branch: "main",
  current_branch: "main",
  remote: "git@example.com:…",
  remote_full: "git@example.com:owner/knowledge-vault.git",
  remote_set: true,
  clean: true,
  changed: 0,
  tracking: true,
  ahead: 0,
  behind: 0,
  has_commits: true,
  last_commit: "abc1234",
  last_commit_message: "Add notes",
  last_commit_at: "2026-11-14T12:00:00Z",
  last_sync_at: "2026-11-14T12:01:00Z",
};

test("the Git section starts uninitialized and offers only the init action", () => {
  reset({
    gitState: "ready",
    git: {...gitStatus, initialized: false, remote_set: false, remote: "", has_commits: false, tracking: false},
  });
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /Git 同步/);
  assert.match(html, /未初始化/);
  assert.match(html, /id="kv-git-init"/);
  assert.doesNotMatch(html, /id="kv-git-pull"/);
  assert.doesNotMatch(html, /id="kv-git-push"/);
  assert.doesNotMatch(html, /id="kv-git-remote-input"/);
});

test("the Git section shows the full remote, status and aligned actions", () => {
  reset({gitState: "ready", git: gitStatus});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /已初始化/);
  assert.match(html, /git@example\.com:owner\/knowledge-vault\.git/);
  assert.doesNotMatch(html, /git@example\.com:…/);
  assert.match(html, /已同步/);
  assert.match(html, /abc1234 · Add notes/);
  assert.match(html, /id="kv-git-remote-save"/);
  assert.match(html, /id="kv-git-sync"/);
  // Clean + tracking + no local commits: commit and push are disabled.
  assert.match(html, /id="kv-git-commit"[^>]*disabled/);
  assert.match(html, /id="kv-git-push"[^>]*disabled/);
  assert.doesNotMatch(html, /id="kv-git-pull"[^>]*disabled/);
  assert.doesNotMatch(html, /id="kv-git-sync"[^>]*disabled/);
});

test("the Git section explains dirty, ahead/behind and busy states", () => {
  reset({gitState: "ready", git: {...gitStatus, clean: false, changed: 3, ahead: 2, behind: 1}});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /有 3 个未提交修改/);
  assert.match(html, /领先 2 · 落后 1/);
  assert.doesNotMatch(html, /id="kv-git-commit"[^>]*disabled/);
  assert.match(html, /id="kv-git-pull"[^>]*disabled/);
  assert.match(html, /id="kv-git-sync"[^>]*disabled/);
  assert.doesNotMatch(html, /id="kv-git-push"[^>]*disabled/);

  reset({gitState: "ready", git: gitStatus, gitBusy: "sync"});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /同步中…/);
  assert.match(html, /id="kv-git-sync"[^>]*disabled/);
  assert.match(html, /id="kv-git-remote-input"[^>]*disabled/);
});

test("the Git section surfaces unavailable, load failure and operation errors", () => {
  reset({gitState: "ready", git: {...gitStatus, available: false}});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /当前环境没有可用的 git 命令/);
  assert.doesNotMatch(html, /id="kv-git-init"/);

  reset({gitState: "error", git: null, gitError: "network down"});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /读取 Git 状态失败：network down/);
  assert.match(html, /data-kv-git-retry/);

  reset({gitState: "ready", git: gitStatus, gitNotice: {kind: "error", message: "git push 失败：remote rejected"}});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /role="alert">git push 失败：remote rejected/);
});

test("the Git remote shows its full SSH address but never credentials", () => {
  reset({gitState: "ready", git: gitStatus});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-copy="git@example\.com:owner\/knowledge-vault\.git"/);
  assert.match(html, /aria-label="复制远程地址"/);
  assert.match(html, /data-kv-copy="\/srv\/vault"[^>]*aria-label="复制库路径"/);
  assert.doesNotMatch(html, /type="password"/);
  assert.doesNotMatch(html, /secret|token@/i);
  assert.match(html, /仅支持 SSH remote/);

  // Payloads produced before remote_full existed keep the masked fallback.
  reset({gitState: "ready", git: {...gitStatus, remote_full: undefined}});
  const masked = vault.renderKnowledgeVaultPage(context());
  assert.match(masked, /git@example\.com:…/);
});

test("generated directory list wikilinks become hash links and keep their labels", () => {
  const markdown = `# 通用知识

手写导语。

<!-- aha-vault:index:start -->
## 文档

- [[global/guides/topic|主题文档]] — 摘要。
- [[global/guides/advanced/index|进阶目录]]
- [[global/guides/code|带 [括号] 的标题]]
<!-- aha-vault:index:end -->
`;
  const rewritten = vault.rewriteVaultWikilinks(markdown);
  assert.match(rewritten, /- \[主题文档\]\(#vault-wikilink=global%2Fguides%2Ftopic\) — 摘要。/);
  assert.match(rewritten, /- \[进阶目录\]\(#vault-wikilink=global%2Fguides%2Fadvanced%2Findex\)/);
  assert.match(rewritten, /- \[带 \\\[括号\\\] 的标题\]\(#vault-wikilink=global%2Fguides%2Fcode\)/);
  assert.doesNotMatch(rewritten, /\[\[/);

  // Inline wikilinks outside a list item and code fences stay untouched: the
  // shared renderer keeps them literal by design.
  const untouched = vault.rewriteVaultWikilinks("参见 [[部署]]。\n\n```\n- [[not|a link]]\n```\n");
  assert.match(untouched, /参见 \[\[部署\]\]。/);
  assert.match(untouched, /- \[\[not\|a link\]\]/);
});

test("wikilink targets resolve by path, directory, suffix and unique title", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "g-topic", title: "主题文档", path: "global/general/topic.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
        {id: "g-other", title: "通用知识", path: "global/general/other.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
      treeFixture("g-reference", "参考资料", "global/reference/index.md", []),
    ]),
  ], "all");
  const byPath = new Map();
  const visit = nodes => nodes.forEach(node => {
    assert.ok(node.path);
    if (!node.id.startsWith("vault-dir:")) byPath.set(node.path.replace(/\.md$/i, ""), node);
    visit(node.children || []);
  });
  visit(tree);
  assert.equal(vault.resolveVaultWikilink("global/general/topic", byPath)?.id, "g-topic");
  assert.equal(vault.resolveVaultWikilink("global/general/topic.md", byPath)?.id, "g-topic");
  assert.equal(vault.resolveVaultWikilink("global/general/index", byPath)?.id, "g-general", "directory targets map to the folder page");
  assert.equal(vault.resolveVaultWikilink("topic", byPath)?.id, "g-topic", "unique suffix is accepted");
  assert.equal(vault.resolveVaultWikilink("主题文档", byPath)?.id, "g-topic", "unique title is accepted");
  assert.equal(vault.resolveVaultWikilink("global/general", byPath)?.id, "g-general");
  assert.equal(vault.resolveVaultWikilink("missing", byPath), null);
});

test("a directory page renders the generated list as links, not raw brackets", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "g-topic", title: "主题文档", path: "global/general/topic.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
      treeFixture("g-reference", "参考资料", "global/reference/index.md", []),
    ]),
  ], "all");
  const indexDoc = {
    ...doc,
    id: "g-general",
    title: "通用知识",
    path: "global/general/index.md",
    relative_path: "global/general/index.md",
    scope: "global",
    project_id: "",
    library_id: "global",
    library_name: "全局知识",
    is_index: true,
    body: `# 通用知识

手写导语。

<!-- aha-vault:index:start -->
## 文档

- [[global/general/topic|主题文档]] — 自动生成的摘要。
- [[global/reference/index|参考资料]] — 子目录。
<!-- aha-vault:index:end -->
`,
    outline: [{level: 1, text: "通用知识", slug: "kv-doc"}, {level: 2, text: "文档", slug: "docs"}],
  };
  reset({tree, selectedID: "g-general", docState: "ready", doc: indexDoc});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /手写导语。/);
  assert.match(html, /<h2>文档<\/h2>/);
  assert.doesNotMatch(html, /\[\[/);
  assert.match(html, /href="#vault-wikilink=global%2Fgeneral%2Ftopic"[^>]*>主题文档<\/a>/);
  assert.match(html, /href="#vault-wikilink=global%2Freference%2Findex"[^>]*>参考资料<\/a>/);
  assert.match(html, /自动生成的摘要。/);
});

test("errored activity feeds and the tree only retry when forced", async () => {
  const originals = {
    tree: api.knowledgeVaultTree,
    recent: api.knowledgeVaultRecent,
    inbox: api.knowledgeVaultInbox,
  };
  const calls = {tree: 0, recent: 0, inbox: 0};
  api.knowledgeVaultTree = async () => { calls.tree += 1; throw new Error("tree 500"); };
  api.knowledgeVaultRecent = async () => { calls.recent += 1; throw new Error("recent 500"); };
  api.knowledgeVaultInbox = async () => { calls.inbox += 1; throw new Error("inbox 500"); };
  try {
    reset({
      treeKey: "all", treeState: "error", treeError: "tree 500",
      recentKey: "", recentState: "error", recentError: "recent 500",
      inboxKey: "", inboxState: "error", inboxError: "inbox 500",
    });
    // Rendering re-enters the tab loaders; those non-force calls must not
    // retry a failed request or the page loops error -> render -> request.
    await vault.__loadTreeForTest();
    await vault.__loadRecentForTest();
    await vault.__loadInboxForTest();
    assert.deepEqual(calls, {tree: 0, recent: 0, inbox: 0}, "non-force loads leave errored feeds alone");
    const errored = vault.__getKnowledgeVaultStateForTest();
    assert.equal(errored.treeState, "error");
    assert.equal(errored.recentState, "error");
    assert.equal(errored.inboxState, "error");
    assert.equal(errored.recentError, "recent 500");

    await Promise.all([
      vault.__loadTreeForTest(true),
      vault.__loadRecentForTest(true),
      vault.__loadInboxForTest(true),
    ]);
    assert.deepEqual(calls, {tree: 1, recent: 1, inbox: 1}, "an explicit retry requests each failed feed once");
    assert.equal(vault.__getKnowledgeVaultStateForTest().recentState, "error", "a failed retry returns to the error state");

    await vault.__loadRecentForTest();
    await vault.__loadInboxForTest();
    assert.deepEqual(calls, {tree: 1, recent: 1, inbox: 1}, "a failed retry does not start another loop");

    reset({recentKey: "", recentState: "ready"});
    await vault.__loadRecentForTest();
    assert.equal(calls.recent, 1, "a ready feed is still a cache hit");
  } finally {
    api.knowledgeVaultTree = originals.tree;
    api.knowledgeVaultRecent = originals.recent;
    api.knowledgeVaultInbox = originals.inbox;
  }
});

test("recent grouping prefers the content change time over a later mtime", () => {
  const groups = vault.groupRecentByDay([
    {id: "a", modified_at: "2026-11-15T09:00:00", changed_at: "2026-11-14T09:00:00"},
    {id: "b", modified_at: "2026-11-13T09:00:00"},
  ], new Date("2026-11-15T12:00:00"));
  assert.deepEqual(groups.map(group => group.key), ["2026-11-14", "2026-11-13"]);
  assert.deepEqual(groups[0].items.map(item => item.id), ["a"], "a later mtime does not move the row to today");
  assert.deepEqual(groups[1].items.map(item => item.id), ["b"]);
});

test("recent marks timestamp-only and unknown rows and exposes the reveal switch", () => {
  const row = {
    type: "practice", status: "verified", scope: "global", library_id: "global", library: "全局知识",
    modified_at: "2026-11-14T10:00:00Z", path: "global/general/build.md",
  };
  reset({
    recentState: "ready",
    recent: [{...row, id: "r1", title: "真实变更", content_changed: true, change_kind: "uncommitted",
      modified_at: "2026-11-14T10:00:00Z", changed_at: "2026-11-14T09:00:00Z"}],
  });
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-recent-unchanged aria-pressed="false"/);
  assert.match(html, />显示仅时间更新</);
  assert.match(html, /data-kv-preview="r1"/);
  assert.doesNotMatch(html, /class="kv-change-note"/, "content changes carry no timestamp-only marker");

  reset({recentState: "ready", recentIncludeUnchanged: true, recent: [
    {...row, id: "r2", title: "仅时间更新", content_changed: false, change_kind: "none"},
    {...row, id: "r3", title: "状态未知", content_changed: true, change_kind: "unknown"},
  ]});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-recent-unchanged aria-pressed="true"/);
  assert.match(html, />隐藏仅时间更新</);
  assert.match(html, /仅时间更新<\/span>/);
  assert.match(html, /class="kv-change-note">变更未知</);
  assert.match(html, /data-kv-preview="r2"/);
  assert.match(html, /data-kv-preview="r3"/);

  reset({recentState: "ready", recent: []});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-recent-unchanged aria-pressed="false"/, "the switch stays reachable above an empty feed");
  assert.match(html, /最近没有变更/);
});

test("loadRecent only requests timestamp-only rows after the switch is enabled", async () => {
  const original = api.knowledgeVaultRecent;
  const calls = [];
  const row = {
    title: "T", type: "practice", status: "verified", scope: "global",
    library_id: "global", library: "全局知识", path: "global/general/build.md",
    modified_at: "2026-11-14T10:00:00Z",
  };
  api.knowledgeVaultRecent = async (...args) => {
    calls.push(args);
    return {
      ok: true, enabled: true, index_ready: true,
      items: [
        {...row, id: "changed", content_changed: true, change_kind: "uncommitted"},
        {...row, id: "same", content_changed: false, change_kind: "none"},
      ],
    };
  };
  try {
    reset({recentState: "idle"});
    await vault.__loadRecentForTest(true);
    assert.deepEqual(calls.at(-1), ["", 120, false]);
    assert.deepEqual(vault.__getKnowledgeVaultStateForTest().recent.map(item => item.id), ["changed"],
      "the client keeps filtering rows from a server without the new default");

    vault.__setKnowledgeVaultStateForTest({recentIncludeUnchanged: true});
    await vault.__loadRecentForTest(true);
    assert.deepEqual(calls.at(-1), ["", 120, true], "the switch requests include_unchanged");
    assert.deepEqual(vault.__getKnowledgeVaultStateForTest().recent.map(item => item.id), ["changed", "same"]);
  } finally {
    api.knowledgeVaultRecent = original;
  }
});

test("a disabled vault renders the blocked landing with a settings CTA, not the legacy workspace", () => {
  reset({blocked: true, blockedStatus: "needs_migration", blockedMessage: "升级后需要先迁移旧知识库。"});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-blocked="needs_migration"/);
  assert.match(html, /class="kv-badge kv-badge-warn">待迁移</);
  assert.match(html, /升级后还没有迁移或导入知识/);
  assert.match(html, /data-kv-open-vault-settings/);
  assert.match(html, /去高级设置迁移或导入/);
  assert.match(html, /旧知识库只作为迁移来源/);
  // No vault workbench and no legacy workspace UI on the blocked route.
  assert.doesNotMatch(html, /kv-tree|kv-tabs|kv-search-trigger|知识首页|待绑定/);
});

test("a plain disabled vault says it is off and still offers the migration entry", () => {
  reset({blocked: true, blockedStatus: "", blockedMessage: ""});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-blocked="disabled"/);
  assert.match(html, /class="kv-badge">已关闭</);
  assert.match(html, /文件知识库当前已关闭/);
  assert.match(html, /data-kv-open-vault-settings/);
});

test("the blocked landing follows the active locale", async () => {
  const panelSource = await readFile(resolve(root, "build", "knowledge_vault.js"), "utf8");
  const version = panelSource.match(/from "\.\/i18n\.js(\?v=[^"]+)"/)?.[1] || "";
  const i18nVersioned = await import(pathToFileURL(resolve(root, "build", "i18n.js")).href + version);
  const previous = i18nVersioned.currentLocale();
  try {
    i18nVersioned.applyLocale("en-US");
    reset({blocked: true, blockedStatus: "needs_migration"});
    const html = vault.renderKnowledgeVaultPage(context());
    assert.match(html, /Knowledge vault unavailable/);
    assert.match(html, /Open advanced settings to migrate or import/);
    assert.match(html, /The legacy knowledge base is only a migration source/);
    assert.doesNotMatch(html, /去高级设置迁移或导入|旧知识库只作为迁移来源/);
  } finally {
    i18nVersioned.applyLocale(previous);
  }
});

test("an uninitialized vault shows the initialization entry instead of an empty workspace", () => {
  reset({blocked: true, blockedStatus: "needs_rebuild"});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-blocked="needs_rebuild"/);
  assert.match(html, /class="kv-badge kv-badge-warn">待初始化</);
  assert.match(html, /文件知识库还没有初始化/);
  assert.match(html, /去高级设置初始化、迁移或导入/);
});

test("the real file directory lists folders, markdown and binary downloads", () => {
  reset({
    filesMode: true, filesPath: "projects/demo", filesState: "ready", openAvailable: false,
    files: [
      {name: "topics", path: "projects/demo/topics", absolute_path: "/vault/projects/demo/topics", is_dir: true, size: 0, kind: "folder"},
      {name: "notes.md", path: "projects/demo/notes.md", absolute_path: "/vault/projects/demo/notes.md", is_dir: false, size: 12, kind: "markdown"},
      {name: "raw.bin", path: "projects/demo/raw.bin", absolute_path: "/vault/projects/demo/raw.bin", is_dir: false, size: 99, kind: "binary"},
    ],
  });
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-files-dir="projects\/demo\/topics"/);
  assert.match(html, /data-kv-files-dir="projects\/demo"/);
  assert.match(html, /data-kv-files-dir="projects"/);
  assert.match(html, /data-kv-files-open="projects\/demo\/notes.md"/);
  assert.match(html, /\/api\/v1\/knowledge-vault\/file\/download\?path=projects%2Fdemo%2Fraw.bin/);
  assert.doesNotMatch(html, /data-kv-reveal=/);
});

test("a file preview offers copy path and hides the file-manager action when remote", () => {
  const file = {
    path: "projects/demo/notes.md", absolute_path: "/vault/projects/demo/notes.md",
    name: "notes.md", title: "笔记", kind: "markdown", content: "---\ntitle: 笔记\n---\n# 笔记\n", size: 30,
  };
  reset({filesMode: true, selectedID: "doc-1", filesPath: "projects/demo", filesState: "ready", files: [], file, fileState: "ready", openAvailable: false});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-copy="\/vault\/projects\/demo\/notes.md"/);
  assert.doesNotMatch(html, /data-kv-reveal=/);

  reset({filesMode: true, selectedID: "doc-1", filesPath: "projects/demo", filesState: "ready", files: [], file, fileState: "ready", openAvailable: true});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /data-kv-reveal="projects\/demo\/notes.md"/);
  assert.match(html, /在文件管理器中打开/);
});

test("the directory header copies and opens the vault/current folder, not the open document", () => {
  reset({vaultPath: "C:\\知识库", openAvailable: false});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /class="kv-panel-actions kv-open-actions"[\s\S]*?data-kv-copy="C:\\知识库"[^>]*title="复制目录路径"[^>]*aria-label="复制目录路径"/);
  assert.doesNotMatch(html, /data-kv-reveal=/, "remote access never shows a file-manager action");
  assert.doesNotMatch(html, /href="vscode:\/\/|href="obsidian:\/\//, "remote access never shows editor links");
  assert.doesNotMatch(html, /复制文档路径/, "the directory copy action must not use the document-path label");

  reset({vaultPath: "/srv/vault", filesMode: true, filesPath: "projects/demo", filesState: "ready", files: []});
  html = vault.renderKnowledgeVaultPage(context());
  const tools = html.slice(html.indexOf('<div class="kv-panel-tools">'), html.indexOf('<div class="kv-tree-body">'));
  assert.match(tools, /data-kv-copy="\/srv\/vault\/projects\/demo"/, "file mode copies the current directory");
  assert.match(tools, /href="vscode:\/\/file\/srv\/vault\/projects\/demo"/, "VSCode opens the current folder");
  assert.match(tools, /href="obsidian:\/\/open\?vault=AHA2%20Knowledge&amp;file=projects%2Fdemo"/, "Obsidian opens the vault at the current folder");
  assert.deepEqual((tools.match(/aria-label="[^"]+"/g) || []).map(value => value.slice(12, -1)), ["在本地 VSCode 打开", "在 Obsidian 打开", "复制目录路径"]);
  assert.doesNotMatch(tools, /data-kv-reveal=|在文件管理器中打开/, "the directory header has no file-manager entry");
  assert.equal((tools.match(/class="kv-open(?: kv-copy)?"/g) || []).length, 3, "two editors plus copy");

  // The indexed tree always describes the vault root, even after the file
  // browser remembers a nested directory.
  reset({vaultPath: "/srv/vault", filesMode: false, filesPath: "projects/demo"});
  html = vault.renderKnowledgeVaultPage(context());
  const rootTools = html.slice(html.indexOf('<div class="kv-panel-tools">'), html.indexOf('<div class="kv-tree-body">'));
  assert.match(rootTools, /data-kv-copy="\/srv\/vault"/);
  assert.match(rootTools, /href="vscode:\/\/file\/srv\/vault"/);
  assert.match(rootTools, /href="obsidian:\/\/open\?vault=AHA2%20Knowledge&amp;file="/);
  assert.doesNotMatch(rootTools, /data-kv-reveal=/);
});

test("remote sessions hide directory editor actions and keep only copy", () => {
  reset({vaultPath: "/srv/vault", openAvailable: false, filesMode: true, filesPath: "projects/demo", filesState: "ready", files: []});
  const html = vault.renderKnowledgeVaultPage(context());
  const tools = html.slice(html.indexOf('<div class="kv-panel-tools">'), html.indexOf('<div class="kv-tree-body">'));
  assert.equal((tools.match(/class="kv-open(?: kv-copy)?"/g) || []).length, 1, "only the copy action remains");
  assert.match(tools, /data-kv-copy="\/srv\/vault\/projects\/demo"/);
  assert.doesNotMatch(tools, /data-kv-reveal=|vscode:\/\/|obsidian:\/\//, "no hidden or empty editor controls");
});

test("remote document actions keep the copy button and drop VSCode/Obsidian", () => {
  reset({selectedID: "doc-1", docState: "ready", doc, openAvailable: false});
  const html = vault.renderKnowledgeVaultPage(context());
  const toolbar = html.slice(html.indexOf('<div class="kv-doc-toolbar">'), html.indexOf('<div class="kv-doc-head">'));
  assert.equal((toolbar.match(/class="kv-open(?: kv-copy)?"/g) || []).length, 1, "only the document copy action remains");
  assert.match(toolbar, /data-kv-copy="\/srv\/vault\/项目 知识\/projects\/p1\/guides\/build\.md"/);
  assert.doesNotMatch(toolbar, /vscode:\/\/|obsidian:\/\//);
  assert.doesNotMatch(toolbar, /(?:title|aria-label)="[^"]*\/srv\//, "remote controls never leak local paths");
});
