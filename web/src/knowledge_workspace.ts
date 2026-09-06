import {api} from "./api.js";
import {icon} from "./icons.js";
import {renderMarkdown} from "./markdown.js";
import type {Knowledge, KnowledgeProposal, ProductLine, Project, Skill, Workspace} from "./types.js";

type KnowledgeArea = "global" | "skills" | "updates" | "project";
type KnowledgeSection = "overview" | "project" | "navigation" | "settings";
type KnowledgeDocumentKind = "global" | "project" | "project-navigation";

export interface KnowledgeWorkspaceContext {
  projects: Project[];
  workspaces: Workspace[];
  knowledge: Knowledge[];
  proposals: KnowledgeProposal[];
  refreshData: () => Promise<void>;
  render: () => void;
  setMessage: (kind: "notice" | "error", message: string) => void;
}

let selectedProjectID = "";
let activeArea: KnowledgeArea = "updates";
let activeSection: KnowledgeSection = "overview";
let productLines: ProductLine[] = [];
let skills: Skill[] = [];
let catalogProjectID = "";
let catalogLoading = false;
let searchTerm = "";
let selectedKnowledgeID = "";
let knowledgeReaderOpen = false;
let updateFilter: "pending" | "all" = "pending";
const collapsedKnowledgeIDs = new Set<string>();
const syntheticHomeID = "__knowledge_home__";

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
  return `<article class="knowledge-entry-card">
    <header><span class="knowledge-entry-kind">${item.is_index ? "\u77e5\u8bc6\u9996\u9875" : "\u6587\u6863"}</span><small>${formatDate(item.updated_at)}</small></header>
    <h3>${escapeHTML(item.title)}</h3>
    <p>${escapeHTML(item.body)}</p>
    <footer><div>${item.product_line_id ? `<span>${icon("branch")}${escapeHTML(lineName(item.product_line_id))}</span>` : ""}</div>
      <div class="knowledge-entry-actions">
        <button type="button" data-knowledge-feedback="helped" data-knowledge-id="${item.id}" title="\u6709\u5e2e\u52a9">${icon("shield")} ${Number(item.helped_count || 0)}</button>
        <button type="button" data-knowledge-feedback="stale" data-knowledge-id="${item.id}" title="\u5185\u5bb9\u8fc7\u65f6">${icon("clock")} ${Number(item.stale_count || 0)}</button>
        ${item.status !== "verified" ? `<button type="button" data-knowledge-verify="${item.id}">\u786e\u8ba4\u5185\u5bb9</button>` : ""}
        ${editable ? `<button type="button" class="icon-button" data-knowledge-edit="${item.id}" title="\u7f16\u8f91">${icon("edit")}</button><button type="button" class="icon-button" data-knowledge-delete="${item.id}" title="\u5220\u9664">${icon("close")}</button>` : ""}
      </div>
    </footer>
  </article>`;
}

function sortKnowledge(entries: Knowledge[]): Knowledge[] {
  return [...entries].sort((left, right) => Number(right.is_index) - Number(left.is_index)
    || Number(left.sort_order || 0) - Number(right.sort_order || 0)
    || String(left.slug || left.title).localeCompare(String(right.slug || right.title)));
}

function visualParentID(item: Knowledge, byID: Map<string, Knowledge>, homeID: string): string {
  if (item.id === homeID) return "";
  return item.parent_id && byID.has(item.parent_id) ? item.parent_id : homeID;
}

function knowledgeTreeRows(entries: Knowledge[], parentID: string, homeID: string, depth = 1, seen = new Set<string>()): string {
  const byID = new Map(entries.map(entry => [entry.id, entry]));
  return sortKnowledge(entries.filter(item => item.id !== homeID && visualParentID(item, byID, homeID) === parentID)).map(item => {
    if (seen.has(item.id)) return "";
    const branch = new Set(seen); branch.add(item.id);
    const childEntries = entries.filter(child => child.id !== homeID && visualParentID(child, byID, homeID) === item.id);
    const collapsed = collapsedKnowledgeIDs.has(item.id);
    const children = collapsed ? "" : knowledgeTreeRows(entries, item.id, homeID, depth + 1, branch);
    const toggle = childEntries.length
      ? `<button type="button" class="knowledge-tree-toggle ${collapsed ? "collapsed" : ""}" data-knowledge-toggle="${escapeHTML(item.id)}" aria-label="${collapsed ? "\u5c55\u5f00" : "\u6536\u8d77"}${escapeHTML(item.title)}" aria-expanded="${!collapsed}"><span>\u25be</span></button>`
      : `<span class="knowledge-tree-toggle-placeholder"></span>`;
    return `<div class="knowledge-tree-node" style="--knowledge-indent:${Math.min(depth, 12) * 18}px"><div class="knowledge-tree-row">${toggle}<button type="button" data-knowledge-open="${escapeHTML(item.id)}" class="knowledge-tree-document ${item.id === selectedKnowledgeID ? "active" : ""}"><span>${escapeHTML(item.title)}</span><small>${childEntries.length ? "\u5206\u7c7b" : "\u6587\u6863"}</small></button></div>${children}</div>`;
  }).join("");
}

function knowledgeBreadcrumbs(item: Knowledge | undefined, entries: Knowledge[], homeID: string, homeTitle: string): string {
  const byID = new Map(entries.map(entry => [entry.id, entry]));
  const path: Knowledge[] = [];
  const seen = new Set<string>();
  let current: Knowledge | undefined = item;
  while (current && !seen.has(current.id)) {
    seen.add(current.id); path.unshift(current); current = current.parent_id ? byID.get(current.parent_id) : undefined;
  }
  if (path[0]?.id !== homeID) {
    return `<button type="button" data-knowledge-open="${escapeHTML(homeID)}">${homeTitle}</button><span>/</span>${path.map(entry => `<button type="button" data-knowledge-open="${escapeHTML(entry.id)}">${escapeHTML(entry.title)}</button>`).join("<span>/</span>")}`;
  }
  return path.map(entry => `<button type="button" data-knowledge-open="${escapeHTML(entry.id)}">${entry.id === homeID ? homeTitle : escapeHTML(entry.title)}</button>`).join("<span>/</span>");
}

export function renderKnowledgeDocumentWorkspace(entries: Knowledge[], empty: string, newKind: KnowledgeDocumentKind): string {
  const ordered = sortKnowledge(entries);
  const root = ordered.find(item => item.is_index && !item.parent_id);
  const homeID = root?.id || `${syntheticHomeID}-${newKind}`;
  const homeTitle = newKind === "global" ? "\u5168\u5c40\u77e5\u8bc6" : newKind === "project-navigation" ? "\u9879\u76ee\u5bfc\u822a" : "\u9879\u76ee\u77e5\u8bc6";
  if (selectedKnowledgeID !== homeID && !ordered.some(item => item.id === selectedKnowledgeID)) selectedKnowledgeID = homeID;
  const selected = ordered.find(item => item.id === selectedKnowledgeID);
  const filtered = searchTerm.trim() ? ordered.filter(entryMatches) : ordered;
  const byID = new Map(ordered.map(entry => [entry.id, entry]));
  const visibleChildren = (parentID: string) => sortKnowledge(ordered.filter(item => item.id !== homeID && visualParentID(item, byID, homeID) === parentID));
  const homeChildren = visibleChildren(homeID);
  const homeCollapsed = collapsedKnowledgeIDs.has(homeID);
  const homeToggle = homeChildren.length ? `<button type="button" class="knowledge-tree-toggle ${homeCollapsed ? "collapsed" : ""}" data-knowledge-toggle="${escapeHTML(homeID)}" aria-label="${homeCollapsed ? "\u5c55\u5f00" : "\u6536\u8d77"}${homeTitle}" aria-expanded="${!homeCollapsed}"><span>\u25be</span></button>` : `<span class="knowledge-tree-toggle-placeholder"></span>`;
  const homeRow = `<div class="knowledge-tree-node knowledge-tree-home" style="--knowledge-indent:0px"><div class="knowledge-tree-row">${homeToggle}<button type="button" data-knowledge-open="${escapeHTML(homeID)}" class="knowledge-tree-document ${selectedKnowledgeID === homeID ? "active" : ""}"><span>${icon("knowledge")}${homeTitle}</span><small>\u5165\u53e3</small></button></div></div>`;
  const tree = searchTerm.trim()
    ? `${homeRow}${filtered.filter(item => item.id !== homeID).map(item => `<button type="button" data-knowledge-open="${escapeHTML(item.id)}" class="knowledge-tree-search-result ${item.id === selectedKnowledgeID ? "active" : ""}"><span>${escapeHTML(item.title)}</span><small>${visibleChildren(item.id).length ? "\u5206\u7c7b" : "\u6587\u6863"}</small></button>`).join("")}`
    : `${homeRow}${homeCollapsed ? "" : knowledgeTreeRows(ordered, homeID, homeID)}`;
  const isHome = selectedKnowledgeID === homeID;
  const children = isHome ? homeChildren : selected ? visibleChildren(selected.id) : [];
  const siblingParent = selected ? visualParentID(selected, byID, homeID) : "";
  const related = selected && !isHome ? visibleChildren(siblingParent).filter(item => item.id !== selected.id).slice(0, 6) : [];
  const title = isHome ? homeTitle : selected?.title || "\u6587\u6863";
  const body = selected ? renderMarkdown(selected.body) : `<p>${escapeHTML(ordered.length ? "\u4ece\u5de6\u4fa7\u76ee\u5f55\u9009\u62e9\u6587\u6863\uff0c\u6216\u65b0\u5efa\u4e00\u7bc7\u6587\u6863\u3002" : empty)}</p>`;
  const linkSection = (label: string, items: Knowledge[]) => items.length ? `<section class="knowledge-related-links"><strong>${label}</strong><div>${items.map(item => `<button type="button" data-knowledge-open="${escapeHTML(item.id)}">${icon("knowledge")}<span><b>${escapeHTML(item.title)}</b><small>${visibleChildren(item.id).length ? "\u5206\u7c7b" : "\u6587\u6863"}</small></span></button>`).join("")}</div></section>` : "";
  const backButton = newKind === "global"
    ? knowledgeReaderOpen ? `<button type="button" class="knowledge-directory-back" data-knowledge-directory-back>${icon("projects")}\u8fd4\u56de\u5168\u5c40\u77e5\u8bc6\u76ee\u5f55</button>` : ""
    : `<button type="button" class="knowledge-mobile-back" data-knowledge-back>${icon("projects")}\u8fd4\u56de\u77e5\u8bc6\u5e93\u5165\u53e3</button>`;
  const document = `<article class="knowledge-document">
      ${backButton}
      <header><div class="knowledge-breadcrumbs">${isHome ? `<button type="button" data-knowledge-open="${escapeHTML(homeID)}">${homeTitle}</button>` : knowledgeBreadcrumbs(selected, ordered, homeID, homeTitle)}</div><div class="knowledge-document-actions">${selected?.status !== "verified" && selected ? `<button type="button" data-knowledge-verify="${selected.id}">\u786e\u8ba4\u5185\u5bb9</button>` : ""}${selected ? `<button type="button" data-knowledge-edit="${selected.id}">${icon("edit")}\u7f16\u8f91</button>${isHome ? "" : `<button type="button" class="icon-button" data-knowledge-delete="${selected.id}" title="\u5220\u9664">${icon("close")}</button>`}` : ""}</div></header>
      <h2>${escapeHTML(title)}</h2>${selected?.updated_at ? `<div class="knowledge-document-meta"><span>\u66f4\u65b0\u4e8e ${formatDate(selected.updated_at)}</span>${selected.product_line_id ? `<span>${icon("branch")}${escapeHTML(lineName(selected.product_line_id))}</span>` : ""}</div>` : ""}
      <div class="markdown-body knowledge-document-body">${body}</div>
      ${linkSection("\u5b50\u6587\u6863", children)}${linkSection("\u76f8\u5173\u6587\u6863", related)}
      ${selected && !isHome ? `<footer><button type="button" data-knowledge-feedback="helped" data-knowledge-id="${selected.id}">${icon("shield")}\u8fd9\u7bc7\u6709\u5e2e\u52a9 ${Number(selected.helped_count || 0)}</button><button type="button" data-knowledge-feedback="stale" data-knowledge-id="${selected.id}">${icon("clock")}\u5185\u5bb9\u5df2\u8fc7\u65f6 ${Number(selected.stale_count || 0)}</button></footer>` : ""}
    </article>`;
  return `<div class="knowledge-section-head"><div><h2>${homeTitle}</h2><p>\u5148\u9605\u8bfb\u5165\u53e3\u6587\u6863\uff0c\u518d\u6cbf\u94fe\u63a5\u67e5\u770b\u5b50\u6587\u6863\u3002</p></div><div class="actions"><button class="primary" type="button" data-knowledge-new="${newKind === "global" ? "global" : "project"}" data-knowledge-type="${newKind === "project-navigation" ? "navigation" : "practice"}" data-knowledge-parent="${escapeHTML(selected?.id || root?.id || "")}">${icon("plus")}\u65b0\u5efa\u6587\u6863</button></div></div>
    <div class="knowledge-document-layout ${knowledgeReaderOpen ? "reader-open" : "directory-open"}"><aside class="panel knowledge-tree"><label>${icon("filter")}<input id="knowledge-search" value="${escapeHTML(searchTerm)}" placeholder="\u641c\u7d22\u6587\u6863"></label><div>${tree}</div></aside><section class="panel knowledge-reader">${document}</section></div>`;
}

function renderBindings(workspaces: Workspace[]): string {
  return `<section class="panel knowledge-bindings"><div class="panel-head"><strong>Workspace Bindings</strong><span>Project \u2192 Native / WSL / SSH</span></div>
    <div class="knowledge-binding-grid">${workspaces.map(item => `<article><div class="knowledge-binding-icon">${icon(item.transport === "ssh" ? "server" : "folder")}</div><div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.transport)} \u00b7 ${escapeHTML(item.locality)} \u00b7 ${escapeHTML(item.health)}</small><p>${escapeHTML(item.root_path)}</p></div></article>`).join("") || renderEmpty("\u8be5\u9879\u76ee\u8fd8\u6ca1\u6709 Workspace Binding\u3002")}</div>
  </section>`;
}

function renderOverview(project: Project, entries: Knowledge[], projectWorkspaces: Workspace[]): string {
  const projectEntries = entries.filter(item => item.scope === "project" && item.project_id === project.id);
  const projectCount = projectEntries.filter(item => !item.is_index && item.type !== "navigation").length;
  const navigationCount = projectEntries.filter(item => !item.is_index && item.type === "navigation").length;
  const portal = (kind: string, iconName: string, title: string, description: string, meta: string) => `<button type="button" class="knowledge-entry-portal" data-knowledge-entry="${kind}"><span class="knowledge-entry-portal-icon">${icon(iconName)}</span><span><strong>${title}</strong><small>${description}</small></span><b>${meta}</b><i>\u6253\u5f00\u5165\u53e3 \u2192</i></button>`;
  return `<section class="knowledge-hero panel"><div><span class="knowledge-kicker">\u9879\u76ee\u5165\u53e3</span><h2>${escapeHTML(project.name)}</h2><p>\u9009\u62e9\u9879\u76ee\u77e5\u8bc6\u3001\u9879\u76ee\u5bfc\u822a\u6216\u8bbe\u7f6e\u3002\u5168\u5c40\u77e5\u8bc6\u8bf7\u4ece\u5de6\u4fa7\u56fa\u5b9a\u5165\u53e3\u8fdb\u5165\u3002</p></div><div class="knowledge-policy ${project.knowledge_policy === "disabled" ? "off" : "on"}"><small>\u4efb\u52a1\u4e2d\u7684\u77e5\u8bc6\u5e93</small><strong>${project.knowledge_policy === "disabled" ? "\u5df2\u5173\u95ed" : "\u5df2\u5f00\u542f"}</strong><span>${projectCount + navigationCount} \u7bc7\u9879\u76ee\u6587\u6863</span></div></section>
    <section class="knowledge-entry-portals">${portal("project", "knowledge", "\u9879\u76ee\u77e5\u8bc6", "\u5f53\u524d\u9879\u76ee\u7684\u5b9e\u8df5\u3001\u51b3\u7b56\u548c\u8bca\u65ad", `${projectCount} \u7bc7`)}${portal("navigation", "branch", "\u9879\u76ee\u5bfc\u822a", "\u6a21\u5757\u5165\u53e3\u3001\u8def\u5f84\u8fb9\u754c\u548c\u5173\u952e\u6d41\u7a0b", `${navigationCount} \u7bc7`)}${portal("settings", "model", "\u8bbe\u7f6e", "\u77e5\u8bc6\u5e93\u5f00\u5173\u3001\u4ea7\u54c1\u7ebf\u548c Workspace \u7ed1\u5b9a", "\u914d\u7f6e")}</section>`;
}

function renderSkills(): string {
  return `<div class="knowledge-section-head"><div><h2>Skills</h2><p>Skill \u91c7\u7528 AHA \u6258\u7ba1\u5305\uff1aSKILL.md \u662f\u5165\u53e3\uff0cscripts / assets / agents \u4e0e\u6280\u80fd\u4e00\u8d77\u6309 Task \u9009\u62e9\u7269\u5316\u3002</p></div><button class="primary" type="button" data-skill-new>${icon("plus")}\u65b0\u5efa Skill</button></div>
    <div class="skill-grid">${skills.map(item => `<article class="skill-card ${item.enabled ? "" : "disabled"}"><header><div class="skill-mark">${icon("bot")}</div><div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.scope)} \u00b7 v${item.version} \u00b7 ${escapeHTML(item.status)}</small></div><label class="skill-switch"><input type="checkbox" data-skill-toggle="${item.id}" ${item.enabled ? "checked" : ""}><span></span></label></header><p>${escapeHTML(item.description || "\u672a\u586b\u5199\u63cf\u8ff0")}</p><div class="skill-package-files"><strong>Skill Package</strong><span>${(item.files || []).map(file => `<code>${escapeHTML(file)}</code>`).join("") || "<code>SKILL.md</code>"}</span></div><footer><span title="${escapeHTML(item.source_path || "")}">${escapeHTML(item.source_path || "AHA managed package")}</span><div><button class="icon-button" type="button" data-skill-edit="${item.id}">${icon("edit")}</button><button class="icon-button" type="button" data-skill-delete="${item.id}">${icon("close")}</button></div></footer></article>`).join("") || renderEmpty("\u8fd8\u6ca1\u6709 Skill\u3002")}</div>`;
}

function proposalValue(proposal: KnowledgeProposal, field: "title" | "body"): string {
  const current = field === "title" ? proposal.current_title : proposal.current_body;
  return String(current ?? proposal[field] ?? proposal.proposed?.[field] ?? "");
}

function proposalBaseValue(proposal: KnowledgeProposal, entry: Knowledge | undefined, field: "title" | "body"): string {
  const direct = field === "title" ? proposal.base_title : proposal.base_body;
  const previous = field === "title" ? proposal.previous_title : proposal.previous_body;
  return String(direct ?? previous ?? proposal.base_entry?.[field] ?? entry?.[field] ?? "");
}

type ProposalDiffLine = {kind: "same" | "remove" | "add"; text: string};

function proposalLineDiff(before: string, after: string): ProposalDiffLine[] {
  const left = before.replaceAll("\r\n", "\n").split("\n");
  const right = after.replaceAll("\r\n", "\n").split("\n");
  if (left.length * right.length > 40000) {
    return [...left.map(text => ({kind: "remove" as const, text})), ...right.map(text => ({kind: "add" as const, text}))];
  }
  const table = Array.from({length: left.length + 1}, () => new Uint16Array(right.length + 1));
  for (let l = left.length - 1; l >= 0; l--) {
    for (let r = right.length - 1; r >= 0; r--) table[l][r] = left[l] === right[r] ? table[l + 1][r + 1] + 1 : Math.max(table[l + 1][r], table[l][r + 1]);
  }
  const result: ProposalDiffLine[] = [];
  let l = 0;
  let r = 0;
  while (l < left.length || r < right.length) {
    if (l < left.length && r < right.length && left[l] === right[r]) {
      result.push({kind: "same", text: left[l]}); l++; r++;
    } else if (r < right.length && (l === left.length || table[l][r + 1] >= table[l + 1][r])) {
      result.push({kind: "add", text: right[r++]});
    } else {
      result.push({kind: "remove", text: left[l++]});
    }
  }
  return result;
}

function proposalDiffMarkup(proposal: KnowledgeProposal, entries: Knowledge[]): string {
  const entry = entries.find(item => item.id === proposal.entry_id);
  const title = proposalValue(proposal, "title");
  const body = proposalValue(proposal, "body");
  if (Number(proposal.base_revision || 0) <= 0) {
    return `<div class="knowledge-proposal-full"><h3>${escapeHTML(title)}</h3><div class="markdown-body">${renderMarkdown(body)}</div></div>`;
  }
  const baseTitle = proposalBaseValue(proposal, entry, "title");
  const baseBody = proposalBaseValue(proposal, entry, "body");
  const titleDiff = baseTitle !== title ? `<div class="knowledge-proposal-title-diff"><del>${escapeHTML(baseTitle || "\u65e0\u6807\u9898")}</del><span>\u2192</span><ins>${escapeHTML(title)}</ins></div>` : "";
  const lines = proposalLineDiff(baseBody, body).map(line => `<div class="${line.kind}"><span>${line.kind === "add" ? "+" : line.kind === "remove" ? "\u2212" : ""}</span><code>${escapeHTML(line.text) || "&nbsp;"}</code></div>`).join("");
  return `${titleDiff}<div class="knowledge-proposal-diff" aria-label="\u65e7\u6b63\u6587\u4e0e\u65b0\u6b63\u6587\u5dee\u5f02">${lines}</div>`;
}

function proposalStatus(status: string): [string, string] {
  if (status === "approved") return ["good", "\u5df2\u6279\u51c6"];
  if (status === "rejected") return ["bad", "\u5df2\u62d2\u7edd"];
  return ["warn", "\u5f85\u5ba1\u6279"];
}

function renderUpdates(entries: Knowledge[], proposals: KnowledgeProposal[], projects: Project[]): string {
  const pending = proposals.filter(item => item.status === "pending");
  const legacyCandidates = entries.filter(item => !item.is_index && item.status === "candidate");
  const pendingCount = pending.length + legacyCandidates.length;
  const visible = (updateFilter === "pending" ? pending : proposals).sort((a, b) => String(b.created_at).localeCompare(String(a.created_at)));
  const projectNames = new Map(projects.map(project => [project.id, project.name]));
  const filters = `<div class="knowledge-update-filters" role="tablist" aria-label="\u63d0\u6848\u7b5b\u9009"><button type="button" data-knowledge-update-filter="pending" class="${updateFilter === "pending" ? "active" : ""}">\u5f85\u5904\u7406 <b>${pendingCount}</b></button><button type="button" data-knowledge-update-filter="all" class="${updateFilter === "all" ? "active" : ""}">\u5168\u90e8\u63d0\u6848 <b>${proposals.length + legacyCandidates.length}</b></button></div>`;
  const cards = visible.map(proposal => {
    const [statusClass, statusLabel] = proposalStatus(proposal.status);
    const revision = Number(proposal.base_revision || 0) > 0;
    const scopeValue = proposal.scope || proposal.proposed?.scope;
    const projectID = proposal.project_id || proposal.proposed?.project_id || "";
    const scope = scopeValue === "global" ? "\u5168\u5c40\u77e5\u8bc6" : projectNames.get(projectID) || "\u9879\u76ee\u77e5\u8bc6";
    const actions = proposal.status === "pending" ? `<div class="knowledge-proposal-actions"><button type="button" data-knowledge-proposal-view="${escapeHTML(proposal.id)}">\u67e5\u770b\u5dee\u5f02</button><button type="button" class="primary" data-knowledge-proposal-approve="${escapeHTML(proposal.id)}">\u6279\u51c6\u66f4\u65b0</button><button type="button" class="danger" data-knowledge-proposal-reject="${escapeHTML(proposal.id)}">\u62d2\u7edd</button></div>` : `<button type="button" data-knowledge-proposal-view="${escapeHTML(proposal.id)}">\u67e5\u770b\u5dee\u5f02</button>`;
    return `<article class="knowledge-proposal-card ${proposal.status === "pending" ? "pending" : ""}"><header><div><span class="status ${statusClass}">${statusLabel}</span><span>${revision ? "\u4fee\u8ba2\u63d0\u6848" : "\u65b0\u77e5\u8bc6"}</span><span>${escapeHTML(scope)}</span></div><small>${formatDate(proposal.created_at)}</small></header>${proposalDiffMarkup(proposal, entries)}<footer><span>${proposal.source_task_id ? `\u6765\u81ea\u4efb\u52a1 ${escapeHTML(proposal.source_task_id)}` : "\u77e5\u8bc6\u4fee\u8ba2\u5efa\u8bae"}</span>${actions}</footer></article>`;
  }).join("");
  const legacyCards = legacyCandidates.map(item => `<article class="knowledge-proposal-card pending legacy"><header><div><span class="status warn">\u5f85\u5ba1\u6279</span><span>\u5347\u7ea7\u524d\u5019\u9009</span><span>${escapeHTML(item.scope === "global" ? "\u5168\u5c40\u77e5\u8bc6" : projectNames.get(item.project_id || "") || "\u9879\u76ee\u77e5\u8bc6")}</span></div><small>${formatDate(item.updated_at)}</small></header><div class="knowledge-proposal-full"><h3>${escapeHTML(item.title)}</h3><div class="markdown-body">${renderMarkdown(item.body)}</div></div><footer><span>\u5f85\u8f6c\u5165\u65b0\u63d0\u6848\u6d41\u7a0b\u7684\u5386\u53f2\u5019\u9009</span><div class="knowledge-proposal-actions"><button type="button" data-knowledge-update-open="${escapeHTML(item.id)}">\u67e5\u770b\u5185\u5bb9</button><button type="button" class="primary" data-knowledge-verify="${escapeHTML(item.id)}">\u6279\u51c6\u6536\u5f55</button><button type="button" class="danger" data-knowledge-delete="${escapeHTML(item.id)}">\u62d2\u7edd\u5e76\u5220\u9664</button></div></footer></article>`).join("");
  const pendingEntryIDs = new Set(pending.map(item => item.entry_id));
  const stale = entries.filter(item => !item.is_index && !pendingEntryIDs.has(item.id) && (item.status === "stale" || item.feedback_state === "wrong"));
  const staleFallback = stale.length ? `<section class="knowledge-stale-fallback"><div><h3>\u7b49\u5f85 Agent \u4fee\u8ba2</h3><p>\u8fd9\u4e9b\u6587\u6863\u5df2\u505c\u6b62\u4f5c\u4e3a\u6709\u6548\u77e5\u8bc6\u6ce8\u5165\u3002\u540e\u7eed\u76f8\u5173 Turn \u4e2d，Agent \u83b7\u5f97\u65b0\u8bc1\u636e\u540e\u4f1a\u63d0\u4ea4\u4fee\u8ba2\u63d0\u6848；\u5982\u679c\u8fc7\u65f6\u53cd\u9988\u662f\u8bef\u5224，\u53ef\u786e\u8ba4\u5f53\u524d\u5185\u5bb9\u4ecd\u7136\u6709\u6548\u3002</p></div>${stale.map(item => `<article><span><strong>${escapeHTML(item.title)}</strong><small>${escapeHTML(item.scope === "global" ? "\u5168\u5c40\u77e5\u8bc6" : projectNames.get(item.project_id || "") || "\u9879\u76ee\u77e5\u8bc6")}</small></span><button type="button" data-knowledge-verify="${escapeHTML(item.id)}">\u786e\u8ba4\u4ecd\u6709\u6548</button></article>`).join("")}</section>` : "";
  const reviewCards = `${cards}${legacyCards}`;
  return `<div class="knowledge-section-head"><div><h2>\u77e5\u8bc6\u66f4\u65b0</h2><p>\u5ba1\u9605 Agent \u63d0\u4ea4\u7684\u65b0\u77e5\u8bc6\u548c\u4fee\u8ba2\u5efa\u8bae\uff0c\u6279\u51c6\u540e\u624d\u4f1a\u66f4\u65b0\u77e5\u8bc6\u5e93\u3002</p></div><span class="status ${pendingCount ? "warn" : "good"}">${pendingCount ? `${pendingCount} \u9879\u5f85\u5ba1\u6279` : "\u6ca1\u6709\u5f85\u5ba1\u6279\u63d0\u6848"}</span></div>${filters}<div class="knowledge-update-feed">${reviewCards || renderEmpty(updateFilter === "pending" ? "\u6ca1\u6709\u5f85\u5ba1\u6279\u7684\u77e5\u8bc6\u63d0\u6848\u3002" : "\u5c1a\u65e0\u77e5\u8bc6\u63d0\u6848\u3002")}</div>${staleFallback}`;
}

function renderSettings(project: Project, projectWorkspaces: Workspace[]): string {
  return `<button type="button" class="knowledge-mobile-back" data-knowledge-back>${icon("projects")}\u8fd4\u56de\u77e5\u8bc6\u5e93\u5165\u53e3</button>
    <div class="knowledge-section-head"><div><h2>\u77e5\u8bc6\u5e93\u8bbe\u7f6e</h2><p>\u77e5\u8bc6\u5e93\u5f52\u5c5e\u4e8e\u9879\u76ee\uff0c\u5de5\u4f5c\u533a\u7528\u4e8e\u9009\u62e9\u5b9e\u9645\u6267\u884c\u4f4d\u7f6e\u3002</p></div></div>
    <section class="panel knowledge-setting-card"><div><strong>\u4efb\u52a1\u9ed8\u8ba4\u4f7f\u7528\u77e5\u8bc6\u5e93</strong><p>\u65b0\u4efb\u52a1\u53ef\u4ee5\u7ee7\u627f\u9879\u76ee\u8bbe\u7f6e\uff0c\u4e5f\u53ef\u5355\u72ec\u5f00\u542f\u6216\u5173\u95ed\u3002</p></div><button type="button" class="${project.knowledge_policy === "disabled" ? "" : "primary"}" data-project-kb-toggle>${project.knowledge_policy === "disabled" ? "\u5df2\u5173\u95ed" : "\u5df2\u5f00\u542f"}</button></section>
    <section class="panel spaced"><div class="panel-head"><strong>Product Lines</strong><button type="button" data-product-line-new>${icon("plus")}\u65b0\u5efa</button></div><div class="product-line-list">${productLines.map(line => `<article><div>${icon("branch")}<span><strong>${escapeHTML(line.name)}</strong><small>${escapeHTML(line.branch_pattern || "*")} ${line.default ? "\u00b7 default" : ""}</small></span></div><button type="button" class="icon-button" data-product-line-delete="${line.id}">${icon("close")}</button></article>`).join("") || renderEmpty("\u672a\u914d\u7f6e Product Line\uff0c\u9879\u76ee\u77e5\u8bc6\u5c06\u4f5c\u4e3a\u901a\u7528\u6761\u76ee\u3002")}</div></section>
    ${renderBindings(projectWorkspaces)}`;
}

function renderSection(project: Project, context: KnowledgeWorkspaceContext): string {
  const projectWorkspaces = context.workspaces.filter(item => item.project_id === project.id);
  const projectEntries = context.knowledge.filter(item => item.scope === "project" && item.project_id === project.id);
  switch (activeSection) {
    case "project":
      return renderKnowledgeDocumentWorkspace(projectEntries.filter(item => !item.is_index && item.type !== "navigation"), "\u8fd8\u6ca1\u6709\u9879\u76ee\u77e5\u8bc6\u3002", "project");
    case "navigation":
      return renderKnowledgeDocumentWorkspace(projectEntries.filter(item => !item.is_index && item.type === "navigation"), "\u8fd8\u6ca1\u6709\u9879\u76ee\u5bfc\u822a\u3002", "project-navigation");
    case "settings": return renderSettings(project, projectWorkspaces);
    default: return renderOverview(project, context.knowledge, projectWorkspaces);
  }
}

function renderTopLevelSection(context: KnowledgeWorkspaceContext): string {
  if (activeArea === "skills") return renderSkills();
  if (activeArea === "updates") return renderUpdates(context.knowledge, context.proposals, context.projects);
  return renderKnowledgeDocumentWorkspace(context.knowledge.filter(item => item.scope === "global"), "\u8fd8\u6ca1\u6709 AHA \u5168\u5c40\u77e5\u8bc6\u3002", "global");
}

function knowledgeDialog(project: Project, entries: Knowledge[]): string {
  const parents = sortKnowledge(entries).filter(item => !item.is_index).map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.title)}</option>`).join("");
  return `<dialog id="knowledge-editor-dialog" class="wide"><form id="knowledge-editor-form"><div class="dialog-head"><h2 data-knowledge-editor-title>\u65b0\u5efa\u6587\u6863</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><input type="hidden" name="id"><input type="hidden" name="project_id" value="${project.id}"><input type="hidden" name="type" value="practice"><input type="hidden" name="status" value="candidate"><input type="hidden" name="is_index" value="false"><div class="two"><label>\u4fdd\u5b58\u4f4d\u7f6e<select name="scope"><option value="project">\u9879\u76ee\u77e5\u8bc6</option><option value="global">\u5168\u5c40\u77e5\u8bc6</option></select></label><label class="knowledge-project-field">\u9002\u7528\u4ea7\u54c1\u7ebf<select name="product_line_id"><option value="">\u5168\u90e8</option>${productLines.map(line => `<option value="${line.id}">${escapeHTML(line.name)}</option>`).join("")}</select></label></div><label>\u7236\u6587\u6863<select name="parent_id"><option value="">\u77e5\u8bc6\u9996\u9875</option>${parents}</select></label><label>\u6807\u9898<input name="title" required placeholder="\u4f8b\u5982\uff1a\u672c\u5730\u5f00\u53d1\u6d41\u7a0b"></label><label>\u6b63\u6587<textarea name="body" rows="14" required placeholder="\u652f\u6301 Markdown \u683c\u5f0f"></textarea></label><details class="knowledge-advanced"><summary>\u9ad8\u7ea7\u8bbe\u7f6e</summary><div class="two"><label>\u94fe\u63a5\u540d\u79f0<input name="slug" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" placeholder="\u7559\u7a7a\u5373\u81ea\u52a8\u751f\u6210"></label><label>\u76ee\u5f55\u987a\u5e8f<input name="sort_order" type="number" min="0" value="0"></label></div><div class="two"><label>\u5185\u5bb9\u53ef\u4fe1\u5ea6<input name="confidence" type="number" min="0" max="1" step="0.05" value="0.8"></label><label>\u5173\u8054\u63d0\u4ea4<input name="verified_commit" placeholder="commit SHA"></label></div><label class="knowledge-project-field">\u9002\u7528\u5206\u652f<input name="branch_scope" placeholder="main / release/*"></label></details></div><div class="dialog-actions"><button type="button" data-kb-close>\u53d6\u6d88</button><button class="primary" type="submit">\u4fdd\u5b58\u6587\u6863</button></div></form></dialog>`;
}

function proposalReviewDialog(): string {
  return `<dialog id="knowledge-proposal-dialog" class="wide knowledge-proposal-dialog"><div class="dialog-head"><h2>\u77e5\u8bc6\u66f4\u65b0\u5dee\u5f02</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body" data-knowledge-proposal-detail></div></dialog>`;
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
  const editorEntries = projectMode ? context.knowledge.filter(item => item.scope === "project" && item.project_id === project.id) : context.knowledge.filter(item => item.scope === "global");
  const reading = knowledgeReaderOpen && projectMode && activeSection !== "overview";
  return `<section class="page knowledge-page ${reading ? "knowledge-reading" : ""}"><div class="page-head"><div><h1>\u77e5\u8bc6\u5e93</h1><p>\u7ba1\u7406\u5168\u5c40\u77e5\u8bc6\u3001\u9879\u76ee\u6587\u6863\u3001Skills \u548c\u5185\u5bb9\u66f4\u65b0\u3002</p></div><button id="knowledge-refresh" type="button">${icon("refresh")}\u5237\u65b0</button></div>
    <div class="knowledge-workspace-layout ${projectMode ? "project-mode" : "top-level"}">
      <aside class="panel knowledge-primary-rail"><header><strong>\u77e5\u8bc6\u5e93</strong><small>${context.projects.length} \u4e2a\u9879\u76ee</small></header><nav>${topLevelLabels.map(([id, iconName, label]) => `<button type="button" data-knowledge-area="${id}" class="${activeArea === id ? "active" : ""}">${icon(iconName)}<span>${label}</span></button>`).join("")}</nav><div class="knowledge-project-group"><header><strong>\u9879\u76ee</strong><small>${context.projects.length}</small></header><div>${context.projects.map(item => `<button type="button" data-knowledge-project="${item.id}" class="${projectMode && item.id === project?.id ? "active" : ""}"><span class="knowledge-project-avatar">${escapeHTML(item.name.slice(0, 1).toUpperCase())}</span><span><strong>${escapeHTML(item.name)}</strong><small>${item.id === project?.id ? `${projectKnowledge} \u7bc7\u5185\u5bb9` : "\u9879\u76ee\u77e5\u8bc6"}</small></span></button>`).join("") || renderEmpty("\u6682\u65e0\u9879\u76ee\u3002")}</div></div></aside>
      <main class="knowledge-content ${catalogLoading ? "loading-catalog" : ""}">${projectMode ? renderSection(project, context) : renderTopLevelSection(context)}</main>
    </div>
    ${knowledgeDialog(project || ({id: ""} as Project), editorEntries)}${proposalReviewDialog()}${skillDialog(context.projects, project?.id || "")}${productLineDialog()}
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
    knowledgeReaderOpen = false;
    selectedKnowledgeID = "";
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-update-filter]").forEach(button => button.addEventListener("click", () => {
    updateFilter = button.dataset.knowledgeUpdateFilter === "all" ? "all" : "pending";
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-proposal-view]").forEach(button => button.addEventListener("click", () => {
    const proposal = context.proposals.find(item => item.id === button.dataset.knowledgeProposalView);
    const dialog = document.querySelector<HTMLDialogElement>("#knowledge-proposal-dialog");
    const detail = dialog?.querySelector<HTMLElement>("[data-knowledge-proposal-detail]");
    const heading = dialog?.querySelector<HTMLElement>("h2");
    if (!proposal || !dialog || !detail) return;
    if (heading) heading.textContent = Number(proposal.base_revision || 0) > 0 ? "\u77e5\u8bc6\u4fee\u8ba2\u5dee\u5f02" : "\u65b0\u77e5\u8bc6\u9884\u89c8";
    detail.innerHTML = proposalDiffMarkup(proposal, context.knowledge);
    dialog.showModal();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-proposal-approve]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeProposalApprove || "";
    void mutate(context, () => api.approveKnowledgeProposal(id), "\u77e5\u8bc6\u66f4\u65b0\u5df2\u6279\u51c6");
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-proposal-reject]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm("\u786e\u5b9a\u62d2\u7edd\u8fd9\u9879\u77e5\u8bc6\u66f4\u65b0\uff1f")) return;
    const id = button.dataset.knowledgeProposalReject || "";
    void mutate(context, () => api.rejectKnowledgeProposal(id), "\u77e5\u8bc6\u66f4\u65b0\u5df2\u62d2\u7edd");
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-update-open]").forEach(button => button.addEventListener("click", () => {
    const item = context.knowledge.find(entry => entry.id === button.dataset.knowledgeUpdateOpen);
    if (!item) return;
    selectedKnowledgeID = item.id;
    knowledgeReaderOpen = true;
    searchTerm = "";
    if (item.scope === "global") {
      activeArea = "global";
    } else {
      if (selectedProjectID !== item.project_id) {
        selectedProjectID = item.project_id || "";
        productLines = [];
        catalogProjectID = "";
      }
      activeArea = "project";
      activeSection = item.type === "navigation" ? "navigation" : "project";
    }
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-entry]").forEach(button => button.addEventListener("click", () => {
    const entry = button.dataset.knowledgeEntry || "project";
    selectedKnowledgeID = "";
    knowledgeReaderOpen = true;
    if (entry === "global") {
      activeArea = "global";
      knowledgeReaderOpen = false;
    } else {
      activeArea = "project";
      activeSection = entry === "settings" ? "settings" : entry === "navigation" ? "navigation" : "project";
    }
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-project]").forEach(button => button.addEventListener("click", () => {
    selectedProjectID = button.dataset.knowledgeProject || "";
    activeArea = "project";
    activeSection = "overview";
    selectedKnowledgeID = "";
    knowledgeReaderOpen = false;
    productLines = [];
    catalogProjectID = "";
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-open]").forEach(button => button.addEventListener("click", () => {
    selectedKnowledgeID = button.dataset.knowledgeOpen || "";
    knowledgeReaderOpen = true;
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-toggle]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeToggle || "";
    if (collapsedKnowledgeIDs.has(id)) collapsedKnowledgeIDs.delete(id);
    else collapsedKnowledgeIDs.add(id);
    context.render();
  }));
  document.querySelector("[data-knowledge-back]")?.addEventListener("click", () => {
    if (context.projects.length) {
      activeArea = "project";
      activeSection = "overview";
      selectedKnowledgeID = "";
    }
    knowledgeReaderOpen = false;
    context.render();
  });
  document.querySelector("[data-knowledge-directory-back]")?.addEventListener("click", () => {
    selectedKnowledgeID = "";
    knowledgeReaderOpen = false;
    context.render();
  });
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
    const scopeSelect = form.elements.namedItem("scope") as HTMLSelectElement | null;
    const parentSelect = form.elements.namedItem("parent_id") as HTMLSelectElement | null;
    if (scopeSelect) scopeSelect.disabled = false;
    if (parentSelect) parentSelect.disabled = false;
    const advanced = form.querySelector<HTMLDetailsElement>(".knowledge-advanced");
    if (advanced) advanced.hidden = false;
    form.querySelectorAll<HTMLOptionElement>(`[name="parent_id"] option`).forEach(option => option.disabled = false);
    const heading = form.querySelector<HTMLElement>("[data-knowledge-editor-title]");
    if (heading) heading.textContent = "\u65b0\u5efa\u6587\u6863";
    fillForm(form, {
      scope: button.dataset.knowledgeNew === "global" ? "global" : "project",
      project_id: project?.id || "",
      parent_id: button.dataset.knowledgeParent || "",
      slug: "",
      is_index: false,
      type: button.dataset.knowledgeType || "practice",
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
    const scopeSelect = form.elements.namedItem("scope") as HTMLSelectElement | null;
    const parentSelect = form.elements.namedItem("parent_id") as HTMLSelectElement | null;
    if (scopeSelect) scopeSelect.disabled = item.is_index;
    if (parentSelect) parentSelect.disabled = item.is_index;
    const advanced = form.querySelector<HTMLDetailsElement>(".knowledge-advanced");
    if (advanced) advanced.hidden = item.is_index;
    form.querySelectorAll<HTMLOptionElement>(`[name="parent_id"] option`).forEach(option => option.disabled = false);
    const heading = form.querySelector<HTMLElement>("[data-knowledge-editor-title]");
    if (heading) heading.textContent = item.is_index ? "\u7f16\u8f91\u77e5\u8bc6\u9996\u9875" : "\u7f16\u8f91\u6587\u6863";
    fillForm(form, item as unknown as Record<string, unknown>);
    const ownParentOption = form.querySelector<HTMLOptionElement>(`[name="parent_id"] option[value="${CSS.escape(item.id)}"]`);
    if (ownParentOption) ownParentOption.disabled = true;
    syncKnowledgeScope(form);
    openDialog("#knowledge-editor-dialog");
  }));
  document.querySelector<HTMLSelectElement>("#knowledge-editor-form [name=scope]")?.addEventListener("change", event => {
    const form = event.currentTarget.form!;
    syncKnowledgeScope(form);
    const parent = form.elements.namedItem("parent_id") as HTMLSelectElement | null;
    if (parent) parent.value = "";
  });
  document.querySelector<HTMLFormElement>("#knowledge-editor-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const payload = Object.fromEntries(new FormData(event.currentTarget).entries()) as Record<string, unknown>;
    const id = String(payload.id || "");
    delete payload.id;
    payload.confidence = Number(payload.confidence || 0);
    payload.sort_order = Number(payload.sort_order || 0);
    payload.is_index = String(new FormData(event.currentTarget).get("is_index") || "false") === "true";
    const existing = context.knowledge.find(item => item.id === id);
    if (existing?.is_index) {
      payload.scope = existing.scope;
      payload.project_id = existing.project_id || "";
      payload.parent_id = "";
      payload.is_index = true;
      payload.status = "verified";
      payload.product_line_id = "";
    }
    if (!String(payload.slug || "").trim()) delete payload.slug;
    if (payload.scope === "global") {
      payload.project_id = "";
      payload.product_line_id = "";
      payload.branch_scope = "";
    }
    void mutate(context, () => id ? api.updateKnowledge(id, payload) : api.createKnowledge(payload), id ? "\u6587\u6863\u5df2\u66f4\u65b0" : "\u6587\u6863\u5df2\u521b\u5efa");
  });
  document.querySelectorAll<HTMLElement>("[data-knowledge-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm("\u786e\u5b9a\u5220\u9664\u8be5\u6587\u6863\uff1f")) return;
    void mutate(context, () => api.deleteKnowledge(button.dataset.knowledgeDelete || ""), "\u6587\u6863\u5df2\u5220\u9664");
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-verify]").forEach(button => button.addEventListener("click", () => void mutate(context, () => api.verifyKnowledge(button.dataset.knowledgeVerify || ""), "\u5185\u5bb9\u5df2\u786e\u8ba4")));
  document.querySelectorAll<HTMLElement>("[data-knowledge-feedback]").forEach(button => button.addEventListener("click", () => void mutate(context, () => api.feedbackKnowledge(button.dataset.knowledgeId || "", button.dataset.knowledgeFeedback as "helped" | "stale" | "wrong"), "\u53cd\u9988\u5df2\u8bb0\u5f55")));

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
