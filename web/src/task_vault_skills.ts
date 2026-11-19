import {api} from "./api.js";
import type {KnowledgeVaultSkill, Skill} from "./types.js";

// The task picker lists vault skill packages only. This module is lazy-loaded
// only on the Tasks view so the vault-specific mapping and copy stay out of
// the first-screen bundle. A disabled or unavailable vault yields an empty
// picker instead of falling back to the retired legacy skills table.
export type VaultTaskSkillBundle = {
  available: boolean;
  runtimeRead: boolean;
  indexReady: boolean;
  boundProjects: string[];
  skills: Skill[];
};

// The Pet Task's system project. The Web already uses this id to recognise the
// pet (for example the built-in pet MCP), so the skill picker follows the same
// identity instead of adding a second "is pet" flag to every API.
export const PET_PROJECT_ID = "project_pet";

function asTaskSkill(skill: KnowledgeVaultSkill): Skill | null {
  // Archives are read-only and never apply to a task. Mapping them to a global
  // scope used to offer an option that the server rejects at submit time.
  if (!skill.can_update || (skill.scope !== "global" && skill.scope !== "project")) return null;
  return {
    id: skill.key,
    package_slug: skill.slug,
    scope: skill.scope,
    project_id: skill.project_id,
    project_name: skill.project_name,
    name: skill.name,
    description: skill.description || "",
    instructions: "",
    version: skill.version || 1,
    status: skill.status || "active",
    // Keep the server's state: the Pet Task picker must show disabled entries
    // and cannot offer them for selection.
    enabled: skill.enabled !== false,
    unavailable_reason: skill.unavailable_reason,
    auto_attach: skill.auto_attach && skill.auto_attach_effective,
    source: "vault",
    source_path: skill.open_path,
    files: skill.files,
    created_at: "",
    updated_at: "",
  };
}

export function vaultTaskSkillsFromCatalog(items: KnowledgeVaultSkill[]): Skill[] {
  return items.map(asTaskSkill).filter((skill): skill is Skill => skill !== null);
}

export async function loadVaultTaskSkills(): Promise<VaultTaskSkillBundle> {
  try {
    const vault = await api.knowledgeVaultSkills();
    return {
      available: true,
      runtimeRead: Boolean(vault.runtime_read),
      indexReady: Boolean(vault.index_ready),
      boundProjects: vault.bound_project_ids || [],
      skills: vaultTaskSkillsFromCatalog(vault.skills || []),
    };
  } catch {
    return {available: false, runtimeRead: false, indexReady: false, boundProjects: [], skills: []};
  }
}

export function isPetTaskProject(projectID: string): boolean {
  return projectID === PET_PROJECT_ID;
}

export function vaultTaskSkillsForProject(
  skills: Skill[],
  runtimeRead: boolean,
  indexReady: boolean,
  boundProjects: string[],
  projectID: string,
  includeIDs: string[] = [],
): Skill[] {
  const included = new Set(includeIDs);
  const pet = isPetTaskProject(projectID);
  const applicable = skills.filter(skill => {
    if (included.has(skill.id)) return true;
    if (!projectID) return false;
    // The Pet Task is the owner's system administrator view: it sees global
    // skills and every project's skills without owning a project vault space.
    // Disabled entries stay in the list so the picker can show their state, but
    // the picker disables them and the server rejects them on save.
    if (pet) return skill.scope === "global" || skill.scope === "project";
    if (!runtimeRead || !indexReady) return false;
    if (skill.scope === "project") return skill.project_id === projectID;
    return skill.scope === "global" && boundProjects.includes(projectID);
  });
  // The same slug may exist globally and in the project library. Keep one so
  // the prompt never lists two Selected Skills for the same package; the
  // project copy wins because it is the more specific choice. The pet view is
  // grouped by project, so two projects may intentionally offer the same slug.
  if (pet) return applicable;
  const bySlug = new Map<string, Skill>();
  for (const skill of applicable) {
    const slug = skill.package_slug || skill.id;
    const current = bySlug.get(slug);
    if (!current || (skill.scope === "project" && current.scope !== "project")) bySlug.set(slug, skill);
  }
  return [...bySlug.values()];
}

export function vaultTaskSkillHint(
  available: boolean,
  runtimeRead: boolean,
  indexReady: boolean,
  boundProjects: string[],
  projectID: string,
): string {
  if (!available || !projectID) return "";
  if (!runtimeRead) return "知识库运行时读取未开启，vault 技能暂不可用。";
  if (!indexReady) return "知识库索引尚未就绪，vault 技能暂不可用。";
  // The pet has no project binding of its own; its cross-project view is the
  // designed state, so the normal "project is not bound" warning is wrong.
  if (isPetTaskProject(projectID)) return "";
  if (!boundProjects.includes(projectID)) return "当前项目未绑定知识库项目库，vault 技能不会出现在这里。";
  return "";
}

// vaultTaskSkillSourceActive mirrors the server gate: a project takes its
// auto-attach source from the vault only when the vault is enabled, runtime
// reads are on, the index is ready and the project is bound. The Pet Task reads
// global auto-attach preferences through the global gate, without a binding.
export function vaultTaskSkillSourceActive(
  available: boolean,
  runtimeRead: boolean,
  indexReady: boolean,
  boundProjects: string[],
  projectID: string,
): boolean {
  if (!projectID) return false;
  if (isPetTaskProject(projectID)) return available && runtimeRead && indexReady;
  return available && runtimeRead && indexReady && boundProjects.includes(projectID);
}

// taskAutoSkillIDs pre-selects the vault auto-attach preferences that are
// effective for the project. The legacy AutoAttach column is never a source.
export function taskAutoSkillIDs(skills: Skill[], vaultSourceActive: boolean, projectID: string): string[] {
  return skills
    .filter(skill => (skill.source === "vault") === vaultSourceActive)
    .filter(skill => Boolean(skill.auto_attach) &&
      (skill.scope === "global" || (skill.scope === "project" && skill.project_id === projectID)))
    .map(skill => skill.id);
}

export function vaultTaskAutoSourceHint(vaultSourceActive: boolean): string {
  if (!vaultSourceActive) return "";
  return "当前项目使用知识库（vault）技能源：自动启用只来自 vault。";
}
