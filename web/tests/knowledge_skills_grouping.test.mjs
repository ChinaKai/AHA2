// Skill catalog grouping: global / archive / project sections survive search
// and filtering, project skills keep their owning project visible, and the
// task picker reuses the same grouping without offering read-only archives.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {readFile} from "node:fs/promises";
import {pathToFileURL} from "node:url";

const build = resolve(import.meta.dirname, "..", "build");
const workspace = await import(pathToFileURL(resolve(build, "knowledge_workspace.js")).href);
const agents = await import(pathToFileURL(resolve(build, "task_agents.js")).href);
const vault = await import(pathToFileURL(resolve(build, "knowledge_vault.js")).href);

const base = {
  instructions: "", version: 1, status: "active", enabled: true,
  can_update: true, source: "vault", files: ["SKILL.md"], created_at: "", updated_at: "",
};
const skills = [
  {...base, id: "vault-skill:global:flat-one", package_slug: "flat-one", scope: "global", name: "Flat Global"},
  {...base, id: "vault-skill:global:global-two", package_slug: "global-two", scope: "global", name: "Global Two", auto_attach: true},
  {...base, id: "vault-skill:archive:project-9:old", package_slug: "old", scope: "archive", project_id: "project-9", name: "Archived Review", can_update: false},
  {...base, id: "vault-skill:project:project-1:build", package_slug: "build", scope: "project", project_id: "project-1", name: "Build Checks"},
  {...base, id: "vault-skill:project:project-1:review", package_slug: "review", scope: "project", project_id: "project-1", name: "Review Checks"},
  {...base, id: "vault-skill:project:project-2:deploy", package_slug: "deploy", scope: "project", project_id: "project-2", name: "Deploy Checks"},
];

function context() {
  return {
    projects: [
      {id: "project-1", name: "Project One"},
      {id: "project-2", name: "Project Two"},
    ],
    workspaces: [], knowledge: [], libraries: [], proposals: [],
    reviewSettings: {auto_approve: false},
    refreshData: async () => {}, render: () => {}, setMessage: () => {},
  };
}

test("skill page renders global, archived, and project groups in order", () => {
  workspace.__setKnowledgeSkillsForTest(skills);
  workspace.__resetKnowledgeSkillFiltersForTest();
  const html = workspace.renderKnowledgeSkillsPage(context());
  const groups = [...html.matchAll(/data-skill-group="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(groups, ["global", "archive", "project"]);
  assert.match(html, /全局技能/);
  assert.match(html, /归档技能/);
  assert.match(html, /项目技能/);
  // Both flat and global/skills packages belong to the global group.
  assert.match(html, /Flat Global/);
  assert.match(html, /Global Two/);
  // Project cards spell out the owning project name.
  assert.match(html, /项目 · Project One/);
  assert.match(html, /项目 · Project Two/);
  // Archives are read-only: no edit/delete/toggle, but the view action stays.
  const archiveCard = html.slice(html.indexOf("Archived Review") - 400, html.indexOf("Archived Review") + 1200);
  assert.match(archiveCard, /只读/);
  assert.doesNotMatch(archiveCard, /data-skill-edit/);
  assert.doesNotMatch(archiveCard, /data-skill-delete/);
  assert.doesNotMatch(archiveCard, /data-skill-toggle/);
  assert.match(archiveCard, /data-skill-view/);
  // Writable scopes are read-only on the Web too: no new/edit/delete/toggle.
  assert.doesNotMatch(html, /data-skill-new/);
  const writableCard = html.slice(html.indexOf("Build Checks") - 400, html.indexOf("Build Checks") + 1200);
  assert.doesNotMatch(writableCard, /data-skill-(edit|delete|toggle)/);
  assert.match(writableCard, /data-skill-view/);
});

test("empty groups are omitted and filtering preserves the group split", () => {
  const onlyArchive = workspace.skillGroupSections(context(), [skills[2]]);
  assert.deepEqual(onlyArchive.map(group => group.key), ["archive"]);
  const onlyProject = workspace.skillGroupSections(context(), [skills[3], skills[3]]);
  assert.deepEqual(onlyProject.map(group => group.key), ["project"]);
  const searchHit = workspace.skillGroupSections(context(), [skills[1], skills[5]]);
  assert.deepEqual(searchHit.map(group => group.key), ["global", "project"]);
  assert.equal(searchHit[1].items[0].project_id, "project-2");
});

test("task picker reuses the grouping and never offers archives", () => {
  const html = agents.renderTaskSkills(skills, "project-1", ["vault-skill:project:project-1:build"]);
  const groups = [...html.matchAll(/data-task-skill-group="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(groups, ["global", "project"]);
  assert.match(html, /全局技能/);
  assert.match(html, /项目技能/);
  assert.match(html, /data-task-skill-project="project-1"/);
  assert.doesNotMatch(html, /Archived Review/);
  // Another project's skill is not offered for this task.
  assert.doesNotMatch(html, /Deploy Checks/);
  assert.match(html, /value="vault-skill:project:project-1:build" checked/);
  assert.doesNotMatch(html, /value="vault-skill:archive:project-9:old"/);
});

test("task picker marks a historical legacy id as unavailable", () => {
  const html = agents.renderTaskSkills(skills, "project-1", ["vault-skill:project:project-1:build", "skill_legacy_old"]);
  assert.match(html, /data-task-skill-group="unavailable"/);
  assert.match(html, /<strong>skill_legacy_old<\/strong>/);
  assert.match(html, /无法在 vault 中解析/);
  // It is never turned into a selectable legacy option.
  assert.doesNotMatch(html, /value="skill_legacy_old"/);
});

test("vault Skills tab groups global, archived, and per-project packages", () => {
  const catalog = [
    {key: "vault-skill:global:flat:flat-one", id: "vault-skill:global:flat:flat-one", slug: "flat-one", name: "Flat Global", scope: "global", library: "全局", file_count: 1, relative_path: "skills/flat-one", absolute_path: "/vault/skills/flat-one", open_path: "/vault/skills/flat-one/SKILL.md", source: "vault", auto_attach: false, auto_attach_effective: false, auto_attach_confirmed: false, can_update: true, project_bound: true},
    {key: "vault-skill:global:global-two", id: "vault-skill:global:global-two", slug: "global-two", name: "Global Two", scope: "global", library: "全局", file_count: 1, relative_path: "global/skills/global-two", absolute_path: "/vault/global/skills/global-two", open_path: "/vault/global/skills/global-two/SKILL.md", source: "vault", auto_attach: true, auto_attach_effective: true, auto_attach_confirmed: true, can_update: true, project_bound: true},
    {key: "vault-skill:archive:project-9:old", id: "vault-skill:archive:project-9:old", slug: "old", name: "Archived Review", scope: "archive", project_id: "project-9", project_name: "Archive Project", library: "Archive Project", file_count: 1, relative_path: "projects/old/skills/old", absolute_path: "/vault/projects/old/skills/old", open_path: "/vault/projects/old/skills/old/SKILL.md", source: "vault", auto_attach: false, auto_attach_effective: false, auto_attach_confirmed: false, can_update: false, project_bound: false, unavailable_reason: "archive_read_only"},
    {key: "vault-skill:project:project-1:review", id: "vault-skill:project:project-1:review", slug: "review", name: "Review Checks", scope: "project", project_id: "project-1", project_name: "Project One", library: "Project One", file_count: 1, relative_path: "projects/one/skills/review", absolute_path: "/vault/projects/one/skills/review", open_path: "/vault/projects/one/skills/review/SKILL.md", source: "vault", auto_attach: false, auto_attach_effective: false, auto_attach_confirmed: false, can_update: true, project_bound: true},
    {key: "vault-skill:project:project-1:build", id: "vault-skill:project:project-1:build", slug: "build", name: "Build Checks", scope: "project", project_id: "project-1", project_name: "Project One", library: "Project One", file_count: 1, relative_path: "projects/one/skills/build", absolute_path: "/vault/projects/one/skills/build", open_path: "/vault/projects/one/skills/build/SKILL.md", source: "vault", auto_attach: false, auto_attach_effective: false, auto_attach_confirmed: false, can_update: true, project_bound: true},
    {key: "vault-skill:project:project-2:deploy", id: "vault-skill:project:project-2:deploy", slug: "deploy", name: "Deploy Checks", scope: "project", project_id: "project-2", project_name: "Project Two", library: "Project Two", file_count: 1, relative_path: "projects/two/skills/deploy", absolute_path: "/vault/projects/two/skills/deploy", open_path: "/vault/projects/two/skills/deploy/SKILL.md", source: "vault", auto_attach: false, auto_attach_effective: false, auto_attach_confirmed: false, can_update: true, project_bound: true},
  ];
  vault.__resetKnowledgeVaultStateForTest();
  vault.__setKnowledgeVaultStateForTest({
    initialized: true, loading: "ready", vaultReady: true, indexReady: true,
    tab: "skills", skillsState: "ready", skillRuntimeRead: true,
    projects: [{id: "project:project-1", kind: "project", name: "Project One", project_id: "project-1", count: 2}],
    skills: catalog,
  });
  const html = vault.renderKnowledgeVaultPage(context());
  const groups = [...html.matchAll(/data-kv-skill-group="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(groups, ["global", "archive", "project"]);
  assert.match(html, /全局技能/);
  assert.match(html, /归档技能/);
  assert.match(html, /项目技能/);
  // Project packages are grouped by project, project names are visible, and
  // each project's cards are stably sorted.
  const projectKeys = [...html.matchAll(/data-kv-skill-project="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(projectKeys, ["project-1", "project-2"]);
  assert.match(html, /<h3>Project One<\/h3>/);
  assert.match(html, /<h3>Project Two<\/h3>/);
  const projectSection = html.slice(html.indexOf('data-kv-skill-group="project"'));
  assert.ok(projectSection.indexOf("Build Checks") < projectSection.indexOf("Review Checks"));
  // Archives stay read-only: the auto-attach switch is disabled and the scope
  // label distinguishes archived from active projects.
  assert.match(html, /归档 · Archive Project/);
  const archiveCard = html.slice(html.indexOf("Archived Review") - 500, html.indexOf("Archived Review") + 1400);
  assert.match(archiveCard, /data-kv-skill-auto="vault-skill:archive:project-9:old"[^>]*disabled/);
  // No Web content editing exists on the vault Skills tab.
  assert.doesNotMatch(html, /data-kv-skill-(new|edit|delete)/);

  // Empty buckets are hidden, and per-project order does not depend on input order.
  const grouping = vault.vaultSkillGrouping([catalog[4], catalog[3], catalog[0]]);
  assert.deepEqual(grouping.archive, []);
  assert.equal(grouping.projects.length, 1);
  assert.deepEqual(grouping.projects[0].items.map(item => item.name), ["Build Checks", "Review Checks"]);
});

test("root library tree keeps the navigation page as the clickable folder", () => {
  const navigationID = "vault-navigation:notes:index.md";
  const tree = vault.buildVaultDirectoryTree([
    {id: navigationID, title: "Notes", path: "notes/index.md", type: "navigation", status: "verified", scope: "root", is_index: true, system: true},
    {id: "vault_note", title: "Root note", path: "notes/doc.md", type: "reference", status: "verified", scope: "root", is_index: false},
    {id: "vault_nested", title: "Nested", path: "notes/sub/index.md", type: "navigation", status: "verified", scope: "root", is_index: true},
  ], "all");
  assert.equal(tree.length, 1);
  assert.equal(tree[0].path, "notes");
  assert.equal(tree[0].id, navigationID);
  assert.equal(tree[0].has_index, true);
  const paths = tree[0].children.map(node => node.path);
  assert.ok(paths.includes("notes/doc.md"));
  assert.ok(paths.includes("notes/sub"));
});

test("task settings offer only the enabled/disabled knowledge switch", async () => {
  const root = resolve(import.meta.dirname, "..");
  const agents = await readFile(resolve(root, "src", "task_agents.ts"), "utf8");
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  for (const source of [agents, main]) {
    assert.doesNotMatch(source, /knowledge_policy[^\n]*value="inherit"/);
    assert.doesNotMatch(source, /value="inherit"[^\n]*knowledge_policy/);
  }
  assert.match(agents, /name="knowledge_policy"><option value="enabled"/);
  assert.match(main, /name="knowledge_policy"><option value="enabled"/);
  // The create form no longer defaults the save payload to inherit.
  assert.match(main, /knowledge_policy: String\(form\.get\("knowledge_policy"\) \|\| "enabled"\)/);
});
