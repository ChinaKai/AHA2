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
const {api} = await import(pathToFileURL(resolve(root, "build", "api.js")).href);

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

test("the page exposes exactly the four read-only entries and no edit or review controls", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-general", "通用知识", "global/general/index.md", [
        {id: "doc-1", title: "构建指南", path: "global/general/build.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
    ]),
  ], "global");
  reset({tree});
  const html = vault.renderKnowledgeVaultPage(context());
  for (const tab of ["knowledge", "recent", "inbox", "skills"]) {
    assert.match(html, new RegExp(`data-kv-tab-button="${tab}"`));
  }
  assert.doesNotMatch(html, />项目知识<|>待审提案<|data-knowledge-section=/);
  assert.doesNotMatch(html, /data-kv-(edit|delete|move|approve)|>删除<|>编辑<|>待审提案</);
  assert.doesNotMatch(html, /系统索引/);
  assert.doesNotMatch(html, /全局知识首页/, "the library root index is not a folder row");
  assert.match(html, /<span class="kv-tree-title">general<\/span>/);
  assert.match(html, /<span class="kv-tree-title">inbox<\/span>/);
  assert.match(html, /<nav id="kv-tree-panel" class="kv-tree-panel" aria-label="目录">/, "tree is a nav, not a fake tree role");
  assert.doesNotMatch(html, /role="tree"/);
  assert.match(html, /data-kv-toggle="g-general"[^>]*aria-expanded="false"/);
  assert.doesNotMatch(html, /href="#knowledge\?doc=doc-1&amp;scope=global"/, "folder children start hidden");
  assert.equal((html.match(/class="kv-tab kv-tab-/g) || []).length, 4);
  // Heading and tabs form one group; controls are a separate group. The old
  // mobile "more" menu must not be rendered at all: skills and settings are
  // directly visible at every width.
  assert.match(html, /<div class="kv-topbar-main">[\s\S]*class="kv-heading"[\s\S]*class="kv-tabs"/);
  assert.match(html, /<div class="kv-controls">[\s\S]*data-kv-settings/);
  assert.equal((html.match(/data-kv-tab-button="/g) || []).length, 4, "no duplicate tab buttons in a more menu");
  assert.doesNotMatch(html, /data-kv-more|kv-more-trigger|kv-more-menu/);
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
  assert.deepEqual(tree.map(node => node.title), ["agent-lessons", "general", "inbox"]);
  const lessons = tree.find(node => node.title === "agent-lessons");
  assert.equal(lessons.id, "g-lessons");
  assert.equal(lessons.path, "global/agent-lessons");
  assert.equal(lessons.has_index, true);
  assert.deepEqual(lessons.children.map(node => node.title), ["behavior-lessons", "technical-diagnostics"]);
  const technical = lessons.children.find(node => node.title === "technical-diagnostics");
  assert.deepEqual(technical.children.map(node => node.title), ["串口登录等待提示"]);
  assert.equal(JSON.stringify(tree).includes("全局知识首页"), false, "the root index title never becomes a folder name");
  assert.equal(JSON.stringify(tree).includes("Agent 经验教训"), false, "index titles never become folder names");
  assert.equal(tree.find(node => node.title === "inbox").has_index, false);
});

test("project trees show theme directories and the real inbox folder", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("p-root", "Demo 索引", "projects/demo/index.md", [
      treeFixture("p-group", "导航组", "projects/demo/navigation-group/index.md", [
        {id: "p-module", title: "模块 A", path: "projects/demo/navigation-group/module-a.md", type: "reference", status: "verified", scope: "project", project_id: "p1", is_index: false, system: false, children: []},
      ], {scope: "project", project_id: "p1"}),
      {id: "p-idea", title: "收件箱一", path: "projects/demo/inbox/idea.md", type: "practice", status: "unverified", scope: "project", project_id: "p1", is_index: false, system: false, children: []},
    ], {scope: "project", project_id: "p1"}),
  ], "project:p1");
  assert.deepEqual(tree.map(node => node.title), ["inbox", "navigation-group"]);
  assert.equal(tree[0].path, "projects/demo/inbox");
  assert.equal(tree[0].has_index, false);
  assert.deepEqual(tree[0].children.map(node => node.title), ["收件箱一"]);
  assert.equal(tree[1].path, "projects/demo/navigation-group");
  assert.equal(tree[1].id, "p-group");
  assert.equal(tree[1].has_index, true);
});

test("directory rows open their index document once and never repeat index.md as a leaf", () => {
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
  assert.equal((html.match(/data-kv-doc="g-general"/g) || []).length, 1, "index document is represented by the folder row only");
  assert.match(html, /data-kv-doc="g-general"/);
  assert.match(html, /href="#knowledge\?doc=g-general&amp;scope=global"/);
  assert.doesNotMatch(html, /data-kv-doc="g-extra"/, "a duplicate index.md entry is not a leaf");
  assert.match(html, /<span class="kv-tree-title">general<\/span>/);
  assert.doesNotMatch(html, /kv-badge-index/);
  const folderLink = html.match(/<a class="kv-tree-link kv-tree-folder-link[^>]*data-kv-doc="g-general"[^>]*>[\s\S]*?<\/a>/)?.[0] || "";
  assert.equal(folderLink.includes("kv-badge"), false, "directory rows do not carry index or navigation badges");
  assert.match(html, /kv-tree-folder-icon/, "directories use a folder icon");
  assert.match(html, /kv-badge-type[^>]*>实践</, "leaf documents keep their type badge");
});

test("folder icons toggle, titles open the index, and document titles align under folder titles", () => {
  const tree = vault.buildVaultDirectoryTree([
    treeFixture("g-root", "全局知识首页", "global/index.md", [
      treeFixture("g-full", "完整目录", "global/full/index.md", [
        treeFixture("g-sub", "子目录", "global/full/sub/index.md", []),
        {id: "g-leaf", title: "同级文档", path: "global/full/leaf.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]),
      treeFixture("g-empty", "只有索引", "global/empty/index.md", []),
      {id: "g-inbox-doc", title: "收件箱文档", path: "global/inbox/idea.md", type: "practice", status: "unverified", scope: "global", is_index: false, system: false, children: []},
    ]),
  ], "global");
  reset({tree, expanded: new Set(["g-full"])});
  const html = vault.renderKnowledgeVaultPage(context());

  // The folder icon is its own button; the title is an independent link.
  const fullRow = html.match(/<div class="kv-tree-row kv-tree-row-toggle" data-kv-row-toggle="g-full">[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(fullRow, /<button type="button" class="kv-tree-toggle" data-kv-toggle="g-full" aria-expanded="true"/);
  assert.match(fullRow, /<a class="kv-tree-link kv-tree-folder-link" href="#knowledge\?doc=g-full&amp;scope=global" data-kv-doc="g-full"/);
  assert.equal(fullRow.indexOf("data-kv-toggle=\"g-full\"") < fullRow.indexOf("data-kv-doc=\"g-full\""), true, "icon precedes the title link");
  assert.doesNotMatch(fullRow, /<a[^>]*data-kv-doc="g-full"[^>]*>[^<]*<button/);

  // A folder with an index but no children keeps a blank icon slot; only the
  // title link opens the index.
  const emptyRow = html.match(/<div class="kv-tree-row">\s*<span class="kv-tree-toggle-space"[\s\S]*?data-kv-doc="g-empty"[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(emptyRow, /kv-tree-toggle-space/);
  assert.match(emptyRow, /data-kv-doc="g-empty"/);
  assert.doesNotMatch(emptyRow, /data-kv-toggle="/);
  assert.doesNotMatch(emptyRow, /data-kv-row-toggle/);

  // Virtual directories have no index; the row itself is the control.
  const inboxRow = html.match(/<div class="kv-tree-row kv-tree-row-toggle" data-kv-row-toggle="vault-dir:global\/inbox">[\s\S]*?<\/div>/)?.[0] || "";
  assert.match(inboxRow, /data-kv-toggle="vault-dir:global\/inbox"/);
  assert.doesNotMatch(inboxRow, /data-kv-doc=/);
  assert.match(inboxRow, /<span class="kv-tree-link kv-tree-folder-link"><span class="kv-tree-title">inbox<\/span><\/span>/);

  // Document rows reserve the same icon width and carry the same depth.
  const docRow = html.match(/<li class="kv-tree-node kv-tree-doc" style="--kv-depth:1"><a[\s\S]*?<\/a><\/li>/)?.[0] || "";
  assert.match(docRow, /<span class="kv-tree-doc-spacer" aria-hidden="true"><\/span><span class="kv-tree-title">同级文档<\/span>/);
  assert.match(html, /<li class="kv-tree-node kv-tree-folder" style="--kv-depth:1">/);
});

test("the sidebar defaults to 270px, collapses to a rail and exposes a 6px separator", async () => {
  reset({});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /style="--kv-tree-width:270px;--kv-resizer-width:6px" data-kv-sidebar-collapsed="false"/);
  assert.match(html, /class="kv-tree-resizer" role="separator" aria-orientation="vertical" tabindex="0"[^>]*aria-valuemin="220" aria-valuemax="460" aria-valuenow="270"/);

  reset({sidebarCollapsed: true});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /style="--kv-tree-width:48px;--kv-resizer-width:0px" data-kv-sidebar-collapsed="true"/);
  assert.match(html, /class="kv-tree-panel collapsed"/);
  assert.match(html, /data-kv-sidebar-toggle aria-expanded="false"/);
  assert.doesNotMatch(html, /kv-tree-resizer/);

  // Route-only styles travel in the lazy knowledge chunk, not styles.css.
  const css = await readFile(resolve(root, "src", "knowledge_vault.ts"), "utf8");
  assert.match(css, /\.kv-tree-list \.kv-tree-list \{ padding-left: 12px; \}/);
  assert.match(css, /\.kv-tree-resizer \{[^}]*width: 6px/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*\.kv-sidebar-toggle, \.kv-tree-resizer \{ display: none; \}/);
  assert.match(css, /@media \(max-width: 760px\)[\s\S]*\.kv-tree-panel\.collapsed \.kv-tree-body \{ display: block; \}/);
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
  assert.deepEqual(demo.children.map(node => node.title), ["guides", "inbox"]);
  assert.equal(demo.children.find(node => node.title === "inbox").has_index, false, "an empty project inbox still renders");

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
  assert.match(html, /<span class="kv-tree-title">inbox<\/span>/);
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
    tab: "recent",
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

test("topbar CSS keeps the tab strip on one row and shows skills/settings on phones", async () => {
  const css = await readFile(resolve(root, "src", "styles.css"), "utf8");
  assert.match(css, /\.kv-topbar-main \{[^}]*flex: 1 1 auto/);
  assert.match(css, /\.kv-tabs \{[^}]*flex-wrap: nowrap/);
  assert.match(css, /\.kv-tabs \{[^}]*overflow-x: auto/);
  assert.match(css, /\.kv-controls \{[^}]*align-items: center/);
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

test("the settings dialog drops the remote host row and offers the index rebuild", () => {
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
  assert.match(html, /id="kv-settings-rebuild"(?![^>]*disabled)/);
  assert.match(html, /索引操作/);
});

test("the gear rebuild stays disabled before settings load or without an initialized vault", () => {
  reset({settingsState: "loading", settings: null});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /id="kv-settings-rebuild"[^>]*disabled/);

  reset({
    settingsState: "ready",
    settings: {
      enabled: true, source: "database", vault_ready: true, vault_initialized: false,
      index_ready: false, vault_path: "/srv/vault", index_path: "/srv/index.db", entry_count: 0,
    },
  });
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /id="kv-settings-rebuild"[^>]*disabled/);
});

test("the gear dialog shows rebuild busy, success and failure states", () => {
  const settings = {
    enabled: true, source: "database", vault_ready: true, vault_initialized: true,
    index_ready: true, vault_name: "AHA2 Knowledge", vault_path: "/srv/vault",
    index_path: "/srv/index.db", entry_count: 5, rebuilt_at: "2026-11-14T13:38:42Z",
  };
  reset({settingsState: "ready", settings, rebuildBusy: true});
  let html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /aria-busy="true"/);
  assert.match(html, /重建中/);
  assert.match(html, /id="kv-settings-rebuild"[^>]*disabled/);

  reset({settingsState: "ready", settings, rebuildNotice: {kind: "notice", message: "已重建 7 条 · 2026/11/14 21:00"}});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /role="status">已重建 7 条/);
  assert.doesNotMatch(html, /远程主机/);

  reset({settingsState: "ready", settings, rebuildNotice: {kind: "error", message: "重建索引失败：磁盘只读"}});
  html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /role="alert">重建索引失败：磁盘只读/);
  assert.match(html, /id="kv-settings-rebuild"(?![^>]*disabled)/);
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
    tab: "inbox",
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

  reset({tab: "skills", skillsState: "ready", skills: [{
    id: "s1", slug: "build", name: "Build", scope: "global", library: "全局知识",
    description: "构建技能", file_count: 3, relative_path: "skills/build",
    absolute_path: "/srv/vault/skills/build", open_path: "/srv/vault/skills/build",
  }]});
  const skills = vault.renderKnowledgeVaultPage(context());
  assert.match(skills, /Build/);
  assert.match(skills, /3 个文件/);
  assert.match(skills, /vscode:\/\/file\/srv\/vault\/skills\/build/);
  // Skill cards keep the same three icon actions, right-aligned in a footer
  // below the file count, with the skill name in each accessible label.
  assert.match(skills, /<footer class="kv-skill-actions"><div class="kv-open-actions compact">/);
  const card = skills.slice(skills.indexOf('<article class="kv-skill-card">'), skills.indexOf("</article>"));
  assert.equal((card.match(/class="kv-open(?: kv-copy)?"/g) || []).length, 3);
  assert.match(card, /aria-label="在本地 VSCode 打开「Build」"/);
  assert.match(card, /aria-label="在 Obsidian 打开「Build」"/);
  assert.match(card, /aria-label="复制技能路径「Build」"/);
  assert.doesNotMatch(card, /VSCode 远程/);
  assert.doesNotMatch(card, /<span>(?:在本地 VSCode 打开|在 Obsidian 打开|复制技能路径)/);
  assert.doesNotMatch(skills, /data-kv-(edit|delete)/);
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
  assert.equal(calls[1], "/api/v1/knowledge-vault/search?q=%E6%9E%84%E5%BB%BA+%E6%8C%87%E5%8D%97&library=project%3Ap1&project=p1&type=practice&status=verified&tag=build&limit=20");
  assert.equal(calls[2], "/api/v1/knowledge-vault/doc/doc%2F1");
});
const gitStatus = {
  available: true,
  initialized: true,
  branch: "main",
  current_branch: "main",
  remote: "git@example.com:…",
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

test("the Git section shows masked remote, status and aligned actions", () => {
  reset({gitState: "ready", git: gitStatus});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /已初始化/);
  assert.match(html, /git@example\.com:…/);
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

test("the Git remote never appears in full and no token input is offered", () => {
  reset({gitState: "ready", git: gitStatus});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /example\.com\/secret/);
  assert.doesNotMatch(html, /type="password"/);
  assert.match(html, /仅支持 SSH remote/);
});
