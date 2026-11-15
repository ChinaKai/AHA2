import {api} from "./api.js";
import type {KnowledgeVaultSkill, Skill} from "./types.js";

// The task picker merges legacy database skills with vault packages. This
// module is lazy-loaded only on the Tasks view so the vault-specific mapping
// and copy stay out of the first-screen bundle. The vault API itself is
// optional: a disabled or unavailable vault must leave legacy skills untouched.
export type VaultTaskSkillBundle = {
  available: boolean;
  runtimeRead: boolean;
  indexReady: boolean;
  boundProjects: string[];
  skills: Skill[];
};

function asTaskSkill(skill: KnowledgeVaultSkill): Skill | null {
  // Archives are read-only and never apply to a task. Mapping them to a global
  // scope used to offer an option that the server rejects at submit time.
  if (!skill.can_update || (skill.scope !== "global" && skill.scope !== "project")) return null;
  return {
    id: skill.key,
    package_slug: skill.slug,
    scope: skill.scope,
    project_id: skill.project_id,
    name: skill.name,
    description: skill.description || "",
    instructions: "",
    version: 1,
    status: "active",
    enabled: true,
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

export function vaultTaskSkillsForProject(
  skills: Skill[],
  runtimeRead: boolean,
  indexReady: boolean,
  boundProjects: string[],
  projectID: string,
  includeIDs: string[] = [],
): Skill[] {
  const included = new Set(includeIDs);
  const applicable = skills.filter(skill => {
    if (included.has(skill.id)) return true;
    if (!runtimeRead || !indexReady || !projectID) return false;
    if (skill.scope === "project") return skill.project_id === projectID;
    return skill.scope === "global" && boundProjects.includes(projectID);
  });
  // The same slug may exist globally and in the project library. Keep one so
  // the prompt never lists two Selected Skills for the same package; the
  // project copy wins because it is the more specific choice.
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
  if (!runtimeRead) return "知识库运行时读取未开启，vault 技能不会自动启用；旧技能不受影响。";
  if (!indexReady) return "知识库索引尚未就绪，vault 技能暂不可用；旧技能不受影响。";
  if (!boundProjects.includes(projectID)) return "当前项目未绑定知识库项目库，vault 技能不会出现在这里；旧技能不受影响。";
  return "";
}

// vaultTaskSkillSourceActive mirrors the server gate: a project takes its
// auto-attach source from the vault only when the vault is enabled, runtime
// reads are on, the index is ready and the project is bound.
export function vaultTaskSkillSourceActive(
  available: boolean,
  runtimeRead: boolean,
  indexReady: boolean,
  boundProjects: string[],
  projectID: string,
): boolean {
  return Boolean(projectID) && available && runtimeRead && indexReady && boundProjects.includes(projectID);
}

// taskAutoSkillIDs pre-selects exactly one auto-attach source. When the vault
// is effective for the project the legacy AutoAttach rows are not offered,
// otherwise the same package would be mounted twice (legacy id plus
// vault-skill id). Projects without an effective vault keep the legacy rule.
export function taskAutoSkillIDs(skills: Skill[], vaultSourceActive: boolean, projectID: string): string[] {
  return skills
    .filter(skill => (skill.source === "vault") === vaultSourceActive)
    .filter(skill => Boolean(skill.auto_attach) &&
      (skill.scope === "global" || (skill.scope === "project" && skill.project_id === projectID)))
    .map(skill => skill.id);
}

export function vaultTaskAutoSourceHint(vaultSourceActive: boolean): string {
  if (!vaultSourceActive) return "";
  return "当前项目使用知识库（vault）技能源：自动启用只来自 vault，旧技能库的自动启用不再挂载。";
}
