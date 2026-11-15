import {api} from "./api.js";
import type {KnowledgeVaultSkill, Skill} from "./types.js";

// The task picker merges legacy database skills with vault packages. This
// module is lazy-loaded only on the Tasks view so the vault-specific mapping
// and copy stay out of the first-screen bundle. The vault API itself is
// optional: a disabled or unavailable vault must leave legacy skills untouched.
export type VaultTaskSkillBundle = {
  available: boolean;
  runtimeRead: boolean;
  boundProjects: string[];
  skills: Skill[];
};

function asTaskSkill(skill: KnowledgeVaultSkill): Skill {
  return {
    id: skill.key,
    package_slug: skill.slug,
    scope: skill.scope === "project" ? "project" : "global",
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

export async function loadVaultTaskSkills(): Promise<VaultTaskSkillBundle> {
  try {
    const vault = await api.knowledgeVaultSkills();
    return {
      available: true,
      runtimeRead: Boolean(vault.runtime_read),
      boundProjects: vault.bound_project_ids || [],
      skills: (vault.skills || []).map(asTaskSkill),
    };
  } catch {
    return {available: false, runtimeRead: false, boundProjects: [], skills: []};
  }
}

export function vaultTaskSkillsForProject(
  skills: Skill[],
  runtimeRead: boolean,
  boundProjects: string[],
  projectID: string,
  includeIDs: string[] = [],
): Skill[] {
  const included = new Set(includeIDs);
  return skills.filter(skill => {
    if (included.has(skill.id)) return true;
    if (!runtimeRead || !projectID) return false;
    if (skill.scope === "project") return skill.project_id === projectID;
    return skill.scope === "global" && boundProjects.includes(projectID);
  });
}

export function vaultTaskSkillHint(
  available: boolean,
  runtimeRead: boolean,
  boundProjects: string[],
  projectID: string,
): string {
  if (!available || !projectID) return "";
  if (!runtimeRead) return "知识库运行时读取未开启，vault 技能不会自动启用；旧技能不受影响。";
  if (!boundProjects.includes(projectID)) return "当前项目未绑定知识库项目库，vault 技能不会出现在这里；旧技能不受影响。";
  return "";
}