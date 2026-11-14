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
    remoteHost: "devbox",
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

test("VSCode remote URIs use ssh-remote and degrade to null without a host", () => {
  assert.equal(
    vault.vscodeRemoteURI("devbox", "/srv/vault/项目 文档/a b.md"),
    "vscode://vscode-remote/ssh-remote+devbox/srv/vault/%E9%A1%B9%E7%9B%AE%20%E6%96%87%E6%A1%A3/a%20b.md",
  );
  assert.equal(vault.vscodeRemoteURI("", "/srv/vault/a.md"), null);
  assert.equal(vault.vscodeRemoteURI("   ", "/srv/vault/a.md"), null);
  assert.equal(
    vault.vscodeRemoteURI("devbox", "D:\\notes\\计划.md"),
    "vscode://vscode-remote/ssh-remote+devbox/D:/notes/%E8%AE%A1%E5%88%92.md",
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

test("the page exposes exactly the four read-only entries and no edit or review controls", () => {
  reset({
    tree: [
      {id: "index", title: "全局索引", path: "global/index.md", type: "navigation", status: "verified", scope: "global", is_index: true, system: true, children: []},
      {id: "folder", title: "guides", path: "guides", type: "reference", status: "verified", scope: "global", is_index: false, system: false, children: [
        {id: "doc-1", title: "构建指南", path: "guides/build.md", type: "practice", status: "verified", scope: "global", is_index: false, system: false, children: []},
      ]},
    ],
  });
  const html = vault.renderKnowledgeVaultPage(context());
  for (const tab of ["knowledge", "recent", "inbox", "skills"]) {
    assert.match(html, new RegExp(`data-kv-tab-button="${tab}"`));
  }
  assert.doesNotMatch(html, />项目知识<|>待审提案<|data-knowledge-section=/);
  assert.doesNotMatch(html, /data-kv-(edit|delete|move|approve)|>删除<|>编辑<|>待审提案</);
  assert.match(html, /系统索引/);
  assert.match(html, /文档/);
  assert.match(html, /href="#knowledge\?doc=doc-1&amp;scope=global"/, "tree rows are real anchors");
  assert.match(html, /<nav class="kv-tree-panel" aria-label="目录">/, "tree is a nav, not a fake tree role");
  assert.doesNotMatch(html, /role="tree"/);
  assert.match(html, /data-kv-toggle="folder"[^>]*aria-expanded="true"/);
  assert.equal((html.match(/class="kv-tab kv-tab-/g) || []).length, 4);
  // Heading and tabs form one group; controls are a separate group. The old
  // mobile "more" menu must not be rendered at all: skills and settings are
  // directly visible at every width.
  assert.match(html, /<div class="kv-topbar-main">[\s\S]*class="kv-heading"[\s\S]*class="kv-tabs"/);
  assert.match(html, /<div class="kv-controls">[\s\S]*data-kv-settings/);
  assert.equal((html.match(/data-kv-tab-button="/g) || []).length, 4, "no duplicate tab buttons in a more menu");
  assert.doesNotMatch(html, /data-kv-more|kv-more-trigger|kv-more-menu/);
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

test("document rendering offers local VSCode, remote VSCode, Obsidian and copy path", () => {
  reset({selectedID: "doc-1", docState: "ready", doc});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /href="vscode:\/\/file\/srv\/vault\/%E9%A1%B9%E7%9B%AE%20%E7%9F%A5%E8%AF%86\/projects\/p1\/guides\/build\.md"/);
  assert.match(html, /href="vscode:\/\/vscode-remote\/ssh-remote\+devbox\/srv\/vault\/%E9%A1%B9%E7%9B%AE%20%E7%9F%A5%E8%AF%86\/projects\/p1\/guides\/build\.md"/);
  assert.match(html, /href="obsidian:\/\/open\?vault=AHA2%20Knowledge&amp;file=projects%2Fp1%2Fguides%2Fbuild"/);
  assert.match(html, /data-kv-copy="\/srv\/vault\/项目 知识\/projects\/p1\/guides\/build\.md"/);
  assert.match(html, /有帮助/);
  assert.match(html, /过时/);
  assert.doesNotMatch(html, /window\.open/);
});

test("without a remote host the remote action is a copy fallback, not a broken link", () => {
  reset({selectedID: "doc-1", docState: "ready", remoteHost: "", doc});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.doesNotMatch(html, /vscode-remote/);
  assert.match(html, /title="无法判断远程主机，已降级为复制路径"/);
});

test("unregistered Obsidian keeps the button and explains how to register the folder", () => {
  reset({selectedID: "doc-1", docState: "ready", obsidianReady: false, doc});
  const html = vault.renderKnowledgeVaultPage(context());
  assert.match(html, /obsidian:\/\/open/);
  assert.match(html, /先在 Obsidian 打开该文件夹/);
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