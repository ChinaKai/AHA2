import {api} from "./api.js";
import {t} from "./i18n.js";
import {icon} from "./icons.js";
import {renderMarkdown} from "./markdown.js";
import type {Knowledge, KnowledgeLibrary, KnowledgeProposal, KnowledgeReviewSettings, ProductLine, Project, Skill, Workspace} from "./types.js";

type KnowledgeScope = "project" | "global";
type KnowledgeMode = "read" | "edit" | "review" | "skills";
type KnowledgeDocumentKind = "global" | "project" | "project-navigation";
type MobilePane = "directory" | "reader";
type ReviewScopeFilter = "all" | "global" | `project:${string}`;

export interface KnowledgeWorkspaceContext {
  projects: Project[];
  workspaces: Workspace[];
  knowledge: Knowledge[];
  libraries: KnowledgeLibrary[];
  proposals: KnowledgeProposal[];
  reviewSettings?: KnowledgeReviewSettings;
  refreshData: () => Promise<void>;
  render: () => void;
  setMessage: (kind: "notice" | "error", message: string) => void;
}

interface KnowledgeEditState {
  id: string;
  isNew: boolean;
  scope: KnowledgeScope;
  projectID: string;
  parentID: string;
  type: string;
  status: string;
  title: string;
  body: string;
  slug: string;
  sortOrder: number;
  confidence: number;
  productLineID: string;
  branchScope: string;
  verifiedCommit: string;
  isIndex: boolean;
  dirty: boolean;
}

export interface KnowledgeTreeGroup {
  key: string;
  label: string;
  items: Knowledge[];
}

export interface KnowledgeReviewItem {
  key: string;
  kind: "proposal" | "legacy";
  title: string;
  scopeLabel: string;
  confidenceLabel: string;
  createdAt: string;
  sourceLabel: string;
  scope: "global" | "project";
  projectID: string;
  proposal?: KnowledgeProposal;
  entry?: Knowledge;
}

export interface KnowledgeDocumentStatus {
  key: "pending" | "conflict" | "muted";
  label: string;
}

let selectedProjectID = "";
let activeScope: KnowledgeScope = "project";
let activeMode: KnowledgeMode = "read";
let selectedKnowledgeID = "";
let selectedLibraryID = "";
let selectedKnowledgeSourceID = "all";
let knowledgeSearch = "";
let directoryCollapsed = false;
let mobilePane: MobilePane = "directory";
// The project settings subpage replaces the reader without touching the
// selected document, directory collapse, or mobile pane, so closing it lands
// back where the operator left the project knowledge page.
let settingsOpen = false;
let reviewIndex = 0;
let reviewScopeFilter: ReviewScopeFilter = "all";
let editState: KnowledgeEditState | null = null;
let productLines: ProductLine[] = [];
let skills: Skill[] = [];
let catalogProjectID = "";
let catalogLoading = false;
let skillSearch = "";
let skillScopeFilter: "all" | "global" | "project" | "bound" = "all";
let skillProjectFilter = "";
let skillStatusFilter = "all";
const SKILL_BATCH_SIZE = 6;
let skillLimit = SKILL_BATCH_SIZE;
const collapsedTreeIDs = new Set<string>();
const collapsedGroupKeys = new Set<string>();
const syntheticHomeID = "__knowledge_home__";
const globalGeneralKnowledgeID = "knowledge_global_general";
const globalAgentLessonsKnowledgeID = "knowledge_global_agent_lessons";
const globalTechnicalLessonsKnowledgeID = "knowledge_global_agent_lessons_technical";
const globalBehaviorLessonsKnowledgeID = "knowledge_global_agent_lessons_behavior";
const managedGlobalKnowledgeIDs = new Set([
  globalGeneralKnowledgeID,
  globalAgentLessonsKnowledgeID,
  globalTechnicalLessonsKnowledgeID,
  globalBehaviorLessonsKnowledgeID,
]);

async function ensureKnowledgeDetail(context: KnowledgeWorkspaceContext, id: string): Promise<Knowledge | undefined> {
  const item = context.knowledge.find(entry => entry.id === id);
  if (!item || typeof item.body === "string") return item;
  const response = await api.knowledgeEntry(id);
  Object.assign(item, response.knowledge);
  return item;
}

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

function projectEntries(context: KnowledgeWorkspaceContext, projectID: string): Knowledge[] {
  return context.knowledge.filter(item => item.scope === "project"
    && (item.project_id === projectID || item.bound_project_id === projectID)
    && !(item.bound_project_id === projectID && item.is_index));
}

type KnowledgeSourceOption = {id: string; label: string; count: number; external: boolean};

function bindingModeLabel(mode?: KnowledgeLibrary["binding_mode"]): string {
  return mode === "project" ? t("knowledge.binding.project") : t("knowledge.binding.external");
}

export function projectKnowledgeSources(project: Project, entries: Knowledge[], libraries: KnowledgeLibrary[]): KnowledgeSourceOption[] {
  const counts = new Map<string, number>();
  for (const entry of entries) {
    if (entry.is_index) continue;
    counts.set(entry.project_id || project.id, (counts.get(entry.project_id || project.id) || 0) + 1);
  }
  const libraryByProject = new Map(libraries.map(library => [library.container_project_id, library]));
  const sources: KnowledgeSourceOption[] = [{id: project.id, label: t("knowledge.sourceProject"), count: counts.get(project.id) || 0, external: false}];
  for (const [id, count] of counts) {
    if (id === project.id) continue;
    const library = libraryByProject.get(id);
    const mode = library?.binding_mode || "external";
    sources.push({id, label: `${library?.name || t("knowledge.sourceBound")}（${bindingModeLabel(mode)}）`, count, external: mode === "external"});
  }
  return sources;
}

function knowledgeSourceToolbar(sources: KnowledgeSourceOption[]): string {
  const total = sources.reduce((sum, source) => sum + source.count, 0);
  return `<div class="knowledge-source-toolbar"><label>${t("knowledge.sourceTitle")}<select data-knowledge-source-filter><option value="all" ${selectedKnowledgeSourceID === "all" ? "selected" : ""}>${t("knowledge.sourceAll", {count: total})}</option>${sources.map(source => `<option value="${escapeHTML(source.id)}" ${selectedKnowledgeSourceID === source.id ? "selected" : ""}>${escapeHTML(t("knowledge.sourceOption", {label: source.label, count: source.count}))}</option>`).join("")}</select></label><span>${t("knowledge.sourceNote")}</span></div>`;
}

function renderEmpty(message: string): string {
  return `<div class="empty knowledge-empty">${escapeHTML(message)}</div>`;
}

function sortKnowledge(entries: Knowledge[]): Knowledge[] {
  return [...entries].sort((left, right) => Number(right.is_index) - Number(left.is_index)
    || Number(left.sort_order || 0) - Number(right.sort_order || 0)
    || String(left.slug || left.title).localeCompare(String(right.slug || right.title)));
}

function sortProjectNavigation(entries: Knowledge[]): Knowledge[] {
  const entryRank = (item: Knowledge) => item.slug === "overview" ? 0 : item.slug === "modules" ? 1 : item.slug === "flows" ? 2 : 3;
  return [...entries].sort((left, right) => entryRank(left) - entryRank(right)
    || Number(left.sort_order || 0) - Number(right.sort_order || 0)
    || String(left.slug || left.title).localeCompare(String(right.slug || right.title)));
}

function navigationDisplayTitle(item: Knowledge): string {
  const title = String(item.title || "").trim().toLowerCase();
  if (item.slug === "overview") return t("knowledge.navigation.overview");
  if (item.slug === "modules" && (!title || title === "modules")) return t("knowledge.navigation.modules");
  if (item.slug === "flows" && (!title || title === "flows")) return t("knowledge.navigation.flows");
  return item.title;
}

function knowledgeHomeBody(kind: KnowledgeDocumentKind, body?: string): string {
  const value = String(body || "").trim();
  const legacy = new Set(["这里汇总跨项目共享的知识文档。", "这里汇总本项目的知识文档与常用入口。"]);
  if (value && !legacy.has(value)) return value;
  if (kind === "global") return t("knowledge.home.globalBody");
  if (kind === "project-navigation") return t("knowledge.home.projectNavigationBody");
  return t("knowledge.home.projectBody");
}

function knowledgeIndexSummary(item: Knowledge): string {
  for (const raw of String(item.body || "").split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("#") || line.startsWith("```")) continue;
    const plain = line
      .replace(/^(?:>|[-*+]|\d+[.)])\s+/, "")
      .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
      .replace(/[*_`~]/g, "")
      .trim();
    if (!plain || plain.toLocaleLowerCase() === String(item.title || "").trim().toLocaleLowerCase()) continue;
    return plain.length > 160 ? `${plain.slice(0, 157)}...` : plain;
  }
  return t("knowledge.openHint");
}

function visualParentID(item: Knowledge, byID: Map<string, Knowledge>, homeID: string): string {
  if (item.id === homeID) return "";
  return item.parent_id && byID.has(item.parent_id) ? item.parent_id : homeID;
}

/** Full path from the knowledge home to the selected document, without duplicates. */
export function knowledgeBreadcrumbPath(item: Knowledge | undefined, entries: Knowledge[], homeID: string): Knowledge[] {
  if (!item) return [];
  const byID = new Map(entries.map(entry => [entry.id, entry]));
  const path: Knowledge[] = [];
  const seen = new Set<string>();
  let current: Knowledge | undefined = item;
  while (current && !seen.has(current.id)) {
    seen.add(current.id);
    path.unshift(current);
    current = current.parent_id ? byID.get(current.parent_id) : undefined;
  }
  if (path[0]?.id !== homeID && homeID) {
    const home = byID.get(homeID);
    if (home) path.unshift(home);
  }
  return path;
}

/** Only "pending" and "conflict" are colored; every other state stays muted. */
export function knowledgeDocumentStatus(item: Knowledge | undefined): KnowledgeDocumentStatus {
  if (!item) return {key: "muted", label: t("knowledge.status.published")};
  if (item.status === "candidate" || item.status === "pending") return {key: "pending", label: t("knowledge.status.pending")};
  if (item.status === "conflict" || item.feedback_state === "wrong") return {key: "conflict", label: t("knowledge.status.conflict")};
  if (item.status === "stale" || item.feedback_state === "stale") return {key: "muted", label: t("knowledge.status.stale")};
  return {key: "muted", label: t("knowledge.status.published")};
}

function groupKeyFor(item: Knowledge, homeID: string): string {
  if (item.id === homeID || (item.is_index && !item.parent_id)) return "index";
  if (item.type === "navigation") return "navigation";
  if (item.type === "diagnostic") return "diagnostic";
  if (!item.type || item.type === "practice" || item.type === "knowledge") return "practice";
  return `type:${item.type}`;
}

/** Split the tree into system index and document groups instead of mixing badges. */
export function groupKnowledgeEntries(entries: Knowledge[], homeID: string): KnowledgeTreeGroup[] {
  const labels: Record<string, string> = {index: t("knowledge.group.index"), navigation: t("knowledge.group.navigation"), practice: t("knowledge.group.practice"), diagnostic: t("knowledge.group.diagnostic")};
  const order = ["index", "navigation", "practice", "diagnostic"];
  const groups = new Map<string, Knowledge[]>();
  for (const item of entries) {
    const key = groupKeyFor(item, homeID);
    const bucket = groups.get(key) || [];
    bucket.push(item);
    groups.set(key, bucket);
  }
  const keys = [...groups.keys()].sort((left, right) => {
    const leftIndex = order.indexOf(left);
    const rightIndex = order.indexOf(right);
    if (leftIndex >= 0 || rightIndex >= 0) return (leftIndex < 0 ? 99 : leftIndex) - (rightIndex < 0 ? 99 : rightIndex);
    return left.localeCompare(right);
  });
  return keys.map(key => ({
    key,
    label: labels[key] || (key.startsWith("type:") ? key.slice(5) : t("knowledge.group.other")),
    items: sortKnowledge(groups.get(key) || []),
  }));
}

function filterTreeBySearch(entries: Knowledge[], query: string): Knowledge[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return entries;
  const byID = new Map(entries.map(entry => [entry.id, entry]));
  const keep = new Set<string>();
  for (const entry of entries) {
    const haystack = `${entry.title} ${entry.slug || ""} ${entry.body || ""}`.toLowerCase();
    if (!haystack.includes(needle)) continue;
    let current: Knowledge | undefined = entry;
    const seen = new Set<string>();
    while (current && !seen.has(current.id)) {
      seen.add(current.id);
      keep.add(current.id);
      current = current.parent_id ? byID.get(current.parent_id) : undefined;
    }
  }
  return entries.filter(entry => keep.has(entry.id));
}

function treeTitle(item: Knowledge, homeID: string, homeLabel: string, titleFor: (item: Knowledge) => string): string {
  return item.id === homeID ? homeLabel : titleFor(item);
}

function renderTreeNodes(items: Knowledge[], parentID: string, depth: number, homeID: string, homeLabel: string, selectedID: string, titleFor: (item: Knowledge) => string, seen = new Set<string>()): string {
  return items
    .filter(item => (item.parent_id && items.some(candidate => candidate.id === item.parent_id) ? item.parent_id : "") === parentID)
    .filter(item => !seen.has(item.id))
    .map(item => {
      const children = items.filter(candidate => candidate.parent_id === item.id && !seen.has(candidate.id));
      const nextSeen = new Set(seen);
      nextSeen.add(item.id);
      const collapsed = collapsedTreeIDs.has(item.id);
      const active = item.id === selectedID;
      const label = treeTitle(item, homeID, homeLabel, titleFor);
      return `<div class="knowledge-tree-node" style="--knowledge-indent:${depth * 14}px">
        <div class="knowledge-tree-row">
          ${children.length ? `<button type="button" class="knowledge-tree-toggle ${collapsed ? "collapsed" : ""}" data-knowledge-tree-toggle="${escapeHTML(item.id)}" aria-expanded="${collapsed ? "false" : "true"}" aria-label="${t("knowledge.collapseNode")}">${icon("chevron-down")}</button>` : '<span class="knowledge-tree-toggle-placeholder"></span>'}
          <button type="button" class="knowledge-tree-document ${active ? "active" : ""}" data-knowledge-open="${escapeHTML(item.id)}" title="${escapeHTML(label)}">${escapeHTML(label)}</button>
        </div>
        ${children.length ? `<div class="knowledge-tree-children ${collapsed ? "is-collapsed" : ""}">${renderTreeNodes(items, item.id, depth + 1, homeID, homeLabel, selectedID, titleFor, nextSeen)}</div>` : ""}
      </div>`;
    })
    .join("");
}

function renderDirectory(context: KnowledgeWorkspaceContext | null, groups: KnowledgeTreeGroup[], homeID: string, homeLabel: string, selectedID: string, titleFor: (item: Knowledge) => string, sourceToolbar: string, allowCreate: boolean, readOnly: boolean, total: number): string {
  const project = context ? selectedProject(context.projects) : null;
  const projectSelect = context && activeScope === "project" && context.projects.length
    ? `<div class="knowledge-project-picker"><label class="knowledge-project-select"><span>${t("knowledge.project")}</span><select id="knowledge-project-select">${context.projects.map(item => `<option value="${escapeHTML(item.id)}" ${item.id === project?.id ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label><button type="button" class="icon-button knowledge-project-settings-button" data-knowledge-project-settings title="${t("knowledge.settings.open")}" aria-label="${t("knowledge.settings.open")}">${icon("settings")}</button></div>`
    : "";
  const newButton = allowCreate && !readOnly
    ? `<button class="primary" type="button" data-knowledge-new="${activeScope === "global" ? "global" : "project"}">${icon("plus")}${t("knowledge.new")}</button>`
    : "";
  const empty = groups.length ? "" : renderEmpty(knowledgeSearch.trim() ? t("knowledge.searchEmpty") : t("knowledge.emptyDirectory"));
  return `<aside class="panel knowledge-directory" aria-label="${t("knowledge.expandDirectory")}">
    <div class="knowledge-directory-head">${projectSelect}<div class="knowledge-directory-actions"><button type="button" class="icon-button" data-knowledge-directory-collapse title="${t("knowledge.collapseDirectory")}">${icon("panel")}</button>${newButton}</div><small>${t("knowledge.documentCount", {count: total})}</small></div>
    ${sourceToolbar}
    ${knowledgeSearch.trim() ? `<p class="knowledge-directory-search-hint">${t("knowledge.searchHint", {query: escapeHTML(knowledgeSearch.trim())})}</p>` : ""}
    <nav class="knowledge-tree" role="tree">${groups.map(group => {
      const stateKey = `${activeScope}:${selectedProjectID}:${homeID}:${group.key}`;
      const collapsed = collapsedGroupKeys.has(stateKey);
      return `<section class="knowledge-tree-group"><button type="button" class="knowledge-tree-group-head" data-knowledge-group-toggle="${escapeHTML(stateKey)}" aria-expanded="${collapsed ? "false" : "true"}">${icon("chevron-down")}<span>${escapeHTML(group.label)}</span><small>${group.items.length}</small></button><div class="knowledge-tree-group-body ${collapsed ? "is-collapsed" : ""}">${renderTreeNodes(group.items, "", 0, homeID, homeLabel, selectedID, titleFor)}</div></section>`;
    }).join("")}</nav>
    ${empty}
  </aside>`;
}

function renderFeedbackFooter(item: Knowledge, canEdit: boolean): string {
  if (!canEdit) return "";
  const feedbackLocked = item.status === "stale" || item.feedback_state === "stale" || item.feedback_state === "wrong";
  return `<footer class="knowledge-document-feedback"><button type="button" class="${item.feedback_state === "helped" ? "knowledge-feedback-active" : ""}" data-knowledge-feedback="helped" data-knowledge-id="${escapeHTML(item.id)}" ${feedbackLocked ? "disabled" : ""}>${icon("shield")}${t("knowledge.helpful")} ${Number(item.helped_count || 0)}</button><button type="button" class="${feedbackLocked ? "knowledge-feedback-active stale" : ""}" data-knowledge-feedback="stale" data-knowledge-id="${escapeHTML(item.id)}">${icon("clock")}${t("knowledge.outdated")} ${Number(item.stale_count || 0)}</button></footer>`;
}

function renderReaderDocument(entries: Knowledge[], empty: string, kind: KnowledgeDocumentKind, homeID: string, homeTitle: string, sourceLabelFor: ((item: Knowledge) => string) | undefined, readOnly: boolean, selectedID: string, titleFor: (item: Knowledge) => string): string {
  const selected = entries.find(item => item.id === selectedID);
  const isHome = !selected || selected.id === homeID;
  const ordered = sortKnowledge(entries);
  const byID = new Map(ordered.map(entry => [entry.id, entry]));
  const homeChildren = ordered.filter(item => item.id !== homeID && visualParentID(item, byID, homeID) === homeID);
  const children = isHome ? homeChildren : selected ? ordered.filter(item => item.id !== homeID && visualParentID(item, byID, homeID) === selected.id) : [];
  const managedGlobalCategory = Boolean(selected && managedGlobalKnowledgeIDs.has(selected.id));
  const canEdit = !readOnly && !managedGlobalCategory && Boolean(selected) && (!selected?.bound_project_id || selected.binding_mode === "project");
  const title = isHome ? homeTitle : selected ? titleFor(selected) : t("knowledge.untitled");
  const bodySource = isHome ? knowledgeHomeBody(kind, selected?.body) : selected ? String(selected.body || "") : empty;
  const body = renderMarkdown(bodySource);
  const status = knowledgeDocumentStatus(selected);
  const sourceLabel = isHome ? (kind === "global" ? t("knowledge.sourceGlobal") : t("knowledge.sourceProject")) : selected && sourceLabelFor ? sourceLabelFor(selected) : t("knowledge.sourceProject");
  const path = isHome ? [] : knowledgeBreadcrumbPath(selected, ordered, homeID);
  const breadcrumbs = isHome
    ? `<button type="button" data-knowledge-open="${escapeHTML(homeID)}">${escapeHTML(homeTitle)}</button>`
    : `<button type="button" data-knowledge-open="${escapeHTML(homeID)}">${escapeHTML(homeTitle)}</button>${path.filter(item => item.id !== homeID).map(item => `<span>/</span><button type="button" data-knowledge-open="${escapeHTML(item.id)}">${escapeHTML(titleFor(item))}</button>`).join("")}`;
  const documentLinks = children.length
    ? `<h2>${t("knowledge.links")}</h2><ul class="knowledge-index-links">${children.map(item => `<li><button type="button" class="knowledge-index-link" data-knowledge-open="${escapeHTML(item.id)}">${escapeHTML(titleFor(item))}</button><span>：${escapeHTML(knowledgeIndexSummary(item))}</span></li>`).join("")}</ul>`
    : "";
  const actions = `<div class="knowledge-document-actions">${directoryCollapsed ? `<button type="button" data-knowledge-directory-expand>${t("knowledge.expandDirectory")}</button>` : ""}${canEdit && selected ? `<button type="button" class="primary" data-knowledge-edit="${escapeHTML(selected.id)}">${icon("edit")}${t("common.edit")}</button>` : ""}</div>`;
  const libraryBack = readOnly ? `<button type="button" class="knowledge-mobile-back knowledge-library-back" data-knowledge-library-back>${icon("projects")}${t("knowledge.backToSettings")}</button>` : "";
  const mobileBack = readOnly ? "" : `<button type="button" class="knowledge-mobile-back" data-knowledge-mobile-back>${icon("projects")}${t("knowledge.backToDirectory")}</button>`;
  return `<section class="panel knowledge-reader">
    <article class="knowledge-document">
      ${libraryBack}${mobileBack}
      <header class="knowledge-document-head"><div class="knowledge-breadcrumbs">${breadcrumbs}</div>${actions}</header>
      <h2>${escapeHTML(title)}</h2>
      <div class="knowledge-document-meta"><span>${t("knowledge.sourceLabel")}：${escapeHTML(sourceLabel)}</span><span>${t("knowledge.updated", {time: formatDate(selected?.updated_at)})}</span><span class="knowledge-status status-${status.key}">${status.label}</span><span class="knowledge-access">${canEdit ? t("knowledge.editable") : t("knowledge.readOnly")}</span></div>
      <div class="markdown-body knowledge-document-body">${body}${documentLinks}</div>
      ${!isHome && selected ? renderFeedbackFooter(selected, canEdit) : ""}
    </article>
  </section>`;
}

function renderReadLayout(context: KnowledgeWorkspaceContext | null, entries: Knowledge[], empty: string, kind: KnowledgeDocumentKind, readOnly = false, sourceLabelFor?: (item: Knowledge) => string, toolbar = "", allowCreate = true): string {
  const progressive = kind === "project-navigation";
  const ordered = progressive ? sortProjectNavigation(entries) : sortKnowledge(entries);
  const root = ordered.find(item => item.is_index && !item.parent_id);
  const homeID = root?.id || `${syntheticHomeID}-${kind}`;
  const homeTitle = kind === "global" ? t("knowledge.home.global") : kind === "project-navigation" ? t("knowledge.home.projectNavigation") : t("knowledge.home.project");
  const homeLabel = kind === "global" ? t("knowledge.home.globalIndex") : t("knowledge.home.projectIndex");
  const titleFor = kind === "global" ? (item: Knowledge) => item.title : navigationDisplayTitle;
  if (selectedKnowledgeID !== homeID && !ordered.some(item => item.id === selectedKnowledgeID)) selectedKnowledgeID = homeID;
  const selectedID = selectedKnowledgeID;
  const visible = filterTreeBySearch(ordered, knowledgeSearch);
  const groups = groupKnowledgeEntries(visible, homeID);
  const directory = renderDirectory(context, groups, homeID, homeLabel, selectedID, titleFor, toolbar, allowCreate, readOnly, ordered.length);
  const reader = renderReaderDocument(ordered, empty, kind, homeID, homeTitle, sourceLabelFor, readOnly, selectedID, titleFor);
  const layoutClass = `knowledge-read-layout ${mobilePane === "reader" ? "reader-open" : "directory-open"} ${directoryCollapsed ? "directory-collapsed" : ""}`;
  return `<div class="${layoutClass}">${directory}${reader}</div>`;
}

export function renderKnowledgeDocumentWorkspace(entries: Knowledge[], empty: string, newKind: KnowledgeDocumentKind, readOnly = false, sourceLabelFor?: (item: Knowledge) => string, toolbar = "", allowCreate = true): string {
  return renderReadLayout(null, entries, empty, newKind, readOnly, sourceLabelFor, toolbar, allowCreate);
}

function renderEditWorkspace(context: KnowledgeWorkspaceContext, project: Project | null): string {
  if (!editState) return renderReadWorkspace(context, project);
  const entries = activeScope === "global" ? context.knowledge.filter(item => item.scope === "global") : project ? projectEntries(context, project.id) : [];
  const ordered = sortKnowledge(entries);
  const root = ordered.find(item => item.is_index && !item.parent_id);
  const homeID = root?.id || `${syntheticHomeID}-project`;
  const homeTitle = activeScope === "global" ? t("knowledge.home.global") : t("knowledge.home.project");
  const homeLabel = activeScope === "global" ? t("knowledge.home.globalIndex") : t("knowledge.home.projectIndex");
  const titleFor = activeScope === "global" ? (item: Knowledge) => item.title : navigationDisplayTitle;
  const groups = groupKnowledgeEntries(filterTreeBySearch(ordered, knowledgeSearch), homeID);
  const sources = activeScope === "project" && project ? projectKnowledgeSources(project, entries, context.libraries || []) : [];
  const sourceToolbar = activeScope === "project" && project ? knowledgeSourceToolbar(sources) : "";
  const directory = renderDirectory(context, groups, homeID, homeLabel, selectedKnowledgeID, titleFor, sourceToolbar, false, false, ordered.length);
  const parentOptions = ordered.filter(item => !item.is_index && item.id !== editState.id).map(item => `<option value="${escapeHTML(item.id)}" ${item.id === editState.parentID ? "selected" : ""}>${escapeHTML(item.title)}</option>`).join("");
  const productLineOptions = productLines.map(line => `<option value="${escapeHTML(line.id)}" ${line.id === editState.productLineID ? "selected" : ""}>${escapeHTML(line.name)}</option>`).join("");
  const heading = editState.isIndex ? t("knowledge.edit.index") : editState.isNew ? t("knowledge.edit.new") : t("knowledge.edit.title");
  const deleteButton = !editState.isNew && !editState.isIndex ? `<button type="button" class="danger" data-knowledge-delete="${escapeHTML(editState.id)}">${t("knowledge.edit.delete")}</button>` : "";
  const form = `<section class="panel knowledge-editor">
    <form id="knowledge-inline-editor" novalidate>
      <input type="hidden" name="id" value="${escapeHTML(editState.id)}">
      <input type="hidden" name="is_index" value="${editState.isIndex ? "true" : "false"}">
      <input type="hidden" name="type" value="${escapeHTML(editState.type)}">
      <input type="hidden" name="status" value="${escapeHTML(editState.status)}">
      <input type="hidden" name="project_id" value="${escapeHTML(editState.projectID || project?.id || "")}">
      <div class="knowledge-editor-head"><div><h2>${heading}</h2><p>${t("knowledge.edit.hint")}</p></div></div>
      <div class="knowledge-editor-fields">
        <label>${t("knowledge.edit.titleLabel")}<input name="title" required value="${escapeHTML(editState.title)}" data-allow-rerender="true"></label>
        <label>${t("knowledge.edit.body")}<textarea name="body" rows="18" required data-allow-rerender="true">${escapeHTML(editState.body)}</textarea></label>
        <div class="two">
          <label>${t("knowledge.edit.scope")}<select name="scope" ${editState.isIndex ? "disabled" : ""}><option value="project" ${editState.scope === "project" ? "selected" : ""}>${t("knowledge.edit.scopeProject")}</option><option value="global" ${editState.scope === "global" ? "selected" : ""}>${t("knowledge.edit.scopeGlobal")}</option></select></label>
          <label class="knowledge-project-field" ${editState.scope === "global" ? "hidden" : ""}>${t("knowledge.edit.productLine")}<select name="product_line_id"><option value="">${t("knowledge.edit.all")}</option>${productLineOptions}</select></label>
        </div>
        <label>${t("knowledge.edit.parent")}<select name="parent_id" ${editState.isIndex ? "disabled" : ""}><option value="">${t("knowledge.edit.home")}</option>${parentOptions}</select></label>
        <details class="knowledge-advanced"><summary>${t("knowledge.edit.advanced")}</summary><div class="two"><label>${t("knowledge.edit.slug")}<input name="slug" pattern="[a-z0-9]+(?:-[a-z0-9]+)*" value="${escapeHTML(editState.slug)}" placeholder="${t("knowledge.edit.slugHint")}"></label><label>${t("knowledge.edit.sort")}<input name="sort_order" type="number" min="0" value="${editState.sortOrder}"></label></div><div class="two"><label>${t("knowledge.edit.confidence")}<input name="confidence" type="number" min="0" max="1" step="0.05" value="${editState.confidence}"></label><label>${t("knowledge.edit.commit")}<input name="verified_commit" value="${escapeHTML(editState.verifiedCommit)}" placeholder="commit SHA"></label></div><label class="knowledge-project-field" ${editState.scope === "global" ? "hidden" : ""}>${t("knowledge.edit.branch")}<input name="branch_scope" value="${escapeHTML(editState.branchScope)}" placeholder="main / release/*"></label></details>
      </div>
      <footer class="knowledge-edit-footer"><span class="knowledge-unsaved" data-knowledge-unsaved>${t("knowledge.edit.unsaved")}</span><span class="knowledge-edit-delete">${deleteButton}</span><div><button type="button" data-knowledge-edit-cancel>${t("common.cancel")}</button><button class="primary" type="submit">${t("common.save")}</button></div></footer>
    </form>
  </section>`;
  const layoutClass = `knowledge-read-layout knowledge-edit-layout ${mobilePane === "reader" ? "reader-open" : "directory-open"}`;
  return `<div class="${layoutClass}">${directory}${form}</div>`;
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
  const titleDiff = baseTitle !== title ? `<div class="knowledge-proposal-title-diff"><del>${escapeHTML(baseTitle || t("knowledge.untitled"))}</del><span>→</span><ins>${escapeHTML(title)}</ins></div>` : "";
  const lines = proposalLineDiff(baseBody, body).map(line => `<div class="${line.kind}"><span>${line.kind === "add" ? "+" : line.kind === "remove" ? "−" : ""}</span><code>${escapeHTML(line.text) || "&nbsp;"}</code></div>`).join("");
  return `${titleDiff}<div class="knowledge-proposal-diff" aria-label="${t("knowledge.diffLabel")}">${lines}</div>`;
}

function proposalScopeLabel(proposal: KnowledgeProposal, entries: Knowledge[], projects: Project[]): string {
  const entry = entries.find(item => item.id === proposal.entry_id);
  const scope = proposal.scope || proposal.proposed?.scope || entry?.scope;
  const projectID = proposal.proposed?.bound_project_id || proposal.project_id || proposal.proposed?.project_id || entry?.bound_project_id || entry?.project_id || "";
  if (scope === "global") return t("knowledge.review.scopeGlobal");
  return projects.find(project => project.id === projectID)?.name || t("knowledge.review.scopeProject");
}

function proposalConfidenceLabel(proposal: KnowledgeProposal, entry?: Knowledge): string {
  const value = proposal.proposed?.confidence ?? entry?.confidence;
  return typeof value === "number" && value > 0 ? `${Math.round(value * 100)}%` : "—";
}

/** Pending proposals plus legacy candidate entries, newest first. */
export function knowledgeReviewQueue(proposals: KnowledgeProposal[], entries: Knowledge[], projects: Project[] = []): KnowledgeReviewItem[] {
  const pending = proposals.filter(item => item.status === "pending").sort((left, right) => String(right.created_at).localeCompare(String(left.created_at)));
  const pendingEntryIDs = new Set(pending.map(item => item.entry_id));
  const legacy = entries.filter(item => !item.is_index && item.status === "candidate" && !pendingEntryIDs.has(item.id)).sort((left, right) => String(right.updated_at).localeCompare(String(left.updated_at)));
  const items: KnowledgeReviewItem[] = pending.map(proposal => {
    const entry = entries.find(item => item.id === proposal.entry_id);
    return {
      key: `proposal:${proposal.id}`,
      kind: "proposal",
      title: proposalValue(proposal, "title") || t("knowledge.review.untitled"),
      scopeLabel: proposalScopeLabel(proposal, entries, projects),
      confidenceLabel: proposalConfidenceLabel(proposal, entry),
      createdAt: proposal.created_at,
      sourceLabel: proposal.source_task_id ? t("knowledge.review.sourceTask", {id: proposal.source_task_id}) : t("knowledge.review.suggestion"),
      scope: proposal.scope || proposal.proposed?.scope || entry?.scope || "project",
      projectID: proposal.proposed?.bound_project_id || proposal.project_id || proposal.proposed?.project_id || entry?.bound_project_id || entry?.project_id || "",
      proposal,
    };
  });
  for (const entry of legacy) {
    items.push({
      key: `legacy:${entry.id}`,
      kind: "legacy",
      title: entry.title,
      scopeLabel: entry.scope === "global" ? t("knowledge.review.scopeGlobal") : projects.find(project => project.id === (entry.bound_project_id || entry.project_id))?.name || t("knowledge.review.scopeProject"),
      confidenceLabel: typeof entry.confidence === "number" && entry.confidence > 0 ? `${Math.round(entry.confidence * 100)}%` : "—",
      createdAt: entry.updated_at,
      sourceLabel: t("knowledge.review.legacy"),
      scope: entry.scope,
      projectID: entry.bound_project_id || entry.project_id || "",
      entry,
    });
  }
  return items;
}

function matchesReviewScope(scope: "global" | "project", projectID: string, filter: ReviewScopeFilter): boolean {
  if (filter === "all") return true;
  if (filter === "global") return scope === "global";
  return scope === "project" && `project:${projectID}` === filter;
}

/** The review inbox is cross-project; the in-page filter narrows it without changing the queue. */
export function filterKnowledgeReviewItems(items: KnowledgeReviewItem[], filter: ReviewScopeFilter): KnowledgeReviewItem[] {
  return items.filter(item => matchesReviewScope(item.scope, item.projectID, filter));
}

function renderStaleFallback(entries: Knowledge[], pendingEntryIDs: Set<string>, projects: Project[], filter: ReviewScopeFilter): string {
  const stale = entries.filter(item => !item.is_index && !pendingEntryIDs.has(item.id) && (item.status === "stale" || item.feedback_state === "wrong") && matchesReviewScope(item.scope, item.bound_project_id || item.project_id || "", filter));
  if (!stale.length) return "";
  const projectNames = new Map(projects.map(project => [project.id, project.name]));
  return `<section class="knowledge-stale-fallback"><div><h3>${t("knowledge.review.staleTitle")}</h3><p>${t("knowledge.review.staleBody")}</p></div>${stale.map(item => `<article><span><strong>${escapeHTML(item.title)}</strong><small>${escapeHTML(item.scope === "global" ? t("knowledge.review.scopeGlobal") : projectNames.get(item.project_id || "") || t("knowledge.review.scopeProject"))}</small></span><button type="button" data-knowledge-verify="${escapeHTML(item.id)}">${t("knowledge.review.verify")}</button></article>`).join("")}</section>`;
}

function renderReviewScopeFilter(projects: Project[]): string {
  const options = [
    `<option value="all" ${reviewScopeFilter === "all" ? "selected" : ""}>${t("knowledge.review.filterAll")}</option>`,
    `<option value="global" ${reviewScopeFilter === "global" ? "selected" : ""}>${t("knowledge.review.scopeGlobal")}</option>`,
    ...projects.map(project => {
      const value = `project:${project.id}`;
      return `<option value="${escapeHTML(value)}" ${reviewScopeFilter === value ? "selected" : ""}>${escapeHTML(t("knowledge.review.filterProject", {name: project.name}))}</option>`;
    }),
  ].join("");
  return `<label class="knowledge-review-filter"><span>${t("knowledge.review.filterLabel")}</span><select data-knowledge-review-scope>${options}</select></label>`;
}

export function renderKnowledgeReviewPage(context: KnowledgeWorkspaceContext): string {
  return renderReviewPage(context);
}

function renderReviewPage(context: KnowledgeWorkspaceContext): string {
  const items = filterKnowledgeReviewItems(knowledgeReviewQueue(context.proposals || [], context.knowledge, context.projects), reviewScopeFilter);
  const pendingEntryIDs = new Set((context.proposals || []).filter(item => item.status === "pending").map(item => item.entry_id));
  if (reviewIndex >= items.length) reviewIndex = Math.max(0, items.length - 1);
  const current = items[reviewIndex];
  const reviewSettings = context.reviewSettings || {auto_approve: false};
  const autoReview = `<label class="knowledge-auto-review-toggle"><input type="checkbox" data-knowledge-auto-review ${reviewSettings.auto_approve ? "checked" : ""}><span><strong>${t("knowledge.review.auto")}</strong><small>${reviewSettings.auto_approve ? t("knowledge.review.autoOn") : t("knowledge.review.autoOff")}</small></span></label>`;
  const head = `<div class="knowledge-review-head"><button type="button" data-knowledge-mode="read">${t("knowledge.review.back")}</button><div><h2>${t("knowledge.review.heading")}</h2><p>${t("knowledge.review.subtitle")}</p></div>${renderReviewScopeFilter(context.projects)}${autoReview}</div>`;
  if (!current) {
    return `<section class="knowledge-review">${head}${renderEmpty(reviewScopeFilter === "all" ? t("knowledge.review.empty") : t("knowledge.review.filterEmpty"))}${renderStaleFallback(context.knowledge, pendingEntryIDs, context.projects, reviewScopeFilter)}</section>`;
  }
  const entry = current.proposal ? context.knowledge.find(item => item.id === current.proposal?.entry_id) : current.entry;
  const body = current.proposal ? proposalDiffMarkup(current.proposal, context.knowledge) : `<div class="knowledge-proposal-full"><div class="markdown-body">${renderMarkdown(current.entry?.body || "")}</div></div>`;
  const actions = current.kind === "proposal"
    ? `<div class="knowledge-review-actions"><button type="button" class="danger" data-knowledge-proposal-reject="${escapeHTML(current.proposal?.id || "")}">${t("knowledge.review.reject")}</button><button type="button" class="primary" data-knowledge-proposal-approve="${escapeHTML(current.proposal?.id || "")}">${t("knowledge.review.approve")}</button></div>`
    : `<div class="knowledge-review-actions"><button type="button" class="danger" data-knowledge-delete="${escapeHTML(current.entry?.id || "")}">${t("knowledge.review.rejectDelete")}</button><button type="button" class="primary" data-knowledge-verify="${escapeHTML(current.entry?.id || "")}">${t("knowledge.review.approveInclude")}</button></div>`;
  const nav = `<div class="knowledge-review-nav"><span>${t("knowledge.review.position", {current: String(reviewIndex + 1), total: String(items.length)})}</span><button type="button" data-knowledge-review-prev ${reviewIndex <= 0 ? "disabled" : ""}>${t("knowledge.review.prev")}</button><button type="button" data-knowledge-review-next ${reviewIndex >= items.length - 1 ? "disabled" : ""}>${t("knowledge.review.next")}</button></div>`;
  const card = `<article class="panel knowledge-review-card" aria-live="polite">
    <header><div><strong>${escapeHTML(current.title)}</strong><small>${escapeHTML(current.sourceLabel)} · ${escapeHTML(current.scopeLabel)}</small></div><div class="knowledge-review-facts"><span>${t("knowledge.review.confidence")} ${escapeHTML(current.confidenceLabel)}</span><span>${escapeHTML(current.createdAt ? formatDate(current.createdAt) : "-")}</span></div></header>
    <div class="knowledge-review-body">${body}</div>
    ${entry ? `<p class="knowledge-review-entry">${t("knowledge.review.target", {title: escapeHTML(entry.title)})}</p>` : ""}
    ${actions}
  </article>`;
  return `<section class="knowledge-review">${head}${nav}${card}${renderStaleFallback(context.knowledge, pendingEntryIDs, context.projects, reviewScopeFilter)}</section>`;
}

function renderBindings(workspaces: Workspace[]): string {
  return `<section class="panel knowledge-bindings"><div class="panel-head"><strong>Workspace Bindings</strong><span>Project → Native / WSL / SSH</span></div>
    <div class="knowledge-binding-grid">${workspaces.map(item => `<article><div class="knowledge-binding-icon">${icon(item.transport === "ssh" ? "server" : "projects")}</div><div><strong>${escapeHTML(item.name)}</strong><small>${escapeHTML(item.transport)} · ${escapeHTML(item.locality)} · ${escapeHTML(item.health)}</small><p>${escapeHTML(item.root_path)}</p></div></article>`).join("") || renderEmpty(t("knowledge.manage.noBindings"))}</div>
  </section>`;
}

function knowledgeListFooter(shown: number, total: number, totalLabel = ""): string {
  if (total === 0) return "";
  const summary = totalLabel || t("knowledge.list.total", {count: total});
  if (shown >= total) return `<div class="knowledge-list-footer"><span>${summary}</span></div>`;
  return `<div class="knowledge-list-footer"><span>${t("knowledge.list.shown", {summary, shown})}</span><button type="button" class="knowledge-load-more" data-knowledge-skill-more>${t("knowledge.list.more")}</button></div>`;
}

function skillProjectName(context: KnowledgeWorkspaceContext, item: Skill): string {
  const project = context.projects.find(candidate => candidate.id === (item.bound_project_id || item.project_id));
  return project?.name || item.bound_project_id || item.project_id || "";
}

function skillScopeLabel(context: KnowledgeWorkspaceContext, item: Skill): string {
  if (item.scope === "global") return t("knowledge.skill.scopeGlobal");
  if (item.bound_project_id || item.binding_mode) return t("knowledge.skill.scopeExternal", {name: skillProjectName(context, item)});
  return t("knowledge.skill.scopeProject", {name: skillProjectName(context, item)});
}

function filteredSkillList(context: KnowledgeWorkspaceContext): Skill[] {
  const query = skillSearch.trim().toLowerCase();
  return skills.filter(item => {
    if (query && ![item.name, item.description].some(value => String(value || "").toLowerCase().includes(query))) return false;
    if (skillScopeFilter === "global" && item.scope !== "global") return false;
    if (skillScopeFilter === "project" && item.scope !== "project") return false;
    if (skillScopeFilter === "bound" && !item.bound_project_id && !item.binding_mode) return false;
    if (skillProjectFilter && item.project_id !== skillProjectFilter && item.bound_project_id !== skillProjectFilter) return false;
    if (skillStatusFilter !== "all" && item.status !== skillStatusFilter) return false;
    return true;
  });
}

function skillCard(context: KnowledgeWorkspaceContext, item: Skill): string {
  const auto = item.auto_attach ? `<span class="capability-badge">${t("knowledge.skill.auto")}</span>` : "";
  return `<article class="skill-card ${item.enabled ? "" : "disabled"}"><header><div class="skill-mark">${icon("bot")}</div><div><strong>${escapeHTML(item.name)}${auto}</strong><small>${escapeHTML(skillScopeLabel(context, item))} · v${item.version} · ${escapeHTML(item.status)}</small></div><label class="skill-switch"><input type="checkbox" data-skill-toggle="${item.id}" ${item.enabled ? "checked" : ""}><span></span></label></header><p>${escapeHTML(item.description || t("knowledge.skill.noDescription"))}</p><div class="skill-package-files"><strong>Skill Package</strong><span>${(item.files || []).map(file => `<code>${escapeHTML(file)}</code>`).join("") || "<code>SKILL.md</code>"}</span></div><footer><span title="${escapeHTML(item.source_path || "")}">${escapeHTML(item.source_path || "AHA managed package")}</span><div><button class="icon-button" type="button" data-skill-view="${item.id}" title="${t("knowledge.skill.view")}">${icon("eye")}</button><button class="icon-button" type="button" data-skill-edit="${item.id}">${icon("edit")}</button><button class="icon-button" type="button" data-skill-delete="${item.id}">${icon("close")}</button></div></footer></article>`;
}

function renderSkills(context: KnowledgeWorkspaceContext): string {
  const visible = filteredSkillList(context);
  const shown = visible.slice(0, skillLimit);
  const projectOptions = context.projects.map(project => `<option value="${escapeHTML(project.id)}" ${project.id === skillProjectFilter ? "selected" : ""}>${escapeHTML(project.name)}</option>`).join("");
  const filtering = Boolean(skillSearch.trim()) || skillScopeFilter !== "all" || Boolean(skillProjectFilter) || skillStatusFilter !== "all";
  const totalLabel = filtering ? t("knowledge.skill.matchTotal", {matched: visible.length, total: skills.length}) : t("knowledge.skill.total", {total: skills.length});
  return `<div class="knowledge-section-head"><div><h2>${t("knowledge.skill.heading")}</h2><p>${t("knowledge.skill.subtitle")}</p></div><button class="primary" type="button" data-skill-new>${icon("plus")}${t("knowledge.skill.new")}</button></div>
    <div class="knowledge-skill-toolbar">
      <input id="knowledge-skill-search" type="search" autocomplete="off" data-allow-rerender="true" placeholder="${t("knowledge.skill.search")}" value="${escapeHTML(skillSearch)}">
      <select id="knowledge-skill-scope" aria-label="${t("knowledge.skill.scopeAria")}">
        <option value="all" ${skillScopeFilter === "all" ? "selected" : ""}>${t("knowledge.skill.scope.all")}</option>
        <option value="global" ${skillScopeFilter === "global" ? "selected" : ""}>${t("knowledge.skill.scope.global")}</option>
        <option value="project" ${skillScopeFilter === "project" ? "selected" : ""}>${t("knowledge.skill.scope.project")}</option>
        <option value="bound" ${skillScopeFilter === "bound" ? "selected" : ""}>${t("knowledge.skill.scope.external")}</option>
      </select>
      <select id="knowledge-skill-project" aria-label="${t("knowledge.skill.projectAria")}">
        <option value="">${t("knowledge.skill.project.all")}</option>${projectOptions}
      </select>
      <select id="knowledge-skill-status" aria-label="${t("knowledge.skill.statusAria")}">
        <option value="all" ${skillStatusFilter === "all" ? "selected" : ""}>${t("knowledge.skill.status.all")}</option>
        <option value="active" ${skillStatusFilter === "active" ? "selected" : ""}>active</option>
        <option value="draft" ${skillStatusFilter === "draft" ? "selected" : ""}>draft</option>
        <option value="archived" ${skillStatusFilter === "archived" ? "selected" : ""}>archived</option>
      </select>
    </div>
    <div class="skill-grid">${shown.map(item => skillCard(context, item)).join("") || renderEmpty(t("knowledge.skill.empty"))}</div>
    ${knowledgeListFooter(shown.length, visible.length, totalLabel)}`;
}

function renderKnowledgeLibraries(context: KnowledgeWorkspaceContext, currentProjectID = ""): string {
  const projectName = new Map(context.projects.map(project => [project.id, project.name]));
  const options = context.projects.map(project => `<option value="${escapeHTML(project.id)}" ${project.id === currentProjectID ? "selected" : ""}>${escapeHTML(project.name)}</option>`).join("");
  const cards = (context.libraries || []).filter(library => !library.bound_project_id).map(library => `<article class="panel knowledge-library-card pending">
    <header><div><span class="knowledge-library-state">${library.bound_project_id ? t("knowledge.manage.boundLabel") : t("knowledge.manage.pendingLabel")}</span><strong>${escapeHTML(library.name)}</strong></div><small>${t("knowledge.manage.counts", {knowledge: library.knowledge_count, skills: library.skill_count})}</small></header>
    <p>${escapeHTML(library.description || library.source_identity || t("knowledge.manage.libraryDescription"))}</p>
    <footer><button type="button" data-knowledge-library-open="${escapeHTML(library.id)}">${t("knowledge.manage.viewLibrary")}</button>${library.bound_project_id
      ? `<span>${t("knowledge.manage.boundTo", {name: escapeHTML(projectName.get(library.bound_project_id) || library.bound_project_id)})}</span><button type="button" data-knowledge-library-unbind="${escapeHTML(library.id)}">${t("knowledge.manage.unbind")}</button>`
      : `<label><span>${t("knowledge.manage.bindTo")}</span><select data-knowledge-library-target="${escapeHTML(library.id)}"><option value="">${t("knowledge.manage.pickProject")}</option>${options}</select></label><label><span>${t("knowledge.manage.bindingMode")}</span><select data-knowledge-library-mode="${escapeHTML(library.id)}"><option value="project">${t("knowledge.binding.project")}</option><option value="external" selected>${t("knowledge.binding.external")}</option></select></label><button class="primary" type="button" data-knowledge-library-bind="${escapeHTML(library.id)}">${t("knowledge.manage.bind")}</button>`}<button class="danger" type="button" data-knowledge-library-delete="${escapeHTML(library.id)}">${t("knowledge.manage.deleteLibrary")}</button>
    </footer></article>`).join("");
  return `<div class="knowledge-section-head"><div><h2>${t("knowledge.manage.unboundTitle")}</h2><p>${t("knowledge.manage.unboundNote")}</p></div></div><div class="knowledge-library-grid">${cards || renderEmpty(t("knowledge.manage.emptyLibraries"))}</div>`;
}

function renderProjectSettingsContent(project: Project, projectWorkspaces: Workspace[], context: KnowledgeWorkspaceContext): string {
  const boundLibraries = (context.libraries || []).filter(item => item.bound_project_id === project.id);
  const directKnowledge = context.knowledge.filter(item => item.scope === "project" && item.project_id === project.id && !item.is_index).length;
  const directSkills = skills.filter(item => item.scope === "project" && item.project_id === project.id).length;
  const ownedKnowledge = directKnowledge + directSkills ? `<article><div>${icon("knowledge")}<span><strong>${t("knowledge.manage.ownName", {name: escapeHTML(project.name)})}</strong><small>${t("knowledge.manage.counts", {knowledge: directKnowledge, skills: directSkills})}</small></span></div><div class="actions"><button type="button" data-project-knowledge-detach>${t("knowledge.manage.detach")}</button><button class="danger" type="button" data-project-knowledge-delete>${t("knowledge.manage.deleteOwn")}</button></div></article>` : "";
  return `<section class="panel knowledge-setting-card"><div><strong>${t("knowledge.manage.policy")}</strong><p>${t("knowledge.manage.policyNote")}</p></div><button type="button" class="${project.knowledge_policy === "disabled" ? "" : "primary"}" data-project-kb-toggle>${project.knowledge_policy === "disabled" ? t("knowledge.manage.disabled") : t("knowledge.manage.enabled")}</button></section>
    <section class="panel spaced knowledge-source-settings"><div class="panel-head"><div><strong>${t("knowledge.manage.own")}</strong><small>${t("knowledge.manage.ownNote")}</small></div><span>${t("knowledge.manage.ownCount", {count: directKnowledge})}</span></div><div class="product-line-list">${ownedKnowledge || renderEmpty(t("knowledge.manage.noOwn"))}</div></section>
    <section class="panel spaced knowledge-source-settings"><div class="panel-head"><div><strong>${t("knowledge.manage.bound")}</strong><small>${t("knowledge.manage.boundNote")}</small></div><span>${boundLibraries.length}</span></div><div class="product-line-list">${boundLibraries.map(item => `<article><div>${icon("knowledge")}<span><strong>${escapeHTML(item.name)}</strong><small>${t("knowledge.manage.counts", {knowledge: item.knowledge_count, skills: item.skill_count})} · ${bindingModeLabel(item.binding_mode)}</small></span></div><div class="actions"><select aria-label="${t("knowledge.manage.bindingMode")}" data-knowledge-library-mode="${escapeHTML(item.id)}"><option value="project" ${item.binding_mode === "project" ? "selected" : ""}>${t("knowledge.binding.project")}</option><option value="external" ${item.binding_mode !== "project" ? "selected" : ""}>${t("knowledge.binding.external")}</option></select><button type="button" data-knowledge-library-mode-save="${escapeHTML(item.id)}">${t("knowledge.manage.saveMode")}</button><button type="button" data-knowledge-library-unbind="${escapeHTML(item.id)}">${t("knowledge.manage.unbind")}</button><button class="danger" type="button" data-knowledge-library-delete="${escapeHTML(item.id)}">${t("knowledge.manage.deleteLibrary")}</button></div></article>`).join("") || renderEmpty(t("knowledge.manage.noBound"))}</div></section>
    <section class="panel spaced"><div class="panel-head"><strong>Product Lines</strong><button type="button" data-product-line-new>${icon("plus")}${t("knowledge.manage.newProductLine")}</button></div><div class="product-line-list">${productLines.map(line => `<article><div>${icon("branch")}<span><strong>${escapeHTML(line.name)}</strong><small>${escapeHTML(line.branch_pattern || "*")} ${line.default ? "· default" : ""}</small></span></div><button type="button" class="icon-button" data-product-line-delete="${line.id}">${icon("close")}</button></article>`).join("") || renderEmpty(t("knowledge.manage.noProductLine"))}</div></section>
    ${renderBindings(projectWorkspaces)}`;
}

/** Project settings is a subpage of project knowledge, not a top-level entry: it only
 *  shows the current project's libraries, bindings, and workspace bindings. */
export function renderKnowledgeProjectSettings(context: KnowledgeWorkspaceContext): string {
  const project = selectedProject(context.projects);
  if (!project) return renderEmpty(t("knowledge.manage.noProject"));
  const projectWorkspaces = context.workspaces.filter(item => item.project_id === project.id);
  const projectSelect = `<label class="knowledge-project-select"><span>${t("knowledge.project")}</span><select id="knowledge-project-select">${context.projects.map(item => `<option value="${escapeHTML(item.id)}" ${item.id === project.id ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label>`;
  return `<section class="knowledge-settings">
    <div class="knowledge-settings-head"><button type="button" class="knowledge-settings-close" data-knowledge-settings-close>${icon("projects")}${t("knowledge.settings.close")}</button><div><h2>${t("knowledge.manage.settings")}</h2><p>${t("knowledge.manage.settingsNote")}</p></div>${projectSelect}</div>
    ${renderProjectSettingsContent(project, projectWorkspaces, context)}
    ${renderKnowledgeLibraries(context, project.id)}
  </section>`;
}

/** Skills keep their own layered catalog; this is a top-level section, not a management tab. */
export function renderKnowledgeSkillsPage(context: KnowledgeWorkspaceContext): string {
  return `<section class="knowledge-skills">${renderSkills(context)}</section>`;
}

function renderReadWorkspace(context: KnowledgeWorkspaceContext, project: Project | null): string {
  if (settingsOpen && activeScope === "project") {
    return renderKnowledgeProjectSettings(context);
  }
  if (selectedLibraryID && activeScope === "project") {
    const library = (context.libraries || []).find(item => item.id === selectedLibraryID);
    if (!library) {
      selectedLibraryID = "";
    } else {
      const libraryEntries = context.knowledge.filter(item => item.scope === "project" && item.project_id === library.container_project_id);
      return renderReadLayout(context, libraryEntries, t("knowledge.emptyDirectory"), "project", true, () => library.name);
    }
  }
  if (activeScope === "global") {
    return renderReadLayout(context, context.knowledge.filter(item => item.scope === "global"), t("knowledge.emptyGlobal"), "global");
  }
  if (!project) return renderEmpty(t("knowledge.emptyProject"));
  const entries = projectEntries(context, project.id);
  const sources = projectKnowledgeSources(project, entries, context.libraries || []);
  if (selectedKnowledgeSourceID !== "all" && !sources.some(source => source.id === selectedKnowledgeSourceID)) selectedKnowledgeSourceID = "all";
  const sourceLabelFor = (entry: Knowledge) => sources.find(source => source.id === (entry.project_id || project.id))?.label || t("knowledge.sourceProject");
  const filtered = selectedKnowledgeSourceID === "all" ? entries : entries.filter(item => item.project_id === selectedKnowledgeSourceID || item.is_index && selectedKnowledgeSourceID === project.id);
  const allowCreate = selectedKnowledgeSourceID === "all" || selectedKnowledgeSourceID === project.id;
  const selectedSource = sources.find(source => source.id === selectedKnowledgeSourceID);
  return renderReadLayout(context, filtered, t("knowledge.emptyProject"), "project", Boolean(selectedSource?.external), sourceLabelFor, knowledgeSourceToolbar(sources), allowCreate);
}

function renderSectionNav(pendingCount: number): string {
  const section = activeMode === "skills" ? "skills" : activeMode === "review" ? "review" : activeScope;
  const button = (key: string, label: string, extra = "") => `<button type="button" data-knowledge-section="${key}" ${extra} aria-current="${section === key ? "page" : "false"}" class="${section === key ? "active" : ""}">${label}</button>`;
  return `<nav class="knowledge-section-nav" aria-label="${t("knowledge.sectionNav")}">
    ${button("project", t("knowledge.tab.project"), 'data-knowledge-scope="project"')}
    ${button("global", t("knowledge.tab.global"), 'data-knowledge-scope="global"')}
    ${button("skills", t("knowledge.skillsEntry"), 'data-knowledge-mode="skills"')}
    ${button("review", `${t("knowledge.reviewEntry")} <b class="knowledge-pending-count ${pendingCount ? "has-pending" : ""}">${pendingCount}</b>`, 'data-knowledge-mode="review"')}
  </nav>`;
}

function renderTopbar(pendingCount: number): string {
  const search = (activeMode === "read" || activeMode === "edit") && !settingsOpen
    ? `<label class="knowledge-search-box">${icon("search")}<input id="knowledge-search" type="search" autocomplete="off" data-allow-rerender="true" placeholder="${t("knowledge.searchPlaceholder")}" value="${escapeHTML(knowledgeSearch)}"></label>`
    : "";
  return `<div class="page-head knowledge-topbar">
    <div class="knowledge-heading"><h1>${t("knowledge.title")}</h1><p>${t("knowledge.subtitle")}</p></div>
    ${renderSectionNav(pendingCount)}
    ${search}
    <button id="knowledge-refresh" type="button">${icon("refresh")}${t("common.refresh")}</button>
  </div>`;
}


function skillDialog(projects: Project[], projectID: string): string {
  return `<dialog id="skill-editor-dialog" class="wide"><form id="skill-editor-form"><div class="dialog-head"><h2>Skill Package</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><input type="hidden" name="id"><div class="two"><label>作用域<select name="scope"><option value="global">Global</option><option value="project">Project</option></select></label><label>状态<select name="status"><option value="active">active</option><option value="draft">draft</option><option value="archived">archived</option></select></label></div><label class="skill-project-field" hidden>Project<select name="project_id"><option value="">请选择</option>${projects.map(item => `<option value="${item.id}" ${item.id === projectID ? "selected" : ""}>${escapeHTML(item.name)}</option>`).join("")}</select></label><label>名称<input name="name" required></label><label>描述<input name="description" placeholder="用于 Task 选择时判断该 Skill 是否适用"></label><label>SKILL.md 指令<textarea name="instructions" rows="12" required></textarea></label><p class="field-help">AHA 会自动管理 Skill 目录。脚本放入该目录的 <code>scripts/</code> 后，会随选中的 Skill 一起进入 Task Context。</p><label class="skill-enabled"><input name="enabled" type="checkbox" checked>启用</label><label class="skill-enabled"><input name="auto_attach" type="checkbox">新建任务时自动启用</label><p class="field-help">自动启用默认勾选、创建任务时可取消；全局 Skill 对所有新任务生效（含宠物和渠道系统任务），项目 Skill 仅对该项目生效。外部绑定 Skill 不自动启用。</p></div><div class="dialog-actions"><button type="button" data-kb-close>取消</button><button class="primary" type="submit">保存</button></div></form></dialog>`;
}

function skillDetailDialog(): string {
  return `<dialog id="skill-detail-dialog" class="wide"><div class="dialog-head"><h2 id="skill-detail-name">Skill</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body" id="skill-detail-body"></div><div class="dialog-actions"><button type="button" id="skill-detail-edit">编辑</button><button type="button" class="primary" data-kb-close>关闭</button></div></dialog>`;
}

function productLineDialog(): string {
  return `<dialog id="product-line-dialog"><form id="product-line-form"><div class="dialog-head"><h2>Product Line</h2><button type="button" data-kb-close class="icon-button">${icon("close")}</button></div><div class="dialog-body"><label>名称<input name="name" required placeholder="Main / Enterprise"></label><label>Git Branch Pattern<input name="branch_pattern" placeholder="main / release/*"></label><label class="skill-enabled"><input name="default" type="checkbox">作为默认产品线</label></div><div class="dialog-actions"><button type="button" data-kb-close>取消</button><button class="primary" type="submit">保存</button></div></form></dialog>`;
}

export function knowledgeEntryListVisible(projects: Project[]): boolean {
  if (activeMode !== "read" || selectedLibraryID || settingsOpen) return false;
  if (activeScope === "global") return true;
  return Boolean(selectedProject(projects));
}

// Deep links keep working: #knowledge?doc=<id>&scope=global|project&project=<id>
// selects the document on first render (or on a pasted hash change) without
// touching the view-level hash routing in main.ts.
let knowledgeDeepLinkHash = "";

function currentHash(): string {
  return typeof window === "undefined" ? "" : window.location.hash.replace(/^#/, "");
}

function knowledgeDeepLink(): {docID: string; scope: "" | "global" | "project"; projectID: string} | null {
  const [name, query = ""] = currentHash().split("?");
  if (name !== "knowledge" || !query) return null;
  const params = new URLSearchParams(query);
  const docID = params.get("doc") || params.get("entry") || "";
  if (!docID) return null;
  const rawScope = params.get("scope");
  return {docID, scope: rawScope === "global" ? "global" : rawScope === "project" ? "project" : "", projectID: params.get("project") || ""};
}

function writeKnowledgeDeepLink(id: string): void {
  if (!id || id.startsWith(syntheticHomeID)) return;
  const params = new URLSearchParams({doc: id});
  if (activeScope === "global") {
    params.set("scope", "global");
  } else {
    params.set("scope", "project");
    if (selectedProjectID) params.set("project", selectedProjectID);
  }
  const next = `#knowledge?${params.toString()}`;
  if (typeof window === "undefined") return;
  if (window.location.hash !== next) window.history.replaceState(null, "", next);
}

function clearKnowledgeDeepLink(): void {
  if (typeof window === "undefined") return;
  if (window.location.hash.startsWith("#knowledge?doc=") || window.location.hash.startsWith("#knowledge?entry=")) {
    window.history.replaceState(null, "", "#knowledge");
  }
}

function applyKnowledgeDeepLink(context: KnowledgeWorkspaceContext): void {
  const raw = currentHash();
  if (knowledgeDeepLinkHash === raw) return;
  knowledgeDeepLinkHash = raw;
  const link = knowledgeDeepLink();
  if (!link) return;
  if (link.scope) activeScope = link.scope;
  if (link.projectID) selectedProjectID = link.projectID;
  activeMode = "read";
  settingsOpen = false;
  selectedKnowledgeID = link.docID;
  mobilePane = "reader";
  const known = context.knowledge.find(entry => entry.id === link.docID);
  if (known) {
    if (known.scope === "global") activeScope = "global";
    else if (known.bound_project_id || known.project_id) selectedProjectID = known.bound_project_id || known.project_id || selectedProjectID;
    return;
  }
  // Documents beyond the current page are fetched by ID instead of degrading to the home page.
  void api.knowledgeEntry(link.docID).then(response => {
    const entry = response.knowledge;
    if (!context.knowledge.some(item => item.id === entry.id)) context.knowledge.push(entry);
    activeScope = entry.scope === "global" ? "global" : "project";
    if (entry.project_id) selectedProjectID = entry.bound_project_id || entry.project_id;
    selectedKnowledgeID = entry.id;
    context.render();
  }).catch(error => {
    context.setMessage("error", error instanceof Error ? error.message : String(error));
  });
}

export function renderKnowledgeWorkspace(context: KnowledgeWorkspaceContext): string {
  applyKnowledgeDeepLink(context);
  const project = selectedProject(context.projects);
  if (!project && activeScope === "project" && !selectedKnowledgeID) activeScope = "global";
  const pendingCount = knowledgeReviewQueue(context.proposals || [], context.knowledge, context.projects).length;
  const body = activeMode === "skills"
    ? renderKnowledgeSkillsPage(context)
    : activeMode === "review"
      ? renderReviewPage(context)
      : activeMode === "edit"
        ? renderEditWorkspace(context, project)
        : renderReadWorkspace(context, project);
  return `<section class="page knowledge-page knowledge-mode-${activeMode}"><div class="knowledge-topbar-wrap">${renderTopbar(pendingCount)}</div>${body}${skillDialog(context.projects, project?.id || "")}${skillDetailDialog()}${productLineDialog()}</section>`;
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

async function mutate(context: KnowledgeWorkspaceContext, action: () => Promise<unknown>, message: string, pendingTarget?: HTMLElement | null, pendingLabel = t("knowledge.processing")): Promise<unknown | null> {
  const button = pendingTarget instanceof HTMLFormElement
    ? pendingTarget.querySelector<HTMLButtonElement>('button[type="submit"]')
    : pendingTarget instanceof HTMLButtonElement ? pendingTarget : null;
  const dialog = pendingTarget?.closest("dialog") as HTMLDialogElement | null;
  const original = button?.innerHTML || "";
  const wasDisabled = button?.disabled || false;
  if (button) {
    button.disabled = true;
    button.innerHTML = `${icon("spinner", true)}<span>${pendingLabel}</span>`;
  }
  pendingTarget?.setAttribute("aria-busy", "true");
  try {
    const result = await action();
    dialog?.close();
    context.setMessage("notice", message);
    context.render();
    catalogProjectID = "";
    await Promise.all([context.refreshData(), loadCatalog(selectedProjectID)]);
    context.render();
    return result ?? true;
  } catch (error) {
    context.setMessage("error", error instanceof Error ? error.message : String(error));
    context.render();
    return null;
  } finally {
    pendingTarget?.removeAttribute("aria-busy");
    if (button?.isConnected) {
      button.disabled = wasDisabled;
      button.innerHTML = original;
    }
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

let knowledgeFilterRenderTimer: number | null = null;

function scheduleKnowledgeFilterRender(context: KnowledgeWorkspaceContext): void {
  if (knowledgeFilterRenderTimer !== null) window.clearTimeout(knowledgeFilterRenderTimer);
  knowledgeFilterRenderTimer = window.setTimeout(() => {
    knowledgeFilterRenderTimer = null;
    context.render();
  }, 140);
}

function openSkillEditor(context: KnowledgeWorkspaceContext, item?: Skill): void {
  const form = document.querySelector<HTMLFormElement>("#skill-editor-form");
  if (!form) return;
  form.reset();
  fillForm(form, item ? (item as unknown as Record<string, unknown>) : {
    scope: "global", project_id: selectedProject(context.projects)?.id || "",
    status: "active", enabled: true, auto_attach: false,
  });
  syncSkillScope(form);
  openDialog("#skill-editor-dialog");
}

function openSkillDetail(context: KnowledgeWorkspaceContext, id: string): void {
  const item = skills.find(skill => skill.id === id);
  const dialog = document.querySelector<HTMLDialogElement>("#skill-detail-dialog");
  const body = dialog?.querySelector<HTMLElement>("#skill-detail-body");
  const name = dialog?.querySelector<HTMLElement>("#skill-detail-name");
  if (!item || !dialog || !body || !name) return;
  name.textContent = item.name;
  const updated = item.updated_at ? formatDate(item.updated_at) : t("knowledge.skill.unknown");
  body.innerHTML = `<div class="skill-detail-meta"><span class="capability-badge">${escapeHTML(skillScopeLabel(context, item))}</span>${item.auto_attach ? '<span class="capability-badge">自动启用</span>' : ""}</div><p>${escapeHTML(item.description || t("knowledge.skill.noDescription"))}</p><dl class="mcp-detail-meta"><dt>${t("knowledge.skill.version")}</dt><dd>v${item.version}</dd><dt>${t("knowledge.skill.statusLabel")}</dt><dd>${escapeHTML(item.status)} · ${item.enabled ? t("knowledge.skill.enabledState") : t("knowledge.skill.disabledState")}</dd><dt>${t("knowledge.skill.source")}</dt><dd>${escapeHTML(item.source_path || "AHA managed package")}</dd><dt>${t("knowledge.skill.updated")}</dt><dd>${escapeHTML(updated)}</dd></dl><h3>SKILL.md</h3><div class="markdown-body skill-detail-instructions">${renderMarkdown(item.instructions || "")}</div><h3>Skill Package</h3><div class="skill-package-files"><span>${(item.files || []).map(file => `<code>${escapeHTML(file)}</code>`).join("") || "<code>SKILL.md</code>"}</span></div>`;
  const edit = dialog.querySelector<HTMLButtonElement>("#skill-detail-edit");
  if (edit) edit.onclick = () => { dialog.close(); openSkillEditor(context, item); };
  dialog.showModal();
}

export const KNOWLEDGE_UNSAVED_WARNING = "当前文档有未保存的修改，离开后将丢失。确定离开吗？";

function confirmDiscardEdit(): boolean {
  if (activeMode !== "edit") return true;
  if (editState?.dirty && !window.confirm(KNOWLEDGE_UNSAVED_WARNING)) return false;
  editState = null;
  activeMode = "read";
  return true;
}

function exitEditor(): void {
  editState = null;
  activeMode = "read";
}

function beginEdit(item: Knowledge): void {
  editState = {
    id: item.id,
    isNew: false,
    scope: item.scope,
    projectID: item.project_id || "",
    parentID: item.parent_id || "",
    type: item.type || "practice",
    status: item.status || "candidate",
    title: item.title || "",
    body: item.body || "",
    slug: item.slug || "",
    sortOrder: Number(item.sort_order || 0),
    confidence: Number(item.confidence || 0.8),
    productLineID: item.product_line_id || "",
    branchScope: item.branch_scope || "",
    verifiedCommit: item.verified_commit || "",
    isIndex: Boolean(item.is_index),
    dirty: false,
  };
  selectedKnowledgeID = item.id;
  activeMode = "edit";
  mobilePane = "reader";
}

function beginNew(context: KnowledgeWorkspaceContext): void {
  const project = selectedProject(context.projects);
  const entries = activeScope === "global" ? context.knowledge.filter(item => item.scope === "global") : project ? projectEntries(context, project.id) : [];
  const ordered = sortKnowledge(entries);
  const root = ordered.find(item => item.is_index && !item.parent_id);
  const selected = ordered.find(item => item.id === selectedKnowledgeID);
  let parentID = selected?.id || root?.id || "";
  let type = "practice";
  if (activeScope === "global") {
    if (!selected || selected.id === root?.id || selected.id === globalGeneralKnowledgeID) parentID = globalGeneralKnowledgeID;
    else if (selected.id === globalAgentLessonsKnowledgeID) parentID = globalBehaviorLessonsKnowledgeID;
    if (selected?.id === globalTechnicalLessonsKnowledgeID) type = "diagnostic";
  }
  editState = {
    id: "", isNew: true, scope: activeScope, projectID: project?.id || "",
    parentID: parentID === `${syntheticHomeID}-global` ? "" : parentID,
    type, status: "candidate", title: "", body: "", slug: "", sortOrder: 0,
    confidence: 0.8, productLineID: "", branchScope: "", verifiedCommit: "", isIndex: false, dirty: false,
  };
  activeMode = "edit";
  mobilePane = "reader";
}

function markEditorDirty(): void {
  if (!editState) return;
  editState.dirty = true;
  document.querySelectorAll<HTMLElement>("[data-knowledge-unsaved]").forEach(element => element.classList.add("visible"));
}

function syncEditStateFromInput(input: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement): void {
  if (!editState) return;
  const value = input.value;
  switch (input.name) {
    case "title": editState.title = value; break;
    case "body": editState.body = value; break;
    case "slug": editState.slug = value; break;
    case "scope": editState.scope = value === "global" ? "global" : "project"; break;
    case "parent_id": editState.parentID = value; break;
    case "product_line_id": editState.productLineID = value; break;
    case "branch_scope": editState.branchScope = value; break;
    case "verified_commit": editState.verifiedCommit = value; break;
    case "sort_order": editState.sortOrder = Number(value || 0); break;
    case "confidence": editState.confidence = Number(value || 0); break;
    default: break;
  }
}

async function startEdit(context: KnowledgeWorkspaceContext, id: string): Promise<void> {
  let item = context.knowledge.find(entry => entry.id === id);
  if (!item) return;
  try {
    item = await ensureKnowledgeDetail(context, id);
  } catch (error) {
    context.setMessage("error", error instanceof Error ? error.message : String(error));
    return;
  }
  if (!item) return;
  beginEdit(item);
  context.render();
}

export function bindKnowledgeWorkspace(context: KnowledgeWorkspaceContext): void {
  const project = selectedProject(context.projects);
  ensureCatalog(context, project?.id || "");

  document.querySelectorAll<HTMLElement>("[data-knowledge-scope]").forEach(button => button.addEventListener("click", () => {
    if (!confirmDiscardEdit()) return;
    activeScope = button.dataset.knowledgeScope === "global" ? "global" : "project";
    activeMode = "read";
    settingsOpen = false;
    selectedKnowledgeID = "";
    selectedLibraryID = "";
    mobilePane = "directory";
    knowledgeSearch = "";
    clearKnowledgeDeepLink();
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-mode]").forEach(button => button.addEventListener("click", () => {
    if (!confirmDiscardEdit()) return;
    activeMode = (button.dataset.knowledgeMode || "read") as KnowledgeMode;
    settingsOpen = false;
    mobilePane = activeMode === "read" ? mobilePane : "directory";
    if (activeMode !== "read") clearKnowledgeDeepLink();
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-project-settings]").forEach(button => button.addEventListener("click", () => {
    if (!confirmDiscardEdit()) return;
    activeScope = "project";
    activeMode = "read";
    settingsOpen = true;
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-settings-close]").forEach(button => button.addEventListener("click", () => {
    settingsOpen = false;
    context.render();
  }));
  document.querySelector<HTMLSelectElement>("[data-knowledge-review-scope]")?.addEventListener("change", event => {
    const value = event.currentTarget.value || "all";
    reviewScopeFilter = value === "global" || value.startsWith("project:") ? value as ReviewScopeFilter : "all";
    reviewIndex = 0;
    context.render();
  });
  document.querySelector<HTMLSelectElement>("#knowledge-project-select")?.addEventListener("change", event => {
    if (!confirmDiscardEdit()) {
      // The render that follows restores control values by region key, so a
      // rejected change would otherwise survive every later render and leave
      // the select showing a project the module never switched to.
      event.currentTarget.value = selectedProjectID;
      context.render();
      return;
    }
    selectedProjectID = event.currentTarget.value || "";
    selectedKnowledgeSourceID = "all";
    selectedKnowledgeID = "";
    selectedLibraryID = "";
    mobilePane = "directory";
    productLines = [];
    catalogProjectID = "";
    clearKnowledgeDeepLink();
    context.render();
  });
  document.querySelector<HTMLInputElement>("#knowledge-search")?.addEventListener("input", event => {
    knowledgeSearch = (event.currentTarget as HTMLInputElement).value;
    scheduleKnowledgeFilterRender(context);
  });
  document.querySelector<HTMLSelectElement>("[data-knowledge-source-filter]")?.addEventListener("change", event => {
    if (!confirmDiscardEdit()) {
      event.currentTarget.value = selectedKnowledgeSourceID;
      context.render();
      return;
    }
    selectedKnowledgeSourceID = event.currentTarget.value || "all";
    selectedKnowledgeID = "";
    clearKnowledgeDeepLink();
    context.render();
  });
  document.querySelectorAll<HTMLElement>("[data-knowledge-group-toggle]").forEach(button => button.addEventListener("click", () => {
    const key = button.dataset.knowledgeGroupToggle || "";
    if (collapsedGroupKeys.has(key)) collapsedGroupKeys.delete(key);
    else collapsedGroupKeys.add(key);
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-tree-toggle]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeTreeToggle || "";
    if (collapsedTreeIDs.has(id)) collapsedTreeIDs.delete(id);
    else collapsedTreeIDs.add(id);
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-directory-collapse]").forEach(button => button.addEventListener("click", () => {
    directoryCollapsed = true;
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-directory-expand]").forEach(button => button.addEventListener("click", () => {
    directoryCollapsed = false;
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-mobile-back]").forEach(button => button.addEventListener("click", () => {
    mobilePane = "directory";
    selectedKnowledgeID = "";
    clearKnowledgeDeepLink();
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-open]").forEach(button => button.addEventListener("click", async () => {
    if (!confirmDiscardEdit()) return;
    selectedKnowledgeID = button.dataset.knowledgeOpen || "";
    mobilePane = "reader";
    writeKnowledgeDeepLink(selectedKnowledgeID);
    try {
      await ensureKnowledgeDetail(context, selectedKnowledgeID);
    } catch (error) {
      context.setMessage("error", error instanceof Error ? error.message : String(error));
    }
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-edit]").forEach(button => button.addEventListener("click", () => {
    void startEdit(context, button.dataset.knowledgeEdit || "");
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-new]").forEach(button => button.addEventListener("click", () => {
    if (!confirmDiscardEdit()) return;
    beginNew(context);
    context.render();
  }));
  document.querySelector<HTMLFormElement>("#knowledge-inline-editor")?.addEventListener("input", event => {
    const target = event.target as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | null;
    if (!target?.name) return;
    syncEditStateFromInput(target);
    if (target.name === "scope") {
      const form = target.form;
      if (form) syncKnowledgeScope(form);
    }
    markEditorDirty();
  });
  document.querySelector<HTMLFormElement>("#knowledge-inline-editor")?.addEventListener("submit", event => {
    event.preventDefault();
    if (!editState) return;
    const form = event.currentTarget;
    const payload = Object.fromEntries(new FormData(form).entries()) as Record<string, unknown>;
    const id = editState.id;
    payload.confidence = Number(payload.confidence || 0);
    payload.sort_order = Number(payload.sort_order || 0);
    payload.is_index = editState.isIndex;
    if (editState.isIndex) {
      payload.scope = editState.scope;
      payload.project_id = editState.projectID;
      payload.parent_id = "";
      payload.status = "verified";
      payload.product_line_id = "";
    }
    if (!String(payload.slug || "").trim()) delete payload.slug;
    if (payload.scope === "global") {
      payload.project_id = "";
      payload.product_line_id = "";
      payload.branch_scope = "";
    }
    if (payload.scope === "project" && !String(payload.project_id || "").trim()) payload.project_id = editState.projectID || project?.id || "";
    delete payload.bound_project_id;
    void mutate(context, () => id ? api.updateKnowledge(id, payload) : api.createKnowledge(payload), id ? t("knowledge.edit.updated") : t("knowledge.edit.created"), form, t("knowledge.edit.saving")).then(result => {
      if (!result) return;
      const created = result as {knowledge?: Knowledge};
      editState = null;
      activeMode = "read";
      mobilePane = "reader";
      selectedKnowledgeID = id || created.knowledge?.id || "";
      writeKnowledgeDeepLink(selectedKnowledgeID);
      context.render();
    });
  });
  document.querySelectorAll<HTMLElement>("[data-knowledge-edit-cancel]").forEach(button => button.addEventListener("click", () => {
    if (editState?.dirty && !window.confirm(t("knowledge.edit.discardShort"))) return;
    exitEditor();
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm(t("knowledge.edit.deleteConfirm"))) return;
    const id = button.dataset.knowledgeDelete || "";
    void mutate(context, () => api.deleteKnowledge(id), t("knowledge.edit.deleted")).then(result => {
      if (!result) return;
      const wasEdit = activeMode === "edit";
      if (editState?.id === id) editState = null;
      if (wasEdit) activeMode = "read";
      const deletedSelected = selectedKnowledgeID === id;
      if (deletedSelected) {
        selectedKnowledgeID = "";
        clearKnowledgeDeepLink();
      }
      if (wasEdit) mobilePane = "directory";
      reviewIndex = Math.max(0, reviewIndex - 1);
      context.render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-verify]").forEach(button => button.addEventListener("click", () => {
    void mutate(context, () => api.verifyKnowledge(button.dataset.knowledgeVerify || ""), t("knowledge.verified")).then(() => context.render());
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-feedback]").forEach(button => button.addEventListener("click", () => {
    void mutate(context, () => api.feedbackKnowledge(button.dataset.knowledgeId || "", button.dataset.knowledgeFeedback as "helped" | "stale" | "wrong"), t("knowledge.feedbackSaved"));
  }));

  document.querySelectorAll<HTMLElement>("[data-knowledge-review-prev]").forEach(button => button.addEventListener("click", () => {
    reviewIndex = Math.max(0, reviewIndex - 1);
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-review-next]").forEach(button => button.addEventListener("click", () => {
    reviewIndex += 1;
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-proposal-approve]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeProposalApprove || "";
    void mutate(context, () => api.approveKnowledgeProposal(id), t("knowledge.review.approved")).then(result => {
      if (result) reviewIndex = Math.max(0, reviewIndex);
      context.render();
    });
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-proposal-reject]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm(t("knowledge.review.rejectConfirm"))) return;
    const id = button.dataset.knowledgeProposalReject || "";
    void mutate(context, () => api.rejectKnowledgeProposal(id), t("knowledge.review.rejected")).then(result => {
      if (result) reviewIndex = Math.max(0, reviewIndex);
      context.render();
    });
  }));
  document.querySelector<HTMLInputElement>("[data-knowledge-auto-review]")?.addEventListener("change", event => {
    const input = event.currentTarget;
    const enabled = input.checked;
    if (enabled && !window.confirm(t("knowledge.review.autoConfirm"))) {
      input.checked = false;
      return;
    }
    void mutate(context, async () => {
      const response = await api.updateKnowledgeReviewSettings(enabled);
      context.reviewSettings = response.review_settings;
    }, enabled ? t("knowledge.review.autoEnabled") : t("knowledge.review.autoDisabled"));
  });

  document.querySelectorAll<HTMLElement>("[data-skill-view]").forEach(button => button.addEventListener("click", () => openSkillDetail(context, button.dataset.skillView || "")));
  document.querySelector<HTMLElement>("[data-skill-new]")?.addEventListener("click", () => openSkillEditor(context));
  document.querySelectorAll<HTMLElement>("[data-skill-edit]").forEach(button => button.addEventListener("click", () => {
    const item = skills.find(skill => skill.id === button.dataset.skillEdit);
    if (item) openSkillEditor(context, item);
  }));
  document.querySelector<HTMLElement>("#knowledge-skill-search")?.addEventListener("input", event => {
    skillSearch = (event.currentTarget as HTMLInputElement).value;
    skillLimit = SKILL_BATCH_SIZE;
    scheduleKnowledgeFilterRender(context);
  });
  document.querySelector<HTMLSelectElement>("#knowledge-skill-scope")?.addEventListener("change", event => {
    skillScopeFilter = ((event.currentTarget as HTMLSelectElement).value || "all") as typeof skillScopeFilter;
    skillLimit = SKILL_BATCH_SIZE;
    context.render();
  });
  document.querySelector<HTMLSelectElement>("#knowledge-skill-project")?.addEventListener("change", event => {
    skillProjectFilter = (event.currentTarget as HTMLSelectElement).value || "";
    skillLimit = SKILL_BATCH_SIZE;
    context.render();
  });
  document.querySelector<HTMLSelectElement>("#knowledge-skill-status")?.addEventListener("change", event => {
    skillStatusFilter = (event.currentTarget as HTMLSelectElement).value || "all";
    skillLimit = SKILL_BATCH_SIZE;
    context.render();
  });
  document.querySelectorAll<HTMLElement>("[data-knowledge-skill-more]").forEach(button => button.addEventListener("click", () => {
    skillLimit += SKILL_BATCH_SIZE;
    context.render();
  }));
  document.querySelector<HTMLSelectElement>("#skill-editor-form [name=scope]")?.addEventListener("change", event => syncSkillScope(event.currentTarget.form!));
  document.querySelector<HTMLFormElement>("#skill-editor-form")?.addEventListener("submit", event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const payload = Object.fromEntries(form.entries()) as Record<string, unknown>;
    const id = String(payload.id || "");
    delete payload.id;
    payload.enabled = form.get("enabled") === "on";
    payload.auto_attach = form.get("auto_attach") === "on";
    if (payload.scope === "global") payload.project_id = "";
    void mutate(context, () => id ? api.updateSkill(id, payload) : api.createSkill(payload), id ? t("knowledge.skill.updatedMessage") : t("knowledge.skill.createdMessage"), event.currentTarget, t("knowledge.edit.saving"));
  });
  document.querySelectorAll<HTMLInputElement>("[data-skill-toggle]").forEach(input => input.addEventListener("change", () => {
    const item = skills.find(skill => skill.id === input.dataset.skillToggle);
    if (!item) return;
    void mutate(context, () => api.updateSkill(item.id, {...item, enabled: input.checked}), input.checked ? t("knowledge.skill.enabledMessage") : t("knowledge.skill.disabledMessage"));
  }));
  document.querySelectorAll<HTMLElement>("[data-skill-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm(t("knowledge.skill.deleteConfirm"))) return;
    void mutate(context, () => api.deleteSkill(button.dataset.skillDelete || ""), t("knowledge.skill.deleted"));
  }));

  document.querySelectorAll<HTMLElement>("[data-knowledge-library-bind]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeLibraryBind || "";
    const select = document.querySelector<HTMLSelectElement>(`[data-knowledge-library-target="${CSS.escape(id)}"]`);
    const modeSelect = document.querySelector<HTMLSelectElement>(`[data-knowledge-library-mode="${CSS.escape(id)}"]`);
    const projectID = select?.value || "";
    if (!projectID) {
      context.setMessage("error", t("knowledge.manage.bindError"));
      context.render();
      return;
    }
    const bindingMode = modeSelect?.value === "project" ? "project" : "external";
    void mutate(context, () => api.bindKnowledgeLibrary(id, projectID, bindingMode), t("knowledge.manage.boundMessage"), button, t("knowledge.manage.binding"));
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-library-mode-save]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeLibraryModeSave || "";
    const library = context.libraries.find(item => item.id === id);
    const select = document.querySelector<HTMLSelectElement>(`[data-knowledge-library-mode="${CSS.escape(id)}"]`);
    if (!library?.bound_project_id || !select) return;
    const bindingMode = select.value === "project" ? "project" : "external";
    void mutate(context, () => api.bindKnowledgeLibrary(id, library.bound_project_id || "", bindingMode), t("knowledge.manage.modeSaved"), button, t("knowledge.edit.saving"));
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-library-open]").forEach(button => button.addEventListener("click", () => {
    if (!confirmDiscardEdit()) return;
    selectedLibraryID = button.dataset.knowledgeLibraryOpen || "";
    selectedKnowledgeID = "";
    activeMode = "read";
    settingsOpen = false;
    mobilePane = "reader";
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-library-back]").forEach(button => button.addEventListener("click", () => {
    selectedLibraryID = "";
    selectedKnowledgeID = "";
    activeMode = "read";
    settingsOpen = true;
    mobilePane = "directory";
    clearKnowledgeDeepLink();
    context.render();
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-library-unbind]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm(t("knowledge.manage.unbindConfirm"))) return;
    void mutate(context, () => api.unbindKnowledgeLibrary(button.dataset.knowledgeLibraryUnbind || ""), t("knowledge.manage.unboundMessage"));
  }));
  document.querySelectorAll<HTMLElement>("[data-knowledge-library-delete]").forEach(button => button.addEventListener("click", () => {
    const id = button.dataset.knowledgeLibraryDelete || "";
    const library = context.libraries.find(item => item.id === id);
    if (!library) return;
    if (!window.confirm(t("knowledge.manage.deleteConfirm", {name: library.name, knowledge: library.knowledge_count, skills: library.skill_count}))) return;
    selectedLibraryID = "";
    void mutate(context, () => api.deleteKnowledgeLibrary(id), t("knowledge.manage.deletedMessage"));
  }));

  document.querySelector("#knowledge-refresh")?.addEventListener("click", () => void mutate(context, async () => {}, t("knowledge.manage.refreshed")));
  document.querySelectorAll<HTMLElement>("[data-kb-close]").forEach(button => button.addEventListener("click", () => button.closest("dialog")?.close()));

  if (!project) return;
  document.querySelector("[data-project-knowledge-detach]")?.addEventListener("click", () => {
    if (!window.confirm(t("knowledge.manage.detachConfirm"))) return;
    void mutate(context, () => api.detachProjectKnowledge(project.id), t("knowledge.manage.detachedMessage"));
  });
  document.querySelector("[data-project-knowledge-delete]")?.addEventListener("click", () => {
    const directKnowledge = context.knowledge.filter(item => item.scope === "project" && item.project_id === project.id && !item.is_index).length;
    const directSkills = skills.filter(item => item.scope === "project" && item.project_id === project.id).length;
    if (!window.confirm(t("knowledge.manage.deleteOwnConfirm", {name: project.name, knowledge: directKnowledge, skills: directSkills}))) return;
    void mutate(context, () => api.deleteProjectKnowledge(project.id), t("knowledge.manage.deleteOwnMessage"));
  });
  document.querySelector("[data-project-kb-toggle]")?.addEventListener("click", () => void mutate(context, () => api.updateProject(project.id, {
    name: project.name,
    description: project.description || "",
    project_type: project.project_type || "folder",
    repository_identity: project.repository_identity || "",
    default_branch: project.default_branch || "",
    knowledge_policy: project.knowledge_policy === "disabled" ? "enabled" : "disabled",
  }), project.knowledge_policy === "disabled" ? t("knowledge.manage.policyOn") : t("knowledge.manage.policyOff")));
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
    }), t("knowledge.manage.productLineCreated"));
  });
  document.querySelectorAll<HTMLElement>("[data-product-line-delete]").forEach(button => button.addEventListener("click", () => {
    if (!window.confirm(t("knowledge.manage.productLineDeleteConfirm"))) return;
    void mutate(context, () => api.deleteProductLine(project.id, button.dataset.productLineDelete || ""), t("knowledge.manage.productLineDeleted"));
  }));
}