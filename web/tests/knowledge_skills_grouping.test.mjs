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
