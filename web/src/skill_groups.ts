// Shared skill catalog grouping for the vault Skills tab, the legacy Skills
// page and the task picker. Bucket assignment, project ordering and per-bucket
// sorting live here so the three views cannot drift apart.

export interface SkillProjectGroup<T> {
  key: string;
  label: string;
  items: T[];
}

export interface SkillGrouping<T> {
  global: T[];
  archive: T[];
  projects: SkillProjectGroup<T>[];
}

export interface SkillGroupAccessors<T> {
  scope: (item: T) => string;
  projectKey: (item: T) => string;
  projectLabel: (item: T) => string;
  compare: (left: T, right: T) => number;
}

// groupSkillCatalog keeps the global / archived / project split after any
// search or filtering the caller already applied. Archived skills stay a
// separate read-only bucket; project skills are grouped by their owning
// project and every bucket is stably sorted by the caller's comparator.
export function groupSkillCatalog<T>(visible: T[], accessors: SkillGroupAccessors<T>): SkillGrouping<T> {
  const global = visible.filter(item => accessors.scope(item) === "global").sort(accessors.compare);
  const archive = visible.filter(item => accessors.scope(item) === "archive").sort(accessors.compare);
  const byProject = new Map<string, SkillProjectGroup<T>>();
  for (const item of visible) {
    if (accessors.scope(item) !== "project") continue;
    const key = accessors.projectKey(item);
    const group = byProject.get(key) || {key, label: accessors.projectLabel(item) || key, items: []};
    group.items.push(item);
    byProject.set(key, group);
  }
  const projects = [...byProject.values()]
    .sort((left, right) => left.label.localeCompare(right.label) || left.key.localeCompare(right.key))
    .map(group => ({...group, items: group.items.sort(accessors.compare)}));
  return {global, archive, projects};
}
