import {api} from "./api.js";
import {icon} from "./icons.js";
import type {Knowledge, ProductLine, Project, Skill, Workspace} from "./types.js";

type KnowledgeArea = "global" | "skills" | "updates" | "project";
type KnowledgeSection = "overview" | "navigation" | "project" | "settings";

export interface KnowledgeWorkspaceContext {
  projects: Project[];
  workspaces: Workspace[];
  knowledge: Knowledge[];
  refreshData: () => Promise<void>;
  render: () => void;
  setMessage: (kind: "notice" | "error", message: string) => void;
}

let selectedProjectID = "";
let activeArea: KnowledgeArea = "project";
let activeSection: KnowledgeSection = "overview";
let productLines: ProductLine[] = [];
let skills: Skill[] = [];
let catalogProjectID = "";
let catalogLoading = false;
let searchTerm = "";

const sectionLabels: Array<[KnowledgeSection, string, string]> = [
  ["overview", "projects", "\u6982\u89c8"],
  ["navigation", "knowledge", "\u9879\u76ee\u5bfc\u822a"],
  ["project", "knowledge", "\u9879\u76ee\u77e5\u8bc6"],
  ["settings", "model", "\u8bbe\u7f6e"],
];

const topLevelLabels: Array<[Exclude<KnowledgeArea, "project">, string, string]> = [
  ["global", "proxy", "\u5168\u5c40\u77e5\u8bc6"],
  ["skills", "bot", "Skills"],
  ["updates", "clock", "\u66f4\u65b0"],
];

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function statusClass(status: string): string {
  if (["active", "verified", "ready", "healthy"].includes(status)) return "good";
  if (["stale", "wrong", "failed", "error"].includes(status)) return "bad";
  if (["candidate", "pending"].includes(status)) return "warn";
  return "info";
}

function selectedProject(projects: Project[]): Project | null {
  if (!projects.length) return null;
  const current = projects.find(project => project.id === selectedProjectID);
  if (current) return current;
  selectedProjectID = projects[0].id;
  return projects[0];
}

function formatDate(value?: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], {month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit"});
}

function lineName(id?: string): string {
  return productLines.find(line => line.id === id)?.name || "\u901a\u7528";
}

function entryMatches(item: Knowledge): boolean {
  const query = searchTerm.trim().toLowerCase();
  if (!query) return true;
  return `${item.title}\n${item.body}\n${item.type}`.toLowerCase().includes(query);
}

function renderEmpty(message: string): string {
  return `<div class="empty knowledge-empty">${escapeHTML(message)}</div>`;
}

function renderKnowledgeEntry(item: Knowledge, editable = true): string {
  const source = item.source_task_id ? `<span>${escapeHTML(item.source_task_id)}${item.source_turn_id ? ` / ${escapeHTML(item.source_turn_id)}` : ""}</span>` : "";
  return `<article class="knowledge-entry-card">
    <header><div><span class="status ${statusClass(item.status)}">${escapeHTML(item.status)}</span><span class="knowledge-scope">${escapeHTML(item.scope)} / ${escapeHTML(item.type)}</span></div><small>r${Math.max(1, Number(item.revision || 1))} \u00b7 ${formatDate(item.updated_at)}</small></header>
    <h3>${escapeHTML(item.title)}</h3>
    <p>${escapeHTML(item.body)}</p>
    <footer><div>${item.product_line_id ? `<span>${icon("branch")}${escapeHTML(lineName(item.product_line_id))}</span>` : ""}${item.verified_commit ? `<span>${escapeHTML(item.verified_commit.slice(0, 10))}</span>` : ""}${source}</div>
      <div class="knowledge-entry-actions">
        <button type="button" data-knowledge-feedback="helped" data-knowledge-id="${item.id}" title="Helped">${icon("shield")} ${Number(item.helped_count || 0)}</button>
        <button type="button" data-knowledge-feedback="stale" data-knowledge-id="${item.id}" title="Stale">${icon("clock")} ${Number(item.stale_count || 0)}</button>
        ${item.status !== "verified" ? `<button type="button" data-knowledge-verify="${item.id}">\u9a8c\u8bc1</button>` : ""}
        ${editable ? `<button type="button" class="icon-button" data-knowledge-edit="${item.id}" title="\u7f16\u8f91">${icon("edit")}</button><button type="button" class="icon-button" data-knowledge-delete="${item.id}" title="\u5220\u9664">${icon("close")}</button>` : ""}
      </div>
    </footer>
  </article>`;
}

function renderEntryList(entries: Knowledge[], empty: string): string {
  const filtered = entries.filter(entryMatches);
  return `<div class="knowledge-list-toolbar"><label>${icon("filter")}<input id="knowledge-search" value="${escapeHTML(searchTerm)}" placeholder="\u641c\u7d22\u6807\u9898\u3001\u6b63\u6587\u6216\u7c7b\u578b"></label><span>${filtered.length} / ${entries.length}</span></div>
    <div class="knowledge-entry-list">${filtered.map(item => renderKnowledgeEntry(item)).join("") || renderEmpty(empty)}</div>`;
}

function renderBindings(workspaces: Workspace[]): string {
  return `<section class="panel knowledge-bindings"><div class="panel-head"><strong>Workspace Bindings</strong><span>Project \u2192 Native / WSL / SSH</span></div>
    <div class="knowledge-binding-grid">${workspaces.map(item => `<article><div class="knowledge-binding-icon">${icon(item.transport === "ssh" ? "server" : "folder")}</div><div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.transport)} \u00b7 ${escapeHTML(item.locality)} \u00b7 ${escapeHTML(item.health)}</small><p>${escapeHTML(item.root_path)}</p></div></article>`).join("") || renderEmpty("\u8be5\u9879\u76ee\u8fd8\u6ca1\u6709 Workspace Binding\u3002")}</div>
  </section>`;
}

function renderOverview(project: Project, entries: Knowledge[], projectWorkspaces: Workspace[]): string {
  const projectEntries = entries.filter(item => item.scope === "project" && item.project_id === project.id);
  const navigation = projectEntries.filter(item => item.type === "navigation");
  const projectSkills = skills.filter(item => item.scope === "project" && item.project_id === project.id);
  const recent = [...entries].filter(item => item.scope === "global" || item.project_id === project.id).sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at))).slice(0, 4);
  return `<section class="knowledge-hero panel"><div><span class="knowledge-kicker">PROJECT KNOWLEDGE</span><h2>${escapeHTML(project.name)}</h2><p>${escapeHTML(project.description || "\u5c06\u7a33\u5b9a\u9879\u76ee\u8eab\u4efd\u3001Git \u4ea7\u54c1\u7ebf\u4e0e\u6267\u884c Workspace \u5206\u79bb\u7ba1\u7406\u3002")}</p></div><div class="knowledge-policy ${project.knowledge_policy === "disabled" ? "off" : "on"}"><small>Task default</small><strong>${project.knowledge_policy === "disabled" ? "KB OFF" : "KB ON"}</strong><span>revision ${Number(project.knowledge_revision || 0)}</span></div></section>
    <div class="knowledge-metrics"><article><small>\u9879\u76ee\u77e5\u8bc6</small><strong>${projectEntries.length}</strong><span>${projectEntries.filter(item => item.status === "verified").length} verified</span></article><article><small>\u5bfc\u822a\u6761\u76ee</small><strong>${navigation.length}</strong><span>Agent pull routes</span></article><article><small>Skills</small><strong>${projectSkills.length}</strong><span>${skills.filter(item => item.enabled).length} enabled total</span></article><article><small>Product Lines</small><strong>${productLines.length}</strong><span>Git branch mapping</span></article></div>
    <section class="knowledge-pull-note">${icon("context")}<div><strong>Agent Pull</strong><span>Prompt \u53ea\u63d0\u4f9b knowledge/index.md \u548c Task \u5df2\u9009 Skill \u7684 SKILL.md \u8def\u5f84\uff0cAgent \u6309\u9700\u8bfb\u53d6\u3002</span></div></section>
    ${renderBindings(projectWorkspaces)}
    <section class="panel spaced"><div class="panel-head"><strong>\u6700\u8fd1\u66f4\u65b0</strong><span>Turn-time evidence</span></div><div class="knowledge-entry-list compact">${recent.map(item => renderKnowledgeEntry(item, false)).join("") || renderEmpty("\u5c1a\u65e0\u77e5\u8bc6\u66f4\u65b0\u3002")}</div></section>`;
}

function renderSkills(): string {
  return `<div class="knowledge-section-head"><div><h2>Skills</h2><p>Skill \u91c7\u7528 AHA \u6258\u7ba1\u5305\uff1aSKILL.md \u662f\u5165\u53e3\uff0cscripts / assets / agents \u4e0e\u6280\u80fd\u4e00\u8d77\u6309 Task \u9009\u62e9\u7269\u5316\u3002</p></div><button class="primary" type="button" data-skill-new>${icon("plus")}\u65b0\u5efa Skill</button></div>
    <div class="skill-grid">${skills.map(item => `<article class="skill-card ${item.enabled ? "" : "disabled"}"><header><div class="skill-mark">${icon("bot")}</div><div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.scope)} \u00b7 v${item.version} \u00b7 ${escapeHTML(item.status)}</small></div><label class="skill-switch"><input type="checkbox" data-skill-toggle="${item.id}" ${item.enabled ? "checked" : ""}><span></span></label></header><p>${escapeHTML(item.description || "\u672a\u586b\u5199\u63cf\u8ff0")}</p><div class="skill-package-files"><strong>Skill Package</strong><span>${(item.files || []).map(file => `<code>${escapeHTML(file)}</code>`).join("") || "<code>SKILL.md</code>"}</span></div><footer><span title="${escapeHTML(item.source_path || "")}">${escapeHTML(item.source_path || "AHA managed package")}</span><div><button class="icon-button" type="button" data-skill-edit="${item.id}">${icon("edit")}</button><button class="icon-button" type="button" data-skill-delete="${item.id}">${icon("close")}</button></div></footer></article>`).join("") || renderEmpty("\u8fd8\u6ca1\u6709 Skill\u3002")}</div>`;
}

function renderUpdates(entries: Knowledge[]): string {
  const visible = [...entries].sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at)));
  const attention = visible.filter(item => item.status === "candidate" || item.status === "stale" || item.feedback_state === "wrong");
  return `<div class="knowledge-section-head"><div><h2>\u66f4\u65b0\u4e0e\u53cd\u9988</h2><p>\u6bcf\u4e2a Turn \u83b7\u5f97\u6709\u6548\u8bc1\u636e\u540e\u5373\u65f6\u5347\u7248\uff0c\u4f7f\u7528\u540e\u56de\u4f20 helped / stale / wrong\u3002</p></div><span class="status ${attention.length ? "warn" : "good"}">${attention.length} attention</span></div>
    <div class="knowledge-update-feed">${visible.map(item => `<article><div class="knowledge-update-line"><span class="status ${statusClass(item.status)}">${escapeHTML(item.status)}</span><strong>${escapeHTML(item.title)}</strong><small>${formatDate(item.updated_at)}</small></div><p>${escapeHTML(item.scope)} / ${escapeHTML(item.type)} \u00b7 revision ${item.revision} \u00b7 helped ${item.helped_count || 0} \u00b7 stale ${item.stale_count || 0}</p>${item.source_task_id ? `<span>source: ${escapeHTML(item.source_task_id)}${item.source_turn_id ? ` / ${escapeHTML(item.source_turn_id)}` : ""}</span>` : ""}</article>`).join("") || renderEmpty("\u5c1a\u65e0\u66f4\u65b0\u8bb0\u5f55\u3002")}</div>`;
}

function renderSettings(project: Project, projectWorkspaces: Workspace[]): string {
  return `<div class="knowledge-section-head"><div><h2>\u77e5\u8bc6\u5e93\u8bbe\u7f6e</h2><p>Knowledge \u7ed1\u5b9a Project\uff1bWorkspace \u53ea\u51b3\u5b9a Agent \u5728\u54ea\u91cc\u6267\u884c\u3002</p></div></div>
    <section class="panel knowledge-setting-card"><div><strong>Task \u9ed8\u8ba4\u4f7f\u7528 KB</strong><p>Task \u53ef\u9009\u62e9\u7ee7\u627f\u3001\u5f3a\u5236\u5f00\u542f\u6216\u5f3a\u5236\u5173\u95ed\u3002</p></div><button type="button" class="${project.knowledge_policy === "disabled" ? "" : "primary"}" data-project-kb-toggle>${project.knowledge_policy === "disabled" ? "\u5df2\u5173\u95ed" : "\u5df2\u5f00\u542f"}</button></section>
    <section class="panel spaced"><div class="panel-head"><strong>Product Lines</strong><button type="button" data-product-line-new>${icon("plus")}\u65b0\u5efa</button></div><div class="product-line-list">${productLines.map(line => `<article><div>${icon("branch")}<span><strong>${escapeHTML(line.name)}</strong><small>${escapeHTML(line.branch_pattern || "*")} ${line.default ? "\u00b7 default" : ""}</small></span></div><button type="button" class="icon-button" data-product-line-delete="${line.id}">${icon("close")}</button></article>`).join("") || renderEmpty("\u672a\u914d\u7f6e Product Line\uff0c\u9879\u76ee\u77e5\u8bc6\u5c06\u4f5c\u4e3a\u901a\u7528\u6761\u76ee\u3002")}</div></section>
    ${renderBindings(projectWorkspaces)}`;
}

function renderSection(project: Project, context: KnowledgeWorkspaceContext): string {
  const projectWorkspaces = context.workspaces.filter(item => item.project_id === project.id);
  const projectEntries = context.knowledge.filter(item => item.scope === "project" && item.project_id === project.id);
  switch (activeSection) {
    case "navigation":
      return `<div class="knowledge-section-head"><div><h2>\u9879\u76ee\u5bfc\u822a</h2><p>\u5b58\u653e\u5165\u53e3\u3001\u6a21\u5757\u8fb9\u754c\u3001\u5173\u952e\u6d41\u7a0b\u4e0e\u9a8c\u8bc1\u547d\u4ee4\u3002</p></div><button class="primary" type="button" data-knowledge-new="navigation">${icon("plus")}\u65b0\u5efa\u5bfc\u822a</button></div>${renderEntryList(projectEntries.filter(item => item.type === "navigation"), "\u8fd8\u6ca1\u6709\u9879\u76ee\u5bfc\u822a\u6761\u76ee\u3002")}`;
    case "project":
      return `<div class="knowledge-section-head"><div><h2>\u9879\u76ee\u77e5\u8bc6</h2><p>\u53ea\u7ed1\u5b9a\u7a33\u5b9a Project\uff0cProduct Line \u8868\u8fbe Git \u5206\u652f\u4ea7\u54c1\u3002</p></div><button class="primary" type="button" data-knowledge-new="project">${icon("plus")}\u65b0\u5efa\u77e5\u8bc6</button></div>${renderEntryList(projectEntries.filter(item => item.type !== "navigation"), "\u4efb\u52a1 Turn \u4f1a\u5728\u83b7\u5f97\u8bc1\u636e\u65f6\u6301\u7eed\u66f4\u65b0\u9879\u76ee\u77e5\u8bc6\u3002")}`;
    case "settings": return renderSettings(project, projectWorkspaces);
    default: return renderOverview(project, context.knowledge, projectWorkspaces);
  }
}

function renderTopLevelSection(context: KnowledgeWorkspaceContext): string {
  if (activeArea === "skills") return renderSkills();
  if (activeArea === "updates") return renderUpdates(context.knowledge);
  return `<div class="knowledge-section-head"><div><h2>AHA \u5168\u5c40\u77e5\u8bc6</h2><p>\u8de8\u9879\u76ee\u901a\u7528\u7684\u7a33\u5b9a\u7ecf\u9a8c\uff0c\u4e0e\u4efb\u4f55\u5355\u4e2a Project \u5e73\u7ea7\u7ba1\u7406\u3002</p></div><button class="primary" type="button" data-knowledge-new="global">${icon("plus")}\u65b0\u5efa\u5168\u5c40\u77e5\u8bc6</button></div>${renderEntryList(context.knowledge.filter(item => item.scope === "global"), "\u8fd8\u6ca1\u6709 AHA \u5168\u5c40\u77e5\u8bc6\u3002")}`;
}

function knowledgeDialog(project: Project): string {
  return `<dialog id="knowledge-editor-dialog" class="wide"><form id="knowledge-editor-form"><div class="dialog-head"><h2>\u77e5\u8bc6\u6761\u76ee</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><input type="hidden" name="id"><div class="two"><label>\u4f5c\u7528\u57df<select name="scope"><option value="project">Project</option><option value="global">Global</option></select></label><label class="knowledge-project-field">Product Line<select name="product_line_id"><option value="">\u901a\u7528</option>${productLines.map(line => `<option value="${line.id}">${escapeHTML(line.name)} \u00b7 ${escapeHTML(line.branch_pattern || "*")}</option>`).join("")}</select></label></div><input type="hidden" name="project_id" value="${project.id}"><div class="two"><label>\u7c7b\u578b<select name="type"><option value="practice">practice</option><option value="navigation">navigation</option><option value="decision">decision</option><option value="solution">solution</option></select></label><label>\u72b6\u6001<select name="status"><option value="candidate">candidate</option><option value="verified">verified</option><option value="stale">stale</option></select></label></div><label>\u6807\u9898<input name="title" required></label><label>\u6b63\u6587<textarea name="body" rows="10" required></textarea></label><div class="two"><label>\u7f6e\u4fe1\u5ea6<input name="confidence" type="number" min="0" max="1" step="0.05" value="0.8"></label><label>Verified Commit<input name="verified_commit" placeholder="commit SHA"></label></div><label class="knowledge-project-field">Branch Scope<input name="branch_scope" placeholder="main / release/*"></label></div><div class="dialog-actions"><button type="button" data-kb-close>\u53d6\u6d88</button><button class="primary" type="submit">\u4fdd\u5b58</button></div></form></dialog>`;
}

function skillDialog(projects: Project[], projectID: string): string {
  return `<dialog id="skill-editor-dialog" class="wide"><form id="skill-editor-form"><div class="dialog-head"><h2>Skill Package</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><input type="hidden" name="id"><div class="two"><label>\u4f5c\u7528\u57df<select name="scope"><option value="global">Global</option><option value="project">Project</option></select></label><label>\u72b6\u6001<select name="status"><option value="active">active</option><option value="draft">draft</option><option value="archived">archived</option></select></label></div><label class="skill-project-field" hidden>Project<select name="project_id"><option value="">\u8bf7\u9009\u62e9</option>${projects.map(item => `<option value="${item.id}" ${item.id === projectID ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label><label>\u540d\u79f0<input name="name" required></label><label>\u63cf\u8ff0<input name="description" placeholder="\u7528\u4e8e Task \u9009\u62e9\u65f6\u5224\u65ad\u8be5 Skill \u662f\u5426\u9002\u7528"></label><label>SKILL.md \u6307\u4ee4<textarea name="instructions" rows="12" required></textarea></label><p class="field-help">AHA \u4f1a\u81ea\u52a8\u7ba1\u7406 Skill \u76ee\u5f55\u3002\u811a\u672c\u653e\u5165\u8be5\u76ee\u5f55\u7684 <code>scripts/</code> \u540e\uff0c\u4f1a\u968f\u9009\u4e2d\u7684 Skill \u4e00\u8d77\u8fdb\u5165 Task Context\u3002</p><label class="skill-enabled"><input name="enabled" type="checkbox" checked>\u542f\u7528</label></div><div class="dialog-actions"><button type="button" data-kb-close>\u53d6\u6d88</button><button class="primary" type="submit">\u4fdd\u5b58</button></div></form></dialog>`;
}

function productLineDialog(): string {
  return `<dialog id="product-line-dialog"><form id="product-line-form"><div class="dialog-head"><h2>Product Line</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><label>\u540d\u79f0<input name="name" required placeholder="Main / Enterprise"></label><label>Git Branch Pattern<input name="branch_pattern" placeholder="main / release/*"></label><label class="skill-enabled"><input name="default" type="checkbox">\u4f5c\u4e3a\u9ed8\u8ba4\u4ea7\u54c1\u7ebf</label></div><div class="dialog-actions"><button type="button" data-kb-close>\u53d6\u6d88</button><button class="primary" type="submit">\u4fdd\u5b58</button></div></form></dialog>`;
}

export function renderKnowledgeWorkspace(context: KnowledgeWorkspaceContext): string {
  const project = selectedProject(context.projects);
  if (!project && activeArea === "project") activeArea = "global";
  const projectKnowledge = project ? context.knowledge.filter(item => item.scope === "project" && item.project_id === project.id).length : 0;
  const projectMode = activeArea === "project" && project;
  return `<section class="page knowledge-page"><div class="page-head"><div><h1>Knowledge</h1><p>\u5168\u5c40\u77e5\u8bc6\u3001Skills\u3001\u66f4\u65b0\u4e0e Projects \u5e73\u7ea7\uff1bProject \u5185\u7ba1\u7406\u6982\u8ff0\u3001\u5bfc\u822a\u3001\u77e5\u8bc6\u548c\u8bbe\u7f6e\u3002</p></div><button id="knowledge-refresh" type="button">${icon("refresh")}\u5237\u65b0</button></div>
    <div class="knowledge-workspace-layout ${projectMode ? "project-mode" : "top-level"}">
      <aside class="panel knowledge-primary-rail"><header><strong>Knowledge</strong><small>${context.projects.length} projects</small></header><nav>${topLevelLabels.map(([id, iconName, label]) => `<button type="button" data-knowledge-area="${id}" class="${activeArea === id ? "active" : ""}">${icon(iconName)}<span>${label}</span></button>`).join("")}</nav><div class="knowledge-project-group"><header><strong>Projects</strong><small>${context.projects.length}</small></header><div>${context.projects.map(item => `<button type="button" data-knowledge-project="${item.id}" class="${projectMode && item.id === project?.id ? "active" : ""}"><span class="knowledge-project-avatar">${escapeHTML(item.name.slice(0, 1).toUpperCase())}</span><span><strong>${escapeHTML(item.name)}</strong><small>${item.id === project?.id ? `${projectKnowledge} knowledge` : escapeHTML(item.project_type || "project")}</small></span></button>`).join("") || renderEmpty("\u6682\u65e0 Project\u3002")}</div></div></aside>
      ${projectMode ? `<aside class="panel knowledge-section-rail"><header><strong>${escapeHTML(project.name)}</strong><small>${project.knowledge_policy === "disabled" ? "KB off" : `revision ${Number(project.knowledge_revision || 0)}`}</small></header><nav>${sectionLabels.map(([id, iconName, label]) => `<button type="button" data-knowledge-section="${id}" class="${activeSection === id ? "active" : ""}">${icon(iconName)}<span>${label}</span></button>`).join("")}</nav></aside>` : ""}
      <main class="knowledge-content ${catalogLoading ? "loading-catalog" : ""}">${projectMode ? renderSection(project, context) : renderTopLevelSection(context)}</main>
    </div>
    ${knowledgeDialog(project || ({id: ""} as Project))}${skillDialog(context.projects, project?.id || "")}${productLineDialog()}
  </section>`;
}

async function loadCatalog(projectID: string): Promise<void> {
  const [lineResponse, skillResponse] = await Promise.all([
    projectID ? api.productLines(projectID) : Promise.resolve({product_lines: []}),
    api.skills(),
  ]);
  productLines = lineResponse.product_lines || [];
  skills = skillResponse.skills || [];
  catalogProjectID = projectID;
}

function ensureCatalog(context: KnowledgeWorkspaceContext, projectID: string): void {
  if (catalogProjectID === projectID || catalogLoading) return;
  catalogLoading = true;
  void loadCatalog(projectID).catch(error => {
    context.setMessage("error", error instanceof Error ? error.message : String(error));
  }).finally(() => {
    catalogLoading = false;
    context.render();
  });
}

async function mutate(context: KnowledgeWorkspaceContext, action: () => Promise<unknown>, message: string): Promise<void> {
  try {
    await action();
    catalogProjectID = "";
    await context.refreshData();
    await loadCatalog(selectedProjectID);
    context.setMessage("notice", message);
    context.render();
  } catch (error) {
    context.setMessage("error", error instanceof Error ? error.message : String(error));
    context.render();
  }
}

function openDialog(id: string): HTMLDialogElement | null {
  const dialog = document.querySelector<HTMLDialogElement>(id);
  dialog?.showModal();
  return dialog;
}

function fillForm(form: HTMLFormElement, values: Record<string, unknown>): void {
  for (const [name, value] of Object.entries(values)) {
    const input = form.elements.namedItem(name) as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | null;
    if (!input) continue;
    if (input instanceof HTMLInputElement && input.type === "checkbox") input.checked = Boolean(value);
    else input.value = String(value ?? "");
  }
}

function syncKnowledgeScope(form: HTMLFormElement): void {
  const scope = String(new FormData(form).get("scope") || "project");
  form.querySelectorAll<HTMLElement>(".knowledge-project-field").forEach(element => element.hidden = scope === "global");
}

function syncSkillScope(form: HTMLFormElement): void {
  const scope = String(new FormData(form).get("scope") || "global");
  form.querySelectorAll<HTMLElement>(".skill-project-field").forEach(element => element.hidden = scope !== "project");
}

export function bindKnowledgeWorkspace(context: KnowledgeWorkspaceContext): void {
  const project = selectedProject(context.projects);
  ensureCatalog(context, project?.id || "");
  document.querySelectorAll<HTMLElement>("[data-knowledge-area]").forEach(button => button.addEventListener("click", () => {
    activeArea = (button.dataset.knowledgeArea || "global") as KnowledgeArea;
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-project]").forEach(button => button.addEventListener("click", () => {
    selectedProjectID = button.dataset.knowledgeProject || "";
    activeArea = "project";
    activeSection = "overview";
    productLines = [];
    catalogProjectID = "";
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-section]").forEach(button => button.addEventListener("click", () => {
    activeSection = (button.dataset.knowledgeSection || "overview") as KnowledgeSection;
    context.render();
  }));
  document.querySelector("#knowledge-refresh")?.addEventListener("click", () => void mutate(context, async () => {}, "\u77e5\u8bc6\u5de5\u4f5c\u533a\u5df2\u5237\u65b0"));
  document.querySelector<HTMLInputElement>("#knowledge-search")?.addEventListener("change", event => {
    searchTerm = event.currentTarget.value;
    context.render();
  });
  document.querySelectorAll<HTMLElement>("[data-kb-close]").forEach(button => button.addEventListener("click", () => button.closest("dialog")?.close()));

  document.querySelectorAll<HTMLElement>("[data-knowledge-new]").forEach(button => button.addEventListener("click", () => {
    const form = document.querySelector<HTMLFormElement>("#knowledge-editor-form");
    if (!form) return;
    form.reset();
    fillForm(form, {
      scope: button.dataset.knowledgeNew === "global" ? "global" : "project",
      project_id: project?.id || "",
      type: button.dataset.knowledgeNew === "navigation" ? "navigation" : "practice",
      confidence: 0.8,
      status: "candidate",
    });
    syncKnowledgeScope(form);
    openDialog("#knowledge-editor-dialog");
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-edit]").forEach(button => button.addEventListener("click", () => {
    const item = context.knowledge.find(entry => entry.id === button.dataset.knowledgeEdit);
    const form = document.querySelector<HTMLFormElement>("#knowledge-editor-form");
    if (!item || !form) return;
    form.reset();
    fillForm(form, item as unknown as Record<string, unknown>);
    syncKnowledgeScope(form);
    openDialog("#knowledge-editor-dialog");
  }));
  document.querySelector<HTMLSelectElement>("#knowledge-editor-form [name=scope]")?.addEventListener("change", event => syncKnowledgeScope(event.currentTarget.form!));
  document.querySelector<HTMLFormElement>("#knowledge-editor-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const payload = Object.fromEntries(new FormData(event.currentTarget).entries()) as Record<string, unknown>;
    const id = String(payload.id || "");
    delete payload.id;
    payload.confidence = Number(payload.confidence || 0);
    if (payload.scope === "global") {
      payload.project_id = "";
      payload.product_line_id = "";
      payload.branch_scope = "";
    }
    void mutate(context, () => id ? api.updateKnowledge(id, payload) : api.createKnowledge(payload), id ? "\u77e5\u8bc6\u6761\u76ee\u5df2\u66f4\u65b0" : "\u77e5\u8bc6\u6761\u76ee\u5df2\u521b\u5efa");
  });
  document.querySelectorAll<HTMLElement>("[data-knowledge-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm("\u786e\u5b9a\u5220\u9664\u8be5\u77e5\u8bc6\u6761\u76ee\uff1f")) return;
    void mutate(context, () => api.deleteKnowledge(button.dataset.knowledgeDelete || ""), "\u77e5\u8bc6\u6761\u76ee\u5df2\u5220\u9664");
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-verify]").forEach(button => button.addEventListener("click", () => void mutate(context, () => api.verifyKnowledge(button.dataset.knowledgeVerify || ""), "\u77e5\u8bc6\u6761\u76ee\u5df2\u9a8c\u8bc1")));
  document.querySelectorAll<HTMLElement>("[data-knowledge-feedback]").forEach(button => button.addEventListener("click", () => void mutate(context, () => api.feedbackKnowledge(button.dataset.knowledgeId || "", button.dataset.knowledgeFeedback as "helped" | "stale" | "wrong"), "\u77e5\u8bc6\u53cd\u9988\u5df2\u8bb0\u5f55")));

  document.querySelector("[data-skill-new]")?.addEventListener("click", () => {
    const form = document.querySelector<HTMLFormElement>("#skill-editor-form");
    if (!form) return;
    form.reset();
    fillForm(form, {scope: "global", project_id: project?.id || "", status: "active", enabled: true});
    syncSkillScope(form);
    openDialog("#skill-editor-dialog");
  });
  document.querySelectorAll<HTMLElement>("[data-skill-edit]").forEach(button => button.addEventListener("click", () => {
    const item = skills.find(skill => skill.id === button.dataset.skillEdit);
    const form = document.querySelector<HTMLFormElement>("#skill-editor-form");
    if (!item || !form) return;
    form.reset();
    fillForm(form, item as unknown as Record<string, unknown>);
    syncSkillScope(form);
    openDialog("#skill-editor-dialog");
  }));
  document.querySelector<HTMLSelectElement>("#skill-editor-form [name=scope]")?.addEventListener("change", event => syncSkillScope(event.currentTarget.form!));
  document.querySelector<HTMLFormElement>("#skill-editor-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const payload = Object.fromEntries(form.entries()) as Record<string, unknown>;
    const id = String(payload.id || "");
    delete payload.id;
    payload.enabled = form.get("enabled") === "on";
    if (payload.scope === "global") payload.project_id = "";
    void mutate(context, () => id ? api.updateSkill(id, payload) : api.createSkill(payload), id ? "Skill \u5df2\u66f4\u65b0" : "Skill \u5df2\u521b\u5efa");
  });
  document.querySelectorAll<HTMLInputElement>("[data-skill-toggle]").forEach(input => input.addEventListener("change", () => {
    const item = skills.find(skill => skill.id === input.dataset.skillToggle);
    if (!item) return;
    void mutate(context, () => api.updateSkill(item.id, {...item, enabled: input.checked}), input.checked ? "Skill \u5df2\u542f\u7528" : "Skill \u5df2\u505c\u7528");
  }));
  document.querySelectorAll<HTMLElement>("[data-skill-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm("\u786e\u5b9a\u5220\u9664\u8be5 Skill\uff1f")) return;
    void mutate(context, () => api.deleteSkill(button.dataset.skillDelete || ""), "Skill \u5df2\u5220\u9664");
  }));

  if (!project) return;
  document.querySelector("[data-project-kb-toggle]")?.addEventListener("click", () => void mutate(context, () => api.updateProject(project.id, {
    name: project.name,
    description: project.description || "",
    project_type: project.project_type || "folder",
    repository_identity: project.repository_identity || "",
    default_branch: project.default_branch || "",
    knowledge_policy: project.knowledge_policy === "disabled" ? "enabled" : "disabled",
  }), project.knowledge_policy === "disabled" ? "Project KB \u5df2\u5f00\u542f" : "Project KB \u5df2\u5173\u95ed"));
  document.querySelector("[data-product-line-new]")?.addEventListener("click", () => {
    document.querySelector<HTMLFormElement>("#product-line-form")?.reset();
    openDialog("#product-line-dialog");
  });
  document.querySelector<HTMLFormElement>("#product-line-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void mutate(context, () => api.createProductLine(project.id, {
      name: form.get("name"),
      branch_pattern: form.get("branch_pattern"),
      default: form.get("default") === "on",
    }), "Product Line \u5df2\u521b\u5efa");
  });
  document.querySelectorAll<HTMLElement>("[data-product-line-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm("\u786e\u5b9a\u5220\u9664\u8be5 Product Line\uff1f")) return;
    void mutate(context, () => api.deleteProductLine(project.id, button.dataset.productLineDelete || ""), "Product Line \u5df2\u5220\u9664");
  }));
}
