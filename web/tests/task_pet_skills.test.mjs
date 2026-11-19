import test from "node:test";
import assert from "node:assert/strict";
import {resolve} from "node:path";
import {readFile} from "node:fs/promises";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "..");
const taskVault = await import(pathToFileURL(resolve(root, "build", "task_vault_skills.js")).href);
const agents = await import(pathToFileURL(resolve(root, "build", "task_agents.js")).href);

const PET = taskVault.PET_PROJECT_ID;
const BU2 = "vault-skill:project:project_bu2:bulbcam-bug-intake";
const DISABLED = "vault-skill:project:project_bu2:disabled-score";
const GLOBAL = "vault-skill:global:demo";
const OTHER = "vault-skill:project:project_other:other-skill";

function catalogSkill(overrides = {}) {
  return {
    key: GLOBAL,
    id: GLOBAL,
    slug: "demo",
    name: "Demo",
    scope: "global",
    project_id: "",
    project_name: "",
    library: "全局技能",
    description: "global demo",
    version: 1,
    status: "active",
    enabled: true,
    file_count: 1,
    files: ["SKILL.md"],
    relative_path: "global/skills/demo",
    absolute_path: "/vault/global/skills/demo",
    open_path: "/vault/global/skills/demo/SKILL.md",
    source: "vault",
    auto_attach: false,
    auto_attach_effective: false,
    auto_attach_confirmed: false,
    can_update: true,
    project_bound: false,
    unavailable_reason: "",
    ...overrides,
  };
}

function petCatalog() {
  return [
    catalogSkill(),
    catalogSkill({
      key: BU2, id: BU2, slug: "bulbcam-bug-intake", name: "Bulbcam Bug Intake",
      scope: "project", project_id: "project_bu2", project_name: "BU2_CAM1",
      description: "Camera bug intake workflow", version: 4,
    }),
    catalogSkill({
      key: DISABLED, id: DISABLED, slug: "disabled-score", name: "Disabled Score",
      scope: "project", project_id: "project_bu2", project_name: "BU2_CAM1",
      enabled: false, version: 2,
    }),
    catalogSkill({
      key: OTHER, id: OTHER, slug: "other-skill", name: "Other Skill",
      scope: "project", project_id: "project_other", project_name: "Other Project",
    }),
  ];
}

test("the pet catalog keeps every project's skill with its version and state", () => {
  const mapped = taskVault.vaultTaskSkillsFromCatalog(petCatalog());
  const offered = taskVault.vaultTaskSkillsForProject(mapped, false, false, [], PET);
  const byID = new Map(offered.map(skill => [skill.id, skill]));
  assert.deepEqual([...byID.keys()].sort(), [BU2, DISABLED, GLOBAL, OTHER].sort());
  const bu2 = byID.get(BU2);
  assert.equal(bu2.version, 4);
  assert.equal(bu2.status, "active");
  assert.equal(bu2.enabled, true);
  assert.equal(bu2.project_id, "project_bu2");
  assert.equal(bu2.project_name, "BU2_CAM1");
  // A disabled package stays visible so the picker can explain it, but it is
  // not silently turned into an enabled option.
  assert.equal(byID.get(DISABLED).enabled, false);
});

test("normal projects keep their existing project-scoped selection", () => {
  const mapped = taskVault.vaultTaskSkillsFromCatalog(petCatalog());
  const offered = taskVault.vaultTaskSkillsForProject(mapped, true, true, ["project_other"], "project_other");
  assert.deepEqual(offered.map(skill => skill.id), [GLOBAL, OTHER]);
  // The pet is not blocked by a project binding it does not own, while a
  // normal unbound project still hides the vault picker.
  assert.equal(taskVault.vaultTaskSkillSourceActive(true, true, true, [], PET), true);
  assert.equal(taskVault.vaultTaskSkillSourceActive(true, true, true, [], "project_other"), false);
  assert.equal(taskVault.vaultTaskSkillHint(true, true, true, [], PET), "");
  assert.match(taskVault.vaultTaskSkillHint(true, true, true, [], "project_other"), /未绑定/);
});

test("the pet default selects only global auto-attach skills", () => {
  const skills = taskVault.vaultTaskSkillsFromCatalog([
    catalogSkill({auto_attach: true, auto_attach_effective: true}),
    catalogSkill({
      key: BU2, id: BU2, slug: "bulbcam-bug-intake", name: "Bulbcam Bug Intake",
      scope: "project", project_id: "project_bu2", project_name: "BU2_CAM1",
      auto_attach: true, auto_attach_effective: true,
    }),
  ]);
  assert.deepEqual(taskVault.taskAutoSkillIDs(skills, true, PET), [GLOBAL]);
});

test("the pet picker groups by project and exposes search, version and disabled state", () => {
  const skills = taskVault.vaultTaskSkillsFromCatalog(petCatalog());
  const html = agents.renderTaskSkills(skills, PET, []);
  assert.match(html, /data-task-skill-search/);
  assert.match(html, /data-task-skill-group="global"/);
  assert.match(html, /data-task-skill-group="project"/);
  assert.match(html, /data-task-skill-project="project_bu2"/);
  assert.match(html, /data-task-skill-project-label="project_bu2"/);
  assert.match(html, /BU2_CAM1/);
  assert.match(html, /Bulbcam Bug Intake/);
  assert.match(html, /v4/);
  assert.match(html, /已禁用/);
  // Disabled skills are rendered but cannot be selected.
  const disabledAt = html.indexOf(`value="${DISABLED}"`);
  assert.ok(disabledAt > 0, "disabled skill was not rendered");
  assert.match(html.slice(disabledAt, disabledAt + 80), /\sdisabled/);
  // Nothing is checked by default: project skills are never auto-selected.
  assert.equal(html.includes(`value="${BU2}" checked`), false);
  // A saved selection is read back into the picker.
  const selected = agents.renderTaskSkills(skills, PET, [BU2]);
  assert.equal(selected.includes(`value="${BU2}" checked`), true);
});

test("pet skill search matches name, project and description", () => {
  const mapped = taskVault.vaultTaskSkillsFromCatalog(petCatalog());
  const bu2 = mapped.find(skill => skill.id === BU2);
  assert.equal(agents.taskSkillMatchesSearch(bu2, "bulbcam"), true);
  assert.equal(agents.taskSkillMatchesSearch(bu2, "BU2"), true);
  assert.equal(agents.taskSkillMatchesSearch(bu2, "camera bug"), true);
  assert.equal(agents.taskSkillMatchesSearch(bu2, "不存在的词"), false);
  assert.equal(agents.taskSkillMatchesSearch(bu2, ""), true);
  const filtered = agents.renderTaskSkills(mapped, PET, [], {query: "BU2"});
  assert.match(filtered, /Bulbcam Bug Intake/);
  assert.doesNotMatch(filtered, /Other Skill/);
  assert.match(filtered, /没有匹配的技能/);
});

test("normal task pickers still hide other projects and the search box", () => {
  const skills = taskVault.vaultTaskSkillsFromCatalog(petCatalog());
  const html = agents.renderTaskSkills(skills, "project_other", []);
  assert.match(html, /Other Skill/);
  assert.doesNotMatch(html, /Bulbcam Bug Intake/);
  assert.doesNotMatch(html, /data-task-skill-search/);
});

test("the main screen wires the pet search input to the in-place filter", async () => {
  const main = await readFile(resolve(root, "src", "main.ts"), "utf8");
  assert.match(main, /filterTaskSkillOptions\(target\.closest\("\.task-skill-options"\)/);
  assert.match(main, /bindTaskSkillSearch\(document\.querySelector<HTMLElement>\("#agent-config-dialog"\)\)/);
});
