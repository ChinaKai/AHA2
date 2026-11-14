import {api} from "./api.js";
import {t, currentLocale, formatLocaleDateTime} from "./i18n.js";
import {icon} from "./icons.js";
import {renderMarkdown} from "./markdown.js";
import type {
  KnowledgeVaultDoc,
  KnowledgeVaultGitResponse,
  KnowledgeVaultGitStatus,
  KnowledgeVaultInbox,
  KnowledgeVaultLibrary,
  KnowledgeVaultLibraries,
  KnowledgeVaultOutlineItem,
  KnowledgeVaultRecent,
  KnowledgeVaultRecentItem,
  KnowledgeVaultSearch,
  KnowledgeVaultSearchResult,
  KnowledgeVaultSettings,
  KnowledgeVaultSkill,
  KnowledgeVaultSkills,
  KnowledgeVaultTreeNode,
  KnowledgeVaultTree,
} from "./types.js";

type VaultTab = "knowledge" | "recent" | "inbox" | "skills";
type MobilePane = "list" | "doc";
type LoadState = "idle" | "loading" | "ready" | "error";
type SearchScope = "all" | "project" | "directory";
type GitBusy = "" | "init" | "remote" | "commit" | "pull" | "push" | "sync";

// Settings and Git labels live in this lazy route chunk rather than the
// first-screen i18n catalog: they are only needed once the knowledge page has
// loaded, and keeping them here preserves the first-screen size budget.
const localText: Record<string, [zhCN: string, enUS: string]> = {
  "kv.settingsIndexAction": ["索引操作", "Index action"],
  "kv.settingsRebuild": ["重建索引", "Rebuild index"],
  "kv.settingsRebuilding": ["重建中…", "Rebuilding…"],
  "kv.settingsRebuildDone": ["已重建 {count} 条 · {time}", "Rebuilt {count} entries · {time}"],
  "kv.settingsRebuildFailed": ["重建索引失败", "Index rebuild failed"],
  "kv.gitTitle": ["Git 同步", "Git sync"],
  "kv.gitRepo": ["仓库", "Repository"],
  "kv.gitUninitialized": ["未初始化", "Not initialized"],
  "kv.gitInitialized": ["已初始化", "Initialized"],
  "kv.gitBranch": ["分支", "Branch"],
  "kv.gitRemote": ["远程", "Remote"],
  "kv.gitRemoteUnset": ["未设置", "Not set"],
  "kv.gitWorktree": ["工作区", "Worktree"],
  "kv.gitClean": ["干净", "Clean"],
  "kv.gitDirty": ["有 {count} 个未提交修改", "{count} uncommitted changes"],
  "kv.gitSyncState": ["同步状态", "Sync state"],
  "kv.gitInSync": ["已同步", "In sync"],
  "kv.gitAhead": ["领先 {count}", "{count} ahead"],
  "kv.gitBehind": ["落后 {count}", "{count} behind"],
  "kv.gitNoTracking": ["尚未建立远程跟踪", "No remote tracking branch yet"],
  "kv.gitLastCommit": ["最近提交", "Last commit"],
  "kv.gitLastSync": ["最近同步", "Last sync"],
  "kv.gitNone": ["无", "None"],
  "kv.gitRemoteInput": ["SSH remote", "SSH remote"],
  "kv.gitRemotePlaceholder": ["git@host:path 或 ssh://user@host/path", "git@host:path or ssh://user@host/path"],
  "kv.gitBranchInput": ["分支", "Branch"],
  "kv.gitSaveRemote": ["保存设置", "Save settings"],
  "kv.gitInit": ["初始化仓库", "Initialize repository"],
  "kv.gitCommit": ["提交", "Commit"],
  "kv.gitCommitMessage": ["提交说明（可选）", "Commit message (optional)"],
  "kv.gitCommitPlaceholder": ["留空使用默认说明", "Leave blank for the default message"],
  "kv.gitPull": ["拉取", "Pull"],
  "kv.gitPush": ["推送", "Push"],
  "kv.gitSync": ["同步", "Sync"],
  "kv.gitInitializing": ["初始化中…", "Initializing…"],
  "kv.gitSaving": ["保存中…", "Saving…"],
  "kv.gitCommitting": ["提交中…", "Committing…"],
  "kv.gitPulling": ["拉取中…", "Pulling…"],
  "kv.gitPushing": ["推送中…", "Pushing…"],
  "kv.gitSyncing": ["同步中…", "Syncing…"],
  "kv.gitUnavailable": ["当前环境没有可用的 git 命令，Git 管理已禁用。", "Git is not available in this environment; Git management is disabled."],
  "kv.gitLoadFailed": ["读取 Git 状态失败", "Failed to read Git status"],
  "kv.gitHint": ["仅支持 SSH remote，使用系统 SSH agent 或默认密钥；Web 不保存密码或 token。所有同步均为手动操作，拉取只做 fast-forward，分叉时停止而不会合并或强推。", "SSH remotes only, using your system SSH agent or default key; the Web UI never stores passwords or tokens. Every sync is manual, pulls are fast-forward only, and divergence stops instead of merging or force pushing."],
};

function kt(key: string, values: Record<string, string | number> = {}): string {
  const [zh, en] = localText[key] ?? ["", ""];
  const template = (currentLocale() === "en-US" ? en : zh) || t(key);
  return template.replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`));
}

// Git is only rendered by this lazy route, so its layout CSS rides in the same
// chunk instead of growing the first-screen stylesheet.
const GIT_STYLES = `
.kv-settings-notice { margin: var(--space-2) 0 0; font-size: var(--font-size-12); line-height: 1.5; overflow-wrap: anywhere; }
.kv-settings-notice.error { color: var(--color-danger); }
.kv-settings-section { margin-top: var(--space-4); padding-top: var(--space-3); border-top: 1px solid var(--color-border); min-width: 0; }
.kv-settings-section h3 { margin: 0; font-size: var(--font-size-14); }
.kv-git-form { display: grid; grid-template-columns: minmax(0, 1fr) minmax(120px, 0.55fr) auto; gap: var(--space-2); align-items: end; min-width: 0; }
.kv-git-form label, .kv-git-commit-row label { display: grid; gap: 4px; min-width: 0; font-size: var(--font-size-12); color: var(--color-text-muted); }
.kv-git-commit-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--space-2); align-items: end; margin-top: var(--space-2); min-width: 0; }
.kv-git-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: var(--space-2); }
@media (max-width: 760px) {
  .kv-git-form, .kv-git-commit-row { grid-template-columns: minmax(0, 1fr); }
  .kv-git-form button, .kv-git-commit-row button { justify-self: start; }
}
`;

function installGitStyles(): void {
  if (document.getElementById("kv-git-styles")) return;
  const style = document.createElement("style");
  style.id = "kv-git-styles";
  style.textContent = GIT_STYLES;
  document.head.append(style);
}

export interface KnowledgeVaultContext {
  render: () => void;
  setMessage: (kind: "notice" | "error", message: string) => void;
}

interface VaultState {
  initialized: boolean;
  loading: LoadState;
  error: string;
  libraries: KnowledgeVaultLibrary[];
  projects: KnowledgeVaultLibrary[];
  vaultReady: boolean;
  indexReady: boolean;
  vaultName: string;
  vaultPath: string;
  obsidianVault: string;
  obsidianReady: boolean;
  rebuiltAt: string;
  warning: string;
  tab: VaultTab;
  libraryID: string;
  typeFilter: string;
  statusFilter: string;
  tagFilter: string;
  tree: KnowledgeVaultTreeNode[];
  treeState: LoadState;
  treeError: string;
  treeKey: string;
  recentKey: string;
  inboxKey: string;
  expanded: Set<string>;
  selectedID: string;
  doc: KnowledgeVaultDoc | null;
  docState: LoadState;
  docError: string;
  recent: KnowledgeVaultRecentItem[];
  recentState: LoadState;
  recentError: string;
  inbox: KnowledgeVaultInbox | null;
  inboxState: LoadState;
  inboxError: string;
  skills: KnowledgeVaultSkill[];
  skillsState: LoadState;
  skillsError: string;
  searchOpen: boolean;
  searchQuery: string;
  searchScope: SearchScope;
  searchType: string;
  searchStatus: string;
  searchTag: string;
  searchState: LoadState;
  searchError: string;
  searchResults: KnowledgeVaultSearchResult[];
  feedback: "" | "helped" | "stale";
  contextOpen: boolean;
  settingsOpen: boolean;
  settingsState: LoadState;
  settings: KnowledgeVaultSettings | null;
  settingsError: string;
  rebuildBusy: boolean;
  rebuildNotice: {kind: "notice" | "error"; message: string} | null;
  gitState: LoadState;
  git: KnowledgeVaultGitStatus | null;
  gitError: string;
  gitBusy: GitBusy;
  gitNotice: {kind: "notice" | "error"; message: string} | null;
  gitRemoteDraft: string;
  gitBranchDraft: string;
  gitCommitDraft: string;
  mobilePane: MobilePane;
  listScroll: number;
  docScroll: number;
  pendingSearch: number;
}

function initialState(): VaultState {
  return {
    initialized: false, loading: "idle", error: "",
    libraries: [], projects: [], vaultReady: false, indexReady: false,
    vaultName: "", vaultPath: "", obsidianVault: "", obsidianReady: false,
    rebuiltAt: "", warning: "",
    tab: "knowledge", libraryID: "global",
    typeFilter: "", statusFilter: "", tagFilter: "",
    tree: [], treeState: "idle", treeError: "", treeKey: "", expanded: new Set(),
    recentKey: "", inboxKey: "",
    selectedID: "", doc: null, docState: "idle", docError: "",
    recent: [], recentState: "idle", recentError: "",
    inbox: null, inboxState: "idle", inboxError: "",
    skills: [], skillsState: "idle", skillsError: "",
    searchOpen: false, searchQuery: "", searchScope: "all",
    searchType: "", searchStatus: "", searchTag: "",
    searchState: "idle", searchError: "", searchResults: [],
    feedback: "", contextOpen: false,
    settingsOpen: false, settingsState: "idle", settings: null, settingsError: "",
    rebuildBusy: false, rebuildNotice: null,
    gitState: "idle", git: null, gitError: "", gitBusy: "", gitNotice: null,
    gitRemoteDraft: "", gitBranchDraft: "", gitCommitDraft: "",
    mobilePane: "list", listScroll: 0, docScroll: 0, pendingSearch: 0,
  };
}

let state = initialState();
let binding = false;
let keyboardBound = false;
let hashBound = false;
let searchTimer: ReturnType<typeof setTimeout> | null = null;
let treeRequest = 0;
let recentRequest = 0;
let inboxRequest = 0;
const pendingLoads = new Set<string>();
// One tree covers every library: its roots are the vault's real top-level
// directories (global/ and projects/), so the page no longer has a separate
// library/project scope selector.
const VAULT_TREE_LIBRARY = "all";

export function __setKnowledgeVaultStateForTest(patch: Partial<VaultState>): void {
  state = {...state, ...patch};
}

export function __resetKnowledgeVaultStateForTest(): void {
  state = initialState();
  binding = false;
}

// The Phase 3 settings panel calls this after a switch, init, or rebuild so the
// page cannot render libraries/tree/doc data fetched under the previous switch
// generation. Global keyboard/hash listeners stay bound; only the reload guard
// is cleared so the next visit fetches fresh data.
export function resetKnowledgeVaultPage(): void {
  state = initialState();
  binding = false;
  pendingLoads.clear();
  if (searchTimer !== null) {
    clearTimeout(searchTimer);
    searchTimer = null;
  }
  treeRequest++;
  recentRequest++;
  inboxRequest++;
}

/* Pure URI builders ------------------------------------------------------ */

function encodeURIPath(value: string): string {
  const normalized = String(value ?? "").replaceAll("\\", "/");
  const drive = normalized.match(/^([A-Za-z]):(.*)$/);
  if (drive) {
    return `${drive[1]}:${drive[2].split("/").map(encodeURIComponent).join("/")}`;
  }
  return normalized.split("/").map(encodeURIComponent).join("/");
}

export function vscodeLocalURI(absolutePath: string): string {
  const normalized = String(absolutePath ?? "").replaceAll("\\", "/").replace(/^\/+/, "");
  return `vscode://file/${encodeURIPath(normalized)}`;
}

export function obsidianURI(vaultName: string, relativePath: string): string {
  const file = String(relativePath ?? "").replaceAll("\\", "/").replace(/\.md$/i, "");
  return `obsidian://open?vault=${encodeURIComponent(vaultName)}&file=${encodeURIComponent(file)}`;
}

export function vaultQuery(params: Record<string, string | number | undefined | null>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    query.set(key, String(value));
  }
  return query.toString();
}

export function docHash(doc: {id: string; scope?: string; project_id?: string}): string {
  const scope = doc.scope === "global" || doc.scope === "project" || doc.scope === "archive" ? doc.scope : "";
  return `#knowledge?${vaultQuery({doc: doc.id, scope, project: doc.project_id || ""})}`;
}

export function parseDocHash(hash: string): {id: string; library: string; project: string} | null {
  const raw = String(hash ?? "").replace(/^#/, "");
  const [name, query = ""] = raw.split("?");
  if (name !== "knowledge") return null;
  const params = new URLSearchParams(query);
  const id = params.get("doc") || "";
  const scope = params.get("scope") || params.get("library") || "";
  const project = params.get("project") || "";
  let library = "";
  if (scope === "global") library = "global";
  else if (scope === "project" && project) library = `project:${project}`;
  else if (scope === "archive" && project) library = `archive:${project}`;
  else if (scope) library = scope;
  return {id, library, project};
}

export function escapeVaultHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

export function highlightVaultText(value: unknown, query: string): string {
  const text = String(value ?? "");
  const needle = String(query ?? "").trim();
  if (!needle) return escapeVaultHTML(text);
  const index = text.toLowerCase().indexOf(needle.toLowerCase());
  if (index < 0) return escapeVaultHTML(text);
  return `${escapeVaultHTML(text.slice(0, index))}<mark>${escapeVaultHTML(text.slice(index, index + needle.length))}</mark>${escapeVaultHTML(text.slice(index + needle.length))}`;
}

function localDayKey(value: string | number | Date): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

export function groupRecentByDay(items: KnowledgeVaultRecentItem[], now: Date = new Date()): Array<{key: string; label: string; items: KnowledgeVaultRecentItem[]}> {
  const today = localDayKey(now);
  const yesterday = localDayKey(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1));
  const groups = new Map<string, KnowledgeVaultRecentItem[]>();
  for (const item of items) {
    const key = localDayKey(item.modified_at || item.updated || "");
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(item);
  }
  return [...groups.entries()].map(([key, groupItems]) => ({
    key,
    label: key === today ? t("kv.today") : key === yesterday ? t("kv.yesterday") : (key ? formatLocaleDateTime(`${key}T00:00:00`, {year: "numeric", month: "short", day: "numeric"}) : t("kv.unknownDate")),
    items: groupItems,
  }));
}

/* Small view helpers ----------------------------------------------------- */

function typeLabel(type: string): string {
  const key = `kv.type.${type}`;
  const label = t(key);
  return label === key ? type : label;
}

function statusLabel(status: string): string {
  const key = `kv.status.${status}`;
  const label = t(key);
  return label === key ? status : label;
}

function safeDateTime(value?: string): string {
  if (!value) return t("kv.never");
  return formatLocaleDateTime(value, {year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit"});
}

export function vaultEmptyState(kind: "loading" | "error" | "empty", message = "", retry = false): string {
  const title = kind === "loading" ? t("kv.loading") : kind === "error" ? t("kv.error") : t("kv.empty");
  const body = message || (kind === "empty" ? t("kv.emptyHint") : "");
  return `<div class="kv-state kv-state-${kind}" role="status" aria-live="polite">
    <strong>${escapeVaultHTML(title)}</strong>
    ${body ? `<p>${escapeVaultHTML(body)}</p>` : ""}
    ${retry ? `<button type="button" class="secondary" data-kv-retry>${t("kv.retry")}</button>` : ""}
  </div>`;
}

function badge(text: string, kind = ""): string {
  return `<span class="kv-badge${kind ? ` kv-badge-${kind}` : ""}">${escapeVaultHTML(text)}</span>`;
}

function metaBadges(item: {type: string; status: string}): string {
  return `${badge(typeLabel(item.type), "type")}${badge(statusLabel(item.status), item.status === "stale" || item.status === "pending" ? "warn" : "status")}`;
}

function openActionLabel(base: string, context = ""): string {
  return context ? t("kv.openActionContext", {action: base, name: context}) : base;
}

function openActions(absolutePath: string, relativePath: string, obsidianFile = "", options: {compact?: boolean; context?: string; copyKey?: string} = {}): string {
  const local = vscodeLocalURI(absolutePath);
  const obsidian = state.obsidianVault ? obsidianURI(state.obsidianVault, obsidianFile || relativePath) : "";
  const obsidianEnabled = Boolean(obsidian) && state.obsidianReady;
  const vscodeLabel = openActionLabel(t("kv.openVSCodeLocal"), options.context);
  const obsidianLabel = openActionLabel(t("kv.openObsidian"), options.context);
  const copyLabel = openActionLabel(t(options.copyKey || "kv.copyPath"), options.context);
  const obsidianHint = `${obsidianLabel}：${t("kv.obsidianUnregistered")}`;
  return `<div class="kv-open-actions${options.compact ? " compact" : ""}">
    <a class="kv-open" href="${escapeVaultHTML(local)}" target="_blank" rel="noopener" title="${escapeVaultHTML(vscodeLabel)}" aria-label="${escapeVaultHTML(vscodeLabel)}">${icon("monitor")}</a>
    ${obsidianEnabled
      ? `<a class="kv-open" href="${escapeVaultHTML(obsidian)}" target="_blank" rel="noopener" title="${escapeVaultHTML(obsidianLabel)}" aria-label="${escapeVaultHTML(obsidianLabel)}">${icon("branch")}</a>`
      : `<button type="button" class="kv-open" disabled title="${escapeVaultHTML(obsidianHint)}" aria-label="${escapeVaultHTML(obsidianHint)}">${icon("branch")}</button>`}
    <button type="button" class="kv-open kv-copy" data-kv-copy="${escapeVaultHTML(absolutePath)}" title="${escapeVaultHTML(copyLabel)}" aria-label="${escapeVaultHTML(copyLabel)}">${icon("copy")}</button>
  </div>`;
}

/* Directory tree --------------------------------------------------------- */

const VAULT_INDEX_FILE = "index.md";

function normalizeTreePath(value: unknown): string {
  return String(value ?? "").replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
}

function treeBaseName(value: string): string {
  const normalized = normalizeTreePath(value);
  const separator = normalized.lastIndexOf("/");
  return separator < 0 ? normalized : normalized.slice(separator + 1);
}

function treeDirectoryPath(value: string): string {
  const normalized = normalizeTreePath(value);
  const separator = normalized.lastIndexOf("/");
  return separator < 0 ? "" : normalized.slice(0, separator);
}

export function isVaultIndexPath(value: string): boolean {
  return treeBaseName(value).toLowerCase() === VAULT_INDEX_FILE;
}

function treeScopeRoot(value: string): string {
  const segments = normalizeTreePath(value).split("/").filter(Boolean);
  if (segments[0] === "global") return "global";
  if ((segments[0] === "projects" || segments[0] === "archive" || segments[0] === "orphans") && segments.length >= 2) {
    return `${segments[0]}/${segments[1]}`;
  }
  return segments.length >= 2 ? segments[0] : "";
}

function cloneVaultDocNode(node: KnowledgeVaultTreeNode): KnowledgeVaultTreeNode {
  return {
    id: node.id,
    title: node.title,
    path: normalizeTreePath(node.path),
    type: node.type,
    status: node.status,
    scope: node.scope,
    project_id: node.project_id,
    updated: node.updated,
    is_index: isVaultIndexPath(node.path),
    system: false,
    children: [],
  };
}

// The server tree already includes every index document and leaf, but it uses
// the index document's title as the folder label and nests by parent/index
// links. Flattening by path lets the UI rebuild the real vault directories:
// directory names always come from the path basename.
function collectVaultDocuments(nodes: KnowledgeVaultTreeNode[], documents: Map<string, KnowledgeVaultTreeNode>): void {
  for (const node of nodes) {
    const relative = normalizeTreePath(node.path);
    if (relative && /\.md$/i.test(relative)) {
      const key = `${node.scope || ""}\u0000${node.project_id || ""}\u0000${relative}`;
      if (!documents.has(key)) documents.set(key, cloneVaultDocNode(node));
    }
    collectVaultDocuments(node.children || [], documents);
  }
}

// A single library tree is rooted at its library directory (global/, or
// projects/<slug>/), which must not appear as a row. The mixed "all libraries"
// view keeps those scope directories visible instead.
export function vaultTreeRoot(documents: KnowledgeVaultTreeNode[], libraryID = ""): string {
  if (documents.length === 0) return "";
  if (libraryID === "global") {
    return documents.some(doc => normalizeTreePath(doc.path).startsWith("global/")) ? "global" : "";
  }
  if (libraryID.startsWith("project:") || libraryID.startsWith("archive:") || libraryID === "orphan") {
    const roots = new Set(documents.map(doc => treeScopeRoot(doc.path)).filter(root => root && root !== "global"));
    return roots.size === 1 ? [...roots][0] : "";
  }
  return "";
}

interface VaultDirectoryBucket {
  name: string;
  path: string;
  index?: KnowledgeVaultTreeNode;
  directories: Map<string, VaultDirectoryBucket>;
  documents: KnowledgeVaultTreeNode[];
}

function newVaultDirectory(path: string): VaultDirectoryBucket {
  return {name: treeBaseName(path), path, directories: new Map(), documents: []};
}

function ensureVaultDirectory(root: VaultDirectoryBucket, directories: Map<string, VaultDirectoryBucket>, target: string, rootPath: string): VaultDirectoryBucket {
  const normalized = normalizeTreePath(target);
  if (!normalized || normalized === rootPath) return root;
  const existing = directories.get(normalized);
  if (existing) return existing;
  const parentPath = treeDirectoryPath(normalized);
  const parent = parentPath ? ensureVaultDirectory(root, directories, parentPath, rootPath) : root;
  const bucket = newVaultDirectory(normalized);
  parent.directories.set(bucket.name, bucket);
  directories.set(normalized, bucket);
  return bucket;
}

function representativeVaultDocument(bucket: VaultDirectoryBucket): KnowledgeVaultTreeNode | undefined {
  if (bucket.index) return bucket.index;
  if (bucket.documents.length > 0) return bucket.documents[0];
  for (const child of bucket.directories.values()) {
    const found = representativeVaultDocument(child);
    if (found) return found;
  }
  return undefined;
}

function compareVaultTreeText(left: string, right: string): number {
  const a = left.toLowerCase();
  const b = right.toLowerCase();
  if (a !== b) return a < b ? -1 : 1;
  if (left === right) return 0;
  return left < right ? -1 : 1;
}

function vaultDirectoryChildren(bucket: VaultDirectoryBucket): KnowledgeVaultTreeNode[] {
  const folders = [...bucket.directories.values()]
    .sort((left, right) => compareVaultTreeText(left.name, right.name))
    .map(vaultFolderNode);
  const documents = bucket.documents
    .slice()
    .sort((left, right) => compareVaultTreeText(left.title, right.title) || compareVaultTreeText(left.id, right.id))
    .map(doc => ({...doc, children: [] as KnowledgeVaultTreeNode[]}));
  return [...folders, ...documents];
}

function vaultFolderNode(bucket: VaultDirectoryBucket): KnowledgeVaultTreeNode {
  const index = bucket.index;
  const representative = representativeVaultDocument(bucket);
  return {
    id: index?.id || `vault-dir:${bucket.path}`,
    title: bucket.name,
    path: bucket.path,
    type: "folder",
    status: index?.status || "",
    scope: index?.scope || representative?.scope || "",
    project_id: index?.project_id || representative?.project_id,
    updated: index?.updated,
    is_index: Boolean(index),
    system: false,
    has_index: Boolean(index),
    children: vaultDirectoryChildren(bucket),
  };
}

// Rebuilds the selectable tree from the API's real vault paths. Each directory
// becomes a folder row named after its path segment; its index.md stays the
// folder's document and is never repeated as a leaf. Empty inbox folders still
// render because the exporter reserves them for every library.
export function buildVaultDirectoryTree(nodes: KnowledgeVaultTreeNode[], libraryID = ""): KnowledgeVaultTreeNode[] {
  const documentsByKey = new Map<string, KnowledgeVaultTreeNode>();
  collectVaultDocuments(nodes || [], documentsByKey);
  const documents = [...documentsByKey.values()];
  if (documents.length === 0) return [];

  const rootPath = vaultTreeRoot(documents, libraryID);
  const root = newVaultDirectory(rootPath);
  const directories = new Map<string, VaultDirectoryBucket>([[rootPath, root]]);
  for (const doc of documents) {
    const relative = normalizeTreePath(doc.path);
    const directoryPath = treeDirectoryPath(relative);
    const bucket = ensureVaultDirectory(root, directories, directoryPath, rootPath);
    if (isVaultIndexPath(relative)) {
      // The library root index is the page behind the tree, not a folder row.
      if (directoryPath === rootPath) continue;
      if (!bucket.index) bucket.index = doc;
      continue;
    }
    bucket.documents.push(doc);
  }
  if (rootPath && (libraryID === "global" || libraryID.startsWith("project:") || libraryID.startsWith("archive:"))) {
    ensureVaultDirectory(root, directories, `${rootPath}/inbox`, rootPath);
  } else if (!rootPath && libraryID === "all") {
    // The mixed view nests every library under its vault directory. Reserve the
    // inbox folder for each of them even when it has no documents yet.
    const scopes = new Set([...directories.keys()].map(path => treeScopeRoot(path)).filter(Boolean));
    for (const scope of scopes) ensureVaultDirectory(root, directories, `${scope}/inbox`, rootPath);
  }
  return vaultDirectoryChildren(root);
}

/* Page render ------------------------------------------------------------ */

export function renderKnowledgeVaultPage(_context: KnowledgeVaultContext): string {
  if (!state.initialized || state.loading === "loading") {
    return `<div class="knowledge-vault" data-kv-tab="${state.tab}">${vaultEmptyState("loading")}</div>`;
  }
  if (state.loading === "error") {
    return `<div class="knowledge-vault" data-kv-tab="${state.tab}">${vaultEmptyState("error", state.error, true)}</div>`;
  }
  return `<div class="knowledge-vault${state.contextOpen ? " context-open" : ""}" data-kv-tab="${state.tab}">
    ${topbarHTML()}
    ${searchPanelHTML()}
    <div class="kv-content" data-kv-mobile-pane="${state.mobilePane}">
      ${state.tab === "knowledge" ? knowledgeBodyHTML() : state.tab === "recent" ? recentBodyHTML() : state.tab === "inbox" ? inboxBodyHTML() : skillsBodyHTML()}
    </div>
    ${settingsDialogHTML()}
  </div>`;
}

function topbarHTML(): string {
  const tabs: Array<[VaultTab, string, string]> = [
    ["knowledge", "knowledge", "kv.tab.knowledge"],
    ["recent", "clock", "kv.tab.recent"],
    ["inbox", "mail", "kv.tab.inbox"],
    ["skills", "bot", "kv.tab.skills"],
  ];
  const inboxCount = state.inbox?.count ?? 0;
  return `<header class="kv-topbar">
    <div class="kv-topbar-main">
      <div class="kv-heading"><strong>${t("kv.title")}</strong><small>${t("kv.subtitle")}</small></div>
      <nav class="kv-tabs" aria-label="${t("kv.sections")}">
        ${tabs.map(([tab, glyph, key]) => `<button type="button" aria-current="${state.tab === tab ? "page" : "false"}" class="kv-tab kv-tab-${tab}" data-kv-tab-button="${tab}">${icon(glyph)}<span>${t(key)}</span>${tab === "inbox" && inboxCount > 0 ? `<span class="kv-count">${inboxCount}</span>` : ""}</button>`).join("")}
      </nav>
    </div>
    <div class="kv-controls">
      <button type="button" class="kv-search-trigger" data-kv-search-open aria-label="${t("kv.searchShortcut")}" title="${t("kv.searchShortcut")}">${icon("search")}<span>${t("kv.search")}</span></button>
      <button type="button" class="kv-settings-trigger icon-button" data-kv-settings aria-label="${t("kv.settings")}" title="${t("kv.settings")}">${icon("settings")}</button>
    </div>
  </header>`;
}

function searchPanelHTML(): string {
  const typeOptions = ["", "navigation", "decision", "practice", "diagnostic", "reference", "architecture"].map(type => `<option value="${type}"${state.searchType === type ? " selected" : ""}>${type ? typeLabel(type) : t("kv.allTypes")}</option>`).join("");
  const statusOptions = ["", "verified", "stale", "pending", "draft", "unverified", "conflict"].map(value => `<option value="${value}"${state.searchStatus === value ? " selected" : ""}>${value ? statusLabel(value) : t("kv.allStatuses")}</option>`).join("");
  const scopeOptions: Array<[SearchScope, string]> = [["all", t("kv.searchScopeAll")], ["project", t("kv.searchScopeProject")], ["directory", t("kv.searchScopeDirectory")]];
  return `<section class="kv-search-panel" ${state.searchOpen ? "" : "hidden"} aria-label="${t("kv.search")}" role="dialog" aria-modal="false">
    <div class="kv-search-head">
      <label class="kv-search-field">${icon("search")}<input id="kv-search" type="search" value="${escapeVaultHTML(state.searchQuery)}" placeholder="${t("kv.searchPlaceholder")}" aria-label="${t("kv.searchPlaceholder")}" autocomplete="off" data-allow-rerender="true"></label>
      <button type="button" class="icon-button" data-kv-search-close aria-label="${t("kv.closeSearch")}" title="${t("kv.closeSearch")}">${icon("close")}</button>
    </div>
    <div class="kv-search-filters">
      <label><span>${t("kv.searchScope")}</span><select id="kv-search-scope">${scopeOptions.map(([value, label]) => `<option value="${value}"${state.searchScope === value ? " selected" : ""}>${escapeVaultHTML(label)}</option>`).join("")}</select></label>
      <label><span>${t("kv.type")}</span><select id="kv-search-type">${typeOptions}</select></label>
      <label><span>${t("kv.status")}</span><select id="kv-search-status">${statusOptions}</select></label>
      <label><span>${t("kv.tag")}</span><input id="kv-search-tag" type="text" value="${escapeVaultHTML(state.searchTag)}" placeholder="${t("kv.tagPlaceholder")}" data-allow-rerender="true"></label>
    </div>
    <div class="kv-search-results" aria-live="polite">
      ${searchResultsHTML()}
    </div>
  </section>`;
}

function searchResultsHTML(): string {
  if (state.searchState === "loading") return vaultEmptyState("loading");
  if (state.searchState === "error") return vaultEmptyState("error", state.searchError, true);
  if (!state.searchQuery.trim() && !state.searchTag.trim()) return `<p class="kv-hint">${t("kv.searchHint")}</p>`;
  if (state.searchState === "ready" && state.searchResults.length === 0) return vaultEmptyState("empty");
  return state.searchResults.map(item => `<div class="kv-search-result">
    <a class="kv-search-result-link" href="${escapeVaultHTML(docHash({id: item.id, scope: item.scope, project_id: item.project_id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">
      <span class="kv-search-result-main"><strong>${highlightVaultText(item.title, state.searchQuery)}</strong><small>${escapeVaultHTML(item.scope || "")}${item.project_id ? ` · ${escapeVaultHTML(item.project_id)}` : ""} · ${highlightVaultText(item.path, state.searchQuery)}</small></span>
      ${item.snippet ? `<p>${highlightVaultText(item.snippet, state.searchQuery)}</p>` : ""}
    </a>
    <span class="kv-search-result-meta">${metaBadges(item)}${item.tags?.length ? item.tags.map(tag => `<button type="button" class="kv-tag" data-kv-tag="${escapeVaultHTML(tag)}">#${escapeVaultHTML(tag)}</button>`).join("") : ""}</span>
  </div>`).join("");
}

function knowledgeBodyHTML(): string {
  return `<div class="kv-workspace" data-kv-mobile-pane="${state.mobilePane}">
    <nav class="kv-tree-panel" aria-label="${t("kv.tree")}">
      <div class="kv-panel-head"><span>${t("kv.tree")}</span><small>${state.treeState === "ready" ? `${state.tree.length} ${t("kv.groups")}` : ""}</small></div>
      ${treeBodyHTML()}
    </nav>
    <article class="kv-doc-panel">
      ${docBodyHTML()}
    </article>
    <aside class="kv-context-panel" aria-label="${t("kv.context")}">
      ${contextBodyHTML()}
    </aside>
  </div>`;
}

function treeBodyHTML(): string {
  if (state.treeState === "loading") return vaultEmptyState("loading");
  if (state.treeState === "error") return vaultEmptyState("error", state.treeError, true);
  if (!state.indexReady) return vaultEmptyState("empty", t("kv.indexMissing"));
  if (state.tree.length === 0) return vaultEmptyState("empty");
  return `<ul class="kv-tree-list">${state.tree.map(node => treeNodeHTML(node)).join("")}</ul>`;
}

function isVaultFolderNode(node: KnowledgeVaultTreeNode): boolean {
  if (node.type === "folder") return true;
  // Old API payloads used the index document as a folder node whose path was
  // the document path; keep those payloads renderable during a rollout.
  return (node.children?.length || 0) > 0 && !/\.md$/i.test(String(node.path ?? ""));
}

function treeNodeHTML(node: KnowledgeVaultTreeNode, depth = 0): string {
  const children = node.children || [];
  // Directories start collapsed like `tree` output; only an explicit toggle (or
  // the selected document's ancestor path) puts an id in `expanded`.
  const expanded = state.expanded.has(node.id);
  const selected = node.id === state.selectedID;
  if (!isVaultFolderNode(node)) {
    return `<li class="kv-tree-node kv-tree-doc" style="--kv-depth:${depth}"><a class="kv-tree-link${selected ? " selected" : ""}" href="${escapeVaultHTML(docHash(node))}" data-kv-doc="${escapeVaultHTML(node.id)}" aria-current="${selected ? "page" : "false"}"><span class="kv-tree-title">${escapeVaultHTML(node.title)}</span>${badge(typeLabel(node.type), "type")}</a></li>`;
  }
  const hasIndex = node.has_index ?? Boolean(node.is_index);
  const rowTitle = `<span class="kv-tree-folder-icon" aria-hidden="true">${icon("folder")}</span><span class="kv-tree-title">${escapeVaultHTML(node.title)}</span>`;
  const row = hasIndex
    ? `<a class="kv-tree-link kv-tree-folder-link${selected ? " selected" : ""}" href="${escapeVaultHTML(docHash(node))}" data-kv-doc="${escapeVaultHTML(node.id)}" aria-current="${selected ? "page" : "false"}">${rowTitle}</a>`
    : `<span class="kv-tree-link kv-tree-folder-link">${rowTitle}</span>`;
  const toggle = children.length > 0
    ? `<button type="button" class="kv-tree-toggle" data-kv-toggle="${escapeVaultHTML(node.id)}" aria-expanded="${expanded}" aria-label="${expanded ? t("kv.collapseNode") : t("kv.expandNode")}">${icon(expanded ? "chevron-up" : "chevron-down")}</button>`
    : `<span class="kv-tree-toggle-space" aria-hidden="true"></span>`;
  return `<li class="kv-tree-node kv-tree-folder" style="--kv-depth:${depth}">
    <div class="kv-tree-row">
      ${toggle}
      ${row}
    </div>
    ${expanded && children.length > 0 ? `<ul class="kv-tree-list">${children.map(child => treeNodeHTML(child, depth + 1)).join("")}</ul>` : ""}
  </li>`;
}

function docBodyHTML(): string {
  const back = `<button type="button" class="kv-back" data-kv-back>${icon("chevron-up")}<span>${t("kv.backToList")}</span></button>`;
  if (state.docState === "loading") return `${back}${vaultEmptyState("loading")}`;
  if (state.docState === "error") return `${back}${vaultEmptyState("error", state.docError, true)}`;
  if (!state.doc) return `${back}${vaultEmptyState("empty", t("kv.selectDoc"))}`;
  const doc = state.doc;
  const crumbs = doc.path.split("/");
  return `${back}
    <div class="kv-doc-toolbar">
      <nav class="kv-breadcrumb" aria-label="${t("kv.breadcrumb")}">${crumbs.map((part, index) => `<span${index === crumbs.length - 1 ? ' aria-current="page"' : ""}>${escapeVaultHTML(part.replace(/\.md$/i, ""))}</span>`).join('<i aria-hidden="true">/</i>')}</nav>
      <div class="kv-doc-actions">
        ${openActions(doc.absolute_path, doc.relative_path, doc.obsidian_file)}
      </div>
    </div>
    <div class="kv-doc-head">
      <h1>${escapeVaultHTML(doc.title)}</h1>
      <div class="kv-doc-subhead">
        <p class="kv-doc-meta"><span>${t("kv.source")}: ${escapeVaultHTML(doc.library_name)}</span>${doc.project_id ? `<span>${t("kv.project")}: ${escapeVaultHTML(doc.project_id)}</span>` : ""}<span>${t("kv.updated")}: ${escapeVaultHTML(safeDateTime(doc.modified_at || doc.updated))}</span><span>${t("kv.status")}: ${escapeVaultHTML(statusLabel(doc.status))}</span></p>
        <button type="button" class="kv-context-toggle" data-kv-context-toggle aria-expanded="${state.contextOpen}" aria-label="${t("kv.context")}" title="${t("kv.context")}">${icon("panel")}<span>${t("kv.context")}</span></button>
      </div>
    </div>
    ${doc.warning ? `<div class="kv-warning" role="status">${escapeVaultHTML(doc.warning)}</div>` : ""}
    <div class="kv-doc-meta-badges">${metaBadges(doc)}${doc.tags?.map(tag => `<button type="button" class="kv-tag" data-kv-tag="${escapeVaultHTML(tag)}">#${escapeVaultHTML(tag)}</button>`).join("") || ""}</div>
    <div class="markdown-body kv-doc-body" data-kv-doc-body>${renderMarkdown(doc.body)}</div>
    <footer class="kv-feedback">
      <span>${t("kv.feedbackPrompt")}</span>
      <button type="button" class="kv-feedback-button${state.feedback === "helped" ? " active" : ""}" data-kv-feedback="helped">${icon("shield")}<span>${t("kv.helpful")}</span></button>
      <button type="button" class="kv-feedback-button${state.feedback === "stale" ? " active" : ""}" data-kv-feedback="stale">${icon("clock")}<span>${t("kv.stale")}</span></button>
      ${state.feedback ? `<small role="status">${t("kv.feedbackReadonly")}</small>` : ""}
    </footer>`;
}

function contextBodyHTML(): string {
  if (!state.doc) return `<p class="kv-hint">${t("kv.selectDoc")}</p>`;
  const doc = state.doc;
  const outline = doc.outline.length
    ? `<ol class="kv-outline">${doc.outline.map((item: KnowledgeVaultOutlineItem) => `<li style="--kv-level:${item.level}"><a href="#${escapeVaultHTML(item.slug)}" data-kv-outline="${escapeVaultHTML(item.slug)}">${escapeVaultHTML(item.text)}</a></li>`).join("")}</ol>`
    : `<p class="kv-hint">${t("kv.noOutline")}</p>`;
  const tags = doc.tags?.length
    ? `<div class="kv-tag-list">${doc.tags.map(tag => `<button type="button" class="kv-tag" data-kv-tag="${escapeVaultHTML(tag)}">#${escapeVaultHTML(tag)}</button>`).join("")}</div>`
    : `<p class="kv-hint">${t("kv.noTags")}</p>`;
  const backlinks = doc.backlinks.length
    ? `<ul class="kv-backlinks">${doc.backlinks.map(item => `<li><a href="${escapeVaultHTML(docHash({id: item.id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">${escapeVaultHTML(item.title)}</a></li>`).join("")}</ul>`
    : `<p class="kv-hint">${t("kv.noBacklinks")}</p>`;
  return `<section class="kv-context-section"><h3>${t("kv.outline")}</h3>${outline}</section>
    <section class="kv-context-section"><h3>${t("kv.tags")}</h3>${tags}</section>
    <section class="kv-context-section"><h3>${t("kv.backlinks")}</h3>${backlinks}</section>
    <button type="button" class="kv-context-close" data-kv-context-toggle>${t("kv.close")}</button>`;
}

function recentBodyHTML(): string {
  if (state.recentState === "loading") return vaultEmptyState("loading");
  if (state.recentState === "error") return vaultEmptyState("error", state.recentError, true);
  if (!state.indexReady) return vaultEmptyState("empty", t("kv.indexMissing"));
  if (state.recent.length === 0) return vaultEmptyState("empty", t("kv.recentEmpty"));
  return `<section class="kv-feed" aria-label="${t("kv.tab.recent")}">
    ${groupRecentByDay(state.recent).map(group => `<section class="kv-feed-day"><h2>${escapeVaultHTML(group.label)}</h2><ul>${group.items.map(item => `<li><a class="kv-feed-item" href="${escapeVaultHTML(docHash({id: item.id, scope: item.scope, project_id: item.project_id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">
      <span class="kv-feed-time">${escapeVaultHTML(formatLocaleDateTime(item.modified_at, {hour: "2-digit", minute: "2-digit"}))}</span>
      <span class="kv-feed-main"><strong>${escapeVaultHTML(item.title)}</strong><small>${escapeVaultHTML(item.library)}${item.project_id ? ` · ${escapeVaultHTML(item.project_id)}` : ""} · ${escapeVaultHTML(item.path)}</small></span>
      ${metaBadges(item)}
    </a></li>`).join("")}</ul></section>`).join("")}
  </section>`;
}

function inboxBodyHTML(): string {
  if (state.inboxState === "loading") return vaultEmptyState("loading");
  if (state.inboxState === "error") return vaultEmptyState("error", state.inboxError, true);
  if (!state.indexReady) return vaultEmptyState("empty", t("kv.indexMissing"));
  if (!state.inbox || state.inbox.items.length === 0) return vaultEmptyState("empty", t("kv.inboxEmpty"));
  return `<section class="kv-inbox" aria-label="${t("kv.tab.inbox")}">
    <header class="kv-inbox-head"><h2>${t("kv.inboxCount", {count: state.inbox.count})}</h2><div class="kv-inbox-counts">${state.inbox.counts.map(item => `<span class="kv-chip kv-chip-static">${escapeVaultHTML(item.library)} <b>${item.count}</b></span>`).join("")}</div></header>
    <ul class="kv-inbox-list">${state.inbox.items.map(item => `<li><a class="kv-inbox-item" href="${escapeVaultHTML(docHash({id: item.id, scope: item.scope, project_id: item.project_id}))}" data-kv-doc="${escapeVaultHTML(item.id)}">
      <span class="kv-feed-main"><strong>${escapeVaultHTML(item.title)}</strong><small>${escapeVaultHTML(item.path)} · ${escapeVaultHTML(item.library)}</small></span>
      ${badge(typeLabel(item.type), "type")}
    </a></li>`).join("")}</ul>
  </section>`;
}

function skillsBodyHTML(): string {
  if (state.skillsState === "loading") return vaultEmptyState("loading");
  if (state.skillsState === "error") return vaultEmptyState("error", state.skillsError, true);
  if (state.skills.length === 0) return vaultEmptyState("empty", t("kv.skillsEmpty"));
  return `<section class="kv-skills" aria-label="${t("kv.tab.skills")}">
    <p class="kv-hint">${t("kv.skillsReadonly")}</p>
    <div class="kv-skill-grid">${state.skills.map(skill => `<article class="kv-skill-card">
      <header><strong>${escapeVaultHTML(skill.name)}</strong>${badge(skill.scope === "global" ? t("kv.scope.global") : t("kv.scope.project"), "type")}</header>
      <p>${escapeVaultHTML(skill.description || t("kv.noDescription"))}</p>
      <small>${escapeVaultHTML(skill.library)} · ${t("kv.skillFiles", {count: skill.file_count})}</small>
      <footer class="kv-skill-actions">${openActions(skill.open_path, `${skill.relative_path}/SKILL.md`, "", {compact: true, context: skill.name, copyKey: "kv.copySkillPath"})}</footer>
    </article>`).join("")}</div>
  </section>`;
}

function settingsDialogHTML(): string {
  const ready = (value: boolean) => value ? t("kv.settingsReady") : t("kv.settingsNotReady");
  const settings = state.settings;
  // The gear panel offers the only rebuild action. It stays disabled until the
  // settings endpoint confirms the vault exists and the runtime switch is on,
  // matching the old advanced-settings rule.
  const canRebuild = Boolean(settings?.enabled && settings?.vault_initialized) && !state.rebuildBusy;
  // The settings endpoint is authoritative for these three fields. Library and
  // document payloads also carry them, but may lag behind a rebuild; prefer the
  // freshly loaded settings so the dialog and the rebuild notice agree.
  const vaultPath = settings?.vault_path || state.vaultPath;
  const indexReady = settings ? Boolean(settings.index_ready) : state.indexReady;
  const rebuiltAt = settings?.rebuilt_at || state.rebuiltAt;
  const rebuildLabel = state.rebuildBusy ? kt("kv.settingsRebuilding") : kt("kv.settingsRebuild");
  const notice = state.rebuildNotice
    ? `<p class="kv-settings-notice ${state.rebuildNotice.kind === "error" ? "error" : "success"}" role="${state.rebuildNotice.kind === "error" ? "alert" : "status"}">${escapeVaultHTML(state.rebuildNotice.message)}</p>`
    : "";
  const settingsError = state.settingsError
    ? `<p class="kv-settings-notice error" role="alert">${escapeVaultHTML(state.settingsError)}</p><button type="button" class="secondary" data-kv-settings-retry>${t("kv.retry")}</button>`
    : "";
  return `<dialog id="kv-settings-dialog" class="kv-settings"${state.rebuildBusy ? ' aria-busy="true"' : ""}>
    <header><h2>${t("kv.settings")}</h2><button type="button" class="icon-button" data-kv-settings-close aria-label="${t("kv.close")}">${icon("close")}</button></header>
    <dl>
      <div><dt>${t("kv.settingsVault")}</dt><dd class="kv-settings-value">${escapeVaultHTML(vaultPath || t("kv.none"))}</dd></div>
      <div><dt>${t("kv.settingsIndex")}</dt><dd class="kv-settings-value">${escapeVaultHTML(indexReady ? t("kv.settingsReady") : t("kv.settingsNotReady"))}</dd></div>
      <div><dt>${t("kv.settingsRebuilt")}</dt><dd class="kv-settings-value">${escapeVaultHTML(rebuiltAt ? safeDateTime(rebuiltAt) : t("kv.none"))}</dd></div>
      <div class="kv-settings-action-row"><dt>${kt("kv.settingsIndexAction")}</dt><dd class="kv-settings-value"><button type="button" id="kv-settings-rebuild" class="secondary" ${canRebuild ? "" : "disabled"}>${rebuildLabel}</button>${notice}</dd></div>
      <div><dt>${t("kv.settingsObsidian")}</dt><dd class="kv-settings-value">${escapeVaultHTML(state.obsidianVault || t("kv.none"))} · ${escapeVaultHTML(ready(state.obsidianReady))}</dd></div>
    </dl>
    ${settingsError}
    ${gitSectionHTML()}
    <p class="kv-hint">${t("kv.settingsHint")}</p>
    <footer><button type="button" data-kv-settings-close>${t("kv.close")}</button></footer>
  </dialog>`;
}

/* Settings dialog ------------------------------------------------------- */

function applyVaultSettings(settings: KnowledgeVaultSettings): void {
  state.settings = settings;
  state.settingsState = "ready";
  state.settingsError = "";
  state.vaultPath = settings.vault_path || state.vaultPath;
  state.indexReady = Boolean(settings.index_ready);
  state.vaultReady = Boolean(settings.vault_ready);
  state.rebuiltAt = settings.rebuilt_at || "";
}

async function loadVaultSettings(): Promise<void> {
  if (state.settingsState === "loading") return;
  state.settingsState = "loading";
  state.settingsError = "";
  rerender();
  try {
    const result = await api.knowledgeVaultSettings();
    applyVaultSettings(result.settings);
  } catch (error) {
    state.settingsState = "error";
    state.settingsError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

function reopenSettingsDialog(): void {
  if (!state.settingsOpen) return;
  const dialog = document.querySelector<HTMLDialogElement>("#kv-settings-dialog");
  if (dialog && !dialog.open) dialog.showModal();
}

function openSettingsDialog(): void {
  state.settingsOpen = true;
  state.rebuildNotice = null;
  rerender();
  if (state.settingsState === "idle" || state.settingsState === "error") void loadVaultSettings();
  void loadVaultGitStatus();
  requestAnimationFrame(reopenSettingsDialog);
}

function closeSettingsDialog(): void {
  state.settingsOpen = false;
  document.querySelector<HTMLDialogElement>("#kv-settings-dialog")?.close();
}

// The files changed on disk; every projection the page cached is one index
// generation old. Reload them in place so the selected document, its deep link
// and the open settings dialog all survive.
async function refreshAfterIndexRebuild(): Promise<void> {
  await Promise.all([
    loadTree(true, true),
    state.selectedID ? loadDoc(state.selectedID, true) : Promise.resolve(),
    loadRecent(true),
    loadInbox(true),
  ]);
}

async function rebuildSettingsIndex(): Promise<void> {
  if (state.rebuildBusy || !state.settings?.enabled || !state.settings.vault_initialized) return;
  state.rebuildBusy = true;
  state.rebuildNotice = null;
  rerender();
  try {
    const result = await api.rebuildKnowledgeVaultIndex();
    applyVaultSettings(result.settings);
    const rebuiltAt = safeDateTime(result.rebuild.rebuilt_at || result.settings.rebuilt_at);
    state.rebuildNotice = {kind: "notice", message: kt("kv.settingsRebuildDone", {count: result.rebuild.entries, time: rebuiltAt})};
    await refreshAfterIndexRebuild();
  } catch (error) {
    state.rebuildNotice = {kind: "error", message: `${kt("kv.settingsRebuildFailed")}：${error instanceof Error ? error.message : String(error)}`};
    // The failed rebuild may have changed which index generation is readable.
    try {
      const current = await api.knowledgeVaultSettings();
      applyVaultSettings(current.settings);
    } catch {
      // Keep the previous status; the error itself is the actionable message.
    }
  } finally {
    state.rebuildBusy = false;
    rerender();
    reopenSettingsDialog();
  }
}

/* Git settings ---------------------------------------------------------- */

const GIT_BUSY_LABEL: Record<Exclude<GitBusy, "">, string> = {
  init: "kv.gitInitializing", remote: "kv.gitSaving", commit: "kv.gitCommitting",
  pull: "kv.gitPulling", push: "kv.gitPushing", sync: "kv.gitSyncing",
};

function gitActionLabel(kind: Exclude<GitBusy, "">, key: string): string {
  return kt(state.gitBusy === kind ? GIT_BUSY_LABEL[kind] : key);
}

function gitNoticeHTML(): string {
  if (!state.gitNotice) return "";
  const role = state.gitNotice.kind === "error" ? "alert" : "status";
  return `<p class="kv-settings-notice ${state.gitNotice.kind === "error" ? "error" : "success"}" role="${role}">${escapeVaultHTML(state.gitNotice.message)}</p>`;
}

function gitSectionHTML(): string {
  const git = state.git;
  let body = "";
  if (state.gitState === "error" && !git) {
    body = `<p class="kv-settings-notice error" role="alert">${kt("kv.gitLoadFailed")}：${escapeVaultHTML(state.gitError)}</p>
      <button type="button" class="secondary" data-kv-git-retry>${t("kv.retry")}</button>`;
  } else if (!git) {
    body = `<p class="kv-hint" role="status">${t("kv.loading")}</p>`;
  } else if (!git.available) {
    body = `<p class="kv-settings-notice error" role="alert">${kt("kv.gitUnavailable")}</p>`;
  } else {
    const busy = Boolean(state.gitBusy);
    const disabled = busy ? " disabled" : "";
    const repo = git.initialized ? kt("kv.gitInitialized") : kt("kv.gitUninitialized");
    const worktree = git.clean ? kt("kv.gitClean") : kt("kv.gitDirty", {count: git.changed});
    let syncState = kt("kv.gitNoTracking");
    if (git.tracking) {
      const parts: string[] = [];
      if (git.ahead > 0) parts.push(kt("kv.gitAhead", {count: git.ahead}));
      if (git.behind > 0) parts.push(kt("kv.gitBehind", {count: git.behind}));
      syncState = parts.length > 0 ? parts.join(" · ") : kt("kv.gitInSync");
    }
    const lastCommit = git.last_commit
      ? `${git.last_commit}${git.last_commit_message ? ` · ${git.last_commit_message}` : ""}${git.last_commit_at ? ` · ${safeDateTime(git.last_commit_at)}` : ""}`
      : kt("kv.gitNone");
    const lastSync = git.last_sync_at ? safeDateTime(git.last_sync_at) : kt("kv.gitNone");
    const remoteForm = git.initialized ? `<div class="kv-git-form">
      <label><span>${kt("kv.gitRemoteInput")}</span><input id="kv-git-remote-input" type="text" data-allow-rerender="true" placeholder="${kt("kv.gitRemotePlaceholder")}" value="${escapeVaultHTML(state.gitRemoteDraft)}" autocomplete="off" spellcheck="false"${disabled}></label>
      <label><span>${kt("kv.gitBranchInput")}</span><input id="kv-git-branch-input" type="text" data-allow-rerender="true" value="${escapeVaultHTML(state.gitBranchDraft || git.branch)}" autocomplete="off" spellcheck="false"${disabled}></label>
      <button type="button" id="kv-git-remote-save" class="secondary"${disabled}>${gitActionLabel("remote", "kv.gitSaveRemote")}</button>
    </div>` : "";
    const actions = git.initialized ? `
      <div class="kv-git-commit-row">
        <label><span>${kt("kv.gitCommitMessage")}</span><input id="kv-git-commit-message" type="text" data-allow-rerender="true" maxlength="200" placeholder="${kt("kv.gitCommitPlaceholder")}" value="${escapeVaultHTML(state.gitCommitDraft)}" autocomplete="off"${disabled}></label>
        <button type="button" id="kv-git-commit" class="secondary"${git.clean ? " disabled" : disabled}>${gitActionLabel("commit", "kv.gitCommit")}</button>
      </div>
      <div class="kv-git-actions">
        <button type="button" id="kv-git-pull"${(!git.remote_set || !git.clean) ? " disabled" : disabled}>${gitActionLabel("pull", "kv.gitPull")}</button>
        <button type="button" id="kv-git-push"${(!git.remote_set || !git.has_commits || (git.tracking && git.ahead === 0)) ? " disabled" : disabled}>${gitActionLabel("push", "kv.gitPush")}</button>
        <button type="button" id="kv-git-sync" class="primary"${(!git.remote_set || !git.clean) ? " disabled" : disabled}>${gitActionLabel("sync", "kv.gitSync")}</button>
      </div>` : `<div class="kv-git-actions"><button type="button" id="kv-git-init" class="primary"${disabled}>${gitActionLabel("init", "kv.gitInit")}</button></div>`;
    body = `<dl>
      <div><dt>${kt("kv.gitRepo")}</dt><dd class="kv-settings-value">${repo}</dd></div>
      <div><dt>${kt("kv.gitBranch")}</dt><dd class="kv-settings-value">${escapeVaultHTML(git.branch)}</dd></div>
      <div><dt>${kt("kv.gitRemote")}</dt><dd class="kv-settings-value">${escapeVaultHTML(git.remote_set ? git.remote || "" : kt("kv.gitRemoteUnset"))}</dd></div>
      <div><dt>${kt("kv.gitWorktree")}</dt><dd class="kv-settings-value">${escapeVaultHTML(worktree)}</dd></div>
      <div><dt>${kt("kv.gitSyncState")}</dt><dd class="kv-settings-value">${escapeVaultHTML(syncState)}</dd></div>
      <div><dt>${kt("kv.gitLastCommit")}</dt><dd class="kv-settings-value">${escapeVaultHTML(lastCommit)}</dd></div>
      <div><dt>${kt("kv.gitLastSync")}</dt><dd class="kv-settings-value">${escapeVaultHTML(lastSync)}</dd></div>
    </dl>
    ${remoteForm}
    ${actions}
    ${gitNoticeHTML()}
    <p class="kv-hint">${kt("kv.gitHint")}</p>`;
  }
  return `<section class="kv-settings-section kv-git" aria-labelledby="kv-git-title">
    <h3 id="kv-git-title">${kt("kv.gitTitle")}</h3>
    ${body}
  </section>`;
}

function applyGitStatus(git: KnowledgeVaultGitStatus): void {
  state.git = git;
  state.gitState = "ready";
  state.gitError = "";
}

function knowledgeVaultGitRequest(action: "status" | "init" | "remote" | "commit" | "pull" | "push" | "sync", payload?: {remote?: string; branch?: string; message?: string}): Promise<KnowledgeVaultGitResponse> {
  return api.request(`/api/v1/knowledge-vault/git/${action}`, action === "status" ? {} : {method: action === "remote" ? "PUT" : "POST", body: payload ? JSON.stringify(payload) : undefined});
}

async function loadVaultGitStatus(): Promise<void> {
  if (state.gitState === "loading") return;
  state.gitState = "loading";
  state.gitError = "";
  rerender();
  try {
    const result = await knowledgeVaultGitRequest("status");
    applyGitStatus(result.git);
  } catch (error) {
    state.gitState = "error";
    state.gitError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function runGitOperation(kind: Exclude<GitBusy, "">, action: () => Promise<{git: KnowledgeVaultGitStatus; message?: string}>): Promise<void> {
  if (state.gitBusy) return;
  state.gitBusy = kind;
  state.gitNotice = null;
  rerender();
  try {
    const result = await action();
    applyGitStatus(result.git);
    state.gitRemoteDraft = "";
    state.gitBranchDraft = "";
    state.gitCommitDraft = "";
    state.gitNotice = {kind: "notice", message: result.message || kt("kv.gitInSync")};
  } catch (error) {
    state.gitNotice = {kind: "error", message: error instanceof Error ? error.message : String(error)};
  } finally {
    state.gitBusy = "";
    rerender();
  }
}

async function initializeVaultGit(): Promise<void> {
  await runGitOperation("init", () => knowledgeVaultGitRequest("init"));
}

async function saveVaultGitRemote(): Promise<void> {
  const remote = document.querySelector<HTMLInputElement>("#kv-git-remote-input")?.value.trim() || "";
  const branch = document.querySelector<HTMLInputElement>("#kv-git-branch-input")?.value.trim() || "";
  await runGitOperation("remote", () => knowledgeVaultGitRequest("remote", {remote, branch}));
}

async function commitVaultGit(): Promise<void> {
  const message = document.querySelector<HTMLInputElement>("#kv-git-commit-message")?.value.trim() || "";
  await runGitOperation("commit", () => knowledgeVaultGitRequest("commit", {message}));
}

async function pullVaultGit(): Promise<void> {
  await runGitOperation("pull", () => knowledgeVaultGitRequest("pull"));
}

async function pushVaultGit(): Promise<void> {
  await runGitOperation("push", () => knowledgeVaultGitRequest("push"));
}

async function syncVaultGit(): Promise<void> {
  await runGitOperation("sync", () => knowledgeVaultGitRequest("sync"));
}

/* Tree expansion -------------------------------------------------------- */

// Returns the ids of the folders between the tree root and `id` (excluding the
// node itself), or null when the node is not in this tree. The trail drives the
// automatic expansion that keeps a deep-linked document visible while every
// sibling folder stays collapsed.
export function vaultTreeAncestors(nodes: KnowledgeVaultTreeNode[], id: string, trail: string[] = []): string[] | null {
  for (const node of nodes) {
    if (node.id === id) return trail;
    const children = node.children || [];
    if (children.length === 0) continue;
    const found = vaultTreeAncestors(children, id, [...trail, node.id]);
    if (found) return found;
  }
  return null;
}

function expandTreePathTo(id: string): void {
  if (!id) return;
  const ancestors = vaultTreeAncestors(state.tree, id);
  if (!ancestors) return;
  for (const ancestor of ancestors) state.expanded.add(ancestor);
}

/* Data loading ----------------------------------------------------------- */

function contextFromConfig(): KnowledgeVaultContext {
  return {render: () => {}, setMessage: () => {}};
}
let activeContext: KnowledgeVaultContext = contextFromConfig();

function rerender(): void {
  activeContext.render();
}

function applyLibraries(payload: KnowledgeVaultLibraries): void {
  state.libraries = payload.libraries || [];
  state.projects = payload.projects || [];
  state.vaultReady = Boolean(payload.vault_ready);
  state.indexReady = Boolean(payload.index_ready);
  state.vaultName = payload.vault_name || "";
  state.vaultPath = payload.vault_path || "";
  state.obsidianVault = payload.obsidian_vault || "";
  state.obsidianReady = Boolean(payload.obsidian_ready);
  state.rebuiltAt = payload.rebuilt_at || "";
  state.warning = payload.warning || "";
  if (!state.libraries.some(item => item.id === state.libraryID)) {
    state.libraryID = state.libraries[0]?.id || "global";
  }
}

async function loadLibraries(): Promise<boolean> {
  state.loading = "loading";
  rerender();
  try {
    const payload = await api.knowledgeVaultLibraries();
    applyLibraries(payload);
    state.initialized = true;
    state.loading = "ready";
    state.error = "";
    rerender();
    return true;
  } catch (error) {
    state.initialized = true;
    state.loading = "error";
    state.error = error instanceof Error ? error.message : String(error);
    rerender();
    return false;
  }
}

function loadOnce(key: string, action: () => Promise<void>): void {
  if (pendingLoads.has(key)) return;
  pendingLoads.add(key);
  void action().catch(error => {
    activeContext.setMessage("error", error instanceof Error ? error.message : String(error));
  }).finally(() => pendingLoads.delete(key));
}

async function loadTree(force = false, preserveExpansion = false): Promise<void> {
  const key = VAULT_TREE_LIBRARY;
  if (!force && state.treeKey === key && state.treeState !== "error") return;
  const ticket = ++treeRequest;
  state.treeKey = key;
  state.treeState = "loading";
  state.treeError = "";
  // A freshly loaded tree starts collapsed; only the selected document's
  // ancestor path is reopened after the nodes arrive. A rebuild refresh keeps
  // the folders the user already opened.
  if (!preserveExpansion) state.expanded = new Set();
  rerender();
  try {
    const payload: KnowledgeVaultTree = await api.knowledgeVaultTree(key);
    if (ticket !== treeRequest || state.treeKey !== key) return;
    state.tree = buildVaultDirectoryTree(payload.nodes || [], key);
    state.indexReady = payload.index_ready;
    state.treeState = "ready";
    expandTreePathTo(state.selectedID);
  } catch (error) {
    if (ticket !== treeRequest || state.treeKey !== key) return;
    state.treeState = "error";
    state.treeError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadDoc(id: string, force = false): Promise<void> {
  if (!id) return;
  if (!force && state.selectedID === id && state.docState === "ready") return;
  state.selectedID = id;
  state.docState = "loading";
  state.docError = "";
  state.feedback = "";
  state.doc = null;
  // Clicking a document, a search result, or following a deep link reopens only
  // that document's ancestor folders so its row is visible in the tree.
  expandTreePathTo(id);
  rerender();
  try {
    const payload = await api.knowledgeVaultDoc(id);
    if (state.selectedID !== id) return;
    state.doc = payload.doc;
    state.docState = "ready";
  } catch (error) {
    if (state.selectedID !== id) return;
    state.docState = "error";
    state.docError = error instanceof Error ? error.message : String(error);
  }
  rerender();
  decorateDocBody();
}

async function loadRecent(force = false): Promise<void> {
  // Recent is always the all-libraries feed; every row carries its own
  // library/project/path context instead of relying on a topbar scope.
  const key = "";
  if (!force && state.recentKey === key && (state.recentState === "ready" || state.recentState === "loading")) return;
  const ticket = ++recentRequest;
  state.recentKey = key;
  state.recentState = "loading";
  rerender();
  try {
    const payload: KnowledgeVaultRecent = await api.knowledgeVaultRecent(key);
    if (ticket !== recentRequest || state.recentKey !== key) return;
    state.recent = payload.items || [];
    state.indexReady = payload.index_ready;
    state.recentState = "ready";
  } catch (error) {
    if (ticket !== recentRequest || state.recentKey !== key) return;
    state.recentState = "error";
    state.recentError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadInbox(force = false): Promise<void> {
  // Like recent, the inbox defaults to every library.
  const key = "";
  if (!force && state.inboxKey === key && (state.inboxState === "ready" || state.inboxState === "loading")) return;
  const ticket = ++inboxRequest;
  state.inboxKey = key;
  state.inboxState = "loading";
  rerender();
  try {
    const payload = await api.knowledgeVaultInbox(key);
    if (ticket !== inboxRequest || state.inboxKey !== key) return;
    state.inbox = payload;
    state.indexReady = payload.index_ready;
    state.inboxState = "ready";
  } catch (error) {
    if (ticket !== inboxRequest || state.inboxKey !== key) return;
    state.inboxState = "error";
    state.inboxError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

async function loadSkills(force = false): Promise<void> {
  if (!force && state.skillsState === "ready") return;
  state.skillsState = "loading";
  rerender();
  try {
    const payload: KnowledgeVaultSkills = await api.knowledgeVaultSkills();
    state.skills = payload.skills || [];
    state.skillsState = "ready";
  } catch (error) {
    state.skillsState = "error";
    state.skillsError = error instanceof Error ? error.message : String(error);
  }
  rerender();
}

function loadTab(tab: VaultTab): void {
  // Tree/recent/inbox dedupe on the parameter key inside each loader. A shared
  // loader-name key would drop the request for a newly selected library while
  // the previous one is still in flight.
  if (tab === "knowledge") void loadTree();
  else if (tab === "recent") void loadRecent();
  else if (tab === "inbox") void loadInbox();
  else if (tab === "skills") loadOnce("skills", () => loadSkills());
}

function currentVaultDirectory(): string {
  const relative = String(state.doc?.path || "");
  const separator = relative.lastIndexOf("/");
  return separator < 0 ? "" : relative.slice(0, separator);
}

function currentSearchContext(): {library: string; project: string} {
  if (state.doc) return {library: state.doc.library_id || "", project: state.doc.project_id || ""};
  return {library: state.libraryID === "global" ? "" : state.libraryID, project: ""};
}

function performSearch(): void {
  const query = state.searchQuery.trim();
  const tag = state.searchTag.trim();
  if (!query && !tag) {
    state.searchResults = [];
    state.searchState = "idle";
    rerender();
    return;
  }
  const ticket = ++state.pendingSearch;
  state.searchState = "loading";
  rerender();
  const scoped = state.searchScope !== "all";
  const context = scoped ? currentSearchContext() : {library: "", project: ""};
  const directory = state.searchScope === "directory" ? currentVaultDirectory() : "";
  void api.knowledgeVaultSearch({
    q: query, tag,
    library: context.library, project: context.project,
    type: state.searchType, status: state.searchStatus, limit: 40,
  }).then((payload: KnowledgeVaultSearch) => {
    if (ticket !== state.pendingSearch) return;
    let results = payload.results || [];
    // The read API has no path-prefix filter, so "current directory" narrows
    // the same scoped result set in the client.
    if (directory) results = results.filter(item => item.path === directory || item.path.startsWith(`${directory}/`));
    state.searchResults = results;
    state.searchState = "ready";
    rerender();
  }).catch((error: unknown) => {
    if (ticket !== state.pendingSearch) return;
    state.searchState = "error";
    state.searchError = error instanceof Error ? error.message : String(error);
    rerender();
  });
}

function scheduleSearch(): void {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    searchTimer = null;
    performSearch();
  }, 160);
}

function openSearch(): void {
  state.searchOpen = true;
  rerender();
  requestAnimationFrame(() => document.querySelector<HTMLInputElement>("#kv-search")?.focus());
}

function closeSearch(): void {
  state.searchOpen = false;
  rerender();
}

function syncHash(doc: KnowledgeVaultDoc | null): void {
  if (!doc) return;
  // A doc request can finish after the user switched to another view. Writing
  // the deep link then would route the next refresh back to knowledge.
  if (!document.querySelector(".knowledge-vault")) return;
  try {
    window.history.replaceState(null, "", docHash(doc));
  } catch {
    // History can be unavailable in embedded webviews; deep linking is then
    // only best-effort and the page state still works.
  }
}

/* Interaction ------------------------------------------------------------ */

function enterDoc(id: string, libraryHint = ""): void {
  if (libraryHint && state.libraries.some(item => item.id === libraryHint)) state.libraryID = libraryHint;
  if (state.mobilePane === "list") state.listScroll = window.scrollY;
  state.mobilePane = "doc";
  state.searchOpen = false;
  void loadDoc(id).then(() => {
    const doc = state.doc;
    if (!doc) return;
    if (state.libraries.some(item => item.id === doc.library_id)) state.libraryID = doc.library_id;
    syncHash(doc);
    if (state.mobilePane === "doc") window.scrollTo(0, 0);
    void loadTree(false);
    decorateDocBody();
  });
}

function backToList(): void {
  state.docScroll = window.scrollY;
  state.mobilePane = "list";
  rerender();
  requestAnimationFrame(() => window.scrollTo(0, state.listScroll));
}

function setTab(tab: VaultTab): void {
  state.tab = tab;
  state.mobilePane = "list";
  rerender();
  loadTab(tab);
}

function normalizedHeadingText(value: string | null | undefined): string {
  return String(value ?? "").replace(/\s+/g, " ").trim();
}

function renderedHeadingText(markdown: string): string {
  const holder = document.createElement("div");
  holder.innerHTML = renderMarkdown(`# ${markdown}`);
  return normalizedHeadingText(holder.querySelector("h1")?.textContent || markdown);
}

function decorateDocBody(): void {
  const body = document.querySelector<HTMLElement>("[data-kv-doc-body]");
  if (!body || !state.doc) return;
  const headings = [...body.querySelectorAll<HTMLElement>("h1,h2,h3,h4,h5,h6")];
  const outline = state.doc.outline;
  // Match by rendered text and level rather than array position. This keeps
  // outline links correct when the index is older than the file or when a
  // heading was filtered out (for example, a `#` line inside a code fence).
  let cursor = 0;
  for (const item of outline) {
    const wanted = renderedHeadingText(item.text);
    for (let index = cursor; index < headings.length; index += 1) {
      const heading = headings[index];
      if (Number(heading.tagName.slice(1)) !== item.level) continue;
      if (normalizedHeadingText(heading.textContent) !== wanted) continue;
      if (!heading.id) heading.id = item.slug;
      cursor = index + 1;
      break;
    }
  }
}

let copyNoticeVersion = 0;

function showCopyNotice(kind: "notice" | "error", message: string): void {
  // Update only the banner live region. A full render would replace the
  // focused copy button and drop keyboard focus.
  const stack = document.querySelector<HTMLElement>("main.workspace > .banner-stack");
  if (!stack) return;
  const version = ++copyNoticeVersion;
  stack.innerHTML = `<div class="banner ${kind === "error" ? "error" : "success"}">${escapeVaultHTML(message)}</div>`;
  window.setTimeout(() => {
    if (version === copyNoticeVersion && stack.isConnected) stack.innerHTML = "";
  }, 5000);
}

function copyPath(value: string): void {
  const done = () => showCopyNotice("notice", t("kv.copied"));
  if (navigator.clipboard?.writeText) {
    void navigator.clipboard.writeText(value).then(done).catch(() => fallbackCopy(value, done));
    return;
  }
  fallbackCopy(value, done);
}

function fallbackCopy(value: string, done: () => void): void {
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  try {
    document.execCommand("copy");
    done();
  } catch {
    showCopyNotice("error", value);
  } finally {
    textarea.remove();
    previous?.focus({preventScroll: true});
  }
}

function bindKeyboard(): void {
  if (keyboardBound) return;
  keyboardBound = true;
  document.addEventListener("keydown", event => {
    if (!document.querySelector(".knowledge-vault")) return;
    const modifier = event.ctrlKey || event.metaKey;
    if (modifier && event.key.toLowerCase() === "k") {
      event.preventDefault();
      event.stopPropagation();
      openSearch();
      return;
    }
    if (event.key === "Escape") {
      if (state.searchOpen) {
        event.preventDefault();
        event.stopPropagation();
        closeSearch();
      } else if (state.contextOpen) {
        state.contextOpen = false;
        rerender();
      }
    }
  }, true);
}

function bindHash(): void {
  if (hashBound) return;
  hashBound = true;
  window.addEventListener("hashchange", () => {
    if (!document.querySelector(".knowledge-vault")) return;
    const parsed = parseDocHash(window.location.hash);
    if (!parsed?.id) return;
    if (parsed.library) state.libraryID = parsed.library;
    const unchanged = parsed.id === state.selectedID && state.docState === "ready";
    state.tab = "knowledge";
    state.mobilePane = "doc";
    if (unchanged) rerender();
    else void loadDoc(parsed.id);
  });
}

function handleDocLink(event: Event): boolean {
  const target = (event.target as HTMLElement | null)?.closest<HTMLElement>("[data-kv-doc]");
  if (!target) return false;
  if (event instanceof MouseEvent && (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0)) return true;
  event.preventDefault();
  const library = target.closest<HTMLElement>("[data-kv-inbox-library]")?.dataset.kvInboxLibrary || "";
  enterDoc(target.dataset.kvDoc || "", library);
  return true;
}

function focusSelector(element: Element | null): string {
  if (!(element instanceof HTMLElement)) return "";
  if (element.id) return `#${typeof CSS !== "undefined" && CSS.escape ? CSS.escape(element.id) : element.id}`;
  for (const name of ["kvToggle", "kvFeedback", "kvTabButton", "kvDoc", "kvCopy", "kvTag"]) {
    const value = (element.dataset as Record<string, string | undefined>)[name];
    if (value) return `[data-kv-${name.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`) }="${typeof CSS !== "undefined" && CSS.escape ? CSS.escape(value) : value}"]`;
  }
  return "";
}

function renderPreservingFocus(renderPage: () => void): void {
  const dialogWasOpen = state.settingsOpen && Boolean(document.querySelector<HTMLDialogElement>("#kv-settings-dialog")?.open);
  const active = document.activeElement;
  const selector = focusSelector(active);
  const start = active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement ? active.selectionStart : null;
  const end = active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement ? active.selectionEnd : null;
  renderPage();
  // The whole page markup, including the <dialog>, is replaced on every render.
  // Reopen it before restoring focus so in-place status updates do not close
  // the panel under the Owner.
  if (dialogWasOpen) reopenSettingsDialog();
  if (!selector) return;
  const next = document.querySelector<HTMLElement>(selector);
  if (!next) return;
  next.focus({preventScroll: true});
  if ((next instanceof HTMLInputElement || next instanceof HTMLTextAreaElement) && start !== null) {
    try { next.setSelectionRange(start, end ?? start); } catch { /* some input types reject selection ranges */ }
  }
}

export function bindKnowledgeVaultPage(context: KnowledgeVaultContext): void {
  installGitStyles();
  activeContext = {render: () => renderPreservingFocus(context.render), setMessage: context.setMessage};
  bindKeyboard();
  bindHash();
  if (!binding) {
    binding = true;
    void loadLibraries().then(ok => {
      if (!ok) return;
      const parsed = parseDocHash(window.location.hash);
      if (parsed?.library && state.libraries.some(item => item.id === parsed.library)) state.libraryID = parsed.library;
      loadTab(state.tab);
      if (parsed?.id) {
        state.tab = "knowledge";
        state.mobilePane = "doc";
        void loadDoc(parsed.id).then(() => { syncHash(state.doc); decorateDocBody(); });
      }
    });
  } else if (state.initialized && state.loading === "ready") {
    loadTab(state.tab);
  }

  const root = document.querySelector<HTMLElement>(".knowledge-vault");
  if (!root) return;

  root.querySelectorAll<HTMLElement>("[data-kv-tab-button]").forEach(button => {
    button.addEventListener("click", () => setTab(button.dataset.kvTabButton as VaultTab));
  });

  root.querySelector<HTMLElement>("[data-kv-search-open]")?.addEventListener("click", openSearch);
  root.querySelector<HTMLElement>("[data-kv-search-close]")?.addEventListener("click", closeSearch);
  root.querySelector<HTMLElement>("[data-kv-settings]")?.addEventListener("click", openSettingsDialog);
  root.querySelectorAll<HTMLElement>("[data-kv-settings-close]").forEach(button => {
    button.addEventListener("click", closeSettingsDialog);
  });
  root.querySelector<HTMLButtonElement>("#kv-settings-rebuild")?.addEventListener("click", () => { void rebuildSettingsIndex(); });
  root.querySelector<HTMLButtonElement>("[data-kv-settings-retry]")?.addEventListener("click", () => { void loadVaultSettings(); });
  root.querySelector<HTMLButtonElement>("[data-kv-git-retry]")?.addEventListener("click", () => { void loadVaultGitStatus(); });
  root.querySelector<HTMLButtonElement>("#kv-git-init")?.addEventListener("click", () => { void initializeVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-remote-save")?.addEventListener("click", () => { void saveVaultGitRemote(); });
  root.querySelector<HTMLButtonElement>("#kv-git-commit")?.addEventListener("click", () => { void commitVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-pull")?.addEventListener("click", () => { void pullVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-push")?.addEventListener("click", () => { void pushVaultGit(); });
  root.querySelector<HTMLButtonElement>("#kv-git-sync")?.addEventListener("click", () => { void syncVaultGit(); });
  root.querySelector<HTMLInputElement>("#kv-git-remote-input")?.addEventListener("input", event => {
    state.gitRemoteDraft = (event.target as HTMLInputElement).value;
  });
  root.querySelector<HTMLInputElement>("#kv-git-branch-input")?.addEventListener("input", event => {
    state.gitBranchDraft = (event.target as HTMLInputElement).value;
  });
  root.querySelector<HTMLInputElement>("#kv-git-commit-message")?.addEventListener("input", event => {
    state.gitCommitDraft = (event.target as HTMLInputElement).value;
  });

  const searchInput = root.querySelector<HTMLInputElement>("#kv-search");
  searchInput?.addEventListener("input", () => {
    state.searchQuery = searchInput.value;
    scheduleSearch();
  });
  const searchTag = root.querySelector<HTMLInputElement>("#kv-search-tag");
  searchTag?.addEventListener("input", () => {
    state.searchTag = searchTag.value;
    scheduleSearch();
  });
  const searchScopeSelect = root.querySelector<HTMLSelectElement>("#kv-search-scope");
  searchScopeSelect?.addEventListener("change", () => {
    state.searchScope = (searchScopeSelect.value as SearchScope) || "all";
    performSearch();
  });
  for (const [selector, key] of [["#kv-search-type", "searchType"], ["#kv-search-status", "searchStatus"]] as const) {
    const element = root.querySelector<HTMLSelectElement>(selector);
    element?.addEventListener("change", () => {
      (state as unknown as Record<string, string>)[key] = element.value;
      performSearch();
    });
  }

  root.querySelectorAll<HTMLElement>("[data-kv-toggle]").forEach(button => {
    button.addEventListener("click", () => {
      const id = button.dataset.kvToggle || "";
      if (state.expanded.has(id)) state.expanded.delete(id);
      else state.expanded.add(id);
      rerender();
    });
  });

  root.addEventListener("click", event => {
    if (handleDocLink(event)) return;
    const target = event.target as HTMLElement | null;
    const copy = target?.closest<HTMLElement>("[data-kv-copy]");
    if (copy) {
      copyPath(copy.dataset.kvCopy || "");
      return;
    }
    const tag = target?.closest<HTMLElement>("[data-kv-tag]");
    if (tag) {
      state.searchTag = tag.dataset.kvTag || "";
      state.searchOpen = true;
      state.tab = "knowledge";
      rerender();
      performSearch();
      requestAnimationFrame(() => document.querySelector<HTMLInputElement>("#kv-search")?.focus());
      return;
    }
    const feedback = target?.closest<HTMLElement>("[data-kv-feedback]");
    if (feedback) {
      state.feedback = feedback.dataset.kvFeedback === "stale" ? "stale" : "helped";
      rerender();
      return;
    }
    if (target?.closest("[data-kv-back]")) {
      backToList();
      return;
    }
    if (target?.closest("[data-kv-context-toggle]")) {
      state.contextOpen = !state.contextOpen;
      rerender();
      return;
    }
    if (target?.closest("[data-kv-retry]")) {
      if (state.loading === "error") void loadLibraries();
      else if (state.docState === "error") void loadDoc(state.selectedID, true);
      else if (state.searchState === "error") performSearch();
      else if (state.tab === "recent") void loadRecent(true);
      else if (state.tab === "inbox") void loadInbox(true);
      else if (state.tab === "skills") void loadSkills(true);
      else void loadTree(true);
      return;
    }
    const outline = target?.closest<HTMLElement>("[data-kv-outline]");
    if (outline) {
      event.preventDefault();
      const heading = document.getElementById(outline.dataset.kvOutline || "");
      heading?.scrollIntoView({behavior: "smooth", block: "start"});
      heading?.setAttribute("tabindex", "-1");
      heading?.focus({preventScroll: true});
      return;
    }
  });

  if (state.searchOpen) requestAnimationFrame(() => root.querySelector<HTMLInputElement>("#kv-search")?.focus());
  if (state.settingsOpen) requestAnimationFrame(reopenSettingsDialog);
  decorateDocBody();
}

/* Test-only state access ------------------------------------------------ */

export function __getKnowledgeVaultStateForTest(): VaultState {
  return state;
}