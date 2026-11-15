import test from "node:test";
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");
const taskVault = await import(pathToFileURL(resolve(root, "build", "task_vault_skills.js")).href);

function catalogSkill(overrides = {}) {
  return {
    key: "vault-skill:global:demo",
    id: "vault-skill:global:demo",
    slug: "demo",
    name: "Demo",
    scope: "global",
    project_id: "",
    library: "全局技能",
    description: "",
    file_count: 1,
    files: ["SKILL.md"],
    relative_path: "global/skills/demo",
    absolute_path: "/vault/global/skills/demo",
    open_path: "/vault/global/skills/demo",
    source: "vault",
    auto_attach: false,
    auto_attach_effective: false,
    auto_attach_confirmed: false,
    can_update: true,
    project_bound: true,
    unavailable_reason: "",
    ...overrides,
  };
}

test("archive vault skills never enter the task selector", () => {
  const skills = taskVault.vaultTaskSkillsFromCatalog([
    catalogSkill(),
    catalogSkill({key: "vault-skill:project:p1:review", id: "vault-skill:project:p1:review", slug: "review", name: "Review", scope: "project", project_id: "p1"}),
    catalogSkill({
      key: "vault-skill:archive:p1:old", id: "vault-skill:archive:p1:old", slug: "old", name: "Old",
      scope: "archive", project_id: "p1", can_update: false, unavailable_reason: "archive_read_only",
    }),
  ]);
  assert.deepEqual(skills.map(skill => skill.id), ["vault-skill:global:demo", "vault-skill:project:p1:review"]);
  assert.equal(skills.find(skill => skill.id === "vault-skill:project:p1:review")?.scope, "project");
  assert.equal(skills.some(skill => skill.scope === "archive" || skill.name === "Old"), false);
});

test("vault task skills stay hidden until runtime read and the index are ready", () => {
  const skills = taskVault.vaultTaskSkillsFromCatalog([catalogSkill()]);
  assert.deepEqual(taskVault.vaultTaskSkillsForProject(skills, true, false, ["p1"], "p1"), []);
  assert.deepEqual(taskVault.vaultTaskSkillsForProject(skills, false, true, ["p1"], "p1"), []);
  assert.deepEqual(taskVault.vaultTaskSkillsForProject(skills, true, true, [], "p1"), []);
  assert.deepEqual(taskVault.vaultTaskSkillsForProject(skills, true, true, ["p1"], "p1").map(skill => skill.id), ["vault-skill:global:demo"]);
  assert.match(taskVault.vaultTaskSkillHint(true, true, false, ["p1"], "p1"), /索引尚未就绪/);
  assert.match(taskVault.vaultTaskSkillHint(true, true, true, [], "p1"), /未绑定/);
});

test("a project skill wins over a global skill with the same slug", () => {
  const skills = taskVault.vaultTaskSkillsFromCatalog([
    catalogSkill(),
    catalogSkill({key: "vault-skill:project:p1:demo", id: "vault-skill:project:p1:demo", scope: "project", project_id: "p1"}),
  ]);
  const offered = taskVault.vaultTaskSkillsForProject(skills, true, true, ["p1"], "p1");
  assert.equal(offered.length, 1);
  assert.equal(offered[0].id, "vault-skill:project:p1:demo");
  assert.equal(offered[0].scope, "project");
});